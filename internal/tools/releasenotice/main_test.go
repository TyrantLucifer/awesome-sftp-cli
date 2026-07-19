package main

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/TyrantLucifer/awesome-sftp-cli/internal/releasepack"
)

func TestRunRendersExactResolvedRuntimeLicenseMaterials(t *testing.T) {
	root := t.TempDir()
	plainRoot := filepath.Join(root, "plain")
	forkRoot := filepath.Join(root, "fork")
	writeLicenseFile(t, plainRoot, "LICENSE", "plain license\n")
	writeLicenseFile(t, forkRoot, "LICENSE", "fork license\n")
	writeLicenseFile(t, forkRoot, "LICENSE-3RD-PARTY.md", "third-party terms\n")

	runtimePath := writeJSON(t, root, "runtime.json", []releasepack.Module{
		{Path: "example.com/plain", Version: "v1.0.0", Sum: validModuleSum("A"), License: "MIT", Targets: releasepack.Targets[:]},
		{
			Path: "example.com/original", Version: "v2.0.0", License: "BSD-3-Clause AND MIT", Targets: releasepack.Targets[:],
			Replacement: &releasepack.ModuleReplacement{Path: "example.com/fork", Version: "v2.0.1-0.20260718000000-abcdef123456", Sum: validModuleSum("B")},
		},
	})
	materialsPath := writeJSON(t, root, "materials.json", materialManifest{
		Schema: materialManifestSchema,
		Modules: []materialModule{
			materialFixture("example.com/plain", "v1.0.0", "example.com/plain", "v1.0.0", "MIT", plainRoot, "LICENSE"),
			materialFixture("example.com/original", "v2.0.0", "example.com/fork", "v2.0.1-0.20260718000000-abcdef123456", "BSD-3-Clause AND MIT", forkRoot, "LICENSE", "LICENSE-3RD-PARTY.md"),
		},
	})
	resolved := map[string]string{
		"example.com/plain@v1.0.0":                              plainRoot,
		"example.com/fork@v2.0.1-0.20260718000000-abcdef123456": forkRoot,
	}

	var output bytes.Buffer
	if err := runWithModuleDirectories([]string{runtimePath, materialsPath}, &output, resolved); err != nil {
		t.Fatal(err)
	}
	rendered := output.String()
	for _, contract := range []string{
		"Declared module: example.com/original@v2.0.0\n",
		"Resolved module: example.com/fork@v2.0.1-0.20260718000000-abcdef123456\n",
		"License source: LICENSE-3RD-PARTY.md\n",
		"third-party terms\n",
		"Declared module: example.com/plain@v1.0.0\n",
	} {
		if strings.Count(rendered, contract) != 1 {
			t.Fatalf("generated notice contract %q count != 1:\n%s", contract, rendered)
		}
	}
}

func TestRunRejectsUnknownFieldsTamperedSourcesAndUnsafeFiles(t *testing.T) {
	root := t.TempDir()
	moduleRoot := filepath.Join(root, "module")
	writeLicenseFile(t, moduleRoot, "LICENSE", "license\n")
	runtimePath := writeJSON(t, root, "runtime.json", []releasepack.Module{{
		Path: "example.com/module", Version: "v1.0.0", Sum: validModuleSum("A"), License: "MIT", Targets: releasepack.Targets[:],
	}})
	material := materialFixture("example.com/module", "v1.0.0", "example.com/module", "v1.0.0", "MIT", moduleRoot, "LICENSE")
	resolved := map[string]string{"example.com/module@v1.0.0": moduleRoot}

	t.Run("unknown field", func(t *testing.T) {
		materialsPath := filepath.Join(root, "unknown.json")
		if err := os.WriteFile(materialsPath, []byte(`{"schema":"amsftp-third-party-materials-v1","modules":[],"unknown":true}`), 0o600); err != nil {
			t.Fatal(err)
		}
		if err := runWithModuleDirectories([]string{runtimePath, materialsPath}, &bytes.Buffer{}, resolved); err == nil {
			t.Fatal("accepted an unknown material field")
		}
	})

	t.Run("tampered bytes", func(t *testing.T) {
		materialsPath := writeJSON(t, root, "tampered.json", materialManifest{Schema: materialManifestSchema, Modules: []materialModule{material}})
		writeLicenseFile(t, moduleRoot, "LICENSE", "changed\n")
		if err := runWithModuleDirectories([]string{runtimePath, materialsPath}, &bytes.Buffer{}, resolved); err == nil {
			t.Fatal("accepted license bytes that did not match the reviewed digest")
		}
		writeLicenseFile(t, moduleRoot, "LICENSE", "license\n")
	})

	t.Run("symlink", func(t *testing.T) {
		target := filepath.Join(moduleRoot, "TARGET")
		writeLicenseFile(t, moduleRoot, "TARGET", "license\n")
		link := filepath.Join(moduleRoot, "LINK")
		if err := os.Symlink(target, link); err != nil {
			t.Fatal(err)
		}
		candidate := material
		candidate.Files = []materialFile{{Name: "LINK", SHA256: digestText("license\n")}}
		materialsPath := writeJSON(t, root, "symlink.json", materialManifest{Schema: materialManifestSchema, Modules: []materialModule{candidate}})
		if err := runWithModuleDirectories([]string{runtimePath, materialsPath}, &bytes.Buffer{}, resolved); err == nil {
			t.Fatal("accepted a symbolic-link license source")
		}
	})
}

func TestRunCheckRequiresByteExactCommittedNotice(t *testing.T) {
	root := t.TempDir()
	moduleRoot := filepath.Join(root, "module")
	writeLicenseFile(t, moduleRoot, "LICENSE", "license\n")
	runtimePath := writeJSON(t, root, "runtime.json", []releasepack.Module{{
		Path: "example.com/module", Version: "v1.0.0", Sum: validModuleSum("A"), License: "MIT", Targets: releasepack.Targets[:],
	}})
	material := materialFixture("example.com/module", "v1.0.0", "example.com/module", "v1.0.0", "MIT", moduleRoot, "LICENSE")
	materialsPath := writeJSON(t, root, "materials.json", materialManifest{Schema: materialManifestSchema, Modules: []materialModule{material}})
	resolved := map[string]string{"example.com/module@v1.0.0": moduleRoot}
	var generated bytes.Buffer
	if err := runWithModuleDirectories([]string{runtimePath, materialsPath}, &generated, resolved); err != nil {
		t.Fatal(err)
	}
	noticePath := filepath.Join(root, "NOTICE")
	if err := os.WriteFile(noticePath, generated.Bytes(), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := runWithModuleDirectories([]string{"--check", runtimePath, materialsPath, noticePath}, &bytes.Buffer{}, resolved); err != nil {
		t.Fatalf("byte-exact notice rejected: %v", err)
	}
	if err := os.WriteFile(noticePath, append(generated.Bytes(), '\n'), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := runWithModuleDirectories([]string{"--check", runtimePath, materialsPath, noticePath}, &bytes.Buffer{}, resolved); err == nil {
		t.Fatal("drifted committed notice was accepted")
	}
}

func materialFixture(path, version, resolvedPath, resolvedVersion, license, root string, names ...string) materialModule {
	files := make([]materialFile, 0, len(names))
	for _, name := range names {
		raw, err := os.ReadFile(filepath.Join(root, name)) //nolint:gosec // names are fixed test-fixture files below a temporary directory.
		if err != nil {
			panic(err)
		}
		files = append(files, materialFile{Name: name, SHA256: digestBytes(raw)})
	}
	return materialModule{Path: path, Version: version, ResolvedPath: resolvedPath, ResolvedVersion: resolvedVersion, License: license, Files: files}
}

func writeLicenseFile(t *testing.T, root, name, body string) {
	t.Helper()
	if err := os.MkdirAll(root, 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, name), []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
}

func writeJSON(t *testing.T, root, name string, value any) string {
	t.Helper()
	raw, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	file := filepath.Join(root, name)
	if err := os.WriteFile(file, append(raw, '\n'), 0o600); err != nil {
		t.Fatal(err)
	}
	return file
}

func digestText(value string) string { return digestBytes([]byte(value)) }

func digestBytes(raw []byte) string {
	digest := sha256.Sum256(raw)
	return hex.EncodeToString(digest[:])
}

func validModuleSum(character string) string {
	return "h1:" + strings.Repeat(character, 43) + "="
}
