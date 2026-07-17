package ipc

import (
	"errors"

	"github.com/TyrantLucifer/awesome-mac-sftp/internal/domain"
	"github.com/TyrantLucifer/awesome-mac-sftp/internal/search"
)

type ContentSearchIdentityWire struct {
	RequestID          string                `json:"request_id"`
	EndpointID         string                `json:"endpoint_id"`
	SessionID          string                `json:"session_id"`
	EndpointGeneration uint64                `json:"endpoint_generation"`
	UIGeneration       uint64                `json:"ui_generation"`
	Scope              WireLocation          `json:"scope"`
	Options            search.ContentOptions `json:"options"`
	Budget             search.ContentBudget  `json:"budget"`
}

type SearchContentStartRequest struct {
	Identity ContentSearchIdentityWire `json:"identity"`
}
type SearchContentStartResponse struct {
	RequestID string `json:"request_id"`
	Done      bool   `json:"done"`
}
type SearchContentNextRequest struct {
	RequestID string `json:"request_id"`
	Limit     uint32 `json:"limit"`
}
type SearchContentNextResponse struct {
	Events []ContentSearchEventWire `json:"events"`
	Done   bool                     `json:"done"`
}
type ContentSearchResultWire struct {
	Location     WireLocation `json:"location"`
	RelativePath WireBytes    `json:"relative_path"`
	Line         uint64       `json:"line"`
	Offset       uint64       `json:"offset"`
	Snippet      WireBytes    `json:"snippet"`
}
type ContentSearchProblemWire struct {
	Location WireLocation      `json:"location"`
	Code     domain.Code       `json:"code"`
	Reason   search.StopReason `json:"reason"`
}
type ContentSearchEventWire struct {
	Identity ContentSearchIdentityWire `json:"identity"`
	Kind     search.ContentEventKind   `json:"kind"`
	Result   *ContentSearchResultWire  `json:"result,omitempty"`
	Problem  *ContentSearchProblemWire `json:"problem,omitempty"`
	Terminal *search.ContentTerminal   `json:"terminal,omitempty"`
}

func EncodeContentSearchIdentity(identity search.ContentIdentity) ContentSearchIdentityWire {
	return ContentSearchIdentityWire{RequestID: string(identity.RequestID), EndpointID: string(identity.EndpointID), SessionID: string(identity.SessionID), EndpointGeneration: identity.EndpointGeneration, UIGeneration: identity.UIGeneration, Scope: EncodeLocation(identity.Scope), Options: identity.Options, Budget: identity.Budget}
}

func DecodeContentSearchIdentity(wire ContentSearchIdentityWire) (search.ContentIdentity, error) {
	requestID, err := domain.ParseRequestID(wire.RequestID)
	if err != nil {
		return search.ContentIdentity{}, err
	}
	endpointID, err := domain.ParseEndpointID(wire.EndpointID)
	if err != nil {
		return search.ContentIdentity{}, err
	}
	sessionID, err := domain.ParseSessionID(wire.SessionID)
	if err != nil {
		return search.ContentIdentity{}, err
	}
	scope, err := DecodeLocation(wire.Scope)
	if err != nil {
		return search.ContentIdentity{}, err
	}
	return search.ContentIdentity{RequestID: requestID, EndpointID: endpointID, SessionID: sessionID, EndpointGeneration: wire.EndpointGeneration, UIGeneration: wire.UIGeneration, Scope: scope, Options: wire.Options, Budget: wire.Budget}, nil
}

func EncodeContentSearchEvent(event search.ContentEvent) ContentSearchEventWire {
	wire := ContentSearchEventWire{Identity: EncodeContentSearchIdentity(event.Identity), Kind: event.Kind}
	switch event.Kind {
	case search.ContentEventResult:
		wire.Result = &ContentSearchResultWire{Location: EncodeLocation(event.Result.Location), RelativePath: EncodeWireBytes([]byte(event.Result.RelativePath)), Line: event.Result.Line, Offset: event.Result.Offset, Snippet: EncodeWireBytes([]byte(event.Result.Snippet))}
	case search.ContentEventProblem:
		wire.Problem = &ContentSearchProblemWire{Location: EncodeLocation(event.Problem.Location), Code: event.Problem.Code, Reason: event.Problem.Reason}
	case search.ContentEventTerminal:
		terminal := event.Terminal
		wire.Terminal = &terminal
	}
	return wire
}

func DecodeContentSearchEvent(wire ContentSearchEventWire) (search.ContentEvent, error) {
	identity, err := DecodeContentSearchIdentity(wire.Identity)
	if err != nil {
		return search.ContentEvent{}, err
	}
	event := search.ContentEvent{Identity: identity, Kind: wire.Kind}
	switch wire.Kind {
	case search.ContentEventResult:
		if wire.Result == nil || wire.Problem != nil || wire.Terminal != nil {
			return search.ContentEvent{}, errors.New("decode content search event: invalid result payload")
		}
		location, err := DecodeLocation(wire.Result.Location)
		if err != nil {
			return search.ContentEvent{}, err
		}
		relative, err := wire.Result.RelativePath.Decode()
		if err != nil {
			return search.ContentEvent{}, err
		}
		snippet, err := wire.Result.Snippet.Decode()
		if err != nil {
			return search.ContentEvent{}, err
		}
		event.Result = search.ContentResult{Location: location, RelativePath: string(relative), Line: wire.Result.Line, Offset: wire.Result.Offset, Snippet: string(snippet)}
	case search.ContentEventProblem:
		if wire.Problem == nil || wire.Result != nil || wire.Terminal != nil {
			return search.ContentEvent{}, errors.New("decode content search event: invalid problem payload")
		}
		location, err := DecodeLocation(wire.Problem.Location)
		if err != nil {
			return search.ContentEvent{}, err
		}
		event.Problem = search.ContentProblem{Location: location, Code: wire.Problem.Code, Reason: wire.Problem.Reason}
	case search.ContentEventTerminal:
		if wire.Terminal == nil || wire.Result != nil || wire.Problem != nil {
			return search.ContentEvent{}, errors.New("decode content search event: invalid terminal payload")
		}
		event.Terminal = *wire.Terminal
	default:
		return search.ContentEvent{}, errors.New("decode content search event: unknown event kind")
	}
	return event, nil
}
