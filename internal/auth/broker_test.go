package auth

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestBrokerDeliversSingleConsumptionAnswerToClaimedOwner(t *testing.T) {
	broker := newTestBroker(t, 4)
	attempt, err := broker.BeginAttempt(context.Background(), "work-host", time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer attempt.Close()

	answer := make(chan testPromptResult, 1)
	go func() {
		value, promptErr := broker.Prompt(context.Background(), attempt.Token(), "Password:", PromptSecret)
		answer <- testPromptResult{answer: value, err: promptErr}
	}()
	challenge, err := broker.Claim(context.Background(), 1)
	if err != nil {
		t.Fatal(err)
	}
	if challenge.Endpoint != "work-host" || challenge.Prompt != "Password:" || challenge.Kind != PromptSecret {
		t.Fatalf("challenge = %#v", challenge)
	}
	resolution := []byte("correct horse")
	if err := broker.Resolve(2, challenge.ID, Resolution{Answer: resolution}); !errors.Is(err, ErrNotOwner) {
		t.Fatalf("wrong-owner Resolve error = %v, want ErrNotOwner", err)
	}
	if err := broker.Resolve(1, challenge.ID, Resolution{Answer: resolution}); err != nil {
		t.Fatal(err)
	}
	resolution[0] = 'X'
	result := <-answer
	if result.err != nil || string(result.answer) != "correct horse" {
		t.Fatalf("Prompt result = %q, %v", result.answer, result.err)
	}
	if err := broker.Resolve(1, challenge.ID, Resolution{Answer: []byte("again")}); !errors.Is(err, ErrChallengeNotFound) {
		t.Fatalf("second Resolve error = %v, want ErrChallengeNotFound", err)
	}
}

func TestBrokerNoClientPromptExpires(t *testing.T) {
	broker := newTestBroker(t, 4)
	attempt, err := broker.BeginAttempt(context.Background(), "work-host", 30*time.Millisecond)
	if err != nil {
		t.Fatal(err)
	}
	defer attempt.Close()
	_, err = broker.Prompt(context.Background(), attempt.Token(), "Password:", PromptSecret)
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("Prompt error = %v, want deadline exceeded", err)
	}
}

func TestBrokerAssignsChallengeToExactlyOneRacingOwner(t *testing.T) {
	broker := newTestBroker(t, 4)
	attempt, err := broker.BeginAttempt(context.Background(), "work-host", time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer attempt.Close()
	promptDone := make(chan error, 1)
	go func() {
		_, promptErr := broker.Prompt(context.Background(), attempt.Token(), "Password:", PromptSecret)
		promptDone <- promptErr
	}()
	type claimResult struct {
		owner     OwnerID
		challenge Challenge
		err       error
	}
	claims := make(chan claimResult, 2)
	for _, owner := range []OwnerID{1, 2} {
		go func(owner OwnerID) {
			ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
			defer cancel()
			challenge, claimErr := broker.Claim(ctx, owner)
			claims <- claimResult{owner: owner, challenge: challenge, err: claimErr}
		}(owner)
	}
	var winner claimResult
	losers := 0
	for range 2 {
		result := <-claims
		if result.err == nil {
			if winner.owner != 0 {
				t.Fatalf("two owners claimed one challenge: %d and %d", winner.owner, result.owner)
			}
			winner = result
		} else if errors.Is(result.err, context.DeadlineExceeded) {
			losers++
		} else {
			t.Fatalf("Claim error = %v", result.err)
		}
	}
	if winner.owner == 0 || losers != 1 {
		t.Fatalf("winner/losers = %d/%d, want one each", winner.owner, losers)
	}
	if err := broker.Resolve(winner.owner, winner.challenge.ID, Resolution{Answer: []byte("answer")}); err != nil {
		t.Fatal(err)
	}
	if err := <-promptDone; err != nil {
		t.Fatal(err)
	}
}

func TestBrokerDetachRequeuesClaimForAnotherOwner(t *testing.T) {
	broker := newTestBroker(t, 4)
	attempt, err := broker.BeginAttempt(context.Background(), "work-host", time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer attempt.Close()
	done := make(chan error, 1)
	go func() {
		_, promptErr := broker.Prompt(context.Background(), attempt.Token(), "Password:", PromptSecret)
		done <- promptErr
	}()
	first, err := broker.Claim(context.Background(), 1)
	if err != nil {
		t.Fatal(err)
	}
	broker.Detach(1)
	second, err := broker.Claim(context.Background(), 2)
	if err != nil {
		t.Fatal(err)
	}
	if second.ID != first.ID {
		t.Fatalf("requeued challenge ID = %q, want %q", second.ID, first.ID)
	}
	if err := broker.Resolve(2, second.ID, Resolution{Cancel: true}); err != nil {
		t.Fatal(err)
	}
	if err := <-done; !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled Prompt error = %v, want canceled", err)
	}
}

func TestBrokerAttemptCancellationWakesPrompt(t *testing.T) {
	broker := newTestBroker(t, 4)
	ctx, cancel := context.WithCancel(context.Background())
	attempt, err := broker.BeginAttempt(ctx, "work-host", time.Second)
	if err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() {
		_, promptErr := broker.Prompt(context.Background(), attempt.Token(), "Password:", PromptSecret)
		done <- promptErr
	}()
	if _, err := broker.Claim(context.Background(), 1); err != nil {
		t.Fatal(err)
	}
	cancel()
	if err := <-done; !errors.Is(err, context.Canceled) {
		t.Fatalf("Prompt error = %v, want canceled", err)
	}
	if _, err := broker.Prompt(context.Background(), attempt.Token(), "again", PromptSecret); !errors.Is(err, ErrAttemptNotFound) {
		t.Fatalf("closed attempt error = %v, want ErrAttemptNotFound", err)
	}
}

func TestBrokerCapsPromptsPerAttempt(t *testing.T) {
	broker := newTestBroker(t, 1)
	attempt, err := broker.BeginAttempt(context.Background(), "work-host", time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer attempt.Close()
	firstDone := make(chan error, 1)
	go func() {
		_, promptErr := broker.Prompt(context.Background(), attempt.Token(), "first", PromptSecret)
		firstDone <- promptErr
	}()
	challenge, err := broker.Claim(context.Background(), 1)
	if err != nil {
		t.Fatal(err)
	}
	if err := broker.Resolve(1, challenge.ID, Resolution{Answer: []byte("answer")}); err != nil {
		t.Fatal(err)
	}
	if err := <-firstDone; err != nil {
		t.Fatal(err)
	}
	if _, err := broker.Prompt(context.Background(), attempt.Token(), "second", PromptSecret); !errors.Is(err, ErrPromptLimit) {
		t.Fatalf("second Prompt error = %v, want ErrPromptLimit", err)
	}
}

type testPromptResult struct {
	answer []byte
	err    error
}

func newTestBroker(t *testing.T, maxPrompts int) *Broker {
	t.Helper()
	broker, err := NewBroker(Config{MaxPrompts: maxPrompts})
	if err != nil {
		t.Fatal(err)
	}
	return broker
}
