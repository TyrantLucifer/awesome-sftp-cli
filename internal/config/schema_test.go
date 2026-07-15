package config

import (
	"strings"
	"testing"
)

const validConfigJSON = `{
  "schema_version": 1,
  "ipc": {"max_frame_bytes": 8388608},
  "listing": {"default_page_size": 256, "max_page_size": 4096}
}`

func TestDefaultConfigIsValid(t *testing.T) {
	want := Config{
		SchemaVersion: 1,
		IPC: IPCConfig{
			MaxFrameBytes: 8 * 1024 * 1024,
		},
		Listing: ListingConfig{
			DefaultPageSize: 256,
			MaxPageSize:     4096,
		},
	}

	got := Default()
	if got != want {
		t.Fatalf("Default() = %#v, want %#v", got, want)
	}
	if err := got.Validate(); err != nil {
		t.Fatalf("Default().Validate() returned error: %v", err)
	}
}

func TestDecodeAcceptsCanonicalConfig(t *testing.T) {
	got, err := Decode(strings.NewReader(validConfigJSON + "\n\t"))
	if err != nil {
		t.Fatalf("Decode() returned error: %v", err)
	}
	if got != Default() {
		t.Fatalf("Decode() = %#v, want %#v", got, Default())
	}
}

func TestDecodeRejectsUnknownField(t *testing.T) {
	input := strings.Replace(validConfigJSON, "\n}", ",\n  \"unexpected\": true\n}", 1)
	assertDecodeErrorContains(t, input, "unknown field")
}

func TestDecodeRejectsTrailingJSONValue(t *testing.T) {
	assertDecodeErrorContains(t, validConfigJSON+"\n{}", "trailing JSON value")
}

func TestDecodeRejectsUnsupportedSchemaVersion(t *testing.T) {
	input := strings.Replace(validConfigJSON, `"schema_version": 1`, `"schema_version": 2`, 1)
	assertDecodeErrorContains(t, input, "schema_version")
}

func TestDecodeAppliesNoImplicitZeroDefaults(t *testing.T) {
	input := `{"schema_version":1,"ipc":{},"listing":{}}`
	assertDecodeErrorContains(t, input, "max_frame_bytes")
}

func TestConfigValidateRejectsInvalidValues(t *testing.T) {
	tests := []struct {
		name      string
		mutate    func(*Config)
		wantError string
	}{
		{
			name: "zero schema version",
			mutate: func(config *Config) {
				config.SchemaVersion = 0
			},
			wantError: "schema_version",
		},
		{
			name: "zero maximum frame size",
			mutate: func(config *Config) {
				config.IPC.MaxFrameBytes = 0
			},
			wantError: "max_frame_bytes",
		},
		{
			name: "frame size above protocol maximum",
			mutate: func(config *Config) {
				config.IPC.MaxFrameBytes++
			},
			wantError: "max_frame_bytes",
		},
		{
			name: "zero default page size",
			mutate: func(config *Config) {
				config.Listing.DefaultPageSize = 0
			},
			wantError: "default_page_size",
		},
		{
			name: "zero maximum page size",
			mutate: func(config *Config) {
				config.Listing.MaxPageSize = 0
			},
			wantError: "max_page_size",
		},
		{
			name: "default page size above configured maximum",
			mutate: func(config *Config) {
				config.Listing.DefaultPageSize = config.Listing.MaxPageSize + 1
			},
			wantError: "default_page_size",
		},
		{
			name: "configured maximum above hard maximum",
			mutate: func(config *Config) {
				config.Listing.MaxPageSize++
			},
			wantError: "max_page_size",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			config := Default()
			test.mutate(&config)

			err := config.Validate()
			if err == nil {
				t.Fatal("Validate() returned nil error")
			}
			if !strings.Contains(err.Error(), test.wantError) {
				t.Fatalf("Validate() error = %q, want it to contain %q", err, test.wantError)
			}
		})
	}
}

func assertDecodeErrorContains(t *testing.T, input, want string) {
	t.Helper()

	_, err := Decode(strings.NewReader(input))
	if err == nil {
		t.Fatal("Decode() returned nil error")
	}
	if !strings.Contains(err.Error(), want) {
		t.Fatalf("Decode() error = %q, want it to contain %q", err, want)
	}
}
