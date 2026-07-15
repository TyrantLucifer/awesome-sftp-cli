package domain

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"
)

func TestCanonicalErrorConstants(t *testing.T) {
	codes := map[Code]string{
		CodeInvalidArgument:      "invalid_argument",
		CodeNotFound:             "not_found",
		CodeAlreadyExists:        "already_exists",
		CodePermissionDenied:     "permission_denied",
		CodeAuthRequired:         "auth_required",
		CodeTransportInterrupted: "transport_interrupted",
		CodeTimeout:              "timeout",
		CodeUnsupported:          "unsupported",
		CodeCapabilityLost:       "capability_lost",
		CodeConflict:             "conflict",
		CodeResourceExhausted:    "resource_exhausted",
		CodeIntegrityFailed:      "integrity_failed",
		CodeCanceled:             "canceled",
		CodeProtocolIncompatible: "protocol_incompatible",
		CodeInternal:             "internal",
	}
	if len(codes) != 15 {
		t.Fatalf("canonical code count = %d, want 15", len(codes))
	}
	for code, want := range codes {
		if got := string(code); got != want {
			t.Errorf("code = %q, want %q", got, want)
		}
	}

	retries := map[RetryKind]string{
		RetryNever:          "never",
		RetryImmediate:      "immediate",
		RetryBackoff:        "backoff",
		RetryAfterReconnect: "after_reconnect",
		RetryAfterAuth:      "after_auth",
		RetryAfterConflict:  "after_conflict",
		RetryAfterReplan:    "after_replan",
	}
	if len(retries) != 7 {
		t.Fatalf("canonical retry count = %d, want 7", len(retries))
	}
	for retry, want := range retries {
		if got := string(retry); got != want {
			t.Errorf("retry kind = %q, want %q", got, want)
		}
	}

	effects := map[EffectStatus]string{
		EffectNone:    "none",
		EffectApplied: "applied",
		EffectUnknown: "unknown",
	}
	if len(effects) != 3 {
		t.Fatalf("canonical effect count = %d, want 3", len(effects))
	}
	for effect, want := range effects {
		if got := string(effect); got != want {
			t.Errorf("effect = %q, want %q", got, want)
		}
	}
}

func TestOpErrorDisplaysOperationAndMessageWithoutCause(t *testing.T) {
	cause := errors.New("secret credential material")
	err := &OpError{
		Code:      CodeNotFound,
		Message:   "path was not found",
		Operation: "stat",
		Cause:     cause,
	}

	if got, want := err.Error(), "stat: path was not found"; got != want {
		t.Fatalf("Error() = %q, want %q", got, want)
	}
	if strings.Contains(err.Error(), cause.Error()) {
		t.Fatalf("Error() = %q, must not expose Cause", err.Error())
	}
	if !errors.Is(err, cause) {
		t.Fatal("errors.Is(OpError, cause) = false, want true")
	}
}

func TestOpErrorDisplayFallbackDoesNotUseCause(t *testing.T) {
	err := &OpError{Code: CodeInternal, Cause: errors.New("private details")}
	if got, want := err.Error(), "internal"; got != want {
		t.Fatalf("Error() = %q, want %q", got, want)
	}

	var nilError *OpError
	if got, want := nilError.Error(), "<nil>"; got != want {
		t.Fatalf("nil Error() = %q, want %q", got, want)
	}
}

func TestIsCodeFindsWrappedOpError(t *testing.T) {
	err := fmt.Errorf("outer: %w", &OpError{Code: CodeConflict, Message: "changed"})

	if !IsCode(err, CodeConflict) {
		t.Fatal("IsCode(conflict) = false, want true")
	}
	if IsCode(err, CodeNotFound) {
		t.Fatal("IsCode(not_found) = true, want false")
	}
	if IsCode(nil, CodeConflict) {
		t.Fatal("IsCode(nil) = true, want false")
	}
}

func TestFromContextMapsCancellationAndDeadline(t *testing.T) {
	tests := []struct {
		name      string
		err       error
		wantCode  Code
		wantRetry RetryKind
	}{
		{
			name:      "canceled",
			err:       fmt.Errorf("wrapped: %w", context.Canceled),
			wantCode:  CodeCanceled,
			wantRetry: RetryNever,
		},
		{
			name:      "deadline",
			err:       fmt.Errorf("wrapped: %w", context.DeadlineExceeded),
			wantCode:  CodeTimeout,
			wantRetry: RetryBackoff,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got := FromContext("list", "ep_aaaaaaaaaaaaaaaaaaaaaaaaaa", nil, test.err)
			var opError *OpError
			if !errors.As(got, &opError) {
				t.Fatalf("FromContext() type = %T, want *OpError", got)
			}
			if opError.Code != test.wantCode {
				t.Fatalf("Code = %q, want %q", opError.Code, test.wantCode)
			}
			if opError.Retry.Kind != test.wantRetry {
				t.Fatalf("Retry.Kind = %q, want %q", opError.Retry.Kind, test.wantRetry)
			}
			if opError.Effect != EffectUnknown {
				t.Fatalf("Effect = %q, want %q", opError.Effect, EffectUnknown)
			}
			if !errors.Is(got, test.err) {
				t.Fatal("errors.Is(FromContext(), input) = false, want true")
			}
		})
	}
}

func TestFromContextEnrichesOpErrorWithoutMutatingOrSharingLocation(t *testing.T) {
	cause := errors.New("transport details")
	original := &OpError{
		Code:    CodeTransportInterrupted,
		Message: "connection ended",
		Retry:   RetryAdvice{Kind: RetryAfterReconnect, After: time.Second},
		Effect:  EffectUnknown,
		Cause:   cause,
	}
	location := &Location{
		EndpointID: "ep_aaaaaaaaaaaaaaaaaaaaaaaaaa",
		Path:       "/before",
	}

	got := FromContext("read", location.EndpointID, location, original)
	var enriched *OpError
	if !errors.As(got, &enriched) {
		t.Fatalf("FromContext() type = %T, want *OpError", got)
	}
	if enriched == original {
		t.Fatal("FromContext() returned caller-owned OpError pointer")
	}
	if original.Operation != "" || original.EndpointID != "" || original.Location != nil {
		t.Fatal("FromContext() mutated input OpError")
	}
	if enriched.Operation != "read" || enriched.EndpointID != location.EndpointID {
		t.Fatalf("context = (%q, %q), want (read, %q)", enriched.Operation, enriched.EndpointID, location.EndpointID)
	}
	if enriched.Location == nil || enriched.Location == location {
		t.Fatal("FromContext() did not copy the caller-owned Location")
	}
	location.Path = "/after"
	if enriched.Location.Path != "/before" {
		t.Fatalf("enriched location after caller mutation = %q, want /before", enriched.Location.Path)
	}
	if !errors.Is(enriched, cause) {
		t.Fatal("enriched error does not preserve original Cause")
	}
}

func TestFromContextPreservesExistingOpErrorContext(t *testing.T) {
	existingLocation := &Location{EndpointID: "ep_existing", Path: "/existing"}
	original := &OpError{
		Code:       CodePermissionDenied,
		Message:    "denied",
		Operation:  "existing operation",
		EndpointID: "ep_existing",
		Location:   existingLocation,
		Retry:      RetryAdvice{Kind: RetryAfterAuth},
		Effect:     EffectNone,
	}

	got := FromContext(
		"new operation",
		"ep_new",
		&Location{EndpointID: "ep_new", Path: "/new"},
		original,
	)
	var enriched *OpError
	if !errors.As(got, &enriched) {
		t.Fatalf("FromContext() type = %T, want *OpError", got)
	}
	if enriched.Operation != original.Operation || enriched.EndpointID != original.EndpointID {
		t.Fatalf("existing context was overwritten: %#v", enriched)
	}
	if enriched.Location == nil || *enriched.Location != *existingLocation {
		t.Fatalf("Location = %#v, want %#v", enriched.Location, existingLocation)
	}
	if enriched.Location == existingLocation {
		t.Fatal("FromContext() shares existing Location pointer")
	}
}

func TestFromContextWrapsUnknownErrorAndHandlesNil(t *testing.T) {
	if got := FromContext("read", "ep_aaaaaaaaaaaaaaaaaaaaaaaaaa", nil, nil); got != nil {
		t.Fatalf("FromContext(nil) = %v, want nil", got)
	}

	cause := errors.New("implementation details")
	got := FromContext("read", "ep_aaaaaaaaaaaaaaaaaaaaaaaaaa", nil, cause)
	var opError *OpError
	if !errors.As(got, &opError) {
		t.Fatalf("FromContext() type = %T, want *OpError", got)
	}
	if opError.Code != CodeInternal || opError.Retry.Kind != RetryNever || opError.Effect != EffectUnknown {
		t.Fatalf("unknown error mapping = %#v", opError)
	}
	if strings.Contains(opError.Error(), cause.Error()) {
		t.Fatalf("Error() = %q, must not expose Cause", opError.Error())
	}
	if !errors.Is(opError, cause) {
		t.Fatal("errors.Is(FromContext(), cause) = false, want true")
	}
}
