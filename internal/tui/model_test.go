package tui

import (
	"fmt"
	"testing"

	"github.com/TyrantLucifer/awesome-mac-sftp/internal/domain"
)

const (
	leftEndpointID  domain.EndpointID = "ep_aaaaaaaaaaaaaaaaaaaaaaaaaa"
	rightEndpointID domain.EndpointID = "ep_bbbbbbbbbbbbbbbbbbbbbbbbbb"
)

func TestReducerKeepsPaneStateIndependent(t *testing.T) {
	model := testModel(t)
	originalRight := model.Panes[Right]

	model, _ = Reduce(model, KeyPress{Key: KeyTab})
	if model.Active != Right {
		t.Fatalf("active pane = %v, want right", model.Active)
	}
	model, intents := Reduce(model, KeyPress{Key: KeyDown})
	if len(intents) != 0 {
		t.Fatalf("down intents = %#v, want none", intents)
	}
	if model.Panes[Right].Cursor != 1 {
		t.Fatalf("right cursor = %d, want 1", model.Panes[Right].Cursor)
	}
	if model.Panes[Left].Cursor != 0 {
		t.Fatalf("left cursor = %d, want 0", model.Panes[Left].Cursor)
	}
	if originalRight.Cursor != 0 {
		t.Fatal("Reduce mutated its input model")
	}
}

func TestReducerEmitsOnlyReadOnlyNavigationIntents(t *testing.T) {
	model := testModel(t)

	model, intents := Reduce(model, KeyPress{Key: KeyParent})
	assertSingleIntent(t, intents, IntentList, Left, "/")

	model, intents = Reduce(model, KeyPress{Key: KeyOpen})
	assertSingleIntent(t, intents, IntentList, Left, "/left/dir")

	model, _ = Reduce(model, KeyPress{Key: KeyDown})
	_, intents = Reduce(model, KeyPress{Key: KeyOpen})
	intent := assertSingleIntent(t, intents, IntentPreview, Left, "/left/file.txt")
	if intent.Limit != PreviewByteLimit {
		t.Fatalf("preview limit = %d, want %d", intent.Limit, PreviewByteLimit)
	}
}

func TestReducerTracksVisualAndDiscreteSelection(t *testing.T) {
	model := testModel(t)

	model, _ = Reduce(model, KeyPress{Key: KeyVisual})
	model, _ = Reduce(model, KeyPress{Key: KeyDown})
	selected := model.Panes[Left].SelectedLocations()
	if len(selected) != 2 {
		t.Fatalf("visual selection count = %d, want 2", len(selected))
	}

	model, _ = Reduce(model, KeyPress{Key: KeyEscape})
	model, _ = Reduce(model, KeyPress{Key: KeyMark})
	selected = model.Panes[Left].SelectedLocations()
	if len(selected) != 1 || selected[0].Path != "/left/file.txt" {
		t.Fatalf("marked locations = %#v, want file.txt", selected)
	}
	model, _ = Reduce(model, KeyPress{Key: KeyMark})
	if got := model.Panes[Left].SelectedLocations(); len(got) != 0 {
		t.Fatalf("selection after toggle = %#v, want empty", got)
	}
}

func TestListingPagesIgnoreStaleGenerationsAndRetainPartialState(t *testing.T) {
	model := testModel(t)
	location := model.Panes[Left].Location
	model, _ = Reduce(model, BeginListing{Pane: Left, Generation: 2, Location: location})

	model, _ = Reduce(model, ListingPage{
		Pane:       Left,
		Generation: 1,
		Entries:    []domain.Entry{testEntry(leftEndpointID, "/stale", domain.EntryFile)},
		Done:       true,
	})
	if got := model.Panes[Left].VisibleCount(); got != 0 {
		t.Fatalf("visible count after stale page = %d, want 0", got)
	}

	model, _ = Reduce(model, ListingPage{
		Pane:       Left,
		Generation: 2,
		Entries:    []domain.Entry{testEntry(leftEndpointID, "/left/partial", domain.EntryFile)},
	})
	model, _ = Reduce(model, ListingFailed{Pane: Left, Generation: 2, Message: "interrupted"})
	pane := model.Panes[Left]
	if pane.VisibleCount() != 1 || pane.Listing.Loading || !pane.Listing.Partial {
		t.Fatalf("listing state = %#v visible=%d, want one partial terminal page", pane.Listing, pane.VisibleCount())
	}
}

func TestFilterAppliesToLoadedAndIncomingEntriesAndClearsLosslessly(t *testing.T) {
	model := testModel(t)
	model, _ = Reduce(model, SetFilter{Pane: Left, Query: "file"})
	if got := model.Panes[Left].VisibleNames(); fmt.Sprint(got) != "[file.txt]" {
		t.Fatalf("filtered names = %v, want [file.txt]", got)
	}

	generation := model.Panes[Left].Listing.Generation
	model, _ = Reduce(model, ListingPage{
		Pane:       Left,
		Generation: generation,
		Entries: []domain.Entry{
			testEntry(leftEndpointID, "/left/another-file", domain.EntryFile),
			testEntry(leftEndpointID, "/left/hidden", domain.EntryFile),
		},
	})
	if got := model.Panes[Left].VisibleNames(); fmt.Sprint(got) != "[file.txt another-file]" {
		t.Fatalf("filtered names after page = %v", got)
	}

	model, _ = Reduce(model, SetFilter{Pane: Left, Query: ""})
	if got := model.Panes[Left].VisibleCount(); got != 5 {
		t.Fatalf("visible count after clear = %d, want all 5 entries", got)
	}
}

func testModel(t *testing.T) Model {
	t.Helper()
	leftLocation := mustLocation(t, leftEndpointID, "/left")
	rightLocation := mustLocation(t, rightEndpointID, "/right")
	left := NewPaneState(domain.Endpoint{ID: leftEndpointID, Kind: domain.EndpointLocal, DisplayName: "left"}, leftLocation)
	right := NewPaneState(domain.Endpoint{ID: rightEndpointID, Kind: domain.EndpointLocal, DisplayName: "right"}, rightLocation)
	left = paneWithEntries(left, []domain.Entry{
		testEntry(leftEndpointID, "/left/dir", domain.EntryDirectory),
		testEntry(leftEndpointID, "/left/file.txt", domain.EntryFile),
		testEntry(leftEndpointID, "/left/notes.md", domain.EntryFile),
	})
	right = paneWithEntries(right, []domain.Entry{
		testEntry(rightEndpointID, "/right/a", domain.EntryFile),
		testEntry(rightEndpointID, "/right/b", domain.EntryFile),
	})
	return NewModel(left, right)
}

func paneWithEntries(pane PaneState, entries []domain.Entry) PaneState {
	model := NewModel(pane, PaneState{})
	model, _ = Reduce(model, BeginListing{Pane: Left, Generation: 1, Location: pane.Location})
	model, _ = Reduce(model, ListingPage{Pane: Left, Generation: 1, Entries: entries, Done: true})
	return model.Panes[Left]
}

func testEntry(endpointID domain.EndpointID, path string, kind domain.EntryKind) domain.Entry {
	location := domain.Location{EndpointID: endpointID, Path: domain.CanonicalPath(path)}
	name := path
	for index := len(path) - 1; index >= 0; index-- {
		if path[index] == '/' {
			name = path[index+1:]
			break
		}
	}
	return domain.Entry{Location: location, Name: name, Kind: kind}
}

func mustLocation(t *testing.T, endpointID domain.EndpointID, path string) domain.Location {
	t.Helper()
	location, err := domain.NewLocation(endpointID, domain.CanonicalPath(path))
	if err != nil {
		t.Fatal(err)
	}
	return location
}

func assertSingleIntent(
	t *testing.T,
	intents []Intent,
	kind IntentKind,
	pane PaneID,
	path string,
) Intent {
	t.Helper()
	if len(intents) != 1 {
		t.Fatalf("intents = %#v, want one", intents)
	}
	intent := intents[0]
	if intent.Kind != kind || intent.Pane != pane || string(intent.Location.Path) != path {
		t.Fatalf("intent = %#v, want kind=%q pane=%v path=%q", intent, kind, pane, path)
	}
	return intent
}
