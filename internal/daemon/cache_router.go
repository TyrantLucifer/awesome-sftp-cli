package daemon

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"

	"github.com/TyrantLucifer/awesome-sftp-cli/internal/cache"
	"github.com/TyrantLucifer/awesome-sftp-cli/internal/cachemanager"
	"github.com/TyrantLucifer/awesome-sftp-cli/internal/domain"
	"github.com/TyrantLucifer/awesome-sftp-cli/internal/ipc"
	providerapi "github.com/TyrantLucifer/awesome-sftp-cli/internal/provider"
)

const (
	CacheMaterialize    = "cache.materialize"
	CacheMarkDirty      = "cache.mark_dirty"
	CacheHeartbeat      = "cache.heartbeat"
	CacheReleaseHandoff = "cache.release_handoff"
	CacheClear          = "cache.clear"
	CacheLifecycle      = "cache.lifecycle"
)

type CacheMaterializeRequest struct {
	Location    ipc.WireLocation       `json:"location"`
	WorkspaceID cache.WorkspaceID      `json:"workspace_id"`
	Policy      cache.Policy           `json:"policy"`
	Pinned      bool                   `json:"pinned"`
	OwnerKind   cache.LeaseOwnerKind   `json:"owner_kind"`
	OwnerID     string                 `json:"owner_id"`
	Process     *cache.ProcessIdentity `json:"process,omitempty"`
}

type CacheMaterializeResponse struct {
	EntryID           cache.EntryID           `json:"entry_id"`
	MaterializationID cache.MaterializationID `json:"materialization_id"`
	ReferenceID       cache.ReferenceID       `json:"reference_id"`
	LeaseID           cache.LeaseID           `json:"lease_id"`
	Path              string                  `json:"path"`
	SourceFingerprint ipc.WireFingerprint     `json:"source_fingerprint"`
	Freshness         cache.EntryFreshness    `json:"freshness"`
	Offline           bool                    `json:"offline"`
}

type CacheReleaseHandoffRequest struct {
	MaterializationID cache.MaterializationID `json:"materialization_id"`
	ReferenceID       cache.ReferenceID       `json:"reference_id"`
	LeaseID           cache.LeaseID           `json:"lease_id"`
	OwnerKind         cache.LeaseOwnerKind    `json:"owner_kind"`
	OwnerID           string                  `json:"owner_id"`
}

type CacheReleaseHandoffResponse struct {
	Released bool `json:"released"`
}

type CacheMarkDirtyRequest struct {
	MaterializationID cache.MaterializationID `json:"materialization_id"`
	ReferenceID       cache.ReferenceID       `json:"reference_id"`
	LeaseID           cache.LeaseID           `json:"lease_id"`
	OwnerKind         cache.LeaseOwnerKind    `json:"owner_kind"`
	OwnerID           string                  `json:"owner_id"`
}

type CacheMarkDirtyResponse struct {
	Dirty         bool         `json:"dirty"`
	CurrentSHA256 cache.BlobID `json:"current_sha256"`
	Size          int64        `json:"size"`
}

type CacheHeartbeatRequest struct {
	MaterializationID cache.MaterializationID `json:"materialization_id"`
	LeaseID           cache.LeaseID           `json:"lease_id"`
	OwnerKind         cache.LeaseOwnerKind    `json:"owner_kind"`
	OwnerID           string                  `json:"owner_id"`
	Process           cache.ProcessIdentity   `json:"process"`
}

type CacheHeartbeatResponse struct {
	Renewed         bool  `json:"renewed"`
	HeartbeatAtUnix int64 `json:"heartbeat_at_unix"`
	ExpiresAtUnix   int64 `json:"expires_at_unix"`
	GraceUntilUnix  int64 `json:"grace_until_unix"`
}

type CacheClearRequest struct {
	Scope       cachemanager.ClearScope `json:"scope"`
	WorkspaceID cache.WorkspaceID       `json:"workspace_id,omitempty"`
	MaxTargets  int                     `json:"max_targets"`
	MaxVisited  int                     `json:"max_visited"`
}

type CacheStatus struct {
	Blobs            int  `json:"blobs"`
	Entries          int  `json:"entries"`
	Materializations int  `json:"materializations"`
	References       int  `json:"references"`
	Leases           int  `json:"leases"`
	NeedsAttention   bool `json:"needs_attention"`
	Fresh            int  `json:"fresh"`
	Stale            int  `json:"stale"`
	UnknownFreshness int  `json:"unknown_freshness"`
	LRU              int  `json:"lru"`
	Ephemeral        int  `json:"ephemeral"`
	PinnedOffline    int  `json:"pinned_offline"`
	Pinned           int  `json:"pinned"`
	Dirty            int  `json:"dirty"`
	FilesystemIssues int  `json:"filesystem_issues"`
	CatalogIssues    int  `json:"catalog_issues"`
	ScanTruncated    bool `json:"scan_truncated"`
}

type CacheClearResponse struct {
	Deleted   int            `json:"deleted"`
	Protected int            `json:"protected"`
	Remaining cache.Quantity `json:"remaining"`
	Status    CacheStatus    `json:"status"`
}

type CacheLifecycleRequest struct {
	MaxVisited int `json:"max_visited"`
	MaxBatches int `json:"max_batches"`
}

type CacheLifecycleResponse struct {
	Recovered         int         `json:"recovered"`
	ReclaimedHandoffs int         `json:"reclaimed_handoffs"`
	Deleted           int         `json:"deleted"`
	Status            CacheStatus `json:"status"`
	CatalogErr        string      `json:"catalog_error,omitempty"`
}

func (s *providerSession) cacheHeartbeat(ctx context.Context, payload json.RawMessage) (any, error) {
	if s.cache == nil {
		return nil, &domain.OpError{Code: domain.CodeUnsupported, Message: "content cache is unavailable", Retry: domain.RetryAdvice{Kind: domain.RetryNever}, Effect: domain.EffectNone}
	}
	var request CacheHeartbeatRequest
	if err := decodePayload(payload, &request); err != nil {
		return nil, invalidArgument("decode cache heartbeat request", err)
	}
	lease, err := s.cache.HeartbeatHandoff(ctx, cachemanager.HeartbeatHandoffRequest{
		MaterializationID: request.MaterializationID, LeaseID: request.LeaseID,
		OwnerKind: request.OwnerKind, OwnerID: request.OwnerID, Process: request.Process,
	})
	if err != nil {
		return nil, &domain.OpError{Code: domain.CodeConflict, Message: "cache heartbeat identity was rejected", Retry: domain.RetryAdvice{Kind: domain.RetryNever}, Effect: domain.EffectNone, Cause: err}
	}
	return CacheHeartbeatResponse{Renewed: true, HeartbeatAtUnix: lease.HeartbeatAt.Unix(), ExpiresAtUnix: lease.ExpiresAt.Unix(), GraceUntilUnix: lease.GraceUntil.Unix()}, nil
}

func (s *providerSession) cacheClear(ctx context.Context, payload json.RawMessage) (any, error) {
	if s.cache == nil {
		return nil, &domain.OpError{Code: domain.CodeUnsupported, Message: "content cache is unavailable", Retry: domain.RetryAdvice{Kind: domain.RetryNever}, Effect: domain.EffectNone}
	}
	var request CacheClearRequest
	if err := decodePayload(payload, &request); err != nil {
		return nil, invalidArgument("decode cache clear request", err)
	}
	result, err := s.cache.ClearEligible(ctx, cachemanager.ClearRequest{Scope: request.Scope, WorkspaceID: request.WorkspaceID, MaxTargets: request.MaxTargets, MaxVisited: request.MaxVisited})
	if errors.Is(err, cachemanager.ErrCacheNeedsAttention) {
		return nil, &domain.OpError{Code: domain.CodeIntegrityFailed, Message: "cache clear refused unreconciled content", Retry: domain.RetryAdvice{Kind: domain.RetryNever}, Effect: domain.EffectNone, Cause: err}
	}
	if err != nil {
		return nil, internalError("clear eligible cache content", err)
	}
	return CacheClearResponse{Deleted: len(result.Deleted), Protected: result.Protected, Remaining: result.Remaining, Status: cacheStatus(result.Status)}, nil
}

func (s *providerSession) cacheLifecycle(ctx context.Context, payload json.RawMessage) (any, error) {
	if s.cache == nil {
		return nil, &domain.OpError{Code: domain.CodeUnsupported, Message: "content cache is unavailable", Retry: domain.RetryAdvice{Kind: domain.RetryNever}, Effect: domain.EffectNone}
	}
	var request CacheLifecycleRequest
	if err := decodePayload(payload, &request); err != nil {
		return nil, invalidArgument("decode cache lifecycle request", err)
	}
	result, err := s.cache.RunStartupLifecycle(ctx, cachemanager.StartupLifecycleRequest{MaxVisited: request.MaxVisited, MaxBatches: request.MaxBatches})
	if errors.Is(err, cachemanager.ErrCacheNeedsAttention) {
		return nil, &domain.OpError{Code: domain.CodeIntegrityFailed, Message: "cache lifecycle found unreconciled content", Retry: domain.RetryAdvice{Kind: domain.RetryNever}, Effect: domain.EffectNone, Cause: err}
	}
	if err != nil {
		return nil, internalError("run cache lifecycle", err)
	}
	return CacheLifecycleResponse{Recovered: len(result.Recovered), ReclaimedHandoffs: result.ReclaimedHandoffs, Deleted: len(result.Quota.Deleted), Status: cacheStatus(result.Reconcile), CatalogErr: result.CatalogErr}, nil
}

func cacheStatus(report cachemanager.ReconcileReport) CacheStatus {
	status := CacheStatus{
		Blobs: len(report.Snapshot.Blobs), Entries: len(report.Snapshot.Entries),
		Materializations: len(report.Snapshot.Materializations), References: len(report.Snapshot.References),
		Leases: len(report.Snapshot.Leases), NeedsAttention: report.NeedsAttention,
		FilesystemIssues: len(report.Filesystem.Orphans) + len(report.Filesystem.Unknown) + len(report.Filesystem.Symlinks),
		CatalogIssues: len(report.UncatalogedBlobs) + len(report.UncatalogedEntries) + len(report.UncatalogedMaterializations) +
			len(report.MissingBlobs) + len(report.MissingEntries) + len(report.MissingMaterializations) + len(report.DirtyCandidates),
		ScanTruncated: report.Filesystem.Truncated,
	}
	for _, entry := range report.Snapshot.Entries {
		switch entry.Freshness {
		case cache.EntryFresh:
			status.Fresh++
		case cache.EntryStale:
			status.Stale++
		case cache.EntryUnknown:
			status.UnknownFreshness++
		}
		switch entry.Policy {
		case cache.PolicyLRU:
			status.LRU++
		case cache.PolicyEphemeral:
			status.Ephemeral++
		case cache.PolicyPinnedOffline:
			status.PinnedOffline++
		}
		if entry.Pinned {
			status.Pinned++
		}
	}
	for _, materialization := range report.Snapshot.Materializations {
		if materialization.Pinned {
			status.Pinned++
		}
		if materialization.State == cache.MaterializationDirty {
			status.Dirty++
		}
	}
	return status
}

func (s *providerSession) cacheMarkDirty(ctx context.Context, payload json.RawMessage) (any, error) {
	if s.cache == nil {
		return nil, &domain.OpError{Code: domain.CodeUnsupported, Message: "content cache is unavailable", Retry: domain.RetryAdvice{Kind: domain.RetryNever}, Effect: domain.EffectNone}
	}
	var request CacheMarkDirtyRequest
	if err := decodePayload(payload, &request); err != nil {
		return nil, invalidArgument("decode cache mark-dirty request", err)
	}
	result, err := s.cache.MarkDirty(ctx, cachemanager.MarkDirtyRequest{
		MaterializationID: request.MaterializationID,
		ReferenceID:       request.ReferenceID,
		LeaseID:           request.LeaseID,
		OwnerKind:         request.OwnerKind,
		OwnerID:           request.OwnerID,
	})
	if err != nil {
		return nil, internalError("mark cache materialization dirty", err)
	}
	return CacheMarkDirtyResponse{Dirty: true, CurrentSHA256: result.Materialization.CurrentBlobID, Size: result.Materialization.Size}, nil
}

func (s *providerSession) cacheReleaseHandoff(ctx context.Context, payload json.RawMessage) (any, error) {
	if s.cache == nil {
		return nil, &domain.OpError{Code: domain.CodeUnsupported, Message: "content cache is unavailable", Retry: domain.RetryAdvice{Kind: domain.RetryNever}, Effect: domain.EffectNone}
	}
	var request CacheReleaseHandoffRequest
	if err := decodePayload(payload, &request); err != nil {
		return nil, invalidArgument("decode cache handoff release request", err)
	}
	if err := s.cache.ReleaseHandoff(ctx, cachemanager.ReleaseHandoffRequest{
		MaterializationID: request.MaterializationID, ReferenceID: request.ReferenceID, LeaseID: request.LeaseID,
		OwnerKind: request.OwnerKind, OwnerID: request.OwnerID,
	}); err != nil {
		return nil, internalError("release cache handoff", err)
	}
	return CacheReleaseHandoffResponse{Released: true}, nil
}

func (s *providerSession) cacheMaterialize(ctx context.Context, payload json.RawMessage) (response any, resultErr error) {
	if s.cache == nil {
		return nil, &domain.OpError{Code: domain.CodeUnsupported, Message: "content cache is unavailable", Retry: domain.RetryAdvice{Kind: domain.RetryNever}, Effect: domain.EffectNone}
	}
	var request CacheMaterializeRequest
	if err := decodePayload(payload, &request); err != nil {
		return nil, invalidArgument("decode cache materialization request", err)
	}
	if request.WorkspaceID == "" || len(request.WorkspaceID) > 128 || request.OwnerID == "" || len(request.OwnerID) > 128 {
		return nil, invalidArgument("validate cache materialization owner", nil)
	}
	switch request.Policy {
	case cache.PolicyLRU, cache.PolicyEphemeral, cache.PolicyPinnedOffline:
	default:
		return nil, invalidArgument("validate cache materialization policy", nil)
	}
	if request.Policy == cache.PolicyPinnedOffline && !request.Pinned {
		return nil, invalidArgument("pinned_offline cache materialization must be pinned", nil)
	}
	switch request.OwnerKind {
	case cache.LeaseOwnerPreview, cache.LeaseOwnerEditor, cache.LeaseOwnerOpener, cache.LeaseOwnerUpload:
	default:
		return nil, invalidArgument("validate cache materialization owner kind", nil)
	}
	if request.Process != nil {
		if err := request.Process.Validate(); err != nil {
			return nil, invalidArgument("validate cache materialization process identity", err)
		}
	}
	location, err := ipc.DecodeLocation(request.Location)
	if err != nil {
		return nil, invalidArgument("decode cache materialization location", err)
	}
	implementation, err := s.provider(location.EndpointID)
	if err != nil {
		return nil, err
	}
	handle, err := implementation.OpenRead(ctx, providerapi.OpenReadRequest{Location: location})
	if err != nil {
		if domain.IsCode(err, domain.CodeTransportInterrupted) && request.Policy == cache.PolicyPinnedOffline && request.Pinned {
			offline, offlineErr := s.cache.ResolvePinnedOffline(ctx, cachemanager.PinnedOfflineRequest{Location: location, WorkspaceID: request.WorkspaceID})
			switch {
			case offlineErr == nil:
				return s.prepareCacheMaterialization(ctx, request, location, offline.Entry, offline.SourceFingerprint, true)
			case errors.Is(offlineErr, cachemanager.ErrPinnedOfflineUnavailable):
				return nil, err
			default:
				return nil, &domain.OpError{Code: domain.CodeIntegrityFailed, Message: "pinned offline cache content could not be selected safely", EndpointID: location.EndpointID, Retry: domain.RetryAdvice{Kind: domain.RetryNever}, Effect: domain.EffectNone, Cause: offlineErr}
			}
		}
		return nil, err
	}
	defer func() { resultErr = errors.Join(resultErr, handle.Close(context.Background())) }()
	info := handle.Info()
	if info.Entry.Kind != domain.EntryFile || info.Fingerprint.Strength() == domain.FingerprintWeak || info.Entry.Metadata.Size == nil {
		return nil, &domain.OpError{Code: domain.CodeUnsupported, Message: "only regular files with a reliable size and fingerprint can be materialized", EndpointID: location.EndpointID, Retry: domain.RetryAdvice{Kind: domain.RetryNever}, Effect: domain.EffectNone}
	}
	if *info.Entry.Metadata.Size > uint64(cache.DefaultGlobalBytes) || *info.Entry.Metadata.Size > math.MaxInt64 {
		return nil, &domain.OpError{Code: domain.CodeResourceExhausted, Message: "file exceeds the cache byte budget", EndpointID: location.EndpointID, Retry: domain.RetryAdvice{Kind: domain.RetryNever}, Effect: domain.EffectNone}
	}
	expectedSize := int64(*info.Entry.Metadata.Size)
	published, err := s.cache.PublishComplete(ctx, cachemanager.PublishRequest{
		Location: location, SourceFingerprint: info.Fingerprint, WorkspaceID: request.WorkspaceID, Policy: request.Policy,
		Pinned: request.Pinned, Source: providerHandleReader{ctx: ctx, handle: handle}, MaxBytes: expectedSize, ExpectedSize: &expectedSize,
	})
	if err != nil {
		if errors.Is(err, cachemanager.ErrQuotaUnsatisfied) {
			return nil, &domain.OpError{Code: domain.CodeResourceExhausted, Message: "cache quota cannot admit materialization", EndpointID: location.EndpointID, Retry: domain.RetryAdvice{Kind: domain.RetryNever}, Effect: domain.EffectNone, Cause: err}
		}
		return nil, internalError("materialize verified cache content", err)
	}
	return s.prepareCacheMaterialization(ctx, request, location, published.Entry, info.Fingerprint, false)
}

func (s *providerSession) prepareCacheMaterialization(ctx context.Context, request CacheMaterializeRequest, location domain.Location, entry cache.Entry, sourceFingerprint domain.Fingerprint, offline bool) (any, error) {
	materializationID, err := randomMaterializationID()
	if err != nil {
		return nil, internalError("create materialization identity", err)
	}
	referenceID, err := randomReferenceID()
	if err != nil {
		return nil, internalError("create cache reference identity", err)
	}
	leaseID, err := randomLeaseID()
	if err != nil {
		return nil, internalError("create cache lease identity", err)
	}
	handoff, err := s.cache.PrepareHandoff(ctx, cachemanager.HandoffRequest{
		EntryID: entry.ID, MaterializationID: materializationID, ReferenceID: referenceID, LeaseID: leaseID,
		OwnerKind: request.OwnerKind, OwnerID: request.OwnerID, Pinned: request.Pinned, Process: request.Process,
		RequireUniquePinnedOffline: offline,
	})
	if err != nil {
		if errors.Is(err, cachemanager.ErrQuotaUnsatisfied) {
			return nil, &domain.OpError{Code: domain.CodeResourceExhausted, Message: "cache quota cannot admit materialization", EndpointID: location.EndpointID, Retry: domain.RetryAdvice{Kind: domain.RetryNever}, Effect: domain.EffectNone, Cause: err}
		}
		return nil, internalError("prepare cache handoff", err)
	}
	freshness := cache.EntryFresh
	if offline {
		freshness = cache.EntryUnknown
	}
	return CacheMaterializeResponse{
		EntryID: entry.ID, MaterializationID: handoff.Materialization.ID, ReferenceID: handoff.Reference.ID,
		LeaseID: handoff.Lease.ID, Path: handoff.Path, SourceFingerprint: ipc.EncodeFingerprint(sourceFingerprint),
		Freshness: freshness, Offline: offline,
	}, nil
}

type providerHandleReader struct {
	ctx    context.Context
	handle providerapi.ReadHandle
}

func (reader providerHandleReader) Read(destination []byte) (int, error) {
	if reader.ctx == nil || reader.handle == nil {
		return 0, io.ErrClosedPipe
	}
	return reader.handle.Read(reader.ctx, destination)
}

func randomMaterializationID() (cache.MaterializationID, error) {
	value, err := randomHexID()
	if err != nil {
		return "", err
	}
	return cache.ParseMaterializationID(value)
}

func randomReferenceID() (cache.ReferenceID, error) {
	value, err := randomHexID()
	if err != nil {
		return "", err
	}
	return cache.ParseReferenceID(value)
}

func randomLeaseID() (cache.LeaseID, error) {
	value, err := randomHexID()
	if err != nil {
		return "", err
	}
	return cache.ParseLeaseID(value)
}

func randomHexID() (string, error) {
	var value [16]byte
	if _, err := io.ReadFull(rand.Reader, value[:]); err != nil {
		return "", fmt.Errorf("read random identity: %w", err)
	}
	return hex.EncodeToString(value[:]), nil
}
