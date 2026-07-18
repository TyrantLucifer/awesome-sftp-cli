package main

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"testing"

	"github.com/TyrantLucifer/awesome-mac-sftp/internal/releasepack"
)

func TestRunBuildsExactPublicReleaseFromConfinedManifestInputs(t *testing.T) {
	root := t.TempDir()
	writeFixtureFile(t, root, "LICENSE", "project license\n")
	writeFixtureFile(t, root, "NOTICE", "dependency notices\n")
	writeFixtureFile(t, root, "INSTALL.md", "install\n")
	writeFixtureFile(t, root, "UNINSTALL.md", "uninstall\n")
	writeFixtureFile(t, root, "amsftp.1", ".TH AMSFTP 1\n")
	platforms := []manifestPlatform{
		{OS: "darwin", Arch: "amd64", Path: "bin/darwin-amd64"},
		{OS: "darwin", Arch: "arm64", Path: "bin/darwin-arm64"},
		{OS: "linux", Arch: "amd64", Path: "bin/linux-amd64"},
		{OS: "linux", Arch: "arm64", Path: "bin/linux-arm64"},
	}
	for _, platform := range platforms {
		writeFixtureFile(t, root, platform.Path, platform.OS+"/"+platform.Arch+" binary\n")
	}
	manifestPath := writeManifest(t, root, inputManifest{
		Schema: "amsftp-public-release-manifest-v1", Version: "1.0.0",
		Commit: strings.Repeat("1", 40), Tree: strings.Repeat("2", 40), SourceDateEpoch: 1_700_000_000,
		Materials: manifestMaterials{License: "LICENSE", Notice: "NOTICE", Install: "INSTALL.md", Uninstall: "UNINSTALL.md", Man: "amsftp.1"},
		Platforms: platforms,
		Modules:   []manifestModule{{Path: "example.com/dependency", Version: "v1.2.3", Sum: "h1:AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA=", License: "BSD-3-Clause", Targets: []manifestTarget{{OS: "darwin", Arch: "amd64"}, {OS: "darwin", Arch: "arm64"}, {OS: "linux", Arch: "amd64"}, {OS: "linux", Arch: "arm64"}}}},
	})
	output := filepath.Join(root, "release")
	var stdout bytes.Buffer
	inspect := func(raw []byte) (releasepack.GoBuildEvidence, error) {
		parts := strings.Fields(string(raw))
		target := strings.Split(parts[0], "/")
		return releasepack.GoBuildEvidence{MainPath: "github.com/TyrantLucifer/awesome-mac-sftp/cmd/amsftp", GOOS: target[0], GOARCH: target[1], Trimpath: true, VCSRevision: strings.Repeat("1", 40), Modules: []releasepack.GoModuleEvidence{{Path: "example.com/dependency", Version: "v1.2.3", Sum: "h1:AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA="}}}, nil
	}
	if err := runWithInspector([]string{manifestPath, output}, &stdout, inspect); err != nil {
		t.Fatal(err)
	}
	if stdout.String() != output+"\n" {
		t.Fatalf("stdout = %q", stdout.String())
	}
	entries, err := os.ReadDir(output)
	if err != nil {
		t.Fatal(err)
	}
	got := make([]string, 0, len(entries))
	for _, entry := range entries {
		got = append(got, entry.Name())
	}
	sort.Strings(got)
	want := []string{
		"amsftp_1.0.0_darwin_amd64.tar.gz", "amsftp_1.0.0_darwin_arm64.tar.gz",
		"amsftp_1.0.0_linux_amd64.tar.gz", "amsftp_1.0.0_linux_arm64.tar.gz",
		"checksums.txt", "provenance.input.json", "sbom.spdx.json",
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("release output = %#v, want %#v", got, want)
	}
}

func TestRunRejectsUnknownManifestFieldsAndInputsOutsideManifestDirectory(t *testing.T) {
	t.Run("unknown field", func(t *testing.T) {
		root := t.TempDir()
		manifestPath := filepath.Join(root, "manifest.json")
		if err := os.WriteFile(manifestPath, []byte(`{"schema":"amsftp-public-release-manifest-v1","unexpected":true}`), 0o600); err != nil {
			t.Fatal(err)
		}
		if err := run([]string{manifestPath, filepath.Join(root, "release")}, &bytes.Buffer{}); err == nil {
			t.Fatal("accepted an unknown manifest field")
		}
	})

	t.Run("absolute material", func(t *testing.T) {
		root := t.TempDir()
		manifestPath := writeManifest(t, root, inputManifest{
			Schema: "amsftp-public-release-manifest-v1", Version: "1.0.0",
			Commit: strings.Repeat("1", 40), Tree: strings.Repeat("2", 40), SourceDateEpoch: 1,
			Materials: manifestMaterials{License: "/etc/passwd", Notice: "NOTICE", Install: "INSTALL.md", Uninstall: "UNINSTALL.md"},
		})
		if err := run([]string{manifestPath, filepath.Join(root, "release")}, &bytes.Buffer{}); err == nil {
			t.Fatal("accepted an absolute material path")
		}
	})
}

func writeFixtureFile(t *testing.T, root, name, body string) {
	t.Helper()
	path := filepath.Join(root, filepath.FromSlash(name))
	if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
}

func writeManifest(t *testing.T, root string, manifest inputManifest) string {
	t.Helper()
	raw, err := json.Marshal(manifest)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(root, "manifest.json")
	if err := os.WriteFile(path, raw, 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}
