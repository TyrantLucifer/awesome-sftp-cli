package tui

import (
	"bytes"
	"strings"
	"testing"

	"github.com/TyrantLucifer/awesome-sftp-cli/internal/domain"
	builtinpreview "github.com/TyrantLucifer/awesome-sftp-cli/internal/preview"
)

func TestPreviewRejectsEveryFullIdentityMismatch(t *testing.T) {
	model := testModel(t)
	identity := previewTestIdentity(t, 7)
	model, _ = Reduce(model, BeginPreview{Generation: 7, Location: identity.Source.Location, Identity: identity})

	mutations := []func(*PreviewRequestIdentity){
		func(value *PreviewRequestIdentity) { value.RequestID = "req_bbbbbbbbbbbbbbbbbbbbbbbbbb" },
		func(value *PreviewRequestIdentity) { value.Pane = Right },
		func(value *PreviewRequestIdentity) { value.EndpointSession = "sess_bbbbbbbbbbbbbbbbbbbbbbbbbb" },
		func(value *PreviewRequestIdentity) { value.EndpointGeneration++ },
		func(value *PreviewRequestIdentity) { value.Mode = builtinpreview.ReadTail },
		func(value *PreviewRequestIdentity) { value.Offset++ },
		func(value *PreviewRequestIdentity) { value.RequestedLimit-- },
		func(value *PreviewRequestIdentity) { value.UIGeneration++ },
	}
	for _, mutate := range mutations {
		other := identity
		mutate(&other)
		model, _ = Reduce(model, PreviewChunk{Generation: 7, Identity: other, Data: []byte("stale"), Done: true})
	}
	if model.Preview.BytesRead != 0 {
		t.Fatalf("mismatched identities wrote %d bytes", model.Preview.BytesRead)
	}
	model, _ = Reduce(model, PreviewChunk{Generation: 7, Identity: identity, Data: []byte("current"), Done: true})
	if got := model.Preview.DisplayText(); got != "current" {
		t.Fatalf("current identity text = %q", got)
	}
}

func previewTestIdentity(t *testing.T, generation uint64) PreviewRequestIdentity {
	t.Helper()
	location := domain.Location{EndpointID: leftEndpointID, Path: "/left/file.txt"}
	size := uint64(7)
	source, err := builtinpreview.FreezeSource(location, domain.Fingerprint{Size: &size})
	if err != nil {
		t.Fatal(err)
	}
	return PreviewRequestIdentity{
		RequestID: "req_aaaaaaaaaaaaaaaaaaaaaaaaaa", Pane: Left,
		EndpointSession: "sess_aaaaaaaaaaaaaaaaaaaaaaaaaa", EndpointGeneration: 3,
		Source: source, Mode: builtinpreview.ReadHead, RequestedLimit: builtinpreview.ReadChunkBytes, UIGeneration: generation,
	}
}

func TestPreviewModelEnforcesHardByteLimitAndIgnoresStaleChunks(t *testing.T) {
	model := testModel(t)
	file := model.Panes[Left].visibleEntry(1).Location
	model, _ = Reduce(model, BeginPreview{Generation: 4, Location: file})
	model, _ = Reduce(model, PreviewChunk{Generation: 3, Data: []byte("stale"), Done: true})
	if model.Preview.BytesRead != 0 {
		t.Fatalf("bytes after stale chunk = %d, want 0", model.Preview.BytesRead)
	}

	payload := bytes.Repeat([]byte("x"), PreviewByteLimit+128)
	model, _ = Reduce(model, PreviewChunk{Generation: 4, Data: payload, Done: true})
	if model.Preview.BytesRead != PreviewByteLimit {
		t.Fatalf("preview bytes = %d, want %d", model.Preview.BytesRead, PreviewByteLimit)
	}
	if !model.Preview.Truncated || model.Preview.Loading {
		t.Fatalf("preview state = %#v, want truncated and complete", model.Preview)
	}
}

func TestPreviewMarksBinaryContentWithoutRenderingControls(t *testing.T) {
	model := testModel(t)
	file := model.Panes[Left].visibleEntry(1).Location
	model, _ = Reduce(model, BeginPreview{Generation: 1, Location: file})
	model, _ = Reduce(model, PreviewChunk{Generation: 1, Data: []byte{'a', 0, 'b'}, Done: true})
	if !model.Preview.Binary {
		t.Fatal("binary preview was not detected")
	}
	if got := model.Preview.DisplayText(); got != "[binary preview omitted]" {
		t.Fatalf("DisplayText() = %q", got)
	}
}

func TestPreviewAcceptsUTF8CodePointSplitAcrossChunks(t *testing.T) {
	model := testModel(t)
	file := model.Panes[Left].visibleEntry(1).Location
	model, _ = Reduce(model, BeginPreview{Generation: 1, Location: file})
	encoded := []byte("界")
	model, _ = Reduce(model, PreviewChunk{Generation: 1, Data: encoded[:2]})
	model, _ = Reduce(model, PreviewChunk{Generation: 1, Data: encoded[2:], Done: true})
	if model.Preview.Binary {
		t.Fatal("split UTF-8 code point was classified as binary")
	}
	if got := model.Preview.DisplayText(); got != "界" {
		t.Fatalf("DisplayText() = %q, want 界", got)
	}
}

func TestPreviewPreservesLineStructureWhileSanitizingEachLine(t *testing.T) {
	preview := PreviewState{Data: []byte("first\x1b[2J\r\nsecond\nthird")}
	got := preview.DisplayText()
	if got != "first�[2J\nsecond\nthird" {
		t.Fatalf("DisplayText() = %q", got)
	}
	if strings.Contains(got, "\x1b") || strings.Contains(got, "\r") {
		t.Fatalf("DisplayText() retained terminal control bytes: %q", got)
	}
}

func TestPreviewPreservesRenderedBinaryFallbackAndSummary(t *testing.T) {
	model := testModel(t)
	file := model.Panes[Left].visibleEntry(1).Location
	model, _ = Reduce(model, BeginPreview{Generation: 9, Location: file})
	model, _ = Reduce(model, PreviewChunk{
		Generation: 9, Data: []byte("1  00000000  00 ff"), Done: true,
		Rendered: true, Kind: "binary", Summary: "partial preview: bytes 0..2", Truncated: true,
	})
	if model.Preview.Binary || model.Preview.Kind != "binary" || model.Preview.Summary == "" || !strings.Contains(model.Preview.DisplayText(), "00000000") {
		t.Fatalf("preview = %#v text=%q", model.Preview, model.Preview.DisplayText())
	}
}

func TestEscapeClosesAndCancelsPreview(t *testing.T) {
	model := testModel(t)
	file := model.Panes[Left].visibleEntry(1).Location
	model, _ = Reduce(model, BeginPreview{Generation: 5, Location: file})
	model, intents := Reduce(model, KeyPress{Key: KeyEscape})
	if model.Preview.Generation != 0 || len(intents) != 1 || intents[0].Kind != IntentPreviewCancel {
		t.Fatalf("escaped preview model=%#v intents=%#v", model.Preview, intents)
	}
}
