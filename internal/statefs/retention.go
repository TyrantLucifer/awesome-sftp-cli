package statefs

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"syscall"
)

const BackupRetentionCount = 2

type RetentionReport struct {
	Deleted         int
	Retained        int
	ResumedDeleting int
}

type catalogBackup struct {
	AttemptID          string
	OriginalHead       int64
	TargetHead         int64
	MigrationSetSHA256 string
	Basename           string
	SHA256             string
	CreatedAtUnix      int64
	Status             string
}

const catalogBackupColumns = "attempt_id, original_head, target_head, migration_set_sha256, backup_basename, backup_sha256, created_at_unix, status"

type rowScanner interface {
	Scan(...any) error
}

func (backup *catalogBackup) scan(scanner rowScanner) error {
	return scanner.Scan(
		&backup.AttemptID,
		&backup.OriginalHead,
		&backup.TargetHead,
		&backup.MigrationSetSHA256,
		&backup.Basename,
		&backup.SHA256,
		&backup.CreatedAtUnix,
		&backup.Status,
	)
}

// ReconcileBackupRetentionAfterSchemaValidation is called only after the
// coordinator has verified the new head's complete history and whole-schema
// contract and cleared the completed attempt. It rechecks the durable head and
// absence of any active attempt in every catalog transaction.
func ReconcileBackupRetentionAfterSchemaValidation(ctx context.Context, connection *sql.Conn, root string, validatedHead int64) (report RetentionReport, returnErr error) {
	if connection == nil || root == "" || validatedHead <= 0 {
		return report, fmt.Errorf("reconcile backup retention: invalid input")
	}
	if _, err := ValidateRoot(root); err != nil {
		return report, fmt.Errorf("reconcile backup retention: %w", err)
	}
	directory, err := openRetentionDirectory(filepath.Clean(root))
	if err != nil {
		return report, fmt.Errorf("reconcile backup retention: %w", err)
	}
	defer func() {
		returnErr = errors.Join(returnErr, directory.close())
	}()
	if err := ensureRetentionAllowed(ctx, connection, validatedHead); err != nil {
		return report, err
	}
	initialDeleting, err := countCatalogStatus(ctx, connection, "deleting")
	if err != nil {
		return report, err
	}
	protected, err := verifyProtectedBackups(ctx, connection, directory)
	if err != nil {
		return report, err
	}
	if initialDeleting > 0 && protected < BackupRetentionCount {
		return report, fmt.Errorf("reconcile backup retention: deleting rows have only %d protected verified backups", protected)
	}
	for {
		deleting, found, err := loadOneCatalogBackup(ctx, connection, "deleting")
		if err != nil {
			return report, err
		}
		if found {
			if err := removeDeletingBackup(directory, deleting); err != nil {
				return report, err
			}
			if err := removeDeletingCatalogRow(ctx, connection, validatedHead, deleting); err != nil {
				return report, err
			}
			report.Deleted++
			if initialDeleting > 0 {
				report.ResumedDeleting++
				initialDeleting--
			}
			continue
		}
		marked, err := markNextRetentionCandidate(ctx, connection, directory, validatedHead)
		if err != nil {
			return report, err
		}
		if marked {
			continue
		}
		retained, err := countCatalogStatus(ctx, connection, "verified")
		if err != nil {
			return report, err
		}
		report.Retained = retained
		return report, nil
	}
}

func verifyProtectedBackups(ctx context.Context, connection *sql.Conn, directory *retentionDirectory) (int, error) {
	rows, err := connection.QueryContext(ctx, "SELECT "+catalogBackupColumns+" FROM migration_backups ORDER BY created_at_unix DESC, attempt_id DESC LIMIT 2")
	if err != nil {
		return 0, fmt.Errorf("reconcile backup retention: load protected backups: %w", err)
	}
	protected := make([]catalogBackup, 0, BackupRetentionCount)
	for rows.Next() {
		var backup catalogBackup
		if err := backup.scan(rows); err != nil {
			_ = rows.Close()
			return 0, fmt.Errorf("reconcile backup retention: scan protected backup: %w", err)
		}
		protected = append(protected, backup)
	}
	if err := errors.Join(rows.Err(), rows.Close()); err != nil {
		return 0, fmt.Errorf("reconcile backup retention: finish protected backup query: %w", err)
	}
	for _, backup := range protected {
		if backup.Status != "verified" {
			return 0, fmt.Errorf("reconcile backup retention: protected backup %q has status %q", backup.Basename, backup.Status)
		}
		if err := verifyCatalogBackupPresent(directory, backup); err != nil {
			return 0, fmt.Errorf("reconcile backup retention: protected backup %q: %w", backup.Basename, err)
		}
	}
	return len(protected), nil
}

func ensureRetentionAllowed(ctx context.Context, connection *sql.Conn, validatedHead int64) error {
	var attempts, historyRows, currentHead, controls int64
	if err := connection.QueryRowContext(ctx, "SELECT count(*) FROM migration_attempts").Scan(&attempts); err != nil {
		return fmt.Errorf("reconcile backup retention: read active attempts: %w", err)
	}
	if attempts != 0 {
		return fmt.Errorf("reconcile backup retention: active migration attempt protects all backups")
	}
	if err := connection.QueryRowContext(ctx, "SELECT count(*) FROM migration_control WHERE singleton=1 AND upgrade_hold=0 AND hold_reason IS NULL AND hold_attempt_id IS NULL").Scan(&controls); err != nil {
		return fmt.Errorf("reconcile backup retention: read migration control: %w", err)
	}
	if controls != 1 {
		return fmt.Errorf("reconcile backup retention: restored-backup hold or invalid migration control blocks retention")
	}
	if err := connection.QueryRowContext(ctx, "SELECT count(*), COALESCE(max(version), 0) FROM schema_migrations").Scan(&historyRows, &currentHead); err != nil {
		return fmt.Errorf("reconcile backup retention: read migration head: %w", err)
	}
	if historyRows != validatedHead || currentHead != validatedHead {
		return fmt.Errorf("reconcile backup retention: history rows/head = %d/%d, want validated head %d", historyRows, currentHead, validatedHead)
	}
	return nil
}

func countCatalogStatus(ctx context.Context, connection *sql.Conn, status string) (int, error) {
	var count int
	if err := connection.QueryRowContext(ctx, "SELECT count(*) FROM migration_backups WHERE status=?", status).Scan(&count); err != nil {
		return 0, fmt.Errorf("reconcile backup retention: count %s catalog rows: %w", status, err)
	}
	return count, nil
}

func loadOneCatalogBackup(ctx context.Context, connection *sql.Conn, status string) (catalogBackup, bool, error) {
	var backup catalogBackup
	err := backup.scan(connection.QueryRowContext(ctx, "SELECT "+catalogBackupColumns+" FROM migration_backups WHERE status=? ORDER BY created_at_unix, attempt_id LIMIT 1", status))
	if errors.Is(err, sql.ErrNoRows) {
		return catalogBackup{}, false, nil
	}
	if err != nil {
		return catalogBackup{}, false, fmt.Errorf("reconcile backup retention: load %s catalog row: %w", status, err)
	}
	return backup, true, nil
}

func markNextRetentionCandidate(ctx context.Context, connection *sql.Conn, directory *retentionDirectory, validatedHead int64) (bool, error) {
	candidate, found, err := loadRetentionCandidate(ctx, connection)
	if err != nil {
		return false, err
	}
	if !found {
		return false, nil
	}
	if err := verifyCatalogBackupPresent(directory, candidate); err != nil {
		return false, fmt.Errorf("reconcile backup retention: verify candidate before deleting marker: %w", err)
	}
	marked := false
	err = withRetentionTransaction(ctx, connection, func() error {
		if err := ensureRetentionAllowed(ctx, connection, validatedHead); err != nil {
			return err
		}
		current, found, err := loadRetentionCandidate(ctx, connection)
		if err != nil {
			return err
		}
		if !found || current != candidate {
			return fmt.Errorf("reconcile backup retention: verified candidate changed before deleting marker")
		}
		updated, err := connection.ExecContext(ctx, "UPDATE migration_backups SET status='deleting' WHERE attempt_id=? AND original_head=? AND target_head=? AND migration_set_sha256=? AND backup_basename=? AND backup_sha256=? AND created_at_unix=? AND status='verified'", candidate.AttemptID, candidate.OriginalHead, candidate.TargetHead, candidate.MigrationSetSHA256, candidate.Basename, candidate.SHA256, candidate.CreatedAtUnix)
		if err != nil {
			return fmt.Errorf("reconcile backup retention: mark deleting: %w", err)
		}
		rows, err := updated.RowsAffected()
		if err != nil {
			return fmt.Errorf("reconcile backup retention: read marked row count: %w", err)
		}
		if rows != 1 {
			return fmt.Errorf("reconcile backup retention: candidate changed before deleting marker")
		}
		marked = true
		return nil
	})
	if err != nil {
		return false, err
	}
	return marked, nil
}

func loadRetentionCandidate(ctx context.Context, connection *sql.Conn) (catalogBackup, bool, error) {
	var candidate catalogBackup
	err := candidate.scan(connection.QueryRowContext(ctx, "SELECT "+catalogBackupColumns+" FROM migration_backups WHERE status='verified' AND attempt_id NOT IN (SELECT attempt_id FROM migration_backups WHERE status='verified' ORDER BY created_at_unix DESC, attempt_id DESC LIMIT 2) ORDER BY created_at_unix, attempt_id LIMIT 1"))
	if errors.Is(err, sql.ErrNoRows) {
		return catalogBackup{}, false, nil
	}
	if err != nil {
		return catalogBackup{}, false, fmt.Errorf("reconcile backup retention: select deletion candidate: %w", err)
	}
	return candidate, true, nil
}

func removeDeletingCatalogRow(ctx context.Context, connection *sql.Conn, validatedHead int64, backup catalogBackup) error {
	return withRetentionTransaction(ctx, connection, func() error {
		if err := ensureRetentionAllowed(ctx, connection, validatedHead); err != nil {
			return err
		}
		deleted, err := connection.ExecContext(ctx, "DELETE FROM migration_backups WHERE attempt_id=? AND original_head=? AND target_head=? AND migration_set_sha256=? AND backup_basename=? AND backup_sha256=? AND created_at_unix=? AND status='deleting'", backup.AttemptID, backup.OriginalHead, backup.TargetHead, backup.MigrationSetSHA256, backup.Basename, backup.SHA256, backup.CreatedAtUnix)
		if err != nil {
			return fmt.Errorf("reconcile backup retention: delete catalog row: %w", err)
		}
		rows, err := deleted.RowsAffected()
		if err != nil {
			return fmt.Errorf("reconcile backup retention: read deleted catalog row count: %w", err)
		}
		if rows != 1 {
			return fmt.Errorf("reconcile backup retention: deleting catalog row changed")
		}
		return nil
	})
}

func removeDeletingBackup(directory *retentionDirectory, backup catalogBackup) error {
	if err := validateCatalogBackupFields(backup); err != nil {
		return fmt.Errorf("reconcile backup retention: %w", err)
	}
	wantBasename := backupBasenameForAttempt(backup.AttemptID)
	if wantBasename == "" || backup.Basename != wantBasename || filepath.Base(backup.Basename) != backup.Basename {
		return fmt.Errorf("reconcile backup retention: invalid catalog basename %q", backup.Basename)
	}
	wantDigest, err := hex.DecodeString(backup.SHA256)
	if err != nil || len(wantDigest) != sha256.Size || hex.EncodeToString(wantDigest) != backup.SHA256 {
		return fmt.Errorf("reconcile backup retention: invalid catalog digest for %q", backup.Basename)
	}
	path := filepath.Join(directory.path, backup.Basename)
	if filepath.Dir(path) != directory.path {
		return fmt.Errorf("reconcile backup retention: catalog path escapes state root")
	}
	if err := requireBackupSidecarsAbsent(directory, backup.Basename); err != nil {
		return fmt.Errorf("reconcile backup retention: %w", err)
	}
	exists, err := directory.exists(backup.Basename)
	if err != nil {
		return fmt.Errorf("reconcile backup retention: inspect deleting backup: %w", err)
	}
	if !exists {
		if err := directory.sync(); err != nil {
			return fmt.Errorf("reconcile backup retention: persist already-missing deleting backup: %w", err)
		}
		return nil
	}
	if err := directory.validateRootIdentity(); err != nil {
		return fmt.Errorf("reconcile backup retention: %w", err)
	}
	if err := validatePrivateRegular(path); err != nil {
		return fmt.Errorf("reconcile backup retention: validate deleting backup: %w", err)
	}
	file, err := directory.open(backup.Basename)
	if err != nil {
		return fmt.Errorf("reconcile backup retention: open deleting backup without following links: %w", err)
	}
	fileInfo, err := file.Stat()
	if err != nil {
		_ = file.Close()
		return fmt.Errorf("reconcile backup retention: stat deleting backup handle: %w", err)
	}
	matches, err := directory.matches(backup.Basename, fileInfo)
	if err != nil {
		_ = file.Close()
		return fmt.Errorf("reconcile backup retention: restat deleting backup: %w", err)
	}
	if !matches {
		_ = file.Close()
		return fmt.Errorf("reconcile backup retention: deleting backup identity changed")
	}
	hasher := sha256.New()
	if _, err := io.Copy(hasher, file); err != nil {
		_ = file.Close()
		return fmt.Errorf("reconcile backup retention: hash deleting backup: %w", err)
	}
	if !equalBytes(hasher.Sum(nil), wantDigest) {
		_ = file.Close()
		return fmt.Errorf("reconcile backup retention: deleting backup digest changed")
	}
	afterInfo, err := file.Stat()
	if err != nil {
		_ = file.Close()
		return fmt.Errorf("reconcile backup retention: restat deleting backup handle: %w", err)
	}
	if err := validateStableFileSnapshot(fileInfo, afterInfo); err != nil {
		_ = file.Close()
		return fmt.Errorf("reconcile backup retention: deleting backup changed while hashing: %w", err)
	}
	if err := validatePrivateRegular(path); err != nil {
		_ = file.Close()
		return fmt.Errorf("reconcile backup retention: revalidate deleting backup: %w", err)
	}
	matches, err = directory.matches(backup.Basename, fileInfo)
	if err != nil {
		_ = file.Close()
		return fmt.Errorf("reconcile backup retention: restat deleting backup before unlink: %w", err)
	}
	if !matches {
		_ = file.Close()
		return fmt.Errorf("reconcile backup retention: deleting backup identity changed before unlink")
	}
	if err := directory.unlink(backup.Basename); err != nil {
		_ = file.Close()
		return fmt.Errorf("reconcile backup retention: remove backup: %w", err)
	}
	if err := file.Close(); err != nil {
		return fmt.Errorf("reconcile backup retention: close removed backup: %w", err)
	}
	if err := directory.sync(); err != nil {
		return fmt.Errorf("reconcile backup retention: persist backup removal: %w", err)
	}
	return nil
}

func verifyCatalogBackupPresent(directory *retentionDirectory, backup catalogBackup) error {
	if err := validateCatalogBackupFields(backup); err != nil {
		return err
	}
	wantBasename := backupBasenameForAttempt(backup.AttemptID)
	if wantBasename == "" || backup.Basename != wantBasename || filepath.Base(backup.Basename) != backup.Basename {
		return fmt.Errorf("invalid catalog basename %q", backup.Basename)
	}
	wantDigest, err := hex.DecodeString(backup.SHA256)
	if err != nil || len(wantDigest) != sha256.Size || hex.EncodeToString(wantDigest) != backup.SHA256 {
		return fmt.Errorf("invalid catalog digest for %q", backup.Basename)
	}
	path := filepath.Join(directory.path, backup.Basename)
	if filepath.Dir(path) != directory.path {
		return fmt.Errorf("catalog path escapes state root")
	}
	if err := requireBackupSidecarsAbsent(directory, backup.Basename); err != nil {
		return err
	}
	exists, err := directory.exists(backup.Basename)
	if err != nil {
		return fmt.Errorf("inspect verified backup: %w", err)
	}
	if !exists {
		return fmt.Errorf("verified backup %q is missing", backup.Basename)
	}
	if err := directory.validateRootIdentity(); err != nil {
		return err
	}
	if err := validatePrivateRegular(path); err != nil {
		return fmt.Errorf("validate verified backup: %w", err)
	}
	file, err := directory.open(backup.Basename)
	if err != nil {
		return fmt.Errorf("open verified backup without following links: %w", err)
	}
	fileInfo, err := file.Stat()
	if err != nil {
		_ = file.Close()
		return fmt.Errorf("stat verified backup handle: %w", err)
	}
	matches, err := directory.matches(backup.Basename, fileInfo)
	if err != nil {
		_ = file.Close()
		return fmt.Errorf("restat verified backup: %w", err)
	}
	if !matches {
		_ = file.Close()
		return fmt.Errorf("verified backup identity changed")
	}
	hasher := sha256.New()
	if _, err := io.Copy(hasher, file); err != nil {
		_ = file.Close()
		return fmt.Errorf("hash verified backup: %w", err)
	}
	if !equalBytes(hasher.Sum(nil), wantDigest) {
		_ = file.Close()
		return fmt.Errorf("verified backup digest changed")
	}
	afterInfo, err := file.Stat()
	if err != nil {
		_ = file.Close()
		return fmt.Errorf("restat verified backup handle: %w", err)
	}
	if err := validateStableFileSnapshot(fileInfo, afterInfo); err != nil {
		_ = file.Close()
		return fmt.Errorf("verified backup changed while hashing: %w", err)
	}
	if err := file.Close(); err != nil {
		return fmt.Errorf("close verified backup: %w", err)
	}
	if err := validatePrivateRegular(path); err != nil {
		return fmt.Errorf("revalidate verified backup: %w", err)
	}
	return nil
}

func validateCatalogBackupFields(backup catalogBackup) error {
	if backup.OriginalHead <= 0 || backup.TargetHead <= backup.OriginalHead || backup.CreatedAtUnix <= 0 {
		return fmt.Errorf("invalid catalog head or creation time")
	}
	decodedSet, err := hex.DecodeString(backup.MigrationSetSHA256)
	if err != nil || len(decodedSet) != sha256.Size || hex.EncodeToString(decodedSet) != backup.MigrationSetSHA256 {
		return fmt.Errorf("invalid catalog migration-set digest")
	}
	return nil
}

func validateStableFileSnapshot(before, after os.FileInfo) error {
	if !os.SameFile(before, after) || before.Size() != after.Size() || before.Mode() != after.Mode() || !before.ModTime().Equal(after.ModTime()) {
		return fmt.Errorf("file identity, size, mode, or modification time changed")
	}
	beforeStat, beforeOK := before.Sys().(*syscall.Stat_t)
	afterStat, afterOK := after.Sys().(*syscall.Stat_t)
	if !beforeOK || !afterOK || beforeStat.Nlink != 1 || afterStat.Nlink != 1 {
		return fmt.Errorf("file must have exactly one hard link")
	}
	return nil
}

func requireBackupSidecarsAbsent(directory *retentionDirectory, basename string) error {
	for _, suffix := range []string{"-wal", "-shm", "-journal"} {
		exists, err := directory.exists(basename + suffix)
		if err != nil {
			return fmt.Errorf("inspect backup sidecar %q: %w", suffix, err)
		}
		if !exists {
			continue
		}
		return fmt.Errorf("backup sidecar %q exists", suffix)
	}
	return nil
}

func backupBasenameForAttempt(attemptID string) string {
	decoded, err := hex.DecodeString(attemptID)
	if err != nil || len(decoded) != 16 || hex.EncodeToString(decoded) != attemptID {
		return ""
	}
	return ".amsftp-backup-v1-" + attemptID + ".sqlite3"
}

func withRetentionTransaction(ctx context.Context, connection *sql.Conn, operation func() error) error {
	if _, err := connection.ExecContext(ctx, "BEGIN IMMEDIATE"); err != nil {
		return fmt.Errorf("reconcile backup retention: begin immediate: %w", err)
	}
	committed := false
	defer func() {
		if !committed {
			_, _ = connection.ExecContext(context.Background(), "ROLLBACK")
		}
	}()
	if err := operation(); err != nil {
		return err
	}
	if _, err := connection.ExecContext(ctx, "COMMIT"); err != nil {
		return fmt.Errorf("reconcile backup retention: commit: %w", err)
	}
	committed = true
	return nil
}

func equalBytes(left, right []byte) bool {
	if len(left) != len(right) {
		return false
	}
	var difference byte
	for index := range left {
		difference |= left[index] ^ right[index]
	}
	return difference == 0
}
