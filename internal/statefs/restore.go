package statefs

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"github.com/TyrantLucifer/awesome-mac-sftp/internal/state/migration"
)

type RestoreMigrationBackupConfig struct {
	Root                string
	CatalogDatabasePath string
	DestinationPath     string
	BackupAttemptID     string
	Migrations          []migration.Migration
	SchemaContracts     map[uint64][]byte
	restoreFault        func(string)
}

const (
	restoreFaultTempReserved    = "temp_reserved"
	restoreFaultCopyComplete    = "copy_complete"
	restoreFaultTempSynced      = "temp_synced"
	restoreFaultTempVerified    = "temp_verified"
	restoreFaultFinalPublished  = "final_published"
	restoreFaultDirectorySynced = "directory_synced"
)

type RestoreMigrationBackupReport struct {
	BackupAttemptID string
	OriginalHead    uint64
	SHA256          [sha256.Size]byte
	Reused          bool
}

// RestoreMigrationBackup publishes a cataloged, closed, immutable backup to a
// distinct absent database path. It never copies a live database/WAL set and
// never replaces or removes an existing destination.
func RestoreMigrationBackup(ctx context.Context, config RestoreMigrationBackupConfig) (report RestoreMigrationBackupReport, returnErr error) {
	if !exactAttemptID(config.BackupAttemptID) {
		return report, fmt.Errorf("restore migration backup: invalid attempt ID")
	}
	if config.CatalogDatabasePath != filepath.Join(config.Root, filepath.Base(config.CatalogDatabasePath)) ||
		config.DestinationPath != filepath.Join(config.Root, filepath.Base(config.DestinationPath)) ||
		config.CatalogDatabasePath == config.DestinationPath {
		return report, fmt.Errorf("restore migration backup: catalog and destination must be distinct direct children of the state root")
	}
	if _, err := ValidateRoot(config.Root); err != nil {
		return report, fmt.Errorf("restore migration backup: %w", err)
	}
	migrations, contracts, err := resolveCompiledState(config.Migrations, config.SchemaContracts)
	if err != nil {
		return report, fmt.Errorf("restore migration backup: %w", err)
	}
	inspection, err := InspectMigrationStateReadOnly(ctx, MigrationInspectionConfig{
		Root: config.Root, DatabasePath: config.CatalogDatabasePath,
		Migrations: migrations, SchemaContracts: contracts,
	})
	if err != nil {
		return report, fmt.Errorf("restore migration backup: inspect catalog database: %w", err)
	}
	if inspection.HasSidecars || inspection.ActiveAttempt != nil || inspection.HoldAttemptID != "" ||
		(inspection.Disposition != MigrationDispositionCurrent && inspection.Disposition != MigrationDispositionUpgradeAvailable) {
		return report, fmt.Errorf("restore migration backup: catalog database is not a quiescent supported source")
	}
	backup, err := loadRestoreCatalogBackup(ctx, config.CatalogDatabasePath, config.BackupAttemptID, migrations, contracts)
	if err != nil {
		return report, err
	}
	if backup.OriginalHead <= 0 || uint64(backup.OriginalHead) > uint64(len(migrations)) { //nolint:gosec // positivity is checked before conversion
		return report, fmt.Errorf("restore migration backup: catalog original head is unsupported")
	}
	report.BackupAttemptID = backup.AttemptID
	report.OriginalHead = uint64(backup.OriginalHead) //nolint:gosec // positivity checked above
	wantDigest, err := hex.DecodeString(backup.SHA256)
	if err != nil || len(wantDigest) != sha256.Size || hex.EncodeToString(wantDigest) != backup.SHA256 {
		return report, fmt.Errorf("restore migration backup: invalid catalog digest")
	}
	copy(report.SHA256[:], wantDigest)
	expectation := backupExpectation{
		attemptID: backup.AttemptID, originalHead: report.OriginalHead,
		migrations: migrations, schemaContracts: contracts,
	}
	directory, err := openRetentionDirectory(config.Root)
	if err != nil {
		return report, fmt.Errorf("restore migration backup: %w", err)
	}
	defer func() {
		returnErr = errors.Join(returnErr, directory.close())
	}()
	if err := verifyCatalogBackupPresent(directory, backup); err != nil {
		return report, fmt.Errorf("restore migration backup: verify cataloged source: %w", err)
	}
	backupPath := filepath.Join(config.Root, backup.Basename)
	if config.DestinationPath == backupPath {
		return report, fmt.Errorf("restore migration backup: destination must be distinct from the cataloged backup")
	}
	if err := verifyImmutableBackup(ctx, backupPath, expectation); err != nil {
		return report, fmt.Errorf("restore migration backup: verify held source schema: %w", err)
	}

	destinationIdentity, identityErr := PreflightIdentity(config.DestinationPath)
	if identityErr == nil && destinationIdentity.Kind == IdentityProject && !destinationIdentity.HasSidecars {
		if err := verifyRestoredDestination(ctx, config, expectation, report.SHA256); err != nil {
			return report, fmt.Errorf("restore migration backup: destination exists and is not the exact published restore: %w", err)
		}
		report.Reused = true
		return report, nil
	}
	if identityErr != nil {
		return report, fmt.Errorf("restore migration backup: destination is not absent: %w", identityErr)
	}
	if destinationIdentity.Kind != IdentityPristine {
		return report, fmt.Errorf("restore migration backup: destination is not pristine")
	}

	tempBasename := filepath.Base(config.DestinationPath) + ".restore-v1-" + config.BackupAttemptID + ".tmp"
	tempPath := filepath.Join(config.Root, tempBasename)
	if exists, err := pathExists(tempPath); err != nil {
		return report, fmt.Errorf("restore migration backup: inspect temporary destination: %w", err)
	} else if exists {
		for _, suffix := range []string{"-wal", "-shm", "-journal"} {
			if _, err := os.Lstat(tempPath + suffix); !errors.Is(err, os.ErrNotExist) {
				return report, fmt.Errorf("restore migration backup: temporary sidecar %s exists or is unreadable: %w", suffix, err)
			}
		}
		if err := validatePrivateRegular(tempPath); err != nil {
			return report, fmt.Errorf("restore migration backup: validate interrupted temporary destination: %w", err)
		}
		if err := os.Remove(tempPath); err != nil { //nolint:gosec // exact attempt-derived direct child
			return report, fmt.Errorf("restore migration backup: remove interrupted temporary destination: %w", err)
		}
		if err := syncDirectory(config.Root); err != nil {
			return report, fmt.Errorf("restore migration backup: persist temporary cleanup: %w", err)
		}
	}
	temp, err := os.OpenFile(tempPath, os.O_CREATE|os.O_EXCL|os.O_RDWR, 0o600) //nolint:gosec // exact attempt-derived direct child
	if err != nil {
		return report, fmt.Errorf("restore migration backup: reserve temporary destination: %w", err)
	}
	published := false
	tempClosed := false
	defer func() {
		if published {
			return
		}
		var closeErr error
		if !tempClosed {
			closeErr = temp.Close()
		}
		returnErr = errors.Join(returnErr, closeErr, cleanupBackupTemp(tempPath), syncDirectory(config.Root))
	}()
	if err := temp.Chmod(0o600); err != nil {
		return report, fmt.Errorf("restore migration backup: set temporary mode: %w", err)
	}
	triggerRestoreFault(config.restoreFault, restoreFaultTempReserved)
	source, err := directory.open(backup.Basename)
	if err != nil {
		return report, fmt.Errorf("restore migration backup: reopen source without following links: %w", err)
	}
	sourceBefore, err := source.Stat()
	if err != nil {
		_ = source.Close()
		return report, fmt.Errorf("restore migration backup: stat source handle: %w", err)
	}
	matches, err := directory.matches(backup.Basename, sourceBefore)
	if err != nil || !matches {
		_ = source.Close()
		return report, fmt.Errorf("restore migration backup: source identity changed: %w", err)
	}
	hasher := sha256.New()
	_, copyErr := io.Copy(io.MultiWriter(temp, hasher), source)
	sourceAfter, statErr := source.Stat()
	stableErr := statErr
	if stableErr == nil {
		stableErr = validateStableFileSnapshot(sourceBefore, sourceAfter)
	}
	sourceCloseErr := source.Close()
	if err := errors.Join(copyErr, stableErr, sourceCloseErr); err != nil {
		return report, fmt.Errorf("restore migration backup: copy stable source: %w", err)
	}
	if !equalBytes(hasher.Sum(nil), report.SHA256[:]) {
		return report, fmt.Errorf("restore migration backup: copied source digest changed")
	}
	triggerRestoreFault(config.restoreFault, restoreFaultCopyComplete)
	if err := fullSyncFile(temp); err != nil {
		return report, fmt.Errorf("restore migration backup: sync temporary destination: %w", err)
	}
	if err := temp.Close(); err != nil {
		return report, fmt.Errorf("restore migration backup: close temporary destination: %w", err)
	}
	tempClosed = true
	triggerRestoreFault(config.restoreFault, restoreFaultTempSynced)
	if err := verifyImmutableBackup(ctx, tempPath, expectation); err != nil {
		return report, fmt.Errorf("restore migration backup: verify temporary destination: %w", err)
	}
	triggerRestoreFault(config.restoreFault, restoreFaultTempVerified)
	if err := publishNoReplace(tempPath, config.DestinationPath); err != nil {
		return report, fmt.Errorf("restore migration backup: publish without replacement: %w", err)
	}
	published = true
	triggerRestoreFault(config.restoreFault, restoreFaultFinalPublished)
	if err := syncDirectory(config.Root); err != nil {
		return report, fmt.Errorf("restore migration backup: persist publication: %w", err)
	}
	triggerRestoreFault(config.restoreFault, restoreFaultDirectorySynced)
	if err := verifyRestoredDestination(ctx, config, expectation, report.SHA256); err != nil {
		return report, fmt.Errorf("restore migration backup: verify published destination: %w", err)
	}
	return report, nil
}

func triggerRestoreFault(fault func(string), boundary string) {
	if fault != nil {
		fault(boundary)
	}
}

func loadRestoreCatalogBackup(ctx context.Context, path, attemptID string, migrations []migration.Migration, contracts map[uint64][]byte) (catalogBackup, error) {
	database, err := openImmutableInspectionDatabase(path)
	if err != nil {
		return catalogBackup{}, fmt.Errorf("restore migration backup: open catalog: %w", err)
	}
	connection, err := database.Conn(ctx)
	if err != nil {
		_ = database.Close()
		return catalogBackup{}, fmt.Errorf("restore migration backup: reserve catalog connection: %w", err)
	}
	_, validationErr := validateConnectionState(ctx, connection, migrations, contracts, false, false)
	var backup catalogBackup
	var scanErr error
	if validationErr == nil {
		scanErr = backup.scan(connection.QueryRowContext(ctx, "SELECT "+catalogBackupColumns+" FROM migration_backups WHERE attempt_id=? AND status='verified'", attemptID))
	}
	closeConnectionErr := connection.Close()
	closeDatabaseErr := database.Close()
	if err := errors.Join(validationErr, scanErr, closeConnectionErr, closeDatabaseErr); err != nil {
		return catalogBackup{}, fmt.Errorf("restore migration backup: load exact verified catalog row: %w", err)
	}
	return backup, nil
}

func verifyRestoredDestination(ctx context.Context, config RestoreMigrationBackupConfig, expectation backupExpectation, wantDigest [sha256.Size]byte) error {
	identity, err := PreflightIdentity(config.DestinationPath)
	if err != nil {
		return err
	}
	if identity.Kind != IdentityProject || identity.HasSidecars {
		return fmt.Errorf("destination is not a sidecar-free project database")
	}
	digest, err := hashStablePrivateStateFile(config.Root, config.DestinationPath)
	if err != nil {
		return err
	}
	if digest != wantDigest {
		return fmt.Errorf("destination digest does not match catalog")
	}
	return verifyImmutableBackup(ctx, config.DestinationPath, expectation)
}
