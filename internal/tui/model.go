package tui

import (
	"path"
	"sort"
	"strings"

	"github.com/TyrantLucifer/awesome-mac-sftp/internal/domain"
	"github.com/TyrantLucifer/awesome-mac-sftp/internal/job"
	"github.com/TyrantLucifer/awesome-mac-sftp/internal/state/jobstore"
	"github.com/TyrantLucifer/awesome-mac-sftp/internal/transfer"
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
	ModePath       Mode = "path"
	ModeEndpoint   Mode = "endpoint"
)

type SortKey string

const (
	SortName     SortKey = "name"
	SortSize     SortKey = "size"
	SortModified SortKey = "modified"
	SortKind     SortKey = "kind"
)

type SortState struct {
	Key        SortKey
	Descending bool
}

const authAnswerByteLimit = 4096

type ListingState struct {
	Generation uint64
	Loading    bool
	Complete   bool
	Partial    bool
	Message    string

	pendingLocation             domain.Location
	pendingEndpoint             domain.Endpoint
	pendingConnection           domain.ConnectionState
	pendingCapabilityGeneration uint64
	pendingCapabilities         domain.CapabilitySnapshot
	commitEndpoint              bool
	hasPage                     bool
	cursorAnchor                domain.Location
	hasCursorAnchor             bool
}

type PaneState struct {
	Endpoint             domain.Endpoint
	Location             domain.Location
	Entries              []domain.Entry
	Cursor               int
	Filter               string
	Sort                 SortState
	ShowHidden           bool
	Listing              ListingState
	Connection           domain.ConnectionState
	CapabilityGeneration uint64
	Capabilities         domain.CapabilitySnapshot

	visible          []int
	marks            map[domain.Location]struct{}
	visualAnchor     domain.Location
	visualAnchorView int
	hasVisualAnchor  bool
}

func NewPaneState(endpoint domain.Endpoint, location domain.Location) PaneState {
	return PaneState{
		Endpoint:   endpoint,
		Location:   location,
		Sort:       SortState{Key: SortName},
		Connection: domain.StateReady,
		marks:      make(map[domain.Location]struct{}),
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
		if (!p.ShowHidden && strings.HasPrefix(p.Entries[index].Name, ".")) || query != "" && !strings.Contains(strings.ToLower(p.Entries[index].Name), query) {
			continue
		}
		p.visible = append(p.visible, index)
	}
	sort.SliceStable(p.visible, func(left, right int) bool {
		return p.entryLess(p.Entries[p.visible[left]], p.Entries[p.visible[right]])
	})
	p.clampCursor()
	p.rebindVisualAnchor()
}

func (p *PaneState) appendEntries(entries []domain.Entry) {
	p.Entries = append(p.Entries, entries...)
	p.rebuildVisible()
}

func (p PaneState) entryLess(left, right domain.Entry) bool {
	if (left.Kind == domain.EntryDirectory) != (right.Kind == domain.EntryDirectory) {
		return left.Kind == domain.EntryDirectory
	}
	comparison, missingOrder := compareEntries(left, right, p.Sort.Key)
	if missingOrder {
		return comparison < 0
	}
	if comparison == 0 {
		comparison = strings.Compare(strings.ToLower(left.Name), strings.ToLower(right.Name))
	}
	if comparison == 0 {
		comparison = strings.Compare(left.Name, right.Name)
	}
	if p.Sort.Descending {
		return comparison > 0
	}
	return comparison < 0
}

func compareEntries(left, right domain.Entry, key SortKey) (int, bool) {
	switch key {
	case SortSize:
		return compareOptionalUint64(left.Metadata.Size, right.Metadata.Size)
	case SortModified:
		if left.Metadata.ModifiedAt == nil || right.Metadata.ModifiedAt == nil {
			if left.Metadata.ModifiedAt == nil && right.Metadata.ModifiedAt == nil {
				return 0, false
			}
			return compareKnown(left.Metadata.ModifiedAt != nil, right.Metadata.ModifiedAt != nil), true
		}
		return left.Metadata.ModifiedAt.Compare(*right.Metadata.ModifiedAt), false
	case SortKind:
		return strings.Compare(string(left.Kind), string(right.Kind)), false
	default:
		return strings.Compare(strings.ToLower(left.Name), strings.ToLower(right.Name)), false
	}
}

func compareOptionalUint64(left, right *uint64) (int, bool) {
	if left == nil || right == nil {
		if left == nil && right == nil {
			return 0, false
		}
		return compareKnown(left != nil, right != nil), true
	}
	if *left < *right {
		return -1, false
	}
	if *left > *right {
		return 1, false
	}
	return 0, false
}

func compareKnown(left, right bool) int {
	if left == right {
		return 0
	}
	if left {
		return -1
	}
	return 1
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
	lines := strings.Split(string(p.Data), "\n")
	for index, line := range lines {
		if index < len(lines)-1 {
			line = strings.TrimSuffix(line, "\r")
		}
		lines[index] = SanitizeTerminalText(line)
	}
	return strings.Join(lines, "\n")
}

type Model struct {
	Panes     [2]PaneState
	Active    PaneID
	Mode      Mode
	Count     int
	Preview   PreviewState
	Auth      AuthState
	Clipboard ClipboardState
	Jobs      []transfer.JobView
	ShowJobs  bool
	JobCursor int
	Notice    string

	workspaceName []rune
	pathInput     []rune
	endpointInput []rune
	Width         int
	Height        int
}

type ClipboardState struct {
	Kind      transfer.ClipboardKind
	Reference transfer.FileRef
	Ready     bool
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
	IntentList               IntentKind = "list"
	IntentPreview            IntentKind = "preview"
	IntentPreviewCancel      IntentKind = "preview_cancel"
	IntentAuthResolve        IntentKind = "auth_resolve"
	IntentWorkspaceSave      IntentKind = "workspace_save"
	IntentConnectEndpoint    IntentKind = "connect_endpoint"
	IntentReleaseEndpoint    IntentKind = "release_endpoint"
	IntentTransferCapture    IntentKind = "transfer_capture"
	IntentCreateCopyJob      IntentKind = "create_copy_job"
	IntentJobList            IntentKind = "job_list"
	IntentJobPause           IntentKind = "job_pause"
	IntentJobResume          IntentKind = "job_resume"
	IntentJobCancel          IntentKind = "job_cancel"
	IntentJobResolveConflict IntentKind = "job_resolve_conflict"
)

const PreviewByteLimit = 64 * 1024

type Intent struct {
	Kind                 IntentKind
	Pane                 PaneID
	Location             domain.Location
	Limit                int
	ChallengeID          string
	Answer               []byte
	Cancel               bool
	Name                 string
	Endpoint             domain.Endpoint
	EndpointID           domain.EndpointID
	Connection           domain.ConnectionState
	CapabilityGeneration uint64
	Capabilities         domain.CapabilitySnapshot
	CommitEndpoint       bool
	Clipboard            transfer.ClipboardKind
	Source               transfer.FileRef
	JobID                domain.JobID
	Resolution           transfer.ConflictPolicy
	ApplyAll             bool
}

type Key string

const (
	KeyTab                   Key = "tab"
	KeyParent                Key = "parent"
	KeyDown                  Key = "down"
	KeyUp                    Key = "up"
	KeyOpen                  Key = "open"
	KeyVisual                Key = "visual"
	KeyVisualLine            Key = "visual_line"
	KeyMark                  Key = "mark"
	KeyFilter                Key = "filter"
	KeyBackspace             Key = "backspace"
	KeyEscape                Key = "escape"
	KeySubmit                Key = "submit"
	KeySave                  Key = "save"
	KeySort                  Key = "sort"
	KeyToggleHidden          Key = "toggle_hidden"
	KeyRefresh               Key = "refresh"
	KeyPath                  Key = "path"
	KeyEndpoint              Key = "endpoint"
	KeyCopy                  Key = "copy"
	KeyCut                   Key = "cut"
	KeyPaste                 Key = "paste"
	KeyJobs                  Key = "jobs"
	KeyJobPause              Key = "job_pause"
	KeyJobResume             Key = "job_resume"
	KeyJobCancel             Key = "job_cancel"
	KeyConflictOverwrite     Key = "conflict_overwrite"
	KeyConflictSkip          Key = "conflict_skip"
	KeyConflictAutoRename    Key = "conflict_auto_rename"
	KeyConflictOverwriteAll  Key = "conflict_overwrite_all"
	KeyConflictSkipAll       Key = "conflict_skip_all"
	KeyConflictAutoRenameAll Key = "conflict_auto_rename_all"
)

type Action interface{ isAction() }

type KeyPress struct{ Key Key }
type CountDigit struct{ Digit uint8 }
type TextInput struct{ Text string }
type Resize struct{ Width, Height int }
type BeginListing struct {
	Pane                 PaneID
	Generation           uint64
	Location             domain.Location
	Endpoint             domain.Endpoint
	Connection           domain.ConnectionState
	CapabilityGeneration uint64
	Capabilities         domain.CapabilitySnapshot
	CommitEndpoint       bool
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
	Code       domain.Code
	Retry      domain.RetryKind
	DaemonLost bool
	Location   domain.Location
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
	Pane                 PaneID
	Endpoint             domain.Endpoint
	Location             domain.Location
	State                domain.ConnectionState
	CapabilityGeneration uint64
	Capabilities         domain.CapabilitySnapshot
	PreserveCommitted    bool
}
type PaneConnectionChanged struct {
	Pane    PaneID
	State   domain.ConnectionState
	Message string
}
type WorkspaceSaveResult struct {
	Name    string
	Message string
}
type ClipboardCaptured struct {
	Clipboard transfer.ClipboardKind
	Reference transfer.FileRef
	Message   string
}
type JobCreated struct {
	JobID   domain.JobID
	State   job.State
	Message string
}
type JobsLoaded struct {
	Jobs    []transfer.JobView
	Message string
}
type JobUpdated struct {
	Snapshot jobstore.Snapshot
	Message  string
}

func (KeyPress) isAction()              {}
func (CountDigit) isAction()            {}
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
func (PaneConnectionChanged) isAction() {}
func (WorkspaceSaveResult) isAction()   {}
func (ClipboardCaptured) isAction()     {}
func (JobCreated) isAction()            {}
func (JobsLoaded) isAction()            {}
func (JobUpdated) isAction()            {}

func parentLocation(location domain.Location) (domain.Location, bool) {
	parent := path.Dir(string(location.Path))
	if parent == "." || parent == string(location.Path) {
		return domain.Location{}, false
	}
	return domain.Location{EndpointID: location.EndpointID, Path: domain.CanonicalPath(parent)}, true
}
