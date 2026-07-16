package migration

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"regexp"
	"time"
)

type AttemptStatus string

const (
	AttemptPreparing   AttemptStatus = "preparing"
	AttemptReady       AttemptStatus = "ready"
	AttemptRunning     AttemptStatus = "running"
	AttemptInterrupted AttemptStatus = "interrupted"
	AttemptFailed      AttemptStatus = "failed"
)

type RestartAction string

const (
	RestartAutoContinue       RestartAction = "auto_continue"
	RestartHoldExplicitResume RestartAction = "hold_explicit_resume"
)

var attemptIDPattern = regexp.MustCompile(`^[0-9a-f]{32}$`)
var attemptErrorKindPattern = regexp.MustCompile(`^[a-z][a-z0-9_]{0,63}$`)

var ErrNoAttempt = errors.New("no active migration attempt")

type AttemptRequest struct {
	AttemptID          string
	OriginalHead       uint64
	TargetHead         uint64
	MigrationSetDigest [sha256.Size]byte
}

type Attempt struct {
	AttemptID              string
	OriginalHead           int64
	CurrentHead            int64
	TargetHead             int64
	MigrationSetSHA256     string
	ReservedBackupBasename string
	BackupSHA256           *string
	Status                 AttemptStatus
	ErrorKind              *string
}

func PrepareAttempt(ctx context.Context, connection *sql.Conn, request AttemptRequest) (Attempt, bool, error) {
	if connection == nil {
		return Attempt{}, false, fmt.Errorf("prepare migration attempt: nil connection")
	}
	if err := validateAttemptRequest(request); err != nil {
		return Attempt{}, false, err
	}
	var result Attempt
	reused := false
	err := withImmediate(ctx, connection, func() error {
		var hold int64
		if err := connection.QueryRowContext(ctx, "SELECT upgrade_hold FROM migration_control WHERE singleton=1").Scan(&hold); err != nil {
			return fmt.Errorf("prepare migration attempt: read upgrade hold: %w", err)
		}
		if hold != 0 {
			return fmt.Errorf("prepare migration attempt: restored backup hold is active")
		}
		existing, err := loadAttempt(ctx, connection)
		if err == nil {
			wantDigest := hex.EncodeToString(request.MigrationSetDigest[:])
			// validateAttemptRequest bounds both heads to SQLite's signed range.
			originalHead := int64(request.OriginalHead) //nolint:gosec
			targetHead := int64(request.TargetHead)     //nolint:gosec
			if existing.AttemptID != request.AttemptID || existing.OriginalHead != originalHead || existing.TargetHead != targetHead || existing.MigrationSetSHA256 != wantDigest {
				return fmt.Errorf("prepare migration attempt: active attempt does not match the frozen request")
			}
			result = existing
			reused = true
			return nil
		}
		if !errors.Is(err, ErrNoAttempt) {
			return err
		}
		basename := ".amsftp-backup-v1-" + request.AttemptID + ".sqlite3"
		if _, err := connection.ExecContext(ctx, "INSERT INTO migration_attempts(singleton, attempt_id, original_head, current_head, target_head, migration_set_sha256, reserved_backup_basename, backup_sha256, status, error_kind) VALUES(1, ?, ?, ?, ?, ?, ?, NULL, 'preparing', NULL)", request.AttemptID, request.OriginalHead, request.OriginalHead, request.TargetHead, hex.EncodeToString(request.MigrationSetDigest[:]), basename); err != nil {
			return fmt.Errorf("prepare migration attempt: insert singleton: %w", err)
		}
		result, err = loadAttempt(ctx, connection)
		return err
	})
	if err != nil {
		return Attempt{}, false, err
	}
	return result, reused, nil
}

func LoadAttempt(ctx context.Context, connection *sql.Conn) (Attempt, error) {
	if connection == nil {
		return Attempt{}, fmt.Errorf("load migration attempt: nil connection")
	}
	return loadAttempt(ctx, connection)
}

func RecordVerifiedBackup(ctx context.Context, connection *sql.Conn, attemptID string, backupDigest [sha256.Size]byte, createdAt time.Time) (Attempt, error) {
	if connection == nil || !attemptIDPattern.MatchString(attemptID) || backupDigest == [sha256.Size]byte{} || createdAt.Unix() <= 0 {
		return Attempt{}, fmt.Errorf("record migration backup: invalid input")
	}
	var result Attempt
	err := withImmediate(ctx, connection, func() error {
		attempt, err := loadAttempt(ctx, connection)
		if err != nil {
			return err
		}
		if attempt.AttemptID != attemptID || attempt.Status != AttemptPreparing || attempt.CurrentHead != attempt.OriginalHead || attempt.BackupSHA256 != nil {
			return fmt.Errorf("record migration backup: attempt is not an unbacked preparing singleton")
		}
		backupHex := hex.EncodeToString(backupDigest[:])
		if _, err := connection.ExecContext(ctx, "INSERT INTO migration_backups(attempt_id, original_head, target_head, migration_set_sha256, backup_basename, backup_sha256, created_at_unix, status) VALUES(?, ?, ?, ?, ?, ?, ?, 'verified')", attempt.AttemptID, attempt.OriginalHead, attempt.TargetHead, attempt.MigrationSetSHA256, attempt.ReservedBackupBasename, backupHex, createdAt.Unix()); err != nil {
			return fmt.Errorf("record migration backup: insert catalog: %w", err)
		}
		updated, err := connection.ExecContext(ctx, "UPDATE migration_attempts SET backup_sha256=?, status='ready', error_kind=NULL WHERE singleton=1 AND attempt_id=? AND status='preparing' AND backup_sha256 IS NULL", backupHex, attemptID)
		if err != nil {
			return fmt.Errorf("record migration backup: mark ready: %w", err)
		}
		rows, err := updated.RowsAffected()
		if err != nil || rows != 1 {
			return fmt.Errorf("record migration backup: expected one preparing attempt")
		}
		result, err = loadAttempt(ctx, connection)
		return err
	})
	if err != nil {
		return Attempt{}, err
	}
	return result, nil
}

func MarkAttemptRunning(ctx context.Context, connection *sql.Conn, attemptID string) (Attempt, error) {
	if connection == nil || !attemptIDPattern.MatchString(attemptID) {
		return Attempt{}, fmt.Errorf("mark migration attempt running: invalid input")
	}
	var result Attempt
	err := withImmediate(ctx, connection, func() error {
		updated, err := connection.ExecContext(ctx, "UPDATE migration_attempts SET status='running', error_kind=NULL WHERE singleton=1 AND attempt_id=? AND status='ready' AND backup_sha256 IS NOT NULL", attemptID)
		if err != nil {
			return fmt.Errorf("mark migration attempt running: %w", err)
		}
		rows, err := updated.RowsAffected()
		if err != nil || rows != 1 {
			return fmt.Errorf("mark migration attempt running: expected one ready attempt")
		}
		result, err = loadAttempt(ctx, connection)
		return err
	})
	if err != nil {
		return Attempt{}, err
	}
	return result, nil
}

func MarkAttemptFailed(ctx context.Context, connection *sql.Conn, attemptID, errorKind string) (Attempt, error) {
	if connection == nil || !attemptIDPattern.MatchString(attemptID) || !attemptErrorKindPattern.MatchString(errorKind) {
		return Attempt{}, fmt.Errorf("mark migration attempt failed: invalid input")
	}
	var result Attempt
	err := withImmediate(ctx, connection, func() error {
		updated, err := connection.ExecContext(ctx, "UPDATE migration_attempts SET status='failed', error_kind=? WHERE singleton=1 AND attempt_id=? AND status IN ('preparing', 'ready', 'running', 'interrupted') AND ((backup_sha256 IS NULL AND current_head=original_head) OR backup_sha256 IS NOT NULL)", errorKind, attemptID)
		if err != nil {
			return fmt.Errorf("mark migration attempt failed: %w", err)
		}
		rows, err := updated.RowsAffected()
		if err != nil {
			return fmt.Errorf("mark migration attempt failed: read row count: %w", err)
		}
		if rows != 1 {
			return fmt.Errorf("mark migration attempt failed: expected one recoverable active attempt")
		}
		result, err = loadAttempt(ctx, connection)
		return err
	})
	if err != nil {
		return Attempt{}, err
	}
	return result, nil
}

func MarkAttemptInterrupted(ctx context.Context, connection *sql.Conn, attemptID, errorKind string) (Attempt, error) {
	if connection == nil || !attemptIDPattern.MatchString(attemptID) || !attemptErrorKindPattern.MatchString(errorKind) {
		return Attempt{}, fmt.Errorf("mark migration attempt interrupted: invalid input")
	}
	var result Attempt
	err := withImmediate(ctx, connection, func() error {
		updated, err := connection.ExecContext(ctx, "UPDATE migration_attempts SET status='interrupted', error_kind=? WHERE singleton=1 AND attempt_id=? AND status='running' AND backup_sha256 IS NOT NULL", errorKind, attemptID)
		if err != nil {
			return fmt.Errorf("mark migration attempt interrupted: %w", err)
		}
		rows, err := updated.RowsAffected()
		if err != nil {
			return fmt.Errorf("mark migration attempt interrupted: read row count: %w", err)
		}
		if rows != 1 {
			return fmt.Errorf("mark migration attempt interrupted: expected one running attempt")
		}
		result, err = loadAttempt(ctx, connection)
		return err
	})
	if err != nil {
		return Attempt{}, err
	}
	return result, nil
}

// RearmAttemptAfterExplicitValidation performs only the durable state change
// after the coordinator has revalidated source identity, history/schema,
// frozen set digest, current prefix, and the exact verified backup.
func RearmAttemptAfterExplicitValidation(ctx context.Context, connection *sql.Conn, request AttemptRequest) (Attempt, error) {
	if connection == nil {
		return Attempt{}, fmt.Errorf("rearm migration attempt: nil connection")
	}
	if err := validateAttemptRequest(request); err != nil {
		return Attempt{}, fmt.Errorf("rearm migration attempt: %w", err)
	}
	var result Attempt
	err := withImmediate(ctx, connection, func() error {
		attempt, err := loadAttempt(ctx, connection)
		if err != nil {
			return err
		}
		wantDigest := hex.EncodeToString(request.MigrationSetDigest[:])
		if attempt.AttemptID != request.AttemptID || attempt.OriginalHead != int64(request.OriginalHead) || attempt.TargetHead != int64(request.TargetHead) || attempt.MigrationSetSHA256 != wantDigest {
			return fmt.Errorf("rearm migration attempt: active attempt does not match revalidated frozen request")
		}
		var next AttemptStatus
		switch {
		case attempt.BackupSHA256 == nil && attempt.CurrentHead == attempt.OriginalHead && (attempt.Status == AttemptPreparing || attempt.Status == AttemptFailed):
			next = AttemptPreparing
		case attempt.BackupSHA256 != nil && (attempt.Status == AttemptRunning || attempt.Status == AttemptInterrupted || attempt.Status == AttemptFailed):
			next = AttemptReady
		default:
			return fmt.Errorf("rearm migration attempt: status %q and backup state are not explicitly resumable", attempt.Status)
		}
		updated, err := connection.ExecContext(ctx, "UPDATE migration_attempts SET status=?, error_kind=NULL WHERE singleton=1 AND attempt_id=? AND status=?", next, request.AttemptID, attempt.Status)
		if err != nil {
			return fmt.Errorf("rearm migration attempt: %w", err)
		}
		rows, err := updated.RowsAffected()
		if err != nil {
			return fmt.Errorf("rearm migration attempt: read row count: %w", err)
		}
		if rows != 1 {
			return fmt.Errorf("rearm migration attempt: active attempt changed")
		}
		result, err = loadAttempt(ctx, connection)
		return err
	})
	if err != nil {
		return Attempt{}, err
	}
	return result, nil
}

func ClearCompletedAttempt(ctx context.Context, connection *sql.Conn, attemptID string) error {
	if connection == nil || !attemptIDPattern.MatchString(attemptID) {
		return fmt.Errorf("clear completed migration attempt: invalid input")
	}
	return withImmediate(ctx, connection, func() error {
		deleted, err := connection.ExecContext(ctx, "DELETE FROM migration_attempts WHERE singleton=1 AND attempt_id=? AND status='running' AND backup_sha256 IS NOT NULL AND current_head=target_head", attemptID)
		if err != nil {
			return fmt.Errorf("clear completed migration attempt: %w", err)
		}
		rows, err := deleted.RowsAffected()
		if err != nil {
			return fmt.Errorf("clear completed migration attempt: read row count: %w", err)
		}
		if rows != 1 {
			return fmt.Errorf("clear completed migration attempt: expected one fully migrated running attempt")
		}
		return nil
	})
}

func RestartDecision(attempt Attempt) RestartAction {
	if attempt.Status == AttemptReady {
		return RestartAutoContinue
	}
	return RestartHoldExplicitResume
}

func loadAttempt(ctx context.Context, queryer interface {
	QueryRowContext(context.Context, string, ...any) *sql.Row
}) (Attempt, error) {
	var attempt Attempt
	var backup, errorKind sql.NullString
	err := queryer.QueryRowContext(ctx, "SELECT attempt_id, original_head, current_head, target_head, migration_set_sha256, reserved_backup_basename, backup_sha256, status, error_kind FROM migration_attempts WHERE singleton=1").Scan(
		&attempt.AttemptID, &attempt.OriginalHead, &attempt.CurrentHead, &attempt.TargetHead, &attempt.MigrationSetSHA256,
		&attempt.ReservedBackupBasename, &backup, &attempt.Status, &errorKind,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return Attempt{}, ErrNoAttempt
	}
	if err != nil {
		return Attempt{}, fmt.Errorf("load migration attempt: %w", err)
	}
	if backup.Valid {
		value := backup.String
		attempt.BackupSHA256 = &value
	}
	if errorKind.Valid {
		value := errorKind.String
		attempt.ErrorKind = &value
	}
	return attempt, nil
}

func validateAttemptRequest(request AttemptRequest) error {
	if !attemptIDPattern.MatchString(request.AttemptID) || request.OriginalHead == 0 || request.OriginalHead >= request.TargetHead || request.TargetHead > maxMigrationVersion || request.MigrationSetDigest == [sha256.Size]byte{} {
		return fmt.Errorf("prepare migration attempt: invalid frozen request")
	}
	return nil
}

func withImmediate(ctx context.Context, connection *sql.Conn, operation func() error) error {
	if connection == nil || operation == nil {
		return fmt.Errorf("migration attempt: nil connection or operation")
	}
	if _, err := connection.ExecContext(ctx, "BEGIN IMMEDIATE"); err != nil {
		return fmt.Errorf("migration attempt: begin immediate: %w", err)
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
		return fmt.Errorf("migration attempt: commit: %w", err)
	}
	committed = true
	return nil
}
