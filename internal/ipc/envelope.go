package ipc

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"unicode/utf8"

	"github.com/TyrantLucifer/awesome-sftp-cli/internal/domain"
)

type MessageKind string

const (
	KindRequest  MessageKind = "request"
	KindResponse MessageKind = "response"
	KindEvent    MessageKind = "event"
)

type RPCError struct {
	Code       domain.Code         `json:"code"`
	Message    string              `json:"message"`
	Operation  string              `json:"operation,omitempty"`
	EndpointID domain.EndpointID   `json:"endpoint_id,omitempty"`
	Location   *WireLocation       `json:"location,omitempty"`
	Retry      domain.RetryAdvice  `json:"retry"`
	Effect     domain.EffectStatus `json:"effect"`
}

type Envelope struct {
	Protocol  ProtocolVersion  `json:"protocol"`
	Kind      MessageKind      `json:"kind"`
	Name      string           `json:"name"`
	RequestID domain.RequestID `json:"request_id,omitempty"`
	Cursor    *EventCursor     `json:"cursor,omitempty"`
	Payload   json.RawMessage  `json:"payload,omitempty"`
	Error     *RPCError        `json:"error,omitempty"`
}

type CancelRequest struct {
	TargetRequestID domain.RequestID `json:"target_request_id"`
}

type CancelState string

const (
	CancelAccepted        CancelState = "accepted"
	CancelAlreadyFinished CancelState = "already_finished"
	CancelNotFound        CancelState = "not_found"
	CancelNotCancellable  CancelState = "not_cancellable"
)

type CancelResult struct {
	State CancelState `json:"state"`
}

func (e Envelope) Validate() error {
	if e.Protocol.Major != ProtocolMajor {
		return errors.New("validate envelope: incompatible protocol major")
	}
	if e.Name == "" {
		return errors.New("validate envelope: name is empty")
	}
	if !utf8.ValidString(e.Name) {
		return errors.New("validate envelope: name is invalid UTF-8")
	}
	if err := validatePayload(e.Payload); err != nil {
		return err
	}

	switch e.Kind {
	case KindRequest:
		if err := validateRequestID(e.RequestID); err != nil {
			return err
		}
		if e.Cursor != nil {
			return errors.New("validate request envelope: cursor is not allowed")
		}
		if e.Error != nil {
			return errors.New("validate request envelope: error is not allowed")
		}
	case KindResponse:
		if err := validateRequestID(e.RequestID); err != nil {
			return err
		}
		if e.Cursor != nil {
			return errors.New("validate response envelope: cursor is not allowed")
		}
		if len(e.Payload) != 0 && e.Error != nil {
			return errors.New("validate response envelope: payload and error are mutually exclusive")
		}
		if err := validateRPCError(e.Error); err != nil {
			return err
		}
	case KindEvent:
		if e.RequestID != "" {
			return errors.New("validate event envelope: request ID is not allowed")
		}
		if e.Cursor == nil {
			return errors.New("validate event envelope: cursor is required")
		}
		if e.Cursor.Epoch == "" {
			return errors.New("validate event envelope: cursor epoch is empty")
		}
		if !utf8.ValidString(e.Cursor.Epoch) {
			return errors.New("validate event envelope: cursor epoch is invalid UTF-8")
		}
		if e.Error != nil {
			return errors.New("validate event envelope: error is not allowed")
		}
	default:
		return errors.New("validate envelope: unknown message kind")
	}

	return nil
}

func DecodeEnvelope(data []byte) (Envelope, error) {
	if !utf8.Valid(data) {
		return Envelope{}, errors.New("decode envelope: invalid UTF-8")
	}
	trimmed := bytes.TrimSpace(data)
	if len(trimmed) == 0 || trimmed[0] != '{' {
		return Envelope{}, errors.New("decode envelope: expected one JSON object")
	}

	decoder := json.NewDecoder(bytes.NewReader(data))
	var envelope Envelope
	if err := decoder.Decode(&envelope); err != nil {
		return Envelope{}, fmt.Errorf("decode envelope: invalid JSON: %w", err)
	}

	var trailing json.RawMessage
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		if err == nil {
			return Envelope{}, errors.New("decode envelope: multiple JSON values")
		}
		return Envelope{}, fmt.Errorf("decode envelope: invalid trailing JSON: %w", err)
	}
	if err := envelope.Validate(); err != nil {
		return Envelope{}, err
	}
	return envelope, nil
}

func EncodeEnvelope(envelope Envelope) ([]byte, error) {
	if err := envelope.Validate(); err != nil {
		return nil, err
	}
	data, err := json.Marshal(envelope)
	if err != nil {
		return nil, fmt.Errorf("encode envelope: invalid value: %w", err)
	}
	return data, nil
}

func validateRequestID(requestID domain.RequestID) error {
	if requestID == "" {
		return errors.New("validate envelope: request ID is required")
	}
	if _, err := domain.ParseRequestID(string(requestID)); err != nil {
		return errors.New("validate envelope: request ID is invalid")
	}
	return nil
}

func validatePayload(payload json.RawMessage) error {
	if len(payload) != 0 {
		if !utf8.Valid(payload) {
			return errors.New("validate envelope: payload is invalid UTF-8")
		}
		if !json.Valid(payload) {
			return errors.New("validate envelope: payload is invalid JSON")
		}
	}
	return nil
}

func validateRPCError(rpcError *RPCError) error {
	if rpcError == nil {
		return nil
	}
	if rpcError.Code == "" {
		return errors.New("validate response envelope: error code is empty")
	}
	if !utf8.ValidString(string(rpcError.Code)) {
		return errors.New("validate response envelope: error code is invalid UTF-8")
	}
	if !isKnownCode(rpcError.Code) {
		return errors.New("validate response envelope: error code is unknown")
	}
	if rpcError.Message == "" {
		return errors.New("validate response envelope: error message is empty")
	}
	if !utf8.ValidString(rpcError.Message) {
		return errors.New("validate response envelope: error message is invalid UTF-8")
	}
	if !utf8.ValidString(rpcError.Operation) {
		return errors.New("validate response envelope: error operation is invalid UTF-8")
	}
	if rpcError.Retry.Kind == "" {
		return errors.New("validate response envelope: retry advice is empty")
	}
	if !utf8.ValidString(string(rpcError.Retry.Kind)) {
		return errors.New("validate response envelope: retry advice is invalid UTF-8")
	}
	if !isKnownRetryKind(rpcError.Retry.Kind) {
		return errors.New("validate response envelope: retry advice is unknown")
	}
	if rpcError.Retry.After < 0 {
		return errors.New("validate response envelope: retry delay is negative")
	}
	if rpcError.Effect == "" {
		return errors.New("validate response envelope: effect status is empty")
	}
	if !utf8.ValidString(string(rpcError.Effect)) {
		return errors.New("validate response envelope: effect status is invalid UTF-8")
	}
	if !isKnownEffectStatus(rpcError.Effect) {
		return errors.New("validate response envelope: effect status is unknown")
	}
	if rpcError.EndpointID != "" {
		if _, err := domain.ParseEndpointID(string(rpcError.EndpointID)); err != nil {
			return errors.New("validate response envelope: endpoint ID is invalid")
		}
	}
	if rpcError.Location != nil {
		if _, err := domain.ParseEndpointID(rpcError.Location.EndpointID); err != nil {
			return errors.New("validate response envelope: location endpoint ID is invalid")
		}
		if _, err := rpcError.Location.Path.Decode(); err != nil {
			return errors.New("validate response envelope: location path encoding is invalid")
		}
	}
	return nil
}

func isKnownCode(code domain.Code) bool {
	switch code {
	case domain.CodeInvalidArgument,
		domain.CodeNotFound,
		domain.CodeAlreadyExists,
		domain.CodePermissionDenied,
		domain.CodeAuthRequired,
		domain.CodeTransportInterrupted,
		domain.CodeTimeout,
		domain.CodeUnsupported,
		domain.CodeCapabilityLost,
		domain.CodeConflict,
		domain.CodeResourceExhausted,
		domain.CodeIntegrityFailed,
		domain.CodeCanceled,
		domain.CodeProtocolIncompatible,
		domain.CodeInternal:
		return true
	default:
		return false
	}
}

func isKnownRetryKind(kind domain.RetryKind) bool {
	switch kind {
	case domain.RetryNever,
		domain.RetryImmediate,
		domain.RetryBackoff,
		domain.RetryAfterReconnect,
		domain.RetryAfterAuth,
		domain.RetryAfterConflict,
		domain.RetryAfterReplan:
		return true
	default:
		return false
	}
}

func isKnownEffectStatus(status domain.EffectStatus) bool {
	switch status {
	case domain.EffectNone, domain.EffectApplied, domain.EffectUnknown:
		return true
	default:
		return false
	}
}
