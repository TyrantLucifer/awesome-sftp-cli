package cachemanager

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"

	"github.com/TyrantLucifer/awesome-sftp-cli/internal/cache"
	"github.com/TyrantLucifer/awesome-sftp-cli/internal/domain"
	"github.com/TyrantLucifer/awesome-sftp-cli/internal/ipc"
)

var (
	ErrPinnedOfflineUnavailable = errors.New("pinned offline cache entry is unavailable")
	ErrPinnedOfflineAmbiguous   = errors.New("pinned offline cache entry is ambiguous")
)

type PinnedOfflineRequest struct {
	Location    domain.Location
	WorkspaceID cache.WorkspaceID
}

type PinnedOfflineResult struct {
	Entry             cache.Entry
	SourceFingerprint domain.Fingerprint
}

// ResolvePinnedOffline returns a single complete, strongly bound cache entry.
// Location is only the discovery key: reuse is authorized by the derived entry
// ID plus the independently revalidated manifest, canonical fingerprint, and
// content digest. Multiple historical fingerprints fail closed.
func (manager *Manager) ResolvePinnedOffline(ctx context.Context, request PinnedOfflineRequest) (PinnedOfflineResult, error) {
	if manager == nil || manager.files == nil || manager.catalog == nil {
		return PinnedOfflineResult{}, fmt.Errorf("resolve pinned offline entry: nil manager")
	}
	if ctx == nil {
		return PinnedOfflineResult{}, fmt.Errorf("resolve pinned offline entry: nil context")
	}
	if _, err := domain.NewLocation(request.Location.EndpointID, request.Location.Path); err != nil || request.WorkspaceID == "" {
		return PinnedOfflineResult{}, fmt.Errorf("resolve pinned offline entry: invalid location or workspace")
	}
	manager.admissionMu.Lock()
	defer manager.admissionMu.Unlock()
	return manager.resolvePinnedOfflineLocked(ctx, request)
}

func (manager *Manager) resolvePinnedOfflineLocked(ctx context.Context, request PinnedOfflineRequest) (PinnedOfflineResult, error) {
	entries, err := manager.catalog.ListEntries(ctx)
	if err != nil {
		return PinnedOfflineResult{}, fmt.Errorf("resolve pinned offline entry catalog: %w", err)
	}
	var candidate *cache.Entry
	for index := range entries {
		entry := &entries[index]
		if entry.EndpointID != string(request.Location.EndpointID) || !bytes.Equal(entry.CanonicalPath, []byte(request.Location.Path)) ||
			entry.WorkspaceID != request.WorkspaceID || entry.Policy != cache.PolicyPinnedOffline || !entry.Pinned {
			continue
		}
		if candidate != nil {
			return PinnedOfflineResult{}, fmt.Errorf("%w: multiple fingerprints match the requested location", ErrPinnedOfflineAmbiguous)
		}
		candidate = entry
	}
	if candidate == nil {
		return PinnedOfflineResult{}, ErrPinnedOfflineUnavailable
	}
	derived, err := cache.DeriveEntryID(candidate.EndpointID, candidate.CanonicalPath, candidate.Fingerprint.Canonical)
	if err != nil || derived != candidate.ID {
		return PinnedOfflineResult{}, fmt.Errorf("resolve pinned offline entry: catalog identity mismatch")
	}
	blob, err := manager.catalog.GetBlob(ctx, candidate.BlobID)
	if err != nil {
		return PinnedOfflineResult{}, fmt.Errorf("resolve pinned offline entry blob catalog: %w", err)
	}
	if blob.State != cache.BlobPublished {
		return PinnedOfflineResult{}, fmt.Errorf("resolve pinned offline entry: blob is not published")
	}
	manifest, err := manager.files.ReadEntryManifest(candidate.ID)
	if err != nil {
		return PinnedOfflineResult{}, fmt.Errorf("resolve pinned offline entry manifest: %w", err)
	}
	if manifest.Manifest.EndpointID != candidate.EndpointID || !bytes.Equal(manifest.CanonicalPath, candidate.CanonicalPath) ||
		manifest.Fingerprint.Strength != candidate.Fingerprint.Strength || !bytes.Equal(manifest.Fingerprint.Canonical, candidate.Fingerprint.Canonical) ||
		manifest.Blob.Identity.ID != candidate.BlobID || manifest.Blob.Identity.Size != blob.Size {
		return PinnedOfflineResult{}, fmt.Errorf("resolve pinned offline entry: filesystem and catalog bindings differ")
	}
	source, err := decodeCachedSourceFingerprint(candidate.Fingerprint, blob)
	if err != nil {
		return PinnedOfflineResult{}, err
	}
	if err := manager.catalog.MarkEntryUnknown(ctx, candidate.ID); err != nil {
		return PinnedOfflineResult{}, fmt.Errorf("resolve pinned offline entry freshness: %w", err)
	}
	candidate.Freshness = cache.EntryUnknown
	return PinnedOfflineResult{Entry: *candidate, SourceFingerprint: source}, nil
}

func decodeCachedSourceFingerprint(fingerprint cache.Fingerprint, blob cache.Blob) (domain.Fingerprint, error) {
	var value struct {
		Format        int                 `json:"format"`
		Source        ipc.WireFingerprint `json:"source"`
		ContentSHA256 cache.BlobID        `json:"content_sha256"`
		ContentSize   int64               `json:"content_size"`
	}
	decoder := json.NewDecoder(bytes.NewReader(fingerprint.Canonical))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&value); err != nil {
		return domain.Fingerprint{}, fmt.Errorf("resolve pinned offline entry fingerprint: %w", err)
	}
	if err := requireJSONEOF(decoder); err != nil {
		return domain.Fingerprint{}, err
	}
	if value.Format != 1 || value.ContentSHA256 != blob.ID || value.ContentSize != blob.Size {
		return domain.Fingerprint{}, fmt.Errorf("resolve pinned offline entry: canonical fingerprint content binding differs")
	}
	source := ipc.DecodeFingerprint(value.Source)
	if source.Strength() == domain.FingerprintWeak {
		return domain.Fingerprint{}, fmt.Errorf("resolve pinned offline entry: embedded source fingerprint is weak")
	}
	return source, nil
}

func requireJSONEOF(decoder *json.Decoder) error {
	var extra any
	if err := decoder.Decode(&extra); !errors.Is(err, io.EOF) {
		if err == nil {
			return fmt.Errorf("resolve pinned offline entry fingerprint: trailing value")
		}
		return fmt.Errorf("resolve pinned offline entry fingerprint: %w", err)
	}
	return nil
}
