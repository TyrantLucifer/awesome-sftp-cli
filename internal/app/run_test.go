package app_test

import (
	"bytes"
	"context"
	"errors"
	"io"
	"reflect"
	"strings"
	"testing"

	"github.com/TyrantLucifer/awesome-sftp-cli/internal/app"
	"github.com/TyrantLucifer/awesome-sftp-cli/internal/buildinfo"
)

func TestRunDispatchesOnlyTheSelectedRole(t *testing.T) {
	tests := []struct {
		name     string
		args     []string
		wantRole app.Role
		wantArgs []string
	}{
		{name: "default client", wantRole: app.RoleClient},
		{name: "single location", args: []string{"devbox:/remote/path"}, wantRole: app.RoleClient, wantArgs: []string{"devbox:/remote/path"}},
		{name: "two pane locations", args: []string{"/local/path", "devbox:/remote/path"}, wantRole: app.RoleClient, wantArgs: []string{"/local/path", "devbox:/remote/path"}},
		{name: "saved workspace", args: []string{"--workspace", "release"}, wantRole: app.RoleClient, wantArgs: []string{"--workspace", "release"}},
		{name: "client option", args: []string{"--future-client-option"}, wantRole: app.RoleClient, wantArgs: []string{"--future-client-option"}},
		{name: "role-like location remains client", args: []string{"daemno"}, wantRole: app.RoleClient, wantArgs: []string{"daemno"}},
		{name: "explicit client", args: []string{"client", "--workspace", "release"}, wantRole: app.RoleClient, wantArgs: []string{"--workspace", "release"}},
		{name: "daemon", args: []string{"daemon", "--socket", "test.sock"}, wantRole: app.RoleDaemon, wantArgs: []string{"--socket", "test.sock"}},
		{name: "askpass", args: []string{"askpass", "Password:"}, wantRole: app.RoleAskpass, wantArgs: []string{"Password:"}},
		{name: "helper", args: []string{"helper", "serve"}, wantRole: app.RoleHelper, wantArgs: []string{"serve"}},
		{name: "job", args: []string{"job", "list", "--limit", "5"}, wantRole: app.RoleJob, wantArgs: []string{"list", "--limit", "5"}},
		{name: "config", args: []string{"config", "validate"}, wantRole: app.RoleConfig, wantArgs: []string{"validate"}},
		{name: "doctor", args: []string{"doctor", "--format", "json"}, wantRole: app.RoleDoctor, wantArgs: []string{"--format", "json"}},
		{name: "support bundle", args: []string{"support-bundle", "preview", "--format", "json"}, wantRole: app.RoleSupportBundle, wantArgs: []string{"preview", "--format", "json"}},
		{name: "completion", args: []string{"completion", "zsh"}, wantRole: app.RoleCompletion, wantArgs: []string{"zsh"}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			type contextKey string
			const key contextKey = "dispatch-test"
			ctx := context.WithValue(context.Background(), key, tt.name)

			var gotRole app.Role
			var gotArgs []string
			record := func(role app.Role) app.Handler {
				return func(gotCtx context.Context, args []string, _ io.Writer, _ io.Writer) error {
					if gotCtx.Value(key) != tt.name {
						t.Fatal("handler did not receive the supplied context")
					}
					gotRole = role
					gotArgs = append([]string(nil), args...)
					return nil
				}
			}
			handlers := app.Handlers{
				Client:        record(app.RoleClient),
				Daemon:        record(app.RoleDaemon),
				Askpass:       record(app.RoleAskpass),
				Helper:        record(app.RoleHelper),
				Job:           record(app.RoleJob),
				Config:        record(app.RoleConfig),
				Doctor:        record(app.RoleDoctor),
				SupportBundle: record(app.RoleSupportBundle),
				Completion:    record(app.RoleCompletion),
			}

			var stdout bytes.Buffer
			var stderr bytes.Buffer
			if got := app.Run(ctx, tt.args, &stdout, &stderr, handlers); got != 0 {
				t.Fatalf("exit code = %d, stderr = %q", got, stderr.String())
			}
			if gotRole != tt.wantRole {
				t.Fatalf("called role = %q, want %q", gotRole, tt.wantRole)
			}
			if !reflect.DeepEqual(gotArgs, tt.wantArgs) {
				t.Fatalf("handler args = %#v, want %#v", gotArgs, tt.wantArgs)
			}
			if stderr.Len() != 0 {
				t.Fatalf("unexpected stderr: %q", stderr.String())
			}
		})
	}
}

func TestRunReturnsStableTypedExitCode(t *testing.T) {
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	handlers := app.Handlers{
		Config: func(context.Context, []string, io.Writer, io.Writer) error {
			return app.NewExitError(app.ExitConfig, errors.New("invalid configuration"))
		},
	}

	if got := app.Run(context.Background(), []string{"config", "validate"}, &stdout, &stderr, handlers); got != int(app.ExitConfig) {
		t.Fatalf("exit code = %d, want %d", got, app.ExitConfig)
	}
	if !strings.Contains(stderr.String(), "invalid configuration") {
		t.Fatalf("stderr = %q", stderr.String())
	}
}

func TestRunReturnsHandlerFailureExitCode(t *testing.T) {
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	handlers := app.Handlers{
		Helper: func(context.Context, []string, io.Writer, io.Writer) error {
			return errors.New("sentinel handler failure")
		},
	}

	if got := app.Run(context.Background(), []string{"helper"}, &stdout, &stderr, handlers); got != 1 {
		t.Fatalf("exit code = %d, want 1", got)
	}
	for _, want := range []string{"helper", "sentinel handler failure"} {
		if !strings.Contains(stderr.String(), want) {
			t.Fatalf("stderr %q does not contain %q", stderr.String(), want)
		}
	}
}

func TestRunFailsWhenSelectedRoleHasNoHandler(t *testing.T) {
	var stdout bytes.Buffer
	var stderr bytes.Buffer

	if got := app.Run(context.Background(), []string{"daemon"}, &stdout, &stderr, app.Handlers{}); got != 1 {
		t.Fatalf("exit code = %d, want 1", got)
	}
	for _, want := range []string{"daemon", "handler"} {
		if !strings.Contains(strings.ToLower(stderr.String()), want) {
			t.Fatalf("stderr %q does not contain %q", stderr.String(), want)
		}
	}
}

func TestRunPrintsVersionWithoutCallingAHandler(t *testing.T) {
	called := false
	handlers := app.Handlers{
		Client: func(context.Context, []string, io.Writer, io.Writer) error {
			called = true
			return nil
		},
	}
	var stdout bytes.Buffer
	var stderr bytes.Buffer

	if got := app.Run(context.Background(), []string{"--version"}, &stdout, &stderr, handlers); got != 0 {
		t.Fatalf("exit code = %d, stderr = %q", got, stderr.String())
	}
	if called {
		t.Fatal("handler was called for --version")
	}
	info := buildinfo.Current()
	for _, want := range []string{info.Version, info.GoVersion, info.GOOS, info.GOARCH} {
		if !strings.Contains(stdout.String(), want) {
			t.Fatalf("version output %q does not contain %q", stdout.String(), want)
		}
	}
	if stderr.Len() != 0 {
		t.Fatalf("unexpected stderr: %q", stderr.String())
	}
}

func TestRunPrintsHelpWithoutCallingAHandler(t *testing.T) {
	called := false
	handlers := app.Handlers{
		Client: func(context.Context, []string, io.Writer, io.Writer) error {
			called = true
			return nil
		},
	}
	var stdout bytes.Buffer
	var stderr bytes.Buffer

	if got := app.Run(context.Background(), []string{"--help"}, &stdout, &stderr, handlers); got != 0 {
		t.Fatalf("exit code = %d, stderr = %q", got, stderr.String())
	}
	if called {
		t.Fatal("handler was called for --help")
	}
	for _, want := range []string{"usage", "<location>", "--workspace <name>"} {
		if !strings.Contains(strings.ToLower(stdout.String()), want) {
			t.Fatalf("help output %q does not contain %q", stdout.String(), want)
		}
	}
	if stderr.Len() != 0 {
		t.Fatalf("unexpected stderr: %q", stderr.String())
	}
}
