package ipc

import (
	"strings"
	"testing"
)

// #nosec G101 -- deterministic opaque capability-format fixture, not a credential.
const testAttemptToken = "YWJjZGVmZ2hpamtsbW5vcHFyc3R1dnd4eXphYmNkZWY"
const testChallengeID = "YWJjZGVmZ2hpamtsbW5vcHFy"

func TestValidateAuthPromptRequest(t *testing.T) {
	valid := AuthPromptRequest{AttemptToken: testAttemptToken, Prompt: "Password:", Kind: "secret"}
	if err := ValidateAuthPromptRequest(valid); err != nil {
		t.Fatal(err)
	}
	tests := []AuthPromptRequest{
		{},
		{AttemptToken: "not-a-token", Prompt: "Password:", Kind: "secret"},
		{AttemptToken: testAttemptToken, Prompt: "", Kind: "secret"},
		{AttemptToken: testAttemptToken, Prompt: strings.Repeat("p", MaxAuthPromptBytes+1), Kind: "secret"},
		{AttemptToken: testAttemptToken, Prompt: "Password:\x00", Kind: "secret"},
		{AttemptToken: testAttemptToken, Prompt: "Password:", Kind: "other"},
	}
	for _, request := range tests {
		if err := ValidateAuthPromptRequest(request); err == nil {
			t.Fatalf("ValidateAuthPromptRequest(%#v) error = nil", request)
		}
	}
}

func TestValidateAuthResolveRequestNeverReturnsAnswer(t *testing.T) {
	secret := "stage1-secret-canary"
	valid := AuthResolveRequest{ChallengeID: testChallengeID, Action: AuthActionAnswer, Answer: secret}
	if err := ValidateAuthResolveRequest(valid); err != nil {
		t.Fatal(err)
	}
	tests := []AuthResolveRequest{
		{},
		{ChallengeID: "bad", Action: AuthActionAnswer, Answer: secret},
		{ChallengeID: testChallengeID, Action: "other", Answer: secret},
		{ChallengeID: testChallengeID, Action: AuthActionCancel, Answer: secret},
		{ChallengeID: testChallengeID, Action: AuthActionAnswer, Answer: "line\nbreak"},
		{ChallengeID: testChallengeID, Action: AuthActionAnswer, Answer: "nul\x00byte"},
		{ChallengeID: testChallengeID, Action: AuthActionAnswer, Answer: strings.Repeat("s", MaxAuthAnswerBytes+1)},
	}
	for _, request := range tests {
		err := ValidateAuthResolveRequest(request)
		if err == nil {
			t.Fatalf("ValidateAuthResolveRequest(%#v) error = nil", request)
		}
		if strings.Contains(err.Error(), secret) {
			t.Fatalf("validation error leaked answer: %v", err)
		}
	}
	if err := ValidateAuthResolveRequest(AuthResolveRequest{ChallengeID: testChallengeID, Action: AuthActionAnswer}); err != nil {
		t.Fatalf("empty answer rejected: %v", err)
	}
	if err := ValidateAuthResolveRequest(AuthResolveRequest{ChallengeID: testChallengeID, Action: AuthActionCancel}); err != nil {
		t.Fatalf("cancel rejected: %v", err)
	}
}
