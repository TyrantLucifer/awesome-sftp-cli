//go:build darwin || linux

package app

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/TyrantLucifer/awesome-mac-sftp/internal/testkit"
)

func TestRunConfigCommandValidatesDefaultOrExplicitPrivateConfig(t *testing.T) {
	directory, err := filepath.EvalSymlinks(testkit.PersistentTempDir(t))
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(directory, 0o700); err != nil { // #nosec G302 -- owner-only directory mode is the property under test.
		t.Fatal(err)
	}
	missingDefault := filepath.Join(directory, "missing.json")
	var stdout bytes.Buffer
	if err := runConfigCommand([]string{"validate"}, missingDefault, &stdout); err != nil {
		t.Fatal(err)
	}
	if got := stdout.String(); got != "config valid (schema 1)\n" {
		t.Fatalf("stdout = %q", got)
	}

	path := filepath.Join(directory, "config.json")
	if err := os.WriteFile(path, []byte(`{"schema_version":1}`), 0o600); err != nil {
		t.Fatal(err)
	}
	stdout.Reset()
	if err := runConfigCommand([]string{"validate", path}, missingDefault, &stdout); err != nil {
		t.Fatal(err)
	}
	if got := stdout.String(); got != "config valid (schema 1)\n" {
		t.Fatalf("stdout = %q", got)
	}
}

func TestRunConfigCommandPrintsVersionedRedactedEffectiveConfig(t *testing.T) {
	directory, err := filepath.EvalSymlinks(testkit.PersistentTempDir(t))
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(directory, 0o700); err != nil { // #nosec G302 -- owner-only directory mode is the property under test.
		t.Fatal(err)
	}
	path := filepath.Join(directory, "config.json")
	encoded := []byte(`{"schema_version":1,"external":{"editor":{"executable":"vim","argv":["secret-stage6"]}}}`)
	if err := os.WriteFile(path, encoded, 0o600); err != nil {
		t.Fatal(err)
	}

	var stdout bytes.Buffer
	if err := runConfigCommand([]string{"print-effective", path}, "", &stdout); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(stdout.String(), "secret-stage6") || !strings.Contains(stdout.String(), `"output_version": 1`) || !strings.Contains(stdout.String(), `"<redacted>"`) {
		t.Fatalf("effective output = %s", stdout.String())
	}
}

func TestRunConfigCommandClassifiesUsageAndConfigFailures(t *testing.T) {
	tests := []struct {
		name string
		args []string
		want ExitCode
	}{
		{name: "missing command", args: nil, want: ExitUsage},
		{name: "unknown command", args: []string{"unknown"}, want: ExitUsage},
		{name: "missing explicit file", args: []string{"validate", "/definitely/not/an/amsftp/config.json"}, want: ExitConfig},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			err := runConfigCommand(test.args, "", &bytes.Buffer{})
			if err == nil {
				t.Fatal("runConfigCommand() returned nil")
			}
			if got := exitCode(err); got != test.want {
				t.Fatalf("exit code = %d, want %d: %v", got, test.want, err)
			}
		})
	}
}
