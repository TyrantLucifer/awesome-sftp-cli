package app

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/TyrantLucifer/awesome-mac-sftp/internal/domain"
	"github.com/TyrantLucifer/awesome-mac-sftp/internal/tui"
)

func TestRunCommandIntentUsesFrozenLocalCWDAndBoundedDirectShellPlan(t *testing.T) {
	cwd := t.TempDir()
	t.Setenv("AMSFTP_COMMAND_SECRET", "must-not-leak")
	intent := tui.Intent{
		Kind: tui.IntentRunCommand, Pane: tui.Right,
		Endpoint:    domain.Endpoint{ID: "ep_aaaaaaaaaaaaaaaaaaaaaaaaaa", Kind: domain.EndpointLocal, DisplayName: "local"},
		Location:    domain.Location{EndpointID: "ep_aaaaaaaaaaaaaaaaaaaaaaaaaa", Path: domain.CanonicalPath(cwd)},
		CommandText: "pwd; printf ':%s' \"$AMSFTP_COMMAND_SECRET\"",
	}
	action := runCommandIntent(context.Background(), intent, append(os.Environ(), "SHELL=/bin/sh"))
	if action.Pane != tui.Right || action.Location != intent.Location || action.ExitCode != 0 || action.Message != "" {
		t.Fatalf("action = %#v", action)
	}
	canonicalCWD, err := filepath.EvalSymlinks(cwd)
	if err != nil {
		t.Fatal(err)
	}
	if got := string(action.Stdout); !strings.HasPrefix(got, filepath.Clean(canonicalCWD)+"\n:") || strings.Contains(got, "must-not-leak") {
		t.Fatalf("stdout = %q", got)
	}
}

func TestRunCommandIntentAppliesBoundedUserFlowTimeout(t *testing.T) {
	cwd := t.TempDir()
	intent := tui.Intent{
		Kind: tui.IntentRunCommand, Pane: tui.Left,
		Endpoint:    domain.Endpoint{ID: "ep_aaaaaaaaaaaaaaaaaaaaaaaaaa", Kind: domain.EndpointLocal, DisplayName: "local"},
		Location:    domain.Location{EndpointID: "ep_aaaaaaaaaaaaaaaaaaaaaaaaaa", Path: domain.CanonicalPath(cwd)},
		CommandText: "sleep 60",
	}
	started := time.Now()
	action := runCommandIntentWithTimeout(context.Background(), intent, append(os.Environ(), "SHELL=/bin/sh"), 50*time.Millisecond)
	if action.Message == "" || time.Since(started) > 2*time.Second {
		t.Fatalf("timeout action = %#v after %s", action, time.Since(started))
	}
}

func TestRunCommandIntentRejectsNonCommandAndUnknownEndpoint(t *testing.T) {
	for _, intent := range []tui.Intent{
		{Kind: tui.IntentList},
		{Kind: tui.IntentRunCommand, Endpoint: domain.Endpoint{Kind: "unknown"}, Location: domain.Location{Path: "/"}, CommandText: "true"},
	} {
		action := runCommandIntent(context.Background(), intent, os.Environ())
		if action.Message == "" {
			t.Fatalf("intent %#v unexpectedly succeeded", intent)
		}
	}
}
