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
	"github.com/TyrantLucifer/awesome-mac-sftp/internal/provider/localfs"
	sftpprovider "github.com/TyrantLucifer/awesome-mac-sftp/internal/provider/sftp"
	"github.com/TyrantLucifer/awesome-mac-sftp/internal/search"
	"github.com/TyrantLucifer/awesome-mac-sftp/internal/transfer"
	"github.com/TyrantLucifer/awesome-mac-sftp/internal/transport/openssh"
)

func TestRealOpenSSHSFTPHostAliasAndNonDefaultPort(t *testing.T) {
	runRealOpenSSHSFTP(t, false)
}

func TestRealSSHDLevel0Search(t *testing.T) {
	runRealOpenSSHSFTP(t, true)
}

func runRealOpenSSHSFTP(t *testing.T, searchOnly bool) {
	t.Helper()
	if os.Getenv("AMSFTP_REAL_SSHD") != "1" {
		t.Skip("set AMSFTP_REAL_SSHD=1 in an isolated account")
	}
	current, err := user.Current()
	if err != nil {
		t.Fatal(err)
	}
	keyRoot := t.TempDir()
	clientKey := filepath.Join(keyRoot, "client_key")
	runSSHKeygen(t, "-q", "-t", "ed25519", "-N", "", "-f", clientKey)
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
	first, firstTransport := connectSFTP(t, ctx, firstAlias, firstServer.root, "ep_aaaaaaaaaaaaaaaaaaaaaaaaaa")
	defer first.Close()
	second, _ := connectSFTP(t, ctx, secondAlias, secondServer.root, "ep_bbbbbbbbbbbbbbbbbbbbbbbbbb")
	defer second.Close()
	assertContainsEntry(t, ctx, first, "endpoint-first.txt")
	assertContainsEntry(t, ctx, second, "endpoint-second.txt")
	assertLevel0SFTPSearch(t, ctx, first, firstServer.root)
	if searchOnly {
		return
	}
	assertBidirectionalDurableTransfer(t, ctx, first, firstServer.root)
	assertDualRemoteDirectoryRelay(t, ctx, first, second, firstServer.root, secondServer.root)
	assertDualRemoteDirectoryRelay(t, ctx, first, first, firstServer.root, firstServer.root)
	if _, err := os.Stat(poisonMarker); !os.IsNotExist(err) {
		t.Fatalf("poisoned PATH ssh executed: %v", err)
	}

	if err := firstTransport.Close(); err != nil {
		t.Fatalf("close first OpenSSH transport: %v", err)
	}
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

func assertLevel0SFTPSearch(t *testing.T, ctx context.Context, implementation *sftpprovider.Provider, remoteRoot string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Join(remoteRoot, "search", "nested"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(remoteRoot, "search", "nested", "target.txt"), []byte("first\nremote needle\nlast\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	snapshot, err := implementation.Snapshot(ctx)
	if err != nil {
		t.Fatal(err)
	}
	scope := normalizeIntegration(t, ctx, implementation, "/search")
	filenameIdentity := search.Identity{
		RequestID: "req_hhhhhhhhhhhhhhhhhhhhhhhhhh", EndpointID: implementation.Descriptor().ID,
		SessionID: snapshot.SessionID, EndpointGeneration: snapshot.Capabilities.Revision.Generation, UIGeneration: 1, Scope: scope,
		Options: search.Options{Pattern: "target", Target: search.MatchRelativePath, CaseSensitive: true, Symlinks: search.SymlinkNever, Ignore: search.IgnoreNone, Types: search.TypeFilter{Files: true}},
		Budget:  search.Budget{PageItems: 8, EventBuffer: 4, ConcurrentLists: 1, MaxDepth: 8, MaxEntries: 64, MaxResults: 8, MaxOutputBytes: 4096, MaxDuration: 5 * time.Second},
	}
	filenameEvents, err := search.StartFilename(ctx, implementation, search.Request{Identity: filenameIdentity})
	if err != nil {
		t.Fatal(err)
	}
	var filenameResult search.Result
	var filenameTerminal search.Terminal
	for event := range filenameEvents {
		if event.Kind == search.EventResult {
			filenameResult = event.Result
		}
		if event.Kind == search.EventTerminal {
			filenameTerminal = event.Terminal
		}
	}
	if filenameResult.RelativePath != "nested/target.txt" || filenameTerminal.Status != search.StatusComplete {
		t.Fatalf("real SFTP filename search = result %#v terminal %#v", filenameResult, filenameTerminal)
	}

	contentIdentity := search.ContentIdentity{
		RequestID: "req_iiiiiiiiiiiiiiiiiiiiiiiiii", EndpointID: implementation.Descriptor().ID,
		SessionID: snapshot.SessionID, EndpointGeneration: snapshot.Capabilities.Revision.Generation, UIGeneration: 2, Scope: scope,
		Options: search.ContentOptions{Pattern: "needle", PatternType: search.PatternLiteral, CaseSensitive: true, Binary: search.BinarySkip},
		Budget:  search.ContentBudget{PageItems: 8, EventBuffer: 4, MaxDepth: 8, MaxEntries: 64, MaxFiles: 8, MaxResults: 8, MaxMatchesPerFile: 4, MaxFileBytes: 4096, MaxReadBytes: 16 * 1024, MaxSnippetBytes: 256, MaxOutputBytes: 4096, MaxDuration: 5 * time.Second},
	}
	contentEvents, err := search.StartContent(ctx, implementation, search.ContentRequest{Identity: contentIdentity})
	if err != nil {
		t.Fatal(err)
	}
	var contentResult search.ContentResult
	var contentTerminal search.ContentTerminal
	for event := range contentEvents {
		if event.Kind == search.ContentEventResult {
			contentResult = event.Result
		}
		if event.Kind == search.ContentEventTerminal {
			contentTerminal = event.Terminal
		}
	}
	if contentResult.RelativePath != "nested/target.txt" || contentResult.Line != 2 || contentTerminal.Status != search.StatusComplete {
		t.Fatalf("real SFTP content search = result %#v terminal %#v", contentResult, contentTerminal)
	}
}

func assertDualRemoteDirectoryRelay(
	t *testing.T,
	ctx context.Context,
	source *sftpprovider.Provider,
	destination *sftpprovider.Provider,
	sourceRoot string,
	destinationRoot string,
) {
	t.Helper()
	if err := os.MkdirAll(filepath.Join(sourceRoot, "relay-tree", "nested"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(sourceRoot, "relay-tree", "root.txt"), []byte("remote-a"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(sourceRoot, "relay-tree", "nested", "child.txt"), []byte("remote-b"), 0o600); err != nil {
		t.Fatal(err)
	}
	resolver := transfer.MapResolver{source.Descriptor().ID: source, destination.Descriptor().ID: destination}
	planner := transfer.NewPlanner(resolver)
	reference, err := planner.Capture(ctx, normalizeIntegration(t, ctx, source, "/relay-tree"))
	if err != nil {
		t.Fatal(err)
	}
	plan, _, err := planner.FreezeCopy(ctx, integrationFreezeRequest(t, 'c', reference, normalizeIntegration(t, ctx, destination, "/"), "relayed-tree"))
	if err != nil {
		t.Fatal(err)
	}
	plan.BufferBytes = 7
	journal := newIntegrationJournal()
	result, err := transfer.NewWorker(resolver, journal).Execute(ctx, plan, nil)
	if err != nil || result.Outcome != transfer.OutcomeCompleted || result.Items != 3 || result.Bytes != 16 {
		t.Fatalf("remote A -> B directory relay = (%#v, %v)", result, err)
	}
	if journal.maxBuffer > 2*int(plan.BufferBytes) {
		t.Fatalf("remote directory relay buffer = %d, ceiling = %d", journal.maxBuffer, 2*plan.BufferBytes)
	}
	for relative, want := range map[string]string{"root.txt": "remote-a", "nested/child.txt": "remote-b"} {
		// #nosec G304 -- destinationRoot is the private root of this test's second sshd.
		data, err := os.ReadFile(filepath.Join(destinationRoot, "relayed-tree", filepath.FromSlash(relative)))
		if err != nil || string(data) != want {
			t.Fatalf("relayed %s = %q, %v", relative, data, err)
		}
	}
}

func TestStage2TemporarySSHDPTYUploadDownloadMVP(t *testing.T) {
	if os.Getenv("AMSFTP_REAL_SSHD") != "1" {
		t.Skip("set AMSFTP_REAL_SSHD=1 in an isolated account")
	}
	current, err := user.Current()
	if err != nil {
		t.Fatal(err)
	}
	keyRoot := t.TempDir()
	clientKey := filepath.Join(keyRoot, "client_key")
	runSSHKeygen(t, "-q", "-t", "ed25519", "-N", "", "-f", clientKey)
	// #nosec G304 -- path is generated inside this test's private TempDir.
	publicKey, err := os.ReadFile(clientKey + ".pub")
	if err != nil {
		t.Fatal(err)
	}
	server := startTestSSHD(t, current.Username, publicKey, "stage2-mvp")
	alias := "amsftp-stage2-mvp"
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
	// #nosec G703 -- guarded integration test writes only the isolated account's canonical SSH config.
	if err := os.WriteFile(configPath, append(original, []byte(sshHostConfig(alias, server, current.Username, clientKey))...), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if hadConfig {
			// #nosec G703 -- restores the exact isolated runner config captured above.
			_ = os.WriteFile(configPath, original, 0o600)
		} else {
			_ = os.Remove(configPath)
		}
	})
	binary, observer := buildStage2MVPFixtures(t)
	remoteRoot := t.TempDir()
	// #nosec G204 -- script and alias are fixed, and paths are test-owned.
	command := exec.Command("python3", "hosted-stage2-mvp.py", binary, alias, remoteRoot)
	command.Env = append(os.Environ(), "AMSFTP_STAGE2_VT_OBSERVER="+observer)
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("Stage 2 temporary-sshd PTY MVP failed: %v\n%s\nsshd:\n%s", err, output, server.logs.String())
	}
}

func assertBidirectionalDurableTransfer(t *testing.T, ctx context.Context, remote *sftpprovider.Provider, remoteRoot string) {
	t.Helper()
	snapshot, err := remote.Snapshot(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := snapshot.Capabilities.Lookup("write"); !ok {
		t.Fatalf("real OpenSSH SFTP capability snapshot = %#v, want write", snapshot.Capabilities)
	}

	uploadRoot := t.TempDir()
	if err := os.WriteFile(filepath.Join(uploadRoot, "upload-source.txt"), []byte("local-to-remote"), 0o600); err != nil {
		t.Fatal(err)
	}
	uploadSource, err := localfs.New(localfs.Config{
		Endpoint:  domain.Endpoint{ID: "ep_cccccccccccccccccccccccccc", Kind: domain.EndpointLocal, DisplayName: "upload-source"},
		SessionID: "sess_cccccccccccccccccccccccccc",
		Root:      uploadRoot,
	})
	if err != nil {
		t.Fatal(err)
	}
	defer uploadSource.Close()
	resolver := transfer.MapResolver{uploadSource.Descriptor().ID: uploadSource, remote.Descriptor().ID: remote}
	uploadPlanner := transfer.NewPlanner(resolver)
	uploadLocation := normalizeIntegration(t, ctx, uploadSource, "/upload-source.txt")
	uploadReference, err := uploadPlanner.Capture(ctx, uploadLocation)
	if err != nil {
		t.Fatal(err)
	}
	uploadPlan, _, err := uploadPlanner.FreezeCopy(ctx, integrationFreezeRequest(t, 'c', uploadReference, normalizeIntegration(t, ctx, remote, "/"), "uploaded.txt"))
	if err != nil {
		t.Fatal(err)
	}
	if result, err := transfer.NewWorker(resolver, newIntegrationJournal()).Execute(ctx, uploadPlan, nil); err != nil || result.Outcome != transfer.OutcomeCompleted {
		t.Fatalf("local -> SFTP transfer = (%#v, %v)", result, err)
	}
	// #nosec G304 -- remoteRoot is this test's private sshd root.
	uploaded, err := os.ReadFile(filepath.Join(remoteRoot, "uploaded.txt"))
	if err != nil || string(uploaded) != "local-to-remote" {
		t.Fatalf("remote upload = %q, %v", uploaded, err)
	}

	downloadRoot := t.TempDir()
	downloadDestination, err := localfs.New(localfs.Config{
		Endpoint:  domain.Endpoint{ID: "ep_dddddddddddddddddddddddddd", Kind: domain.EndpointLocal, DisplayName: "download-destination"},
		SessionID: "sess_dddddddddddddddddddddddddd",
		Root:      downloadRoot,
	})
	if err != nil {
		t.Fatal(err)
	}
	defer downloadDestination.Close()
	resolver = transfer.MapResolver{remote.Descriptor().ID: remote, downloadDestination.Descriptor().ID: downloadDestination}
	downloadPlanner := transfer.NewPlanner(resolver)
	downloadReference, err := downloadPlanner.Capture(ctx, normalizeIntegration(t, ctx, remote, "/uploaded.txt"))
	if err != nil {
		t.Fatal(err)
	}
	downloadPlan, _, err := downloadPlanner.FreezeCopy(ctx, integrationFreezeRequest(t, 'd', downloadReference, normalizeIntegration(t, ctx, downloadDestination, "/"), "downloaded.txt"))
	if err != nil {
		t.Fatal(err)
	}
	if result, err := transfer.NewWorker(resolver, newIntegrationJournal()).Execute(ctx, downloadPlan, nil); err != nil || result.Outcome != transfer.OutcomeCompleted {
		t.Fatalf("SFTP -> local transfer = (%#v, %v)", result, err)
	}
	// #nosec G304 -- downloadRoot is this test's private local provider root.
	downloaded, err := os.ReadFile(filepath.Join(downloadRoot, "downloaded.txt"))
	if err != nil || string(downloaded) != "local-to-remote" {
		t.Fatalf("local download = %q, %v", downloaded, err)
	}
}

func integrationFreezeRequest(t *testing.T, seed byte, source transfer.FileRef, destination domain.Location, name string) transfer.FreezeRequest {
	t.Helper()
	generator := &domain.RandomGenerator{Reader: strings.NewReader(strings.Repeat(string([]byte{seed}), 64))}
	requestID, err := domain.NewRequestID(generator)
	if err != nil {
		t.Fatal(err)
	}
	jobID, err := domain.NewJobID(generator)
	if err != nil {
		t.Fatal(err)
	}
	eventID, err := domain.NewEventID(generator)
	if err != nil {
		t.Fatal(err)
	}
	return transfer.FreezeRequest{
		Intent: transfer.Intent{
			Clipboard:            transfer.ClipboardCopy,
			Source:               source,
			DestinationDirectory: destination,
			Name:                 name,
			ConflictPolicy:       transfer.ConflictAsk,
		},
		RequestID: requestID,
		PlanID:    "plan_" + string(jobID)[4:],
		JobID:     jobID,
		EventID:   eventID,
		Now:       time.Now(),
	}
}

func normalizeIntegration(t *testing.T, ctx context.Context, implementation provider.Provider, input string) domain.Location {
	t.Helper()
	location, err := implementation.Normalize(ctx, domain.NormalizeRequest{EndpointID: implementation.Descriptor().ID, Input: input})
	if err != nil {
		t.Fatal(err)
	}
	return location
}

type integrationJournal struct {
	mu          sync.Mutex
	checkpoints map[domain.JobID]transfer.Checkpoint
	maxBuffer   int
}

func (journal *integrationJournal) ObserveBuffer(bytes int) {
	journal.mu.Lock()
	defer journal.mu.Unlock()
	if bytes > journal.maxBuffer {
		journal.maxBuffer = bytes
	}
}

func newIntegrationJournal() *integrationJournal {
	return &integrationJournal{checkpoints: make(map[domain.JobID]transfer.Checkpoint)}
}

func (journal *integrationJournal) Load(_ context.Context, jobID domain.JobID) (*transfer.Checkpoint, error) {
	journal.mu.Lock()
	defer journal.mu.Unlock()
	checkpoint, ok := journal.checkpoints[jobID]
	if !ok {
		return nil, nil
	}
	checkpoint.ChecksumState = append([]byte(nil), checkpoint.ChecksumState...)
	return &checkpoint, nil
}

func (journal *integrationJournal) Save(_ context.Context, checkpoint transfer.Checkpoint) error {
	journal.mu.Lock()
	defer journal.mu.Unlock()
	checkpoint.ChecksumState = append([]byte(nil), checkpoint.ChecksumState...)
	journal.checkpoints[checkpoint.JobID] = checkpoint
	return nil
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
	runSSHKeygen(t, "-q", "-t", "ed25519", "-N", "", "-f", hostKey)
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

func connectSFTP(t *testing.T, ctx context.Context, alias, root string, endpointID domain.EndpointID) (*sftpprovider.Provider, *openssh.Session) {
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
	return implementation, transport
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

func runSSHKeygen(t *testing.T, args ...string) {
	t.Helper()
	// #nosec G204 -- executable is fixed and callers supply only test-owned arguments.
	command := exec.Command("/usr/bin/ssh-keygen", args...)
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("ssh-keygen: %v\n%s", err, output)
	}
}
