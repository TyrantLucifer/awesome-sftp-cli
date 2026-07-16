package cachefs

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path"
	"path/filepath"
	"strings"
	"unicode/utf8"

	"github.com/TyrantLucifer/awesome-mac-sftp/internal/cache"
	"github.com/TyrantLucifer/awesome-mac-sftp/internal/ipc"
)

const (
	ManifestFilename  = "manifest-v1.json"
	maxManifestBytes  = 2 << 20
	maxEndpointBytes  = 255
	maxEntryPathBytes = 4096
	manifestFormat    = 1
	entryKind         = "entry"
	materialKind      = "materialization"
)

var ErrPublicationConflict = errors.New("cache publication conflicts with existing target")

// EntryManifest is the frozen, content-free filesystem binding between a
// canonical Location/fingerprint and a verified immutable blob.
type EntryManifest struct {
	Format              int                       `json:"format"`
	Kind                string                    `json:"kind"`
	EntryID             cache.EntryID             `json:"entry_id"`
	EndpointID          string                    `json:"endpoint_id"`
	Path                ipc.WireBytes             `json:"path"`
	FingerprintStrength cache.FingerprintStrength `json:"fingerprint_strength"`
	Fingerprint         ipc.WireBytes             `json:"fingerprint"`
	BlobID              cache.BlobID              `json:"blob_id"`
	BlobSize            int64                     `json:"blob_size"`
}

type EntryManifestInfo struct {
	Manifest      EntryManifest
	CanonicalPath []byte
	Fingerprint   cache.Fingerprint
	Blob          BlobInfo
	Path          string
}

type MaterializationManifest struct {
	Format            int                     `json:"format"`
	Kind              string                  `json:"kind"`
	MaterializationID cache.MaterializationID `json:"materialization_id"`
	EntryID           cache.EntryID           `json:"entry_id"`
	BaselineBlobID    cache.BlobID            `json:"baseline_blob_id"`
	ContentSHA256     cache.BlobID            `json:"content_sha256"`
	ContentSize       int64                   `json:"content_size"`
}

type MaterializationResult struct {
	Info         MaterializationInfo
	Manifest     MaterializationManifest
	Deduplicated bool
}

type MaterializationManifestInfo struct {
	Manifest MaterializationManifest
	Info     MaterializationInfo
}

func (store *Store) PublishEntryManifest(entryID cache.EntryID, endpointID string, canonicalPath []byte, fingerprint cache.Fingerprint, blob BlobIdentity) (EntryManifestInfo, error) {
	if store == nil {
		return EntryManifestInfo{}, fmt.Errorf("publish cache entry manifest: nil store")
	}
	store.publishMu.Lock()
	defer store.publishMu.Unlock()

	manifest, err := buildEntryManifest(entryID, endpointID, canonicalPath, fingerprint, blob)
	if err != nil {
		return EntryManifestInfo{}, err
	}
	verified, err := store.InspectBlob(blob.ID)
	if err != nil {
		return EntryManifestInfo{}, fmt.Errorf("publish cache entry manifest verify blob: %w", err)
	}
	if verified.Identity != blob {
		return EntryManifestInfo{}, fmt.Errorf("%w: entry blob expected id=%s size=%d, inspected id=%s size=%d", ErrIdentityMismatch, blob.ID, blob.Size, verified.Identity.ID, verified.Identity.Size)
	}
	encoded, err := encodeCanonicalManifest(manifest)
	if err != nil {
		return EntryManifestInfo{}, fmt.Errorf("publish cache entry manifest encode: %w", err)
	}
	finalPath, err := store.entryManifestPath(entryID)
	if err != nil {
		return EntryManifestInfo{}, err
	}
	if err := store.publishManifestFile("entry", finalPath, encoded); err != nil {
		if errors.Is(err, os.ErrExist) {
			existing, readErr := store.ReadEntryManifest(entryID)
			if readErr == nil && existing.Manifest == manifest {
				return existing, nil
			}
			return EntryManifestInfo{}, fmt.Errorf("%w: entry %s: %v", ErrPublicationConflict, entryID, readErr)
		}
		return EntryManifestInfo{}, err
	}
	return store.ReadEntryManifest(entryID)
}

func (store *Store) ReadEntryManifest(entryID cache.EntryID) (EntryManifestInfo, error) {
	if store == nil {
		return EntryManifestInfo{}, fmt.Errorf("read cache entry manifest: nil store")
	}
	manifestPath, err := store.entryManifestPath(entryID)
	if err != nil {
		return EntryManifestInfo{}, err
	}
	if err := validatePrivateDirectory(filepath.Dir(manifestPath)); err != nil {
		return EntryManifestInfo{}, fmt.Errorf("read cache entry manifest %q directory: %w", entryID, err)
	}
	var manifest EntryManifest
	if err := readCanonicalManifest(manifestPath, &manifest); err != nil {
		return EntryManifestInfo{}, fmt.Errorf("read cache entry manifest %q: %w", entryID, err)
	}
	canonicalPath, fingerprint, err := validateEntryManifest(manifest, entryID)
	if err != nil {
		return EntryManifestInfo{}, err
	}
	blob, err := store.InspectBlob(manifest.BlobID)
	if err != nil {
		return EntryManifestInfo{}, fmt.Errorf("read cache entry manifest %q blob: %w", entryID, err)
	}
	if blob.Identity.Size != manifest.BlobSize {
		return EntryManifestInfo{}, fmt.Errorf("%w: entry manifest blob size=%d inspected=%d", ErrIdentityMismatch, manifest.BlobSize, blob.Identity.Size)
	}
	return EntryManifestInfo{Manifest: manifest, CanonicalPath: canonicalPath, Fingerprint: fingerprint, Blob: blob, Path: manifestPath}, nil
}

func (store *Store) CreateMaterialization(ctx context.Context, id cache.MaterializationID, entryID cache.EntryID, baseline BlobIdentity) (MaterializationResult, error) {
	if store == nil {
		return MaterializationResult{}, fmt.Errorf("create cache materialization: nil store")
	}
	if ctx == nil {
		return MaterializationResult{}, fmt.Errorf("create cache materialization: nil context")
	}
	if _, err := cache.ParseMaterializationID(string(id)); err != nil {
		return MaterializationResult{}, err
	}
	if _, err := cache.ParseEntryID(string(entryID)); err != nil {
		return MaterializationResult{}, err
	}
	if _, err := cache.ParseBlobID(string(baseline.ID)); err != nil || baseline.Size < 0 {
		return MaterializationResult{}, fmt.Errorf("create cache materialization baseline: invalid identity")
	}

	store.publishMu.Lock()
	defer store.publishMu.Unlock()
	if existing, err := store.readExactMaterialization(id, entryID, baseline); err == nil {
		existing.Deduplicated = true
		return existing, nil
	} else if !errors.Is(err, os.ErrNotExist) {
		return MaterializationResult{}, fmt.Errorf("%w: materialization %s: %v", ErrPublicationConflict, id, err)
	}
	entry, err := store.ReadEntryManifest(entryID)
	if err != nil {
		return MaterializationResult{}, fmt.Errorf("create cache materialization verify entry: %w", err)
	}
	if entry.Blob.Identity != baseline {
		return MaterializationResult{}, fmt.Errorf("%w: materialization baseline differs from entry blob", ErrIdentityMismatch)
	}

	source, inspected, err := store.OpenBlob(baseline.ID)
	if err != nil {
		return MaterializationResult{}, fmt.Errorf("create cache materialization verify baseline: %w", err)
	}
	defer source.Close()
	if inspected.Identity != baseline {
		return MaterializationResult{}, fmt.Errorf("%w: materialization baseline differs from inspected blob", ErrIdentityMismatch)
	}
	staging := filepath.Join(store.root, "staging")
	contentTemp, contentTempInfo, err := createManifestTemp(staging, "materialization")
	if err != nil {
		return MaterializationResult{}, err
	}
	contentTempPath := contentTemp.Name()
	contentClosed := false
	cleanupContent := func() {
		if !contentClosed {
			_ = contentTemp.Close()
			contentClosed = true
		}
		if removePublicationTemp(contentTempPath, contentTempInfo) == nil {
			_ = syncDirectory(staging)
		}
	}
	digest := sha256.New()
	written, copyErr := io.CopyBuffer(io.MultiWriter(contentTemp, digest), &contextReader{ctx: ctx, reader: source}, make([]byte, 64<<10))
	if copyErr != nil {
		cleanupContent()
		return MaterializationResult{}, fmt.Errorf("create cache materialization copy: %w", copyErr)
	}
	var sum [sha256.Size]byte
	copy(sum[:], digest.Sum(nil))
	computed := BlobIdentity{ID: cache.BlobIDFromDigest(sum), Size: written}
	if computed != baseline {
		cleanupContent()
		return MaterializationResult{}, fmt.Errorf("%w: materialization copied id=%s size=%d, expected id=%s size=%d", ErrIdentityMismatch, computed.ID, computed.Size, baseline.ID, baseline.Size)
	}
	if err := fullSyncFile(contentTemp); err != nil {
		cleanupContent()
		return MaterializationResult{}, fmt.Errorf("create cache materialization content durability: %w", err)
	}
	if err := contentTemp.Close(); err != nil {
		contentClosed = true
		cleanupContent()
		return MaterializationResult{}, fmt.Errorf("create cache materialization close content: %w", err)
	}
	contentClosed = true

	manifest := MaterializationManifest{Format: manifestFormat, Kind: materialKind, MaterializationID: id, EntryID: entryID, BaselineBlobID: baseline.ID, ContentSHA256: computed.ID, ContentSize: computed.Size}
	manifestBytes, err := encodeCanonicalManifest(manifest)
	if err != nil {
		cleanupContent()
		return MaterializationResult{}, err
	}
	manifestTemp, manifestTempInfo, err := createManifestTemp(staging, "manifest")
	if err != nil {
		cleanupContent()
		return MaterializationResult{}, err
	}
	manifestTempPath := manifestTemp.Name()
	manifestClosed := false
	cleanupManifest := func() {
		if !manifestClosed {
			_ = manifestTemp.Close()
			manifestClosed = true
		}
		if removePublicationTemp(manifestTempPath, manifestTempInfo) == nil {
			_ = syncDirectory(staging)
		}
	}
	if _, err := manifestTemp.Write(manifestBytes); err != nil {
		cleanupManifest()
		cleanupContent()
		return MaterializationResult{}, fmt.Errorf("create cache materialization write manifest: %w", err)
	}
	if err := fullSyncFile(manifestTemp); err != nil {
		cleanupManifest()
		cleanupContent()
		return MaterializationResult{}, fmt.Errorf("create cache materialization manifest durability: %w", err)
	}
	if err := manifestTemp.Close(); err != nil {
		manifestClosed = true
		cleanupManifest()
		cleanupContent()
		return MaterializationResult{}, fmt.Errorf("create cache materialization close manifest: %w", err)
	}
	manifestClosed = true

	targetDir := filepath.Join(store.root, "materializations", string(id))
	if err := os.Mkdir(targetDir, 0o700); err != nil {
		cleanupManifest()
		cleanupContent()
		if errors.Is(err, os.ErrExist) {
			if existing, readErr := store.readExactMaterialization(id, entryID, baseline); readErr == nil {
				existing.Deduplicated = true
				return existing, nil
			}
			return MaterializationResult{}, fmt.Errorf("%w: materialization %s already exists", ErrPublicationConflict, id)
		}
		return MaterializationResult{}, fmt.Errorf("create cache materialization directory: %w", err)
	}
	if err := validatePrivateDirectory(targetDir); err != nil {
		cleanupManifest()
		cleanupContent()
		return MaterializationResult{}, err
	}
	if err := syncDirectory(filepath.Dir(targetDir)); err != nil {
		cleanupManifest()
		cleanupContent()
		return MaterializationResult{}, err
	}
	if err := os.Link(contentTempPath, filepath.Join(targetDir, "content")); err != nil {
		cleanupManifest()
		cleanupContent()
		return MaterializationResult{}, fmt.Errorf("create cache materialization publish content: %w", err)
	}
	if err := os.Link(manifestTempPath, filepath.Join(targetDir, ManifestFilename)); err != nil {
		cleanupManifest()
		cleanupContent()
		return MaterializationResult{}, fmt.Errorf("create cache materialization publish manifest: %w", err)
	}
	if err := syncDirectory(targetDir); err != nil {
		cleanupManifest()
		cleanupContent()
		return MaterializationResult{}, err
	}
	cleanupManifest()
	cleanupContent()
	if err := syncDirectory(staging); err != nil {
		return MaterializationResult{}, err
	}
	return store.readExactMaterialization(id, entryID, baseline)
}

func (store *Store) ReadMaterializationManifest(id cache.MaterializationID) (MaterializationManifest, error) {
	if store == nil {
		return MaterializationManifest{}, fmt.Errorf("read cache materialization manifest: nil store")
	}
	if _, err := cache.ParseMaterializationID(string(id)); err != nil {
		return MaterializationManifest{}, err
	}
	manifestPath := filepath.Join(store.root, "materializations", string(id), ManifestFilename)
	if err := validatePrivateDirectory(filepath.Dir(manifestPath)); err != nil {
		return MaterializationManifest{}, fmt.Errorf("read cache materialization manifest %q directory: %w", id, err)
	}
	var manifest MaterializationManifest
	if err := readCanonicalManifest(manifestPath, &manifest); err != nil {
		return MaterializationManifest{}, fmt.Errorf("read cache materialization manifest %q: %w", id, err)
	}
	if manifest.Format != manifestFormat || manifest.Kind != materialKind || manifest.MaterializationID != id {
		return MaterializationManifest{}, fmt.Errorf("%w: materialization manifest identity", ErrIdentityMismatch)
	}
	if _, err := cache.ParseEntryID(string(manifest.EntryID)); err != nil {
		return MaterializationManifest{}, err
	}
	if _, err := cache.ParseBlobID(string(manifest.BaselineBlobID)); err != nil {
		return MaterializationManifest{}, err
	}
	if _, err := cache.ParseBlobID(string(manifest.ContentSHA256)); err != nil {
		return MaterializationManifest{}, err
	}
	if manifest.ContentSize < 0 {
		return MaterializationManifest{}, fmt.Errorf("materialization manifest has negative content size")
	}
	baseline, err := store.InspectBlob(manifest.BaselineBlobID)
	if err != nil {
		return MaterializationManifest{}, fmt.Errorf("materialization manifest baseline: %w", err)
	}
	if baseline.Identity.ID != manifest.BaselineBlobID {
		return MaterializationManifest{}, fmt.Errorf("%w: materialization baseline", ErrIdentityMismatch)
	}
	entry, err := store.ReadEntryManifest(manifest.EntryID)
	if err != nil {
		return MaterializationManifest{}, fmt.Errorf("materialization manifest entry binding: %w", err)
	}
	if entry.Blob.Identity != baseline.Identity {
		return MaterializationManifest{}, fmt.Errorf("%w: materialization entry baseline", ErrIdentityMismatch)
	}
	return manifest, nil
}

func (store *Store) readExactMaterialization(id cache.MaterializationID, entryID cache.EntryID, baseline BlobIdentity) (MaterializationResult, error) {
	manifest, err := store.ReadMaterializationManifest(id)
	if err != nil {
		return MaterializationResult{}, err
	}
	if manifest.EntryID != entryID || manifest.BaselineBlobID != baseline.ID || manifest.ContentSHA256 != baseline.ID || manifest.ContentSize != baseline.Size {
		return MaterializationResult{}, fmt.Errorf("%w: existing materialization binding differs", ErrPublicationConflict)
	}
	info, err := store.InspectMaterialization(id)
	if err != nil {
		return MaterializationResult{}, err
	}
	if info.SHA256 != manifest.ContentSHA256 || info.Size != manifest.ContentSize {
		return MaterializationResult{}, fmt.Errorf("%w: existing materialization content differs from creation manifest", ErrPublicationConflict)
	}
	return MaterializationResult{Info: info, Manifest: manifest}, nil
}

func buildEntryManifest(entryID cache.EntryID, endpointID string, canonicalPath []byte, fingerprint cache.Fingerprint, blob BlobIdentity) (EntryManifest, error) {
	if _, err := cache.ParseEntryID(string(entryID)); err != nil {
		return EntryManifest{}, err
	}
	if err := validateCanonicalRawPath(canonicalPath); err != nil {
		return EntryManifest{}, err
	}
	if len(endpointID) == 0 || len(endpointID) > maxEndpointBytes {
		return EntryManifest{}, fmt.Errorf("publish cache entry manifest: endpoint ID length must be in [1,%d]", maxEndpointBytes)
	}
	if err := fingerprint.Validate(); err != nil {
		return EntryManifest{}, err
	}
	if fingerprint.Strength != cache.FingerprintStrong {
		return EntryManifest{}, fmt.Errorf("publish cache entry manifest: weak-only fingerprints cannot bind complete blobs")
	}
	if _, err := cache.ParseBlobID(string(blob.ID)); err != nil || blob.Size < 0 {
		return EntryManifest{}, fmt.Errorf("publish cache entry manifest: invalid blob identity")
	}
	derived, err := cache.DeriveEntryID(endpointID, canonicalPath, fingerprint.Canonical)
	if err != nil {
		return EntryManifest{}, err
	}
	if derived != entryID {
		return EntryManifest{}, fmt.Errorf("%w: entry basename=%s derived=%s", ErrIdentityMismatch, entryID, derived)
	}
	return EntryManifest{Format: manifestFormat, Kind: entryKind, EntryID: entryID, EndpointID: endpointID, Path: ipc.EncodeWireBytes(canonicalPath), FingerprintStrength: fingerprint.Strength, Fingerprint: ipc.EncodeWireBytes(fingerprint.Canonical), BlobID: blob.ID, BlobSize: blob.Size}, nil
}

func validateEntryManifest(manifest EntryManifest, expected cache.EntryID) ([]byte, cache.Fingerprint, error) {
	if manifest.Format != manifestFormat || manifest.Kind != entryKind || manifest.EntryID != expected {
		return nil, cache.Fingerprint{}, fmt.Errorf("%w: cache entry manifest identity", ErrIdentityMismatch)
	}
	canonicalPath, err := manifest.Path.Decode()
	if err != nil {
		return nil, cache.Fingerprint{}, err
	}
	if err := validateCanonicalRawPath(canonicalPath); err != nil {
		return nil, cache.Fingerprint{}, err
	}
	if len(manifest.EndpointID) == 0 || len(manifest.EndpointID) > maxEndpointBytes {
		return nil, cache.Fingerprint{}, fmt.Errorf("cache entry manifest endpoint ID length must be in [1,%d]", maxEndpointBytes)
	}
	canonicalFingerprint, err := manifest.Fingerprint.Decode()
	if err != nil {
		return nil, cache.Fingerprint{}, err
	}
	fingerprint := cache.Fingerprint{Strength: manifest.FingerprintStrength, Canonical: canonicalFingerprint}
	if err := fingerprint.Validate(); err != nil {
		return nil, cache.Fingerprint{}, err
	}
	if fingerprint.Strength != cache.FingerprintStrong {
		return nil, cache.Fingerprint{}, fmt.Errorf("cache entry manifest cannot bind a complete blob with a weak-only fingerprint")
	}
	derived, err := cache.DeriveEntryID(manifest.EndpointID, canonicalPath, canonicalFingerprint)
	if err != nil {
		return nil, cache.Fingerprint{}, err
	}
	if derived != expected {
		return nil, cache.Fingerprint{}, fmt.Errorf("%w: entry basename=%s derived=%s", ErrIdentityMismatch, expected, derived)
	}
	if _, err := cache.ParseBlobID(string(manifest.BlobID)); err != nil || manifest.BlobSize < 0 {
		return nil, cache.Fingerprint{}, fmt.Errorf("entry manifest has invalid blob identity")
	}
	return canonicalPath, fingerprint, nil
}

func validateCanonicalRawPath(value []byte) error {
	if len(value) == 0 || len(value) > maxEntryPathBytes || value[0] != '/' || bytes.IndexByte(value, 0) >= 0 || path.Clean(string(value)) != string(value) {
		return fmt.Errorf("cache manifest path must be non-empty, absolute, canonical, and NUL-free")
	}
	return nil
}

func (store *Store) entryManifestPath(id cache.EntryID) (string, error) {
	parsed, err := cache.ParseEntryID(string(id))
	if err != nil {
		return "", err
	}
	return filepath.Join(store.root, "entries", string(parsed), ManifestFilename), nil
}

func (store *Store) publishManifestFile(kind, finalPath string, content []byte) error {
	staging := filepath.Join(store.root, "staging")
	if err := validatePrivateDirectory(staging); err != nil {
		return err
	}
	temp, tempInfo, err := createManifestTemp(staging, kind)
	if err != nil {
		return err
	}
	tempPath := temp.Name()
	closed := false
	cleanup := func() {
		if !closed {
			_ = temp.Close()
			closed = true
		}
		if removePublicationTemp(tempPath, tempInfo) == nil {
			_ = syncDirectory(staging)
		}
	}
	if _, err := temp.Write(content); err != nil {
		cleanup()
		return fmt.Errorf("publish cache manifest write: %w", err)
	}
	if err := fullSyncFile(temp); err != nil {
		cleanup()
		return fmt.Errorf("publish cache manifest durability: %w", err)
	}
	if err := temp.Close(); err != nil {
		closed = true
		cleanup()
		return err
	}
	closed = true
	parent := filepath.Dir(finalPath)
	created := false
	if err := os.Mkdir(parent, 0o700); err != nil {
		if !errors.Is(err, os.ErrExist) {
			cleanup()
			return fmt.Errorf("publish cache manifest directory: %w", err)
		}
	} else {
		created = true
	}
	if err := validatePrivateDirectory(parent); err != nil {
		cleanup()
		return err
	}
	if created {
		if err := syncDirectory(filepath.Dir(parent)); err != nil {
			cleanup()
			return err
		}
	}
	if err := os.Link(tempPath, finalPath); err != nil {
		cleanup()
		return err
	}
	if err := syncDirectory(parent); err != nil {
		cleanup()
		return err
	}
	cleanup()
	return syncDirectory(staging)
}

// Publication uses a hard link as the portable no-replace primitive. During
// the short interval before the staging name is removed, the exact inode has
// two links; ordinary cleanup deliberately rejects that state.
func removePublicationTemp(path string, expected os.FileInfo) error {
	current, err := os.Lstat(path) //nolint:gosec // exact random staging path
	if err != nil {
		return err
	}
	if !os.SameFile(expected, current) || !current.Mode().IsRegular() || current.Mode().Perm() != 0o600 {
		return fmt.Errorf("refuse to remove changed cache publication temp")
	}
	return os.Remove(path)
}

func createManifestTemp(staging, kind string) (*os.File, os.FileInfo, error) {
	if kind == "" || strings.ContainsAny(kind, "/\\") {
		return nil, nil, fmt.Errorf("create cache manifest temp: invalid kind")
	}
	for range 8 {
		var random [16]byte
		if _, err := io.ReadFull(rand.Reader, random[:]); err != nil {
			return nil, nil, err
		}
		name := ".amsftp-cache-" + kind + "-" + hex.EncodeToString(random[:]) + ".tmp"
		file, err := os.OpenFile(filepath.Join(staging, name), os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600) //nolint:gosec // random O_EXCL name under verified staging
		if err != nil {
			if errors.Is(err, os.ErrExist) {
				continue
			}
			return nil, nil, err
		}
		metadata, err := file.Stat()
		if err != nil {
			_ = file.Close()
			return nil, nil, err
		}
		if err := validatePrivateRegular(file.Name(), metadata); err != nil {
			_ = file.Close()
			return nil, nil, err
		}
		return file, metadata, nil
	}
	return nil, nil, fmt.Errorf("create cache manifest temp: exhausted collisions")
}

func encodeCanonicalManifest(value any) ([]byte, error) {
	encoded, err := json.Marshal(value)
	if err != nil {
		return nil, err
	}
	if len(encoded) > maxManifestBytes {
		return nil, fmt.Errorf("cache manifest exceeds %d bytes", maxManifestBytes)
	}
	return encoded, nil
}

func readCanonicalManifest(manifestPath string, destination any) error {
	file, before, err := openPrivateRegular(manifestPath)
	if err != nil {
		return err
	}
	defer file.Close()
	if before.Size() < 2 || before.Size() > maxManifestBytes {
		return fmt.Errorf("cache manifest size must be in [2,%d]", maxManifestBytes)
	}
	content, err := io.ReadAll(io.LimitReader(file, maxManifestBytes+1))
	if err != nil {
		return err
	}
	if int64(len(content)) != before.Size() || len(content) > maxManifestBytes {
		return fmt.Errorf("cache manifest size changed or exceeds limit")
	}
	if !utf8.Valid(content) {
		return fmt.Errorf("cache manifest is not strict UTF-8")
	}
	decoder := json.NewDecoder(bytes.NewReader(content))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(destination); err != nil {
		return fmt.Errorf("decode cache manifest: %w", err)
	}
	if decoder.More() {
		return fmt.Errorf("cache manifest contains trailing JSON value")
	}
	canonical, err := encodeCanonicalManifest(destination)
	if err != nil {
		return err
	}
	if !bytes.Equal(content, canonical) {
		return fmt.Errorf("cache manifest is not canonical JSON")
	}
	if err := validateOpenAndPathStable(manifestPath, file, before); err != nil {
		return err
	}
	return nil
}
