package tui

import (
	"testing"

	"github.com/TyrantLucifer/awesome-mac-sftp/internal/domain"
	"github.com/TyrantLucifer/awesome-mac-sftp/internal/edit"
)

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
	model, _ = Reduce(model, EditSessionObserved{SessionID: "44444444444444444444444444444444", Pane: Left, Location: model.Panes[Left].Entries[0].Location, State: edit.StateConflict})
	model, inspect := Reduce(model, KeyPress{Key: KeyPreviewDrawer})
	if model.Mode != ModeEditDecision || model.Drawer.Mode != DrawerPreview || model.Drawer.Focus != FocusDrawer || len(inspect) != 1 || inspect[0].Kind != IntentPreview || inspect[0].Location.Path != "/file.txt" {
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
