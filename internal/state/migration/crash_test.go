package migration

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/TyrantLucifer/awesome-mac-sftp/internal/testkit"
	_ "modernc.org/sqlite"
)

const (
	migrationCrashHelperEnvironment = "AMSFTP_MIGRATION_CRASH_HELPER"
	migrationCrashPathEnvironment   = "AMSFTP_MIGRATION_CRASH_PATH"
	migrationCrashPointEnvironment  = "AMSFTP_MIGRATION_CRASH_POINT"
	migrationCrashExitCode          = 92
	migrationCrashAttemptID         = "eeeeeeeeeeeeeeeeeeeeeeeeeeeeeeee"
)

func TestRunnerRecoversExactPrefixAfterProcessDeathAtTransactionBoundaries(t *testing.T) {
	points := []struct {
		name      string
		committed bool
	}{
		{name: "statement_applied"},
		{name: "history_inserted"},
		{name: "attempt_advanced"},
		{name: "before_commit"},
		{name: "commit_returned", committed: true},
	}
	for _, point := range points {
		t.Run(point.name, func(t *testing.T) {
			ctx := context.Background()
			path, request := migrationCrashFixture(t, ctx)
			runMigrationCrashHelper(t, path, point.name)
			if _, err := os.Lstat(path + "-wal"); err != nil {
				t.Fatalf("process death did not leave a physical WAL: %v", err)
			}

			database := openMigrationCrashDatabase(t, path)
			connection, err := database.Conn(ctx)
			if err != nil {
				t.Fatalf("reserve recovered connection: %v", err)
			}
			t.Cleanup(func() { _ = connection.Close() })
			var tableCount, historyCount, currentHead int
			if err := connection.QueryRowContext(ctx, "SELECT count(*) FROM sqlite_schema WHERE type='table' AND name='crash_v2'").Scan(&tableCount); err != nil {
				t.Fatalf("read recovered table: %v", err)
			}
			if err := connection.QueryRowContext(ctx, "SELECT count(*) FROM schema_migrations WHERE version=2").Scan(&historyCount); err != nil {
				t.Fatalf("read recovered history: %v", err)
			}
			if err := connection.QueryRowContext(ctx, "SELECT current_head FROM migration_attempts WHERE singleton=1").Scan(&currentHead); err != nil {
				t.Fatalf("read recovered attempt: %v", err)
			}
			want := 0
			wantHead := 1
			if point.committed {
				want = 1
				wantHead = 2
			}
			if tableCount != want || historyCount != want || currentHead != wantHead {
				t.Fatalf("recovered prefix table/history/head = %d/%d/%d, want %d/%d/%d", tableCount, historyCount, currentHead, want, want, wantHead)
			}

			v2 := migrationCrashV2()
			if !point.committed {
				if err := (Runner{AttemptID: migrationCrashAttemptID}).Apply(ctx, connection, v2, "2026-07-16T00:40:01Z"); err != nil {
					t.Fatalf("resume uncommitted prefix: %v", err)
				}
			}
			v2Contract, err := BuildSchemaContract(ctx, connection, 2)
			if err != nil {
				t.Fatalf("build recovered Version 2 contract: %v", err)
			}
			if err := ValidateHead(ctx, connection, []Migration{Version1(), v2}, map[uint64][]byte{1: Version1SchemaContract(), 2: v2Contract}, 2); err != nil {
				t.Fatalf("validate recovered Version 2 prefix: %v", err)
			}
			if err := ClearCompletedAttempt(ctx, connection, request); err != nil {
				t.Fatalf("clear recovered attempt: %v", err)
			}
			if _, err := LoadAttempt(ctx, connection); !errors.Is(err, ErrNoAttempt) {
				t.Fatalf("LoadAttempt(after recovery) error = %v", err)
			}
		})
	}
}

func TestMigrationProcessDeathHelper(t *testing.T) {
	if os.Getenv(migrationCrashHelperEnvironment) == "" {
		return
	}
	path := os.Getenv(migrationCrashPathEnvironment)
	point := os.Getenv(migrationCrashPointEnvironment)
	database := openMigrationCrashDatabase(t, path)
	connection, err := database.Conn(context.Background())
	if err != nil {
		t.Fatalf("reserve crash helper connection: %v", err)
	}
	if err := (Runner{
		AttemptID: migrationCrashAttemptID,
		fault: func(actual string) {
			if actual == point {
				os.Exit(migrationCrashExitCode)
			}
		},
	}).Apply(context.Background(), connection, migrationCrashV2(), "2026-07-16T00:40:00Z"); err != nil {
		t.Fatalf("apply crash helper migration: %v", err)
	}
	t.Fatalf("migration crash point %q was not reached", point)
}

func migrationCrashFixture(t *testing.T, ctx context.Context) (string, AttemptRequest) {
	t.Helper()
	root := testkit.PersistentTempDir(t)
	if err := os.Chmod(root, 0o700); err != nil { //nolint:gosec // persistent state fixture is owner-private
		t.Fatalf("set migration crash root mode: %v", err)
	}
	path := filepath.Join(root, "migration-crash.sqlite3")
	database := openMigrationCrashDatabase(t, path)
	connection, err := database.Conn(ctx)
	if err != nil {
		t.Fatalf("reserve fixture connection: %v", err)
	}
	if err := (Runner{}).Apply(ctx, connection, Version1(), "2026-07-16T00:39:00Z"); err != nil {
		t.Fatalf("apply fixture Version 1: %v", err)
	}
	if err := os.Chmod(path, 0o600); err != nil {
		t.Fatalf("set migration crash database mode: %v", err)
	}
	var journalMode string
	if err := connection.QueryRowContext(ctx, "PRAGMA journal_mode=WAL").Scan(&journalMode); err != nil || journalMode != "wal" {
		t.Fatalf("enable fixture WAL: mode=%q error=%v", journalMode, err)
	}
	request := AttemptRequest{
		AttemptID: migrationCrashAttemptID, OriginalHead: 1, TargetHead: 2,
		MigrationSetDigest: sha256.Sum256([]byte("migration crash fixture")),
	}
	if _, _, err := PrepareAttempt(ctx, connection, request); err != nil {
		t.Fatalf("prepare fixture attempt: %v", err)
	}
	if _, err := RecordVerifiedBackup(ctx, connection, request.AttemptID, sha256.Sum256([]byte("migration crash backup")), time.Unix(2_200, 0)); err != nil {
		t.Fatalf("record fixture backup: %v", err)
	}
	if _, err := MarkAttemptRunning(ctx, connection, request.AttemptID); err != nil {
		t.Fatalf("mark fixture running: %v", err)
	}
	if _, err := connection.ExecContext(ctx, "PRAGMA wal_checkpoint(TRUNCATE)"); err != nil {
		t.Fatalf("truncate fixture WAL: %v", err)
	}
	if err := connection.Close(); err != nil {
		t.Fatalf("close fixture connection: %v", err)
	}
	if err := database.Close(); err != nil {
		t.Fatalf("close fixture database: %v", err)
	}
	return path, request
}

func runMigrationCrashHelper(t *testing.T, path, point string) {
	t.Helper()
	executable, err := os.Executable()
	if err != nil {
		t.Fatalf("resolve migration test executable: %v", err)
	}
	command := exec.Command(executable, "-test.run=^TestMigrationProcessDeathHelper$") //nolint:gosec // exact current test binary
	command.Env = []string{
		migrationCrashHelperEnvironment + "=1",
		migrationCrashPathEnvironment + "=" + path,
		migrationCrashPointEnvironment + "=" + point,
	}
	output, err := command.CombinedOutput()
	var exitError *exec.ExitError
	if !errors.As(err, &exitError) || exitError.ExitCode() != migrationCrashExitCode {
		t.Fatalf("migration crash helper point=%s error=%v output=%s", point, err, output)
	}
}

func openMigrationCrashDatabase(t *testing.T, path string) *sql.DB {
	t.Helper()
	database, err := sql.Open("sqlite", "file:"+path)
	if err != nil {
		t.Fatalf("open migration crash database: %v", err)
	}
	database.SetMaxOpenConns(1)
	t.Cleanup(func() { _ = database.Close() })
	return database
}

func migrationCrashV2() Migration {
	return Migration{
		Version: 2, Name: "crash_v2",
		Statements:           []string{"CREATE TABLE crash_v2(id INTEGER PRIMARY KEY, value TEXT) STRICT"},
		MaxMigrationWalBytes: 1 << 20,
	}
}
