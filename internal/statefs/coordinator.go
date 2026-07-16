package statefs

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"time"

	"github.com/TyrantLucifer/awesome-mac-sftp/internal/state/migration"
)

var ErrExplicitMigrationResumeRequired = errors.New("explicit migration resume required")

type UpgradeConfig struct {
	Root            string
	DatabasePath    string
	Database        *sql.DB
	Migrations      []migration.Migration
	SchemaContracts map[uint64][]byte
	AttemptID       string
	Now             time.Time
	ExplicitResume  bool
}

type UpgradeReport struct {
	OriginalHead   uint64
	TargetHead     uint64
	Applied        int
	BackupBasename string
	Retention      RetentionReport
}

// UpgradeDatabase coordinates one frozen original..target migration attempt.
// The caller must keep business readers and writers quiesced for the call.
func UpgradeDatabase(ctx context.Context, config UpgradeConfig) (report UpgradeReport, returnErr error) {
	if config.Database == nil || config.Root == "" || config.DatabasePath == "" || config.Now.Unix() <= 0 {
		return report, fmt.Errorf("upgrade database: invalid configuration")
	}
	if config.DatabasePath != filepathDirectChild(config.Root, config.DatabasePath) {
		return report, fmt.Errorf("upgrade database: database must be a direct child of the state root")
	}
	if _, err := ValidateRoot(config.Root); err != nil {
		return report, fmt.Errorf("upgrade database: %w", err)
	}
	contractDigests, err := migration.SchemaContractDigests(config.Migrations, config.SchemaContracts)
	if err != nil {
		return report, fmt.Errorf("upgrade database: %w", err)
	}
	targetHead := uint64(len(config.Migrations)) //nolint:gosec // ValidateSet bounds the continuous migration set
	connection, err := config.Database.Conn(ctx)
	if err != nil {
		return report, fmt.Errorf("upgrade database: reserve coordinator connection: %w", err)
	}
	defer func() {
		returnErr = errors.Join(returnErr, connection.Close())
	}()
	currentHead, err := readMigrationHead(ctx, connection)
	if err != nil {
		return report, err
	}
	if currentHead > targetHead {
		return report, fmt.Errorf("upgrade database: schema head %d is newer than binary target %d", currentHead, targetHead)
	}
	if err := migration.ValidateHead(ctx, connection, config.Migrations, config.SchemaContracts, currentHead); err != nil {
		return report, fmt.Errorf("upgrade database: validate current head: %w", err)
	}
	if err := requireUpgradeHoldClear(ctx, connection); err != nil {
		return report, err
	}
	report.OriginalHead = currentHead
	report.TargetHead = targetHead
	if currentHead == targetHead {
		if _, err := migration.LoadAttempt(ctx, connection); !errors.Is(err, migration.ErrNoAttempt) {
			if err == nil {
				err = fmt.Errorf("active attempt remains at target head")
			}
			return report, fmt.Errorf("upgrade database: %w", err)
		}
		return report, nil
	}

	attempt, loadErr := migration.LoadAttempt(ctx, connection)
	freshAttempt := false
	var request migration.AttemptRequest
	if errors.Is(loadErr, migration.ErrNoAttempt) {
		attemptID := config.AttemptID
		if attemptID == "" {
			attemptID, err = newMigrationAttemptID()
			if err != nil {
				return report, fmt.Errorf("upgrade database: generate attempt ID: %w", err)
			}
		}
		setDigest, err := migration.MigrationSetDigest(currentHead, targetHead, config.Migrations, contractDigests)
		if err != nil {
			return report, fmt.Errorf("upgrade database: freeze migration set: %w", err)
		}
		request = migration.AttemptRequest{
			AttemptID: attemptID, OriginalHead: currentHead, TargetHead: targetHead, MigrationSetDigest: setDigest,
		}
		pending := config.Migrations[currentHead:targetHead]
		if _, err := ReconcileBackupRetentionAfterSchemaValidation(ctx, connection, config.Root, int64(currentHead)); err != nil { //nolint:gosec // currentHead is a validated SQLite signed head
			return report, fmt.Errorf("upgrade database: pre-attempt retention: %w", err)
		}
		if _, err := CheckMigrationSpace(ctx, connection, config.Root, pending); err != nil {
			return report, fmt.Errorf("upgrade database: migration space gate: %w", err)
		}
		attempt, _, err = migration.PrepareAttempt(ctx, connection, request)
		if err != nil {
			return report, fmt.Errorf("upgrade database: prepare attempt: %w", err)
		}
		freshAttempt = true
	} else if loadErr != nil {
		return report, fmt.Errorf("upgrade database: load attempt: %w", loadErr)
	} else {
		request, err = validateFrozenAttempt(config, contractDigests, currentHead, targetHead, attempt)
		if err != nil {
			return report, err
		}
		report.OriginalHead = uint64(attempt.OriginalHead) //nolint:gosec // validateFrozenAttempt requires a positive signed head
		pending := config.Migrations[currentHead:targetHead]
		if _, err := CheckMigrationSpace(ctx, connection, config.Root, pending); err != nil {
			return report, fmt.Errorf("upgrade database: resumed migration space gate: %w", err)
		}
		if attempt.BackupSHA256 != nil {
			if err := verifyResumableAttemptBackup(ctx, connection, config, attempt); err != nil {
				return report, err
			}
		}
		if attempt.Status != migration.AttemptReady {
			if !config.ExplicitResume {
				return report, fmt.Errorf("upgrade database: attempt status %q: %w", attempt.Status, ErrExplicitMigrationResumeRequired)
			}
			attempt, err = migration.RearmAttemptAfterExplicitValidation(ctx, connection, request)
			if err != nil {
				return report, fmt.Errorf("upgrade database: rearm explicitly validated attempt: %w", err)
			}
			freshAttempt = attempt.Status == migration.AttemptPreparing
		}
	}

	if freshAttempt {
		_, _, err := CreateMigrationBackup(ctx, BackupConfig{
			Root: config.Root, Source: config.Database, Attempt: attempt, CreatedAt: config.Now,
			Migrations: config.Migrations, SchemaContracts: config.SchemaContracts,
		})
		if err != nil {
			return report, fmt.Errorf("upgrade database: create pre-upgrade backup: %w", err)
		}
		attempt, err = migration.LoadAttempt(ctx, connection)
		if err != nil {
			return report, fmt.Errorf("upgrade database: reload ready attempt: %w", err)
		}
	}
	if attempt.Status != migration.AttemptReady {
		return report, fmt.Errorf("upgrade database: attempt status %q is not ready", attempt.Status)
	}
	report.BackupBasename = attempt.ReservedBackupBasename
	attempt, err = migration.MarkAttemptRunning(ctx, connection, attempt.AttemptID)
	if err != nil {
		return report, fmt.Errorf("upgrade database: mark attempt running: %w", err)
	}
	for version := uint64(attempt.CurrentHead) + 1; version <= targetHead; version++ { //nolint:gosec // current head is validated positive and bounded by target
		item := config.Migrations[version-1]
		appliedAt := config.Now.Add(time.Duration(report.Applied) * time.Nanosecond).UTC().Format(time.RFC3339Nano)
		if err := (migration.Runner{AttemptID: attempt.AttemptID}).Apply(ctx, connection, item, appliedAt); err != nil {
			markerErr := markUpgradeFailure(ctx, connection, attempt.AttemptID, err)
			return report, fmt.Errorf("upgrade database: apply version %d: %w", version, errors.Join(err, markerErr))
		}
		report.Applied++
		if err := migration.ValidateHead(ctx, connection, config.Migrations, config.SchemaContracts, version); err != nil {
			_, markerErr := migration.MarkAttemptInterrupted(ctx, connection, attempt.AttemptID, "contract_mismatch")
			return report, fmt.Errorf("upgrade database: validate committed version %d: %w", version, errors.Join(err, markerErr))
		}
	}
	if err := migration.ClearCompletedAttempt(ctx, connection, request); err != nil {
		return report, fmt.Errorf("upgrade database: clear completed attempt: %w", err)
	}
	if err := migration.ValidateHead(ctx, connection, config.Migrations, config.SchemaContracts, targetHead); err != nil {
		return report, fmt.Errorf("upgrade database: validate target head: %w", err)
	}
	report.Retention, err = ReconcileBackupRetentionAfterSchemaValidation(ctx, connection, config.Root, int64(targetHead)) //nolint:gosec // target validated by migration set
	if err != nil {
		return report, fmt.Errorf("upgrade database: retention: %w", err)
	}
	if err := finalConnectionChecks(ctx, connection); err != nil {
		return report, fmt.Errorf("upgrade database: final checks: %w", err)
	}
	return report, nil
}

func newMigrationAttemptID() (string, error) {
	var value [16]byte
	if _, err := io.ReadFull(rand.Reader, value[:]); err != nil {
		return "", err
	}
	return hex.EncodeToString(value[:]), nil
}

func filepathDirectChild(root, path string) string {
	return filepath.Join(root, filepath.Base(path))
}

func readMigrationHead(ctx context.Context, connection *sql.Conn) (uint64, error) {
	var rows, head int64
	if err := connection.QueryRowContext(ctx, "SELECT count(*), COALESCE(max(version), 0) FROM schema_migrations").Scan(&rows, &head); err != nil {
		return 0, fmt.Errorf("upgrade database: read migration head: %w", err)
	}
	if rows <= 0 || head <= 0 || rows != head {
		return 0, fmt.Errorf("upgrade database: invalid history rows/head %d/%d", rows, head)
	}
	return uint64(head), nil //nolint:gosec // positivity checked above
}

func requireUpgradeHoldClear(ctx context.Context, connection *sql.Conn) error {
	var rows int64
	if err := connection.QueryRowContext(ctx, "SELECT count(*) FROM migration_control WHERE singleton=1 AND upgrade_hold=0 AND hold_reason IS NULL AND hold_attempt_id IS NULL").Scan(&rows); err != nil {
		return fmt.Errorf("upgrade database: read upgrade hold: %w", err)
	}
	if rows != 1 {
		return fmt.Errorf("upgrade database: restored-backup hold is active or invalid")
	}
	return nil
}

func validateFrozenAttempt(config UpgradeConfig, contractDigests map[uint64][sha256.Size]byte, currentHead, targetHead uint64, attempt migration.Attempt) (migration.AttemptRequest, error) {
	if attempt.OriginalHead <= 0 || attempt.CurrentHead <= 0 || attempt.TargetHead <= 0 {
		return migration.AttemptRequest{}, fmt.Errorf("upgrade database: active attempt has invalid heads")
	}
	originalHead := uint64(attempt.OriginalHead)  //nolint:gosec // positivity checked above
	attemptCurrent := uint64(attempt.CurrentHead) //nolint:gosec // positivity checked above
	attemptTarget := uint64(attempt.TargetHead)   //nolint:gosec // positivity checked above
	if attemptCurrent != currentHead || attemptTarget != targetHead {
		return migration.AttemptRequest{}, fmt.Errorf("upgrade database: active attempt head/target does not match database and binary")
	}
	digest, err := migration.MigrationSetDigest(originalHead, attemptTarget, config.Migrations, contractDigests)
	if err != nil {
		return migration.AttemptRequest{}, fmt.Errorf("upgrade database: recompute frozen migration set: %w", err)
	}
	request := migration.AttemptRequest{AttemptID: attempt.AttemptID, OriginalHead: originalHead, TargetHead: attemptTarget, MigrationSetDigest: digest}
	if attempt.MigrationSetSHA256 != fmt.Sprintf("%x", digest) {
		return migration.AttemptRequest{}, fmt.Errorf("upgrade database: active attempt migration-set digest changed")
	}
	return request, nil
}

func markUpgradeFailure(ctx context.Context, connection *sql.Conn, attemptID string, applyErr error) error {
	var committedViolation *migration.CommittedMigrationWALViolation
	if errors.As(applyErr, &committedViolation) {
		_, err := migration.MarkAttemptInterrupted(ctx, connection, attemptID, "wal_committed_growth")
		return err
	}
	_, err := migration.MarkAttemptFailed(ctx, connection, attemptID, "migration_failed")
	return err
}

func verifyResumableAttemptBackup(ctx context.Context, connection *sql.Conn, config UpgradeConfig, attempt migration.Attempt) error {
	if attempt.BackupSHA256 == nil {
		return fmt.Errorf("upgrade database: resumable attempt has no backup digest")
	}
	expectation, err := resolveBackupExpectation(BackupConfig{
		Attempt: attempt, Migrations: config.Migrations, SchemaContracts: config.SchemaContracts,
	})
	if err != nil {
		return fmt.Errorf("upgrade database: validate resumable backup expectation: %w", err)
	}
	path := filepath.Join(config.Root, attempt.ReservedBackupBasename)
	if err := validatePrivateRegular(path); err != nil {
		return fmt.Errorf("upgrade database: validate resumable backup: %w", err)
	}
	if _, err := os.Lstat(path + ".tmp"); !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("upgrade database: resumable backup temporary path is present or unreadable: %w", err)
	}
	for _, suffix := range []string{"-wal", "-shm", "-journal"} {
		if _, err := os.Lstat(path + suffix); !errors.Is(err, os.ErrNotExist) {
			return fmt.Errorf("upgrade database: resumable backup sidecar %s is present or unreadable: %w", suffix, err)
		}
	}
	if err := verifyImmutableBackup(ctx, path, expectation); err != nil {
		return fmt.Errorf("upgrade database: verify immutable resumable backup contents: %w", err)
	}
	file, err := os.Open(path) //nolint:gosec // exact validated attempt-derived backup path
	if err != nil {
		return fmt.Errorf("upgrade database: open resumable backup: %w", err)
	}
	hasher := sha256.New()
	_, hashErr := io.Copy(hasher, file)
	closeErr := file.Close()
	if err := errors.Join(hashErr, closeErr); err != nil {
		return fmt.Errorf("upgrade database: hash resumable backup: %w", err)
	}
	actualDigest := fmt.Sprintf("%x", hasher.Sum(nil))
	if actualDigest != *attempt.BackupSHA256 {
		return fmt.Errorf("upgrade database: resumable backup digest changed")
	}
	var catalogRows int64
	if err := connection.QueryRowContext(ctx, "SELECT count(*) FROM migration_backups WHERE attempt_id=? AND original_head=? AND target_head=? AND migration_set_sha256=? AND backup_basename=? AND backup_sha256=? AND status='verified'", attempt.AttemptID, attempt.OriginalHead, attempt.TargetHead, attempt.MigrationSetSHA256, attempt.ReservedBackupBasename, *attempt.BackupSHA256).Scan(&catalogRows); err != nil {
		return fmt.Errorf("upgrade database: read resumable backup catalog: %w", err)
	}
	if catalogRows != 1 {
		return fmt.Errorf("upgrade database: resumable backup catalog rows = %d, want 1", catalogRows)
	}
	return nil
}

func finalConnectionChecks(ctx context.Context, connection *sql.Conn) error {
	var quick string
	if err := connection.QueryRowContext(ctx, "PRAGMA quick_check").Scan(&quick); err != nil || quick != "ok" {
		return fmt.Errorf("quick_check = %q: %w", quick, err)
	}
	rows, err := connection.QueryContext(ctx, "PRAGMA foreign_key_check")
	if err != nil {
		return fmt.Errorf("foreign_key_check: %w", err)
	}
	violated := rows.Next()
	if err := errors.Join(rows.Err(), rows.Close()); err != nil {
		return fmt.Errorf("foreign_key_check rows: %w", err)
	}
	if violated {
		return fmt.Errorf("foreign_key_check returned a violation")
	}
	var busy, logFrames, checkpointed int64
	if err := connection.QueryRowContext(ctx, "PRAGMA wal_checkpoint(TRUNCATE)").Scan(&busy, &logFrames, &checkpointed); err != nil {
		return fmt.Errorf("truncate WAL: %w", err)
	}
	if busy != 0 || logFrames != 0 || checkpointed != 0 {
		return fmt.Errorf("truncate WAL result = (%d,%d,%d), want (0,0,0)", busy, logFrames, checkpointed)
	}
	return nil
}
