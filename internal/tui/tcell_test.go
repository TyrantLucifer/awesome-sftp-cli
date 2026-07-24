package tui

import (
	"testing"
	"time"

	"github.com/gdamore/tcell/v3"
	"github.com/gdamore/tcell/v3/color"
	"github.com/gdamore/tcell/v3/vt"
)

func TestGraphiteThemeUsesExplicitSemanticContrast(t *testing.T) {
	theme := newGraphiteTheme()
	canvas := theme.style(StyleCanvas)
	if canvas.GetForeground() == color.Default || canvas.GetBackground() == color.Default {
		t.Fatal("graphite canvas must not depend on terminal default colors")
	}
	if theme.style(StyleCursor).GetBackground() == canvas.GetBackground() {
		t.Fatal("cursor background does not contrast with the canvas")
	}
	if theme.style(StyleActiveHeader).GetForeground() == theme.style(StyleHeader).GetForeground() {
		t.Fatal("active and inactive headers are visually indistinguishable")
	}
	if theme.style(StyleError).GetForeground() == theme.style(StyleWarning).GetForeground() {
		t.Fatal("danger and warning roles must remain distinguishable")
	}
}

func TestTranslateTCellEvents(t *testing.T) {
	tests := []struct {
		name  string
		event tcell.Event
		mode  Mode
		want  Action
	}{
		{name: "tab", event: tcell.NewEventKey(tcell.KeyTab, "", tcell.ModNone), mode: ModeNormal, want: KeyPress{Key: KeyTab}},
		{name: "down", event: tcell.NewEventKey(tcell.KeyRune, "j", tcell.ModNone), mode: ModeNormal, want: KeyPress{Key: KeyDown}},
		{name: "arrow down", event: tcell.NewEventKey(tcell.KeyDown, "", tcell.ModNone), mode: ModeNormal, want: KeyPress{Key: KeyDown}},
		{name: "arrow up", event: tcell.NewEventKey(tcell.KeyUp, "", tcell.ModNone), mode: ModeNormal, want: KeyPress{Key: KeyUp}},
		{name: "arrow left", event: tcell.NewEventKey(tcell.KeyLeft, "", tcell.ModNone), mode: ModeNormal, want: KeyPress{Key: KeyParent}},
		{name: "arrow right", event: tcell.NewEventKey(tcell.KeyRight, "", tcell.ModNone), mode: ModeNormal, want: KeyPress{Key: KeyOpen}},
		{name: "visual arrow down", event: tcell.NewEventKey(tcell.KeyDown, "", tcell.ModNone), mode: ModeVisual, want: KeyPress{Key: KeyDown}},
		{name: "visual arrow up", event: tcell.NewEventKey(tcell.KeyUp, "", tcell.ModNone), mode: ModeVisual, want: KeyPress{Key: KeyUp}},
		{name: "visual arrow left", event: tcell.NewEventKey(tcell.KeyLeft, "", tcell.ModNone), mode: ModeVisual, want: KeyPress{Key: KeyParent}},
		{name: "visual arrow right", event: tcell.NewEventKey(tcell.KeyRight, "", tcell.ModNone), mode: ModeVisual, want: KeyPress{Key: KeyOpen}},
		{name: "count", event: tcell.NewEventKey(tcell.KeyRune, "3", tcell.ModNone), mode: ModeNormal, want: CountDigit{Digit: 3}},
		{name: "filter digit", event: tcell.NewEventKey(tcell.KeyRune, "3", tcell.ModNone), mode: ModeFilter, want: TextInput{Text: "3"}},
		{name: "filter text", event: tcell.NewEventKey(tcell.KeyRune, "界", tcell.ModNone), mode: ModeFilter, want: TextInput{Text: "界"}},
		{name: "filter h", event: tcell.NewEventKey(tcell.KeyRune, "h", tcell.ModNone), mode: ModeFilter, want: TextInput{Text: "h"}},
		{name: "filter j", event: tcell.NewEventKey(tcell.KeyRune, "j", tcell.ModNone), mode: ModeFilter, want: TextInput{Text: "j"}},
		{name: "filter k", event: tcell.NewEventKey(tcell.KeyRune, "k", tcell.ModNone), mode: ModeFilter, want: TextInput{Text: "k"}},
		{name: "filter command letter", event: tcell.NewEventKey(tcell.KeyRune, "l", tcell.ModNone), mode: ModeFilter, want: TextInput{Text: "l"}},
		{name: "filter v", event: tcell.NewEventKey(tcell.KeyRune, "v", tcell.ModNone), mode: ModeFilter, want: TextInput{Text: "v"}},
		{name: "filter V", event: tcell.NewEventKey(tcell.KeyRune, "V", tcell.ModNone), mode: ModeFilter, want: TextInput{Text: "V"}},
		{name: "filter space", event: tcell.NewEventKey(tcell.KeyRune, " ", tcell.ModNone), mode: ModeFilter, want: TextInput{Text: " "}},
		{name: "filter escape", event: tcell.NewEventKey(tcell.KeyEscape, "", tcell.ModNone), mode: ModeFilter, want: KeyPress{Key: KeyEscape}},
		{name: "filter backspace", event: tcell.NewEventKey(tcell.KeyBackspace, "", tcell.ModNone), mode: ModeFilter, want: KeyPress{Key: KeyBackspace}},
		{name: "filter down", event: tcell.NewEventKey(tcell.KeyDown, "", tcell.ModNone), mode: ModeFilter, want: KeyPress{Key: KeyDown}},
		{name: "filter up", event: tcell.NewEventKey(tcell.KeyUp, "", tcell.ModNone), mode: ModeFilter, want: KeyPress{Key: KeyUp}},
		{name: "filter submit", event: tcell.NewEventKey(tcell.KeyEnter, "", tcell.ModNone), mode: ModeFilter, want: KeyPress{Key: KeySubmit}},
		{name: "filename search", event: tcell.NewEventKey(tcell.KeyRune, "f", tcell.ModNone), mode: ModeNormal, want: KeyPress{Key: KeyFilenameSearch}},
		{name: "filename search text", event: tcell.NewEventKey(tcell.KeyRune, "界", tcell.ModNone), mode: ModeFilenameSearch, want: TextInput{Text: "界"}},
		{name: "filename search submit", event: tcell.NewEventKey(tcell.KeyEnter, "", tcell.ModNone), mode: ModeFilenameSearch, want: KeyPress{Key: KeySubmit}},
		{name: "auth text", event: tcell.NewEventKey(tcell.KeyRune, "界", tcell.ModNone), mode: ModeAuth, want: TextInput{Text: "界"}},
		{name: "auth submit", event: tcell.NewEventKey(tcell.KeyEnter, "", tcell.ModNone), mode: ModeAuth, want: KeyPress{Key: KeySubmit}},
		{name: "auth cancel", event: tcell.NewEventKey(tcell.KeyEscape, "", tcell.ModNone), mode: ModeAuth, want: KeyPress{Key: KeyEscape}},
		{name: "auth backspace", event: tcell.NewEventKey(tcell.KeyBackspace, "", tcell.ModNone), mode: ModeAuth, want: KeyPress{Key: KeyBackspace}},
		{name: "workspace save", event: tcell.NewEventKey(tcell.KeyRune, "S", tcell.ModNone), mode: ModeNormal, want: KeyPress{Key: KeySave}},
		{name: "workspace text", event: tcell.NewEventKey(tcell.KeyRune, "r", tcell.ModNone), mode: ModeWorkspace, want: TextInput{Text: "r"}},
		{name: "workspace submit", event: tcell.NewEventKey(tcell.KeyEnter, "", tcell.ModNone), mode: ModeWorkspace, want: KeyPress{Key: KeySubmit}},
		{name: "sort", event: tcell.NewEventKey(tcell.KeyRune, "s", tcell.ModNone), mode: ModeNormal, want: KeyPress{Key: KeySort}},
		{name: "hidden", event: tcell.NewEventKey(tcell.KeyRune, "H", tcell.ModNone), mode: ModeNormal, want: KeyPress{Key: KeyToggleHidden}},
		{name: "refresh", event: tcell.NewEventKey(tcell.KeyRune, "R", tcell.ModNone), mode: ModeNormal, want: KeyPress{Key: KeyRefresh}},
		{name: "path", event: tcell.NewEventKey(tcell.KeyRune, "g", tcell.ModNone), mode: ModeNormal, want: KeyPress{Key: KeyPath}},
		{name: "bottom", event: tcell.NewEventKey(tcell.KeyRune, "G", tcell.ModNone), mode: ModeNormal, want: KeyPress{Key: KeyBottom}},
		{name: "path text", event: tcell.NewEventKey(tcell.KeyRune, "/", tcell.ModNone), mode: ModePath, want: TextInput{Text: "/"}},
		{name: "path submit", event: tcell.NewEventKey(tcell.KeyEnter, "", tcell.ModNone), mode: ModePath, want: KeyPress{Key: KeySubmit}},
		{name: "endpoint", event: tcell.NewEventKey(tcell.KeyRune, "c", tcell.ModNone), mode: ModeNormal, want: KeyPress{Key: KeyEndpoint}},
		{name: "copy", event: tcell.NewEventKey(tcell.KeyRune, "y", tcell.ModNone), mode: ModeNormal, want: KeyPress{Key: KeyCopy}},
		{name: "cut", event: tcell.NewEventKey(tcell.KeyRune, "d", tcell.ModNone), mode: ModeNormal, want: KeyPress{Key: KeyCut}},
		{name: "delete", event: tcell.NewEventKey(tcell.KeyRune, "D", tcell.ModNone), mode: ModeNormal, want: KeyPress{Key: KeyDelete}},
		{name: "rename", event: tcell.NewEventKey(tcell.KeyRune, "r", tcell.ModNone), mode: ModeNormal, want: KeyPress{Key: KeyRename}},
		{name: "repeat", event: tcell.NewEventKey(tcell.KeyRune, ".", tcell.ModNone), mode: ModeNormal, want: KeyPress{Key: KeyRepeat}},
		{name: "paste", event: tcell.NewEventKey(tcell.KeyRune, "p", tcell.ModNone), mode: ModeNormal, want: KeyPress{Key: KeyPaste}},
		{name: "edit", event: tcell.NewEventKey(tcell.KeyRune, "e", tcell.ModNone), mode: ModeNormal, want: KeyPress{Key: KeyEdit}},
		{name: "open external", event: tcell.NewEventKey(tcell.KeyRune, "o", tcell.ModNone), mode: ModeNormal, want: KeyPress{Key: KeyOpenExternal}},
		{name: "jobs", event: tcell.NewEventKey(tcell.KeyRune, "J", tcell.ModNone), mode: ModeNormal, want: KeyPress{Key: KeyJobs}},
		{name: "job pause", event: tcell.NewEventKey(tcell.KeyRune, "P", tcell.ModNone), mode: ModeNormal, want: KeyPress{Key: KeyJobPause}},
		{name: "job resume", event: tcell.NewEventKey(tcell.KeyRune, "U", tcell.ModNone), mode: ModeNormal, want: KeyPress{Key: KeyJobResume}},
		{name: "job cancel", event: tcell.NewEventKey(tcell.KeyRune, "C", tcell.ModNone), mode: ModeNormal, want: KeyPress{Key: KeyJobCancel}},
		{name: "conflict overwrite", event: tcell.NewEventKey(tcell.KeyRune, "w", tcell.ModNone), mode: ModeNormal, want: KeyPress{Key: KeyConflictOverwrite}},
		{name: "conflict auto rename all", event: tcell.NewEventKey(tcell.KeyRune, "A", tcell.ModNone), mode: ModeNormal, want: KeyPress{Key: KeyConflictAutoRenameAll}},
		{name: "endpoint text", event: tcell.NewEventKey(tcell.KeyRune, "w", tcell.ModNone), mode: ModeEndpoint, want: TextInput{Text: "w"}},
		{name: "endpoint down", event: tcell.NewEventKey(tcell.KeyDown, "", tcell.ModNone), mode: ModeEndpoint, want: KeyPress{Key: KeyDown}},
		{name: "endpoint up", event: tcell.NewEventKey(tcell.KeyUp, "", tcell.ModNone), mode: ModeEndpoint, want: KeyPress{Key: KeyUp}},
		{name: "endpoint submit", event: tcell.NewEventKey(tcell.KeyEnter, "", tcell.ModNone), mode: ModeEndpoint, want: KeyPress{Key: KeySubmit}},
		{name: "resize", event: tcell.NewEventResize(91, 27), mode: ModeFilter, want: Resize{Width: 91, Height: 27}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got, ok := TranslateTCellEvent(test.event, test.mode)
			if !ok || got != test.want {
				t.Fatalf("TranslateTCellEvent() = (%#v, %t), want %#v", got, ok, test.want)
			}
		})
	}
}

func TestTCellSurfaceRendersThroughV3MockTerminal(t *testing.T) {
	terminal := vt.NewMockTerm(vt.MockOptSize{X: 40, Y: 10})
	screen, err := tcell.NewTerminfoScreenFromTty(terminal, tcell.OptNegotiation(false))
	if err != nil {
		t.Fatal(err)
	}
	if err := screen.Init(); err != nil {
		t.Fatal(err)
	}
	defer screen.Fini()

	model := testModel(t)
	surface := NewTCellSurface(screen)
	Render(surface, model, RenderOptions{Overscan: 1})
	screen.Show()

	cell := terminal.GetCell(vt.Coord{X: 1, Y: 0})
	if cell.C == "" || cell.C == " " {
		t.Fatalf("header cell = %q, want rendered header", cell.C)
	}
	if cell.S.Bg() == color.Default {
		t.Fatal("header cell uses the terminal default background")
	}

	// Synchronize with tcell's input filter before Fini closes EventQ. The
	// mock terminal can otherwise still be forwarding its initial events.
	terminal.SendRaw([]byte("x"))
	deadline := time.NewTimer(time.Second)
	defer deadline.Stop()
	for {
		select {
		case event := <-screen.EventQ():
			key, ok := event.(*tcell.EventKey)
			if ok && key.Str() == "x" {
				return
			}
		case <-deadline.C:
			t.Fatal("timed out waiting for mock terminal input synchronization")
		}
	}
}
