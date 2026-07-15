package app

import (
	"context"
	"errors"
	"reflect"
	"testing"
	"time"

	"github.com/TyrantLucifer/awesome-mac-sftp/internal/daemon"
	"github.com/TyrantLucifer/awesome-mac-sftp/internal/domain"
	"github.com/TyrantLucifer/awesome-mac-sftp/internal/ipc"
)

func TestClassifySSHConnectErrorDoesNotRetryAuthHostKeyOrConfig(t *testing.T) {
	tests := []struct {
		message string
		code    domain.Code
		retry   domain.RetryKind
	}{
		{message: "Permission denied (publickey,password)", code: domain.CodeAuthRequired, retry: domain.RetryAfterAuth},
		{message: "REMOTE HOST IDENTIFICATION HAS CHANGED", code: domain.CodePermissionDenied, retry: domain.RetryNever},
		{message: "subsystem request failed on channel 0", code: domain.CodeUnsupported, retry: domain.RetryNever},
		{message: "Connection refused", code: domain.CodeTransportInterrupted, retry: domain.RetryAfterReconnect},
	}
	for _, test := range tests {
		code, retry := classifySSHConnectError(errors.New(test.message))
		if code != test.code || retry != test.retry {
			t.Fatalf("classify %q = (%s, %s), want (%s, %s)", test.message, code, retry, test.code, test.retry)
		}
	}
}

func TestRunReconnectUsesBoundedBackoffAndStopsOnNonRetryableError(t *testing.T) {
	var sleeps []time.Duration
	policy := reconnectPolicy{
		Delays: []time.Duration{10 * time.Millisecond, 20 * time.Millisecond, 40 * time.Millisecond},
		Sleep: func(_ context.Context, delay time.Duration) error {
			sleeps = append(sleeps, delay)
			return nil
		},
	}
	attempts := 0
	err := runReconnect(context.Background(), policy, func() error {
		attempts++
		if attempts < 3 {
			return remoteRetryError(domain.RetryAfterReconnect)
		}
		return nil
	})
	if err != nil || attempts != 3 || !reflect.DeepEqual(sleeps, []time.Duration{10 * time.Millisecond, 20 * time.Millisecond}) {
		t.Fatalf("reconnect err=%v attempts=%d sleeps=%v", err, attempts, sleeps)
	}

	attempts = 0
	sleeps = nil
	err = runReconnect(context.Background(), policy, func() error {
		attempts++
		return remoteRetryError(domain.RetryAfterAuth)
	})
	if err == nil || attempts != 1 || len(sleeps) != 0 {
		t.Fatalf("non-retryable err=%v attempts=%d sleeps=%v", err, attempts, sleeps)
	}
}

func TestProviderCallFailureSeparatesEndpointAndDaemonLoss(t *testing.T) {
	code, retry, daemonLost := providerCallFailure(remoteRetryError(domain.RetryAfterReconnect))
	if code != domain.CodeTransportInterrupted || retry != domain.RetryAfterReconnect || daemonLost {
		t.Fatalf("remote failure = (%s, %s, %t)", code, retry, daemonLost)
	}
	code, retry, daemonLost = providerCallFailure(errors.New("local socket closed"))
	if code != domain.CodeTransportInterrupted || retry != domain.RetryAfterReconnect || !daemonLost {
		t.Fatalf("daemon failure = (%s, %s, %t)", code, retry, daemonLost)
	}
}

func TestDaemonConnectionLostFollowsWrappedTransportButNotRemotePolicy(t *testing.T) {
	if !daemonConnectionLost(&authRPCError{operation: "claim", cause: errors.New("socket closed")}) {
		t.Fatal("wrapped local transport was not treated as daemon loss")
	}
	if daemonConnectionLost(&authRPCError{operation: "claim", cause: remoteRetryError(domain.RetryNever)}) {
		t.Fatal("structured remote failure was treated as daemon loss")
	}
	if daemonConnectionLost(context.Canceled) {
		t.Fatal("context cancellation was treated as daemon loss")
	}
}

func TestRecoveryParentWalksTowardRootWithoutChangingEndpoint(t *testing.T) {
	location := domain.Location{EndpointID: domain.EndpointID("ep_aaaaaaaaaaaaaaaaaaaaaaaaaa"), Path: "/srv/missing/deep"}
	parent, ok := recoveryParent(location)
	if !ok || parent.EndpointID != location.EndpointID || parent.Path != "/srv/missing" {
		t.Fatalf("recovery parent = %#v, %t", parent, ok)
	}
	if _, ok := recoveryParent(domain.Location{EndpointID: location.EndpointID, Path: "/"}); ok {
		t.Fatal("root unexpectedly has a recovery parent")
	}
}

func remoteRetryError(retry domain.RetryKind) error {
	return &daemon.RemoteError{RPC: ipc.RPCError{
		Code:    domain.CodeTransportInterrupted,
		Message: "connection failed",
		Retry:   domain.RetryAdvice{Kind: retry},
		Effect:  domain.EffectNone,
	}}
}
