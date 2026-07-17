package tui

import (
	"testing"
	"time"

	"github.com/TyrantLucifer/awesome-mac-sftp/internal/domain"
	"github.com/TyrantLucifer/awesome-mac-sftp/internal/search"
)

func TestFilenameSearchInputFreezesActivePaneScope(t *testing.T) {
	model := testModel(t)
	model, intents := Reduce(model, KeyPress{Key: KeyFilenameSearch})
	if len(intents) != 0 || model.Mode != ModeFilenameSearch {
		t.Fatalf("open search model=%#v intents=%#v", model, intents)
	}
	model, _ = Reduce(model, TextInput{Text: "target"})
	model, intents = Reduce(model, KeyPress{Key: KeySubmit})
	if len(intents) != 1 || intents[0].Kind != IntentSearchFilename {
		t.Fatalf("submit intents = %#v", intents)
	}
	if intents[0].Pane != Left || intents[0].Location != model.Panes[Left].Location || intents[0].SearchPattern != "target" {
		t.Fatalf("search intent = %#v", intents[0])
	}
	if model.Mode != ModeNormal || model.Drawer.Mode != DrawerSearch || model.Drawer.Focus != FocusDrawer || !model.Search.Loading {
		t.Fatalf("search UI state = %#v", model)
	}
}

func TestFilenameSearchAcceptsOnlyExactFrozenIdentity(t *testing.T) {
	model := testModel(t)
	identity := searchUIIdentity()
	model, _ = Reduce(model, BeginSearch{Identity: identity})
	current := search.Event{
		Identity: identity,
		Kind:     search.EventResult,
		Result: search.Result{
			Location:     domain.Location{EndpointID: leftEndpointID, Path: "/left/nested/target.txt"},
			RelativePath: "nested/target.txt",
			Entry:        testEntry(leftEndpointID, "/left/nested/target.txt", domain.EntryFile),
		},
	}
	staleUI := current
	staleUI.Identity.UIGeneration++
	staleEndpoint := current
	staleEndpoint.Identity.EndpointGeneration++
	model, _ = Reduce(model, SearchEvents{Events: []search.Event{staleUI, staleEndpoint, current}})
	if len(model.Search.Results) != 1 || model.Search.Results[0].RelativePath != "nested/target.txt" {
		t.Fatalf("search results = %#v", model.Search.Results)
	}

	terminal := search.Event{Identity: identity, Kind: search.EventTerminal, Terminal: search.Terminal{Status: search.StatusPartial, StopReason: search.StopPermissionDenied, Results: 1}}
	model, _ = Reduce(model, SearchEvents{Events: []search.Event{terminal}})
	if model.Search.Loading || model.Search.Terminal.Status != search.StatusPartial || model.Search.Terminal.StopReason != search.StopPermissionDenied {
		t.Fatalf("search terminal = %#v", model.Search)
	}
}

func TestFilenameSearchEscapeCancelsExactActiveRequest(t *testing.T) {
	model := testModel(t)
	identity := searchUIIdentity()
	model, _ = Reduce(model, BeginSearch{Identity: identity})
	model.Drawer = DrawerState{Mode: DrawerSearch, Focus: FocusDrawer, Rows: 6}
	model, intents := Reduce(model, KeyPress{Key: KeyEscape})
	if len(intents) != 1 || intents[0].Kind != IntentSearchCancel || intents[0].SearchIdentity != identity {
		t.Fatalf("cancel intents = %#v", intents)
	}
	if model.Drawer.Focus != FocusPane {
		t.Fatalf("drawer focus = %q", model.Drawer.Focus)
	}
}

func TestContentSearchGSlashFreezesActivePaneScope(t *testing.T) {
	model := testModel(t)
	model, _ = Reduce(model, KeyPress{Key: KeyPath})
	model, _ = Reduce(model, TextInput{Text: "/"})
	if model.Mode != ModeContentSearch {
		t.Fatalf("g/ mode = %q", model.Mode)
	}
	model, _ = Reduce(model, TextInput{Text: "needle"})
	model, intents := Reduce(model, KeyPress{Key: KeySubmit})
	if len(intents) != 0 || model.Mode != ModeContentSearchConfirm {
		t.Fatalf("content confirmation model=%#v intents=%#v", model, intents)
	}
	model, intents = Reduce(model, KeyPress{Key: KeySubmit})
	if len(intents) != 1 || intents[0].Kind != IntentSearchContent || intents[0].Pane != Left || intents[0].Location.Path != "/left" || intents[0].SearchPattern != "needle" {
		t.Fatalf("content search intents = %#v", intents)
	}
	if model.Drawer.Mode != DrawerContentSearch || !model.ContentSearch.Loading {
		t.Fatalf("content search state = %#v", model.ContentSearch)
	}
}

func TestContentSearchRejectsStaleEvents(t *testing.T) {
	model := testModel(t)
	identity := contentSearchUIIdentity()
	model, _ = Reduce(model, BeginContentSearch{Identity: identity})
	current := search.ContentEvent{Identity: identity, Kind: search.ContentEventResult, Result: search.ContentResult{Location: domain.Location{EndpointID: leftEndpointID, Path: "/left/a.txt"}, RelativePath: "a.txt", Line: 4, Offset: 20, Snippet: "needle"}}
	stale := current
	stale.Identity.UIGeneration++
	model, _ = Reduce(model, ContentSearchEvents{Events: []search.ContentEvent{stale, current}})
	if len(model.ContentSearch.Results) != 1 || model.ContentSearch.Results[0].Line != 4 {
		t.Fatalf("content results = %#v", model.ContentSearch.Results)
	}
}

func searchUIIdentity() search.Identity {
	return search.Identity{
		RequestID:          "req_ffffffffffffffffffffffffff",
		EndpointID:         leftEndpointID,
		SessionID:          "sess_ffffffffffffffffffffffffff",
		EndpointGeneration: 9,
		UIGeneration:       4,
		Scope:              domain.Location{EndpointID: leftEndpointID, Path: "/left"},
		Options:            search.Options{Pattern: "target", Target: search.MatchRelativePath, CaseSensitive: false, Symlinks: search.SymlinkNever, Ignore: search.IgnoreNone, Types: search.TypeFilter{Files: true, Directories: true}},
		Budget:             search.Budget{PageItems: 256, EventBuffer: 64, ConcurrentLists: 1, MaxDepth: 64, MaxEntries: 1_000_000, MaxResults: 10_000, MaxOutputBytes: 8 << 20, MaxDuration: time.Minute},
	}
}

func contentSearchUIIdentity() search.ContentIdentity {
	return search.ContentIdentity{
		RequestID: "req_xxxxxxxxxxxxxxxxxxxxxxxxxx", EndpointID: leftEndpointID, SessionID: "sess_xxxxxxxxxxxxxxxxxxxxxxxxxx",
		EndpointGeneration: 9, UIGeneration: 5, Scope: domain.Location{EndpointID: leftEndpointID, Path: "/left"},
		Options: search.ContentOptions{Pattern: "needle", PatternType: search.PatternLiteral, Binary: search.BinarySkip},
		Budget:  search.ContentBudget{PageItems: 128, EventBuffer: 32, MaxDepth: 32, MaxEntries: 1000, MaxFiles: 100, MaxResults: 100, MaxMatchesPerFile: 10, MaxFileBytes: 1 << 20, MaxReadBytes: 4 << 20, MaxSnippetBytes: 256, MaxOutputBytes: 1 << 20, MaxDuration: time.Minute},
	}
}
