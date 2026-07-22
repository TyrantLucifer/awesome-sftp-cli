package tui

import (
	"fmt"
	"strings"

	"github.com/TyrantLucifer/awesome-sftp-cli/internal/cache"
	"github.com/TyrantLucifer/awesome-sftp-cli/internal/diagnostic"
	"github.com/TyrantLucifer/awesome-sftp-cli/internal/domain"
	"github.com/TyrantLucifer/awesome-sftp-cli/internal/job"
	"github.com/TyrantLucifer/awesome-sftp-cli/internal/transfer"
)

type CellStyle uint8

const (
	StyleCanvas CellStyle = iota
	StylePlain
	StyleMuted
	StyleHeader
	StyleActiveHeader
	StyleCursor
	StyleInactiveCursor
	StyleSelected
	StyleStatus
	StyleStatusAccent
	StylePreview
	StyleBorder
	StyleDirectory
	StyleSymlink
	StyleSuccess
	StyleWarning
	StyleError
	StyleModal
	StyleModalTitle
	StyleModalMuted
	StyleModalWarning
	StyleModalError
	StyleInput
	StyleTab
	StyleActiveTab
	styleCount
)

type Surface interface {
	Size() (width, height int)
	Clear()
	Fill(x, y, width int, style CellStyle)
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
	surface.Fill(0, 0, width, StyleHeader)
	surface.PutClipped(0, 0, width, " AMSFTP  Open workspace or SSH host", StyleActiveHeader)
	if height == 1 {
		return
	}
	choices := picker.Visible()
	surface.Fill(0, 1, width, StyleInput)
	surface.PutClipped(0, 1, width, " SSH › "+SanitizeTerminalText(picker.Query()), StyleInput)
	count := fmt.Sprintf("%d matches ", len(choices))
	if len(choices) == 1 {
		count = "1 match "
	}
	if width >= 24 {
		putRight(surface, 0, 1, width, count, StyleModalMuted)
	}
	if height == 2 {
		return
	}
	surface.PutClipped(0, 2, width, strings.Repeat("─", width), StyleBorder)
	if height == 3 {
		return
	}
	choiceRows := max(0, height-4)
	for index := 0; index < len(choices) && index < choiceRows; index++ {
		choice := choices[index]
		marker := "  "
		style := StylePlain
		if index == picker.SelectedIndex() {
			marker = "▌ "
			style = StyleCursor
		}
		line := fmt.Sprintf("%s%-10s %s", marker, pickerKindLabel(choice.Kind), SanitizeTerminalText(choice.Name))
		if choice.Problem != "" {
			line += " — " + SanitizeTerminalText(choice.Problem)
			if index != picker.SelectedIndex() {
				style = StyleError
			}
		}
		if index == picker.SelectedIndex() {
			surface.Fill(0, 3+index, width, style)
		}
		surface.PutClipped(0, 3+index, width, line, style)
	}
	footer := " ↑/↓ select · Enter open · Esc quit"
	if message != "" {
		footer = " " + SanitizeTerminalText(message)
	}
	surface.Fill(0, height-1, width, StyleStatus)
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
	stats.VisitedEntries += renderPaneRows(surface, model.Panes[Left], model.Active == Left, 0, leftWidth, 1, listRows, options.Overscan)
	stats.VisitedEntries += renderPaneRows(surface, model.Panes[Right], model.Active == Right, rightX, rightWidth, 1, listRows, options.Overscan)

	for y := 0; y < height-drawerRows-1; y++ {
		surface.PutClipped(leftWidth, y, 1, "│", StyleBorder)
	}
	statusY := height - drawerRows - 1
	renderStatusLine(surface, model, statusY, width)

	if drawerRows != 0 {
		renderDrawer(surface, model, statusY+1, width, drawerRows)
	}
	if model.Auth.Active {
		renderAuthModal(surface, model.Auth, width, height)
	}
	if model.Mode == ModeWorkspace {
		renderWorkspaceModal(surface, string(model.workspaceName), width, height)
	}
	if model.Mode == ModeFilenameSearch || model.Mode == ModeContentSearch || model.Mode == ModeContentSearchConfirm {
		renderSearchModal(surface, string(model.searchInput), model.Mode == ModeContentSearch || model.Mode == ModeContentSearchConfirm, model.Mode == ModeContentSearchConfirm, width, height)
	}
	if model.Mode == ModePath {
		renderPathModal(surface, string(model.pathInput), width, height)
	}
	if model.Mode == ModeEndpoint {
		renderEndpointModal(surface, model.endpointPicker, width, height)
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

func renderStatusLine(surface Surface, model Model, y, width int) {
	if width <= 0 {
		return
	}
	surface.Fill(0, y, width, StyleStatus)
	badge := " " + modeLabel(model.Mode) + " "
	surface.PutClipped(0, y, width, badge, StyleStatusAccent)
	leftX := len([]rune(badge)) + 1
	primary := renderStatusBar(model)
	right := renderStatusOptions(model.Panes[model.Active], model.CachePolicy)
	rightWidth := len([]rune(right))
	rightX := width - rightWidth - 1
	if right == "" || rightX <= leftX+12 || len([]rune(primary)) > rightX-leftX-1 {
		right = ""
		rightX = width
	} else {
		surface.PutClipped(rightX, y, rightWidth, right, StyleStatus)
	}
	surface.PutClipped(leftX, y, max(0, rightX-leftX-1), primary, StyleStatus)
}

func modeLabel(mode Mode) string {
	switch mode {
	case ModeVisual:
		return "VISUAL"
	case ModeVisualLine:
		return "V-LINE"
	case ModeFilter:
		return "JUMP"
	case ModeFilenameSearch:
		return "FIND"
	case ModeContentSearch, ModeContentSearchConfirm:
		return "CONTENT"
	case ModeEndpoint:
		return "ENDPOINT"
	case ModeWorkspace:
		return "SAVE"
	case ModePath:
		return "PATH"
	case ModeRename:
		return "RENAME"
	case ModeCommand, ModeCommandConfirm:
		return "COMMAND"
	case ModeAuth:
		return "AUTH"
	default:
		return "NORMAL"
	}
}

func renderStatusBar(model Model) string {
	active := model.Panes[model.Active]
	if model.Mode == ModeFilter {
		query := SanitizeTerminalText(active.Filter)
		if query == "" {
			query = "type to search"
		}
		matchLabel := "matches"
		if active.VisibleCount() == 1 {
			matchLabel = "match"
		}
		return fmt.Sprintf("Jump: %s | %d %s | ↑/↓ select | Enter jump | Esc clear", query, active.VisibleCount(), matchLabel)
	}
	segments := []string{renderPaneStatus(active)}
	if model.Mode == ModeVisual {
		segments = append(segments, "Visual selection")
	}
	if model.RecoverableEdits != 0 {
		label := "edits"
		if model.RecoverableEdits == 1 {
			label = "edit"
		}
		segments = append(segments, fmt.Sprintf("%d %s to recover (E)", model.RecoverableEdits, label))
	}
	if active.Listing.Message != "" {
		segments = append(segments, SanitizeTerminalText(active.Listing.Message))
	}
	if model.Notice != "" && (model.RecoverableEdits == 0 || !strings.Contains(model.Notice, "recoverable edit session")) {
		segments = append(segments, SanitizeTerminalText(model.Notice))
	}
	if active.Filter != "" {
		segments = append(segments, "Filter: "+SanitizeTerminalText(active.Filter))
	}
	if model.Count != 0 {
		segments = append(segments, fmt.Sprintf("Count: %d", model.Count))
	}
	if active.Endpoint.Kind == domain.EndpointSSH {
		if helper := renderHelperStatus(active.Capabilities); helper != "" {
			segments = append(segments, helper)
		}
	}
	return strings.Join(segments, " | ")
}

func renderStatusOptions(active PaneState, policy cache.Policy) string {
	direction := "↑"
	if active.Sort.Descending {
		direction = "↓"
	}
	segments := []string{"Sort: " + string(active.Sort.Key) + " " + direction}
	if active.ShowHidden {
		segments = append(segments, "Hidden files shown")
	}
	switch policy {
	case cache.PolicyLRU:
		segments = append(segments, "Cache: automatic")
	case cache.PolicyEphemeral:
		segments = append(segments, "Cache: temporary")
	case cache.PolicyPinnedOffline:
		segments = append(segments, "Cache: offline")
	}
	return strings.Join(segments, " · ")
}

func renderPaneStatus(pane PaneState) string {
	if pane.Listing.Partial {
		return "Partial results"
	}
	if pane.Listing.Loading {
		return "Loading…"
	}
	switch pane.Connection {
	case domain.StateConnecting:
		return "Connecting…"
	case domain.StateDisconnected:
		return "Disconnected"
	case domain.StateDegraded:
		return "Limited connection"
	case domain.StateAuthRequired:
		return "Authentication required"
	case domain.StateFailed:
		return "Connection failed"
	default:
		return "Ready"
	}
}

func renderHelperStatus(snapshot domain.CapabilitySnapshot) string {
	capability, ok := snapshot.Lookup("helper_status")
	if !ok {
		return ""
	}
	values := make(map[string]string, len(capability.Constraints))
	for _, constraint := range capability.Constraints {
		values[constraint.Name] = constraint.Value
	}
	level := values["level"]
	if level != "0" && level != "1" {
		return "Connection mode unknown"
	}
	if level == "1" {
		result := "Enhanced: Helper"
		if version := values["version"]; version != "" {
			result += " " + SanitizeTerminalText(version)
		}
		return result
	}
	if values["reason"] == "session_failed" {
		return "Standard SFTP (enhancement failed)"
	}
	return "Standard SFTP"
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
	fillPanel(surface, x, y, modalWidth, 4)
	surface.PutClipped(x+1, y, modalWidth-2, "Clear eligible cache", StyleModalTitle)
	surface.PutClipped(x+1, y+1, modalWidth-2, "Scope: "+scope, StyleModal)
	surface.PutClipped(x+1, y+2, modalWidth-2, "Dirty, pinned, leased, referenced, edit-bound, and unknown content is preserved.", StyleModalWarning)
	surface.Fill(x, y+3, modalWidth, StyleStatus)
	surface.PutClipped(x, y+3, modalWidth, "[Enter] clear  [Esc] cancel", StyleStatus)
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
	fillPanel(surface, x, y, modalWidth, visibleRows+3)
	surface.PutClipped(x+1, y, modalWidth-2, fmt.Sprintf("Recoverable edits (%d)", len(items)), StyleModalTitle)
	for row := 0; row < visibleRows; row++ {
		item := items[start+row]
		marker := "  "
		style := StyleModal
		if start+row == model.EditRecovery.Cursor {
			marker, style = "▌ ", StyleCursor
			surface.Fill(x, y+1+row, modalWidth, style)
		}
		availability := "ready"
		if !item.Usable {
			availability = "retained: " + item.Diagnostic
		}
		line := fmt.Sprintf("%s%s %s %s · %s", marker, item.SessionID, item.Purpose, item.Location.Path, availability)
		surface.PutClipped(x+1, y+1+row, modalWidth-2, line, style)
	}
	selected := items[model.EditRecovery.Cursor]
	surface.Fill(x, y+1+visibleRows, modalWidth, StyleStatus)
	surface.PutClipped(x, y+1+visibleRows, modalWidth, fmt.Sprintf("state:%s durable:%s", selected.State, selected.Lifecycle), StyleStatus)
	surface.PutClipped(x+1, y+2+visibleRows, modalWidth-2, "j/k select · Enter resume/check · K inspect remote · Esc retain", StyleModal)
}

func renderEditDecisionModal(surface Surface, model Model, width, height int) {
	state := model.EditDecision
	if !state.Active || width < 20 || height < 5 {
		return
	}
	modalWidth := min(width-4, 76)
	x := max(0, (width-modalWidth)/2)
	y := max(1, height/2-2)
	fillPanel(surface, x, y, modalWidth, 4)
	surface.PutClipped(x+1, y, modalWidth-2, "Edit result", StyleModalTitle)
	surface.PutClipped(x+1, y+1, modalWidth-2, SanitizeTerminalText(string(state.Location.Path)), StyleModal)
	if model.Mode == ModeEditSaveAs {
		surface.PutClipped(x+1, y+2, modalWidth-2, "Save as: "+SanitizeTerminalText(string(model.editSaveAs)), StyleInput)
		surface.Fill(x, y+3, modalWidth, StyleStatus)
		surface.PutClipped(x+1, y+3, modalWidth-2, "[Enter] confirm  [Esc] back", StyleStatus)
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
	surface.PutClipped(x+1, y+2, modalWidth-2, instruction, StyleModalWarning)
	if state.Message != "" {
		surface.PutClipped(x+1, y+3, modalWidth-2, SanitizeTerminalText(state.Message), StyleModal)
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
	fillPanel(surface, x, y, modalWidth, 3)
	surface.PutClipped(x+1, y, modalWidth-2, "Confirm external direct execution", StyleModalTitle)
	surface.PutClipped(x+1, y+1, modalWidth-2, SanitizeTerminalText(state.Command), StyleModal)
	surface.Fill(x, y+2, modalWidth, StyleStatus)
	surface.PutClipped(x, y+2, modalWidth, "[Enter] run  [Esc] retain without launching", StyleStatus)
}

func drawerRows(drawer DrawerState, height int) int {
	if drawer.Mode == DrawerClosed || height < 5 {
		return 0
	}
	requested := drawer.Rows
	if requested <= 0 {
		requested = 6
	}
	if drawer.Mode == DrawerPreview {
		requested = max(requested, min(16, height/2))
	}
	return min(requested, max(2, height-3))
}

func renderDrawer(surface Surface, model Model, y, width, rows int) {
	style := StyleTab
	if model.Drawer.Focus == FocusDrawer {
		style = StyleActiveTab
	}
	header := drawerTabLabel(model.Drawer.Mode)
	surface.Fill(0, y, width, style)
	surface.PutClipped(0, y, width, header, style)
	if rows <= 1 {
		return
	}
	for row := 1; row < rows; row++ {
		surface.Fill(0, y+row, width, StylePreview)
	}
	switch model.Drawer.Mode {
	case DrawerPreview:
		renderPreviewDrawer(surface, model.Preview, y+1, width, rows-1)
	case DrawerSearch:
		renderSearchDrawer(surface, model.Search, y+1, width, rows-1)
	case DrawerContentSearch:
		renderContentSearchDrawer(surface, model.ContentSearch, y+1, width, rows-1)
	case DrawerJobs:
		renderJobsDrawer(surface, model.Jobs, model.jobProgress, model.JobCursor, y+1, width, rows-1)
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
		style := StylePreview
		switch strings.ToUpper(record.Level) {
		case "ERROR":
			style = StyleError
		case "WARN", "WARNING":
			style = StyleWarning
		}
		surface.PutClipped(0, y+row, width, line, style)
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
			marker, style = "▌ ", StyleCursor
			surface.Fill(0, y+1+index-window.VisibleStart, width, style)
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
			marker, style = "▌ ", StyleCursor
			surface.Fill(0, y+1+index-window.VisibleStart, width, style)
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
	lineOffset := min(max(0, preview.LineOffset), max(0, len(lines)-1))
	for row := 0; row < rows-1 && lineOffset+row < len(lines); row++ {
		surface.PutClipped(0, y+1+row, width, lines[lineOffset+row], style)
	}
}

func renderJobsDrawer(surface Surface, jobs []transfer.JobView, progress map[domain.JobID]jobProgressSample, cursor, y, width, rows int) {
	if len(jobs) == 0 {
		surface.PutClipped(0, y, width, "No durable Jobs", StylePreview)
		return
	}
	cursor = min(max(cursor, 0), len(jobs)-1)
	details := jobDetailLines(jobs[cursor])
	if len(details) > max(0, rows-1) {
		details = details[:max(0, rows-1)]
	}
	visibleJobs := max(1, rows-len(details))
	start := max(0, cursor-visibleJobs/2)
	if start+visibleJobs > len(jobs) {
		start = max(0, len(jobs)-visibleJobs)
	}
	screenRow := 0
	for index := start; index < len(jobs) && screenRow < rows; index++ {
		view := jobs[index]
		selected := index == cursor
		style := jobStyle(view)
		marker := "  "
		if selected {
			style = StyleCursor
			marker = "▌ "
			surface.Fill(0, y+screenRow, width, style)
		}
		surface.PutClipped(0, y+screenRow, width, marker+jobSummary(view, progress[view.Snapshot.JobID]), style)
		screenRow++
		if selected {
			for _, detail := range details {
				if screenRow >= rows {
					break
				}
				surface.PutClipped(0, y+screenRow, width, fitJobDetail(detail.label, detail.value, width), StylePreview)
				screenRow++
			}
		}
	}
}

func jobStyle(view transfer.JobView) CellStyle {
	switch view.Snapshot.State {
	case job.StateCompleted, job.StateCompletedWithSourceRetained:
		return StyleSuccess
	case job.StateFailed:
		return StyleError
	case job.StateWaitingAuth, job.StateWaitingConflict, job.StateRetryWait, job.StateAwaitingConfirmation:
		return StyleWarning
	default:
		return StylePreview
	}
}

type jobDetail struct {
	label string
	value string
}

func jobDetailLines(view transfer.JobView) []jobDetail {
	if view.Kind == transfer.OperationDelete {
		return []jobDetail{{label: "  Target: ", value: string(view.Source.Path)}}
	}
	return []jobDetail{
		{label: "  From: ", value: string(view.Source.Path)},
		{label: "  To:   ", value: string(view.Final.Path)},
	}
}

func fitJobDetail(label, value string, width int) string {
	value = SanitizeTerminalText(value)
	text := label + value
	if width <= 0 || len([]rune(text)) <= width {
		return text
	}
	available := width - len([]rune(label)) - 1
	if available <= 1 {
		return label
	}
	runes := []rune(value)
	head := max(1, available/3)
	tail := max(1, available-head)
	if head+tail >= len(runes) {
		return text
	}
	return label + string(runes[:head]) + "…" + string(runes[len(runes)-tail:])
}

func jobSummary(view transfer.JobView, sample jobProgressSample) string {
	parts := []string{jobOperationLabel(view.Kind), jobStateLabel(view), formatJobBytes(view.Bytes, view.BytesTotal)}
	if jobShowsSpeed(view) {
		speed := "—/s"
		if sample.rateKnown {
			speed = formatBytes(sample.bytesPerSecond) + "/s"
		}
		parts = append(parts, speed)
	}
	if view.BytesTotal == nil && view.Items != 0 {
		label := "items"
		if view.Items == 1 {
			label = "item"
		}
		parts = append(parts, fmt.Sprintf("%d %s", view.Items, label))
	}
	return strings.Join(parts, " · ")
}

func jobOperationLabel(kind transfer.OperationKind) string {
	switch kind {
	case transfer.OperationCopy:
		return "Copy"
	case transfer.OperationMove:
		return "Move"
	case transfer.OperationDelete:
		return "Delete"
	default:
		return "Transfer"
	}
}

func jobStateLabel(view transfer.JobView) string {
	switch view.Snapshot.State {
	case job.StateDraft:
		return "Draft"
	case job.StateAwaitingConfirmation:
		return "Awaiting confirmation"
	case job.StateQueued:
		return "Queued"
	case job.StateRunning:
		switch view.Phase {
		case transfer.PhasePrepared:
			return "Preparing"
		case transfer.PhaseStreaming:
			return "Transferring"
		case transfer.PhaseTransferred, transfer.PhaseVerified, transfer.PhaseCommitting:
			return "Finalizing"
		default:
			return "Running"
		}
	case job.StateVerifying:
		return "Verifying"
	case job.StatePaused:
		return "Paused"
	case job.StateWaitingAuth:
		return "Waiting for authentication"
	case job.StateWaitingConflict:
		return "Waiting for conflict choice"
	case job.StateRetryWait:
		return "Waiting to retry"
	case job.StateCompleted:
		return "Completed"
	case job.StateCompletedWithSourceRetained:
		return "Completed (source retained)"
	case job.StateFailed:
		return "Failed"
	case job.StateCanceled:
		return "Canceled"
	default:
		return "Unknown"
	}
}

func jobShowsSpeed(view transfer.JobView) bool {
	return view.Snapshot.State == job.StateRunning && view.Phase == transfer.PhaseStreaming
}

func formatJobBytes(completed uint64, total *uint64) string {
	if total == nil {
		return formatBytes(completed)
	}
	progress := formatBytes(completed) + " / " + formatBytes(*total)
	if *total != 0 {
		progress += fmt.Sprintf(" (%.0f%%)", float64(completed)*100/float64(*total))
	}
	return progress
}

func renderWorkspaceModal(surface Surface, name string, width, height int) {
	modalWidth := min(width-4, 52)
	if modalWidth < 20 || height < 7 {
		return
	}
	const modalHeight = 5
	x := (width - modalWidth) / 2
	y := (height - modalHeight) / 2
	fillPanel(surface, x, y, modalWidth, modalHeight)
	surface.PutClipped(x+1, y, modalWidth-2, "Save workspace", StyleModalTitle)
	surface.PutClipped(x+1, y+2, modalWidth-2, "Name: "+SanitizeTerminalText(name), StyleInput)
	surface.Fill(x, y+3, modalWidth, StyleStatus)
	surface.PutClipped(x+1, y+3, modalWidth-2, "[Enter] save  [Esc] cancel", StyleStatus)
}

func renderSearchModal(surface Surface, value string, content, confirm bool, width, height int) {
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
	if confirm {
		title = " Confirm slow SFTP scan "
		footer = "≤1000 files · ≤1 MiB/file · ≤32 MiB total · ≤2 min · Enter accept · Esc back"
	}
	fillPanel(surface, x, y, modalWidth, 3)
	surface.PutClipped(x+1, y, modalWidth-2, strings.TrimSpace(title), StyleModalTitle)
	surface.PutClipped(x+1, y+1, modalWidth-2, "Pattern: "+SanitizeTerminalText(value), StyleInput)
	surface.Fill(x, y+2, modalWidth, StyleStatus)
	surface.PutClipped(x+1, y+2, modalWidth-2, footer, StyleStatus)
}

func renderPathModal(surface Surface, value string, width, height int) {
	modalWidth := min(width-4, 64)
	if modalWidth < 20 || height < 7 {
		return
	}
	const modalHeight = 5
	x := (width - modalWidth) / 2
	y := (height - modalHeight) / 2
	fillPanel(surface, x, y, modalWidth, modalHeight)
	surface.PutClipped(x+1, y, modalWidth-2, "Go to absolute path", StyleModalTitle)
	surface.PutClipped(x+1, y+2, modalWidth-2, "Path: "+SanitizeTerminalText(value), StyleInput)
	surface.Fill(x, y+3, modalWidth, StyleStatus)
	surface.PutClipped(x+1, y+3, modalWidth-2, "[Enter] open  [Esc] cancel", StyleStatus)
}

func renderEndpointModal(surface Surface, picker Picker, width, height int) {
	modalWidth := min(width-4, 72)
	if modalWidth < 20 || height < 8 {
		return
	}
	choices := picker.Visible()
	visibleRows := min(len(choices), max(1, min(8, height-7)))
	modalHeight := visibleRows + 5
	x := (width - modalWidth) / 2
	y := (height - modalHeight) / 2
	fillPanel(surface, x, y, modalWidth, modalHeight)
	surface.PutClipped(x+1, y, modalWidth-2, "Change active endpoint", StyleModalTitle)
	surface.PutClipped(x+1, y+1, modalWidth-2, "Filter: "+SanitizeTerminalText(picker.Query()), StyleInput)
	window := ComputeWindow(len(choices), picker.SelectedIndex(), visibleRows, 0)
	for row, index := 0, window.VisibleStart; index < window.VisibleEnd; row, index = row+1, index+1 {
		marker, style := "  ", StylePlain
		if index == picker.SelectedIndex() {
			marker, style = "▌ ", StyleCursor
			surface.Fill(x, y+2+row, modalWidth, style)
		} else {
			style = StyleModal
		}
		surface.PutClipped(x+1, y+2+row, modalWidth-2, marker+SanitizeTerminalText(choices[index].Name), style)
	}
	if len(choices) == 0 {
		surface.PutClipped(x+1, y+2, modalWidth-2, "No configured Host matches", StyleModalError)
	}
	surface.Fill(x, y+modalHeight-2, modalWidth, StyleStatus)
	surface.PutClipped(x+1, y+modalHeight-2, modalWidth-2, "↑/↓ select · Enter connect · Esc cancel", StyleStatus)
}

func renderRenameModal(surface Surface, reference transfer.FileRef, value string, width, height int) {
	modalWidth := min(width-4, 64)
	if modalWidth < 20 || height < 7 {
		return
	}
	const modalHeight = 6
	x := (width - modalWidth) / 2
	y := (height - modalHeight) / 2
	fillPanel(surface, x, y, modalWidth, modalHeight)
	surface.PutClipped(x+1, y, modalWidth-2, "Rename through durable Job", StyleModalTitle)
	surface.PutClipped(x+1, y+1, modalWidth-2, "Source: "+SanitizeTerminalText(string(reference.Location.Path)), StyleModal)
	surface.PutClipped(x+1, y+3, modalWidth-2, "Name: "+SanitizeTerminalText(value), StyleInput)
	surface.Fill(x, y+4, modalWidth, StyleStatus)
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
	fillPanel(surface, x, y, modalWidth, modalHeight)
	title := "Delete frozen selection"
	message := "This action is irreversible when trash is unavailable."
	if confirmation >= 2 {
		title = "Confirm irreversible deletion"
		message = "Second confirmation: queue deletion of the frozen identities."
	}
	surface.PutClipped(x+1, y, modalWidth-2, title, StyleModalError)
	surface.PutClipped(x+1, y+2, modalWidth-2, fmt.Sprintf("Targets: %d", len(references)), StyleModal)
	if len(references) != 0 {
		surface.PutClipped(x+1, y+3, modalWidth-2, SanitizeTerminalText(string(references[0].Location.Path)), StyleModal)
	}
	surface.PutClipped(x+1, y+4, modalWidth-2, message, StyleModalError)
	surface.Fill(x, y+5, modalWidth, StyleStatus)
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
	fillPanel(surface, x, y, modalWidth, modalHeight)
	surface.PutClipped(x+1, y, modalWidth-2, "Confirm durable move", StyleModalTitle)
	surface.PutClipped(x+1, y+2, modalWidth-2, fmt.Sprintf("Frozen operations: %d", len(intents)), StyleModal)
	if len(intents) != 0 {
		line := fmt.Sprintf("%s → %s", intents[0].Source.Location.Path, intents[0].Location.Path)
		surface.PutClipped(x+1, y+3, modalWidth-2, SanitizeTerminalText(line), StyleModal)
	}
	surface.Fill(x, y+4, modalWidth, StyleStatus)
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
	fillPanel(surface, x, y, modalWidth, modalHeight)
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
	surface.PutClipped(x+1, y, modalWidth-2, title, StyleModalTitle)
	surface.PutClipped(x+1, y+1, modalWidth-2, "Endpoint: "+SanitizeTerminalText(endpoint), StyleModal)
	surface.PutClipped(x+1, y+2, modalWidth-2, "CWD: "+SanitizeTerminalText(string(pane.Location.Path)), StyleModal)
	execution := "Exec: local shell -c; cwd via process Dir"
	if pane.Endpoint.Kind == domain.EndpointSSH {
		execution = "Exec: fresh ssh -T; cwd marker; no fallback"
	}
	surface.PutClipped(x+1, y+3, modalWidth-2, execution, StyleModalWarning)
	surface.PutClipped(x+1, y+4, modalWidth-2, "Command: "+SanitizeTerminalText(string(model.commandInput)), StyleInput)
	surface.Fill(x, y+5, modalWidth, StyleStatus)
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
	fillPanel(surface, x, y, modalWidth, modalHeight)
	title := "Authentication — " + SanitizeTerminalText(state.Endpoint)
	surface.PutClipped(x+1, y, modalWidth-2, title, StyleModalTitle)
	surface.PutClipped(x+1, y+1, modalWidth-2, SanitizeTerminalText(state.Prompt), StyleModal)
	surface.Fill(x, y+3, modalWidth, StyleStatus)
	if state.Kind == "confirm" {
		surface.PutClipped(x+1, y+3, modalWidth-2, "[Enter] continue  [Esc] cancel", StyleStatus)
		return
	}
	masked := strings.Repeat("•", len(state.answer))
	surface.PutClipped(x+1, y+3, modalWidth-2, "Answer: "+masked, StyleInput)
}

func putPaneHeader(surface Surface, pane PaneState, paneID, active PaneID, x, width int) {
	name := pane.Endpoint.DisplayName
	if name == "" {
		name = "local"
	}
	style := StyleHeader
	if paneID == active {
		style = StyleActiveHeader
	}
	surface.Fill(x, 0, width, style)
	nameLabel := " " + strings.ToUpper(SanitizeTerminalText(name)) + "  "
	surface.PutClipped(x, 0, width, nameLabel, style)
	connection := "● " + connectionLabel(pane.Connection) + " "
	connectionWidth := len([]rune(connection))
	pathX := x + len([]rune(nameLabel))
	pathWidth := max(0, width-len([]rune(nameLabel))-connectionWidth-1)
	surface.PutClipped(pathX, 0, pathWidth, SanitizeTerminalText(string(pane.Location.Path)), style)
	putRight(surface, x, 0, width, connection, connectionStyle(pane.Connection))
}

func connectionLabel(state domain.ConnectionState) string {
	switch state {
	case domain.StateReady:
		return "READY"
	case domain.StateConnecting:
		return "CONNECTING"
	case domain.StateDegraded:
		return "DEGRADED"
	case domain.StateAuthRequired:
		return "AUTH"
	case domain.StateDisconnected:
		return "OFFLINE"
	case domain.StateFailed:
		return "FAILED"
	default:
		return "UNKNOWN"
	}
}

func connectionStyle(state domain.ConnectionState) CellStyle {
	switch state {
	case domain.StateReady:
		return StyleSuccess
	case domain.StateConnecting, domain.StateDegraded, domain.StateAuthRequired:
		return StyleWarning
	case domain.StateDisconnected, domain.StateFailed:
		return StyleError
	default:
		return StyleHeader
	}
}

func renderPaneRows(
	surface Surface,
	pane PaneState,
	active bool,
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
		selected := pane.selectedAt(index)
		rowStyle := StylePlain
		if selected {
			marker = "• "
			rowStyle = StyleSelected
		}
		if index == pane.Cursor {
			marker = "▌ "
			rowStyle = StyleInactiveCursor
			if active {
				rowStyle = StyleCursor
			}
			if selected {
				marker = "▌•"
			}
		}
		rowY := y + index - window.VisibleStart
		if rowStyle != StylePlain {
			surface.Fill(x, rowY, width, rowStyle)
		}
		surface.PutClipped(x, rowY, min(2, width), marker, rowStyle)
		metadata := formatEntryMetadata(entry, width)
		metadataWidth := len([]rune(metadata))
		metadataX := x + width - metadataWidth - 1
		nameX := x + 2
		nameWidth := max(0, metadataX-nameX-1)
		if metadata == "" {
			nameWidth = max(0, width-3)
		}
		nameStyle := rowStyle
		metadataStyle := rowStyle
		if rowStyle == StylePlain {
			nameStyle = entryStyle(entry)
			metadataStyle = StyleMuted
		}
		name := SanitizeTerminalText(entry.Name)
		if entry.Symlink != nil {
			name += " → " + SanitizeTerminalText(entry.Symlink.RawTarget)
		}
		surface.PutClipped(nameX, rowY, nameWidth, name, nameStyle)
		if metadata != "" && metadataX > nameX {
			surface.PutClipped(metadataX, rowY, metadataWidth, metadata, metadataStyle)
		}
	}
	return visited
}

func entryStyle(entry domain.Entry) CellStyle {
	switch entry.Kind {
	case domain.EntryDirectory:
		return StyleDirectory
	case domain.EntrySymlink:
		return StyleSymlink
	default:
		return StylePlain
	}
}

func formatEntryMetadata(entry domain.Entry, width int) string {
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
	if width >= 58 {
		return fmt.Sprintf("%-4s %8s %16s %4s", entryKindLabel(entry.Kind), size, modified, mode)
	}
	if width >= 38 {
		if entry.Metadata.ModifiedAt != nil {
			modified = entry.Metadata.ModifiedAt.UTC().Format("2006-01-02")
		}
		return fmt.Sprintf("%8s %10s %4s", size, modified, mode)
	}
	if width >= 24 {
		return fmt.Sprintf("%8s", size)
	}
	return ""
}

func entryKindLabel(kind domain.EntryKind) string {
	switch kind {
	case domain.EntryDirectory:
		return "DIR"
	case domain.EntrySymlink:
		return "LINK"
	case domain.EntryFile:
		return "FILE"
	default:
		return strings.ToUpper(string(kind))
	}
}

func putRight(surface Surface, x, y, width int, text string, style CellStyle) {
	textWidth := len([]rune(text))
	if width <= 0 || textWidth <= 0 || textWidth > width {
		return
	}
	surface.PutClipped(x+width-textWidth, y, textWidth, text, style)
}

func fillPanel(surface Surface, x, y, width, height int) {
	for row := 0; row < height; row++ {
		surface.Fill(x, y+row, width, StyleModal)
	}
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
