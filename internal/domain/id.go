package domain

import (
	"crypto/rand"
	"encoding/base32"
	"fmt"
	"io"
	"sync"
)

const (
	endpointIDPrefix = "ep_"
	sessionIDPrefix  = "sess_"
	requestIDPrefix  = "req_"
	jobIDPrefix      = "job_"
	eventIDPrefix    = "evt_"

	idRandomBytes  = 16
	idEncodedBytes = 26
)

var idEncoding = base32.NewEncoding("abcdefghijklmnopqrstuvwxyz234567").WithPadding(base32.NoPadding)

type EndpointID string

type SessionID string

type RequestID string

type JobID string

type EventID string

type Generator interface {
	New(prefix string) (string, error)
}

type RandomGenerator struct {
	Reader io.Reader

	mu sync.Mutex
}

func (g *RandomGenerator) New(prefix string) (string, error) {
	g.mu.Lock()
	defer g.mu.Unlock()

	reader := g.Reader
	if reader == nil {
		reader = rand.Reader
	}

	random := make([]byte, idRandomBytes)
	if _, err := io.ReadFull(reader, random); err != nil {
		return "", fmt.Errorf("generate %q ID: read randomness: %w", prefix, err)
	}

	return prefix + idEncoding.EncodeToString(random), nil
}

func NewEndpointID(generator Generator) (EndpointID, error) {
	value, err := newID(generator, endpointIDPrefix)
	return EndpointID(value), err
}

func ParseEndpointID(value string) (EndpointID, error) {
	parsed, err := parseID(value, endpointIDPrefix)
	return EndpointID(parsed), err
}

func NewSessionID(generator Generator) (SessionID, error) {
	value, err := newID(generator, sessionIDPrefix)
	return SessionID(value), err
}

func ParseSessionID(value string) (SessionID, error) {
	parsed, err := parseID(value, sessionIDPrefix)
	return SessionID(parsed), err
}

func NewRequestID(generator Generator) (RequestID, error) {
	value, err := newID(generator, requestIDPrefix)
	return RequestID(value), err
}

func ParseRequestID(value string) (RequestID, error) {
	parsed, err := parseID(value, requestIDPrefix)
	return RequestID(parsed), err
}

func NewJobID(generator Generator) (JobID, error) {
	value, err := newID(generator, jobIDPrefix)
	return JobID(value), err
}

func ParseJobID(value string) (JobID, error) {
	parsed, err := parseID(value, jobIDPrefix)
	return JobID(parsed), err
}

func NewEventID(generator Generator) (EventID, error) {
	value, err := newID(generator, eventIDPrefix)
	return EventID(value), err
}

func ParseEventID(value string) (EventID, error) {
	parsed, err := parseID(value, eventIDPrefix)
	return EventID(parsed), err
}

func newID(generator Generator, prefix string) (string, error) {
	if generator == nil {
		return "", fmt.Errorf("generate %q ID: nil generator", prefix)
	}

	value, err := generator.New(prefix)
	if err != nil {
		return "", fmt.Errorf("generate %q ID: %w", prefix, err)
	}
	return parseID(value, prefix)
}

func parseID(value string, prefix string) (string, error) {
	if len(value) != len(prefix)+idEncodedBytes || value[:len(prefix)] != prefix {
		return "", fmt.Errorf("parse %q ID: expected %q prefix and %d base32 characters", prefix, prefix, idEncodedBytes)
	}

	decoded, err := idEncoding.DecodeString(value[len(prefix):])
	if err != nil || len(decoded) != idRandomBytes {
		return "", fmt.Errorf("parse %q ID: invalid lowercase base32 payload", prefix)
	}
	return value, nil
}
