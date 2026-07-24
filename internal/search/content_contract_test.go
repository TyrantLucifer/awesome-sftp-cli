package search

import (
	"bytes"
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

func TestContentSearchProducerReturnsAfterTimeoutWhenTerminalBufferIsFull(t *testing.T) {
	implementation := newFilenameContractProvider(t)
	request := contentContractRequest()
	request.Identity.Budget.MaxDuration = 20 * time.Millisecond
	events := make(chan ContentEvent, 1)
	events <- ContentEvent{Kind: ContentEventResult}

	done := make(chan struct{})
	go func() {
		runContent(context.Background(), implementation, request.Identity, events)
		close(done)
	}()

	select {
	case <-done:
		return
	case <-time.After(2 * time.Second):
		// Unblock the old implementation before failing so the test itself does
		// not leave a goroutine behind.
		<-events
		<-done
		t.Fatal("content search producer blocked sending its terminal event after its deadline")
	}
}

func TestContentSearchCancellationStillReportsTerminalWhenStreamCanAcceptIt(t *testing.T) {
	implementation := newFilenameContractProvider(t)
	request := contentContractRequest()
	events := make(chan ContentEvent, 1)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	runContent(ctx, implementation, request.Identity, events)
	event, ok := <-events
	if !ok || event.Kind != ContentEventTerminal || event.Terminal.Status != StatusCanceled || event.Terminal.StopReason != StopCanceled {
		t.Fatalf("canceled content terminal = (%#v, %t)", event, ok)
	}
	if _, ok := <-events; ok {
		t.Fatal("content event stream remained open after its terminal event")
	}
}

func TestContentLineCursorPreservesLineBoundariesWhileMovingForward(t *testing.T) {
	data := []byte("zero\none\n\nthree")
	cursor := newContentLineCursor(data)
	tests := []struct {
		offset    int
		lineStart int
		lineEnd   int
		line      uint64
	}{
		{offset: 0, lineStart: 0, lineEnd: 4, line: 1},
		{offset: 4, lineStart: 0, lineEnd: 4, line: 1},
		{offset: 5, lineStart: 5, lineEnd: 8, line: 2},
		{offset: 8, lineStart: 5, lineEnd: 8, line: 2},
		{offset: 9, lineStart: 9, lineEnd: 9, line: 3},
		{offset: 10, lineStart: 10, lineEnd: 15, line: 4},
	}
	lastStart := -1
	for _, test := range tests {
		lineStart, lineEnd, line := cursor.span(test.offset)
		if lineStart != test.lineStart || lineEnd != test.lineEnd || line != test.line {
			t.Fatalf("span(%d) = (%d, %d, %d), want (%d, %d, %d)", test.offset, lineStart, lineEnd, line, test.lineStart, test.lineEnd, test.line)
		}
		if lineStart < lastStart {
			t.Fatalf("line cursor moved backward from %d to %d", lastStart, lineStart)
		}
		lastStart = lineStart
	}

	reference := []byte("zero\r\none\n\nπ three\nlast")
	cursor = newContentLineCursor(reference)
	for offset := range reference {
		wantStart := bytes.LastIndexByte(reference[:offset], '\n') + 1
		wantEnd := len(reference)
		if relativeEnd := bytes.IndexByte(reference[offset:], '\n'); relativeEnd >= 0 {
			wantEnd = offset + relativeEnd
		}
		wantLine := uint64(bytes.Count(reference[:offset], []byte{'\n'})) + 1 // #nosec G115 -- the fixed test input is far smaller than uint64.
		lineStart, lineEnd, line := cursor.span(offset)
		if lineStart != wantStart || lineEnd != wantEnd || line != wantLine {
			t.Fatalf("reference span(%d) = (%d, %d, %d), want (%d, %d, %d)", offset, lineStart, lineEnd, line, wantStart, wantEnd, wantLine)
		}
	}

	dense := bytes.Repeat([]byte("x\n"), 8_192)
	cursor = newContentLineCursor(dense)
	for offset := 0; offset < len(dense); offset += 2 {
		lineStart, lineEnd, line := cursor.span(offset)
		if lineStart != offset || lineEnd != offset+1 || line != uint64(offset/2+1) {
			t.Fatalf("dense span(%d) = (%d, %d, %d)", offset, lineStart, lineEnd, line)
		}
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
