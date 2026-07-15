package daemon

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"sort"
	"sync"

	"github.com/TyrantLucifer/awesome-mac-sftp/internal/domain"
	"github.com/TyrantLucifer/awesome-mac-sftp/internal/ipc"
	providerapi "github.com/TyrantLucifer/awesome-mac-sftp/internal/provider"
)

const (
	ProviderEndpoints = "provider.endpoints"
	ProviderSnapshot  = "provider.snapshot"
	ProviderNormalize = "provider.normalize"
	ProviderList      = "provider.list"
	ProviderStat      = "provider.stat"
	ProviderRead      = "provider.read"
)

type ProviderSessions struct {
	providers    map[domain.EndpointID]providerapi.Provider
	maxReadBytes uint32
}

func NewProviderSessions(providers []providerapi.Provider, maxReadBytes uint32) (*ProviderSessions, error) {
	if len(providers) == 0 {
		return nil, errors.New("create provider sessions: no providers")
	}
	if maxReadBytes == 0 {
		return nil, errors.New("create provider sessions: maximum read bytes is zero")
	}
	indexed := make(map[domain.EndpointID]providerapi.Provider, len(providers))
	for _, implementation := range providers {
		if implementation == nil {
			return nil, errors.New("create provider sessions: nil provider")
		}
		descriptor := implementation.Descriptor()
		if descriptor.ID == "" {
			return nil, errors.New("create provider sessions: provider endpoint ID is empty")
		}
		if _, duplicate := indexed[descriptor.ID]; duplicate {
			return nil, fmt.Errorf("create provider sessions: duplicate endpoint %q", descriptor.ID)
		}
		indexed[descriptor.ID] = implementation
	}
	return &ProviderSessions{providers: indexed, maxReadBytes: maxReadBytes}, nil
}

func (s *ProviderSessions) NewSession() Session {
	return &providerSession{
		providers:    s.providers,
		maxReadBytes: s.maxReadBytes,
		cursors:      make(map[cursorKey]providerapi.Provider),
	}
}

type cursorKey struct {
	endpointID domain.EndpointID
	cursor     providerapi.PageCursor
}

type cursorDiscarder interface {
	DiscardCursor(providerapi.PageCursor) error
}

type providerSession struct {
	mu sync.Mutex

	providers    map[domain.EndpointID]providerapi.Provider
	maxReadBytes uint32
	cursors      map[cursorKey]providerapi.Provider
	closed       bool
}

func (s *providerSession) Handle(ctx context.Context, name string, payload json.RawMessage) (any, error) {
	s.mu.Lock()
	closed := s.closed
	s.mu.Unlock()
	if closed {
		return nil, internalError("provider session is closed", nil)
	}
	switch name {
	case ProviderEndpoints:
		return s.endpoints(), nil
	case ProviderSnapshot:
		return s.snapshot(ctx, payload)
	case ProviderNormalize:
		return s.normalize(ctx, payload)
	case ProviderList:
		return s.list(ctx, payload)
	case ProviderStat:
		return s.stat(ctx, payload)
	case ProviderRead:
		return s.read(ctx, payload)
	default:
		return nil, &domain.OpError{
			Code:    domain.CodeUnsupported,
			Message: "unsupported daemon request",
			Retry:   domain.RetryAdvice{Kind: domain.RetryNever},
			Effect:  domain.EffectNone,
		}
	}
}

func (s *providerSession) Close() error {
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return nil
	}
	s.closed = true
	cursors := s.cursors
	s.cursors = make(map[cursorKey]providerapi.Provider)
	s.mu.Unlock()
	var result error
	for key, implementation := range cursors {
		if discarder, ok := implementation.(cursorDiscarder); ok {
			result = errors.Join(result, discarder.DiscardCursor(key.cursor))
		}
	}
	return result
}

func (s *providerSession) endpoints() ipc.ProviderEndpointsResponse {
	endpoints := make([]ipc.WireEndpoint, 0, len(s.providers))
	for _, implementation := range s.providers {
		endpoints = append(endpoints, ipc.EncodeEndpoint(implementation.Descriptor()))
	}
	sort.Slice(endpoints, func(left, right int) bool { return endpoints[left].ID < endpoints[right].ID })
	return ipc.ProviderEndpointsResponse{Endpoints: endpoints}
}

func (s *providerSession) snapshot(ctx context.Context, payload json.RawMessage) (any, error) {
	var request ipc.ProviderSnapshotRequest
	if err := decodePayload(payload, &request); err != nil {
		return nil, invalidArgument("decode provider snapshot request", err)
	}
	implementation, err := s.providerByString(request.EndpointID)
	if err != nil {
		return nil, err
	}
	snapshot, err := implementation.Snapshot(ctx)
	if err != nil {
		return nil, err
	}
	items := make([]ipc.WireCapability, len(snapshot.Capabilities.Items))
	for index, item := range snapshot.Capabilities.Items {
		constraints := append([]domain.CapabilityConstraint(nil), item.Constraints...)
		items[index] = ipc.WireCapability{Name: item.Name, Version: item.Version, Constraints: constraints}
	}
	return ipc.ProviderSnapshotResponse{
		EndpointID: string(snapshot.EndpointID),
		SessionID:  string(snapshot.SessionID),
		State:      snapshot.State,
		Generation: snapshot.Capabilities.Revision.Generation,
		Complete:   snapshot.Capabilities.Complete,
		Items:      items,
	}, nil
}

func (s *providerSession) normalize(ctx context.Context, payload json.RawMessage) (any, error) {
	var request ipc.ProviderNormalizeRequest
	if err := decodePayload(payload, &request); err != nil {
		return nil, invalidArgument("decode provider normalize request", err)
	}
	implementation, err := s.providerByString(request.EndpointID)
	if err != nil {
		return nil, err
	}
	input, err := request.Input.Decode()
	if err != nil {
		return nil, invalidArgument("decode normalize input", err)
	}
	domainRequest := domain.NormalizeRequest{EndpointID: implementation.Descriptor().ID, Input: string(input)}
	if request.Base != nil {
		base, err := ipc.DecodeLocation(*request.Base)
		if err != nil {
			return nil, invalidArgument("decode normalize base", err)
		}
		domainRequest.Base = &base
	}
	location, err := implementation.Normalize(ctx, domainRequest)
	if err != nil {
		return nil, err
	}
	return ipc.ProviderNormalizeResponse{Location: ipc.EncodeLocation(location)}, nil
}

func (s *providerSession) list(ctx context.Context, payload json.RawMessage) (any, error) {
	var request ipc.ProviderListRequest
	if err := decodePayload(payload, &request); err != nil {
		return nil, invalidArgument("decode provider list request", err)
	}
	location, err := ipc.DecodeLocation(request.Location)
	if err != nil {
		return nil, invalidArgument("decode list location", err)
	}
	implementation, err := s.provider(location.EndpointID)
	if err != nil {
		return nil, err
	}
	page, err := implementation.List(ctx, providerapi.ListRequest{
		Location: location,
		Cursor:   request.Cursor,
		Limit:    request.Limit,
		Sort:     request.Sort,
	})
	if err != nil {
		return nil, err
	}
	entries := make([]ipc.WireEntry, len(page.Entries))
	for index, entry := range page.Entries {
		entries[index] = ipc.EncodeEntry(entry)
	}
	s.mu.Lock()
	if request.Cursor != "" {
		delete(s.cursors, cursorKey{endpointID: location.EndpointID, cursor: request.Cursor})
	}
	if page.NextCursor != "" {
		s.cursors[cursorKey{endpointID: location.EndpointID, cursor: page.NextCursor}] = implementation
	}
	s.mu.Unlock()
	return ipc.ProviderListResponse{
		Entries:              entries,
		NextCursor:           page.NextCursor,
		Done:                 page.Done,
		RequestedSortApplied: page.RequestedSortApplied,
		Consistency:          page.Consistency,
		DirectoryFingerprint: ipc.EncodeFingerprint(page.DirectoryFingerprint),
	}, nil
}

func (s *providerSession) stat(ctx context.Context, payload json.RawMessage) (any, error) {
	var request ipc.ProviderStatRequest
	if err := decodePayload(payload, &request); err != nil {
		return nil, invalidArgument("decode provider stat request", err)
	}
	location, err := ipc.DecodeLocation(request.Location)
	if err != nil {
		return nil, invalidArgument("decode stat location", err)
	}
	implementation, err := s.provider(location.EndpointID)
	if err != nil {
		return nil, err
	}
	entry, err := implementation.Stat(ctx, providerapi.StatRequest{
		Location:       location,
		FollowSymlinks: request.FollowSymlinks,
	})
	if err != nil {
		return nil, err
	}
	return ipc.ProviderStatResponse{Entry: ipc.EncodeEntry(entry)}, nil
}

func (s *providerSession) read(ctx context.Context, payload json.RawMessage) (response any, resultErr error) {
	var request ipc.ProviderReadRequest
	if err := decodePayload(payload, &request); err != nil {
		return nil, invalidArgument("decode provider read request", err)
	}
	if request.Limit == 0 || request.Limit > s.maxReadBytes {
		return nil, invalidArgument("read limit is outside the allowed range", nil)
	}
	location, err := ipc.DecodeLocation(request.Location)
	if err != nil {
		return nil, invalidArgument("decode read location", err)
	}
	implementation, err := s.provider(location.EndpointID)
	if err != nil {
		return nil, err
	}
	limit := int64(request.Limit)
	providerRequest := providerapi.OpenReadRequest{Location: location, Offset: request.Offset, Limit: &limit}
	if request.ExpectedFingerprint != nil {
		expected := ipc.DecodeFingerprint(*request.ExpectedFingerprint)
		providerRequest.ExpectedFingerprint = &expected
	}
	handle, err := implementation.OpenRead(ctx, providerRequest)
	if err != nil {
		return nil, err
	}
	defer func() {
		if closeErr := handle.Close(context.Background()); resultErr == nil && closeErr != nil {
			resultErr = closeErr
			response = nil
		}
	}()
	data := make([]byte, request.Limit)
	total := 0
	eof := false
	for total < len(data) {
		n, readErr := handle.Read(ctx, data[total:])
		total += n
		if errors.Is(readErr, io.EOF) {
			eof = true
			break
		}
		if readErr != nil {
			return nil, readErr
		}
		if n == 0 {
			return nil, internalError("provider read made no progress", io.ErrNoProgress)
		}
	}
	info := handle.Info()
	return ipc.ProviderReadResponse{
		Info: ipc.ReadInfoWire{
			Entry:       ipc.EncodeEntry(info.Entry),
			Fingerprint: ipc.EncodeFingerprint(info.Fingerprint),
		},
		Data: ipc.EncodeWireBytes(data[:total]),
		EOF:  eof,
	}, nil
}

func (s *providerSession) providerByString(value string) (providerapi.Provider, error) {
	endpointID, err := domain.ParseEndpointID(value)
	if err != nil {
		return nil, invalidArgument("endpoint ID is invalid", err)
	}
	return s.provider(endpointID)
}

func (s *providerSession) provider(endpointID domain.EndpointID) (providerapi.Provider, error) {
	implementation := s.providers[endpointID]
	if implementation == nil {
		return nil, &domain.OpError{
			Code:       domain.CodeNotFound,
			Message:    "endpoint is not registered",
			EndpointID: endpointID,
			Retry:      domain.RetryAdvice{Kind: domain.RetryNever},
			Effect:     domain.EffectNone,
		}
	}
	return implementation, nil
}

func internalError(message string, cause error) error {
	return &domain.OpError{
		Code:    domain.CodeInternal,
		Message: message,
		Retry:   domain.RetryAdvice{Kind: domain.RetryNever},
		Effect:  domain.EffectUnknown,
		Cause:   cause,
	}
}
