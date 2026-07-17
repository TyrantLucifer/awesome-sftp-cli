package tui

import (
	"testing"

	"github.com/TyrantLucifer/awesome-mac-sftp/internal/keymap"
	"github.com/gdamore/tcell/v3"
)

func TestTranslateTCellEventWithKeymapAppliesOnlyTheActiveContext(t *testing.T) {
	mapping, err := NewKeymap([]keymap.Override{{Context: keymap.ContextVisual, Input: "n", Action: keymap.ActionDown}})
	if err != nil {
		t.Fatal(err)
	}

	action, ok := TranslateTCellEventWithKeymap(tcell.NewEventKey(tcell.KeyRune, "n", tcell.ModNone), ModeVisual, mapping)
	if !ok || action != (KeyPress{Key: KeyDown}) {
		t.Fatalf("visual n = %#v, %v", action, ok)
	}
	action, ok = TranslateTCellEventWithKeymap(tcell.NewEventKey(tcell.KeyRune, "j", tcell.ModNone), ModeVisual, mapping)
	if !ok || action != (TextInput{Text: "j"}) {
		t.Fatalf("remapped visual j = %#v, %v", action, ok)
	}
	action, ok = TranslateTCellEventWithKeymap(tcell.NewEventKey(tcell.KeyRune, "j", tcell.ModNone), ModeNormal, mapping)
	if !ok || action != (KeyPress{Key: KeyDown}) {
		t.Fatalf("normal j = %#v, %v", action, ok)
	}
}
