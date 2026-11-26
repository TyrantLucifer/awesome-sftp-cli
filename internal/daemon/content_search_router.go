package daemon

import (
	"context"
	"encoding/json"

	"github.com/TyrantLucifer/awesome-sftp-cli/internal/ipc"
	"github.com/TyrantLucifer/awesome-sftp-cli/internal/search"
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
	err = startSearchCursor(s, identity.RequestID, func(ctx context.Context) (<-chan search.ContentEvent, error) {
		return s.startContentEvents(ctx, implementation, identity)
	})
	if err != nil {
		return nil, err
	}
	return ipc.SearchContentStartResponse{RequestID: string(identity.RequestID)}, nil
}

func (s *providerSession) nextContentSearch(ctx context.Context, payload json.RawMessage) (any, error) {
	var request ipc.SearchContentNextRequest
	if err := decodePayload(payload, &request); err != nil {
		return nil, invalidArgument("decode content search next request", err)
	}
	events, done, err := nextSearchPage(
		ctx, s, request.RequestID, request.Limit,
		"content", "content search request is not active", "content_search_next",
		func(event search.ContentEvent) bool { return event.Kind == search.ContentEventTerminal },
		ipc.EncodeContentSearchEvent,
	)
	if err != nil {
		return nil, err
	}
	return ipc.SearchContentNextResponse{Events: events, Done: done}, nil
}
