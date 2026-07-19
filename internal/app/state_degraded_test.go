package app

import (
	"context"
	"database/sql"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/TyrantLucifer/awesome-mac-sftp/internal/daemon"
	"github.com/TyrantLucifer/awesome-mac-sftp/internal/domain"
	"github.com/TyrantLucifer/awesome-mac-sftp/internal/ipc"
	"github.com/TyrantLucifer/awesome-mac-sftp/internal/job"
	"github.com/TyrantLucifer/awesome-mac-sftp/internal/platform"
	"github.com/TyrantLucifer/awesome-mac-sftp/internal/state/jobstore"
	"github.com/TyrantLucifer/awesome-mac-sftp/internal/state/migration"
	"github.com/TyrantLucifer/awesome-mac-sftp/internal/statefs"
	"github.com/TyrantLucifer/awesome-mac-sftp/internal/testkit"
	"github.com/TyrantLucifer/awesome-mac-sftp/internal/workspace"
)

func TestDaemonRestartRecoversEveryNonterminalJobStateDeterministically(t *testing.T) {
	paths, purpose := degradedStatePaths(t)
	if err := platform.PreparePrivateDirectory(paths.StateDir, platform.ValidatePersistent); err != nil {
		t.Fatalf("prepare state directory: %v", err)
	}
	ctx := context.Background()
	database, _, err := statefs.Initialize(ctx, statefs.InitializeConfig{
		Root: paths.StateDir, DatabasePath: paths.DatabaseFile, Now: time.Unix(2_200, 0),
	})
	if err != nil {
		t.Fatalf("initialize restart fixture: %v", err)
	}
	store, err := jobstore.New(ctx, database)
	if err != nil {
		t.Fatalf("open restart fixture store: %v", err)
	}
	states := []job.State{
		job.StateDraft, job.StateAwaitingConfirmation, job.StateQueued,
		job.StateRunning, job.StateVerifying, job.StatePaused,
		job.StateWaitingAuth, job.StateWaitingConflict, job.StateRetryWait,
	}
	jobIDs := make(map[job.State]domain.JobID, len(states))
	for index, state := range states {
		request := restartFixtureRequest(t, byte(index+1))
		created, _, err := store.Create(ctx, request)
		if err != nil {
			t.Fatalf("create %q restart fixture: %v", state, err)
		}
		jobIDs[state] = created.JobID
		if _, err := database.ExecContext(ctx, "UPDATE jobs SET state=?, retry_at_unix=CASE WHEN ?='retry_wait' THEN 2300 ELSE NULL END WHERE job_id=?", state, state, created.JobID); err != nil {
			t.Fatalf("seed %q restart state: %v", state, err)
		}
		if state == job.StateRunning || state == job.StateVerifying {
			if _, err := database.ExecContext(ctx, "UPDATE job_steps SET state=? WHERE job_id=? AND step_index=0", state, created.JobID); err != nil {
				t.Fatalf("seed %q active step: %v", state, err)
			}
		}
	}
	if err := database.Close(); err != nil {
		t.Fatalf("close restart fixture: %v", err)
	}

	daemonContext, stopDaemon := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- runDaemonWithPaths(daemonContext, paths, purpose) }()
	client := waitForTestDaemon(t, paths, purpose)
	observer, err := sql.Open("sqlite", "file:"+paths.DatabaseFile+"?mode=ro")
	if err != nil {
		t.Fatalf("open recovery observer: %v", err)
	}
	deadline := time.Now().Add(5 * time.Second)
	for {
		var queuedState job.State
		if err := observer.QueryRowContext(ctx, "SELECT state FROM jobs WHERE job_id=?", jobIDs[job.StateQueued]).Scan(&queuedState); err != nil {
			_ = observer.Close()
			t.Fatalf("observe queued restart fixture: %v", err)
		}
		if queuedState == job.StateFailed {
			break
		}
		if time.Now().After(deadline) {
			_ = observer.Close()
			t.Fatalf("queued restart fixture remained %q, want failed", queuedState)
		}
		time.Sleep(10 * time.Millisecond)
	}
	if err := observer.Close(); err != nil {
		t.Fatalf("close recovery observer: %v", err)
	}
	_ = client.Close()
	stopDaemon()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("stop recovery daemon: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("recovery daemon did not stop")
	}

	reopened, _, err := statefs.Initialize(ctx, statefs.InitializeConfig{Root: paths.StateDir, DatabasePath: paths.DatabaseFile})
	if err != nil {
		t.Fatalf("reopen recovered state: %v", err)
	}
	defer reopened.Close()
	for _, before := range states {
		want := before
		wantVersion := int64(1)
		wantSequence := int64(2)
		// The restart fixture deliberately stores placeholder plan JSON. A queued
		// Job is execution-eligible, so the daemon must close that invalid durable
		// work as failed instead of silently stranding it in queued.
		if before == job.StateQueued {
			want = job.StateFailed
			wantVersion++
			wantSequence++
		}
		if before == job.StateRunning || before == job.StateVerifying {
			want = job.StatePaused
			wantVersion++
			wantSequence++
		}
		var got job.State
		var version, sequence int64
		if err := reopened.QueryRowContext(ctx, "SELECT state, state_version, next_event_sequence FROM jobs WHERE job_id=?", jobIDs[before]).Scan(&got, &version, &sequence); err != nil {
			t.Fatalf("read recovered %q Job: %v", before, err)
		}
		if got != want || version != wantVersion || sequence != wantSequence {
			t.Fatalf("restart state %q = (%q, %d, %d), want (%q, %d, %d)", before, got, version, sequence, want, wantVersion, wantSequence)
		}
		var eventCount, recoveredCount int
		if err := reopened.QueryRowContext(ctx, "SELECT COUNT(*), COUNT(*) FILTER (WHERE kind='job_recovered') FROM job_events WHERE job_id=?", jobIDs[before]).Scan(&eventCount, &recoveredCount); err != nil {
			t.Fatalf("count recovered %q events: %v", before, err)
		}
		wantEvents, wantRecovered := 1, 0
		if before == job.StateQueued {
			wantEvents = 2
		}
		if before == job.StateRunning || before == job.StateVerifying {
			wantEvents, wantRecovered = 2, 1
		}
		if eventCount != wantEvents || recoveredCount != wantRecovered {
			t.Fatalf("restart events for %q = (%d, %d recovered), want (%d, %d recovered)", before, eventCount, recoveredCount, wantEvents, wantRecovered)
		}
	}
}

func TestInitializeDurableJobStoreResumesRetentionClaimBeforeReturn(t *testing.T) {
	paths, _ := degradedStatePaths(t)
	if err := platform.PreparePrivateDirectory(paths.StateDir, platform.ValidatePersistent); err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	database, _, err := statefs.Initialize(ctx, statefs.InitializeConfig{
		Root: paths.StateDir, DatabasePath: paths.DatabaseFile, Now: time.Unix(2_200, 0),
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = database.Close() })
	store, err := jobstore.New(ctx, database)
	if err != nil {
		t.Fatal(err)
	}
	request := restartFixtureRequest(t, 10)
	claimedJob, _, err := store.Create(ctx, request)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := database.ExecContext(ctx, "UPDATE jobs SET state='completed', terminal_summary='done', updated_at_unix=2200 WHERE job_id=?", claimedJob.JobID); err != nil {
		t.Fatal(err)
	}
	if _, err := database.ExecContext(ctx, "INSERT INTO job_history_retention(singleton,job_id,policy_version,claimed_at_unix) VALUES(1,?,1,2200)", claimedJob.JobID); err != nil {
		t.Fatal(err)
	}
	if _, err := initializeDurableJobStore(ctx, database, &domain.RandomGenerator{}, time.Unix(2_201, 0)); err != nil {
		t.Fatal(err)
	}
	var retainedJobs, retainedClaims int
	if err := database.QueryRowContext(ctx, "SELECT count(*) FROM jobs WHERE job_id=?", claimedJob.JobID).Scan(&retainedJobs); err != nil {
		t.Fatal(err)
	}
	if err := database.QueryRowContext(ctx, "SELECT count(*) FROM job_history_retention").Scan(&retainedClaims); err != nil {
		t.Fatal(err)
	}
	if retainedJobs != 0 || retainedClaims != 0 {
		t.Fatalf("startup retention left jobs/claims = %d/%d, want 0/0", retainedJobs, retainedClaims)
	}
}

func TestDaemonKeepsStage1ReadOnlyWhenPersistentStateIsUnsafe(t *testing.T) {
	fixtures := []struct {
		name   string
		mutate func(*testing.T, string)
	}{
		{name: "corrupt project database", mutate: corruptProjectDatabase},
		{name: "newer schema", mutate: addNewerSchemaHistory},
		{name: "database not owner writable", mutate: func(t *testing.T, path string) {
			t.Helper()
			if err := os.Chmod(path, 0o400); err != nil { //nolint:gosec // deliberately read-only negative fixture
				t.Fatalf("make database read-only: %v", err)
			}
			t.Cleanup(func() { _ = os.Chmod(path, 0o600) }) //nolint:gosec // restore private cleanup mode
		}},
	}
	for _, fixture := range fixtures {
		t.Run(fixture.name, func(t *testing.T) {
			paths, purpose := degradedStatePaths(t)
			if err := platform.PreparePrivateDirectory(paths.StateDir, platform.ValidatePersistent); err != nil {
				t.Fatalf("prepare state directory: %v", err)
			}
			database, _, err := statefs.Initialize(context.Background(), statefs.InitializeConfig{
				Root: paths.StateDir, DatabasePath: paths.DatabaseFile, Now: time.Unix(2_300, 0),
			})
			if err != nil {
				t.Fatalf("initialize fixture database: %v", err)
			}
			if err := database.Close(); err != nil {
				t.Fatalf("close fixture database: %v", err)
			}
			fixture.mutate(t, paths.DatabaseFile)
			before, err := os.ReadFile(paths.DatabaseFile) //nolint:gosec // exact test-owned database path
			if err != nil {
				t.Fatalf("read unsafe database before daemon: %v", err)
			}
			beforeInfo, err := os.Lstat(paths.DatabaseFile)
			if err != nil {
				t.Fatalf("stat unsafe database before daemon: %v", err)
			}

			ctx, cancel := context.WithCancel(context.Background())
			done := make(chan error, 1)
			go func() { done <- runDaemonWithPaths(ctx, paths, purpose) }()
			client := waitForTestDaemon(t, paths, purpose)
			var endpoints ipc.ProviderEndpointsResponse
			if err := client.Call(context.Background(), daemon.ProviderEndpoints, struct{}{}, &endpoints); err != nil {
				t.Fatalf("list endpoints in degraded mode: %v", err)
			}
			if len(endpoints.Endpoints) != 1 || endpoints.Endpoints[0].Kind != "local" {
				t.Fatalf("degraded endpoints = %#v", endpoints.Endpoints)
			}
			var workspaces workspace.ListResponse
			err = client.Call(context.Background(), daemon.WorkspaceList, workspace.ListRequest{}, &workspaces)
			var remoteError *daemon.RemoteError
			if !errors.As(err, &remoteError) || remoteError.RPC.Code != domain.CodeUnsupported {
				t.Fatalf("degraded WorkspaceList error = %v, want unsupported mutation store", err)
			}
			_ = client.Close()
			cancel()
			select {
			case err := <-done:
				if err != nil {
					t.Fatalf("stop degraded daemon: %v", err)
				}
			case <-time.After(5 * time.Second):
				t.Fatal("degraded daemon did not stop")
			}

			after, err := os.ReadFile(paths.DatabaseFile) //nolint:gosec // exact test-owned database path
			if err != nil {
				t.Fatalf("read unsafe database after daemon: %v", err)
			}
			afterInfo, err := os.Lstat(paths.DatabaseFile)
			if err != nil {
				t.Fatalf("stat unsafe database after daemon: %v", err)
			}
			if !reflect.DeepEqual(after, before) || afterInfo.Mode() != beforeInfo.Mode() || afterInfo.Size() != beforeInfo.Size() || !afterInfo.ModTime().Equal(beforeInfo.ModTime()) {
				t.Fatalf("degraded daemon mutated unsafe database: before=%v after=%v", beforeInfo, afterInfo)
			}
			for _, forbidden := range []string{paths.LogFile, filepath.Join(paths.StateDir, "workspaces")} {
				if _, err := os.Lstat(forbidden); !errors.Is(err, os.ErrNotExist) {
					t.Fatalf("degraded daemon created persistent path %q: %v", forbidden, err)
				}
			}
		})
	}
}

func TestDaemonExplicitMigrationResumeReturnsStateFailureInsteadOfServingDegraded(t *testing.T) {
	paths, purpose := degradedStatePaths(t)
	if err := platform.PreparePrivateDirectory(paths.StateDir, platform.ValidatePersistent); err != nil {
		t.Fatalf("prepare state directory: %v", err)
	}
	database, _, err := statefs.Initialize(context.Background(), statefs.InitializeConfig{
		Root: paths.StateDir, DatabasePath: paths.DatabaseFile, Now: time.Unix(2_400, 0),
	})
	if err != nil {
		t.Fatalf("initialize explicit-resume fixture: %v", err)
	}
	if err := database.Close(); err != nil {
		t.Fatalf("close explicit-resume fixture: %v", err)
	}
	addNewerSchemaHistory(t, paths.DatabaseFile)

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	err = runDaemonWithPathsAndOptions(ctx, paths, purpose, daemonOptions{explicitMigrationResume: true})
	if err == nil || !strings.Contains(err.Error(), "explicit migration resume") {
		t.Fatalf("runDaemonWithPathsAndOptions(explicit invalid state) error = %v", err)
	}
	if _, err := os.Lstat(paths.ControlSocket); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("explicit resume failure created control socket: %v", err)
	}
}

func corruptProjectDatabase(t *testing.T, path string) {
	t.Helper()
	file, err := os.OpenFile(path, os.O_RDWR, 0) //nolint:gosec // exact test-owned database path
	if err != nil {
		t.Fatalf("open database for corruption: %v", err)
	}
	if _, err := file.WriteAt(make([]byte, 256), 100); err != nil {
		_ = file.Close()
		t.Fatalf("corrupt database page: %v", err)
	}
	if err := file.Sync(); err != nil {
		_ = file.Close()
		t.Fatalf("sync corrupt database: %v", err)
	}
	if err := file.Close(); err != nil {
		t.Fatalf("close corrupt database: %v", err)
	}
}

func addNewerSchemaHistory(t *testing.T, path string) {
	t.Helper()
	database, err := sql.Open("sqlite", "file:"+path)
	if err != nil {
		t.Fatalf("open database for newer history: %v", err)
	}
	compiledMigrations, _ := migration.CompiledSet()
	compiledTarget := len(compiledMigrations)
	if _, err := database.Exec("INSERT INTO schema_migrations(version, name, sha256, applied_at) VALUES(?, 'future', ?, '2026-07-16T00:50:00Z')", compiledTarget+1, strings.Repeat("a", 64)); err != nil {
		_ = database.Close()
		t.Fatalf("insert newer history: %v", err)
	}
	if _, err := database.Exec("PRAGMA wal_checkpoint(TRUNCATE)"); err != nil {
		_ = database.Close()
		t.Fatalf("truncate newer-history WAL: %v", err)
	}
	if err := database.Close(); err != nil {
		t.Fatalf("close newer-history database: %v", err)
	}
}

func restartFixtureRequest(t *testing.T, seed byte) jobstore.CreateRequest {
	t.Helper()
	generator := &domain.RandomGenerator{Reader: strings.NewReader(strings.Repeat(string([]byte{seed}), 64))}
	requestID, err := domain.NewRequestID(generator)
	if err != nil {
		t.Fatalf("create restart request ID: %v", err)
	}
	jobID, err := domain.NewJobID(generator)
	if err != nil {
		t.Fatalf("create restart Job ID: %v", err)
	}
	eventID, err := domain.NewEventID(generator)
	if err != nil {
		t.Fatalf("create restart event ID: %v", err)
	}
	destination := `{"path":"/destination"}`
	return jobstore.CreateRequest{
		PlanID: "plan_" + string(jobID)[4:], RequestID: requestID, JobID: jobID,
		Kind: "copy", SourceJSON: `{"path":"/source"}`, DestinationJSON: &destination,
		Route: "local_relay", Verification: "size", ConflictPolicy: "ask", RiskClass: "ordinary",
		InitialState: job.StateQueued, EventID: eventID, Now: time.Unix(int64(seed)+100, 0),
		Steps: []jobstore.Step{{Kind: "copy"}},
	}
}

func degradedStatePaths(t *testing.T) (platform.Paths, platform.ValidationPurpose) {
	t.Helper()
	runtimeRoot, err := os.MkdirTemp("/tmp", "amsftp-degraded-")
	if err != nil {
		t.Fatalf("create degraded runtime root: %v", err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(runtimeRoot) })
	if err := os.Chmod(runtimeRoot, 0o700); err != nil { //nolint:gosec // private runtime root
		t.Fatalf("set degraded runtime mode: %v", err)
	}
	persistent := testkit.PersistentTempDir(t)
	stateDir := filepath.Join(persistent, "state")
	paths := platform.Paths{
		StateDir: stateDir, DatabaseFile: filepath.Join(stateDir, "amsftp.db"),
		LogFile: filepath.Join(persistent, "log", "daemon.jsonl"), RuntimeDir: runtimeRoot,
		ControlSocket: filepath.Join(runtimeRoot, "control-v1.sock"), LockFile: filepath.Join(runtimeRoot, "daemon.lock"),
	}
	return paths, platform.ValidateRuntimeFallback
}

func waitForTestDaemon(t *testing.T, paths platform.Paths, purpose platform.ValidationPurpose) *daemon.Client {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	var client *daemon.Client
	var err error
	for time.Now().Before(deadline) {
		attemptCtx, stop := context.WithTimeout(context.Background(), 200*time.Millisecond)
		client, err = connectExisting(attemptCtx, paths, purpose)
		stop()
		if err == nil {
			return client
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("connect degraded daemon: %v", err)
	return nil
}
