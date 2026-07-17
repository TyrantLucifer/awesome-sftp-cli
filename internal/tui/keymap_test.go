package tui

import (
	"testing"

	"github.com/TyrantLucifer/awesome-mac-sftp/internal/domain"
	"github.com/TyrantLucifer/awesome-mac-sftp/internal/keymap"
	"github.com/TyrantLucifer/awesome-mac-sftp/internal/transfer"
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

func TestConfiguredKeymapPreservesCountVisualRepeatAndDeleteConfirmation(t *testing.T) {
	mapping, err := NewKeymap([]keymap.Override{
		{Context: keymap.ContextNormal, Input: "n", Action: keymap.ActionDown},
		{Context: keymap.ContextVisual, Input: "m", Action: keymap.ActionDown},
	})
	if err != nil {
		t.Fatal(err)
	}
	reduceRune := func(t *testing.T, model Model, value string) (Model, []Intent) {
		t.Helper()
		action, ok := TranslateTCellEventWithKeymap(tcell.NewEventKey(tcell.KeyRune, value, tcell.ModNone), model.Mode, mapping)
		if !ok {
			t.Fatalf("translate %q in %s = not handled", value, model.Mode)
		}
		return Reduce(model, action)
	}

	model := modelWithEntryCount(t, 20)
	model, _ = reduceRune(t, model, "2")
	model, _ = reduceRune(t, model, "n")
	if model.Panes[Left].Cursor != 2 || model.Count != 0 {
		t.Fatalf("configured counted navigation = cursor %d count %d", model.Panes[Left].Cursor, model.Count)
	}
	model, _ = reduceRune(t, model, "v")
	model, _ = reduceRune(t, model, "m")
	if model.Mode != ModeVisual || len(model.Panes[Left].SelectedLocations()) != 2 {
		t.Fatalf("configured Visual navigation = mode %s selection %#v", model.Mode, model.Panes[Left].SelectedLocations())
	}

	model = testModel(t)
	model, intents := reduceRune(t, model, "D")
	prepare := assertSingleIntent(t, intents, IntentPrepareDelete, "/left/dir")
	model, _ = Reduce(model, DeletePrepared{References: []transfer.FileRef{{Location: prepare.Location, Kind: domain.EntryDirectory}}})
	model, intents = reduceRune(t, model, ".")
	if model.Mode != ModeDeleteConfirm || model.DeleteConfirmation != 1 || len(intents) != 0 {
		t.Fatalf("repeat bypassed configured delete confirmation: model=%#v intents=%#v", model, intents)
	}
}
