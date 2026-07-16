package daemon

import (
	"context"
	"encoding/json"

	"github.com/TyrantLucifer/awesome-mac-sftp/internal/domain"
	"github.com/TyrantLucifer/awesome-mac-sftp/internal/ipc"
	"github.com/TyrantLucifer/awesome-mac-sftp/internal/search"
)

func (s *providerSession) startContentSearch(payload json.RawMessage) (any, error) {
	var request ipc.SearchContentStartRequest
	if err := decodePayload(payload, &request); err != nil {
		return nil, invalidArgument("decode content search request", err)
	}
	identity, err := ipc.DecodeContentSearchIdentity(request.Identity)
	if err != nil {
		return nil, invalidArgument("decode content search identity", err)
	}
	implementation, err := s.provider(identity.EndpointID)
	if err != nil {
		return nil, err
	}
	s.mu.Lock()
	_, filenameDuplicate := s.searches[identity.RequestID]
	_, duplicate := s.contentSearches[identity.RequestID]
	full := len(s.searches)+len(s.contentSearches) >= maxActiveFilenameSearches
	closed := s.closed
	s.mu.Unlock()
	if closed {
		return nil, internalError("provider session is closed", nil)
	}
	if duplicate || filenameDuplicate {
		return nil, searchCursorError(domain.CodeConflict, "search request ID is already active")
	}
	if full {
		return nil, searchCursorError(domain.CodeResourceExhausted, "active search limit reached")
	}
	searchCtx, cancel := context.WithCancel(context.Background())
	events, err := search.StartContent(searchCtx, implementation, search.ContentRequest{Identity: identity})
	if err != nil {
		cancel()
		return nil, err
	}
	cursor := &contentSearchCursor{events: events, cancel: cancel}
	s.mu.Lock()
	_, filenameDuplicate = s.searches[identity.RequestID]
	_, duplicate = s.contentSearches[identity.RequestID]
	if s.closed || duplicate || filenameDuplicate || len(s.searches)+len(s.contentSearches) >= maxActiveFilenameSearches {
		closed = s.closed
		s.mu.Unlock()
		cancelAndDrainContentSearch(cursor)
		if closed {
			return nil, internalError("provider session is closed", nil)
		}
		if duplicate || filenameDuplicate {
			return nil, searchCursorError(domain.CodeConflict, "search request ID is already active")
		}
		return nil, searchCursorError(domain.CodeResourceExhausted, "active search limit reached")
	}
	s.contentSearches[identity.RequestID] = cursor
	s.mu.Unlock()
	return ipc.SearchContentStartResponse{RequestID: string(identity.RequestID)}, nil
}

func (s *providerSession) nextContentSearch(ctx context.Context, payload json.RawMessage) (any, error) {
	var request ipc.SearchContentNextRequest
	if err := decodePayload(payload, &request); err != nil {
		return nil, invalidArgument("decode content search next request", err)
	}
	requestID, err := domain.ParseRequestID(request.RequestID)
	if err != nil {
		return nil, invalidArgument("validate content search request ID", err)
	}
	if request.Limit == 0 || request.Limit > maxFilenameEventsPerPage {
		return nil, invalidArgument("validate content search page limit", nil)
	}
	s.mu.Lock()
	cursor := s.contentSearches[requestID]
	s.mu.Unlock()
	if cursor == nil {
		return nil, searchCursorError(domain.CodeNotFound, "content search request is not active")
	}
	cursor.mu.Lock()
	defer cursor.mu.Unlock()
	response := ipc.SearchContentNextResponse{Events: make([]ipc.ContentSearchEventWire, 0, request.Limit)}
	first, ok, err := receiveContentSearchEvent(ctx, cursor.events)
	if err != nil {
		return nil, err
	}
	if !ok {
		response.Done = true
	} else {
		response.Events = append(response.Events, ipc.EncodeContentSearchEvent(first))
		response.Done = first.Kind == search.ContentEventTerminal
	}
	for !response.Done && len(response.Events) < int(request.Limit) {
		select {
		case event, open := <-cursor.events:
			if !open {
				response.Done = true
				break
			}
			response.Events = append(response.Events, ipc.EncodeContentSearchEvent(event))
			response.Done = event.Kind == search.ContentEventTerminal
		default:
			return response, nil
		}
	}
	if response.Done {
		s.mu.Lock()
		if s.contentSearches[requestID] == cursor {
			delete(s.contentSearches, requestID)
		}
		s.mu.Unlock()
		cursor.cancel()
	}
	return response, nil
}

func receiveContentSearchEvent(ctx context.Context, events <-chan search.ContentEvent) (search.ContentEvent, bool, error) {
	select {
	case event, ok := <-events:
		return event, ok, nil
	case <-ctx.Done():
		return search.ContentEvent{}, false, domain.FromContext("content_search_next", "", nil, ctx.Err())
	}
}

func cancelAndDrainContentSearch(cursor *contentSearchCursor) {
	cursor.cancel()
	for range cursor.events {
	}
}
