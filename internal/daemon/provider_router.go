package daemon

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"sort"
	"sync"
	"sync/atomic"

	"github.com/TyrantLucifer/awesome-mac-sftp/internal/auth"
	"github.com/TyrantLucifer/awesome-mac-sftp/internal/cachemanager"
	"github.com/TyrantLucifer/awesome-mac-sftp/internal/domain"
	"github.com/TyrantLucifer/awesome-mac-sftp/internal/ipc"
	providerapi "github.com/TyrantLucifer/awesome-mac-sftp/internal/provider"
	"github.com/TyrantLucifer/awesome-mac-sftp/internal/transfer"
	"github.com/TyrantLucifer/awesome-mac-sftp/internal/workspace"
)

const (
	ProviderEndpoints  = "provider.endpoints"
	ProviderSnapshot   = "provider.snapshot"
	ProviderNormalize  = "provider.normalize"
	ProviderList       = "provider.list"
	ProviderStat       = "provider.stat"
	ProviderRead       = "provider.read"
	ProviderConnectSSH = "provider.connect_ssh"
	ProviderRelease    = "provider.release"
	AuthPrompt         = "auth.prompt"
	AuthClaim          = "auth.claim"
	AuthResolve        = "auth.resolve"
	WorkspaceList      = "workspace.list"
	WorkspaceLoad      = "workspace.load"
	WorkspaceSave      = "workspace.save"
	DiagnosticList     = "diagnostic.list"
)

type SSHConnector func(context.Context, string) (providerapi.Provider, error)
type EndpointConnector func(context.Context, domain.Endpoint) (providerapi.Provider, error)

type ProviderSessions struct {
	mu              sync.Mutex
	providers       map[domain.EndpointID]providerapi.Provider
	endpoints       map[domain.EndpointID]*endpointLease
	maxReadBytes    uint32
	connectSSH      SSHConnector
	connectEndpoint EndpointConnector
	authBroker      *auth.Broker
	workspace       *workspace.Store
	transfer        TransferService
	diagnostics     DiagnosticSource
	cache           *cachemanager.Manager
	nextOwner       atomic.Uint64
}

type endpointLease struct {
	implementation providerapi.Provider
	permanent      bool
	sessions       int
	jobs           int
}

func (s *ProviderSessions) SetSSHConnector(connector SSHConnector) { s.connectSSH = connector }
func (s *ProviderSessions) SetEndpointConnector(connector EndpointConnector) {
	s.connectEndpoint = connector
}
func (s *ProviderSessions) SetAuthBroker(broker *auth.Broker)             { s.authBroker = broker }
func (s *ProviderSessions) SetWorkspaceStore(store *workspace.Store)      { s.workspace = store }
func (s *ProviderSessions) SetTransferService(service TransferService)    { s.transfer = service }
func (s *ProviderSessions) SetDiagnosticSource(source DiagnosticSource)   { s.diagnostics = source }
func (s *ProviderSessions) SetCacheManager(manager *cachemanager.Manager) { s.cache = manager }

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
	endpoints := make(map[domain.EndpointID]*endpointLease, len(indexed))
	for id, implementation := range indexed {
		endpoints[id] = &endpointLease{implementation: implementation, permanent: true}
	}
	return &ProviderSessions{providers: indexed, endpoints: endpoints, maxReadBytes: maxReadBytes}, nil
}

func (s *ProviderSessions) NewSession() Session {
	providers := make(map[domain.EndpointID]providerapi.Provider, len(s.providers))
	for id, implementation := range s.providers {
		providers[id] = implementation
	}
	session := &providerSession{
		owner:        s,
		providers:    providers,
		maxReadBytes: s.maxReadBytes,
		connectSSH:   s.connectSSH,
		owned:        make([]providerapi.Provider, 0),
		cursors:      make(map[cursorKey]providerapi.Provider),
		workspace:    s.workspace,
		transfer:     s.transfer,
		diagnostics:  s.diagnostics,
		cache:        s.cache,
	}
	if s.authBroker != nil {
		session.authBroker = s.authBroker
		session.authOwner = auth.OwnerID(s.nextOwner.Add(1))
	}
	return session
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

	owner        *ProviderSessions
	providers    map[domain.EndpointID]providerapi.Provider
	maxReadBytes uint32
	cursors      map[cursorKey]providerapi.Provider
	closed       bool
	connectSSH   SSHConnector
	owned        []providerapi.Provider
	authBroker   *auth.Broker
	authOwner    auth.OwnerID
	workspace    *workspace.Store
	transfer     TransferService
	diagnostics  DiagnosticSource
	cache        *cachemanager.Manager
}

func (s *providerSession) Handle(ctx context.Context, name string, payload json.RawMessage) (any, error) {
	s.mu.Lock()
	closed := s.closed
	s.mu.Unlock()
	if closed {
		return nil, internalError("provider session is closed", nil)
	}
	switch name {
	case AuthPrompt:
		return s.authPrompt(ctx, payload)
	case AuthClaim:
		return s.authClaim(ctx, payload)
	case AuthResolve:
		return s.authResolve(payload)
	case WorkspaceList:
		return s.listWorkspaces(payload)
	case WorkspaceLoad:
		return s.loadWorkspace(payload)
	case WorkspaceSave:
		return s.saveWorkspace(payload)
	case DiagnosticList:
		return s.listDiagnostics(payload)
	case JobCapture, JobCaptureDelete, JobCreateCopy, JobCreateDelete, JobList, JobEvents, JobPause, JobResume, JobCancel, JobResolveConflict:
		return s.handleJob(ctx, name, payload)
	case ProviderConnectSSH:
		return s.connect(ctx, payload)
	case ProviderRelease:
		return s.release(payload)
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

func (s *providerSession) listWorkspaces(payload json.RawMessage) (any, error) {
	if s.workspace == nil {
		return nil, unsupportedWorkspace()
	}
	var request workspace.ListRequest
	if err := decodePayload(payload, &request); err != nil {
		return nil, invalidArgument("decode workspace list request", err)
	}
	summaries, err := s.workspace.List()
	if err != nil {
		return nil, internalError("list workspaces", err)
	}
	return workspace.ListResponse{Workspaces: summaries}, nil
}

func (s *providerSession) loadWorkspace(payload json.RawMessage) (any, error) {
	if s.workspace == nil {
		return nil, unsupportedWorkspace()
	}
	var request workspace.LoadRequest
	if err := decodePayload(payload, &request); err != nil {
		return nil, invalidArgument("decode workspace load request", err)
	}
	if err := workspace.ValidateName(request.Name); err != nil {
		return nil, invalidArgument("validate workspace name", err)
	}
	document, err := s.workspace.Load(request.Name)
	if err != nil {
		code := domain.CodeIntegrityFailed
		if errors.Is(err, os.ErrNotExist) {
			code = domain.CodeNotFound
		}
		return nil, &domain.OpError{Code: code, Message: "load workspace failed", Operation: "workspace.load", Retry: domain.RetryAdvice{Kind: domain.RetryNever}, Effect: domain.EffectNone, Cause: err}
	}
	return workspace.LoadResponse{Document: document}, nil
}

func (s *providerSession) saveWorkspace(payload json.RawMessage) (any, error) {
	if s.workspace == nil {
		return nil, unsupportedWorkspace()
	}
	var request workspace.SaveRequest
	if err := decodePayload(payload, &request); err != nil {
		return nil, invalidArgument("decode workspace save request", err)
	}
	if err := workspace.ValidateName(request.Name); err != nil {
		return nil, invalidArgument("validate workspace name", err)
	}
	if err := request.Document.Validate(); err != nil {
		return nil, invalidArgument("validate workspace document", err)
	}
	if err := s.workspace.Save(request.Name, request.Document); err != nil {
		return nil, internalError("save workspace", err)
	}
	return workspace.SaveResponse{}, nil
}

func unsupportedWorkspace() error {
	return &domain.OpError{Code: domain.CodeUnsupported, Message: "workspace store is unavailable", Retry: domain.RetryAdvice{Kind: domain.RetryNever}, Effect: domain.EffectNone}
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
	owned := s.owned
	s.owned = nil
	s.mu.Unlock()
	if s.authBroker != nil {
		s.authBroker.Detach(s.authOwner)
	}
	var result error
	for key, implementation := range cursors {
		if discarder, ok := implementation.(cursorDiscarder); ok {
			result = errors.Join(result, discarder.DiscardCursor(key.cursor))
		}
	}
	for _, implementation := range owned {
		result = errors.Join(result, s.owner.releaseSession(implementation.Descriptor().ID))
	}
	return result
}

func (s *providerSession) authPrompt(ctx context.Context, payload json.RawMessage) (any, error) {
	if s.authBroker == nil {
		return nil, unsupportedAuth()
	}
	var request ipc.AuthPromptRequest
	if err := decodePayload(payload, &request); err != nil {
		return nil, invalidArgument("decode authentication prompt", err)
	}
	if err := ipc.ValidateAuthPromptRequest(request); err != nil {
		return nil, invalidArgument("validate authentication prompt", err)
	}
	answer, err := s.authBroker.Prompt(ctx, auth.Token(request.AttemptToken), request.Prompt, auth.PromptKind(request.Kind))
	if err != nil {
		return nil, mapAuthError(err)
	}
	defer clear(answer)
	return ipc.AuthPromptResponse{Answer: string(answer)}, nil
}

func (s *providerSession) authClaim(ctx context.Context, payload json.RawMessage) (any, error) {
	if s.authBroker == nil {
		return nil, unsupportedAuth()
	}
	var request ipc.AuthClaimRequest
	if err := decodePayload(payload, &request); err != nil {
		return nil, invalidArgument("decode authentication claim", err)
	}
	challenge, err := s.authBroker.Claim(ctx, s.authOwner)
	if err != nil {
		return nil, mapAuthError(err)
	}
	return ipc.AuthClaimResponse{ChallengeID: string(challenge.ID), Endpoint: challenge.Endpoint, Prompt: challenge.Prompt, Kind: string(challenge.Kind), ExpiresAt: challenge.ExpiresAt}, nil
}

func (s *providerSession) authResolve(payload json.RawMessage) (any, error) {
	if s.authBroker == nil {
		return nil, unsupportedAuth()
	}
	var request ipc.AuthResolveRequest
	if err := decodePayload(payload, &request); err != nil {
		return nil, invalidArgument("decode authentication response", err)
	}
	if err := ipc.ValidateAuthResolveRequest(request); err != nil {
		return nil, invalidArgument("validate authentication response", err)
	}
	answer := []byte(request.Answer)
	defer clear(answer)
	err := s.authBroker.Resolve(s.authOwner, auth.ChallengeID(request.ChallengeID), auth.Resolution{Answer: answer, Cancel: request.Action == ipc.AuthActionCancel})
	if err != nil {
		return nil, mapAuthError(err)
	}
	return ipc.AuthResolveResponse{}, nil
}

func mapAuthError(err error) error {
	code := domain.CodeInternal
	retry := domain.RetryAdvice{Kind: domain.RetryNever}
	switch {
	case errors.Is(err, context.Canceled):
		code = domain.CodeCanceled
	case errors.Is(err, context.DeadlineExceeded):
		code = domain.CodeTimeout
	case errors.Is(err, auth.ErrNotOwner), errors.Is(err, auth.ErrAttemptNotFound):
		code = domain.CodePermissionDenied
	case errors.Is(err, auth.ErrChallengeNotFound):
		code = domain.CodeInvalidArgument
	case errors.Is(err, auth.ErrPromptLimit):
		code = domain.CodeResourceExhausted
	}
	return &domain.OpError{Code: code, Message: "authentication request failed", Retry: retry, Effect: domain.EffectNone, Cause: err}
}

func unsupportedAuth() error {
	return &domain.OpError{Code: domain.CodeUnsupported, Message: "authentication broker is unavailable", Retry: domain.RetryAdvice{Kind: domain.RetryNever}, Effect: domain.EffectNone}
}

func (s *providerSession) connect(ctx context.Context, payload json.RawMessage) (any, error) {
	if s.connectSSH == nil {
		return nil, &domain.OpError{Code: domain.CodeUnsupported, Message: "SSH connector is unavailable", Retry: domain.RetryAdvice{Kind: domain.RetryNever}, Effect: domain.EffectNone}
	}
	var request ipc.ProviderConnectSSHRequest
	if err := decodePayload(payload, &request); err != nil {
		return nil, invalidArgument("decode SSH connect request", err)
	}
	implementation, err := s.connectSSH(ctx, request.HostAlias)
	if err != nil {
		return nil, err
	}
	descriptor := implementation.Descriptor()
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		if closer, ok := implementation.(interface{ Close() error }); ok {
			_ = closer.Close()
		}
		return nil, &domain.OpError{Code: domain.CodeCanceled, Message: "SSH connection completed after client session closed", Retry: domain.RetryAdvice{Kind: domain.RetryNever}, Effect: domain.EffectNone}
	}
	if _, exists := s.providers[descriptor.ID]; exists {
		s.mu.Unlock()
		if closer, ok := implementation.(interface{ Close() error }); ok {
			_ = closer.Close()
		}
		return nil, invalidArgument("duplicate SSH endpoint", nil)
	}
	if err := s.owner.attachSession(implementation); err != nil {
		s.mu.Unlock()
		if closer, ok := implementation.(interface{ Close() error }); ok {
			_ = closer.Close()
		}
		return nil, err
	}
	s.providers[descriptor.ID] = implementation
	s.owned = append(s.owned, implementation)
	s.mu.Unlock()
	return ipc.ProviderConnectSSHResponse{Endpoint: ipc.EncodeEndpoint(descriptor)}, nil
}

func (s *providerSession) endpoints() ipc.ProviderEndpointsResponse {
	s.mu.Lock()
	defer s.mu.Unlock()
	endpoints := make([]ipc.WireEndpoint, 0, len(s.providers))
	for _, implementation := range s.providers {
		endpoints = append(endpoints, ipc.EncodeEndpoint(implementation.Descriptor()))
	}
	sort.Slice(endpoints, func(left, right int) bool { return endpoints[left].ID < endpoints[right].ID })
	return ipc.ProviderEndpointsResponse{Endpoints: endpoints}
}

func (s *providerSession) release(payload json.RawMessage) (any, error) {
	var request ipc.ProviderReleaseRequest
	if err := decodePayload(payload, &request); err != nil {
		return nil, invalidArgument("decode provider release request", err)
	}
	endpointID, err := domain.ParseEndpointID(request.EndpointID)
	if err != nil {
		return nil, invalidArgument("release endpoint ID is invalid", err)
	}
	s.mu.Lock()
	implementation := s.providers[endpointID]
	ownedIndex := -1
	for index, candidate := range s.owned {
		if candidate.Descriptor().ID == endpointID {
			ownedIndex = index
			break
		}
	}
	if implementation == nil {
		s.mu.Unlock()
		return nil, &domain.OpError{Code: domain.CodeNotFound, Message: "endpoint is not registered", EndpointID: endpointID, Retry: domain.RetryAdvice{Kind: domain.RetryNever}, Effect: domain.EffectNone}
	}
	if ownedIndex < 0 {
		s.mu.Unlock()
		return nil, &domain.OpError{Code: domain.CodeUnsupported, Message: "base endpoint cannot be released", EndpointID: endpointID, Retry: domain.RetryAdvice{Kind: domain.RetryNever}, Effect: domain.EffectNone}
	}
	delete(s.providers, endpointID)
	s.owned = append(s.owned[:ownedIndex], s.owned[ownedIndex+1:]...)
	var cursors []providerapi.PageCursor
	for key := range s.cursors {
		if key.endpointID == endpointID {
			delete(s.cursors, key)
			cursors = append(cursors, key.cursor)
		}
	}
	s.mu.Unlock()

	var result error
	if discarder, ok := implementation.(cursorDiscarder); ok {
		for _, cursor := range cursors {
			result = errors.Join(result, discarder.DiscardCursor(cursor))
		}
	}
	result = errors.Join(result, s.owner.releaseSession(endpointID))
	if result != nil {
		return nil, internalError("release SSH endpoint", result)
	}
	return ipc.ProviderReleaseResponse{}, nil
}

func (s *ProviderSessions) Resolve(endpointID domain.EndpointID) (providerapi.Provider, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	entry := s.endpoints[endpointID]
	if entry == nil {
		return nil, &domain.OpError{Code: domain.CodeNotFound, Message: "endpoint is not registered", EndpointID: endpointID, Retry: domain.RetryAdvice{Kind: domain.RetryAfterReconnect}, Effect: domain.EffectNone}
	}
	return entry.implementation, nil
}

// Acquire retains both frozen endpoints until the returned release function is
// called. Each endpoint is counted once when source and destination coincide.
func (s *ProviderSessions) Acquire(ctx context.Context, plan transfer.Plan) (func(), error) {
	descriptors := []domain.Endpoint{plan.SourceEndpoint}
	if plan.DestinationEndpoint.ID != plan.SourceEndpoint.ID {
		descriptors = append(descriptors, plan.DestinationEndpoint)
	}
	for _, descriptor := range descriptors {
		if err := s.ensureEndpoint(ctx, descriptor); err != nil {
			return nil, err
		}
	}
	s.mu.Lock()
	for _, descriptor := range descriptors {
		entry := s.endpoints[descriptor.ID]
		if entry == nil || entry.implementation.Descriptor() != descriptor {
			s.mu.Unlock()
			return nil, &domain.OpError{Code: domain.CodeNotFound, Message: "frozen endpoint is not attached", EndpointID: descriptor.ID, Retry: domain.RetryAdvice{Kind: domain.RetryAfterReconnect}, Effect: domain.EffectNone}
		}
	}
	for _, descriptor := range descriptors {
		s.endpoints[descriptor.ID].jobs++
	}
	s.mu.Unlock()
	var once sync.Once
	return func() {
		once.Do(func() {
			for _, descriptor := range descriptors {
				_ = s.releaseJob(descriptor.ID)
			}
		})
	}, nil
}

func (s *ProviderSessions) ensureEndpoint(ctx context.Context, descriptor domain.Endpoint) error {
	s.mu.Lock()
	entry := s.endpoints[descriptor.ID]
	connector := s.connectEndpoint
	s.mu.Unlock()
	if entry != nil {
		if entry.implementation.Descriptor() != descriptor {
			return &domain.OpError{Code: domain.CodeConflict, Message: "endpoint ID has different frozen identity", EndpointID: descriptor.ID, Retry: domain.RetryAdvice{Kind: domain.RetryAfterConflict}, Effect: domain.EffectNone}
		}
		return nil
	}
	if connector == nil {
		return &domain.OpError{Code: domain.CodeNotFound, Message: "frozen endpoint is not attached", EndpointID: descriptor.ID, Retry: domain.RetryAdvice{Kind: domain.RetryAfterReconnect}, Effect: domain.EffectNone}
	}
	implementation, err := connector(ctx, descriptor)
	if err != nil {
		return err
	}
	if implementation == nil || implementation.Descriptor() != descriptor {
		if closer, ok := implementation.(interface{ Close() error }); ok {
			_ = closer.Close()
		}
		return &domain.OpError{Code: domain.CodeIntegrityFailed, Message: "endpoint connector returned different identity", EndpointID: descriptor.ID, Retry: domain.RetryAdvice{Kind: domain.RetryNever}, Effect: domain.EffectNone}
	}
	s.mu.Lock()
	if current := s.endpoints[descriptor.ID]; current != nil {
		s.mu.Unlock()
		if closer, ok := implementation.(interface{ Close() error }); ok {
			_ = closer.Close()
		}
		if current.implementation.Descriptor() != descriptor {
			return &domain.OpError{Code: domain.CodeConflict, Message: "endpoint changed during reconnect", EndpointID: descriptor.ID, Retry: domain.RetryAdvice{Kind: domain.RetryAfterConflict}, Effect: domain.EffectNone}
		}
		return nil
	}
	s.endpoints[descriptor.ID] = &endpointLease{implementation: implementation}
	s.mu.Unlock()
	return nil
}

func (s *ProviderSessions) attachSession(implementation providerapi.Provider) error {
	descriptor := implementation.Descriptor()
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, exists := s.endpoints[descriptor.ID]; exists {
		return invalidArgument("duplicate SSH endpoint", nil)
	}
	s.endpoints[descriptor.ID] = &endpointLease{implementation: implementation, sessions: 1}
	return nil
}

func (s *ProviderSessions) releaseSession(endpointID domain.EndpointID) error {
	return s.release(endpointID, true)
}

func (s *ProviderSessions) releaseJob(endpointID domain.EndpointID) error {
	return s.release(endpointID, false)
}

func (s *ProviderSessions) release(endpointID domain.EndpointID, session bool) error {
	s.mu.Lock()
	entry := s.endpoints[endpointID]
	if entry == nil || entry.permanent {
		s.mu.Unlock()
		return nil
	}
	if session {
		if entry.sessions > 0 {
			entry.sessions--
		}
	} else if entry.jobs > 0 {
		entry.jobs--
	}
	if entry.sessions > 0 || entry.jobs > 0 {
		s.mu.Unlock()
		return nil
	}
	delete(s.endpoints, endpointID)
	implementation := entry.implementation
	s.mu.Unlock()
	if closer, ok := implementation.(interface{ Close() error }); ok {
		return closer.Close()
	}
	return nil
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
	s.mu.Lock()
	defer s.mu.Unlock()
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
