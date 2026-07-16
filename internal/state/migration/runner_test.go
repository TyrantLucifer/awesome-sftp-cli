package migration

import (
	"context"
	"database/sql"
	"encoding/hex"
	"path/filepath"
	"strings"
	"testing"

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
	if err := (Runner{AttemptID: "55555555555555555555555555555555"}).Apply(ctx, connection, migration, "2026-07-16T00:00:00Z"); err == nil {
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
	if err := (Runner{AttemptID: "66666666666666666666666666666666"}).Apply(ctx, connection, migration, "2026-07-16T00:00:00Z"); err == nil {
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
