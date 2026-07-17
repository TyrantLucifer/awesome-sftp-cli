package tui

import (
	"bytes"
	"fmt"
	"path"
	"sort"
	"strings"
	"unicode/utf8"

	"github.com/TyrantLucifer/awesome-mac-sftp/internal/cache"
	"github.com/TyrantLucifer/awesome-mac-sftp/internal/diagnostic"
	"github.com/TyrantLucifer/awesome-mac-sftp/internal/domain"
	"github.com/TyrantLucifer/awesome-mac-sftp/internal/edit"
	builtinpreview "github.com/TyrantLucifer/awesome-mac-sftp/internal/preview"
	"github.com/TyrantLucifer/awesome-mac-sftp/internal/search"
	"github.com/TyrantLucifer/awesome-mac-sftp/internal/transfer"
)

const navigationCountLimit = 1_000_000
const maxBatchJobIntents = 1024

func Reduce(model Model, action Action) (Model, []Intent) {
	switch action := action.(type) {
	case KeyPress:
		return reduceKey(model, action.Key)
	case CountDigit:
		if action.Digit > 9 || model.Mode != ModeNormal && model.Mode != ModeVisual && model.Mode != ModeVisualLine {
			return model, nil
		}
		if model.Count == 0 && action.Digit == 0 {
			return model, nil
		}
		if model.Count > (navigationCountLimit-int(action.Digit))/10 {
			return model, nil
		}
		model.Count = model.Count*10 + int(action.Digit)
		return model, nil
	case TextInput:
		if model.Mode == ModeFilenameSearch || model.Mode == ModeContentSearch {
			if action.Text == "" || strings.ContainsAny(action.Text, "\x00\r\n") || len(string(model.searchInput))+len(action.Text) > 4096 {
				return model, nil
			}
			model.searchInput = append(append([]rune(nil), model.searchInput...), []rune(action.Text)...)
			return model, nil
		}
		if model.Mode == ModeEditSaveAs {
			if action.Text == "" || strings.ContainsAny(action.Text, "\x00\r\n") || len(string(model.editSaveAs))+len(action.Text) > 4096 {
				return model, nil
			}
			suggested := string(model.EditDecision.Location.Path) + ".copy"
			if strings.HasPrefix(action.Text, "/") && string(model.editSaveAs) == suggested {
				model.editSaveAs = nil
			}
			model.editSaveAs = append(append([]rune(nil), model.editSaveAs...), []rune(action.Text)...)
			return model, nil
		}
		if model.Auth.Active {
			answerBytes := 0
			for _, value := range model.Auth.answer {
				answerBytes += utf8.RuneLen(value)
			}
			if action.Text == "" || strings.ContainsAny(action.Text, "\x00\r\n") || answerBytes+len(action.Text) > authAnswerByteLimit {
				return model, nil
			}
			model.Auth.answer = append(append([]rune(nil), model.Auth.answer...), []rune(action.Text)...)
			return model, nil
		}
		if model.Mode == ModeWorkspace {
			if action.Text == "" || strings.ContainsAny(action.Text, "\x00\r\n") || len(string(model.workspaceName))+len(action.Text) > 64 {
				return model, nil
			}
			model.workspaceName = append(append([]rune(nil), model.workspaceName...), []rune(action.Text)...)
			return model, nil
		}
		if model.Mode == ModePath {
			if len(model.pathInput) == 0 && action.Text == "/" {
				model.Mode = ModeContentSearch
				model.searchInput = nil
				model.Notice = "slow SFTP content search; bounded remote reads may be expensive"
				return model, nil
			}
			if len(model.pathInput) == 0 && action.Text == "p" {
				model.Mode = ModeNormal
				switch model.CachePolicy {
				case cache.PolicyLRU:
					model.CachePolicy = cache.PolicyEphemeral
				case cache.PolicyEphemeral:
					model.CachePolicy = cache.PolicyPinnedOffline
				default:
					model.CachePolicy = cache.PolicyLRU
				}
				model.Notice = "workspace cache policy: " + string(model.CachePolicy)
				return model, []Intent{{Kind: IntentCachePolicy, CachePolicy: model.CachePolicy}}
			}
			if len(model.pathInput) == 0 && (action.Text == "c" || action.Text == "C") {
				model.Mode = ModeCacheClearConfirm
				model.CacheClearScope = CacheClearWorkspace
				if action.Text == "C" {
					model.CacheClearScope = CacheClearAll
				}
				model.Notice = "cache clear only removes currently eligible managed content"
				return model, nil
			}
			if len(model.pathInput) == 0 && (action.Text == "s" || action.Text == "S") {
				pane := model.Panes[model.Active]
				model.Mode = ModeNormal
				return model, []Intent{{Kind: IntentShell, Pane: model.Active, Location: pane.Location, Endpoint: pane.Endpoint, ShellHome: action.Text == "S"}}
			}
			if action.Text == "" || strings.ContainsAny(action.Text, "\x00\r\n") || len(string(model.pathInput))+len(action.Text) > 4096 {
				return model, nil
			}
			model.pathInput = append(append([]rune(nil), model.pathInput...), []rune(action.Text)...)
			return model, nil
		}
		if model.Mode == ModeEndpoint {
			if action.Text == "" || strings.ContainsAny(action.Text, "\x00\r\n") || len(string(model.endpointInput))+len(action.Text) > 255 {
				return model, nil
			}
			model.endpointInput = append(append([]rune(nil), model.endpointInput...), []rune(action.Text)...)
			return model, nil
		}
		if model.Mode == ModeRename {
			if action.Text == "" || strings.ContainsAny(action.Text, "\x00/\r\n") || len(string(model.renameInput))+len(action.Text) > 255 {
				return model, nil
			}
			model.renameInput = append(append([]rune(nil), model.renameInput...), []rune(action.Text)...)
			return model, nil
		}
		if model.Mode == ModeCommand {
			if action.Text == "" || strings.ContainsAny(action.Text, "\x00\r\n") || len(string(model.commandInput))+len(action.Text) > CommandByteLimit {
				model.Notice = "command must be one UTF-8 line of at most 32768 bytes"
				return model, nil
			}
			model.commandInput = append(append([]rune(nil), model.commandInput...), []rune(action.Text)...)
			return model, nil
		}
		if model.Mode != ModeFilter || action.Text == "" {
			model.Count = 0
			return model, nil
		}
		pane := model.Panes[model.Active].clone()
		pane.Filter += action.Text
		pane.visible = nil
		pane.rebuildVisible()
		model.Panes[model.Active] = pane
		return model, nil
	case Resize:
		model.Width, model.Height = action.Width, action.Height
		return model, nil
	case BeginListing:
		if !validPane(action.Pane) || action.Generation == 0 {
			return model, nil
		}
		if action.CommitEndpoint && (action.Endpoint.ID == "" || action.Location.EndpointID != action.Endpoint.ID) {
			return model, nil
		}
		pane := model.Panes[action.Pane].clone()
		if !action.CommitEndpoint && action.Location.EndpointID != pane.Endpoint.ID {
			return model, nil
		}
		anchor, hasAnchor := pane.currentLocation()
		pane.Listing = ListingState{
			Generation:                  action.Generation,
			Loading:                     true,
			pendingLocation:             action.Location,
			pendingEndpoint:             action.Endpoint,
			pendingConnection:           action.Connection,
			pendingCapabilityGeneration: action.CapabilityGeneration,
			pendingCapabilities:         action.Capabilities,
			commitEndpoint:              action.CommitEndpoint,
			cursorAnchor:                anchor,
			hasCursorAnchor:             hasAnchor,
		}
		model.Panes[action.Pane] = pane
		return model, nil
	case ListingPage:
		if !validPane(action.Pane) || model.Panes[action.Pane].Listing.Generation != action.Generation {
			return model, nil
		}
		pane := model.Panes[action.Pane].clone()
		var intents []Intent
		if !pane.Listing.hasPage {
			target := pane.Listing.pendingLocation
			if target.EndpointID == "" {
				return model, nil
			}
			if pane.Listing.commitEndpoint {
				oldEndpoint := pane.Endpoint
				pane.Endpoint = pane.Listing.pendingEndpoint
				pane.Connection = pane.Listing.pendingConnection
				if pane.Connection == "" {
					pane.Connection = domain.StateReady
				}
				pane.CapabilityGeneration = pane.Listing.pendingCapabilityGeneration
				pane.Capabilities = pane.Listing.pendingCapabilities
				if oldEndpoint.Kind == domain.EndpointSSH && oldEndpoint.ID != pane.Endpoint.ID {
					intents = append(intents, Intent{Kind: IntentReleaseEndpoint, Pane: action.Pane, EndpointID: oldEndpoint.ID})
				}
			}
			if target != pane.Location {
				pane.Filter = ""
				pane.marks = make(map[domain.Location]struct{})
				pane.hasVisualAnchor = false
				pane.Listing.hasCursorAnchor = false
			}
			pane.Location = target
			pane.Entries = nil
			pane.visible = nil
			pane.Cursor = 0
			pane.Listing.hasPage = true
			pane.Listing.pendingEndpoint = domain.Endpoint{}
			pane.Listing.pendingConnection = ""
			pane.Listing.pendingCapabilityGeneration = 0
			pane.Listing.pendingCapabilities = domain.CapabilitySnapshot{}
			pane.Listing.commitEndpoint = false
		} else {
			pane.Entries = append([]domain.Entry(nil), pane.Entries...)
			pane.visible = append([]int(nil), pane.visible...)
		}
		pane.appendEntries(action.Entries)
		pane.rebindCursorAnchor()
		pane.Listing.Partial = pane.Listing.Partial || action.Partial
		if action.Done {
			pane.Listing.Loading = false
			pane.Listing.Complete = !pane.Listing.Partial
			if pane.Listing.Complete {
				pane.Connection = domain.StateReady
			}
			pane.Listing.pendingLocation = domain.Location{}
			pane.pruneMarks()
			pane.rebindVisualAnchor()
			pane.rebindCursorAnchor()
		}
		model.Panes[action.Pane] = pane
		return model, intents
	case ListingFailed:
		if !validPane(action.Pane) || model.Panes[action.Pane].Listing.Generation != action.Generation {
			return model, nil
		}
		pane := model.Panes[action.Pane].clone()
		pane.Listing.Loading = false
		pane.Listing.Complete = false
		pane.Listing.Partial = pane.Listing.hasPage && len(pane.Entries) != 0
		pane.Listing.Message = action.Message
		pane.Listing.pendingLocation = domain.Location{}
		pane.Listing.pendingEndpoint = domain.Endpoint{}
		pane.Listing.pendingConnection = ""
		pane.Listing.pendingCapabilityGeneration = 0
		pane.Listing.pendingCapabilities = domain.CapabilitySnapshot{}
		pane.Listing.commitEndpoint = false
		if pane.Listing.Partial {
			pane.Connection = domain.StateDegraded
		}
		model.Panes[action.Pane] = pane
		return model, nil
	case SetFilter:
		if !validPane(action.Pane) {
			return model, nil
		}
		pane := model.Panes[action.Pane].clone()
		current, hasCurrent := pane.currentLocation()
		pane.Filter = action.Query
		pane.visible = nil
		pane.rebuildVisible()
		if hasCurrent {
			for index := range pane.visible {
				if pane.visibleEntry(index).Location == current {
					pane.Cursor = index
					break
				}
			}
		}
		model.Panes[action.Pane] = pane
		return model, nil
	case BeginPreview:
		if action.Generation == 0 {
			return model, nil
		}
		if action.Identity.RequestID != "" && (action.Identity.UIGeneration != action.Generation || action.Identity.Source.Location != action.Location || !validPane(action.Identity.Pane)) {
			return model, nil
		}
		model.Preview = PreviewState{Generation: action.Generation, Identity: action.Identity, Location: action.Location, Loading: true, View: action.View}
		return model, nil
	case BeginSearch:
		if action.Identity.RequestID == "" || action.Identity.Scope.EndpointID != action.Identity.EndpointID {
			return model, nil
		}
		model.Search = SearchState{Identity: action.Identity, Query: action.Identity.Options.Pattern, Loading: true}
		model.Drawer.Mode = DrawerSearch
		model.Drawer.Focus = FocusDrawer
		return model, nil
	case SearchEvents:
		state := model.Search
		for _, event := range action.Events {
			if !search.EventCurrent(state.Identity, event) {
				continue
			}
			switch event.Kind {
			case search.EventResult:
				if len(state.Results) < SearchResultLimit {
					state.Results = append(append([]search.Result(nil), state.Results...), event.Result)
				}
			case search.EventProblem:
				if len(state.Problems) < 64 {
					state.Problems = append(append([]search.Problem(nil), state.Problems...), event.Problem)
				}
			case search.EventTerminal:
				state.Terminal = event.Terminal
				state.Loading = false
			}
		}
		state.Cursor = min(state.Cursor, max(0, len(state.Results)-1))
		model.Search = state
		return model, nil
	case SearchFailed:
		if action.Identity != model.Search.Identity {
			return model, nil
		}
		model.Search.Loading = false
		model.Search.Message = action.Message
		return model, nil
	case BeginContentSearch:
		if action.Identity.RequestID == "" || action.Identity.Scope.EndpointID != action.Identity.EndpointID {
			return model, nil
		}
		model.ContentSearch = ContentSearchState{Identity: action.Identity, Query: action.Identity.Options.Pattern, Loading: true}
		model.Drawer.Mode = DrawerContentSearch
		model.Drawer.Focus = FocusDrawer
		return model, nil
	case ContentSearchEvents:
		state := model.ContentSearch
		for _, event := range action.Events {
			if !search.ContentEventCurrent(state.Identity, event) {
				continue
			}
			switch event.Kind {
			case search.ContentEventResult:
				if len(state.Results) < SearchResultLimit {
					state.Results = append(append([]search.ContentResult(nil), state.Results...), event.Result)
				}
			case search.ContentEventProblem:
				if len(state.Problems) < 64 {
					state.Problems = append(append([]search.ContentProblem(nil), state.Problems...), event.Problem)
				}
			case search.ContentEventTerminal:
				state.Terminal = event.Terminal
				state.Loading = false
			}
		}
		state.Cursor = min(state.Cursor, max(0, len(state.Results)-1))
		model.ContentSearch = state
		return model, nil
	case ContentSearchFailed:
		if action.Identity != model.ContentSearch.Identity {
			return model, nil
		}
		model.ContentSearch.Loading = false
		model.ContentSearch.Message = action.Message
		return model, nil
	case PreviewChunk:
		if model.Preview.Generation != action.Generation || !previewIdentityMatches(model.Preview.Identity, action.Identity) {
			return model, nil
		}
		preview := model.Preview
		preview.Data = append([]byte(nil), preview.Data...)
		remaining := PreviewByteLimit - len(preview.Data)
		if remaining > 0 {
			appendCount := min(remaining, len(action.Data))
			preview.Data = append(preview.Data, action.Data[:appendCount]...)
		}
		preview.BytesRead = len(preview.Data)
		preview.Truncated = preview.Truncated || action.Truncated || len(action.Data) > remaining
		preview.Binary = preview.Binary || !action.Rendered && bytes.IndexByte(action.Data, 0) >= 0
		if action.Kind != "" {
			preview.Kind = action.Kind
		}
		preview.Summary = action.Summary
		preview.Message = action.Message
		if action.Done || preview.Truncated || action.Message != "" {
			preview.Loading = false
		}
		model.Preview = preview
		return model, nil
	case AuthChallengeReceived:
		if model.Auth.Active || action.ChallengeID == "" || action.Prompt == "" {
			return model, nil
		}
		returnMode := model.Mode
		if returnMode == ModeAuth {
			returnMode = ModeNormal
		}
		model.Mode = ModeAuth
		model.Auth = AuthState{
			Active:      true,
			ChallengeID: action.ChallengeID,
			Endpoint:    action.Endpoint,
			Prompt:      action.Prompt,
			Kind:        action.Kind,
			ReturnMode:  returnMode,
		}
		return model, nil
	case PaneConnected:
		if !validPane(action.Pane) || action.Endpoint.ID == "" || action.Location.EndpointID != action.Endpoint.ID {
			return model, nil
		}
		pane := NewPaneState(action.Endpoint, action.Location)
		if action.PreserveCommitted {
			pane = model.Panes[action.Pane].clone()
			pane.Listing.Message = ""
			model.Panes[action.Pane] = pane
			return model, []Intent{{
				Kind:                 IntentList,
				Pane:                 action.Pane,
				Location:             action.Location,
				Endpoint:             action.Endpoint,
				Connection:           action.State,
				CapabilityGeneration: action.CapabilityGeneration,
				Capabilities:         action.Capabilities,
				CommitEndpoint:       true,
			}}
		}
		pane.Connection = action.State
		if pane.Connection == "" {
			pane.Connection = domain.StateReady
		}
		pane.CapabilityGeneration = action.CapabilityGeneration
		pane.Capabilities = action.Capabilities
		model.Panes[action.Pane] = pane
		return model, []Intent{{Kind: IntentList, Pane: action.Pane, Location: action.Location}}
	case PaneConnectionChanged:
		if !validPane(action.Pane) || action.State == "" {
			return model, nil
		}
		pane := model.Panes[action.Pane].clone()
		pane.Connection = action.State
		pane.Listing.Message = action.Message
		if action.State != domain.StateReady {
			pane.Listing.Loading = false
		}
		model.Panes[action.Pane] = pane
		return model, nil
	case PaneCapabilitiesRefreshed:
		if !validPane(action.Pane) || action.EndpointID == "" {
			return model, nil
		}
		pane := model.Panes[action.Pane].clone()
		if pane.Endpoint.ID != action.EndpointID || pane.Capabilities.Revision.SessionID == "" ||
			action.Capabilities.Revision != pane.Capabilities.Revision {
			return model, nil
		}
		pane.Capabilities = action.Capabilities
		pane.CapabilityGeneration = action.Capabilities.Revision.Generation
		model.Panes[action.Pane] = pane
		return model, nil
	case WorkspaceSaveResult:
		if action.Message != "" {
			model.Notice = action.Message
		} else {
			model.Notice = "workspace saved: " + action.Name
		}
		return model, nil
	case ClipboardCaptured:
		if action.Message != "" {
			model.Notice = action.Message
			return model, nil
		}
		references := append([]transfer.FileRef(nil), action.References...)
		if len(references) == 0 && action.Reference.Location.Path != "" {
			references = []transfer.FileRef{action.Reference}
		}
		if len(references) == 0 {
			model.Notice = "capture returned no source"
			return model, nil
		}
		model.Clipboard = ClipboardState{Kind: action.Clipboard, Reference: references[0], References: references, Ready: true}
		if len(references) == 1 {
			model.Notice = string(action.Clipboard) + " source captured: " + string(references[0].Location.Path)
		} else {
			model.Notice = string(action.Clipboard) + " sources captured: " + fmt.Sprint(len(references))
		}
		return model, nil
	case DeletePrepared:
		if action.Message != "" {
			model.Notice = action.Message
			return model, nil
		}
		if len(action.References) == 0 {
			model.Notice = "delete preparation returned no target"
			return model, nil
		}
		model.pendingDelete = append([]transfer.FileRef(nil), action.References...)
		model.DeleteConfirmation = 1
		model.Mode = ModeDeleteConfirm
		return model, nil
	case RenamePrepared:
		if action.Message != "" {
			model.Notice = action.Message
			return model, nil
		}
		if action.Reference.Location.Path == "" {
			model.Notice = "rename preparation returned no source"
			return model, nil
		}
		model.pendingRename = action.Reference
		model.renameInput = nil
		model.Mode = ModeRename
		return model, nil
	case JobCreated:
		if action.Message != "" {
			model.Notice = action.Message
		} else {
			model.Notice = "Job queued: " + string(action.JobID) + " (" + string(action.State) + ")"
		}
		return model, nil
	case JobsLoaded:
		model.Jobs = append([]transfer.JobView(nil), action.Jobs...)
		model.JobCursor = min(model.JobCursor, max(0, len(model.Jobs)-1))
		model.Notice = action.Message
		return model, nil
	case JobUpdated:
		for index := range model.Jobs {
			if model.Jobs[index].Snapshot.JobID == action.Snapshot.JobID {
				model.Jobs = append([]transfer.JobView(nil), model.Jobs...)
				model.Jobs[index].Snapshot = action.Snapshot
				break
			}
		}
		model.Notice = action.Message
		return model, nil
	case DiagnosticsLoaded:
		model.Diagnostics = append([]diagnostic.Record(nil), action.Records...)
		model.Notice = action.Message
		return model, nil
	case CommandCompleted:
		model.CommandRunning = false
		if !validPane(action.Pane) || action.Location.Path == "" {
			return model, nil
		}
		if action.Message != "" {
			model.Notice = action.Message
		} else {
			model.Notice = fmt.Sprintf("command exit %d", action.ExitCode)
			output := action.Stdout
			if len(output) == 0 {
				output = action.Stderr
			}
			if len(output) != 0 {
				line, _, _ := bytes.Cut(output, []byte{'\n'})
				model.Notice += ": " + truncateCommandNotice(SanitizeTerminalText(string(line)), 160)
			}
		}
		if action.EffectUnknown {
			model.Notice += "; remote effect unknown"
		}
		if discarded := action.StdoutDiscarded + action.StderrDiscarded; discarded != 0 {
			model.Notice += fmt.Sprintf("; %d bytes discarded", discarded)
		}
		return model, []Intent{{Kind: IntentList, Pane: action.Pane, Location: action.Location}}
	case EditSessionObserved:
		if action.SessionID == "" || !validPane(action.Pane) {
			return model, nil
		}
		model.EditDecision = EditDecisionState{
			Active: true, SessionID: action.SessionID, Pane: action.Pane,
			Location: action.Location, State: action.State, Message: action.Message, Decision: action.Decision,
			ConflictView: action.ConflictView,
		}
		model.Mode = ModeEditDecision
		model.Notice = action.Message
		return model, []Intent{{Kind: IntentList, Pane: action.Pane, Location: action.Location}}
	case EditLaunchReady:
		if action.SessionID == "" || !validPane(action.Pane) || action.Command == "" {
			return model, nil
		}
		model.EditLaunch = EditLaunchState{Active: true, SessionID: action.SessionID, Pane: action.Pane, Location: action.Location, Command: action.Command}
		model.Mode = ModeEditLaunchConfirm
		model.Notice = "external command resolved; confirm direct execution"
		if action.Message != "" {
			model.Notice += "; " + action.Message
		}
		return model, nil
	case EditSessionFinished:
		model.Notice = action.Message
		return model, []Intent{{Kind: IntentList, Pane: action.Pane, Location: action.Location}}
	case EditSessionFailed:
		model.Notice = action.Message
		return model, nil
	case EditRecoveryLoaded:
		if action.Count < 0 || len(action.Sessions) > MaxRecoverableEditSessions {
			return model, nil
		}
		items := append([]EditRecoveryItem(nil), action.Sessions...)
		sort.SliceStable(items, func(left, right int) bool {
			if !items[left].UpdatedAt.Equal(items[right].UpdatedAt) {
				return items[left].UpdatedAt.After(items[right].UpdatedAt)
			}
			return items[left].SessionID < items[right].SessionID
		})
		model.EditRecovery = EditRecoveryState{Items: items}
		model.RecoverableEdits = action.Count
		if len(items) != 0 {
			model.RecoverableEdits = len(items)
			model.Mode = ModeEditRecovery
		}
		if action.Message != "" {
			model.Notice = action.Message
		} else if action.Count > 0 {
			model.Notice = fmt.Sprintf("%d recoverable edit session(s) retained", action.Count)
		}
		return model, nil
	case CacheCleared:
		model.Notice = action.Message
		if model.Notice == "" {
			model.Notice = fmt.Sprintf("cache cleared %d eligible item(s); %d protected; %d bytes remain", action.Deleted, action.Protected, action.RemainingBytes)
		}
		return model, nil
	default:
		return model, nil
	}
}

func truncateCommandNotice(value string, limit int) string {
	runes := []rune(value)
	if len(runes) <= limit {
		return value
	}
	return string(runes[:limit]) + "…"
}

func previewIdentityMatches(expected, actual PreviewRequestIdentity) bool {
	if expected.RequestID == "" && actual.RequestID == "" {
		return true
	}
	return expected == actual
}

func reduceKey(model Model, key Key) (Model, []Intent) {
	if model.Mode == ModeContentSearchConfirm {
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
	if model.Mode == ModeFilenameSearch || model.Mode == ModeContentSearch {
		content := model.Mode == ModeContentSearch
		switch key {
		case KeyBackspace:
			if len(model.searchInput) != 0 {
				model.searchInput = append([]rune(nil), model.searchInput[:len(model.searchInput)-1]...)
			}
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
	if model.Mode == ModeNormal && model.CommandRunning && key == KeyEscape {
		model.Notice = "canceling active one-time command"
		return model, []Intent{{Kind: IntentCommandCancel}}
	}
	if model.Mode == ModeCacheClearConfirm {
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
	if model.Mode == ModeEditRecovery {
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
	if model.Mode == ModeEditLaunchConfirm {
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
		switch key {
		case KeyBackspace:
			if len(model.editSaveAs) != 0 {
				model.editSaveAs = append([]rune(nil), model.editSaveAs[:len(model.editSaveAs)-1]...)
			}
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
	if model.Mode == ModeEditDecision {
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
	if model.Mode == ModeCommand {
		switch key {
		case KeyBackspace:
			if len(model.commandInput) != 0 {
				model.commandInput = append([]rune(nil), model.commandInput[:len(model.commandInput)-1]...)
			}
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
	if model.Mode == ModeCommandConfirm {
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
	if model.Auth.Active {
		switch key {
		case KeyBackspace:
			if len(model.Auth.answer) != 0 {
				model.Auth.answer = append([]rune(nil), model.Auth.answer[:len(model.Auth.answer)-1]...)
			}
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
	if model.Mode == ModeDeleteConfirm {
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
	if model.Mode == ModeMoveConfirm {
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
	if model.Mode == ModeRename {
		switch key {
		case KeyBackspace:
			if len(model.renameInput) != 0 {
				model.renameInput = append([]rune(nil), model.renameInput[:len(model.renameInput)-1]...)
			}
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
	if model.Mode == ModeWorkspace {
		switch key {
		case KeyBackspace:
			if len(model.workspaceName) != 0 {
				model.workspaceName = append([]rune(nil), model.workspaceName[:len(model.workspaceName)-1]...)
			}
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
	if model.Mode == ModePath {
		switch key {
		case KeyBackspace:
			if len(model.pathInput) != 0 {
				model.pathInput = append([]rune(nil), model.pathInput[:len(model.pathInput)-1]...)
			}
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
	if model.Mode == ModeEndpoint {
		switch key {
		case KeyBackspace:
			if len(model.endpointInput) != 0 {
				model.endpointInput = append([]rune(nil), model.endpointInput[:len(model.endpointInput)-1]...)
			}
			return model, nil
		case KeyEscape:
			model.endpointInput = nil
			model.Mode = ModeNormal
			return model, nil
		case KeySubmit:
			name := string(model.endpointInput)
			if name == "" {
				model.Notice = "endpoint name is required"
				return model, nil
			}
			model.endpointInput = nil
			model.Mode = ModeNormal
			return model, []Intent{{Kind: IntentConnectEndpoint, Pane: model.Active, Name: name}}
		default:
			return model, nil
		}
	}
	if drawerMode, ok := drawerModeForKey(key); ok {
		return reduceDrawerToggle(model, drawerMode)
	}
	if model.Drawer.Focus == FocusDrawer {
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
		if model.Drawer.Mode == DrawerSearch {
			return reduceSearchKey(model, key)
		}
		if model.Drawer.Mode == DrawerContentSearch {
			return reduceContentSearchKey(model, key)
		}
		if model.Drawer.Mode == DrawerJobs {
			return reduceJobsKey(model, key)
		}
		if model.Drawer.Mode == DrawerPreview {
			return reducePreviewKey(model, key)
		}
		return model, nil
	}
	count := model.Count
	model.Count = 0
	if count != 0 && key != KeyDown && key != KeyUp && key != KeyCopy && key != KeyCut && key != KeyPaste && key != KeyDelete && key != KeyRename {
		return model, nil
	}
	steps := 1
	if count != 0 {
		steps = count
	}
	if key == KeyEscape && model.Preview.Generation != 0 {
		model.Preview = PreviewState{}
		return model, []Intent{{Kind: IntentPreviewCancel}}
	}
	if key == KeyTab {
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
	pane := model.Panes[model.Active].clone()

	if model.Mode == ModeFilter {
		switch key {
		case KeyEscape:
			pane.Filter = ""
			pane.visible = nil
			pane.rebuildVisible()
			model.Mode = ModeNormal
		case KeyBackspace:
			if pane.Filter != "" {
				_, size := utf8.DecodeLastRuneInString(pane.Filter)
				pane.Filter = pane.Filter[:len(pane.Filter)-size]
				pane.visible = nil
				pane.rebuildVisible()
			}
		}
		model.Panes[model.Active] = pane
		return model, nil
	}

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
		return model, []Intent{{Kind: IntentTransferCapture, Pane: model.Active, Location: locations[0], Locations: locations, Clipboard: clipboard}}
	case KeyPaste:
		if !model.Clipboard.Ready {
			model.Notice = "copy/cut clipboard is empty"
			return model, nil
		}
		references := model.Clipboard.References
		if len(references) == 0 {
			references = []transfer.FileRef{model.Clipboard.Reference}
		}
		repetitions := 1
		if count > 0 {
			repetitions = count
		}
		if len(references) == 0 || repetitions > maxBatchJobIntents/len(references) {
			model.Notice = "paste count exceeds the bounded Job batch"
			return model, nil
		}
		intents := make([]Intent, 0, len(references)*repetitions)
		for range repetitions {
			for _, reference := range references {
				intents = append(intents, Intent{
					Kind: IntentCreateCopyJob, Pane: model.Active, Location: pane.Location,
					Clipboard: model.Clipboard.Kind, Source: reference,
					Name: path.Base(string(reference.Location.Path)),
				})
			}
		}
		if model.Clipboard.Kind == transfer.ClipboardCut {
			model.pendingMove = append([]Intent(nil), intents...)
			model.Mode = ModeMoveConfirm
			return model, nil
		}
		model.repeatIntents = append([]Intent(nil), intents...)
		model.repeatMove = nil
		model.repeatDelete = nil
		return model, intents
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
		switch pane.Sort.Key {
		case SortName:
			pane.Sort.Key = SortSize
		case SortSize:
			pane.Sort.Key = SortModified
		case SortModified:
			pane.Sort.Key = SortKind
		default:
			pane.Sort.Key = SortName
			pane.Sort.Descending = !pane.Sort.Descending
		}
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
		model.endpointInput = nil
	case KeyDown:
		if len(pane.visible) != 0 {
			pane.Cursor = min(pane.Cursor+steps, len(pane.visible)-1)
		}
	case KeyUp:
		pane.Cursor = max(pane.Cursor-steps, 0)
	case KeyParent:
		location, ok := parentLocation(pane.Location)
		if ok {
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
		model.Mode = ModeFilter
	case KeyFilenameSearch:
		model.Mode = ModeFilenameSearch
		model.searchInput = nil
	case KeyEscape:
		pane.hasVisualAnchor = false
		model.Mode = ModeNormal
	}
	model.Panes[model.Active] = pane
	if model.Drawer.Mode == DrawerPreview && (key == KeyDown || key == KeyUp) {
		currentLocation, hasCurrentLocation := pane.currentLocation()
		if hadPreviousLocation != hasCurrentLocation || currentLocation != previousLocation {
			return model, previewRefreshIntents(model)
		}
	}
	return model, nil
}

func recoveryPane(model Model, location domain.Location) PaneID {
	for _, pane := range []PaneID{Left, Right} {
		if model.Panes[pane].Endpoint.ID == location.EndpointID {
			return pane
		}
	}
	return model.Active
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
		return model, []Intent{{
			Kind: IntentPreview, Pane: pane, Location: result.Location, Limit: PreviewByteLimit,
			EndpointSession: model.Search.Identity.SessionID, EndpointGeneration: model.Search.Identity.EndpointGeneration,
			PreviewMode: builtinpreview.ReadHead, PreviewView: builtinpreview.ViewAuto,
		}}
	case KeyCopy:
		return model, []Intent{{Kind: IntentTransferCapture, Pane: pane, Location: result.Location, Locations: []domain.Location{result.Location}, Clipboard: transfer.ClipboardCopy}}
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
		return model, []Intent{{
			Kind: IntentPreview, Pane: pane, Location: result.Location, Limit: PreviewByteLimit,
			EndpointSession: model.ContentSearch.Identity.SessionID, EndpointGeneration: model.ContentSearch.Identity.EndpointGeneration,
			PreviewMode: builtinpreview.ReadHead, PreviewView: builtinpreview.ViewAuto,
		}}
	case KeyCopy:
		return model, []Intent{{Kind: IntentTransferCapture, Pane: pane, Location: result.Location, Locations: []domain.Location{result.Location}, Clipboard: transfer.ClipboardCopy}}
	default:
		return model, nil
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
	step := model.Preview.Identity.RequestedLimit
	if step == 0 {
		step = builtinpreview.ReadChunkBytes
	}
	switch key {
	case KeyParent:
		intent.PreviewMode = builtinpreview.ReadHead
		intent.PreviewOffset = 0
	case KeyOpen:
		intent.PreviewMode = builtinpreview.ReadTail
		intent.PreviewOffset = 0
	case KeyDown:
		intent.PreviewMode = builtinpreview.ReadRange
		if intent.PreviewOffset <= ^uint64(0)-step {
			intent.PreviewOffset += step
		}
	case KeyUp:
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

func drawerModeForKey(key Key) (DrawerMode, bool) {
	switch key {
	case KeyPreviewDrawer:
		return DrawerPreview, true
	case KeyJobs:
		return DrawerJobs, true
	case KeyLogDrawer:
		return DrawerLog, true
	default:
		return DrawerClosed, false
	}
}

func reduceDrawerToggle(model Model, mode DrawerMode) (Model, []Intent) {
	model.Count = 0
	wasPreview := model.Drawer.Mode == DrawerPreview
	if model.Drawer.Mode == mode {
		if model.Drawer.Focus == FocusDrawer {
			model.Drawer.Mode = DrawerClosed
			model.Drawer.Focus = FocusPane
			if wasPreview {
				model.Preview = PreviewState{}
				return model, []Intent{{Kind: IntentPreviewCancel}}
			}
			return model, nil
		}
		model.Drawer.Focus = FocusDrawer
		return model, drawerOpenIntents(model, mode, false)
	}
	model.Drawer.Mode = mode
	model.Drawer.Focus = FocusDrawer
	intents := make([]Intent, 0, 2)
	if wasPreview {
		model.Preview = PreviewState{}
		intents = append(intents, Intent{Kind: IntentPreviewCancel})
	}
	intents = append(intents, drawerOpenIntents(model, mode, true)...)
	return model, intents
}

func drawerOpenIntents(model Model, mode DrawerMode, switching bool) []Intent {
	switch mode {
	case DrawerPreview:
		return previewOpenIntents(model, switching)
	case DrawerJobs:
		return []Intent{{Kind: IntentJobList}}
	case DrawerLog:
		return []Intent{{Kind: IntentDiagnosticList, Limit: 256}}
	default:
		return nil
	}
}

func previewOpenIntents(model Model, switching bool) []Intent {
	pane := model.Panes[model.Active]
	entry := pane.visibleEntry(pane.Cursor)
	if entry.Kind != domain.EntryFile || entry.Location.Path == "" {
		return nil
	}
	intents := make([]Intent, 0, 2)
	if !switching && model.Preview.Generation != 0 {
		intents = append(intents, Intent{Kind: IntentPreviewCancel})
	}
	return append(intents, previewIntent(model.Active, pane, entry))
}

func previewRefreshIntents(model Model) []Intent {
	intents := []Intent{{Kind: IntentPreviewCancel}}
	pane := model.Panes[model.Active]
	entry := pane.visibleEntry(pane.Cursor)
	if entry.Kind == domain.EntryFile && entry.Location.Path != "" {
		intents = append(intents, previewIntent(model.Active, pane, entry))
	}
	return intents
}

func previewIntent(paneID PaneID, pane PaneState, entry domain.Entry) Intent {
	return Intent{
		Kind: IntentPreview, Pane: paneID, Location: entry.Location, Limit: PreviewByteLimit,
		EndpointSession: pane.Capabilities.Revision.SessionID, EndpointGeneration: pane.Capabilities.Revision.Generation,
		PreviewMode: builtinpreview.ReadHead, PreviewView: builtinpreview.ViewAuto,
	}
}

func validPane(pane PaneID) bool {
	return pane == Left || pane == Right
}

func selectedLocations(pane PaneState, count int) []domain.Location {
	if selected := pane.SelectedLocations(); len(selected) != 0 {
		return selected
	}
	if len(pane.visible) == 0 || pane.Cursor < 0 || pane.Cursor >= len(pane.visible) {
		return nil
	}
	length := 1
	if count > 0 {
		length = count
	}
	end := min(pane.Cursor+length, len(pane.visible))
	locations := make([]domain.Location, 0, end-pane.Cursor)
	for index := pane.Cursor; index < end; index++ {
		entry := pane.visibleEntry(index)
		if entry.Location.Path != "" {
			locations = append(locations, entry.Location)
		}
	}
	return locations
}
