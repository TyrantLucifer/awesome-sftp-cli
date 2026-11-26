package daemon

import (
	"context"
	"encoding/json"
	"sync"

	"github.com/TyrantLucifer/awesome-sftp-cli/internal/domain"
	"github.com/TyrantLucifer/awesome-sftp-cli/internal/ipc"
	"github.com/TyrantLucifer/awesome-sftp-cli/internal/search"
)

type activeSearch interface {
	cancelSearch()
	closeAndDrain()
}

type searchCursor[E any] struct {
	mu     sync.Mutex
	events <-chan E
	cancel context.CancelFunc
}

func (c *searchCursor[E]) cancelSearch() {
	c.cancel()
}

func (c *searchCursor[E]) closeAndDrain() {
	c.cancel()
	c.mu.Lock()
	for range c.events {
	}
	c.mu.Unlock()
}

func (s *providerSession) checkSearchAdmissionLocked(requestID domain.RequestID) error {
	if s.closed {
		return internalError("provider session is closed", nil)
	}
	if _, duplicate := s.searches[requestID]; duplicate {
		return searchCursorError(domain.CodeConflict, "search request ID is already active")
	}
	if len(s.searches) >= maxActiveFilenameSearches {
		return searchCursorError(domain.CodeResourceExhausted, "active search limit reached")
	}
	return nil
}

func startSearchCursor[E any](
	s *providerSession,
	requestID domain.RequestID,
	start func(context.Context) (<-chan E, error),
) error {
	s.mu.Lock()
	admissionErr := s.checkSearchAdmissionLocked(requestID)
	s.mu.Unlock()
	if admissionErr != nil {
		return admissionErr
	}

	searchCtx, cancel := context.WithCancel(context.Background())
	events, err := start(searchCtx)
	if err != nil {
		cancel()
		return err
	}
	cursor := &searchCursor[E]{events: events, cancel: cancel}

	s.mu.Lock()
	if err := s.checkSearchAdmissionLocked(requestID); err != nil {
		s.mu.Unlock()
		cursor.closeAndDrain()
		return err
	}
	s.searches[requestID] = cursor
	s.mu.Unlock()
	return nil
}

func nextSearchPage[E any, W any](
	ctx context.Context,
	s *providerSession,
	rawRequestID string,
	limit uint32,
	label, notFoundMessage, opName string,
	isTerminal func(E) bool,
	encode func(E) W,
) ([]W, bool, error) {
	requestID, err := domain.ParseRequestID(rawRequestID)
	if err != nil {
		return nil, false, invalidArgument("validate "+label+" search request ID", err)
	}
	if limit == 0 || limit > maxFilenameEventsPerPage {
		return nil, false, invalidArgument("validate "+label+" search page limit", nil)
	}
	s.mu.Lock()
	cursor, ok := s.searches[requestID].(*searchCursor[E])
	s.mu.Unlock()
	if !ok || cursor == nil {
		return nil, false, searchCursorError(domain.CodeNotFound, notFoundMessage)
	}

	cursor.mu.Lock()
	defer cursor.mu.Unlock()
	wireEvents := make([]W, 0, limit)
	done := false

	select {
	case event, open := <-cursor.events:
		if !open {
			done = true
		} else {
			wireEvents = append(wireEvents, encode(event))
			done = isTerminal(event)
		}
	case <-ctx.Done():
		return nil, false, domain.FromContext(opName, "", nil, ctx.Err())
	}

	for !done && len(wireEvents) < int(limit) {
		select {
		case event, open := <-cursor.events:
			if !open {
				done = true
				break
			}
			wireEvents = append(wireEvents, encode(event))
			done = isTerminal(event)
		default:
			return wireEvents, false, nil
		}
	}
	if done {
		s.mu.Lock()
		if s.searches[requestID] == cursor {
			delete(s.searches, requestID)
		}
		s.mu.Unlock()
		cursor.cancel()
	}
	return wireEvents, done, nil
}

func (s *providerSession) startFilenameSearch(payload json.RawMessage) (any, error) {
	var request ipc.SearchFilenameStartRequest
	if err := decodePayload(payload, &request); err != nil {
		return nil, invalidArgument("decode filename search request", err)
	}
	identity, err := ipc.DecodeSearchIdentity(request.Identity)
	if err != nil {
		return nil, invalidArgument("decode filename search identity", err)
	}
	implementation, err := s.provider(identity.EndpointID)
	if err != nil {
		return nil, err
	}
	err = startSearchCursor(s, identity.RequestID, func(ctx context.Context) (<-chan search.Event, error) {
		return s.startFilenameEvents(ctx, implementation, identity)
	})
	if err != nil {
		return nil, err
	}
	return ipc.SearchFilenameStartResponse{RequestID: string(identity.RequestID)}, nil
}

func (s *providerSession) nextFilenameSearch(ctx context.Context, payload json.RawMessage) (any, error) {
	var request ipc.SearchFilenameNextRequest
	if err := decodePayload(payload, &request); err != nil {
		return nil, invalidArgument("decode filename search next request", err)
	}
	events, done, err := nextSearchPage(
		ctx, s, request.RequestID, request.Limit,
		"filename", "search request is not active", "search_next",
		func(event search.Event) bool { return event.Kind == search.EventTerminal },
		ipc.EncodeSearchEvent,
	)
	if err != nil {
		return nil, err
	}
	return ipc.SearchFilenameNextResponse{Events: events, Done: done}, nil
}

func (s *providerSession) cancelSearch(payload json.RawMessage) (any, error) {
	var request ipc.SearchCancelRequest
	if err := decodePayload(payload, &request); err != nil {
		return nil, invalidArgument("decode search cancel request", err)
	}
	requestID, err := domain.ParseRequestID(request.RequestID)
	if err != nil {
		return nil, invalidArgument("validate search cancel request ID", err)
	}
	s.mu.Lock()
	cursor := s.searches[requestID]
	s.mu.Unlock()
	if cursor == nil {
		return nil, searchCursorError(domain.CodeNotFound, "search request is not active")
	}
	cursor.cancelSearch()
	return ipc.SearchCancelResponse{}, nil
}

func searchCursorError(code domain.Code, message string) error {
	return &domain.OpError{
		Code:    code,
		Message: message,
		Retry:   domain.RetryAdvice{Kind: domain.RetryNever},
		Effect:  domain.EffectNone,
	}
}
