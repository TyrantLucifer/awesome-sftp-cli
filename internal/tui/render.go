package tui

import (
	"fmt"
	"strings"

	"github.com/TyrantLucifer/awesome-mac-sftp/internal/diagnostic"
	"github.com/TyrantLucifer/awesome-mac-sftp/internal/domain"
	"github.com/TyrantLucifer/awesome-mac-sftp/internal/transfer"
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
	if width < 20 || height < 5 {
		if width > 0 && height > 0 {
			surface.PutClipped(0, 0, width, "resize terminal", StyleStatus)
		}
		return RenderStats{}
	}

	drawerRows := drawerRows(model.Drawer, height)
	listRows := max(0, height-2-drawerRows)
	leftWidth := width / 2
	rightX := leftWidth + 1
	rightWidth := width - rightX

	putPaneHeader(surface, model.Panes[Left], Left, model.Active, 0, leftWidth)
	putPaneHeader(surface, model.Panes[Right], Right, model.Active, rightX, rightWidth)

	stats := RenderStats{ListRows: listRows}
	stats.VisitedEntries += renderPaneRows(surface, model.Panes[Left], 0, leftWidth, 1, listRows, options.Overscan)
	stats.VisitedEntries += renderPaneRows(surface, model.Panes[Right], rightX, rightWidth, 1, listRows, options.Overscan)

	for y := 0; y < height-drawerRows-1; y++ {
		surface.PutClipped(leftWidth, y, 1, "│", StylePlain)
	}
	statusY := height - drawerRows - 1
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
	if active.Listing.Message != "" {
		status += " | " + SanitizeTerminalText(active.Listing.Message)
	}
	if active.CapabilityGeneration != 0 {
		status += fmt.Sprintf(" | caps:%d@%d", len(active.Capabilities.Items), active.CapabilityGeneration)
	}
	direction := "↑"
	if active.Sort.Descending {
		direction = "↓"
	}
	status += " | sort:" + string(active.Sort.Key) + direction
	if active.ShowHidden {
		status += " | hidden:on"
	} else {
		status += " | hidden:off"
	}
	if model.Count != 0 {
		status += fmt.Sprintf(" | %d", model.Count)
	}
	if model.RecoverableEdits != 0 {
		status += fmt.Sprintf(" | edits:recoverable(%d)", model.RecoverableEdits)
	}
	status += " | cache:" + string(model.CachePolicy)
	status += " | " + string(model.Mode)
	if model.Notice != "" {
		status += " | " + SanitizeTerminalText(model.Notice)
	}
	surface.PutClipped(0, statusY, width, status, StyleStatus)

	if drawerRows != 0 {
		renderDrawer(surface, model, statusY+1, width, drawerRows)
	}
	if model.Auth.Active {
		renderAuthModal(surface, model.Auth, width, height)
	}
	if model.Mode == ModeWorkspace {
		renderWorkspaceModal(surface, string(model.workspaceName), width, height)
	}
	if model.Mode == ModeFilenameSearch || model.Mode == ModeContentSearch {
		renderSearchModal(surface, string(model.searchInput), model.Mode == ModeContentSearch, width, height)
	}
	if model.Mode == ModePath {
		renderPathModal(surface, string(model.pathInput), width, height)
	}
	if model.Mode == ModeEndpoint {
		renderEndpointModal(surface, string(model.endpointInput), width, height)
	}
	if model.Mode == ModeRename {
		renderRenameModal(surface, model.pendingRename, string(model.renameInput), width, height)
	}
	if model.Mode == ModeMoveConfirm {
		renderMoveModal(surface, model.pendingMove, width, height)
	}
	if model.Mode == ModeDeleteConfirm {
		renderDeleteModal(surface, model.pendingDelete, model.DeleteConfirmation, width, height)
	}
	if model.Mode == ModeCommand || model.Mode == ModeCommandConfirm {
		renderCommandModal(surface, model, width, height)
	}
	if model.Mode == ModeEditDecision || model.Mode == ModeEditSaveAs {
		renderEditDecisionModal(surface, model, width, height)
	}
	if model.Mode == ModeEditLaunchConfirm {
		renderEditLaunchModal(surface, model, width, height)
	}
	if model.Mode == ModeEditRecovery {
		renderEditRecoveryModal(surface, model, width, height)
	}
	if model.Mode == ModeCacheClearConfirm {
		renderCacheClearModal(surface, model, width, height)
	}
	return stats
}

func renderCacheClearModal(surface Surface, model Model, width, height int) {
	if width < 28 || height < 6 {
		return
	}
	modalWidth := min(width-4, 82)
	x := max(0, (width-modalWidth)/2)
	y := max(1, height/2-2)
	scope := "current workspace"
	if model.CacheClearScope == CacheClearAll {
		scope = "all workspaces"
	}
	surface.PutClipped(x, y, modalWidth, " Clear eligible cache ", StyleActiveHeader)
	surface.PutClipped(x, y+1, modalWidth, "Scope: "+scope, StylePlain)
	surface.PutClipped(x, y+2, modalWidth, "Dirty, pinned, leased, referenced, edit-bound, and unknown content is preserved.", StyleError)
	surface.PutClipped(x, y+3, modalWidth, "Enter clear · Esc cancel", StyleStatus)
}

func renderEditRecoveryModal(surface Surface, model Model, width, height int) {
	items := model.EditRecovery.Items
	if len(items) == 0 || width < 24 || height < 7 {
		return
	}
	modalWidth := min(width-4, 96)
	visibleRows := min(len(items), max(1, min(8, height-6)))
	start := max(0, min(model.EditRecovery.Cursor-visibleRows/2, len(items)-visibleRows))
	x := max(0, (width-modalWidth)/2)
	y := max(1, (height-(visibleRows+4))/2)
	surface.PutClipped(x, y, modalWidth, fmt.Sprintf(" Recoverable edits (%d) ", len(items)), StyleActiveHeader)
	for row := 0; row < visibleRows; row++ {
		item := items[start+row]
		marker := "  "
		style := StylePlain
		if start+row == model.EditRecovery.Cursor {
			marker, style = "> ", StyleCursor
		}
		availability := "ready"
		if !item.Usable {
			availability = "retained: " + item.Diagnostic
		}
		line := fmt.Sprintf("%s%s %s %s · %s", marker, item.SessionID, item.Purpose, item.Location.Path, availability)
		surface.PutClipped(x, y+1+row, modalWidth, line, style)
	}
	selected := items[model.EditRecovery.Cursor]
	surface.PutClipped(x, y+1+visibleRows, modalWidth, fmt.Sprintf("state:%s durable:%s", selected.State, selected.Lifecycle), StyleStatus)
	surface.PutClipped(x, y+2+visibleRows, modalWidth, "j/k select · Enter resume/check · K inspect remote · Esc retain", StylePlain)
}

func renderEditDecisionModal(surface Surface, model Model, width, height int) {
	state := model.EditDecision
	if !state.Active || width < 20 || height < 5 {
		return
	}
	modalWidth := min(width-4, 76)
	x := max(0, (width-modalWidth)/2)
	y := max(1, height/2-2)
	surface.PutClipped(x, y, modalWidth, " Edit result ", StyleActiveHeader)
	surface.PutClipped(x, y+1, modalWidth, SanitizeTerminalText(string(state.Location.Path)), StylePlain)
	if model.Mode == ModeEditSaveAs {
		surface.PutClipped(x, y+2, modalWidth, "Save as: "+SanitizeTerminalText(string(model.editSaveAs)), StyleStatus)
		surface.PutClipped(x, y+3, modalWidth, "Enter confirm · Esc back", StylePlain)
		return
	}
	var instruction string
	switch state.State {
	case "ready":
		instruction = "Opener lease retained: Enter check changes · Esc keep for recovery"
	case "awaiting_upload_confirmation":
		instruction = "Enter upload · a save as · x skip · Esc retain"
	case "remote_changed":
		instruction = "Remote changed: Enter refresh · x skip · Esc retain"
	case "conflict":
		instruction = "Conflict: K inspect remote · w overwrite · a save as · x skip · Esc retain"
	case "sync_back_frozen":
		instruction = "Prepared sync-back: Enter queue exact plan · K inspect remote · Esc retain"
	default:
		instruction = "Observation uncertain: x abandon · Esc retain for recovery"
	}
	surface.PutClipped(x, y+2, modalWidth, instruction, StyleError)
	if state.Message != "" {
		surface.PutClipped(x, y+3, modalWidth, SanitizeTerminalText(state.Message), StylePlain)
	}
}

func renderEditLaunchModal(surface Surface, model Model, width, height int) {
	state := model.EditLaunch
	if !state.Active || width < 20 || height < 5 {
		return
	}
	modalWidth := min(width-4, 90)
	x := max(0, (width-modalWidth)/2)
	y := max(1, height/2-2)
	surface.PutClipped(x, y, modalWidth, " Confirm external direct execution ", StyleActiveHeader)
	surface.PutClipped(x, y+1, modalWidth, SanitizeTerminalText(state.Command), StylePlain)
	surface.PutClipped(x, y+2, modalWidth, "Enter run · Esc retain without launching", StyleStatus)
}

func drawerRows(drawer DrawerState, height int) int {
	if drawer.Mode == DrawerClosed || height < 5 {
		return 0
	}
	requested := drawer.Rows
	if requested <= 0 {
		requested = 6
	}
	return min(requested, max(2, height-3))
}

func renderDrawer(surface Surface, model Model, y, width, rows int) {
	style := StyleHeader
	if model.Drawer.Focus == FocusDrawer {
		style = StyleActiveHeader
	}
	header := drawerTabLabel(model.Drawer.Mode)
	surface.PutClipped(0, y, width, header, style)
	if rows <= 1 {
		return
	}
	switch model.Drawer.Mode {
	case DrawerPreview:
		renderPreviewDrawer(surface, model.Preview, y+1, width, rows-1)
	case DrawerSearch:
		renderSearchDrawer(surface, model.Search, y+1, width, rows-1)
	case DrawerContentSearch:
		renderContentSearchDrawer(surface, model.ContentSearch, y+1, width, rows-1)
	case DrawerJobs:
		renderJobsDrawer(surface, model.Jobs, model.JobCursor, y+1, width, rows-1)
	case DrawerLog:
		renderLogDrawer(surface, model.Diagnostics, y+1, width, rows-1)
	}
}

func renderLogDrawer(surface Surface, records []diagnostic.Record, y, width, rows int) {
	if len(records) == 0 {
		surface.PutClipped(0, y, width, "No bounded log records", StylePreview)
		return
	}
	start := max(0, len(records)-rows)
	for row := 0; row < rows && start+row < len(records); row++ {
		record := records[start+row]
		line := fmt.Sprintf("%s  %-5s  %s/%s", record.Time.Format("15:04:05"), record.Level, record.Component, record.Event)
		if record.JobID != "" {
			line += "  job=" + string(record.JobID)
		}
		if record.EndpointID != "" {
			line += "  endpoint=" + string(record.EndpointID)
		}
		if record.ErrorCode != "" {
			line += "  code=" + string(record.ErrorCode)
		}
		surface.PutClipped(0, y+row, width, line, StylePreview)
	}
}

func drawerTabLabel(active DrawerMode) string {
	if active == DrawerSearch {
		return "[Files]  Content  — f/g/ search; Esc pane"
	}
	if active == DrawerContentSearch {
		return "Files  [Content]  — f/g/ search; Esc pane"
	}
	labels := []struct {
		mode DrawerMode
		name string
	}{{DrawerPreview, "Preview"}, {DrawerJobs, "Jobs"}, {DrawerLog, "Log"}}
	parts := make([]string, 0, len(labels))
	for _, label := range labels {
		if label.mode == active {
			parts = append(parts, "["+label.name+"]")
		} else {
			parts = append(parts, label.name)
		}
	}
	return strings.Join(parts, "  ") + "  — K/J/L switch; Esc pane"
}

func renderSearchDrawer(surface Surface, state SearchState, y, width, rows int) {
	header := fmt.Sprintf("f:%s · %d results", SanitizeTerminalText(state.Query), len(state.Results))
	if state.Loading {
		header += " · searching"
	} else if state.Terminal.Status != "" {
		header += " · " + string(state.Terminal.Status)
		if state.Terminal.StopReason != "none" && state.Terminal.StopReason != "" {
			header += "/" + string(state.Terminal.StopReason)
		}
	}
	if state.Message != "" {
		header += " · " + SanitizeTerminalText(state.Message)
	}
	surface.PutClipped(0, y, width, header, StylePreview)
	if rows <= 1 {
		return
	}
	visible := rows - 1
	window := ComputeWindow(len(state.Results), state.Cursor, visible, 0)
	for index := window.VisibleStart; index < window.VisibleEnd; index++ {
		result := state.Results[index]
		marker := "  "
		style := StylePreview
		if index == state.Cursor {
			marker, style = "> ", StyleCursor
		}
		line := marker + string(result.Entry.Kind) + "  " + SanitizeTerminalText(result.RelativePath)
		surface.PutClipped(0, y+1+index-window.VisibleStart, width, line, style)
	}
}

func renderContentSearchDrawer(surface Surface, state ContentSearchState, y, width, rows int) {
	header := fmt.Sprintf("g/:%s · %d matches", SanitizeTerminalText(state.Query), len(state.Results))
	if state.Loading {
		header += " · slow SFTP scan"
	} else if state.Terminal.Status != "" {
		header += " · " + string(state.Terminal.Status)
		if state.Terminal.StopReason != "none" && state.Terminal.StopReason != "" {
			header += "/" + string(state.Terminal.StopReason)
		}
	}
	if state.Message != "" {
		header += " · " + SanitizeTerminalText(state.Message)
	}
	surface.PutClipped(0, y, width, header, StylePreview)
	if rows <= 1 {
		return
	}
	window := ComputeWindow(len(state.Results), state.Cursor, rows-1, 0)
	for index := window.VisibleStart; index < window.VisibleEnd; index++ {
		result := state.Results[index]
		marker, style := "  ", StylePreview
		if index == state.Cursor {
			marker, style = "> ", StyleCursor
		}
		line := fmt.Sprintf("%s%s:%d:%d  %s", marker, SanitizeTerminalText(result.RelativePath), result.Line, result.Offset, SanitizeTerminalText(result.Snippet))
		surface.PutClipped(0, y+1+index-window.VisibleStart, width, line, style)
	}
}

func renderPreviewDrawer(surface Surface, preview PreviewState, y, width, rows int) {
	header := string(preview.Location.Path)
	if preview.Kind != "" {
		header += " [" + preview.Kind + "]"
	}
	if preview.Loading {
		header += " [loading]"
	}
	if preview.Truncated {
		header += " [truncated]"
	}
	if preview.Summary != "" {
		header += " " + SanitizeTerminalText(preview.Summary)
	}
	surface.PutClipped(0, y, width, header, StylePreview)
	if rows <= 1 {
		return
	}
	style := StylePreview
	if preview.Message != "" {
		style = StyleError
	}
	lines := strings.Split(preview.DisplayText(), "\n")
	for row := 0; row < rows-1 && row < len(lines); row++ {
		surface.PutClipped(0, y+1+row, width, lines[row], style)
	}
}

func renderJobsDrawer(surface Surface, jobs []transfer.JobView, cursor, y, width, rows int) {
	if len(jobs) == 0 {
		surface.PutClipped(0, y, width, "No durable Jobs", StylePreview)
		return
	}
	start := min(max(cursor, 0), len(jobs)-1)
	for row := 0; row < rows && start+row < len(jobs); row++ {
		view := jobs[start+row]
		state := string(view.Snapshot.State)
		if view.WaitingReason != "" {
			state += " (" + view.WaitingReason + ")"
		}
		line := fmt.Sprintf("%s  %s  %d item(s)  %s  %s → %s", state, view.Phase, view.Items, formatJobBytes(view.Bytes, view.BytesTotal), view.Source.Path, view.Final.Path)
		rowStyle := StylePreview
		if start+row == cursor {
			rowStyle = StyleCursor
		}
		surface.PutClipped(0, y+row, width, line, rowStyle)
	}
}

func formatJobBytes(completed uint64, total *uint64) string {
	if total == nil {
		return fmt.Sprintf("%d B", completed)
	}
	return fmt.Sprintf("%d/%d B", completed, *total)
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

func renderSearchModal(surface Surface, value string, content bool, width, height int) {
	if width < 24 || height < 5 {
		return
	}
	modalWidth := min(width-4, 76)
	x := max(0, (width-modalWidth)/2)
	y := max(1, height/2-2)
	title := " Recursive filename search "
	footer := "Provider-only · no symlink following · Enter search · Esc cancel"
	if content {
		title = " Slow SFTP content search "
		footer = "Literal text · binary skipped · remote reads are bounded · Enter search"
	}
	surface.PutClipped(x, y, modalWidth, title, StyleActiveHeader)
	surface.PutClipped(x, y+1, modalWidth, "Pattern: "+SanitizeTerminalText(value), StyleStatus)
	surface.PutClipped(x, y+2, modalWidth, footer, StylePlain)
}

func renderPathModal(surface Surface, value string, width, height int) {
	modalWidth := min(width-4, 64)
	if modalWidth < 20 || height < 7 {
		return
	}
	const modalHeight = 5
	x := (width - modalWidth) / 2
	y := (height - modalHeight) / 2
	for row := 0; row < modalHeight; row++ {
		surface.PutClipped(x, y+row, modalWidth, strings.Repeat(" ", modalWidth), StyleStatus)
	}
	surface.PutClipped(x+1, y, modalWidth-2, "Go to absolute path", StyleStatus)
	surface.PutClipped(x+1, y+2, modalWidth-2, "Path: "+SanitizeTerminalText(value), StyleStatus)
	surface.PutClipped(x+1, y+3, modalWidth-2, "[Enter] open  [Esc] cancel", StyleStatus)
}

func renderEndpointModal(surface Surface, value string, width, height int) {
	modalWidth := min(width-4, 64)
	if modalWidth < 20 || height < 7 {
		return
	}
	const modalHeight = 6
	x := (width - modalWidth) / 2
	y := (height - modalHeight) / 2
	for row := 0; row < modalHeight; row++ {
		surface.PutClipped(x, y+row, modalWidth, strings.Repeat(" ", modalWidth), StyleStatus)
	}
	surface.PutClipped(x+1, y, modalWidth-2, "Change active endpoint", StyleStatus)
	surface.PutClipped(x+1, y+2, modalWidth-2, "Host alias: "+SanitizeTerminalText(value), StyleStatus)
	surface.PutClipped(x+1, y+3, modalWidth-2, "type local for LocalFS", StyleStatus)
	surface.PutClipped(x+1, y+4, modalWidth-2, "[Enter] connect  [Esc] cancel", StyleStatus)
}

func renderRenameModal(surface Surface, reference transfer.FileRef, value string, width, height int) {
	modalWidth := min(width-4, 64)
	if modalWidth < 20 || height < 7 {
		return
	}
	const modalHeight = 6
	x := (width - modalWidth) / 2
	y := (height - modalHeight) / 2
	for row := 0; row < modalHeight; row++ {
		surface.PutClipped(x, y+row, modalWidth, strings.Repeat(" ", modalWidth), StyleStatus)
	}
	surface.PutClipped(x+1, y, modalWidth-2, "Rename through durable Job", StyleStatus)
	surface.PutClipped(x+1, y+1, modalWidth-2, "Source: "+SanitizeTerminalText(string(reference.Location.Path)), StyleStatus)
	surface.PutClipped(x+1, y+3, modalWidth-2, "Name: "+SanitizeTerminalText(value), StyleStatus)
	surface.PutClipped(x+1, y+4, modalWidth-2, "[Enter] queue  [Esc] cancel", StyleStatus)
}

func renderDeleteModal(surface Surface, references []transfer.FileRef, confirmation, width, height int) {
	modalWidth := min(width-4, 72)
	if modalWidth < 20 || height < 8 {
		return
	}
	const modalHeight = 7
	x := (width - modalWidth) / 2
	y := (height - modalHeight) / 2
	for row := 0; row < modalHeight; row++ {
		surface.PutClipped(x, y+row, modalWidth, strings.Repeat(" ", modalWidth), StyleStatus)
	}
	title := "Delete frozen selection"
	message := "This action is irreversible when trash is unavailable."
	if confirmation >= 2 {
		title = "Confirm irreversible deletion"
		message = "Second confirmation: queue deletion of the frozen identities."
	}
	surface.PutClipped(x+1, y, modalWidth-2, title, StyleStatus)
	surface.PutClipped(x+1, y+2, modalWidth-2, fmt.Sprintf("Targets: %d", len(references)), StyleStatus)
	if len(references) != 0 {
		surface.PutClipped(x+1, y+3, modalWidth-2, SanitizeTerminalText(string(references[0].Location.Path)), StyleStatus)
	}
	surface.PutClipped(x+1, y+4, modalWidth-2, message, StyleError)
	surface.PutClipped(x+1, y+5, modalWidth-2, "[Enter] confirm  [Esc] cancel", StyleStatus)
}

func renderMoveModal(surface Surface, intents []Intent, width, height int) {
	modalWidth := min(width-4, 72)
	if modalWidth < 20 || height < 7 {
		return
	}
	const modalHeight = 6
	x := (width - modalWidth) / 2
	y := (height - modalHeight) / 2
	for row := 0; row < modalHeight; row++ {
		surface.PutClipped(x, y+row, modalWidth, strings.Repeat(" ", modalWidth), StyleStatus)
	}
	surface.PutClipped(x+1, y, modalWidth-2, "Confirm durable move", StyleStatus)
	surface.PutClipped(x+1, y+2, modalWidth-2, fmt.Sprintf("Frozen operations: %d", len(intents)), StyleStatus)
	if len(intents) != 0 {
		line := fmt.Sprintf("%s → %s", intents[0].Source.Location.Path, intents[0].Location.Path)
		surface.PutClipped(x+1, y+3, modalWidth-2, SanitizeTerminalText(line), StyleStatus)
	}
	surface.PutClipped(x+1, y+4, modalWidth-2, "Source is deleted only after destination verification. [Enter] queue  [Esc] cancel", StyleStatus)
}

func renderCommandModal(surface Surface, model Model, width, height int) {
	modalWidth := min(width-4, 76)
	if modalWidth < 24 || height < 9 {
		return
	}
	const modalHeight = 7
	x := (width - modalWidth) / 2
	y := (height - modalHeight) / 2
	for row := 0; row < modalHeight; row++ {
		surface.PutClipped(x, y+row, modalWidth, strings.Repeat(" ", modalWidth), StyleStatus)
	}
	pane := model.Panes[model.Active]
	title := "One-time command"
	footer := "[Enter] review  [Esc] cancel"
	if model.Mode == ModeCommandConfirm {
		title = "Confirm one-time command"
		footer = "[Enter] run  [Esc] cancel"
	}
	endpoint := pane.Endpoint.DisplayName
	if endpoint == "" {
		endpoint = string(pane.Endpoint.Kind)
	}
	surface.PutClipped(x+1, y, modalWidth-2, title, StyleStatus)
	surface.PutClipped(x+1, y+1, modalWidth-2, "Endpoint: "+SanitizeTerminalText(endpoint), StyleStatus)
	surface.PutClipped(x+1, y+2, modalWidth-2, "CWD: "+SanitizeTerminalText(string(pane.Location.Path)), StyleStatus)
	execution := "Exec: local shell -c; cwd via process Dir"
	if pane.Endpoint.Kind == domain.EndpointSSH {
		execution = "Exec: fresh ssh -T; cwd marker; no fallback"
	}
	surface.PutClipped(x+1, y+3, modalWidth-2, execution, StyleStatus)
	surface.PutClipped(x+1, y+4, modalWidth-2, "Command: "+SanitizeTerminalText(string(model.commandInput)), StyleStatus)
	surface.PutClipped(x+1, y+5, modalWidth-2, footer, StyleStatus)
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
	connection := connectionLabel(pane.Connection)
	header := fmt.Sprintf(" %s  %s (%s)", SanitizeTerminalText(name), SanitizeTerminalText(string(pane.Location.Path)), connection)
	style := StyleHeader
	if paneID == active {
		style = StyleActiveHeader
		header = fmt.Sprintf("[%s] %s (%s)", SanitizeTerminalText(name), SanitizeTerminalText(string(pane.Location.Path)), connection)
	}
	surface.PutClipped(x, 0, width, header, style)
}

func connectionLabel(state domain.ConnectionState) string {
	if state == domain.StateAuthRequired {
		return "waiting_auth"
	}
	if state == "" {
		return "unknown"
	}
	return string(state)
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
		text := marker + SanitizeTerminalText(entry.Name) + formatEntryMetadata(entry)
		surface.PutClipped(x, y+index-window.VisibleStart, width, text, style)
	}
	return visited
}

func formatEntryMetadata(entry domain.Entry) string {
	size := "—"
	if entry.Metadata.Size != nil {
		size = formatBytes(*entry.Metadata.Size)
	}
	mode := "—"
	if entry.Metadata.Mode != nil {
		mode = fmt.Sprintf("%04o", *entry.Metadata.Mode&0o7777)
	}
	modified := "—"
	if entry.Metadata.ModifiedAt != nil {
		modified = entry.Metadata.ModifiedAt.UTC().Format("2006-01-02 15:04")
	}
	result := fmt.Sprintf("  [%s] %s %s %s", entry.Kind, size, modified, mode)
	if entry.Symlink != nil {
		result += " -> " + SanitizeTerminalText(entry.Symlink.RawTarget)
	}
	return result
}

func formatBytes(value uint64) string {
	const unit = uint64(1024)
	if value < unit {
		return fmt.Sprintf("%d B", value)
	}
	units := []string{"KiB", "MiB", "GiB", "TiB"}
	amount := float64(value)
	index := -1
	for amount >= float64(unit) && index+1 < len(units) {
		amount /= float64(unit)
		index++
	}
	return fmt.Sprintf("%.1f %s", amount, units[index])
}
