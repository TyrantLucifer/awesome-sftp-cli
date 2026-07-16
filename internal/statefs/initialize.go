package statefs

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/TyrantLucifer/awesome-mac-sftp/internal/platform"
	"github.com/TyrantLucifer/awesome-mac-sftp/internal/state/migration"
	_ "github.com/TyrantLucifer/awesome-mac-sftp/internal/state/sqlite"
)

const (
	bootstrapIntentName = ".amsftp-bootstrap-v1.intent"
	bootstrapPrefix     = ".amsftp-bootstrap-v1-"
	bootstrapSuffix     = ".sqlite3"
	runtimeBusyTimeout  = 5000
)

type InitializeConfig struct {
	Root                    string
	DatabasePath            string
	Random                  io.Reader
	Now                     time.Time
	Migrations              []migration.Migration
	SchemaContracts         map[uint64][]byte
	MigrationAttemptID      string
	ExplicitMigrationResume bool
	bootstrapFault          func(string)
}

type InitializeReport struct {
	Filesystem   Filesystem
	Bootstrapped bool
	Recovered    bool
	SchemaHead   uint64
}

func Initialize(ctx context.Context, config InitializeConfig) (*sql.DB, InitializeReport, error) {
	if config.DatabasePath != filepath.Join(config.Root, filepath.Base(config.DatabasePath)) {
		return nil, InitializeReport{}, fmt.Errorf("initialize state: database must be a direct child of the state root")
	}
	filesystem, err := ValidateRoot(config.Root)
	if err != nil {
		return nil, InitializeReport{}, err
	}
	migrations, contracts, err := resolveCompiledState(config.Migrations, config.SchemaContracts)
	if err != nil {
		return nil, InitializeReport{}, err
	}
	identity, err := PreflightIdentity(config.DatabasePath)
	if err != nil {
		return nil, InitializeReport{}, err
	}
	recovered, err := recoverBootstrapIntent(ctx, config.Root, config.DatabasePath)
	if err != nil {
		return nil, InitializeReport{}, err
	}
	if recovered {
		identity, err = PreflightIdentity(config.DatabasePath)
		if err != nil {
			return nil, InitializeReport{}, err
		}
	}
	if identity.Kind == IdentityProject && !identity.HasSidecars {
		if _, err := validateImmutableState(ctx, config.DatabasePath, migrations, contracts, false); err != nil {
			return nil, InitializeReport{}, err
		}
	}
	if err := ProbeAfterIdentity(ctx, config.Root, ProbeConfig{Random: config.Random}); err != nil {
		return nil, InitializeReport{}, err
	}
	report := InitializeReport{Filesystem: filesystem, Recovered: recovered, SchemaHead: 1}
	if identity.Kind == IdentityPristine {
		if err := bootstrapVersion1(ctx, config); err != nil {
			return nil, InitializeReport{}, err
		}
		report.Bootstrapped = true
	}
	database, err := openRuntimeDatabase(ctx, config.DatabasePath, migrations, contracts)
	if err != nil {
		return nil, InitializeReport{}, err
	}
	now := config.Now
	if now.IsZero() {
		now = time.Now()
	}
	upgradeReport, err := UpgradeDatabase(ctx, UpgradeConfig{
		Root: config.Root, DatabasePath: config.DatabasePath, Database: database,
		Migrations: migrations, SchemaContracts: contracts,
		AttemptID: config.MigrationAttemptID, Now: now, ExplicitResume: config.ExplicitMigrationResume,
	})
	if err != nil {
		_ = database.Close()
		return nil, InitializeReport{}, err
	}
	finalConnection, err := database.Conn(ctx)
	if err != nil {
		_ = database.Close()
		return nil, InitializeReport{}, fmt.Errorf("initialize state: reserve final-check connection: %w", err)
	}
	finalErr := finalConnectionChecks(ctx, finalConnection)
	closeConnectionErr := finalConnection.Close()
	closeDatabaseErr := database.Close()
	if err := errors.Join(finalErr, closeConnectionErr, closeDatabaseErr); err != nil {
		return nil, InitializeReport{}, fmt.Errorf("initialize state: quiesce upgraded database: %w", err)
	}
	if err := requireDatabaseSidecarsAbsent(config.DatabasePath); err != nil {
		return nil, InitializeReport{}, err
	}
	validatedHead, err := validateImmutableState(ctx, config.DatabasePath, migrations, contracts, true)
	if err != nil {
		return nil, InitializeReport{}, err
	}
	if validatedHead != upgradeReport.TargetHead {
		return nil, InitializeReport{}, fmt.Errorf("initialize state: immutable head %d, want target %d", validatedHead, upgradeReport.TargetHead)
	}
	database, err = openRuntimeDatabase(ctx, config.DatabasePath, migrations, contracts)
	if err != nil {
		return nil, InitializeReport{}, err
	}
	report.SchemaHead = validatedHead
	return database, report, nil
}

func bootstrapVersion1(ctx context.Context, config InitializeConfig) (returnErr error) {
	identity, err := PreflightIdentity(config.DatabasePath)
	if err != nil {
		return err
	}
	if identity.Kind != IdentityPristine {
		return fmt.Errorf("bootstrap state: final is not pristine")
	}
	random := config.Random
	if random == nil {
		random = rand.Reader
	}
	generation := make([]byte, 16)
	if _, err := io.ReadFull(random, generation); err != nil {
		return fmt.Errorf("bootstrap state: generate ID: %w", err)
	}
	generationID := hex.EncodeToString(generation)
	intentPath := filepath.Join(config.Root, bootstrapIntentName)
	tempPath := filepath.Join(config.Root, bootstrapPrefix+generationID+bootstrapSuffix)
	intent, err := os.OpenFile(intentPath, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600) //nolint:gosec // exact owner-private intent mode
	if err != nil {
		return fmt.Errorf("bootstrap state: create intent: %w", err)
	}
	published := false
	defer func() {
		if returnErr != nil && !published {
			returnErr = errors.Join(returnErr, cleanupBootstrapGeneration(config.Root, generationID, false))
		}
	}()
	if _, err := io.WriteString(intent, generationID); err != nil {
		_ = intent.Close()
		return fmt.Errorf("bootstrap state: write intent: %w", err)
	}
	if err := fullSyncFile(intent); err != nil {
		_ = intent.Close()
		return fmt.Errorf("bootstrap state: sync intent: %w", err)
	}
	if err := intent.Close(); err != nil {
		return fmt.Errorf("bootstrap state: close intent: %w", err)
	}
	if err := syncDirectory(config.Root); err != nil {
		return fmt.Errorf("bootstrap state: persist intent: %w", err)
	}
	config.bootstrapCheckpoint("intent_persisted")
	temp, err := os.OpenFile(tempPath, os.O_CREATE|os.O_EXCL|os.O_RDWR, 0o600) //nolint:gosec // exact owner-private temporary DB mode
	if err != nil {
		return fmt.Errorf("bootstrap state: create temporary database: %w", err)
	}
	if err := temp.Chmod(0o600); err != nil {
		_ = temp.Close()
		return fmt.Errorf("bootstrap state: set temporary database mode: %w", err)
	}
	if err := temp.Close(); err != nil {
		return fmt.Errorf("bootstrap state: close initial temporary database: %w", err)
	}
	config.bootstrapCheckpoint("temp_created")

	database, err := sql.Open("sqlite", durabilityURI(tempPath, false))
	if err != nil {
		return fmt.Errorf("bootstrap state: open temporary database: %w", err)
	}
	database.SetMaxOpenConns(1)
	connection, err := database.Conn(ctx)
	if err != nil {
		_ = database.Close()
		return fmt.Errorf("bootstrap state: reserve connection: %w", err)
	}
	if _, err := connection.ExecContext(ctx, "PRAGMA journal_mode=DELETE"); err != nil {
		_ = connection.Close()
		_ = database.Close()
		return fmt.Errorf("bootstrap state: select rollback journal: %w", err)
	}
	now := config.Now
	if now.IsZero() {
		now = time.Now()
	}
	if err := (migration.Runner{}).Apply(ctx, connection, migration.Version1(), now.UTC().Format(time.RFC3339Nano)); err != nil {
		_ = connection.Close()
		_ = database.Close()
		return fmt.Errorf("bootstrap state: apply Version 1: %w", err)
	}
	config.bootstrapCheckpoint("version_committed")
	if err := validateConnectionVersion1(ctx, connection, true); err != nil {
		_ = connection.Close()
		_ = database.Close()
		return fmt.Errorf("bootstrap state: %w", err)
	}
	if err := connection.Close(); err != nil {
		_ = database.Close()
		return fmt.Errorf("bootstrap state: close connection: %w", err)
	}
	if err := database.Close(); err != nil {
		return fmt.Errorf("bootstrap state: close database: %w", err)
	}
	for _, suffix := range []string{"-wal", "-shm", "-journal"} {
		if _, err := os.Lstat(tempPath + suffix); !errors.Is(err, os.ErrNotExist) {
			return fmt.Errorf("bootstrap state: temporary sidecar %s remains", suffix)
		}
	}
	// tempPath is derived from the validated state root and CSPRNG generation.
	temp, err = os.OpenFile(tempPath, os.O_RDWR, 0) //nolint:gosec
	if err != nil {
		return fmt.Errorf("bootstrap state: reopen temporary database: %w", err)
	}
	if err := fullSyncFile(temp); err != nil {
		_ = temp.Close()
		return fmt.Errorf("bootstrap state: sync temporary database: %w", err)
	}
	if err := temp.Close(); err != nil {
		return fmt.Errorf("bootstrap state: close synced temporary database: %w", err)
	}
	config.bootstrapCheckpoint("temp_synced")
	if _, err := PreflightIdentity(tempPath); err != nil {
		return fmt.Errorf("bootstrap state: validate temporary identity: %w", err)
	}
	if err := publishNoReplace(tempPath, config.DatabasePath); err != nil {
		return fmt.Errorf("bootstrap state: publish without replacement: %w", err)
	}
	published = true
	config.bootstrapCheckpoint("final_published")
	if err := syncDirectory(config.Root); err != nil {
		return fmt.Errorf("bootstrap state: persist published database: %w", err)
	}
	config.bootstrapCheckpoint("final_persisted")
	if err := os.Remove(intentPath); err != nil {
		return fmt.Errorf("bootstrap state: remove intent: %w", err)
	}
	config.bootstrapCheckpoint("intent_removed")
	if err := syncDirectory(config.Root); err != nil {
		return fmt.Errorf("bootstrap state: persist intent removal: %w", err)
	}
	if identity, err := PreflightIdentity(config.DatabasePath); err != nil || identity.Kind != IdentityProject || identity.HasSidecars {
		if err == nil {
			err = fmt.Errorf("identity kind %d sidecars=%t", identity.Kind, identity.HasSidecars)
		}
		return fmt.Errorf("bootstrap state: published identity is invalid: %w", err)
	}
	return nil
}

func (config InitializeConfig) bootstrapCheckpoint(point string) {
	if config.bootstrapFault != nil {
		config.bootstrapFault(point)
	}
}

func recoverBootstrapIntent(ctx context.Context, root, finalPath string) (bool, error) {
	intentPath := filepath.Join(root, bootstrapIntentName)
	if _, err := os.Lstat(intentPath); errors.Is(err, os.ErrNotExist) {
		return false, nil
	} else if err != nil {
		return false, fmt.Errorf("recover bootstrap intent: inspect: %w", err)
	}
	if err := validatePrivateRegular(intentPath); err != nil {
		return false, fmt.Errorf("recover bootstrap intent: %w", err)
	}
	// intentPath is now proven to be the fixed private regular singleton.
	content, err := os.ReadFile(intentPath) //nolint:gosec
	if err != nil {
		return false, fmt.Errorf("recover bootstrap intent: read: %w", err)
	}
	generationID := string(content)
	if len(generationID) != 32 {
		return false, fmt.Errorf("recover bootstrap intent: invalid generation length")
	}
	decoded, err := hex.DecodeString(generationID)
	if err != nil || len(decoded) != 16 || generationID != strings.ToLower(generationID) {
		return false, fmt.Errorf("recover bootstrap intent: invalid generation")
	}
	if _, err := os.Lstat(finalPath); err == nil {
		identity, identityErr := PreflightIdentity(finalPath)
		if identityErr != nil || identity.Kind != IdentityProject || identity.HasSidecars {
			return false, fmt.Errorf("recover bootstrap intent: published final identity=%#v: %w", identity, identityErr)
		}
		if err := validateImmutableVersion1(ctx, finalPath); err != nil {
			return false, fmt.Errorf("recover bootstrap intent: validate published final: %w", err)
		}
		tempPath := filepath.Join(root, bootstrapPrefix+generationID+bootstrapSuffix)
		for _, candidate := range []string{tempPath, tempPath + "-wal", tempPath + "-shm", tempPath + "-journal"} {
			// candidate is an exact basename derived from the validated intent ID.
			if _, err := os.Lstat(candidate); !errors.Is(err, os.ErrNotExist) { //nolint:gosec
				return false, fmt.Errorf("recover bootstrap intent: published final collides with temp %q", candidate)
			}
		}
		if err := syncDirectory(root); err != nil {
			return false, fmt.Errorf("recover bootstrap intent: persist published final: %w", err)
		}
		if err := os.Remove(intentPath); err != nil {
			return false, fmt.Errorf("recover bootstrap intent: remove completed intent: %w", err)
		}
		if err := syncDirectory(root); err != nil {
			return false, fmt.Errorf("recover bootstrap intent: persist completed intent removal: %w", err)
		}
		return true, nil
	} else if !errors.Is(err, os.ErrNotExist) {
		return false, fmt.Errorf("recover bootstrap intent: inspect final: %w", err)
	}
	if err := cleanupBootstrapGeneration(root, generationID, true); err != nil {
		return false, err
	}
	return true, nil
}

func cleanupBootstrapGeneration(root, generationID string, removeIntent bool) error {
	tempPath := filepath.Join(root, bootstrapPrefix+generationID+bootstrapSuffix)
	for _, candidate := range []string{tempPath + "-wal", tempPath + "-shm", tempPath + "-journal", tempPath} {
		// candidate is an exact basename derived from the validated intent ID.
		if _, err := os.Lstat(candidate); errors.Is(err, os.ErrNotExist) { //nolint:gosec
			continue
		} else if err != nil {
			return fmt.Errorf("cleanup bootstrap generation: inspect %q: %w", candidate, err)
		}
		if err := validatePrivateRegular(candidate); err != nil {
			return fmt.Errorf("cleanup bootstrap generation: %w", err)
		}
		if err := os.Remove(candidate); err != nil { //nolint:gosec // exact validated generation path
			return fmt.Errorf("cleanup bootstrap generation: remove %q: %w", candidate, err)
		}
	}
	if removeIntent {
		if err := os.Remove(filepath.Join(root, bootstrapIntentName)); err != nil {
			return fmt.Errorf("cleanup bootstrap generation: remove intent: %w", err)
		}
	}
	return syncDirectory(root)
}

func openRuntimeDatabase(ctx context.Context, path string, migrations []migration.Migration, contracts map[uint64][]byte) (*sql.DB, error) {
	database, err := sql.Open("sqlite", durabilityURI(path, true))
	if err != nil {
		return nil, fmt.Errorf("open runtime state: %w", err)
	}
	database.SetMaxOpenConns(4)
	database.SetMaxIdleConns(4)
	connection, err := database.Conn(ctx)
	if err != nil {
		_ = database.Close()
		return nil, fmt.Errorf("open runtime state: reserve initializer: %w", err)
	}
	var journalMode string
	if err := connection.QueryRowContext(ctx, "PRAGMA journal_mode=WAL").Scan(&journalMode); err != nil {
		_ = connection.Close()
		_ = database.Close()
		return nil, fmt.Errorf("open runtime state: enable WAL: %w", err)
	}
	if journalMode != "wal" {
		_ = connection.Close()
		_ = database.Close()
		return nil, fmt.Errorf("open runtime state: journal mode %q, want wal", journalMode)
	}
	if _, err := validateConnectionState(ctx, connection, migrations, contracts, true, false); err != nil {
		_ = connection.Close()
		_ = database.Close()
		return nil, fmt.Errorf("open runtime state: %w", err)
	}
	if err := connection.Close(); err != nil {
		_ = database.Close()
		return nil, fmt.Errorf("open runtime state: close initializer: %w", err)
	}
	return database, nil
}

func resolveCompiledState(migrations []migration.Migration, contracts map[uint64][]byte) ([]migration.Migration, map[uint64][]byte, error) {
	if len(migrations) == 0 && contracts == nil {
		return []migration.Migration{migration.Version1()}, map[uint64][]byte{1: migration.Version1SchemaContract()}, nil
	}
	if len(migrations) == 0 || contracts == nil {
		return nil, nil, fmt.Errorf("initialize state: migrations and schema contracts must be supplied together")
	}
	if _, err := migration.SchemaContractDigests(migrations, contracts); err != nil {
		return nil, nil, fmt.Errorf("initialize state: %w", err)
	}
	return migrations, contracts, nil
}

func validateImmutableState(ctx context.Context, path string, migrations []migration.Migration, contracts map[uint64][]byte, requireFinal bool) (uint64, error) {
	uri := &url.URL{Scheme: "file", Path: path}
	query := uri.Query()
	query.Set("immutable", "1")
	query.Set("mode", "ro")
	uri.RawQuery = query.Encode()
	database, err := sql.Open("sqlite", uri.String())
	if err != nil {
		return 0, fmt.Errorf("validate immutable state: open: %w", err)
	}
	database.SetMaxOpenConns(1)
	connection, err := database.Conn(ctx)
	if err != nil {
		_ = database.Close()
		return 0, fmt.Errorf("validate immutable state: reserve connection: %w", err)
	}
	head, validationErr := validateConnectionState(ctx, connection, migrations, contracts, false, requireFinal)
	closeConnectionErr := connection.Close()
	closeDatabaseErr := database.Close()
	if err := errors.Join(validationErr, closeConnectionErr, closeDatabaseErr); err != nil {
		return 0, fmt.Errorf("validate immutable state: %w", err)
	}
	return head, nil
}

func validateConnectionState(ctx context.Context, connection *sql.Conn, migrations []migration.Migration, contracts map[uint64][]byte, requireMaxPageCount, requireFinal bool) (uint64, error) {
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
			return 0, fmt.Errorf("validate state: read PRAGMA %s: %w", expectation.name, err)
		}
		if got != expectation.want {
			return 0, fmt.Errorf("validate state: PRAGMA %s = %d, want %d", expectation.name, got, expectation.want)
		}
	}
	head, err := readMigrationHead(ctx, connection)
	if err != nil {
		return 0, err
	}
	if err := migration.ValidateHead(ctx, connection, migrations, contracts, head); err != nil {
		return 0, err
	}
	var controlRows, attemptRows int64
	if err := connection.QueryRowContext(ctx, "SELECT count(*) FROM migration_control WHERE singleton=1 AND ((upgrade_hold=0 AND hold_reason IS NULL AND hold_attempt_id IS NULL) OR (upgrade_hold=1 AND hold_reason='restored_backup' AND hold_attempt_id IS NOT NULL AND length(hold_attempt_id)=32 AND hold_attempt_id NOT GLOB '*[^0-9a-f]*'))").Scan(&controlRows); err != nil || controlRows != 1 {
		return 0, fmt.Errorf("validate state: migration control rows = %d: %w", controlRows, err)
	}
	if err := connection.QueryRowContext(ctx, "SELECT count(*) FROM migration_attempts").Scan(&attemptRows); err != nil {
		return 0, fmt.Errorf("validate state: active attempt count: %w", err)
	}
	if attemptRows < 0 || attemptRows > 1 || (requireFinal && attemptRows != 0) {
		return 0, fmt.Errorf("validate state: active attempt rows = %d", attemptRows)
	}
	if requireFinal {
		if err := requireUpgradeHoldClear(ctx, connection); err != nil {
			return 0, err
		}
	}
	var quick string
	if err := connection.QueryRowContext(ctx, "PRAGMA quick_check").Scan(&quick); err != nil || quick != "ok" {
		return 0, fmt.Errorf("validate state: quick_check = %q: %w", quick, err)
	}
	foreignRows, err := connection.QueryContext(ctx, "PRAGMA foreign_key_check")
	if err != nil {
		return 0, fmt.Errorf("validate state: foreign_key_check: %w", err)
	}
	violated := foreignRows.Next()
	if err := errors.Join(foreignRows.Err(), foreignRows.Close()); err != nil {
		return 0, fmt.Errorf("validate state: foreign_key_check rows: %w", err)
	}
	if violated {
		return 0, fmt.Errorf("validate state: foreign key violation")
	}
	return head, nil
}

func requireDatabaseSidecarsAbsent(path string) error {
	for _, suffix := range []string{"-wal", "-shm", "-journal"} {
		if _, err := os.Lstat(path + suffix); !errors.Is(err, os.ErrNotExist) {
			return fmt.Errorf("initialize state: sidecar %s remains after quiesce: %w", suffix, err)
		}
	}
	return nil
}

func validateImmutableVersion1(ctx context.Context, path string) error {
	uri := &url.URL{Scheme: "file", Path: path}
	query := uri.Query()
	query.Set("immutable", "1")
	query.Set("mode", "ro")
	uri.RawQuery = query.Encode()
	database, err := sql.Open("sqlite", uri.String())
	if err != nil {
		return fmt.Errorf("validate immutable state: open: %w", err)
	}
	database.SetMaxOpenConns(1)
	connection, err := database.Conn(ctx)
	if err != nil {
		_ = database.Close()
		return fmt.Errorf("validate immutable state: reserve connection: %w", err)
	}
	validationErr := validateConnectionVersion1(ctx, connection, false)
	closeConnectionErr := connection.Close()
	closeDatabaseErr := database.Close()
	if err := errors.Join(validationErr, closeConnectionErr, closeDatabaseErr); err != nil {
		return fmt.Errorf("validate immutable state: %w", err)
	}
	return nil
}

func validateConnectionVersion1(ctx context.Context, connection *sql.Conn, requireMaxPageCount bool) error {
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
			return fmt.Errorf("validate Version 1: read PRAGMA %s: %w", expectation.name, err)
		}
		if got != expectation.want {
			return fmt.Errorf("validate Version 1: PRAGMA %s = %d, want %d", expectation.name, got, expectation.want)
		}
	}
	var version int64
	var name, checksum string
	if err := connection.QueryRowContext(ctx, "SELECT version, name, sha256 FROM schema_migrations ORDER BY version DESC LIMIT 1").Scan(&version, &name, &checksum); err != nil {
		return fmt.Errorf("validate Version 1 history: %w", err)
	}
	digest, err := migration.Checksum(migration.Version1())
	if err != nil {
		return fmt.Errorf("validate Version 1 checksum: %w", err)
	}
	if version != 1 || name != "init" || checksum != hex.EncodeToString(digest[:]) {
		return fmt.Errorf("validate Version 1 history: got (%d,%q,%q)", version, name, checksum)
	}
	var historyRows, controlRows, attemptRows int64
	if err := connection.QueryRowContext(ctx, "SELECT count(*) FROM schema_migrations").Scan(&historyRows); err != nil || historyRows != 1 {
		return fmt.Errorf("validate Version 1 history rows = %d: %w", historyRows, err)
	}
	if err := connection.QueryRowContext(ctx, "SELECT count(*) FROM migration_control WHERE singleton=1 AND upgrade_hold=0 AND hold_reason IS NULL AND hold_attempt_id IS NULL").Scan(&controlRows); err != nil || controlRows != 1 {
		return fmt.Errorf("validate Version 1 control rows = %d: %w", controlRows, err)
	}
	if err := connection.QueryRowContext(ctx, "SELECT count(*) FROM migration_attempts").Scan(&attemptRows); err != nil || attemptRows != 0 {
		return fmt.Errorf("validate Version 1 attempt rows = %d: %w", attemptRows, err)
	}
	var quickCheck string
	if err := connection.QueryRowContext(ctx, "PRAGMA quick_check").Scan(&quickCheck); err != nil || quickCheck != "ok" {
		return fmt.Errorf("validate Version 1 quick_check = %q: %w", quickCheck, err)
	}
	foreignRows, err := connection.QueryContext(ctx, "PRAGMA foreign_key_check")
	if err != nil {
		return fmt.Errorf("validate Version 1 foreign_key_check: %w", err)
	}
	hasForeignViolation := foreignRows.Next()
	foreignErr := errors.Join(foreignRows.Err(), foreignRows.Close())
	if foreignErr != nil {
		return fmt.Errorf("validate Version 1 foreign_key_check rows: %w", foreignErr)
	}
	if hasForeignViolation {
		return fmt.Errorf("validate Version 1: foreign key violation")
	}
	return migration.ValidateVersion1SchemaContract(ctx, connection)
}

func durabilityURI(path string, runtime bool) string {
	pragmas := []string{
		"checkpoint_fullfsync(1)",
		"fullfsync(1)",
		"synchronous(FULL)",
		"foreign_keys(1)",
		"trusted_schema(0)",
		"busy_timeout(" + fmt.Sprint(runtimeBusyTimeout) + ")",
		"max_page_count(2097152)",
	}
	if runtime {
		pragmas = append(pragmas, "wal_autocheckpoint(1000)")
	}
	parts := make([]string, 0, len(pragmas))
	for _, pragma := range pragmas {
		parts = append(parts, "_pragma="+url.QueryEscape(pragma))
	}
	uri := &url.URL{Scheme: "file", Path: path, RawQuery: strings.Join(parts, "&")}
	return uri.String()
}

func validatePrivateRegular(path string) error {
	metadata, err := os.Lstat(path) //nolint:gosec // caller passes an exact state-owned path
	if err != nil {
		return fmt.Errorf("inspect private file %q: %w", path, err)
	}
	if !metadata.Mode().IsRegular() || metadata.Mode().Perm() != 0o600 {
		return fmt.Errorf("private file %q must be regular 0600", path)
	}
	if err := platform.ValidatePrivateFile(path, platform.ValidatePersistent); err != nil {
		return fmt.Errorf("validate private file %q: %w", path, err)
	}
	return nil
}

func syncDirectory(path string) error {
	directory, err := os.Open(path) //nolint:gosec // caller passes the validated state root
	if err != nil {
		return fmt.Errorf("open directory %q: %w", path, err)
	}
	if err := directory.Sync(); err != nil {
		_ = directory.Close()
		return fmt.Errorf("sync directory %q: %w", path, err)
	}
	if err := directory.Close(); err != nil {
		return fmt.Errorf("close directory %q: %w", path, err)
	}
	return nil
}
