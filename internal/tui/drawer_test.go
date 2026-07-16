package tui

import (
	"strings"
	"testing"

	"github.com/TyrantLucifer/awesome-mac-sftp/internal/domain"
	"github.com/TyrantLucifer/awesome-mac-sftp/internal/job"
	"github.com/TyrantLucifer/awesome-mac-sftp/internal/state/jobstore"
	"github.com/TyrantLucifer/awesome-mac-sftp/internal/transfer"
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
		Source:   domain.Location{Path: "/source"}, Final: domain.Location{Path: "/final"},
		Phase: transfer.PhaseStreaming, Bytes: 42, Items: 1, WaitingReason: "waiting_auth",
	}}

	for _, size := range []struct{ width, height int }{{100, 16}, {32, 7}} {
		surface := newMemorySurface(size.width, size.height)
		stats := Render(surface, model, RenderOptions{Overscan: 1})
		got := surface.String()
		for _, want := range []string{"Preview", "[Jobs]", "Log", "waiting_auth"} {
			if !strings.Contains(got, want) {
				t.Fatalf("%dx%d drawer missing %q:\n%s", size.width, size.height, want, got)
			}
		}
		if stats.ListRows >= size.height-2 {
			t.Fatalf("%dx%d list rows = %d, drawer did not reserve a bounded bottom region", size.width, size.height, stats.ListRows)
		}
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
