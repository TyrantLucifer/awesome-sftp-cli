package preview

import (
	"bytes"
	"strings"
	"testing"
)

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

func TestEncodeKittyImageUsesBoundedChunkedAPC(t *testing.T) {
	payload := bytes.Repeat([]byte{0xab}, 5000)
	encoded, err := EncodeTerminalImage(ImageProtocolKitty, "image/png", payload, ImageOutputLimits{MaxPayloadBytes: 6000, MaxOutputBytes: 12000, ChunkBytes: 4096})
	if err != nil {
		t.Fatal(err)
	}
	text := string(encoded)
	if !strings.HasPrefix(text, "\x1b_Gf=100,a=T,q=2,m=1;") || !strings.HasSuffix(text, "\x1b\\") || strings.Count(text, "\x1b_G") != 2 {
		t.Fatalf("kitty output framing/chunks = %q", text[:min(len(text), 80)])
	}
	if len(encoded) > 12000 {
		t.Fatalf("output bytes = %d", len(encoded))
	}
}

func TestEncodeITerm2ImageUsesExactInlineEnvelope(t *testing.T) {
	encoded, err := EncodeTerminalImage(ImageProtocolITerm2, "image/png", []byte("png"), DefaultImageOutputLimits())
	if err != nil {
		t.Fatal(err)
	}
	if got, want := string(encoded), "\x1b]1337;File=inline=1;size=3:cG5n\a"; got != want {
		t.Fatalf("output = %q, want %q", got, want)
	}
}

func TestEncodeTerminalImageFailsClosedForNoneSixelWrongMediaAndBudgets(t *testing.T) {
	for _, protocol := range []ImageProtocol{ImageProtocolNone, ImageProtocolSixel, ImageProtocol("unknown")} {
		if output, err := EncodeTerminalImage(protocol, "image/png", []byte("png"), DefaultImageOutputLimits()); err == nil || len(output) != 0 {
			t.Fatalf("protocol %q output=%q err=%v", protocol, output, err)
		}
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
