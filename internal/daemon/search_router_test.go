package daemon

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/TyrantLucifer/awesome-mac-sftp/internal/domain"
	"github.com/TyrantLucifer/awesome-mac-sftp/internal/ipc"
	providerapi "github.com/TyrantLucifer/awesome-mac-sftp/internal/provider"
	"github.com/TyrantLucifer/awesome-mac-sftp/internal/provider/localfs"
	"github.com/TyrantLucifer/awesome-mac-sftp/internal/search"
)

func TestProviderSessionStreamsBoundedLevel0FilenameSearchPages(t *testing.T) {
	implementation, root := newSearchLocalProvider(t)
	if err := os.MkdirAll(filepath.Join(root, "nested"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "nested", "target.txt"), []byte("match"), 0o600); err != nil {
		t.Fatal(err)
	}
	snapshot, err := implementation.Snapshot(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	factory, err := NewProviderSessions([]providerapi.Provider{implementation}, 4)
	if err != nil {
		t.Fatal(err)
	}
	session := factory.NewSession()
	t.Cleanup(func() { _ = session.Close() })

	identity := search.Identity{
		RequestID:          "req_aaaaaaaaaaaaaaaaaaaaaaaaaa",
		EndpointID:         implementation.Descriptor().ID,
		SessionID:          snapshot.SessionID,
		EndpointGeneration: snapshot.Capabilities.Revision.Generation,
		UIGeneration:       3,
		Scope:              domain.Location{EndpointID: implementation.Descriptor().ID, Path: "/"},
		Options: search.Options{
			Pattern:       "target",
			Target:        search.MatchRelativePath,
			CaseSensitive: true,
			Symlinks:      search.SymlinkNever,
			Ignore:        search.IgnoreNone,
			Types:         search.TypeFilter{Files: true},
		},
		Budget: search.Budget{
			PageItems:       2,
			EventBuffer:     2,
			ConcurrentLists: 1,
			MaxDepth:        8,
			MaxEntries:      64,
			MaxResults:      16,
			MaxOutputBytes:  64 * 1024,
			MaxDuration:     time.Second,
		},
	}
	started := handlePayload[ipc.SearchFilenameStartResponse](t, session, SearchFilenameStart, ipc.SearchFilenameStartRequest{
		Identity: ipc.EncodeSearchIdentity(identity),
	})
	if started.RequestID != string(identity.RequestID) {
		t.Fatalf("started request = %q, want %q", started.RequestID, identity.RequestID)
	}

	var events []search.Event
	for !started.Done {
		page := handlePayload[ipc.SearchFilenameNextResponse](t, session, SearchFilenameNext, ipc.SearchFilenameNextRequest{
			RequestID: started.RequestID,
			Limit:     1,
		})
		if len(page.Events) > 1 {
			t.Fatalf("event page has %d events, want at most 1", len(page.Events))
		}
		for _, wire := range page.Events {
			event, err := ipc.DecodeSearchEvent(wire)
			if err != nil {
				t.Fatal(err)
			}
			events = append(events, event)
		}
		started.Done = page.Done
	}
	if len(events) != 2 || events[0].Kind != search.EventResult || events[0].Result.RelativePath != "nested/target.txt" || events[1].Kind != search.EventTerminal {
		t.Fatalf("events = %#v", events)
	}
}

func TestProviderSessionRejectsDuplicateAndUnknownSearchCursors(t *testing.T) {
	implementation, _ := newSearchLocalProvider(t)
	snapshot, err := implementation.Snapshot(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	factory, err := NewProviderSessions([]providerapi.Provider{implementation}, 4)
	if err != nil {
		t.Fatal(err)
	}
	session := factory.NewSession()
	defer session.Close()
	identity := search.Identity{
		RequestID:          "req_bbbbbbbbbbbbbbbbbbbbbbbbbb",
		EndpointID:         implementation.Descriptor().ID,
		SessionID:          snapshot.SessionID,
		EndpointGeneration: snapshot.Capabilities.Revision.Generation,
		UIGeneration:       1,
		Scope:              domain.Location{EndpointID: implementation.Descriptor().ID, Path: "/"},
		Options:            search.Options{Pattern: "file", Target: search.MatchName, CaseSensitive: true, Symlinks: search.SymlinkNever, Ignore: search.IgnoreNone, Types: search.TypeFilter{Files: true}},
		Budget:             search.Budget{PageItems: 2, EventBuffer: 2, ConcurrentLists: 1, MaxDepth: 8, MaxEntries: 64, MaxResults: 16, MaxOutputBytes: 64 * 1024, MaxDuration: time.Second},
	}
	request := ipc.SearchFilenameStartRequest{Identity: ipc.EncodeSearchIdentity(identity)}
	_ = handlePayload[ipc.SearchFilenameStartResponse](t, session, SearchFilenameStart, request)
	if _, err := handleRawSearch(session, SearchFilenameStart, request); !domain.IsCode(err, domain.CodeConflict) {
		t.Fatalf("duplicate start error = %v, want conflict", err)
	}
	if _, err := handleRawSearch(session, SearchFilenameNext, ipc.SearchFilenameNextRequest{RequestID: "req_cccccccccccccccccccccccccc", Limit: 1}); !domain.IsCode(err, domain.CodeNotFound) {
		t.Fatalf("unknown next error = %v, want not_found", err)
	}
}

func TestProviderSessionCancelDrainsToCanceledTerminal(t *testing.T) {
	local, _ := newSearchLocalProvider(t)
	implementation := &blockingSearchProvider{Provider: local, entered: make(chan struct{})}
	snapshot, err := implementation.Snapshot(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	factory, err := NewProviderSessions([]providerapi.Provider{implementation}, 4)
	if err != nil {
		t.Fatal(err)
	}
	session := factory.NewSession()
	defer session.Close()
	identity := blockingSearchIdentity(implementation.Descriptor().ID, snapshot)
	started := handlePayload[ipc.SearchFilenameStartResponse](t, session, SearchFilenameStart, ipc.SearchFilenameStartRequest{
		Identity: ipc.EncodeSearchIdentity(identity),
	})

	select {
	case <-implementation.entered:
	case <-time.After(2 * time.Second):
		t.Fatal("search did not enter the blocking Provider List call")
	}
	_ = handlePayload[ipc.SearchCancelResponse](t, session, SearchCancel, ipc.SearchCancelRequest{RequestID: started.RequestID})
	page := handlePayload[ipc.SearchFilenameNextResponse](t, session, SearchFilenameNext, ipc.SearchFilenameNextRequest{
		RequestID: started.RequestID,
		Limit:     2,
	})
	if !page.Done || len(page.Events) != 1 {
		t.Fatalf("cancel page = %#v, want one terminal event", page)
	}
	event, err := ipc.DecodeSearchEvent(page.Events[0])
	if err != nil {
		t.Fatal(err)
	}
	if event.Kind != search.EventTerminal || event.Terminal.Status != search.StatusCanceled || event.Terminal.StopReason != search.StopCanceled {
		t.Fatalf("cancel terminal = %#v", event)
	}
	if _, err := handleRawSearch(session, SearchFilenameNext, ipc.SearchFilenameNextRequest{RequestID: started.RequestID, Limit: 1}); !domain.IsCode(err, domain.CodeNotFound) {
		t.Fatalf("drained cursor error = %v, want not_found", err)
	}
}

func TestProviderSessionCloseCancelsAndDrainsFilenameSearch(t *testing.T) {
	local, _ := newSearchLocalProvider(t)
	implementation := &blockingSearchProvider{Provider: local, entered: make(chan struct{})}
	snapshot, err := implementation.Snapshot(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	factory, err := NewProviderSessions([]providerapi.Provider{implementation}, 4)
	if err != nil {
		t.Fatal(err)
	}
	session := factory.NewSession()
	identity := blockingSearchIdentity(implementation.Descriptor().ID, snapshot)
	_ = handlePayload[ipc.SearchFilenameStartResponse](t, session, SearchFilenameStart, ipc.SearchFilenameStartRequest{
		Identity: ipc.EncodeSearchIdentity(identity),
	})

	select {
	case <-implementation.entered:
	case <-time.After(2 * time.Second):
		t.Fatal("search did not enter the blocking Provider List call")
	}
	done := make(chan error, 1)
	go func() { done <- session.Close() }()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("Close(): %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("Close did not cancel and drain the active filename search")
	}
}

func TestProviderSessionStreamsBoundedLevel0ContentSearchPages(t *testing.T) {
	implementation, root := newSearchLocalProvider(t)
	if err := os.WriteFile(filepath.Join(root, "content.txt"), []byte("line one\nneedle here\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	snapshot, err := implementation.Snapshot(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	factory, err := NewProviderSessions([]providerapi.Provider{implementation}, 4)
	if err != nil {
		t.Fatal(err)
	}
	session := factory.NewSession()
	defer session.Close()
	identity := search.ContentIdentity{
		RequestID: "req_gggggggggggggggggggggggggg", EndpointID: implementation.Descriptor().ID,
		SessionID: snapshot.SessionID, EndpointGeneration: snapshot.Capabilities.Revision.Generation, UIGeneration: 2,
		Scope:   domain.Location{EndpointID: implementation.Descriptor().ID, Path: "/"},
		Options: search.ContentOptions{Pattern: "needle", PatternType: search.PatternLiteral, CaseSensitive: true, Binary: search.BinarySkip},
		Budget:  search.ContentBudget{PageItems: 2, EventBuffer: 2, MaxDepth: 8, MaxEntries: 64, MaxFiles: 16, MaxResults: 16, MaxMatchesPerFile: 4, MaxFileBytes: 1024, MaxReadBytes: 4096, MaxSnippetBytes: 128, MaxOutputBytes: 4096, MaxDuration: time.Second},
	}
	started := handlePayload[ipc.SearchContentStartResponse](t, session, SearchContentStart, ipc.SearchContentStartRequest{Identity: ipc.EncodeContentSearchIdentity(identity)})
	var events []search.ContentEvent
	for !started.Done {
		page := handlePayload[ipc.SearchContentNextResponse](t, session, SearchContentNext, ipc.SearchContentNextRequest{RequestID: started.RequestID, Limit: 1})
		if len(page.Events) > 1 {
			t.Fatalf("content page has %d events", len(page.Events))
		}
		for _, wire := range page.Events {
			event, err := ipc.DecodeContentSearchEvent(wire)
			if err != nil {
				t.Fatal(err)
			}
			events = append(events, event)
		}
		started.Done = page.Done
	}
	if len(events) != 2 || events[0].Kind != search.ContentEventResult || events[0].Result.Line != 2 || events[1].Kind != search.ContentEventTerminal {
		t.Fatalf("content events = %#v", events)
	}
}

func blockingSearchIdentity(endpointID domain.EndpointID, snapshot domain.EndpointSnapshot) search.Identity {
	return search.Identity{
		RequestID:          "req_eeeeeeeeeeeeeeeeeeeeeeeeee",
		EndpointID:         endpointID,
		SessionID:          snapshot.SessionID,
		EndpointGeneration: snapshot.Capabilities.Revision.Generation,
		UIGeneration:       1,
		Scope:              domain.Location{EndpointID: endpointID, Path: "/"},
		Options:            search.Options{Pattern: "never", Target: search.MatchName, CaseSensitive: true, Symlinks: search.SymlinkNever, Ignore: search.IgnoreNone, Types: search.TypeFilter{Files: true}},
		Budget:             search.Budget{PageItems: 2, EventBuffer: 2, ConcurrentLists: 1, MaxDepth: 8, MaxEntries: 64, MaxResults: 16, MaxOutputBytes: 64 * 1024, MaxDuration: time.Minute},
	}
}

type blockingSearchProvider struct {
	providerapi.Provider
	entered chan struct{}
}

func (p *blockingSearchProvider) List(ctx context.Context, _ providerapi.ListRequest) (providerapi.ListPage, error) {
	select {
	case <-p.entered:
	default:
		close(p.entered)
	}
	<-ctx.Done()
	return providerapi.ListPage{}, ctx.Err()
}

func newSearchLocalProvider(t *testing.T) (*localfs.Provider, string) {
	t.Helper()
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "file"), []byte("fixture"), 0o600); err != nil {
		t.Fatal(err)
	}
	implementation, err := localfs.New(localfs.Config{
		Endpoint:   domain.Endpoint{ID: "ep_dddddddddddddddddddddddddd", Kind: domain.EndpointLocal, DisplayName: "search"},
		SessionID:  "sess_dddddddddddddddddddddddddd",
		Root:       root,
		MaxCursors: 8,
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = implementation.Close() })
	return implementation, root
}

func handleRawSearch(session Session, name string, request any) (any, error) {
	payload, err := json.Marshal(request)
	if err != nil {
		return nil, err
	}
	return session.Handle(context.Background(), name, payload)
}
