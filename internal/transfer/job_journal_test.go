package transfer

import (
	"context"
	"database/sql"
	"errors"
	"net/url"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/TyrantLucifer/awesome-mac-sftp/internal/state/jobstore"
	"github.com/TyrantLucifer/awesome-mac-sftp/internal/state/migration"
	"github.com/TyrantLucifer/awesome-mac-sftp/internal/testkit"
	_ "modernc.org/sqlite"
)

func TestJobJournalResumesTransferAfterDatabaseAndWorkerRestart(t *testing.T) {
	ctx := context.Background()
	fixture := newWorkerFixture(t, []byte("restart-safe-payload"), ConflictAsk)
	fixture.plan.BufferBytes = 4
	root := testkit.PersistentTempDir(t)
	if err := os.Chmod(root, 0o700); err != nil { //nolint:gosec // state root must be owner-private
		t.Fatal(err)
	}
	databasePath := filepath.Join(root, "state.sqlite3")
	store, database := openTransferStore(t, ctx, databasePath, true)
	if _, _, err := store.Create(ctx, fixture.create); err != nil {
		t.Fatalf("create durable Job: %v", err)
	}
	now := func() time.Time { return time.Unix(1_800_000_001, 0) }
	journal := JobJournal{Store: store, StepIndex: 0, Now: now}
	control := ControlFunc(func(checkpoint Checkpoint) ControlAction {
		if checkpoint.Offset >= 4 {
			return ControlPause
		}
		return ControlContinue
	})
	if _, err := NewWorker(fixture.resolver, journal).Execute(ctx, fixture.plan, control); !errors.Is(err, ErrPaused) {
		t.Fatalf("first worker error = %v, want ErrPaused", err)
	}
	persisted, err := journal.Load(ctx, fixture.plan.JobID)
	if err != nil {
		t.Fatalf("load paused checkpoint: %v", err)
	}
	if persisted == nil || persisted.Offset != 4 || persisted.Phase != PhaseStreaming || len(persisted.ChecksumState) == 0 {
		t.Fatalf("paused checkpoint = %#v", persisted)
	}
	if err := database.Close(); err != nil {
		t.Fatalf("close first database: %v", err)
	}

	restartedStore, restartedDatabase := openTransferStore(t, ctx, databasePath, false)
	t.Cleanup(func() { _ = restartedDatabase.Close() })
	restartedJournal := JobJournal{Store: restartedStore, StepIndex: 0, Now: now}
	result, err := NewWorker(fixture.resolver, restartedJournal).Execute(ctx, fixture.plan, nil)
	if err != nil {
		t.Fatalf("restarted worker: %v", err)
	}
	if result.Outcome != OutcomeCompleted {
		t.Fatalf("restarted result = %#v", result)
	}
	assertWorkerBytes(t, fixture.destination, fixture.plan.Final, []byte("restart-safe-payload"))
	committed, err := restartedJournal.Load(ctx, fixture.plan.JobID)
	if err != nil {
		t.Fatalf("load committed checkpoint: %v", err)
	}
	if committed == nil || committed.Phase != PhaseCommitted || committed.Offset != uint64(len("restart-safe-payload")) {
		t.Fatalf("committed checkpoint = %#v", committed)
	}
}

func TestJobJournalPersistsDirectIdentityAcrossDatabaseRestart(t *testing.T) {
	ctx := context.Background()
	fixture := newWorkerFixture(t, []byte("direct journal identity"), ConflictAsk)
	root := testkit.PersistentTempDir(t)
	if err := os.Chmod(root, 0o700); err != nil { //nolint:gosec // state root must be owner-private
		t.Fatal(err)
	}
	databasePath := filepath.Join(root, "state.sqlite3")
	store, database := openTransferStore(t, ctx, databasePath, true)
	if _, _, err := store.Create(ctx, fixture.create); err != nil {
		t.Fatalf("create durable Job: %v", err)
	}
	want := Checkpoint{
		JobID: fixture.plan.JobID, Phase: PhaseStreaming, Offset: 7,
		SourceFingerprint: cloneFingerprint(fixture.plan.Source.Fingerprint),
		Part:              fixture.plan.Part, Final: fixture.plan.Final,
		ActualRoute: RouteLevel2Direct, RouteReason: ReasonLevel2PreflightPassed,
		DirectFormatVersion: Level2DirectFormatVersion,
		DirectNonce:         "0123456789abcdef0123456789abcdef",
	}
	if err := (JobJournal{Store: store, StepIndex: 0}).Save(ctx, want); err != nil {
		t.Fatalf("save direct checkpoint: %v", err)
	}
	if err := database.Close(); err != nil {
		t.Fatalf("close first database: %v", err)
	}

	restartedStore, restartedDatabase := openTransferStore(t, ctx, databasePath, false)
	t.Cleanup(func() { _ = restartedDatabase.Close() })
	got, err := (JobJournal{Store: restartedStore, StepIndex: 0}).Load(ctx, fixture.plan.JobID)
	if err != nil {
		t.Fatalf("load direct checkpoint after restart: %v", err)
	}
	if got == nil || got.DirectFormatVersion != want.DirectFormatVersion || got.DirectNonce != want.DirectNonce {
		t.Fatalf("direct identity after restart = %#v, want format=%d nonce=%q", got, want.DirectFormatVersion, want.DirectNonce)
	}
}

func openTransferStore(t *testing.T, ctx context.Context, databasePath string, initialize bool) (*jobstore.Store, *sql.DB) {
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
		if err := (migration.Runner{}).Apply(ctx, connection, migration.Version1(), "2026-07-16T00:00:00Z"); err != nil {
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
		t.Fatalf("enable WAL: mode=%q error=%v", mode, err)
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
