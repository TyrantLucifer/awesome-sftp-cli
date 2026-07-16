package cache

import (
	"fmt"
	"math"
	"sort"
	"time"
)

const (
	DefaultGlobalBytes    int64 = 2 << 30
	DefaultGlobalEntries        = 4096
	DefaultWorkspaceBytes int64 = 1 << 30
	DefaultMaxCandidates        = 256
)

type Quantity struct {
	Bytes   int64
	Entries int
}

type Usage struct {
	Global      Quantity
	Workspaces  map[WorkspaceID]int64
	SharedBytes int64
}

type Limits struct {
	GlobalBytes    int64
	GlobalEntries  int
	WorkspaceBytes int64
	MaxCandidates  int
}

func DefaultLimits() Limits {
	return Limits{
		GlobalBytes: DefaultGlobalBytes, GlobalEntries: DefaultGlobalEntries,
		WorkspaceBytes: DefaultWorkspaceBytes, MaxCandidates: DefaultMaxCandidates,
	}
}

type EvictionTarget struct {
	BlobID            BlobID
	EntryID           EntryID
	MaterializationID MaterializationID
}

func BlobEviction(id BlobID) EvictionTarget {
	return EvictionTarget{BlobID: id}
}

func EntryEviction(id EntryID) EvictionTarget {
	return EvictionTarget{EntryID: id}
}

func MaterializationEviction(id MaterializationID) EvictionTarget {
	return EvictionTarget{MaterializationID: id}
}

type QuotaDiagnostic struct {
	Required   Quantity
	Releasable Quantity
	Pinned     Quantity
	Dirty      Quantity
	Leased     Quantity
	Shared     Quantity
	Referenced Quantity
}

type EvictionPlan struct {
	Before     Usage
	After      Usage
	Targets    []EvictionTarget
	Satisfied  bool
	Considered int
	Diagnostic QuotaDiagnostic
}

func Account(snapshot Snapshot) (Usage, error) {
	if err := snapshot.Validate(); err != nil {
		return Usage{}, fmt.Errorf("account cache quota: %w", err)
	}
	return accountValidated(snapshot)
}

func PlanEvictions(snapshot Snapshot, limits Limits, leases *LeaseManager) (EvictionPlan, error) {
	if err := snapshot.Validate(); err != nil {
		return EvictionPlan{}, fmt.Errorf("plan cache evictions: %w", err)
	}
	if err := limits.validate(); err != nil {
		return EvictionPlan{}, err
	}
	if len(snapshot.Leases) > 0 && leases == nil {
		return EvictionPlan{}, fmt.Errorf("plan cache evictions: leases require a lease manager")
	}

	working := cloneSnapshot(snapshot)
	before, err := accountValidated(working)
	if err != nil {
		return EvictionPlan{}, err
	}
	plan := EvictionPlan{Before: before, After: before}
	for !withinLimits(plan.After, limits) && plan.Considered < limits.MaxCandidates {
		protections := inspectProtections(working, leases)
		candidates := enumerateCandidates(working, protections, withinByteLimits(plan.After, limits))
		if len(candidates) == 0 {
			break
		}
		sort.Slice(candidates, func(left int, right int) bool {
			if !candidates[left].lastAccess.Equal(candidates[right].lastAccess) {
				return candidates[left].lastAccess.Before(candidates[right].lastAccess)
			}
			if candidates[left].kind != candidates[right].kind {
				return candidates[left].kind < candidates[right].kind
			}
			return candidates[left].id < candidates[right].id
		})

		selected := false
		for _, candidate := range candidates {
			if plan.Considered >= limits.MaxCandidates {
				break
			}
			plan.Considered++
			trial := cloneSnapshot(working)
			targets := applyCandidate(&trial, candidate, protections)
			trialUsage, accountErr := accountValidated(trial)
			if accountErr != nil {
				return EvictionPlan{}, accountErr
			}
			if !improvesViolation(plan.After, trialUsage, limits) {
				continue
			}
			working = trial
			plan.After = trialUsage
			plan.Targets = append(plan.Targets, targets...)
			selected = true
			break
		}
		if !selected {
			break
		}
	}

	plan.Satisfied = withinLimits(plan.After, limits)
	if !plan.Satisfied {
		protections := inspectProtections(working, leases)
		plan.Diagnostic = buildDiagnostic(working, plan.After, limits, protections)
	}
	return plan, nil
}

func (limits Limits) validate() error {
	if limits.GlobalBytes < 0 || limits.GlobalEntries < 0 || limits.WorkspaceBytes < 0 || limits.MaxCandidates <= 0 {
		return fmt.Errorf("plan cache evictions: quota limits must be non-negative and max candidates positive")
	}
	return nil
}

func accountValidated(snapshot Snapshot) (Usage, error) {
	usage := Usage{Global: Quantity{Entries: len(snapshot.Entries)}, Workspaces: make(map[WorkspaceID]int64)}
	entriesByBlob := make(map[BlobID][]Entry)
	entriesByID := make(map[EntryID]Entry, len(snapshot.Entries))
	for _, entry := range snapshot.Entries {
		entriesByBlob[entry.BlobID] = append(entriesByBlob[entry.BlobID], entry)
		entriesByID[entry.ID] = entry
		usage.Workspaces[entry.WorkspaceID] += 0
	}
	for _, blob := range snapshot.Blobs {
		if err := addBytes(&usage.Global.Bytes, blob.Size); err != nil {
			return Usage{}, fmt.Errorf("account cache quota blob %q: %w", blob.ID, err)
		}
		workspaces := make(map[WorkspaceID]struct{})
		for _, entry := range entriesByBlob[blob.ID] {
			workspaces[entry.WorkspaceID] = struct{}{}
		}
		switch len(workspaces) {
		case 0:
		case 1:
			for workspaceID := range workspaces {
				workspaceBytes := usage.Workspaces[workspaceID]
				if err := addBytes(&workspaceBytes, blob.Size); err != nil {
					return Usage{}, fmt.Errorf("account cache quota workspace %q: %w", workspaceID, err)
				}
				usage.Workspaces[workspaceID] = workspaceBytes
			}
		default:
			if err := addBytes(&usage.SharedBytes, blob.Size); err != nil {
				return Usage{}, fmt.Errorf("account cache shared quota: %w", err)
			}
		}
	}
	for _, materialization := range snapshot.Materializations {
		if err := addBytes(&usage.Global.Bytes, materialization.Size); err != nil {
			return Usage{}, fmt.Errorf("account cache quota materialization %q: %w", materialization.ID, err)
		}
		entry := entriesByID[materialization.EntryID]
		workspaceBytes := usage.Workspaces[entry.WorkspaceID]
		if err := addBytes(&workspaceBytes, materialization.Size); err != nil {
			return Usage{}, fmt.Errorf("account cache quota workspace %q: %w", entry.WorkspaceID, err)
		}
		usage.Workspaces[entry.WorkspaceID] = workspaceBytes
	}
	return usage, nil
}

func addBytes(destination *int64, amount int64) error {
	if amount > math.MaxInt64-*destination {
		return fmt.Errorf("byte count overflow")
	}
	*destination += amount
	return nil
}

type protectionIndex struct {
	leasedBlobs                map[BlobID]struct{}
	leasedMaterializations     map[MaterializationID]struct{}
	referencedBlobs            map[BlobID]struct{}
	referencedMaterializations map[MaterializationID]struct{}
	materializationsByEntry    map[EntryID]struct{}
	entriesByBlob              map[BlobID]int
}

func inspectProtections(snapshot Snapshot, leases *LeaseManager) protectionIndex {
	index := protectionIndex{
		leasedBlobs: make(map[BlobID]struct{}), leasedMaterializations: make(map[MaterializationID]struct{}),
		referencedBlobs: make(map[BlobID]struct{}), referencedMaterializations: make(map[MaterializationID]struct{}),
		materializationsByEntry: make(map[EntryID]struct{}), entriesByBlob: make(map[BlobID]int),
	}
	for _, entry := range snapshot.Entries {
		index.entriesByBlob[entry.BlobID]++
	}
	for _, materialization := range snapshot.Materializations {
		index.materializationsByEntry[materialization.EntryID] = struct{}{}
	}
	for _, reference := range snapshot.References {
		if reference.Target.BlobID != "" {
			index.referencedBlobs[reference.Target.BlobID] = struct{}{}
		} else {
			index.referencedMaterializations[reference.Target.MaterializationID] = struct{}{}
		}
	}
	for _, lease := range snapshot.Leases {
		if leases != nil && leases.Classify(lease) == LeaseReclaimable {
			continue
		}
		if lease.Target.BlobID != "" {
			index.leasedBlobs[lease.Target.BlobID] = struct{}{}
		} else {
			index.leasedMaterializations[lease.Target.MaterializationID] = struct{}{}
		}
	}
	return index
}

type evictionCandidate struct {
	kind       string
	id         string
	lastAccess time.Time
	target     EvictionTarget
}

func enumerateCandidates(snapshot Snapshot, protections protectionIndex, allowSharedEntries bool) []evictionCandidate {
	candidates := make([]evictionCandidate, 0, len(snapshot.Entries)+len(snapshot.Materializations)+len(snapshot.Blobs))
	blobsByID := make(map[BlobID]Blob, len(snapshot.Blobs))
	for _, blob := range snapshot.Blobs {
		blobsByID[blob.ID] = blob
	}
	for _, entry := range snapshot.Entries {
		if entry.Pinned || protections.entriesByBlob[entry.BlobID] > 1 && !allowSharedEntries {
			continue
		}
		if _, exists := protections.materializationsByEntry[entry.ID]; exists {
			continue
		}
		if targetBlobProtected(entry.BlobID, protections) {
			continue
		}
		lastAccess := entry.LastAccessAt
		if blob := blobsByID[entry.BlobID]; blob.LastAccessAt.After(lastAccess) {
			lastAccess = blob.LastAccessAt
		}
		candidates = append(candidates, evictionCandidate{
			kind: "entry", id: string(entry.ID), lastAccess: lastAccess, target: EntryEviction(entry.ID),
		})
	}
	for _, materialization := range snapshot.Materializations {
		if materialization.Pinned || materialization.State != MaterializationClean {
			continue
		}
		if _, exists := protections.leasedMaterializations[materialization.ID]; exists {
			continue
		}
		if _, exists := protections.referencedMaterializations[materialization.ID]; exists {
			continue
		}
		candidates = append(candidates, evictionCandidate{
			kind: "materialization", id: string(materialization.ID), lastAccess: materialization.LastAccessAt,
			target: MaterializationEviction(materialization.ID),
		})
	}
	for _, blob := range snapshot.Blobs {
		if blob.State != BlobPublished || protections.entriesByBlob[blob.ID] != 0 || blobReachableFromMaterialization(blob.ID, snapshot.Materializations) ||
			targetBlobProtected(blob.ID, protections) {
			continue
		}
		candidates = append(candidates, evictionCandidate{
			kind: "blob", id: string(blob.ID), lastAccess: blob.LastAccessAt, target: BlobEviction(blob.ID),
		})
	}
	return candidates
}

func targetBlobProtected(blobID BlobID, protections protectionIndex) bool {
	if _, exists := protections.leasedBlobs[blobID]; exists {
		return true
	}
	_, exists := protections.referencedBlobs[blobID]
	return exists
}

func blobReachableFromMaterialization(blobID BlobID, materializations []Materialization) bool {
	for _, materialization := range materializations {
		if materialization.BaselineBlobID == blobID {
			return true
		}
	}
	return false
}

func applyCandidate(snapshot *Snapshot, candidate evictionCandidate, protections protectionIndex) []EvictionTarget {
	switch {
	case candidate.target.EntryID != "":
		var blobID BlobID
		remaining := snapshot.Entries[:0]
		for _, entry := range snapshot.Entries {
			if entry.ID == candidate.target.EntryID {
				blobID = entry.BlobID
				continue
			}
			remaining = append(remaining, entry)
		}
		snapshot.Entries = remaining
		targets := []EvictionTarget{candidate.target}
		if blobID != "" && !blobReachable(blobID, *snapshot) && !targetBlobProtected(blobID, protections) {
			removeBlob(snapshot, blobID)
			targets = append(targets, BlobEviction(blobID))
		}
		return targets
	case candidate.target.MaterializationID != "":
		remaining := snapshot.Materializations[:0]
		for _, materialization := range snapshot.Materializations {
			if materialization.ID != candidate.target.MaterializationID {
				remaining = append(remaining, materialization)
			}
		}
		snapshot.Materializations = remaining
		return []EvictionTarget{candidate.target}
	default:
		removeBlob(snapshot, candidate.target.BlobID)
		return []EvictionTarget{candidate.target}
	}
}

func blobReachable(blobID BlobID, snapshot Snapshot) bool {
	for _, entry := range snapshot.Entries {
		if entry.BlobID == blobID {
			return true
		}
	}
	return blobReachableFromMaterialization(blobID, snapshot.Materializations)
}

func removeBlob(snapshot *Snapshot, blobID BlobID) {
	remaining := snapshot.Blobs[:0]
	for _, blob := range snapshot.Blobs {
		if blob.ID != blobID {
			remaining = append(remaining, blob)
		}
	}
	snapshot.Blobs = remaining
}

func withinLimits(usage Usage, limits Limits) bool {
	if usage.Global.Bytes > limits.GlobalBytes || usage.Global.Entries > limits.GlobalEntries {
		return false
	}
	for _, bytes := range usage.Workspaces {
		if bytes > limits.WorkspaceBytes {
			return false
		}
	}
	return true
}

func withinByteLimits(usage Usage, limits Limits) bool {
	if usage.Global.Bytes > limits.GlobalBytes {
		return false
	}
	for _, bytes := range usage.Workspaces {
		if bytes > limits.WorkspaceBytes {
			return false
		}
	}
	return true
}

func improvesViolation(before Usage, after Usage, limits Limits) bool {
	if before.Global.Bytes > limits.GlobalBytes && after.Global.Bytes < before.Global.Bytes {
		return true
	}
	if before.Global.Entries > limits.GlobalEntries && after.Global.Entries < before.Global.Entries {
		return true
	}
	for workspaceID, bytes := range before.Workspaces {
		if bytes > limits.WorkspaceBytes && after.Workspaces[workspaceID] < bytes {
			return true
		}
	}
	return false
}

func buildDiagnostic(snapshot Snapshot, usage Usage, limits Limits, protections protectionIndex) QuotaDiagnostic {
	diagnostic := QuotaDiagnostic{Required: requiredQuantity(usage, limits)}
	blobsByID := make(map[BlobID]Blob, len(snapshot.Blobs))
	entriesByBlob := make(map[BlobID][]Entry)
	materializationsByID := make(map[MaterializationID]Materialization, len(snapshot.Materializations))
	for _, blob := range snapshot.Blobs {
		blobsByID[blob.ID] = blob
	}
	for _, entry := range snapshot.Entries {
		entriesByBlob[entry.BlobID] = append(entriesByBlob[entry.BlobID], entry)
		if entry.Pinned {
			diagnostic.Pinned.Entries++
		}
	}
	for _, materialization := range snapshot.Materializations {
		materializationsByID[materialization.ID] = materialization
		if materialization.Pinned {
			diagnostic.Pinned.Bytes += materialization.Size
		}
		if materialization.State == MaterializationDirty || materialization.State == MaterializationOrphaned {
			diagnostic.Dirty.Bytes += materialization.Size
		}
	}
	seenPinnedBlobs := make(map[BlobID]struct{})
	for _, entry := range snapshot.Entries {
		if !entry.Pinned {
			continue
		}
		if _, seen := seenPinnedBlobs[entry.BlobID]; !seen {
			diagnostic.Pinned.Bytes += blobsByID[entry.BlobID].Size
			seenPinnedBlobs[entry.BlobID] = struct{}{}
		}
	}
	for blobID := range protections.leasedBlobs {
		diagnostic.Leased.Bytes += blobsByID[blobID].Size
		diagnostic.Leased.Entries += len(entriesByBlob[blobID])
	}
	for materializationID := range protections.leasedMaterializations {
		diagnostic.Leased.Bytes += materializationsByID[materializationID].Size
	}
	for blobID := range protections.referencedBlobs {
		diagnostic.Referenced.Bytes += blobsByID[blobID].Size
		diagnostic.Referenced.Entries += len(entriesByBlob[blobID])
	}
	for materializationID := range protections.referencedMaterializations {
		diagnostic.Referenced.Bytes += materializationsByID[materializationID].Size
	}
	for blobID, count := range protections.entriesByBlob {
		if count > 1 {
			diagnostic.Shared.Bytes += blobsByID[blobID].Size
			diagnostic.Shared.Entries += count
		}
	}
	for _, candidate := range enumerateCandidates(snapshot, protections, false) {
		switch {
		case candidate.target.EntryID != "":
			diagnostic.Releasable.Entries++
			for _, entry := range snapshot.Entries {
				if entry.ID == candidate.target.EntryID {
					diagnostic.Releasable.Bytes += blobsByID[entry.BlobID].Size
					break
				}
			}
		case candidate.target.MaterializationID != "":
			diagnostic.Releasable.Bytes += materializationsByID[candidate.target.MaterializationID].Size
		default:
			diagnostic.Releasable.Bytes += blobsByID[candidate.target.BlobID].Size
		}
	}
	return diagnostic
}

func requiredQuantity(usage Usage, limits Limits) Quantity {
	required := Quantity{}
	if usage.Global.Bytes > limits.GlobalBytes {
		required.Bytes = usage.Global.Bytes - limits.GlobalBytes
	}
	if usage.Global.Entries > limits.GlobalEntries {
		required.Entries = usage.Global.Entries - limits.GlobalEntries
	}
	var workspaceRequired int64
	for _, bytes := range usage.Workspaces {
		if bytes > limits.WorkspaceBytes {
			workspaceRequired += bytes - limits.WorkspaceBytes
		}
	}
	if workspaceRequired > required.Bytes {
		required.Bytes = workspaceRequired
	}
	return required
}

func cloneSnapshot(snapshot Snapshot) Snapshot {
	clone := snapshot
	clone.Blobs = append([]Blob(nil), snapshot.Blobs...)
	clone.Entries = append([]Entry(nil), snapshot.Entries...)
	clone.Materializations = append([]Materialization(nil), snapshot.Materializations...)
	clone.References = append([]Reference(nil), snapshot.References...)
	clone.Leases = append([]Lease(nil), snapshot.Leases...)
	return clone
}
