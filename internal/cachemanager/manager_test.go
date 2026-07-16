package cachemanager

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/hex"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/TyrantLucifer/awesome-mac-sftp/internal/cache"
	"github.com/TyrantLucifer/awesome-mac-sftp/internal/cachefs"
	"github.com/TyrantLucifer/awesome-mac-sftp/internal/domain"
	"github.com/TyrantLucifer/awesome-mac-sftp/internal/state/cachestore"
	"github.com/TyrantLucifer/awesome-mac-sftp/internal/state/migration"
	_ "github.com/TyrantLucifer/awesome-mac-sftp/internal/state/sqlite"
	"github.com/TyrantLucifer/awesome-mac-sftp/internal/testkit"
)

func TestPublishCompleteBindsLocationFingerprintAndDeduplicatedContent(t *testing.T) {
	manager, _, _ := newManager(t)
	ctx := context.Background()
	content := []byte("same verified content")
	fingerprint := testSourceFingerprint(uint64(len(content)))

	first, err := manager.PublishComplete(ctx, PublishRequest{
		Location:          testLocation(t, "/one"),
		SourceFingerprint: fingerprint,
		WorkspaceID:       "workspace-a",
		Policy:            cache.PolicyLRU,
		Source:            bytes.NewReader(content),
		MaxBytes:          int64(len(content)),
		ExpectedSize:      sizePointer(int64(len(content))),
	})
	if err != nil {
		t.Fatal(err)
	}
	second, err := manager.PublishComplete(ctx, PublishRequest{
		Location:          testLocation(t, "/two"),
		SourceFingerprint: fingerprint,
		WorkspaceID:       "workspace-b",
		Policy:            cache.PolicyEphemeral,
		Source:            bytes.NewReader(content),
		MaxBytes:          int64(len(content)),
		ExpectedSize:      sizePointer(int64(len(content))),
	})
	if err != nil {
		t.Fatal(err)
	}
	if first.Entry.ID == second.Entry.ID || first.Blob.ID != second.Blob.ID || !second.Deduplicated {
		t.Fatalf("publications = %#v %#v", first, second)
	}
	snapshot, err := manager.catalog.LoadSnapshot(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(snapshot.Blobs) != 1 || len(snapshot.Entries) != 2 {
		t.Fatalf("snapshot = %#v", snapshot)
	}
}

func TestPublishCompleteRejectsShortOrWeakSourceWithoutCatalogRows(t *testing.T) {
	manager, _, _ := newManager(t)
	request := PublishRequest{
		Location:          testLocation(t, "/short"),
		SourceFingerprint: testSourceFingerprint(10),
		WorkspaceID:       "workspace",
		Policy:            cache.PolicyLRU,
		Source:            bytes.NewReader([]byte("short")),
		MaxBytes:          10,
		ExpectedSize:      sizePointer(10),
	}
	if _, err := manager.PublishComplete(context.Background(), request); err == nil {
		t.Fatal("short publication succeeded")
	}
	request.SourceFingerprint = domain.Fingerprint{}
	request.ExpectedSize = sizePointer(5)
	request.MaxBytes = 5
	request.Source = bytes.NewReader([]byte("short"))
	if _, err := manager.PublishComplete(context.Background(), request); err == nil {
		t.Fatal("weak source publication succeeded")
	}
	snapshot, err := manager.catalog.LoadSnapshot(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(snapshot.Blobs) != 0 || len(snapshot.Entries) != 0 {
		t.Fatalf("failed publication wrote catalog rows: %#v", snapshot)
	}
}

func TestPrepareHandoffPublishesMaterializationBeforeAtomicReachabilityAndLease(t *testing.T) {
	manager, _, _ := newManager(t)
	ctx := context.Background()
	content := []byte("editable")
	published, err := manager.PublishComplete(ctx, PublishRequest{
		Location: testLocation(t, "/edit"), SourceFingerprint: testSourceFingerprint(uint64(len(content))), WorkspaceID: "workspace",
		Policy: cache.PolicyLRU, Source: bytes.NewReader(content), MaxBytes: int64(len(content)), ExpectedSize: sizePointer(int64(len(content))),
	})
	if err != nil {
		t.Fatal(err)
	}
	result, err := manager.PrepareHandoff(ctx, HandoffRequest{
		EntryID: published.Entry.ID, MaterializationID: cache.MaterializationID(strings.Repeat("a", 32)),
		ReferenceID: cache.ReferenceID(strings.Repeat("b", 32)), LeaseID: cache.LeaseID(strings.Repeat("c", 32)),
		OwnerKind: cache.LeaseOwnerEditor, OwnerID: "edit-session", Process: &cache.ProcessIdentity{PID: 123, BirthID: "birth"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.Path == "" || result.Lease.Target.MaterializationID != result.Materialization.ID {
		t.Fatalf("handoff = %#v", result)
	}
	if got, err := os.ReadFile(result.Path); err != nil || !bytes.Equal(got, content) {
		t.Fatalf("materialization content = %q, %v", got, err)
	}
	snapshot, err := manager.catalog.LoadSnapshot(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(snapshot.Materializations) != 1 || len(snapshot.References) != 1 || len(snapshot.Leases) != 1 {
		t.Fatalf("snapshot = %#v", snapshot)
	}
	if err := os.WriteFile(result.Path, []byte("locally changed"), 0o600); err != nil {
		t.Fatal(err)
	}
	reconcile, err := manager.Reconcile(ctx, 100)
	if err != nil {
		t.Fatal(err)
	}
	if len(reconcile.DirtyCandidates) != 1 || reconcile.DirtyCandidates[0] != result.Materialization.ID || !reconcile.NeedsAttention {
		t.Fatalf("dirty reconcile = %#v", reconcile)
	}
}

func TestReconcilePreservesAndReportsFilesystemOrphans(t *testing.T) {
	manager, files, _ := newManager(t)
	orphan, err := files.PublishBlob(context.Background(), bytes.NewReader([]byte("orphan")), 6, nil)
	if err != nil {
		t.Fatal(err)
	}
	report, err := manager.Reconcile(context.Background(), 100)
	if err != nil {
		t.Fatal(err)
	}
	if !report.NeedsAttention || len(report.UncatalogedBlobs) != 1 || report.UncatalogedBlobs[0] != orphan.Identity.ID {
		t.Fatalf("report = %#v", report)
	}
	if _, err := files.InspectBlob(orphan.Identity.ID); err != nil {
		t.Fatalf("orphan was not preserved: %v", err)
	}
}

type fixedClock struct{ now time.Time }

func (clock fixedClock) Now() time.Time { return clock.now }

func newManager(t *testing.T) (*Manager, *cachefs.Store, *sql.DB) {
	t.Helper()
	ctx := context.Background()
	root := testkit.PersistentTempDir(t)
	cacheRoot := filepath.Join(root, "cache")
	if err := os.Mkdir(cacheRoot, 0o700); err != nil {
		t.Fatal(err)
	}
	files, err := cachefs.Initialize(cacheRoot)
	if err != nil {
		t.Fatal(err)
	}
	database := newVersion2Database(t, ctx, filepath.Join(root, "state.db"))
	catalog, err := cachestore.New(ctx, database)
	if err != nil {
		t.Fatal(err)
	}
	manager, err := New(files, catalog, fixedClock{now: time.Unix(1_700_000_000, 0).UTC()}, strings.Repeat("d", 32), cache.DefaultLimits())
	if err != nil {
		t.Fatal(err)
	}
	return manager, files, database
}

func newVersion2Database(t *testing.T, ctx context.Context, path string) *sql.DB {
	t.Helper()
	dsn := "file:" + path + "?_pragma=foreign_keys(1)&_pragma=journal_mode(WAL)&_pragma=synchronous(FULL)&_pragma=busy_timeout(5000)"
	database, err := sql.Open("sqlite", dsn)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = database.Close() })
	database.SetMaxOpenConns(8)
	for index, item := range []migration.Migration{migration.Version1(), migration.Version2()} {
		for _, statement := range item.Statements {
			if _, err := database.ExecContext(ctx, statement); err != nil {
				t.Fatalf("apply v%d: %v", index+1, err)
			}
		}
		sum, _ := migration.Checksum(item)
		if _, err := database.ExecContext(ctx, "INSERT INTO schema_migrations(version,name,sha256,applied_at) VALUES(?,?,?,?)", index+1, item.Name, hex.EncodeToString(sum[:]), "2026-07-16T00:00:00Z"); err != nil {
			t.Fatal(err)
		}
	}
	for _, candidate := range []string{path, path + "-wal"} {
		if err := os.Chmod(candidate, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	return database
}

func testLocation(t *testing.T, path string) domain.Location {
	t.Helper()
	location, err := domain.NewLocation(domain.EndpointID("ep_aaaaaaaaaaaaaaaaaaaaaaaaaa"), domain.CanonicalPath(path))
	if err != nil {
		t.Fatal(err)
	}
	return location
}

func testSourceFingerprint(size uint64) domain.Fingerprint {
	modified := time.Unix(1_699_999_999, 0).UTC()
	precision := domain.TimePrecision("second")
	return domain.Fingerprint{Size: &size, ModifiedAt: &modified, ModifiedPrecision: &precision}
}

func sizePointer(value int64) *int64 { return &value }
