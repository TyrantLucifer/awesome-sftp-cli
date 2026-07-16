package ipc

import (
	"errors"

	"github.com/TyrantLucifer/awesome-mac-sftp/internal/domain"
	"github.com/TyrantLucifer/awesome-mac-sftp/internal/search"
)

type SearchIdentityWire struct {
	RequestID          string         `json:"request_id"`
	EndpointID         string         `json:"endpoint_id"`
	SessionID          string         `json:"session_id"`
	EndpointGeneration uint64         `json:"endpoint_generation"`
	UIGeneration       uint64         `json:"ui_generation"`
	Scope              WireLocation   `json:"scope"`
	Options            search.Options `json:"options"`
	Budget             search.Budget  `json:"budget"`
}

type SearchFilenameStartRequest struct {
	Identity SearchIdentityWire `json:"identity"`
}

type SearchFilenameStartResponse struct {
	RequestID string `json:"request_id"`
	Done      bool   `json:"done"`
}

type SearchFilenameNextRequest struct {
	RequestID string `json:"request_id"`
	Limit     uint32 `json:"limit"`
}

type SearchFilenameNextResponse struct {
	Events []SearchEventWire `json:"events"`
	Done   bool              `json:"done"`
}

type SearchCancelRequest struct {
	RequestID string `json:"request_id"`
}

type SearchCancelResponse struct{}

type SearchResultWire struct {
	Location     WireLocation `json:"location"`
	RelativePath WireBytes    `json:"relative_path"`
	Entry        WireEntry    `json:"entry"`
}

type SearchProblemWire struct {
	Location WireLocation `json:"location"`
	Code     domain.Code  `json:"code"`
}

type SearchEventWire struct {
	Identity SearchIdentityWire `json:"identity"`
	Kind     search.EventKind   `json:"kind"`
	Result   *SearchResultWire  `json:"result,omitempty"`
	Problem  *SearchProblemWire `json:"problem,omitempty"`
	Terminal *search.Terminal   `json:"terminal,omitempty"`
}

func EncodeSearchIdentity(identity search.Identity) SearchIdentityWire {
	return SearchIdentityWire{
		RequestID:          string(identity.RequestID),
		EndpointID:         string(identity.EndpointID),
		SessionID:          string(identity.SessionID),
		EndpointGeneration: identity.EndpointGeneration,
		UIGeneration:       identity.UIGeneration,
		Scope:              EncodeLocation(identity.Scope),
		Options:            identity.Options,
		Budget:             identity.Budget,
	}
}

func DecodeSearchIdentity(wire SearchIdentityWire) (search.Identity, error) {
	requestID, err := domain.ParseRequestID(wire.RequestID)
	if err != nil {
		return search.Identity{}, err
	}
	endpointID, err := domain.ParseEndpointID(wire.EndpointID)
	if err != nil {
		return search.Identity{}, err
	}
	sessionID, err := domain.ParseSessionID(wire.SessionID)
	if err != nil {
		return search.Identity{}, err
	}
	scope, err := DecodeLocation(wire.Scope)
	if err != nil {
		return search.Identity{}, err
	}
	return search.Identity{
		RequestID:          requestID,
		EndpointID:         endpointID,
		SessionID:          sessionID,
		EndpointGeneration: wire.EndpointGeneration,
		UIGeneration:       wire.UIGeneration,
		Scope:              scope,
		Options:            wire.Options,
		Budget:             wire.Budget,
	}, nil
}

func EncodeSearchEvent(event search.Event) SearchEventWire {
	wire := SearchEventWire{Identity: EncodeSearchIdentity(event.Identity), Kind: event.Kind}
	switch event.Kind {
	case search.EventResult:
		wire.Result = &SearchResultWire{
			Location:     EncodeLocation(event.Result.Location),
			RelativePath: EncodeWireBytes([]byte(event.Result.RelativePath)),
			Entry:        EncodeEntry(event.Result.Entry),
		}
	case search.EventProblem:
		wire.Problem = &SearchProblemWire{Location: EncodeLocation(event.Problem.Location), Code: event.Problem.Code}
	case search.EventTerminal:
		terminal := event.Terminal
		wire.Terminal = &terminal
	}
	return wire
}

func DecodeSearchEvent(wire SearchEventWire) (search.Event, error) {
	identity, err := DecodeSearchIdentity(wire.Identity)
	if err != nil {
		return search.Event{}, err
	}
	event := search.Event{Identity: identity, Kind: wire.Kind}
	switch wire.Kind {
	case search.EventResult:
		if wire.Result == nil || wire.Problem != nil || wire.Terminal != nil {
			return search.Event{}, errors.New("decode search event: invalid result payload")
		}
		location, err := DecodeLocation(wire.Result.Location)
		if err != nil {
			return search.Event{}, err
		}
		relative, err := wire.Result.RelativePath.Decode()
		if err != nil {
			return search.Event{}, err
		}
		entry, err := DecodeEntry(wire.Result.Entry)
		if err != nil {
			return search.Event{}, err
		}
		event.Result = search.Result{Location: location, RelativePath: string(relative), Entry: entry}
	case search.EventProblem:
		if wire.Problem == nil || wire.Result != nil || wire.Terminal != nil {
			return search.Event{}, errors.New("decode search event: invalid problem payload")
		}
		location, err := DecodeLocation(wire.Problem.Location)
		if err != nil {
			return search.Event{}, err
		}
		event.Problem = search.Problem{Location: location, Code: wire.Problem.Code}
	case search.EventTerminal:
		if wire.Terminal == nil || wire.Result != nil || wire.Problem != nil {
			return search.Event{}, errors.New("decode search event: invalid terminal payload")
		}
		event.Terminal = *wire.Terminal
	default:
		return search.Event{}, errors.New("decode search event: unknown event kind")
	}
	return event, nil
}
