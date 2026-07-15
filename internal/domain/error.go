package domain

import (
	"context"
	"errors"
	"time"
)

type Code string

const (
	CodeInvalidArgument      Code = "invalid_argument"
	CodeNotFound             Code = "not_found"
	CodeAlreadyExists        Code = "already_exists"
	CodePermissionDenied     Code = "permission_denied"
	CodeAuthRequired         Code = "auth_required"
	CodeTransportInterrupted Code = "transport_interrupted"
	CodeTimeout              Code = "timeout"
	CodeUnsupported          Code = "unsupported"
	CodeCapabilityLost       Code = "capability_lost"
	CodeConflict             Code = "conflict"
	CodeResourceExhausted    Code = "resource_exhausted"
	CodeIntegrityFailed      Code = "integrity_failed"
	CodeCanceled             Code = "canceled"
	CodeProtocolIncompatible Code = "protocol_incompatible"
	CodeInternal             Code = "internal"
)

type RetryKind string

const (
	RetryNever          RetryKind = "never"
	RetryImmediate      RetryKind = "immediate"
	RetryBackoff        RetryKind = "backoff"
	RetryAfterReconnect RetryKind = "after_reconnect"
	RetryAfterAuth      RetryKind = "after_auth"
	RetryAfterConflict  RetryKind = "after_conflict"
	RetryAfterReplan    RetryKind = "after_replan"
)

type EffectStatus string

const (
	EffectNone    EffectStatus = "none"
	EffectApplied EffectStatus = "applied"
	EffectUnknown EffectStatus = "unknown"
)

type RetryAdvice struct {
	Kind  RetryKind
	After time.Duration
}

type OpError struct {
	Code       Code
	Message    string
	Operation  string
	EndpointID EndpointID
	Location   *Location
	Retry      RetryAdvice
	Effect     EffectStatus
	Cause      error
}

func (e *OpError) Error() string {
	if e == nil {
		return "<nil>"
	}

	display := e.Message
	if display == "" {
		display = string(e.Code)
	}
	if display == "" {
		display = "operation failed"
	}
	if e.Operation != "" {
		return e.Operation + ": " + display
	}
	return display
}

func (e *OpError) Unwrap() error {
	if e == nil {
		return nil
	}
	return e.Cause
}

func IsCode(err error, code Code) bool {
	var opError *OpError
	return errors.As(err, &opError) && opError != nil && opError.Code == code
}

func FromContext(operation string, endpointID EndpointID, loc *Location, err error) error {
	if err == nil {
		return nil
	}

	var existing *OpError
	if errors.As(err, &existing) && existing != nil {
		enriched := *existing
		if enriched.Operation == "" {
			enriched.Operation = operation
		}
		if enriched.EndpointID == "" {
			enriched.EndpointID = endpointID
		}
		if enriched.Location == nil {
			enriched.Location = cloneLocation(loc)
		} else {
			enriched.Location = cloneLocation(enriched.Location)
		}
		return &enriched
	}

	opError := &OpError{
		Operation:  operation,
		EndpointID: endpointID,
		Location:   cloneLocation(loc),
		Retry:      RetryAdvice{Kind: RetryNever},
		Effect:     EffectUnknown,
		Cause:      err,
	}
	switch {
	case errors.Is(err, context.Canceled):
		opError.Code = CodeCanceled
		opError.Message = "operation canceled"
	case errors.Is(err, context.DeadlineExceeded):
		opError.Code = CodeTimeout
		opError.Message = "operation timed out"
		opError.Retry.Kind = RetryBackoff
	default:
		opError.Code = CodeInternal
		opError.Message = "operation failed"
	}
	return opError
}

func cloneLocation(location *Location) *Location {
	if location == nil {
		return nil
	}
	cloned := *location
	return &cloned
}
