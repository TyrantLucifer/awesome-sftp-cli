// Package search implements transport-neutral, bounded search contracts.
package search

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/TyrantLucifer/awesome-sftp-cli/internal/domain"
	providerapi "github.com/TyrantLucifer/awesome-sftp-cli/internal/provider"
)

const (
	maxPatternBytes      = 4096
	maxPageItems         = 4096
	maxEventBuffer       = 4096
	maxConcurrentLists   = 64
	maxSearchDepth       = 256
	maxSearchEntries     = 10_000_000
	maxSearchResults     = 1_000_000
	maxSearchOutputBytes = 512 << 20
	maxSearchDuration    = 24 * time.Hour
)

type MatchTarget string

const (
	MatchName         MatchTarget = "name"
	MatchRelativePath MatchTarget = "relative_path"
)

type SymlinkPolicy string

const SymlinkNever SymlinkPolicy = "never"

type IgnorePolicy string

const (
	IgnoreNone    IgnorePolicy = "none"
	IgnoreDefault IgnorePolicy = "default"
)

type TypeFilter struct {
	Files       bool
	Directories bool
	Symlinks    bool
}

type Options struct {
	Pattern       string
	Target        MatchTarget
	CaseSensitive bool
	IncludeHidden bool
	Symlinks      SymlinkPolicy
	Ignore        IgnorePolicy
	Types         TypeFilter
}

type Budget struct {
	PageItems       uint32
	EventBuffer     uint32
	ConcurrentLists uint32
	MaxDepth        uint32
	MaxEntries      uint64
	MaxResults      uint64
	MaxOutputBytes  uint64
	MaxDuration     time.Duration
}

// Identity is copied into every event. Its value fields deliberately make the
// request scope, options and hard budgets part of stale-result rejection.
type Identity struct {
	RequestID          domain.RequestID
	EndpointID         domain.EndpointID
	SessionID          domain.SessionID
	EndpointGeneration uint64
	UIGeneration       uint64
	Scope              domain.Location
	Options            Options
	Budget             Budget
}

type Request struct {
	Identity Identity
}

type EventKind string

const (
	EventResult   EventKind = "result"
	EventProblem  EventKind = "problem"
	EventTerminal EventKind = "terminal"
)

type Result struct {
	Location     domain.Location
	RelativePath string
	Entry        domain.Entry
}

type Problem struct {
	Location domain.Location
	Code     domain.Code
}

type Status string

const (
	StatusComplete Status = "complete"
	StatusPartial  Status = "partial_results"
	StatusCanceled Status = "canceled"
)

type StopReason string

const (
	StopNone              StopReason = "none"
	StopCanceled          StopReason = "canceled"
	StopPermissionDenied  StopReason = "permission_denied"
	StopGenerationChanged StopReason = "generation_changed"
	StopDepthLimit        StopReason = "depth_limit"
	StopEntryLimit        StopReason = "entry_limit"
	StopResultLimit       StopReason = "result_limit"
	StopByteLimit         StopReason = "byte_limit"
	StopTimeLimit         StopReason = "time_limit"
	StopProviderError     StopReason = "provider_error"
	StopIntegrityFailed   StopReason = "integrity_failed"
	StopBinarySkipped     StopReason = "binary_skipped"
	StopEncodingInvalid   StopReason = "encoding_invalid"
	StopFileByteLimit     StopReason = "file_byte_limit"
	StopFileChanged       StopReason = "file_changed"
	StopFileLimit         StopReason = "file_limit"
	StopFileResultLimit   StopReason = "file_result_limit"
)

type Terminal struct {
	Status      Status
	StopReason  StopReason
	Entries     uint64
	Results     uint64
	OutputBytes uint64
	ListCalls   uint64
}

type Event struct {
	Identity Identity
	Kind     EventKind
	Result   Result
	Problem  Problem
	Terminal Terminal
}

// EventCurrent rejects any result from a different endpoint session, provider
// generation, UI generation, request, scope, option set, or budget.
func EventCurrent(expected Identity, event Event) bool {
	return expected == event.Identity
}

// StartFilename begins the Level 0 recursive filename search. Its only remote
// dependency is the standard read-only Provider; it cannot probe, install, or
// invoke a Helper.
func StartFilename(ctx context.Context, implementation providerapi.Provider, request Request) (<-chan Event, error) {
	if ctx == nil {
		return nil, errors.New("start filename search: context is required")
	}
	if implementation == nil {
		return nil, errors.New("start filename search: provider is required")
	}
	if err := validateRequest(implementation.Descriptor().ID, request); err != nil {
		return nil, err
	}
	snapshot, err := implementation.Snapshot(ctx)
	if err != nil {
		return nil, fmt.Errorf("start filename search: snapshot: %w", err)
	}
	if !snapshotMatches(request.Identity, snapshot) {
		return nil, errors.New("start filename search: endpoint session or generation changed")
	}
	root, err := implementation.Stat(ctx, providerapi.StatRequest{Location: request.Identity.Scope})
	if err != nil {
		return nil, err
	}
	if root.Kind != domain.EntryDirectory {
		return nil, errors.New("start filename search: scope is not a directory")
	}

	events := make(chan Event, int(request.Identity.Budget.EventBuffer))
	go runFilename(ctx, implementation, request.Identity, events)
	return events, nil
}

// ValidateFilenameRequest applies the transport-neutral filename-search
// identity, option, and hard-budget contract without starting an operation.
func ValidateFilenameRequest(endpointID domain.EndpointID, request Request) error {
	return validateRequest(endpointID, request)
}

func validateRequest(endpointID domain.EndpointID, request Request) error {
	identity := request.Identity
	if _, err := domain.ParseRequestID(string(identity.RequestID)); err != nil {
		return errors.New("start filename search: request id is invalid")
	}
	if identity.EndpointID == "" || identity.EndpointID != endpointID || identity.Scope.EndpointID != identity.EndpointID {
		return errors.New("start filename search: endpoint identity is invalid")
	}
	if identity.SessionID == "" || identity.EndpointGeneration == 0 || identity.UIGeneration == 0 {
		return errors.New("start filename search: session or generation is invalid")
	}
	if identity.Scope.Path == "" || identity.Scope.Path[0] != '/' || strings.IndexByte(string(identity.Scope.Path), 0) >= 0 {
		return errors.New("start filename search: scope is invalid")
	}
	options := identity.Options
	if options.Pattern == "" || len(options.Pattern) > maxPatternBytes || !utf8.ValidString(options.Pattern) || strings.IndexByte(options.Pattern, 0) >= 0 {
		return errors.New("start filename search: pattern is invalid")
	}
	if options.Target != MatchName && options.Target != MatchRelativePath {
		return errors.New("start filename search: match target is invalid")
	}
	if options.Symlinks != SymlinkNever {
		return errors.New("start filename search: Level 0 only supports no-follow symlinks")
	}
	if options.Ignore != IgnoreNone {
		return errors.New("start filename search: Level 0 only supports the explicit no-ignore policy")
	}
	if !options.Types.Files && !options.Types.Directories && !options.Types.Symlinks {
		return errors.New("start filename search: at least one result type is required")
	}
	budget := identity.Budget
	if budget.PageItems == 0 || budget.PageItems > maxPageItems ||
		budget.EventBuffer == 0 || budget.EventBuffer > maxEventBuffer ||
		budget.ConcurrentLists == 0 || budget.ConcurrentLists > maxConcurrentLists ||
		budget.MaxDepth == 0 || budget.MaxDepth > maxSearchDepth ||
		budget.MaxEntries == 0 || budget.MaxEntries > maxSearchEntries ||
		budget.MaxResults == 0 || budget.MaxResults > maxSearchResults ||
		budget.MaxOutputBytes == 0 || budget.MaxOutputBytes > maxSearchOutputBytes ||
		budget.MaxDuration <= 0 || budget.MaxDuration > maxSearchDuration {
		return errors.New("start filename search: budget is outside hard limits")
	}
	return nil
}

func snapshotMatches(identity Identity, snapshot domain.EndpointSnapshot) bool {
	return snapshot.EndpointID == identity.EndpointID &&
		snapshot.SessionID == identity.SessionID &&
		snapshot.Capabilities.Revision.SessionID == identity.SessionID &&
		snapshot.Capabilities.Revision.Generation == identity.EndpointGeneration
}

type runState struct {
	identity      Identity
	events        chan<- Event
	entries       uint64
	results       uint64
	outputBytes   uint64
	listCalls     uint64
	partialReason StopReason
	visited       map[domain.CanonicalPath]struct{}
}

type walkSignal uint8

const (
	walkContinue walkSignal = iota
	walkStop
)

func runFilename(parent context.Context, implementation providerapi.Provider, identity Identity, events chan Event) {
	defer close(events)
	ctx, cancel := context.WithTimeout(parent, identity.Budget.MaxDuration)
	defer cancel()
	state := &runState{
		identity:      identity,
		events:        events,
		partialReason: StopNone,
		visited:       make(map[domain.CanonicalPath]struct{}),
	}
	state.visited[identity.Scope.Path] = struct{}{}
	signal, reason := walkFilename(ctx, implementation, identity.Scope, 0, state)
	_ = signal
	if reason == StopNone {
		reason = state.partialReason
	}
	status := StatusComplete
	if reason != StopNone {
		status = StatusPartial
	}
	if reason == StopCanceled {
		status = StatusCanceled
	}
	state.events <- Event{
		Identity: identity,
		Kind:     EventTerminal,
		Terminal: Terminal{
			Status:      status,
			StopReason:  reason,
			Entries:     state.entries,
			Results:     state.results,
			OutputBytes: state.outputBytes,
			ListCalls:   state.listCalls,
		},
	}
}

func walkFilename(
	ctx context.Context,
	implementation providerapi.Provider,
	directory domain.Location,
	depth uint32,
	state *runState,
) (walkSignal, StopReason) {
	if reason := contextStopReason(ctx); reason != StopNone {
		return walkStop, reason
	}
	snapshot, err := implementation.Snapshot(ctx)
	if err != nil {
		if reason := contextStopReason(ctx); reason != StopNone {
			return walkStop, reason
		}
		return walkStop, StopProviderError
	}
	if !snapshotMatches(state.identity, snapshot) {
		return walkStop, StopGenerationChanged
	}

	var cursor providerapi.PageCursor
	for {
		request := providerapi.ListRequest{
			Location: directory,
			Cursor:   cursor,
			Limit:    state.identity.Budget.PageItems,
		}
		state.listCalls++
		page, err := implementation.List(ctx, request)
		if err != nil {
			if reason := contextStopReason(ctx); reason != StopNone {
				return walkStop, reason
			}
			if domain.IsCode(err, domain.CodePermissionDenied) {
				state.markPartial(StopPermissionDenied)
				state.emitProblem(directory, domain.CodePermissionDenied)
				return walkContinue, StopNone
			}
			return walkStop, StopProviderError
		}
		if err := providerapi.ValidateListPage(request, page); err != nil {
			return walkStop, StopIntegrityFailed
		}
		for _, item := range page.Entries {
			if reason := contextStopReason(ctx); reason != StopNone {
				return walkStop, reason
			}
			state.entries++
			if state.entries > state.identity.Budget.MaxEntries {
				return walkStop, StopEntryLimit
			}
			relative, err := relativeEntry(state.identity.Scope, directory, item)
			if err != nil {
				return walkStop, StopIntegrityFailed
			}
			if !state.identity.Options.IncludeHidden && hasHiddenComponent(relative) {
				continue
			}
			if resultTypeAllowed(state.identity.Options.Types, item.Kind) && filenameMatches(state.identity.Options, item, relative) {
				resultBytes := uint64(len(relative) + len(item.Name))
				if resultBytes > state.identity.Budget.MaxOutputBytes-state.outputBytes {
					return walkStop, StopByteLimit
				}
				if state.results >= state.identity.Budget.MaxResults {
					return walkStop, StopResultLimit
				}
				result := Result{Location: item.Location, RelativePath: relative, Entry: item}
				select {
				case state.events <- Event{Identity: state.identity, Kind: EventResult, Result: result}:
					state.results++
					state.outputBytes += resultBytes
				case <-ctx.Done():
					return walkStop, contextStopReason(ctx)
				}
				if state.results >= state.identity.Budget.MaxResults {
					return walkStop, StopResultLimit
				}
			}
			if item.Kind != domain.EntryDirectory {
				continue
			}
			itemDepth := depth + 1
			if itemDepth >= state.identity.Budget.MaxDepth {
				state.markPartial(StopDepthLimit)
				continue
			}
			if _, seen := state.visited[item.Location.Path]; seen {
				state.markPartial(StopIntegrityFailed)
				continue
			}
			state.visited[item.Location.Path] = struct{}{}
			signal, reason := walkFilename(ctx, implementation, item.Location, itemDepth, state)
			if signal == walkStop {
				return signal, reason
			}
		}
		if page.Done {
			return walkContinue, StopNone
		}
		cursor = page.NextCursor
	}
}

func (state *runState) markPartial(reason StopReason) {
	if state.partialReason == StopNone {
		state.partialReason = reason
	}
}

func (state *runState) emitProblem(location domain.Location, code domain.Code) {
	select {
	case state.events <- Event{
		Identity: state.identity,
		Kind:     EventProblem,
		Problem:  Problem{Location: location, Code: code},
	}:
	default:
		// Problem details are advisory and bounded by the shared event buffer.
		// The terminal partial reason remains authoritative when the consumer is slow.
	}
}

func contextStopReason(ctx context.Context) StopReason {
	if ctx == nil || ctx.Err() == nil {
		return StopNone
	}
	if errors.Is(ctx.Err(), context.DeadlineExceeded) {
		return StopTimeLimit
	}
	return StopCanceled
}

func relativeEntry(root, directory domain.Location, entry domain.Entry) (string, error) {
	if entry.Location.EndpointID != root.EndpointID || entry.Location.EndpointID != directory.EndpointID {
		return "", errors.New("search entry endpoint mismatch")
	}
	child := string(entry.Location.Path)
	dir := string(directory.Path)
	if child == "" || child[0] != '/' || strings.IndexByte(child, 0) >= 0 || child == dir {
		return "", errors.New("search entry path is invalid")
	}
	prefix := dir
	if prefix != "/" {
		prefix += "/"
	}
	if !strings.HasPrefix(child, prefix) {
		return "", errors.New("search entry is outside its listing directory")
	}
	immediate := strings.TrimPrefix(child, prefix)
	if immediate == "" || strings.Contains(immediate, "/") || immediate == "." || immediate == ".." || entry.Name != immediate {
		return "", errors.New("search entry is not an immediate canonical child")
	}
	rootPath := string(root.Path)
	rootPrefix := rootPath
	if rootPrefix != "/" {
		rootPrefix += "/"
	}
	if !strings.HasPrefix(child, rootPrefix) {
		return "", errors.New("search entry is outside frozen scope")
	}
	return strings.TrimPrefix(child, rootPrefix), nil
}

func hasHiddenComponent(relative string) bool {
	for _, component := range strings.Split(relative, "/") {
		if strings.HasPrefix(component, ".") && component != "." && component != ".." {
			return true
		}
	}
	return false
}

func resultTypeAllowed(filter TypeFilter, kind domain.EntryKind) bool {
	switch kind {
	case domain.EntryFile:
		return filter.Files
	case domain.EntryDirectory:
		return filter.Directories
	case domain.EntrySymlink:
		return filter.Symlinks
	default:
		return false
	}
}

func filenameMatches(options Options, entry domain.Entry, relative string) bool {
	candidate := entry.Name
	if options.Target == MatchRelativePath {
		candidate = relative
	}
	pattern := options.Pattern
	if !options.CaseSensitive {
		candidate = strings.ToLower(candidate)
		pattern = strings.ToLower(pattern)
	}
	return strings.Contains(candidate, pattern)
}
