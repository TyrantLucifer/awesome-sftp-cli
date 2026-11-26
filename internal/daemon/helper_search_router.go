package daemon

import (
	"context"
	"encoding/json"
	"errors"
	"path"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/TyrantLucifer/awesome-sftp-cli/internal/domain"
	helperruntime "github.com/TyrantLucifer/awesome-sftp-cli/internal/helper"
	providerapi "github.com/TyrantLucifer/awesome-sftp-cli/internal/provider"
	"github.com/TyrantLucifer/awesome-sftp-cli/internal/search"
)

func (s *providerSession) startFilenameEvents(ctx context.Context, implementation providerapi.Provider, identity search.Identity) (<-chan search.Event, error) {
	client := s.helperClient(identity.EndpointID, helperruntime.CapabilityFilenameSearch)
	if client != nil {
		events, err := startHelperFilename(ctx, implementation, client, identity)
		if err == nil {
			return events, nil
		}
	}
	return search.StartFilename(ctx, implementation, search.Request{Identity: identity})
}

func (s *providerSession) startContentEvents(ctx context.Context, implementation providerapi.Provider, identity search.ContentIdentity) (<-chan search.ContentEvent, error) {
	client := s.helperClient(identity.EndpointID, helperruntime.CapabilityContentSearch)
	if client != nil {
		events, err := startHelperContent(ctx, implementation, client, identity)
		if err == nil {
			return events, nil
		}
	}
	return search.StartContent(ctx, implementation, search.ContentRequest{Identity: identity})
}

func (s *providerSession) helperClient(endpointID domain.EndpointID, capability helperruntime.CapabilityName) *helperruntime.Client {
	s.mu.Lock()
	client := s.helpers[endpointID]
	s.mu.Unlock()
	if client == nil || client.Level() != 1 || !client.HasCapability(capability) {
		return nil
	}
	return client
}

func startHelperFilename(ctx context.Context, implementation providerapi.Provider, client *helperruntime.Client, identity search.Identity) (<-chan search.Event, error) {
	if err := search.ValidateFilenameRequest(implementation.Descriptor().ID, search.Request{Identity: identity}); err != nil {
		return nil, err
	}
	if err := validateHelperSearchSnapshot(ctx, implementation, identity.EndpointID, identity.SessionID, identity.EndpointGeneration); err != nil {
		return nil, err
	}
	body, err := json.Marshal(helperruntime.FilenameSearchRequest{
		Scope: string(identity.Scope.Path), Pattern: identity.Options.Pattern, Match: string(identity.Options.Target),
		CaseSensitive: identity.Options.CaseSensitive, IncludeHidden: identity.Options.IncludeHidden,
		Ignore: string(identity.Options.Ignore),
		Types:  helperruntime.FilenameTypes{Files: identity.Options.Types.Files, Directories: identity.Options.Types.Directories, Symlinks: identity.Options.Types.Symlinks},
		Budget: helperSearchBudget(identity.Budget.MaxDepth, identity.Budget.MaxEntries, identity.Budget.MaxResults, identity.Budget.MaxOutputBytes, identity.Budget.MaxDuration, identity.Budget.PageItems),
	})
	if err != nil {
		return nil, err
	}
	clientEvents, err := client.Start(ctx, identity.RequestID, helperruntime.CapabilityFilenameSearch, body)
	if err != nil {
		return nil, err
	}
	events := make(chan search.Event, int(identity.Budget.EventBuffer))
	go adaptHelperFilename(ctx, implementation, client, identity, clientEvents, events)
	return events, nil
}

func startHelperContent(ctx context.Context, implementation providerapi.Provider, client *helperruntime.Client, identity search.ContentIdentity) (<-chan search.ContentEvent, error) {
	if err := search.ValidateContentRequest(implementation.Descriptor().ID, search.ContentRequest{Identity: identity}); err != nil {
		return nil, err
	}
	if err := validateHelperSearchSnapshot(ctx, implementation, identity.EndpointID, identity.SessionID, identity.EndpointGeneration); err != nil {
		return nil, err
	}
	body, err := json.Marshal(helperruntime.ContentSearchRequest{
		Scope: string(identity.Scope.Path), Pattern: identity.Options.Pattern, PatternType: string(identity.Options.PatternType),
		CaseSensitive: identity.Options.CaseSensitive, IncludeHidden: identity.Options.IncludeHidden,
		FileNameContains: identity.Options.FileNameContains, BinaryPolicy: string(identity.Options.Binary), ContextLines: identity.Options.ContextLines,
		Budget: helperruntime.ContentOperationBudget{
			OperationSearchBudget: helperSearchBudget(identity.Budget.MaxDepth, identity.Budget.MaxEntries, identity.Budget.MaxResults, identity.Budget.MaxOutputBytes, identity.Budget.MaxDuration, identity.Budget.PageItems),
			MaxFiles:              identity.Budget.MaxFiles, MaxMatchesPerFile: identity.Budget.MaxMatchesPerFile,
			MaxFileBytes: identity.Budget.MaxFileBytes, MaxReadBytes: identity.Budget.MaxReadBytes, MaxSnippetBytes: identity.Budget.MaxSnippetBytes,
		},
	})
	if err != nil {
		return nil, err
	}
	clientEvents, err := client.Start(ctx, identity.RequestID, helperruntime.CapabilityContentSearch, body)
	if err != nil {
		return nil, err
	}
	events := make(chan search.ContentEvent, int(identity.Budget.EventBuffer))
	go adaptHelperContent(ctx, implementation, client, identity, clientEvents, events)
	return events, nil
}

func helperSearchBudget(depth uint32, entries, results, output uint64, duration time.Duration, page uint32) helperruntime.OperationSearchBudget {
	var durationMillis uint64
	if duration > 0 {
		durationMillis = uint64(duration / time.Millisecond)
	}
	return helperruntime.OperationSearchBudget{
		MaxDepth: depth, MaxEntries: entries, MaxResults: results, MaxOutputBytes: output,
		MaxDurationMS: durationMillis, PageEntries: page,
	}
}

func validateHelperSearchSnapshot(ctx context.Context, implementation providerapi.Provider, endpointID domain.EndpointID, sessionID domain.SessionID, generation uint64) error {
	snapshot, err := implementation.Snapshot(ctx)
	if err != nil {
		return err
	}
	if snapshot.EndpointID != endpointID || snapshot.SessionID != sessionID || snapshot.Capabilities.Revision.SessionID != sessionID || snapshot.Capabilities.Revision.Generation != generation {
		return errors.New("start helper search: endpoint session or generation changed")
	}
	return nil
}

type helperStreamAdapter[E any] struct {
	requestID  domain.RequestID
	endpointID domain.EndpointID
	sessionID  domain.SessionID
	generation uint64
	scopePath  domain.CanonicalPath
	onResult   func(json.RawMessage) (E, bool)
	onProblem  func(domain.Location, helperruntime.OperationProblem) E
	onComplete func(helperruntime.Completion) E
	onTerminal func(search.Status, search.StopReason) E
}

func adaptHelperStream[E any](
	ctx context.Context,
	implementation providerapi.Provider,
	client *helperruntime.Client,
	input <-chan helperruntime.ClientEvent,
	output chan<- E,
	adapter helperStreamAdapter[E],
) {
	defer close(output)
	failInvalid := func() {
		closeInvalidHelper(client)
		output <- adapter.onTerminal(search.StatusPartial, search.StopProviderError)
	}
	for event := range input {
		if ctx.Err() != nil {
			_ = client.Cancel(adapter.requestID)
			output <- adapter.onTerminal(search.StatusCanceled, search.StopCanceled)
			return
		}
		if err := validateHelperSearchSnapshot(ctx, implementation, adapter.endpointID, adapter.sessionID, adapter.generation); err != nil {
			_ = client.Cancel(adapter.requestID)
			output <- adapter.onTerminal(search.StatusPartial, search.StopGenerationChanged)
			return
		}
		if event.Err != nil {
			break
		}
		switch event.Type {
		case helperruntime.FrameResult:
			converted, ok := adapter.onResult(event.Payload)
			if !ok {
				failInvalid()
				return
			}
			output <- converted
		case helperruntime.FrameProgress:
			var problem helperruntime.OperationProblem
			if helperruntime.DecodePayload(event.Payload, &problem) != nil {
				failInvalid()
				return
			}
			location, ok := helperResultLocation(adapter.endpointID, adapter.scopePath, problem.RelativePath)
			if !ok {
				failInvalid()
				return
			}
			output <- adapter.onProblem(location, problem)
		case helperruntime.FrameError:
			var structured helperruntime.StructuredError
			if helperruntime.DecodePayload(event.Payload, &structured) != nil {
				failInvalid()
				return
			}
		case helperruntime.FrameComplete:
			var completion helperruntime.Completion
			if helperruntime.DecodePayload(event.Payload, &completion) != nil {
				failInvalid()
				return
			}
			output <- adapter.onComplete(completion)
			return
		}
	}
	if ctx.Err() != nil {
		output <- adapter.onTerminal(search.StatusCanceled, search.StopCanceled)
		return
	}
	output <- adapter.onTerminal(search.StatusPartial, search.StopProviderError)
}

func adaptHelperFilename(ctx context.Context, implementation providerapi.Provider, client *helperruntime.Client, identity search.Identity, input <-chan helperruntime.ClientEvent, output chan<- search.Event) {
	adaptHelperStream(ctx, implementation, client, input, output, helperStreamAdapter[search.Event]{
		requestID:  identity.RequestID,
		endpointID: identity.EndpointID,
		sessionID:  identity.SessionID,
		generation: identity.EndpointGeneration,
		scopePath:  identity.Scope.Path,
		onResult: func(payload json.RawMessage) (search.Event, bool) {
			var result helperruntime.FilenameSearchResult
			if helperruntime.DecodePayload(payload, &result) != nil {
				return search.Event{}, false
			}
			converted, ok := convertHelperFilenameResult(identity, result)
			if !ok {
				return search.Event{}, false
			}
			return search.Event{Identity: identity, Kind: search.EventResult, Result: converted}, true
		},
		onProblem: func(location domain.Location, problem helperruntime.OperationProblem) search.Event {
			return search.Event{Identity: identity, Kind: search.EventProblem, Problem: search.Problem{Location: location, Code: helperProblemCode(problem.Reason)}}
		},
		onComplete: func(completion helperruntime.Completion) search.Event {
			return search.Event{Identity: identity, Kind: search.EventTerminal, Terminal: filenameTerminal(completion)}
		},
		onTerminal: func(status search.Status, reason search.StopReason) search.Event {
			return search.Event{Identity: identity, Kind: search.EventTerminal, Terminal: search.Terminal{Status: status, StopReason: reason}}
		},
	})
}

func adaptHelperContent(ctx context.Context, implementation providerapi.Provider, client *helperruntime.Client, identity search.ContentIdentity, input <-chan helperruntime.ClientEvent, output chan<- search.ContentEvent) {
	adaptHelperStream(ctx, implementation, client, input, output, helperStreamAdapter[search.ContentEvent]{
		requestID:  identity.RequestID,
		endpointID: identity.EndpointID,
		sessionID:  identity.SessionID,
		generation: identity.EndpointGeneration,
		scopePath:  identity.Scope.Path,
		onResult: func(payload json.RawMessage) (search.ContentEvent, bool) {
			var result helperruntime.ContentSearchResult
			if helperruntime.DecodePayload(payload, &result) != nil {
				return search.ContentEvent{}, false
			}
			location, ok := helperResultLocation(identity.EndpointID, identity.Scope.Path, result.RelativePath)
			if !ok || result.Line == 0 || len(result.Snippet) > int(identity.Budget.MaxSnippetBytes) {
				return search.ContentEvent{}, false
			}
			return search.ContentEvent{
				Identity: identity,
				Kind:     search.ContentEventResult,
				Result:   search.ContentResult{Location: location, RelativePath: result.RelativePath, Line: result.Line, Offset: result.Offset, Snippet: result.Snippet},
			}, true
		},
		onProblem: func(location domain.Location, problem helperruntime.OperationProblem) search.ContentEvent {
			return search.ContentEvent{
				Identity: identity,
				Kind:     search.ContentEventProblem,
				Problem:  search.ContentProblem{Location: location, Code: helperProblemCode(problem.Reason), Reason: helperStopReason(problem.Reason)},
			}
		},
		onComplete: func(completion helperruntime.Completion) search.ContentEvent {
			return search.ContentEvent{Identity: identity, Kind: search.ContentEventTerminal, Terminal: contentTerminal(completion)}
		},
		onTerminal: func(status search.Status, reason search.StopReason) search.ContentEvent {
			return search.ContentEvent{Identity: identity, Kind: search.ContentEventTerminal, Terminal: search.ContentTerminal{Status: status, StopReason: reason}}
		},
	})
}

func closeInvalidHelper(client *helperruntime.Client) {
	_ = client.Close()
}

func convertHelperFilenameResult(identity search.Identity, result helperruntime.FilenameSearchResult) (search.Result, bool) {
	location, ok := helperResultLocation(identity.EndpointID, identity.Scope.Path, result.RelativePath)
	if !ok {
		return search.Result{}, false
	}
	kind := domain.EntryKind(result.Kind)
	if kind != domain.EntryFile && kind != domain.EntryDirectory && kind != domain.EntrySymlink {
		return search.Result{}, false
	}
	name := path.Base(result.RelativePath)
	mode := result.Mode
	modified := time.Unix(0, result.ModifiedUnixNS)
	metadata := domain.Metadata{Mode: &mode, ModifiedAt: &modified}
	if kind == domain.EntryFile {
		size := result.Size
		metadata.Size = &size
	}
	return search.Result{Location: location, RelativePath: result.RelativePath, Entry: domain.Entry{Location: location, Name: name, Kind: kind, Metadata: metadata}}, true
}

func helperResultLocation(endpointID domain.EndpointID, scope domain.CanonicalPath, relative string) (domain.Location, bool) {
	if relative == "" || len(relative) > helperruntime.MaxHelperStringBytes || !utf8.ValidString(relative) || strings.IndexByte(relative, 0) >= 0 || path.IsAbs(relative) || path.Clean(relative) != relative || relative == ".." || strings.HasPrefix(relative, "../") {
		return domain.Location{}, false
	}
	joined := path.Join(string(scope), relative)
	prefix := strings.TrimSuffix(string(scope), "/") + "/"
	if !strings.HasPrefix(joined, prefix) {
		return domain.Location{}, false
	}
	return domain.Location{EndpointID: endpointID, Path: domain.CanonicalPath(joined)}, true
}

func filenameTerminal(completion helperruntime.Completion) search.Terminal {
	return search.Terminal{Status: helperStatus(completion.Status), StopReason: helperStopReason(completion.Reason), Entries: completion.Entries, Results: completion.Results, OutputBytes: completion.OutputBytes}
}

func contentTerminal(completion helperruntime.Completion) search.ContentTerminal {
	return search.ContentTerminal{Status: helperStatus(completion.Status), StopReason: helperStopReason(completion.Reason), Entries: completion.Entries, Files: completion.Files, Results: completion.Results, BytesRead: completion.BytesRead, OutputBytes: completion.OutputBytes}
}

func helperStatus(status string) search.Status {
	switch status {
	case "complete":
		return search.StatusComplete
	case "canceled":
		return search.StatusCanceled
	default:
		return search.StatusPartial
	}
}

func helperStopReason(reason string) search.StopReason {
	switch reason {
	case "none":
		return search.StopNone
	case "canceled":
		return search.StopCanceled
	case "permission_denied":
		return search.StopPermissionDenied
	case "depth_limit":
		return search.StopDepthLimit
	case "entry_limit":
		return search.StopEntryLimit
	case "result_limit":
		return search.StopResultLimit
	case "output_byte_limit", "byte_limit", "read_byte_limit":
		return search.StopByteLimit
	case "time_limit":
		return search.StopTimeLimit
	case "binary_skipped":
		return search.StopBinarySkipped
	case "encoding_invalid":
		return search.StopEncodingInvalid
	case "file_byte_limit", "line_limit":
		return search.StopFileByteLimit
	case "file_changed":
		return search.StopFileChanged
	case "file_limit":
		return search.StopFileLimit
	case "file_result_limit":
		return search.StopFileResultLimit
	default:
		return search.StopProviderError
	}
}

func helperProblemCode(reason string) domain.Code {
	switch reason {
	case "permission_denied":
		return domain.CodePermissionDenied
	case "binary_skipped":
		return domain.CodeUnsupported
	case "encoding_invalid":
		return domain.CodeInvalidArgument
	case "file_changed":
		return domain.CodeConflict
	case "depth_limit", "entry_limit", "result_limit", "output_byte_limit", "byte_limit", "read_byte_limit", "file_byte_limit", "line_limit", "file_limit", "file_result_limit":
		return domain.CodeResourceExhausted
	default:
		return domain.CodeInternal
	}
}
