package daemon

import (
	"context"
	"encoding/json"

	"github.com/TyrantLucifer/awesome-sftp-cli/internal/domain"
	"github.com/TyrantLucifer/awesome-sftp-cli/internal/state/editstore"
)

const (
	EditSessionCreate      = "edit.session_create"
	EditSessionGet         = "edit.session_get"
	EditSessionTransition  = "edit.session_transition"
	EditSessionEvents      = "edit.session_events"
	EditSessionRecoverable = "edit.session_recoverable"
)

type EditSessionStore interface {
	Create(context.Context, editstore.CreateRequest) (editstore.Record, error)
	Get(context.Context, string) (editstore.Record, error)
	Transition(context.Context, editstore.TransitionRequest) (editstore.Record, error)
	ListEvents(context.Context, string, int64, int) ([]editstore.EventRecord, error)
	ListRecoverable(context.Context, int) ([]editstore.RecoveryRecord, error)
}

type EditSessionCreateRequest struct {
	Request editstore.CreateRequest `json:"request"`
}

type EditSessionGetRequest struct {
	SessionID string `json:"session_id"`
}

type EditSessionTransitionRequest struct {
	Request editstore.TransitionRequest `json:"request"`
}

type EditSessionEventsRequest struct {
	SessionID     string `json:"session_id"`
	AfterSequence int64  `json:"after_sequence"`
	Limit         int    `json:"limit"`
}

type EditSessionResponse struct {
	Session editstore.Record `json:"session"`
}

type EditSessionEventsResponse struct {
	Events []editstore.EventRecord `json:"events"`
}

type EditSessionRecoverableRequest struct {
	Limit int `json:"limit"`
}

type EditSessionRecoverableResponse struct {
	Sessions []editstore.RecoveryRecord `json:"sessions"`
}

func handleEditRPC[Req any, Resp any](
	payload json.RawMessage,
	decodeMsg, failMsg string,
	call func(Req) (Resp, error),
) (any, error) {
	request, err := decodeRPC[Req](payload, decodeMsg)
	if err != nil {
		return nil, err
	}
	response, err := call(request)
	if err != nil {
		return nil, internalError(failMsg, err)
	}
	return response, nil
}

func (session *providerSession) handleEditSession(ctx context.Context, name string, payload json.RawMessage) (any, error) {
	if session.editSessions == nil {
		return nil, &domain.OpError{Code: domain.CodeUnsupported, Message: "durable edit sessions are unavailable", Retry: domain.RetryAdvice{Kind: domain.RetryNever}, Effect: domain.EffectNone}
	}
	switch name {
	case EditSessionCreate:
		return handleEditRPC(payload, "decode edit session create request", "create edit session", func(request EditSessionCreateRequest) (EditSessionResponse, error) {
			record, err := session.editSessions.Create(ctx, request.Request)
			return EditSessionResponse{Session: record}, err
		})
	case EditSessionGet:
		return handleEditRPC(payload, "decode edit session get request", "get edit session", func(request EditSessionGetRequest) (EditSessionResponse, error) {
			record, err := session.editSessions.Get(ctx, request.SessionID)
			return EditSessionResponse{Session: record}, err
		})
	case EditSessionTransition:
		return handleEditRPC(payload, "decode edit session transition request", "transition edit session", func(request EditSessionTransitionRequest) (EditSessionResponse, error) {
			record, err := session.editSessions.Transition(ctx, request.Request)
			return EditSessionResponse{Session: record}, err
		})
	case EditSessionEvents:
		return handleEditRPC(payload, "decode edit session events request", "list edit session events", func(request EditSessionEventsRequest) (EditSessionEventsResponse, error) {
			events, err := session.editSessions.ListEvents(ctx, request.SessionID, request.AfterSequence, request.Limit)
			return EditSessionEventsResponse{Events: events}, err
		})
	case EditSessionRecoverable:
		return handleEditRPC(payload, "decode recoverable edit sessions request", "list recoverable edit sessions", func(request EditSessionRecoverableRequest) (EditSessionRecoverableResponse, error) {
			sessions, err := session.editSessions.ListRecoverable(ctx, request.Limit)
			return EditSessionRecoverableResponse{Sessions: sessions}, err
		})
	default:
		return nil, &domain.OpError{Code: domain.CodeUnsupported, Message: "unsupported edit session request", Retry: domain.RetryAdvice{Kind: domain.RetryNever}, Effect: domain.EffectNone}
	}
}
