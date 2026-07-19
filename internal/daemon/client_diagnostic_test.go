package daemon

import (
	"errors"
	"strings"
	"testing"

	"github.com/TyrantLucifer/awesome-sftp-cli/internal/domain"
	"github.com/TyrantLucifer/awesome-sftp-cli/internal/ipc"
)

func TestDiagnosticSummaryContainsOnlySafeCorrelationFields(t *testing.T) {
	requestID := domain.RequestID("req_aaaaaaaaaaaaaaaaaaaaaaaaaa")
	endpointID := domain.EndpointID("ep_aaaaaaaaaaaaaaaaaaaaaaaaaa")
	remote := &RemoteError{RequestID: requestID, RPC: ipc.RPCError{
		Code:       domain.CodeAuthRequired,
		Message:    "password secret-canary",
		Operation:  "connect /private/path",
		EndpointID: endpointID,
		Retry:      domain.RetryAdvice{Kind: domain.RetryAfterAuth},
		Effect:     domain.EffectNone,
	}}
	summary := DiagnosticSummary(remote)
	if summary.RequestID != requestID || summary.ErrorCode != domain.CodeAuthRequired || summary.EndpointID != endpointID || summary.Retry != domain.RetryAfterAuth || summary.Effect != domain.EffectNone {
		t.Fatalf("DiagnosticSummary() = %#v", summary)
	}
	if encoded := summary.String(); strings.Contains(encoded, "secret-canary") || strings.Contains(encoded, "/private/path") {
		t.Fatalf("safe summary leaked sensitive detail: %q", encoded)
	}
	var found *RemoteError
	if !errors.As(remote, &found) {
		t.Fatal("errors.As() did not preserve RemoteError")
	}
}

func TestDecodeClientResponsePreservesRequestIDOnRemoteError(t *testing.T) {
	requestID := domain.RequestID("req_aaaaaaaaaaaaaaaaaaaaaaaaaa")
	err := decodeClientResponse(ipc.Envelope{RequestID: requestID, Error: &ipc.RPCError{Code: domain.CodeNotFound}}, nil)
	var remote *RemoteError
	if !errors.As(err, &remote) || remote.RequestID != requestID {
		t.Fatalf("decodeClientResponse() error = %#v", err)
	}
}
