package cachefs

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/TyrantLucifer/awesome-sftp-cli/internal/cache"
	"github.com/TyrantLucifer/awesome-sftp-cli/internal/platform"
)

// DeleteBlob removes only bytes whose content still matches the catalog claim.
// Missing bytes are an idempotent success for crash recovery.
func (store *Store) DeleteBlob(expected BlobIdentity) error {
	path, err := store.BlobPath(expected.ID)
	if err != nil {
		return err
	}
	store.publishMu.Lock()
	defer store.publishMu.Unlock()
	file, inspected, err := store.openBlob(expected.ID)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("delete cache blob %q: %w", expected.ID, err)
	}
	metadata, statErr := file.Stat()
	closeErr := file.Close()
	if statErr != nil || closeErr != nil {
		return fmt.Errorf("delete cache blob %q inspect handle: %w", expected.ID, errors.Join(statErr, closeErr))
	}
	if inspected.Identity != expected {
		return fmt.Errorf("delete cache blob %q: %w", expected.ID, ErrIdentityMismatch)
	}
	if err := removeVerifiedFile(path, metadata); err != nil {
		return fmt.Errorf("delete cache blob %q: %w", expected.ID, err)
	}
	return syncExistingDirectory(filepath.Dir(path))
}

// DeleteEntry removes the exact owner-private manifest directory for an entry.
func (store *Store) DeleteEntry(id cache.EntryID, expectedBlob cache.BlobID) error {
	if _, err := cache.ParseEntryID(string(id)); err != nil {
		return err
	}
	if _, err := cache.ParseBlobID(string(expectedBlob)); err != nil {
		return err
	}
	store.publishMu.Lock()
	defer store.publishMu.Unlock()
	directory := filepath.Join(store.root, "entries", string(id))
	manifestPath := filepath.Join(directory, ManifestFilename)
	before, err := os.Lstat(manifestPath) //nolint:gosec // exact ID-derived cache path
	if errors.Is(err, os.ErrNotExist) {
		if err := removeKnownDirectory(directory, map[string]os.FileInfo{}); err != nil {
			return fmt.Errorf("delete cache entry %q crash residue: %w", id, err)
		}
		return nil
	}
	if err != nil {
		return fmt.Errorf("delete cache entry %q: %w", id, err)
	}
	manifest, err := store.ReadEntryManifest(id)
	if err != nil {
		return fmt.Errorf("delete cache entry %q verify manifest: %w", id, err)
	}
	if manifest.Manifest.BlobID != expectedBlob {
		return fmt.Errorf("delete cache entry %q: %w", id, ErrIdentityMismatch)
	}
	after, err := os.Lstat(manifestPath) //nolint:gosec // exact ID-derived cache path
	if err != nil || !sameStrictFile(before, after) {
		return fmt.Errorf("delete cache entry %q: manifest identity changed", id)
	}
	if err := removeKnownDirectory(directory, map[string]os.FileInfo{ManifestFilename: after}); err != nil {
		return fmt.Errorf("delete cache entry %q: %w", id, err)
	}
	return nil
}

// DeleteMaterialization removes only content and its manifest from the exact
// ID-derived directory. It never follows links or recursively removes data.
func (store *Store) DeleteMaterialization(id cache.MaterializationID, expected BlobIdentity) error {
	if _, err := cache.ParseMaterializationID(string(id)); err != nil {
		return err
	}
	if _, err := cache.ParseBlobID(string(expected.ID)); err != nil || expected.Size < 0 {
		return fmt.Errorf("delete cache materialization: invalid expected identity")
	}
	store.publishMu.Lock()
	defer store.publishMu.Unlock()
	directory := filepath.Join(store.root, "materializations", string(id))
	file, inspected, err := store.openMaterialization(id)
	if errors.Is(err, os.ErrNotExist) {
		manifest, manifestErr := store.ReadMaterializationManifest(id)
		if errors.Is(manifestErr, os.ErrNotExist) {
			if cleanupErr := removeKnownDirectory(directory, map[string]os.FileInfo{}); cleanupErr != nil {
				return fmt.Errorf("delete cache materialization %q crash residue: %w", id, cleanupErr)
			}
			return nil
		}
		if manifestErr != nil || manifest.ContentSHA256 != expected.ID || manifest.ContentSize != expected.Size {
			return fmt.Errorf("delete cache materialization %q crash manifest: %w", id, errors.Join(ErrIdentityMismatch, manifestErr))
		}
		manifestMetadata, statErr := os.Lstat(filepath.Join(directory, ManifestFilename)) //nolint:gosec // exact ID-derived cache path
		if statErr != nil {
			return fmt.Errorf("delete cache materialization %q crash manifest metadata: %w", id, statErr)
		}
		if cleanupErr := removeKnownDirectory(directory, map[string]os.FileInfo{ManifestFilename: manifestMetadata}); cleanupErr != nil {
			return fmt.Errorf("delete cache materialization %q crash residue: %w", id, cleanupErr)
		}
		return nil
	}
	if err != nil {
		return fmt.Errorf("delete cache materialization %q inspect: %w", id, err)
	}
	contentMetadata, statErr := file.Stat()
	closeErr := file.Close()
	if statErr != nil || closeErr != nil {
		return fmt.Errorf("delete cache materialization %q inspect handle: %w", id, errors.Join(statErr, closeErr))
	}
	if inspected.SHA256 != expected.ID || inspected.Size != expected.Size {
		return fmt.Errorf("delete cache materialization %q: %w", id, ErrIdentityMismatch)
	}
	manifest, err := store.ReadMaterializationManifest(id)
	if err != nil {
		return fmt.Errorf("delete cache materialization %q manifest: %w", id, err)
	}
	if manifest.ContentSHA256 != expected.ID || manifest.ContentSize != expected.Size {
		return fmt.Errorf("delete cache materialization %q: %w", id, ErrIdentityMismatch)
	}
	manifestMetadata, err := os.Lstat(filepath.Join(directory, ManifestFilename)) //nolint:gosec // exact ID-derived cache path
	if err != nil {
		return fmt.Errorf("delete cache materialization %q manifest metadata: %w", id, err)
	}
	if err := removeKnownDirectory(directory, map[string]os.FileInfo{"content": contentMetadata, ManifestFilename: manifestMetadata}); err != nil {
		return fmt.Errorf("delete cache materialization %q: %w", id, err)
	}
	return nil
}

func removeKnownDirectory(directory string, expected map[string]os.FileInfo) error {
	metadata, err := os.Lstat(directory) //nolint:gosec // exact ID-derived cache path
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	if err := validatePrivateDirectoryMetadata(directory, metadata); err != nil {
		return err
	}
	if err := validatePrivateDirectory(directory); err != nil {
		return err
	}
	entries, err := os.ReadDir(directory) //nolint:gosec // exact validated cache directory
	if err != nil {
		return err
	}
	type claimedFile struct {
		path     string
		metadata os.FileInfo
	}
	files := make([]claimedFile, 0, len(entries))
	for _, entry := range entries {
		claimed, ok := expected[entry.Name()]
		if !ok {
			return fmt.Errorf("refuse to remove cache directory with unexpected child %q", entry.Name())
		}
		path := filepath.Join(directory, entry.Name())
		child, statErr := os.Lstat(path) //nolint:gosec // child basename is allowlisted above
		if statErr != nil {
			return statErr
		}
		if err := validatePrivateRegular(path, child); err != nil {
			return err
		}
		if !sameStrictFile(claimed, child) {
			return fmt.Errorf("refuse to remove changed cache child %q", entry.Name())
		}
		files = append(files, claimedFile{path: path, metadata: child})
	}
	for _, file := range files {
		if err := removeVerifiedFile(file.path, file.metadata); err != nil {
			return err
		}
	}
	if err := syncDirectory(directory); err != nil {
		return err
	}
	if err := os.Remove(directory); err != nil {
		return err
	}
	return syncExistingDirectory(filepath.Dir(directory))
}

func removeVerifiedFile(path string, expected os.FileInfo) error {
	metadata, err := os.Lstat(path) //nolint:gosec // exact ID-derived or allowlisted cache path
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	if err := validatePrivateRegular(path, metadata); err != nil {
		return err
	}
	if !sameStrictFile(expected, metadata) {
		return fmt.Errorf("refuse to remove changed cache file")
	}
	if err := os.Remove(path); err != nil {
		return err
	}
	return nil
}

func sameStrictFile(left, right os.FileInfo) bool {
	return platform.SameExecutableIdentity(left, right)
}

func syncExistingDirectory(path string) error {
	if err := syncDirectory(path); errors.Is(err, os.ErrNotExist) {
		return nil
	} else {
		return err
	}
}
