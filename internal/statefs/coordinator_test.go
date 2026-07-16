package statefs

import (
	"context"
	"database/sql"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/TyrantLucifer/awesome-mac-sftp/internal/state/migration"
	"github.com/TyrantLucifer/awesome-mac-sftp/internal/testkit"
	_ "modernc.org/sqlite"
)

func TestUpgradeDatabaseRunsOneFrozenAttemptAcrossMultiplePendingVersions(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	root := privateTempDir(t)
	path := filepath.Join(root, "amsftp.db")
	database, _, err := Initialize(ctx, withVersion1CompiledState(InitializeConfig{
		Root: root, DatabasePath: path,
		Random: strings.NewReader(strings.Repeat("i", probeRandomBytes+16)), Now: time.Unix(700, 0),
	}))
	if err != nil {
		t.Fatalf("initialize source: %v", err)
	}
	defer func() {
		if err := database.Close(); err != nil {
			t.Errorf("close source database: %v", err)
		}
	}()
	migrations, contracts := coordinatorFixture(t, ctx)
	report, err := UpgradeDatabase(ctx, UpgradeConfig{
		Root: root, DatabasePath: path, Database: database,
		Migrations: migrations, SchemaContracts: contracts,
		AttemptID: "77777777777777777777777777777777", Now: time.Unix(701, 0),
	})
	if err != nil {
		t.Fatalf("UpgradeDatabase(): %v", err)
	}
	if report.OriginalHead != 1 || report.TargetHead != 3 || report.Applied != 2 || report.BackupBasename != ".amsftp-backup-v1-77777777777777777777777777777777.sqlite3" {
		t.Fatalf("upgrade report = %#v", report)
	}
	connection, err := database.Conn(ctx)
	if err != nil {
		t.Fatalf("reserve upgraded connection: %v", err)
	}
	defer func() {
		if err := connection.Close(); err != nil {
			t.Errorf("close upgraded connection: %v", err)
		}
	}()
	if err := migration.ValidateHead(ctx, connection, migrations, contracts, 3); err != nil {
		t.Fatalf("validate upgraded head: %v", err)
	}
	var attempts, backups int
	if err := connection.QueryRowContext(ctx, "SELECT count(*) FROM migration_attempts").Scan(&attempts); err != nil {
		t.Fatalf("count attempts: %v", err)
	}
	if err := connection.QueryRowContext(ctx, "SELECT count(*) FROM migration_backups WHERE status='verified'").Scan(&backups); err != nil {
		t.Fatalf("count backups: %v", err)
	}
	if attempts != 0 || backups != 1 {
		t.Fatalf("post-upgrade attempts/backups = %d/%d, want 0/1", attempts, backups)
	}
	if _, err := os.Lstat(filepath.Join(root, report.BackupBasename)); err != nil {
		t.Fatalf("stat migration backup: %v", err)
	}
}

func TestUpgradeDatabaseRequiresExplicitResumeForRunningAttemptAndReusesBackup(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	root := privateTempDir(t)
	path := filepath.Join(root, "amsftp.db")
	database, _, err := Initialize(ctx, withVersion1CompiledState(InitializeConfig{
		Root: root, DatabasePath: path,
		Random: strings.NewReader(strings.Repeat("j", probeRandomBytes+16)), Now: time.Unix(800, 0),
	}))
	if err != nil {
		t.Fatalf("initialize source: %v", err)
	}
	defer func() {
		if err := database.Close(); err != nil {
			t.Errorf("close source database: %v", err)
		}
	}()
	migrations, contracts := coordinatorFixture(t, ctx)
	digests, err := migration.SchemaContractDigests(migrations, contracts)
	if err != nil {
		t.Fatalf("build contract digests: %v", err)
	}
	setDigest, err := migration.MigrationSetDigest(1, 3, migrations, digests)
	if err != nil {
		t.Fatalf("build migration-set digest: %v", err)
	}
	connection, err := database.Conn(ctx)
	if err != nil {
		t.Fatalf("reserve source connection: %v", err)
	}
	request := migration.AttemptRequest{
		AttemptID: "88888888888888888888888888888888", OriginalHead: 1, TargetHead: 3, MigrationSetDigest: setDigest,
	}
	attempt, _, err := migration.PrepareAttempt(ctx, connection, request)
	if err != nil {
		t.Fatalf("prepare interrupted attempt: %v", err)
	}
	backupPath, _, err := CreateMigrationBackup(ctx, BackupConfig{
		Root: root, Source: database, Attempt: attempt, CreatedAt: time.Unix(801, 0),
		Migrations: migrations, SchemaContracts: contracts,
	})
	if err != nil {
		t.Fatalf("create interrupted-attempt backup: %v", err)
	}
	before, err := os.Lstat(backupPath)
	if err != nil {
		t.Fatalf("stat interrupted-attempt backup: %v", err)
	}
	if _, err := migration.MarkAttemptRunning(ctx, connection, request.AttemptID); err != nil {
		t.Fatalf("mark interrupted attempt running: %v", err)
	}
	if err := connection.Close(); err != nil {
		t.Fatalf("close source setup connection: %v", err)
	}

	config := UpgradeConfig{
		Root: root, DatabasePath: path, Database: database,
		Migrations: migrations, SchemaContracts: contracts, Now: time.Unix(802, 0),
	}
	if _, err := UpgradeDatabase(ctx, config); !errors.Is(err, ErrExplicitMigrationResumeRequired) {
		t.Fatalf("UpgradeDatabase(implicit running) error = %v", err)
	}
	config.ExplicitResume = true
	report, err := UpgradeDatabase(ctx, config)
	if err != nil {
		t.Fatalf("UpgradeDatabase(explicit resume): %v", err)
	}
	if report.Applied != 2 || report.BackupBasename != filepath.Base(backupPath) {
		t.Fatalf("resumed upgrade report = %#v", report)
	}
	after, err := os.Lstat(backupPath)
	if err != nil {
		t.Fatalf("restat interrupted-attempt backup: %v", err)
	}
	if !os.SameFile(before, after) {
		t.Fatal("explicit resume replaced the verified backup")
	}
}

func coordinatorFixture(t *testing.T, ctx context.Context) ([]migration.Migration, map[uint64][]byte) {
	t.Helper()
	v2 := migration.Version2()
	v3 := migration.Migration{Version: 3, Name: "third", Statements: []string{"CREATE TABLE coordinator_third(id INTEGER PRIMARY KEY, note TEXT) STRICT"}, MaxMigrationWalBytes: 1 << 20}
	migrations := []migration.Migration{migration.Version1(), v2, v3}
	contracts := map[uint64][]byte{1: migration.Version1SchemaContract()}
	root := testkit.PersistentTempDir(t)
	if err := os.Chmod(root, 0o700); err != nil { //nolint:gosec // private test fixture root
		t.Fatalf("set reference root mode: %v", err)
	}
	path := filepath.Join(root, "reference.sqlite3")
	database, err := sql.Open("sqlite", durabilityURI(path, true))
	if err != nil {
		t.Fatalf("open reference database: %v", err)
	}
	t.Cleanup(func() {
		if err := database.Close(); err != nil {
			t.Errorf("close reference database: %v", err)
		}
	})
	connection, err := database.Conn(ctx)
	if err != nil {
		t.Fatalf("reserve reference connection: %v", err)
	}
	t.Cleanup(func() {
		if err := connection.Close(); err != nil {
			t.Errorf("close reference connection: %v", err)
		}
	})
	if err := (migration.Runner{}).Apply(ctx, connection, migration.Version1(), "2026-07-16T00:20:00Z"); err != nil {
		t.Fatalf("apply reference version 1: %v", err)
	}
	if err := os.Chmod(path, 0o600); err != nil {
		t.Fatalf("set reference database mode: %v", err)
	}
	if _, err := connection.ExecContext(ctx, "INSERT INTO migration_attempts(singleton, attempt_id, original_head, current_head, target_head, migration_set_sha256, reserved_backup_basename, backup_sha256, status, error_kind) VALUES(1, 'aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa', 1, 1, 3, ?, '.amsftp-backup-v1-aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa.sqlite3', ?, 'running', NULL)", strings.Repeat("a", 64), strings.Repeat("b", 64)); err != nil {
		t.Fatalf("insert reference attempt: %v", err)
	}
	monitor := coordinatorNoopWALMonitor{}
	if err := (migration.Runner{AttemptID: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", WALMonitor: monitor}).Apply(ctx, connection, v2, "2026-07-16T00:20:01Z"); err != nil {
		t.Fatalf("apply reference version 2: %v", err)
	}
	contracts[2], err = migration.BuildSchemaContract(ctx, connection, 2)
	if err != nil {
		t.Fatalf("build reference contract 2: %v", err)
	}
	if err := (migration.Runner{AttemptID: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", WALMonitor: monitor}).Apply(ctx, connection, v3, "2026-07-16T00:20:02Z"); err != nil {
		t.Fatalf("apply reference version 3: %v", err)
	}
	contracts[3], err = migration.BuildSchemaContract(ctx, connection, 3)
	if err != nil {
		t.Fatalf("build reference contract 3: %v", err)
	}
	return migrations, contracts
}

type coordinatorNoopWALMonitor struct{}

func (coordinatorNoopWALMonitor) Prepare(context.Context, *sql.Conn, migration.Migration) error {
	return nil
}
func (coordinatorNoopWALMonitor) AfterStatement(context.Context, int) error { return nil }
func (coordinatorNoopWALMonitor) BeforeCommit(context.Context) error        { return nil }
func (coordinatorNoopWALMonitor) AfterCommit(context.Context) error         { return nil }
func (coordinatorNoopWALMonitor) Checkpoint(context.Context) error          { return nil }
