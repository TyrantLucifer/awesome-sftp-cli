package tui

import (
	"path"
	"strings"
	"unicode/utf8"

	"github.com/TyrantLucifer/awesome-sftp-cli/internal/cache"
	"github.com/TyrantLucifer/awesome-sftp-cli/internal/domain"
	"github.com/TyrantLucifer/awesome-sftp-cli/internal/edit"
	builtinpreview "github.com/TyrantLucifer/awesome-sftp-cli/internal/preview"
	"github.com/TyrantLucifer/awesome-sftp-cli/internal/transfer"
)

func runeByteLen(runes []rune) int {
	total := 0
	for _, value := range runes {
		total += utf8.RuneLen(value)
	}
	return total
}

func appendBoundedRunes(buffer []rune, text, forbidden string, maxBytes int) ([]rune, bool) {
	if text == "" || strings.ContainsAny(text, forbidden) || runeByteLen(buffer)+len(text) > maxBytes {
		return buffer, false
	}
	return append(append([]rune(nil), buffer...), []rune(text)...), true
}

func popLastRune(buffer []rune) []rune {
	if len(buffer) == 0 {
		return buffer
	}
	return append([]rune(nil), buffer[:len(buffer)-1]...)
}

func reduceTextInput(model Model, text string) (Model, []Intent) {
	const singleLineForbidden = "\x00\r\n"

	switch model.Mode {
	case ModeFilenameSearch, ModeContentSearch:
		if updated, ok := appendBoundedRunes(model.searchInput, text, singleLineForbidden, 4096); ok {
			model.searchInput = updated
		}
		return model, nil
	case ModeEditSaveAs:
		if text == "" || strings.ContainsAny(text, singleLineForbidden) || runeByteLen(model.editSaveAs)+len(text) > 4096 {
			return model, nil
		}
		suggested := string(model.EditDecision.Location.Path) + ".copy"
		if strings.HasPrefix(text, "/") && string(model.editSaveAs) == suggested {
			model.editSaveAs = nil
		}
		model.editSaveAs = append(append([]rune(nil), model.editSaveAs...), []rune(text)...)
		return model, nil
	}
	if model.Auth.Active {
		if updated, ok := appendBoundedRunes(model.Auth.answer, text, singleLineForbidden, authAnswerByteLimit); ok {
			model.Auth.answer = updated
		}
		return model, nil
	}
	switch model.Mode {
	case ModeWorkspace:
		if updated, ok := appendBoundedRunes(model.workspaceName, text, singleLineForbidden, 64); ok {
			model.workspaceName = updated
		}
		return model, nil
	case ModePath:
		return reducePathTextInput(model, text)
	case ModeEndpoint:
		if text == "" || strings.ContainsAny(text, singleLineForbidden) || len(model.endpointPicker.Query())+len(text) > 255 {
			return model, nil
		}
		picker := model.endpointPicker.clone()
		picker.SetQuery(picker.Query() + text)
		model.endpointPicker = picker
		model.Notice = ""
		return model, nil
	case ModeRename:
		if updated, ok := appendBoundedRunes(model.renameInput, text, "\x00/\r\n", 255); ok {
			model.renameInput = updated
		}
		return model, nil
	case ModeCommand:
		updated, ok := appendBoundedRunes(model.commandInput, text, singleLineForbidden, CommandByteLimit)
		if !ok {
			model.Notice = "command must be one UTF-8 line of at most 32768 bytes"
			return model, nil
		}
		model.commandInput = updated
		return model, nil
	case ModeFilter:
		if text == "" {
			model.Count = 0
			return model, nil
		}
		pane := model.Panes[model.Active].clone()
		if strings.ContainsAny(text, singleLineForbidden) || len(pane.Filter)+len(text) > 255 {
			model.Notice = "entry jump must be one line of at most 255 bytes"
			return model, nil
		}
		pane.Filter += text
		pane.visible = nil
		pane.rebuildVisible()
		pane.Cursor = 0
		model.Panes[model.Active] = pane
		return model, nil
	default:
		model.Count = 0
		return model, nil
	}
}

func reducePathTextInput(model Model, text string) (Model, []Intent) {
	if len(model.pathInput) == 0 {
		switch text {
		case "g":
			return jumpToBoundary(model, KeyTop)
		case "/":
			model.Mode = ModeContentSearch
			model.searchInput = nil
			model.Notice = "slow SFTP content search; bounded remote reads may be expensive"
			return model, nil
		case "p":
			model.Mode = ModeNormal
			model.CachePolicy = nextCachePolicy(model.CachePolicy)
			model.Notice = "workspace cache policy: " + string(model.CachePolicy)
			return model, []Intent{{Kind: IntentCachePolicy, CachePolicy: model.CachePolicy}}
		case "c", "C":
			model.Mode = ModeCacheClearConfirm
			model.CacheClearScope = CacheClearWorkspace
			if text == "C" {
				model.CacheClearScope = CacheClearAll
			}
			model.Notice = "cache clear only removes currently eligible managed content"
			return model, nil
		case "s", "S":
			pane := model.Panes[model.Active]
			model.Mode = ModeNormal
			return model, []Intent{{Kind: IntentShell, Pane: model.Active, Location: pane.Location, Endpoint: pane.Endpoint, ShellHome: text == "S"}}
		}
	}
	if updated, ok := appendBoundedRunes(model.pathInput, text, "\x00\r\n", 4096); ok {
		model.pathInput = updated
	}
	return model, nil
}

func nextCachePolicy(current cache.Policy) cache.Policy {
	switch current {
	case cache.PolicyLRU:
		return cache.PolicyEphemeral
	case cache.PolicyEphemeral:
		return cache.PolicyPinnedOffline
	default:
		return cache.PolicyLRU
	}
}

func reduceKey(model Model, key Key) (Model, []Intent) {
	switch model.Mode {
	case ModeContentSearchConfirm:
		return reduceContentSearchConfirmKey(model, key)
	case ModeFilenameSearch, ModeContentSearch:
		return reduceSearchInputKey(model, key)
	case ModeCacheClearConfirm:
		return reduceCacheClearConfirmKey(model, key)
	case ModeEditRecovery:
		return reduceEditRecoveryKey(model, key)
	case ModeEditLaunchConfirm:
		return reduceEditLaunchConfirmKey(model, key)
	}
	if model.Mode == ModeNormal && model.CommandRunning && key == KeyEscape {
		model.Notice = "canceling active one-time command"
		return model, []Intent{{Kind: IntentCommandCancel}}
	}
	if key == KeyEditRecovery {
		if len(model.EditRecovery.Items) == 0 {
			model.Notice = "no recoverable edit sessions"
			return model, nil
		}
		model.EditRecovery.Cursor = min(model.EditRecovery.Cursor, len(model.EditRecovery.Items)-1)
		model.Mode = ModeEditRecovery
		return model, nil
	}
	if model.Mode == ModeEditSaveAs {
		return reduceEditSaveAsKey(model, key)
	}
	if model.Mode == ModeEditDecision {
		return reduceEditDecisionKey(model, key)
	}
	if model.Mode == ModeCommand {
		return reduceCommandInputKey(model, key)
	}
	if model.Mode == ModeCommandConfirm {
		return reduceCommandConfirmKey(model, key)
	}
	if model.Auth.Active {
		return reduceAuthKey(model, key)
	}
	switch model.Mode {
	case ModeDeleteConfirm:
		return reduceDeleteConfirmKey(model, key)
	case ModeMoveConfirm:
		return reduceMoveConfirmKey(model, key)
	case ModeRename:
		return reduceRenameKey(model, key)
	case ModeWorkspace:
		return reduceWorkspaceKey(model, key)
	case ModePath:
		return reducePathKey(model, key)
	case ModeEndpoint:
		return reduceEndpointKey(model, key)
	}
	if drawerMode, ok := drawerModeForKey(key); ok {
		return reduceDrawerToggle(model, drawerMode)
	}
	if model.Drawer.Focus == FocusDrawer {
		return reduceDrawerFocusedKey(model, key)
	}
	return reducePaneKey(model, key)
}

func reduceContentSearchConfirmKey(model Model, key Key) (Model, []Intent) {
	switch key {
	case KeyEscape:
		model.Mode = ModeContentSearch
		return model, nil
	case KeySubmit:
		pane := model.Panes[model.Active]
		pattern := string(model.searchInput)
		model.searchInput = nil
		model.Mode = ModeNormal
		model.Drawer = DrawerState{Mode: DrawerContentSearch, Focus: FocusDrawer, Rows: model.Drawer.Rows}
		model.ContentSearch = ContentSearchState{Query: pattern, Loading: true}
		return model, []Intent{{Kind: IntentSearchContent, Pane: model.Active, Location: pane.Location, SearchPattern: pattern}}
	default:
		return model, nil
	}
}

func reduceSearchInputKey(model Model, key Key) (Model, []Intent) {
	content := model.Mode == ModeContentSearch
	switch key {
	case KeyBackspace:
		model.searchInput = popLastRune(model.searchInput)
		return model, nil
	case KeyEscape:
		model.searchInput = nil
		model.Mode = ModeNormal
		return model, nil
	case KeySubmit:
		if len(model.searchInput) == 0 {
			model.Notice = "filename search pattern is required"
			return model, nil
		}
		pane := model.Panes[model.Active]
		pattern := string(model.searchInput)
		if content {
			model.Mode = ModeContentSearchConfirm
			model.Notice = "confirm slow scan: max 1000 files, 1 MiB/file, 32 MiB total, 2 minutes"
			return model, nil
		}
		model.searchInput = nil
		model.Mode = ModeNormal
		model.Drawer = DrawerState{Mode: DrawerSearch, Focus: FocusDrawer, Rows: model.Drawer.Rows}
		model.Search = SearchState{Query: pattern, Loading: true}
		return model, []Intent{{Kind: IntentSearchFilename, Pane: model.Active, Location: pane.Location, SearchPattern: pattern}}
	default:
		return model, nil
	}
}

func reduceCacheClearConfirmKey(model Model, key Key) (Model, []Intent) {
	switch key {
	case KeyEscape:
		model.Mode = ModeNormal
		model.CacheClearScope = ""
		model.Notice = "cache clear canceled"
		return model, nil
	case KeySubmit:
		scope := model.CacheClearScope
		model.Mode = ModeNormal
		model.CacheClearScope = ""
		return model, []Intent{{Kind: IntentCacheClear, Pane: model.Active, Location: model.Panes[model.Active].Location, CacheClearScope: scope}}
	default:
		return model, nil
	}
}

func reduceEditRecoveryKey(model Model, key Key) (Model, []Intent) {
	if len(model.EditRecovery.Items) == 0 {
		model.Mode = ModeNormal
		return model, nil
	}
	switch key {
	case KeyEscape:
		model.Mode = ModeNormal
		model.Notice = "recoverable edits retained; press E to reopen recovery"
		return model, nil
	case KeyDown:
		model.EditRecovery.Cursor = min(model.EditRecovery.Cursor+1, len(model.EditRecovery.Items)-1)
		return model, nil
	case KeyUp:
		model.EditRecovery.Cursor = max(model.EditRecovery.Cursor-1, 0)
		return model, nil
	case KeyPreviewDrawer:
		item := model.EditRecovery.Items[model.EditRecovery.Cursor]
		pane := recoveryPane(model, item.Location)
		paneState := model.Panes[pane]
		return model, []Intent{{Kind: IntentPreview, Pane: pane, Location: item.Location,
			EndpointSession: paneState.Capabilities.Revision.SessionID, EndpointGeneration: paneState.Capabilities.Revision.Generation}}
	case KeySubmit:
		item := model.EditRecovery.Items[model.EditRecovery.Cursor]
		if !item.Usable {
			model.Notice = "recovery retained: " + item.Diagnostic
			return model, nil
		}
		pane := recoveryPane(model, item.Location)
		model.Mode = ModeNormal
		return model, []Intent{{Kind: IntentEditResume, Pane: pane, Location: item.Location, EditSessionID: item.SessionID}}
	default:
		return model, nil
	}
}

func reduceEditLaunchConfirmKey(model Model, key Key) (Model, []Intent) {
	switch key {
	case KeyEscape:
		model.Mode = ModeNormal
		model.EditLaunch = EditLaunchState{}
		model.Notice = "external launch canceled; materialization retained for recovery"
		return model, nil
	case KeySubmit:
		pending := model.EditLaunch
		model.Mode = ModeNormal
		model.EditLaunch = EditLaunchState{}
		return model, []Intent{{Kind: IntentEditLaunch, Pane: pending.Pane, Location: pending.Location, EditSessionID: pending.SessionID}}
	default:
		return model, nil
	}
}

func reduceEditSaveAsKey(model Model, key Key) (Model, []Intent) {
	switch key {
	case KeyBackspace:
		model.editSaveAs = popLastRune(model.editSaveAs)
		return model, nil
	case KeyEscape:
		model.editSaveAs = nil
		model.Mode = ModeEditDecision
		return model, nil
	case KeySubmit:
		target, err := domain.NewLocation(model.EditDecision.Location.EndpointID, domain.CanonicalPath(string(model.editSaveAs)))
		if err != nil || !strings.HasPrefix(string(target.Path), "/") {
			model.Notice = "save-as requires an absolute canonical path"
			return model, nil
		}
		return finishEditDecision(model, edit.DecisionSaveAs, target, false)
	default:
		return model, nil
	}
}

func reduceEditDecisionKey(model Model, key Key) (Model, []Intent) {
	state := model.EditDecision.State
	switch key {
	case KeyEscape:
		model.Mode = ModeNormal
		model.EditDecision = EditDecisionState{}
		model.Notice = "edit retained for recovery; no upload was started"
		return model, nil
	case KeyPreviewDrawer:
		model.Drawer.Mode = DrawerPreview
		model.Drawer.Focus = FocusDrawer
		if model.EditDecision.ConflictView.Text != "" {
			view := model.EditDecision.ConflictView
			data := []byte(view.Text)
			if len(data) > PreviewByteLimit {
				data = data[:PreviewByteLimit]
				view.Truncated = true
			}
			generation := model.Preview.Generation + 1
			if generation == 0 {
				generation = 1
			}
			model.Preview = PreviewState{
				Generation: generation, Location: model.EditDecision.Location, Data: append([]byte(nil), data...),
				BytesRead: len(data), Truncated: view.Truncated, Kind: "conflict-diff", Summary: view.Summary,
			}
			return model, nil
		}
		if state == edit.StateConflict {
			model.Notice = "conflict diff is unavailable; retained edit was not changed"
			return model, nil
		}
		pane := model.Panes[model.EditDecision.Pane]
		return model, []Intent{{
			Kind: IntentPreview, Pane: model.EditDecision.Pane, Location: model.EditDecision.Location,
			EndpointSession: pane.Capabilities.Revision.SessionID, EndpointGeneration: pane.Capabilities.Revision.Generation,
		}}
	case KeyConflictSkip:
		return finishEditDecision(model, edit.DecisionSkip, domain.Location{}, false)
	case KeyConflictAutoRename:
		if state != edit.StateConflict && state != edit.StateAwaitingUploadConfirmation {
			return model, nil
		}
		model.editSaveAs = []rune(string(model.EditDecision.Location.Path) + ".copy")
		model.Mode = ModeEditSaveAs
		return model, nil
	case KeyConflictOverwrite:
		if state == edit.StateConflict {
			return finishEditDecision(model, edit.DecisionOverwrite, domain.Location{}, false)
		}
	case KeySubmit:
		switch state {
		case edit.StateReady:
			pending := model.EditDecision
			model.Mode = ModeNormal
			model.EditDecision = EditDecisionState{}
			return model, []Intent{{Kind: IntentEditCheck, Pane: pending.Pane, Location: pending.Location, EditSessionID: pending.SessionID}}
		case edit.StateAwaitingUploadConfirmation:
			return finishEditDecision(model, edit.DecisionUpload, domain.Location{}, false)
		case edit.StateRemoteChanged:
			return finishEditDecision(model, edit.DecisionSkip, domain.Location{}, true)
		case edit.StateSyncBackFrozen:
			if model.EditDecision.Decision != "" {
				return finishEditDecision(model, model.EditDecision.Decision, domain.Location{}, false)
			}
		}
	}
	return model, nil
}

func reduceCommandInputKey(model Model, key Key) (Model, []Intent) {
	switch key {
	case KeyBackspace:
		model.commandInput = popLastRune(model.commandInput)
		return model, nil
	case KeyEscape:
		model.commandInput = nil
		model.Mode = ModeNormal
		return model, nil
	case KeySubmit:
		if len(model.commandInput) == 0 {
			model.Notice = "command is required"
			return model, nil
		}
		model.Mode = ModeCommandConfirm
		return model, nil
	default:
		return model, nil
	}
}

func reduceCommandConfirmKey(model Model, key Key) (Model, []Intent) {
	switch key {
	case KeyEscape:
		model.commandInput = nil
		model.Mode = ModeNormal
		return model, nil
	case KeySubmit:
		pane := model.Panes[model.Active]
		commandText := string(model.commandInput)
		model.commandInput = nil
		model.Mode = ModeNormal
		model.CommandRunning = true
		model.Notice = "one-time command running; Esc cancels"
		return model, []Intent{{Kind: IntentRunCommand, Pane: model.Active, Location: pane.Location, Endpoint: pane.Endpoint, CommandText: commandText}}
	default:
		return model, nil
	}
}

func reduceAuthKey(model Model, key Key) (Model, []Intent) {
	switch key {
	case KeyBackspace:
		model.Auth.answer = popLastRune(model.Auth.answer)
		return model, nil
	case KeySubmit, KeyEscape:
		intent := Intent{Kind: IntentAuthResolve, ChallengeID: model.Auth.ChallengeID, Cancel: key == KeyEscape}
		if !intent.Cancel {
			intent.Answer = []byte(string(model.Auth.answer))
		}
		returnMode := model.Auth.ReturnMode
		if returnMode == "" || returnMode == ModeAuth {
			returnMode = ModeNormal
		}
		model.Auth = AuthState{}
		model.Mode = returnMode
		return model, []Intent{intent}
	default:
		return model, nil
	}
}

func reduceDeleteConfirmKey(model Model, key Key) (Model, []Intent) {
	switch key {
	case KeyEscape:
		model.pendingDelete = nil
		model.DeleteConfirmation = 0
		model.Mode = ModeNormal
		return model, nil
	case KeySubmit:
		if model.DeleteConfirmation == 1 {
			model.DeleteConfirmation = 2
			return model, nil
		}
		intents := make([]Intent, 0, len(model.pendingDelete))
		for _, reference := range model.pendingDelete {
			intents = append(intents, Intent{
				Kind: IntentCreateDeleteJob, Target: reference,
				Recursive: reference.Kind == domain.EntryDirectory,
				Confirmed: true, IrreversibleConfirmed: true,
			})
		}
		model.repeatDelete = append([]transfer.FileRef(nil), model.pendingDelete...)
		model.repeatIntents = nil
		model.pendingDelete = nil
		model.DeleteConfirmation = 0
		model.Mode = ModeNormal
		return model, intents
	default:
		return model, nil
	}
}

func reduceMoveConfirmKey(model Model, key Key) (Model, []Intent) {
	switch key {
	case KeyEscape:
		model.pendingMove = nil
		model.Mode = ModeNormal
		return model, nil
	case KeySubmit:
		intents := append([]Intent(nil), model.pendingMove...)
		model.repeatMove = append([]Intent(nil), intents...)
		model.repeatDelete = nil
		model.repeatIntents = nil
		model.pendingMove = nil
		model.Mode = ModeNormal
		return model, intents
	default:
		return model, nil
	}
}

func reduceRenameKey(model Model, key Key) (Model, []Intent) {
	switch key {
	case KeyBackspace:
		model.renameInput = popLastRune(model.renameInput)
		return model, nil
	case KeyEscape:
		model.renameInput = nil
		model.pendingRename = transfer.FileRef{}
		model.Mode = ModeNormal
		return model, nil
	case KeySubmit:
		name := string(model.renameInput)
		if name == "" || name == "." || name == ".." || path.Base(name) != name {
			model.Notice = "rename requires one plain entry name"
			return model, nil
		}
		if name == path.Base(string(model.pendingRename.Location.Path)) {
			model.Notice = "rename requires a different name"
			return model, nil
		}
		parent, ok := parentLocation(model.pendingRename.Location)
		if !ok {
			model.Notice = "rename cannot target an endpoint root"
			return model, nil
		}
		intent := Intent{Kind: IntentCreateCopyJob, Pane: model.Active, Location: parent, Clipboard: transfer.ClipboardCut, Source: model.pendingRename, Name: name}
		model.repeatMove = []Intent{intent}
		model.repeatIntents = nil
		model.repeatDelete = nil
		model.renameInput = nil
		model.pendingRename = transfer.FileRef{}
		model.Mode = ModeNormal
		return model, []Intent{intent}
	default:
		return model, nil
	}
}

func reduceWorkspaceKey(model Model, key Key) (Model, []Intent) {
	switch key {
	case KeyBackspace:
		model.workspaceName = popLastRune(model.workspaceName)
		return model, nil
	case KeyEscape:
		model.workspaceName = nil
		model.Mode = ModeNormal
		return model, nil
	case KeySubmit:
		if len(model.workspaceName) == 0 {
			model.Notice = "workspace name is required"
			return model, nil
		}
		name := string(model.workspaceName)
		model.workspaceName = nil
		model.Mode = ModeNormal
		model.Notice = "saving workspace: " + name
		return model, []Intent{{Kind: IntentWorkspaceSave, Name: name}}
	default:
		return model, nil
	}
}

func reducePathKey(model Model, key Key) (Model, []Intent) {
	switch key {
	case KeyBackspace:
		model.pathInput = popLastRune(model.pathInput)
		return model, nil
	case KeyEscape:
		model.pathInput = nil
		model.Mode = ModeNormal
		return model, nil
	case KeySubmit:
		value := string(model.pathInput)
		if !path.IsAbs(value) || path.Clean(value) != value {
			model.Notice = "path must be canonical and absolute"
			return model, nil
		}
		location := domain.Location{EndpointID: model.Panes[model.Active].Endpoint.ID, Path: domain.CanonicalPath(value)}
		model.pathInput = nil
		model.Mode = ModeNormal
		return model, []Intent{{Kind: IntentList, Pane: model.Active, Location: location}}
	default:
		return model, nil
	}
}

func reduceEndpointKey(model Model, key Key) (Model, []Intent) {
	switch key {
	case KeyBackspace:
		query := []rune(model.endpointPicker.Query())
		if len(query) != 0 {
			picker := model.endpointPicker.clone()
			picker.SetQuery(string(query[:len(query)-1]))
			model.endpointPicker = picker
		}
		model.Notice = ""
		return model, nil
	case KeyEscape:
		model.Mode = ModeNormal
		return model, nil
	case KeyDown:
		picker := model.endpointPicker.clone()
		picker.Move(1)
		model.endpointPicker = picker
		return model, nil
	case KeyUp:
		picker := model.endpointPicker.clone()
		picker.Move(-1)
		model.endpointPicker = picker
		return model, nil
	case KeySubmit:
		choice, ok := model.endpointPicker.Selected()
		if !ok {
			model.Notice = "no matching endpoint; choose a configured SSH host or local"
			return model, nil
		}
		model.Mode = ModeNormal
		return model, []Intent{{Kind: IntentConnectEndpoint, Pane: model.Active, Name: choice.Name}}
	default:
		return model, nil
	}
}

func reduceDrawerFocusedKey(model Model, key Key) (Model, []Intent) {
	if model.Drawer.Mode == DrawerPreview && key == KeyPath {
		model.Mode = ModePath
		model.pathInput = nil
		return model, nil
	}
	if key == KeyEscape {
		model.Drawer.Focus = FocusPane
		if model.Drawer.Mode == DrawerSearch && model.Search.Loading && model.Search.Identity.RequestID != "" {
			return model, []Intent{{Kind: IntentSearchCancel, SearchIdentity: model.Search.Identity}}
		}
		if model.Drawer.Mode == DrawerContentSearch && model.ContentSearch.Loading && model.ContentSearch.Identity.RequestID != "" {
			return model, []Intent{{Kind: IntentSearchCancel, ContentSearchIdentity: model.ContentSearch.Identity}}
		}
		return model, nil
	}
	switch model.Drawer.Mode {
	case DrawerSearch:
		return reduceSearchKey(model, key)
	case DrawerContentSearch:
		return reduceContentSearchKey(model, key)
	case DrawerJobs:
		return reduceJobsKey(model, key)
	case DrawerPreview:
		return reducePreviewKey(model, key)
	default:
		return model, nil
	}
}

func reducePaneKey(model Model, key Key) (Model, []Intent) {
	count := model.Count
	model.Count = 0
	if count != 0 && !keyAcceptsCount(key) {
		return model, nil
	}
	steps := max(1, count)
	if key == KeyEscape && model.Preview.Generation != 0 {
		model.Preview = PreviewState{}
		return model, []Intent{{Kind: IntentPreviewCancel}}
	}
	if key == KeyTab {
		return reducePaneSwitch(model)
	}
	pane := model.Panes[model.Active].clone()
	if model.Mode == ModeFilter {
		return reduceFilterKey(model, pane, key)
	}
	return reduceNormalPaneKey(model, pane, key, count, steps)
}

func keyAcceptsCount(key Key) bool {
	switch key {
	case KeyDown, KeyUp, KeyCopy, KeyCut, KeyPaste, KeyDelete, KeyRename:
		return true
	default:
		return false
	}
}

func reducePaneSwitch(model Model) (Model, []Intent) {
	if model.Mode == ModeFilter {
		pane := model.Panes[model.Active].clone()
		restoreDirectoryJump(&pane, &model)
		model.Panes[model.Active] = pane
	}
	if model.Active == Left {
		model.Active = Right
	} else {
		model.Active = Left
	}
	model.Mode = ModeNormal
	if model.Drawer.Mode == DrawerPreview {
		return model, previewRefreshIntents(model)
	}
	return model, nil
}

func restoreDirectoryJump(pane *PaneState, model *Model) {
	pane.Filter = model.jumpFilter
	pane.visible = nil
	pane.rebuildVisible()
	if model.hasJumpAnchor {
		selectPaneLocation(pane, model.jumpAnchor)
	}
	model.clearDirectoryJump()
}

func reduceFilterKey(model Model, pane PaneState, key Key) (Model, []Intent) {
	switch key {
	case KeyEscape:
		restoreDirectoryJump(&pane, &model)
		model.Mode = ModeNormal
	case KeyBackspace:
		if pane.Filter != "" {
			_, size := utf8.DecodeLastRuneInString(pane.Filter)
			pane.Filter = pane.Filter[:len(pane.Filter)-size]
			pane.visible = nil
			pane.rebuildVisible()
			pane.Cursor = 0
		}
	case KeyDown:
		if len(pane.visible) != 0 {
			pane.Cursor = min(pane.Cursor+1, len(pane.visible)-1)
		}
	case KeyUp:
		pane.Cursor = max(pane.Cursor-1, 0)
	case KeySubmit:
		if len(pane.visible) == 0 {
			model.Notice = "no matching entries"
			model.Panes[model.Active] = pane
			return model, nil
		}
		location, hasLocation := pane.currentLocation()
		pane.Filter = ""
		pane.visible = nil
		pane.rebuildVisible()
		if hasLocation {
			selectPaneLocation(&pane, location)
		}
		model.clearDirectoryJump()
		model.Mode = ModeNormal
		model.Notice = "entry selected; available actions are shown below"
	}
	model.Panes[model.Active] = pane
	return model, nil
}

func reduceNormalPaneKey(model Model, pane PaneState, key Key, count, steps int) (Model, []Intent) {
	previousLocation, hadPreviousLocation := pane.currentLocation()
	switch key {
	case KeyCopy, KeyCut:
		locations := selectedLocations(pane, count)
		if len(locations) == 0 {
			model.Notice = "copy/cut requires at least one file or directory"
			return model, nil
		}
		clipboard := transfer.ClipboardCopy
		if key == KeyCut {
			clipboard = transfer.ClipboardCut
		}
		return beginClipboardCapture(model, model.Active, locations, clipboard)
	case KeyPaste:
		repetitions := max(1, count)
		if model.Clipboard.Capturing {
			if repetitions > maxBatchJobIntents {
				model.Notice = "paste count exceeds the bounded Job batch"
				return model, nil
			}
			model.pendingPaste = pendingPasteState{
				Generation: model.Clipboard.Generation, Pane: model.Active, Location: pane.Location,
				Repetitions: repetitions, Active: true,
			}
			model.Notice = "copy/cut capture in progress; paste queued"
			return model, nil
		}
		if !model.Clipboard.Ready {
			model.Notice = "copy/cut clipboard is empty"
			return model, nil
		}
		return pasteClipboard(model, model.Active, pane.Location, repetitions)
	case KeyDelete:
		locations := selectedLocations(pane, count)
		if len(locations) == 0 {
			model.Notice = "delete requires at least one target"
			return model, nil
		}
		return model, []Intent{{Kind: IntentPrepareDelete, Pane: model.Active, Location: locations[0], Locations: locations}}
	case KeyRename:
		locations := selectedLocations(pane, count)
		if len(locations) != 1 {
			model.Notice = "rename requires exactly one item"
			return model, nil
		}
		return model, []Intent{{Kind: IntentPrepareRename, Pane: model.Active, Location: locations[0], Locations: locations}}
	case KeyEdit, KeyOpenExternal:
		entry := pane.visibleEntry(pane.Cursor)
		if entry.Location.Path == "" || entry.Kind != domain.EntryFile {
			model.Notice = "edit/open requires a regular file"
			return model, nil
		}
		kind := IntentEdit
		if key == KeyOpenExternal {
			kind = IntentOpenExternal
		}
		return model, []Intent{{Kind: kind, Pane: model.Active, Location: entry.Location}}
	case KeyCommand:
		if model.CommandRunning {
			model.Notice = "one-time command already running; Esc cancels"
			return model, nil
		}
		model.commandInput = nil
		model.Mode = ModeCommand
		model.Notice = "one-time command: enter text, then confirm"
	case KeyRepeat:
		if len(model.repeatDelete) != 0 {
			model.pendingDelete = append([]transfer.FileRef(nil), model.repeatDelete...)
			model.DeleteConfirmation = 1
			model.Mode = ModeDeleteConfirm
			return model, nil
		}
		if len(model.repeatMove) != 0 {
			model.pendingMove = append([]Intent(nil), model.repeatMove...)
			model.Mode = ModeMoveConfirm
			return model, nil
		}
		if len(model.repeatIntents) != 0 {
			return model, append([]Intent(nil), model.repeatIntents...)
		}
		model.Notice = "no repeatable frozen operation"
		return model, nil
	case KeySave:
		model.Mode = ModeWorkspace
		model.workspaceName = nil
	case KeySort:
		cyclePaneSort(&pane.Sort)
		pane.rebuildVisible()
	case KeyToggleHidden:
		pane.ShowHidden = !pane.ShowHidden
		pane.rebuildVisible()
	case KeyRefresh:
		return model, []Intent{{Kind: IntentList, Pane: model.Active, Location: pane.Location}}
	case KeyPath:
		model.Mode = ModePath
		model.pathInput = nil
	case KeyEndpoint:
		model.Mode = ModeEndpoint
		picker := model.endpointPicker.clone()
		picker.SetQuery("")
		model.endpointPicker = picker
		model.Notice = ""
	case KeyDown:
		if len(pane.visible) != 0 {
			pane.Cursor = min(pane.Cursor+steps, len(pane.visible)-1)
		}
	case KeyUp:
		pane.Cursor = max(pane.Cursor-steps, 0)
	case KeyBottom:
		pane.Cursor = max(len(pane.visible)-1, 0)
	case KeyParent:
		if location, ok := parentLocation(pane.Location); ok {
			return model, []Intent{{Kind: IntentList, Pane: model.Active, Location: location}}
		}
	case KeyOpen:
		entry := pane.visibleEntry(pane.Cursor)
		if entry.Location.Path == "" {
			return model, nil
		}
		if entry.Kind == domain.EntryDirectory {
			return model, []Intent{{Kind: IntentList, Pane: model.Active, Location: entry.Location}}
		}
		model.Drawer.Mode = DrawerPreview
		model.Drawer.Focus = FocusDrawer
		return model, []Intent{previewIntent(model.Active, pane, entry)}
	case KeyVisual, KeyVisualLine:
		if len(pane.visible) != 0 {
			pane.visualAnchor = pane.visibleEntry(pane.Cursor).Location
			pane.visualAnchorView = pane.Cursor
			pane.hasVisualAnchor = true
			if key == KeyVisualLine {
				model.Mode = ModeVisualLine
			} else {
				model.Mode = ModeVisual
			}
		}
	case KeyMark:
		if location, ok := pane.currentLocation(); ok {
			pane.marks = cloneMarks(pane.marks)
			if _, marked := pane.marks[location]; marked {
				delete(pane.marks, location)
			} else {
				pane.marks[location] = struct{}{}
			}
		}
	case KeyFilter:
		model.jumpAnchor, model.hasJumpAnchor = pane.currentLocation()
		model.jumpFilter = pane.Filter
		pane.Filter = ""
		pane.visible = nil
		pane.rebuildVisible()
		if model.hasJumpAnchor {
			selectPaneLocation(&pane, model.jumpAnchor)
		}
		model.Mode = ModeFilter
	case KeyFilenameSearch:
		model.Mode = ModeFilenameSearch
		model.searchInput = nil
	case KeyEscape:
		pane.hasVisualAnchor = false
		model.Mode = ModeNormal
	}
	model.Panes[model.Active] = pane
	if model.Drawer.Mode == DrawerPreview && (key == KeyDown || key == KeyUp || key == KeyBottom) {
		currentLocation, hasCurrentLocation := pane.currentLocation()
		if hadPreviousLocation != hasCurrentLocation || currentLocation != previousLocation {
			return model, previewRefreshIntents(model)
		}
	}
	return model, nil
}

func cyclePaneSort(spec *SortState) {
	switch spec.Key {
	case SortName:
		spec.Key = SortSize
	case SortSize:
		spec.Key = SortModified
	case SortModified:
		spec.Key = SortKind
	default:
		spec.Key = SortName
		spec.Descending = !spec.Descending
	}
}

func reduceSearchKey(model Model, key Key) (Model, []Intent) {
	switch key {
	case KeyDown:
		model.Search.Cursor = min(model.Search.Cursor+1, max(0, len(model.Search.Results)-1))
		return model, nil
	case KeyUp:
		model.Search.Cursor = max(model.Search.Cursor-1, 0)
		return model, nil
	case KeyFilenameSearch:
		model.Mode = ModeFilenameSearch
		model.searchInput = []rune(model.Search.Query)
		return model, nil
	}
	if model.Search.Cursor < 0 || model.Search.Cursor >= len(model.Search.Results) {
		return model, nil
	}
	result := model.Search.Results[model.Search.Cursor]
	pane := recoveryPane(model, result.Location)
	switch key {
	case KeySubmit, KeyOpen:
		model.Active = pane
		model.Drawer.Focus = FocusPane
		if result.Entry.Kind == domain.EntryDirectory {
			return model, []Intent{{Kind: IntentList, Pane: pane, Location: result.Location}}
		}
		model.Drawer.Mode = DrawerPreview
		model.Drawer.Focus = FocusDrawer
		return model, []Intent{searchPreviewIntent(pane, result.Location, model.Search.Identity.SessionID, model.Search.Identity.EndpointGeneration)}
	case KeyCopy:
		return beginClipboardCapture(model, pane, []domain.Location{result.Location}, transfer.ClipboardCopy)
	default:
		return model, nil
	}
}

func reduceContentSearchKey(model Model, key Key) (Model, []Intent) {
	switch key {
	case KeyDown:
		model.ContentSearch.Cursor = min(model.ContentSearch.Cursor+1, max(0, len(model.ContentSearch.Results)-1))
		return model, nil
	case KeyUp:
		model.ContentSearch.Cursor = max(model.ContentSearch.Cursor-1, 0)
		return model, nil
	case KeyFilenameSearch:
		model.Mode = ModeContentSearch
		model.searchInput = []rune(model.ContentSearch.Query)
		return model, nil
	}
	if model.ContentSearch.Cursor < 0 || model.ContentSearch.Cursor >= len(model.ContentSearch.Results) {
		return model, nil
	}
	result := model.ContentSearch.Results[model.ContentSearch.Cursor]
	pane := recoveryPane(model, result.Location)
	switch key {
	case KeySubmit, KeyOpen:
		model.Active = pane
		model.Drawer.Mode = DrawerPreview
		model.Drawer.Focus = FocusDrawer
		return model, []Intent{searchPreviewIntent(pane, result.Location, model.ContentSearch.Identity.SessionID, model.ContentSearch.Identity.EndpointGeneration)}
	case KeyCopy:
		return beginClipboardCapture(model, pane, []domain.Location{result.Location}, transfer.ClipboardCopy)
	default:
		return model, nil
	}
}

func searchPreviewIntent(pane PaneID, location domain.Location, sessionID domain.SessionID, generation uint64) Intent {
	return Intent{
		Kind: IntentPreview, Pane: pane, Location: location, Limit: PreviewByteLimit,
		EndpointSession: sessionID, EndpointGeneration: generation,
		PreviewMode: builtinpreview.ReadHead, PreviewView: builtinpreview.ViewAuto,
	}
}

func finishEditDecision(model Model, decision edit.DecisionKind, saveAs domain.Location, refresh bool) (Model, []Intent) {
	state := model.EditDecision
	intent := Intent{
		Kind: IntentEditDecision, Pane: state.Pane, Location: state.Location,
		EditSessionID: state.SessionID, EditDecision: decision,
		SaveAsTarget: saveAs, RefreshAfterEdit: refresh,
	}
	model.Mode = ModeNormal
	model.EditDecision = EditDecisionState{}
	model.editSaveAs = nil
	return model, []Intent{intent}
}

func reduceJobsKey(model Model, key Key) (Model, []Intent) {
	if key == KeyDown {
		model.JobCursor = min(model.JobCursor+1, max(0, len(model.Jobs)-1))
		return model, nil
	}
	if key == KeyUp {
		model.JobCursor = max(model.JobCursor-1, 0)
		return model, nil
	}
	if len(model.Jobs) == 0 || model.JobCursor < 0 || model.JobCursor >= len(model.Jobs) {
		return model, nil
	}
	jobID := model.Jobs[model.JobCursor].Snapshot.JobID
	intent := Intent{JobID: jobID}
	switch key {
	case KeyJobPause:
		intent.Kind = IntentJobPause
	case KeyJobResume:
		intent.Kind = IntentJobResume
	case KeyJobCancel:
		intent.Kind = IntentJobCancel
	case KeyConflictOverwrite, KeyConflictOverwriteAll:
		intent.Kind = IntentJobResolveConflict
		intent.Resolution = transfer.ConflictOverwrite
		intent.ApplyAll = key == KeyConflictOverwriteAll
	case KeyConflictSkip, KeyConflictSkipAll:
		intent.Kind = IntentJobResolveConflict
		intent.Resolution = transfer.ConflictSkip
		intent.ApplyAll = key == KeyConflictSkipAll
	case KeyConflictAutoRename, KeyConflictAutoRenameAll:
		intent.Kind = IntentJobResolveConflict
		intent.Resolution = transfer.ConflictAutoRename
		intent.ApplyAll = key == KeyConflictAutoRenameAll
	default:
		return model, nil
	}
	return model, []Intent{intent}
}

func reducePreviewKey(model Model, key Key) (Model, []Intent) {
	pane := model.Panes[model.Active]
	entry := pane.visibleEntry(pane.Cursor)
	if entry.Kind != domain.EntryFile || entry.Location.Path == "" {
		return model, nil
	}
	intent := previewIntent(model.Active, pane, entry)
	intent.PreviewMode = model.Preview.Identity.Mode
	intent.PreviewOffset = model.Preview.Identity.Offset
	intent.PreviewView = model.Preview.View
	step := builtinpreview.ReadChunkBytes
	switch key {
	case KeyParent, KeyTop:
		intent.PreviewMode = builtinpreview.ReadHead
		intent.PreviewOffset = 0
	case KeyOpen, KeyBottom:
		intent.PreviewMode = builtinpreview.ReadTail
		intent.PreviewOffset = 0
	case KeyDown:
		lineCount := strings.Count(model.Preview.DisplayText(), "\n") + 1
		if model.Preview.LineOffset < lineCount-1 {
			model.Preview.LineOffset++
			return model, nil
		}
		if !model.Preview.Truncated || model.Preview.Identity.Mode == builtinpreview.ReadTail {
			return model, nil
		}
		intent.PreviewMode = builtinpreview.ReadRange
		if intent.PreviewOffset <= ^uint64(0)-step {
			intent.PreviewOffset += step
		}
	case KeyUp:
		if model.Preview.LineOffset > 0 {
			model.Preview.LineOffset--
			return model, nil
		}
		if !model.Preview.Truncated || model.Preview.Identity.Mode == builtinpreview.ReadHead {
			return model, nil
		}
		intent.PreviewMode = builtinpreview.ReadRange
		if intent.PreviewOffset > step {
			intent.PreviewOffset -= step
		} else {
			intent.PreviewOffset = 0
		}
	case KeyRename:
		intent.PreviewView = builtinpreview.ToggleView(model.Preview.View, true)
		model.Preview.View = intent.PreviewView
	default:
		return model, nil
	}
	return model, []Intent{{Kind: IntentPreviewCancel}, intent}
}

func jumpToBoundary(model Model, key Key) (Model, []Intent) {
	model.Mode = ModeNormal
	model.pathInput = nil
	if model.Drawer.Focus == FocusDrawer && model.Drawer.Mode == DrawerPreview {
		return reducePreviewKey(model, key)
	}

	pane := model.Panes[model.Active].clone()
	previousLocation, hadPreviousLocation := pane.currentLocation()
	switch key {
	case KeyTop:
		pane.Cursor = 0
	case KeyBottom:
		pane.Cursor = max(len(pane.visible)-1, 0)
	default:
		return model, nil
	}
	model.Panes[model.Active] = pane
	if model.Drawer.Mode == DrawerPreview {
		currentLocation, hasCurrentLocation := pane.currentLocation()
		if hadPreviousLocation != hasCurrentLocation || currentLocation != previousLocation {
			return model, previewRefreshIntents(model)
		}
	}
	return model, nil
}
