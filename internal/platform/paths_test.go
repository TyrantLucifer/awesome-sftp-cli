package platform

import (
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func TestResolvePathsDarwinDefaults(t *testing.T) {
	sources := pathSources{
		goos:          "darwin",
		euid:          501,
		homeDir:       "/Users/alice",
		userConfigDir: "/Users/alice/Library/Application Support",
		userCacheDir:  "/Users/alice/Library/Caches",
		environment:   map[string]string{"TMPDIR": "/private/var/folders/ab/T"},
	}

	got, diagnostics, err := resolvePaths(sources, Overrides{})
	if err != nil {
		t.Fatalf("resolvePaths(): %v", err)
	}
	if len(diagnostics) != 0 {
		t.Fatalf("diagnostics = %#v, want none", diagnostics)
	}

	want := Paths{
		ConfigDir:     "/Users/alice/Library/Application Support/io.github.tyrantlucifer.amsftp",
		ConfigFile:    "/Users/alice/Library/Application Support/io.github.tyrantlucifer.amsftp/config.json",
		StateDir:      "/Users/alice/Library/Application Support/io.github.tyrantlucifer.amsftp/state",
		DatabaseFile:  "/Users/alice/Library/Application Support/io.github.tyrantlucifer.amsftp/state/amsftp.db",
		LogDir:        "/Users/alice/Library/Logs/io.github.tyrantlucifer.amsftp",
		LogFile:       "/Users/alice/Library/Logs/io.github.tyrantlucifer.amsftp/daemon.jsonl",
		CacheDir:      "/Users/alice/Library/Caches/io.github.tyrantlucifer.amsftp",
		RuntimeDir:    "/private/var/folders/ab/T/amsftp-501",
		ControlSocket: "/private/var/folders/ab/T/amsftp-501/control-v1.sock",
		LockFile:      "/private/var/folders/ab/T/amsftp-501/daemon.lock",
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("paths = %#v, want %#v", got, want)
	}
}

func TestResolvePathsLinuxUsesAbsoluteXDGDirectories(t *testing.T) {
	sources := pathSources{
		goos:          "linux",
		euid:          1000,
		homeDir:       "/home/alice",
		userConfigDir: "/xdg/config",
		userCacheDir:  "/xdg/cache",
		environment: map[string]string{
			"XDG_STATE_HOME":  "/xdg/state",
			"XDG_RUNTIME_DIR": "/run/user/1000",
		},
	}

	got, diagnostics, err := resolvePaths(sources, Overrides{})
	if err != nil {
		t.Fatalf("resolvePaths(): %v", err)
	}
	if len(diagnostics) != 0 {
		t.Fatalf("diagnostics = %#v, want none", diagnostics)
	}

	checks := map[string]string{
		"config":   got.ConfigFile,
		"state":    got.StateDir,
		"database": got.DatabaseFile,
		"log":      got.LogFile,
		"cache":    got.CacheDir,
		"runtime":  got.RuntimeDir,
		"socket":   got.ControlSocket,
		"lock":     got.LockFile,
	}
	wants := map[string]string{
		"config":   "/xdg/config/amsftp/config.json",
		"state":    "/xdg/state/amsftp",
		"database": "/xdg/state/amsftp/amsftp.db",
		"log":      "/xdg/state/amsftp/log/daemon.jsonl",
		"cache":    "/xdg/cache/amsftp",
		"runtime":  "/run/user/1000/amsftp",
		"socket":   "/run/user/1000/amsftp/control-v1.sock",
		"lock":     "/run/user/1000/amsftp/daemon.lock",
	}
	for name, value := range checks {
		if value != wants[name] {
			t.Errorf("%s = %q, want %q", name, value, wants[name])
		}
	}
}

func TestResolvePathsTreatsRelativeXDGValuesAsUnset(t *testing.T) {
	sources := pathSources{
		goos:          "linux",
		euid:          1000,
		homeDir:       "/home/alice",
		userConfigDir: "/home/alice/.config",
		userCacheDir:  "/home/alice/.cache",
		environment: map[string]string{
			"XDG_STATE_HOME":  "relative-state",
			"XDG_RUNTIME_DIR": "relative-runtime/secret",
		},
	}

	got, diagnostics, err := resolvePaths(sources, Overrides{})
	if err != nil {
		t.Fatalf("resolvePaths(): %v", err)
	}
	if got.StateDir != "/home/alice/.local/state/amsftp" {
		t.Fatalf("StateDir = %q", got.StateDir)
	}
	if got.RuntimeDir != "/tmp/amsftp-1000" {
		t.Fatalf("RuntimeDir = %q", got.RuntimeDir)
	}
	if len(diagnostics) != 1 || diagnostics[0].Code != DiagnosticUnsafePreferredRuntime {
		t.Fatalf("diagnostics = %#v", diagnostics)
	}
	if strings.Contains(diagnostics[0].Message, "relative-runtime") {
		t.Fatalf("diagnostic leaked runtime path: %q", diagnostics[0].Message)
	}
}

func TestResolvePathsRejectsLexicallyUncleanAbsoluteXDGStateHome(t *testing.T) {
	sources := pathSources{
		goos:          "linux",
		euid:          1000,
		homeDir:       "/home/alice",
		userConfigDir: "/home/alice/.config",
		userCacheDir:  "/home/alice/.cache",
		environment:   map[string]string{"XDG_STATE_HOME": "/safe/../state"},
	}

	if _, _, err := resolvePaths(sources, Overrides{}); err == nil {
		t.Fatal("resolvePaths() error = nil")
	}
}

func TestResolvePathsAppliesExplicitOverrides(t *testing.T) {
	sources := pathSources{
		goos:          "linux",
		euid:          1000,
		homeDir:       "/home/alice",
		userConfigDir: "/home/alice/.config",
		userCacheDir:  "/home/alice/.cache",
		environment:   map[string]string{"XDG_RUNTIME_DIR": "/run/user/1000"},
	}
	overrides := Overrides{
		ConfigFile: "/secure/config/custom.json",
		StateDir:   "/secure/state",
		CacheDir:   "/secure/cache",
		RuntimeDir: "/secure/run",
	}

	got, diagnostics, err := resolvePaths(sources, overrides)
	if err != nil {
		t.Fatalf("resolvePaths(): %v", err)
	}
	if len(diagnostics) != 0 {
		t.Fatalf("diagnostics = %#v, want none", diagnostics)
	}
	if got.ConfigDir != "/secure/config" || got.ConfigFile != overrides.ConfigFile {
		t.Fatalf("config paths = (%q, %q)", got.ConfigDir, got.ConfigFile)
	}
	if got.StateDir != overrides.StateDir || got.DatabaseFile != "/secure/state/amsftp.db" {
		t.Fatalf("state paths = (%q, %q)", got.StateDir, got.DatabaseFile)
	}
	if got.CacheDir != overrides.CacheDir || got.RuntimeDir != overrides.RuntimeDir {
		t.Fatalf("cache/runtime = (%q, %q)", got.CacheDir, got.RuntimeDir)
	}
	if got.ControlSocket != "/secure/run/control-v1.sock" || got.LockFile != "/secure/run/daemon.lock" {
		t.Fatalf("runtime children = (%q, %q)", got.ControlSocket, got.LockFile)
	}
}

func TestResolvePathsRejectsUnsafeOverrides(t *testing.T) {
	sources := pathSources{
		goos:          "linux",
		euid:          1000,
		homeDir:       "/home/alice",
		userConfigDir: "/home/alice/.config",
		userCacheDir:  "/home/alice/.cache",
	}
	tests := map[string]Overrides{
		"relative config": {ConfigFile: "config.json"},
		"unclean state":   {StateDir: "/secure/../state"},
		"relative cache":  {CacheDir: "cache"},
		"unclean runtime": {RuntimeDir: "/secure/run/.."},
		"nul":             {RuntimeDir: "/secure/run\x00bad"},
	}

	for name, overrides := range tests {
		t.Run(name, func(t *testing.T) {
			_, _, err := resolvePaths(sources, overrides)
			if err == nil {
				t.Fatal("resolvePaths() error = nil")
			}
		})
	}
}

func TestResolvePathsRejectsUnsupportedInputs(t *testing.T) {
	valid := pathSources{
		goos:          "linux",
		euid:          1000,
		homeDir:       "/home/alice",
		userConfigDir: "/home/alice/.config",
		userCacheDir:  "/home/alice/.cache",
	}
	tests := map[string]pathSources{
		"unsupported os": func() pathSources { value := valid; value.goos = "windows"; return value }(),
		"negative euid":  func() pathSources { value := valid; value.euid = -1; return value }(),
		"relative home":  func() pathSources { value := valid; value.homeDir = "home/alice"; return value }(),
	}

	for name, sources := range tests {
		t.Run(name, func(t *testing.T) {
			_, _, err := resolvePaths(sources, Overrides{})
			if err == nil {
				t.Fatal("resolvePaths() error = nil")
			}
		})
	}
}

func TestSocketPathByteLimit(t *testing.T) {
	directoryAtLimit := "/" + strings.Repeat("a", maxControlSocketBytes-len("/control-v1.sock")-1)
	if got := len([]byte(filepath.Join(directoryAtLimit, "control-v1.sock"))); got != maxControlSocketBytes {
		t.Fatalf("test setup socket length = %d", got)
	}
	if err := validateControlSocketPath(filepath.Join(directoryAtLimit, "control-v1.sock")); err != nil {
		t.Fatalf("validateControlSocketPath(at limit): %v", err)
	}

	overLimit := directoryAtLimit + "b"
	if err := validateControlSocketPath(filepath.Join(overLimit, "control-v1.sock")); err == nil {
		t.Fatal("validateControlSocketPath(over limit) error = nil")
	}
}
