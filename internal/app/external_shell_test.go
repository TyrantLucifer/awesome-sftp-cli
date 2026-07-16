package app

import (
	"reflect"
	"strings"
	"testing"

	"github.com/TyrantLucifer/awesome-mac-sftp/internal/terminalhandoff"
)

type fakeHandoffTCellScreen struct {
	calls []string
	w, h  int
}

func (screen *fakeHandoffTCellScreen) Size() (int, int) { return screen.w, screen.h }
func (screen *fakeHandoffTCellScreen) ShowCursor(_, _ int) {
	screen.calls = append(screen.calls, "show_cursor")
}
func (screen *fakeHandoffTCellScreen) Show() { screen.calls = append(screen.calls, "show") }
func (screen *fakeHandoffTCellScreen) Suspend() error {
	screen.calls = append(screen.calls, "suspend")
	return nil
}
func (screen *fakeHandoffTCellScreen) Resume() error {
	screen.calls = append(screen.calls, "resume")
	return nil
}
func (screen *fakeHandoffTCellScreen) Sync() { screen.calls = append(screen.calls, "sync") }

func TestTCellHandoffScreenMapsControllerPhasesWithoutDoubleResume(t *testing.T) {
	native := &fakeHandoffTCellScreen{w: 97, h: 31}
	screen := newTCellHandoffScreen(native, func() {
		native.calls = append(native.calls, "reprobe")
	})
	snapshot, err := screen.Freeze()
	if err != nil || snapshot.TerminalSize() != (terminalhandoff.Size{Columns: 97, Rows: 31}) {
		t.Fatalf("snapshot=%#v err=%v", snapshot, err)
	}
	for _, operation := range []func() error{
		screen.StopInput, screen.ShowCursor, screen.LeaveAlternate, screen.LeaveRaw,
		screen.EnterAlternate, screen.EnterRaw,
		func() error { return screen.RestoreCursor(snapshot) },
		func() error { return screen.ReplayResize(snapshot.TerminalSize()) },
		func() error { return screen.Resume(snapshot) },
	} {
		if err := operation(); err != nil {
			t.Fatal(err)
		}
	}
	want := []string{"show_cursor", "show", "suspend", "reprobe", "resume", "sync", "sync"}
	if !reflect.DeepEqual(native.calls, want) {
		t.Fatalf("calls = %#v, want %#v", native.calls, want)
	}
}

func TestTCellHandoffScreenReprobesBeforeTCellInputResumes(t *testing.T) {
	native := &fakeHandoffTCellScreen{w: 80, h: 24}
	screen := newTCellHandoffScreen(native, func() {
		native.calls = append(native.calls, "reprobe")
	})

	if err := screen.EnterAlternate(); err != nil {
		t.Fatal(err)
	}
	if want := []string{"reprobe", "resume"}; !reflect.DeepEqual(native.calls, want) {
		t.Fatalf("calls = %#v, want %#v", native.calls, want)
	}
}

func TestRemoteCurrentDirectoryShellFailureOffersExplicitHomeRetry(t *testing.T) {
	for _, result := range []terminalhandoff.Result{
		{Kind: terminalhandoff.ExitNonZero, ExitCode: 17},
		{Kind: terminalhandoff.ExitSignaled, Signal: "terminated"},
		{Kind: terminalhandoff.ExitPTYLoss},
	} {
		message := formatShellResult(result, true)
		if !strings.Contains(message, "press gS for an explicit home shell") {
			t.Fatalf("formatShellResult(%#v) = %q", result, message)
		}
	}
	if message := formatShellResult(terminalhandoff.Result{Kind: terminalhandoff.ExitNormal}, true); strings.Contains(message, "gS") {
		t.Fatalf("normal current-directory shell = %q", message)
	}
	if message := formatShellResult(terminalhandoff.Result{Kind: terminalhandoff.ExitNonZero, ExitCode: 17}, false); strings.Contains(message, "gS") {
		t.Fatalf("home shell failure = %q", message)
	}
}
