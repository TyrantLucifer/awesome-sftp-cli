package config

import (
	"reflect"
	"strings"
	"testing"

	"github.com/TyrantLucifer/awesome-mac-sftp/internal/keymap"
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
		Cache: CacheConfig{
			GlobalBytes: 2 << 30, GlobalEntries: 4096,
			WorkspaceBytes: 1 << 30, MaxEvictionCandidates: 256,
		},
		Transfer: TransferConfig{MaxConcurrent: 4, MaxQueued: 128},
	}

	got := Default()
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("Default() = %#v, want %#v", got, want)
	}
	if err := got.Validate(); err != nil {
		t.Fatalf("Default().Validate() returned error: %v", err)
	}
}

func TestDecodeAcceptsContextKeymapRemap(t *testing.T) {
	input := `{"schema_version":1,"keymap":{"bindings":[{"context":"visual","input":"n","action":"down"}]}}`
	got, err := Decode(strings.NewReader(input))
	if err != nil {
		t.Fatal(err)
	}
	want := []keymap.Override{{Context: keymap.ContextVisual, Input: "n", Action: keymap.ActionDown}}
	if !reflect.DeepEqual(got.Keymap.Bindings, want) {
		t.Fatalf("keymap bindings = %#v, want %#v", got.Keymap.Bindings, want)
	}
}

func TestDefaultCacheAndTransferSettingsFreezeCurrentRuntimeBehavior(t *testing.T) {
	got := Default()
	if got.Cache != (CacheConfig{GlobalBytes: 2 << 30, GlobalEntries: 4096, WorkspaceBytes: 1 << 30, MaxEvictionCandidates: 256}) {
		t.Fatalf("cache defaults = %#v", got.Cache)
	}
	if got.Transfer != (TransferConfig{MaxConcurrent: 4, MaxQueued: 128}) {
		t.Fatalf("transfer defaults = %#v", got.Transfer)
	}
}

func TestDecodeAppliesPartialCacheAndTransferSettings(t *testing.T) {
	input := `{"schema_version":1,"cache":{"global_bytes":1073741824},"transfer":{"max_concurrent":2,"global_bytes_per_second":1048576}}`
	got, err := Decode(strings.NewReader(input))
	if err != nil {
		t.Fatal(err)
	}
	if got.Cache.GlobalBytes != 1<<30 || got.Cache.GlobalEntries != 4096 || got.Cache.MaxEvictionCandidates != 256 {
		t.Fatalf("partial cache = %#v", got.Cache)
	}
	if got.Transfer.MaxConcurrent != 2 || got.Transfer.MaxQueued != 128 || got.Transfer.GlobalBytesPerSecond != 1<<20 {
		t.Fatalf("partial transfer = %#v", got.Transfer)
	}
}

func TestCacheAndTransferSettingsCanOnlyTightenFrozenCeilings(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  string
	}{
		{name: "cache bytes", input: `{"schema_version":1,"cache":{"global_bytes":2147483649}}`, want: "cache.global_bytes"},
		{name: "workspace above global", input: `{"schema_version":1,"cache":{"global_bytes":1024,"workspace_bytes":2048}}`, want: "cache.workspace_bytes"},
		{name: "concurrency", input: `{"schema_version":1,"transfer":{"max_concurrent":5}}`, want: "transfer.max_concurrent"},
		{name: "queue below concurrency", input: `{"schema_version":1,"transfer":{"max_concurrent":4,"max_queued":3}}`, want: "transfer.max_queued"},
		{name: "bandwidth", input: `{"schema_version":1,"transfer":{"global_bytes_per_second":1099511627777}}`, want: "transfer.global_bytes_per_second"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			assertDecodeErrorContains(t, test.input, test.want)
		})
	}
}

func TestDecodeRejectsConflictingOrReservedKeymap(t *testing.T) {
	assertDecodeErrorContains(t, `{"schema_version":1,"keymap":{"bindings":[{"context":"normal","input":"k","action":"down"}]}}`, "conflict")
	assertDecodeErrorContains(t, `{"schema_version":1,"keymap":{"bindings":[{"context":"normal","input":"z","action":"delete"}]}}`, "reserved")
}

func TestDecodeAcceptsCanonicalConfig(t *testing.T) {
	got, err := Decode(strings.NewReader(validConfigJSON + "\n\t"))
	if err != nil {
		t.Fatalf("Decode() returned error: %v", err)
	}
	if !reflect.DeepEqual(got, Default()) {
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

func TestDecodeAppliesDocumentedDefaultsToOmittedFields(t *testing.T) {
	got, err := Decode(strings.NewReader(`{"schema_version":1}`))
	if err != nil {
		t.Fatalf("Decode() returned error: %v", err)
	}
	if !reflect.DeepEqual(got, Default()) {
		t.Fatalf("Decode() = %#v, want documented defaults %#v", got, Default())
	}
}

func TestDecodeRequiresExplicitSchemaVersion(t *testing.T) {
	assertDecodeErrorContains(t, `{}`, "schema_version")
}

func TestDecodeDoesNotReplaceExplicitInvalidZeroWithDefault(t *testing.T) {
	assertDecodeErrorContains(t, `{"schema_version":1,"ipc":{"max_frame_bytes":0}}`, "max_frame_bytes")
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

func TestDecodeAcceptsBoundedStructuredExternalCommands(t *testing.T) {
	input := `{
	  "schema_version": 1,
	  "ipc": {"max_frame_bytes": 8388608},
	  "listing": {"default_page_size": 256, "max_page_size": 4096},
	  "external": {
	    "editor": {"executable": "vim", "argv": ["-f"]},
	    "opener": {"executable": "/usr/bin/open", "argv": []},
	    "previewers": [{
	      "name": "pdf",
	      "media_types": ["application/pdf"],
	      "extensions": [".pdf"],
	      "command": {"executable": "pdftotext", "argv": ["-"]},
	      "timeout_ms": 5000,
	      "max_input_bytes": 1048576,
	      "require_complete": true
	    }]
	  }
	}`
	got, err := Decode(strings.NewReader(input))
	if err != nil {
		t.Fatal(err)
	}
	if got.External.Editor == nil || got.External.Editor.Executable != "vim" || len(got.External.Previewers) != 1 {
		t.Fatalf("external config = %#v", got.External)
	}
}

func TestExternalConfigRejectsUnboundedOrAmbiguousRules(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*Config)
	}{
		{name: "empty editor", mutate: func(config *Config) {
			config.External.Editor = &CommandConfig{}
		}},
		{name: "control argument", mutate: func(config *Config) {
			config.External.Opener = &CommandConfig{Executable: "/usr/bin/open", Args: []string{"bad\narg"}}
		}},
		{name: "empty match", mutate: func(config *Config) {
			config.External.Previewers = []PreviewerConfig{validPreviewerConfig()}
			config.External.Previewers[0].MediaTypes = nil
			config.External.Previewers[0].Extensions = nil
		}},
		{name: "duplicate name", mutate: func(config *Config) {
			config.External.Previewers = []PreviewerConfig{validPreviewerConfig(), validPreviewerConfig()}
		}},
		{name: "zero timeout", mutate: func(config *Config) {
			config.External.Previewers = []PreviewerConfig{validPreviewerConfig()}
			config.External.Previewers[0].TimeoutMS = 0
		}},
		{name: "zero input limit", mutate: func(config *Config) {
			config.External.Previewers = []PreviewerConfig{validPreviewerConfig()}
			config.External.Previewers[0].MaxInputBytes = 0
		}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			config := Default()
			test.mutate(&config)
			if err := config.Validate(); err == nil {
				t.Fatal("Validate() returned nil error")
			}
		})
	}
}

func validPreviewerConfig() PreviewerConfig {
	return PreviewerConfig{
		Name: "one", Extensions: []string{".txt"},
		Command: CommandConfig{Executable: "preview"}, TimeoutMS: 1000, MaxInputBytes: 1024,
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
