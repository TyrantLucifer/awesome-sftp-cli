package tui

import (
	"testing"
	"time"

	"github.com/gdamore/tcell/v3"
	"github.com/gdamore/tcell/v3/vt"
)

func TestTranslateTCellEvents(t *testing.T) {
	tests := []struct {
		name  string
		event tcell.Event
		mode  Mode
		want  Action
	}{
		{name: "tab", event: tcell.NewEventKey(tcell.KeyTab, "", tcell.ModNone), mode: ModeNormal, want: KeyPress{Key: KeyTab}},
		{name: "down", event: tcell.NewEventKey(tcell.KeyRune, "j", tcell.ModNone), mode: ModeNormal, want: KeyPress{Key: KeyDown}},
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

	cell := terminal.GetCell(vt.Coord{X: 0, Y: 0})
	if cell.C == "" || cell.C == " " {
		t.Fatalf("first cell = %q, want rendered header", cell.C)
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
