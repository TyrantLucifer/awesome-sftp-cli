package app

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/TyrantLucifer/awesome-mac-sftp/internal/daemon"
	"github.com/TyrantLucifer/awesome-mac-sftp/internal/ipc"
	"github.com/TyrantLucifer/awesome-mac-sftp/internal/tui"
)

func TestRunAuthClaimLoopDeliversSequentialChallengesAndResolutions(t *testing.T) {
	client := &fakeAuthClaimRPC{resolved: make(chan struct{}, 2), claims: []ipc.AuthClaimResponse{
		{ChallengeID: "YWJjZGVmZ2hpamtsbW5vcXJz", Endpoint: "password-host", Prompt: "Password:", Kind: "secret", ExpiresAt: time.Now().Add(time.Minute)},
		{ChallengeID: "YWJjZGVmZ2hpamtsbW5vcXR1", Endpoint: "confirm-host", Prompt: "Continue?", Kind: "confirm", ExpiresAt: time.Now().Add(time.Minute)},
	}}
	actions := make(chan tui.Action)
	resolutions := make(chan tui.Intent)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- runAuthClaimLoop(ctx, client, actions, resolutions) }()

	first := (<-actions).(tui.AuthChallengeReceived)
	if first.Endpoint != "password-host" || first.Prompt != "Password:" || first.Kind != "secret" {
		t.Fatalf("first challenge = %#v", first)
	}
	resolutions <- tui.Intent{Kind: tui.IntentAuthResolve, ChallengeID: first.ChallengeID, Answer: []byte("stage1-secret-canary")}

	second := (<-actions).(tui.AuthChallengeReceived)
	if second.Endpoint != "confirm-host" || second.Kind != "confirm" {
		t.Fatalf("second challenge = %#v", second)
	}
	resolutions <- tui.Intent{Kind: tui.IntentAuthResolve, ChallengeID: second.ChallengeID, Cancel: true}

	deadline := time.NewTimer(time.Second)
	defer deadline.Stop()
	for range 2 {
		select {
		case <-client.resolved:
		case <-deadline.C:
			t.Fatal("timed out waiting for authentication resolutions")
		}
	}
	cancel()
	if err := <-done; !errors.Is(err, context.Canceled) {
		t.Fatalf("shutdown error = %v, want context canceled", err)
	}
	got := client.resolutionsCopy()
	if len(got) != 2 || got[0].Action != ipc.AuthActionAnswer || got[0].Answer != "stage1-secret-canary" || got[1].Action != ipc.AuthActionCancel || got[1].Answer != "" {
		t.Fatalf("resolutions = %#v", got)
	}
}

func TestRunAuthClaimLoopDoesNotExposeDaemonFailureOrRetainRejectedAnswer(t *testing.T) {
	client := failingAuthClaimRPC{err: errors.New("stage1-secret-canary")}
	err := runAuthClaimLoop(context.Background(), client, make(chan tui.Action), make(chan tui.Intent))
	if err == nil || strings.Contains(err.Error(), "stage1-secret-canary") {
		t.Fatalf("claim error = %v", err)
	}

	answer := []byte("temporary-answer")
	err = resolveAuthChallenge(context.Background(), client, "expected", tui.Intent{Kind: tui.IntentAuthResolve, ChallengeID: "wrong", Answer: answer})
	if err == nil {
		t.Fatal("mismatched challenge error = nil")
	}
	for index, value := range answer {
		if value != 0 {
			t.Fatalf("answer byte %d was not cleared", index)
		}
	}
}

type fakeAuthClaimRPC struct {
	mu          sync.Mutex
	claims      []ipc.AuthClaimResponse
	nextClaim   int
	resolutions []ipc.AuthResolveRequest
	resolved    chan struct{}
}

func (c *fakeAuthClaimRPC) Call(ctx context.Context, name string, request, response any) error {
	switch name {
	case daemon.AuthClaim:
		c.mu.Lock()
		if c.nextClaim < len(c.claims) {
			claim := c.claims[c.nextClaim]
			c.nextClaim++
			c.mu.Unlock()
			*response.(*ipc.AuthClaimResponse) = claim
			return nil
		}
		c.mu.Unlock()
		<-ctx.Done()
		return ctx.Err()
	case daemon.AuthResolve:
		c.mu.Lock()
		c.resolutions = append(c.resolutions, request.(ipc.AuthResolveRequest))
		c.mu.Unlock()
		c.resolved <- struct{}{}
		return nil
	default:
		return context.Canceled
	}
}

func (c *fakeAuthClaimRPC) resolutionsCopy() []ipc.AuthResolveRequest {
	c.mu.Lock()
	defer c.mu.Unlock()
	return append([]ipc.AuthResolveRequest(nil), c.resolutions...)
}

type failingAuthClaimRPC struct{ err error }

func (c failingAuthClaimRPC) Call(context.Context, string, any, any) error { return c.err }
