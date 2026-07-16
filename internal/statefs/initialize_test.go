package statefs

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/hex"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/TyrantLucifer/awesome-mac-sftp/internal/state/migration"
)

func TestResolveCompiledStateDefaultsToFrozenVersion3Head(t *testing.T) {
	t.Parallel()

	migrations, contracts, err := resolveCompiledState(nil, nil)
	if err != nil {
		t.Fatalf("resolveCompiledState(default): %v", err)
	}
	wantMigrations := []migration.Migration{migration.Version1(), migration.Version2(), migration.Version3()}
	if !reflect.DeepEqual(migrations, wantMigrations) {
		t.Fatalf("default migrations = %#v, want Version 1 through Version 3", migrations)
	}
	if len(contracts) != 3 || !bytes.Equal(contracts[1], migration.Version1SchemaContract()) || !bytes.Equal(contracts[2], migration.Version2SchemaContract()) || !bytes.Equal(contracts[3], migration.Version3SchemaContract()) {
		t.Fatalf("default schema contracts do not exactly match the three frozen contracts")
	}
}

func TestInitializePristineStateMigratesToVersion3WithRandomAttemptAndOneBackup(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	root := privateTempDir(t)
	path := filepath.Join(root, "amsftp.db")
	// The configured reader has exactly enough bytes for the filesystem probe
	// and Version 1 bootstrap. Migration attempt IDs must use crypto/rand
	// directly, rather than extending this caller-controlled test seam.
	random := strings.NewReader(strings.Repeat("i", probeRandomBytes+16))
	database, report, err := Initialize(ctx, InitializeConfig{
		Root: root, DatabasePath: path, Random: random, Now: time.Unix(1_100, 0),
	})
	if err != nil {
		t.Fatalf("Initialize(pristine default): %v", err)
	}
	if !report.Bootstrapped || report.SchemaHead != 3 {
		t.Fatalf("initialize report = %#v, want bootstrapped Version 3", report)
	}
	connection, err := database.Conn(ctx)
	if err != nil {
		t.Fatalf("reserve Version 2 connection: %v", err)
	}
	migrations := []migration.Migration{migration.Version1(), migration.Version2(), migration.Version3()}
	contracts := map[uint64][]byte{1: migration.Version1SchemaContract(), 2: migration.Version2SchemaContract(), 3: migration.Version3SchemaContract()}
	if err := migration.ValidateHead(ctx, connection, migrations, contracts, 3); err != nil {
		t.Fatalf("validate default Version 3 head: %v", err)
	}
	var attemptID, backupBasename, journalMode string
	if err := connection.QueryRowContext(ctx, "SELECT attempt_id, backup_basename FROM migration_backups WHERE status='verified'").Scan(&attemptID, &backupBasename); err != nil {
		t.Fatalf("read default migration backup: %v", err)
	}
	if err := connection.QueryRowContext(ctx, "PRAGMA journal_mode").Scan(&journalMode); err != nil {
		t.Fatalf("read runtime journal mode: %v", err)
	}
	if err := connection.Close(); err != nil {
		t.Fatalf("close Version 2 connection: %v", err)
	}
	if journalMode != "wal" {
		t.Fatalf("runtime journal mode = %q, want wal", journalMode)
	}
	decoded, err := hex.DecodeString(attemptID)
	if err != nil || len(decoded) != 16 || hex.EncodeToString(decoded) != attemptID {
		t.Fatalf("migration attempt ID = %q, want canonical 32-lower-hex", attemptID)
	}
	wantBackup := ".amsftp-backup-v1-" + attemptID + ".sqlite3"
	if backupBasename != wantBackup {
		t.Fatalf("backup basename = %q, want %q", backupBasename, wantBackup)
	}
	if _, err := os.Lstat(filepath.Join(root, backupBasename)); err != nil {
		t.Fatalf("stat default migration backup: %v", err)
	}
	if err := database.Close(); err != nil {
		t.Fatalf("close default Version 2 database: %v", err)
	}

	backupBefore, err := os.Lstat(filepath.Join(root, backupBasename))
	if err != nil {
		t.Fatalf("stat backup before reopen: %v", err)
	}
	reopened, reopenReport, err := Initialize(ctx, InitializeConfig{
		Root: root, DatabasePath: path,
		Random: strings.NewReader(strings.Repeat("r", probeRandomBytes)), Now: time.Unix(1_101, 0),
	})
	if err != nil {
		t.Fatalf("Initialize(reopen Version 2): %v", err)
	}
	if reopenReport.Bootstrapped || reopenReport.SchemaHead != 3 {
		t.Fatalf("reopen report = %#v", reopenReport)
	}
	var backups int
	if err := reopened.QueryRowContext(ctx, "SELECT count(*) FROM migration_backups WHERE status='verified'").Scan(&backups); err != nil || backups != 1 {
		t.Fatalf("verified backup count after reopen = %d, error=%v", backups, err)
	}
	if err := reopened.Close(); err != nil {
		t.Fatalf("close reopened Version 2 database: %v", err)
	}
	backupAfter, err := os.Lstat(filepath.Join(root, backupBasename))
	if err != nil {
		t.Fatalf("stat backup after reopen: %v", err)
	}
	if !os.SameFile(backupBefore, backupAfter) {
		t.Fatal("reopen replaced the verified migration backup")
	}
}

func TestInitializeUpgradesFrozenVersion2HeadToVersion3WithSeparateRollbackBackup(t *testing.T) {
	ctx := context.Background()
	root := privateTempDir(t)
	path := filepath.Join(root, "amsftp.db")
	v2 := initializeVersion2WithExplicitSet(t, ctx, root, path, time.Unix(1_150, 0), strings.Repeat("2", 32))
	if err := v2.Close(); err != nil {
		t.Fatal(err)
	}
	database, report, err := Initialize(ctx, InitializeConfig{
		Root: root, DatabasePath: path, Random: strings.NewReader(strings.Repeat("v", probeRandomBytes)),
		Now: time.Unix(1_151, 0), MigrationAttemptID: strings.Repeat("3", 32),
	})
	if err != nil {
		t.Fatalf("upgrade Version 2 to Version 3: %v", err)
	}
	defer database.Close()
	if report.SchemaHead != 3 {
		t.Fatalf("upgrade report = %#v", report)
	}
	var backups int
	if err := database.QueryRowContext(ctx, "SELECT count(*) FROM migration_backups WHERE status='verified'").Scan(&backups); err != nil || backups != 2 {
		t.Fatalf("verified rollback backups = %d, error=%v", backups, err)
	}
	connection, err := database.Conn(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer connection.Close()
	if err := migration.ValidateHead(ctx, connection,
		[]migration.Migration{migration.Version1(), migration.Version2(), migration.Version3()},
		map[uint64][]byte{1: migration.Version1SchemaContract(), 2: migration.Version2SchemaContract(), 3: migration.Version3SchemaContract()}, 3); err != nil {
		t.Fatalf("validate upgraded Version 3 head: %v", err)
	}
}

func TestInitializePersistedNonterminalVersion2AttemptsRequireExplicitResumeAndReuseBackup(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		attemptID  string
		transition func(context.Context, *sql.Conn, string) error
	}{
		{
			name: "running", attemptID: strings.Repeat("a", 32),
			transition: func(ctx context.Context, connection *sql.Conn, attemptID string) error {
				_, err := migration.MarkAttemptRunning(ctx, connection, attemptID)
				return err
			},
		},
		{
			name: "interrupted", attemptID: strings.Repeat("b", 32),
			transition: func(ctx context.Context, connection *sql.Conn, attemptID string) error {
				if _, err := migration.MarkAttemptRunning(ctx, connection, attemptID); err != nil {
					return err
				}
				_, err := migration.MarkAttemptInterrupted(ctx, connection, attemptID, "test_interrupt")
				return err
			},
		},
		{
			name: "failed", attemptID: strings.Repeat("c", 32),
			transition: func(ctx context.Context, connection *sql.Conn, attemptID string) error {
				_, err := migration.MarkAttemptFailed(ctx, connection, attemptID, "test_failure")
				return err
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			ctx := context.Background()
			root := privateTempDir(t)
			path := filepath.Join(root, "amsftp.db")
			database := initializeVersion1Only(t, ctx, root, path, time.Unix(1_200, 0))
			migrations, contracts := compiledVersion2State()
			digests, err := migration.SchemaContractDigests(migrations, contracts)
			if err != nil {
				t.Fatalf("build schema contract digests: %v", err)
			}
			setDigest, err := migration.MigrationSetDigest(1, 2, migrations, digests)
			if err != nil {
				t.Fatalf("build Version 2 migration-set digest: %v", err)
			}
			connection, err := database.Conn(ctx)
			if err != nil {
				t.Fatalf("reserve Version 1 connection: %v", err)
			}
			request := migration.AttemptRequest{AttemptID: tt.attemptID, OriginalHead: 1, TargetHead: 2, MigrationSetDigest: setDigest}
			attempt, _, err := migration.PrepareAttempt(ctx, connection, request)
			if err != nil {
				t.Fatalf("prepare persisted attempt: %v", err)
			}
			backupPath, _, err := CreateMigrationBackup(ctx, BackupConfig{
				Root: root, Source: database, Attempt: attempt, CreatedAt: time.Unix(1_201, 0),
				Migrations: migrations, SchemaContracts: contracts,
			})
			if err != nil {
				t.Fatalf("create persisted attempt backup: %v", err)
			}
			if err := tt.transition(ctx, connection, tt.attemptID); err != nil {
				t.Fatalf("transition persisted attempt: %v", err)
			}
			if err := connection.Close(); err != nil {
				t.Fatalf("close persisted attempt connection: %v", err)
			}
			if err := database.Close(); err != nil {
				t.Fatalf("close persisted attempt database: %v", err)
			}
			backupBefore, err := os.Lstat(backupPath)
			if err != nil {
				t.Fatalf("stat persisted backup: %v", err)
			}

			_, _, err = Initialize(ctx, InitializeConfig{
				Root: root, DatabasePath: path,
				Random: strings.NewReader(strings.Repeat("p", probeRandomBytes)), Now: time.Unix(1_202, 0),
				Migrations: migrations, SchemaContracts: contracts,
			})
			if !errors.Is(err, ErrExplicitMigrationResumeRequired) {
				t.Fatalf("Initialize(implicit %s resume) error = %v", tt.name, err)
			}
			backupAfterImplicit, err := os.Lstat(backupPath)
			if err != nil {
				t.Fatalf("stat backup after implicit refusal: %v", err)
			}
			if !os.SameFile(backupBefore, backupAfterImplicit) {
				t.Fatal("implicit resume refusal replaced the persisted backup")
			}

			resumed, report, err := Initialize(ctx, InitializeConfig{
				Root: root, DatabasePath: path,
				Random: strings.NewReader(strings.Repeat("q", probeRandomBytes)), Now: time.Unix(1_203, 0),
				ExplicitMigrationResume: true,
				Migrations:              migrations, SchemaContracts: contracts,
			})
			if err != nil {
				t.Fatalf("Initialize(explicit %s resume): %v", tt.name, err)
			}
			if report.SchemaHead != 2 {
				t.Fatalf("explicit resume report = %#v", report)
			}
			var backups int
			if err := resumed.QueryRowContext(ctx, "SELECT count(*) FROM migration_backups WHERE attempt_id=? AND status='verified'", tt.attemptID).Scan(&backups); err != nil || backups != 1 {
				t.Fatalf("persisted backup rows after resume = %d, error=%v", backups, err)
			}
			if err := resumed.Close(); err != nil {
				t.Fatalf("close explicitly resumed database: %v", err)
			}
			backupAfterResume, err := os.Lstat(backupPath)
			if err != nil {
				t.Fatalf("stat backup after explicit resume: %v", err)
			}
			if !os.SameFile(backupBefore, backupAfterResume) {
				t.Fatal("explicit resume replaced the persisted backup")
			}
		})
	}
}

func TestInitializeCompiledStateValidationFailsClosed(t *testing.T) {
	t.Parallel()

	t.Run("missing Version 2 contract", func(t *testing.T) {
		t.Parallel()
		root := privateTempDir(t)
		path := filepath.Join(root, "amsftp.db")
		migrations, _ := compiledVersion2State()
		_, _, err := Initialize(context.Background(), InitializeConfig{
			Root: root, DatabasePath: path, Migrations: migrations,
			SchemaContracts: map[uint64][]byte{1: migration.Version1SchemaContract()},
		})
		if err == nil || !strings.Contains(err.Error(), "schema contract") {
			t.Fatalf("Initialize(missing Version 2 contract) error = %v", err)
		}
		if entries := directoryEntries(t, root); len(entries) != 0 {
			t.Fatalf("missing contract rejection wrote artifacts: %v", entries)
		}
	})

	t.Run("changed Version 2 contract", func(t *testing.T) {
		t.Parallel()
		ctx := context.Background()
		root := privateTempDir(t)
		path := filepath.Join(root, "amsftp.db")
		database := initializeVersion2WithExplicitSet(t, ctx, root, path, time.Unix(1_300, 0), strings.Repeat("d", 32))
		if err := database.Close(); err != nil {
			t.Fatalf("close Version 2 fixture: %v", err)
		}
		before := directoryEntries(t, root)
		migrations, contracts := compiledVersion2State()
		contracts[2] = append([]byte(nil), contracts[2]...)
		contracts[2][0] ^= 0xff
		_, _, err := Initialize(ctx, InitializeConfig{
			Root: root, DatabasePath: path, Migrations: migrations, SchemaContracts: contracts,
			Random: strings.NewReader(""), Now: time.Unix(1_301, 0),
		})
		if err == nil {
			t.Fatalf("Initialize(changed Version 2 contract) error = %v", err)
		}
		if after := directoryEntries(t, root); !reflect.DeepEqual(after, before) {
			t.Fatalf("changed contract rejection changed artifacts: before=%v after=%v", before, after)
		}
	})

	t.Run("Version 2 head against Version 1 binary", func(t *testing.T) {
		t.Parallel()
		ctx := context.Background()
		root := privateTempDir(t)
		path := filepath.Join(root, "amsftp.db")
		database := initializeVersion2WithExplicitSet(t, ctx, root, path, time.Unix(1_400, 0), strings.Repeat("e", 32))
		if err := database.Close(); err != nil {
			t.Fatalf("close Version 2 fixture: %v", err)
		}
		before := directoryEntries(t, root)
		_, _, err := Initialize(ctx, version1OnlyConfig(root, path, time.Unix(1_401, 0)))
		if err == nil {
			t.Fatalf("Initialize(Version 1 binary on Version 2 head) error = %v", err)
		}
		if after := directoryEntries(t, root); !reflect.DeepEqual(after, before) {
			t.Fatalf("newer-head rejection changed artifacts: before=%v after=%v", before, after)
		}
	})

	t.Run("Version 3 head against default Version 2 binary", func(t *testing.T) {
		t.Parallel()
		ctx := context.Background()
		root := privateTempDir(t)
		path := filepath.Join(root, "amsftp.db")
		database := initializeVersion1Only(t, ctx, root, path, time.Unix(1_500, 0))
		if err := database.Close(); err != nil {
			t.Fatalf("close Version 1 future-head source: %v", err)
		}
		migrations, contracts := coordinatorFixture(t, ctx)
		future, report, err := Initialize(ctx, InitializeConfig{
			Root: root, DatabasePath: path,
			Random: strings.NewReader(strings.Repeat("f", probeRandomBytes)), Now: time.Unix(1_501, 0),
			Migrations: migrations, SchemaContracts: contracts,
			MigrationAttemptID: strings.Repeat("f", 32),
		})
		if err != nil {
			t.Fatalf("Initialize(Version 3 fixture): %v", err)
		}
		if report.SchemaHead != 3 {
			t.Fatalf("Version 3 fixture report = %#v", report)
		}
		if err := future.Close(); err != nil {
			t.Fatalf("close Version 3 fixture: %v", err)
		}
		before := directoryEntries(t, root)
		_, _, err = Initialize(ctx, InitializeConfig{
			Root: root, DatabasePath: path,
			Random: strings.NewReader(""), Now: time.Unix(1_502, 0),
		})
		if err == nil {
			t.Fatal("Initialize(default Version 2 binary on Version 3 head) error = nil")
		}
		if after := directoryEntries(t, root); !reflect.DeepEqual(after, before) {
			t.Fatalf("future-head rejection changed artifacts: before=%v after=%v", before, after)
		}
	})
}

func compiledVersion2State() ([]migration.Migration, map[uint64][]byte) {
	return []migration.Migration{migration.Version1(), migration.Version2()}, map[uint64][]byte{
		1: migration.Version1SchemaContract(),
		2: migration.Version2SchemaContract(),
	}
}

func version1OnlyConfig(root, path string, now time.Time) InitializeConfig {
	return withVersion1CompiledState(InitializeConfig{
		Root: root, DatabasePath: path,
		Random: strings.NewReader(strings.Repeat("v", probeRandomBytes+16)), Now: now,
	})
}

func withVersion1CompiledState(config InitializeConfig) InitializeConfig {
	config.Migrations = []migration.Migration{migration.Version1()}
	config.SchemaContracts = map[uint64][]byte{1: migration.Version1SchemaContract()}
	return config
}

func initializeVersion1Only(t *testing.T, ctx context.Context, root, path string, now time.Time) *sql.DB {
	t.Helper()
	database, report, err := Initialize(ctx, version1OnlyConfig(root, path, now))
	if err != nil {
		t.Fatalf("Initialize(Version 1 fixture): %v", err)
	}
	if report.SchemaHead != 1 {
		t.Fatalf("Version 1 fixture report = %#v", report)
	}
	return database
}

func initializeVersion2WithExplicitSet(t *testing.T, ctx context.Context, root, path string, now time.Time, attemptID string) *sql.DB {
	t.Helper()
	migrations, contracts := compiledVersion2State()
	database, report, err := Initialize(ctx, InitializeConfig{
		Root: root, DatabasePath: path,
		Random: strings.NewReader(strings.Repeat("w", probeRandomBytes+16)), Now: now,
		Migrations: migrations, SchemaContracts: contracts, MigrationAttemptID: attemptID,
	})
	if err != nil {
		t.Fatalf("Initialize(Version 2 fixture): %v", err)
	}
	if report.SchemaHead != 2 {
		t.Fatalf("Version 2 fixture report = %#v", report)
	}
	return database
}

func TestInitializeBootstrapsVersion1AndReopensExistingState(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	root := privateTempDir(t)
	databasePath := filepath.Join(root, "amsftp.db")
	random := strings.NewReader(strings.Repeat("i", probeRandomBytes+16))
	database, report, err := Initialize(ctx, withVersion1CompiledState(InitializeConfig{Root: root, DatabasePath: databasePath, Random: random, Now: time.Unix(1_000, 0)}))
	if err != nil {
		t.Fatalf("Initialize(pristine): %v", err)
	}
	if !report.Bootstrapped || report.SchemaHead != 1 {
		t.Fatalf("bootstrap report = %#v", report)
	}
	if err := database.Close(); err != nil {
		t.Fatalf("close bootstrapped database: %v", err)
	}
	identity, err := PreflightIdentity(databasePath)
	if err != nil {
		t.Fatalf("preflight bootstrapped database: %v", err)
	}
	if identity.Kind != IdentityProject || identity.HasSidecars {
		t.Fatalf("bootstrapped identity = %#v", identity)
	}
	for _, entry := range directoryEntries(t, root) {
		if entry != "amsftp.db" {
			t.Fatalf("unexpected bootstrap artifact %q", entry)
		}
	}

	reopened, reopenReport, err := Initialize(ctx, withVersion1CompiledState(InitializeConfig{Root: root, DatabasePath: databasePath, Random: strings.NewReader(strings.Repeat("p", probeRandomBytes))}))
	if err != nil {
		t.Fatalf("Initialize(existing): %v", err)
	}
	if reopenReport.Bootstrapped || reopenReport.SchemaHead != 1 {
		t.Fatalf("reopen report = %#v", reopenReport)
	}
	if err := reopened.Close(); err != nil {
		t.Fatalf("close reopened database: %v", err)
	}
}

func TestInitializeRejectsForeignFinalBeforeProbeWrites(t *testing.T) {
	t.Parallel()

	root := privateTempDir(t)
	path := filepath.Join(root, "amsftp.db")
	content := []byte("foreign state")
	if err := os.WriteFile(path, content, 0o600); err != nil {
		t.Fatalf("write foreign final: %v", err)
	}
	before := directoryEntries(t, root)
	beforeRootInfo, err := os.Lstat(root)
	if err != nil {
		t.Fatalf("stat foreign database parent: %v", err)
	}
	beforeFileInfo, err := os.Lstat(path)
	if err != nil {
		t.Fatalf("stat foreign database: %v", err)
	}
	if _, _, err := Initialize(context.Background(), InitializeConfig{Root: root, DatabasePath: path, Random: strings.NewReader(strings.Repeat("x", 64))}); err == nil {
		t.Fatal("Initialize(foreign) error = nil")
	}
	after := directoryEntries(t, root)
	if !reflect.DeepEqual(before, after) {
		t.Fatalf("foreign rejection changed directory: before=%v after=%v", before, after)
	}
	got, err := os.ReadFile(path) //nolint:gosec // test-owned path
	if err != nil {
		t.Fatalf("read foreign final: %v", err)
	}
	if !reflect.DeepEqual(got, content) {
		t.Fatalf("foreign content changed: %q", got)
	}
	afterRootInfo, err := os.Lstat(root)
	if err != nil {
		t.Fatalf("stat foreign database parent after rejection: %v", err)
	}
	afterFileInfo, err := os.Lstat(path)
	if err != nil {
		t.Fatalf("stat foreign database after rejection: %v", err)
	}
	if afterRootInfo.Mode() != beforeRootInfo.Mode() || afterRootInfo.Size() != beforeRootInfo.Size() || !afterRootInfo.ModTime().Equal(beforeRootInfo.ModTime()) {
		t.Fatalf("foreign database parent metadata changed: before=%v after=%v", beforeRootInfo, afterRootInfo)
	}
	if afterFileInfo.Mode() != beforeFileInfo.Mode() || afterFileInfo.Size() != beforeFileInfo.Size() || !afterFileInfo.ModTime().Equal(beforeFileInfo.ModTime()) {
		t.Fatalf("foreign database attrs changed: before=%v after=%v", beforeFileInfo, afterFileInfo)
	}
}

func TestInitializeUpgradesThenReopensOnlyTheImmutableValidatedTarget(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	root := privateTempDir(t)
	path := filepath.Join(root, "amsftp.db")
	initial, _, err := Initialize(ctx, withVersion1CompiledState(InitializeConfig{
		Root: root, DatabasePath: path,
		Random: strings.NewReader(strings.Repeat("k", probeRandomBytes+16)), Now: time.Unix(900, 0),
	}))
	if err != nil {
		t.Fatalf("initialize version 1: %v", err)
	}
	if err := initial.Close(); err != nil {
		t.Fatalf("close version 1 runtime: %v", err)
	}
	migrations, contracts := coordinatorFixture(t, ctx)
	upgraded, report, err := Initialize(ctx, InitializeConfig{
		Root: root, DatabasePath: path,
		Random: strings.NewReader(strings.Repeat("l", probeRandomBytes)), Now: time.Unix(901, 0),
		Migrations: migrations, SchemaContracts: contracts,
		MigrationAttemptID: "99999999999999999999999999999999",
	})
	if err != nil {
		t.Fatalf("Initialize(upgrade): %v", err)
	}
	defer func() {
		if err := upgraded.Close(); err != nil {
			t.Errorf("close upgraded runtime: %v", err)
		}
	}()
	if report.Bootstrapped || report.SchemaHead != 3 {
		t.Fatalf("upgrade initialize report = %#v", report)
	}
	connection, err := upgraded.Conn(ctx)
	if err != nil {
		t.Fatalf("reserve reopened target connection: %v", err)
	}
	defer func() {
		if err := connection.Close(); err != nil {
			t.Errorf("close reopened target connection: %v", err)
		}
	}()
	if err := migration.ValidateHead(ctx, connection, migrations, contracts, 3); err != nil {
		t.Fatalf("validate reopened target: %v", err)
	}
	var attempts int
	if err := connection.QueryRowContext(ctx, "SELECT count(*) FROM migration_attempts").Scan(&attempts); err != nil || attempts != 0 {
		t.Fatalf("reopened target attempts = %d, error=%v", attempts, err)
	}
}

func TestRecoverBootstrapIntentRemovesOnlyClaimedGeneration(t *testing.T) {
	t.Parallel()

	root := privateTempDir(t)
	generation := strings.Repeat("a", 32)
	intent := filepath.Join(root, bootstrapIntentName)
	temp := filepath.Join(root, bootstrapPrefix+generation+bootstrapSuffix)
	decoy := filepath.Join(root, bootstrapPrefix+strings.Repeat("b", 32)+bootstrapSuffix)
	if err := os.WriteFile(intent, []byte(generation), 0o600); err != nil {
		t.Fatalf("write intent: %v", err)
	}
	if err := os.WriteFile(temp, []byte("partial"), 0o600); err != nil {
		t.Fatalf("write claimed temp: %v", err)
	}
	if err := os.WriteFile(decoy, []byte("decoy"), 0o600); err != nil {
		t.Fatalf("write decoy temp: %v", err)
	}
	recovered, err := recoverBootstrapIntent(context.Background(), root, filepath.Join(root, "amsftp.db"))
	if err != nil {
		t.Fatalf("recoverBootstrapIntent(): %v", err)
	}
	if !recovered {
		t.Fatal("recovery did not report work")
	}
	if _, err := os.Lstat(temp); !os.IsNotExist(err) {
		t.Fatalf("claimed temp remains: %v", err)
	}
	if _, err := os.Lstat(intent); !os.IsNotExist(err) {
		t.Fatalf("intent remains: %v", err)
	}
	if _, err := os.Lstat(decoy); err != nil {
		t.Fatalf("decoy was removed: %v", err)
	}
}
