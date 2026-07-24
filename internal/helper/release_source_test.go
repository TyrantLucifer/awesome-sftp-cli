package helper

import (
	"archive/tar"
	"bufio"
	"bytes"
	"compress/gzip"
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestProductionReleaseAssetsFreezeImmutableFourTargetIdentity(t *testing.T) {
	want := ReleaseAssets{
		ArchiveName:   "amsftp_1.0.0_linux_amd64.tar.gz",
		ManifestName:  "amsftp_1.0.0_linux_amd64.helper-manifest",
		SignatureName: "amsftp_1.0.0_linux_amd64.helper-manifest.sig",
		ArchiveURL:    "https://github.com/TyrantLucifer/awesome-sftp-cli/releases/download/v1.0.0/amsftp_1.0.0_linux_amd64.tar.gz",
		ManifestURL:   "https://github.com/TyrantLucifer/awesome-sftp-cli/releases/download/v1.0.0/amsftp_1.0.0_linux_amd64.helper-manifest",
		SignatureURL:  "https://github.com/TyrantLucifer/awesome-sftp-cli/releases/download/v1.0.0/amsftp_1.0.0_linux_amd64.helper-manifest.sig",
	}
	got, err := ProductionReleaseAssets("1.0.0", Target{OS: "linux", Arch: "amd64"})
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("assets = %#v, want %#v", got, want)
	}
	for _, target := range []Target{{OS: "darwin", Arch: "amd64"}, {OS: "darwin", Arch: "arm64"}, {OS: "linux", Arch: "amd64"}, {OS: "linux", Arch: "arm64"}} {
		if _, err := ProductionReleaseAssets("1.0.0", target); err != nil {
			t.Fatalf("canonical target %#v: %v", target, err)
		}
	}
	for _, test := range []struct {
		version string
		target  Target
	}{{"v1.0.0", Target{OS: "linux", Arch: "amd64"}}, {"1.0.0-rc1", Target{OS: "linux", Arch: "amd64"}}, {"../1.0.0", Target{OS: "linux", Arch: "amd64"}}, {"1.0.0", Target{OS: "freebsd", Arch: "amd64"}}, {"1.0.0", Target{OS: "linux", Arch: "386"}}} {
		if _, err := ProductionReleaseAssets(test.version, test.target); err == nil {
			t.Fatalf("accepted version %q target %#v", test.version, test.target)
		}
	}
}

func TestProductionReleaseSourceAllowsOnlyExpectedHTTPSCDNRedirects(t *testing.T) {
	source := NewProductionReleaseSource()
	initialURL, err := url.Parse("https://github.com/TyrantLucifer/awesome-sftp-cli/releases/download/v1.0.0/asset")
	if err != nil {
		t.Fatal(err)
	}
	via := []*http.Request{{URL: initialURL}}
	for _, rawURL := range []string{"https://release-assets.githubusercontent.com/github-production-release-asset/asset", "https://objects.githubusercontent.com/github-production-release-asset/asset"} {
		redirectURL, err := url.Parse(rawURL)
		if err != nil {
			t.Fatal(err)
		}
		if err := source.client.CheckRedirect(&http.Request{URL: redirectURL}, via); err != nil {
			t.Fatalf("official redirect %q: %v", rawURL, err)
		}
	}
	for _, rawURL := range []string{"http://release-assets.githubusercontent.com/asset", "https://example.invalid/asset", "https://github.com/other"} {
		redirectURL, err := url.Parse(rawURL)
		if err != nil {
			t.Fatal(err)
		}
		if err := source.client.CheckRedirect(&http.Request{URL: redirectURL}, via); err == nil {
			t.Fatalf("accepted redirect %q", rawURL)
		}
	}
	tooMany := append(append([]*http.Request(nil), via...), via...)
	tooMany = append(tooMany, via...)
	allowedURL, _ := url.Parse("https://release-assets.githubusercontent.com/asset")
	if err := source.client.CheckRedirect(&http.Request{URL: allowedURL}, tooMany); err == nil {
		t.Fatal("accepted an excessive redirect chain")
	}
}

func TestReleaseSourceVerifiesMetadataAndCurrentPolicyBeforeArchiveRead(t *testing.T) {
	fixture := newInstallerFixture(t)
	archive := canonicalHelperArchive(t, fixture.manifest.Target(), fixture.artifact)
	for name, mutate := range map[string]func(*releaseFixtureAssets, *Policy){
		"untrusted signature": func(assets *releaseFixtureAssets, _ *Policy) {
			assets.signature[0] ^= 1
		},
		"closed policy": func(_ *releaseFixtureAssets, policy *Policy) {
			*policy = NewProductionPolicy()
		},
		"signed target mismatch": func(assets *releaseFixtureAssets, _ *Policy) {
			assets.manifest = []byte(strings.Replace(string(assets.manifest), "arch=amd64", "arch=arm64", 1))
			assets.signature = fixtureSignature(t, assets.manifest)
		},
	} {
		t.Run(name, func(t *testing.T) {
			assets := releaseFixtureAssets{manifest: append([]byte(nil), fixture.request.RawManifest...), signature: append([]byte(nil), fixture.request.RawSignature...), archive: archive}
			policy := fixture.request.Policy
			mutate(&assets, &policy)
			server, hits := serveReleaseAssets(t, Target{OS: "linux", Arch: "amd64"}, assets)
			defer server.Close()
			source := newReleaseSource(server.Client(), server.URL)
			if _, err := source.Resolve(context.Background(), "4.0.0", Target{OS: "linux", Arch: "amd64"}, fixture.request.Verifier, policy); err == nil {
				t.Fatal("invalid release metadata resolved")
			}
			if got := hits.archiveCount(); got != 0 {
				t.Fatalf("archive reads = %d, want zero before trust admission", got)
			}
		})
	}
}

func TestReleaseSourceExtractsExactBoundBinaryAndComposesInstaller(t *testing.T) {
	fixture := newInstallerFixture(t)
	assets := releaseFixtureAssets{
		manifest:  fixture.request.RawManifest,
		signature: fixture.request.RawSignature,
		archive:   canonicalHelperArchive(t, fixture.manifest.Target(), fixture.artifact),
	}
	server, hits := serveReleaseAssets(t, fixture.manifest.Target(), assets)
	defer server.Close()
	source := newReleaseSource(server.Client(), server.URL)
	resolved, err := source.Resolve(context.Background(), "4.0.0", fixture.manifest.Target(), fixture.request.Verifier, fixture.request.Policy)
	if err != nil {
		t.Fatal(err)
	}
	if resolved.VerifiedManifest().ArtifactID() != fixture.manifest.ArtifactID() || resolved.Source() != server.URL+"/v4.0.0/amsftp_4.0.0_linux_amd64.tar.gz" {
		t.Fatalf("resolved identity = %#v from %q", resolved.VerifiedManifest(), resolved.Source())
	}
	if hits.archiveCount() != 0 {
		t.Fatalf("archive reads before consent/probe/high-water = %d", hits.archiveCount())
	}

	base := fixture.request
	base.RawManifest = nil
	base.RawSignature = nil
	base.Verifier = Verifier{}
	base.Policy = Policy{}
	base.Artifact = nil
	base.ArtifactSource = ""
	if _, err := resolved.BindInstallRequest(base, fixture.request.Verifier, NewProductionPolicy()); err == nil {
		t.Fatal("resolved release bypassed a newly closed current policy")
	}
	request, err := resolved.BindInstallRequest(base, fixture.request.Verifier, fixture.request.Policy)
	if err != nil {
		t.Fatal(err)
	}
	result, err := fixture.installer.Install(context.Background(), request)
	if err != nil {
		t.Fatal(err)
	}
	if !result.Enabled || !bytes.Equal(fixture.remote.files[result.FinalPath], fixture.artifact) {
		t.Fatalf("installed result = %#v bytes=%q", result, fixture.remote.files[result.FinalPath])
	}
	if hits.archiveCount() != 3 {
		t.Fatalf("archive reads = %d, want two drift-bound validations and one upload stream", hits.archiveCount())
	}
	if _, err := resolved.BindInstallRequest(request, fixture.request.Verifier, fixture.request.Policy); err == nil {
		t.Fatal("resolver overwrote an already-bound install request")
	}
}

func TestReleaseSourceDefersArchiveUntilPreliminaryConsent(t *testing.T) {
	fixture := newInstallerFixture(t)
	assets := releaseFixtureAssets{manifest: fixture.request.RawManifest, signature: fixture.request.RawSignature, archive: canonicalHelperArchive(t, fixture.manifest.Target(), fixture.artifact)}
	server, hits := serveReleaseAssets(t, fixture.manifest.Target(), assets)
	defer server.Close()
	resolved, err := newReleaseSource(server.Client(), server.URL).Resolve(context.Background(), "4.0.0", fixture.manifest.Target(), fixture.request.Verifier, fixture.request.Policy)
	if err != nil {
		t.Fatal(err)
	}
	base := fixture.request
	base.RawManifest, base.RawSignature, base.ArtifactSource = nil, nil, ""
	base.Verifier, base.Policy, base.Artifact = Verifier{}, Policy{}, nil
	base.Consent = &fakeInstallConsent{preliminary: PreliminaryApproval{Approved: false}, remote: fixture.remote}
	request, err := resolved.BindInstallRequest(base, fixture.request.Verifier, fixture.request.Policy)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := fixture.installer.Install(context.Background(), request); err == nil {
		t.Fatal("declined preliminary consent was accepted")
	}
	if hits.archiveCount() != 0 {
		t.Fatalf("archive reads before preliminary consent = %d", hits.archiveCount())
	}
}

func TestReleaseSourceRejectsNoncanonicalArchiveWithoutReturningArtifact(t *testing.T) {
	fixture := newInstallerFixture(t)
	for name, entries := range map[string][]releaseArchiveEntry{
		"wrong binary path": canonicalArchiveEntries(fixture.manifest.Target(), fixture.artifact, func(entries []releaseArchiveEntry) []releaseArchiveEntry {
			for index := range entries {
				if strings.HasSuffix(entries[index].name, "/amsftp") {
					entries[index].name = "other/amsftp"
				}
			}
			return entries
		}),
		"symlink binary": canonicalArchiveEntries(fixture.manifest.Target(), fixture.artifact, func(entries []releaseArchiveEntry) []releaseArchiveEntry {
			for index := range entries {
				if strings.HasSuffix(entries[index].name, "/amsftp") {
					entries[index].typeflag = tar.TypeSymlink
					entries[index].linkname = "/bin/sh"
					entries[index].body = nil
				}
			}
			return entries
		}),
		"duplicate binary": canonicalArchiveEntries(fixture.manifest.Target(), fixture.artifact, func(entries []releaseArchiveEntry) []releaseArchiveEntry {
			for _, entry := range entries {
				if strings.HasSuffix(entry.name, "/amsftp") {
					return append(entries, entry)
				}
			}
			return entries
		}),
		"unexpected entry": canonicalArchiveEntries(fixture.manifest.Target(), fixture.artifact, func(entries []releaseArchiveEntry) []releaseArchiveEntry {
			root := "amsftp_4.0.0_linux_amd64"
			return append(entries, releaseArchiveEntry{name: root + "/helper", mode: 0o755, typeflag: tar.TypeReg, body: fixture.artifact})
		}),
		"tampered binary": canonicalArchiveEntries(fixture.manifest.Target(), fixture.artifact, func(entries []releaseArchiveEntry) []releaseArchiveEntry {
			for index := range entries {
				if strings.HasSuffix(entries[index].name, "/amsftp") {
					entries[index].body = append([]byte(nil), entries[index].body...)
					entries[index].body[0] ^= 1
				}
			}
			return entries
		}),
		"GNU binary header": canonicalArchiveEntries(fixture.manifest.Target(), fixture.artifact, func(entries []releaseArchiveEntry) []releaseArchiveEntry {
			for index := range entries {
				if strings.HasSuffix(entries[index].name, "/amsftp") {
					entries[index].format = tar.FormatGNU
				}
			}
			return entries
		}),
	} {
		t.Run(name, func(t *testing.T) {
			assets := releaseFixtureAssets{manifest: fixture.request.RawManifest, signature: fixture.request.RawSignature, archive: writeHelperArchive(t, entries)}
			server, _ := serveReleaseAssets(t, fixture.manifest.Target(), assets)
			defer server.Close()
			resolved, err := newReleaseSource(server.Client(), server.URL).Resolve(context.Background(), "4.0.0", fixture.manifest.Target(), fixture.request.Verifier, fixture.request.Policy)
			if err != nil {
				t.Fatal(err)
			}
			request, err := resolved.BindInstallRequest(InstallRequest{}, fixture.request.Verifier, fixture.request.Policy)
			if err != nil {
				t.Fatal(err)
			}
			reader, err := request.Artifact(context.Background())
			if err == nil {
				err = ValidateArtifact(context.Background(), reader, fixture.manifest)
				_ = reader.Close()
			}
			if err == nil {
				t.Fatal("noncanonical archive resolved")
			}
		})
	}
}

func TestReleaseArchiveExpandedLimitIsEnforcedInsideTarReader(t *testing.T) {
	fixture := newInstallerFixture(t)
	archiveName := "amsftp_4.0.0_linux_amd64.tar.gz"
	archive := canonicalHelperArchive(t, fixture.manifest.Target(), fixture.artifact)
	reader, err := materializeCanonicalHelperBinaryWithLimit(context.Background(), bufio.NewReader(bytes.NewReader(archive)), archiveName, fixture.manifest, 1024)
	if err == nil {
		_ = reader.Close()
		t.Fatal("expanded archive crossed the hard tar-reader limit")
	}
}

type releaseFixtureAssets struct {
	manifest  []byte
	signature []byte
	archive   []byte
}

type releaseHits struct {
	mu      sync.Mutex
	archive int
}

func (h *releaseHits) archiveCount() int {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.archive
}

func serveReleaseAssets(t *testing.T, target Target, assets releaseFixtureAssets) (*httptest.Server, *releaseHits) {
	t.Helper()
	hits := &releaseHits{}
	version := "4.0.0"
	base := "amsftp_" + version + "_" + target.OS + "_" + target.Arch
	bodies := map[string][]byte{
		"/v" + version + "/" + base + ".helper-manifest":     assets.manifest,
		"/v" + version + "/" + base + ".helper-manifest.sig": assets.signature,
		"/v" + version + "/" + base + ".tar.gz":              assets.archive,
	}
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		body, ok := bodies[request.URL.Path]
		if !ok {
			http.NotFound(writer, request)
			return
		}
		if strings.HasSuffix(request.URL.Path, ".tar.gz") {
			hits.mu.Lock()
			hits.archive++
			hits.mu.Unlock()
		}
		writer.Header().Set("Content-Length", stringInt(len(body)))
		_, _ = writer.Write(body)
	}))
	return server, hits
}

type releaseArchiveEntry struct {
	name     string
	mode     int64
	typeflag byte
	format   tar.Format
	linkname string
	body     []byte
}

func canonicalHelperArchive(t *testing.T, target Target, binary []byte) []byte {
	t.Helper()
	return writeHelperArchive(t, canonicalArchiveEntries(target, binary, nil))
}

func canonicalArchiveEntries(target Target, binary []byte, mutate func([]releaseArchiveEntry) []releaseArchiveEntry) []releaseArchiveEntry {
	root := "amsftp_4.0.0_" + target.OS + "_" + target.Arch
	entries := []releaseArchiveEntry{
		{name: root + "/", mode: 0o755, typeflag: tar.TypeDir},
		{name: root + "/INSTALL.md", mode: 0o644, typeflag: tar.TypeReg, body: []byte("install and uninstall\n")},
		{name: root + "/LICENSE", mode: 0o644, typeflag: tar.TypeReg, body: []byte("license\n")},
		{name: root + "/NOTICE", mode: 0o644, typeflag: tar.TypeReg, body: []byte("notice\n")},
		{name: root + "/VERSION.json", mode: 0o644, typeflag: tar.TypeReg, body: []byte("{}\n")},
		{name: root + "/amsftp", mode: 0o755, typeflag: tar.TypeReg, body: binary},
		{name: root + "/share/", mode: 0o755, typeflag: tar.TypeDir},
		{name: root + "/share/man/", mode: 0o755, typeflag: tar.TypeDir},
		{name: root + "/share/man/man1/", mode: 0o755, typeflag: tar.TypeDir},
		{name: root + "/share/man/man1/amsftp.1", mode: 0o644, typeflag: tar.TypeReg, body: []byte("manual\n")},
	}
	if mutate != nil {
		entries = mutate(entries)
	}
	return entries
}

func writeHelperArchive(t *testing.T, entries []releaseArchiveEntry) []byte {
	t.Helper()
	var output bytes.Buffer
	gzipWriter := gzip.NewWriter(&output)
	gzipWriter.ModTime = time.Unix(1, 0)
	tarWriter := tar.NewWriter(gzipWriter)
	for _, entry := range entries {
		header := &tar.Header{Name: entry.name, Mode: entry.mode, Typeflag: entry.typeflag, Format: entry.format, Linkname: entry.linkname, Size: int64(len(entry.body)), ModTime: time.Unix(1, 0)}
		if entry.typeflag == tar.TypeSymlink || entry.typeflag == tar.TypeDir {
			header.Size = 0
		}
		if err := tarWriter.WriteHeader(header); err != nil {
			t.Fatal(err)
		}
		if len(entry.body) > 0 {
			if _, err := tarWriter.Write(entry.body); err != nil {
				t.Fatal(err)
			}
		}
	}
	if err := tarWriter.Close(); err != nil {
		t.Fatal(err)
	}
	if err := gzipWriter.Close(); err != nil {
		t.Fatal(err)
	}
	return output.Bytes()
}

func stringInt(value int) string {
	var buffer [32]byte
	position := len(buffer)
	for value > 0 {
		position--
		buffer[position] = byte('0' + value%10)
		value /= 10
	}
	if position == len(buffer) {
		return "0"
	}
	return string(buffer[position:])
}
