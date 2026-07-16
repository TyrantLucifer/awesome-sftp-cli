package ipc

import (
	"encoding/base64"
	"errors"
	"strings"
	"time"
	"unicode/utf8"
)

const (
	MaxAuthPromptBytes = 4096
	MaxAuthAnswerBytes = 4096
	AuthActionAnswer   = "answer"
	AuthActionCancel   = "cancel"
)

type AuthPromptRequest struct {
	AttemptToken string `json:"attempt_token"`
	Prompt       string `json:"prompt"`
	Kind         string `json:"kind"`
}

type AuthPromptResponse struct {
	Answer string `json:"answer"`
}

type AuthClaimRequest struct{}

type AuthClaimResponse struct {
	ChallengeID string    `json:"challenge_id"`
	Endpoint    string    `json:"endpoint"`
	Prompt      string    `json:"prompt"`
	Kind        string    `json:"kind"`
	ExpiresAt   time.Time `json:"expires_at"`
}

type AuthResolveRequest struct {
	ChallengeID string `json:"challenge_id"`
	Action      string `json:"action"`
	Answer      string `json:"answer,omitempty"`
}

type AuthResolveResponse struct{}

func ValidateAuthPromptRequest(request AuthPromptRequest) error {
	if !validEncodedAuthID(request.AttemptToken, 32) {
		return errors.New("validate authentication prompt: invalid attempt token")
	}
	if request.Prompt == "" || len(request.Prompt) > MaxAuthPromptBytes || !utf8.ValidString(request.Prompt) || strings.IndexByte(request.Prompt, 0) >= 0 {
		return errors.New("validate authentication prompt: invalid prompt")
	}
	if request.Kind != "secret" && request.Kind != "confirm" && request.Kind != "unknown" {
		return errors.New("validate authentication prompt: invalid kind")
	}
	return nil
}

func ValidateAuthResolveRequest(request AuthResolveRequest) error {
	if !validEncodedAuthID(request.ChallengeID, 18) {
		return errors.New("validate authentication response: invalid challenge ID")
	}
	switch request.Action {
	case AuthActionCancel:
		if request.Answer != "" {
			return errors.New("validate authentication response: cancel cannot include an answer")
		}
	case AuthActionAnswer:
		if len(request.Answer) > MaxAuthAnswerBytes || !utf8.ValidString(request.Answer) || strings.ContainsAny(request.Answer, "\x00\r\n") {
			return errors.New("validate authentication response: invalid answer")
		}
	default:
		return errors.New("validate authentication response: invalid action")
	}
	return nil
}

func validEncodedAuthID(value string, decodedBytes int) bool {
	decoded, err := base64.RawURLEncoding.DecodeString(value)
	return err == nil && len(decoded) == decodedBytes
}
