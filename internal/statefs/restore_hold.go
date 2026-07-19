package statefs

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"path/filepath"

	"github.com/TyrantLucifer/awesome-mac-sftp/internal/state/migration"
)

type RestoreUpgradeApprovalConfig struct {
	Root            string
	DatabasePath    string
	ExpectedSHA256  [sha256.Size]byte
	HeldAttemptID   string
	NewAttemptID    string
	Random          io.Reader
	Migrations      []migration.Migration
	SchemaContracts map[uint64][]byte
}

// ApproveRestoredBackupForUpgrade verifies one exact held database preimage,
// proves the state filesystem capability, and atomically replaces the restore
// hold with a new preparing attempt. It does not run migrations: the caller
// must explicitly resume the returned attempt, which first creates a new
// verified pre-upgrade backup.
func ApproveRestoredBackupForUpgrade(ctx context.Context, config RestoreUpgradeApprovalConfig) (migration.Attempt, error) {
	if config.ExpectedSHA256 == [sha256.Size]byte{} || !exactAttemptID(config.NewAttemptID) {
		return migration.Attempt{}, fmt.Errorf("approve restored backup: invalid expected digest or new attempt ID")
	}
	inspectionConfig := MigrationInspectionConfig{
		Root: config.Root, DatabasePath: config.DatabasePath,
		Migrations: config.Migrations, SchemaContracts: config.SchemaContracts,
	}
	report, err := InspectMigrationStateReadOnly(ctx, inspectionConfig)
	if err != nil {
		return migration.Attempt{}, fmt.Errorf("approve restored backup: %w", err)
	}
	if report.Disposition != MigrationDispositionRestoredBackupHold || report.HoldAttemptID != config.HeldAttemptID {
		return migration.Attempt{}, fmt.Errorf("approve restored backup: exact restore hold does not match")
	}
	actualDigest, err := hashStablePrivateStateFile(config.Root, config.DatabasePath)
	if err != nil {
		return migration.Attempt{}, fmt.Errorf("approve restored backup: hash held database: %w", err)
	}
	if actualDigest != config.ExpectedSHA256 {
		return migration.Attempt{}, fmt.Errorf("approve restored backup: held database digest changed")
	}
	migrations, contracts, err := resolveCompiledState(config.Migrations, config.SchemaContracts)
	if err != nil {
		return migration.Attempt{}, fmt.Errorf("approve restored backup: %w", err)
	}
	contractDigests, err := migration.SchemaContractDigests(migrations, contracts)
	if err != nil {
		return migration.Attempt{}, fmt.Errorf("approve restored backup: %w", err)
	}
	setDigest, err := migration.MigrationSetDigest(report.SchemaHead, report.BinaryTargetHead, migrations, contractDigests)
	if err != nil {
		return migration.Attempt{}, fmt.Errorf("approve restored backup: freeze new migration set: %w", err)
	}
	request := migration.AttemptRequest{
		AttemptID: config.NewAttemptID, OriginalHead: report.SchemaHead,
		TargetHead: report.BinaryTargetHead, MigrationSetDigest: setDigest,
	}

	if err := ProbeAfterIdentity(ctx, config.Root, ProbeConfig{Random: config.Random}); err != nil {
		return migration.Attempt{}, fmt.Errorf("approve restored backup: %w", err)
	}
	revalidated, err := InspectMigrationStateReadOnly(ctx, inspectionConfig)
	if err != nil {
		return migration.Attempt{}, fmt.Errorf("approve restored backup: revalidate after capability probe: %w", err)
	}
	if revalidated.Disposition != report.Disposition || revalidated.SchemaHead != report.SchemaHead || revalidated.HoldAttemptID != report.HoldAttemptID {
		return migration.Attempt{}, fmt.Errorf("approve restored backup: held state changed after capability probe")
	}
	actualDigest, err = hashStablePrivateStateFile(config.Root, config.DatabasePath)
	if err != nil || actualDigest != config.ExpectedSHA256 {
		return migration.Attempt{}, fmt.Errorf("approve restored backup: held database identity changed after capability probe: %w", err)
	}

	database, err := openRuntimeDatabase(ctx, config.DatabasePath, migrations, contracts)
	if err != nil {
		return migration.Attempt{}, fmt.Errorf("approve restored backup: %w", err)
	}
	connection, err := database.Conn(ctx)
	if err != nil {
		_ = database.Close()
		return migration.Attempt{}, fmt.Errorf("approve restored backup: reserve transition connection: %w", err)
	}
	attempt, transitionErr := migration.BeginAttemptFromRestoreHold(ctx, connection, config.HeldAttemptID, request)
	checksErr := transitionErr
	if checksErr == nil {
		checksErr = finalConnectionChecks(ctx, connection)
	}
	closeConnectionErr := connection.Close()
	closeDatabaseErr := database.Close()
	if err := errors.Join(checksErr, closeConnectionErr, closeDatabaseErr); err != nil {
		return migration.Attempt{}, fmt.Errorf("approve restored backup: persist preparing transition: %w", err)
	}
	if err := requireDatabaseSidecarsAbsent(config.DatabasePath); err != nil {
		return migration.Attempt{}, fmt.Errorf("approve restored backup: %w", err)
	}
	finalReport, err := InspectMigrationStateReadOnly(ctx, inspectionConfig)
	if err != nil {
		return migration.Attempt{}, fmt.Errorf("approve restored backup: validate preparing transition: %w", err)
	}
	if finalReport.Disposition != MigrationDispositionExplicitResumeRequired || finalReport.ActiveAttempt == nil || finalReport.ActiveAttempt.AttemptID != config.NewAttemptID || finalReport.HoldAttemptID != "" {
		return migration.Attempt{}, fmt.Errorf("approve restored backup: preparing transition did not persist exactly")
	}
	return attempt, nil
}

func exactAttemptID(value string) bool {
	decoded, err := hex.DecodeString(value)
	return err == nil && len(decoded) == 16 && hex.EncodeToString(decoded) == value
}

func hashStablePrivateStateFile(root, path string) ([sha256.Size]byte, error) {
	var digest [sha256.Size]byte
	if path != filepath.Join(root, filepath.Base(path)) {
		return digest, fmt.Errorf("database must be a direct child of the state root")
	}
	if err := validatePrivateRegular(path); err != nil {
		return digest, err
	}
	directory, err := openRetentionDirectory(root)
	if err != nil {
		return digest, err
	}
	defer directory.file.Close()
	basename := filepath.Base(path)
	file, err := directory.open(basename)
	if err != nil {
		return digest, fmt.Errorf("open database without following links: %w", err)
	}
	before, err := file.Stat()
	if err != nil {
		_ = file.Close()
		return digest, fmt.Errorf("stat database handle: %w", err)
	}
	matches, err := directory.matches(basename, before)
	if err != nil || !matches {
		_ = file.Close()
		return digest, fmt.Errorf("database path identity changed: %w", err)
	}
	hasher := sha256.New()
	if _, err := io.Copy(hasher, file); err != nil {
		_ = file.Close()
		return digest, fmt.Errorf("hash database: %w", err)
	}
	after, err := file.Stat()
	if err != nil {
		_ = file.Close()
		return digest, fmt.Errorf("restat database handle: %w", err)
	}
	if err := validateStableFileSnapshot(before, after); err != nil {
		_ = file.Close()
		return digest, fmt.Errorf("database changed while hashing: %w", err)
	}
	if err := file.Close(); err != nil {
		return digest, fmt.Errorf("close database handle: %w", err)
	}
	if err := directory.validateRootIdentity(); err != nil {
		return digest, err
	}
	copy(digest[:], hasher.Sum(nil))
	return digest, nil
}
