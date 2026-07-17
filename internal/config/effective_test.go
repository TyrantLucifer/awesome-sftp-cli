package config

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"
)

func TestWriteRedactedEffectiveConfigIsVersionedAndDoesNotExposeArguments(t *testing.T) {
	input := Default()
	input.External.Editor = &CommandConfig{
		Executable: "/usr/bin/vim",
		Args:       []string{"--cmd", "secret-token-stage6"},
	}

	var output bytes.Buffer
	if err := WriteRedactedEffective(&output, input); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(output.String(), "secret-token-stage6") {
		t.Fatalf("effective config exposed a command argument: %s", output.String())
	}

	var decoded struct {
		OutputVersion int    `json:"output_version"`
		Config        Config `json:"config"`
	}
	if err := json.Unmarshal(output.Bytes(), &decoded); err != nil {
		t.Fatalf("effective config is not JSON: %v", err)
	}
	if decoded.OutputVersion != EffectiveOutputVersion {
		t.Fatalf("output_version = %d, want %d", decoded.OutputVersion, EffectiveOutputVersion)
	}
	if decoded.Config.SchemaVersion != SchemaVersion {
		t.Fatalf("schema_version = %d, want %d", decoded.Config.SchemaVersion, SchemaVersion)
	}
	if got := decoded.Config.External.Editor.Args; len(got) != 2 || got[0] != RedactedValue || got[1] != RedactedValue {
		t.Fatalf("redacted argv = %#v", got)
	}
}

func TestWriteRedactedEffectiveConfigDoesNotMutateInput(t *testing.T) {
	input := Default()
	input.External.Editor = &CommandConfig{Executable: "vim", Args: []string{"original"}}

	var output bytes.Buffer
	if err := WriteRedactedEffective(&output, input); err != nil {
		t.Fatal(err)
	}
	if got := input.External.Editor.Args; len(got) != 1 || got[0] != "original" {
		t.Fatalf("input argv was mutated: %#v", got)
	}
}
