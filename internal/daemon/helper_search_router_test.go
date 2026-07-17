package daemon

import (
	"context"
	"net"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/TyrantLucifer/awesome-mac-sftp/internal/domain"
	helperruntime "github.com/TyrantLucifer/awesome-mac-sftp/internal/helper"
	"github.com/TyrantLucifer/awesome-mac-sftp/internal/ipc"
	providerapi "github.com/TyrantLucifer/awesome-mac-sftp/internal/provider"
	"github.com/TyrantLucifer/awesome-mac-sftp/internal/search"
)

func TestProviderSessionRoutesNegotiatedSearchCapabilitiesThroughHelper(t *testing.T) {
	implementation, root := newSearchLocalProvider(t)
	if err := os.MkdirAll(filepath.Join(root, "nested"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "nested", "level-one.txt"), []byte("first\nhelper needle\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	snapshot, err := implementation.Snapshot(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	client := newDaemonHelperClient(t)
	factory, err := NewProviderSessions([]providerapi.Provider{implementation}, 4)
	if err != nil {
		t.Fatal(err)
	}
	factory.helpers[implementation.Descriptor().ID] = client
	session := factory.NewSession()
	t.Cleanup(func() { _ = session.Close() })

	filenameIdentity := search.Identity{
		RequestID: "req_hhhhhhhhhhhhhhhhhhhhhhhhhh", EndpointID: implementation.Descriptor().ID,
		SessionID: snapshot.SessionID, EndpointGeneration: snapshot.Capabilities.Revision.Generation, UIGeneration: 4,
		Scope:   domain.Location{EndpointID: implementation.Descriptor().ID, Path: domain.CanonicalPath(root)},
		Options: search.Options{Pattern: "level-one", Target: search.MatchName, CaseSensitive: true, Symlinks: search.SymlinkNever, Ignore: search.IgnoreNone, Types: search.TypeFilter{Files: true}},
		Budget:  search.Budget{PageItems: 2, EventBuffer: 2, ConcurrentLists: 1, MaxDepth: 8, MaxEntries: 64, MaxResults: 16, MaxOutputBytes: 64 * 1024, MaxDuration: time.Second},
	}
	filenameEvents := collectFilenameSearch(t, session, filenameIdentity)
	if len(filenameEvents) != 2 || filenameEvents[0].Kind != search.EventResult || filenameEvents[0].Result.RelativePath != "nested/level-one.txt" || filenameEvents[1].Terminal.Status != search.StatusComplete {
		t.Fatalf("Level 1 filename events = %#v", filenameEvents)
	}
	if !search.EventCurrent(filenameIdentity, filenameEvents[0]) {
		t.Fatal("Level 1 filename result lost its exact request identity")
	}

	contentIdentity := search.ContentIdentity{
		RequestID: "req_iiiiiiiiiiiiiiiiiiiiiiiiii", EndpointID: implementation.Descriptor().ID,
		SessionID: snapshot.SessionID, EndpointGeneration: snapshot.Capabilities.Revision.Generation, UIGeneration: 5,
		Scope:   domain.Location{EndpointID: implementation.Descriptor().ID, Path: domain.CanonicalPath(root)},
		Options: search.ContentOptions{Pattern: "helper needle", PatternType: search.PatternLiteral, CaseSensitive: true, Binary: search.BinarySkip},
		Budget:  search.ContentBudget{PageItems: 2, EventBuffer: 2, MaxDepth: 8, MaxEntries: 64, MaxFiles: 16, MaxResults: 16, MaxMatchesPerFile: 4, MaxFileBytes: 1024, MaxReadBytes: 4096, MaxSnippetBytes: 128, MaxOutputBytes: 4096, MaxDuration: time.Second},
	}
	contentEvents := collectContentSearch(t, session, contentIdentity)
	if len(contentEvents) != 2 || contentEvents[0].Kind != search.ContentEventResult || contentEvents[0].Result.RelativePath != "nested/level-one.txt" || contentEvents[0].Result.Line != 2 || contentEvents[1].Terminal.Status != search.StatusComplete {
		t.Fatalf("Level 1 content events = %#v", contentEvents)
	}
	if !search.ContentEventCurrent(contentIdentity, contentEvents[0]) {
		t.Fatal("Level 1 content result lost its exact request identity")
	}
}

func TestProviderSessionFallsBackToLevel0AfterHelperSessionCloses(t *testing.T) {
	implementation, _ := newSearchLocalProvider(t)
	snapshot, err := implementation.Snapshot(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	client := newDaemonHelperClient(t)
	factory, err := NewProviderSessions([]providerapi.Provider{implementation}, 4)
	if err != nil {
		t.Fatal(err)
	}
	factory.helpers[implementation.Descriptor().ID] = client
	session := factory.NewSession()
	t.Cleanup(func() { _ = session.Close() })
	if err := client.Close(); err != nil {
		t.Fatal(err)
	}

	identity := search.Identity{
		RequestID: "req_jjjjjjjjjjjjjjjjjjjjjjjjjj", EndpointID: implementation.Descriptor().ID,
		SessionID: snapshot.SessionID, EndpointGeneration: snapshot.Capabilities.Revision.Generation, UIGeneration: 6,
		Scope:   domain.Location{EndpointID: implementation.Descriptor().ID, Path: "/"},
		Options: search.Options{Pattern: "file", Target: search.MatchName, CaseSensitive: true, Symlinks: search.SymlinkNever, Ignore: search.IgnoreNone, Types: search.TypeFilter{Files: true}},
		Budget:  search.Budget{PageItems: 2, EventBuffer: 2, ConcurrentLists: 1, MaxDepth: 8, MaxEntries: 64, MaxResults: 16, MaxOutputBytes: 64 * 1024, MaxDuration: time.Second},
	}
	events := collectFilenameSearch(t, session, identity)
	if len(events) != 2 || events[0].Kind != search.EventResult || events[0].Result.RelativePath != "file" || events[1].Terminal.Status != search.StatusComplete {
		t.Fatalf("Level 0 fallback events = %#v", events)
	}
	response := handlePayload[ipc.ProviderSnapshotResponse](t, session, ProviderSnapshot, ipc.ProviderSnapshotRequest{EndpointID: string(implementation.Descriptor().ID)})
	if response.EndpointID != string(implementation.Descriptor().ID) || response.SessionID != string(snapshot.SessionID) {
		t.Fatalf("Provider snapshot after Helper failure = %#v", response)
	}
}

func newDaemonHelperClient(t *testing.T) *helperruntime.Client {
	t.Helper()
	server, clientConnection := net.Pipe()
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		done <- helperruntime.Serve(ctx, server, server, helperruntime.NewLocalServiceConfig(helperruntime.Version{Major: 4}))
	}()
	client, err := helperruntime.NewClient(context.Background(), clientConnection, clientConnection, helperruntime.ClientHello{
		MinimumProtocol: 1, MaximumProtocol: 1, MaximumFrame: helperruntime.MaxHelperFrameBytes, MaximumConcurrent: 2,
		ClientVersion: helperruntime.Version{Major: 4},
		Capabilities: []helperruntime.CapabilityRequest{
			{Name: helperruntime.CapabilityFilenameSearch, MaximumVersion: 1},
			{Name: helperruntime.CapabilityContentSearch, MaximumVersion: 1},
		},
	})
	if err != nil {
		cancel()
		_ = server.Close()
		_ = clientConnection.Close()
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = client.Close()
		cancel()
		_ = server.Close()
		select {
		case <-done:
		case <-time.After(time.Second):
			t.Error("Helper service did not stop")
		}
	})
	return client
}

func collectFilenameSearch(t *testing.T, session Session, identity search.Identity) []search.Event {
	t.Helper()
	started := handlePayload[ipc.SearchFilenameStartResponse](t, session, SearchFilenameStart, ipc.SearchFilenameStartRequest{Identity: ipc.EncodeSearchIdentity(identity)})
	var events []search.Event
	for !started.Done {
		page := handlePayload[ipc.SearchFilenameNextResponse](t, session, SearchFilenameNext, ipc.SearchFilenameNextRequest{RequestID: started.RequestID, Limit: 1})
		for _, wire := range page.Events {
			event, err := ipc.DecodeSearchEvent(wire)
			if err != nil {
				t.Fatal(err)
			}
			events = append(events, event)
		}
		started.Done = page.Done
	}
	return events
}

func collectContentSearch(t *testing.T, session Session, identity search.ContentIdentity) []search.ContentEvent {
	t.Helper()
	started := handlePayload[ipc.SearchContentStartResponse](t, session, SearchContentStart, ipc.SearchContentStartRequest{Identity: ipc.EncodeContentSearchIdentity(identity)})
	var events []search.ContentEvent
	for !started.Done {
		page := handlePayload[ipc.SearchContentNextResponse](t, session, SearchContentNext, ipc.SearchContentNextRequest{RequestID: started.RequestID, Limit: 1})
		for _, wire := range page.Events {
			event, err := ipc.DecodeContentSearchEvent(wire)
			if err != nil {
				t.Fatal(err)
			}
			events = append(events, event)
		}
		started.Done = page.Done
	}
	return events
}
