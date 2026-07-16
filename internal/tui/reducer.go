package tui

import (
	"bytes"
	"path"
	"strings"
	"unicode/utf8"

	"github.com/TyrantLucifer/awesome-mac-sftp/internal/domain"
	"github.com/TyrantLucifer/awesome-mac-sftp/internal/transfer"
)

const navigationCountLimit = 1_000_000

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
		model.Preview = PreviewState{Generation: action.Generation, Location: action.Location, Loading: true}
		return model, nil
	case PreviewChunk:
		if model.Preview.Generation != action.Generation {
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
		preview.Binary = preview.Binary || bytes.IndexByte(action.Data, 0) >= 0
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
		model.Clipboard = ClipboardState{Kind: action.Clipboard, Reference: action.Reference, Ready: true}
		model.Notice = string(action.Clipboard) + " source captured: " + string(action.Reference.Location.Path)
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
		model.Notice = action.Message
		return model, nil
	default:
		return model, nil
	}
}

func reduceKey(model Model, key Key) (Model, []Intent) {
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
	count := model.Count
	model.Count = 0
	if count != 0 && key != KeyDown && key != KeyUp {
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
	if key == KeyJobs {
		model.ShowJobs = !model.ShowJobs
		if model.ShowJobs {
			return model, []Intent{{Kind: IntentJobList}}
		}
		return model, nil
	}
	if key == KeyTab {
		if model.Active == Left {
			model.Active = Right
		} else {
			model.Active = Left
		}
		model.Mode = ModeNormal
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

	switch key {
	case KeyCopy, KeyCut:
		entry := pane.visibleEntry(pane.Cursor)
		if entry.Kind != domain.EntryFile || entry.Location.Path == "" {
			model.Notice = "Stage 2 capture requires one regular file"
			return model, nil
		}
		clipboard := transfer.ClipboardCopy
		if key == KeyCut {
			clipboard = transfer.ClipboardCut
		}
		return model, []Intent{{Kind: IntentTransferCapture, Pane: model.Active, Location: entry.Location, Clipboard: clipboard}}
	case KeyPaste:
		if !model.Clipboard.Ready {
			model.Notice = "copy/cut clipboard is empty"
			return model, nil
		}
		return model, []Intent{{
			Kind: IntentCreateCopyJob, Pane: model.Active, Location: pane.Location,
			Clipboard: model.Clipboard.Kind, Source: model.Clipboard.Reference,
			Name: path.Base(string(model.Clipboard.Reference.Location.Path)),
		}}
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
		return model, []Intent{{
			Kind: IntentPreview, Pane: model.Active, Location: entry.Location, Limit: PreviewByteLimit,
		}}
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
	case KeyEscape:
		pane.hasVisualAnchor = false
		model.Mode = ModeNormal
	}
	model.Panes[model.Active] = pane
	return model, nil
}

func validPane(pane PaneID) bool {
	return pane == Left || pane == Right
}
