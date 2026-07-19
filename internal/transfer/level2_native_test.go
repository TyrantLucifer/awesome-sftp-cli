//go:build darwin || linux

package transfer

import (
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

	"github.com/TyrantLucifer/awesome-sftp-cli/internal/domain"
	providerapi "github.com/TyrantLucifer/awesome-sftp-cli/internal/provider"
	sftpprovider "github.com/TyrantLucifer/awesome-sftp-cli/internal/provider/sftp"
	"github.com/TyrantLucifer/awesome-sftp-cli/internal/testkit"
	pkgsftp "github.com/pkg/sftp"
)

type nativeDirectSSHD struct {
	root     string
	dataRoot string
	port     int
	cmd      *exec.Cmd
	logs     testkit.ConcurrentBuffer
	once     sync.Once
}

type nativeSFTPProcess struct {
	client *pkgsftp.Client
	cmd    *exec.Cmd
}

func TestLevel2NativeDualSSHDUsesIsolatedControlSessionsAndFixtureLocalDataPlane(t *testing.T) {
	if os.Getenv("AMSFTP_REAL_SSHD") != "1" {
		t.Skip("set AMSFTP_REAL_SSHD=1 in an isolated account")
	}
	current, err := user.Current()
	if err != nil {
		t.Fatal(err)
	}
	keyRoot := t.TempDir()
	clientKey := filepath.Join(keyRoot, "client_key")
	nativeSSHKeygen(t, "-q", "-t", "ed25519", "-N", "", "-f", clientKey)
	publicKey, err := os.ReadFile(clientKey + ".pub") // #nosec G304 -- isolated generated fixture key.
	if err != nil {
		t.Fatal(err)
	}
	sourceServer := startNativeDirectSSHD(t, current.Username, publicKey)
	destinationServer := startNativeDirectSSHD(t, current.Username, publicKey)
	if err := os.WriteFile(filepath.Join(sourceServer.dataRoot, "source.bin"), []byte("direct payload"), 0o600); err != nil {
		t.Fatal(err)
	}
	configPath := writeNativeDirectSSHConfig(t, current.Username, clientKey, sourceServer, destinationServer)
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	sourceProcess := startNativeSFTPProcess(t, ctx, configPath, "native-direct-source")
	destinationProcess := startNativeSFTPProcess(t, ctx, configPath, "native-direct-target")
	sourceProvider := newNativeSFTPProvider(t, sourceProcess, sourceServer.dataRoot, "ep_aaaaaaaaaaaaaaaaaaaaaaaaaa", "native-direct-source")
	destinationProvider := newNativeSFTPProvider(t, destinationProcess, destinationServer.dataRoot, "ep_bbbbbbbbbbbbbbbbbbbbbbbbbb", "native-direct-target")
	resolver := MapResolver{sourceProvider.Descriptor().ID: sourceProvider, destinationProvider.Descriptor().ID: destinationProvider}
	planner := newLevel2FixturePlanner(resolver, &level2PreflightFixture{result: passingLevel2PreflightResult})
	reference, err := planner.Capture(ctx, nativeNormalize(t, ctx, sourceProvider, "/source.bin"))
	if err != nil {
		t.Fatal(err)
	}
	request := validFreezeRequest(reference, nativeNormalize(t, ctx, destinationProvider, "/"))
	request.Intent.DirectPolicy = DirectPolicy{UserEnabled: true, WorkspaceEnabled: true, DataAllowed: true, Integrity: IntegrityRequireStrong}
	plan, _, err := planner.FreezeCopy(ctx, request)
	if err != nil {
		t.Fatal(err)
	}
	source := &countingNativeProvider{mutableTestProvider: sourceProvider}
	destination := &countingNativeProvider{mutableTestProvider: destinationProvider}
	backend := &level2DataFixture{sourceRoot: sourceServer.dataRoot, destinationRoot: destinationServer.dataRoot}
	result, err := newLevel2FixtureWorker(MapResolver{plan.SourceEndpoint.ID: source, plan.DestinationEndpoint.ID: destination}, newMemoryJournal(), backend).Execute(ctx, plan, nil)
	if err != nil || result.Outcome != OutcomeCompleted {
		t.Fatalf("native dual-sshd direct = (%#v, %v)\nsource sshd:\n%s\ntarget sshd:\n%s", result, err, sourceServer.logs.String(), destinationServer.logs.String())
	}
	if source.openReads != 0 || destination.openReads != 0 || backend.stagedBytes != result.Bytes {
		t.Fatalf("native control-session Provider reads/fixture-local staged bytes = %d/%d/%d", source.openReads, destination.openReads, backend.stagedBytes)
	}
	entries, err := os.ReadDir(destinationServer.dataRoot)
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range entries {
		name := strings.ToLower(entry.Name())
		if strings.Contains(name, "key") || strings.Contains(name, "known") || strings.Contains(name, "ticket") || strings.Contains(name, "credential") {
			t.Fatalf("credential material entered target root: %s", entry.Name())
		}
	}
}

type countingNativeProvider struct {
	mutableTestProvider
	openReads int
}

func (provider *countingNativeProvider) OpenRead(ctx context.Context, request providerapi.OpenReadRequest) (providerapi.ReadHandle, error) {
	provider.openReads++
	return provider.mutableTestProvider.OpenRead(ctx, request)
}

func startNativeDirectSSHD(t *testing.T, username string, publicKey []byte) *nativeDirectSSHD {
	t.Helper()
	server := &nativeDirectSSHD{root: t.TempDir()}
	server.dataRoot = filepath.Join(server.root, "data")
	if err := os.Mkdir(server.dataRoot, 0o700); err != nil {
		t.Fatal(err)
	}
	hostKey := filepath.Join(server.root, "host_key")
	nativeSSHKeygen(t, "-q", "-t", "ed25519", "-N", "", "-f", hostKey)
	authorized := filepath.Join(server.root, "authorized_keys")
	// #nosec G703 -- destination is fixed beneath the isolated server TempDir.
	if err := os.WriteFile(authorized, publicKey, 0o600); err != nil {
		t.Fatal(err)
	}
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	server.port = listener.Addr().(*net.TCPAddr).Port
	_ = listener.Close()
	config := fmt.Sprintf("Port %d\nListenAddress 127.0.0.1\nHostKey %s\nPidFile %s\nAuthorizedKeysFile %s\nPasswordAuthentication no\nKbdInteractiveAuthentication no\nUsePAM no\nStrictModes no\nPermitRootLogin no\nSubsystem sftp internal-sftp\nAllowUsers %s\n", server.port, hostKey, filepath.Join(server.root, "sshd.pid"), authorized, username)
	configPath := filepath.Join(server.root, "sshd_config")
	if err := os.WriteFile(configPath, []byte(config), 0o600); err != nil {
		t.Fatal(err)
	}
	server.cmd = exec.Command("/usr/sbin/sshd", "-D", "-e", "-f", configPath) // #nosec G204 -- fixed sshd and isolated config.
	server.cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	server.cmd.Stdout, server.cmd.Stderr = &server.logs, &server.logs
	if err := server.cmd.Start(); err != nil {
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
			t.Fatalf("native direct sshd not ready: %v\n%s", dialErr, server.logs.String())
		}
		time.Sleep(25 * time.Millisecond)
	}
}

func (server *nativeDirectSSHD) stop() {
	server.once.Do(func() {
		if server.cmd.Process != nil {
			_ = syscall.Kill(-server.cmd.Process.Pid, syscall.SIGKILL)
		}
		_ = server.cmd.Wait()
	})
}

func writeNativeDirectSSHConfig(t *testing.T, username, clientKey string, source, destination *nativeDirectSSHD) string {
	t.Helper()
	configPath := filepath.Join(t.TempDir(), "ssh_config")
	var config strings.Builder
	for _, item := range []struct {
		alias  string
		server *nativeDirectSSHD
	}{{"native-direct-source", source}, {"native-direct-target", destination}} {
		publicKey, err := os.ReadFile(filepath.Join(item.server.root, "host_key.pub")) // #nosec G304 -- generated fixture key.
		if err != nil {
			t.Fatal(err)
		}
		knownHosts := filepath.Join(item.server.root, "known_hosts")
		line := fmt.Sprintf("[127.0.0.1]:%d %s", item.server.port, publicKey)
		// #nosec G703 -- destination is fixed beneath the isolated server TempDir.
		if err := os.WriteFile(knownHosts, []byte(line), 0o600); err != nil {
			t.Fatal(err)
		}
		fmt.Fprintf(&config, "Host %s\n HostName 127.0.0.1\n Port %d\n User %s\n IdentityFile %s\n IdentitiesOnly yes\n BatchMode yes\n StrictHostKeyChecking yes\n UserKnownHostsFile %s\n GlobalKnownHostsFile /dev/null\n ForwardAgent no\n ForwardX11 no\n GSSAPIAuthentication no\n GSSAPIDelegateCredentials no\n ControlMaster no\n ControlPath none\n ControlPersist no\n PasswordAuthentication no\n KbdInteractiveAuthentication no\n RequestTTY no\n ClearAllForwardings yes\n\n", item.alias, item.server.port, username, clientKey, knownHosts)
	}
	if err := os.WriteFile(configPath, []byte(config.String()), 0o600); err != nil {
		t.Fatal(err)
	}
	return configPath
}

func startNativeSFTPProcess(t *testing.T, ctx context.Context, configPath, alias string) *nativeSFTPProcess {
	t.Helper()
	args := []string{"-F", configPath, "-oForwardAgent=no", "-oGSSAPIDelegateCredentials=no", "-oControlMaster=no", "-oControlPath=none", "-oControlPersist=no", "-oBatchMode=yes", "-oStrictHostKeyChecking=yes", alias, "-s", "sftp"}
	command := exec.CommandContext(ctx, "/usr/bin/ssh", args...) // #nosec G204 -- fixed ssh and typed test-owned config/alias.
	stdin, err := command.StdinPipe()
	if err != nil {
		t.Fatal(err)
	}
	stdout, err := command.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	var stderr testkit.ConcurrentBuffer
	command.Stderr = &stderr
	if err := command.Start(); err != nil {
		t.Fatal(err)
	}
	client, err := pkgsftp.NewClientPipe(stdout, stdin)
	if err != nil {
		_ = command.Process.Kill()
		t.Fatalf("native SFTP handshake: %v\n%s", err, stderr.String())
	}
	process := &nativeSFTPProcess{client: client, cmd: command}
	t.Cleanup(func() { _ = client.Close(); _ = command.Wait() })
	return process
}

func newNativeSFTPProvider(t *testing.T, process *nativeSFTPProcess, root string, endpointID domain.EndpointID, alias string) *sftpprovider.Provider {
	t.Helper()
	provider, err := sftpprovider.New(sftpprovider.Config{Endpoint: domain.Endpoint{ID: endpointID, Kind: domain.EndpointSSH, DisplayName: alias, SSHHostAlias: alias}, SessionID: "sess_aaaaaaaaaaaaaaaaaaaaaaaaaa", Client: process.client, Root: root})
	if err != nil {
		t.Fatal(err)
	}
	return provider
}

func nativeNormalize(t *testing.T, ctx context.Context, provider providerapi.Provider, input string) domain.Location {
	t.Helper()
	location, err := provider.Normalize(ctx, domain.NormalizeRequest{EndpointID: provider.Descriptor().ID, Input: input})
	if err != nil {
		t.Fatal(err)
	}
	return location
}

func nativeSSHKeygen(t *testing.T, args ...string) {
	t.Helper()
	command := exec.Command("/usr/bin/ssh-keygen", args...) // #nosec G204 -- fixed tool and isolated fixture arguments.
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("ssh-keygen: %v\n%s", err, output)
	}
}
