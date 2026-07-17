package keymap

import (
	"bytes"
	"encoding/json"
	"testing"
)

func TestWriteEffectiveIsVersionedDeterministicAndContextComplete(t *testing.T) {
	overrides := []Override{{Context: ContextVisual, Input: "n", Action: ActionDown}}
	var first bytes.Buffer
	if err := WriteEffective(&first, overrides); err != nil {
		t.Fatal(err)
	}
	var second bytes.Buffer
	if err := WriteEffective(&second, overrides); err != nil {
		t.Fatal(err)
	}
	if first.String() != second.String() {
		t.Fatalf("effective keymap output is nondeterministic:\n%s\n%s", first.String(), second.String())
	}
	var output struct {
		OutputVersion int                `json:"output_version"`
		Bindings      []EffectiveBinding `json:"bindings"`
	}
	if err := json.Unmarshal(first.Bytes(), &output); err != nil {
		t.Fatal(err)
	}
	if output.OutputVersion != EffectiveOutputVersion || len(output.Bindings) != 2*len(defaults) {
		t.Fatalf("effective output = %#v", output)
	}
	assertEffectiveBinding(t, output.Bindings, EffectiveBinding{Context: ContextNormal, Input: "j", Action: ActionDown, DefaultInput: "j", Remappable: true})
	assertEffectiveBinding(t, output.Bindings, EffectiveBinding{Context: ContextVisual, Input: "n", Action: ActionDown, DefaultInput: "j", Remappable: true, Overridden: true})
	for _, item := range output.Bindings {
		if item.Context == ContextVisual && item.Input == "j" && item.Action == ActionDown {
			t.Fatal("effective visual keymap retained unreachable default input j")
		}
	}
}

func assertEffectiveBinding(t *testing.T, bindings []EffectiveBinding, want EffectiveBinding) {
	t.Helper()
	for _, item := range bindings {
		if item == want {
			return
		}
	}
	t.Fatalf("effective bindings do not contain %#v: %#v", want, bindings)
}
