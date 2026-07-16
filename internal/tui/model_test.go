package tui

import (
	"fmt"
	"reflect"
	"strings"
	"testing"

	"github.com/TyrantLucifer/awesome-mac-sftp/internal/domain"
	"github.com/TyrantLucifer/awesome-mac-sftp/internal/job"
	"github.com/TyrantLucifer/awesome-mac-sftp/internal/state/jobstore"
	"github.com/TyrantLucifer/awesome-mac-sftp/internal/transfer"
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

func TestReducerAppliesBoundedCountsOnlyToSafeNavigation(t *testing.T) {
	model := modelWithEntryCount(t, 20)
	model, _ = Reduce(model, CountDigit{Digit: 1})
	model, _ = Reduce(model, CountDigit{Digit: 2})
	if model.Count != 12 {
		t.Fatalf("pending count = %d, want 12", model.Count)
	}
	model, intents := Reduce(model, KeyPress{Key: KeyDown})
	if len(intents) != 0 || model.Panes[Left].Cursor != 12 || model.Count != 0 {
		t.Fatalf("counted down model=%#v intents=%#v", model, intents)
	}

	model, _ = Reduce(model, CountDigit{Digit: 9})
	model, intents = Reduce(model, KeyPress{Key: KeyOpen})
	if len(intents) != 0 || model.Panes[Left].Cursor != 12 || model.Count != 0 {
		t.Fatalf("unsafe counted action changed state: model=%#v intents=%#v", model, intents)
	}

	model, _ = Reduce(model, CountDigit{Digit: 9})
	model, _ = Reduce(model, KeyPress{Key: KeyUp})
	if model.Panes[Left].Cursor != 3 {
		t.Fatalf("counted up cursor = %d, want 3", model.Panes[Left].Cursor)
	}

	model.Count = navigationCountLimit
	model, _ = Reduce(model, CountDigit{Digit: 9})
	if model.Count != navigationCountLimit {
		t.Fatalf("overflowing count = %d, want bounded %d", model.Count, navigationCountLimit)
	}
}

func TestReducerEmitsOnlyReadOnlyNavigationIntents(t *testing.T) {
	model := testModel(t)

	model, intents := Reduce(model, KeyPress{Key: KeyParent})
	assertSingleIntent(t, intents, IntentList, "/")

	model, intents = Reduce(model, KeyPress{Key: KeyOpen})
	assertSingleIntent(t, intents, IntentList, "/left/dir")

	model, _ = Reduce(model, KeyPress{Key: KeyDown})
	_, intents = Reduce(model, KeyPress{Key: KeyOpen})
	intent := assertSingleIntent(t, intents, IntentPreview, "/left/file.txt")
	if intent.Limit != PreviewByteLimit {
		t.Fatalf("preview limit = %d, want %d", intent.Limit, PreviewByteLimit)
	}
}

func TestReducerCapturesFrozenCopyOrCutAndPastesIntoCurrentPane(t *testing.T) {
	for _, test := range []struct {
		name      string
		key       Key
		clipboard transfer.ClipboardKind
	}{
		{name: "copy", key: KeyCopy, clipboard: transfer.ClipboardCopy},
		{name: "cut", key: KeyCut, clipboard: transfer.ClipboardCut},
	} {
		t.Run(test.name, func(t *testing.T) {
			model := testModel(t)
			model, _ = Reduce(model, KeyPress{Key: KeyDown})
			model, intents := Reduce(model, KeyPress{Key: test.key})
			capture := assertSingleIntent(t, intents, IntentTransferCapture, "/left/file.txt")
			if capture.Clipboard != test.clipboard {
				t.Fatalf("capture clipboard = %q, want %q", capture.Clipboard, test.clipboard)
			}
			reference := transfer.FileRef{
				Location: capture.Location, Kind: domain.EntryFile,
				CapabilityRevision: domain.CapabilityRevision{SessionID: "sess_aaaaaaaaaaaaaaaaaaaaaaaaaa", Generation: 7},
			}
			model, _ = Reduce(model, ClipboardCaptured{Clipboard: test.clipboard, Reference: reference})
			model, _ = Reduce(model, KeyPress{Key: KeyTab})
			model, intents = Reduce(model, KeyPress{Key: KeyPaste})
			if test.clipboard == transfer.ClipboardCut {
				if len(intents) != 0 || model.Mode != ModeMoveConfirm {
					t.Fatalf("cut paste bypassed confirmation: model=%#v intents=%#v", model, intents)
				}
				model, intents = Reduce(model, KeyPress{Key: KeySubmit})
			}
			if len(intents) != 1 {
				t.Fatalf("paste intents = %#v, want one", intents)
			}
			paste := intents[0]
			if paste.Kind != IntentCreateCopyJob || paste.Pane != Right || paste.Location.Path != "/right" {
				t.Fatalf("paste route = %#v", paste)
			}
			if paste.Clipboard != test.clipboard || paste.Source.Location != reference.Location || paste.Name != "file.txt" {
				t.Fatalf("paste intent = %#v", paste)
			}
			model, _ = Reduce(model, CountDigit{Digit: 2})
			model, intents = Reduce(model, KeyPress{Key: KeyPaste})
			if test.clipboard == transfer.ClipboardCut {
				if len(intents) != 0 || model.Mode != ModeMoveConfirm {
					t.Fatalf("counted cut bypassed confirmation: model=%#v intents=%#v", model, intents)
				}
				model, intents = Reduce(model, KeyPress{Key: KeySubmit})
			}
			if len(intents) != 2 || intents[0].Source != reference || intents[1].Source != reference {
				t.Fatalf("counted paste intents = %#v", intents)
			}
			repeatedModel, repeated := Reduce(model, KeyPress{Key: KeyRepeat})
			if test.clipboard == transfer.ClipboardCut {
				if len(repeated) != 0 || repeatedModel.Mode != ModeMoveConfirm {
					t.Fatalf("cut repeat bypassed confirmation: model=%#v intents=%#v", repeatedModel, repeated)
				}
				_, repeated = Reduce(repeatedModel, KeyPress{Key: KeySubmit})
			}
			if !reflect.DeepEqual(repeated, intents) {
				t.Fatalf("repeat intents = %#v, want frozen %#v", repeated, intents)
			}
		})
	}
}

func TestReducerCapturesDirectoriesAndMultiSelectionAsFrozenClipboard(t *testing.T) {
	model := testModel(t)
	model, _ = Reduce(model, KeyPress{Key: KeyMark})
	model, _ = Reduce(model, KeyPress{Key: KeyDown})
	model, _ = Reduce(model, KeyPress{Key: KeyMark})
	model, intents := Reduce(model, KeyPress{Key: KeyCopy})
	if len(intents) != 1 || intents[0].Kind != IntentTransferCapture || len(intents[0].Locations) != 2 {
		t.Fatalf("capture intents = %#v, want one two-item capture", intents)
	}
	references := []transfer.FileRef{
		{Location: intents[0].Locations[0], Kind: domain.EntryDirectory},
		{Location: intents[0].Locations[1], Kind: domain.EntryFile},
	}
	model, _ = Reduce(model, ClipboardCaptured{Clipboard: transfer.ClipboardCopy, References: references})
	model, _ = Reduce(model, KeyPress{Key: KeyTab})
	_, intents = Reduce(model, KeyPress{Key: KeyPaste})
	if len(intents) != 2 {
		t.Fatalf("paste intents = %#v, want two", intents)
	}
	for index, intent := range intents {
		if intent.Kind != IntentCreateCopyJob || intent.Source.Location != references[index].Location {
			t.Fatalf("paste intent %d = %#v", index, intent)
		}
	}
}

func TestReducerDeleteRequiresPreparationAndTwoConfirmations(t *testing.T) {
	model := testModel(t)
	model, intents := Reduce(model, KeyPress{Key: KeyDelete})
	prepare := assertSingleIntent(t, intents, IntentPrepareDelete, "/left/dir")
	if len(prepare.Locations) != 1 {
		t.Fatalf("delete locations = %#v", prepare.Locations)
	}
	reference := transfer.FileRef{Location: prepare.Location, Kind: domain.EntryDirectory}
	model, intents = Reduce(model, DeletePrepared{References: []transfer.FileRef{reference}})
	if model.Mode != ModeDeleteConfirm || model.DeleteConfirmation != 1 || len(intents) != 0 {
		t.Fatalf("prepared delete model=%#v intents=%#v", model, intents)
	}
	model, intents = Reduce(model, KeyPress{Key: KeySubmit})
	if model.DeleteConfirmation != 2 || len(intents) != 0 {
		t.Fatalf("first confirmation model=%#v intents=%#v", model, intents)
	}
	model, intents = Reduce(model, KeyPress{Key: KeySubmit})
	if model.Mode != ModeNormal || len(intents) != 1 || intents[0].Kind != IntentCreateDeleteJob {
		t.Fatalf("second confirmation model=%#v intents=%#v", model, intents)
	}
	if intents[0].Target != reference || !intents[0].Recursive || !intents[0].Confirmed || !intents[0].IrreversibleConfirmed {
		t.Fatalf("delete intent = %#v", intents[0])
	}

	model, intents = Reduce(model, KeyPress{Key: KeyRepeat})
	if model.Mode != ModeDeleteConfirm || model.DeleteConfirmation != 1 || len(intents) != 0 {
		t.Fatalf("repeat bypassed confirmation: model=%#v intents=%#v", model, intents)
	}
}

func TestReducerCountFreezesBatchButCannotRepeatDestructiveConfirmation(t *testing.T) {
	model := testModel(t)
	model, _ = Reduce(model, CountDigit{Digit: 2})
	model, intents := Reduce(model, KeyPress{Key: KeyDelete})
	if len(intents) != 1 || len(intents[0].Locations) != 2 || model.Count != 0 {
		t.Fatalf("counted delete model=%#v intents=%#v", model, intents)
	}
	references := []transfer.FileRef{
		{Location: intents[0].Locations[0], Kind: domain.EntryDirectory},
		{Location: intents[0].Locations[1], Kind: domain.EntryFile},
	}
	model, _ = Reduce(model, DeletePrepared{References: references})
	model, _ = Reduce(model, KeyPress{Key: KeySubmit})
	model, intents = Reduce(model, KeyPress{Key: KeySubmit})
	if len(intents) != 2 {
		t.Fatalf("confirmed batch intents = %#v", intents)
	}
	model, intents = Reduce(model, KeyPress{Key: KeyRepeat})
	if len(intents) != 0 || model.Mode != ModeDeleteConfirm || model.DeleteConfirmation != 1 {
		t.Fatalf("repeat bypassed batch confirmation: model=%#v intents=%#v", model, intents)
	}
}

func TestReducerRenameUsesFrozenReferenceAndRejectsMultiSelection(t *testing.T) {
	model := testModel(t)
	model, _ = Reduce(model, KeyPress{Key: KeyDown})
	model, intents := Reduce(model, KeyPress{Key: KeyRename})
	prepare := assertSingleIntent(t, intents, IntentPrepareRename, "/left/file.txt")
	reference := transfer.FileRef{Location: prepare.Location, Kind: domain.EntryFile}
	model, _ = Reduce(model, RenamePrepared{Reference: reference})
	model, _ = Reduce(model, TextInput{Text: "renamed.txt"})
	model, intents = Reduce(model, KeyPress{Key: KeySubmit})
	if model.Mode != ModeNormal || len(intents) != 1 || intents[0].Kind != IntentCreateCopyJob {
		t.Fatalf("rename submit model=%#v intents=%#v", model, intents)
	}
	if intents[0].Clipboard != transfer.ClipboardCut || intents[0].Source != reference || intents[0].Name != "renamed.txt" || intents[0].Location.Path != "/left" {
		t.Fatalf("rename intent = %#v", intents[0])
	}
	model, repeated := Reduce(model, KeyPress{Key: KeyRepeat})
	if len(repeated) != 0 || model.Mode != ModeMoveConfirm {
		t.Fatalf("rename repeat bypassed confirmation: model=%#v intents=%#v", model, repeated)
	}

	model = testModel(t)
	model, _ = Reduce(model, KeyPress{Key: KeyMark})
	model, _ = Reduce(model, KeyPress{Key: KeyDown})
	model, _ = Reduce(model, KeyPress{Key: KeyMark})
	model, intents = Reduce(model, KeyPress{Key: KeyRename})
	if len(intents) != 0 || !strings.Contains(model.Notice, "one item") {
		t.Fatalf("multi rename model=%#v intents=%#v", model, intents)
	}
}

func TestReducerOpensMinimalDurableJobsView(t *testing.T) {
	model := testModel(t)
	model, intents := Reduce(model, KeyPress{Key: KeyJobs})
	if !model.ShowJobs || len(intents) != 1 || intents[0].Kind != IntentJobList {
		t.Fatalf("open Jobs model=%#v intents=%#v", model, intents)
	}
	view := transfer.JobView{Snapshot: jobstore.Snapshot{JobID: "job_aaaaaaaaaaaaaaaaaaaaaaaaaa", State: job.StateWaitingAuth}, Phase: transfer.PhaseStreaming, Bytes: 42, Items: 1, WaitingReason: "waiting_auth"}
	model, _ = Reduce(model, JobsLoaded{Jobs: []transfer.JobView{view}})
	if len(model.Jobs) != 1 || model.Jobs[0].Snapshot.JobID != view.Snapshot.JobID {
		t.Fatalf("Jobs model = %#v", model.Jobs)
	}
	model, intents = Reduce(model, KeyPress{Key: KeyJobs})
	if model.ShowJobs || len(intents) != 0 {
		t.Fatalf("close Jobs model=%#v intents=%#v", model, intents)
	}
}

func TestReducerControlsSelectedDurableJob(t *testing.T) {
	model := testModel(t)
	model.ShowJobs = true
	first := transfer.JobView{Snapshot: jobstore.Snapshot{JobID: "job_aaaaaaaaaaaaaaaaaaaaaaaaaa", State: job.StateRunning}}
	second := transfer.JobView{Snapshot: jobstore.Snapshot{JobID: "job_aaaaaaaaaaaaaaaaaaaaaaaaab", State: job.StateWaitingConflict}}
	model, _ = Reduce(model, JobsLoaded{Jobs: []transfer.JobView{first, second}})
	model, _ = Reduce(model, KeyPress{Key: KeyDown})
	if model.JobCursor != 1 {
		t.Fatalf("Job cursor = %d, want 1", model.JobCursor)
	}
	_, intents := Reduce(model, KeyPress{Key: KeyJobPause})
	if len(intents) != 1 || intents[0].Kind != IntentJobPause || intents[0].JobID != second.Snapshot.JobID {
		t.Fatalf("pause intents = %#v", intents)
	}
	_, intents = Reduce(model, KeyPress{Key: KeyConflictAutoRenameAll})
	if len(intents) != 1 || intents[0].Kind != IntentJobResolveConflict || intents[0].Resolution != transfer.ConflictAutoRename || !intents[0].ApplyAll {
		t.Fatalf("conflict intents = %#v", intents)
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

func TestReducerCollectsWorkspaceNameAndEmitsSaveIntent(t *testing.T) {
	model := testModel(t)
	model, _ = Reduce(model, KeyPress{Key: KeySave})
	if model.Mode != ModeWorkspace {
		t.Fatalf("mode = %q, want %q", model.Mode, ModeWorkspace)
	}
	model, _ = Reduce(model, TextInput{Text: "release界"})
	model, intents := Reduce(model, KeyPress{Key: KeySubmit})
	if model.Mode != ModeNormal || len(intents) != 1 || intents[0].Kind != IntentWorkspaceSave || intents[0].Name != "release界" {
		t.Fatalf("save result model=%#v intents=%#v", model, intents)
	}
	model, _ = Reduce(model, WorkspaceSaveResult{Name: "release界"})
	if model.Notice != "workspace saved: release界" {
		t.Fatalf("notice = %q", model.Notice)
	}
}

func TestWorkspaceSaveModalRequiresNameAndCanCancel(t *testing.T) {
	model := testModel(t)
	model, _ = Reduce(model, KeyPress{Key: KeySave})
	model, intents := Reduce(model, KeyPress{Key: KeySubmit})
	if len(intents) != 0 || model.Mode != ModeWorkspace || model.Notice != "workspace name is required" {
		t.Fatalf("empty submit model=%#v intents=%#v", model, intents)
	}
	model, _ = Reduce(model, TextInput{Text: "draft"})
	model, _ = Reduce(model, KeyPress{Key: KeyBackspace})
	model, _ = Reduce(model, KeyPress{Key: KeyEscape})
	if model.Mode != ModeNormal || len(model.workspaceName) != 0 {
		t.Fatalf("canceled workspace model=%#v", model)
	}
}

func TestPaneSortHiddenAndRefreshControlsRemainIndependent(t *testing.T) {
	model := testModel(t)
	hidden := testEntry(leftEndpointID, "/left/.hidden", domain.EntryFile)
	large := testEntry(leftEndpointID, "/left/large", domain.EntryFile)
	large.Metadata.Size = uint64Pointer(200)
	small := testEntry(leftEndpointID, "/left/small", domain.EntryFile)
	small.Metadata.Size = uint64Pointer(10)
	left := model.Panes[Left]
	left.Entries = append(left.Entries, hidden, large, small)
	left.rebuildVisible()
	model.Panes[Left] = left

	if names := model.Panes[Left].VisibleNames(); containsString(names, ".hidden") {
		t.Fatalf("hidden entry visible by default: %#v", names)
	}
	model, _ = Reduce(model, KeyPress{Key: KeyToggleHidden})
	if names := model.Panes[Left].VisibleNames(); !containsString(names, ".hidden") {
		t.Fatalf("hidden entry missing after toggle: %#v", names)
	}
	model, _ = Reduce(model, KeyPress{Key: KeySort})
	if model.Panes[Left].Sort.Key != SortSize {
		t.Fatalf("sort = %#v, want size", model.Panes[Left].Sort)
	}
	names := model.Panes[Left].VisibleNames()
	if indexOf(names, "small") > indexOf(names, "large") {
		t.Fatalf("size sort names = %#v", names)
	}
	unknown := testEntry(leftEndpointID, "/left/unknown-size", domain.EntryFile)
	left = model.Panes[Left]
	left.Entries = append(left.Entries, unknown)
	left.Sort.Descending = true
	left.rebuildVisible()
	if names := left.VisibleNames(); indexOf(names, "large") > indexOf(names, "unknown-size") || indexOf(names, "small") > indexOf(names, "unknown-size") {
		t.Fatalf("descending size sort did not keep unknown metadata after known values: %#v", names)
	}
	model.Panes[Left] = left
	beforeRight := model.Panes[Right]
	_, intents := Reduce(model, KeyPress{Key: KeyRefresh})
	assertSingleIntent(t, intents, IntentList, "/left")
	if !reflect.DeepEqual(model.Panes[Right], beforeRight) {
		t.Fatal("left controls changed right pane")
	}
}

func TestDirectPathModeRequiresCanonicalAbsolutePath(t *testing.T) {
	model := testModel(t)
	model, _ = Reduce(model, KeyPress{Key: KeyPath})
	if model.Mode != ModePath {
		t.Fatalf("mode = %q, want path", model.Mode)
	}
	model, _ = Reduce(model, TextInput{Text: "relative"})
	model, intents := Reduce(model, KeyPress{Key: KeySubmit})
	if len(intents) != 0 || model.Mode != ModePath || model.Notice == "" {
		t.Fatalf("relative submit model=%#v intents=%#v", model, intents)
	}
	model, _ = Reduce(model, KeyPress{Key: KeyEscape})
	model, _ = Reduce(model, KeyPress{Key: KeyPath})
	model, _ = Reduce(model, TextInput{Text: "/srv/data"})
	model, intents = Reduce(model, KeyPress{Key: KeySubmit})
	assertSingleIntent(t, intents, IntentList, "/srv/data")
	if model.Mode != ModeNormal {
		t.Fatalf("mode after path submit = %q", model.Mode)
	}
}

func TestConnectionRecoveryStateIsPaneLocalAndReplacesCapabilities(t *testing.T) {
	model := testModel(t)
	originalEntries := append([]domain.Entry(nil), model.Panes[Left].Entries...)
	model, _ = Reduce(model, PaneConnectionChanged{Pane: Left, State: domain.StateConnecting, Message: "reconnecting 1/4"})
	if model.Panes[Left].Connection != domain.StateConnecting || model.Panes[Right].Connection != domain.StateReady {
		t.Fatalf("pane connection states = left %q right %q", model.Panes[Left].Connection, model.Panes[Right].Connection)
	}
	model, _ = Reduce(model, PaneConnectionChanged{Pane: Left, State: domain.StateDisconnected, Message: "reconnect exhausted"})
	if !reflect.DeepEqual(model.Panes[Left].Entries, originalEntries) || model.Panes[Left].Listing.Message != "reconnect exhausted" {
		t.Fatalf("failed reconnect discarded committed pane: %#v", model.Panes[Left])
	}
	newEndpoint := domain.Endpoint{ID: domain.EndpointID("ep_cccccccccccccccccccccccccc"), Kind: domain.EndpointSSH, DisplayName: "work", SSHHostAlias: "work"}
	newLocation, err := domain.NewLocation(newEndpoint.ID, "/left")
	if err != nil {
		t.Fatal(err)
	}
	model, intents := Reduce(model, PaneConnected{Pane: Left, Endpoint: newEndpoint, Location: newLocation, State: domain.StateReady, CapabilityGeneration: 7})
	assertSingleIntent(t, intents, IntentList, "/left")
	if model.Panes[Left].Connection != domain.StateReady || model.Panes[Left].CapabilityGeneration != 7 {
		t.Fatalf("reconnected pane = %#v", model.Panes[Left])
	}
}

func TestEndpointSwitchCommitsEndpointAndLocationOnFirstSuccessfulPage(t *testing.T) {
	model := testModel(t)
	oldCapabilities := mustCapabilitySnapshot(t, "sess_aaaaaaaaaaaaaaaaaaaaaaaaaa", 9, "read")
	left := model.Panes[Left]
	left.Endpoint.Kind = domain.EndpointSSH
	left.Endpoint.DisplayName = "old"
	left.Endpoint.SSHHostAlias = "old"
	left.Capabilities = oldCapabilities
	left.CapabilityGeneration = oldCapabilities.Revision.Generation
	model.Panes[Left] = left
	before := model.Panes[Left]
	newEndpoint := domain.Endpoint{ID: domain.EndpointID("ep_cccccccccccccccccccccccccc"), Kind: domain.EndpointSSH, DisplayName: "work", SSHHostAlias: "work"}
	newLocation := mustLocation(t, newEndpoint.ID, "/srv/data")
	newCapabilities := mustCapabilitySnapshot(t, "sess_bbbbbbbbbbbbbbbbbbbbbbbbbb", 1, "metadata")

	model, intents := Reduce(model, PaneConnected{
		Pane:                 Left,
		Endpoint:             newEndpoint,
		Location:             newLocation,
		State:                domain.StateReady,
		CapabilityGeneration: newCapabilities.Revision.Generation,
		Capabilities:         newCapabilities,
		PreserveCommitted:    true,
	})
	intent := assertSingleIntent(t, intents, IntentList, "/srv/data")
	if model.Panes[Left].Endpoint != before.Endpoint || model.Panes[Left].Location != before.Location {
		t.Fatalf("connected switch committed before validation: %#v", model.Panes[Left])
	}
	if model.Panes[Left].Endpoint.ID != model.Panes[Left].Location.EndpointID {
		t.Fatalf("endpoint/location invariant broken before listing: %#v", model.Panes[Left])
	}

	model, _ = Reduce(model, BeginListing{
		Pane:                 Left,
		Generation:           20,
		Location:             intent.Location,
		Endpoint:             intent.Endpoint,
		Connection:           intent.Connection,
		CapabilityGeneration: intent.CapabilityGeneration,
		Capabilities:         intent.Capabilities,
		CommitEndpoint:       intent.CommitEndpoint,
	})
	if model.Panes[Left].Endpoint != before.Endpoint || model.Panes[Left].Location != before.Location {
		t.Fatalf("begin switch committed before validation: %#v", model.Panes[Left])
	}
	model, _ = Reduce(model, ListingFailed{Pane: Left, Generation: 20, Message: "permission denied"})
	if model.Panes[Left].Endpoint != before.Endpoint || model.Panes[Left].Location != before.Location || model.Panes[Left].Connection != before.Connection || !reflect.DeepEqual(model.Panes[Left].Capabilities, oldCapabilities) {
		t.Fatalf("failed switch changed committed pane: before=%#v after=%#v", before, model.Panes[Left])
	}

	model, intents = Reduce(model, PaneConnected{
		Pane:                 Left,
		Endpoint:             newEndpoint,
		Location:             newLocation,
		State:                domain.StateReady,
		CapabilityGeneration: newCapabilities.Revision.Generation,
		Capabilities:         newCapabilities,
		PreserveCommitted:    true,
	})
	intent = assertSingleIntent(t, intents, IntentList, "/srv/data")
	model, _ = Reduce(model, BeginListing{
		Pane:                 Left,
		Generation:           21,
		Location:             intent.Location,
		Endpoint:             intent.Endpoint,
		Connection:           intent.Connection,
		CapabilityGeneration: intent.CapabilityGeneration,
		Capabilities:         intent.Capabilities,
		CommitEndpoint:       intent.CommitEndpoint,
	})
	model, intents = Reduce(model, ListingPage{Pane: Left, Generation: 21, Done: true})
	pane := model.Panes[Left]
	if pane.Endpoint != newEndpoint || pane.Location != newLocation || pane.Connection != domain.StateReady || pane.CapabilityGeneration != 1 || !reflect.DeepEqual(pane.Capabilities, newCapabilities) {
		t.Fatalf("successful switch pane = %#v", pane)
	}
	if pane.Endpoint.ID != pane.Location.EndpointID {
		t.Fatalf("endpoint/location invariant broken after listing: %#v", pane)
	}
	if len(intents) != 1 || intents[0].Kind != IntentReleaseEndpoint || intents[0].EndpointID != before.Endpoint.ID {
		t.Fatalf("endpoint release intents = %#v, want old endpoint", intents)
	}
}

func mustCapabilitySnapshot(t *testing.T, sessionID domain.SessionID, generation uint64, names ...domain.CapabilityName) domain.CapabilitySnapshot {
	t.Helper()
	items := make([]domain.Capability, len(names))
	for index, name := range names {
		items[index] = domain.Capability{Name: name, Version: 1}
	}
	snapshot, err := domain.NewCapabilitySnapshot(domain.CapabilityRevision{SessionID: sessionID, Generation: generation}, true, items)
	if err != nil {
		t.Fatal(err)
	}
	return snapshot
}

func TestEndpointModeEmitsActivePaneConnectionWithoutChangingOtherPane(t *testing.T) {
	model := testModel(t)
	originalRight := model.Panes[Right]
	model, _ = Reduce(model, KeyPress{Key: KeyEndpoint})
	if model.Mode != ModeEndpoint {
		t.Fatalf("mode = %q, want endpoint", model.Mode)
	}
	model, _ = Reduce(model, TextInput{Text: "work-host"})
	model, intents := Reduce(model, KeyPress{Key: KeySubmit})
	if len(intents) != 1 || intents[0].Kind != IntentConnectEndpoint || intents[0].Pane != Left || intents[0].Name != "work-host" {
		t.Fatalf("endpoint intents = %#v", intents)
	}
	if !reflect.DeepEqual(model.Panes[Right], originalRight) || model.Mode != ModeNormal {
		t.Fatalf("endpoint mode changed unrelated state: %#v", model)
	}
}

func uint64Pointer(value uint64) *uint64 { return &value }

func containsString(values []string, wanted string) bool { return indexOf(values, wanted) >= 0 }

func indexOf(values []string, wanted string) int {
	for index, value := range values {
		if value == wanted {
			return index
		}
	}
	return -1
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
	if got := model.Panes[Left].VisibleCount(); got != 3 {
		t.Fatalf("visible count after stale page = %d, want committed 3", got)
	}

	model, _ = Reduce(model, ListingPage{
		Pane:       Left,
		Generation: 2,
		Entries:    []domain.Entry{testEntry(leftEndpointID, "/left/partial", domain.EntryFile)},
	})
	model, _ = Reduce(model, ListingFailed{Pane: Left, Generation: 2, Message: "interrupted"})
	pane := model.Panes[Left]
	if pane.VisibleCount() != 1 || pane.Listing.Loading || !pane.Listing.Partial || pane.Connection != domain.StateDegraded {
		t.Fatalf("listing state = %#v visible=%d, want one partial terminal page", pane.Listing, pane.VisibleCount())
	}
}

func TestListingFailurePreservesCommittedPaneState(t *testing.T) {
	model := testModel(t)
	model, _ = Reduce(model, KeyPress{Key: KeyDown})
	model, _ = Reduce(model, KeyPress{Key: KeyMark})
	model, _ = Reduce(model, SetFilter{Pane: Left, Query: "file"})
	before := model.Panes[Left]
	target := mustLocation(t, leftEndpointID, "/left/missing")

	model, _ = Reduce(model, BeginListing{Pane: Left, Generation: 9, Location: target})
	if model.Panes[Left].Location != before.Location || model.Panes[Left].VisibleCount() != before.VisibleCount() {
		t.Fatalf("BeginListing committed unverified target: %#v", model.Panes[Left])
	}
	model, _ = Reduce(model, ListingFailed{Pane: Left, Generation: 9, Message: "not found"})
	after := model.Panes[Left]
	if after.Location != before.Location || after.Cursor != before.Cursor || after.Filter != before.Filter ||
		!reflect.DeepEqual(after.VisibleNames(), before.VisibleNames()) || !reflect.DeepEqual(after.SelectedLocations(), before.SelectedLocations()) {
		t.Fatalf("failed navigation changed committed pane: before=%#v after=%#v", before, after)
	}
	if after.Listing.Loading || after.Listing.Message != "not found" {
		t.Fatalf("failed listing state = %#v", after.Listing)
	}
}

func TestListingFirstSuccessfulPageCommitsTargetIncludingEmptyDirectory(t *testing.T) {
	model := testModel(t)
	target := mustLocation(t, leftEndpointID, "/left/empty")
	model, _ = Reduce(model, BeginListing{Pane: Left, Generation: 10, Location: target})
	if model.Panes[Left].Location == target {
		t.Fatal("target committed before a successful page")
	}
	model, _ = Reduce(model, ListingPage{Pane: Left, Generation: 10, Done: true})
	pane := model.Panes[Left]
	if pane.Location != target || pane.VisibleCount() != 0 || pane.Listing.Loading || !pane.Listing.Complete {
		t.Fatalf("empty target pane = %#v", pane)
	}
}

func TestRefreshRemapsCursorAndMarksByLocation(t *testing.T) {
	model := testModel(t)
	model, _ = Reduce(model, KeyPress{Key: KeyDown})
	model, _ = Reduce(model, KeyPress{Key: KeyMark})
	location := model.Panes[Left].Location
	file := testEntry(leftEndpointID, "/left/file.txt", domain.EntryFile)
	notes := testEntry(leftEndpointID, "/left/notes.md", domain.EntryFile)
	model, _ = Reduce(model, BeginListing{Pane: Left, Generation: 11, Location: location})
	model, _ = Reduce(model, ListingPage{Pane: Left, Generation: 11, Entries: []domain.Entry{notes, file}, Done: true})
	pane := model.Panes[Left]
	if current, ok := pane.currentLocation(); !ok || current != file.Location {
		t.Fatalf("refresh cursor location = %#v, %v; want file", current, ok)
	}
	if selected := pane.SelectedLocations(); len(selected) != 1 || selected[0] != file.Location {
		t.Fatalf("refresh marks = %#v, want file", selected)
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
	if got := model.Panes[Left].VisibleNames(); fmt.Sprint(got) != "[another-file file.txt]" {
		t.Fatalf("filtered names after page = %v", got)
	}

	model, _ = Reduce(model, SetFilter{Pane: Left, Query: ""})
	if got := model.Panes[Left].VisibleCount(); got != 5 {
		t.Fatalf("visible count after clear = %d, want all 5 entries", got)
	}
}

func TestReducerHandlesAuthenticationModalAndEmitsOneResolution(t *testing.T) {
	model := testModel(t)
	model, intents := Reduce(model, AuthChallengeReceived{
		ChallengeID: "challenge-1",
		Endpoint:    "work-host",
		Prompt:      "Password:",
		Kind:        "secret",
	})
	if len(intents) != 0 || model.Mode != ModeAuth || !model.Auth.Active {
		t.Fatalf("challenge state = mode %q auth %#v intents %#v", model.Mode, model.Auth, intents)
	}
	model, _ = Reduce(model, TextInput{Text: "s3cr界t"})
	model, _ = Reduce(model, KeyPress{Key: KeyBackspace})
	model, intents = Reduce(model, KeyPress{Key: KeySubmit})
	if model.Auth.Active || model.Mode != ModeNormal {
		t.Fatalf("resolved state = mode %q auth %#v", model.Mode, model.Auth)
	}
	if len(intents) != 1 || intents[0].Kind != IntentAuthResolve || intents[0].ChallengeID != "challenge-1" || intents[0].Cancel || string(intents[0].Answer) != "s3cr界" {
		t.Fatalf("resolution intents = %#v", intents)
	}

	model, _ = Reduce(model, AuthChallengeReceived{ChallengeID: "challenge-2", Endpoint: "work-host", Prompt: "Continue?", Kind: "confirm"})
	model, intents = Reduce(model, KeyPress{Key: KeyEscape})
	if len(intents) != 1 || !intents[0].Cancel || intents[0].ChallengeID != "challenge-2" || len(intents[0].Answer) != 0 {
		t.Fatalf("cancel intents = %#v", intents)
	}
}

func TestReducerConnectsPaneBeforeListingRemoteLocation(t *testing.T) {
	model := testModel(t)
	endpoint := domain.Endpoint{ID: rightEndpointID, Kind: domain.EndpointSSH, DisplayName: "work-host", SSHHostAlias: "work-host"}
	location := mustLocation(t, rightEndpointID, "/srv/data")

	model, intents := Reduce(model, PaneConnected{Pane: Left, Endpoint: endpoint, Location: location})
	if model.Panes[Left].Endpoint != endpoint || model.Panes[Left].Location != location {
		t.Fatalf("connected pane = %#v", model.Panes[Left])
	}
	assertSingleIntent(t, intents, IntentList, "/srv/data")
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
	path string,
) Intent {
	t.Helper()
	if len(intents) != 1 {
		t.Fatalf("intents = %#v, want one", intents)
	}
	intent := intents[0]
	if intent.Kind != kind || intent.Pane != Left || string(intent.Location.Path) != path {
		t.Fatalf("intent = %#v, want kind=%q pane=%v path=%q", intent, kind, Left, path)
	}
	return intent
}
