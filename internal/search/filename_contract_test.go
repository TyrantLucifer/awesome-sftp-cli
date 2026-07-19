package search

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/TyrantLucifer/awesome-sftp-cli/internal/domain"
	providerapi "github.com/TyrantLucifer/awesome-sftp-cli/internal/provider"
)

const (
	searchEndpointID domain.EndpointID = "ep_zzzzzzzzzzzzzzzzzzzzzzzzzz"
	searchSessionID  domain.SessionID  = "sess_zzzzzzzzzzzzzzzzzzzzzzzzzz"
)

func TestLevel0FilenameSearchStreamsBoundedPagesWithoutHelperSurfaces(t *testing.T) {
	implementation := newFilenameContractProvider(t)
	request := filenameContractRequest()
	events, err := StartFilename(context.Background(), implementation, request)
	if err != nil {
		t.Fatalf("StartFilename(): %v", err)
	}
	if cap(events) != int(request.Identity.Budget.EventBuffer) {
		t.Fatalf("event buffer = %d, want frozen budget %d", cap(events), request.Identity.Budget.EventBuffer)
	}

	var results []Result
	var terminal Terminal
	for event := range events {
		if event.Identity != request.Identity {
			t.Fatalf("event identity = %#v, want frozen %#v", event.Identity, request.Identity)
		}
		switch event.Kind {
		case EventResult:
			results = append(results, event.Result)
		case EventTerminal:
			terminal = event.Terminal
		}
	}

	if terminal.Status != StatusPartial || terminal.StopReason != StopPermissionDenied {
		t.Fatalf("terminal = %#v, want permission-bounded partial results", terminal)
	}
	if len(results) != 1 || results[0].RelativePath != "src/target.txt" {
		t.Fatalf("results = %#v, want only src/target.txt", results)
	}
	if implementation.maxListLimit() > request.Identity.Budget.PageItems {
		t.Fatalf("maximum List limit = %d, budget = %d", implementation.maxListLimit(), request.Identity.Budget.PageItems)
	}
	if implementation.listed("/vendor-link") {
		t.Fatal("Level 0 filename search followed a symlink directory")
	}
	if implementation.openReadCalls() != 0 {
		t.Fatalf("OpenRead calls = %d, filename search must not read file content", implementation.openReadCalls())
	}
}

func TestLevel0FilenameSearchCancellationRetainsResultsAndReportsCanceled(t *testing.T) {
	implementation := newFilenameContractProvider(t)
	implementation.blockPath = "/slow"
	request := filenameContractRequest()
	request.Identity.Options.Pattern = "match"

	ctx, cancel := context.WithCancel(context.Background())
	events, err := StartFilename(ctx, implementation, request)
	if err != nil {
		t.Fatalf("StartFilename(): %v", err)
	}

	first := <-events
	if first.Kind != EventResult || first.Result.RelativePath != "match.txt" {
		t.Fatalf("first event = %#v, want streamed match before blocked subtree", first)
	}
	cancelStarted := time.Now()
	cancel()

	terminal := terminalEvent(t, events)
	cancelLatency := time.Since(cancelStarted)
	if terminal.Status != StatusCanceled || terminal.StopReason != StopCanceled || terminal.Results != 1 {
		t.Fatalf("terminal = %#v, want one retained result and canceled status", terminal)
	}
	select {
	case <-implementation.blockObserved:
	case <-time.After(2 * time.Second):
		t.Fatal("provider List did not observe propagated cancellation")
	}
	if cancelLatency > 250*time.Millisecond {
		t.Fatalf("cancel latency = %s, want <= 250ms", cancelLatency)
	}
	t.Logf("cancel latency=%s", cancelLatency)
}

func TestLevel0FilenameSearchStopsWhenEndpointGenerationChanges(t *testing.T) {
	implementation := newFilenameContractProvider(t)
	request := filenameContractRequest()
	request.Identity.Options.Pattern = "match"
	implementation.changeGenerationAfterFirstList = true

	events, err := StartFilename(context.Background(), implementation, request)
	if err != nil {
		t.Fatalf("StartFilename(): %v", err)
	}
	terminal := terminalEvent(t, events)
	if terminal.Status != StatusPartial || terminal.StopReason != StopGenerationChanged {
		t.Fatalf("terminal = %#v, want generation-changed partial result", terminal)
	}
	if terminal.Results != 1 {
		t.Fatalf("retained results = %d, want 1", terminal.Results)
	}

	stale := request.Identity
	stale.UIGeneration++
	if EventCurrent(stale, Event{Identity: request.Identity}) {
		t.Fatal("event with a stale UI generation was accepted")
	}
	stale = request.Identity
	stale.EndpointGeneration++
	if EventCurrent(stale, Event{Identity: request.Identity}) {
		t.Fatal("event with a stale Endpoint generation was accepted")
	}
	if !EventCurrent(request.Identity, Event{Identity: request.Identity}) {
		t.Fatal("event with the exact frozen identity was rejected")
	}
}

func TestFilenameRequestValidationFreezesScopeOptionsAndHardBudgets(t *testing.T) {
	implementation := newFilenameContractProvider(t)
	valid := filenameContractRequest()
	_ = runToTerminal(t, implementation, valid)

	tests := []struct {
		name   string
		mutate func(*Request)
	}{
		{"missing request id", func(request *Request) { request.Identity.RequestID = "" }},
		{"wrong endpoint", func(request *Request) { request.Identity.EndpointID = "ep_other0000000000000000000000" }},
		{"wrong session", func(request *Request) { request.Identity.SessionID = "sess_other00000000000000000000" }},
		{"zero endpoint generation", func(request *Request) { request.Identity.EndpointGeneration = 0 }},
		{"zero ui generation", func(request *Request) { request.Identity.UIGeneration = 0 }},
		{"empty scope", func(request *Request) { request.Identity.Scope.Path = "" }},
		{"empty pattern", func(request *Request) { request.Identity.Options.Pattern = "" }},
		{"unknown match target", func(request *Request) { request.Identity.Options.Target = MatchTarget("unknown") }},
		{"follow symlink forbidden", func(request *Request) { request.Identity.Options.Symlinks = SymlinkPolicy("follow") }},
		{"default ignore unsupported", func(request *Request) { request.Identity.Options.Ignore = IgnoreDefault }},
		{"no result types", func(request *Request) { request.Identity.Options.Types = TypeFilter{} }},
		{"zero page", func(request *Request) { request.Identity.Budget.PageItems = 0 }},
		{"oversize page", func(request *Request) { request.Identity.Budget.PageItems = 4097 }},
		{"zero event buffer", func(request *Request) { request.Identity.Budget.EventBuffer = 0 }},
		{"oversize event buffer", func(request *Request) { request.Identity.Budget.EventBuffer = 4097 }},
		{"zero concurrent lists", func(request *Request) { request.Identity.Budget.ConcurrentLists = 0 }},
		{"oversize concurrent lists", func(request *Request) { request.Identity.Budget.ConcurrentLists = 65 }},
		{"zero depth", func(request *Request) { request.Identity.Budget.MaxDepth = 0 }},
		{"oversize depth", func(request *Request) { request.Identity.Budget.MaxDepth = 257 }},
		{"zero entries", func(request *Request) { request.Identity.Budget.MaxEntries = 0 }},
		{"zero results", func(request *Request) { request.Identity.Budget.MaxResults = 0 }},
		{"zero output bytes", func(request *Request) { request.Identity.Budget.MaxOutputBytes = 0 }},
		{"zero duration", func(request *Request) { request.Identity.Budget.MaxDuration = 0 }},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			request := valid
			test.mutate(&request)
			if _, err := StartFilename(context.Background(), implementation, request); err == nil {
				t.Fatal("StartFilename() succeeded, want fail-closed validation error")
			}
		})
	}
}

func TestLevel0FilenameSearchResultAndDepthLimitsAreExplicitPartialReasons(t *testing.T) {
	t.Run("result limit", func(t *testing.T) {
		implementation := newFilenameContractProvider(t)
		request := filenameContractRequest()
		request.Identity.Options.Pattern = "match"
		request.Identity.Budget.MaxResults = 1
		terminal := runToTerminal(t, implementation, request)
		if terminal.Status != StatusPartial || terminal.StopReason != StopResultLimit || terminal.Results != 1 {
			t.Fatalf("terminal = %#v", terminal)
		}
	})

	t.Run("depth limit", func(t *testing.T) {
		implementation := newFilenameContractProvider(t)
		request := filenameContractRequest()
		request.Identity.Options.Pattern = "target"
		request.Identity.Budget.MaxDepth = 1
		terminal := runToTerminal(t, implementation, request)
		if terminal.Status != StatusPartial || terminal.StopReason != StopDepthLimit {
			t.Fatalf("terminal = %#v", terminal)
		}
	})
}

func filenameContractRequest() Request {
	return Request{Identity: Identity{
		RequestID:          "req_zzzzzzzzzzzzzzzzzzzzzzzzzz",
		EndpointID:         searchEndpointID,
		SessionID:          searchSessionID,
		EndpointGeneration: 7,
		UIGeneration:       11,
		Scope:              domain.Location{EndpointID: searchEndpointID, Path: "/"},
		Options: Options{
			Pattern:       "target",
			Target:        MatchRelativePath,
			CaseSensitive: true,
			IncludeHidden: false,
			Symlinks:      SymlinkNever,
			Ignore:        IgnoreNone,
			Types:         TypeFilter{Files: true, Directories: true, Symlinks: true},
		},
		Budget: Budget{
			PageItems:       2,
			EventBuffer:     2,
			ConcurrentLists: 2,
			MaxDepth:        8,
			MaxEntries:      64,
			MaxResults:      16,
			MaxOutputBytes:  64 * 1024,
			MaxDuration:     time.Second,
		},
	}}
}

func runToTerminal(t *testing.T, implementation providerapi.Provider, request Request) Terminal {
	t.Helper()
	events, err := StartFilename(context.Background(), implementation, request)
	if err != nil {
		t.Fatalf("StartFilename(): %v", err)
	}
	return terminalEvent(t, events)
}

func terminalEvent(t *testing.T, events <-chan Event) Terminal {
	t.Helper()
	var terminal Terminal
	seen := false
	for event := range events {
		if event.Kind == EventTerminal {
			terminal = event.Terminal
			seen = true
		}
	}
	if !seen {
		t.Fatal("search event stream closed without a terminal event")
	}
	return terminal
}

type filenameContractProvider struct {
	t             *testing.T
	endpoint      domain.Endpoint
	root          domain.Location
	blockPath     domain.CanonicalPath
	blockObserved chan struct{}

	mu                             sync.Mutex
	snapshot                       domain.EndpointSnapshot
	listCalls                      []domain.CanonicalPath
	maximumLimit                   uint32
	reads                          int
	changeGenerationAfterFirstList bool
}

func newFilenameContractProvider(t *testing.T) *filenameContractProvider {
	t.Helper()
	capabilities, err := domain.NewCapabilitySnapshot(
		domain.CapabilityRevision{SessionID: searchSessionID, Generation: 7},
		true,
		[]domain.Capability{{Name: "read", Version: 1}},
	)
	if err != nil {
		t.Fatal(err)
	}
	endpoint := domain.Endpoint{ID: searchEndpointID, Kind: domain.EndpointSSH, DisplayName: "level-zero"}
	return &filenameContractProvider{
		t:             t,
		endpoint:      endpoint,
		root:          domain.Location{EndpointID: searchEndpointID, Path: "/"},
		blockObserved: make(chan struct{}),
		snapshot: domain.EndpointSnapshot{
			EndpointID:   searchEndpointID,
			SessionID:    searchSessionID,
			State:        domain.StateReady,
			Capabilities: capabilities,
		},
	}
}

func (provider *filenameContractProvider) Descriptor() domain.Endpoint { return provider.endpoint }

func (provider *filenameContractProvider) Snapshot(context.Context) (domain.EndpointSnapshot, error) {
	provider.mu.Lock()
	defer provider.mu.Unlock()
	return provider.snapshot, nil
}

func (provider *filenameContractProvider) Normalize(_ context.Context, request domain.NormalizeRequest) (domain.Location, error) {
	return domain.Location{EndpointID: searchEndpointID, Path: domain.CanonicalPath(request.Input)}, nil
}

func (provider *filenameContractProvider) Stat(_ context.Context, request providerapi.StatRequest) (domain.Entry, error) {
	if request.Location != provider.root {
		return domain.Entry{}, fmt.Errorf("unexpected Stat location %q", request.Location.Path)
	}
	return entry("/", domain.EntryDirectory), nil
}

func (provider *filenameContractProvider) List(ctx context.Context, request providerapi.ListRequest) (providerapi.ListPage, error) {
	provider.mu.Lock()
	provider.listCalls = append(provider.listCalls, request.Location.Path)
	if request.Limit > provider.maximumLimit {
		provider.maximumLimit = request.Limit
	}
	call := len(provider.listCalls)
	if provider.changeGenerationAfterFirstList && call == 1 {
		capabilities, err := domain.NewCapabilitySnapshot(
			domain.CapabilityRevision{SessionID: searchSessionID, Generation: 8},
			true,
			[]domain.Capability{{Name: "read", Version: 1}},
		)
		if err != nil {
			provider.mu.Unlock()
			return providerapi.ListPage{}, err
		}
		provider.snapshot.Capabilities = capabilities
	}
	provider.mu.Unlock()

	if request.Location.Path == provider.blockPath {
		select {
		case <-provider.blockObserved:
		default:
			close(provider.blockObserved)
		}
		<-ctx.Done()
		return providerapi.ListPage{}, ctx.Err()
	}
	if request.Location.Path == "/denied" {
		location := request.Location
		return providerapi.ListPage{}, &domain.OpError{
			Code:       domain.CodePermissionDenied,
			Operation:  "list",
			EndpointID: searchEndpointID,
			Location:   &location,
			Message:    "permission denied",
		}
	}

	entries := map[domain.CanonicalPath][]domain.Entry{
		"/": {
			entry("/match.txt", domain.EntryFile),
			entry("/slow", domain.EntryDirectory),
			entry("/src", domain.EntryDirectory),
			entry("/.secret.txt", domain.EntryFile),
			entry("/vendor-link", domain.EntrySymlink),
			entry("/denied", domain.EntryDirectory),
		},
		"/src": {
			entry("/src/target.txt", domain.EntryFile),
			entry("/src/nested", domain.EntryDirectory),
		},
		"/src/nested": {
			entry("/src/nested/second-match.txt", domain.EntryFile),
		},
		"/slow": nil,
	}[request.Location.Path]
	start := 0
	if request.Cursor != "" {
		_, err := fmt.Sscanf(string(request.Cursor), "%d", &start)
		if err != nil {
			return providerapi.ListPage{}, err
		}
	}
	end := start + int(request.Limit)
	if end > len(entries) {
		end = len(entries)
	}
	next := providerapi.PageCursor("")
	if end < len(entries) {
		next = providerapi.PageCursor(fmt.Sprintf("%d", end))
	}
	return providerapi.ListPage{
		Entries:     append([]domain.Entry(nil), entries[start:end]...),
		NextCursor:  next,
		Done:        next == "",
		Consistency: providerapi.ConsistencyBestEffort,
	}, nil
}

func (provider *filenameContractProvider) OpenRead(context.Context, providerapi.OpenReadRequest) (providerapi.ReadHandle, error) {
	provider.mu.Lock()
	provider.reads++
	provider.mu.Unlock()
	return nil, errors.New("filename search must not call OpenRead")
}

func (provider *filenameContractProvider) maxListLimit() uint32 {
	provider.mu.Lock()
	defer provider.mu.Unlock()
	return provider.maximumLimit
}

func (provider *filenameContractProvider) listed(path domain.CanonicalPath) bool {
	provider.mu.Lock()
	defer provider.mu.Unlock()
	for _, listed := range provider.listCalls {
		if listed == path {
			return true
		}
	}
	return false
}

func (provider *filenameContractProvider) openReadCalls() int {
	provider.mu.Lock()
	defer provider.mu.Unlock()
	return provider.reads
}

func entry(path domain.CanonicalPath, kind domain.EntryKind) domain.Entry {
	name := string(path)
	for index := len(name) - 1; index >= 0; index-- {
		if name[index] == '/' {
			name = name[index+1:]
			break
		}
	}
	if path == "/" {
		name = "/"
	}
	return domain.Entry{
		Location: domain.Location{EndpointID: searchEndpointID, Path: path},
		Name:     name,
		Kind:     kind,
	}
}
