//go:build darwin || linux

package app

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/TyrantLucifer/awesome-mac-sftp/internal/config"
	"github.com/TyrantLucifer/awesome-mac-sftp/internal/edit"
	"github.com/TyrantLucifer/awesome-mac-sftp/internal/externalpreviewer"
	"github.com/TyrantLucifer/awesome-mac-sftp/internal/testkit"
)

func TestResolveExternalRuntimeConfigFreezesStructuredCommandsAndOrderedPreviewers(t *testing.T) {
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	executable, err = filepath.EvalSymlinks(executable)
	if err != nil {
		t.Fatal(err)
	}
	input := config.ExternalConfig{
		Editor: &config.CommandConfig{Executable: executable, Args: []string{"editor"}},
		Opener: &config.CommandConfig{Executable: executable, Args: []string{"opener"}},
		Previewers: []config.PreviewerConfig{
			{Name: "first", Extensions: []string{".bin"}, Command: config.CommandConfig{Executable: executable, Args: []string{"first"}}, TimeoutMS: 1000, MaxInputBytes: 4096, RequireComplete: true},
			{Name: "second", MediaTypes: []string{"application/octet-stream"}, Command: config.CommandConfig{Executable: executable, Args: []string{"second"}}, TimeoutMS: 2000, MaxInputBytes: 8192, RequireComplete: true},
		},
	}
	resolved, err := resolveExternalRuntimeConfig(input, []string{"PATH=/usr/bin:/bin"})
	if err != nil {
		t.Fatal(err)
	}
	editor, err := resolved.command(edit.PurposeEditor)
	if err != nil || editor.Executable != executable || len(editor.Args) != 1 || editor.Args[0] != "editor" {
		t.Fatalf("editor = %#v, %v", editor, err)
	}
	opener, err := resolved.command(edit.PurposeOpener)
	if err != nil || opener.Executable != executable || len(opener.Args) != 1 || opener.Args[0] != "opener" {
		t.Fatalf("opener = %#v, %v", opener, err)
	}
	result := resolved.previewer.Run(t.Context(), externalpreviewer.Request{Path: "/file.bin", MediaType: "application/octet-stream", Complete: false})
	if !result.Matched || result.Rule != "first" || result.Code != externalpreviewer.CodeIncompleteInput {
		t.Fatalf("ordered previewer = %#v", result)
	}
}

func TestResolveExternalRuntimeConfigLeavesUnavailableDefaultsAsActionErrors(t *testing.T) {
	resolved, err := resolveExternalRuntimeConfig(config.ExternalConfig{}, []string{"PATH="})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := resolved.command(edit.PurposeEditor); err == nil {
		t.Fatal("missing default editor unexpectedly resolved")
	}
	if resolved.previewer != nil {
		t.Fatal("empty previewer config created a runner")
	}
}

func TestLoadApplicationConfigUsesDefaultsWhenMissingAndValidatesPrivateFiles(t *testing.T) {
	directory, err := filepath.EvalSymlinks(testkit.PersistentTempDir(t))
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(directory, 0o700); err != nil { // #nosec G302 -- owner-only directory mode is the property under test.
		t.Fatal(err)
	}
	missing := filepath.Join(directory, "missing.json")
	loaded, err := loadApplicationConfig(missing)
	if err != nil || loaded.SchemaVersion != config.SchemaVersion {
		t.Fatalf("missing config = %#v, %v", loaded, err)
	}
	path := filepath.Join(directory, "config.json")
	encoded := []byte(`{"schema_version":1,"ipc":{"max_frame_bytes":8388608},"listing":{"default_page_size":256,"max_page_size":4096}}`)
	if err := os.WriteFile(path, encoded, 0o600); err != nil {
		t.Fatal(err)
	}
	loaded, err = loadApplicationConfig(path)
	if err != nil || loaded.SchemaVersion != config.SchemaVersion {
		t.Fatalf("private config = %#v, %v", loaded, err)
	}
	if err := os.Chmod(path, 0o644); err != nil { // #nosec G302 -- deliberately broad mode must be rejected by loadApplicationConfig.
		t.Fatal(err)
	}
	if _, err := loadApplicationConfig(path); err == nil {
		t.Fatal("broad config mode was accepted")
	}
}
