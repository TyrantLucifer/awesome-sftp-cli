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

	"github.com/TyrantLucifer/awesome-mac-sftp/internal/cache"
	"github.com/TyrantLucifer/awesome-mac-sftp/internal/cachemanager"
	"github.com/TyrantLucifer/awesome-mac-sftp/internal/domain"
	"github.com/TyrantLucifer/awesome-mac-sftp/internal/ipc"
	providerapi "github.com/TyrantLucifer/awesome-mac-sftp/internal/provider"
)

const CacheMaterialize = "cache.materialize"

type CacheMaterializeRequest struct {
	Location    ipc.WireLocation     `json:"location"`
	WorkspaceID cache.WorkspaceID    `json:"workspace_id"`
	Policy      cache.Policy         `json:"policy"`
	Pinned      bool                 `json:"pinned"`
	OwnerKind   cache.LeaseOwnerKind `json:"owner_kind"`
	OwnerID     string               `json:"owner_id"`
}

type CacheMaterializeResponse struct {
	EntryID           cache.EntryID           `json:"entry_id"`
	MaterializationID cache.MaterializationID `json:"materialization_id"`
	ReferenceID       cache.ReferenceID       `json:"reference_id"`
	LeaseID           cache.LeaseID           `json:"lease_id"`
	Path              string                  `json:"path"`
	SourceFingerprint ipc.WireFingerprint     `json:"source_fingerprint"`
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
		return nil, internalError("materialize verified cache content", err)
	}
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
		EntryID: published.Entry.ID, MaterializationID: materializationID, ReferenceID: referenceID, LeaseID: leaseID,
		OwnerKind: request.OwnerKind, OwnerID: request.OwnerID, Pinned: request.Pinned,
	})
	if err != nil {
		return nil, internalError("prepare cache handoff", err)
	}
	return CacheMaterializeResponse{
		EntryID: published.Entry.ID, MaterializationID: handoff.Materialization.ID, ReferenceID: handoff.Reference.ID,
		LeaseID: handoff.Lease.ID, Path: handoff.Path, SourceFingerprint: ipc.EncodeFingerprint(info.Fingerprint),
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
