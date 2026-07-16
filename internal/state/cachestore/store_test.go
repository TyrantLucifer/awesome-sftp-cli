package cachestore

import (
	"context"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/TyrantLucifer/awesome-mac-sftp/internal/cache"
	"github.com/TyrantLucifer/awesome-mac-sftp/internal/state/migration"
	"github.com/TyrantLucifer/awesome-mac-sftp/internal/testkit"
	_ "modernc.org/sqlite"
)

func TestStoreRoundTripsCompleteCatalogAndReopens(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	database := newVersion2Database(t, ctx)
	store, err := New(ctx, database)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	blob, entry, materialization, reference, lease := validGraph()
	if err := store.Publish(ctx, blob, entry); err != nil {
		t.Fatalf("Publish: %v", err)
	}
	if err := store.CreateMaterialization(ctx, materialization); err != nil {
		t.Fatalf("CreateMaterialization: %v", err)
	}
	if err := store.AddReference(ctx, reference); err != nil {
		t.Fatalf("AddReference: %v", err)
	}
	if err := store.AcquireLease(ctx, lease); err != nil {
		t.Fatalf("AcquireLease: %v", err)
	}

	got, err := store.LoadSnapshot(ctx)
	if err != nil {
		t.Fatalf("LoadSnapshot: %v", err)
	}
	want := cache.Snapshot{Blobs: []cache.Blob{blob}, Entries: []cache.Entry{entry}, Materializations: []cache.Materialization{materialization}, References: []cache.Reference{reference}, Leases: []cache.Lease{lease}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("snapshot = %#v, want %#v", got, want)
	}

	reopened, err := New(ctx, database)
	if err != nil {
		t.Fatalf("reopen: %v", err)
	}
	if _, err := reopened.GetBlob(ctx, blob.ID); err != nil {
		t.Fatalf("GetBlob after reopen: %v", err)
	}
	if _, err := reopened.GetEntry(ctx, entry.ID); err != nil {
		t.Fatalf("GetEntry after reopen: %v", err)
	}
}

func TestPublishRollsBackBlobWhenEntryFails(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	database := newVersion2Database(t, ctx)
	store := newStore(t, ctx, database)
	blob, entry, _, _, _ := validGraph()
	if err := store.Publish(ctx, blob, entry); err != nil {
		t.Fatal(err)
	}
	secondBlob := blob
	secondBlob.ID = cache.BlobID(strings.Repeat("1", 64))
	entry.BlobID = secondBlob.ID
	if err := store.Publish(ctx, secondBlob, entry); err == nil {
		t.Fatal("Publish succeeded with duplicate entry")
	}
	var count int
	if err := database.QueryRowContext(ctx, "SELECT count(*) FROM cache_blobs").Scan(&count); err != nil || count != 1 {
		t.Fatalf("blob count = %d, err %v", count, err)
	}
}

func TestPublishReusesVerifiedBlobForDistinctEntries(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	database := newVersion2Database(t, ctx)
	store := newStore(t, ctx, database)
	blob, entry, _, _, _ := validGraph()
	must(t, store.Publish(ctx, blob, entry))
	second := entry
	second.ID = cache.EntryID(strings.Repeat("2", 64))
	second.CanonicalPath = []byte("/other")
	second.Fingerprint = cache.Fingerprint{Strength: cache.FingerprintWeak, Canonical: []byte("weak-fingerprint")}
	second.LastAccessAt = time.Unix(11, 0).UTC()
	must(t, store.Publish(ctx, blob, second))
	var blobs, entries int
	must(t, database.QueryRowContext(ctx, "SELECT count(*) FROM cache_blobs").Scan(&blobs))
	must(t, database.QueryRowContext(ctx, "SELECT count(*) FROM cache_entries").Scan(&entries))
	if blobs != 1 || entries != 2 {
		t.Fatalf("counts = blobs %d entries %d", blobs, entries)
	}
}

func TestLeaseOwnerTranslationsRoundTrip(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	database := newVersion2Database(t, ctx)
	store := newStore(t, ctx, database)
	blob, entry, materialization, _, lease := validGraph()
	must(t, store.Publish(ctx, blob, entry))
	must(t, store.CreateMaterialization(ctx, materialization))
	wantOwners := []cache.LeaseOwnerKind{cache.LeaseOwnerPreview, cache.LeaseOwnerEditor, cache.LeaseOwnerOpener, cache.LeaseOwnerUpload}
	wantSQL := []string{"preview", "edit", "open", "upload"}
	for index, owner := range wantOwners {
		item := lease
		item.ID = cache.LeaseID(fmt.Sprintf("%032x", index+1))
		item.OwnerKind = owner
		must(t, store.AcquireLease(ctx, item))
		var encoded string
		if err := database.QueryRowContext(ctx, "SELECT owner_kind FROM cache_leases WHERE lease_id=?", item.ID).Scan(&encoded); err != nil || encoded != wantSQL[index] {
			t.Fatalf("owner %q SQL = %q, %v", owner, encoded, err)
		}
	}
	got, err := store.ListLeases(ctx)
	if err != nil {
		t.Fatal(err)
	}
	for index := range got {
		if got[index].OwnerKind != wantOwners[index] {
			t.Fatalf("owner %d = %q, want %q", index, got[index].OwnerKind, wantOwners[index])
		}
	}
}

func TestForeignKeysDuplicatesAndUnpersistableKindsFail(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	database := newVersion2Database(t, ctx)
	store := newStore(t, ctx, database)
	blob, entry, materialization, reference, lease := validGraph()
	if err := store.CreateMaterialization(ctx, materialization); err == nil {
		t.Fatal("missing entry foreign key accepted")
	}
	if err := store.Publish(ctx, blob, entry); err != nil {
		t.Fatal(err)
	}
	if err := store.Publish(ctx, blob, entry); err == nil {
		t.Fatal("duplicate publish accepted")
	}
	reference.OwnerKind = cache.ReferenceOwnerStage2Part
	if err := store.AddReference(ctx, reference); err == nil || !strings.Contains(err.Error(), "not persistable") {
		t.Fatalf("stage2 reference error = %v", err)
	}
	lease.DaemonInstanceID = "daemon-secret-token"
	if err := store.AcquireLease(ctx, lease); err == nil {
		t.Fatal("non-contract daemon ID accepted")
	}
}

func TestTouchUpdateReleaseAndRemove(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	database := newVersion2Database(t, ctx)
	store := newStore(t, ctx, database)
	blob, entry, materialization, reference, lease := validGraph()
	must(t, store.Publish(ctx, blob, entry))
	must(t, store.CreateMaterialization(ctx, materialization))
	must(t, store.AddReference(ctx, reference))
	must(t, store.AcquireLease(ctx, lease))
	next := time.Unix(20, 0).UTC()
	must(t, store.TouchBlob(ctx, blob.ID, next))
	must(t, store.TouchEntry(ctx, entry.ID, next))
	materialization.State = cache.MaterializationDirty
	materialization.LastAccessAt = next
	must(t, store.UpdateMaterialization(ctx, materialization))
	lease.HeartbeatAt = next
	lease.ExpiresAt = next.Add(time.Minute)
	lease.GraceUntil = next.Add(2 * time.Minute)
	must(t, store.HeartbeatLease(ctx, lease))
	lease.State = cache.LeaseReleased
	lease.ReleasedAt = time.Unix(21, 0).UTC()
	must(t, store.ReleaseLease(ctx, lease))
	must(t, store.RemoveReference(ctx, reference.ID))
	snapshot, err := store.LoadSnapshot(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if snapshot.Blobs[0].LastAccessAt != next || snapshot.Materializations[0].State != cache.MaterializationDirty || len(snapshot.References) != 0 || snapshot.Leases[0].State != cache.LeaseReleased {
		t.Fatalf("updated snapshot = %#v", snapshot)
	}
}

func TestPrepareHandoffAtomicallyCreatesMaterializationReferenceAndLease(t *testing.T) {
	ctx := context.Background()
	store := newStore(t, ctx, newVersion2Database(t, ctx))
	blob, entry, materialization, reference, lease := validGraph()
	reference.Target = cache.MaterializationTarget(materialization.ID)
	must(t, store.Publish(ctx, blob, entry))
	if err := store.PrepareHandoff(ctx, materialization, reference, lease); err != nil {
		t.Fatalf("PrepareHandoff() error = %v", err)
	}
	snapshot, err := store.LoadSnapshot(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(snapshot.Materializations) != 1 || len(snapshot.References) != 1 || len(snapshot.Leases) != 1 {
		t.Fatalf("snapshot = %#v", snapshot)
	}

	second := materialization
	second.ID = cache.MaterializationID(strings.Repeat("9", 32))
	secondReference := reference
	secondReference.ID = cache.ReferenceID(strings.Repeat("8", 32))
	secondReference.Target = cache.MaterializationTarget(second.ID)
	duplicateLease := lease
	duplicateLease.Target = cache.MaterializationTarget(second.ID)
	if err := store.PrepareHandoff(ctx, second, secondReference, duplicateLease); err == nil {
		t.Fatal("PrepareHandoff() accepted a duplicate lease")
	}
	snapshot, err = store.LoadSnapshot(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(snapshot.Materializations) != 1 || len(snapshot.References) != 1 || len(snapshot.Leases) != 1 {
		t.Fatalf("failed handoff left partial rows: %#v", snapshot)
	}
}

func TestMarkMaterializationDirtyRequiresExactActiveHandoff(t *testing.T) {
	ctx := context.Background()
	store := newStore(t, ctx, newVersion2Database(t, ctx))
	blob, entry, materialization, reference, lease := validGraph()
	reference.OwnerKind = cache.ReferenceOwnerEdit
	reference.OwnerID = lease.OwnerID
	reference.Target = cache.MaterializationTarget(materialization.ID)
	must(t, store.Publish(ctx, blob, entry))
	must(t, store.PrepareHandoff(ctx, materialization, reference, lease))

	request := MarkDirtyRequest{
		MaterializationID: materialization.ID, ReferenceID: reference.ID, LeaseID: lease.ID,
		OwnerKind: lease.OwnerKind, OwnerID: lease.OwnerID, CurrentBlobID: cache.BlobID(strings.Repeat("1", 64)),
		Size: 11, ObservedAt: time.Unix(20, 0).UTC(),
	}
	wrong := request
	wrong.OwnerID = "wrong-owner"
	if _, err := store.MarkMaterializationDirty(ctx, wrong); err == nil {
		t.Fatal("MarkMaterializationDirty accepted the wrong owner")
	}
	got, err := store.MarkMaterializationDirty(ctx, request)
	if err != nil {
		t.Fatal(err)
	}
	if got.State != cache.MaterializationDirty || got.CurrentBlobID != request.CurrentBlobID || got.Size != request.Size {
		t.Fatalf("dirty materialization = %#v", got)
	}
}

func TestBeginEvictionRechecksProtectionsAndPersistsCrashResumeClaim(t *testing.T) {
	ctx := context.Background()
	store := newStore(t, ctx, newVersion2Database(t, ctx))
	blob, entry, materialization, reference, lease := validGraph()
	reference.OwnerKind = cache.ReferenceOwnerEdit
	reference.OwnerID = lease.OwnerID
	reference.Target = cache.MaterializationTarget(materialization.ID)
	must(t, store.Publish(ctx, blob, entry))
	must(t, store.PrepareHandoff(ctx, materialization, reference, lease))

	if _, err := store.BeginEviction(ctx, cache.MaterializationEviction(materialization.ID), time.Unix(20, 0).UTC()); !errors.Is(err, ErrEvictionProtected) {
		t.Fatalf("BeginEviction protected error = %v", err)
	}
	must(t, store.ReleaseHandoff(ctx, ReleaseHandoffRequest{
		MaterializationID: materialization.ID, ReferenceID: reference.ID, LeaseID: lease.ID,
		OwnerKind: lease.OwnerKind, OwnerID: lease.OwnerID, ReleasedAt: time.Unix(21, 0).UTC(),
	}))
	claim, err := store.BeginEviction(ctx, cache.MaterializationEviction(materialization.ID), time.Unix(22, 0).UTC())
	if err != nil {
		t.Fatal(err)
	}
	if claim.Target.MaterializationID != materialization.ID {
		t.Fatalf("claim = %#v", claim)
	}
	pending, err := store.ListPendingEvictions(ctx, 8)
	if err != nil || len(pending) != 1 || pending[0] != claim {
		t.Fatalf("pending = %#v, %v", pending, err)
	}
	if err := store.FinalizeEviction(ctx, claim); err != nil {
		t.Fatal(err)
	}
	snapshot, err := store.LoadSnapshot(ctx)
	if err != nil || len(snapshot.Materializations) != 0 {
		t.Fatalf("snapshot after finalize = %#v, %v", snapshot, err)
	}
}

func TestEntryEvictionClaimRejectsConcurrentReferenceAndFinalizesEntryBlob(t *testing.T) {
	ctx := context.Background()
	store := newStore(t, ctx, newVersion2Database(t, ctx))
	blob, entry, _, reference, _ := validGraph()
	must(t, store.Publish(ctx, blob, entry))
	must(t, store.AddReference(ctx, reference))
	if _, err := store.BeginEviction(ctx, cache.EntryEviction(entry.ID), time.Unix(20, 0).UTC()); !errors.Is(err, ErrEvictionProtected) {
		t.Fatalf("BeginEviction with reference = %v", err)
	}
	must(t, store.RemoveReference(ctx, reference.ID))
	claim, err := store.BeginEviction(ctx, cache.EntryEviction(entry.ID), time.Unix(21, 0).UTC())
	if err != nil {
		t.Fatal(err)
	}
	if claim.EntryID != entry.ID || claim.BlobID != blob.ID {
		t.Fatalf("claim = %#v", claim)
	}
	if err := store.AddReference(ctx, reference); !errors.Is(err, ErrEvictionProtected) {
		t.Fatalf("AddReference against deleting blob = %v", err)
	}
	if err := store.FinalizeEviction(ctx, claim); err != nil {
		t.Fatal(err)
	}
	snapshot, err := store.LoadSnapshot(ctx)
	if err != nil || len(snapshot.Entries) != 0 || len(snapshot.Blobs) != 0 {
		t.Fatalf("snapshot after entry eviction = %#v, %v", snapshot, err)
	}
}

func TestPrepareHandoffCannotReachBlobAfterEvictionClaim(t *testing.T) {
	ctx := context.Background()
	store := newStore(t, ctx, newVersion2Database(t, ctx))
	blob, entry, materialization, reference, lease := validGraph()
	reference.OwnerKind = cache.ReferenceOwnerEdit
	reference.OwnerID = lease.OwnerID
	reference.Target = cache.MaterializationTarget(materialization.ID)
	must(t, store.Publish(ctx, blob, entry))
	shared := entry
	shared.ID = cache.EntryID(strings.Repeat("9", 64))
	shared.CanonicalPath = []byte("/shared-raw-path")
	shared.Fingerprint = cache.Fingerprint{Strength: cache.FingerprintStrong, Canonical: []byte("shared-strong-fingerprint")}
	must(t, store.Publish(ctx, blob, shared))
	if _, err := store.BeginEviction(ctx, cache.EntryEviction(entry.ID), time.Unix(20, 0).UTC()); err != nil {
		t.Fatal(err)
	}
	if err := store.PrepareHandoff(ctx, materialization, reference, lease); !errors.Is(err, ErrEvictionProtected) {
		t.Fatalf("PrepareHandoff after claim = %v", err)
	}
	snapshot, err := store.LoadSnapshot(ctx)
	if err != nil || len(snapshot.Materializations) != 0 || len(snapshot.Leases) != 0 || len(snapshot.References) != 1 || snapshot.References[0].OwnerID != evictionEntryOwnerPrefix+string(entry.ID) {
		t.Fatalf("failed handoff snapshot = %#v, %v", snapshot, err)
	}
}

func TestConcurrentDuplicateReferenceLeavesOneRow(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	database := newVersion2Database(t, ctx)
	store := newStore(t, ctx, database)
	blob, entry, _, reference, _ := validGraph()
	must(t, store.Publish(ctx, blob, entry))
	var wg sync.WaitGroup
	errs := make(chan error, 2)
	for range 2 {
		wg.Add(1)
		go func() { defer wg.Done(); errs <- store.AddReference(ctx, reference) }()
	}
	wg.Wait()
	close(errs)
	successes := 0
	for err := range errs {
		if err == nil {
			successes++
		}
	}
	if successes != 1 {
		t.Fatalf("successful duplicate inserts = %d, want 1", successes)
	}
	refs, err := store.ListReferences(ctx)
	if err != nil || len(refs) != 1 {
		t.Fatalf("references = %#v, %v", refs, err)
	}
}

func TestNewRejectsVersion1AndChangedContract(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	v1 := newDatabaseAtHead(t, ctx, 1)
	if _, err := New(ctx, v1); err == nil {
		t.Fatal("New accepted Version 1")
	}
	v2 := newVersion2Database(t, ctx)
	if _, err := v2.ExecContext(ctx, "CREATE TABLE injected(value TEXT) STRICT"); err != nil {
		t.Fatal(err)
	}
	if _, err := New(ctx, v2); err == nil {
		t.Fatal("New accepted changed schema contract")
	}
}

func TestNewAcceptsVersion3WithoutChangingVersion2CatalogBehavior(t *testing.T) {
	ctx := context.Background()
	database := newDatabaseAtHead(t, ctx, 3)
	store, err := New(ctx, database)
	if err != nil {
		t.Fatalf("New(Version 3): %v", err)
	}
	blob, entry, _, _, _ := validGraph()
	if err := store.Publish(ctx, blob, entry); err != nil {
		t.Fatalf("publish through Version 3 cache store: %v", err)
	}
}

func TestLoadRejectsUnsupportedRecoveryEnumsAndDoesNotLeakPayloads(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	database := newVersion2Database(t, ctx)
	store := newStore(t, ctx, database)
	blob, entry, _, _, _ := validGraph()
	must(t, store.Publish(ctx, blob, entry))
	_, err := database.ExecContext(ctx, "INSERT INTO cache_materializations(materialization_id,entry_id,baseline_blob_sha256,basename,size_bytes,current_sha256,state,pinned,created_at_unix,updated_at_unix,last_access_unix) VALUES(?,?,?,?,?,NULL,'preparing',0,?,?,?)", strings.Repeat("2", 32), entry.ID, blob.ID, "materializations/"+strings.Repeat("2", 32)+"/content", 0, 10, 10, 10)
	if err != nil {
		t.Fatal(err)
	}
	_, err = store.LoadSnapshot(ctx)
	if err == nil || !strings.Contains(err.Error(), "unsupported") {
		t.Fatalf("LoadSnapshot error = %v", err)
	}
	for _, secret := range []string{"password", "credential", "preview body", "command output"} {
		if strings.Contains(strings.ToLower(err.Error()), secret) {
			t.Fatalf("error leaked %q: %v", secret, err)
		}
	}
}

func TestLoadRejectsUnsupportedUncertainLease(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	database := newVersion2Database(t, ctx)
	store := newStore(t, ctx, database)
	blob, entry, _, _, lease := validGraph()
	must(t, store.Publish(ctx, blob, entry))
	_, err := database.ExecContext(ctx, "INSERT INTO cache_leases(lease_id,blob_sha256,materialization_id,owner_kind,owner_id,daemon_instance_id,owner_pid,process_birth_identity,heartbeat_at_unix,expires_at_unix,grace_until_unix,state) VALUES(?,?,NULL,'preview',?,?,NULL,NULL,?,?,?,'uncertain')", lease.ID, blob.ID, lease.OwnerID, lease.DaemonInstanceID, 10, 20, 30)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.LoadSnapshot(ctx); err == nil || !strings.Contains(err.Error(), "unsupported recovery state \"uncertain\"") {
		t.Fatalf("LoadSnapshot error = %v", err)
	}
}

func TestFingerprintPersistenceIsTypedAndDoesNotStorePlaintextPayload(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	database := newVersion2Database(t, ctx)
	store := newStore(t, ctx, database)
	blob, entry, _, _, _ := validGraph()
	entry.Fingerprint.Canonical = []byte("credential-shaped-sentinel")
	must(t, store.Publish(ctx, blob, entry))
	var algorithm, encoded string
	var size, modified sql.NullInt64
	var precision, fileID, versionID sql.NullString
	if err := database.QueryRowContext(ctx, "SELECT hash_algorithm,hash_hex,fingerprint_size,modified_unix_nano,modified_precision,file_id,version_id FROM cache_entries WHERE entry_id=?", entry.ID).Scan(&algorithm, &encoded, &size, &modified, &precision, &fileID, &versionID); err != nil {
		t.Fatal(err)
	}
	if algorithm != fingerprintCodec || encoded == string(entry.Fingerprint.Canonical) || size.Valid || modified.Valid || precision.Valid || fileID.Valid || versionID.Valid {
		t.Fatalf("fingerprint columns = algorithm %q encoded %q nullable %#v %#v %#v %#v %#v", algorithm, encoded, size, modified, precision, fileID, versionID)
	}
	var jsonColumns int
	if err := database.QueryRowContext(ctx, "SELECT count(*) FROM pragma_table_info('cache_entries') WHERE lower(name) LIKE '%json%'").Scan(&jsonColumns); err != nil || jsonColumns != 0 {
		t.Fatalf("cache entry JSON columns = %d, %v", jsonColumns, err)
	}
}

func TestCanceledContextDoesNotPublishPartialCatalog(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	database := newVersion2Database(t, ctx)
	store := newStore(t, ctx, database)
	canceled, cancel := context.WithCancel(ctx)
	cancel()
	blob, entry, _, _, _ := validGraph()
	if err := store.Publish(canceled, blob, entry); err == nil {
		t.Fatal("Publish succeeded with canceled context")
	}
	var count int
	must(t, database.QueryRowContext(ctx, "SELECT count(*) FROM cache_blobs").Scan(&count))
	if count != 0 {
		t.Fatalf("blob count after canceled publish = %d", count)
	}
}

func validGraph() (cache.Blob, cache.Entry, cache.Materialization, cache.Reference, cache.Lease) {
	now := time.Unix(10, 0).UTC()
	blobID := cache.BlobID(strings.Repeat("a", 64))
	entryID := cache.EntryID(strings.Repeat("b", 64))
	materializationID := cache.MaterializationID(strings.Repeat("c", 32))
	blob := cache.Blob{ID: blobID, Size: 7, State: cache.BlobPublished, CreatedAt: now, LastAccessAt: now}
	entry := cache.Entry{ID: entryID, EndpointID: "endpoint", CanonicalPath: []byte("/raw/path"), Fingerprint: cache.Fingerprint{Strength: cache.FingerprintStrong, Canonical: []byte("strong-fingerprint")}, Freshness: cache.EntryFresh, Policy: cache.PolicyLRU, WorkspaceID: "workspace", BlobID: blobID, CreatedAt: now, LastAccessAt: now}
	materialization := cache.Materialization{ID: materializationID, EntryID: entryID, BaselineBlobID: blobID, CurrentBlobID: blobID, Size: 7, State: cache.MaterializationClean, CreatedAt: now, LastAccessAt: now}
	reference := cache.Reference{ID: cache.ReferenceID(strings.Repeat("d", 32)), OwnerKind: cache.ReferenceOwnerPreview, OwnerID: "preview-owner", Target: cache.BlobTarget(blobID), CreatedAt: now}
	lease := cache.Lease{ID: cache.LeaseID(strings.Repeat("e", 32)), OwnerKind: cache.LeaseOwnerEditor, OwnerID: "edit-owner", DaemonInstanceID: strings.Repeat("f", 32), Target: cache.MaterializationTarget(materializationID), State: cache.LeaseActive, HeartbeatAt: now, ExpiresAt: now.Add(time.Minute), GraceUntil: now.Add(2 * time.Minute), Process: &cache.ProcessIdentity{PID: 123, BirthID: "birth"}}
	return blob, entry, materialization, reference, lease
}

func newVersion2Database(t *testing.T, ctx context.Context) *sql.DB {
	return newDatabaseAtHead(t, ctx, 2)
}
func newDatabaseAtHead(t *testing.T, ctx context.Context, head int) *sql.DB {
	t.Helper()
	root := testkit.PersistentTempDir(t)
	path := filepath.Join(root, "state.db")
	dsn := "file:" + path + "?_pragma=foreign_keys(1)&_pragma=journal_mode(WAL)&_pragma=synchronous(FULL)&_pragma=busy_timeout(5000)"
	database, err := sql.Open("sqlite", dsn)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = database.Close() })
	database.SetMaxOpenConns(8)
	if err := os.Chmod(filepath.Dir(path), 0o700); err != nil { //nolint:gosec // directory requires owner traversal
		t.Fatal(err)
	}
	migrations := []migration.Migration{migration.Version1(), migration.Version2(), migration.Version3()}
	for index := 0; index < head; index++ {
		m := migrations[index]
		for _, statement := range m.Statements {
			if _, err := database.ExecContext(ctx, statement); err != nil {
				t.Fatalf("apply v%d: %v", index+1, err)
			}
		}
		sum, _ := migration.Checksum(m)
		if _, err := database.ExecContext(ctx, "INSERT INTO schema_migrations(version,name,sha256,applied_at) VALUES(?,?,?,?)", index+1, m.Name, hex.EncodeToString(sum[:]), "2026-07-16T00:00:00Z"); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.Chmod(path, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(path+"-wal", 0o600); err != nil {
		t.Fatal(err)
	}
	return database
}

func must(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
}

func newStore(t *testing.T, ctx context.Context, database *sql.DB) *Store {
	t.Helper()
	store, err := New(ctx, database)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return store
}
