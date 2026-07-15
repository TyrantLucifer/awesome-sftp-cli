package tui

import (
	"path"
	"sort"
	"strings"

	"github.com/TyrantLucifer/awesome-mac-sftp/internal/domain"
)

type PaneID uint8

const (
	Left PaneID = iota
	Right
)

type Mode string

const (
	ModeNormal     Mode = "normal"
	ModeFilter     Mode = "filter"
	ModeVisual     Mode = "visual"
	ModeVisualLine Mode = "visual_line"
	ModeAuth       Mode = "auth"
	ModeWorkspace  Mode = "workspace_save"
)

const authAnswerByteLimit = 4096

type ListingState struct {
	Generation uint64
	Loading    bool
	Complete   bool
	Partial    bool
	Message    string

	pendingLocation domain.Location
	hasPage         bool
	cursorAnchor    domain.Location
	hasCursorAnchor bool
}

type PaneState struct {
	Endpoint domain.Endpoint
	Location domain.Location
	Entries  []domain.Entry
	Cursor   int
	Filter   string
	Listing  ListingState

	visible          []int
	marks            map[domain.Location]struct{}
	visualAnchor     domain.Location
	visualAnchorView int
	hasVisualAnchor  bool
}

func NewPaneState(endpoint domain.Endpoint, location domain.Location) PaneState {
	return PaneState{
		Endpoint: endpoint,
		Location: location,
		marks:    make(map[domain.Location]struct{}),
	}
}

func (p PaneState) VisibleCount() int {
	return len(p.visible)
}

func (p PaneState) VisibleNames() []string {
	names := make([]string, len(p.visible))
	for index := range p.visible {
		names[index] = p.visibleEntry(index).Name
	}
	return names
}

func (p PaneState) SelectedLocations() []domain.Location {
	selected := make(map[domain.Location]struct{}, len(p.marks)+len(p.visible))
	for location := range p.marks {
		selected[location] = struct{}{}
	}
	if p.hasVisualAnchor && len(p.visible) != 0 {
		start, end := p.visualAnchorView, p.Cursor
		if start > end {
			start, end = end, start
		}
		start = max(0, start)
		end = min(end, len(p.visible)-1)
		for index := start; index <= end; index++ {
			selected[p.visibleEntry(index).Location] = struct{}{}
		}
	}
	locations := make([]domain.Location, 0, len(selected))
	for location := range selected {
		locations = append(locations, location)
	}
	sort.Slice(locations, func(i, j int) bool {
		if locations[i].EndpointID != locations[j].EndpointID {
			return locations[i].EndpointID < locations[j].EndpointID
		}
		return locations[i].Path < locations[j].Path
	})
	return locations
}

func (p PaneState) visibleEntry(index int) domain.Entry {
	if index < 0 || index >= len(p.visible) {
		return domain.Entry{}
	}
	return p.Entries[p.visible[index]]
}

func (p PaneState) currentLocation() (domain.Location, bool) {
	if len(p.visible) == 0 || p.Cursor < 0 || p.Cursor >= len(p.visible) {
		return domain.Location{}, false
	}
	return p.visibleEntry(p.Cursor).Location, true
}

func (p PaneState) selectedAt(index int) bool {
	if index < 0 || index >= len(p.visible) {
		return false
	}
	if _, marked := p.marks[p.visibleEntry(index).Location]; marked {
		return true
	}
	if !p.hasVisualAnchor {
		return false
	}
	start, end := p.visualAnchorView, p.Cursor
	if start > end {
		start, end = end, start
	}
	return index >= start && index <= end
}

func (p *PaneState) rebuildVisible() {
	query := strings.ToLower(p.Filter)
	p.visible = p.visible[:0]
	for index := range p.Entries {
		if query == "" || strings.Contains(strings.ToLower(p.Entries[index].Name), query) {
			p.visible = append(p.visible, index)
		}
	}
	p.clampCursor()
	p.rebindVisualAnchor()
}

func (p *PaneState) appendEntries(entries []domain.Entry) {
	start := len(p.Entries)
	p.Entries = append(p.Entries, entries...)
	query := strings.ToLower(p.Filter)
	for offset := range entries {
		if query == "" || strings.Contains(strings.ToLower(entries[offset].Name), query) {
			p.visible = append(p.visible, start+offset)
		}
	}
	p.clampCursor()
}

func (p *PaneState) clampCursor() {
	if len(p.visible) == 0 {
		p.Cursor = 0
		return
	}
	p.Cursor = min(max(p.Cursor, 0), len(p.visible)-1)
}

func (p *PaneState) rebindVisualAnchor() {
	if !p.hasVisualAnchor {
		return
	}
	for index := range p.visible {
		if p.visibleEntry(index).Location == p.visualAnchor {
			p.visualAnchorView = index
			return
		}
	}
	p.hasVisualAnchor = false
}

func (p *PaneState) rebindCursorAnchor() {
	if !p.Listing.hasCursorAnchor {
		return
	}
	for index := range p.visible {
		if p.visibleEntry(index).Location == p.Listing.cursorAnchor {
			p.Cursor = index
			return
		}
	}
	p.clampCursor()
}

func (p *PaneState) pruneMarks() {
	if len(p.marks) == 0 {
		return
	}
	available := make(map[domain.Location]struct{}, len(p.Entries))
	for _, entry := range p.Entries {
		available[entry.Location] = struct{}{}
	}
	marks := cloneMarks(p.marks)
	for location := range marks {
		if _, exists := available[location]; !exists {
			delete(marks, location)
		}
	}
	p.marks = marks
}

func (p PaneState) clone() PaneState {
	return p
}

func cloneMarks(marks map[domain.Location]struct{}) map[domain.Location]struct{} {
	cloned := make(map[domain.Location]struct{}, len(marks))
	for location := range marks {
		cloned[location] = struct{}{}
	}
	return cloned
}

type PreviewState struct {
	Generation uint64
	Location   domain.Location
	Data       []byte
	BytesRead  int
	Loading    bool
	Truncated  bool
	Binary     bool
	Message    string
}

type AuthState struct {
	Active      bool
	ChallengeID string
	Endpoint    string
	Prompt      string
	Kind        string
	ReturnMode  Mode

	answer []rune
}

func (p PreviewState) DisplayText() string {
	if p.Binary {
		return "[binary preview omitted]"
	}
	if p.Message != "" {
		return SanitizeTerminalText(p.Message)
	}
	return SanitizeTerminalText(string(p.Data))
}

type Model struct {
	Panes   [2]PaneState
	Active  PaneID
	Mode    Mode
	Preview PreviewState
	Auth    AuthState
	Notice  string

	workspaceName []rune
	Width         int
	Height        int
}

func NewModel(left, right PaneState) Model {
	if left.marks == nil {
		left.marks = make(map[domain.Location]struct{})
	}
	if right.marks == nil {
		right.marks = make(map[domain.Location]struct{})
	}
	left.visible = nil
	right.visible = nil
	left.rebuildVisible()
	right.rebuildVisible()
	return Model{Panes: [2]PaneState{left, right}, Active: Left, Mode: ModeNormal}
}

type IntentKind string

const (
	IntentList          IntentKind = "list"
	IntentPreview       IntentKind = "preview"
	IntentAuthResolve   IntentKind = "auth_resolve"
	IntentWorkspaceSave IntentKind = "workspace_save"
)

const PreviewByteLimit = 64 * 1024

type Intent struct {
	Kind        IntentKind
	Pane        PaneID
	Location    domain.Location
	Limit       int
	ChallengeID string
	Answer      []byte
	Cancel      bool
	Name        string
}

type Key string

const (
	KeyTab        Key = "tab"
	KeyParent     Key = "parent"
	KeyDown       Key = "down"
	KeyUp         Key = "up"
	KeyOpen       Key = "open"
	KeyVisual     Key = "visual"
	KeyVisualLine Key = "visual_line"
	KeyMark       Key = "mark"
	KeyFilter     Key = "filter"
	KeyBackspace  Key = "backspace"
	KeyEscape     Key = "escape"
	KeySubmit     Key = "submit"
	KeySave       Key = "save"
)

type Action interface{ isAction() }

type KeyPress struct{ Key Key }
type TextInput struct{ Text string }
type Resize struct{ Width, Height int }
type BeginListing struct {
	Pane       PaneID
	Generation uint64
	Location   domain.Location
}
type ListingPage struct {
	Pane       PaneID
	Generation uint64
	Entries    []domain.Entry
	Done       bool
	Partial    bool
}
type ListingFailed struct {
	Pane       PaneID
	Generation uint64
	Message    string
}
type SetFilter struct {
	Pane  PaneID
	Query string
}
type BeginPreview struct {
	Generation uint64
	Location   domain.Location
}
type PreviewChunk struct {
	Generation uint64
	Data       []byte
	Done       bool
	Truncated  bool
	Message    string
}
type AuthChallengeReceived struct {
	ChallengeID string
	Endpoint    string
	Prompt      string
	Kind        string
}
type PaneConnected struct {
	Pane     PaneID
	Endpoint domain.Endpoint
	Location domain.Location
}
type WorkspaceSaveResult struct {
	Name    string
	Message string
}

func (KeyPress) isAction()              {}
func (TextInput) isAction()             {}
func (Resize) isAction()                {}
func (BeginListing) isAction()          {}
func (ListingPage) isAction()           {}
func (ListingFailed) isAction()         {}
func (SetFilter) isAction()             {}
func (BeginPreview) isAction()          {}
func (PreviewChunk) isAction()          {}
func (AuthChallengeReceived) isAction() {}
func (PaneConnected) isAction()         {}
func (WorkspaceSaveResult) isAction()   {}

func parentLocation(location domain.Location) (domain.Location, bool) {
	parent := path.Dir(string(location.Path))
	if parent == "." || parent == string(location.Path) {
		return domain.Location{}, false
	}
	return domain.Location{EndpointID: location.EndpointID, Path: domain.CanonicalPath(parent)}, true
}
