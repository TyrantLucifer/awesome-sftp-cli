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

func (session *providerSession) handleEditSession(ctx context.Context, name string, payload json.RawMessage) (any, error) {
	if session.editSessions == nil {
		return nil, &domain.OpError{Code: domain.CodeUnsupported, Message: "durable edit sessions are unavailable", Retry: domain.RetryAdvice{Kind: domain.RetryNever}, Effect: domain.EffectNone}
	}
	switch name {
	case EditSessionCreate:
		var request EditSessionCreateRequest
		if err := decodePayload(payload, &request); err != nil {
			return nil, invalidArgument("decode edit session create request", err)
		}
		record, err := session.editSessions.Create(ctx, request.Request)
		if err != nil {
			return nil, internalError("create edit session", err)
		}
		return EditSessionResponse{Session: record}, nil
	case EditSessionGet:
		var request EditSessionGetRequest
		if err := decodePayload(payload, &request); err != nil {
			return nil, invalidArgument("decode edit session get request", err)
		}
		record, err := session.editSessions.Get(ctx, request.SessionID)
		if err != nil {
			return nil, internalError("get edit session", err)
		}
		return EditSessionResponse{Session: record}, nil
	case EditSessionTransition:
		var request EditSessionTransitionRequest
		if err := decodePayload(payload, &request); err != nil {
			return nil, invalidArgument("decode edit session transition request", err)
		}
		record, err := session.editSessions.Transition(ctx, request.Request)
		if err != nil {
			return nil, internalError("transition edit session", err)
		}
		return EditSessionResponse{Session: record}, nil
	case EditSessionEvents:
		var request EditSessionEventsRequest
		if err := decodePayload(payload, &request); err != nil {
			return nil, invalidArgument("decode edit session events request", err)
		}
		events, err := session.editSessions.ListEvents(ctx, request.SessionID, request.AfterSequence, request.Limit)
		if err != nil {
			return nil, internalError("list edit session events", err)
		}
		return EditSessionEventsResponse{Events: events}, nil
	case EditSessionRecoverable:
		var request EditSessionRecoverableRequest
		if err := decodePayload(payload, &request); err != nil {
			return nil, invalidArgument("decode recoverable edit sessions request", err)
		}
		sessions, err := session.editSessions.ListRecoverable(ctx, request.Limit)
		if err != nil {
			return nil, internalError("list recoverable edit sessions", err)
		}
		return EditSessionRecoverableResponse{Sessions: sessions}, nil
	default:
		return nil, &domain.OpError{Code: domain.CodeUnsupported, Message: "unsupported edit session request", Retry: domain.RetryAdvice{Kind: domain.RetryNever}, Effect: domain.EffectNone}
	}
}
