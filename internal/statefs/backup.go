package statefs

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"database/sql/driver"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net/url"
	"os"
	"path/filepath"
	"time"

	"github.com/TyrantLucifer/awesome-mac-sftp/internal/state/migration"
	moderncsqlite "modernc.org/sqlite"
)

type BackupConfig struct {
	Root            string
	Source          *sql.DB
	Attempt         migration.Attempt
	CreatedAt       time.Time
	Migrations      []migration.Migration
	SchemaContracts map[uint64][]byte
}

type backupExpectation struct {
	attemptID       string
	originalHead    uint64
	migrations      []migration.Migration
	schemaContracts map[uint64][]byte
}

type backupSourceConnection interface {
	NewBackup(string) (*moderncsqlite.Backup, error)
}

type driverExecer interface {
	ExecContext(context.Context, string, []driver.NamedValue) (driver.Result, error)
}

type driverQueryer interface {
	QueryContext(context.Context, string, []driver.NamedValue) (driver.Rows, error)
}

// CreateMigrationBackup creates, sanitizes, verifies, and no-replace publishes
// the one backup reserved by a preparing attempt. The source catalog transition
// happens only after the durable final exists.
func CreateMigrationBackup(ctx context.Context, config BackupConfig) (path string, digest [sha256.Size]byte, returnErr error) {
	if _, err := ValidateRoot(config.Root); err != nil {
		return "", digest, err
	}
	if config.Source == nil || config.CreatedAt.Unix() <= 0 {
		return "", digest, fmt.Errorf("create migration backup: invalid source or creation time")
	}
	attempt := config.Attempt
	wantBasename, err := validateBackupAttempt(attempt)
	if err != nil {
		return "", digest, err
	}
	expectation, err := resolveBackupExpectation(config)
	if err != nil {
		return "", digest, err
	}
	root := filepath.Clean(config.Root)
	finalPath := filepath.Join(root, wantBasename)
	if filepath.Dir(finalPath) != root || filepath.Base(finalPath) != wantBasename {
		return "", digest, fmt.Errorf("create migration backup: invalid attempt-derived destination")
	}
	defer func() {
		if returnErr != nil {
			returnErr = errors.Join(returnErr, markBackupAttemptFailed(config.Source, attempt.AttemptID))
		}
	}()
	tempPath := finalPath + ".tmp"
	finalExists, err := pathExists(finalPath)
	if err != nil {
		return "", digest, fmt.Errorf("create migration backup: inspect final destination: %w", err)
	}
	tempExists, err := pathExists(tempPath)
	if err != nil {
		return "", digest, fmt.Errorf("create migration backup: inspect temporary destination: %w", err)
	}
	if finalExists {
		if tempExists {
			return "", digest, fmt.Errorf("create migration backup: final and temporary destinations both exist")
		}
		return resumePublishedBackup(ctx, config, expectation, finalPath)
	}
	if tempExists {
		if err := cleanupBackupTemp(tempPath); err != nil {
			return "", digest, fmt.Errorf("create migration backup: validate and remove interrupted temporary destination: %w", err)
		}
		if err := syncDirectory(config.Root); err != nil {
			return "", digest, fmt.Errorf("create migration backup: persist interrupted temporary cleanup: %w", err)
		}
	}
	temp, err := os.OpenFile(tempPath, os.O_CREATE|os.O_EXCL|os.O_RDWR, 0o600) //nolint:gosec // exact owner-private attempt-derived temp
	if err != nil {
		return "", digest, fmt.Errorf("create migration backup: reserve temporary destination: %w", err)
	}
	if err := temp.Chmod(0o600); err != nil {
		_ = temp.Close()
		_ = os.Remove(tempPath)
		return "", digest, fmt.Errorf("create migration backup: set temporary mode: %w", err)
	}
	if err := temp.Close(); err != nil {
		_ = os.Remove(tempPath)
		return "", digest, fmt.Errorf("create migration backup: close reserved temporary destination: %w", err)
	}
	published := false
	defer func() {
		if !published {
			returnErr = errors.Join(returnErr, cleanupBackupTemp(tempPath), syncDirectory(root))
		}
	}()

	sourceConnection, err := config.Source.Conn(ctx)
	if err != nil {
		return "", digest, fmt.Errorf("create migration backup: reserve source connection: %w", err)
	}
	if err := migration.ValidateHead(ctx, sourceConnection, expectation.migrations, expectation.schemaContracts, expectation.originalHead); err != nil {
		_ = sourceConnection.Close()
		return "", digest, fmt.Errorf("create migration backup: validate source head: %w", err)
	}
	var destinationConnection driver.Conn
	err = sourceConnection.Raw(func(raw any) error {
		connection, ok := raw.(backupSourceConnection)
		if !ok {
			return fmt.Errorf("modernc source connection does not implement NewBackup")
		}
		backup, err := connection.NewBackup(backupDestinationURI(tempPath))
		if err != nil {
			return err
		}
		for {
			if err := ctx.Err(); err != nil {
				return errors.Join(fmt.Errorf("online backup canceled: %w", err), backup.Finish())
			}
			more, err := backup.Step(128)
			if err != nil {
				return errors.Join(fmt.Errorf("copy pages: %w", err), backup.Finish())
			}
			if !more {
				break
			}
		}
		destinationConnection, err = backup.Commit()
		if err != nil {
			return fmt.Errorf("commit page copy: %w", err)
		}
		return nil
	})
	if err != nil {
		_ = sourceConnection.Close()
		return "", digest, fmt.Errorf("create migration backup: online backup: %w", err)
	}
	if err := sourceConnection.Close(); err != nil {
		_ = destinationConnection.Close()
		return "", digest, fmt.Errorf("create migration backup: close source connection: %w", err)
	}
	if err := verifyBackupDurabilityPragmas(ctx, destinationConnection); err != nil {
		_ = destinationConnection.Close()
		return "", digest, err
	}
	if err := sanitizeBackupDestination(ctx, destinationConnection, attempt.AttemptID); err != nil {
		_ = destinationConnection.Close()
		return "", digest, err
	}
	if err := destinationConnection.Close(); err != nil {
		return "", digest, fmt.Errorf("create migration backup: close destination connection: %w", err)
	}
	if err := verifySanitizedBackupForAttempt(ctx, tempPath, expectation); err != nil {
		return "", digest, err
	}
	if err := validatePrivateRegular(tempPath); err != nil {
		return "", digest, fmt.Errorf("create migration backup: validate temporary file: %w", err)
	}
	backupFile, err := os.OpenFile(tempPath, os.O_RDWR, 0) //nolint:gosec // exact validated attempt-derived path
	if err != nil {
		return "", digest, fmt.Errorf("create migration backup: open temporary for full sync: %w", err)
	}
	hasher := sha256.New()
	if _, err := io.Copy(hasher, backupFile); err != nil {
		_ = backupFile.Close()
		return "", digest, fmt.Errorf("create migration backup: calculate digest: %w", err)
	}
	copy(digest[:], hasher.Sum(nil))
	if err := fullSyncFile(backupFile); err != nil {
		_ = backupFile.Close()
		return "", digest, fmt.Errorf("create migration backup: %w", err)
	}
	if err := backupFile.Close(); err != nil {
		return "", digest, fmt.Errorf("create migration backup: close synced temporary: %w", err)
	}
	if err := publishNoReplace(tempPath, finalPath); err != nil {
		return "", digest, fmt.Errorf("create migration backup: publish without replacement: %w", err)
	}
	published = true
	if err := syncDirectory(config.Root); err != nil {
		return "", digest, fmt.Errorf("create migration backup: persist publication: %w", err)
	}

	catalogConnection, err := config.Source.Conn(ctx)
	if err != nil {
		return "", digest, fmt.Errorf("create migration backup: reserve source catalog connection: %w", err)
	}
	_, catalogErr := migration.RecordVerifiedBackup(ctx, catalogConnection, attempt.AttemptID, digest, config.CreatedAt)
	closeErr := catalogConnection.Close()
	if err := errors.Join(catalogErr, closeErr); err != nil {
		return "", digest, fmt.Errorf("create migration backup: record source catalog: %w", err)
	}
	return finalPath, digest, nil
}

func validateBackupAttempt(attempt migration.Attempt) (string, error) {
	decodedID, err := hex.DecodeString(attempt.AttemptID)
	if err != nil || len(decodedID) != 16 || hex.EncodeToString(decodedID) != attempt.AttemptID {
		return "", fmt.Errorf("create migration backup: invalid attempt ID")
	}
	decodedSet, err := hex.DecodeString(attempt.MigrationSetSHA256)
	if err != nil || len(decodedSet) != sha256.Size || hex.EncodeToString(decodedSet) != attempt.MigrationSetSHA256 {
		return "", fmt.Errorf("create migration backup: invalid attempt migration-set digest")
	}
	wantBasename := ".amsftp-backup-v1-" + attempt.AttemptID + ".sqlite3"
	if attempt.Status != migration.AttemptPreparing || attempt.OriginalHead <= 0 || attempt.CurrentHead != attempt.OriginalHead || attempt.TargetHead <= attempt.OriginalHead || attempt.BackupSHA256 != nil || attempt.ErrorKind != nil || attempt.ReservedBackupBasename != wantBasename {
		return "", fmt.Errorf("create migration backup: invalid attempt: not an exact unbacked preparing singleton")
	}
	return wantBasename, nil
}

func resolveBackupExpectation(config BackupConfig) (backupExpectation, error) {
	attempt := config.Attempt
	if attempt.OriginalHead <= 0 {
		return backupExpectation{}, fmt.Errorf("create migration backup: invalid original head")
	}
	migrations := config.Migrations
	contracts := config.SchemaContracts
	if len(migrations) == 0 && contracts == nil && attempt.OriginalHead == 1 {
		migrations = []migration.Migration{migration.Version1()}
		contracts = map[uint64][]byte{1: migration.Version1SchemaContract()}
	}
	if err := migration.ValidateSet(migrations); err != nil {
		return backupExpectation{}, fmt.Errorf("create migration backup: invalid compiled migrations: %w", err)
	}
	originalHead := uint64(attempt.OriginalHead) //nolint:gosec // positivity checked above
	if originalHead > uint64(len(migrations)) {  //nolint:gosec // len is non-negative
		return backupExpectation{}, fmt.Errorf("create migration backup: original head %d exceeds compiled target", originalHead)
	}
	if len(contracts[originalHead]) == 0 {
		return backupExpectation{}, fmt.Errorf("create migration backup: schema contract for original head %d is missing", originalHead)
	}
	return backupExpectation{
		attemptID: attempt.AttemptID, originalHead: originalHead,
		migrations: migrations, schemaContracts: contracts,
	}, nil
}

func markBackupAttemptFailed(database *sql.DB, attemptID string) error {
	connection, err := database.Conn(context.Background())
	if err != nil {
		return fmt.Errorf("create migration backup: reserve failure-marker connection: %w", err)
	}
	attempt, loadErr := migration.LoadAttempt(context.Background(), connection)
	if loadErr != nil {
		closeErr := connection.Close()
		return fmt.Errorf("create migration backup: load attempt for failure marker: %w", errors.Join(loadErr, closeErr))
	}
	if attempt.AttemptID != attemptID {
		closeErr := connection.Close()
		if closeErr != nil {
			return fmt.Errorf("create migration backup: failure-marker attempt changed; close connection: %w", closeErr)
		}
		return fmt.Errorf("create migration backup: failure-marker attempt changed")
	}
	if attempt.Status != migration.AttemptPreparing {
		return connection.Close()
	}
	_, markerErr := migration.MarkAttemptFailed(context.Background(), connection, attemptID, "backup_failed")
	closeErr := connection.Close()
	if err := errors.Join(markerErr, closeErr); err != nil {
		return fmt.Errorf("create migration backup: persist failure marker: %w", err)
	}
	return nil
}

func resumePublishedBackup(ctx context.Context, config BackupConfig, expectation backupExpectation, finalPath string) (string, [sha256.Size]byte, error) {
	var digest [sha256.Size]byte
	if err := validatePrivateRegular(finalPath); err != nil {
		return "", digest, fmt.Errorf("create migration backup: validate published final: %w", err)
	}
	if err := verifySanitizedBackupForAttempt(ctx, finalPath, expectation); err != nil {
		return "", digest, fmt.Errorf("create migration backup: verify published final: %w", err)
	}
	backupFile, err := os.Open(finalPath) //nolint:gosec // exact validated attempt-derived final
	if err != nil {
		return "", digest, fmt.Errorf("create migration backup: open published final for digest: %w", err)
	}
	hasher := sha256.New()
	if _, err := io.Copy(hasher, backupFile); err != nil {
		_ = backupFile.Close()
		return "", digest, fmt.Errorf("create migration backup: calculate published final digest: %w", err)
	}
	copy(digest[:], hasher.Sum(nil))
	if err := backupFile.Close(); err != nil {
		return "", digest, fmt.Errorf("create migration backup: close published final: %w", err)
	}
	catalogConnection, err := config.Source.Conn(ctx)
	if err != nil {
		return "", digest, fmt.Errorf("create migration backup: reserve source catalog connection for published final: %w", err)
	}
	_, catalogErr := migration.RecordVerifiedBackup(ctx, catalogConnection, config.Attempt.AttemptID, digest, config.CreatedAt)
	closeErr := catalogConnection.Close()
	if err := errors.Join(catalogErr, closeErr); err != nil {
		return "", digest, fmt.Errorf("create migration backup: record published final in source catalog: %w", err)
	}
	return finalPath, digest, nil
}

func pathExists(path string) (bool, error) {
	if _, err := os.Lstat(path); err == nil {
		return true, nil
	} else if errors.Is(err, os.ErrNotExist) {
		return false, nil
	} else {
		return false, err
	}
}

func backupDestinationURI(path string) string {
	uri := &url.URL{Scheme: "file", Path: path}
	uri.RawQuery = "_pragma=" + url.QueryEscape("checkpoint_fullfsync(1)") +
		"&_pragma=" + url.QueryEscape("fullfsync(1)") +
		"&_pragma=" + url.QueryEscape("synchronous(FULL)")
	return uri.String()
}

func verifyBackupDurabilityPragmas(ctx context.Context, connection driver.Conn) error {
	for _, expectation := range []struct {
		name string
		want int64
	}{
		{name: "checkpoint_fullfsync", want: 1},
		{name: "fullfsync", want: 1},
		{name: "synchronous", want: 2},
	} {
		got, err := queryDriverInteger(ctx, connection, "PRAGMA "+expectation.name)
		if err != nil {
			return fmt.Errorf("create migration backup: read destination %s: %w", expectation.name, err)
		}
		if got != expectation.want {
			return fmt.Errorf("create migration backup: destination %s = %d, want %d", expectation.name, got, expectation.want)
		}
	}
	return nil
}

func sanitizeBackupDestination(ctx context.Context, connection driver.Conn, attemptID string) error {
	execer, ok := connection.(driverExecer)
	if !ok {
		return fmt.Errorf("create migration backup: destination does not implement driver.ExecerContext")
	}
	if _, err := execer.ExecContext(ctx, "BEGIN IMMEDIATE", nil); err != nil {
		return fmt.Errorf("create migration backup: begin sanitize: %w", err)
	}
	committed := false
	defer func() {
		if !committed {
			_, _ = execer.ExecContext(context.Background(), "ROLLBACK", nil)
		}
	}()
	deleted, err := execer.ExecContext(ctx, "DELETE FROM migration_attempts WHERE singleton=1 AND attempt_id=?", []driver.NamedValue{{Ordinal: 1, Value: attemptID}})
	if err != nil {
		return fmt.Errorf("create migration backup: delete captured attempt: %w", err)
	}
	rows, err := deleted.RowsAffected()
	if err != nil {
		return fmt.Errorf("create migration backup: read captured attempt row count: %w", err)
	}
	if rows != 1 {
		return fmt.Errorf("create migration backup: captured attempt rows = %d, want 1", rows)
	}
	held, err := execer.ExecContext(ctx, "UPDATE migration_control SET upgrade_hold=1, hold_reason='restored_backup', hold_attempt_id=? WHERE singleton=1 AND upgrade_hold=0 AND hold_reason IS NULL AND hold_attempt_id IS NULL", []driver.NamedValue{{Ordinal: 1, Value: attemptID}})
	if err != nil {
		return fmt.Errorf("create migration backup: set restore hold: %w", err)
	}
	rows, err = held.RowsAffected()
	if err != nil {
		return fmt.Errorf("create migration backup: read restore hold row count: %w", err)
	}
	if rows != 1 {
		return fmt.Errorf("create migration backup: restore hold rows = %d, want 1", rows)
	}
	if _, err := execer.ExecContext(ctx, "COMMIT", nil); err != nil {
		return fmt.Errorf("create migration backup: commit sanitize: %w", err)
	}
	committed = true
	return nil
}

func verifySanitizedBackup(ctx context.Context, path, attemptID string) error {
	return verifySanitizedBackupForAttempt(ctx, path, backupExpectation{
		attemptID: attemptID, originalHead: 1,
		migrations:      []migration.Migration{migration.Version1()},
		schemaContracts: map[uint64][]byte{1: migration.Version1SchemaContract()},
	})
}

func verifySanitizedBackupForAttempt(ctx context.Context, path string, expectation backupExpectation) error {
	database, err := sql.Open("sqlite", durabilityURI(path, false))
	if err != nil {
		return fmt.Errorf("create migration backup: reopen sanitized destination: %w", err)
	}
	database.SetMaxOpenConns(1)
	connection, err := database.Conn(ctx)
	if err != nil {
		_ = database.Close()
		return fmt.Errorf("create migration backup: reserve sanitized destination: %w", err)
	}
	validationErr := validateBackupHead(ctx, connection, expectation, true)
	if validationErr == nil {
		var busy, logFrames, checkpointed int64
		validationErr = connection.QueryRowContext(ctx, "PRAGMA wal_checkpoint(TRUNCATE)").Scan(&busy, &logFrames, &checkpointed)
		if validationErr == nil && (busy != 0 || logFrames != 0 || checkpointed != 0) {
			validationErr = fmt.Errorf("checkpoint result = (%d,%d,%d), want (0,0,0)", busy, logFrames, checkpointed)
		}
	}
	closeConnectionErr := connection.Close()
	closeDatabaseErr := database.Close()
	if err := errors.Join(validationErr, closeConnectionErr, closeDatabaseErr); err != nil {
		return fmt.Errorf("create migration backup: verify sanitized destination: %w", err)
	}
	for _, suffix := range []string{"-wal", "-shm", "-journal"} {
		if _, err := os.Lstat(path + suffix); !errors.Is(err, os.ErrNotExist) {
			return fmt.Errorf("create migration backup: sanitized destination sidecar %s remains", suffix)
		}
	}
	return verifyImmutableBackup(ctx, path, expectation)
}

func verifyImmutableBackup(ctx context.Context, path string, expectation backupExpectation) error {
	uri := &url.URL{Scheme: "file", Path: path}
	query := uri.Query()
	query.Set("immutable", "1")
	query.Set("mode", "ro")
	uri.RawQuery = query.Encode()
	database, err := sql.Open("sqlite", uri.String())
	if err != nil {
		return fmt.Errorf("create migration backup: open immutable final: %w", err)
	}
	database.SetMaxOpenConns(1)
	connection, err := database.Conn(ctx)
	if err != nil {
		_ = database.Close()
		return fmt.Errorf("create migration backup: reserve immutable final: %w", err)
	}
	validationErr := validateBackupHead(ctx, connection, expectation, false)
	closeConnectionErr := connection.Close()
	closeDatabaseErr := database.Close()
	if err := errors.Join(validationErr, closeConnectionErr, closeDatabaseErr); err != nil {
		return fmt.Errorf("create migration backup: verify immutable final: %w", err)
	}
	return nil
}

func validateBackupHead(ctx context.Context, connection *sql.Conn, expectation backupExpectation, requireMaxPageCount bool) error {
	expectations := []struct {
		name string
		want int64
	}{
		{name: "application_id", want: int64(applicationID)},
		{name: "user_version", want: 0},
		{name: "page_size", want: int64(statePageSize)},
	}
	if requireMaxPageCount {
		expectations = append(expectations, struct {
			name string
			want int64
		}{name: "max_page_count", want: 2_097_152})
	}
	for _, expectation := range expectations {
		var got int64
		if err := connection.QueryRowContext(ctx, "PRAGMA "+expectation.name).Scan(&got); err != nil {
			return fmt.Errorf("backup head PRAGMA %s: %w", expectation.name, err)
		}
		if got != expectation.want {
			return fmt.Errorf("backup head PRAGMA %s = %d, want %d", expectation.name, got, expectation.want)
		}
	}
	if err := migration.ValidateHead(ctx, connection, expectation.migrations, expectation.schemaContracts, expectation.originalHead); err != nil {
		return fmt.Errorf("backup migration head: %w", err)
	}
	var attempts, holds int64
	if err := connection.QueryRowContext(ctx, "SELECT count(*) FROM migration_attempts").Scan(&attempts); err != nil {
		return fmt.Errorf("backup active attempts: %w", err)
	}
	if attempts != 0 {
		return fmt.Errorf("backup active attempts = %d, want 0", attempts)
	}
	if err := connection.QueryRowContext(ctx, "SELECT count(*) FROM migration_control WHERE singleton=1 AND upgrade_hold=1 AND hold_reason='restored_backup' AND hold_attempt_id=?", expectation.attemptID).Scan(&holds); err != nil {
		return fmt.Errorf("backup restore holds: %w", err)
	}
	if holds != 1 {
		return fmt.Errorf("backup restore holds = %d, want 1", holds)
	}
	var quick string
	if err := connection.QueryRowContext(ctx, "PRAGMA quick_check").Scan(&quick); err != nil {
		return fmt.Errorf("backup quick_check: %w", err)
	}
	if quick != "ok" {
		return fmt.Errorf("backup quick_check = %q, want ok", quick)
	}
	foreignRows, err := connection.QueryContext(ctx, "PRAGMA foreign_key_check")
	if err != nil {
		return fmt.Errorf("backup foreign_key_check: %w", err)
	}
	violated := foreignRows.Next()
	foreignErr := errors.Join(foreignRows.Err(), foreignRows.Close())
	if foreignErr != nil || violated {
		return fmt.Errorf("backup foreign_key_check violation=%t: %w", violated, foreignErr)
	}
	return nil
}

func queryDriverInteger(ctx context.Context, connection driver.Conn, query string) (int64, error) {
	queryer, ok := connection.(driverQueryer)
	if !ok {
		return 0, fmt.Errorf("driver connection does not implement QueryerContext")
	}
	rows, err := queryer.QueryContext(ctx, query, nil)
	if err != nil {
		return 0, err
	}
	closeRows := func() error {
		if err := rows.Close(); err != nil {
			return fmt.Errorf("close driver rows: %w", err)
		}
		return nil
	}
	values := make([]driver.Value, 1)
	if err := rows.Next(values); err != nil {
		return 0, errors.Join(err, closeRows())
	}
	value, ok := values[0].(int64)
	if !ok {
		return 0, errors.Join(fmt.Errorf("driver integer has type %T", values[0]), closeRows())
	}
	if err := rows.Next(values); !errors.Is(err, io.EOF) {
		return 0, errors.Join(fmt.Errorf("driver integer has an unexpected second row: %w", err), closeRows())
	}
	return value, closeRows()
}

func cleanupBackupTemp(path string) error {
	var result error
	for _, candidate := range []string{path + "-wal", path + "-shm", path + "-journal", path} {
		if _, err := os.Lstat(candidate); errors.Is(err, os.ErrNotExist) {
			continue
		} else if err != nil {
			result = errors.Join(result, err)
			continue
		}
		if err := validatePrivateRegular(candidate); err != nil {
			result = errors.Join(result, err)
			continue
		}
		if err := os.Remove(candidate); err != nil {
			result = errors.Join(result, err)
		}
	}
	return result
}
