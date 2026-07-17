package search

import (
	"bytes"
	"context"
	"errors"
	"io"
	"reflect"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/TyrantLucifer/awesome-mac-sftp/internal/domain"
	providerapi "github.com/TyrantLucifer/awesome-mac-sftp/internal/provider"
)

type PatternType string

const PatternLiteral PatternType = "literal"

type BinaryPolicy string

const BinarySkip BinaryPolicy = "skip"

type ContentOptions struct {
	Pattern          string
	PatternType      PatternType
	CaseSensitive    bool
	IncludeHidden    bool
	Binary           BinaryPolicy
	ContextLines     uint32
	FileNameContains string
}

type ContentBudget struct {
	PageItems         uint32
	EventBuffer       uint32
	MaxDepth          uint32
	MaxEntries        uint64
	MaxFiles          uint64
	MaxResults        uint64
	MaxMatchesPerFile uint64
	MaxFileBytes      uint64
	MaxReadBytes      uint64
	MaxSnippetBytes   uint32
	MaxOutputBytes    uint64
	MaxDuration       time.Duration
}

type ContentIdentity struct {
	RequestID          domain.RequestID
	EndpointID         domain.EndpointID
	SessionID          domain.SessionID
	EndpointGeneration uint64
	UIGeneration       uint64
	Scope              domain.Location
	Options            ContentOptions
	Budget             ContentBudget
}

type ContentRequest struct{ Identity ContentIdentity }

type ContentEventKind string

const (
	ContentEventResult   ContentEventKind = "result"
	ContentEventProblem  ContentEventKind = "problem"
	ContentEventTerminal ContentEventKind = "terminal"
)

type ContentResult struct {
	Location     domain.Location
	RelativePath string
	Line         uint64
	Offset       uint64
	Snippet      string
}

type ContentProblem struct {
	Location domain.Location
	Code     domain.Code
	Reason   StopReason
}

type ContentTerminal struct {
	Status      Status
	StopReason  StopReason
	Entries     uint64
	Files       uint64
	Results     uint64
	BytesRead   uint64
	OutputBytes uint64
}

type ContentEvent struct {
	Identity ContentIdentity
	Kind     ContentEventKind
	Result   ContentResult
	Problem  ContentProblem
	Terminal ContentTerminal
}

func ContentEventCurrent(expected ContentIdentity, event ContentEvent) bool {
	return expected == event.Identity
}

func StartContent(ctx context.Context, implementation providerapi.Provider, request ContentRequest) (<-chan ContentEvent, error) {
	if ctx == nil || implementation == nil {
		return nil, errors.New("start content search: context and provider are required")
	}
	if err := validateContentRequest(implementation.Descriptor().ID, request); err != nil {
		return nil, err
	}
	snapshot, err := implementation.Snapshot(ctx)
	if err != nil {
		return nil, err
	}
	if !contentSnapshotMatches(request.Identity, snapshot) {
		return nil, errors.New("start content search: endpoint session or generation changed")
	}
	root, err := implementation.Stat(ctx, providerapi.StatRequest{Location: request.Identity.Scope})
	if err != nil {
		return nil, err
	}
	if root.Kind != domain.EntryDirectory {
		return nil, errors.New("start content search: scope is not a directory")
	}
	events := make(chan ContentEvent, int(request.Identity.Budget.EventBuffer))
	go runContent(ctx, implementation, request.Identity, events)
	return events, nil
}

// ValidateContentRequest applies the transport-neutral content-search
// identity, option, and hard-budget contract without starting an operation.
func ValidateContentRequest(endpointID domain.EndpointID, request ContentRequest) error {
	return validateContentRequest(endpointID, request)
}

func validateContentRequest(endpointID domain.EndpointID, request ContentRequest) error {
	i := request.Identity
	if _, err := domain.ParseRequestID(string(i.RequestID)); err != nil || i.EndpointID != endpointID || i.Scope.EndpointID != endpointID || i.SessionID == "" || i.EndpointGeneration == 0 || i.UIGeneration == 0 {
		return errors.New("start content search: identity is invalid")
	}
	if i.Scope.Path == "" || i.Scope.Path[0] != '/' {
		return errors.New("start content search: scope is invalid")
	}
	if i.Options.Pattern == "" || len(i.Options.Pattern) > maxPatternBytes || !utf8.ValidString(i.Options.Pattern) || strings.IndexByte(i.Options.Pattern, 0) >= 0 || i.Options.PatternType != PatternLiteral || i.Options.Binary != BinarySkip || i.Options.ContextLines > 3 || strings.IndexByte(i.Options.FileNameContains, 0) >= 0 {
		return errors.New("start content search: options are invalid or unsupported by Level 0")
	}
	b := i.Budget
	if b.PageItems == 0 || b.PageItems > maxPageItems || b.EventBuffer == 0 || b.EventBuffer > maxEventBuffer || b.MaxDepth == 0 || b.MaxDepth > maxSearchDepth || b.MaxEntries == 0 || b.MaxEntries > maxSearchEntries || b.MaxFiles == 0 || b.MaxFiles > 1_000_000 || b.MaxResults == 0 || b.MaxResults > maxSearchResults || b.MaxMatchesPerFile == 0 || b.MaxMatchesPerFile > 100_000 || b.MaxFileBytes == 0 || b.MaxFileBytes > 64<<20 || b.MaxReadBytes == 0 || b.MaxReadBytes > maxSearchOutputBytes || b.MaxSnippetBytes == 0 || b.MaxSnippetBytes > 4096 || b.MaxOutputBytes == 0 || b.MaxOutputBytes > maxSearchOutputBytes || b.MaxDuration <= 0 || b.MaxDuration > maxSearchDuration {
		return errors.New("start content search: budget is outside hard limits")
	}
	return nil
}

func contentSnapshotMatches(identity ContentIdentity, snapshot domain.EndpointSnapshot) bool {
	return snapshot.EndpointID == identity.EndpointID && snapshot.SessionID == identity.SessionID && snapshot.Capabilities.Revision.SessionID == identity.SessionID && snapshot.Capabilities.Revision.Generation == identity.EndpointGeneration
}

type contentRunState struct {
	identity      ContentIdentity
	events        chan<- ContentEvent
	entries       uint64
	files         uint64
	results       uint64
	bytesRead     uint64
	outputBytes   uint64
	partialReason StopReason
	visited       map[domain.CanonicalPath]struct{}
}

func runContent(parent context.Context, implementation providerapi.Provider, identity ContentIdentity, events chan ContentEvent) {
	defer close(events)
	ctx, cancel := context.WithTimeout(parent, identity.Budget.MaxDuration)
	defer cancel()
	state := &contentRunState{identity: identity, events: events, partialReason: StopNone, visited: map[domain.CanonicalPath]struct{}{identity.Scope.Path: {}}}
	_, reason := walkContent(ctx, implementation, identity.Scope, 0, state)
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
	events <- ContentEvent{Identity: identity, Kind: ContentEventTerminal, Terminal: ContentTerminal{Status: status, StopReason: reason, Entries: state.entries, Files: state.files, Results: state.results, BytesRead: state.bytesRead, OutputBytes: state.outputBytes}}
}

func walkContent(ctx context.Context, implementation providerapi.Provider, directory domain.Location, depth uint32, state *contentRunState) (walkSignal, StopReason) {
	if reason := contextStopReason(ctx); reason != StopNone {
		return walkStop, reason
	}
	snapshot, err := implementation.Snapshot(ctx)
	if err != nil {
		return walkStop, contextOrProviderReason(ctx)
	}
	if !contentSnapshotMatches(state.identity, snapshot) {
		return walkStop, StopGenerationChanged
	}
	var cursor providerapi.PageCursor
	for {
		request := providerapi.ListRequest{Location: directory, Cursor: cursor, Limit: state.identity.Budget.PageItems}
		page, err := implementation.List(ctx, request)
		if err != nil {
			if domain.IsCode(err, domain.CodePermissionDenied) {
				state.markPartial(StopPermissionDenied)
				state.emitProblem(directory, domain.CodePermissionDenied, StopPermissionDenied)
				return walkContinue, StopNone
			}
			return walkStop, contextOrProviderReason(ctx)
		}
		if err := providerapi.ValidateListPage(request, page); err != nil {
			return walkStop, StopIntegrityFailed
		}
		for _, item := range page.Entries {
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
			if item.Kind == domain.EntryFile && (state.identity.Options.FileNameContains == "" || strings.Contains(strings.ToLower(item.Name), strings.ToLower(state.identity.Options.FileNameContains))) {
				if state.files >= state.identity.Budget.MaxFiles {
					return walkStop, StopFileLimit
				}
				state.files++
				if reason := state.scanFile(ctx, implementation, item, relative); reason != StopNone {
					if reason == StopPermissionDenied || reason == StopBinarySkipped || reason == StopEncodingInvalid || reason == StopFileByteLimit || reason == StopFileChanged || reason == StopFileResultLimit {
						state.markPartial(reason)
						continue
					}
					return walkStop, reason
				}
			}
			if item.Kind != domain.EntryDirectory {
				continue
			}
			if depth+1 >= state.identity.Budget.MaxDepth {
				state.markPartial(StopDepthLimit)
				continue
			}
			if _, seen := state.visited[item.Location.Path]; seen {
				state.markPartial(StopIntegrityFailed)
				continue
			}
			state.visited[item.Location.Path] = struct{}{}
			if signal, reason := walkContent(ctx, implementation, item.Location, depth+1, state); signal == walkStop {
				return signal, reason
			}
		}
		if page.Done {
			return walkContinue, StopNone
		}
		cursor = page.NextCursor
	}
}

func (state *contentRunState) scanFile(ctx context.Context, implementation providerapi.Provider, entry domain.Entry, relative string) StopReason {
	limit := state.identity.Budget.MaxFileBytes
	totalLimited := limit > state.identity.Budget.MaxReadBytes-state.bytesRead
	if totalLimited {
		limit = state.identity.Budget.MaxReadBytes - state.bytesRead
	}
	if limit == 0 {
		return StopByteLimit
	}
	readLimit := int64(limit)
	var expected *domain.Fingerprint
	if entry.Fingerprint.Strength() != domain.FingerprintWeak {
		copy := entry.Fingerprint
		expected = &copy
	}
	handle, err := implementation.OpenRead(ctx, providerapi.OpenReadRequest{Location: entry.Location, Limit: &readLimit, ExpectedFingerprint: expected})
	if err != nil {
		if domain.IsCode(err, domain.CodePermissionDenied) {
			state.emitProblem(entry.Location, domain.CodePermissionDenied, StopPermissionDenied)
			return StopPermissionDenied
		}
		return contextOrProviderReason(ctx)
	}
	defer handle.Close(context.Background())
	data := make([]byte, 0, int(limit))
	buffer := make([]byte, min(32*1024, int(limit)))
	for uint64(len(data)) < limit {
		n, readErr := handle.Read(ctx, buffer)
		if n > 0 {
			data = append(data, buffer[:n]...)
			state.bytesRead += uint64(n)
		}
		if readErr != nil {
			if errors.Is(readErr, io.EOF) {
				break
			}
			return contextOrProviderReason(ctx)
		}
		if n == 0 {
			return StopProviderError
		}
	}
	if bytes.IndexByte(data, 0) >= 0 {
		state.emitProblem(entry.Location, domain.CodeUnsupported, StopBinarySkipped)
		return StopBinarySkipped
	}
	if !utf8.Valid(data) {
		state.emitProblem(entry.Location, domain.CodeInvalidArgument, StopEncodingInvalid)
		return StopEncodingInvalid
	}
	if reason := state.emitContentMatches(ctx, entry.Location, relative, data); reason != StopNone {
		return reason
	}
	current, err := implementation.Stat(ctx, providerapi.StatRequest{Location: entry.Location})
	if err != nil {
		return contextOrProviderReason(ctx)
	}
	if !reflect.DeepEqual(current.Fingerprint, handle.Info().Fingerprint) {
		state.emitProblem(entry.Location, domain.CodeConflict, StopFileChanged)
		return StopFileChanged
	}
	if totalLimited {
		return StopByteLimit
	}
	if entry.Metadata.Size != nil && *entry.Metadata.Size > uint64(len(data)) || entry.Metadata.Size == nil && uint64(len(data)) == state.identity.Budget.MaxFileBytes {
		state.emitProblem(entry.Location, domain.CodeResourceExhausted, StopFileByteLimit)
		return StopFileByteLimit
	}
	return StopNone
}

func (state *contentRunState) emitContentMatches(ctx context.Context, location domain.Location, relative string, data []byte) StopReason {
	haystack := data
	needle := []byte(state.identity.Options.Pattern)
	if !state.identity.Options.CaseSensitive {
		haystack = asciiLower(data)
		needle = asciiLower(needle)
	}
	var perFile uint64
	for start := 0; start <= len(haystack)-len(needle); {
		index := bytes.Index(haystack[start:], needle)
		if index < 0 {
			break
		}
		offset := start + index
		lineStart := bytes.LastIndexByte(data[:offset], '\n') + 1
		lineEndRelative := bytes.IndexByte(data[offset:], '\n')
		lineEnd := len(data)
		if lineEndRelative >= 0 {
			lineEnd = offset + lineEndRelative
		}
		snippet := data[lineStart:lineEnd]
		if len(snippet) > int(state.identity.Budget.MaxSnippetBytes) {
			snippet = snippet[:state.identity.Budget.MaxSnippetBytes]
		}
		resultBytes := uint64(len(relative) + len(snippet) + 32)
		if state.results >= state.identity.Budget.MaxResults {
			return StopResultLimit
		}
		if resultBytes > state.identity.Budget.MaxOutputBytes-state.outputBytes {
			return StopByteLimit
		}
		result := ContentResult{Location: location, RelativePath: relative, Line: uint64(bytes.Count(data[:offset], []byte{'\n'})) + 1, Offset: uint64(offset), Snippet: string(snippet)}
		select {
		case state.events <- ContentEvent{Identity: state.identity, Kind: ContentEventResult, Result: result}:
			state.results++
			state.outputBytes += resultBytes
			perFile++
		case <-ctx.Done():
			return contextStopReason(ctx)
		}
		if perFile >= state.identity.Budget.MaxMatchesPerFile {
			state.emitProblem(location, domain.CodeResourceExhausted, StopFileResultLimit)
			return StopFileResultLimit
		}
		start = offset + max(1, len(needle))
	}
	return StopNone
}

func asciiLower(value []byte) []byte {
	result := append([]byte(nil), value...)
	for index, current := range result {
		if current >= 'A' && current <= 'Z' {
			result[index] = current + ('a' - 'A')
		}
	}
	return result
}

func (state *contentRunState) markPartial(reason StopReason) {
	if state.partialReason == StopNone {
		state.partialReason = reason
	}
}

func (state *contentRunState) emitProblem(location domain.Location, code domain.Code, reason StopReason) {
	select {
	case state.events <- ContentEvent{Identity: state.identity, Kind: ContentEventProblem, Problem: ContentProblem{Location: location, Code: code, Reason: reason}}:
	default:
	}
}

func contextOrProviderReason(ctx context.Context) StopReason {
	if reason := contextStopReason(ctx); reason != StopNone {
		return reason
	}
	return StopProviderError
}
