package app

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/TyrantLucifer/awesome-sftp-cli/internal/auth"
	"github.com/TyrantLucifer/awesome-sftp-cli/internal/ipc"
)

func TestRunAskpassWritesOnlyBrokerAnswer(t *testing.T) {
	client := &fakeAuthRPC{answer: "correct horse"}
	var stdout bytes.Buffer
	err := runAskpassWith(context.Background(), []string{"Password:"}, &stdout, client, func(key string) string {
		switch key {
		case auth.EnvAttemptToken:
			return testAskpassToken
		case "SSH_ASKPASS_PROMPT":
			return "none"
		default:
			return ""
		}
	})
	if err != nil {
		t.Fatal(err)
	}
	if stdout.String() != "correct horse\n" {
		t.Fatalf("stdout = %q", stdout.String())
	}
	if client.request.AttemptToken != testAskpassToken || client.request.Prompt != "Password:" || client.request.Kind != string(auth.PromptSecret) {
		t.Fatalf("prompt request = %#v", client.request)
	}
	if !client.closed {
		t.Fatal("askpass client was not closed")
	}
}

func TestRunAskpassUsesConfirmKindAndHidesFailures(t *testing.T) {
	client := &fakeAuthRPC{err: errors.New("stage1-secret-canary")}
	err := runAskpassWith(context.Background(), []string{"Continue?"}, &bytes.Buffer{}, client, func(key string) string {
		if key == auth.EnvAttemptToken {
			return testAskpassToken
		}
		if key == "SSH_ASKPASS_PROMPT" {
			return "confirm"
		}
		return ""
	})
	if err == nil || strings.Contains(err.Error(), "stage1-secret-canary") {
		t.Fatalf("runAskpassWith error = %v", err)
	}
	if client.request.Kind != string(auth.PromptConfirm) {
		t.Fatalf("prompt kind = %q", client.request.Kind)
	}
}

func TestRunAskpassRejectsInvalidInvocationBeforeRPC(t *testing.T) {
	client := &fakeAuthRPC{}
	for _, args := range [][]string{nil, {"one", "two"}} {
		if err := runAskpassWith(context.Background(), args, &bytes.Buffer{}, client, func(string) string { return "" }); err == nil {
			t.Fatalf("runAskpassWith(%q) error = nil", args)
		}
	}
	if client.calls != 0 {
		t.Fatalf("RPC calls = %d, want 0", client.calls)
	}
}

// #nosec G101 -- deterministic opaque capability-format fixture, not a credential.
const testAskpassToken = "YWJjZGVmZ2hpamtsbW5vcHFyc3R1dnd4eXphYmNkZWY"

type fakeAuthRPC struct {
	request ipc.AuthPromptRequest
	answer  string
	err     error
	calls   int
	closed  bool
}

func (c *fakeAuthRPC) Call(_ context.Context, name string, request, response any) error {
	c.calls++
	if name != daemonAuthPromptName {
		return errors.New("unexpected RPC")
	}
	c.request = request.(ipc.AuthPromptRequest)
	if c.err != nil {
		return c.err
	}
	response.(*ipc.AuthPromptResponse).Answer = c.answer
	return nil
}

func (c *fakeAuthRPC) Close() error { c.closed = true; return nil }
