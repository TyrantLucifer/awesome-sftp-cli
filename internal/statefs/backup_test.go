package statefs

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/TyrantLucifer/awesome-mac-sftp/internal/state/migration"
)

func TestCreateMigrationBackupSanitizesPublishesAndCatalogsOnlineSnapshot(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	root := privateTempDir(t)
	path := filepath.Join(root, "amsftp.db")
	database, _, err := Initialize(ctx, InitializeConfig{
		Root: root, DatabasePath: path,
		Random: strings.NewReader(strings.Repeat("b", probeRandomBytes+16)), Now: time.Unix(100, 0),
	})
	if err != nil {
		t.Fatalf("initialize source: %v", err)
	}
	defer func() {
		if err := database.Close(); err != nil {
			t.Errorf("close source database: %v", err)
		}
	}()
	connection, err := database.Conn(ctx)
	if err != nil {
		t.Fatalf("reserve source connection: %v", err)
	}
	defer func() {
		if err := connection.Close(); err != nil {
			t.Errorf("close source connection: %v", err)
		}
	}()
	setDigest := sha256.Sum256([]byte("v1-to-v2-set"))
	request := migration.AttemptRequest{
		AttemptID: "0123456789abcdef0123456789abcdef", OriginalHead: 1, TargetHead: 2, MigrationSetDigest: setDigest,
	}
	attempt, _, err := migration.PrepareAttempt(ctx, connection, request)
	if err != nil {
		t.Fatalf("prepare source attempt: %v", err)
	}
	walInfo, err := os.Lstat(path + "-wal")
	if err != nil || walInfo.Size() <= 32 {
		t.Fatalf("source attempt is not present in a live WAL: info=%v error=%v", walInfo, err)
	}

	backupPath, backupDigest, err := CreateMigrationBackup(ctx, BackupConfig{
		Root: root, Source: database, Attempt: attempt, CreatedAt: time.Unix(101, 0),
	})
	if err != nil {
		t.Fatalf("CreateMigrationBackup(): %v", err)
	}
	if backupPath != filepath.Join(root, attempt.ReservedBackupBasename) {
		t.Fatalf("backup path = %q", backupPath)
	}
	content, err := os.ReadFile(backupPath) //nolint:gosec // test-owned exact backup path
	if err != nil {
		t.Fatalf("read backup: %v", err)
	}
	if got := sha256.Sum256(content); got != backupDigest {
		t.Fatalf("backup digest = %x, want %x", got, backupDigest)
	}
	for _, suffix := range []string{"-wal", "-shm", "-journal", ".tmp"} {
		if _, err := os.Lstat(backupPath + suffix); !os.IsNotExist(err) {
			t.Fatalf("backup sidecar/temp %q remains: %v", suffix, err)
		}
	}

	backupDB, err := sql.Open("sqlite", "file:"+backupPath+"?mode=ro")
	if err != nil {
		t.Fatalf("open backup: %v", err)
	}
	defer func() {
		if err := backupDB.Close(); err != nil {
			t.Errorf("close backup database: %v", err)
		}
	}()
	var attempts, holds int
	if err := backupDB.QueryRowContext(ctx, "SELECT count(*) FROM migration_attempts").Scan(&attempts); err != nil {
		t.Fatalf("read backup attempts: %v", err)
	}
	if err := backupDB.QueryRowContext(ctx, "SELECT count(*) FROM migration_control WHERE singleton=1 AND upgrade_hold=1 AND hold_reason='restored_backup' AND hold_attempt_id=?", request.AttemptID).Scan(&holds); err != nil {
		t.Fatalf("read backup hold: %v", err)
	}
	if attempts != 0 || holds != 1 {
		t.Fatalf("sanitized backup attempts=%d holds=%d", attempts, holds)
	}

	ready, err := migration.LoadAttempt(ctx, connection)
	if err != nil {
		t.Fatalf("load ready source attempt: %v", err)
	}
	if ready.Status != migration.AttemptReady || ready.BackupSHA256 == nil || *ready.BackupSHA256 != hex.EncodeToString(backupDigest[:]) {
		t.Fatalf("ready source attempt = %#v", ready)
	}
	var catalogRows int
	if err := connection.QueryRowContext(ctx, "SELECT count(*) FROM migration_backups WHERE attempt_id=? AND backup_sha256=? AND status='verified'", request.AttemptID, hex.EncodeToString(backupDigest[:])).Scan(&catalogRows); err != nil || catalogRows != 1 {
		t.Fatalf("source backup catalog rows=%d error=%v", catalogRows, err)
	}
}

func TestCreateMigrationBackupNeverReplacesExistingDestination(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	root := privateTempDir(t)
	path := filepath.Join(root, "amsftp.db")
	database, _, err := Initialize(ctx, InitializeConfig{
		Root: root, DatabasePath: path,
		Random: strings.NewReader(strings.Repeat("c", probeRandomBytes+16)), Now: time.Unix(200, 0),
	})
	if err != nil {
		t.Fatalf("initialize source: %v", err)
	}
	defer func() {
		if err := database.Close(); err != nil {
			t.Errorf("close source database: %v", err)
		}
	}()
	connection, err := database.Conn(ctx)
	if err != nil {
		t.Fatalf("reserve source connection: %v", err)
	}
	defer func() {
		if err := connection.Close(); err != nil {
			t.Errorf("close source connection: %v", err)
		}
	}()
	request := migration.AttemptRequest{
		AttemptID: "abcdef0123456789abcdef0123456789", OriginalHead: 1, TargetHead: 2,
		MigrationSetDigest: sha256.Sum256([]byte("collision-set")),
	}
	attempt, _, err := migration.PrepareAttempt(ctx, connection, request)
	if err != nil {
		t.Fatalf("prepare source attempt: %v", err)
	}
	final := filepath.Join(root, attempt.ReservedBackupBasename)
	decoy := []byte("must not be replaced")
	if err := os.WriteFile(final, decoy, 0o600); err != nil {
		t.Fatalf("write destination decoy: %v", err)
	}
	if _, _, err := CreateMigrationBackup(ctx, BackupConfig{Root: root, Source: database, Attempt: attempt, CreatedAt: time.Unix(201, 0)}); err == nil {
		t.Fatal("CreateMigrationBackup(collision) error = nil")
	}
	got, err := os.ReadFile(final) //nolint:gosec // test-owned exact path
	if err != nil {
		t.Fatalf("read destination decoy: %v", err)
	}
	if string(got) != string(decoy) {
		t.Fatalf("destination decoy changed: %q", got)
	}
	failed, err := migration.LoadAttempt(ctx, connection)
	if err != nil {
		t.Fatalf("load failed source attempt: %v", err)
	}
	if failed.Status != migration.AttemptFailed || failed.ErrorKind == nil || *failed.ErrorKind != "backup_failed" || failed.BackupSHA256 != nil {
		t.Fatalf("failed source attempt = %#v", failed)
	}
}

func TestCreateMigrationBackupRejectsForgedAttemptBeforePathUse(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	root := privateTempDir(t)
	path := filepath.Join(root, "amsftp.db")
	database, _, err := Initialize(ctx, InitializeConfig{
		Root: root, DatabasePath: path,
		Random: strings.NewReader(strings.Repeat("f", probeRandomBytes+16)), Now: time.Unix(250, 0),
	})
	if err != nil {
		t.Fatalf("initialize source: %v", err)
	}
	defer func() {
		if err := database.Close(); err != nil {
			t.Errorf("close source database: %v", err)
		}
	}()
	forgedID := "x/../../outside-root"
	attempt := migration.Attempt{
		AttemptID: forgedID, OriginalHead: 1, CurrentHead: 1, TargetHead: 2,
		MigrationSetSHA256:     strings.Repeat("a", 64),
		ReservedBackupBasename: ".amsftp-backup-v1-" + forgedID + ".sqlite3",
		Status:                 migration.AttemptPreparing,
	}
	outside := filepath.Clean(filepath.Join(root, attempt.ReservedBackupBasename+".tmp"))
	if _, _, err := CreateMigrationBackup(ctx, BackupConfig{
		Root: root, Source: database, Attempt: attempt, CreatedAt: time.Unix(251, 0),
	}); err == nil || !strings.Contains(err.Error(), "invalid attempt") {
		t.Fatalf("CreateMigrationBackup(forged attempt) error = %v", err)
	}
	if _, err := os.Lstat(outside); !os.IsNotExist(err) {
		t.Fatalf("forged out-of-root path exists: %v", err)
	}
}

func TestCreateMigrationBackupRebuildsExactPartialTempAfterCrash(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	root := privateTempDir(t)
	path := filepath.Join(root, "amsftp.db")
	database, _, err := Initialize(ctx, InitializeConfig{
		Root: root, DatabasePath: path,
		Random: strings.NewReader(strings.Repeat("d", probeRandomBytes+16)), Now: time.Unix(300, 0),
	})
	if err != nil {
		t.Fatalf("initialize source: %v", err)
	}
	defer func() {
		if err := database.Close(); err != nil {
			t.Errorf("close source database: %v", err)
		}
	}()
	connection, err := database.Conn(ctx)
	if err != nil {
		t.Fatalf("reserve source connection: %v", err)
	}
	defer func() {
		if err := connection.Close(); err != nil {
			t.Errorf("close source connection: %v", err)
		}
	}()
	request := migration.AttemptRequest{
		AttemptID: "22222222222222222222222222222222", OriginalHead: 1, TargetHead: 2,
		MigrationSetDigest: sha256.Sum256([]byte("partial-temp-set")),
	}
	attempt, _, err := migration.PrepareAttempt(ctx, connection, request)
	if err != nil {
		t.Fatalf("prepare source attempt: %v", err)
	}
	temp := filepath.Join(root, attempt.ReservedBackupBasename) + ".tmp"
	if err := os.WriteFile(temp, []byte("interrupted online backup"), 0o600); err != nil {
		t.Fatalf("write exact partial temp: %v", err)
	}

	backupPath, _, err := CreateMigrationBackup(ctx, BackupConfig{
		Root: root, Source: database, Attempt: attempt, CreatedAt: time.Unix(301, 0),
	})
	if err != nil {
		t.Fatalf("CreateMigrationBackup(partial restart): %v", err)
	}
	if backupPath != filepath.Join(root, attempt.ReservedBackupBasename) {
		t.Fatalf("backup path = %q", backupPath)
	}
	if _, err := os.Lstat(temp); !os.IsNotExist(err) {
		t.Fatalf("partial temp remains: %v", err)
	}
}

func TestCreateMigrationBackupReusesVerifiedPublishedFinalAfterCrash(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	root := privateTempDir(t)
	path := filepath.Join(root, "amsftp.db")
	database, _, err := Initialize(ctx, InitializeConfig{
		Root: root, DatabasePath: path,
		Random: strings.NewReader(strings.Repeat("e", probeRandomBytes+16)), Now: time.Unix(400, 0),
	})
	if err != nil {
		t.Fatalf("initialize source: %v", err)
	}
	defer func() {
		if err := database.Close(); err != nil {
			t.Errorf("close source database: %v", err)
		}
	}()
	connection, err := database.Conn(ctx)
	if err != nil {
		t.Fatalf("reserve source connection: %v", err)
	}
	defer func() {
		if err := connection.Close(); err != nil {
			t.Errorf("close source connection: %v", err)
		}
	}()
	request := migration.AttemptRequest{
		AttemptID: "33333333333333333333333333333333", OriginalHead: 1, TargetHead: 2,
		MigrationSetDigest: sha256.Sum256([]byte("published-final-set")),
	}
	attempt, _, err := migration.PrepareAttempt(ctx, connection, request)
	if err != nil {
		t.Fatalf("prepare source attempt: %v", err)
	}
	backupPath, wantDigest, err := CreateMigrationBackup(ctx, BackupConfig{
		Root: root, Source: database, Attempt: attempt, CreatedAt: time.Unix(401, 0),
	})
	if err != nil {
		t.Fatalf("create initial backup: %v", err)
	}
	before, err := os.Lstat(backupPath)
	if err != nil {
		t.Fatalf("stat published backup: %v", err)
	}
	if _, err := connection.ExecContext(ctx, "BEGIN IMMEDIATE"); err != nil {
		t.Fatalf("begin crash fixture reset: %v", err)
	}
	if _, err := connection.ExecContext(ctx, "DELETE FROM migration_backups WHERE attempt_id=?", request.AttemptID); err != nil {
		t.Fatalf("delete catalog fixture: %v", err)
	}
	if _, err := connection.ExecContext(ctx, "UPDATE migration_attempts SET backup_sha256=NULL, status='preparing' WHERE singleton=1 AND attempt_id=?", request.AttemptID); err != nil {
		t.Fatalf("reset attempt fixture: %v", err)
	}
	if _, err := connection.ExecContext(ctx, "COMMIT"); err != nil {
		t.Fatalf("commit crash fixture reset: %v", err)
	}
	preparing, err := migration.LoadAttempt(ctx, connection)
	if err != nil {
		t.Fatalf("reload preparing attempt: %v", err)
	}

	gotPath, gotDigest, err := CreateMigrationBackup(ctx, BackupConfig{
		Root: root, Source: database, Attempt: preparing, CreatedAt: time.Unix(402, 0),
	})
	if err != nil {
		t.Fatalf("CreateMigrationBackup(published restart): %v", err)
	}
	after, err := os.Lstat(backupPath)
	if err != nil {
		t.Fatalf("restat published backup: %v", err)
	}
	if gotPath != backupPath || gotDigest != wantDigest || !os.SameFile(before, after) {
		t.Fatalf("published backup was not reused: path=%q digest=%x same=%t", gotPath, gotDigest, os.SameFile(before, after))
	}
	ready, err := migration.LoadAttempt(ctx, connection)
	if err != nil {
		t.Fatalf("load resumed ready attempt: %v", err)
	}
	if ready.Status != migration.AttemptReady || ready.BackupSHA256 == nil || *ready.BackupSHA256 != hex.EncodeToString(wantDigest[:]) {
		t.Fatalf("resumed ready attempt = %#v", ready)
	}
}

func TestVerifySanitizedBackupRejectsExtraMigrationHistory(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	root := privateTempDir(t)
	path := filepath.Join(root, "amsftp.db")
	database, _, err := Initialize(ctx, InitializeConfig{
		Root: root, DatabasePath: path,
		Random: strings.NewReader(strings.Repeat("g", probeRandomBytes+16)), Now: time.Unix(500, 0),
	})
	if err != nil {
		t.Fatalf("initialize source: %v", err)
	}
	connection, err := database.Conn(ctx)
	if err != nil {
		t.Fatalf("reserve source connection: %v", err)
	}
	request := migration.AttemptRequest{
		AttemptID: "44444444444444444444444444444444", OriginalHead: 1, TargetHead: 2,
		MigrationSetDigest: sha256.Sum256([]byte("extra-history-set")),
	}
	attempt, _, err := migration.PrepareAttempt(ctx, connection, request)
	if err != nil {
		t.Fatalf("prepare source attempt: %v", err)
	}
	backupPath, _, err := CreateMigrationBackup(ctx, BackupConfig{
		Root: root, Source: database, Attempt: attempt, CreatedAt: time.Unix(501, 0),
	})
	if err != nil {
		t.Fatalf("create backup: %v", err)
	}
	if err := connection.Close(); err != nil {
		t.Fatalf("close source connection: %v", err)
	}
	if err := database.Close(); err != nil {
		t.Fatalf("close source database: %v", err)
	}

	tamper, err := sql.Open("sqlite", durabilityURI(backupPath, false))
	if err != nil {
		t.Fatalf("open backup for tamper: %v", err)
	}
	if _, err := tamper.ExecContext(ctx, "INSERT INTO schema_migrations(version, name, sha256, applied_at) VALUES(2, 'tampered', ?, '2026-07-16T00:00:00Z')", strings.Repeat("a", 64)); err != nil {
		t.Fatalf("insert extra history: %v", err)
	}
	if _, err := tamper.ExecContext(ctx, "PRAGMA wal_checkpoint(TRUNCATE)"); err != nil {
		t.Fatalf("checkpoint tampered backup: %v", err)
	}
	if err := tamper.Close(); err != nil {
		t.Fatalf("close tampered backup: %v", err)
	}
	if err := verifySanitizedBackup(ctx, backupPath, request.AttemptID); err == nil {
		t.Fatal("verifySanitizedBackup(extra history) error = nil")
	}
}
