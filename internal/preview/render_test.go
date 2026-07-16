package preview

import (
	"encoding/base64"
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
