package preview

import (
	"bytes"
	"encoding/base64"
	"fmt"
	"math"
	"strings"
	"testing"
)

func TestRenderTextIsTerminalSafeNumberedAndBounded(t *testing.T) {
	limits := DefaultLimits()
	limits.MaxRenderedLines = 2
	result := Render(Request{Path: "notes.txt", Data: []byte("first\nsecond\x1b[2J\nthird\xff"), Complete: true}, limits)
	if result.Kind != KindText || !result.Truncated || result.Lines != 2 {
		t.Fatalf("result = %#v", result)
	}
	if !strings.Contains(result.Text, "1  first") || !strings.Contains(result.Text, "2  second\\x1b[2J") {
		t.Fatalf("text = %q", result.Text)
	}
	if strings.ContainsRune(result.Text, '\x1b') || strings.ContainsRune(result.Text, '\ufffd') {
		t.Fatalf("unsafe or ambiguous text = %q", result.Text)
	}
}

func TestRenderJSONPrettyPrintsWithinDepthBudget(t *testing.T) {
	result := Render(Request{Path: "config.json", Data: []byte(`{"name":"amsftp","items":[1,true]}`), Complete: true}, DefaultLimits())
	if result.Kind != KindJSON || !strings.Contains(result.Text, "\"name\": \"amsftp\"") || !strings.Contains(result.Text, "\"items\": [") {
		t.Fatalf("result = %#v", result)
	}

	limits := DefaultLimits()
	limits.MaxJSONDepth = 2
	tooDeep := Render(Request{Path: "deep.json", Data: []byte(`{"a":{"b":{"c":1}}}`), Complete: true}, limits)
	if tooDeep.Kind != KindText || tooDeep.Warning != "JSON depth exceeds preview budget" {
		t.Fatalf("tooDeep = %#v", tooDeep)
	}
}

func TestRenderPartialJSONDoesNotPretendToBeComplete(t *testing.T) {
	result := Render(Request{Path: "large.json", Data: []byte(`{"partial":`), Complete: false, Offset: 4096}, DefaultLimits())
	if result.Kind != KindText || !result.Partial || !strings.Contains(result.Summary, "bytes 4096..") {
		t.Fatalf("result = %#v", result)
	}
}

func TestRenderInvalidCompleteJSONWarnsAboutTextFallback(t *testing.T) {
	result := Render(Request{Path: "broken.json", Data: []byte(`{"missing":}`), Complete: true}, DefaultLimits())
	if result.Kind != KindText || result.Warning != "invalid JSON; showing text fallback" {
		t.Fatalf("result = %#v", result)
	}
}

func TestRenderRejectsDeepJSONBeforeBuildingValueTree(t *testing.T) {
	limits := DefaultLimits()
	data := []byte(strings.Repeat("[", limits.MaxJSONDepth+1) + strings.Repeat("]", limits.MaxJSONDepth+1))
	result := Render(Request{Path: "deep.json", Data: data, Complete: true}, limits)
	if result.Kind != KindText || result.Warning != "JSON depth exceeds preview budget" {
		t.Fatalf("result = %#v", result)
	}
}

func TestRenderBinaryUsesBoundedHexMetadata(t *testing.T) {
	limits := DefaultLimits()
	limits.MaxOutputBytes = 80
	result := Render(Request{Path: "payload.bin", Data: []byte{0, 1, 2, 3, 0xff, 0x1b}, Complete: true}, limits)
	if result.Kind != KindBinary || !result.Binary || !strings.Contains(result.Text, "00000000") || strings.ContainsRune(result.Text, '\x1b') {
		t.Fatalf("result = %#v", result)
	}
	if len(result.Text) > limits.MaxOutputBytes {
		t.Fatalf("output bytes = %d, want <= %d", len(result.Text), limits.MaxOutputBytes)
	}
}

func TestRenderRangeHexUsesAbsolute64BitSourceOffsets(t *testing.T) {
	const offset = (uint64(100) << 30) - 16
	result := Render(Request{Path: "payload.bin", Data: []byte{0, 1, 2, 3}, Offset: offset, Complete: false}, DefaultLimits())
	if result.Kind != KindBinary || !strings.Contains(result.Text, fmt.Sprintf("%016x", offset)) {
		t.Fatalf("range hex = %#v", result)
	}
}

func TestRenderImageReturnsMetadataWithoutEmbeddingPayload(t *testing.T) {
	pngBytes, err := base64.StdEncoding.DecodeString("iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAQAAAC1HAwCAAAAC0lEQVR42mNk+A8AAQUBAScY42YAAAAASUVORK5CYII=")
	if err != nil {
		t.Fatal(err)
	}
	result := Render(Request{Path: "pixel.png", Data: pngBytes, Complete: true}, DefaultLimits())
	if result.Kind != KindImage || result.Image == nil || result.Image.Width != 1 || result.Image.Height != 1 || result.Image.MediaType != "image/png" {
		t.Fatalf("result = %#v", result)
	}
	if strings.Contains(result.Text, string(pngBytes)) {
		t.Fatal("image preview embedded raw payload")
	}
}

func TestRenderRejectsInvalidBudgetsWithoutReadingBeyondInputLimit(t *testing.T) {
	limits := DefaultLimits()
	limits.MaxInputBytes = 4
	result := Render(Request{Path: "large.txt", Data: []byte("abcdefgh"), Complete: true}, limits)
	if result.InputBytes != 4 || !result.Truncated || !strings.Contains(result.Summary, "preview budget") {
		t.Fatalf("result = %#v", result)
	}
	if got := Render(Request{Path: "x", Data: []byte("x")}, Limits{}); got.Warning == "" {
		t.Fatalf("zero limits result = %#v", got)
	}
}

func TestRenderIntermediateBuffersStayBoundedByOutputBudgets(t *testing.T) {
	binaryData := make([]byte, DefaultLimits().MaxInputBytes)
	binaryLimits := DefaultLimits()
	binaryLimits.MaxRenderedLines = 2
	binaryLimits.MaxOutputBytes = 128
	binaryBenchmark := testing.Benchmark(func(b *testing.B) {
		for range b.N {
			_ = Render(Request{Path: "large.bin", Data: binaryData, Complete: true}, binaryLimits)
		}
	})
	if binaryBenchmark.AllocedBytesPerOp() > 256*1024 {
		t.Fatalf("bounded binary render allocated %d bytes/op", binaryBenchmark.AllocedBytesPerOp())
	}

	lineData := []byte(strings.Repeat("x", DefaultLimits().MaxInputBytes))
	lineLimits := DefaultLimits()
	lineLimits.MaxRenderedLines = 1
	lineLimits.MaxOutputBytes = 32
	lineBenchmark := testing.Benchmark(func(b *testing.B) {
		for range b.N {
			_, _, _ = renderNumberedLines(lineData, lineLimits, false)
		}
	})
	if lineBenchmark.AllocedBytesPerOp() > 256*1024 {
		t.Fatalf("bounded line render allocated %d bytes/op", lineBenchmark.AllocedBytesPerOp())
	}
}

func TestRenderRawJSONAndMetadataViewsArePureAndBounded(t *testing.T) {
	data := []byte(`{"name":"amsftp","count":2}`)
	raw := Render(Request{Path: "config.json", Data: data, Complete: true, View: ViewRawJSON}, DefaultLimits())
	if raw.Kind != KindJSON || raw.View != ViewRawJSON || !strings.Contains(raw.Text, `{"name":"amsftp","count":2}`) || strings.Contains(raw.Text, `"name": "amsftp"`) {
		t.Fatalf("raw = %#v", raw)
	}
	metadata := Render(Request{Path: "config.json", Data: data, Complete: true, View: ViewMetadata, FileSize: uint64(len(data)), HasFileSize: true}, DefaultLimits())
	if metadata.Kind != KindMetadata || metadata.View != ViewMetadata || metadata.Metadata == nil || metadata.Metadata.MediaType != "application/json" || metadata.Metadata.FileSize != uint64(len(data)) {
		t.Fatalf("metadata = %#v", metadata)
	}
	if strings.Contains(metadata.Text, "amsftp") || len(metadata.Text) > DefaultLimits().MaxOutputBytes {
		t.Fatalf("metadata leaked content or exceeded output budget: %q", metadata.Text)
	}
	if got := ToggleView(ViewAuto, true); got != ViewRawJSON || ToggleView(got, true) != ViewMetadata || ToggleView(ViewMetadata, true) != ViewAuto || ToggleView(ViewAuto, false) != ViewMetadata {
		t.Fatalf("toggle sequence is not deterministic: first=%q", got)
	}
}

func TestRenderImageMetadataViewIncludesDimensionsWithoutPayload(t *testing.T) {
	payload := testPNG(t)
	result := Render(Request{Path: "pixel.png", Data: payload, Complete: true, View: ViewMetadata, HasFileSize: true, FileSize: uint64(len(payload))}, DefaultLimits())
	if result.Kind != KindMetadata || result.Metadata == nil || result.Metadata.Width != 1 || result.Metadata.Height != 1 || result.Metadata.MediaType != "image/png" {
		t.Fatalf("metadata = %#v", result)
	}
	if strings.Contains(result.Text, string(payload)) {
		t.Fatal("metadata embedded image payload")
	}
}

func TestRenderCodeReturnsBoundedANSIIndependentSyntaxSpans(t *testing.T) {
	limits := DefaultLimits()
	limits.MaxStyleSpans = 8
	result := Render(Request{Path: "main.go", Data: []byte("package main\n// hostile \x1b[2J\nconst answer = 42\n"), Complete: true}, limits)
	if result.Kind != KindCode || len(result.Styles) == 0 || len(result.Styles) > limits.MaxStyleSpans {
		t.Fatalf("result = %#v", result)
	}
	if strings.ContainsRune(result.Text, '\x1b') {
		t.Fatalf("terminal control survived: %q", result.Text)
	}
	for _, span := range result.Styles {
		if span.Start < 0 || span.End <= span.Start || span.End > len(result.Text) || span.Class == SyntaxPlain {
			t.Fatalf("invalid style span %#v for %d-byte output", span, len(result.Text))
		}
	}
}

func TestRenderRejectsOverflowingRangeAndUnknownView(t *testing.T) {
	overflow := Render(Request{Path: "x.txt", Data: []byte("ab"), Offset: math.MaxUint64, Complete: false}, DefaultLimits())
	if overflow.Warning != "preview source range overflows" || overflow.Text != "" {
		t.Fatalf("overflow = %#v", overflow)
	}
	unknown := Render(Request{Path: "x.txt", Data: []byte("x"), Complete: true, View: ViewMode(string([]byte{0xff, 0x1b}))}, DefaultLimits())
	if unknown.Warning != "unsupported preview view" || unknown.Text != "" || strings.ContainsRune(unknown.Warning, '\x1b') {
		t.Fatalf("unknown = %#v", unknown)
	}
}

func TestRenderRejectsInconsistentCompleteAndUnavailableRawViews(t *testing.T) {
	inconsistent := Render(Request{Path: "x.txt", Data: []byte("x"), Offset: 1, Complete: true}, DefaultLimits())
	if inconsistent.Warning != "complete preview must start at offset zero" || inconsistent.Text != "" {
		t.Fatalf("inconsistent = %#v", inconsistent)
	}
	rawText := Render(Request{Path: "x.txt", Data: []byte("not json"), Complete: true, View: ViewRawJSON}, DefaultLimits())
	if rawText.Warning != "raw JSON view unavailable; showing text fallback" || rawText.Kind != KindText {
		t.Fatalf("raw text = %#v", rawText)
	}
}

func TestRenderSanitizationAllocationFollowsOutputNotExpansion(t *testing.T) {
	limits := DefaultLimits()
	limits.MaxOutputBytes = 32
	data := bytes.Repeat([]byte{0x1b}, limits.MaxInputBytes)
	benchmark := testing.Benchmark(func(b *testing.B) {
		for range b.N {
			_ = Render(Request{Path: "hostile.txt", Data: data, Complete: true}, limits)
		}
	})
	if benchmark.AllocedBytesPerOp() > 128*1024 {
		t.Fatalf("sanitization allocated %d bytes/op for %d-byte output budget", benchmark.AllocedBytesPerOp(), limits.MaxOutputBytes)
	}
}

func FuzzRenderIsTerminalSafeAndOutputBounded(f *testing.F) {
	f.Add("hostile.go", []byte("const x = \x1b[2J\xff\n"), uint64(0), true, uint8(0))
	f.Add("data.json", []byte(`{"x":[1,2]}`), uint64(0), true, uint8(1))
	f.Fuzz(func(t *testing.T, path string, data []byte, offset uint64, complete bool, viewIndex uint8) {
		views := [...]ViewMode{ViewAuto, ViewRawJSON, ViewMetadata}
		limits := DefaultLimits()
		limits.MaxInputBytes = 4096
		limits.MaxOutputBytes = 8192
		result := Render(Request{Path: path, Data: data, Offset: offset, Complete: complete, View: views[int(viewIndex)%len(views)]}, limits)
		if len(result.Text) > limits.MaxOutputBytes {
			t.Fatalf("output bytes = %d", len(result.Text))
		}
		if strings.ContainsRune(result.Text, '\x1b') {
			t.Fatalf("terminal escape survived in %q", result.Text)
		}
		if len(result.Styles) > limits.MaxStyleSpans {
			t.Fatalf("style spans = %d", len(result.Styles))
		}
		for _, span := range result.Styles {
			if span.Start < 0 || span.End <= span.Start || span.End > len(result.Text) {
				t.Fatalf("invalid style span %#v", span)
			}
		}
	})
}
