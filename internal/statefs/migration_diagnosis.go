package statefs

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"net/url"
	"path/filepath"

	"github.com/TyrantLucifer/awesome-sftp-cli/internal/state/migration"
)

type MigrationDisposition string

const (
	MigrationDispositionPristine                MigrationDisposition = "pristine"
	MigrationDispositionCurrent                 MigrationDisposition = "current"
	MigrationDispositionUpgradeAvailable        MigrationDisposition = "upgrade_available"
	MigrationDispositionUpgradeReady            MigrationDisposition = "upgrade_ready"
	MigrationDispositionExplicitResumeRequired  MigrationDisposition = "explicit_resume_required"
	MigrationDispositionRestoredBackupHold      MigrationDisposition = "restored_backup_hold"
	MigrationDispositionNewerSchema             MigrationDisposition = "newer_schema"
	MigrationDispositionSidecarRecoveryRequired MigrationDisposition = "sidecar_recovery_required"
)

type MigrationInspectionConfig struct {
	Root            string
	DatabasePath    string
	Migrations      []migration.Migration
	SchemaContracts map[uint64][]byte
}

type MigrationAttemptSummary struct {
	AttemptID          string
	OriginalHead       int64
	CurrentHead        int64
	TargetHead         int64
	Status             migration.AttemptStatus
	ErrorKind          string
	BackupSHA256       string
	BackupBasename     string
	MigrationSetSHA256 string
}

type MigrationInspectionReport struct {
	Disposition      MigrationDisposition
	SchemaHead       uint64
	BinaryTargetHead uint64
	HasSidecars      bool
	HoldAttemptID    string
	ActiveAttempt    *MigrationAttemptSummary
}

// InspectMigrationStateReadOnly classifies persistent migration state without
// opening a recovery-capable or writable SQLite connection. Sidecars are only
// reported because a main-only immutable view cannot safely diagnose them.
func InspectMigrationStateReadOnly(ctx context.Context, config MigrationInspectionConfig) (report MigrationInspectionReport, returnErr error) {
	if config.DatabasePath != filepath.Join(config.Root, filepath.Base(config.DatabasePath)) {
		return report, fmt.Errorf("inspect migration state: database must be a direct child of the state root")
	}
	if _, err := ValidateRoot(config.Root); err != nil {
		return report, fmt.Errorf("inspect migration state: %w", err)
	}
	migrations, contracts, err := resolveCompiledState(config.Migrations, config.SchemaContracts)
	if err != nil {
		return report, fmt.Errorf("inspect migration state: %w", err)
	}
	report.BinaryTargetHead = uint64(len(migrations)) //nolint:gosec // validated continuous migration set
	identity, err := PreflightIdentity(config.DatabasePath)
	if err != nil {
		return report, fmt.Errorf("inspect migration state: %w", err)
	}
	if identity.Kind == IdentityPristine {
		report.Disposition = MigrationDispositionPristine
		return report, nil
	}
	report.HasSidecars = identity.HasSidecars
	if identity.HasSidecars {
		report.Disposition = MigrationDispositionSidecarRecoveryRequired
		return report, nil
	}

	database, err := openImmutableInspectionDatabase(config.DatabasePath)
	if err != nil {
		return report, err
	}
	connection, err := database.Conn(ctx)
	if err != nil {
		_ = database.Close()
		return report, fmt.Errorf("inspect migration state: reserve immutable connection: %w", err)
	}
	defer func() {
		returnErr = errors.Join(returnErr, connection.Close(), database.Close())
	}()
	report.SchemaHead, err = readMigrationHead(ctx, connection)
	if err != nil {
		return report, fmt.Errorf("inspect migration state: %w", err)
	}
	if report.SchemaHead > report.BinaryTargetHead {
		report.Disposition = MigrationDispositionNewerSchema
		return report, nil
	}
	if _, err := validateConnectionState(ctx, connection, migrations, contracts, false, false); err != nil {
		return report, fmt.Errorf("inspect migration state: validate immutable state: %w", err)
	}

	var hold int64
	var reason, holdAttempt sql.NullString
	if err := connection.QueryRowContext(ctx, "SELECT upgrade_hold, hold_reason, hold_attempt_id FROM migration_control WHERE singleton=1").Scan(&hold, &reason, &holdAttempt); err != nil {
		return report, fmt.Errorf("inspect migration state: read migration control: %w", err)
	}
	attempt, attemptErr := migration.LoadAttempt(ctx, connection)
	if attemptErr == nil {
		report.ActiveAttempt = summarizeMigrationAttempt(attempt)
	} else if !errors.Is(attemptErr, migration.ErrNoAttempt) {
		return report, fmt.Errorf("inspect migration state: load active attempt: %w", attemptErr)
	}
	if hold != 0 {
		if !reason.Valid || reason.String != "restored_backup" || !holdAttempt.Valid || report.ActiveAttempt != nil {
			return report, fmt.Errorf("inspect migration state: invalid restored-backup hold")
		}
		report.HoldAttemptID = holdAttempt.String
		report.Disposition = MigrationDispositionRestoredBackupHold
		return report, nil
	}
	if report.ActiveAttempt != nil {
		if report.ActiveAttempt.Status == migration.AttemptReady {
			report.Disposition = MigrationDispositionUpgradeReady
		} else {
			report.Disposition = MigrationDispositionExplicitResumeRequired
		}
		return report, nil
	}
	if report.SchemaHead < report.BinaryTargetHead {
		report.Disposition = MigrationDispositionUpgradeAvailable
	} else {
		report.Disposition = MigrationDispositionCurrent
	}
	return report, nil
}

func openImmutableInspectionDatabase(path string) (*sql.DB, error) {
	uri := &url.URL{Scheme: "file", Path: path}
	query := uri.Query()
	query.Set("immutable", "1")
	query.Set("mode", "ro")
	uri.RawQuery = query.Encode()
	database, err := sql.Open("sqlite", uri.String())
	if err != nil {
		return nil, fmt.Errorf("inspect migration state: open immutable database: %w", err)
	}
	database.SetMaxOpenConns(1)
	return database, nil
}

func summarizeMigrationAttempt(attempt migration.Attempt) *MigrationAttemptSummary {
	summary := &MigrationAttemptSummary{
		AttemptID: attempt.AttemptID, OriginalHead: attempt.OriginalHead, CurrentHead: attempt.CurrentHead,
		TargetHead: attempt.TargetHead, Status: attempt.Status, BackupBasename: attempt.ReservedBackupBasename,
		MigrationSetSHA256: attempt.MigrationSetSHA256,
	}
	if attempt.ErrorKind != nil {
		summary.ErrorKind = *attempt.ErrorKind
	}
	if attempt.BackupSHA256 != nil {
		summary.BackupSHA256 = *attempt.BackupSHA256
	}
	return summary
}
