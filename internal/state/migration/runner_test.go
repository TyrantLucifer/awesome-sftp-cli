package migration

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	_ "modernc.org/sqlite"
)

func TestRunnerAppliesExactVersion1Atomically(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	database := openTestDatabase(t, filepath.Join(t.TempDir(), "state.sqlite3"))
	connection, err := database.Conn(ctx)
	if err != nil {
		t.Fatalf("reserve database connection: %v", err)
	}
	defer connection.Close()

	runner := Runner{}
	if err := runner.Apply(ctx, connection, Version1(), "2026-07-16T00:00:00Z"); err != nil {
		t.Fatalf("apply version 1: %v", err)
	}

	assertPragmaInt(t, ctx, connection, "application_id", applicationID)
	assertPragmaInt(t, ctx, connection, "user_version", 0)
	assertPragmaInt(t, ctx, connection, "page_size", statePageSize)
	assertPragmaInt(t, ctx, connection, "max_page_count", stateMaxPageCount)

	digest, err := Checksum(Version1())
	if err != nil {
		t.Fatalf("checksum version 1: %v", err)
	}
	var version int64
	var name, checksum, appliedAt string
	if err := connection.QueryRowContext(ctx, "SELECT version, name, sha256, applied_at FROM schema_migrations").Scan(&version, &name, &checksum, &appliedAt); err != nil {
		t.Fatalf("read migration history: %v", err)
	}
	if version != 1 || name != "init" || checksum != hex.EncodeToString(digest[:]) || appliedAt != "2026-07-16T00:00:00Z" {
		t.Fatalf("migration row = (%d, %q, %q, %q), want exact version 1 row", version, name, checksum, appliedAt)
	}

	var controlRows int
	if err := connection.QueryRowContext(ctx, "SELECT count(*) FROM migration_control WHERE singleton=1 AND upgrade_hold=0 AND hold_reason IS NULL AND hold_attempt_id IS NULL").Scan(&controlRows); err != nil {
		t.Fatalf("read migration control: %v", err)
	}
	if controlRows != 1 {
		t.Fatalf("valid migration control rows = %d, want 1", controlRows)
	}
}

func TestRunnerRollsBackWholeMigrationOnStatementFailure(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	database := openTestDatabase(t, filepath.Join(t.TempDir(), "failure.sqlite3"))
	connection, err := database.Conn(ctx)
	if err != nil {
		t.Fatalf("reserve database connection: %v", err)
	}
	defer connection.Close()

	migration := Migration{
		Version: 2,
		Name:    "failure",
		Statements: []string{
			"CREATE TABLE first_table(id INTEGER PRIMARY KEY)",
			"CREATE TABLE first_table(id INTEGER PRIMARY KEY)",
		},
		MaxMigrationWalBytes: 1,
	}
	if err := (Runner{AttemptID: "55555555555555555555555555555555", WALMonitor: noopMigrationWALMonitor{}}).Apply(ctx, connection, migration, "2026-07-16T00:00:00Z"); err == nil {
		t.Fatal("Apply() error = nil, want duplicate table failure")
	} else if !strings.Contains(err.Error(), "statement 1") {
		t.Fatalf("Apply() error = %v, want second-statement failure", err)
	}
	var count int
	if err := connection.QueryRowContext(ctx, "SELECT count(*) FROM sqlite_schema WHERE name='first_table'").Scan(&count); err != nil {
		t.Fatalf("inspect schema after rollback: %v", err)
	}
	if count != 0 {
		t.Fatalf("first_table count = %d, want 0 after rollback", count)
	}
}

func TestRunnerRejectsSQLTailBeforeOpeningTransaction(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	database := openTestDatabase(t, filepath.Join(t.TempDir(), "tail.sqlite3"))
	connection, err := database.Conn(ctx)
	if err != nil {
		t.Fatalf("reserve database connection: %v", err)
	}
	defer connection.Close()

	migration := Migration{Version: 2, Name: "tail", Statements: []string{"CREATE TABLE escaped(id INTEGER); CREATE TABLE tail(id INTEGER)"}, MaxMigrationWalBytes: 1}
	if err := (Runner{AttemptID: "66666666666666666666666666666666", WALMonitor: noopMigrationWALMonitor{}}).Apply(ctx, connection, migration, "2026-07-16T00:00:00Z"); err == nil {
		t.Fatal("Apply() error = nil, want SQL tail rejection")
	} else if !strings.Contains(err.Error(), "tokens after statement separator") {
		t.Fatalf("Apply() error = %v, want SQL-tail admission failure", err)
	}
	var count int
	if err := connection.QueryRowContext(ctx, "SELECT count(*) FROM sqlite_schema").Scan(&count); err != nil {
		t.Fatalf("inspect empty schema: %v", err)
	}
	if count != 0 {
		t.Fatalf("schema object count = %d, want 0", count)
	}
}

func TestRunnerWALBudgetRollsBackBeforeCommitAndPreservesCommittedPrefix(t *testing.T) {
	t.Parallel()

	t.Run("pre-commit violation", func(t *testing.T) {
		t.Parallel()
		ctx := context.Background()
		connection, request := runningAttemptConnection(t, ctx, "71000000000000000000000000000000")
		monitor := &faultMigrationWALMonitor{afterStatementError: errors.New("declared WAL budget exceeded")}
		v2 := Migration{Version: 2, Name: "wal_precommit", Statements: []string{"CREATE TABLE wal_precommit(id INTEGER PRIMARY KEY) STRICT"}, MaxMigrationWalBytes: 1 << 20}
		if err := (Runner{AttemptID: request.AttemptID, WALMonitor: monitor}).Apply(ctx, connection, v2, "2026-07-16T00:00:03Z"); err == nil {
			t.Fatal("Apply(pre-commit violation) error = nil")
		}
		assertMigrationPrefix(t, ctx, connection, request.AttemptID, "wal_precommit", 1, false)
	})

	t.Run("committed violation", func(t *testing.T) {
		t.Parallel()
		ctx := context.Background()
		connection, request := runningAttemptConnection(t, ctx, "72000000000000000000000000000000")
		monitor := &faultMigrationWALMonitor{afterCommitError: &CommittedMigrationWALViolation{BudgetBytes: 1024, ActualBytes: 2048}}
		v2 := Migration{Version: 2, Name: "wal_committed", Statements: []string{"CREATE TABLE wal_committed(id INTEGER PRIMARY KEY) STRICT"}, MaxMigrationWalBytes: 1 << 20}
		err := (Runner{AttemptID: request.AttemptID, WALMonitor: monitor}).Apply(ctx, connection, v2, "2026-07-16T00:00:04Z")
		var violation *CommittedMigrationWALViolation
		if !errors.As(err, &violation) {
			t.Fatalf("Apply(committed violation) error = %v", err)
		}
		assertMigrationPrefix(t, ctx, connection, request.AttemptID, "wal_committed", 2, true)
	})
}

func TestNativeMigrationWALMonitorCheckpointsBudgetedVersion(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	connection, request := runningAttemptConnection(t, ctx, "73000000000000000000000000000000")
	v2 := Migration{Version: 2, Name: "wal_native", Statements: []string{"CREATE TABLE wal_native(id INTEGER PRIMARY KEY) STRICT"}, MaxMigrationWalBytes: 1 << 20}
	if err := (Runner{AttemptID: request.AttemptID}).Apply(ctx, connection, v2, "2026-07-16T00:00:05Z"); err != nil {
		t.Fatalf("Apply(native WAL monitor): %v", err)
	}
	assertMigrationPrefix(t, ctx, connection, request.AttemptID, "wal_native", 2, true)
	var busy, logFrames, checkpointed int64
	if err := connection.QueryRowContext(ctx, "PRAGMA wal_checkpoint(PASSIVE)").Scan(&busy, &logFrames, &checkpointed); err != nil {
		t.Fatalf("read post-migration WAL state: %v", err)
	}
	if busy != 0 || logFrames != 0 || checkpointed != 0 {
		t.Fatalf("post-migration WAL state = (%d,%d,%d), want (0,0,0)", busy, logFrames, checkpointed)
	}
}

type noopMigrationWALMonitor struct{}

func (noopMigrationWALMonitor) Prepare(context.Context, *sql.Conn, Migration) error { return nil }
func (noopMigrationWALMonitor) AfterStatement(context.Context, int) error           { return nil }
func (noopMigrationWALMonitor) BeforeCommit(context.Context) error                  { return nil }
func (noopMigrationWALMonitor) AfterCommit(context.Context) error                   { return nil }
func (noopMigrationWALMonitor) Checkpoint(context.Context) error                    { return nil }

type faultMigrationWALMonitor struct {
	noopMigrationWALMonitor
	afterStatementError error
	afterCommitError    error
}

func (monitor *faultMigrationWALMonitor) AfterStatement(context.Context, int) error {
	return monitor.afterStatementError
}

func (monitor *faultMigrationWALMonitor) AfterCommit(context.Context) error {
	return monitor.afterCommitError
}

func runningAttemptConnection(t *testing.T, ctx context.Context, attemptID string) (*sql.Conn, AttemptRequest) {
	t.Helper()
	root := t.TempDir()
	if err := os.Chmod(root, 0o700); err != nil { //nolint:gosec // directory requires owner traversal
		t.Fatalf("set private database root: %v", err)
	}
	path := filepath.Join(root, "running.sqlite3")
	database := openTestDatabase(t, path)
	connection, err := database.Conn(ctx)
	if err != nil {
		t.Fatalf("reserve running connection: %v", err)
	}
	t.Cleanup(func() {
		if err := connection.Close(); err != nil {
			t.Errorf("close running connection: %v", err)
		}
	})
	if err := (Runner{}).Apply(ctx, connection, Version1(), "2026-07-16T00:00:00Z"); err != nil {
		t.Fatalf("apply version 1: %v", err)
	}
	if err := os.Chmod(path, 0o600); err != nil {
		t.Fatalf("set private database mode: %v", err)
	}
	var journalMode string
	if err := connection.QueryRowContext(ctx, "PRAGMA journal_mode=WAL").Scan(&journalMode); err != nil || journalMode != "wal" {
		t.Fatalf("enable WAL: mode=%q error=%v", journalMode, err)
	}
	request := AttemptRequest{
		AttemptID: attemptID, OriginalHead: 1, TargetHead: 2,
		MigrationSetDigest: sha256.Sum256([]byte("runner WAL " + attemptID)),
	}
	if _, _, err := PrepareAttempt(ctx, connection, request); err != nil {
		t.Fatalf("PrepareAttempt(): %v", err)
	}
	if _, err := RecordVerifiedBackup(ctx, connection, attemptID, sha256.Sum256([]byte("backup "+attemptID)), time.Unix(1000, 0)); err != nil {
		t.Fatalf("RecordVerifiedBackup(): %v", err)
	}
	if _, err := MarkAttemptRunning(ctx, connection, attemptID); err != nil {
		t.Fatalf("MarkAttemptRunning(): %v", err)
	}
	return connection, request
}

func assertMigrationPrefix(t *testing.T, ctx context.Context, connection *sql.Conn, attemptID, table string, wantHead int64, wantTable bool) {
	t.Helper()
	attempt, err := LoadAttempt(ctx, connection)
	if err != nil {
		t.Fatalf("LoadAttempt(): %v", err)
	}
	if attempt.AttemptID != attemptID || attempt.CurrentHead != wantHead {
		t.Fatalf("attempt prefix = %#v, want head %d", attempt, wantHead)
	}
	var tableRows, historyRows int
	if err := connection.QueryRowContext(ctx, "SELECT count(*) FROM sqlite_schema WHERE type='table' AND name=?", table).Scan(&tableRows); err != nil {
		t.Fatalf("read table prefix: %v", err)
	}
	if err := connection.QueryRowContext(ctx, "SELECT count(*) FROM schema_migrations WHERE version=2").Scan(&historyRows); err != nil {
		t.Fatalf("read history prefix: %v", err)
	}
	wantRows := 0
	if wantTable {
		wantRows = 1
	}
	if tableRows != wantRows || historyRows != wantRows {
		t.Fatalf("prefix table/history rows = %d/%d, want %d/%d", tableRows, historyRows, wantRows, wantRows)
	}
}

func openTestDatabase(t *testing.T, path string) *sql.DB {
	t.Helper()
	database, err := sql.Open("sqlite", "file:"+path)
	if err != nil {
		t.Fatalf("open test database: %v", err)
	}
	database.SetMaxOpenConns(1)
	t.Cleanup(func() {
		if err := database.Close(); err != nil {
			t.Errorf("close test database: %v", err)
		}
	})
	return database
}

func assertPragmaInt(t *testing.T, ctx context.Context, connection *sql.Conn, name string, want int64) {
	t.Helper()
	var got int64
	if err := connection.QueryRowContext(ctx, "PRAGMA "+name).Scan(&got); err != nil {
		t.Fatalf("read PRAGMA %s: %v", name, err)
	}
	if got != want {
		t.Fatalf("PRAGMA %s = %d, want %d", name, got, want)
	}
}
