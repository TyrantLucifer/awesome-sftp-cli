//go:build darwin || linux

package integration

import (
	"bytes"
	"context"
	"fmt"
	"net"
	"os"
	"os/exec"
	"os/user"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/TyrantLucifer/awesome-mac-sftp/internal/domain"
	"github.com/TyrantLucifer/awesome-mac-sftp/internal/provider"
	sftpprovider "github.com/TyrantLucifer/awesome-mac-sftp/internal/provider/sftp"
	"github.com/TyrantLucifer/awesome-mac-sftp/internal/transport/openssh"
)

func TestRealOpenSSHSFTPHostAliasAndNonDefaultPort(t *testing.T) {
	if os.Getenv("AMSFTP_REAL_SSHD") != "1" {
		t.Skip("set AMSFTP_REAL_SSHD=1 in an isolated account")
	}
	current, err := user.Current()
	if err != nil {
		t.Fatal(err)
	}
	keyRoot := t.TempDir()
	clientKey := filepath.Join(keyRoot, "client_key")
	run(t, "/usr/bin/ssh-keygen", "-q", "-t", "ed25519", "-N", "", "-f", clientKey)
	// #nosec G304 -- path is generated inside this test's private TempDir.
	publicKey, err := os.ReadFile(clientKey + ".pub")
	if err != nil {
		t.Fatal(err)
	}
	firstServer := startTestSSHD(t, current.Username, publicKey, "first")
	secondServer := startTestSSHD(t, current.Username, publicKey, "second")

	sshDir := filepath.Join(current.HomeDir, ".ssh")
	if err := os.MkdirAll(sshDir, 0o700); err != nil {
		t.Fatal(err)
	}
	configPath := filepath.Join(sshDir, "config")
	// #nosec G304 -- guarded integration test intentionally reads the isolated runner account's SSH config.
	original, readErr := os.ReadFile(configPath)
	hadConfig := readErr == nil
	if readErr != nil && !os.IsNotExist(readErr) {
		t.Fatal(readErr)
	}
	firstAlias := "amsftp-stage1-first"
	secondAlias := "amsftp-stage1-second"
	clientConfig := sshHostConfig(firstAlias, firstServer, current.Username, clientKey) + sshHostConfig(secondAlias, secondServer, current.Username, clientKey)
	// #nosec G703 -- guarded integration test writes only the current isolated runner account's canonical SSH config.
	if err := os.WriteFile(configPath, append(original, []byte(clientConfig)...), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if hadConfig {
			// #nosec G703 -- restores the exact isolated runner config path captured above.
			_ = os.WriteFile(configPath, original, 0o600)
		} else {
			_ = os.Remove(configPath)
		}
	})

	poisonedPath := t.TempDir()
	poisonMarker := filepath.Join(poisonedPath, "fake-ssh-ran")
	fakeSSH := fmt.Sprintf("#!/bin/sh\n/usr/bin/touch %s\nexit 99\n", poisonMarker)
	// #nosec G306 -- executable fixture proves PATH lookup is never used.
	if err := os.WriteFile(filepath.Join(poisonedPath, "ssh"), []byte(fakeSSH), 0o700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", poisonedPath)

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	first := connectSFTP(t, ctx, firstAlias, firstServer.root, "ep_aaaaaaaaaaaaaaaaaaaaaaaaaa")
	defer first.Close()
	second := connectSFTP(t, ctx, secondAlias, secondServer.root, "ep_bbbbbbbbbbbbbbbbbbbbbbbbbb")
	defer second.Close()
	assertContainsEntry(t, ctx, first, "endpoint-first.txt")
	assertContainsEntry(t, ctx, second, "endpoint-second.txt")
	if _, err := os.Stat(poisonMarker); !os.IsNotExist(err) {
		t.Fatalf("poisoned PATH ssh executed: %v", err)
	}

	firstServer.stop()
	root, err := first.Normalize(ctx, domain.NormalizeRequest{EndpointID: first.Descriptor().ID, Input: "/"})
	if err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(5 * time.Second)
	for {
		_, statErr := first.Stat(ctx, provider.StatRequest{Location: root})
		if domain.IsCode(statErr, domain.CodeTransportInterrupted) {
			break
		}
		if statErr != nil || time.Now().After(deadline) {
			t.Fatalf("disconnected endpoint error = %v, want transport_interrupted", statErr)
		}
		time.Sleep(25 * time.Millisecond)
	}
	assertContainsEntry(t, ctx, second, "endpoint-second.txt")
}

type testSSHD struct {
	root     string
	port     int
	command  *exec.Cmd
	logs     bytes.Buffer
	stopOnce sync.Once
}

func startTestSSHD(t *testing.T, username string, publicKey []byte, label string) *testSSHD {
	t.Helper()
	server := &testSSHD{root: t.TempDir()}
	hostKey := filepath.Join(server.root, "host_key")
	run(t, "/usr/bin/ssh-keygen", "-q", "-t", "ed25519", "-N", "", "-f", hostKey)
	authorized := filepath.Join(server.root, "authorized_keys")
	// #nosec G703 -- destination is fixed inside this test's private TempDir.
	if err := os.WriteFile(authorized, publicKey, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(server.root, "endpoint-"+label+".txt"), []byte(label), 0o600); err != nil {
		t.Fatal(err)
	}
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	server.port = listener.Addr().(*net.TCPAddr).Port
	_ = listener.Close()
	serverConfig := filepath.Join(server.root, "sshd_config")
	pidFile := filepath.Join(server.root, "sshd.pid")
	config := fmt.Sprintf("Port %d\nListenAddress 127.0.0.1\nHostKey %s\nPidFile %s\nAuthorizedKeysFile %s\nPasswordAuthentication no\nKbdInteractiveAuthentication no\nUsePAM no\nStrictModes no\nPermitRootLogin no\nSubsystem sftp internal-sftp\nAllowUsers %s\n", server.port, hostKey, pidFile, authorized, username)
	if err := os.WriteFile(serverConfig, []byte(config), 0o600); err != nil {
		t.Fatal(err)
	}
	// #nosec G204 -- fixed system sshd path and test-owned configuration path.
	server.command = exec.Command("/usr/sbin/sshd", "-D", "-e", "-f", serverConfig)
	server.command.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	server.command.Stdout = &server.logs
	server.command.Stderr = &server.logs
	if err := server.command.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(server.stop)
	deadline := time.Now().Add(5 * time.Second)
	for {
		connection, dialErr := net.DialTimeout("tcp", net.JoinHostPort("127.0.0.1", strconv.Itoa(server.port)), 100*time.Millisecond)
		if dialErr == nil {
			_ = connection.Close()
			return server
		}
		if time.Now().After(deadline) {
			t.Fatalf("sshd not ready: %v\n%s", dialErr, server.logs.String())
		}
		time.Sleep(25 * time.Millisecond)
	}
}

func (s *testSSHD) stop() {
	s.stopOnce.Do(func() {
		if s.command.Process != nil {
			pid := strconv.Itoa(s.command.Process.Pid)
			// #nosec G204 -- the fixed pkill path receives only the decimal PID of the test-owned sshd.
			_ = exec.Command("/usr/bin/pkill", "-KILL", "-P", pid).Run()
			_ = syscall.Kill(-s.command.Process.Pid, syscall.SIGKILL)
		}
		waitDone := make(chan struct{})
		go func() {
			_ = s.command.Wait()
			close(waitDone)
		}()
		select {
		case <-waitDone:
		case <-time.After(2 * time.Second):
		}
	})
}

func sshHostConfig(alias string, server *testSSHD, username, clientKey string) string {
	knownHosts := filepath.Join(server.root, "known_hosts")
	return fmt.Sprintf("\nHost %s\n  HostName 127.0.0.1\n  Port %d\n  User %s\n  IdentityFile %s\n  IdentitiesOnly yes\n  BatchMode yes\n  StrictHostKeyChecking accept-new\n  UserKnownHostsFile %s\n  GlobalKnownHostsFile /dev/null\n  RequestTTY force\n  EscapeChar ~\n  SessionType none\n  ForwardAgent yes\n  ForwardX11 yes\n  PermitLocalCommand yes\n  LocalCommand /usr/bin/false\n  RemoteCommand /usr/bin/false\n  StdinNull yes\n  ForkAfterAuthentication yes\n  Tunnel yes\n  ClearAllForwardings no\n", alias, server.port, username, clientKey, knownHosts)
}

func connectSFTP(t *testing.T, ctx context.Context, alias, root string, endpointID domain.EndpointID) *sftpprovider.Provider {
	t.Helper()
	transport, err := openssh.Dial(ctx, openssh.Config{HostAlias: alias})
	if err != nil {
		t.Fatalf("OpenSSH dial %s: %v", alias, err)
	}
	implementation, err := sftpprovider.New(sftpprovider.Config{Endpoint: domain.Endpoint{ID: endpointID, Kind: domain.EndpointSSH, DisplayName: alias, SSHHostAlias: alias}, SessionID: "sess_aaaaaaaaaaaaaaaaaaaaaaaaaa", Client: transport.Client(), Close: transport.Close, Root: root})
	if err != nil {
		_ = transport.Close()
		t.Fatal(err)
	}
	return implementation
}

func assertContainsEntry(t *testing.T, ctx context.Context, implementation *sftpprovider.Provider, expected string) {
	t.Helper()
	location, err := implementation.Normalize(ctx, domain.NormalizeRequest{EndpointID: implementation.Descriptor().ID, Input: "/"})
	if err != nil {
		t.Fatal(err)
	}
	page, err := implementation.List(ctx, provider.ListRequest{Location: location, Limit: 64})
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range page.Entries {
		if entry.Name == expected {
			return
		}
	}
	names := make([]string, 0, len(page.Entries))
	for _, entry := range page.Entries {
		names = append(names, entry.Name)
	}
	t.Fatalf("SFTP list = %s, missing %s", strings.Join(names, ", "), expected)
}

func run(t *testing.T, binary string, args ...string) {
	t.Helper()
	// #nosec G204 -- callers supply fixed /usr/bin/ssh-keygen and test-owned arguments only.
	command := exec.Command(binary, args...)
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("%s: %v\n%s", binary, err, output)
	}
}
