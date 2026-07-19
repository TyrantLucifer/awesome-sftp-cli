package tui

import (
	"strings"
	"testing"
	"time"

	"github.com/TyrantLucifer/awesome-sftp-cli/internal/domain"
	"github.com/TyrantLucifer/awesome-sftp-cli/internal/edit"
)

func TestColdStartRecoveryChooserIsBoundedSelectableAndExplicit(t *testing.T) {
	model := editTestModel(t)
	oversized := make([]EditRecoveryItem, MaxRecoverableEditSessions+1)
	unchanged, intents := Reduce(model, EditRecoveryLoaded{Sessions: oversized})
	if unchanged.Mode != ModeNormal || len(unchanged.EditRecovery.Items) != 0 || len(intents) != 0 {
		t.Fatalf("oversized recovery list was accepted: mode %q items %d", unchanged.Mode, len(unchanged.EditRecovery.Items))
	}
	first := EditRecoveryItem{
		SessionID: "11111111111111111111111111111111", Purpose: edit.PurposeEditor,
		Location: model.Panes[Left].Entries[0].Location, State: edit.StateAwaitingUploadConfirmation,
		Lifecycle: "awaiting_decision", UpdatedAt: time.Unix(20, 0), Usable: true,
	}
	second := EditRecoveryItem{
		SessionID: "22222222222222222222222222222222", Purpose: edit.PurposeOpener,
		Location: model.Panes[Left].Entries[0].Location, State: edit.StateRecoveryRequired,
		Lifecycle: "recovery", UpdatedAt: time.Unix(10, 0), Diagnostic: "local bytes could not be revalidated",
	}
	model, intents = Reduce(model, EditRecoveryLoaded{Sessions: []EditRecoveryItem{second, first}})
	if model.Mode != ModeEditRecovery || len(intents) != 0 || len(model.EditRecovery.Items) != 2 || model.EditRecovery.Items[0].SessionID != first.SessionID {
		t.Fatalf("loaded recovery = mode %q state %#v intents %#v", model.Mode, model.EditRecovery, intents)
	}
	model, intents = Reduce(model, KeyPress{Key: KeySubmit})
	if model.Mode != ModeNormal || len(intents) != 1 || intents[0].Kind != IntentEditResume || intents[0].EditSessionID != first.SessionID {
		t.Fatalf("resume = mode %q intents %#v", model.Mode, intents)
	}

	model, _ = Reduce(model, KeyPress{Key: KeyEditRecovery})
	model, _ = Reduce(model, KeyPress{Key: KeyDown})
	model, intents = Reduce(model, KeyPress{Key: KeySubmit})
	if len(intents) != 0 || model.Mode != ModeEditRecovery || model.Notice == "" {
		t.Fatalf("unusable recovery = mode %q notice %q intents %#v", model.Mode, model.Notice, intents)
	}
	model, intents = Reduce(model, KeyPress{Key: KeyPreviewDrawer})
	if len(intents) != 1 || intents[0].Kind != IntentPreview || intents[0].Location != second.Location {
		t.Fatalf("inspect recovery = %#v", intents)
	}
}

func TestEditObservationRequiresExplicitUploadDecision(t *testing.T) {
	model := editTestModel(t)
	model, intents := Reduce(model, EditSessionObserved{
		SessionID: "44444444444444444444444444444444", Pane: Left,
		Location: model.Panes[Left].Entries[0].Location, State: edit.StateAwaitingUploadConfirmation,
	})
	if model.Mode != ModeEditDecision || !model.EditDecision.Active || len(intents) != 1 || intents[0].Kind != IntentList {
		t.Fatalf("observation = mode %q, state %#v, intents %#v", model.Mode, model.EditDecision, intents)
	}
	model, intents = Reduce(model, KeyPress{Key: KeySubmit})
	if model.Mode != ModeNormal || len(intents) != 1 || intents[0].Kind != IntentEditDecision || intents[0].EditDecision != edit.DecisionUpload {
		t.Fatalf("confirmation = mode %q, intents %#v", model.Mode, intents)
	}
}

func TestEditLaunchShowsFrozenExecutableBeforeConfirmation(t *testing.T) {
	model := editTestModel(t)
	model, intents := Reduce(model, EditLaunchReady{SessionID: "44444444444444444444444444444444", Pane: Left, Location: model.Panes[Left].Entries[0].Location, Command: "/usr/bin/vi -- /private/cache/file"})
	if model.Mode != ModeEditLaunchConfirm || len(intents) != 0 || model.EditLaunch.Command == "" {
		t.Fatalf("launch ready = mode %q state %#v intents %#v", model.Mode, model.EditLaunch, intents)
	}
	model, intents = Reduce(model, KeyPress{Key: KeySubmit})
	if model.Mode != ModeNormal || len(intents) != 1 || intents[0].Kind != IntentEditLaunch {
		t.Fatalf("launch confirmation = mode %q intents %#v", model.Mode, intents)
	}
}

func TestEditLaunchShowsPinnedOfflineUnknownFreshnessBeforeConfirmation(t *testing.T) {
	model := editTestModel(t)
	model, intents := Reduce(model, EditLaunchReady{
		SessionID: "44444444444444444444444444444444", Pane: Left,
		Location: model.Panes[Left].Entries[0].Location, Command: "/usr/bin/vi -- /private/cache/file",
		Message: "opened pinned offline content; remote freshness is unknown until reconnect revalidation",
	})
	if model.Mode != ModeEditLaunchConfirm || len(intents) != 0 || !strings.Contains(model.Notice, "freshness is unknown") {
		t.Fatalf("offline launch notice = mode %q notice %q intents %#v", model.Mode, model.Notice, intents)
	}
}

func TestEditConflictOffersOverwriteSaveAsAndSkipWithoutDefault(t *testing.T) {
	for _, test := range []struct {
		name     string
		key      Key
		decision edit.DecisionKind
	}{
		{name: "overwrite", key: KeyConflictOverwrite, decision: edit.DecisionOverwrite},
		{name: "skip", key: KeyConflictSkip, decision: edit.DecisionSkip},
	} {
		t.Run(test.name, func(t *testing.T) {
			model := editTestModel(t)
			model, intents := Reduce(model, EditSessionObserved{SessionID: "44444444444444444444444444444444", Pane: Left, Location: model.Panes[Left].Entries[0].Location, State: edit.StateConflict})
			if model.Mode != ModeEditDecision || len(intents) != 1 || intents[0].Kind != IntentList {
				t.Fatalf("conflict opened = mode %q, intents %#v", model.Mode, intents)
			}
			model, intents = Reduce(model, KeyPress{Key: test.key})
			if model.Mode != ModeNormal || len(intents) != 1 || intents[0].EditDecision != test.decision {
				t.Fatalf("decision = mode %q, intents %#v", model.Mode, intents)
			}
		})
	}

	model := editTestModel(t)
	conflictView := edit.ConflictView{Text: "--- remote\n+++ local\n-remote edit\n+local edit\n", Summary: "remote → local conflict diff"}
	model, _ = Reduce(model, EditSessionObserved{
		SessionID: "44444444444444444444444444444444", Pane: Left,
		Location: model.Panes[Left].Entries[0].Location, State: edit.StateConflict, ConflictView: conflictView,
	})
	model, inspect := Reduce(model, KeyPress{Key: KeyPreviewDrawer})
	if model.Mode != ModeEditDecision || model.Drawer.Mode != DrawerPreview || model.Drawer.Focus != FocusDrawer || len(inspect) != 0 ||
		model.Preview.Kind != "conflict-diff" || model.Preview.Summary != conflictView.Summary || model.Preview.DisplayText() != conflictView.Text {
		t.Fatalf("conflict inspect = mode %q intents %#v", model.Mode, inspect)
	}
	model, intents := Reduce(model, KeyPress{Key: KeyConflictAutoRename})
	if model.Mode != ModeEditSaveAs || len(intents) != 0 {
		t.Fatalf("save-as entry = mode %q, intents %#v", model.Mode, intents)
	}
	model, _ = Reduce(model, TextInput{Text: "/safe-copy.txt"})
	model, intents = Reduce(model, KeyPress{Key: KeySubmit})
	if model.Mode != ModeNormal || len(intents) != 1 || intents[0].EditDecision != edit.DecisionSaveAs || string(intents[0].SaveAsTarget.Path) != "/safe-copy.txt" {
		t.Fatalf("save-as = mode %q, intents %#v", model.Mode, intents)
	}
}

func TestEditSaveAsKeepsSeparatorsTypedOneRuneAtATime(t *testing.T) {
	model := editTestModel(t)
	model, _ = Reduce(model, EditSessionObserved{
		SessionID: "44444444444444444444444444444444",
		Pane:      Left,
		Location:  model.Panes[Left].Entries[0].Location,
		State:     edit.StateConflict,
	})
	model, _ = Reduce(model, KeyPress{Key: KeyConflictAutoRename})

	for _, value := range "/safe/copy.txt" {
		model, _ = Reduce(model, TextInput{Text: string(value)})
	}
	model, intents := Reduce(model, KeyPress{Key: KeySubmit})
	if model.Mode != ModeNormal || len(intents) != 1 || string(intents[0].SaveAsTarget.Path) != "/safe/copy.txt" {
		t.Fatalf("save-as = mode %q, intents %#v", model.Mode, intents)
	}
}

func TestEditCompletionRefreshesFrozenPane(t *testing.T) {
	model := editTestModel(t)
	model, intents := Reduce(model, EditSessionFinished{Pane: Left, Location: model.Panes[Left].Entries[0].Location, Message: "done"})
	if len(intents) != 1 || intents[0].Kind != IntentList || intents[0].Pane != Left || intents[0].Location.Path != "/file.txt" {
		t.Fatalf("finished intents = %#v", intents)
	}
}

func TestRemoteOnlyEditPromptsRefreshAndRecoveryIsVisible(t *testing.T) {
	model := editTestModel(t)
	model, intents := Reduce(model, EditSessionObserved{SessionID: "44444444444444444444444444444444", Pane: Left, Location: model.Panes[Left].Entries[0].Location, State: edit.StateRemoteChanged})
	if model.Mode != ModeEditDecision || len(intents) != 1 || intents[0].Kind != IntentList {
		t.Fatalf("remote-only observation = mode %q, intents %#v", model.Mode, intents)
	}
	model, intents = Reduce(model, KeyPress{Key: KeySubmit})
	if len(intents) != 1 || intents[0].EditDecision != edit.DecisionSkip || !intents[0].RefreshAfterEdit {
		t.Fatalf("remote refresh decision = %#v", intents)
	}
	model, _ = Reduce(model, EditRecoveryLoaded{Count: 2})
	if model.RecoverableEdits != 2 || model.Notice == "" {
		t.Fatalf("recovery visibility = count %d notice %q", model.RecoverableEdits, model.Notice)
	}
}

func TestEscapeRetainsPendingEditForRecovery(t *testing.T) {
	model := editTestModel(t)
	model, _ = Reduce(model, EditSessionObserved{SessionID: "44444444444444444444444444444444", Pane: Left, Location: model.Panes[Left].Entries[0].Location, State: edit.StateRecoveryRequired})
	model, intents := Reduce(model, KeyPress{Key: KeyEscape})
	if model.Mode != ModeNormal || len(intents) != 0 || model.Notice == "" {
		t.Fatalf("escape = mode %q, notice %q, intents %#v", model.Mode, model.Notice, intents)
	}
}

func editTestModel(t *testing.T) Model {
	t.Helper()
	endpoint := domain.Endpoint{ID: "ep_aaaaaaaaaaaaaaaaaaaaaaaaaa", Kind: domain.EndpointSSH, SSHHostAlias: "host"}
	location, err := domain.NewLocation(endpoint.ID, "/file.txt")
	if err != nil {
		t.Fatal(err)
	}
	pane := NewPaneState(endpoint, domain.Location{EndpointID: endpoint.ID, Path: "/"})
	pane.Entries = []domain.Entry{{Name: "file.txt", Kind: domain.EntryFile, Location: location}}
	return NewModel(pane, pane)
}
