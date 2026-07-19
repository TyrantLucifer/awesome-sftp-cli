package config

import (
	"encoding/json"
	"fmt"
	"io"

	"github.com/TyrantLucifer/awesome-sftp-cli/internal/redaction"
)

const (
	EffectiveOutputVersion = 1
	RedactedValue          = redaction.Placeholder
)

type effectiveOutput struct {
	OutputVersion    int              `json:"output_version"`
	ResolutionPolicy ResolutionPolicy `json:"resolution_policy"`
	Config           Config           `json:"config"`
}

type ResolutionPolicy struct {
	Precedence        []string `json:"precedence"`
	Unsupported       []string `json:"unsupported_layers"`
	EnvironmentRole   string   `json:"environment_role"`
	HotReloadPolicy   string   `json:"hot_reload_policy"`
	JobSemanticPolicy string   `json:"job_semantic_policy"`
}

func DefaultResolutionPolicy() ResolutionPolicy {
	return ResolutionPolicy{
		Precedence:        []string{"cli_startup_selection", "workspace_state", "user_config", "built_in_defaults"},
		Unsupported:       []string{"system_config", "amsftp_environment_config"},
		EnvironmentRole:   "openssh_and_external_command_discovery_only",
		HotReloadPolicy:   "none_restart_required",
		JobSemanticPolicy: "frozen_at_plan_creation",
	}
}

func Write(w io.Writer, input Config) error {
	if err := input.Validate(); err != nil {
		return fmt.Errorf("validate config for write: %w", err)
	}
	encoder := json.NewEncoder(w)
	encoder.SetEscapeHTML(false)
	encoder.SetIndent("", "  ")
	if err := encoder.Encode(input); err != nil {
		return fmt.Errorf("encode config: %w", err)
	}
	return nil
}

func WriteRedactedEffective(w io.Writer, input Config) error {
	if err := input.Validate(); err != nil {
		return fmt.Errorf("validate effective config: %w", err)
	}

	redacted := redact(input)
	encoder := json.NewEncoder(w)
	encoder.SetEscapeHTML(false)
	encoder.SetIndent("", "  ")
	if err := encoder.Encode(effectiveOutput{
		OutputVersion: EffectiveOutputVersion, ResolutionPolicy: DefaultResolutionPolicy(), Config: redacted,
	}); err != nil {
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
		result.Args[index], _ = redaction.ExportString(redaction.Pseudonymous, input.Args[index])
	}
	return result
}
