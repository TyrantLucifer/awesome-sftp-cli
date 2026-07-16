package tui

import (
	"bytes"
	"strings"
	"testing"
)

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

func TestEscapeClosesAndCancelsPreview(t *testing.T) {
	model := testModel(t)
	file := model.Panes[Left].visibleEntry(1).Location
	model, _ = Reduce(model, BeginPreview{Generation: 5, Location: file})
	model, intents := Reduce(model, KeyPress{Key: KeyEscape})
	if model.Preview.Generation != 0 || len(intents) != 1 || intents[0].Kind != IntentPreviewCancel {
		t.Fatalf("escaped preview model=%#v intents=%#v", model.Preview, intents)
	}
}
