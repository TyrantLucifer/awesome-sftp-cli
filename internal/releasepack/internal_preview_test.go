package releasepack

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func TestInternalPreviewBundleIsDistinctAndPublicAdmissionRejectsIt(t *testing.T) {
	request := internalPreviewFixture(t)
	bundle, err := BuildInternalPreviewBundle(request)
	if err != nil {
		t.Fatal(err)
	}
	if len(bundle.Archives) != len(Targets) {
		t.Fatalf("internal archives = %d, want %d", len(bundle.Archives), len(Targets))
	}
	if _, err := BuildPublicBundle(request); err == nil {
		t.Fatal("public release admission accepted an internal preview")
	}

	publicWithInternalMaterial := releaseFixture(t)
	publicWithInternalMaterial.Materials.InternalPreview = request.Materials.InternalPreview
	if _, err := BuildPublicBundle(publicWithInternalMaterial); err == nil {
		t.Fatal("public release admission accepted internal-only material")
	}

	wrongState := request
	wrongState.Platforms = append([]PlatformBinary(nil), request.Platforms...)
	wrongState.Platforms[0].State = BinaryPublicPreview
	if _, err := BuildInternalPreviewBundle(wrongState); err == nil {
		t.Fatal("internal preview accepted a public-preview binary state")
	}
}

func TestInternalPreviewHasInstallableFourTargetContentsAndClosedProductionClaims(t *testing.T) {
	request := internalPreviewFixture(t)
	bundle, err := BuildInternalPreviewBundle(request)
	if err != nil {
		t.Fatal(err)
	}
	wantNames := []string{
		"amsftp_0.1.0-internal_darwin_amd64.tar.gz",
		"amsftp_0.1.0-internal_darwin_arm64.tar.gz",
		"amsftp_0.1.0-internal_linux_amd64.tar.gz",
		"amsftp_0.1.0-internal_linux_arm64.tar.gz",
	}
	if got := archiveNames(bundle.Archives); !reflect.DeepEqual(got, wantNames) {
		t.Fatalf("archive names = %#v, want %#v", got, wantNames)
	}
	for _, archive := range bundle.Archives {
		root := strings.TrimSuffix(archive.Name, ".tar.gz")
		entries := readArchive(t, archive.Bytes)
		for _, name := range []string{
			root + "/INTERNAL-PREVIEW.md",
			root + "/amsftp",
			root + "/share/man/man1/amsftp.1",
			root + "/share/bash-completion/completions/amsftp",
			root + "/share/zsh/site-functions/_amsftp",
			root + "/share/fish/vendor_completions.d/amsftp.fish",
		} {
			if _, ok := entries[name]; !ok {
				t.Fatalf("%s missing %s", archive.Name, name)
			}
		}
		var metadata VersionMetadata
		if err := json.Unmarshal(entries[root+"/VERSION.json"].Body, &metadata); err != nil {
			t.Fatal(err)
		}
		if metadata.Version != InternalPreviewVersion || metadata.Distribution != "internal_preview" || !metadata.NonRedistributable || !metadata.ProductionHelperClosed || !metadata.ProductionLevel2Closed || metadata.ReleaseCandidate {
			t.Fatalf("internal metadata = %#v", metadata)
		}
	}
	if len(bundle.Checksums) == 0 || len(bundle.SBOM) == 0 || len(bundle.Provenance) == 0 {
		t.Fatal("internal preview omitted checksums, SBOM, or provenance")
	}
	var provenance ProvenanceInput
	if err := json.Unmarshal(bundle.Provenance, &provenance); err != nil {
		t.Fatal(err)
	}
	if provenance.Version != InternalPreviewVersion || provenance.Distribution != "internal_preview" || !provenance.NonRedistributable || !provenance.ProductionHelperClosed || !provenance.ProductionLevel2Closed || len(provenance.Archives) != len(Targets) {
		t.Fatalf("internal provenance = %#v", provenance)
	}
}

func TestInternalPreviewWritesExactBundleOnceWithoutOverwrite(t *testing.T) {
	request := internalPreviewFixture(t)
	bundle, err := BuildInternalPreviewBundle(request)
	if err != nil {
		t.Fatal(err)
	}
	parent := t.TempDir()
	destination := filepath.Join(parent, "amsftp_"+InternalPreviewVersion)
	if err := WriteBundle(destination, bundle); err != nil {
		t.Fatal(err)
	}
	entries, err := os.ReadDir(destination)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != len(Targets)+3 {
		t.Fatalf("written files = %d, want %d", len(entries), len(Targets)+3)
	}
	if err := WriteBundle(destination, bundle); err == nil {
		t.Fatal("internal preview bundle overwrote an existing destination")
	}
}

func internalPreviewFixture(t *testing.T) BundleRequest {
	t.Helper()
	request := releaseFixture(t)
	request.Version = InternalPreviewVersion
	request.Materials.InternalPreview = []byte("AMSFTP INTERNAL PREVIEW\nOwner-only and not for redistribution.\nUnsigned. Production Helper: CLOSED. Level 2: CLOSED.\n")
	request.Materials.BashCompletion = []byte("complete -F _amsftp amsftp\n")
	request.Materials.ZshCompletion = []byte("#compdef amsftp\n")
	request.Materials.FishCompletion = []byte("complete -c amsftp\n")
	for index := range request.Platforms {
		request.Platforms[index].State = BinaryInternalPreview
	}
	return request
}
