package cachemanager

import (
	"bytes"
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/TyrantLucifer/awesome-mac-sftp/internal/cache"
	"github.com/TyrantLucifer/awesome-mac-sftp/internal/cachefs"
	"github.com/TyrantLucifer/awesome-mac-sftp/internal/cacheprocess"
	"github.com/TyrantLucifer/awesome-mac-sftp/internal/domain"
	"github.com/TyrantLucifer/awesome-mac-sftp/internal/state/cachestore"
	"github.com/TyrantLucifer/awesome-mac-sftp/internal/state/migration"
	_ "github.com/TyrantLucifer/awesome-mac-sftp/internal/state/sqlite"
	"github.com/TyrantLucifer/awesome-mac-sftp/internal/testkit"
)

func TestPublishCompleteBindsLocationFingerprintAndDeduplicatedContent(t *testing.T) {
	manager, _ := newManager(t)
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

func TestPublishCompleteIsIdempotentForTheExactLocationFingerprint(t *testing.T) {
	manager, _ := newManager(t)
	ctx := context.Background()
	content := []byte("unchanged content")
	request := PublishRequest{
		Location: testLocation(t, "/same"), SourceFingerprint: testSourceFingerprint(uint64(len(content))),
		WorkspaceID: "workspace", Policy: cache.PolicyLRU, Source: bytes.NewReader(content),
		MaxBytes: int64(len(content)), ExpectedSize: sizePointer(int64(len(content))),
	}
	first, err := manager.PublishComplete(ctx, request)
	if err != nil {
		t.Fatal(err)
	}
	request.Source = bytes.NewReader(content)
	second, err := manager.PublishComplete(ctx, request)
	if err != nil {
		t.Fatalf("repeat exact publication: %v", err)
	}
	if second.Entry.ID != first.Entry.ID || second.Blob.ID != first.Blob.ID || !second.Deduplicated {
		t.Fatalf("repeat publication = %#v, first = %#v", second, first)
	}
	snapshot, err := manager.catalog.LoadSnapshot(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(snapshot.Entries) != 1 || len(snapshot.Blobs) != 1 {
		t.Fatalf("repeat publication duplicated catalog state: %#v", snapshot)
	}
}

func TestPublishCompleteRejectsShortOrWeakSourceWithoutCatalogRows(t *testing.T) {
	manager, files := newManager(t)
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
	reconcile, err := manager.Reconcile(context.Background(), 100)
	if err != nil {
		t.Fatal(err)
	}
	if reconcile.NeedsAttention || len(reconcile.Filesystem.VerifiedBlobs) != 0 || len(reconcile.Filesystem.Orphans) != 0 {
		t.Fatalf("failed short publication left filesystem residue: %#v", reconcile)
	}
	if _, err := files.Scan(100); err != nil {
		t.Fatalf("scan cache after rejected publication: %v", err)
	}
}

func TestPublishCompleteAdmitsByEvictingCleanLRUBeforeCatalogCommit(t *testing.T) {
	manager, files := newManager(t)
	ctx := context.Background()
	oldContent := []byte("old")
	old, err := manager.PublishComplete(ctx, PublishRequest{
		Location: testLocation(t, "/old"), SourceFingerprint: testSourceFingerprint(uint64(len(oldContent))), WorkspaceID: "workspace",
		Policy: cache.PolicyLRU, Source: bytes.NewReader(oldContent), MaxBytes: int64(len(oldContent)), ExpectedSize: sizePointer(int64(len(oldContent))),
	})
	if err != nil {
		t.Fatal(err)
	}
	manager.limits = cache.Limits{GlobalBytes: 3, GlobalEntries: 1, WorkspaceBytes: 3, MaxCandidates: 8}
	newContent := []byte("new")
	newResult, err := manager.PublishComplete(ctx, PublishRequest{
		Location: testLocation(t, "/new"), SourceFingerprint: testSourceFingerprint(uint64(len(newContent))), WorkspaceID: "workspace",
		Policy: cache.PolicyLRU, Source: bytes.NewReader(newContent), MaxBytes: int64(len(newContent)), ExpectedSize: sizePointer(int64(len(newContent))),
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := files.InspectBlob(old.Blob.ID); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("old LRU blob survived admission: %v", err)
	}
	snapshot, err := manager.catalog.LoadSnapshot(ctx)
	if err != nil {
		t.Fatal(err)
	}
	usage, err := cache.Account(snapshot)
	if err != nil || usage.Global != (cache.Quantity{Bytes: 3, Entries: 1}) || len(snapshot.Entries) != 1 || snapshot.Entries[0].ID != newResult.Entry.ID {
		t.Fatalf("admitted snapshot = %#v usage=%#v err=%v", snapshot, usage, err)
	}
}

func TestPublishCompleteRejectsWhenOnlyProtectedContentCouldMakeRoom(t *testing.T) {
	manager, files := newManager(t)
	ctx := context.Background()
	protectedContent := []byte("keep")
	protected, err := manager.PublishComplete(ctx, PublishRequest{
		Location: testLocation(t, "/protected-admission"), SourceFingerprint: testSourceFingerprint(uint64(len(protectedContent))), WorkspaceID: "workspace",
		Policy: cache.PolicyLRU, Pinned: true, Source: bytes.NewReader(protectedContent), MaxBytes: int64(len(protectedContent)), ExpectedSize: sizePointer(int64(len(protectedContent))),
	})
	if err != nil {
		t.Fatal(err)
	}
	manager.limits = cache.Limits{GlobalBytes: 4, GlobalEntries: 1, WorkspaceBytes: 4, MaxCandidates: 8}
	incoming := []byte("deny")
	_, err = manager.PublishComplete(ctx, PublishRequest{
		Location: testLocation(t, "/denied-admission"), SourceFingerprint: testSourceFingerprint(uint64(len(incoming))), WorkspaceID: "workspace",
		Policy: cache.PolicyLRU, Source: bytes.NewReader(incoming), MaxBytes: int64(len(incoming)), ExpectedSize: sizePointer(int64(len(incoming))),
	})
	if !errors.Is(err, ErrQuotaUnsatisfied) {
		t.Fatalf("protected admission error = %v, want ErrQuotaUnsatisfied", err)
	}
	if _, err := files.InspectBlob(protected.Blob.ID); err != nil {
		t.Fatalf("protected blob changed: %v", err)
	}
	snapshot, loadErr := manager.catalog.LoadSnapshot(ctx)
	if loadErr != nil || len(snapshot.Blobs) != 1 || len(snapshot.Entries) != 1 || snapshot.Entries[0].ID != protected.Entry.ID {
		t.Fatalf("protected admission snapshot = %#v, %v", snapshot, loadErr)
	}
	reconcile, reconcileErr := manager.Reconcile(ctx, 100)
	if reconcileErr != nil || reconcile.NeedsAttention {
		t.Fatalf("reconcile after denied admission = %#v, %v", reconcile, reconcileErr)
	}
}

func TestPublishCompleteEnforcesWorkspaceShareAtAdmission(t *testing.T) {
	manager, _ := newManager(t)
	ctx := context.Background()
	firstContent := []byte("123456")
	first, err := manager.PublishComplete(ctx, PublishRequest{
		Location: testLocation(t, "/workspace-first"), SourceFingerprint: testSourceFingerprint(uint64(len(firstContent))), WorkspaceID: "workspace-a",
		Policy: cache.PolicyLRU, Pinned: true, Source: bytes.NewReader(firstContent), MaxBytes: int64(len(firstContent)), ExpectedSize: sizePointer(int64(len(firstContent))),
	})
	if err != nil {
		t.Fatal(err)
	}
	manager.limits = cache.Limits{GlobalBytes: 20, GlobalEntries: 4, WorkspaceBytes: 10, MaxCandidates: 8}
	secondContent := []byte("abcde")
	_, err = manager.PublishComplete(ctx, PublishRequest{
		Location: testLocation(t, "/workspace-second"), SourceFingerprint: testSourceFingerprint(uint64(len(secondContent))), WorkspaceID: "workspace-a",
		Policy: cache.PolicyLRU, Source: bytes.NewReader(secondContent), MaxBytes: int64(len(secondContent)), ExpectedSize: sizePointer(int64(len(secondContent))),
	})
	if !errors.Is(err, ErrQuotaUnsatisfied) {
		t.Fatalf("workspace admission error = %v", err)
	}
	snapshot, loadErr := manager.catalog.LoadSnapshot(ctx)
	if loadErr != nil || len(snapshot.Entries) != 1 || snapshot.Entries[0].ID != first.Entry.ID {
		t.Fatalf("workspace admission snapshot = %#v, %v", snapshot, loadErr)
	}
}

func TestPublishCompleteCrossWorkspaceDedupMovesBlobToSharedAccounting(t *testing.T) {
	manager, _ := newManager(t)
	ctx := context.Background()
	content := []byte("shared")
	first, err := manager.PublishComplete(ctx, PublishRequest{
		Location: testLocation(t, "/shared-workspace-a"), SourceFingerprint: testSourceFingerprint(uint64(len(content))), WorkspaceID: "workspace-a",
		Policy: cache.PolicyLRU, Source: bytes.NewReader(content), MaxBytes: int64(len(content)), ExpectedSize: sizePointer(int64(len(content))),
	})
	if err != nil {
		t.Fatal(err)
	}
	manager.limits = cache.Limits{GlobalBytes: int64(len(content)), GlobalEntries: 2, WorkspaceBytes: 1, MaxCandidates: 8}
	second, err := manager.PublishComplete(ctx, PublishRequest{
		Location: testLocation(t, "/shared-workspace-b"), SourceFingerprint: testSourceFingerprint(uint64(len(content))), WorkspaceID: "workspace-b",
		Policy: cache.PolicyLRU, Source: bytes.NewReader(content), MaxBytes: int64(len(content)), ExpectedSize: sizePointer(int64(len(content))),
	})
	if err != nil {
		t.Fatal(err)
	}
	if first.Blob.ID != second.Blob.ID || !second.Deduplicated {
		t.Fatalf("cross-workspace publications = %#v %#v", first, second)
	}
	snapshot, err := manager.catalog.LoadSnapshot(ctx)
	if err != nil {
		t.Fatal(err)
	}
	usage, err := cache.Account(snapshot)
	if err != nil || usage.SharedBytes != int64(len(content)) || usage.Workspaces["workspace-a"] != 0 || usage.Workspaces["workspace-b"] != 0 {
		t.Fatalf("cross-workspace usage = %#v, %v", usage, err)
	}
}

func TestPublishCompleteDedupAdmissionNeverDeletesTheIncomingBlob(t *testing.T) {
	manager, files := newManager(t)
	ctx := context.Background()
	content := []byte("same")
	first, err := manager.PublishComplete(ctx, PublishRequest{
		Location: testLocation(t, "/dedup-first"), SourceFingerprint: testSourceFingerprint(uint64(len(content))), WorkspaceID: "workspace",
		Policy: cache.PolicyLRU, Source: bytes.NewReader(content), MaxBytes: int64(len(content)), ExpectedSize: sizePointer(int64(len(content))),
	})
	if err != nil {
		t.Fatal(err)
	}
	manager.limits = cache.Limits{GlobalBytes: int64(len(content)), GlobalEntries: 1, WorkspaceBytes: int64(len(content)), MaxCandidates: 8}
	_, err = manager.PublishComplete(ctx, PublishRequest{
		Location: testLocation(t, "/dedup-second"), SourceFingerprint: testSourceFingerprint(uint64(len(content))), WorkspaceID: "workspace",
		Policy: cache.PolicyLRU, Source: bytes.NewReader(content), MaxBytes: int64(len(content)), ExpectedSize: sizePointer(int64(len(content))),
	})
	if !errors.Is(err, ErrQuotaUnsatisfied) {
		t.Fatalf("deduplicated admission error = %v", err)
	}
	if _, err := files.InspectBlob(first.Blob.ID); err != nil {
		t.Fatalf("deduplicated incoming blob was deleted: %v", err)
	}
	snapshot, loadErr := manager.catalog.LoadSnapshot(ctx)
	if loadErr != nil || len(snapshot.Blobs) != 1 || len(snapshot.Entries) != 1 || snapshot.Entries[0].ID != first.Entry.ID {
		t.Fatalf("deduplicated admission snapshot = %#v, %v", snapshot, loadErr)
	}
}

func TestConcurrentPublishAdmissionsCannotSpendTheSameCapacity(t *testing.T) {
	manager, _ := newManager(t)
	manager.limits = cache.Limits{GlobalBytes: 1, GlobalEntries: 1, WorkspaceBytes: 1, MaxCandidates: 8}
	ctx := context.Background()
	requests := []PublishRequest{
		{Location: testLocation(t, "/concurrent-a"), SourceFingerprint: testSourceFingerprint(1), WorkspaceID: "workspace", Policy: cache.PolicyLRU, Source: bytes.NewReader([]byte("a")), MaxBytes: 1, ExpectedSize: sizePointer(1)},
		{Location: testLocation(t, "/concurrent-b"), SourceFingerprint: testSourceFingerprint(1), WorkspaceID: "workspace", Policy: cache.PolicyLRU, Source: bytes.NewReader([]byte("b")), MaxBytes: 1, ExpectedSize: sizePointer(1)},
	}
	start := make(chan struct{})
	errorsByRequest := make(chan error, len(requests))
	for _, request := range requests {
		request := request
		go func() {
			<-start
			_, err := manager.PublishComplete(ctx, request)
			errorsByRequest <- err
		}()
	}
	close(start)
	for range requests {
		if err := <-errorsByRequest; err != nil {
			t.Fatal(err)
		}
	}
	snapshot, err := manager.catalog.LoadSnapshot(ctx)
	if err != nil {
		t.Fatal(err)
	}
	usage, err := cache.Account(snapshot)
	if err != nil || usage.Global.Bytes > 1 || usage.Global.Entries+usage.Materializations > 1 || len(snapshot.Entries) != 1 {
		t.Fatalf("concurrent admission exceeded capacity: snapshot=%#v usage=%#v err=%v", snapshot, usage, err)
	}
}

func TestPrepareHandoffBoundsRepeatedZeroByteMaterializationsAndLeases(t *testing.T) {
	manager, _ := newManager(t)
	ctx := context.Background()
	published, err := manager.PublishComplete(ctx, PublishRequest{
		Location: testLocation(t, "/empty"), SourceFingerprint: testSourceFingerprint(0), WorkspaceID: "workspace",
		Policy: cache.PolicyLRU, Source: bytes.NewReader(nil), MaxBytes: 0, ExpectedSize: sizePointer(0),
	})
	if err != nil {
		t.Fatal(err)
	}
	manager.limits = cache.Limits{GlobalBytes: 0, GlobalEntries: 2, WorkspaceBytes: 0, MaxCandidates: 8}
	first, err := manager.PrepareHandoff(ctx, HandoffRequest{
		EntryID: published.Entry.ID, MaterializationID: cache.MaterializationID(strings.Repeat("1", 32)),
		ReferenceID: cache.ReferenceID(strings.Repeat("2", 32)), LeaseID: cache.LeaseID(strings.Repeat("3", 32)),
		OwnerKind: cache.LeaseOwnerEditor, OwnerID: "first",
	})
	if err != nil {
		t.Fatal(err)
	}
	_, err = manager.PrepareHandoff(ctx, HandoffRequest{
		EntryID: published.Entry.ID, MaterializationID: cache.MaterializationID(strings.Repeat("4", 32)),
		ReferenceID: cache.ReferenceID(strings.Repeat("5", 32)), LeaseID: cache.LeaseID(strings.Repeat("6", 32)),
		OwnerKind: cache.LeaseOwnerEditor, OwnerID: "second",
	})
	if !errors.Is(err, ErrQuotaUnsatisfied) {
		t.Fatalf("second zero-byte handoff error = %v", err)
	}
	snapshot, loadErr := manager.catalog.LoadSnapshot(ctx)
	if loadErr != nil || len(snapshot.Materializations) != 1 || snapshot.Materializations[0].ID != first.Materialization.ID || len(snapshot.References) != 1 || len(snapshot.Leases) != 1 {
		t.Fatalf("bounded zero-byte snapshot = %#v, %v", snapshot, loadErr)
	}
}

func TestPrepareHandoffPublishesMaterializationBeforeAtomicReachabilityAndLease(t *testing.T) {
	manager, _ := newManager(t)
	ctx := context.Background()
	content := []byte("editable")
	published, err := manager.PublishComplete(ctx, PublishRequest{
		Location: testLocation(t, "/edit"), SourceFingerprint: testSourceFingerprint(uint64(len(content))), WorkspaceID: "workspace",
		Policy: cache.PolicyLRU, Source: bytes.NewReader(content), MaxBytes: int64(len(content)), ExpectedSize: sizePointer(int64(len(content))),
	})
	if err != nil {
		t.Fatal(err)
	}
	identity, err := cacheprocess.CurrentIdentity()
	if err != nil {
		t.Fatal(err)
	}
	result, err := manager.PrepareHandoff(ctx, HandoffRequest{
		EntryID: published.Entry.ID, MaterializationID: cache.MaterializationID(strings.Repeat("a", 32)),
		ReferenceID: cache.ReferenceID(strings.Repeat("b", 32)), LeaseID: cache.LeaseID(strings.Repeat("c", 32)),
		OwnerKind: cache.LeaseOwnerEditor, OwnerID: "edit-session", Process: &identity,
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

func TestPrepareHandoffRejectsUnclassifiableSuppliedProcessIdentity(t *testing.T) {
	manager, _ := newManager(t)
	ctx := context.Background()
	content := []byte("identity")
	published, err := manager.PublishComplete(ctx, PublishRequest{
		Location: testLocation(t, "/identity"), SourceFingerprint: testSourceFingerprint(uint64(len(content))), WorkspaceID: "workspace",
		Policy: cache.PolicyLRU, Source: bytes.NewReader(content), MaxBytes: int64(len(content)), ExpectedSize: sizePointer(int64(len(content))),
	})
	if err != nil {
		t.Fatal(err)
	}
	identity := cache.ProcessIdentity{PID: os.Getpid(), BirthID: "not-a-native-birth-identity"}
	_, err = manager.PrepareHandoff(ctx, HandoffRequest{
		EntryID: published.Entry.ID, MaterializationID: cache.MaterializationID(strings.Repeat("1", 32)),
		ReferenceID: cache.ReferenceID(strings.Repeat("2", 32)), LeaseID: cache.LeaseID(strings.Repeat("3", 32)),
		OwnerKind: cache.LeaseOwnerEditor, OwnerID: "edit-session", Process: &identity,
	})
	if err == nil {
		t.Fatal("PrepareHandoff accepted an unclassifiable process identity")
	}
	snapshot, loadErr := manager.catalog.LoadSnapshot(ctx)
	if loadErr != nil || len(snapshot.Materializations)+len(snapshot.References)+len(snapshot.Leases) != 0 {
		t.Fatalf("rejected identity published handoff rows: %#v, %v", snapshot, loadErr)
	}
}

func TestReleaseHandoffIsAtomicIdempotentAndNeverDeletesDirtyMaterialization(t *testing.T) {
	manager, _ := newManager(t)
	ctx := context.Background()
	content := []byte("editable")
	published, err := manager.PublishComplete(ctx, PublishRequest{
		Location: testLocation(t, "/release"), SourceFingerprint: testSourceFingerprint(uint64(len(content))), WorkspaceID: "workspace",
		Policy: cache.PolicyLRU, Source: bytes.NewReader(content), MaxBytes: int64(len(content)), ExpectedSize: sizePointer(int64(len(content))),
	})
	if err != nil {
		t.Fatal(err)
	}
	handoff, err := manager.PrepareHandoff(ctx, HandoffRequest{
		EntryID: published.Entry.ID, MaterializationID: cache.MaterializationID(strings.Repeat("a", 32)),
		ReferenceID: cache.ReferenceID(strings.Repeat("b", 32)), LeaseID: cache.LeaseID(strings.Repeat("c", 32)),
		OwnerKind: cache.LeaseOwnerEditor, OwnerID: "edit-session",
	})
	if err != nil {
		t.Fatal(err)
	}
	dirty := handoff.Materialization
	dirty.State = cache.MaterializationDirty
	dirty.CurrentBlobID = cache.BlobID(strings.Repeat("e", 64))
	dirty.Size++
	if err := manager.catalog.UpdateMaterialization(ctx, dirty); err != nil {
		t.Fatal(err)
	}
	request := ReleaseHandoffRequest{
		MaterializationID: dirty.ID, ReferenceID: handoff.Reference.ID, LeaseID: handoff.Lease.ID,
		OwnerKind: cache.LeaseOwnerEditor, OwnerID: "edit-session",
	}
	if err := manager.ReleaseHandoff(ctx, request); err != nil {
		t.Fatalf("release handoff: %v", err)
	}
	if err := manager.ReleaseHandoff(ctx, request); err != nil {
		t.Fatalf("repeat release handoff: %v", err)
	}
	snapshot, err := manager.catalog.LoadSnapshot(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(snapshot.Materializations) != 1 || snapshot.Materializations[0].State != cache.MaterializationDirty || len(snapshot.References) != 0 || len(snapshot.Leases) != 1 || snapshot.Leases[0].State != cache.LeaseReleased {
		t.Fatalf("snapshot after release = %#v", snapshot)
	}
}

func TestReleaseHandoffRollsBackWhenReferenceIdentityDoesNotMatch(t *testing.T) {
	manager, _ := newManager(t)
	ctx := context.Background()
	content := []byte("editable")
	published, err := manager.PublishComplete(ctx, PublishRequest{
		Location: testLocation(t, "/rollback"), SourceFingerprint: testSourceFingerprint(uint64(len(content))), WorkspaceID: "workspace",
		Policy: cache.PolicyLRU, Source: bytes.NewReader(content), MaxBytes: int64(len(content)), ExpectedSize: sizePointer(int64(len(content))),
	})
	if err != nil {
		t.Fatal(err)
	}
	handoff, err := manager.PrepareHandoff(ctx, HandoffRequest{
		EntryID: published.Entry.ID, MaterializationID: cache.MaterializationID(strings.Repeat("a", 32)),
		ReferenceID: cache.ReferenceID(strings.Repeat("b", 32)), LeaseID: cache.LeaseID(strings.Repeat("c", 32)),
		OwnerKind: cache.LeaseOwnerEditor, OwnerID: "edit-session",
	})
	if err != nil {
		t.Fatal(err)
	}
	err = manager.ReleaseHandoff(ctx, ReleaseHandoffRequest{
		MaterializationID: handoff.Materialization.ID, ReferenceID: cache.ReferenceID(strings.Repeat("f", 32)), LeaseID: handoff.Lease.ID,
		OwnerKind: cache.LeaseOwnerEditor, OwnerID: "edit-session",
	})
	if err == nil {
		t.Fatal("release with wrong reference ID succeeded")
	}
	snapshot, loadErr := manager.catalog.LoadSnapshot(ctx)
	if loadErr != nil || len(snapshot.References) != 1 || snapshot.Leases[0].State != cache.LeaseActive {
		t.Fatalf("snapshot after rejected release = (%#v, %v)", snapshot, loadErr)
	}
}

func TestMarkDirtyHashesStableMaterializationAndRequiresExactOwner(t *testing.T) {
	manager, _ := newManager(t)
	ctx := context.Background()
	content := []byte("editable")
	published, err := manager.PublishComplete(ctx, PublishRequest{
		Location: testLocation(t, "/dirty"), SourceFingerprint: testSourceFingerprint(uint64(len(content))), WorkspaceID: "workspace",
		Policy: cache.PolicyLRU, Source: bytes.NewReader(content), MaxBytes: int64(len(content)), ExpectedSize: sizePointer(int64(len(content))),
	})
	if err != nil {
		t.Fatal(err)
	}
	handoff, err := manager.PrepareHandoff(ctx, HandoffRequest{
		EntryID: published.Entry.ID, MaterializationID: cache.MaterializationID(strings.Repeat("a", 32)),
		ReferenceID: cache.ReferenceID(strings.Repeat("b", 32)), LeaseID: cache.LeaseID(strings.Repeat("c", 32)),
		OwnerKind: cache.LeaseOwnerEditor, OwnerID: "edit-session",
	})
	if err != nil {
		t.Fatal(err)
	}
	changed := []byte("locally changed and rehashed")
	if err := os.WriteFile(handoff.Path, changed, 0o600); err != nil {
		t.Fatal(err)
	}
	request := MarkDirtyRequest{
		MaterializationID: handoff.Materialization.ID, ReferenceID: handoff.Reference.ID, LeaseID: handoff.Lease.ID,
		OwnerKind: cache.LeaseOwnerEditor, OwnerID: "edit-session",
	}
	wrong := request
	wrong.OwnerID = "wrong-owner"
	if _, err := manager.MarkDirty(ctx, wrong); err == nil {
		t.Fatal("MarkDirty accepted wrong owner")
	}
	result, err := manager.MarkDirty(ctx, request)
	if err != nil {
		t.Fatal(err)
	}
	wantDigest := cache.BlobIDFromDigest(sha256.Sum256(changed))
	if result.Materialization.State != cache.MaterializationDirty || result.Materialization.CurrentBlobID != wantDigest || result.Materialization.Size != int64(len(changed)) || result.Path != handoff.Path {
		t.Fatalf("dirty result = %#v, want digest %s", result, wantDigest)
	}
}

func TestEnforceQuotaDeletesExactClaimedEntryAndBlob(t *testing.T) {
	manager, files := newManager(t)
	ctx := context.Background()
	content := []byte("quota bytes")
	published, err := manager.PublishComplete(ctx, PublishRequest{
		Location: testLocation(t, "/quota"), SourceFingerprint: testSourceFingerprint(uint64(len(content))), WorkspaceID: "workspace",
		Policy: cache.PolicyLRU, Source: bytes.NewReader(content), MaxBytes: int64(len(content)), ExpectedSize: sizePointer(int64(len(content))),
	})
	if err != nil {
		t.Fatal(err)
	}
	manager.limits = cache.Limits{GlobalBytes: 0, GlobalEntries: 0, WorkspaceBytes: 0, MaxCandidates: 8}
	result, err := manager.EnforceQuota(ctx, 4)
	if err != nil {
		t.Fatal(err)
	}
	if !result.Plan.Satisfied || len(result.Deleted) != 1 || result.Deleted[0].EntryID != published.Entry.ID {
		t.Fatalf("quota result = %#v", result)
	}
	if _, err := files.InspectBlob(published.Blob.ID); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("blob still inspectable: %v", err)
	}
	snapshot, err := manager.catalog.LoadSnapshot(ctx)
	if err != nil || len(snapshot.Blobs)+len(snapshot.Entries) != 0 {
		t.Fatalf("snapshot after quota = %#v, %v", snapshot, err)
	}
}

func TestRecoverPendingEvictionsResumesAfterCatalogClaim(t *testing.T) {
	manager, files := newManager(t)
	ctx := context.Background()
	content := []byte("resume bytes")
	published, err := manager.PublishComplete(ctx, PublishRequest{
		Location: testLocation(t, "/resume"), SourceFingerprint: testSourceFingerprint(uint64(len(content))), WorkspaceID: "workspace",
		Policy: cache.PolicyLRU, Source: bytes.NewReader(content), MaxBytes: int64(len(content)), ExpectedSize: sizePointer(int64(len(content))),
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := manager.catalog.BeginEviction(ctx, cache.EntryEviction(published.Entry.ID), manager.now()); err != nil {
		t.Fatal(err)
	}
	deleted, err := manager.RecoverPendingEvictions(ctx, 8)
	if err != nil || len(deleted) != 1 || deleted[0].EntryID != published.Entry.ID {
		t.Fatalf("RecoverPendingEvictions = %#v, %v", deleted, err)
	}
	if _, err := files.InspectBlob(published.Blob.ID); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("pending blob still inspectable: %v", err)
	}
}

func TestEnforceQuotaReturnsResourceExhaustionWithoutMutatingProtectedContent(t *testing.T) {
	manager, files := newManager(t)
	ctx := context.Background()
	content := []byte("protected")
	published, err := manager.PublishComplete(ctx, PublishRequest{
		Location: testLocation(t, "/protected"), SourceFingerprint: testSourceFingerprint(uint64(len(content))), WorkspaceID: "workspace",
		Policy: cache.PolicyLRU, Source: bytes.NewReader(content), MaxBytes: int64(len(content)), ExpectedSize: sizePointer(int64(len(content))),
	})
	if err != nil {
		t.Fatal(err)
	}
	reference := cache.Reference{ID: cache.ReferenceID(strings.Repeat("1", 32)), OwnerKind: cache.ReferenceOwnerPreview, OwnerID: "preview", Target: cache.BlobTarget(published.Blob.ID), CreatedAt: manager.now()}
	if err := manager.catalog.AddReference(ctx, reference); err != nil {
		t.Fatal(err)
	}
	manager.limits = cache.Limits{GlobalBytes: 0, GlobalEntries: 0, WorkspaceBytes: 0, MaxCandidates: 8}
	result, err := manager.EnforceQuota(ctx, 2)
	if !errors.Is(err, ErrQuotaUnsatisfied) || result.Plan.Satisfied || len(result.Deleted) != 0 {
		t.Fatalf("protected quota result = %#v, %v", result, err)
	}
	if _, err := files.InspectBlob(published.Blob.ID); err != nil {
		t.Fatalf("protected blob changed: %v", err)
	}
	snapshot, err := manager.catalog.LoadSnapshot(ctx)
	if err != nil || len(snapshot.Blobs) != 1 || len(snapshot.Entries) != 1 || len(snapshot.References) != 1 {
		t.Fatalf("protected snapshot = %#v, %v", snapshot, err)
	}
}

func TestEnforceQuotaEvictsOneSharedEntryWithoutDeletingDeduplicatedBlob(t *testing.T) {
	manager, files := newManager(t)
	ctx := context.Background()
	content := []byte("shared quota bytes")
	first, err := manager.PublishComplete(ctx, PublishRequest{
		Location: testLocation(t, "/shared-a"), SourceFingerprint: testSourceFingerprint(uint64(len(content))), WorkspaceID: "workspace",
		Policy: cache.PolicyLRU, Source: bytes.NewReader(content), MaxBytes: int64(len(content)), ExpectedSize: sizePointer(int64(len(content))),
	})
	if err != nil {
		t.Fatal(err)
	}
	second, err := manager.PublishComplete(ctx, PublishRequest{
		Location: testLocation(t, "/shared-b"), SourceFingerprint: testSourceFingerprint(uint64(len(content))), WorkspaceID: "workspace",
		Policy: cache.PolicyLRU, Source: bytes.NewReader(content), MaxBytes: int64(len(content)), ExpectedSize: sizePointer(int64(len(content))),
	})
	if err != nil || first.Blob.ID != second.Blob.ID {
		t.Fatalf("shared publish = %#v %#v, %v", first, second, err)
	}
	manager.limits = cache.Limits{GlobalBytes: 1 << 20, GlobalEntries: 1, WorkspaceBytes: 1 << 20, MaxCandidates: 8}
	result, err := manager.EnforceQuota(ctx, 4)
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Deleted) != 1 || result.Deleted[0].SharedEntryReferenceID == "" {
		t.Fatalf("shared quota result = %#v", result)
	}
	if _, err := files.InspectBlob(first.Blob.ID); err != nil {
		t.Fatalf("shared blob was deleted: %v", err)
	}
	snapshot, err := manager.catalog.LoadSnapshot(ctx)
	if err != nil || len(snapshot.Blobs) != 1 || len(snapshot.Entries) != 1 || len(snapshot.References) != 0 {
		t.Fatalf("shared snapshot = %#v, %v", snapshot, err)
	}
}

func TestRecoverPendingEvictionPreservesPostClaimBlobReplacement(t *testing.T) {
	manager, files := newManager(t)
	ctx := context.Background()
	content := []byte("claimed identity")
	published, err := manager.PublishComplete(ctx, PublishRequest{
		Location: testLocation(t, "/replacement"), SourceFingerprint: testSourceFingerprint(uint64(len(content))), WorkspaceID: "workspace",
		Policy: cache.PolicyLRU, Source: bytes.NewReader(content), MaxBytes: int64(len(content)), ExpectedSize: sizePointer(int64(len(content))),
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := manager.catalog.BeginEviction(ctx, cache.EntryEviction(published.Entry.ID), manager.now()); err != nil {
		t.Fatal(err)
	}
	replacement := []byte("post-claim replacement must survive")
	if err := os.WriteFile(published.Path, replacement, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := manager.RecoverPendingEvictions(ctx, 8); err == nil {
		t.Fatal("RecoverPendingEvictions deleted changed identity")
	}
	got, err := os.ReadFile(published.Path)
	if err != nil || !bytes.Equal(got, replacement) {
		t.Fatalf("replacement content = %q, %v", got, err)
	}
	if _, err := files.InspectBlob(published.Blob.ID); err == nil {
		t.Fatal("replacement unexpectedly validates as claimed blob")
	}
}

func TestRecoverPendingEvictionPreservesPostClaimMaterializationChange(t *testing.T) {
	manager, _ := newManager(t)
	ctx := context.Background()
	content := []byte("clean materialization")
	published, err := manager.PublishComplete(ctx, PublishRequest{
		Location: testLocation(t, "/materialization-replacement"), SourceFingerprint: testSourceFingerprint(uint64(len(content))), WorkspaceID: "workspace",
		Policy: cache.PolicyLRU, Source: bytes.NewReader(content), MaxBytes: int64(len(content)), ExpectedSize: sizePointer(int64(len(content))),
	})
	if err != nil {
		t.Fatal(err)
	}
	handoff, err := manager.PrepareHandoff(ctx, HandoffRequest{
		EntryID: published.Entry.ID, MaterializationID: cache.MaterializationID(strings.Repeat("6", 32)),
		ReferenceID: cache.ReferenceID(strings.Repeat("7", 32)), LeaseID: cache.LeaseID(strings.Repeat("8", 32)),
		OwnerKind: cache.LeaseOwnerEditor, OwnerID: "edit-session",
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := manager.ReleaseHandoff(ctx, ReleaseHandoffRequest{
		MaterializationID: handoff.Materialization.ID, ReferenceID: handoff.Reference.ID, LeaseID: handoff.Lease.ID,
		OwnerKind: cache.LeaseOwnerEditor, OwnerID: "edit-session",
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := manager.catalog.BeginEviction(ctx, cache.MaterializationEviction(handoff.Materialization.ID), manager.now()); err != nil {
		t.Fatal(err)
	}
	replacement := []byte("changed after materialization claim")
	if err := os.WriteFile(handoff.Path, replacement, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := manager.RecoverPendingEvictions(ctx, 8); err == nil {
		t.Fatal("RecoverPendingEvictions deleted changed materialization")
	}
	got, err := os.ReadFile(handoff.Path)
	if err != nil || !bytes.Equal(got, replacement) {
		t.Fatalf("replacement materialization = %q, %v", got, err)
	}
}

func TestReconcilePreservesAndReportsFilesystemOrphans(t *testing.T) {
	manager, files := newManager(t)
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

func newManager(t *testing.T) (*Manager, *cachefs.Store) {
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
	return manager, files
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
