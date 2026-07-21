package tui

import (
	"strings"
	"testing"
	"time"

	"github.com/TyrantLucifer/awesome-sftp-cli/internal/diagnostic"
	"github.com/TyrantLucifer/awesome-sftp-cli/internal/domain"
	"github.com/TyrantLucifer/awesome-sftp-cli/internal/job"
	builtinpreview "github.com/TyrantLucifer/awesome-sftp-cli/internal/preview"
	"github.com/TyrantLucifer/awesome-sftp-cli/internal/state/jobstore"
	"github.com/TyrantLucifer/awesome-sftp-cli/internal/transfer"
	"github.com/gdamore/tcell/v3"
)

func TestDrawerReducerOpensFocusesSwitchesAndClosesWithoutChangingPane(t *testing.T) {
	model := testModel(t)
	model, _ = Reduce(model, KeyPress{Key: KeyDown})
	originalActive := model.Active
	originalLeft := model.Panes[Left].Location
	originalRight := model.Panes[Right].Location

	model, intents := Reduce(model, KeyPress{Key: KeyPreviewDrawer})
	if model.Drawer.Mode != DrawerPreview || model.Drawer.Focus != FocusDrawer {
		t.Fatalf("open Preview drawer = %#v", model.Drawer)
	}
	if len(intents) != 1 || intents[0].Kind != IntentPreview {
		t.Fatalf("Preview open intents = %#v", intents)
	}

	model, intents = Reduce(model, KeyPress{Key: KeyJobs})
	if model.Drawer.Mode != DrawerJobs || model.Drawer.Focus != FocusDrawer {
		t.Fatalf("switch to Jobs drawer = %#v", model.Drawer)
	}
	if len(intents) != 2 || intents[0].Kind != IntentPreviewCancel || intents[1].Kind != IntentJobList {
		t.Fatalf("Jobs switch intents = %#v", intents)
	}

	model, intents = Reduce(model, KeyPress{Key: KeyEscape})
	if model.Drawer.Mode != DrawerJobs || model.Drawer.Focus != FocusPane || len(intents) != 0 {
		t.Fatalf("drawer escape model=%#v intents=%#v", model.Drawer, intents)
	}
	model, intents = Reduce(model, KeyPress{Key: KeyJobs})
	if model.Drawer.Mode != DrawerJobs || model.Drawer.Focus != FocusDrawer || len(intents) != 1 || intents[0].Kind != IntentJobList {
		t.Fatalf("refocus Jobs model=%#v intents=%#v", model.Drawer, intents)
	}
	model, intents = Reduce(model, KeyPress{Key: KeyJobs})
	if model.Drawer.Mode != DrawerClosed || model.Drawer.Focus != FocusPane || len(intents) != 0 {
		t.Fatalf("close Jobs model=%#v intents=%#v", model.Drawer, intents)
	}

	if model.Active != originalActive || model.Panes[Left].Location != originalLeft || model.Panes[Right].Location != originalRight {
		t.Fatalf("drawer transitions changed pane context: active=%v left=%#v right=%#v", model.Active, model.Panes[Left].Location, model.Panes[Right].Location)
	}
}

func TestLogDrawerRequestsAndRendersBoundedSanitizedSnapshot(t *testing.T) {
	model := testModel(t)
	model, intents := Reduce(model, KeyPress{Key: KeyLogDrawer})
	if len(intents) != 1 || intents[0].Kind != IntentDiagnosticList || intents[0].Limit != 256 {
		t.Fatalf("Log open intents = %#v", intents)
	}
	model, _ = Reduce(model, DiagnosticsLoaded{Records: []diagnostic.Record{{
		Sequence: 7, Time: time.Unix(100, 0).UTC(), Level: "ERROR", Message: "diagnostic",
		Component: "cache", Event: "quota_exhausted", ErrorCode: domain.CodeResourceExhausted,
	}}})
	surface := newMemorySurface(100, 12)
	Render(surface, model, RenderOptions{Overscan: 1})
	got := surface.String()
	for _, want := range []string{"[Log]", "ERROR", "cache", "quota_exhausted", "resource_exhausted"} {
		if !strings.Contains(got, want) {
			t.Fatalf("Log drawer missing %q:\n%s", want, got)
		}
	}
}

func TestDrawerReducerKeepsLowercaseNavigationSeparateAndRefreshesPreviewGeneration(t *testing.T) {
	model := testModel(t)
	model, _ = Reduce(model, KeyPress{Key: KeyPreviewDrawer})
	model, _ = Reduce(model, KeyPress{Key: KeyEscape})
	before := model.Panes[Left].Cursor

	model, intents := Reduce(model, KeyPress{Key: KeyDown})
	if model.Panes[Left].Cursor != before+1 {
		t.Fatalf("lowercase navigation cursor = %d, want %d", model.Panes[Left].Cursor, before+1)
	}
	if model.Drawer.Mode != DrawerPreview || model.Drawer.Focus != FocusPane {
		t.Fatalf("lowercase navigation changed drawer = %#v", model.Drawer)
	}
	if len(intents) != 2 || intents[0].Kind != IntentPreviewCancel || intents[1].Kind != IntentPreview {
		t.Fatalf("cursor preview intents = %#v", intents)
	}
	if intents[1].Location != model.Panes[Left].visibleEntry(model.Panes[Left].Cursor).Location {
		t.Fatalf("preview location = %#v", intents[1].Location)
	}
}

func TestDrawerRendererUsesBoundedBottomRegionAtNormalAndNarrowSizes(t *testing.T) {
	model := testModel(t)
	model.Drawer = DrawerState{Mode: DrawerJobs, Focus: FocusDrawer, Rows: 6}
	model.Jobs = []transfer.JobView{{
		Snapshot: jobstore.Snapshot{JobID: "job_aaaaaaaaaaaaaaaaaaaaaaaaaa", State: job.StateWaitingAuth},
		Kind:     transfer.OperationCopy,
		Source:   domain.Location{Path: "/source"}, Final: domain.Location{Path: "/final"},
		Phase: transfer.PhaseStreaming, Route: transfer.RouteHelperSameHost, Bytes: 42, Items: 1, WaitingReason: "waiting_auth",
	}}

	for _, size := range []struct{ width, height int }{{100, 16}, {32, 7}} {
		surface := newMemorySurface(size.width, size.height)
		stats := Render(surface, model, RenderOptions{Overscan: 1})
		got := surface.String()
		for _, want := range []string{"Preview", "[Jobs]", "Log", "Copy", "Waiting"} {
			if !strings.Contains(got, want) {
				t.Fatalf("%dx%d drawer missing %q:\n%s", size.width, size.height, want, got)
			}
		}
		if strings.Contains(got, "helper_same_host") || strings.Contains(got, "waiting_auth") {
			t.Fatalf("%dx%d drawer exposed internal Job details:\n%s", size.width, size.height, got)
		}
		if stats.ListRows >= size.height-2 {
			t.Fatalf("%dx%d list rows = %d, drawer did not reserve a bounded bottom region", size.width, size.height, stats.ListRows)
		}
	}
}

func TestPreviewDrawerAdaptsToTerminalHeightWithoutExpandingOtherDrawers(t *testing.T) {
	for _, test := range []struct {
		height int
		want   int
	}{{height: 7, want: 4}, {height: 12, want: 6}, {height: 24, want: 12}, {height: 40, want: 16}} {
		if got := drawerRows(DrawerState{Mode: DrawerPreview, Rows: 6}, test.height); got != test.want {
			t.Fatalf("preview drawer rows at height %d = %d, want %d", test.height, got, test.want)
		}
	}
	if got := drawerRows(DrawerState{Mode: DrawerJobs, Rows: 6}, 24); got != 6 {
		t.Fatalf("Jobs drawer rows = %d, want fixed 6", got)
	}
	if got := drawerRows(DrawerState{Mode: DrawerLog, Rows: 6}, 24); got != 6 {
		t.Fatalf("Log drawer rows = %d, want fixed 6", got)
	}
}

func TestAdaptivePreviewDrawerShowsTenContentLinesAtNormalHeight(t *testing.T) {
	model := testModel(t)
	model.Drawer = DrawerState{Mode: DrawerPreview, Focus: FocusDrawer, Rows: 6}
	model.Preview = PreviewState{Location: model.Panes[Left].Entries[1].Location, Data: []byte(strings.Join([]string{
		"line-00", "line-01", "line-02", "line-03", "line-04", "line-05",
		"line-06", "line-07", "line-08", "line-09", "line-10",
	}, "\n"))}
	surface := newMemorySurface(100, 24)

	stats := Render(surface, model, RenderOptions{Overscan: 1})
	got := surface.String()
	if !strings.Contains(got, "line-09") || strings.Contains(got, "line-10") {
		t.Fatalf("adaptive Preview did not render exactly ten content lines:\n%s", got)
	}
	if stats.ListRows != 10 {
		t.Fatalf("list rows = %d, want 10 with a 12-row Preview drawer", stats.ListRows)
	}
}

func TestTranslateTCellDistinguishesUppercaseDrawerKeys(t *testing.T) {
	for input, want := range map[string]Key{"K": KeyPreviewDrawer, "J": KeyJobs, "L": KeyLogDrawer} {
		action, ok := TranslateTCellEvent(tcell.NewEventKey(tcell.KeyRune, input, tcell.ModNone), ModeNormal)
		if !ok {
			t.Fatalf("translate %q returned no action", input)
		}
		press, ok := action.(KeyPress)
		if !ok || press.Key != want {
			t.Fatalf("translate %q = %#v, want %q", input, action, want)
		}
	}
}

func TestPreviewDrawerKeysEmitBoundedReadModesAndViewToggles(t *testing.T) {
	model := testModel(t)
	model, _ = Reduce(model, KeyPress{Key: KeyDown})
	model, _ = Reduce(model, KeyPress{Key: KeyPreviewDrawer})
	model.Preview = PreviewState{
		Identity:  PreviewRequestIdentity{Mode: builtinpreview.ReadRange, Offset: 65536, RequestedLimit: 65536},
		Truncated: true,
		View:      builtinpreview.ViewAuto,
	}
	tests := []struct {
		key        Key
		wantMode   builtinpreview.ReadMode
		wantOffset uint64
		wantView   builtinpreview.ViewMode
	}{
		{key: KeyParent, wantMode: builtinpreview.ReadHead, wantView: builtinpreview.ViewAuto},
		{key: KeyOpen, wantMode: builtinpreview.ReadTail, wantView: builtinpreview.ViewAuto},
		{key: KeyDown, wantMode: builtinpreview.ReadRange, wantOffset: 131072, wantView: builtinpreview.ViewAuto},
		{key: KeyUp, wantMode: builtinpreview.ReadRange, wantOffset: 0, wantView: builtinpreview.ViewAuto},
		{key: KeyRename, wantMode: builtinpreview.ReadRange, wantOffset: 65536, wantView: builtinpreview.ViewRawJSON},
	}
	for _, test := range tests {
		got, intents := Reduce(model, KeyPress{Key: test.key})
		if len(intents) != 2 || intents[0].Kind != IntentPreviewCancel || intents[1].Kind != IntentPreview {
			t.Fatalf("key %q intents = %#v", test.key, intents)
		}
		intent := intents[1]
		if intent.PreviewMode != test.wantMode || intent.PreviewOffset != test.wantOffset || intent.PreviewView != test.wantView {
			t.Fatalf("key %q intent = %#v", test.key, intent)
		}
		if got.Preview.View != test.wantView {
			t.Fatalf("key %q view = %q, want %q", test.key, got.Preview.View, test.wantView)
		}
	}
}

func TestPreviewDrawerScrollsRenderedLinesBeforeRequestingAnotherRange(t *testing.T) {
	model := testModel(t)
	model, _ = Reduce(model, KeyPress{Key: KeyDown})
	model, _ = Reduce(model, KeyPress{Key: KeyPreviewDrawer})
	model.Preview = PreviewState{
		Identity: PreviewRequestIdentity{Mode: builtinpreview.ReadHead, RequestedLimit: builtinpreview.ReadChunkBytes},
		Data:     []byte("line zero\nline one\nline two"),
		View:     builtinpreview.ViewAuto,
	}

	model, intents := Reduce(model, KeyPress{Key: KeyDown})
	if len(intents) != 0 || model.Preview.LineOffset != 1 {
		t.Fatalf("scroll down model=%#v intents=%#v", model.Preview, intents)
	}
	model, intents = Reduce(model, KeyPress{Key: KeyUp})
	if len(intents) != 0 || model.Preview.LineOffset != 0 {
		t.Fatalf("scroll up model=%#v intents=%#v", model.Preview, intents)
	}
	model.Preview.LineOffset = 2
	model, intents = Reduce(model, KeyPress{Key: KeyDown})
	if len(intents) != 0 || model.Preview.LineOffset != 2 {
		t.Fatalf("scroll past complete preview model=%#v intents=%#v", model.Preview, intents)
	}
}
