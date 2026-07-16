package cache

import (
	"slices"
	"testing"
	"time"
)

func TestDefaultLimitsMatchADR0014(t *testing.T) {
	t.Parallel()

	want := Limits{
		GlobalBytes: 2 << 30, GlobalEntries: 4096,
		WorkspaceBytes: 1 << 30, MaxCandidates: 256,
	}
	if got := DefaultLimits(); got != want {
		t.Fatalf("default limits = %+v, want %+v", got, want)
	}
}

func TestAccountCountsDeduplicatedBytesEntriesAndWorkspaceShare(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 7, 16, 10, 0, 0, 0, time.UTC)
	blob := testBlob('a', 100, now)
	first := testEntry('b', blob.ID, "workspace-a", now)
	second := testEntry('c', blob.ID, "workspace-a", now)
	snapshot := Snapshot{Blobs: []Blob{blob}, Entries: []Entry{first, second}}

	usage, err := Account(snapshot)
	if err != nil {
		t.Fatalf("account same-workspace dedup: %v", err)
	}
	if usage.Global != (Quantity{Bytes: 100, Entries: 2}) {
		t.Fatalf("global usage = %+v", usage.Global)
	}
	if got := usage.Workspaces["workspace-a"]; got != 100 {
		t.Fatalf("workspace usage = %d, want 100", got)
	}

	third := testEntry('d', blob.ID, "workspace-b", now)
	snapshot.Entries = append(snapshot.Entries, third)
	usage, err = Account(snapshot)
	if err != nil {
		t.Fatalf("account cross-workspace dedup: %v", err)
	}
	if usage.Global != (Quantity{Bytes: 100, Entries: 3}) {
		t.Fatalf("shared global usage = %+v", usage.Global)
	}
	if usage.SharedBytes != 100 {
		t.Fatalf("shared bytes = %d, want 100", usage.SharedBytes)
	}
	if usage.Workspaces["workspace-a"] != 0 || usage.Workspaces["workspace-b"] != 0 {
		t.Fatalf("shared blob charged to one workspace: %+v", usage.Workspaces)
	}
}

func TestAccountIncludesMaterializationBytesInGlobalAndWorkspaceUsage(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 7, 16, 10, 0, 0, 0, time.UTC)
	blob := testBlob('a', 100, now)
	entry := testEntry('b', blob.ID, "workspace-a", now)
	materialization := testMaterialization('c', entry.ID, blob.ID, 25, MaterializationClean, now)
	usage, err := Account(Snapshot{
		Blobs: []Blob{blob}, Entries: []Entry{entry}, Materializations: []Materialization{materialization},
	})
	if err != nil {
		t.Fatalf("account materialization: %v", err)
	}
	if usage.Global != (Quantity{Bytes: 125, Entries: 1}) {
		t.Fatalf("global usage = %+v", usage.Global)
	}
	if usage.Workspaces["workspace-a"] != 125 {
		t.Fatalf("workspace usage = %d, want 125", usage.Workspaces["workspace-a"])
	}
}

func TestPlanEvictionsCanDropOneSharedEntryWithoutSchedulingSharedBlob(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 7, 16, 10, 0, 0, 0, time.UTC)
	blob := testBlob('a', 100, now)
	oldEntry := testEntry('b', blob.ID, "workspace-a", now.Add(-time.Hour))
	newEntry := testEntry('c', blob.ID, "workspace-a", now)
	plan, err := PlanEvictions(Snapshot{Blobs: []Blob{blob}, Entries: []Entry{oldEntry, newEntry}}, Limits{
		GlobalBytes: 100, GlobalEntries: 1, WorkspaceBytes: 100, MaxCandidates: 4,
	}, testLeaseManager(t, now))
	if err != nil {
		t.Fatal(err)
	}
	if !plan.Satisfied || !equalEvictions(plan.Targets, []EvictionTarget{EntryEviction(oldEntry.ID)}) {
		t.Fatalf("shared entry plan = %#v", plan)
	}
	if plan.After.Global != (Quantity{Bytes: 100, Entries: 1}) {
		t.Fatalf("shared entry after usage = %#v", plan.After.Global)
	}
}

func TestPlanEvictionsCanReclaimCleanUnreferencedMaterialization(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 7, 16, 10, 0, 0, 0, time.UTC)
	blob := testBlob('a', 100, now)
	entry := testEntry('b', blob.ID, "workspace-a", now)
	materialization := testMaterialization('c', entry.ID, blob.ID, 25, MaterializationClean, now.Add(-time.Hour))
	manager := testLeaseManager(t, now)

	plan, err := PlanEvictions(Snapshot{
		Blobs: []Blob{blob}, Entries: []Entry{entry}, Materializations: []Materialization{materialization},
	}, Limits{GlobalBytes: 100, GlobalEntries: 1, WorkspaceBytes: 100, MaxCandidates: 4}, manager)
	if err != nil {
		t.Fatalf("plan materialization quota: %v", err)
	}
	want := []EvictionTarget{MaterializationEviction(materialization.ID)}
	if !plan.Satisfied || !equalEvictions(plan.Targets, want) {
		t.Fatalf("plan = satisfied:%t targets:%+v, want %+v", plan.Satisfied, plan.Targets, want)
	}
}

func TestPlanEvictionsUsesDeterministicLRUForBytesAndEntries(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 7, 16, 10, 0, 0, 0, time.UTC)
	oldBlob := testBlob('a', 10, now.Add(-3*time.Hour))
	oldEntry := testEntry('b', oldBlob.ID, "workspace-a", now.Add(-3*time.Hour))
	midBlob := testBlob('c', 10, now.Add(-2*time.Hour))
	midEntry := testEntry('d', midBlob.ID, "workspace-a", now.Add(-2*time.Hour))
	newBlob := testBlob('e', 10, now.Add(-time.Hour))
	newEntry := testEntry('f', newBlob.ID, "workspace-a", now.Add(-time.Hour))
	snapshot := Snapshot{
		Blobs:   []Blob{newBlob, oldBlob, midBlob},
		Entries: []Entry{newEntry, oldEntry, midEntry},
	}
	manager := testLeaseManager(t, now)

	plan, err := PlanEvictions(snapshot, Limits{
		GlobalBytes: 20, GlobalEntries: 2, WorkspaceBytes: 20, MaxCandidates: 16,
	}, manager)
	if err != nil {
		t.Fatalf("plan evictions: %v", err)
	}
	if !plan.Satisfied {
		t.Fatalf("plan not satisfied: %+v", plan.Diagnostic)
	}
	want := []EvictionTarget{EntryEviction(oldEntry.ID), BlobEviction(oldBlob.ID)}
	if !equalEvictions(plan.Targets, want) {
		t.Fatalf("targets = %+v, want %+v", plan.Targets, want)
	}
	if plan.After.Global != (Quantity{Bytes: 20, Entries: 2}) {
		t.Fatalf("after usage = %+v", plan.After.Global)
	}
}

func TestPlanEvictionsEnforcesWorkspaceNonSharedShare(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 7, 16, 10, 0, 0, 0, time.UTC)
	aOldBlob := testBlob('a', 10, now.Add(-2*time.Hour))
	aOldEntry := testEntry('b', aOldBlob.ID, "workspace-a", now.Add(-2*time.Hour))
	aNewBlob := testBlob('c', 10, now.Add(-time.Hour))
	aNewEntry := testEntry('d', aNewBlob.ID, "workspace-a", now.Add(-time.Hour))
	bOlderBlob := testBlob('e', 10, now.Add(-3*time.Hour))
	bOlderEntry := testEntry('f', bOlderBlob.ID, "workspace-b", now.Add(-3*time.Hour))
	snapshot := Snapshot{
		Blobs:   []Blob{aOldBlob, aNewBlob, bOlderBlob},
		Entries: []Entry{aOldEntry, aNewEntry, bOlderEntry},
	}
	manager := testLeaseManager(t, now)

	plan, err := PlanEvictions(snapshot, Limits{
		GlobalBytes: 100, GlobalEntries: 10, WorkspaceBytes: 10, MaxCandidates: 16,
	}, manager)
	if err != nil {
		t.Fatalf("plan workspace quota: %v", err)
	}
	want := []EvictionTarget{EntryEviction(aOldEntry.ID), BlobEviction(aOldBlob.ID)}
	if !equalEvictions(plan.Targets, want) {
		t.Fatalf("targets = %+v, want workspace-a LRU %+v", plan.Targets, want)
	}
	if plan.After.Workspaces["workspace-a"] != 10 || plan.After.Workspaces["workspace-b"] != 10 {
		t.Fatalf("workspace usage after = %+v", plan.After.Workspaces)
	}
}

func TestPlanEvictionsProtectsPinnedDirtyLeasedSharedAndReferencedTargets(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 7, 16, 10, 0, 0, 0, time.UTC)
	pinnedBlob := testBlob('a', 10, now.Add(-6*time.Hour))
	pinnedEntry := testEntry('b', pinnedBlob.ID, "workspace-a", now.Add(-6*time.Hour))
	pinnedEntry.Pinned = true

	dirtyBlob := testBlob('c', 10, now.Add(-5*time.Hour))
	dirtyEntry := testEntry('d', dirtyBlob.ID, "workspace-a", now.Add(-5*time.Hour))
	dirtyMaterialization := testMaterialization('e', dirtyEntry.ID, dirtyBlob.ID, 10, MaterializationDirty, now.Add(-5*time.Hour))

	leasedBlob := testBlob('f', 10, now.Add(-4*time.Hour))
	leasedEntry := testEntry('1', leasedBlob.ID, "workspace-a", now.Add(-4*time.Hour))
	lease := Lease{
		ID: testLeaseID('2'), OwnerKind: LeaseOwnerPreview, OwnerID: "preview-1", DaemonInstanceID: "daemon-1",
		Target: BlobTarget(leasedBlob.ID), State: LeaseActive, HeartbeatAt: now,
		ExpiresAt: now.Add(2 * time.Minute), GraceUntil: now.Add(17 * time.Minute),
	}

	referencedBlob := testBlob('3', 10, now.Add(-3*time.Hour))
	referencedEntry := testEntry('4', referencedBlob.ID, "workspace-a", now.Add(-3*time.Hour))
	reference := Reference{
		ID: testReferenceID('5'), OwnerKind: ReferenceOwnerUpload, OwnerID: "upload-1",
		Target: BlobTarget(referencedBlob.ID), CreatedAt: now,
	}

	sharedBlob := testBlob('6', 10, now.Add(-2*time.Hour))
	sharedEntryA := testEntry('7', sharedBlob.ID, "workspace-a", now.Add(-2*time.Hour))
	sharedEntryB := testEntry('8', sharedBlob.ID, "workspace-b", now.Add(-2*time.Hour))

	freeBlob := testBlob('9', 10, now.Add(-time.Hour))
	freeEntry := testEntry('a', freeBlob.ID, "workspace-a", now.Add(-time.Hour))
	snapshot := Snapshot{
		Blobs:            []Blob{pinnedBlob, dirtyBlob, leasedBlob, referencedBlob, sharedBlob, freeBlob},
		Entries:          []Entry{pinnedEntry, dirtyEntry, leasedEntry, referencedEntry, sharedEntryA, sharedEntryB, freeEntry},
		Materializations: []Materialization{dirtyMaterialization}, References: []Reference{reference}, Leases: []Lease{lease},
	}
	manager := testLeaseManager(t, now)

	plan, err := PlanEvictions(snapshot, Limits{
		GlobalBytes: 60, GlobalEntries: 20, WorkspaceBytes: 100, MaxCandidates: 16,
	}, manager)
	if err != nil {
		t.Fatalf("plan protected quota: %v", err)
	}
	want := []EvictionTarget{EntryEviction(freeEntry.ID), BlobEviction(freeBlob.ID)}
	if !equalEvictions(plan.Targets, want) {
		t.Fatalf("targets = %+v, want only free object %+v", plan.Targets, want)
	}
}

func TestPlanEvictionsReportsBoundedAllProtectedDiagnostic(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 7, 16, 10, 0, 0, 0, time.UTC)
	pinnedBlob := testBlob('a', 10, now.Add(-5*time.Hour))
	pinnedEntry := testEntry('b', pinnedBlob.ID, "workspace-a", now.Add(-5*time.Hour))
	pinnedEntry.Pinned = true
	dirtyBlob := testBlob('c', 10, now.Add(-4*time.Hour))
	dirtyEntry := testEntry('d', dirtyBlob.ID, "workspace-a", now.Add(-4*time.Hour))
	dirtyMaterialization := testMaterialization('e', dirtyEntry.ID, dirtyBlob.ID, 10, MaterializationDirty, now.Add(-4*time.Hour))
	leasedBlob := testBlob('f', 10, now.Add(-3*time.Hour))
	leasedEntry := testEntry('1', leasedBlob.ID, "workspace-a", now.Add(-3*time.Hour))
	lease := Lease{
		ID: testLeaseID('2'), OwnerKind: LeaseOwnerPreview, OwnerID: "preview-1", DaemonInstanceID: "daemon-1",
		Target: BlobTarget(leasedBlob.ID), State: LeaseActive, HeartbeatAt: now,
		ExpiresAt: now.Add(2 * time.Minute), GraceUntil: now.Add(17 * time.Minute),
	}
	referencedBlob := testBlob('3', 10, now.Add(-2*time.Hour))
	referencedEntry := testEntry('4', referencedBlob.ID, "workspace-a", now.Add(-2*time.Hour))
	reference := Reference{
		ID: testReferenceID('5'), OwnerKind: ReferenceOwnerUpload, OwnerID: "upload-1",
		Target: BlobTarget(referencedBlob.ID), CreatedAt: now,
	}
	sharedBlob := testBlob('6', 10, now.Add(-time.Hour))
	sharedEntryA := testEntry('7', sharedBlob.ID, "workspace-a", now.Add(-time.Hour))
	sharedEntryB := testEntry('8', sharedBlob.ID, "workspace-b", now.Add(-time.Hour))
	snapshot := Snapshot{
		Blobs:            []Blob{pinnedBlob, dirtyBlob, leasedBlob, referencedBlob, sharedBlob},
		Entries:          []Entry{pinnedEntry, dirtyEntry, leasedEntry, referencedEntry, sharedEntryA, sharedEntryB},
		Materializations: []Materialization{dirtyMaterialization}, References: []Reference{reference}, Leases: []Lease{lease},
	}
	manager := testLeaseManager(t, now)

	plan, err := PlanEvictions(snapshot, Limits{
		GlobalBytes: 0, GlobalEntries: 5, WorkspaceBytes: 100, MaxCandidates: 4,
	}, manager)
	if err != nil {
		t.Fatalf("plan all-protected quota: %v", err)
	}
	if plan.Satisfied {
		t.Fatal("all-protected plan unexpectedly satisfied")
	}
	if plan.Diagnostic.Required.Bytes == 0 || plan.Diagnostic.Required.Entries == 0 {
		t.Fatalf("missing required quantities: %+v", plan.Diagnostic.Required)
	}
	if plan.Diagnostic.Pinned.Bytes == 0 || plan.Diagnostic.Pinned.Entries == 0 {
		t.Fatalf("missing pinned diagnostic: %+v", plan.Diagnostic.Pinned)
	}
	if plan.Diagnostic.Dirty.Bytes == 0 || plan.Diagnostic.Leased.Bytes == 0 ||
		plan.Diagnostic.Shared.Bytes == 0 || plan.Diagnostic.Referenced.Bytes == 0 {
		t.Fatalf("incomplete protection diagnostic: %+v", plan.Diagnostic)
	}
	if plan.Considered > 4 {
		t.Fatalf("considered %d candidates, max 4", plan.Considered)
	}
}

func TestPlanEvictionsDoesNotReselectDeletingBlob(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 7, 16, 10, 0, 0, 0, time.UTC)
	deleting := testBlob('a', 10, now.Add(-time.Hour))
	deleting.State = BlobDeleting
	manager := testLeaseManager(t, now)

	plan, err := PlanEvictions(Snapshot{Blobs: []Blob{deleting}}, Limits{
		GlobalBytes: 0, GlobalEntries: 0, WorkspaceBytes: 0, MaxCandidates: 4,
	}, manager)
	if err != nil {
		t.Fatalf("plan deleting blob quota: %v", err)
	}
	if len(plan.Targets) != 0 {
		t.Fatalf("deleting blob was selected again: %+v", plan.Targets)
	}
	if plan.Satisfied {
		t.Fatal("deleting blob bytes were treated as already reclaimed")
	}
}

func TestQuotaDiagnosticDoesNotDoubleCountGlobalAndWorkspaceDeficit(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 7, 16, 10, 0, 0, 0, time.UTC)
	blob := testBlob('a', 10, now.Add(-time.Hour))
	entry := testEntry('b', blob.ID, "workspace-a", now.Add(-time.Hour))
	entry.Pinned = true
	manager := testLeaseManager(t, now)

	plan, err := PlanEvictions(Snapshot{Blobs: []Blob{blob}, Entries: []Entry{entry}}, Limits{
		GlobalBytes: 0, GlobalEntries: 1, WorkspaceBytes: 0, MaxCandidates: 4,
	}, manager)
	if err != nil {
		t.Fatalf("plan overlapping quota deficit: %v", err)
	}
	if plan.Diagnostic.Required.Bytes != 10 {
		t.Fatalf("required bytes = %d, want 10 without double counting", plan.Diagnostic.Required.Bytes)
	}
}

func TestPlanEvictionsProtectsUncertainLeaseAndReclaimsBirthMismatch(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 7, 16, 10, 0, 0, 0, time.UTC)
	identity := ProcessIdentity{PID: 1234, BirthID: "linux-start-ticks:12"}
	tests := []struct {
		name        string
		status      ProcessStatus
		satisfied   bool
		targetCount int
	}{
		{name: "uncertain stays protected", status: ProcessUncertain, satisfied: false, targetCount: 0},
		{name: "birth mismatch is reclaimable", status: ProcessBirthMismatch, satisfied: true, targetCount: 2},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			blob := testBlob('a', 10, now.Add(-time.Hour))
			entry := testEntry('b', blob.ID, "workspace-a", now.Add(-time.Hour))
			lease := Lease{
				ID: testLeaseID('c'), OwnerKind: LeaseOwnerEditor, OwnerID: "editor-1", DaemonInstanceID: "daemon-1",
				Target: BlobTarget(blob.ID), State: LeaseActive, HeartbeatAt: now.Add(-20 * time.Minute),
				ExpiresAt: now.Add(-18 * time.Minute), GraceUntil: now.Add(-3 * time.Minute), Process: &identity,
			}
			manager, err := NewLeaseManager(&manualClock{now: now}, &fixedProcessClassifier{status: test.status}, 2*time.Minute, 15*time.Minute)
			if err != nil {
				t.Fatalf("new lease manager: %v", err)
			}
			plan, err := PlanEvictions(Snapshot{Blobs: []Blob{blob}, Entries: []Entry{entry}, Leases: []Lease{lease}}, Limits{
				GlobalBytes: 0, GlobalEntries: 0, WorkspaceBytes: 0, MaxCandidates: 4,
			}, manager)
			if err != nil {
				t.Fatalf("plan expired lease quota: %v", err)
			}
			if plan.Satisfied != test.satisfied || len(plan.Targets) != test.targetCount {
				t.Fatalf("plan = satisfied:%t targets:%+v, want satisfied:%t target count:%d", plan.Satisfied, plan.Targets, test.satisfied, test.targetCount)
			}
		})
	}
}

func testBlob(value byte, size int64, lastAccess time.Time) Blob {
	return Blob{
		ID: testBlobID(value), Size: size, State: BlobPublished,
		CreatedAt: lastAccess.Add(-time.Hour), LastAccessAt: lastAccess,
	}
}

func testEntry(value byte, blobID BlobID, workspace WorkspaceID, lastAccess time.Time) Entry {
	return Entry{
		ID: testEntryID(value), EndpointID: "endpoint_local", CanonicalPath: []byte("/" + string(value)),
		Fingerprint: Fingerprint{Strength: FingerprintStrong, Canonical: []byte("strong:" + string(value))},
		Freshness:   EntryFresh, Policy: PolicyLRU, WorkspaceID: workspace, BlobID: blobID,
		CreatedAt: lastAccess.Add(-time.Hour), LastAccessAt: lastAccess,
	}
}

func testMaterialization(value byte, entryID EntryID, blobID BlobID, size int64, state MaterializationState, lastAccess time.Time) Materialization {
	return Materialization{
		ID: testMaterializationID(value), EntryID: entryID, BaselineBlobID: blobID,
		CurrentBlobID: blobID, Size: size, State: state,
		CreatedAt: lastAccess.Add(-time.Hour), LastAccessAt: lastAccess,
	}
}

func testLeaseManager(t *testing.T, now time.Time) *LeaseManager {
	t.Helper()
	manager, err := NewLeaseManager(&manualClock{now: now}, &fixedProcessClassifier{status: ProcessUncertain}, 2*time.Minute, 15*time.Minute)
	if err != nil {
		t.Fatalf("new lease manager: %v", err)
	}
	return manager
}

func equalEvictions(got []EvictionTarget, want []EvictionTarget) bool {
	return slices.Equal(got, want)
}
