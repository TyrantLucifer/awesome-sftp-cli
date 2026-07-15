package tui

import (
	"bytes"
	"unicode/utf8"

	"github.com/TyrantLucifer/awesome-mac-sftp/internal/domain"
)

func Reduce(model Model, action Action) (Model, []Intent) {
	switch action := action.(type) {
	case KeyPress:
		return reduceKey(model, action.Key)
	case TextInput:
		if model.Mode != ModeFilter || action.Text == "" {
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
		pane := model.Panes[action.Pane].clone()
		if pane.Location != action.Location {
			pane.Filter = ""
			pane.marks = make(map[domain.Location]struct{})
		}
		pane.Location = action.Location
		pane.Entries = nil
		pane.visible = nil
		pane.Cursor = 0
		pane.hasVisualAnchor = false
		pane.Listing = ListingState{Generation: action.Generation, Loading: true}
		model.Panes[action.Pane] = pane
		return model, nil
	case ListingPage:
		if !validPane(action.Pane) || model.Panes[action.Pane].Listing.Generation != action.Generation {
			return model, nil
		}
		pane := model.Panes[action.Pane].clone()
		pane.Entries = append([]domain.Entry(nil), pane.Entries...)
		pane.visible = append([]int(nil), pane.visible...)
		pane.appendEntries(action.Entries)
		pane.Listing.Partial = pane.Listing.Partial || action.Partial
		if action.Done {
			pane.Listing.Loading = false
			pane.Listing.Complete = !pane.Listing.Partial
		}
		model.Panes[action.Pane] = pane
		return model, nil
	case ListingFailed:
		if !validPane(action.Pane) || model.Panes[action.Pane].Listing.Generation != action.Generation {
			return model, nil
		}
		pane := model.Panes[action.Pane].clone()
		pane.Listing.Loading = false
		pane.Listing.Complete = false
		pane.Listing.Partial = len(pane.Entries) != 0
		pane.Listing.Message = action.Message
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
	default:
		return model, nil
	}
}

func reduceKey(model Model, key Key) (Model, []Intent) {
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
	case KeyDown:
		if pane.Cursor+1 < len(pane.visible) {
			pane.Cursor++
		}
	case KeyUp:
		if pane.Cursor > 0 {
			pane.Cursor--
		}
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
