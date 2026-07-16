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

func TestReconcileBackupRetentionKeepsNewestTwoAndUnknownFiles(t *testing.T) {
	t.Parallel()

	ctx, root, database, connection := retentionDatabase(t)
	ids := []string{
		"10000000000000000000000000000000",
		"20000000000000000000000000000000",
		"30000000000000000000000000000000",
		"40000000000000000000000000000000",
	}
	for index, id := range ids {
		addCatalogBackup(t, ctx, connection, root, id, int64(index+1), "verified", []byte("backup-"+id), true)
	}
	decoy := filepath.Join(root, "unknown-backup-decoy")
	if err := os.WriteFile(decoy, []byte("untouched"), 0o600); err != nil {
		t.Fatalf("write decoy: %v", err)
	}

	report, err := ReconcileBackupRetentionAfterSchemaValidation(ctx, connection, root, 1)
	if err != nil {
		t.Fatalf("ReconcileBackupRetentionAfterSchemaValidation(): %v", err)
	}
	if report.Deleted != 2 || report.Retained != BackupRetentionCount || report.ResumedDeleting != 0 {
		t.Fatalf("retention report = %#v", report)
	}
	for _, id := range ids[:2] {
		if _, err := os.Lstat(filepath.Join(root, backupBasename(id))); !os.IsNotExist(err) {
			t.Fatalf("old backup %s remains: %v", id, err)
		}
	}
	for _, id := range ids[2:] {
		if _, err := os.Lstat(filepath.Join(root, backupBasename(id))); err != nil {
			t.Fatalf("retained backup %s missing: %v", id, err)
		}
	}
	if content, err := os.ReadFile(decoy); err != nil || string(content) != "untouched" { //nolint:gosec // exact test-owned decoy
		t.Fatalf("decoy changed: content=%q error=%v", content, err)
	}
	var verified, deleting int
	if err := connection.QueryRowContext(ctx, "SELECT count(*) FILTER (WHERE status='verified'), count(*) FILTER (WHERE status='deleting') FROM migration_backups").Scan(&verified, &deleting); err != nil {
		t.Fatalf("read retention catalog: %v", err)
	}
	if verified != 2 || deleting != 0 {
		t.Fatalf("catalog verified=%d deleting=%d", verified, deleting)
	}
	if err := database.PingContext(ctx); err != nil {
		t.Fatalf("database after retention: %v", err)
	}
}

func TestReconcileBackupRetentionResumesDeletingWithPresentOrMissingFile(t *testing.T) {
	t.Parallel()

	for name, writeFile := range map[string]bool{"present": true, "missing": false} {
		writeFile := writeFile
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			ctx, root, _, connection := retentionDatabase(t)
			addCatalogBackup(t, ctx, connection, root, "50000000000000000000000000000000", 1, "deleting", []byte("deleting"), writeFile)
			addCatalogBackup(t, ctx, connection, root, "60000000000000000000000000000000", 2, "verified", []byte("keep-1"), true)
			addCatalogBackup(t, ctx, connection, root, "70000000000000000000000000000000", 3, "verified", []byte("keep-2"), true)

			report, err := ReconcileBackupRetentionAfterSchemaValidation(ctx, connection, root, 1)
			if err != nil {
				t.Fatalf("ReconcileBackupRetentionAfterSchemaValidation(): %v", err)
			}
			if report.Deleted != 1 || report.ResumedDeleting != 1 || report.Retained != 2 {
				t.Fatalf("retention report = %#v", report)
			}
			var rows int
			if err := connection.QueryRowContext(ctx, "SELECT count(*) FROM migration_backups WHERE attempt_id='50000000000000000000000000000000'").Scan(&rows); err != nil || rows != 0 {
				t.Fatalf("deleting catalog rows=%d error=%v", rows, err)
			}
		})
	}
}

func TestReconcileBackupRetentionFailsClosedForActiveAttemptOrChangedFile(t *testing.T) {
	t.Parallel()

	t.Run("active attempt", func(t *testing.T) {
		t.Parallel()
		ctx, root, _, connection := retentionDatabase(t)
		for index, id := range []string{
			"80000000000000000000000000000000",
			"90000000000000000000000000000000",
			"a0000000000000000000000000000000",
		} {
			addCatalogBackup(t, ctx, connection, root, id, int64(index+1), "verified", []byte(id), true)
		}
		request := migration.AttemptRequest{
			AttemptID: "b0000000000000000000000000000000", OriginalHead: 1, TargetHead: 2,
			MigrationSetDigest: sha256.Sum256([]byte("active retention attempt")),
		}
		if _, _, err := migration.PrepareAttempt(ctx, connection, request); err != nil {
			t.Fatalf("PrepareAttempt(): %v", err)
		}
		if _, err := ReconcileBackupRetentionAfterSchemaValidation(ctx, connection, root, 1); err == nil {
			t.Fatal("ReconcileBackupRetentionAfterSchemaValidation(active) error = nil")
		}
		var rows int
		if err := connection.QueryRowContext(ctx, "SELECT count(*) FROM migration_backups").Scan(&rows); err != nil || rows != 3 {
			t.Fatalf("catalog rows after active rejection=%d error=%v", rows, err)
		}
	})

	t.Run("changed deleting file", func(t *testing.T) {
		t.Parallel()
		ctx, root, _, connection := retentionDatabase(t)
		id := "c0000000000000000000000000000000"
		addCatalogBackup(t, ctx, connection, root, id, 1, "deleting", []byte("cataloged"), true)
		addCatalogBackup(t, ctx, connection, root, "c1000000000000000000000000000000", 2, "verified", []byte("protected-1"), true)
		addCatalogBackup(t, ctx, connection, root, "c2000000000000000000000000000000", 3, "verified", []byte("protected-2"), true)
		path := filepath.Join(root, backupBasename(id))
		if err := os.WriteFile(path, []byte("changed"), 0o600); err != nil {
			t.Fatalf("replace deleting content: %v", err)
		}
		if _, err := ReconcileBackupRetentionAfterSchemaValidation(ctx, connection, root, 1); err == nil {
			t.Fatal("ReconcileBackupRetentionAfterSchemaValidation(changed) error = nil")
		}
		if content, err := os.ReadFile(path); err != nil || string(content) != "changed" { //nolint:gosec // exact test-owned path
			t.Fatalf("changed file was touched: content=%q error=%v", content, err)
		}
		var status string
		if err := connection.QueryRowContext(ctx, "SELECT status FROM migration_backups WHERE attempt_id=?", id).Scan(&status); err != nil || status != "deleting" {
			t.Fatalf("changed catalog status=%q error=%v", status, err)
		}
	})

	t.Run("missing verified candidate", func(t *testing.T) {
		t.Parallel()
		ctx, root, _, connection := retentionDatabase(t)
		missingID := "d0000000000000000000000000000000"
		addCatalogBackup(t, ctx, connection, root, missingID, 1, "verified", []byte("missing"), false)
		addCatalogBackup(t, ctx, connection, root, "e0000000000000000000000000000000", 2, "verified", []byte("keep-1"), true)
		addCatalogBackup(t, ctx, connection, root, "f0000000000000000000000000000000", 3, "verified", []byte("keep-2"), true)
		if _, err := ReconcileBackupRetentionAfterSchemaValidation(ctx, connection, root, 1); err == nil {
			t.Fatal("ReconcileBackupRetentionAfterSchemaValidation(missing verified) error = nil")
		}
		var status string
		if err := connection.QueryRowContext(ctx, "SELECT status FROM migration_backups WHERE attempt_id=?", missingID).Scan(&status); err != nil || status != "verified" {
			t.Fatalf("missing verified catalog status=%q error=%v", status, err)
		}
	})

	t.Run("missing protected latest", func(t *testing.T) {
		t.Parallel()
		ctx, root, _, connection := retentionDatabase(t)
		oldestID := "01000000000000000000000000000000"
		addCatalogBackup(t, ctx, connection, root, oldestID, 1, "verified", []byte("only usable rollback"), true)
		addCatalogBackup(t, ctx, connection, root, "02000000000000000000000000000000", 2, "verified", []byte("missing latest 1"), false)
		addCatalogBackup(t, ctx, connection, root, "03000000000000000000000000000000", 3, "verified", []byte("missing latest 2"), false)
		if _, err := ReconcileBackupRetentionAfterSchemaValidation(ctx, connection, root, 1); err == nil {
			t.Fatal("ReconcileBackupRetentionAfterSchemaValidation(missing latest) error = nil")
		}
		if content, err := os.ReadFile(filepath.Join(root, backupBasename(oldestID))); err != nil || string(content) != "only usable rollback" { //nolint:gosec // exact test-owned path
			t.Fatalf("sole usable rollback changed: content=%q error=%v", content, err)
		}
		var verified int
		if err := connection.QueryRowContext(ctx, "SELECT count(*) FROM migration_backups WHERE status='verified'").Scan(&verified); err != nil || verified != 3 {
			t.Fatalf("verified rows after protected failure=%d error=%v", verified, err)
		}
	})

	t.Run("restored hold", func(t *testing.T) {
		t.Parallel()
		ctx, root, _, connection := retentionDatabase(t)
		for index, id := range []string{
			"11000000000000000000000000000000",
			"12000000000000000000000000000000",
			"13000000000000000000000000000000",
		} {
			addCatalogBackup(t, ctx, connection, root, id, int64(index+1), "verified", []byte(id), true)
		}
		if _, err := connection.ExecContext(ctx, "UPDATE migration_control SET upgrade_hold=1, hold_reason='restored_backup', hold_attempt_id='14000000000000000000000000000000' WHERE singleton=1"); err != nil {
			t.Fatalf("set restored hold: %v", err)
		}
		if _, err := ReconcileBackupRetentionAfterSchemaValidation(ctx, connection, root, 1); err == nil {
			t.Fatal("ReconcileBackupRetentionAfterSchemaValidation(restored hold) error = nil")
		}
		var rows int
		if err := connection.QueryRowContext(ctx, "SELECT count(*) FROM migration_backups").Scan(&rows); err != nil || rows != 3 {
			t.Fatalf("catalog rows after hold rejection=%d error=%v", rows, err)
		}
	})

	t.Run("latest row already deleting", func(t *testing.T) {
		t.Parallel()
		ctx, root, _, connection := retentionDatabase(t)
		addCatalogBackup(t, ctx, connection, root, "15000000000000000000000000000000", 1, "verified", []byte("older-1"), true)
		addCatalogBackup(t, ctx, connection, root, "16000000000000000000000000000000", 2, "verified", []byte("older-2"), true)
		latestID := "17000000000000000000000000000000"
		addCatalogBackup(t, ctx, connection, root, latestID, 3, "deleting", []byte("latest"), true)
		if _, err := ReconcileBackupRetentionAfterSchemaValidation(ctx, connection, root, 1); err == nil {
			t.Fatal("ReconcileBackupRetentionAfterSchemaValidation(latest deleting) error = nil")
		}
		if content, err := os.ReadFile(filepath.Join(root, backupBasename(latestID))); err != nil || string(content) != "latest" { //nolint:gosec // exact test-owned path
			t.Fatalf("latest deleting backup changed: content=%q error=%v", content, err)
		}
	})

	t.Run("candidate sidecar", func(t *testing.T) {
		t.Parallel()
		ctx, root, _, connection := retentionDatabase(t)
		candidateID := "18000000000000000000000000000000"
		addCatalogBackup(t, ctx, connection, root, candidateID, 1, "verified", []byte("candidate"), true)
		addCatalogBackup(t, ctx, connection, root, "19000000000000000000000000000000", 2, "verified", []byte("keep-1"), true)
		addCatalogBackup(t, ctx, connection, root, "1a000000000000000000000000000000", 3, "verified", []byte("keep-2"), true)
		sidecar := filepath.Join(root, backupBasename(candidateID)+"-wal")
		if err := os.WriteFile(sidecar, []byte("unexpected"), 0o600); err != nil {
			t.Fatalf("write candidate sidecar: %v", err)
		}
		if _, err := ReconcileBackupRetentionAfterSchemaValidation(ctx, connection, root, 1); err == nil {
			t.Fatal("ReconcileBackupRetentionAfterSchemaValidation(sidecar) error = nil")
		}
		if _, err := os.Lstat(sidecar); err != nil {
			t.Fatalf("candidate sidecar was touched: %v", err)
		}
	})
}

func retentionDatabase(t *testing.T) (context.Context, string, *sql.DB, *sql.Conn) {
	t.Helper()
	ctx := context.Background()
	root := privateTempDir(t)
	path := filepath.Join(root, "amsftp.db")
	database, _, err := Initialize(ctx, InitializeConfig{
		Root: root, DatabasePath: path,
		Random: strings.NewReader(strings.Repeat("r", probeRandomBytes+16)), Now: time.Unix(900, 0),
	})
	if err != nil {
		t.Fatalf("Initialize(): %v", err)
	}
	t.Cleanup(func() {
		if err := database.Close(); err != nil {
			t.Errorf("close retention database: %v", err)
		}
	})
	connection, err := database.Conn(ctx)
	if err != nil {
		t.Fatalf("reserve retention connection: %v", err)
	}
	t.Cleanup(func() {
		if err := connection.Close(); err != nil {
			t.Errorf("close retention connection: %v", err)
		}
	})
	return ctx, root, database, connection
}

func addCatalogBackup(t *testing.T, ctx context.Context, connection *sql.Conn, root, attemptID string, createdAt int64, status string, content []byte, writeFile bool) {
	t.Helper()
	digest := sha256.Sum256(content)
	if _, err := connection.ExecContext(ctx, "INSERT INTO migration_backups(attempt_id, original_head, target_head, migration_set_sha256, backup_basename, backup_sha256, created_at_unix, status) VALUES(?, 1, 2, ?, ?, ?, ?, ?)", attemptID, strings.Repeat("d", 64), backupBasename(attemptID), hex.EncodeToString(digest[:]), createdAt, status); err != nil {
		t.Fatalf("insert backup catalog %s: %v", attemptID, err)
	}
	if writeFile {
		if err := os.WriteFile(filepath.Join(root, backupBasename(attemptID)), content, 0o600); err != nil {
			t.Fatalf("write backup %s: %v", attemptID, err)
		}
	}
}

func backupBasename(attemptID string) string {
	return ".amsftp-backup-v1-" + attemptID + ".sqlite3"
}
