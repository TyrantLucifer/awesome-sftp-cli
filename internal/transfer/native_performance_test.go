package transfer

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/TyrantLucifer/awesome-sftp-cli/internal/domain"
	"github.com/TyrantLucifer/awesome-sftp-cli/internal/foundation"
	providerapi "github.com/TyrantLucifer/awesome-sftp-cli/internal/provider"
	sftpprovider "github.com/TyrantLucifer/awesome-sftp-cli/internal/provider/sftp"
	"github.com/TyrantLucifer/awesome-sftp-cli/internal/testkit"
	pkgsftp "github.com/pkg/sftp"
)

type nativePerformanceJournal struct {
	Journal
	start, timeTransferred, timeVerified time.Time
	last                                 Checkpoint
}

func (j *nativePerformanceJournal) Save(ctx context.Context, c Checkpoint) error {
	err := j.Journal.Save(ctx, c)
	if c.Phase == PhaseTransferred {
		j.timeTransferred = time.Now()
	}
	if c.Phase == PhaseVerified {
		j.timeVerified = time.Now()
	}
	j.last = cloneCheckpoint(c)
	return err
}
func nativePerformanceProvider(t *testing.T, rtt int, root, stats string, peer ...domain.EndpointID) (*sftpprovider.Provider, func()) {
	t.Helper()
	cmd := exec.CommandContext(t.Context(), "/usr/bin/python3", nativePerformanceProxy(t), fmt.Sprint(rtt), stats) //nolint:gosec // controlled latency and stats path passed to the repository-owned test proxy.
	in, e := cmd.StdinPipe()
	if e != nil {
		t.Fatal(e)
	}
	out, e := cmd.StdoutPipe()
	if e != nil {
		t.Fatal(e)
	}
	cmd.Stderr = os.Stderr
	if e = cmd.Start(); e != nil {
		t.Fatal(e)
	}
	var client *pkgsftp.Client
	var once sync.Once
	cleanup := func() {
		once.Do(func() {
			if client != nil {
				_ = client.Close()
			} else {
				_ = in.Close()
				_ = out.Close()
				_ = cmd.Process.Kill()
			}
			if err := cmd.Wait(); err != nil {
				t.Errorf("native proxy exit: %v", err)
			}
		})
	}
	t.Cleanup(cleanup)
	client, e = pkgsftp.NewClientPipe(out, in, pkgsftp.MaxConcurrentRequestsPerFile(64), pkgsftp.UseConcurrentWrites(true))
	if e != nil {
		t.Fatal(e)
	}
	endpointID := domain.EndpointID("ep_cccccccccccccccccccccccccc")
	if len(peer) > 0 {
		endpointID = peer[0]
	}
	p, e := sftpprovider.New(sftpprovider.Config{
		Endpoint:  domain.Endpoint{ID: endpointID, Kind: domain.EndpointSSH, SSHHostAlias: "performance-local", DisplayName: "performance-local"},
		SessionID: "ss_aaaaaaaaaaaaaaaaaaaaaaaaaa", Client: client, Root: root,
	})
	if e != nil {
		t.Fatal(e)
	}
	return p, cleanup
}
func nativePerformanceCopy(t *testing.T, rtt int, verb, src, dst, stats string) time.Duration {
	t.Helper()
	batch := filepath.Join(t.TempDir(), "batch")
	if e := os.WriteFile(batch, []byte(fmt.Sprintf("%s %q %q\n", verb, src, dst)), 0600); e != nil {
		t.Fatal(e)
	}
	// #nosec G204 -- fixed executable and repository-owned proxy; batch paths are test fixtures.
	cmd := exec.CommandContext(t.Context(), "/usr/bin/sftp", "-q", "-B", "32768", "-R", "64", "-D",
		nativePerformanceProxy(t), "-b", batch) //nolint:gosec // fixed native executable and repository-owned test proxy; paths are test fixtures.
	cmd.Env = append(os.Environ(), fmt.Sprintf("AMSFTP_PERFORMANCE_RTT=%d", rtt), "AMSFTP_PERFORMANCE_STATS="+stats)
	start := time.Now()
	if out, e := cmd.CombinedOutput(); e != nil {
		t.Fatalf("native: %v %s", e, out)
	}
	return time.Since(start)
}

// TestNativeSFTPPerformance is an opt-in protocol-latency comparison against
// the installed native OpenSSH client. It deliberately excludes SSH encryption
// and TCP; the normal native integration suite covers the actual SSH boundary.
func TestNativeSFTPPerformance(t *testing.T) {
	if os.Getenv("AMSFTP_NATIVE_PERFORMANCE") != "1" {
		t.Skip("set AMSFTP_NATIVE_PERFORMANCE=1 to run the native comparison")
	}
	for _, tool := range []string{"/usr/bin/python3", "/usr/bin/sftp", "/usr/lib/openssh/sftp-server"} {
		if _, err := os.Stat(tool); err != nil {
			t.Fatalf("native comparison requires %s: %v", tool, err)
		}
	}
	ctx := context.Background()
	outdir := os.Getenv("AMSFTP_PERFORMANCE_OUT")
	if outdir == "" {
		outdir = t.TempDir()
	} else if err := os.MkdirAll(outdir, 0700); err != nil { //nolint:gosec // explicit opt-in benchmark artifact directory selected by the caller.
		t.Fatal(err)
	}
	data := make([]byte, 8<<20)
	for i := range data {
		data[i] = byte((i*37 + i/251) % 256)
	}
	expected := sha256.Sum256(data)
	unlimited := make(map[string]time.Duration)
	for _, rtt := range []int{0, 20, 80, 200} {
		for repeat := 0; repeat < 2; repeat++ {
			for _, direction := range []string{"upload", "download"} {
				for _, limited := range []bool{false, true} {
					t.Run(fmt.Sprintf("%s/rtt%d/repeat%d/limited%t", direction, rtt, repeat, limited), func(t *testing.T) {
						f := newWorkerFixture(t, data, ConflictAsk)
						remote := t.TempDir()
						prefix := fmt.Sprintf("%s-rtt%d-repeat%d", direction, rtt, repeat)
						if limited {
							prefix = "limited-" + prefix
						}
						nativeStats := filepath.Join(outdir, prefix+"-native.json")
						nativeDst := filepath.Join(remote, "native")
						var nativeElapsed time.Duration
						if direction == "upload" {
							nativeElapsed = nativePerformanceCopy(t, rtt, "put", filepath.Join(f.sourceRoot, "source"), nativeDst, nativeStats)
						} else {
							if e := os.WriteFile(filepath.Join(remote, "source"), data, 0600); e != nil {
								t.Fatal(e)
							}
							nativeDst = filepath.Join(t.TempDir(), "native")
							nativeElapsed = nativePerformanceCopy(t, rtt, "get", filepath.Join(remote, "source"), nativeDst, nativeStats)
						}
						got, e := os.ReadFile(nativeDst) //nolint:gosec // nativeDst is a test-owned output file.
						if e != nil || sha256.Sum256(got) != expected {
							t.Fatal("native content mismatch", e)
						}
						nativeVerifiedElapsed := nativeElapsed
						if direction == "upload" {
							nativeVerifiedElapsed = nativePerformanceCopy(t, rtt, "put -f", filepath.Join(f.sourceRoot, "source"), nativeDst, filepath.Join(outdir, prefix+"-native-fsync.json"))
							for pass := 0; pass < 2; pass++ {
								readback := filepath.Join(t.TempDir(), "readback")
								nativeVerifiedElapsed += nativePerformanceCopy(t, rtt, "get", nativeDst, readback, filepath.Join(outdir, fmt.Sprintf("%s-native-readback%d.json", prefix, pass)))
								hashStart := time.Now()
								readbackBytes, err := os.ReadFile(readback) //nolint:gosec // file created in this test's temporary directory.
								if err != nil || sha256.Sum256(readbackBytes) != expected {
									t.Fatal("native readback mismatch", err)
								}
								nativeVerifiedElapsed += time.Since(hashStart)
							}
						} else {
							nativeVerifiedElapsed = nativePerformanceCopy(t, rtt, "get -f", filepath.Join(remote, "source"), nativeDst, filepath.Join(outdir, prefix+"-native-fsync.json"))
							hashStart := time.Now()
							for pass := 0; pass < 2; pass++ {
								contents, err := os.ReadFile(nativeDst) //nolint:gosec // output below this test temporary root.
								if err != nil || sha256.Sum256(contents) != expected {
									t.Fatal("native local readback mismatch", err)
								}
							} //nolint:gosec // nativeDst is inside this test's temporary directory.
							nativeVerifiedElapsed += time.Since(hashStart)
						}
						workerStats := filepath.Join(outdir, prefix+"-worker.json")
						p, closeP := nativePerformanceProvider(t, rtt, remote, workerStats)
						f.resolver[p.Descriptor().ID] = p
						source := f.source
						dest := providerapi.Provider(p)
						if direction == "download" {
							source = p
							dest = f.destination
						}
						planner := NewPlanner(f.resolver)
						reference, e := planner.Capture(ctx, normalizePlanTest(t, source, "/source"))
						if e != nil {
							t.Fatal(e)
						}
						request := validFreezeRequest(reference, normalizePlanTest(t, dest, "/"))
						if limited {
							request.Intent.Bandwidth.JobBytesPerSecond = 1 << 30
						}
						plan, create, e := planner.FreezeCopy(ctx, request)
						if e != nil {
							t.Fatal(e)
						}
						dbroot := testkit.PersistentTempDir(t)
						store, db := openTransferStore(t, ctx, filepath.Join(dbroot, "state.sqlite3"), true)
						defer db.Close()
						if _, _, e := store.Create(ctx, create); e != nil {
							t.Fatal(e)
						}
						journal := &nativePerformanceJournal{Journal: JobJournal{Store: store, StepIndex: 0}}
						worker := NewWorker(f.resolver, journal)
						scheduler, e := NewTransferScheduler(foundation.RealClock{}, SchedulerPolicy{})
						if e != nil {
							t.Fatal(e)
						}
						worker.scheduler = scheduler
						journal.start = time.Now()
						result, e := worker.Execute(ctx, plan, nil)
						elapsed := time.Since(journal.start)
						if e != nil {
							t.Fatal(e)
						}
						if result.Outcome != OutcomeCompleted || result.Bytes != uint64(len(data)) || result.SHA256 != fmt.Sprintf("%x", expected) {
							t.Fatal("worker result mismatch", result)
						}
						closeP()
						workerDest := filepath.Join(remote, filepath.Base(string(result.Final.Path)))
						if direction == "download" {
							workerDest = filepath.Join(f.destinationRoot, filepath.Base(string(result.Final.Path)))
						}
						got, e = os.ReadFile(workerDest) //nolint:gosec // workerDest is below the test destination root.
						if e != nil || sha256.Sum256(got) != expected {
							t.Fatal("worker content mismatch", e)
						}
						comparisonKey := fmt.Sprintf("%s/%d/%d", direction, rtt, repeat)
						if !limited {
							unlimited[comparisonKey] = elapsed
						} else if prior, ok := unlimited[comparisonKey]; ok && rtt >= 20 && elapsed > prior+prior/2+time.Second {
							t.Errorf("high rate limit regressed completion: unlimited=%s limited=%s", prior, elapsed)
						}
						var protocol struct {
							Read  uint64 `json:"read_payload_bytes"`
							Write uint64 `json:"write_payload_bytes"`
						}
						encoded, err := os.ReadFile(workerStats) //nolint:gosec // proxy output in the requested test artifact directory.
						if err != nil {
							t.Fatal(err)
						}
						if err = json.Unmarshal(encoded, &protocol); err != nil {
							t.Fatal(err)
						}
						wantRead, wantWrite := uint64(len(data)), uint64(0)
						if direction == "upload" {
							wantRead *= 2
							wantWrite = uint64(len(data))
						}
						if protocol.Read != wantRead || protocol.Write != wantWrite {
							t.Errorf("protocol bytes=%+v, want read=%d write=%d", protocol, wantRead, wantWrite)
						}
						if journal.last.Performance == nil || journal.last.Performance.Chunks > 4 {
							t.Errorf("checkpoint amplification: %+v", journal.last.Performance)
						}
						row := map[string]any{"direction": direction, "rtt_ms": rtt, "repeat": repeat, "bytes": len(data), "limited": limited,
							"native_seconds": nativeElapsed.Seconds(), "native_fsync_readback_seconds": nativeVerifiedElapsed.Seconds(), "worker_seconds": elapsed.Seconds(),
							"stream_seconds":      journal.timeTransferred.Sub(journal.start).Seconds(),
							"verify_part_seconds": journal.timeVerified.Sub(journal.timeTransferred).Seconds(),
							"commit_seconds":      journal.start.Add(elapsed).Sub(journal.timeVerified).Seconds(),
							"performance":         journal.last.Performance}
						b, _ := json.Marshal(row)
						t.Log(string(b))
						if e := os.WriteFile(filepath.Join(outdir, prefix+"-timing.json"), b, 0600); e != nil { //nolint:gosec // fixed benchmark filename in the caller-selected artifact directory.
							t.Fatal(e)
						}
					})
				}
			}
		}
	}
}

func TestNativeRelayAndDirectoryPerformance(t *testing.T) {
	if os.Getenv("AMSFTP_NATIVE_PERFORMANCE") != "1" {
		t.Skip("set AMSFTP_NATIVE_PERFORMANCE=1 to run the native comparison")
	}
	outdir := os.Getenv("AMSFTP_PERFORMANCE_OUT")
	if outdir == "" {
		outdir = t.TempDir()
	} else if err := os.MkdirAll(outdir, 0700); err != nil { //nolint:gosec // explicit opt-in benchmark artifact directory selected by the caller.
		t.Fatal(err)
	}
	for _, rtt := range []int{0, 20, 80, 200} {
		for _, direction := range []string{"relay", "directory_upload"} {
			for _, limited := range []bool{false, true} {
				t.Run(fmt.Sprintf("%s/rtt%d/limited%t", direction, rtt, limited), func(t *testing.T) {
					data := bytes.Repeat([]byte("pipeline"), 1<<20)
					if direction == "directory_upload" {
						data = data[:4096]
					}
					fixture := newWorkerFixture(t, data, ConflictAsk)
					remote := t.TempDir()
					prefix := fmt.Sprintf("%s-rtt%d", direction, rtt)
					if limited {
						prefix = "limited-" + prefix
					}
					target, closeTarget := nativePerformanceProvider(t, rtt, remote, filepath.Join(outdir, prefix+"-worker-target.json"))
					fixture.resolver[target.Descriptor().ID] = target
					source := fixture.source
					sourceName := "/source"
					nativeDestination := filepath.Join(remote, "native")
					var nativeElapsed time.Duration
					names := []string{"source"}
					if direction == "relay" {
						remoteSource := t.TempDir()
						if err := os.WriteFile(filepath.Join(remoteSource, "source"), data, 0600); err != nil {
							t.Fatal(err)
						}
						peer, closePeer := nativePerformanceProvider(t, rtt, remoteSource, filepath.Join(outdir, prefix+"-worker-source.json"), "ep_dddddddddddddddddddddddddd")
						defer closePeer()
						source = peer
						fixture.resolver[peer.Descriptor().ID] = peer
						// Native sftp has no two-server stream. Its comparison is explicitly a
						// staged get followed by put, with both payload legs included.
						staged := filepath.Join(t.TempDir(), "staged")
						nativeElapsed = nativePerformanceCopy(t, rtt, "get", filepath.Join(remoteSource, "source"), staged, filepath.Join(outdir, prefix+"-native-get.json"))
						nativeElapsed += nativePerformanceCopy(t, rtt, "put -f", staged, nativeDestination, filepath.Join(outdir, prefix+"-native-put.json"))
					} else {
						sourceName = "/tree"
						tree := filepath.Join(fixture.sourceRoot, "tree")
						if err := os.Mkdir(tree, 0700); err != nil {
							t.Fatal(err)
						}
						names = nil
						for i := 0; i < 8; i++ {
							name := fmt.Sprintf("file-%02d", i)
							names = append(names, name)
							if err := os.WriteFile(filepath.Join(tree, name), data, 0600); err != nil {
								t.Fatal(err)
							}
						}
						nativeElapsed = nativePerformanceCopy(t, rtt, "put -fr", tree, nativeDestination, filepath.Join(outdir, prefix+"-native.json"))
					}
					ctx := t.Context()
					planner := NewPlanner(fixture.resolver)
					reference, err := planner.Capture(ctx, normalizePlanTest(t, source, sourceName))
					if err != nil {
						t.Fatal(err)
					}
					request := validFreezeRequest(reference, normalizePlanTest(t, target, "/"))
					request.Intent.Name = "copied"
					if limited {
						request.Intent.Bandwidth.JobBytesPerSecond = 1 << 30
					}
					plan, create, err := planner.FreezeCopy(ctx, request)
					if err != nil {
						t.Fatal(err)
					}
					store, db := openTransferStore(t, ctx, filepath.Join(testkit.PersistentTempDir(t), "state.sqlite3"), true)
					defer db.Close()
					if _, _, err := store.Create(ctx, create); err != nil {
						t.Fatal(err)
					}
					journal := JobJournal{Store: store, StepIndex: 0}
					worker := NewWorker(fixture.resolver, journal)
					scheduler, err := NewTransferScheduler(foundation.RealClock{}, SchedulerPolicy{})
					if err != nil {
						t.Fatal(err)
					}
					worker.scheduler = scheduler
					started := time.Now()
					result, err := worker.Execute(ctx, plan, nil)
					elapsed := time.Since(started)
					if err != nil || result.Outcome != OutcomeCompleted || result.Bytes != uint64(len(data)*len(names)) {
						t.Fatalf("worker=%#v,%v", result, err)
					}
					closeTarget()
					expected := sha256.Sum256(data)
					for _, name := range names {
						final := filepath.Join(remote, "copied")
						nativeFinal := nativeDestination
						if direction == "directory_upload" {
							final = filepath.Join(final, name)
							nativeFinal = filepath.Join(nativeFinal, name)
						}
						for _, file := range []string{final, nativeFinal} {
							content, err := os.ReadFile(file) //nolint:gosec // both files are outputs below this test's temporary roots.
							if err != nil || sha256.Sum256(content) != expected {
								t.Fatalf("comparison content mismatch: %v", err)
							}
						}
					}
					checkpoint, err := journal.Load(ctx, plan.JobID)
					if err != nil {
						t.Fatal(err)
					}
					encoded, err := json.Marshal(map[string]any{"direction": direction, "rtt_ms": rtt, "limited": limited, "bytes": result.Bytes, "items": result.Items, "worker_seconds": elapsed.Seconds(), "native_seconds": nativeElapsed.Seconds(), "performance": checkpoint.Performance})
					if err != nil {
						t.Fatal(err)
					}
					t.Log(string(encoded))
					if err := os.WriteFile(filepath.Join(outdir, prefix+"-timing.json"), encoded, 0600); err != nil { //nolint:gosec // fixed benchmark filename in the caller-selected artifact directory.
						t.Fatal(err)
					}
				})
			}
		}
	}
}

func nativePerformanceProxy(t *testing.T) string {
	t.Helper()
	proxy, err := filepath.Abs("testdata/sftp_delay_proxy.py")
	if err != nil {
		t.Fatal(err)
	}
	return proxy
}
