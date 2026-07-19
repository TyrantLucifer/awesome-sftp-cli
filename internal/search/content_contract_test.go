package search

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/TyrantLucifer/awesome-sftp-cli/internal/domain"
	"github.com/TyrantLucifer/awesome-sftp-cli/internal/provider/localfs"
)

func TestLevel0ContentSearchStreamsBoundedLiteralMatches(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "src"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "src", "a.txt"), []byte("first\nTarget value\nlast\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "src", "binary.txt"), []byte{'T', 'a', 0, 'r'}, 0o600); err != nil {
		t.Fatal(err)
	}
	implementation, err := localfs.New(localfs.Config{
		Endpoint:  domain.Endpoint{ID: searchEndpointID, Kind: domain.EndpointLocal, DisplayName: "content"},
		SessionID: searchSessionID, Root: root, MaxCursors: 8,
	})
	if err != nil {
		t.Fatal(err)
	}
	defer implementation.Close()

	request := contentContractRequest()
	events, err := StartContent(context.Background(), implementation, request)
	if err != nil {
		t.Fatal(err)
	}
	var matches []ContentResult
	var terminal ContentTerminal
	for event := range events {
		if event.Identity != request.Identity {
			t.Fatalf("event identity changed: %#v", event.Identity)
		}
		switch event.Kind {
		case ContentEventResult:
			matches = append(matches, event.Result)
		case ContentEventTerminal:
			terminal = event.Terminal
		}
	}
	if len(matches) != 1 || matches[0].RelativePath != "src/a.txt" || matches[0].Line != 2 || matches[0].Offset != 6 || matches[0].Snippet != "Target value" {
		t.Fatalf("matches = %#v", matches)
	}
	if terminal.Status != StatusPartial || terminal.StopReason != StopBinarySkipped || terminal.BytesRead > request.Identity.Budget.MaxReadBytes {
		t.Fatalf("terminal = %#v", terminal)
	}
}

func TestLevel0ContentSearchReportsPerFileLimitAsPartial(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "large.txt"), []byte("0123456789target"), 0o600); err != nil {
		t.Fatal(err)
	}
	implementation, err := localfs.New(localfs.Config{
		Endpoint:  domain.Endpoint{ID: searchEndpointID, Kind: domain.EndpointLocal, DisplayName: "content"},
		SessionID: searchSessionID, Root: root, MaxCursors: 8,
	})
	if err != nil {
		t.Fatal(err)
	}
	defer implementation.Close()
	request := contentContractRequest()
	request.Identity.Budget.MaxFileBytes = 8
	terminal := contentTerminalEvent(t, implementation, request)
	if terminal.Status != StatusPartial || terminal.StopReason != StopFileByteLimit || terminal.Results != 0 || terminal.BytesRead != 8 {
		t.Fatalf("terminal = %#v", terminal)
	}
}

func TestLevel0ContentSearchDoesNotMisreportExactKnownFileSizeAsTruncated(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "exact.txt"), []byte("target!!"), 0o600); err != nil {
		t.Fatal(err)
	}
	implementation, err := localfs.New(localfs.Config{
		Endpoint:  domain.Endpoint{ID: searchEndpointID, Kind: domain.EndpointLocal, DisplayName: "content"},
		SessionID: searchSessionID, Root: root, MaxCursors: 8,
	})
	if err != nil {
		t.Fatal(err)
	}
	defer implementation.Close()
	request := contentContractRequest()
	request.Identity.Budget.MaxFileBytes = 8
	terminal := contentTerminalEvent(t, implementation, request)
	if terminal.Status != StatusComplete || terminal.StopReason != StopNone || terminal.Results != 1 || terminal.BytesRead != 8 {
		t.Fatalf("terminal = %#v", terminal)
	}
}

func contentContractRequest() ContentRequest {
	return ContentRequest{Identity: ContentIdentity{
		RequestID: "req_yyyyyyyyyyyyyyyyyyyyyyyyyy", EndpointID: searchEndpointID, SessionID: searchSessionID,
		EndpointGeneration: 1, UIGeneration: 12,
		Scope:   domain.Location{EndpointID: searchEndpointID, Path: "/"},
		Options: ContentOptions{Pattern: "target", PatternType: PatternLiteral, CaseSensitive: false, Binary: BinarySkip, ContextLines: 0, FileNameContains: ".txt"},
		Budget:  ContentBudget{PageItems: 2, EventBuffer: 2, MaxDepth: 8, MaxEntries: 64, MaxFiles: 16, MaxResults: 16, MaxMatchesPerFile: 4, MaxFileBytes: 64, MaxReadBytes: 256, MaxSnippetBytes: 32, MaxOutputBytes: 1024, MaxDuration: time.Second},
	}}
}

func contentTerminalEvent(t *testing.T, implementation *localfs.Provider, request ContentRequest) ContentTerminal {
	t.Helper()
	events, err := StartContent(context.Background(), implementation, request)
	if err != nil {
		t.Fatal(err)
	}
	var terminal ContentTerminal
	for event := range events {
		if event.Kind == ContentEventTerminal {
			terminal = event.Terminal
		}
	}
	return terminal
}
