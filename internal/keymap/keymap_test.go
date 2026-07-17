package keymap

import (
	"strings"
	"testing"
)

func TestDefaultSnapshotFreezesVimFirstBindingsAndReservedActions(t *testing.T) {
	want := strings.TrimSpace(`
normal A conflict_auto_rename_all reserved
normal C job_cancel reserved
normal D delete reserved
normal E edit_recovery remappable
normal H toggle_hidden remappable
normal J jobs remappable
normal K preview_drawer remappable
normal L log_drawer remappable
normal P job_pause reserved
normal R refresh remappable
normal S save remappable
normal U job_resume reserved
normal V visual_line remappable
normal W conflict_overwrite_all reserved
normal X conflict_skip_all reserved
normal ! command reserved
normal . repeat reserved
normal / filter remappable
normal a conflict_auto_rename reserved
normal c endpoint remappable
normal d cut remappable
normal e edit remappable
normal f filename_search remappable
normal g path reserved
normal h parent remappable
normal j down remappable
normal k up remappable
normal l open remappable
normal o open_external remappable
normal p paste remappable
normal r rename remappable
normal s sort remappable
normal v visual remappable
normal w conflict_overwrite reserved
normal x conflict_skip reserved
normal y copy remappable
normal <space> mark remappable`)
	if got := DefaultSnapshotText(); got != want {
		t.Fatalf("default keymap snapshot changed:\n%s\nwant:\n%s", got, want)
	}
}

func TestNewSupportsContextRemapWithoutChangingOtherContexts(t *testing.T) {
	mapping, err := New([]Override{{Context: ContextVisual, Input: "n", Action: ActionDown}})
	if err != nil {
		t.Fatal(err)
	}
	if got, ok := mapping.Lookup(ContextVisual, "n"); !ok || got != ActionDown {
		t.Fatalf("visual n = %q, %v", got, ok)
	}
	if _, ok := mapping.Lookup(ContextVisual, "j"); ok {
		t.Fatal("visual j remained reachable after remap")
	}
	if got, ok := mapping.Lookup(ContextNormal, "j"); !ok || got != ActionDown {
		t.Fatalf("normal j = %q, %v", got, ok)
	}
}

func TestNewRejectsConflictReservedDangerousAndUnreachableOverrides(t *testing.T) {
	tests := []struct {
		name      string
		overrides []Override
		want      string
	}{
		{name: "conflict", overrides: []Override{{Context: ContextNormal, Input: "k", Action: ActionDown}}, want: "conflict"},
		{name: "reserved action", overrides: []Override{{Context: ContextNormal, Input: "z", Action: ActionDelete}}, want: "reserved"},
		{name: "reserved input", overrides: []Override{{Context: ContextNormal, Input: ".", Action: ActionDown}}, want: "reserved"},
		{name: "digit", overrides: []Override{{Context: ContextNormal, Input: "2", Action: ActionDown}}, want: "count"},
		{name: "unknown context", overrides: []Override{{Context: "command", Input: "z", Action: ActionDown}}, want: "context"},
		{name: "unknown action", overrides: []Override{{Context: ContextNormal, Input: "z", Action: "macro_record"}}, want: "action"},
		{name: "duplicate action", overrides: []Override{{Context: ContextNormal, Input: "n", Action: ActionDown}, {Context: ContextNormal, Input: "m", Action: ActionDown}}, want: "duplicate"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			_, err := New(test.overrides)
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("New() error = %v, want %q", err, test.want)
			}
		})
	}
}
