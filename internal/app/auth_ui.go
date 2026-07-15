package app

import (
	"context"
	"errors"

	"github.com/TyrantLucifer/awesome-mac-sftp/internal/daemon"
	"github.com/TyrantLucifer/awesome-mac-sftp/internal/ipc"
	"github.com/TyrantLucifer/awesome-mac-sftp/internal/tui"
)

type authClaimRPC interface {
	Call(context.Context, string, any, any) error
}

func runAuthClaimLoop(ctx context.Context, client authClaimRPC, actions chan<- tui.Action, resolutions <-chan tui.Intent) error {
	if client == nil {
		return errors.New("authentication broker is unavailable")
	}
	for {
		var claim ipc.AuthClaimResponse
		if err := client.Call(ctx, daemon.AuthClaim, ipc.AuthClaimRequest{}, &claim); err != nil {
			if ctx.Err() != nil {
				return ctx.Err()
			}
			return &authRPCError{operation: "claim authentication challenge", cause: err}
		}
		if claim.ChallengeID == "" || claim.Endpoint == "" || claim.Prompt == "" {
			return errors.New("authentication broker returned an invalid challenge")
		}
		select {
		case actions <- tui.AuthChallengeReceived{ChallengeID: claim.ChallengeID, Endpoint: claim.Endpoint, Prompt: claim.Prompt, Kind: claim.Kind}:
		case <-ctx.Done():
			return ctx.Err()
		}
		select {
		case resolution := <-resolutions:
			if err := resolveAuthChallenge(ctx, client, claim.ChallengeID, resolution); err != nil {
				if ctx.Err() != nil {
					return ctx.Err()
				}
				return err
			}
		case <-ctx.Done():
			return ctx.Err()
		}
	}
}

type authRPCError struct {
	operation string
	cause     error
}

func (e *authRPCError) Error() string { return e.operation + " failed" }
func (e *authRPCError) Unwrap() error { return e.cause }

func resolveAuthChallenge(ctx context.Context, client authClaimRPC, challengeID string, resolution tui.Intent) error {
	answer := resolution.Answer
	defer clear(answer)
	if resolution.Kind != tui.IntentAuthResolve || resolution.ChallengeID != challengeID {
		return errors.New("authentication response does not match the active challenge")
	}
	request := ipc.AuthResolveRequest{ChallengeID: challengeID, Action: ipc.AuthActionAnswer, Answer: string(answer)}
	if resolution.Cancel {
		request.Action = ipc.AuthActionCancel
		request.Answer = ""
	}
	if err := ipc.ValidateAuthResolveRequest(request); err != nil {
		return errors.New("authentication response is invalid")
	}
	var response ipc.AuthResolveResponse
	if err := client.Call(ctx, daemon.AuthResolve, request, &response); err != nil {
		return &authRPCError{operation: "resolve authentication challenge", cause: err}
	}
	return nil
}
