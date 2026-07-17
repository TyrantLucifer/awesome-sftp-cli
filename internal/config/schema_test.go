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
		Preview: PreviewConfig{
			MaxInputBytes: 512 * 1024, MaxJSONBytes: 256 * 1024, MaxJSONDepth: 64,
			MaxRenderedLines: 10_000, MaxOutputBytes: 512 * 1024, MaxImagePixels: 40_000_000,
			MaxStyleSpans: 4096, ImageMaxPayloadBytes: 4 * 1024 * 1024,
			ImageMaxOutputBytes: 6 * 1024 * 1024, ImageChunkBytes: 4096, ImageMaxPixels: 1_000_000,
		},
		Search: SearchConfig{
			Filename: FilenameSearchConfig{
				PageItems: 256, EventBuffer: 64, ConcurrentLists: 1, MaxDepth: 128,
				MaxEntries: 1_000_000, MaxResults: 10_000, MaxOutputBytes: 8 << 20, MaxDurationMS: 300_000,
			},
			Content: ContentSearchConfig{
				PageItems: 128, EventBuffer: 32, MaxDepth: 32, MaxEntries: 10_000,
				MaxFiles: 1_000, MaxResults: 5_000, MaxMatchesPerFile: 100,
				MaxFileBytes: 1 << 20, MaxReadBytes: 32 << 20, MaxSnippetBytes: 512,
				MaxOutputBytes: 8 << 20, MaxDurationMS: 120_000,
			},
		},
		Retry:          RetryConfig{ReconnectDelaysMS: []int64{100, 250, 500}, JobRetryDelayMS: 60_000},
		Integrity:      IntegrityConfig{TransferPolicy: "strong"},
		DirectTransfer: DirectTransferConfig{Enabled: false},
		Diagnostic: DiagnosticConfig{
			LogMaxBytes: 4 * 1024 * 1024, LogBackups: 3, RingRecords: 1000,
		},
	}

	got := Default()
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("Default() = %#v, want %#v", got, want)
	}
	if err := got.Validate(); err != nil {
		t.Fatalf("Default().Validate() returned error: %v", err)
	}
}

func TestDecodeAppliesPartialDiagnosticSettings(t *testing.T) {
	got, err := Decode(strings.NewReader(`{"schema_version":1,"diagnostic":{"log_max_bytes":1048576,"log_backups":2,"ring_records":500}}`))
	if err != nil {
		t.Fatal(err)
	}
	want := DiagnosticConfig{LogMaxBytes: 1 << 20, LogBackups: 2, RingRecords: 500}
	if got.Diagnostic != want {
		t.Fatalf("diagnostic settings = %#v, want %#v", got.Diagnostic, want)
	}
}

func TestDiagnosticSettingsCanOnlyTightenFrozenCeilings(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  string
	}{
		{name: "log too small", input: `{"schema_version":1,"diagnostic":{"log_max_bytes":255}}`, want: "diagnostic.log_max_bytes"},
		{name: "log too large", input: `{"schema_version":1,"diagnostic":{"log_max_bytes":4194305}}`, want: "diagnostic.log_max_bytes"},
		{name: "zero backups", input: `{"schema_version":1,"diagnostic":{"log_backups":0}}`, want: "diagnostic.log_backups"},
		{name: "too many backups", input: `{"schema_version":1,"diagnostic":{"log_backups":4}}`, want: "diagnostic.log_backups"},
		{name: "zero records", input: `{"schema_version":1,"diagnostic":{"ring_records":0}}`, want: "diagnostic.ring_records"},
		{name: "too many records", input: `{"schema_version":1,"diagnostic":{"ring_records":1001}}`, want: "diagnostic.ring_records"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			assertDecodeErrorContains(t, test.input, test.want)
		})
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

func TestDefaultPreviewSettingsFreezeCurrentRuntimeBehavior(t *testing.T) {
	got := Default().Preview
	want := PreviewConfig{
		MaxInputBytes: 512 * 1024, MaxJSONBytes: 256 * 1024, MaxJSONDepth: 64,
		MaxRenderedLines: 10_000, MaxOutputBytes: 512 * 1024, MaxImagePixels: 40_000_000,
		MaxStyleSpans: 4096, ImageMaxPayloadBytes: 4 * 1024 * 1024,
		ImageMaxOutputBytes: 6 * 1024 * 1024, ImageChunkBytes: 4096, ImageMaxPixels: 1_000_000,
	}
	if got != want {
		t.Fatalf("preview defaults = %#v, want %#v", got, want)
	}
}

func TestDefaultSearchSettingsFreezeCurrentRuntimeBehavior(t *testing.T) {
	got := Default().Search
	want := SearchConfig{
		Filename: FilenameSearchConfig{
			PageItems: 256, EventBuffer: 64, ConcurrentLists: 1, MaxDepth: 128,
			MaxEntries: 1_000_000, MaxResults: 10_000, MaxOutputBytes: 8 << 20, MaxDurationMS: 300_000,
		},
		Content: ContentSearchConfig{
			PageItems: 128, EventBuffer: 32, MaxDepth: 32, MaxEntries: 10_000,
			MaxFiles: 1_000, MaxResults: 5_000, MaxMatchesPerFile: 100,
			MaxFileBytes: 1 << 20, MaxReadBytes: 32 << 20, MaxSnippetBytes: 512,
			MaxOutputBytes: 8 << 20, MaxDurationMS: 120_000,
		},
	}
	if got != want {
		t.Fatalf("search defaults = %#v, want %#v", got, want)
	}
}

func TestDecodeAppliesPartialSearchSettings(t *testing.T) {
	input := `{"schema_version":1,"search":{"filename":{"max_depth":64},"content":{"max_files":500}}}`
	got, err := Decode(strings.NewReader(input))
	if err != nil {
		t.Fatal(err)
	}
	if got.Search.Filename.MaxDepth != 64 || got.Search.Filename.MaxEntries != 1_000_000 || got.Search.Content.MaxFiles != 500 || got.Search.Content.MaxReadBytes != 32<<20 {
		t.Fatalf("partial search = %#v", got.Search)
	}
}

func TestSearchSettingsCanOnlyTightenFrozenCeilings(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  string
	}{
		{name: "filename page", input: `{"schema_version":1,"search":{"filename":{"page_items":257}}}`, want: "search.filename.page_items"},
		{name: "filename buffer", input: `{"schema_version":1,"search":{"filename":{"event_buffer":65}}}`, want: "search.filename.event_buffer"},
		{name: "filename concurrency", input: `{"schema_version":1,"search":{"filename":{"concurrent_lists":2}}}`, want: "search.filename.concurrent_lists"},
		{name: "filename depth", input: `{"schema_version":1,"search":{"filename":{"max_depth":129}}}`, want: "search.filename.max_depth"},
		{name: "filename entries", input: `{"schema_version":1,"search":{"filename":{"max_entries":1000001}}}`, want: "search.filename.max_entries"},
		{name: "filename results", input: `{"schema_version":1,"search":{"filename":{"max_results":10001}}}`, want: "search.filename.max_results"},
		{name: "filename output", input: `{"schema_version":1,"search":{"filename":{"max_output_bytes":8388609}}}`, want: "search.filename.max_output_bytes"},
		{name: "filename zero duration", input: `{"schema_version":1,"search":{"filename":{"max_duration_ms":0}}}`, want: "search.filename.max_duration_ms"},
		{name: "filename negative duration", input: `{"schema_version":1,"search":{"filename":{"max_duration_ms":-1}}}`, want: "search.filename.max_duration_ms"},
		{name: "filename duration", input: `{"schema_version":1,"search":{"filename":{"max_duration_ms":300001}}}`, want: "search.filename.max_duration_ms"},
		{name: "content page", input: `{"schema_version":1,"search":{"content":{"page_items":129}}}`, want: "search.content.page_items"},
		{name: "content buffer", input: `{"schema_version":1,"search":{"content":{"event_buffer":33}}}`, want: "search.content.event_buffer"},
		{name: "content depth", input: `{"schema_version":1,"search":{"content":{"max_depth":33}}}`, want: "search.content.max_depth"},
		{name: "content entries", input: `{"schema_version":1,"search":{"content":{"max_entries":10001}}}`, want: "search.content.max_entries"},
		{name: "content files", input: `{"schema_version":1,"search":{"content":{"max_files":1001}}}`, want: "search.content.max_files"},
		{name: "content results", input: `{"schema_version":1,"search":{"content":{"max_results":5001}}}`, want: "search.content.max_results"},
		{name: "content per file", input: `{"schema_version":1,"search":{"content":{"max_matches_per_file":101}}}`, want: "search.content.max_matches_per_file"},
		{name: "content file bytes", input: `{"schema_version":1,"search":{"content":{"max_file_bytes":1048577}}}`, want: "search.content.max_file_bytes"},
		{name: "content read bytes", input: `{"schema_version":1,"search":{"content":{"max_read_bytes":33554433}}}`, want: "search.content.max_read_bytes"},
		{name: "content snippet", input: `{"schema_version":1,"search":{"content":{"max_snippet_bytes":513}}}`, want: "search.content.max_snippet_bytes"},
		{name: "content output", input: `{"schema_version":1,"search":{"content":{"max_output_bytes":8388609}}}`, want: "search.content.max_output_bytes"},
		{name: "content zero duration", input: `{"schema_version":1,"search":{"content":{"max_duration_ms":0}}}`, want: "search.content.max_duration_ms"},
		{name: "content negative duration", input: `{"schema_version":1,"search":{"content":{"max_duration_ms":-1}}}`, want: "search.content.max_duration_ms"},
		{name: "content duration", input: `{"schema_version":1,"search":{"content":{"max_duration_ms":120001}}}`, want: "search.content.max_duration_ms"},
		{name: "file above total read", input: `{"schema_version":1,"search":{"content":{"max_file_bytes":1048576,"max_read_bytes":524288}}}`, want: "search.content.max_file_bytes"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			assertDecodeErrorContains(t, test.input, test.want)
		})
	}
}

func TestDecodeAppliesPartialPreviewSettings(t *testing.T) {
	input := `{"schema_version":1,"preview":{"max_input_bytes":262144,"image_max_pixels":500000}}`
	got, err := Decode(strings.NewReader(input))
	if err != nil {
		t.Fatal(err)
	}
	if got.Preview.MaxInputBytes != 256*1024 || got.Preview.MaxJSONBytes != 256*1024 || got.Preview.ImageMaxPixels != 500_000 || got.Preview.ImageChunkBytes != 4096 {
		t.Fatalf("partial preview = %#v", got.Preview)
	}
}

func TestPreviewSettingsCanOnlyTightenFrozenCeilings(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  string
	}{
		{name: "input bytes", input: `{"schema_version":1,"preview":{"max_input_bytes":524289}}`, want: "preview.max_input_bytes"},
		{name: "json above input", input: `{"schema_version":1,"preview":{"max_input_bytes":1024,"max_json_bytes":2048}}`, want: "preview.max_json_bytes"},
		{name: "json depth", input: `{"schema_version":1,"preview":{"max_json_depth":65}}`, want: "preview.max_json_depth"},
		{name: "rendered lines", input: `{"schema_version":1,"preview":{"max_rendered_lines":10001}}`, want: "preview.max_rendered_lines"},
		{name: "output bytes", input: `{"schema_version":1,"preview":{"max_output_bytes":524289}}`, want: "preview.max_output_bytes"},
		{name: "render image pixels", input: `{"schema_version":1,"preview":{"max_image_pixels":40000001}}`, want: "preview.max_image_pixels"},
		{name: "style spans", input: `{"schema_version":1,"preview":{"max_style_spans":4097}}`, want: "preview.max_style_spans"},
		{name: "image output", input: `{"schema_version":1,"preview":{"image_max_output_bytes":6291457}}`, want: "preview.image_max_output_bytes"},
		{name: "image payload above output", input: `{"schema_version":1,"preview":{"image_max_payload_bytes":4194304,"image_max_output_bytes":1048576}}`, want: "preview.image_max_payload_bytes"},
		{name: "image chunk above payload", input: `{"schema_version":1,"preview":{"image_max_payload_bytes":1024,"image_chunk_bytes":2048}}`, want: "preview.image_chunk_bytes"},
		{name: "image pixels", input: `{"schema_version":1,"preview":{"image_max_pixels":1000001}}`, want: "preview.image_max_pixels"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			assertDecodeErrorContains(t, test.input, test.want)
		})
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
