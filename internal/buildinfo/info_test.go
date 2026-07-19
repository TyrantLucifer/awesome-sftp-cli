package buildinfo_test

import (
	"encoding/json"
	"runtime"
	"strings"
	"testing"

	"github.com/TyrantLucifer/awesome-sftp-cli/internal/buildinfo"
)

func TestCurrentUsesDevelopmentAndRuntimeDefaults(t *testing.T) {
	got := buildinfo.Current()

	if got.Version != "dev" {
		t.Fatalf("Version = %q, want dev", got.Version)
	}
	if got.Commit == "" {
		t.Fatal("Commit is empty")
	}
	if got.GoVersion != runtime.Version() {
		t.Fatalf("GoVersion = %q, want %q", got.GoVersion, runtime.Version())
	}
	if got.GOOS != runtime.GOOS {
		t.Fatalf("GOOS = %q, want %q", got.GOOS, runtime.GOOS)
	}
	if got.GOARCH != runtime.GOARCH {
		t.Fatalf("GOARCH = %q, want %q", got.GOARCH, runtime.GOARCH)
	}
}

func TestInfoStringIncludesEveryBuildField(t *testing.T) {
	info := buildinfo.Info{
		Version:   "v1.2.3",
		Commit:    "abc123",
		Dirty:     true,
		GoVersion: "go1.26.5",
		GOOS:      "darwin",
		GOARCH:    "arm64",
	}

	got := info.String()
	for _, want := range []string{"v1.2.3", "abc123", "true", "go1.26.5", "darwin", "arm64"} {
		if !strings.Contains(got, want) {
			t.Fatalf("String() = %q, want it to contain %q", got, want)
		}
	}
}

func TestInfoJSONContractHasNoBuildTime(t *testing.T) {
	data, err := json.Marshal(buildinfo.Info{})
	if err != nil {
		t.Fatal(err)
	}

	var got map[string]any
	if err := json.Unmarshal(data, &got); err != nil {
		t.Fatal(err)
	}
	wantKeys := []string{"version", "commit", "dirty", "go_version", "goos", "goarch"}
	if len(got) != len(wantKeys) {
		t.Fatalf("JSON keys = %#v, want exactly %#v", got, wantKeys)
	}
	for _, key := range wantKeys {
		if _, ok := got[key]; !ok {
			t.Fatalf("JSON is missing key %q: %s", key, data)
		}
	}
}
