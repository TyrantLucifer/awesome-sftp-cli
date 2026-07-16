//go:build darwin || linux

package app

import (
	"bytes"
	"image"
	"image/color"
	"image/png"
	"os"
	"testing"

	builtinpreview "github.com/TyrantLucifer/awesome-mac-sftp/internal/preview"
)

// TestStage3RealKittyImageProtocol is an opt-in native gate. It must run as the
// foreground process inside a real Kitty window; environment hints alone cannot
// construct the proof that permits the live image write.
func TestStage3RealKittyImageProtocol(t *testing.T) {
	if os.Getenv("AMSFTP_REAL_KITTY") != "1" {
		t.Skip("set AMSFTP_REAL_KITTY=1 inside a real Kitty terminal")
	}
	if os.Getenv("KITTY_WINDOW_ID") == "" {
		t.Fatal("real Kitty gate requested without a Kitty window identity")
	}
	proof, probeErr := probeTerminalImageCapabilityResult(os.Environ())
	if proof.Protocol() != builtinpreview.ImageProtocolKitty {
		t.Fatalf("active terminal probe confirmed %q, want kitty: %v", proof.Protocol(), probeErr)
	}

	pixel := image.NewNRGBA(image.Rect(0, 0, 1, 1))
	pixel.SetNRGBA(0, 0, color.NRGBA{R: 0x2e, G: 0xcc, B: 0x71, A: 0xff})
	var payload bytes.Buffer
	if err := png.Encode(&payload, pixel); err != nil {
		t.Fatalf("encode native proof PNG: %v", err)
	}
	output, err := builtinpreview.EncodeTerminalImageWithProof(proof, "image/png", payload.Bytes(), builtinpreview.DefaultImageOutputLimits())
	if err != nil {
		t.Fatalf("encode actively proven Kitty image: %v", err)
	}
	if err := writeTerminalImage(output); err != nil {
		t.Fatalf("write actively proven Kitty image: %v", err)
	}
	t.Logf("real Kitty protocol confirmed and image emitted: payload=%d output=%d", payload.Len(), len(output))
}
