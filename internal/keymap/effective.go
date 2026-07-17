package keymap

import (
	"encoding/json"
	"fmt"
	"io"
)

const EffectiveOutputVersion = 1

type EffectiveBinding struct {
	Context      Context `json:"context"`
	Input        string  `json:"input"`
	Action       Action  `json:"action"`
	DefaultInput string  `json:"default_input"`
	Remappable   bool    `json:"remappable"`
	Overridden   bool    `json:"overridden"`
}

type effectiveOutput struct {
	OutputVersion int                `json:"output_version"`
	Bindings      []EffectiveBinding `json:"bindings"`
}

func WriteEffective(w io.Writer, overrides []Override) error {
	mapping, err := New(overrides)
	if err != nil {
		return fmt.Errorf("validate effective keymap: %w", err)
	}
	bindings := make([]EffectiveBinding, 0, 2*len(defaults))
	for _, context := range []Context{ContextNormal, ContextVisual} {
		for _, item := range defaults {
			input := inputForAction(mapping.contexts[context], item.action)
			bindings = append(bindings, EffectiveBinding{
				Context: context, Input: input, Action: item.action, DefaultInput: item.input,
				Remappable: item.remappable, Overridden: input != item.input,
			})
		}
	}
	encoder := json.NewEncoder(w)
	encoder.SetEscapeHTML(false)
	encoder.SetIndent("", "  ")
	if err := encoder.Encode(effectiveOutput{OutputVersion: EffectiveOutputVersion, Bindings: bindings}); err != nil {
		return fmt.Errorf("encode effective keymap: %w", err)
	}
	return nil
}

func inputForAction(mapping map[string]Action, target Action) string {
	for input, action := range mapping {
		if action == target {
			return input
		}
	}
	return ""
}
