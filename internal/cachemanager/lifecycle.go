package cachemanager

import (
	"context"
	"errors"
	"fmt"

	"github.com/TyrantLucifer/awesome-mac-sftp/internal/cache"
	"github.com/TyrantLucifer/awesome-mac-sftp/internal/state/cachestore"
)

var ErrCacheNeedsAttention = errors.New("cache lifecycle requires operator attention")

const (
	maxLifecycleVisited = 1_000_000
	maxLifecycleBatches = 64
)

type StartupLifecycleRequest struct {
	MaxVisited int
	MaxBatches int
}

type StartupLifecycleResult struct {
	Recovered         []cachestore.EvictionClaim
	ReclaimedHandoffs int
	Reconcile         ReconcileReport
	Quota             QuotaExecution
	CatalogErr        string
}

// RunStartupLifecycle completes already-claimed deletions, inventories both
// sides, and only enforces quota when that inventory is fully reconciled.
// Unknown, corrupt, truncated, or catalog-inconsistent state is preserved.
func (manager *Manager) RunStartupLifecycle(ctx context.Context, request StartupLifecycleRequest) (StartupLifecycleResult, error) {
	if manager == nil || manager.files == nil || manager.catalog == nil {
		return StartupLifecycleResult{}, fmt.Errorf("run cache startup lifecycle: nil manager")
	}
	if ctx == nil || request.MaxVisited <= 0 || request.MaxVisited > maxLifecycleVisited || request.MaxBatches <= 0 || request.MaxBatches > maxLifecycleBatches {
		return StartupLifecycleResult{}, fmt.Errorf("run cache startup lifecycle: invalid bounds")
	}
	result := StartupLifecycleResult{}
	for batch := 0; batch < request.MaxBatches; batch++ {
		recovered, err := manager.RecoverPendingEvictions(ctx, manager.limits.MaxCandidates)
		if err != nil {
			return result, fmt.Errorf("run cache startup lifecycle recover: %w", err)
		}
		result.Recovered = append(result.Recovered, recovered...)
		if len(recovered) == 0 {
			break
		}
	}
	remaining, err := manager.catalog.ListPendingEvictions(ctx, 1)
	if err != nil {
		return result, fmt.Errorf("run cache startup lifecycle inspect pending: %w", err)
	}
	if len(remaining) != 0 {
		return result, fmt.Errorf("run cache startup lifecycle: pending eviction recovery exceeded %d batches", request.MaxBatches)
	}
	result.ReclaimedHandoffs, err = manager.reclaimStalePreviewHandoffs(ctx, manager.limits.MaxCandidates)
	if err != nil {
		return result, fmt.Errorf("run cache startup lifecycle reclaim stale preview handoffs: %w", err)
	}
	reconcile, err := manager.Reconcile(ctx, request.MaxVisited)
	result.Reconcile = reconcile
	if err != nil {
		result.CatalogErr = err.Error()
		return result, errors.Join(ErrCacheNeedsAttention, err)
	}
	if reconcile.NeedsAttention {
		return result, ErrCacheNeedsAttention
	}
	quota, err := manager.EnforceQuota(ctx, request.MaxBatches)
	result.Quota = quota
	if err != nil {
		return result, err
	}
	result.Reconcile, err = manager.Reconcile(ctx, request.MaxVisited)
	if err != nil {
		result.CatalogErr = err.Error()
		return result, errors.Join(ErrCacheNeedsAttention, err)
	}
	if result.Reconcile.NeedsAttention {
		return result, ErrCacheNeedsAttention
	}
	return result, nil
}

func (manager *Manager) reclaimStalePreviewHandoffs(ctx context.Context, limit int) (int, error) {
	if manager.leaseState == nil || limit <= 0 {
		return 0, fmt.Errorf("reclaim stale preview handoffs: invalid manager or limit")
	}
	leases, err := manager.catalog.ListLeases(ctx)
	if err != nil {
		return 0, err
	}
	references, err := manager.catalog.ListReferences(ctx)
	if err != nil {
		return 0, err
	}
	reclaimed := 0
	for _, lease := range leases {
		if reclaimed >= limit {
			break
		}
		if lease.State != cache.LeaseActive || lease.OwnerKind != cache.LeaseOwnerPreview ||
			lease.Target.MaterializationID == "" || manager.leaseState.Classify(lease) != cache.LeaseReclaimable {
			continue
		}
		var exact *cache.Reference
		ambiguous := false
		for index := range references {
			reference := &references[index]
			if reference.OwnerKind != cache.ReferenceOwnerPreview || reference.OwnerID != lease.OwnerID ||
				reference.Target != lease.Target {
				continue
			}
			if exact != nil {
				ambiguous = true
				break
			}
			exact = reference
		}
		if exact == nil || ambiguous {
			continue
		}
		current, err := manager.catalog.GetLease(ctx, lease.ID)
		if err != nil {
			return reclaimed, err
		}
		if current.State != cache.LeaseActive || current.OwnerKind != cache.LeaseOwnerPreview ||
			current.OwnerID != lease.OwnerID || current.Target != lease.Target ||
			manager.leaseState.Classify(current) != cache.LeaseReclaimable {
			continue
		}
		if err := manager.catalog.ReleaseHandoff(ctx, cachestore.ReleaseHandoffRequest{
			MaterializationID: current.Target.MaterializationID,
			ReferenceID:       exact.ID,
			LeaseID:           current.ID,
			OwnerKind:         current.OwnerKind,
			OwnerID:           current.OwnerID,
			ReleasedAt:        manager.now(),
		}); err != nil {
			return reclaimed, err
		}
		reclaimed++
	}
	return reclaimed, nil
}

type ClearScope string

const (
	ClearAll       ClearScope = "all"
	ClearWorkspace ClearScope = "workspace"
	ClearEphemeral ClearScope = "ephemeral"
)

type ClearRequest struct {
	Scope       ClearScope
	WorkspaceID cache.WorkspaceID
	MaxTargets  int
	MaxVisited  int
}

type ClearResult struct {
	Deleted   []cachestore.EvictionClaim
	Protected int
	Remaining cache.Quantity
	Status    ReconcileReport
}

// ClearEligible deletes only catalog-selected, transactionally revalidated
// targets. A filesystem anomaly aborts before the first claim; exact deletion
// routines never recurse and preserve unexpected children.
func (manager *Manager) ClearEligible(ctx context.Context, request ClearRequest) (ClearResult, error) {
	if manager == nil || manager.files == nil || manager.catalog == nil || manager.clock == nil {
		return ClearResult{}, fmt.Errorf("clear eligible cache: nil manager")
	}
	if ctx == nil || request.MaxTargets <= 0 || request.MaxTargets > manager.limits.MaxCandidates || request.MaxVisited <= 0 || request.MaxVisited > maxLifecycleVisited {
		return ClearResult{}, fmt.Errorf("clear eligible cache: invalid bounds")
	}
	switch request.Scope {
	case ClearAll, ClearEphemeral:
		if request.WorkspaceID != "" {
			return ClearResult{}, fmt.Errorf("clear eligible cache: workspace ID is only valid for workspace scope")
		}
	case ClearWorkspace:
		if request.WorkspaceID == "" || len(request.WorkspaceID) > 128 {
			return ClearResult{}, fmt.Errorf("clear eligible cache: invalid workspace ID")
		}
	default:
		return ClearResult{}, fmt.Errorf("clear eligible cache: invalid scope %q", request.Scope)
	}
	reconcile, err := manager.Reconcile(ctx, request.MaxVisited)
	if err != nil {
		return ClearResult{Status: reconcile}, errors.Join(ErrCacheNeedsAttention, err)
	}
	if reconcile.NeedsAttention {
		return ClearResult{Status: reconcile}, ErrCacheNeedsAttention
	}

	entries := make(map[cache.EntryID]cache.Entry, len(reconcile.Snapshot.Entries))
	for _, entry := range reconcile.Snapshot.Entries {
		entries[entry.ID] = entry
	}
	targets := make([]cache.EvictionTarget, 0, request.MaxTargets)
	for _, materialization := range reconcile.Snapshot.Materializations {
		if entry, ok := entries[materialization.EntryID]; ok && clearMatches(request, entry) {
			targets = append(targets, cache.MaterializationEviction(materialization.ID))
		}
	}
	for _, entry := range reconcile.Snapshot.Entries {
		if clearMatches(request, entry) {
			targets = append(targets, cache.EntryEviction(entry.ID))
		}
	}
	if request.Scope == ClearAll {
		reachable := make(map[cache.BlobID]struct{}, len(reconcile.Snapshot.Entries)+len(reconcile.Snapshot.Materializations))
		for _, entry := range reconcile.Snapshot.Entries {
			reachable[entry.BlobID] = struct{}{}
		}
		for _, materialization := range reconcile.Snapshot.Materializations {
			reachable[materialization.BaselineBlobID] = struct{}{}
		}
		for _, blob := range reconcile.Snapshot.Blobs {
			if _, ok := reachable[blob.ID]; !ok {
				targets = append(targets, cache.BlobEviction(blob.ID))
			}
		}
	}
	if len(targets) > request.MaxTargets {
		targets = targets[:request.MaxTargets]
	}

	result := ClearResult{Status: reconcile}
	for _, target := range targets {
		claim, err := manager.catalog.BeginEviction(ctx, target, manager.now())
		if errors.Is(err, cachestore.ErrEvictionProtected) {
			result.Protected++
			continue
		}
		if err != nil {
			return result, fmt.Errorf("clear eligible cache claim: %w", err)
		}
		if err := manager.executeEviction(ctx, claim); err != nil {
			return result, err
		}
		result.Deleted = append(result.Deleted, claim)
	}
	result.Status, err = manager.Reconcile(ctx, request.MaxVisited)
	if err != nil || result.Status.NeedsAttention {
		return result, errors.Join(ErrCacheNeedsAttention, err)
	}
	usage, err := cache.Account(result.Status.Snapshot)
	if err != nil {
		return result, err
	}
	result.Remaining = usage.Global
	return result, nil
}

func clearMatches(request ClearRequest, entry cache.Entry) bool {
	switch request.Scope {
	case ClearAll:
		return true
	case ClearWorkspace:
		return entry.WorkspaceID == request.WorkspaceID
	case ClearEphemeral:
		return entry.Policy == cache.PolicyEphemeral
	default:
		return false
	}
}
