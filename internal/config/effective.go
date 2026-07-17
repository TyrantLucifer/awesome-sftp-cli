package config

import (
	"encoding/json"
	"fmt"
	"io"
)

const (
	EffectiveOutputVersion = 1
	RedactedValue          = "<redacted>"
)

type effectiveOutput struct {
	OutputVersion int    `json:"output_version"`
	Config        Config `json:"config"`
}

func WriteRedactedEffective(w io.Writer, input Config) error {
	if err := input.Validate(); err != nil {
		return fmt.Errorf("validate effective config: %w", err)
	}

	redacted := redact(input)
	encoder := json.NewEncoder(w)
	encoder.SetEscapeHTML(false)
	encoder.SetIndent("", "  ")
	if err := encoder.Encode(effectiveOutput{OutputVersion: EffectiveOutputVersion, Config: redacted}); err != nil {
		return fmt.Errorf("encode effective config: %w", err)
	}
	return nil
}

func redact(input Config) Config {
	result := input
	if input.External.Editor != nil {
		command := redactCommand(*input.External.Editor)
		result.External.Editor = &command
	}
	if input.External.Opener != nil {
		command := redactCommand(*input.External.Opener)
		result.External.Opener = &command
	}
	result.External.Previewers = append([]PreviewerConfig(nil), input.External.Previewers...)
	for index := range result.External.Previewers {
		result.External.Previewers[index].Command = redactCommand(input.External.Previewers[index].Command)
	}
	return result
}

func redactCommand(input CommandConfig) CommandConfig {
	result := input
	result.Args = make([]string, len(input.Args))
	for index := range result.Args {
		result.Args[index] = RedactedValue
	}
	return result
}
