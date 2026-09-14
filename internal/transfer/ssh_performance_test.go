//go:build linux || darwin

package transfer

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"os"
	"os/exec"
	"os/user"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/TyrantLucifer/awesome-sftp-cli/internal/domain"
	"github.com/TyrantLucifer/awesome-sftp-cli/internal/foundation"
	"github.com/TyrantLucifer/awesome-sftp-cli/internal/platform"
	providerapi "github.com/TyrantLucifer/awesome-sftp-cli/internal/provider"
	sftpprovider "github.com/TyrantLucifer/awesome-sftp-cli/internal/provider/sftp"
	"github.com/TyrantLucifer/awesome-sftp-cli/internal/testkit"
	"github.com/TyrantLucifer/awesome-sftp-cli/internal/transport/openssh"
)

// This opt-in benchmark uses real system ssh/sshd, TCP, encryption, SFTP,
// SQLite checkpoints and the production Worker. All keys/configs/data belong
// to temporary directories; it never edits the user's SSH configuration.
func TestRealSSHTransferPerformance(t *testing.T) {
	if os.Getenv("AMSFTP_REAL_SSH_PERFORMANCE") != "1" {
		t.Skip("set AMSFTP_REAL_SSH_PERFORMANCE=1 for real SSH throughput comparison")
	}
	config := performanceSSHD(t)
	sshBinary, sftpBinary := "/usr/bin/ssh", "/usr/bin/sftp"
	if dir := os.Getenv("AMSFTP_PERFORMANCE_OPENSSH_DIR"); dir != "" {
		sshBinary = filepath.Join(dir, "ssh")
		sftpBinary = filepath.Join(dir, "sftp")
	}
	for _, binary := range []string{sshBinary, sftpBinary} {
		if _, err := platform.ExecutableIdentity(binary); err != nil {
			t.Fatal(err)
		}
	}
	const size = 256 << 20
	for _, direction := range []string{"upload", "download"} {
		for _, mode := range []string{"default", "completion", "sha256"} {
			for repeat := range 3 {
				t.Run(fmt.Sprintf("%s/%s/%d", direction, mode, repeat), func(t *testing.T) {
					f := newWorkerFixture(t, nil, ConflictAsk)
					remote := t.TempDir()
					sourcePath := filepath.Join(f.sourceRoot, "source")
					if direction == "download" {
						sourcePath = filepath.Join(remote, "source")
					}
					expected := writePerformanceData(t, sourcePath, size)
					nativeFinal := filepath.Join(remote, "native")
					if direction == "download" {
						nativeFinal = filepath.Join(f.destinationRoot, "native")
					}
					verb := "put"
					if direction == "download" {
						verb = "get"
					}
					if mode == "completion" {
						verb += " -f"
					}
					batch := filepath.Join(t.TempDir(), "batch")
					if err := os.WriteFile(batch, []byte(fmt.Sprintf("%s %q %q\n", verb, sourcePath, nativeFinal)), 0600); err != nil {
						t.Fatal(err)
					}
					runNative := func() time.Duration {
						start := time.Now()
						// #nosec G204 G702 -- executable paths pass platform.ExecutableIdentity above; configuration and batch are private test files.
						cmd := exec.CommandContext(t.Context(), sftpBinary, "-S", sshBinary, "-q", "-F", config, "-B", "32768", "-R", "64", "-b", batch, "amsftp-performance")
						if out, err := cmd.CombinedOutput(); err != nil {
							t.Fatalf("native: %v: %s", err, out)
						}
						return time.Since(start)
					}
					store, db := openTransferStore(t, t.Context(), filepath.Join(testkit.PersistentTempDir(t), "state.sqlite3"), true)
					defer db.Close()
					var nativeElapsed time.Duration
					if repeat%2 == 0 {
						nativeElapsed = runNative()
					}
					start := time.Now()
					session, err := openssh.Dial(t.Context(), openssh.Config{HostAlias: "amsftp-performance", ConfigFile: config, Binary: sshBinary})
					if err != nil {
						t.Fatal(err)
					}
					defer session.Close()
					p, err := sftpprovider.New(sftpprovider.Config{Endpoint: domain.Endpoint{ID: "ep_cccccccccccccccccccccccccc", Kind: domain.EndpointSSH, SSHHostAlias: "amsftp-performance", DisplayName: "performance"}, SessionID: "sess_aaaaaaaaaaaaaaaaaaaaaaaaaa", Client: session.Client(), Root: remote})
					if err != nil {
						t.Fatal(err)
					}
					f.resolver[p.Descriptor().ID] = p
					source, destination := f.source, providerapi.Provider(p)
					if direction == "download" {
						source, destination = p, f.destination
					}
					planner := NewPlanner(f.resolver)
					reference, err := planner.Capture(t.Context(), normalizePlanTest(t, source, "/source"))
					if err != nil {
						t.Fatal(err)
					}
					req := validFreezeRequest(reference, normalizePlanTest(t, destination, "/"))
					// JSON also lets this same harness run unchanged on the previous release.
					if mode == "completion" {
						if err := json.Unmarshal([]byte(`{"durability":"completion"}`), &req.Intent); err != nil {
							t.Fatal(err)
						}
					}
					if mode == "sha256" {
						if err := json.Unmarshal([]byte(`{"verification":"stream_sha256"}`), &req.Intent); err != nil {
							t.Fatal(err)
						}
					}
					plan, create, err := planner.FreezeCopy(t.Context(), req)
					if err != nil {
						t.Fatal(err)
					}
					if _, _, err := store.Create(t.Context(), create); err != nil {
						t.Fatal(err)
					}
					journal := &nativePerformanceJournal{Journal: JobJournal{Store: store}}
					worker := NewWorker(f.resolver, journal)
					worker.scheduler, err = NewTransferScheduler(foundation.RealClock{}, SchedulerPolicy{})
					if err != nil {
						t.Fatal(err)
					}
					transferStart := time.Now()
					result, err := worker.Execute(t.Context(), plan, nil)
					elapsed := time.Since(transferStart)
					endToEnd := time.Since(start)
					if err != nil || result.Outcome != OutcomeCompleted || result.Bytes != size {
						t.Fatalf("worker: outcome=%s bytes=%d: %v", result.Outcome, result.Bytes, err)
					}
					if err := session.Close(); err != nil {
						t.Fatal(err)
					}
					if repeat%2 != 0 {
						nativeElapsed = runNative()
					}
					final := filepath.Join(remote, filepath.Base(string(result.Final.Path)))
					if direction == "download" {
						final = filepath.Join(f.destinationRoot, filepath.Base(string(result.Final.Path)))
					}
					checkPerformanceHash(t, final, expected)
					checkPerformanceHash(t, nativeFinal, expected)
					row := map[string]any{"direction": direction, "mode": mode, "repeat": repeat, "bytes": size, "native_seconds": nativeElapsed.Seconds(), "worker_seconds": elapsed.Seconds(), "worker_end_to_end_seconds": endToEnd.Seconds(), "throughput_ratio": float64(nativeElapsed) / float64(endToEnd), "performance": journal.last.Performance}
					encoded, err := json.Marshal(row)
					if err != nil {
						t.Fatal(err)
					}
					t.Log(string(encoded))
					if out := os.Getenv("AMSFTP_PERFORMANCE_OUT"); out != "" {
						// #nosec G703 -- opt-in benchmark output directory supplied by the caller.
						if err := os.MkdirAll(out, 0700); err != nil {
							t.Fatal(err)
						}
						// #nosec G703 -- fixed artifact filename under the explicitly selected benchmark directory.
						if err := os.WriteFile(filepath.Join(out, fmt.Sprintf("ssh-%s-%s-%d.json", direction, mode, repeat)), encoded, 0600); err != nil {
							t.Fatal(err)
						}
					}
				})
			}
		}
	}
}

func writePerformanceData(t *testing.T, path string, size int64) [32]byte {
	t.Helper()
	// #nosec G304 -- path belongs to this test's temporary source directory.
	file, err := os.OpenFile(path, os.O_WRONLY|os.O_TRUNC|os.O_CREATE, 0600)
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	block := make([]byte, 1<<20)
	for i := range block {
		block[i] = byte((i*37 + i/251) % 256)
	}
	digest := sha256.New()
	for remaining := size; remaining > 0; {
		n := min(int64(len(block)), remaining)
		if _, err := file.Write(block[:n]); err != nil {
			t.Fatal(err)
		}
		_, _ = digest.Write(block[:n])
		remaining -= n
	}
	if err := file.Close(); err != nil {
		t.Fatal(err)
	}
	var result [32]byte
	copy(result[:], digest.Sum(nil))
	return result
}
func checkPerformanceHash(t *testing.T, path string, expected [32]byte) {
	t.Helper()
	// #nosec G304 -- path is a transfer output inside this test's temporary root.
	file, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	digest := sha256.New()
	if _, err := io.Copy(digest, file); err != nil {
		t.Fatal(err)
	}
	if fmt.Sprintf("%x", digest.Sum(nil)) != fmt.Sprintf("%x", expected) {
		t.Fatal("output checksum mismatch")
	}
}
func performanceSSHD(t *testing.T) string {
	t.Helper()
	root := testkit.PersistentTempDir(t)
	current, err := user.Current()
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"host", "client"} {
		// #nosec G204 -- fixed system key generator creates only ephemeral test keys.
		if out, err := exec.CommandContext(t.Context(), "/usr/bin/ssh-keygen", "-q", "-t", "ed25519", "-N", "", "-f", filepath.Join(root, name)).CombinedOutput(); err != nil {
			t.Fatalf("keygen: %v: %s", err, out)
		}
	}
	// #nosec G304 -- ephemeral public key in the private test root.
	clientKey, err := os.ReadFile(filepath.Join(root, "client.pub"))
	if err != nil {
		t.Fatal(err)
	} //nolint:gosec // ephemeral public fixture key.
	// #nosec G703 -- the authorized key file is fixed inside the private test root.
	if err := os.WriteFile(filepath.Join(root, "authorized_keys"), clientKey, 0600); err != nil {
		t.Fatal(err)
	}
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	port := listener.Addr().(*net.TCPAddr).Port
	_ = listener.Close()
	serverConfig := fmt.Sprintf("Port %d\nListenAddress 127.0.0.1\nHostKey %s/host\nPidFile %s/sshd.pid\nAuthorizedKeysFile %s/authorized_keys\nPasswordAuthentication no\nKbdInteractiveAuthentication no\nUsePAM no\nStrictModes yes\nPermitRootLogin no\nSubsystem sftp internal-sftp\nAllowUsers %s\n", port, root, root, root, current.Username)
	serverPath := filepath.Join(root, "sshd_config")
	if err := os.WriteFile(serverPath, []byte(serverConfig), 0600); err != nil {
		t.Fatal(err)
	}
	var logs testkit.ConcurrentBuffer
	// #nosec G204 -- fixed system daemon with a private test-only loopback configuration.
	cmd := exec.CommandContext(t.Context(), "/usr/sbin/sshd", "-D", "-e", "-f", serverPath)
	cmd.Stdout = &logs
	cmd.Stderr = &logs
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = cmd.Process.Kill(); _ = cmd.Wait() })
	deadline := time.Now().Add(5 * time.Second)
	for {
		c, err := net.DialTimeout("tcp", fmt.Sprintf("127.0.0.1:%d", port), 100*time.Millisecond)
		if err == nil {
			_ = c.Close()
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("sshd: %s", logs.String())
		}
		time.Sleep(25 * time.Millisecond)
	}
	// #nosec G304 -- generated public host key in the private test root.
	hostKey, err := os.ReadFile(filepath.Join(root, "host.pub"))
	if err != nil {
		t.Fatal(err)
	} //nolint:gosec // ephemeral public host key.
	known := fmt.Sprintf("[127.0.0.1]:%d %s\n", port, strings.TrimSpace(string(hostKey)))
	// #nosec G703 -- fixed host-key trust file in the private test root.
	if err := os.WriteFile(filepath.Join(root, "known_hosts"), []byte(known), 0600); err != nil {
		t.Fatal(err)
	}
	config := fmt.Sprintf("Host amsftp-performance\n HostName 127.0.0.1\n Port %d\n User %s\n IdentityFile %s/client\n IdentitiesOnly yes\n BatchMode yes\n StrictHostKeyChecking yes\n UserKnownHostsFile %s/known_hosts\n GlobalKnownHostsFile /dev/null\n Compression no\n ControlMaster no\n ControlPath none\n", port, current.Username, root, root)
	path := filepath.Join(root, "config")
	if err := os.WriteFile(path, []byte(config), 0600); err != nil {
		t.Fatal(err)
	}
	return path
}
