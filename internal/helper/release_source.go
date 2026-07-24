package helper

import (
	"archive/tar"
	"bufio"
	"compress/gzip"
	"context"
	"crypto/ed25519"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"
)

const (
	productionReleaseBaseURL          = "https://github.com/TyrantLucifer/awesome-sftp-cli/releases/download"
	maxHelperReleaseArchiveBytes      = MaxHelperArtifactBytes + 8<<20
	maxHelperReleaseExpandedBytes     = MaxHelperArtifactBytes + 8<<20
	maxHelperReleaseMaterialBytes     = 1 << 20
	maxHelperReleaseVersionEntryBytes = 64 << 10
	releaseMetadataTimeout            = 30 * time.Second
	releaseArchiveTimeout             = 5 * time.Minute
)

type ReleaseAssets struct {
	ArchiveName   string
	ManifestName  string
	SignatureName string
	ArchiveURL    string
	ManifestURL   string
	SignatureURL  string
}

func ProductionReleaseAssets(version string, target Target) (ReleaseAssets, error) {
	return releaseAssets(productionReleaseBaseURL, version, target)
}

func releaseAssets(baseURL, version string, target Target) (ReleaseAssets, error) {
	parsed, err := parseReleaseVersion(version)
	if err != nil || parsed.String() != version || !supportedReleaseTarget(target) {
		return ReleaseAssets{}, errors.New("resolve Helper release assets: version or target is invalid")
	}
	baseURL = strings.TrimSuffix(baseURL, "/")
	if baseURL == "" {
		return ReleaseAssets{}, errors.New("resolve Helper release assets: release base URL is invalid")
	}
	baseName := "amsftp_" + version + "_" + target.OS + "_" + target.Arch
	releaseURL := baseURL + "/v" + version + "/"
	archiveName := baseName + ".tar.gz"
	manifestName := baseName + ".helper-manifest"
	signatureName := manifestName + ".sig"
	return ReleaseAssets{
		ArchiveName: archiveName, ManifestName: manifestName, SignatureName: signatureName,
		ArchiveURL: releaseURL + archiveName, ManifestURL: releaseURL + manifestName, SignatureURL: releaseURL + signatureName,
	}, nil
}

func supportedReleaseTarget(target Target) bool {
	return (target.OS == "darwin" || target.OS == "linux") && (target.Arch == "amd64" || target.Arch == "arm64")
}

type ReleaseSource struct {
	client  *http.Client
	baseURL string
}

func NewProductionReleaseSource() ReleaseSource {
	return newReleaseSource(http.DefaultClient, productionReleaseBaseURL)
}

func newReleaseSource(client *http.Client, baseURL string) ReleaseSource {
	if client == nil {
		return ReleaseSource{baseURL: baseURL}
	}
	configured := *client
	configured.CheckRedirect = releaseRedirectPolicy(baseURL)
	return ReleaseSource{client: &configured, baseURL: strings.TrimSuffix(baseURL, "/")}
}

func releaseRedirectPolicy(baseURL string) func(*http.Request, []*http.Request) error {
	base, _ := url.Parse(baseURL)
	return func(request *http.Request, via []*http.Request) error {
		if request == nil || request.URL == nil || len(via) == 0 || len(via) >= 3 || request.URL.User != nil {
			return errors.New("helper release asset redirect is invalid")
		}
		if base != nil && base.Scheme == "https" && strings.EqualFold(base.Hostname(), "github.com") {
			if request.URL.Scheme != "https" || !officialGitHubReleaseAssetHost(request.URL.Hostname()) {
				return errors.New("helper release asset redirect left the official HTTPS CDN")
			}
			return nil
		}
		if base == nil || request.URL.Scheme != base.Scheme || !strings.EqualFold(request.URL.Host, base.Host) {
			return errors.New("helper release asset redirect left the injected test origin")
		}
		return nil
	}
}

func officialGitHubReleaseAssetHost(host string) bool {
	return strings.EqualFold(host, "release-assets.githubusercontent.com") || strings.EqualFold(host, "objects.githubusercontent.com")
}

type ResolvedReleaseArtifact struct {
	manifest       Manifest
	artifactSource string
	assets         ReleaseAssets
	source         ReleaseSource
	rawManifest    []byte
	rawSignature   []byte
}

func (resolved ResolvedReleaseArtifact) VerifiedManifest() Manifest {
	manifest := resolved.manifest
	manifest.Raw = append([]byte(nil), manifest.Raw...)
	return manifest
}

func (resolved ResolvedReleaseArtifact) Source() string { return resolved.artifactSource }

func (source ReleaseSource) Resolve(ctx context.Context, version string, target Target, verifier Verifier, policy Policy) (ResolvedReleaseArtifact, error) {
	if ctx == nil || source.client == nil {
		return ResolvedReleaseArtifact{}, errors.New("resolve Helper release: source or context is invalid")
	}
	assets, err := releaseAssets(source.baseURL, version, target)
	if err != nil {
		return ResolvedReleaseArtifact{}, err
	}
	rawManifest, err := source.fetchMetadata(ctx, assets.ManifestURL, MaxManifestBytes)
	if err != nil {
		return ResolvedReleaseArtifact{}, fmt.Errorf("resolve Helper release: manifest: %w", err)
	}
	rawSignature, err := source.fetchMetadata(ctx, assets.SignatureURL, detachedSignatureBytes)
	if err != nil {
		return ResolvedReleaseArtifact{}, fmt.Errorf("resolve Helper release: signature: %w", err)
	}
	manifest, err := verifyCurrentPolicy(verifier, policy, rawManifest, rawSignature)
	if err != nil {
		return ResolvedReleaseArtifact{}, fmt.Errorf("resolve Helper release: metadata admission: %w", err)
	}
	requestedVersion, err := parseReleaseVersion(version)
	if err != nil || manifest.Version != requestedVersion || manifest.Target() != target {
		return ResolvedReleaseArtifact{}, errors.New("resolve Helper release: signed identity does not match requested release asset")
	}
	return ResolvedReleaseArtifact{
		manifest: manifest, artifactSource: assets.ArchiveURL, assets: assets, source: source,
		rawManifest: append([]byte(nil), rawManifest...), rawSignature: append([]byte(nil), rawSignature...),
	}, nil
}

func (source ReleaseSource) fetchMetadata(ctx context.Context, assetURL string, maximum int) ([]byte, error) {
	requestContext, cancel := context.WithTimeout(ctx, releaseMetadataTimeout)
	defer cancel()
	response, err := source.get(requestContext, assetURL, int64(maximum))
	if err != nil {
		return nil, err
	}
	defer response.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(response.Body, int64(maximum)+1))
	if err != nil {
		return nil, fmt.Errorf("read bounded asset: %w", err)
	}
	if len(raw) == 0 || len(raw) > maximum {
		return nil, errors.New("asset length is invalid")
	}
	return raw, nil
}

func (source ReleaseSource) openArchiveBinary(ctx context.Context, assetURL, archiveName string, manifest Manifest) (io.ReadCloser, error) {
	if ctx == nil {
		return nil, errors.New("open Helper release archive: context is required")
	}
	requestContext, cancel := context.WithTimeout(ctx, releaseArchiveTimeout)
	defer cancel()
	response, err := source.get(requestContext, assetURL, maxHelperReleaseArchiveBytes)
	if err != nil {
		return nil, err
	}
	defer response.Body.Close()
	limited := &io.LimitedReader{R: response.Body, N: maxHelperReleaseArchiveBytes + 1}
	buffered := bufio.NewReader(limited)
	artifact, err := materializeCanonicalHelperBinary(requestContext, buffered, archiveName, manifest)
	if err != nil {
		return nil, err
	}
	if consumed := maxHelperReleaseArchiveBytes + 1 - limited.N; consumed > maxHelperReleaseArchiveBytes {
		return nil, errors.New("compressed archive exceeds its byte limit")
	}
	return artifact, nil
}

func (source ReleaseSource) get(ctx context.Context, assetURL string, maximum int64) (*http.Response, error) {
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, assetURL, nil)
	if err != nil {
		return nil, fmt.Errorf("create request: %w", err)
	}
	response, err := source.client.Do(request)
	if err != nil {
		return nil, fmt.Errorf("fetch immutable asset: %w", err)
	}
	if response.StatusCode != http.StatusOK {
		_ = response.Body.Close()
		return nil, fmt.Errorf("fetch immutable asset: HTTP status %d", response.StatusCode)
	}
	if response.ContentLength == 0 || response.ContentLength > maximum {
		_ = response.Body.Close()
		return nil, errors.New("fetch immutable asset: Content-Length is invalid")
	}
	return response, nil
}

func materializeCanonicalHelperBinary(ctx context.Context, compressed *bufio.Reader, archiveName string, manifest Manifest) (io.ReadCloser, error) {
	return materializeCanonicalHelperBinaryWithLimit(ctx, compressed, archiveName, manifest, maxHelperReleaseExpandedBytes)
}

func materializeCanonicalHelperBinaryWithLimit(ctx context.Context, compressed *bufio.Reader, archiveName string, manifest Manifest, maximumExpanded int64) (io.ReadCloser, error) {
	if ctx == nil || compressed == nil || !strings.HasSuffix(archiveName, ".tar.gz") || maximumExpanded <= 0 || maximumExpanded > maxHelperReleaseExpandedBytes {
		return nil, errors.New("extract Helper release: archive identity is invalid")
	}
	gzipReader, err := gzip.NewReader(compressed)
	if err != nil {
		return nil, fmt.Errorf("extract Helper release: gzip header: %w", err)
	}
	gzipReader.Multistream(false)
	expanded := &io.LimitedReader{R: gzipReader, N: maximumExpanded + 1}
	tarReader := tar.NewReader(expanded)
	root := strings.TrimSuffix(archiveName, ".tar.gz")
	expected := canonicalReleaseEntries(root, manifest)
	artifact, err := os.CreateTemp("", ".amsftp-helper-release-*")
	if err != nil {
		_ = gzipReader.Close()
		return nil, fmt.Errorf("extract Helper release: create private staging file: %w", err)
	}
	artifactName := artifact.Name()
	cleanup := true
	defer func() {
		if cleanup {
			_ = artifact.Close()
			_ = os.Remove(artifactName)
		}
	}()
	info, err := artifact.Stat()
	if err != nil || !info.Mode().IsRegular() || info.Mode().Perm() != 0o600 || info.Size() != 0 {
		_ = gzipReader.Close()
		return nil, errors.New("extract Helper release: private staging file attributes are invalid")
	}
	if err := os.Remove(artifactName); err != nil {
		_ = gzipReader.Close()
		return nil, fmt.Errorf("extract Helper release: unlink private staging file: %w", err)
	}
	for index, expectation := range expected {
		if err := ctx.Err(); err != nil {
			_ = gzipReader.Close()
			return nil, fmt.Errorf("extract Helper release: %w", err)
		}
		header, nextErr := tarReader.Next()
		if nextErr != nil {
			_ = gzipReader.Close()
			return nil, fmt.Errorf("extract Helper release: entry %d: %w", index, nextErr)
		}
		if err := validateReleaseHeader(header, expectation); err != nil {
			_ = gzipReader.Close()
			return nil, err
		}
		if expectation.binary {
			written, copyErr := io.Copy(artifact, tarReader) //nolint:gosec // gzip output is hard-limited before tar parsing and the signed entry size is capped.
			if copyErr != nil || written != header.Size {
				_ = gzipReader.Close()
				return nil, errors.New("extract Helper release: binary stream is incomplete")
			}
		} else if _, err := io.Copy(io.Discard, tarReader); err != nil { //nolint:gosec // every material header and the entire gzip output have hard limits.
			_ = gzipReader.Close()
			return nil, fmt.Errorf("extract Helper release: material: %w", err)
		}
		if expanded.N == 0 {
			_ = gzipReader.Close()
			return nil, errors.New("extract Helper release: expanded archive exceeds its byte limit")
		}
	}
	if header, err := tarReader.Next(); err != io.EOF || header != nil {
		_ = gzipReader.Close()
		return nil, errors.New("extract Helper release: archive has missing, duplicate, or unexpected entries")
	}
	if trailing, err := io.ReadAll(io.LimitReader(gzipReader, 1)); err != nil || len(trailing) != 0 {
		_ = gzipReader.Close()
		return nil, errors.New("extract Helper release: trailing uncompressed data is forbidden")
	}
	if err := gzipReader.Close(); err != nil {
		return nil, fmt.Errorf("extract Helper release: close gzip stream: %w", err)
	}
	if trailing, err := compressed.ReadByte(); err != io.EOF || trailing != 0 {
		return nil, errors.New("extract Helper release: trailing compressed data is forbidden")
	}
	if expanded.N == 0 {
		return nil, errors.New("extract Helper release: expanded archive exceeds its byte limit")
	}
	if _, err := artifact.Seek(0, io.SeekStart); err != nil {
		return nil, fmt.Errorf("extract Helper release: rewind staged binary: %w", err)
	}
	cleanup = false
	return artifact, nil
}

type releaseEntryExpectation struct {
	name     string
	mode     int64
	typeflag byte
	maximum  int64
	size     int64
	binary   bool
}

func canonicalReleaseEntries(root string, manifest Manifest) []releaseEntryExpectation {
	return []releaseEntryExpectation{
		{name: root + "/", mode: 0o755, typeflag: tar.TypeDir},
		{name: root + "/INSTALL.md", mode: 0o644, typeflag: tar.TypeReg, maximum: maxHelperReleaseMaterialBytes},
		{name: root + "/LICENSE", mode: 0o644, typeflag: tar.TypeReg, maximum: maxHelperReleaseMaterialBytes},
		{name: root + "/NOTICE", mode: 0o644, typeflag: tar.TypeReg, maximum: maxHelperReleaseMaterialBytes},
		{name: root + "/VERSION.json", mode: 0o644, typeflag: tar.TypeReg, maximum: maxHelperReleaseVersionEntryBytes},
		{name: root + "/amsftp", mode: 0o755, typeflag: tar.TypeReg, size: int64(manifest.Size), binary: true}, // #nosec G115 -- manifest size is capped at 128 MiB.
		{name: root + "/share/", mode: 0o755, typeflag: tar.TypeDir},
		{name: root + "/share/man/", mode: 0o755, typeflag: tar.TypeDir},
		{name: root + "/share/man/man1/", mode: 0o755, typeflag: tar.TypeDir},
		{name: root + "/share/man/man1/amsftp.1", mode: 0o644, typeflag: tar.TypeReg, maximum: maxHelperReleaseMaterialBytes},
	}
}

func validateReleaseHeader(header *tar.Header, expected releaseEntryExpectation) error {
	if header == nil || header.Format != tar.FormatUSTAR || header.Name != expected.name || header.Typeflag != expected.typeflag || header.Mode != expected.mode || header.Uid != 0 || header.Gid != 0 || header.Uname != "" || header.Gname != "" || header.Linkname != "" || len(header.PAXRecords) != 0 {
		return errors.New("extract Helper release: archive header is not canonical")
	}
	if expected.typeflag == tar.TypeDir {
		if header.Size != 0 {
			return errors.New("extract Helper release: directory size is invalid")
		}
		return nil
	}
	if header.Size <= 0 || expected.binary && header.Size != expected.size || !expected.binary && header.Size > expected.maximum {
		return errors.New("extract Helper release: entry size is invalid")
	}
	return nil
}

func (resolved ResolvedReleaseArtifact) BindInstallRequest(request InstallRequest, verifier Verifier, policy Policy) (InstallRequest, error) {
	if len(request.RawManifest) != 0 || len(request.RawSignature) != 0 || request.Artifact != nil || request.ArtifactSource != "" {
		return InstallRequest{}, errors.New("bind Helper release: install request already contains release material")
	}
	manifest, err := verifyCurrentPolicy(verifier, policy, resolved.rawManifest, resolved.rawSignature)
	if err != nil || manifest.ArtifactID() != resolved.manifest.ArtifactID() || resolved.source.client == nil || resolved.assets.ArchiveURL != resolved.artifactSource {
		return InstallRequest{}, errors.New("bind Helper release: resolved material no longer passes current policy")
	}
	request.RawManifest = append([]byte(nil), resolved.rawManifest...)
	request.RawSignature = append([]byte(nil), resolved.rawSignature...)
	request.Verifier = cloneVerifier(verifier)
	request.Policy = clonePolicy(policy)
	request.Artifact = func(ctx context.Context) (io.ReadCloser, error) {
		return resolved.source.openArchiveBinary(ctx, resolved.assets.ArchiveURL, resolved.assets.ArchiveName, manifest)
	}
	request.ArtifactSource = resolved.artifactSource
	return request, nil
}

func cloneVerifier(verifier Verifier) Verifier {
	keys := make(map[string]ed25519.PublicKey, len(verifier.keys))
	for keyID, key := range verifier.keys {
		keys[keyID] = append(ed25519.PublicKey(nil), key...)
	}
	return Verifier{keys: keys}
}

func clonePolicy(policy Policy) Policy {
	floors := make(map[FloorKey]Version, len(policy.floors))
	for key, floor := range policy.floors {
		floors[key] = floor
	}
	revoked := make(map[string]struct{}, len(policy.revokedKeys))
	for keyID := range policy.revokedKeys {
		revoked[keyID] = struct{}{}
	}
	denied := make(map[ArtifactID]struct{}, len(policy.denied))
	for artifact := range policy.denied {
		denied[artifact] = struct{}{}
	}
	return Policy{clientVersion: policy.clientVersion, floors: floors, revokedKeys: revoked, denied: denied}
}
