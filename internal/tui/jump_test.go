package tui

import (
	"strings"
	"testing"

	"github.com/TyrantLucifer/awesome-sftp-cli/internal/domain"
)

func TestDirectoryFilterFuzzyRanksAndAcceptsSelectedEntry(t *testing.T) {
	model := testModel(t)
	left := model.Panes[Left]
	left = paneWithEntries(left, []domain.Entry{
		testEntry(leftEndpointID, "/left/docker-compose.yml", domain.EntryFile),
		testEntry(leftEndpointID, "/left/docs", domain.EntryDirectory),
		testEntry(leftEndpointID, "/left/documentation", domain.EntryDirectory),
		testEntry(leftEndpointID, "/left/downloads", domain.EntryDirectory),
	})
	model.Panes[Left] = left

	model, _ = Reduce(model, KeyPress{Key: KeyFilter})
	model, _ = Reduce(model, TextInput{Text: "doc"})
	pane := model.Panes[Left]
	if model.Mode != ModeFilter || pane.VisibleCount() != 3 || pane.visibleEntry(0).Name != "docs" || pane.visibleEntry(1).Name != "documentation" {
		t.Fatalf("fuzzy results = mode %q visible %#v", model.Mode, visibleNames(pane))
	}

	model, _ = Reduce(model, KeyPress{Key: KeyDown})
	if got := model.Panes[Left].visibleEntry(model.Panes[Left].Cursor).Name; got != "documentation" {
		t.Fatalf("selected fuzzy result = %q, want documentation", got)
	}
	model, intents := Reduce(model, KeyPress{Key: KeySubmit})
	if model.Mode != ModeNormal || len(intents) != 0 || model.Panes[Left].Filter != "" || model.Panes[Left].VisibleCount() != 4 || model.Panes[Left].visibleEntry(model.Panes[Left].Cursor).Name != "documentation" {
		t.Fatalf("accepted jump = mode %q pane %#v intents %#v", model.Mode, model.Panes[Left], intents)
	}
	if model.Notice != "entry selected; available actions are shown below" {
		t.Fatalf("accepted jump notice = %q", model.Notice)
	}

	model, intents = Reduce(model, KeyPress{Key: KeyOpen})
	if len(intents) != 1 || intents[0].Kind != IntentList || string(intents[0].Location.Path) != "/left/documentation" {
		t.Fatalf("open accepted fuzzy result = %#v", intents)
	}
}

func TestDirectoryFilterSupportsNonContiguousMatchAndClear(t *testing.T) {
	model := testModel(t)
	model.Panes[Left].Cursor = 1
	model, _ = Reduce(model, KeyPress{Key: KeyFilter})
	model, _ = Reduce(model, TextInput{Text: "nmd"})
	if got := visibleNames(model.Panes[Left]); len(got) != 1 || got[0] != "notes.md" {
		t.Fatalf("non-contiguous fuzzy results = %#v", got)
	}
	model, _ = Reduce(model, KeyPress{Key: KeyEscape})
	if model.Mode != ModeNormal || model.Panes[Left].Filter != "" || model.Panes[Left].VisibleCount() != 3 || model.Panes[Left].visibleEntry(model.Panes[Left].Cursor).Name != "file.txt" {
		t.Fatalf("cleared fuzzy jump = mode %q pane %#v", model.Mode, model.Panes[Left])
	}
}

func TestDirectoryFilterCancelRestoresExistingFilterAndCursor(t *testing.T) {
	model := testModel(t)
	left := model.Panes[Left]
	left.Filter = "file"
	left.rebuildVisible()
	model.Panes[Left] = left

	model, _ = Reduce(model, KeyPress{Key: KeyFilter})
	model, _ = Reduce(model, TextInput{Text: "nmd"})
	model, _ = Reduce(model, KeyPress{Key: KeyEscape})
	pane := model.Panes[Left]
	if model.Mode != ModeNormal || pane.Filter != "file" || pane.VisibleCount() != 1 || pane.visibleEntry(pane.Cursor).Name != "file.txt" {
		t.Fatalf("restored filtered cursor = mode %q pane %#v", model.Mode, pane)
	}
}

func TestDirectoryFilterStatusExplainsFuzzyJumpControls(t *testing.T) {
	model := testModel(t)
	model, _ = Reduce(model, KeyPress{Key: KeyFilter})
	model, _ = Reduce(model, TextInput{Text: "dir"})
	surface := newMemorySurface(100, 10)
	Render(surface, model, RenderOptions{Overscan: 1})
	got := surface.String()
	for _, want := range []string{"Jump: dir", "1 match", "↑/↓ select", "Enter jump", "Esc clear"} {
		if !strings.Contains(got, want) {
			t.Fatalf("jump status missing %q:\n%s", want, got)
		}
	}
}

func visibleNames(pane PaneState) []string {
	names := make([]string, 0, pane.VisibleCount())
	for index := 0; index < pane.VisibleCount(); index++ {
		names = append(names, pane.visibleEntry(index).Name)
	}
	return names
}
