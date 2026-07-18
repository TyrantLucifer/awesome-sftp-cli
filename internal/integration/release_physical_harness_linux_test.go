//go:build linux

package integration

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/url"
	"os"
	"os/exec"
	"os/user"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/TyrantLucifer/awesome-mac-sftp/internal/domain"
	"github.com/TyrantLucifer/awesome-mac-sftp/internal/provider/localfs"
	sftpprovider "github.com/TyrantLucifer/awesome-mac-sftp/internal/provider/sftp"
	"github.com/TyrantLucifer/awesome-mac-sftp/internal/state/jobstore"
	"github.com/TyrantLucifer/awesome-mac-sftp/internal/state/migration"
	"github.com/TyrantLucifer/awesome-mac-sftp/internal/transfer"
	"github.com/TyrantLucifer/awesome-mac-sftp/internal/transport/openssh"
	"golang.org/x/sys/unix"
	_ "modernc.org/sqlite"
)

const (
	physicalReleaseReportSchema = "amsftp.release-lab.physical-100gib.v1"
	physicalReleasePurposePreRC = "pre-rc-harness-proof-not-final-rc-evidence"
)

var lowerHex40 = regexp.MustCompile(`^[0-9a-f]{40}$`)

type physicalReleaseEnvironment struct {
	RepoRoot        string
	LabRoot         string
	ControlRoot     string
	EvidencePath    string
	CandidateCommit string
	CandidateTree   string
	Bytes           int64
	CancelAfter     int64
}

type physicalAllocation struct {
	LogicalBytes  int64 `json:"logical_bytes"`
	PhysicalBytes int64 `json:"physical_bytes"`
}

type physicalReleaseReport struct {
	Schema             string             `json:"schema"`
	Purpose            string             `json:"purpose"`
	CandidateCommit    string             `json:"candidate_commit"`
	CandidateTree      string             `json:"candidate_tree"`
	StartedAt          time.Time          `json:"started_at"`
	CompletedAt        time.Time          `json:"completed_at"`
	Filesystem         string             `json:"filesystem"`
	BytesPerDirection  uint64             `json:"bytes_per_direction"`
	TotalBytes         uint64             `json:"total_bytes"`
	ResumeOffset       uint64             `json:"resume_offset"`
	UploadSHA256       string             `json:"upload_sha256"`
	DownloadSHA256     string             `json:"download_sha256"`
	SourceAllocation   physicalAllocation `json:"source_allocation"`
	RemoteAllocation   physicalAllocation `json:"remote_allocation"`
	DownloadAllocation physicalAllocation `json:"download_allocation"`
}

func validatePhysicalReleaseEnvironment(environment physicalReleaseEnvironment) error {
	for label, path := range map[string]string{
		"repository root": environment.RepoRoot,
		"lab root":        environment.LabRoot,
		"control root":    environment.ControlRoot,
		"evidence path":   environment.EvidencePath,
	} {
		if !filepath.IsAbs(path) || filepath.Clean(path) != path {
			return fmt.Errorf("%s must be absolute and canonical", label)
		}
	}
	if environment.LabRoot == string(filepath.Separator) || environment.ControlRoot == string(filepath.Separator) {
		return errors.New("lab and control roots must not be the filesystem root")
	}
	if pathWithin(environment.RepoRoot, environment.LabRoot) {
		return errors.New("lab root must be outside repository")
	}
	if pathWithin(environment.RepoRoot, environment.ControlRoot) {
		return errors.New("control root must be outside repository")
	}
	if pathWithin(environment.LabRoot, environment.ControlRoot) || pathWithin(environment.ControlRoot, environment.LabRoot) {
		return errors.New("control root must be separate from lab root")
	}
	if pathWithin(environment.LabRoot, environment.EvidencePath) {
		return errors.New("evidence path must be outside lab root")
	}
	if pathWithin(environment.ControlRoot, environment.EvidencePath) {
		return errors.New("evidence path must be outside control root")
	}
	if pathWithin(environment.RepoRoot, environment.EvidencePath) {
		return errors.New("evidence path must be outside repository")
	}
	if environment.Bytes != physicalReleaseBytes {
		return errors.New("physical release size must be exactly 100 GiB")
	}
	if environment.CancelAfter <= 0 || environment.CancelAfter >= environment.Bytes {
		return errors.New("cancel checkpoint must be positive and below the transfer size")
	}
	if !lowerHex40.MatchString(environment.CandidateCommit) {
		return errors.New("candidate commit must be 40 lowercase hexadecimal characters")
	}
	if !lowerHex40.MatchString(environment.CandidateTree) {
		return errors.New("candidate tree must be 40 lowercase hexadecimal characters")
	}
	return nil
}

func pathWithin(parent, candidate string) bool {
	relative, err := filepath.Rel(parent, candidate)
	return err == nil && relative != ".." && !strings.HasPrefix(relative, ".."+string(filepath.Separator))
}

func inspectPhysicalAllocation(path string, minimum int64) (physicalAllocation, error) {
	info, err := os.Lstat(path)
	if err != nil {
		return physicalAllocation{}, err
	}
	if !info.Mode().IsRegular() {
		return physicalAllocation{}, fmt.Errorf("physical evidence %q is not a regular file", path)
	}
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !ok {
		return physicalAllocation{}, fmt.Errorf("physical evidence %q has unsupported stat metadata", path)
	}
	allocation := physicalAllocation{LogicalBytes: info.Size(), PhysicalBytes: stat.Blocks * 512}
	if allocation.LogicalBytes < minimum {
		return physicalAllocation{}, fmt.Errorf("physical evidence %q is %d bytes, want at least %d", path, allocation.LogicalBytes, minimum)
	}
	if allocation.PhysicalBytes < allocation.LogicalBytes {
		return physicalAllocation{}, fmt.Errorf("physical evidence %q is sparse: logical=%d physical=%d", path, allocation.LogicalBytes, allocation.PhysicalBytes)
	}
	return allocation, nil
}

func validatePhysicalReleaseReport(report physicalReleaseReport) error {
	if report.Schema != physicalReleaseReportSchema || report.Purpose != physicalReleasePurposePreRC {
		return errors.New("physical report schema or purpose is not the pre-RC contract")
	}
	if !lowerHex40.MatchString(report.CandidateCommit) || !lowerHex40.MatchString(report.CandidateTree) {
		return errors.New("physical report candidate identities are invalid")
	}
	if report.StartedAt.IsZero() || !report.CompletedAt.After(report.StartedAt) {
		return errors.New("physical report timestamps are invalid")
	}
	if report.Filesystem == "" {
		return errors.New("physical report filesystem is empty")
	}
	wantBytes := uint64(physicalReleaseBytes)
	if report.BytesPerDirection != wantBytes {
		return fmt.Errorf("bytes per direction = %d, want %d", report.BytesPerDirection, wantBytes)
	}
	if report.TotalBytes != 2*wantBytes {
		return fmt.Errorf("total bytes = %d, want %d", report.TotalBytes, 2*wantBytes)
	}
	if report.ResumeOffset == 0 || report.ResumeOffset >= wantBytes {
		return errors.New("physical report resume offset is invalid")
	}
	if len(report.UploadSHA256) != 64 || report.UploadSHA256 != report.DownloadSHA256 {
		return errors.New("upload and download SHA-256 values do not match")
	}
	for label, allocation := range map[string]physicalAllocation{
		"source allocation":   report.SourceAllocation,
		"remote allocation":   report.RemoteAllocation,
		"download allocation": report.DownloadAllocation,
	} {
		if allocation.LogicalBytes != physicalReleaseBytes || allocation.PhysicalBytes < physicalReleaseBytes {
			return fmt.Errorf("%s is not a dense 100 GiB file: %#v", label, allocation)
		}
	}
	return nil
}

func runPhysicalReleaseRoundTrip(t *testing.T) {
	t.Helper()
	environment := physicalReleaseEnvironmentFromProcess(t)
	if err := validatePhysicalReleaseEnvironment(environment); err != nil {
		t.Fatal(err)
	}
	verifyPhysicalCandidate(t, environment)
	if err := os.Mkdir(environment.LabRoot, 0o700); err != nil {
		t.Fatalf("create exclusive release lab root: %v", err)
	}
	if err := os.Mkdir(environment.ControlRoot, 0o700); err != nil {
		_ = os.Remove(environment.LabRoot)
		t.Fatalf("create exclusive release control root: %v", err)
	}
	t.Cleanup(func() {
		if err := os.RemoveAll(environment.LabRoot); err != nil {
			t.Errorf("remove release lab root: %v", err)
		}
		if err := os.RemoveAll(environment.ControlRoot); err != nil {
			t.Errorf("remove release control root: %v", err)
		}
	})
	if err := requirePhysicalCapacity(environment.LabRoot, 220<<30); err != nil {
		t.Fatal(err)
	}

	startedAt := time.Now().UTC()
	sourceRoot := filepath.Join(environment.LabRoot, "source")
	remoteRoot := filepath.Join(environment.LabRoot, "remote")
	downloadRoot := filepath.Join(environment.LabRoot, "download")
	controlRoot := environment.ControlRoot
	for _, path := range []string{sourceRoot, remoteRoot, downloadRoot} {
		if err := os.Mkdir(path, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	sourcePath := filepath.Join(sourceRoot, "source-100gib.bin")
	allocateDenseFile(t, sourcePath, environment.Bytes)
	sourceAllocation, err := inspectPhysicalAllocation(sourcePath, environment.Bytes)
	if err != nil {
		t.Fatal(err)
	}

	current, err := user.Current()
	if err != nil {
		t.Fatal(err)
	}
	clientKey := filepath.Join(controlRoot, "client_key")
	runSSHKeygen(t, "-q", "-t", "ed25519", "-N", "", "-f", clientKey)
	publicKey, err := os.ReadFile(clientKey + ".pub") // #nosec G304 -- fixed release-lab path.
	if err != nil {
		t.Fatal(err)
	}
	server := startPhysicalSSHD(t, controlRoot, remoteRoot, current.Username, publicKey)
	alias := "amsftp-physical-release-lab"
	sshConfig := filepath.Join(controlRoot, "ssh_config")
	knownHosts := filepath.Join(controlRoot, "known_hosts")
	config := fmt.Sprintf("Host %s\n  HostName 127.0.0.1\n  Port %d\n  User %s\n  IdentityFile %s\n  IdentitiesOnly yes\n  BatchMode yes\n  StrictHostKeyChecking accept-new\n  UserKnownHostsFile %s\n  GlobalKnownHostsFile /dev/null\n", alias, server.port, current.Username, clientKey, knownHosts)
	if err := os.WriteFile(sshConfig, []byte(config), 0o600); err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 12*time.Hour)
	defer cancel()
	transport, err := openssh.Dial(ctx, openssh.Config{HostAlias: alias, ConfigFile: sshConfig, Fresh: true})
	if err != nil {
		t.Fatalf("dial physical release SFTP: %v", err)
	}
	remote, err := sftpprovider.New(sftpprovider.Config{
		Endpoint:  domain.Endpoint{ID: "ep_aaaaaaaaaaaaaaaaaaaaaaaaaa", Kind: domain.EndpointSSH, DisplayName: alias, SSHHostAlias: alias},
		SessionID: "sess_aaaaaaaaaaaaaaaaaaaaaaaaaa",
		Client:    transport.Client(),
		Close:     transport.Close,
		Root:      remoteRoot,
	})
	if err != nil {
		_ = transport.Close()
		t.Fatal(err)
	}
	defer remote.Close()

	uploadSource, err := localfs.New(localfs.Config{
		Endpoint:  domain.Endpoint{ID: "ep_cccccccccccccccccccccccccc", Kind: domain.EndpointLocal, DisplayName: "physical-source"},
		SessionID: "sess_cccccccccccccccccccccccccc",
		Root:      sourceRoot,
	})
	if err != nil {
		t.Fatal(err)
	}
	defer uploadSource.Close()
	uploadResolver := transfer.MapResolver{uploadSource.Descriptor().ID: uploadSource, remote.Descriptor().ID: remote}
	uploadPlanner := transfer.NewPlanner(uploadResolver)
	uploadReference, err := uploadPlanner.Capture(ctx, normalizeIntegration(t, ctx, uploadSource, "/source-100gib.bin"))
	if err != nil {
		t.Fatal(err)
	}
	uploadPlan, uploadCreate, err := uploadPlanner.FreezeCopy(ctx, integrationFreezeRequest(t, 'p', uploadReference, normalizeIntegration(t, ctx, remote, "/"), "remote-100gib.bin"))
	if err != nil {
		t.Fatal(err)
	}
	databasePath := filepath.Join(controlRoot, "state.sqlite3")
	store, database := openPhysicalTransferStore(t, ctx, databasePath, true)
	if _, _, err := store.Create(ctx, uploadCreate); err != nil {
		t.Fatalf("create durable upload job: %v", err)
	}
	journal := transfer.JobJournal{Store: store, StepIndex: 0}
	first, firstErr := transfer.NewWorker(uploadResolver, journal).Execute(ctx, uploadPlan, transfer.ControlFunc(func(checkpoint transfer.Checkpoint) transfer.ControlAction {
		if checkpoint.Offset >= physicalUint64(environment.CancelAfter) {
			return transfer.ControlCancel
		}
		return transfer.ControlContinue
	}))
	if !errors.Is(firstErr, transfer.ErrCanceled) || !first.PartRetained || first.Bytes < physicalUint64(environment.CancelAfter) {
		t.Fatalf("cancel checkpoint result = (%#v, %v)", first, firstErr)
	}
	if err := database.Close(); err != nil {
		t.Fatal(err)
	}

	restartedStore, restartedDatabase := openPhysicalTransferStore(t, ctx, databasePath, false)
	defer restartedDatabase.Close()
	restartedJournal := transfer.JobJournal{Store: restartedStore, StepIndex: 0}
	uploadResult, err := transfer.NewWorker(uploadResolver, restartedJournal).Execute(ctx, uploadPlan, nil)
	if err != nil || uploadResult.Outcome != transfer.OutcomeCompleted || uploadResult.Bytes != physicalUint64(environment.Bytes) {
		t.Fatalf("resumed local -> SFTP result = (%#v, %v)", uploadResult, err)
	}
	remotePath := filepath.Join(remoteRoot, "remote-100gib.bin")
	remoteAllocation, err := inspectPhysicalAllocation(remotePath, environment.Bytes)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(sourcePath); err != nil {
		t.Fatalf("remove release-lab source after verified upload: %v", err)
	}

	downloadDestination, err := localfs.New(localfs.Config{
		Endpoint:  domain.Endpoint{ID: "ep_dddddddddddddddddddddddddd", Kind: domain.EndpointLocal, DisplayName: "physical-download"},
		SessionID: "sess_dddddddddddddddddddddddddd",
		Root:      downloadRoot,
	})
	if err != nil {
		t.Fatal(err)
	}
	defer downloadDestination.Close()
	downloadResolver := transfer.MapResolver{remote.Descriptor().ID: remote, downloadDestination.Descriptor().ID: downloadDestination}
	downloadPlanner := transfer.NewPlanner(downloadResolver)
	downloadReference, err := downloadPlanner.Capture(ctx, normalizeIntegration(t, ctx, remote, "/remote-100gib.bin"))
	if err != nil {
		t.Fatal(err)
	}
	downloadPlan, downloadCreate, err := downloadPlanner.FreezeCopy(ctx, integrationFreezeRequest(t, 'q', downloadReference, normalizeIntegration(t, ctx, downloadDestination, "/"), "downloaded-100gib.bin"))
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := restartedStore.Create(ctx, downloadCreate); err != nil {
		t.Fatalf("create durable download job: %v", err)
	}
	downloadResult, err := transfer.NewWorker(downloadResolver, transfer.JobJournal{Store: restartedStore, StepIndex: 0}).Execute(ctx, downloadPlan, nil)
	if err != nil || downloadResult.Outcome != transfer.OutcomeCompleted || downloadResult.Bytes != physicalUint64(environment.Bytes) {
		t.Fatalf("SFTP -> local result = (%#v, %v)", downloadResult, err)
	}
	downloadAllocation, err := inspectPhysicalAllocation(filepath.Join(downloadRoot, "downloaded-100gib.bin"), environment.Bytes)
	if err != nil {
		t.Fatal(err)
	}

	report := physicalReleaseReport{
		Schema:             physicalReleaseReportSchema,
		Purpose:            physicalReleasePurposePreRC,
		CandidateCommit:    environment.CandidateCommit,
		CandidateTree:      environment.CandidateTree,
		StartedAt:          startedAt,
		CompletedAt:        time.Now().UTC(),
		Filesystem:         filesystemName(environment.LabRoot),
		BytesPerDirection:  physicalUint64(environment.Bytes),
		TotalBytes:         physicalUint64(environment.Bytes) * 2,
		ResumeOffset:       first.Bytes,
		UploadSHA256:       uploadResult.SHA256,
		DownloadSHA256:     downloadResult.SHA256,
		SourceAllocation:   sourceAllocation,
		RemoteAllocation:   remoteAllocation,
		DownloadAllocation: downloadAllocation,
	}
	if err := validatePhysicalReleaseReport(report); err != nil {
		t.Fatal(err)
	}
	if err := writePhysicalReleaseReport(environment.EvidencePath, report); err != nil {
		t.Fatal(err)
	}
	t.Logf("pre-RC physical 100 GiB harness proof written to %s", environment.EvidencePath)
}

func physicalReleaseEnvironmentFromProcess(t *testing.T) physicalReleaseEnvironment {
	t.Helper()
	repository := gitOutput(t, "rev-parse", "--show-toplevel")
	return physicalReleaseEnvironment{
		RepoRoot:        repository,
		LabRoot:         os.Getenv("AMSFTP_RELEASE_LAB_ROOT"),
		ControlRoot:     os.Getenv("AMSFTP_RELEASE_CONTROL_ROOT"),
		EvidencePath:    os.Getenv("AMSFTP_RELEASE_EVIDENCE_PATH"),
		CandidateCommit: os.Getenv("AMSFTP_RELEASE_CANDIDATE_COMMIT"),
		CandidateTree:   os.Getenv("AMSFTP_RELEASE_CANDIDATE_TREE"),
		Bytes:           physicalReleaseBytes,
		CancelAfter:     1 << 30,
	}
}

func verifyPhysicalCandidate(t *testing.T, environment physicalReleaseEnvironment) {
	t.Helper()
	if got := gitOutput(t, "rev-parse", "HEAD"); got != environment.CandidateCommit {
		t.Fatalf("candidate commit = %s, checkout = %s", environment.CandidateCommit, got)
	}
	if got := gitOutput(t, "rev-parse", "HEAD^{tree}"); got != environment.CandidateTree {
		t.Fatalf("candidate tree = %s, checkout = %s", environment.CandidateTree, got)
	}
	if status := gitOutput(t, "status", "--porcelain=v1", "--untracked-files=all"); status != "" {
		t.Fatalf("physical release checkout must be clean:\n%s", status)
	}
}

func gitOutput(t *testing.T, arguments ...string) string {
	t.Helper()
	command := exec.Command("/usr/bin/git", arguments...) // #nosec G204 -- fixed git path and fixed caller arguments.
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("git %s: %v: %s", strings.Join(arguments, " "), err, output)
	}
	return strings.TrimSpace(string(output))
}

func requirePhysicalCapacity(path string, minimum uint64) error {
	var stat unix.Statfs_t
	if err := unix.Statfs(path, &stat); err != nil {
		return err
	}
	if stat.Bsize <= 0 {
		return fmt.Errorf("release lab filesystem block size = %d", stat.Bsize)
	}
	available := stat.Bavail * physicalUint64(stat.Bsize)
	if available < minimum {
		return fmt.Errorf("release lab has %d available bytes, want at least %d", available, minimum)
	}
	return nil
}

func physicalUint64(value int64) uint64 {
	if value < 0 {
		panic("physical release byte count must be non-negative")
	}
	return uint64(value) // #nosec G115 -- the explicit non-negative check makes this conversion exact.
}

func allocateDenseFile(t *testing.T, path string, size int64) {
	t.Helper()
	file, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600) // #nosec G304 -- validated release-lab path.
	if err != nil {
		t.Fatal(err)
	}
	if err := unix.Fallocate(int(file.Fd()), 0, 0, size); err != nil {
		_ = file.Close()
		t.Fatalf("physically allocate %d bytes: %v", size, err)
	}
	if err := file.Sync(); err != nil {
		_ = file.Close()
		t.Fatal(err)
	}
	if err := file.Close(); err != nil {
		t.Fatal(err)
	}
}

func startPhysicalSSHD(t *testing.T, controlRoot, dataRoot, username string, publicKey []byte) *testSSHD {
	t.Helper()
	server := &testSSHD{root: dataRoot}
	hostKey := filepath.Join(controlRoot, "host_key")
	runSSHKeygen(t, "-q", "-t", "ed25519", "-N", "", "-f", hostKey)
	authorized := filepath.Join(controlRoot, "authorized_keys")
	// #nosec G703 -- authorized is fixed inside the validated owner-private control root.
	if err := os.WriteFile(authorized, publicKey, 0o600); err != nil {
		t.Fatal(err)
	}
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	server.port = listener.Addr().(*net.TCPAddr).Port
	_ = listener.Close()
	serverConfig := filepath.Join(controlRoot, "sshd_config")
	pidFile := filepath.Join(controlRoot, "sshd.pid")
	config := fmt.Sprintf("Port %d\nListenAddress 127.0.0.1\nHostKey %s\nPidFile %s\nAuthorizedKeysFile %s\nPasswordAuthentication no\nKbdInteractiveAuthentication no\nUsePAM no\nStrictModes no\nPermitRootLogin no\nSubsystem sftp internal-sftp\nAllowUsers %s\n", server.port, hostKey, pidFile, authorized, username)
	if err := os.WriteFile(serverConfig, []byte(config), 0o600); err != nil {
		t.Fatal(err)
	}
	server.command = exec.Command("/usr/sbin/sshd", "-D", "-e", "-f", serverConfig) // #nosec G204 -- fixed sshd and test-owned config.
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
			t.Fatalf("physical release sshd not ready: %v\n%s", dialErr, server.logs.String())
		}
		time.Sleep(25 * time.Millisecond)
	}
}

func openPhysicalTransferStore(t *testing.T, ctx context.Context, databasePath string, initialize bool) (*jobstore.Store, *sql.DB) {
	t.Helper()
	uri := &url.URL{Scheme: "file", Path: databasePath, RawQuery: "_pragma=" + url.QueryEscape("wal_autocheckpoint(1000)")}
	database, err := sql.Open("sqlite", uri.String())
	if err != nil {
		t.Fatal(err)
	}
	database.SetMaxOpenConns(4)
	connection, err := database.Conn(ctx)
	if err != nil {
		_ = database.Close()
		t.Fatal(err)
	}
	if initialize {
		if err := (migration.Runner{}).Apply(ctx, connection, migration.Version1(), "2026-07-19T00:00:00Z"); err != nil {
			_ = connection.Close()
			_ = database.Close()
			t.Fatal(err)
		}
		if err := os.Chmod(databasePath, 0o600); err != nil {
			_ = connection.Close()
			_ = database.Close()
			t.Fatal(err)
		}
	}
	var mode string
	if err := connection.QueryRowContext(ctx, "PRAGMA journal_mode=WAL").Scan(&mode); err != nil || mode != "wal" {
		_ = connection.Close()
		_ = database.Close()
		t.Fatalf("enable release-lab WAL: mode=%q error=%v", mode, err)
	}
	if err := connection.Close(); err != nil {
		_ = database.Close()
		t.Fatal(err)
	}
	store, err := jobstore.New(ctx, database)
	if err != nil {
		_ = database.Close()
		t.Fatal(err)
	}
	return store, database
}

func filesystemName(path string) string {
	var stat unix.Statfs_t
	if err := unix.Statfs(path, &stat); err != nil {
		return "unknown"
	}
	switch stat.Type {
	case 0xef53:
		return "ext4"
	case 0x58465342:
		return "xfs"
	case 0x9123683e:
		return "btrfs"
	default:
		return fmt.Sprintf("linux-magic-0x%x", stat.Type)
	}
}

func writePhysicalReleaseReport(path string, report physicalReleaseReport) error {
	parent := filepath.Dir(path)
	if err := os.MkdirAll(parent, 0o700); err != nil {
		return err
	}
	resolved, err := filepath.EvalSymlinks(parent)
	if err != nil {
		return err
	}
	if resolved != parent {
		return fmt.Errorf("evidence parent must not traverse symlinks: %s", parent)
	}
	file, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600) // #nosec G304 -- explicit validated evidence path.
	if err != nil {
		return err
	}
	encoder := json.NewEncoder(file)
	encoder.SetIndent("", "  ")
	if err := encoder.Encode(report); err != nil {
		_ = file.Close()
		return err
	}
	if err := file.Sync(); err != nil {
		_ = file.Close()
		return err
	}
	if err := file.Close(); err != nil {
		return err
	}
	directory, err := os.Open(parent) // #nosec G304 -- explicit validated evidence parent.
	if err != nil {
		return err
	}
	defer directory.Close()
	return directory.Sync()
}
