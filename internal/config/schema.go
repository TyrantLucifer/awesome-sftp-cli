package config

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"
	"unicode/utf8"
)

const (
	SchemaVersion = 1

	protocolMaxFrameBytes    uint32 = 8 * 1024 * 1024
	hardMaxPageSize          uint32 = 4096
	maxExternalCommands             = 32
	maxExternalArguments            = 128
	maxExternalItemBytes            = 4096
	maxExternalCommandBytes         = 32768
	maxExternalRuleNameBytes        = 128
	maxExternalMatchItems           = 64
	maxExternalTimeoutMS     int64  = 120_000
	maxExternalInputBytes    int64  = 1 << 30
)

type Config struct {
	SchemaVersion int            `json:"schema_version"`
	IPC           IPCConfig      `json:"ipc"`
	Listing       ListingConfig  `json:"listing"`
	External      ExternalConfig `json:"external,omitempty"`
}

type IPCConfig struct {
	MaxFrameBytes uint32 `json:"max_frame_bytes"`
}

type ListingConfig struct {
	DefaultPageSize uint32 `json:"default_page_size"`
	MaxPageSize     uint32 `json:"max_page_size"`
}

type CommandConfig struct {
	Executable string   `json:"executable"`
	Args       []string `json:"argv"`
}

type ExternalConfig struct {
	Editor     *CommandConfig    `json:"editor,omitempty"`
	Opener     *CommandConfig    `json:"opener,omitempty"`
	Previewers []PreviewerConfig `json:"previewers,omitempty"`
}

type PreviewerConfig struct {
	Name            string        `json:"name"`
	MediaTypes      []string      `json:"media_types,omitempty"`
	Extensions      []string      `json:"extensions,omitempty"`
	Command         CommandConfig `json:"command"`
	TimeoutMS       int64         `json:"timeout_ms"`
	MaxInputBytes   int64         `json:"max_input_bytes"`
	RequireComplete bool          `json:"require_complete"`
}

func Default() Config {
	return Config{
		SchemaVersion: SchemaVersion,
		IPC: IPCConfig{
			MaxFrameBytes: protocolMaxFrameBytes,
		},
		Listing: ListingConfig{
			DefaultPageSize: 256,
			MaxPageSize:     hardMaxPageSize,
		},
	}
}

func Decode(r io.Reader) (Config, error) {
	decoder := json.NewDecoder(r)
	decoder.DisallowUnknownFields()

	var config Config
	if err := decoder.Decode(&config); err != nil {
		return Config{}, fmt.Errorf("decode config: %w", err)
	}

	var trailing json.RawMessage
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		if err == nil {
			return Config{}, errors.New("decode config: trailing JSON value")
		}
		return Config{}, fmt.Errorf("decode config trailing data: %w", err)
	}

	if err := config.Validate(); err != nil {
		return Config{}, fmt.Errorf("validate config: %w", err)
	}
	return config, nil
}

func (c Config) Validate() error {
	if c.SchemaVersion != SchemaVersion {
		return fmt.Errorf("schema_version %d is unsupported; want %d", c.SchemaVersion, SchemaVersion)
	}
	if c.IPC.MaxFrameBytes == 0 {
		return errors.New("ipc.max_frame_bytes must be greater than zero")
	}
	if c.IPC.MaxFrameBytes > protocolMaxFrameBytes {
		return fmt.Errorf("ipc.max_frame_bytes %d exceeds protocol maximum %d", c.IPC.MaxFrameBytes, protocolMaxFrameBytes)
	}
	if c.Listing.DefaultPageSize == 0 {
		return errors.New("listing.default_page_size must be greater than zero")
	}
	if c.Listing.MaxPageSize == 0 {
		return errors.New("listing.max_page_size must be greater than zero")
	}
	if c.Listing.MaxPageSize > hardMaxPageSize {
		return fmt.Errorf("listing.max_page_size %d exceeds hard maximum %d", c.Listing.MaxPageSize, hardMaxPageSize)
	}
	if c.Listing.DefaultPageSize > c.Listing.MaxPageSize {
		return fmt.Errorf(
			"listing.default_page_size %d exceeds listing.max_page_size %d",
			c.Listing.DefaultPageSize,
			c.Listing.MaxPageSize,
		)
	}
	if err := c.External.validate(); err != nil {
		return fmt.Errorf("external: %w", err)
	}
	return nil
}

func (config ExternalConfig) validate() error {
	if config.Editor != nil {
		if err := config.Editor.validate(); err != nil {
			return fmt.Errorf("editor: %w", err)
		}
	}
	if config.Opener != nil {
		if err := config.Opener.validate(); err != nil {
			return fmt.Errorf("opener: %w", err)
		}
	}
	if len(config.Previewers) > maxExternalCommands {
		return fmt.Errorf("previewer count exceeds %d", maxExternalCommands)
	}
	names := make(map[string]struct{}, len(config.Previewers))
	for index, previewer := range config.Previewers {
		if previewer.Name == "" || len(previewer.Name) > maxExternalRuleNameBytes || !utf8.ValidString(previewer.Name) || hasASCIIControl(previewer.Name) {
			return fmt.Errorf("previewer %d name is invalid", index)
		}
		if _, duplicate := names[previewer.Name]; duplicate {
			return fmt.Errorf("previewer %d name %q is duplicated", index, previewer.Name)
		}
		names[previewer.Name] = struct{}{}
		if len(previewer.MediaTypes) == 0 && len(previewer.Extensions) == 0 {
			return fmt.Errorf("previewer %d match is empty", index)
		}
		if len(previewer.MediaTypes)+len(previewer.Extensions) > maxExternalMatchItems {
			return fmt.Errorf("previewer %d match item count exceeds %d", index, maxExternalMatchItems)
		}
		items := append(append([]string(nil), previewer.MediaTypes...), previewer.Extensions...)
		for _, item := range items {
			if item == "" || len(item) > maxExternalItemBytes || !utf8.ValidString(item) || hasASCIIControl(item) {
				return fmt.Errorf("previewer %d match item is invalid", index)
			}
		}
		for _, extension := range previewer.Extensions {
			if !strings.HasPrefix(extension, ".") || strings.ContainsAny(extension, `/\\`) {
				return fmt.Errorf("previewer %d extension %q is invalid", index, extension)
			}
		}
		if err := previewer.Command.validate(); err != nil {
			return fmt.Errorf("previewer %d command: %w", index, err)
		}
		if previewer.TimeoutMS <= 0 || previewer.TimeoutMS > maxExternalTimeoutMS {
			return fmt.Errorf("previewer %d timeout_ms must be in [1,%d]", index, maxExternalTimeoutMS)
		}
		if previewer.MaxInputBytes <= 0 || previewer.MaxInputBytes > maxExternalInputBytes {
			return fmt.Errorf("previewer %d max_input_bytes must be in [1,%d]", index, maxExternalInputBytes)
		}
	}
	return nil
}

func (command CommandConfig) validate() error {
	if command.Executable == "" || len(command.Args) > maxExternalArguments {
		return errors.New("executable is empty or argv is too large")
	}
	total := 0
	items := append([]string{command.Executable}, command.Args...)
	for _, item := range items {
		if len(item) > maxExternalItemBytes || !utf8.ValidString(item) || hasASCIIControl(item) {
			return errors.New("command item is invalid or too large")
		}
		total += len(item)
	}
	if total > maxExternalCommandBytes {
		return fmt.Errorf("executable and argv exceed %d bytes", maxExternalCommandBytes)
	}
	return nil
}

func hasASCIIControl(value string) bool {
	for index := 0; index < len(value); index++ {
		if value[index] < 0x20 || value[index] == 0x7f {
			return true
		}
	}
	return false
}
