package tui

import (
	"strings"
	"testing"

	"github.com/gdamore/tcell/v3"
)

func TestCommandModeFreezesPaneContextOnlyAfterExplicitConfirmation(t *testing.T) {
	model := testModel(t)
	active := model.Panes[model.Active]

	model, intents := Reduce(model, KeyPress{Key: KeyCommand})
	if model.Mode != ModeCommand || len(intents) != 0 {
		t.Fatalf("open command mode = mode %q intents %#v", model.Mode, intents)
	}
	model, intents = Reduce(model, TextInput{Text: "printf hello"})
	if model.Mode != ModeCommand || len(intents) != 0 {
		t.Fatalf("type command = mode %q intents %#v", model.Mode, intents)
	}
	model, intents = Reduce(model, KeyPress{Key: KeySubmit})
	if model.Mode != ModeCommandConfirm || len(intents) != 0 {
		t.Fatalf("first submit = mode %q intents %#v", model.Mode, intents)
	}
	model, intents = Reduce(model, KeyPress{Key: KeySubmit})
	if model.Mode != ModeNormal || len(intents) != 1 {
		t.Fatalf("confirmed command = mode %q intents %#v", model.Mode, intents)
	}
	intent := intents[0]
	if intent.Kind != IntentRunCommand || intent.Pane != model.Active || intent.Location != active.Location || intent.Endpoint != active.Endpoint || intent.CommandText != "printf hello" {
		t.Fatalf("command intent = %#v", intent)
	}
}

func TestCommandModeRejectsMultilineAndBoundsInput(t *testing.T) {
	model := testModel(t)
	model, _ = Reduce(model, KeyPress{Key: KeyCommand})
	model, _ = Reduce(model, TextInput{Text: "first\nsecond"})
	model, _ = Reduce(model, TextInput{Text: strings.Repeat("x", CommandByteLimit+1)})
	model, intents := Reduce(model, KeyPress{Key: KeySubmit})
	if model.Mode != ModeCommand || len(intents) != 0 || model.Notice == "" {
		t.Fatalf("invalid command = mode %q intents %#v notice %q", model.Mode, intents, model.Notice)
	}
}

func TestGoShellSequenceFreezesActivePaneWithoutPathFallback(t *testing.T) {
	model := testModel(t)
	active := model.Panes[model.Active]
	model, _ = Reduce(model, KeyPress{Key: KeyPath})
	model, intents := Reduce(model, TextInput{Text: "s"})
	if model.Mode != ModeNormal || len(intents) != 1 {
		t.Fatalf("gs = mode %q intents %#v", model.Mode, intents)
	}
	intent := intents[0]
	if intent.Kind != IntentShell || intent.Pane != model.Active || intent.Location != active.Location || intent.Endpoint != active.Endpoint {
		t.Fatalf("shell intent = %#v", intent)
	}
}

func TestGoShellHomeFallbackIsExplicit(t *testing.T) {
	model := testModel(t)
	model, _ = Reduce(model, KeyPress{Key: KeyPath})
	model, intents := Reduce(model, TextInput{Text: "S"})
	if model.Mode != ModeNormal || len(intents) != 1 || intents[0].Kind != IntentShell || !intents[0].ShellHome {
		t.Fatalf("gS = mode %q intents %#v", model.Mode, intents)
	}
}

func TestTranslateCommandAndRenderConfirmationContext(t *testing.T) {
	action, ok := TranslateTCellEvent(tcell.NewEventKey(tcell.KeyRune, "!", tcell.ModNone), ModeNormal)
	if !ok || action != (KeyPress{Key: KeyCommand}) {
		t.Fatalf("translate ! = %#v, %t", action, ok)
	}
	model := testModel(t)
	model, _ = Reduce(model, KeyPress{Key: KeyCommand})
	model, _ = Reduce(model, TextInput{Text: "true"})
	model, _ = Reduce(model, KeyPress{Key: KeySubmit})
	surface := newMemorySurface(100, 16)
	Render(surface, model, RenderOptions{Overscan: 1})
	got := surface.String()
	for _, want := range []string{"Confirm one-time command", "true", string(model.Panes[model.Active].Location.Path), "local shell -c", "[Enter] run"} {
		if !strings.Contains(got, want) {
			t.Fatalf("command modal missing %q:\n%s", want, got)
		}
	}
}

func TestRemoteCommandConfirmationShowsFreshTransportAndNoFallback(t *testing.T) {
	model := testModel(t)
	pane := model.Panes[model.Active]
	pane.Endpoint.Kind = "ssh"
	pane.Endpoint.DisplayName = "example-remote"
	model.Panes[model.Active] = pane
	model, _ = Reduce(model, KeyPress{Key: KeyCommand})
	model, _ = Reduce(model, TextInput{Text: "true"})
	model, _ = Reduce(model, KeyPress{Key: KeySubmit})
	surface := newMemorySurface(100, 16)
	Render(surface, model, RenderOptions{Overscan: 1})
	got := surface.String()
	for _, want := range []string{"fresh ssh -T", "cwd marker", "no fallback"} {
		if !strings.Contains(got, want) {
			t.Fatalf("remote command modal missing %q:\n%s", want, got)
		}
	}
}

func TestCommandCompletionIsBoundedVisibleAndRefreshesFrozenPane(t *testing.T) {
	model := testModel(t)
	location := model.Panes[Left].Location
	model, intents := Reduce(model, CommandCompleted{
		Pane: Left, Location: location, ExitCode: 7,
		Stdout: []byte("result\nsecond line"), StdoutDiscarded: 4096,
	})
	if len(intents) != 1 || intents[0].Kind != IntentList || intents[0].Pane != Left || intents[0].Location != location {
		t.Fatalf("completion intents = %#v", intents)
	}
	for _, want := range []string{"exit 7", "result", "4096 bytes discarded"} {
		if !strings.Contains(model.Notice, want) {
			t.Fatalf("notice %q missing %q", model.Notice, want)
		}
	}
}

func TestOneTimeCommandIsSingleFlightAndEscapeCancelsTheActiveRun(t *testing.T) {
	model := testModel(t)
	model, _ = Reduce(model, KeyPress{Key: KeyCommand})
	model, _ = Reduce(model, TextInput{Text: "sleep 60"})
	model, _ = Reduce(model, KeyPress{Key: KeySubmit})
	model, intents := Reduce(model, KeyPress{Key: KeySubmit})
	if !model.CommandRunning || len(intents) != 1 || intents[0].Kind != IntentRunCommand {
		t.Fatalf("confirmed command = running %t intents %#v", model.CommandRunning, intents)
	}
	model, intents = Reduce(model, KeyPress{Key: KeyCommand})
	if len(intents) != 0 || model.Mode != ModeNormal || !strings.Contains(model.Notice, "already running") {
		t.Fatalf("second command = mode %q intents %#v notice %q", model.Mode, intents, model.Notice)
	}
	model, intents = Reduce(model, KeyPress{Key: KeyEscape})
	if len(intents) != 1 || intents[0].Kind != IntentCommandCancel || !model.CommandRunning {
		t.Fatalf("cancel command = running %t intents %#v", model.CommandRunning, intents)
	}
	model, _ = Reduce(model, CommandCompleted{Pane: Left, Location: model.Panes[Left].Location, Message: "command canceled"})
	if model.CommandRunning {
		t.Fatal("command remained active after completion")
	}
}
