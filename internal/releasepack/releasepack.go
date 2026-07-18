package releasepack

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"crypto/sha256"
	gobuildinfo "debug/buildinfo"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"
)

const maxReleaseInputBytes = 256 << 20

type Target struct {
	OS   string `json:"os"`
	Arch string `json:"arch"`
}

var Targets = [...]Target{
	{OS: "darwin", Arch: "amd64"},
	{OS: "darwin", Arch: "arm64"},
	{OS: "linux", Arch: "amd64"},
	{OS: "linux", Arch: "arm64"},
}

type BinaryState string

const (
	BinaryPublicPreview        BinaryState = "public_preview"
	BinaryLinuxFinalUnsigned   BinaryState = "linux_final_unsigned"
	BinaryDarwinAcceptedSigned BinaryState = "darwin_accepted_signed"
)

type DarwinEvidence struct {
	DeveloperIDApplication  string `json:"developer_id_application"`
	TeamID                  string `json:"team_id"`
	LeafFingerprint         string `json:"leaf_fingerprint"`
	HardenedRuntime         bool   `json:"hardened_runtime"`
	TrustedTimestamp        bool   `json:"trusted_timestamp"`
	StrictVerified          bool   `json:"strict_verified"`
	NotaryStatus            string `json:"notary_status"`
	SubmissionID            string `json:"submission_id"`
	CDHash                  string `json:"cdhash"`
	AcceptedZIPBinarySHA256 string `json:"accepted_zip_binary_sha256"`
}

type PlatformBinary struct {
	Target Target
	Bytes  []byte
	State  BinaryState
	Darwin *DarwinEvidence
	Build  *GoBuildEvidence
}

type GoBuildEvidence struct {
	MainPath    string `json:"main_path"`
	GoVersion   string `json:"go_version"`
	GOOS        string `json:"goos"`
	GOARCH      string `json:"goarch"`
	CGOEnabled  bool   `json:"cgo_enabled"`
	Trimpath    bool   `json:"trimpath"`
	VCSRevision string `json:"vcs_revision"`
	VCSModified bool   `json:"vcs_modified"`
}

type Materials struct {
	License   []byte
	Notice    []byte
	Install   []byte
	Uninstall []byte
}

type Module struct {
	Path    string `json:"path"`
	Version string `json:"version"`
	Sum     string `json:"sum"`
	License string `json:"license"`
}

type BundleRequest struct {
	Version         string
	Commit          string
	Tree            string
	SourceDateEpoch int64
	Materials       Materials
	Platforms       []PlatformBinary
	Modules         []Module
}

type Archive struct {
	Name   string
	Target Target
	Bytes  []byte
}

type Bundle struct {
	Archives   []Archive
	Checksums  []byte
	SBOM       []byte
	Provenance []byte
}

type VersionMetadata struct {
	Schema                 string `json:"schema"`
	Version                string `json:"version"`
	Commit                 string `json:"commit"`
	Tree                   string `json:"tree"`
	Target                 Target `json:"target"`
	ReleaseCandidate       bool   `json:"release_candidate"`
	ProductionHelperClosed bool   `json:"production_helper_closed"`
	ApplicationID          string `json:"application_id"`
	LaunchdLabel           string `json:"launchd_label"`
	SystemdUserUnit        string `json:"systemd_user_unit"`
	HomebrewFormula        string `json:"homebrew_formula"`
}

type SPDXChecksum struct {
	Algorithm     string `json:"algorithm"`
	ChecksumValue string `json:"checksumValue"`
}

type SPDXPackage struct {
	Name             string            `json:"name"`
	SPDXID           string            `json:"SPDXID"`
	VersionInfo      string            `json:"versionInfo"`
	DownloadLocation string            `json:"downloadLocation"`
	FilesAnalyzed    bool              `json:"filesAnalyzed"`
	Checksums        []SPDXChecksum    `json:"checksums,omitempty"`
	LicenseConcluded string            `json:"licenseConcluded"`
	LicenseDeclared  string            `json:"licenseDeclared"`
	CopyrightText    string            `json:"copyrightText"`
	ExternalRefs     []SPDXExternalRef `json:"externalRefs,omitempty"`
}

type SPDXExternalRef struct {
	ReferenceCategory string `json:"referenceCategory"`
	ReferenceType     string `json:"referenceType"`
	ReferenceLocator  string `json:"referenceLocator"`
}

type SPDXCreationInfo struct {
	Created  string   `json:"created"`
	Creators []string `json:"creators"`
}

type SPDXDocument struct {
	SPDXVersion       string           `json:"spdxVersion"`
	DataLicense       string           `json:"dataLicense"`
	SPDXID            string           `json:"SPDXID"`
	Name              string           `json:"name"`
	DocumentNamespace string           `json:"documentNamespace"`
	CreationInfo      SPDXCreationInfo `json:"creationInfo"`
	Packages          []SPDXPackage    `json:"packages"`
}

type ProvenanceArchive struct {
	Name   string `json:"name"`
	Target Target `json:"target"`
	Size   uint64 `json:"size"`
	SHA256 string `json:"sha256"`
}

type ProvenanceInput struct {
	Schema                 string              `json:"schema"`
	Version                string              `json:"version"`
	Commit                 string              `json:"commit"`
	Tree                   string              `json:"tree"`
	SourceDateEpoch        int64               `json:"source_date_epoch"`
	ReleaseCandidate       bool                `json:"release_candidate"`
	ProductionHelperClosed bool                `json:"production_helper_closed"`
	Archives               []ProvenanceArchive `json:"archives"`
}

type FrozenBinary struct {
	Target Target
	Size   uint64
	SHA256 string
	State  BinaryState
	Darwin *DarwinEvidence
}

func BuildPublicBundle(request BundleRequest) (Bundle, error) {
	if err := validateBundleRequest(request); err != nil {
		return Bundle{}, err
	}
	archives := make([]Archive, 0, len(Targets))
	for _, target := range Targets {
		platform := findPlatform(request.Platforms, target)
		archive, err := buildArchive(request, platform)
		if err != nil {
			return Bundle{}, err
		}
		archives = append(archives, archive)
	}
	checksums := buildChecksums(archives)
	sbom, err := buildSBOM(request, archives)
	if err != nil {
		return Bundle{}, err
	}
	provenance, err := buildProvenance(request, archives)
	if err != nil {
		return Bundle{}, err
	}
	return Bundle{Archives: archives, Checksums: checksums, SBOM: sbom, Provenance: provenance}, nil
}

func InspectGoBinary(raw []byte) (GoBuildEvidence, error) {
	if len(raw) == 0 || len(raw) > maxReleaseInputBytes {
		return GoBuildEvidence{}, errors.New("inspect release binary: binary size is invalid")
	}
	info, err := gobuildinfo.Read(bytes.NewReader(raw))
	if err != nil {
		return GoBuildEvidence{}, fmt.Errorf("inspect release binary: %w", err)
	}
	evidence := GoBuildEvidence{MainPath: info.Path, GoVersion: info.GoVersion}
	for _, setting := range info.Settings {
		switch setting.Key {
		case "GOOS":
			evidence.GOOS = setting.Value
		case "GOARCH":
			evidence.GOARCH = setting.Value
		case "CGO_ENABLED":
			evidence.CGOEnabled = setting.Value == "1"
		case "-trimpath":
			evidence.Trimpath = setting.Value == "true"
		case "vcs.revision":
			evidence.VCSRevision = setting.Value
		case "vcs.modified":
			evidence.VCSModified = setting.Value == "true"
		}
	}
	if evidence.MainPath == "" || evidence.GoVersion == "" || evidence.GOOS == "" || evidence.GOARCH == "" {
		return GoBuildEvidence{}, errors.New("inspect release binary: required Go build identity is absent")
	}
	return evidence, nil
}

func WriteBundle(outputDir string, bundle Bundle) (err error) {
	if outputDir == "" || filepath.Clean(outputDir) == "." {
		return errors.New("write release bundle: output directory is invalid")
	}
	if err := validateBundleForWrite(bundle); err != nil {
		return err
	}
	if err := os.Mkdir(outputDir, 0o755); err != nil { //nolint:gosec // release directories are intentionally world-readable/searchable.
		return fmt.Errorf("write release bundle: create output directory: %w", err)
	}
	for _, archive := range bundle.Archives {
		if err := writeNewReleaseFile(outputDir, archive.Name, archive.Bytes); err != nil {
			return err
		}
	}
	for _, artifact := range []struct {
		name string
		body []byte
	}{
		{name: "checksums.txt", body: bundle.Checksums},
		{name: "provenance.input.json", body: bundle.Provenance},
		{name: "sbom.spdx.json", body: bundle.SBOM},
	} {
		if err := writeNewReleaseFile(outputDir, artifact.name, artifact.body); err != nil {
			return err
		}
	}
	return nil
}

func validateBundleForWrite(bundle Bundle) error {
	if len(bundle.Archives) != len(Targets) || len(bundle.Checksums) == 0 || len(bundle.SBOM) == 0 || len(bundle.Provenance) == 0 {
		return errors.New("write release bundle: bundle is incomplete")
	}
	const firstSuffix = "_darwin_amd64.tar.gz"
	firstName := bundle.Archives[0].Name
	if !strings.HasPrefix(firstName, "amsftp_") || !strings.HasSuffix(firstName, firstSuffix) {
		return errors.New("write release bundle: archive identity is invalid")
	}
	version := strings.TrimSuffix(strings.TrimPrefix(firstName, "amsftp_"), firstSuffix)
	if !validReleaseVersion(version) {
		return errors.New("write release bundle: archive version is invalid")
	}
	for index, target := range Targets {
		archive := bundle.Archives[index]
		if archive.Target != target || archive.Name != archiveName(version, target) || len(archive.Bytes) == 0 {
			return errors.New("write release bundle: exact ordered archive set is required")
		}
	}
	return nil
}

func writeNewReleaseFile(outputDir, name string, body []byte) error {
	file, err := os.OpenFile(filepath.Join(outputDir, name), os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o644) //nolint:gosec // outputDir is newly created and name is selected from the exact validated artifact set.
	if err != nil {
		return fmt.Errorf("write release bundle: create %s: %w", name, err)
	}
	if _, err := file.Write(body); err != nil {
		_ = file.Close()
		return fmt.Errorf("write release bundle: write %s: %w", name, err)
	}
	if err := file.Sync(); err != nil {
		_ = file.Close()
		return fmt.Errorf("write release bundle: sync %s: %w", name, err)
	}
	if err := file.Close(); err != nil {
		return fmt.Errorf("write release bundle: close %s: %w", name, err)
	}
	if err := os.Chmod(filepath.Join(outputDir, name), 0o644); err != nil { //nolint:gosec // release artifacts are intentionally world-readable.
		return fmt.Errorf("write release bundle: set %s mode: %w", name, err)
	}
	return nil
}

func AdmitProductionHelperBinary(platform PlatformBinary) (FrozenBinary, error) {
	if !validTarget(platform.Target) || len(platform.Bytes) == 0 || len(platform.Bytes) > maxReleaseInputBytes {
		return FrozenBinary{}, errors.New("admit production Helper binary: target or binary size is invalid")
	}
	digest := digestBytes(platform.Bytes)
	switch platform.Target.OS {
	case "linux":
		if platform.State != BinaryLinuxFinalUnsigned || platform.Darwin != nil {
			return FrozenBinary{}, errors.New("admit production Helper binary: Linux bytes are not frozen final unsigned bytes")
		}
		return FrozenBinary{Target: platform.Target, Size: uint64(len(platform.Bytes)), SHA256: digest, State: platform.State}, nil
	case "darwin":
		if platform.State != BinaryDarwinAcceptedSigned || !validDarwinEvidence(platform.Darwin, digest) {
			return FrozenBinary{}, errors.New("admit production Helper binary: Darwin signing or notarization evidence is incomplete or byte-mismatched")
		}
		evidence := *platform.Darwin
		return FrozenBinary{Target: platform.Target, Size: uint64(len(platform.Bytes)), SHA256: digest, State: platform.State, Darwin: &evidence}, nil
	default:
		return FrozenBinary{}, errors.New("admit production Helper binary: unsupported target")
	}
}

func validateBundleRequest(request BundleRequest) error {
	if !validReleaseVersion(request.Version) || !isLowerHex(request.Commit, 40) || !isLowerHex(request.Tree, 40) || request.SourceDateEpoch < 0 {
		return errors.New("build public release bundle: release identity is invalid")
	}
	if err := validateMaterials(request.Materials); err != nil {
		return err
	}
	if len(request.Platforms) != len(Targets) {
		return errors.New("build public release bundle: exact four-target set is required")
	}
	seen := make(map[Target]struct{}, len(Targets))
	for _, platform := range request.Platforms {
		if !validTarget(platform.Target) || platform.State != BinaryPublicPreview || platform.Darwin != nil || len(platform.Bytes) == 0 || len(platform.Bytes) > maxReleaseInputBytes || containsFixtureTrust(platform.Bytes) || !validPublicBuildEvidence(platform, request.Commit) {
			return errors.New("build public release bundle: public platform input is invalid")
		}
		if _, duplicate := seen[platform.Target]; duplicate {
			return errors.New("build public release bundle: duplicate target")
		}
		seen[platform.Target] = struct{}{}
	}
	for _, target := range Targets {
		if _, exists := seen[target]; !exists {
			return errors.New("build public release bundle: target set is incomplete")
		}
	}
	moduleKeys := make(map[string]struct{}, len(request.Modules))
	for _, module := range request.Modules {
		key := module.Path + "@" + module.Version
		if module.Path == "" || module.Version == "" || module.Sum == "" || module.License == "" || strings.IndexByte(key, 0) >= 0 {
			return errors.New("build public release bundle: module identity is invalid")
		}
		if _, duplicate := moduleKeys[key]; duplicate {
			return errors.New("build public release bundle: duplicate module")
		}
		moduleKeys[key] = struct{}{}
	}
	return nil
}

func validPublicBuildEvidence(platform PlatformBinary, commit string) bool {
	return platform.Build != nil &&
		platform.Build.MainPath == "github.com/TyrantLucifer/awesome-mac-sftp/cmd/amsftp" &&
		platform.Build.GOOS == platform.Target.OS &&
		platform.Build.GOARCH == platform.Target.Arch &&
		!platform.Build.CGOEnabled &&
		platform.Build.Trimpath &&
		platform.Build.VCSRevision == commit &&
		!platform.Build.VCSModified
}

func validateMaterials(materials Materials) error {
	for _, material := range []struct {
		name string
		raw  []byte
	}{
		{name: "LICENSE", raw: materials.License},
		{name: "NOTICE", raw: materials.Notice},
		{name: "INSTALL.md", raw: materials.Install},
		{name: "UNINSTALL.md", raw: materials.Uninstall},
	} {
		if len(material.raw) == 0 || len(material.raw) > 1<<20 || !utf8.Valid(material.raw) || material.raw[len(material.raw)-1] != '\n' || bytes.IndexByte(material.raw, 0) >= 0 || containsFixtureTrust(material.raw) {
			return fmt.Errorf("build public release bundle: %s material is invalid", material.name)
		}
	}
	return nil
}

func buildArchive(request BundleRequest, platform PlatformBinary) (Archive, error) {
	name := archiveName(request.Version, platform.Target)
	root := strings.TrimSuffix(name, ".tar.gz")
	metadata := VersionMetadata{
		Schema: "amsftp-release-version-v1", Version: request.Version, Commit: request.Commit, Tree: request.Tree, Target: platform.Target,
		ReleaseCandidate: false, ProductionHelperClosed: true,
		ApplicationID: "io.github.tyrantlucifer.amsftp", LaunchdLabel: "io.github.tyrantlucifer.amsftp.daemon",
		SystemdUserUnit: "amsftp-daemon.service", HomebrewFormula: "amsftp",
	}
	metadataBytes, err := marshalCanonicalJSON(metadata)
	if err != nil {
		return Archive{}, err
	}
	files := []archiveFile{
		{name: "INSTALL.md", mode: 0o644, body: request.Materials.Install},
		{name: "LICENSE", mode: 0o644, body: request.Materials.License},
		{name: "NOTICE", mode: 0o644, body: request.Materials.Notice},
		{name: "UNINSTALL.md", mode: 0o644, body: request.Materials.Uninstall},
		{name: "VERSION.json", mode: 0o644, body: metadataBytes},
		{name: "amsftp", mode: 0o755, body: platform.Bytes},
	}
	var output bytes.Buffer
	gzipWriter, err := gzip.NewWriterLevel(&output, gzip.BestCompression)
	if err != nil {
		return Archive{}, err
	}
	modTime := time.Unix(request.SourceDateEpoch, 0).UTC()
	gzipWriter.ModTime = modTime
	gzipWriter.OS = 255
	tarWriter := tar.NewWriter(gzipWriter)
	if err := tarWriter.WriteHeader(canonicalTarHeader(root+"/", 0o755, 0, tar.TypeDir, modTime)); err != nil {
		return Archive{}, closeArchiveWriters(tarWriter, gzipWriter, err)
	}
	for _, file := range files {
		header := canonicalTarHeader(root+"/"+file.name, file.mode, int64(len(file.body)), tar.TypeReg, modTime)
		if err := tarWriter.WriteHeader(header); err != nil {
			return Archive{}, closeArchiveWriters(tarWriter, gzipWriter, err)
		}
		if _, err := tarWriter.Write(file.body); err != nil {
			return Archive{}, closeArchiveWriters(tarWriter, gzipWriter, err)
		}
	}
	if err := tarWriter.Close(); err != nil {
		_ = gzipWriter.Close()
		return Archive{}, err
	}
	if err := gzipWriter.Close(); err != nil {
		return Archive{}, err
	}
	return Archive{Name: name, Target: platform.Target, Bytes: output.Bytes()}, nil
}

type archiveFile struct {
	name string
	mode int64
	body []byte
}

func canonicalTarHeader(name string, mode, size int64, kind byte, modTime time.Time) *tar.Header {
	return &tar.Header{Name: name, Mode: mode, Size: size, ModTime: modTime, Typeflag: kind, Format: tar.FormatUSTAR}
}

func closeArchiveWriters(tarWriter *tar.Writer, gzipWriter *gzip.Writer, cause error) error {
	_ = tarWriter.Close()
	_ = gzipWriter.Close()
	return cause
}

func buildChecksums(archives []Archive) []byte {
	var output strings.Builder
	for _, archive := range archives {
		fmt.Fprintf(&output, "%s  %s\n", digestBytes(archive.Bytes), archive.Name)
	}
	return []byte(output.String())
}

func buildSBOM(request BundleRequest, archives []Archive) ([]byte, error) {
	packages := make([]SPDXPackage, 0, len(archives)+len(request.Modules))
	for index, archive := range archives {
		packages = append(packages, SPDXPackage{
			Name: archive.Name, SPDXID: fmt.Sprintf("SPDXRef-Archive-%d", index+1), VersionInfo: request.Version,
			DownloadLocation: "NOASSERTION", FilesAnalyzed: false, Checksums: []SPDXChecksum{{Algorithm: "SHA256", ChecksumValue: digestBytes(archive.Bytes)}},
			LicenseConcluded: "NOASSERTION", LicenseDeclared: "NOASSERTION", CopyrightText: "NOASSERTION",
		})
	}
	modules := append([]Module(nil), request.Modules...)
	sortModules(modules)
	for index, module := range modules {
		packages = append(packages, SPDXPackage{
			Name: module.Path, SPDXID: fmt.Sprintf("SPDXRef-Module-%d", index+1), VersionInfo: module.Version,
			DownloadLocation: "NOASSERTION", FilesAnalyzed: false, LicenseConcluded: module.License, LicenseDeclared: module.License, CopyrightText: "NOASSERTION",
			ExternalRefs: []SPDXExternalRef{{ReferenceCategory: "PACKAGE-MANAGER", ReferenceType: "purl", ReferenceLocator: "pkg:golang/" + module.Path + "@" + module.Version + "?checksum=" + module.Sum}},
		})
	}
	document := SPDXDocument{
		SPDXVersion: "SPDX-2.3", DataLicense: "CC0-1.0", SPDXID: "SPDXRef-DOCUMENT", Name: "AMSFTP " + request.Version,
		DocumentNamespace: "https://github.com/TyrantLucifer/awesome-mac-sftp/releases/" + request.Version + "/sbom-" + request.Commit,
		CreationInfo:      SPDXCreationInfo{Created: time.Unix(request.SourceDateEpoch, 0).UTC().Format(time.RFC3339), Creators: []string{"Tool: amsftp-releasepack-v1"}},
		Packages:          packages,
	}
	return marshalCanonicalJSON(document)
}

func buildProvenance(request BundleRequest, archives []Archive) ([]byte, error) {
	items := make([]ProvenanceArchive, 0, len(archives))
	for _, archive := range archives {
		items = append(items, ProvenanceArchive{Name: archive.Name, Target: archive.Target, Size: uint64(len(archive.Bytes)), SHA256: digestBytes(archive.Bytes)})
	}
	return marshalCanonicalJSON(ProvenanceInput{
		Schema: "amsftp-release-provenance-input-v1", Version: request.Version, Commit: request.Commit, Tree: request.Tree, SourceDateEpoch: request.SourceDateEpoch,
		ReleaseCandidate: false, ProductionHelperClosed: true, Archives: items,
	})
}

func marshalCanonicalJSON(value any) ([]byte, error) {
	raw, err := json.Marshal(value)
	if err != nil {
		return nil, err
	}
	return append(raw, '\n'), nil
}

func archiveName(version string, target Target) string {
	return fmt.Sprintf("amsftp_%s_%s_%s.tar.gz", version, target.OS, target.Arch)
}

func findPlatform(platforms []PlatformBinary, target Target) PlatformBinary {
	for _, platform := range platforms {
		if platform.Target == target {
			return platform
		}
	}
	return PlatformBinary{}
}

func validTarget(target Target) bool {
	for _, candidate := range Targets {
		if target == candidate {
			return true
		}
	}
	return false
}

func validReleaseVersion(value string) bool {
	parts := strings.Split(value, ".")
	if len(parts) != 3 || len(value) > 32 {
		return false
	}
	for _, part := range parts {
		if part == "" || len(part) > 10 || len(part) > 1 && part[0] == '0' {
			return false
		}
		for _, character := range part {
			if character < '0' || character > '9' {
				return false
			}
		}
		number, err := strconv.ParseUint(part, 10, 31)
		if err != nil || number > 2_147_483_647 {
			return false
		}
	}
	return true
}

func validDarwinEvidence(evidence *DarwinEvidence, digest string) bool {
	if evidence == nil || !strings.HasPrefix(evidence.DeveloperIDApplication, "Developer ID Application: ") || !isUpperAlphaNumeric(evidence.TeamID, 10) || !isLowerHex(evidence.LeafFingerprint, 64) || !isLowerHex(evidence.CDHash, 40) {
		return false
	}
	if !evidence.HardenedRuntime || !evidence.TrustedTimestamp || !evidence.StrictVerified || evidence.NotaryStatus != "Accepted" || evidence.AcceptedZIPBinarySHA256 != digest {
		return false
	}
	return validUUID(evidence.SubmissionID)
}

func validUUID(value string) bool {
	if len(value) != 36 || value[8] != '-' || value[13] != '-' || value[18] != '-' || value[23] != '-' {
		return false
	}
	for index, character := range value {
		if index == 8 || index == 13 || index == 18 || index == 23 {
			continue
		}
		if character < '0' || character > '9' && (character < 'a' || character > 'f') {
			return false
		}
	}
	return true
}

func isUpperAlphaNumeric(value string, length int) bool {
	if len(value) != length {
		return false
	}
	for _, character := range value {
		if character < '0' || character > '9' && (character < 'A' || character > 'Z') {
			return false
		}
	}
	return true
}

func isLowerHex(value string, length int) bool {
	if len(value) != length {
		return false
	}
	for _, character := range value {
		if character < '0' || character > '9' && (character < 'a' || character > 'f') {
			return false
		}
	}
	return true
}

func containsFixtureTrust(raw []byte) bool {
	lower := strings.ToLower(string(raw))
	for _, forbidden := range []string{"testdata/nonrelease-helper-fixture", "fixture signing key", "fixture private key"} {
		if strings.Contains(lower, forbidden) {
			return true
		}
	}
	return false
}

func digestBytes(raw []byte) string {
	digest := sha256.Sum256(raw)
	return hex.EncodeToString(digest[:])
}

func sortModules(modules []Module) {
	for right := 1; right < len(modules); right++ {
		for left := right; left > 0; left-- {
			leftKey := modules[left-1].Path + "@" + modules[left-1].Version
			rightKey := modules[left].Path + "@" + modules[left].Version
			if leftKey <= rightKey {
				break
			}
			modules[left-1], modules[left] = modules[left], modules[left-1]
		}
	}
}
