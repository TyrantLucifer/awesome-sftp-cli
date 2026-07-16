package preview

import (
	"bytes"
	"encoding/base64"
	"image"
	"image/png"
	"math"
	"strings"
	"testing"
)

func pngWithDimensions(t *testing.T, width, height int) []byte {
	t.Helper()
	var output bytes.Buffer
	if err := png.Encode(&output, image.NewNRGBA(image.Rect(0, 0, width, height))); err != nil {
		t.Fatal(err)
	}
	return output.Bytes()
}

func testPNG(t *testing.T) []byte {
	t.Helper()
	payload, err := base64.StdEncoding.DecodeString("iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAQAAAC1HAwCAAAAC0lEQVR42mNk+A8AAQUBAScY42YAAAAASUVORK5CYII=")
	if err != nil {
		t.Fatal(err)
	}
	return payload
}

func TestSelectImageProtocolRequiresConfirmedCapability(t *testing.T) {
	tests := []struct {
		name         string
		environment  map[string]string
		capabilities ImageCapabilities
		want         ImageProtocol
	}{
		{name: "kitty confirmed", environment: map[string]string{"TERM": "xterm-kitty"}, capabilities: ImageCapabilities{Kitty: true}, want: ImageProtocolKitty},
		{name: "kitty hint unconfirmed", environment: map[string]string{"KITTY_WINDOW_ID": "1"}, want: ImageProtocolNone},
		{name: "iterm confirmed", environment: map[string]string{"TERM_PROGRAM": "iTerm.app"}, capabilities: ImageCapabilities{ITerm2: true}, want: ImageProtocolITerm2},
		{name: "sixel confirmed", environment: map[string]string{"TERM": "xterm-sixel"}, capabilities: ImageCapabilities{Sixel: true}, want: ImageProtocolSixel},
		{name: "priority", environment: map[string]string{"TERM": "xterm-kitty", "TERM_PROGRAM": "iTerm.app"}, capabilities: ImageCapabilities{Kitty: true, ITerm2: true}, want: ImageProtocolKitty},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := SelectImageProtocol(test.environment, test.capabilities); got != test.want {
				t.Fatalf("protocol = %q, want %q", got, test.want)
			}
		})
	}
}

func TestConfirmImageCapabilityAcceptsOnlyBoundedExactProbeResponses(t *testing.T) {
	tests := []struct {
		protocol ImageProtocol
		response []byte
	}{
		{ImageProtocolKitty, []byte("\x1b_Gi=31;OK\x1b\\")},
		{ImageProtocolITerm2, []byte("\x1bP>|iTerm2 3.5.14\x1b\\")},
		{ImageProtocolSixel, []byte("\x1b[?1;2;4c")},
	}
	for _, test := range tests {
		proof, err := ConfirmImageCapability(test.protocol, test.response)
		if err != nil || proof.Protocol() != test.protocol {
			t.Fatalf("confirm %q = %q, %v", test.protocol, proof.Protocol(), err)
		}
		if _, err := EncodeTerminalImageWithProof(proof, "image/png", testPNG(t), DefaultImageOutputLimits()); err != nil {
			t.Fatalf("encode with %q proof: %v", test.protocol, err)
		}
	}
	for name, test := range map[string]struct {
		protocol ImageProtocol
		response []byte
	}{
		"environment hint": {ImageProtocolKitty, []byte("xterm-kitty")},
		"kitty wrong id":   {ImageProtocolKitty, []byte("\x1b_Gi=32;OK\x1b\\")},
		"sixel absent":     {ImageProtocolSixel, []byte("\x1b[?1;2c")},
		"iterm injected":   {ImageProtocolITerm2, []byte("\x1bP>|iTerm2 3.5\x1b\\\x1b[2J")},
		"oversized":        {ImageProtocolKitty, bytes.Repeat([]byte("x"), 257)},
	} {
		t.Run(name, func(t *testing.T) {
			if proof, err := ConfirmImageCapability(test.protocol, test.response); err == nil || proof.Protocol() != ImageProtocolNone {
				t.Fatalf("unsafe response accepted: proof=%q err=%v", proof.Protocol(), err)
			}
		})
	}
	if output, err := EncodeTerminalImageWithProof(ImageCapabilityProof{}, "image/png", testPNG(t), DefaultImageOutputLimits()); err == nil || len(output) != 0 {
		t.Fatalf("unconfirmed proof output=%q err=%v", output, err)
	}
}

func TestImageCapabilityProbeIsFixedAndBounded(t *testing.T) {
	for _, protocol := range []ImageProtocol{ImageProtocolKitty, ImageProtocolITerm2, ImageProtocolSixel} {
		query, err := ImageCapabilityProbe(protocol)
		if err != nil || len(query) == 0 || len(query) > 64 {
			t.Fatalf("probe %q = %q, %v", protocol, query, err)
		}
	}
	for _, protocol := range []ImageProtocol{ImageProtocolNone, ImageProtocol("unknown")} {
		if query, err := ImageCapabilityProbe(protocol); err == nil || len(query) != 0 {
			t.Fatalf("unsafe probe %q = %q, %v", protocol, query, err)
		}
	}
}

func TestEncodeKittyImageUsesBoundedChunkedAPC(t *testing.T) {
	payload := testPNG(t)
	encoded, err := EncodeTerminalImage(ImageProtocolKitty, "image/png", payload, ImageOutputLimits{MaxPayloadBytes: 6000, MaxOutputBytes: 12000, ChunkBytes: 64, MaxPixels: 100})
	if err != nil {
		t.Fatal(err)
	}
	text := string(encoded)
	if !strings.HasPrefix(text, terminalImageSafetyPrefix+"\x1b_Gf=100,a=T,q=2,m=1;") || !strings.HasSuffix(text, terminalImageSafetySuffix) || strings.Count(text, "\x1b_G") != 2 {
		t.Fatalf("kitty output framing/chunks = %q", text[:min(len(text), 80)])
	}
	if len(encoded) > 12000 {
		t.Fatalf("output bytes = %d", len(encoded))
	}
}

func TestEncodeITerm2ImageUsesExactInlineEnvelope(t *testing.T) {
	payload := testPNG(t)
	encoded, err := EncodeTerminalImage(ImageProtocolITerm2, "image/png", payload, DefaultImageOutputLimits())
	if err != nil {
		t.Fatal(err)
	}
	if got, want := string(encoded), terminalImageSafetyPrefix+"\x1b]1337;File=inline=1;size=68:"+base64.StdEncoding.EncodeToString(payload)+"\a"+terminalImageSafetySuffix; got != want {
		t.Fatalf("output = %q, want %q", got, want)
	}
}

func TestEncodeTerminalImageFailsClosedForNoneSixelWrongMediaAndBudgets(t *testing.T) {
	for _, protocol := range []ImageProtocol{ImageProtocolNone, ImageProtocol("unknown")} {
		if output, err := EncodeTerminalImage(protocol, "image/png", testPNG(t), DefaultImageOutputLimits()); err == nil || len(output) != 0 {
			t.Fatalf("protocol %q output=%q err=%v", protocol, output, err)
		}
	}
	if _, err := EncodeTerminalImage(ImageProtocol(string([]byte{0xff, 0x1b})), "image/png", testPNG(t), DefaultImageOutputLimits()); err == nil || err.Error() != "encode terminal image: unsupported protocol" {
		t.Fatalf("unsafe unsupported-protocol error = %v", err)
	}
	if output, err := EncodeTerminalImage(ImageProtocolKitty, "text/plain", []byte("secret"), DefaultImageOutputLimits()); err == nil || len(output) != 0 {
		t.Fatalf("wrong media output=%q err=%v", output, err)
	}
	limits := DefaultImageOutputLimits()
	limits.MaxPayloadBytes = 2
	if output, err := EncodeTerminalImage(ImageProtocolKitty, "image/png", []byte("png"), limits); err == nil || len(output) != 0 {
		t.Fatalf("oversize output=%q err=%v", output, err)
	}
	limits = DefaultImageOutputLimits()
	limits.MaxOutputBytes = 8
	if output, err := EncodeTerminalImage(ImageProtocolITerm2, "image/png", []byte("png"), limits); err == nil || len(output) != 0 {
		t.Fatalf("output-budget output=%q err=%v", output, err)
	}
}

func TestEncodeSixelImageUsesBoundedDCSAndNeverEmbedsTerminalInput(t *testing.T) {
	payload := testPNG(t)
	limits := DefaultImageOutputLimits()
	encoded, err := EncodeTerminalImage(ImageProtocolSixel, "image/png", payload, limits)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.HasPrefix(encoded, []byte(terminalImageSafetyPrefix+"\x1bPq")) || !bytes.HasSuffix(encoded, []byte(terminalImageSafetySuffix)) || !bytes.Contains(encoded, []byte("\x1b\\"+terminalImageSafetySuffix)) || len(encoded) > limits.MaxOutputBytes {
		t.Fatalf("sixel framing/output = %q", encoded)
	}
	if bytes.Contains(encoded, payload) {
		t.Fatal("sixel output embedded compressed source payload")
	}
}

func TestEncodeTerminalImageEnforcesPixelBudgetBeforeDecode(t *testing.T) {
	limits := DefaultImageOutputLimits()
	limits.MaxPixels = 0
	if output, err := EncodeTerminalImage(ImageProtocolKitty, "image/png", testPNG(t), limits); err == nil || len(output) != 0 {
		t.Fatalf("zero-pixel budget output=%q err=%v", output, err)
	}
	limits = DefaultImageOutputLimits()
	limits.MaxPixels = 1
	if output, err := EncodeTerminalImage(ImageProtocolSixel, "image/png", pngWithDimensions(t, 2, 1), limits); err == nil || len(output) != 0 {
		t.Fatalf("oversize pixels output=%q err=%v", output, err)
	}
}

func TestEncodeTerminalImageVerifiesPNGContent(t *testing.T) {
	valid := testPNG(t)
	corrupt := append([]byte(nil), valid...)
	corrupt[len(corrupt)-8] ^= 0xff
	for name, payload := range map[string][]byte{
		"wrong signature":    []byte("not a png"),
		"missing image data": valid[:33],
		"corrupt chunk":      corrupt,
	} {
		if output, err := EncodeTerminalImage(ImageProtocolKitty, "image/png", payload, DefaultImageOutputLimits()); err == nil || len(output) != 0 {
			t.Fatalf("%s output=%q err=%v", name, output, err)
		}
	}
}

func TestEncodeTerminalImageRejectsProjectedOutputBeforeBase64Allocation(t *testing.T) {
	payload := append(testPNG(t), bytes.Repeat([]byte("x"), 4*1024*1024-68)...)
	limits := ImageOutputLimits{MaxPayloadBytes: len(payload), MaxOutputBytes: 32, ChunkBytes: 4096, MaxPixels: 100}
	benchmark := testing.Benchmark(func(b *testing.B) {
		for range b.N {
			_, _ = EncodeTerminalImage(ImageProtocolITerm2, "image/png", payload, limits)
		}
	})
	if benchmark.AllocedBytesPerOp() > 256*1024 {
		t.Fatalf("projected rejection allocated %d bytes/op", benchmark.AllocedBytesPerOp())
	}
}

func TestProjectedImageOutputRejectsArithmeticBoundary(t *testing.T) {
	limits := ImageOutputLimits{MaxPayloadBytes: math.MaxInt, MaxOutputBytes: math.MaxInt, ChunkBytes: 1}
	if _, err := projectedImageOutputBytes(ImageProtocolKitty, math.MaxInt, math.MaxInt, limits); err == nil {
		t.Fatal("overflowing Kitty envelope projection succeeded")
	}
}

func FuzzEncodeTerminalImageIsFailClosedAndBounded(f *testing.F) {
	f.Add(uint8(0), testPNGForFuzz())
	f.Add(uint8(3), []byte("\x1bPqmalicious"))
	f.Fuzz(func(t *testing.T, protocolIndex uint8, payload []byte) {
		protocols := [...]ImageProtocol{ImageProtocolKitty, ImageProtocolITerm2, ImageProtocolSixel, ImageProtocolNone, ImageProtocol("unknown")}
		limits := ImageOutputLimits{MaxPayloadBytes: 4096, MaxOutputBytes: 16384, ChunkBytes: 64, MaxPixels: 1024}
		output, err := EncodeTerminalImage(protocols[int(protocolIndex)%len(protocols)], "image/png", payload, limits)
		if err != nil && len(output) != 0 {
			t.Fatalf("failed encoding returned %d bytes", len(output))
		}
		if len(output) > limits.MaxOutputBytes {
			t.Fatalf("output bytes = %d", len(output))
		}
	})
}

func testPNGForFuzz() []byte {
	payload, _ := base64.StdEncoding.DecodeString("iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAQAAAC1HAwCAAAAC0lEQVR42mNk+A8AAQUBAScY42YAAAAASUVORK5CYII=")
	return payload
}
