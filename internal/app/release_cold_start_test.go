//go:build darwin || linux

package app

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/TyrantLucifer/awesome-mac-sftp/internal/keymap"
	"github.com/TyrantLucifer/awesome-mac-sftp/internal/testkit"
)

func TestReleaseColdStartDefaultConfigurationIsCompleteAndReadOnly(t *testing.T) {
	directory, err := filepath.EvalSymlinks(testkit.PersistentTempDir(t))
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(directory, 0o700); err != nil { // #nosec G302 -- owner-only directory mode is the property under test.
		t.Fatal(err)
	}
	missing := filepath.Join(directory, "config.json")

	var stdout bytes.Buffer
	if err := runConfigCommand([]string{"validate"}, missing, &stdout); err != nil {
		t.Fatal(err)
	}
	if stdout.String() != "config valid (schema 1)\n" {
		t.Fatalf("validate output = %q", stdout.String())
	}
	stdout.Reset()
	if err := runConfigCommand([]string{"print-effective"}, missing, &stdout); err != nil {
		t.Fatal(err)
	}

	var output struct {
		OutputVersion    int                        `json:"output_version"`
		ResolutionPolicy map[string]json.RawMessage `json:"resolution_policy"`
		Config           map[string]json.RawMessage `json:"config"`
	}
	if err := json.Unmarshal(stdout.Bytes(), &output); err != nil {
		t.Fatalf("decode effective configuration: %v\n%s", err, stdout.String())
	}
	if output.OutputVersion != 1 {
		t.Fatalf("output version = %d, want 1", output.OutputVersion)
	}
	if len(output.ResolutionPolicy) == 0 {
		t.Fatal("effective configuration omitted the resolution policy")
	}
	gotKeys := make([]string, 0, len(output.Config))
	for key := range output.Config {
		gotKeys = append(gotKeys, key)
	}
	sort.Strings(gotKeys)
	wantKeys := []string{
		"cache", "diagnostic", "direct_transfer", "external", "helper", "integrity", "ipc",
		"keymap", "listing", "preview", "retry", "schema_version", "search", "transfer",
	}
	if strings.Join(gotKeys, ",") != strings.Join(wantKeys, ",") {
		t.Fatalf("effective configuration keys = %v, want %v", gotKeys, wantKeys)
	}
	if _, err := os.Lstat(missing); !os.IsNotExist(err) {
		t.Fatalf("cold-start inspection created the absent configuration: %v", err)
	}
}

func TestReleaseColdStartDefaultKeymapIsVimFirstAndUnmodified(t *testing.T) {
	directory, err := filepath.EvalSymlinks(testkit.PersistentTempDir(t))
	if err != nil {
		t.Fatal(err)
	}
	missing := filepath.Join(directory, "config.json")
	var stdout bytes.Buffer
	if err := runConfigCommand([]string{"print-effective-keymap"}, missing, &stdout); err != nil {
		t.Fatal(err)
	}

	var output struct {
		OutputVersion int                       `json:"output_version"`
		Bindings      []keymap.EffectiveBinding `json:"bindings"`
	}
	if err := json.Unmarshal(stdout.Bytes(), &output); err != nil {
		t.Fatalf("decode effective keymap: %v\n%s", err, stdout.String())
	}
	if output.OutputVersion != 1 || len(output.Bindings) != 74 {
		t.Fatalf("effective keymap version/bindings = %d/%d, want 1/74", output.OutputVersion, len(output.Bindings))
	}
	want := map[string]string{
		"normal:down": "j", "normal:up": "k", "normal:parent": "h", "normal:open": "l",
		"normal:visual": "v", "normal:repeat": ".", "visual:down": "j", "visual:up": "k",
	}
	for _, binding := range output.Bindings {
		if binding.Overridden || binding.Input != binding.DefaultInput {
			t.Fatalf("cold-start binding is overridden: %#v", binding)
		}
		key := string(binding.Context) + ":" + string(binding.Action)
		if expected, ok := want[key]; ok {
			if binding.Input != expected {
				t.Fatalf("binding %s = %q, want %q", key, binding.Input, expected)
			}
			delete(want, key)
		}
	}
	if len(want) != 0 {
		t.Fatalf("effective keymap omitted Vim-first bindings: %v", want)
	}
	if _, err := os.Lstat(missing); !os.IsNotExist(err) {
		t.Fatalf("cold-start keymap inspection created the absent configuration: %v", err)
	}
}

func TestReleaseColdStartRejectsAndDoesNotAdvertiseMacrosOrNamedRegisters(t *testing.T) {
	directory, err := filepath.EvalSymlinks(testkit.PersistentTempDir(t))
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(directory, 0o700); err != nil { // #nosec G302 -- owner-only directory mode is the property under test.
		t.Fatal(err)
	}
	path := filepath.Join(directory, "config.json")
	content := []byte(`{"schema_version":1,"keymap":{"bindings":[{"context":"normal","input":"z","action":"macro_record"}]}}`)
	if err := os.WriteFile(path, content, 0o600); err != nil {
		t.Fatal(err)
	}
	err = runConfigCommand([]string{"validate", path}, "", &bytes.Buffer{})
	if err == nil || exitCode(err) != ExitConfig {
		t.Fatalf("macro action validation error = %v", err)
	}
	after, readErr := os.ReadFile(path)
	if readErr != nil {
		t.Fatal(readErr)
	}
	if !bytes.Equal(after, content) {
		t.Fatal("rejected macro configuration was modified")
	}

	surfaces := []string{Usage(), RenderManPage()}
	for _, shell := range []string{"bash", "zsh", "fish"} {
		completion, renderErr := RenderCompletion(shell)
		if renderErr != nil {
			t.Fatal(renderErr)
		}
		surfaces = append(surfaces, completion)
	}
	for index, surface := range surfaces {
		lower := strings.ToLower(surface)
		if strings.Contains(lower, "macro") || strings.Contains(lower, "named-register") || strings.Contains(lower, "named_register") {
			t.Fatalf("public surface %d advertises a macro or named register", index)
		}
	}
}
