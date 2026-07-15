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
	root := t.TempDir()
	clientKey := filepath.Join(root, "client_key")
	hostKey := filepath.Join(root, "host_key")
	run(t, "/usr/bin/ssh-keygen", "-q", "-t", "ed25519", "-N", "", "-f", clientKey)
	run(t, "/usr/bin/ssh-keygen", "-q", "-t", "ed25519", "-N", "", "-f", hostKey)
	// #nosec G304 -- path is generated inside this test's private TempDir.
	publicKey, err := os.ReadFile(clientKey + ".pub")
	if err != nil {
		t.Fatal(err)
	}
	authorized := filepath.Join(root, "authorized_keys")
	// #nosec G703 -- destination is fixed inside this test's private TempDir.
	if err := os.WriteFile(authorized, publicKey, 0o600); err != nil {
		t.Fatal(err)
	}
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	port := listener.Addr().(*net.TCPAddr).Port
	_ = listener.Close()
	serverConfig := filepath.Join(root, "sshd_config")
	pidFile := filepath.Join(root, "sshd.pid")
	config := fmt.Sprintf("Port %d\nListenAddress 127.0.0.1\nHostKey %s\nPidFile %s\nAuthorizedKeysFile %s\nPasswordAuthentication no\nKbdInteractiveAuthentication no\nUsePAM no\nStrictModes no\nPermitRootLogin no\nSubsystem sftp internal-sftp\nAllowUsers %s\n", port, hostKey, pidFile, authorized, current.Username)
	if err := os.WriteFile(serverConfig, []byte(config), 0o600); err != nil {
		t.Fatal(err)
	}
	var logs bytes.Buffer
	// #nosec G204 -- fixed system sshd path and test-owned configuration path.
	server := exec.Command("/usr/sbin/sshd", "-D", "-e", "-f", serverConfig)
	server.Stdout = &logs
	server.Stderr = &logs
	if err := server.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if server.Process != nil {
			_ = server.Process.Kill()
		}
		_ = server.Wait()
	})
	deadline := time.Now().Add(5 * time.Second)
	for {
		connection, dialErr := net.DialTimeout("tcp", net.JoinHostPort("127.0.0.1", strconv.Itoa(port)), 100*time.Millisecond)
		if dialErr == nil {
			_ = connection.Close()
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("sshd not ready: %v\n%s", dialErr, logs.String())
		}
		time.Sleep(25 * time.Millisecond)
	}
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
	knownHosts := filepath.Join(root, "known_hosts")
	alias := "amsftp-stage1-test"
	clientConfig := fmt.Sprintf("\nHost %s\n  HostName 127.0.0.1\n  Port %d\n  User %s\n  IdentityFile %s\n  IdentitiesOnly yes\n  BatchMode yes\n  StrictHostKeyChecking accept-new\n  UserKnownHostsFile %s\n  GlobalKnownHostsFile /dev/null\n", alias, port, current.Username, clientKey, knownHosts)
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
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	transport, err := openssh.Dial(ctx, openssh.Config{HostAlias: alias})
	if err != nil {
		t.Fatalf("OpenSSH dial: %v", err)
	}
	implementation, err := sftpprovider.New(sftpprovider.Config{Endpoint: domain.Endpoint{ID: "ep_aaaaaaaaaaaaaaaaaaaaaaaaaa", Kind: domain.EndpointSSH, DisplayName: alias, SSHHostAlias: alias}, SessionID: "sess_aaaaaaaaaaaaaaaaaaaaaaaaaa", Client: transport.Client(), Close: transport.Close, Root: root})
	if err != nil {
		t.Fatal(err)
	}
	defer implementation.Close()
	location, err := implementation.Normalize(ctx, domain.NormalizeRequest{EndpointID: implementation.Descriptor().ID, Input: "/"})
	if err != nil {
		t.Fatal(err)
	}
	page, err := implementation.List(ctx, provider.ListRequest{Location: location, Limit: 16})
	if err != nil {
		t.Fatal(err)
	}
	if len(page.Entries) == 0 {
		t.Fatal("real SFTP list returned no entries")
	}
}

func run(t *testing.T, binary string, args ...string) {
	t.Helper()
	// #nosec G204 -- callers supply fixed /usr/bin/ssh-keygen and test-owned arguments only.
	command := exec.Command(binary, args...)
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("%s: %v\n%s", binary, err, output)
	}
}
