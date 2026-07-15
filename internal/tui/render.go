package tui

import (
	"fmt"
	"strings"
)

type CellStyle uint8

const (
	StylePlain CellStyle = iota
	StyleHeader
	StyleActiveHeader
	StyleCursor
	StyleSelected
	StyleStatus
	StylePreview
	StyleError
)

type Surface interface {
	Size() (width, height int)
	Clear()
	PutClipped(x, y, width int, text string, style CellStyle)
}

type RenderOptions struct {
	Overscan int
}

type RenderStats struct {
	VisitedEntries int
	ListRows       int
}

func RenderPicker(surface Surface, picker Picker, message string) {
	width, height := surface.Size()
	surface.Clear()
	if width <= 0 || height <= 0 {
		return
	}
	surface.PutClipped(0, 0, width, "Open workspace or SSH host", StyleActiveHeader)
	if height == 1 {
		return
	}
	surface.PutClipped(0, 1, width, "Host: "+SanitizeTerminalText(picker.Query()), StyleStatus)
	if height == 2 {
		return
	}
	choices := picker.Visible()
	choiceRows := max(0, height-3)
	for index := 0; index < len(choices) && index < choiceRows; index++ {
		choice := choices[index]
		marker := "  "
		style := StylePlain
		if index == picker.SelectedIndex() {
			marker = "> "
			style = StyleCursor
		}
		line := fmt.Sprintf("%s%-10s %s", marker, pickerKindLabel(choice.Kind), SanitizeTerminalText(choice.Name))
		if choice.Problem != "" {
			line += " — " + SanitizeTerminalText(choice.Problem)
			style = StyleError
		}
		surface.PutClipped(0, 2+index, width, line, style)
	}
	footer := "Type an SSH alias; ↑/↓ select; Enter open; Esc quit"
	if message != "" {
		footer = SanitizeTerminalText(message)
	}
	surface.PutClipped(0, height-1, width, footer, StyleStatus)
}

func pickerKindLabel(kind PickerKind) string {
	switch kind {
	case PickerWorkspace:
		return "workspace"
	case PickerHost:
		return "host"
	case PickerManualHost:
		return "manual"
	default:
		return "unknown"
	}
}

type Window struct {
	Start        int
	End          int
	VisibleStart int
	VisibleEnd   int
}

func ComputeWindow(total, cursor, rows, overscan int) Window {
	if total <= 0 || rows <= 0 {
		return Window{}
	}
	cursor = min(max(cursor, 0), total-1)
	rows = min(rows, total)
	visibleStart := cursor - rows/2
	visibleStart = min(max(visibleStart, 0), total-rows)
	visibleEnd := visibleStart + rows
	overscan = max(overscan, 0)
	return Window{
		Start:        max(0, visibleStart-overscan),
		End:          min(total, visibleEnd+overscan),
		VisibleStart: visibleStart,
		VisibleEnd:   visibleEnd,
	}
}

func Render(surface Surface, model Model, options RenderOptions) RenderStats {
	width, height := surface.Size()
	surface.Clear()
	if width < 3 || height < 3 {
		return RenderStats{}
	}

	previewRows := 0
	if model.Preview.Generation != 0 {
		previewRows = min(3, max(0, height-3))
	}
	listRows := max(0, height-2-previewRows)
	leftWidth := width / 2
	rightX := leftWidth + 1
	rightWidth := width - rightX

	putPaneHeader(surface, model.Panes[Left], Left, model.Active, 0, leftWidth)
	putPaneHeader(surface, model.Panes[Right], Right, model.Active, rightX, rightWidth)

	stats := RenderStats{ListRows: listRows}
	stats.VisitedEntries += renderPaneRows(surface, model.Panes[Left], 0, leftWidth, 1, listRows, options.Overscan)
	stats.VisitedEntries += renderPaneRows(surface, model.Panes[Right], rightX, rightWidth, 1, listRows, options.Overscan)

	for y := 0; y < height-previewRows-1; y++ {
		surface.PutClipped(leftWidth, y, 1, "│", StylePlain)
	}
	statusY := height - previewRows - 1
	status := "READ-ONLY"
	active := model.Panes[model.Active]
	if active.Listing.Partial {
		status += " | partial"
	} else if active.Listing.Loading {
		status += " | loading"
	}
	if active.Filter != "" {
		status += " | /" + SanitizeTerminalText(active.Filter)
	}
	status += " | " + string(model.Mode)
	if model.Notice != "" {
		status += " | " + SanitizeTerminalText(model.Notice)
	}
	surface.PutClipped(0, statusY, width, status, StyleStatus)

	if previewRows != 0 {
		previewY := statusY + 1
		surface.PutClipped(0, previewY, width, "Preview", StyleHeader)
		previewText := model.Preview.DisplayText()
		style := StylePreview
		if model.Preview.Message != "" {
			style = StyleError
		}
		lines := strings.Split(previewText, "\n")
		for row := 0; row < previewRows-1 && row < len(lines); row++ {
			surface.PutClipped(0, previewY+1+row, width, lines[row], style)
		}
	}
	if model.Auth.Active {
		renderAuthModal(surface, model.Auth, width, height)
	}
	if model.Mode == ModeWorkspace {
		renderWorkspaceModal(surface, string(model.workspaceName), width, height)
	}
	return stats
}

func renderWorkspaceModal(surface Surface, name string, width, height int) {
	modalWidth := min(width-4, 52)
	if modalWidth < 20 || height < 7 {
		return
	}
	const modalHeight = 5
	x := (width - modalWidth) / 2
	y := (height - modalHeight) / 2
	for row := 0; row < modalHeight; row++ {
		surface.PutClipped(x, y+row, modalWidth, strings.Repeat(" ", modalWidth), StyleStatus)
	}
	surface.PutClipped(x+1, y, modalWidth-2, "Save workspace", StyleStatus)
	surface.PutClipped(x+1, y+2, modalWidth-2, "Name: "+SanitizeTerminalText(name), StyleStatus)
	surface.PutClipped(x+1, y+3, modalWidth-2, "[Enter] save  [Esc] cancel", StyleStatus)
}

func renderAuthModal(surface Surface, state AuthState, width, height int) {
	modalWidth := min(width-4, 52)
	if modalWidth < 20 || height < 7 {
		return
	}
	const modalHeight = 5
	x := (width - modalWidth) / 2
	y := (height - modalHeight) / 2
	for row := 0; row < modalHeight; row++ {
		surface.PutClipped(x, y+row, modalWidth, strings.Repeat(" ", modalWidth), StyleStatus)
	}
	title := "Authentication — " + SanitizeTerminalText(state.Endpoint)
	surface.PutClipped(x+1, y, modalWidth-2, title, StyleStatus)
	surface.PutClipped(x+1, y+1, modalWidth-2, SanitizeTerminalText(state.Prompt), StyleStatus)
	if state.Kind == "confirm" {
		surface.PutClipped(x+1, y+3, modalWidth-2, "[Enter] continue  [Esc] cancel", StyleStatus)
		return
	}
	masked := strings.Repeat("•", len(state.answer))
	surface.PutClipped(x+1, y+3, modalWidth-2, "Answer: "+masked, StyleStatus)
}

func putPaneHeader(surface Surface, pane PaneState, paneID, active PaneID, x, width int) {
	name := pane.Endpoint.DisplayName
	if name == "" {
		name = "local"
	}
	header := fmt.Sprintf(" %s  %s", SanitizeTerminalText(name), SanitizeTerminalText(string(pane.Location.Path)))
	style := StyleHeader
	if paneID == active {
		style = StyleActiveHeader
		header = fmt.Sprintf("[%s] %s", SanitizeTerminalText(name), SanitizeTerminalText(string(pane.Location.Path)))
	}
	surface.PutClipped(x, 0, width, header, style)
}

func renderPaneRows(
	surface Surface,
	pane PaneState,
	x, width, y, rows, overscan int,
) int {
	window := ComputeWindow(len(pane.visible), pane.Cursor, rows, overscan)
	visited := 0
	for index := window.Start; index < window.End; index++ {
		visited++
		if index < window.VisibleStart || index >= window.VisibleEnd {
			continue
		}
		entry := pane.visibleEntry(index)
		marker := "  "
		style := StylePlain
		if pane.selectedAt(index) {
			style = StyleSelected
		}
		if index == pane.Cursor {
			marker = "> "
			style = StyleCursor
		}
		text := marker + SanitizeTerminalText(entry.Name)
		surface.PutClipped(x, y+index-window.VisibleStart, width, text, style)
	}
	return visited
}
