//go:build darwin || linux

package app

import (
	"sync"
	"testing"

	builtinpreview "github.com/TyrantLucifer/awesome-sftp-cli/internal/preview"
)

func TestImageProbeCandidateUsesHintsOnlyToChooseAnActiveProbe(t *testing.T) {
	tests := []struct {
		environment map[string]string
		want        builtinpreview.ImageProtocol
	}{
		{map[string]string{"TERM": "xterm-kitty"}, builtinpreview.ImageProtocolKitty},
		{map[string]string{"TERM": "xterm", "KITTY_WINDOW_ID": "7"}, builtinpreview.ImageProtocolKitty},
		{map[string]string{"TERM": "xterm-256color", "TERM_PROGRAM": "iTerm.app"}, builtinpreview.ImageProtocolITerm2},
		{map[string]string{"TERM": "xterm-sixel"}, builtinpreview.ImageProtocolSixel},
		{map[string]string{"TERM": "xterm-256color"}, builtinpreview.ImageProtocolNone},
		{map[string]string{"TERM": "dumb"}, builtinpreview.ImageProtocolNone},
	}
	for _, test := range tests {
		if got := imageProbeCandidate(test.environment); got != test.want {
			t.Fatalf("imageProbeCandidate(%v) = %q, want %q", test.environment, got, test.want)
		}
	}
}

func TestImageResponseProtocolNeverTreatsPlainInputAsCapability(t *testing.T) {
	for _, response := range [][]byte{
		[]byte("xterm-kitty"),
		[]byte("\x1b_Gi=31;OK\x1b\\"),
		[]byte("\x1bP>|iTerm2 3.5\x1b\\"),
		[]byte("\x1b[?1;4c"),
	} {
		protocol := imageResponseProtocol(response)
		if string(response) == "xterm-kitty" && protocol != builtinpreview.ImageProtocolNone {
			t.Fatalf("plain environment hint became proof: %q", protocol)
		}
		if string(response) != "xterm-kitty" && protocol == builtinpreview.ImageProtocolNone {
			t.Fatalf("bounded probe response was not classified: %q", response)
		}
	}
}

func TestReprobeTerminalImageCapabilityClearsStaleProofWhenNoProtocolIsCandidate(t *testing.T) {
	proof, err := builtinpreview.ConfirmImageCapability(builtinpreview.ImageProtocolKitty, []byte("\x1b_Gi=31;OK\x1b\\"))
	if err != nil {
		t.Fatal(err)
	}
	state := newTerminalImageCapabilityState(proof)

	state.Reprobe([]string{"TERM=dumb"})

	if got := state.Current().Protocol(); got != builtinpreview.ImageProtocolNone {
		t.Fatalf("re-probed protocol = %q, want none", got)
	}
}

func TestTerminalImageCapabilityStateSynchronizesPreviewReadsWithReprobe(t *testing.T) {
	proof, err := builtinpreview.ConfirmImageCapability(builtinpreview.ImageProtocolKitty, []byte("\x1b_Gi=31;OK\x1b\\"))
	if err != nil {
		t.Fatal(err)
	}
	state := newTerminalImageCapabilityState(proof)

	start := make(chan struct{})
	var workers sync.WaitGroup
	workers.Add(2)
	go func() {
		defer workers.Done()
		<-start
		for range 1_000 {
			protocol := state.Current().Protocol()
			if protocol != builtinpreview.ImageProtocolKitty && protocol != builtinpreview.ImageProtocolNone {
				t.Errorf("current protocol = %q", protocol)
				return
			}
		}
	}()
	go func() {
		defer workers.Done()
		<-start
		for range 1_000 {
			state.Reprobe([]string{"TERM=dumb"})
		}
	}()
	close(start)
	workers.Wait()

	if got := state.Current().Protocol(); got != builtinpreview.ImageProtocolNone {
		t.Fatalf("final protocol = %q, want none", got)
	}
}
