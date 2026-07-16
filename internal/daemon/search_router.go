package daemon

import (
	"context"
	"encoding/json"

	"github.com/TyrantLucifer/awesome-mac-sftp/internal/domain"
	"github.com/TyrantLucifer/awesome-mac-sftp/internal/ipc"
	"github.com/TyrantLucifer/awesome-mac-sftp/internal/search"
)

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

	s.mu.Lock()
	_, duplicate := s.searches[identity.RequestID]
	_, contentDuplicate := s.contentSearches[identity.RequestID]
	duplicate = duplicate || contentDuplicate
	full := len(s.searches)+len(s.contentSearches) >= maxActiveFilenameSearches
	closed := s.closed
	s.mu.Unlock()
	if closed {
		return nil, internalError("provider session is closed", nil)
	}
	if duplicate {
		return nil, searchCursorError(domain.CodeConflict, "search request ID is already active")
	}
	if full {
		return nil, searchCursorError(domain.CodeResourceExhausted, "active search limit reached")
	}

	searchCtx, cancel := context.WithCancel(context.Background())
	events, err := search.StartFilename(searchCtx, implementation, search.Request{Identity: identity})
	if err != nil {
		cancel()
		return nil, err
	}
	cursor := &filenameSearchCursor{events: events, cancel: cancel}
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		cancelAndDrainSearch(cursor)
		return nil, internalError("provider session is closed", nil)
	}
	_, contentDuplicate = s.contentSearches[identity.RequestID]
	if _, duplicate = s.searches[identity.RequestID]; duplicate || contentDuplicate || len(s.searches)+len(s.contentSearches) >= maxActiveFilenameSearches {
		s.mu.Unlock()
		cancelAndDrainSearch(cursor)
		if duplicate {
			return nil, searchCursorError(domain.CodeConflict, "search request ID is already active")
		}
		return nil, searchCursorError(domain.CodeResourceExhausted, "active search limit reached")
	}
	s.searches[identity.RequestID] = cursor
	s.mu.Unlock()
	return ipc.SearchFilenameStartResponse{RequestID: string(identity.RequestID)}, nil
}

func (s *providerSession) nextFilenameSearch(ctx context.Context, payload json.RawMessage) (any, error) {
	var request ipc.SearchFilenameNextRequest
	if err := decodePayload(payload, &request); err != nil {
		return nil, invalidArgument("decode filename search next request", err)
	}
	requestID, err := domain.ParseRequestID(request.RequestID)
	if err != nil {
		return nil, invalidArgument("validate filename search request ID", err)
	}
	if request.Limit == 0 || request.Limit > maxFilenameEventsPerPage {
		return nil, invalidArgument("validate filename search page limit", nil)
	}
	s.mu.Lock()
	cursor := s.searches[requestID]
	s.mu.Unlock()
	if cursor == nil {
		return nil, searchCursorError(domain.CodeNotFound, "search request is not active")
	}

	cursor.mu.Lock()
	defer cursor.mu.Unlock()
	response := ipc.SearchFilenameNextResponse{Events: make([]ipc.SearchEventWire, 0, request.Limit)}
	first, ok, err := receiveSearchEvent(ctx, cursor.events)
	if err != nil {
		return nil, err
	}
	if !ok {
		response.Done = true
	} else {
		response.Events = append(response.Events, ipc.EncodeSearchEvent(first))
		response.Done = first.Kind == search.EventTerminal
	}
	for !response.Done && len(response.Events) < int(request.Limit) {
		select {
		case event, open := <-cursor.events:
			if !open {
				response.Done = true
				break
			}
			response.Events = append(response.Events, ipc.EncodeSearchEvent(event))
			response.Done = event.Kind == search.EventTerminal
		default:
			return response, nil
		}
	}
	if response.Done {
		s.mu.Lock()
		if s.searches[requestID] == cursor {
			delete(s.searches, requestID)
		}
		s.mu.Unlock()
		cursor.cancel()
	}
	return response, nil
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
	contentCursor := s.contentSearches[requestID]
	s.mu.Unlock()
	if cursor == nil && contentCursor == nil {
		return nil, searchCursorError(domain.CodeNotFound, "search request is not active")
	}
	if cursor != nil {
		cursor.cancel()
	} else {
		contentCursor.cancel()
	}
	return ipc.SearchCancelResponse{}, nil
}

func receiveSearchEvent(ctx context.Context, events <-chan search.Event) (search.Event, bool, error) {
	select {
	case event, ok := <-events:
		return event, ok, nil
	case <-ctx.Done():
		return search.Event{}, false, domain.FromContext("search_next", "", nil, ctx.Err())
	}
}

func cancelAndDrainSearch(cursor *filenameSearchCursor) {
	cursor.cancel()
	for range cursor.events {
	}
}

func searchCursorError(code domain.Code, message string) error {
	return &domain.OpError{
		Code:    code,
		Message: message,
		Retry:   domain.RetryAdvice{Kind: domain.RetryNever},
		Effect:  domain.EffectNone,
	}
}
