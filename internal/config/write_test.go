package config

import (
	"bytes"
	"reflect"
	"strings"
	"testing"

	"github.com/TyrantLucifer/awesome-mac-sftp/internal/keymap"
)

func TestWriteProducesValidatedRoundTrippableUnredactedConfig(t *testing.T) {
	input := Default()
	input.External.Editor = &CommandConfig{Executable: "/usr/bin/vi", Args: []string{"retain-me"}}
	input.Keymap.Bindings = []keymap.Override{{Context: keymap.ContextVisual, Input: "n", Action: keymap.ActionDown}}
	var output bytes.Buffer
	if err := Write(&output, input); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(output.String(), "retain-me") {
		t.Fatalf("config write unexpectedly redacted persisted value: %s", output.String())
	}
	decoded, err := Decode(bytes.NewReader(output.Bytes()))
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(decoded, input) {
		t.Fatalf("round trip = %#v, want %#v", decoded, input)
	}
}
