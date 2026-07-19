package releasepack

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"sort"
	"strings"
	"testing"
	"time"
)

func TestPublicBundleHasExactFourArchiveNamesAndRequiredDeterministicContents(t *testing.T) {
	request := releaseFixture(t)
	first, err := BuildPublicBundle(request)
	if err != nil {
		t.Fatal(err)
	}
	second, err := BuildPublicBundle(request)
	if err != nil {
		t.Fatal(err)
	}
	wantNames := []string{
		"amsftp_1.0.0_darwin_amd64.tar.gz",
		"amsftp_1.0.0_darwin_arm64.tar.gz",
		"amsftp_1.0.0_linux_amd64.tar.gz",
		"amsftp_1.0.0_linux_arm64.tar.gz",
	}
	if got := archiveNames(first.Archives); !reflect.DeepEqual(got, wantNames) {
		t.Fatalf("archive names = %#v, want %#v", got, wantNames)
	}
	for index, archive := range first.Archives {
		if !bytes.Equal(archive.Bytes, second.Archives[index].Bytes) {
			t.Fatalf("archive %q is not deterministic", archive.Name)
		}
		entries := readArchive(t, archive.Bytes)
		root := strings.TrimSuffix(archive.Name, ".tar.gz")
		wantEntries := []string{root + "/", root + "/INSTALL.md", root + "/LICENSE", root + "/NOTICE", root + "/UNINSTALL.md", root + "/VERSION.json", root + "/amsftp", root + "/share/", root + "/share/man/", root + "/share/man/man1/", root + "/share/man/man1/amsftp.1"}
		if got := archiveEntryNames(entries); !reflect.DeepEqual(got, wantEntries) {
			t.Fatalf("%s entries = %#v, want %#v", archive.Name, got, wantEntries)
		}
		for _, entry := range entries {
			if !entry.ModTime.Equal(time.Unix(request.SourceDateEpoch, 0).UTC()) || entry.Uid != 0 || entry.Gid != 0 || entry.Uname != "" || entry.Gname != "" {
				t.Fatalf("%s header is not canonical: %#v", entry.Name, entry.Header)
			}
			if strings.HasSuffix(entry.Name, "/amsftp") && entry.Mode != 0o755 {
				t.Fatalf("binary mode = %04o", entry.Mode)
			}
			if !strings.HasSuffix(entry.Name, "/") && !strings.HasSuffix(entry.Name, "/amsftp") && entry.Mode != 0o644 {
				t.Fatalf("material mode for %s = %04o", entry.Name, entry.Mode)
			}
		}
		versionEntry := entries[root+"/VERSION.json"]
		var metadata VersionMetadata
		if err := json.Unmarshal(versionEntry.Body, &metadata); err != nil {
			t.Fatal(err)
		}
		if metadata.Version != request.Version || metadata.Commit != request.Commit || metadata.Tree != request.Tree || metadata.Target != archive.Target || metadata.ReleaseCandidate || !metadata.ProductionHelperClosed {
			t.Fatalf("version metadata = %#v", metadata)
		}
		if metadata.ApplicationID != "io.github.tyrantlucifer.amsftp" || metadata.LaunchdLabel != "io.github.tyrantlucifer.amsftp.daemon" || metadata.SystemdUserUnit != "amsftp-daemon.service" || metadata.HomebrewFormula != "amsftp" {
			t.Fatalf("ADR-0009 identifiers = %#v", metadata)
		}
	}
}

func TestPublicBundleChecksumsSBOMAndProvenanceBindTheSameFourArchives(t *testing.T) {
	bundle, err := BuildPublicBundle(releaseFixture(t))
	if err != nil {
		t.Fatal(err)
	}
	wantChecksumLines := make([]string, 0, len(bundle.Archives))
	for _, archive := range bundle.Archives {
		digest := sha256.Sum256(archive.Bytes)
		wantChecksumLines = append(wantChecksumLines, hex.EncodeToString(digest[:])+"  "+archive.Name)
	}
	if got, want := string(bundle.Checksums), strings.Join(wantChecksumLines, "\n")+"\n"; got != want {
		t.Fatalf("checksums = %q, want %q", got, want)
	}

	var sbom SPDXDocument
	if err := json.Unmarshal(bundle.SBOM, &sbom); err != nil {
		t.Fatal(err)
	}
	if sbom.SPDXVersion != "SPDX-2.3" || sbom.DataLicense != "CC0-1.0" || len(sbom.Packages) != 4+len(releaseFixture(t).Modules) {
		t.Fatalf("SBOM header/packages = %#v", sbom)
	}
	for _, archive := range bundle.Archives {
		if !sbomBindsArchive(sbom, archive) {
			t.Fatalf("SBOM does not bind %s", archive.Name)
		}
	}

	var provenance ProvenanceInput
	if err := json.Unmarshal(bundle.Provenance, &provenance); err != nil {
		t.Fatal(err)
	}
	if provenance.Schema != "amsftp-release-provenance-input-v1" || provenance.ReleaseCandidate || !provenance.ProductionHelperClosed || len(provenance.Archives) != 4 {
		t.Fatalf("provenance = %#v", provenance)
	}
	for _, archive := range bundle.Archives {
		if !provenanceBindsArchive(provenance, archive) {
			t.Fatalf("provenance does not bind %s", archive.Name)
		}
	}
}

func TestPublicBundleRejectsMissingMaterialWrongTargetSetAndFixtureTrustLeak(t *testing.T) {
	request := releaseFixture(t)
	request.Materials.License = nil
	if _, err := BuildPublicBundle(request); err == nil {
		t.Fatal("bundle accepted missing LICENSE")
	}
	request = releaseFixture(t)
	request.Materials.Man = nil
	if _, err := BuildPublicBundle(request); err == nil {
		t.Fatal("bundle accepted missing man page")
	}
	request = releaseFixture(t)
	request.Platforms = request.Platforms[:3]
	if _, err := BuildPublicBundle(request); err == nil {
		t.Fatal("bundle accepted an incomplete target set")
	}
	request = releaseFixture(t)
	request.Materials.Notice = []byte("contains testdata/nonrelease-helper-fixture and fixture signing key\n")
	if _, err := BuildPublicBundle(request); err == nil {
		t.Fatal("bundle accepted fixture-trust material")
	}
}

func TestProductionHelperAdmissionEnforcesDarwinAcceptedSignedAndLinuxFinalUnsignedBytes(t *testing.T) {
	linuxBytes := []byte("final unsigned linux binary")
	linux := PlatformBinary{Target: Target{OS: "linux", Arch: "amd64"}, Bytes: linuxBytes, State: BinaryLinuxFinalUnsigned}
	if frozen, err := AdmitProductionHelperBinary(linux); err != nil || frozen.SHA256 != digestHex(linuxBytes) {
		t.Fatalf("linux admission = %#v, %v", frozen, err)
	}
	linux.State = BinaryPublicPreview
	if _, err := AdmitProductionHelperBinary(linux); err == nil {
		t.Fatal("production admission accepted preview Linux bytes")
	}

	darwinBytes := []byte("accepted Developer ID signed darwin binary")
	darwin := PlatformBinary{
		Target: Target{OS: "darwin", Arch: "arm64"}, Bytes: darwinBytes, State: BinaryDarwinAcceptedSigned,
		Darwin: &DarwinEvidence{
			DeveloperIDApplication: "Developer ID Application: Example (TEAMID1234)", TeamID: "TEAMID1234", LeafFingerprint: strings.Repeat("a", 64),
			HardenedRuntime: true, TrustedTimestamp: true, StrictVerified: true, NotaryStatus: "Accepted",
			SubmissionID: "11111111-2222-3333-4444-555555555555", CDHash: strings.Repeat("b", 40), AcceptedZIPBinarySHA256: digestHex(darwinBytes),
		},
	}
	if frozen, err := AdmitProductionHelperBinary(darwin); err != nil || frozen.SHA256 != digestHex(darwinBytes) {
		t.Fatalf("darwin admission = %#v, %v", frozen, err)
	}
	for name, mutate := range map[string]func(*PlatformBinary){
		"pre-sign":         func(value *PlatformBinary) { value.State = BinaryPublicPreview },
		"notary pending":   func(value *PlatformBinary) { value.Darwin.NotaryStatus = "In Progress" },
		"no runtime":       func(value *PlatformBinary) { value.Darwin.HardenedRuntime = false },
		"no timestamp":     func(value *PlatformBinary) { value.Darwin.TrustedTimestamp = false },
		"no strict verify": func(value *PlatformBinary) { value.Darwin.StrictVerified = false },
		"ZIP byte drift":   func(value *PlatformBinary) { value.Darwin.AcceptedZIPBinarySHA256 = strings.Repeat("c", 64) },
	} {
		t.Run(name, func(t *testing.T) {
			candidate := darwin
			evidence := *darwin.Darwin
			candidate.Darwin = &evidence
			mutate(&candidate)
			if _, err := AdmitProductionHelperBinary(candidate); err == nil {
				t.Fatal("invalid Darwin evidence was admitted")
			}
		})
	}
}

func TestArchiveNameAndReleaseIdentityRejectNonCanonicalInputs(t *testing.T) {
	for _, version := range []string{"v1.0.0", "1.0", "1.0.0-rc1", "../1.0.0"} {
		request := releaseFixture(t)
		request.Version = version
		if _, err := BuildPublicBundle(request); err == nil {
			t.Fatalf("accepted version %q", version)
		}
	}
	request := releaseFixture(t)
	request.Commit = strings.Repeat("A", 40)
	if _, err := BuildPublicBundle(request); err == nil {
		t.Fatal("accepted noncanonical commit")
	}
}

func TestPublicBundleRejectsUnboundDirtyOrWrongTargetGoBuildEvidence(t *testing.T) {
	for name, mutate := range map[string]func(*GoBuildEvidence){
		"wrong package":  func(evidence *GoBuildEvidence) { evidence.MainPath = "example.invalid/not-amsftp" },
		"wrong target":   func(evidence *GoBuildEvidence) { evidence.GOARCH = "386" },
		"cgo enabled":    func(evidence *GoBuildEvidence) { evidence.CGOEnabled = true },
		"no trimpath":    func(evidence *GoBuildEvidence) { evidence.Trimpath = false },
		"dirty vcs":      func(evidence *GoBuildEvidence) { evidence.VCSModified = true },
		"wrong revision": func(evidence *GoBuildEvidence) { evidence.VCSRevision = strings.Repeat("3", 40) },
	} {
		t.Run(name, func(t *testing.T) {
			request := releaseFixture(t)
			evidence := *request.Platforms[0].Build
			request.Platforms[0].Build = &evidence
			mutate(&evidence)
			if _, err := BuildPublicBundle(request); err == nil {
				t.Fatal("accepted invalid Go build evidence")
			}
		})
	}
	if _, err := InspectGoBinary([]byte("not a Go executable")); err == nil {
		t.Fatal("inspector accepted non-Go bytes")
	}
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(executable) //nolint:gosec // os.Executable returns this exact test process.
	if err != nil {
		t.Fatal(err)
	}
	evidence, err := InspectGoBinary(raw)
	if err != nil {
		t.Fatal(err)
	}
	if evidence.GOOS != runtime.GOOS || evidence.GOARCH != runtime.GOARCH || evidence.MainPath == "" || evidence.GoVersion == "" {
		t.Fatalf("test executable evidence = %#v", evidence)
	}
}

func TestPublicBundleRequiresExactBinaryDependencyAndReplacementInventory(t *testing.T) {
	t.Run("target-specific dependency inventory", func(t *testing.T) {
		request := releaseFixture(t)
		darwinOnly := Module{Path: "example.com/darwin-only", Version: "v1.0.0", Sum: testModuleSum("darwin-only"), License: "MIT", Targets: []Target{{OS: "darwin", Arch: "amd64"}, {OS: "darwin", Arch: "arm64"}}}
		request.Modules = append(request.Modules, darwinOnly)
		for index := range request.Platforms {
			if request.Platforms[index].Target.OS != "darwin" {
				continue
			}
			evidence := *request.Platforms[index].Build
			evidence.Modules = append([]GoModuleEvidence(nil), evidence.Modules...)
			evidence.Modules = append(evidence.Modules, GoModuleEvidence{Path: darwinOnly.Path, Version: darwinOnly.Version, Sum: darwinOnly.Sum})
			request.Platforms[index].Build = &evidence
		}
		if _, err := BuildPublicBundle(request); err != nil {
			t.Fatalf("target-specific dependency inventory rejected: %v", err)
		}
		leaked := request
		leaked.Platforms = append([]PlatformBinary(nil), request.Platforms...)
		for index := range leaked.Platforms {
			if leaked.Platforms[index].Target.OS != "linux" {
				continue
			}
			evidence := *leaked.Platforms[index].Build
			evidence.Modules = append([]GoModuleEvidence(nil), evidence.Modules...)
			evidence.Modules = append(evidence.Modules, GoModuleEvidence{Path: darwinOnly.Path, Version: darwinOnly.Version, Sum: darwinOnly.Sum})
			leaked.Platforms[index].Build = &evidence
			break
		}
		if _, err := BuildPublicBundle(leaked); err == nil {
			t.Fatal("accepted target-specific dependency in an undeclared target")
		}
	})
	t.Run("missing binary dependency evidence", func(t *testing.T) {
		request := releaseFixture(t)
		evidence := *request.Platforms[0].Build
		evidence.Modules = nil
		request.Platforms[0].Build = &evidence
		if _, err := BuildPublicBundle(request); err == nil {
			t.Fatal("accepted incomplete binary dependency evidence")
		}
	})
	t.Run("undeclared binary dependency", func(t *testing.T) {
		request := releaseFixture(t)
		evidence := *request.Platforms[0].Build
		evidence.Modules = append([]GoModuleEvidence(nil), evidence.Modules...)
		evidence.Modules = append(evidence.Modules, GoModuleEvidence{Path: "example.com/undeclared", Version: "v9.9.9", Sum: testModuleSum("undeclared")})
		request.Platforms[0].Build = &evidence
		if _, err := BuildPublicBundle(request); err == nil {
			t.Fatal("accepted an undeclared linked dependency")
		}
	})
	t.Run("replacement drift", func(t *testing.T) {
		request := releaseFixture(t)
		replacement := &ModuleReplacement{Path: "example.com/fork", Version: "v1.2.4", Sum: testModuleSum("fork")}
		request.Modules[0].Replacement = replacement
		for index := range request.Platforms {
			evidence := *request.Platforms[index].Build
			evidence.Modules = []GoModuleEvidence{{Path: request.Modules[0].Path, Version: request.Modules[0].Version, Replacement: &GoModuleEvidence{Path: "example.com/other-fork", Version: "v1.2.4", Sum: testModuleSum("fork")}}}
			request.Platforms[index].Build = &evidence
		}
		if _, err := BuildPublicBundle(request); err == nil {
			t.Fatal("accepted replacement source drift")
		}
	})
	t.Run("replacement bound into SBOM", func(t *testing.T) {
		request := releaseFixture(t)
		replacement := &ModuleReplacement{Path: "example.com/fork", Version: "v1.2.4", Sum: testModuleSum("replacement")}
		request.Modules[0].Sum = ""
		request.Modules[0].Replacement = replacement
		for index := range request.Platforms {
			evidence := *request.Platforms[index].Build
			evidence.Modules = []GoModuleEvidence{{Path: request.Modules[0].Path, Version: request.Modules[0].Version, Replacement: &GoModuleEvidence{Path: replacement.Path, Version: replacement.Version, Sum: replacement.Sum}}}
			request.Platforms[index].Build = &evidence
		}
		bundle, err := BuildPublicBundle(request)
		if err != nil {
			t.Fatal(err)
		}
		var document SPDXDocument
		if err := json.Unmarshal(bundle.SBOM, &document); err != nil {
			t.Fatal(err)
		}
		dependency := document.Packages[len(document.Packages)-1]
		if dependency.Name != replacement.Path || dependency.VersionInfo != replacement.Version || !strings.Contains(dependency.Comment, request.Modules[0].Path+"@"+request.Modules[0].Version) {
			t.Fatalf("replacement SPDX package = %#v", dependency)
		}
	})
	t.Run("invalid sum or license", func(t *testing.T) {
		for name, mutate := range map[string]func(*Module){
			"invalid sum":          func(module *Module) { module.Sum = "h1:not-base64" },
			"no assertion":         func(module *Module) { module.License = "NOASSERTION" },
			"control license":      func(module *Module) { module.License = "MIT\nOR Apache-2.0" },
			"dangling operator":    func(module *Module) { module.License = "MIT OR" },
			"missing operator":     func(module *Module) { module.License = "MIT Apache-2.0" },
			"unclosed parentheses": func(module *Module) { module.License = "(MIT OR Apache-2.0" },
			"empty targets":        func(module *Module) { module.Targets = nil },
			"duplicate target":     func(module *Module) { module.Targets = []Target{Targets[0], Targets[0]} },
			"unsupported target":   func(module *Module) { module.Targets = []Target{{OS: "windows", Arch: "amd64"}} },
		} {
			t.Run(name, func(t *testing.T) {
				request := releaseFixture(t)
				mutate(&request.Modules[0])
				if _, err := BuildPublicBundle(request); err == nil {
					t.Fatal("accepted invalid dependency declaration")
				}
			})
		}
	})
	t.Run("compound SPDX expression", func(t *testing.T) {
		request := releaseFixture(t)
		request.Modules[0].License = "BSD-3-Clause AND MIT"
		if _, err := BuildPublicBundle(request); err != nil {
			t.Fatalf("valid compound SPDX expression rejected: %v", err)
		}
	})
}

func TestWriteBundleCreatesExactReleaseDirectoryOnceWithoutOverwrite(t *testing.T) {
	bundle, err := BuildPublicBundle(releaseFixture(t))
	if err != nil {
		t.Fatal(err)
	}
	output := filepath.Join(t.TempDir(), "release")
	if err := WriteBundle(output, bundle); err != nil {
		t.Fatal(err)
	}
	want := []string{
		"amsftp_1.0.0_darwin_amd64.tar.gz",
		"amsftp_1.0.0_darwin_arm64.tar.gz",
		"amsftp_1.0.0_linux_amd64.tar.gz",
		"amsftp_1.0.0_linux_arm64.tar.gz",
		"checksums.txt",
		"provenance.input.json",
		"sbom.spdx.json",
	}
	entries, err := os.ReadDir(output)
	if err != nil {
		t.Fatal(err)
	}
	got := make([]string, 0, len(entries))
	for _, entry := range entries {
		got = append(got, entry.Name())
		info, err := entry.Info()
		if err != nil {
			t.Fatal(err)
		}
		if !info.Mode().IsRegular() || info.Mode().Perm() != 0o644 {
			t.Fatalf("output %q mode = %v", entry.Name(), info.Mode())
		}
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("release files = %#v, want %#v", got, want)
	}
	if err := WriteBundle(output, bundle); err == nil {
		t.Fatal("writer overwrote an existing release directory")
	}
	raw, err := os.ReadFile(filepath.Join(output, "checksums.txt")) //nolint:gosec // exact file in a test-owned release directory.
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(raw, bundle.Checksums) {
		t.Fatal("checksums changed while writing")
	}
}

func releaseFixture(t *testing.T) BundleRequest {
	t.Helper()
	platforms := make([]PlatformBinary, 0, len(Targets))
	modules := []Module{{Path: "example.com/dependency", Version: "v1.2.3", Sum: testModuleSum("fixture"), License: "BSD-3-Clause", Targets: Targets[:]}}
	for _, target := range Targets {
		platforms = append(platforms, PlatformBinary{
			Target: target, Bytes: []byte(fmt.Sprintf("fixture binary %s/%s\n", target.OS, target.Arch)), State: BinaryPublicPreview,
			Build: &GoBuildEvidence{MainPath: "github.com/TyrantLucifer/awesome-sftp-cli/cmd/amsftp", GOOS: target.OS, GOARCH: target.Arch, CGOEnabled: false, Trimpath: true, VCSRevision: strings.Repeat("1", 40), Modules: []GoModuleEvidence{{Path: modules[0].Path, Version: modules[0].Version, Sum: modules[0].Sum}}},
		})
	}
	return BundleRequest{
		Version: "1.0.0", Commit: strings.Repeat("1", 40), Tree: strings.Repeat("2", 40), SourceDateEpoch: 1_700_000_000,
		Materials: Materials{License: []byte("fixture project license\n"), Notice: []byte("fixture third-party notices\n"), Install: []byte("fixture install\n"), Uninstall: []byte("fixture uninstall\n"), Man: []byte(".TH AMSFTP 1\n")},
		Platforms: platforms,
		Modules:   modules,
	}
}

func testModuleSum(seed string) string {
	digest := sha256.Sum256([]byte(seed))
	return "h1:" + base64.StdEncoding.EncodeToString(digest[:])
}

type archiveEntry struct {
	*tar.Header
	Body []byte
}

func readArchive(t *testing.T, raw []byte) map[string]archiveEntry {
	t.Helper()
	gzipReader, err := gzip.NewReader(bytes.NewReader(raw))
	if err != nil {
		t.Fatal(err)
	}
	defer gzipReader.Close()
	reader := tar.NewReader(gzipReader)
	result := make(map[string]archiveEntry)
	for {
		header, err := reader.Next()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			t.Fatal(err)
		}
		body, err := io.ReadAll(reader)
		if err != nil {
			t.Fatal(err)
		}
		result[header.Name] = archiveEntry{Header: header, Body: body}
	}
	return result
}

func archiveNames(archives []Archive) []string {
	result := make([]string, 0, len(archives))
	for _, archive := range archives {
		result = append(result, archive.Name)
	}
	return result
}

func archiveEntryNames(entries map[string]archiveEntry) []string {
	result := make([]string, 0, len(entries))
	for name := range entries {
		result = append(result, name)
	}
	sort.Strings(result)
	return result
}

func digestHex(raw []byte) string {
	digest := sha256.Sum256(raw)
	return hex.EncodeToString(digest[:])
}

func sbomBindsArchive(document SPDXDocument, archive Archive) bool {
	for _, item := range document.Packages {
		if item.Name == archive.Name && item.DownloadLocation == "NOASSERTION" && item.FilesAnalyzed == false && len(item.Checksums) == 1 && item.Checksums[0].Algorithm == "SHA256" && item.Checksums[0].ChecksumValue == digestHex(archive.Bytes) {
			return true
		}
	}
	return false
}

func provenanceBindsArchive(input ProvenanceInput, archive Archive) bool {
	for _, item := range input.Archives {
		if item.Name == archive.Name && item.Target == archive.Target && item.SHA256 == digestHex(archive.Bytes) && item.Size == uint64(len(archive.Bytes)) {
			return true
		}
	}
	return false
}
