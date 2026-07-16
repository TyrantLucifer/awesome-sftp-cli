package auth

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"sync"
	"time"
)

var (
	ErrAttemptNotFound   = errors.New("authentication attempt not found")
	ErrChallengeNotFound = errors.New("authentication challenge not found")
	ErrNotOwner          = errors.New("authentication challenge belongs to another client")
	ErrPromptLimit       = errors.New("authentication prompt limit reached")
)

type Token string
type ChallengeID string
type OwnerID uint64
type PromptKind string

const (
	PromptSecret  PromptKind = "secret"
	PromptConfirm PromptKind = "confirm"
	PromptUnknown PromptKind = "unknown"
)

type Challenge struct {
	ID        ChallengeID
	Endpoint  string
	Kind      PromptKind
	Prompt    string
	ExpiresAt time.Time
}

type Resolution struct {
	Answer []byte
	Cancel bool
}

type Config struct {
	MaxPrompts int
	Random     io.Reader
}

type Broker struct {
	mu         sync.Mutex
	maxPrompts int
	random     io.Reader
	attempts   map[Token]*attemptState
	challenges map[ChallengeID]*challengeState
	order      []ChallengeID
	changed    chan struct{}
}

type attemptState struct {
	token    Token
	endpoint string
	ctx      context.Context
	cancel   context.CancelFunc
	deadline time.Time
	prompts  int
}

type challengeState struct {
	challenge Challenge
	attempt   Token
	owner     OwnerID
	result    chan promptResult
}

type promptResult struct {
	answer []byte
	err    error
}

type Attempt struct {
	broker *Broker
	token  Token
	once   sync.Once
}

func NewBroker(config Config) (*Broker, error) {
	if config.MaxPrompts < 1 {
		return nil, errors.New("create authentication broker: maximum prompts must be positive")
	}
	random := config.Random
	if random == nil {
		random = rand.Reader
	}
	return &Broker{maxPrompts: config.MaxPrompts, random: random, attempts: make(map[Token]*attemptState), challenges: make(map[ChallengeID]*challengeState), changed: make(chan struct{})}, nil
}

func (b *Broker) BeginAttempt(ctx context.Context, endpoint string, timeout time.Duration) (*Attempt, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if endpoint == "" || timeout <= 0 {
		return nil, errors.New("begin authentication attempt: endpoint and positive timeout are required")
	}
	tokenValue, err := b.randomID(32)
	if err != nil {
		return nil, err
	}
	token := Token(tokenValue)
	attemptCtx, cancel := context.WithTimeout(ctx, timeout)
	deadline, _ := attemptCtx.Deadline()
	state := &attemptState{token: token, endpoint: endpoint, ctx: attemptCtx, cancel: cancel, deadline: deadline}
	b.mu.Lock()
	if _, exists := b.attempts[token]; exists {
		b.mu.Unlock()
		cancel()
		return nil, errors.New("begin authentication attempt: random token collision")
	}
	b.attempts[token] = state
	b.notifyLocked()
	b.mu.Unlock()
	go func() {
		<-attemptCtx.Done()
		b.closeAttempt(token, attemptCtx.Err())
	}()
	return &Attempt{broker: b, token: token}, nil
}

func (a *Attempt) Token() Token {
	if a == nil {
		return ""
	}
	return a.token
}

func (a *Attempt) Close() error {
	if a == nil {
		return nil
	}
	a.once.Do(func() { a.broker.closeAttempt(a.token, context.Canceled) })
	return nil
}

func (b *Broker) Prompt(ctx context.Context, token Token, prompt string, kind PromptKind) ([]byte, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if token == "" || prompt == "" || !validPromptKind(kind) {
		return nil, errors.New("create authentication challenge: invalid request")
	}
	idValue, err := b.randomID(18)
	if err != nil {
		return nil, err
	}
	id := ChallengeID(idValue)
	b.mu.Lock()
	attempt := b.attempts[token]
	if attempt == nil || attempt.ctx.Err() != nil {
		b.mu.Unlock()
		if attempt != nil {
			b.closeAttempt(token, attempt.ctx.Err())
		}
		return nil, ErrAttemptNotFound
	}
	if attempt.prompts >= b.maxPrompts {
		b.mu.Unlock()
		return nil, ErrPromptLimit
	}
	if _, exists := b.challenges[id]; exists {
		b.mu.Unlock()
		return nil, errors.New("create authentication challenge: random ID collision")
	}
	attempt.prompts++
	state := &challengeState{challenge: Challenge{ID: id, Endpoint: attempt.endpoint, Kind: kind, Prompt: prompt, ExpiresAt: attempt.deadline}, attempt: token, result: make(chan promptResult, 1)}
	b.challenges[id] = state
	b.order = append(b.order, id)
	b.notifyLocked()
	b.mu.Unlock()

	select {
	case result := <-state.result:
		return result.answer, result.err
	case <-ctx.Done():
		b.abandonChallenge(id)
		return nil, ctx.Err()
	case <-attempt.ctx.Done():
		b.abandonChallenge(id)
		return nil, attempt.ctx.Err()
	}
}

func (b *Broker) Claim(ctx context.Context, owner OwnerID) (Challenge, error) {
	if owner == 0 {
		return Challenge{}, errors.New("claim authentication challenge: owner is empty")
	}
	for {
		if err := ctx.Err(); err != nil {
			return Challenge{}, err
		}
		b.mu.Lock()
		for _, state := range b.challenges {
			if state.owner == owner {
				challenge := state.challenge
				b.mu.Unlock()
				return challenge, nil
			}
		}
		for _, id := range b.order {
			state := b.challenges[id]
			if state != nil && state.owner == 0 {
				state.owner = owner
				challenge := state.challenge
				b.mu.Unlock()
				return challenge, nil
			}
		}
		changed := b.changed
		b.mu.Unlock()
		select {
		case <-ctx.Done():
			return Challenge{}, ctx.Err()
		case <-changed:
		}
	}
}

func (b *Broker) Resolve(owner OwnerID, id ChallengeID, resolution Resolution) error {
	if owner == 0 || id == "" || resolution.Cancel && len(resolution.Answer) != 0 {
		return errors.New("resolve authentication challenge: invalid request")
	}
	b.mu.Lock()
	state := b.challenges[id]
	if state == nil {
		b.mu.Unlock()
		return ErrChallengeNotFound
	}
	if state.owner != owner {
		b.mu.Unlock()
		return ErrNotOwner
	}
	b.deleteChallengeLocked(id)
	b.notifyLocked()
	b.mu.Unlock()
	if resolution.Cancel {
		state.result <- promptResult{err: context.Canceled}
		return nil
	}
	answer := append([]byte(nil), resolution.Answer...)
	state.result <- promptResult{answer: answer}
	return nil
}

func (b *Broker) Detach(owner OwnerID) {
	if owner == 0 {
		return
	}
	b.mu.Lock()
	changed := false
	for _, state := range b.challenges {
		if state.owner == owner {
			state.owner = 0
			changed = true
		}
	}
	if changed {
		b.notifyLocked()
	}
	b.mu.Unlock()
}

func (b *Broker) closeAttempt(token Token, cause error) {
	b.mu.Lock()
	attempt := b.attempts[token]
	if attempt == nil {
		b.mu.Unlock()
		return
	}
	delete(b.attempts, token)
	states := make([]*challengeState, 0)
	for id, state := range b.challenges {
		if state.attempt == token {
			b.deleteChallengeLocked(id)
			states = append(states, state)
		}
	}
	b.notifyLocked()
	b.mu.Unlock()
	attempt.cancel()
	if cause == nil {
		cause = context.Canceled
	}
	for _, state := range states {
		state.result <- promptResult{err: cause}
	}
}

func (b *Broker) abandonChallenge(id ChallengeID) {
	b.mu.Lock()
	if b.challenges[id] != nil {
		b.deleteChallengeLocked(id)
		b.notifyLocked()
	}
	b.mu.Unlock()
}

func (b *Broker) notifyLocked() {
	close(b.changed)
	b.changed = make(chan struct{})
}

func (b *Broker) deleteChallengeLocked(id ChallengeID) {
	delete(b.challenges, id)
	for index, orderedID := range b.order {
		if orderedID == id {
			b.order = append(b.order[:index], b.order[index+1:]...)
			return
		}
	}
}

func (b *Broker) randomID(size int) (string, error) {
	value := make([]byte, size)
	if _, err := io.ReadFull(b.random, value); err != nil {
		return "", fmt.Errorf("generate authentication identifier: %w", err)
	}
	return base64.RawURLEncoding.EncodeToString(value), nil
}

func validPromptKind(kind PromptKind) bool {
	return kind == PromptSecret || kind == PromptConfirm || kind == PromptUnknown
}
