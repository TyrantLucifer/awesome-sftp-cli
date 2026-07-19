package cachemanager

import (
	"bytes"
	"context"
	"errors"
	"fmt"

	"github.com/TyrantLucifer/awesome-sftp-cli/internal/cache"
	"github.com/TyrantLucifer/awesome-sftp-cli/internal/cachefs"
	"github.com/TyrantLucifer/awesome-sftp-cli/internal/state/cachestore"
)

var ErrQuotaUnsatisfied = errors.New("cache quota cannot be satisfied without deleting protected content")

const admissionMaxBatches = 4

type MarkDirtyRequest struct {
	MaterializationID cache.MaterializationID
	ReferenceID       cache.ReferenceID
	LeaseID           cache.LeaseID
	OwnerKind         cache.LeaseOwnerKind
	OwnerID           string
}

type MarkDirtyResult struct {
	Path            string
	Materialization cache.Materialization
}

// MarkDirty computes the current identity from the exact no-follow filesystem
// path and then durably binds that observation to the active handoff owner.
func (manager *Manager) MarkDirty(ctx context.Context, request MarkDirtyRequest) (MarkDirtyResult, error) {
	if manager == nil || manager.files == nil || manager.catalog == nil || manager.clock == nil {
		return MarkDirtyResult{}, fmt.Errorf("mark cache materialization dirty: nil manager")
	}
	if ctx == nil {
		return MarkDirtyResult{}, fmt.Errorf("mark cache materialization dirty: nil context")
	}
	info, err := manager.files.InspectMaterialization(request.MaterializationID)
	if err != nil {
		return MarkDirtyResult{}, fmt.Errorf("mark cache materialization dirty inspect content: %w", err)
	}
	materialization, err := manager.catalog.MarkMaterializationDirty(ctx, cachestore.MarkDirtyRequest{
		MaterializationID: request.MaterializationID,
		ReferenceID:       request.ReferenceID,
		LeaseID:           request.LeaseID,
		OwnerKind:         request.OwnerKind,
		OwnerID:           request.OwnerID,
		CurrentBlobID:     info.SHA256,
		Size:              info.Size,
		ObservedAt:        manager.now(),
	})
	if err != nil {
		return MarkDirtyResult{}, fmt.Errorf("mark cache materialization dirty catalog: %w", err)
	}
	return MarkDirtyResult{Path: info.Path, Materialization: materialization}, nil
}

type QuotaExecution struct {
	Plan    cache.EvictionPlan
	Deleted []cachestore.EvictionClaim
	Batches int
}

// RecoverPendingEvictions resumes durable deleting claims. It is safe to call
// at daemon startup and is deliberately bounded.
func (manager *Manager) RecoverPendingEvictions(ctx context.Context, limit int) ([]cachestore.EvictionClaim, error) {
	if manager == nil || manager.files == nil || manager.catalog == nil {
		return nil, fmt.Errorf("recover pending cache evictions: nil manager")
	}
	if ctx == nil {
		return nil, fmt.Errorf("recover pending cache evictions: nil context")
	}
	manager.admissionMu.Lock()
	defer manager.admissionMu.Unlock()
	return manager.recoverPendingEvictionsLocked(ctx, limit)
}

func (manager *Manager) recoverPendingEvictionsLocked(ctx context.Context, limit int) ([]cachestore.EvictionClaim, error) {
	claims, err := manager.catalog.ListPendingEvictions(ctx, limit)
	if err != nil {
		return nil, err
	}
	completed := make([]cachestore.EvictionClaim, 0, len(claims))
	for _, claim := range claims {
		if err := manager.executeEviction(ctx, claim); err != nil {
			return completed, err
		}
		completed = append(completed, claim)
	}
	return completed, nil
}

// EnforceQuota repeatedly reloads and replans after bounded batches. Every
// filesystem deletion is preceded by a catalog claim that transactionally
// rechecks references, leases, dirty/pinned state, and graph reachability.
func (manager *Manager) EnforceQuota(ctx context.Context, maxBatches int) (QuotaExecution, error) {
	if manager == nil || manager.catalog == nil || manager.files == nil || manager.clock == nil || manager.leaseState == nil {
		return QuotaExecution{}, fmt.Errorf("enforce cache quota: nil manager")
	}
	if ctx == nil || maxBatches <= 0 {
		return QuotaExecution{}, fmt.Errorf("enforce cache quota: nil context or non-positive batch limit")
	}
	manager.admissionMu.Lock()
	defer manager.admissionMu.Unlock()
	return manager.enforceQuotaLocked(ctx, maxBatches)
}

func (manager *Manager) enforceQuotaLocked(ctx context.Context, maxBatches int) (QuotaExecution, error) {
	result := QuotaExecution{}
	for result.Batches < maxBatches {
		pending, err := manager.recoverPendingEvictionsLocked(ctx, manager.limits.MaxCandidates)
		if err != nil {
			return result, fmt.Errorf("enforce cache quota recover pending: %w", err)
		}
		if len(pending) > 0 {
			result.Deleted = append(result.Deleted, pending...)
			result.Batches++
			continue
		}
		plan, err := manager.PlanQuota(ctx)
		if err != nil {
			return result, err
		}
		result.Plan = plan
		if len(plan.Targets) == 0 && plan.Satisfied {
			return result, nil
		}
		if len(plan.Targets) == 0 {
			return result, fmt.Errorf("%w: required bytes=%d entries=%d", ErrQuotaUnsatisfied, plan.Diagnostic.Required.Bytes, plan.Diagnostic.Required.Entries)
		}
		progress := false
		for _, target := range plan.Targets {
			claim, err := manager.catalog.BeginEviction(ctx, target, manager.now())
			if errors.Is(err, cachestore.ErrEvictionProtected) {
				continue
			}
			if err != nil {
				return result, fmt.Errorf("enforce cache quota claim: %w", err)
			}
			if err := manager.executeEviction(ctx, claim); err != nil {
				return result, err
			}
			result.Deleted = append(result.Deleted, claim)
			progress = true
		}
		result.Batches++
		if !progress {
			break
		}
	}
	plan, err := manager.PlanQuota(ctx)
	if err != nil {
		return result, err
	}
	result.Plan = plan
	if len(plan.Targets) == 0 && plan.Satisfied {
		return result, nil
	}
	return result, fmt.Errorf("%w after %d bounded batches: required bytes=%d entries=%d", ErrQuotaUnsatisfied, result.Batches, plan.Diagnostic.Required.Bytes, plan.Diagnostic.Required.Entries)
}

// admitLocked makes enough room for a not-yet-visible catalog record. The
// caller holds admissionMu through the eventual catalog commit, preventing a
// second live write from consuming the same capacity.
func (manager *Manager) admitLocked(ctx context.Context, admission cache.Admission) error {
	for batch := 0; batch < admissionMaxBatches; batch++ {
		if _, err := manager.recoverPendingEvictionsLocked(ctx, manager.limits.MaxCandidates); err != nil {
			return fmt.Errorf("admit cache content recover pending: %w", err)
		}
		snapshot, err := manager.catalog.LoadSnapshotBounded(ctx, maxLifecycleVisited)
		if err != nil {
			return fmt.Errorf("admit cache content load snapshot: %w", err)
		}
		candidate, err := normalizeAdmission(snapshot, admission)
		if err != nil {
			return err
		}
		if candidate.Blob == nil && candidate.Entry == nil && candidate.Materialization == nil {
			return nil
		}
		plan, err := cache.PlanAdmission(snapshot, candidate, manager.limits, manager.leaseState)
		if err != nil {
			return err
		}
		if plan.Satisfied && len(plan.Targets) == 0 {
			return nil
		}
		if len(plan.Targets) == 0 {
			return quotaAdmissionError(plan, batch)
		}
		progress := false
		for _, target := range plan.Targets {
			claim, claimErr := manager.catalog.BeginEviction(ctx, target, manager.now())
			if errors.Is(claimErr, cachestore.ErrEvictionProtected) {
				continue
			}
			if claimErr != nil {
				return fmt.Errorf("admit cache content claim eviction: %w", claimErr)
			}
			if err := manager.executeEviction(ctx, claim); err != nil {
				return err
			}
			progress = true
		}
		if !progress {
			return quotaAdmissionError(plan, batch+1)
		}
	}
	snapshot, err := manager.catalog.LoadSnapshotBounded(ctx, maxLifecycleVisited)
	if err != nil {
		return fmt.Errorf("admit cache content final snapshot: %w", err)
	}
	candidate, err := normalizeAdmission(snapshot, admission)
	if err != nil {
		return err
	}
	if candidate.Blob == nil && candidate.Entry == nil && candidate.Materialization == nil {
		return nil
	}
	plan, err := cache.PlanAdmission(snapshot, candidate, manager.limits, manager.leaseState)
	if err != nil {
		return err
	}
	if plan.Satisfied && len(plan.Targets) == 0 {
		return nil
	}
	return quotaAdmissionError(plan, admissionMaxBatches)
}

func normalizeAdmission(snapshot cache.Snapshot, admission cache.Admission) (cache.Admission, error) {
	if admission.Blob != nil {
		for _, existing := range snapshot.Blobs {
			if existing.ID != admission.Blob.ID {
				continue
			}
			if existing.Size != admission.Blob.Size || existing.State != cache.BlobPublished {
				return cache.Admission{}, fmt.Errorf("admit cache content: blob %q conflicts with catalog identity", existing.ID)
			}
			admission.Blob = nil
			break
		}
	}
	if admission.Entry != nil {
		for _, existing := range snapshot.Entries {
			if existing.ID != admission.Entry.ID {
				continue
			}
			if existing.EndpointID != admission.Entry.EndpointID || !bytes.Equal(existing.CanonicalPath, admission.Entry.CanonicalPath) ||
				!bytes.Equal(existing.Fingerprint.Canonical, admission.Entry.Fingerprint.Canonical) || existing.BlobID != admission.Entry.BlobID ||
				existing.WorkspaceID != admission.Entry.WorkspaceID {
				return cache.Admission{}, fmt.Errorf("admit cache content: entry %q conflicts with catalog identity", existing.ID)
			}
			admission.Entry = nil
			break
		}
	}
	return admission, nil
}

func quotaAdmissionError(plan cache.EvictionPlan, batches int) error {
	return fmt.Errorf("%w after %d bounded batches: required bytes=%d entries=%d pinned bytes=%d entries=%d dirty bytes=%d leased bytes=%d referenced bytes=%d",
		ErrQuotaUnsatisfied, batches, plan.Diagnostic.Required.Bytes, plan.Diagnostic.Required.Entries,
		plan.Diagnostic.Pinned.Bytes, plan.Diagnostic.Pinned.Entries, plan.Diagnostic.Dirty.Bytes,
		plan.Diagnostic.Leased.Bytes, plan.Diagnostic.Referenced.Bytes)
}

func (manager *Manager) executeEviction(ctx context.Context, claim cachestore.EvictionClaim) error {
	switch {
	case claim.Target.MaterializationID != "":
		if err := manager.files.DeleteMaterialization(claim.Target.MaterializationID, cachefs.BlobIdentity{ID: claim.MaterializationBlobID, Size: claim.MaterializationSize}); err != nil {
			return fmt.Errorf("execute cache materialization eviction bytes: %w", err)
		}
	case claim.EntryID != "":
		if err := manager.files.DeleteEntry(claim.EntryID, claim.BlobID); err != nil {
			return fmt.Errorf("execute cache entry eviction bytes: %w", err)
		}
		if claim.SharedEntryReferenceID == "" {
			if err := manager.files.DeleteBlob(cachefs.BlobIdentity{ID: claim.BlobID, Size: claim.BlobSize}); err != nil {
				return fmt.Errorf("execute cache entry blob eviction bytes: %w", err)
			}
		}
	case claim.BlobID != "":
		if err := manager.files.DeleteBlob(cachefs.BlobIdentity{ID: claim.BlobID, Size: claim.BlobSize}); err != nil {
			return fmt.Errorf("execute cache blob eviction bytes: %w", err)
		}
	default:
		return fmt.Errorf("execute cache eviction: invalid claim")
	}
	if err := manager.catalog.FinalizeEviction(ctx, claim); err != nil {
		return fmt.Errorf("execute cache eviction finalize catalog: %w", err)
	}
	return nil
}
