package statefs

import (
	"context"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/TyrantLucifer/awesome-mac-sftp/internal/state/migration"
)

func TestInitializeBootstrapsVersion1AndReopensExistingState(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	root := privateTempDir(t)
	databasePath := filepath.Join(root, "amsftp.db")
	random := strings.NewReader(strings.Repeat("i", probeRandomBytes+16))
	database, report, err := Initialize(ctx, InitializeConfig{Root: root, DatabasePath: databasePath, Random: random, Now: time.Unix(1_000, 0)})
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

	reopened, reopenReport, err := Initialize(ctx, InitializeConfig{Root: root, DatabasePath: databasePath, Random: strings.NewReader(strings.Repeat("p", probeRandomBytes))})
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
	initial, _, err := Initialize(ctx, InitializeConfig{
		Root: root, DatabasePath: path,
		Random: strings.NewReader(strings.Repeat("k", probeRandomBytes+16)), Now: time.Unix(900, 0),
	})
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
