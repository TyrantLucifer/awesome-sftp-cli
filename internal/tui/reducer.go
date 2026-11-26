package tui

import (
	"bytes"
	"fmt"
	"path"
	"sort"
	"time"

	"github.com/TyrantLucifer/awesome-sftp-cli/internal/diagnostic"
	"github.com/TyrantLucifer/awesome-sftp-cli/internal/domain"
	"github.com/TyrantLucifer/awesome-sftp-cli/internal/job"
	builtinpreview "github.com/TyrantLucifer/awesome-sftp-cli/internal/preview"
	"github.com/TyrantLucifer/awesome-sftp-cli/internal/search"
	"github.com/TyrantLucifer/awesome-sftp-cli/internal/transfer"
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
		return reduceTextInput(model, action.Text)
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
		generation := action.Generation
		if generation == 0 {
			generation = model.Clipboard.Generation
		}
		if action.Generation != 0 && action.Generation != model.Clipboard.Generation {
			return model, nil
		}
		if action.Message != "" {
			model.Clipboard.Capturing = false
			model.Clipboard.Ready = false
			if model.pendingPaste.Generation == generation {
				model.pendingPaste = pendingPasteState{}
			}
			model.Notice = action.Message
			return model, nil
		}
		references := append([]transfer.FileRef(nil), action.References...)
		if len(references) == 0 && action.Reference.Location.Path != "" {
			references = []transfer.FileRef{action.Reference}
		}
		if len(references) == 0 {
			model.Clipboard.Capturing = false
			model.Clipboard.Ready = false
			if model.pendingPaste.Generation == generation {
				model.pendingPaste = pendingPasteState{}
			}
			model.Notice = "capture returned no source"
			return model, nil
		}
		model.Clipboard = ClipboardState{Kind: action.Clipboard, Reference: references[0], References: references, Generation: generation, Ready: true}
		if len(references) == 1 {
			model.Notice = string(action.Clipboard) + " source captured: " + string(references[0].Location.Path)
		} else {
			model.Notice = string(action.Clipboard) + " sources captured: " + fmt.Sprint(len(references))
		}
		if model.pendingPaste.Active && model.pendingPaste.Generation == generation {
			pending := model.pendingPaste
			model.pendingPaste = pendingPasteState{}
			return pasteClipboard(model, pending.Pane, pending.Location, pending.Repetitions)
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
		if action.JobID == "" {
			return model, nil
		}
		jobs := append([]transfer.JobView(nil), model.Jobs...)
		tracked := false
		for index := range jobs {
			if jobs[index].Snapshot.JobID == action.JobID {
				jobs[index].Snapshot.State = action.State
				tracked = true
				break
			}
		}
		if !tracked {
			view := transfer.JobView{}
			view.Snapshot.JobID = action.JobID
			view.Snapshot.State = action.State
			jobs = append(jobs, view)
		}
		model.Jobs = jobs
		return model, []Intent{{Kind: IntentJobList}}
	case JobsLoaded:
		if action.Message != "" {
			model.Notice = action.Message
			return model, nil
		}
		jobs := append([]transfer.JobView(nil), action.Jobs...)
		intents := completedJobRefreshIntents(model, model.Jobs, jobs)
		model.jobProgress = sampleJobProgress(model.jobProgress, jobs, action.ObservedAt)
		model.Jobs = jobs
		model.JobCursor = min(model.JobCursor, max(0, len(model.Jobs)-1))
		return model, intents
	case JobUpdated:
		previous := model.Jobs
		jobs := append([]transfer.JobView(nil), previous...)
		for index := range jobs {
			if jobs[index].Snapshot.JobID == action.Snapshot.JobID {
				jobs[index].Snapshot = action.Snapshot
				break
			}
		}
		intents := completedJobRefreshIntents(model, previous, jobs)
		model.Jobs = jobs
		model.Notice = action.Message
		return model, intents
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

func completedJobRefreshIntents(model Model, previous, current []transfer.JobView) []Intent {
	previousStates := make(map[domain.JobID]job.State, len(previous))
	for _, view := range previous {
		previousStates[view.Snapshot.JobID] = view.Snapshot.State
	}
	refreshed := [2]bool{}
	intents := make([]Intent, 0, 2)
	for _, view := range current {
		if !successfulJobState(view.Snapshot.State) || successfulJobState(previousStates[view.Snapshot.JobID]) {
			continue
		}
		for _, location := range completedJobDirectories(view) {
			for pane := Left; pane <= Right; pane++ {
				if !refreshed[pane] && model.Panes[pane].Location == location {
					refreshed[pane] = true
					intents = append(intents, Intent{Kind: IntentList, Pane: pane, Location: location})
				}
			}
		}
	}
	return intents
}

func sampleJobProgress(previous map[domain.JobID]jobProgressSample, jobs []transfer.JobView, observedAt time.Time) map[domain.JobID]jobProgressSample {
	samples := make(map[domain.JobID]jobProgressSample, len(jobs))
	for _, view := range jobs {
		jobID := view.Snapshot.JobID
		if jobID == "" {
			continue
		}
		prior, hadPrior := previous[jobID]
		if observedAt.IsZero() {
			if hadPrior {
				samples[jobID] = prior
			}
			continue
		}
		if hadPrior && !observedAt.After(prior.observedAt) {
			samples[jobID] = prior
			continue
		}
		sample := appendJobProgressPoint(prior, hadPrior, view.Bytes, observedAt)
		samples[jobID] = sample
	}
	return samples
}

func appendJobProgressPoint(prior jobProgressSample, hadPrior bool, bytes uint64, observedAt time.Time) jobProgressSample {
	sample := jobProgressSample{bytes: bytes, observedAt: observedAt}
	if !hadPrior || prior.observedAt.IsZero() || bytes < prior.bytes {
		sample.points[0] = jobProgressPoint{bytes: bytes, observedAt: observedAt}
		sample.pointCount = 1
		return sample
	}

	sample.points = prior.points
	count := int(prior.pointCount)
	if count == 0 {
		sample.points[0] = jobProgressPoint{bytes: prior.bytes, observedAt: prior.observedAt}
		count = 1
	}
	if count == len(sample.points) {
		copy(sample.points[:], sample.points[1:])
		count--
	}
	sample.points[count] = jobProgressPoint{bytes: bytes, observedAt: observedAt}
	count++

	cutoff := observedAt.Add(-jobSpeedWindow)
	for count > 2 && sample.points[0].observedAt.Before(cutoff) {
		copy(sample.points[:], sample.points[1:count])
		count--
	}
	sample.pointCount = uint8(count) //nolint:gosec // count is bounded by the fixed sample array.
	first := sample.points[0]
	elapsed := observedAt.Sub(first.observedAt)
	if elapsed > 0 && bytes >= first.bytes {
		sample.bytesPerSecond = uint64(float64(bytes-first.bytes) / elapsed.Seconds())
		sample.rateKnown = true
	}
	return sample
}

func successfulJobState(state job.State) bool {
	return state == job.StateCompleted || state == job.StateCompletedWithSourceRetained
}

func completedJobDirectories(view transfer.JobView) []domain.Location {
	locations := make([]domain.Location, 0, 2)
	appendParent := func(location domain.Location) {
		parent, ok := parentLocation(location)
		if !ok {
			return
		}
		for _, existing := range locations {
			if existing == parent {
				return
			}
		}
		locations = append(locations, parent)
	}
	switch view.Kind {
	case transfer.OperationCopy:
		appendParent(view.Final)
	case transfer.OperationDelete:
		appendParent(view.Source)
	case transfer.OperationMove:
		if view.Snapshot.State == job.StateCompleted {
			appendParent(view.Source)
		}
		appendParent(view.Final)
	}
	return locations
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

func selectPaneLocation(pane *PaneState, location domain.Location) {
	for index := range pane.visible {
		if pane.visibleEntry(index).Location == location {
			pane.Cursor = index
			return
		}
	}
	pane.clampCursor()
}

func (model *Model) clearDirectoryJump() {
	model.jumpAnchor = domain.Location{}
	model.jumpFilter = ""
	model.hasJumpAnchor = false
}

func recoveryPane(model Model, location domain.Location) PaneID {
	for _, pane := range []PaneID{Left, Right} {
		if model.Panes[pane].Endpoint.ID == location.EndpointID {
			return pane
		}
	}
	return model.Active
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

func beginClipboardCapture(model Model, pane PaneID, locations []domain.Location, clipboard transfer.ClipboardKind) (Model, []Intent) {
	generation := model.Clipboard.Generation + 1
	if generation == 0 {
		generation = 1
	}
	model.Clipboard = ClipboardState{Kind: clipboard, Generation: generation, Capturing: true}
	model.pendingPaste = pendingPasteState{}
	model.Notice = fmt.Sprintf("capturing %d %s source(s)", len(locations), clipboard)
	return model, []Intent{{
		Kind: IntentTransferCapture, Pane: pane, Location: locations[0], Locations: locations,
		Clipboard: clipboard, ClipboardGeneration: generation,
	}}
}

func pasteClipboard(model Model, pane PaneID, location domain.Location, repetitions int) (Model, []Intent) {
	references := model.Clipboard.References
	if len(references) == 0 && model.Clipboard.Reference.Location.Path != "" {
		references = []transfer.FileRef{model.Clipboard.Reference}
	}
	if repetitions <= 0 {
		repetitions = 1
	}
	if len(references) == 0 || repetitions > maxBatchJobIntents/len(references) {
		model.Notice = "paste count exceeds the bounded Job batch"
		return model, nil
	}
	intents := make([]Intent, 0, len(references)*repetitions)
	for range repetitions {
		for _, reference := range references {
			intents = append(intents, Intent{
				Kind: IntentCreateCopyJob, Pane: pane, Location: location,
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
}
