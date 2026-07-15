package app

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
	"unicode/utf8"

	"github.com/TyrantLucifer/awesome-mac-sftp/internal/auth"
	"github.com/TyrantLucifer/awesome-mac-sftp/internal/buildinfo"
	"github.com/TyrantLucifer/awesome-mac-sftp/internal/daemon"
	"github.com/TyrantLucifer/awesome-mac-sftp/internal/ipc"
	"github.com/TyrantLucifer/awesome-mac-sftp/internal/platform"
)

const daemonAuthPromptName = daemon.AuthPrompt

type authRPCClient interface {
	Call(context.Context, string, any, any) error
	Close() error
}

func runAskpass(ctx context.Context, args []string, stdout, _ io.Writer) error {
	if _, err := newAuthPromptRequest(args, os.Getenv); err != nil {
		return err
	}
	client, err := connectExistingAuthDaemon(ctx)
	if err != nil {
		return errors.New("authentication prompt is unavailable")
	}
	return runAskpassWith(ctx, args, stdout, client, os.Getenv)
}

func runAskpassWith(ctx context.Context, args []string, stdout io.Writer, client authRPCClient, getenv func(string) string) error {
	request, err := newAuthPromptRequest(args, getenv)
	if err != nil {
		return err
	}
	if client == nil {
		return errors.New("authentication prompt is unavailable")
	}
	defer client.Close()
	var response ipc.AuthPromptResponse
	if err := client.Call(ctx, daemonAuthPromptName, request, &response); err != nil {
		return errors.New("authentication prompt failed")
	}
	if len(response.Answer) > ipc.MaxAuthAnswerBytes || !utf8.ValidString(response.Answer) || strings.ContainsAny(response.Answer, "\x00\r\n") {
		return errors.New("authentication prompt returned an invalid answer")
	}
	answer := append([]byte(response.Answer), '\n')
	defer clear(answer)
	if _, err := stdout.Write(answer); err != nil {
		return fmt.Errorf("write authentication answer: %w", err)
	}
	return nil
}

func newAuthPromptRequest(args []string, getenv func(string) string) (ipc.AuthPromptRequest, error) {
	if len(args) != 1 || getenv == nil {
		return ipc.AuthPromptRequest{}, errors.New("authentication prompt invocation is invalid")
	}
	kind := auth.PromptSecret
	if getenv("SSH_ASKPASS_PROMPT") == "confirm" {
		kind = auth.PromptConfirm
	}
	request := ipc.AuthPromptRequest{AttemptToken: getenv(auth.EnvAttemptToken), Prompt: args[0], Kind: string(kind)}
	if err := ipc.ValidateAuthPromptRequest(request); err != nil {
		return ipc.AuthPromptRequest{}, errors.New("authentication prompt invocation is invalid")
	}
	return request, nil
}

func connectExistingAuthDaemon(ctx context.Context) (*daemon.Client, error) {
	paths, purpose, err := runtimePaths()
	if err != nil {
		return nil, err
	}
	connection, err := platform.DialControlSocket(ctx, paths.ControlSocket, purpose)
	if err != nil {
		return nil, err
	}
	client, err := daemon.NewClient(ctx, connection, buildinfo.Current().String(), fmt.Sprintf("askpass-%d", os.Getpid()))
	if err != nil {
		_ = connection.Close()
		return nil, err
	}
	return client, nil
}
