package cachefs

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/TyrantLucifer/awesome-mac-sftp/internal/cache"
	"github.com/TyrantLucifer/awesome-mac-sftp/internal/testkit"
)

func TestDeleteExactCacheObjectsIsIdempotent(t *testing.T) {
	store := newDeletionStore(t)
	blob, err := store.PublishBlob(context.Background(), bytes.NewReader([]byte("delete me")), 9, nil)
	if err != nil {
		t.Fatal(err)
	}
	fingerprint := cache.Fingerprint{Strength: cache.FingerprintStrong, Canonical: []byte("delete-fingerprint")}
	entryID, err := cache.DeriveEntryID("endpoint", []byte("/delete"), fingerprint.Canonical)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.PublishEntryManifest(entryID, "endpoint", []byte("/delete"), fingerprint, blob.Identity); err != nil {
		t.Fatal(err)
	}
	materializationID := cache.MaterializationID(strings.Repeat("a", 32))
	if _, err := store.CreateMaterialization(context.Background(), materializationID, entryID, blob.Identity); err != nil {
		t.Fatal(err)
	}

	for attempt := 0; attempt < 2; attempt++ {
		if err := store.DeleteMaterialization(materializationID, blob.Identity); err != nil {
			t.Fatalf("DeleteMaterialization attempt %d: %v", attempt+1, err)
		}
		if err := store.DeleteEntry(entryID, blob.Identity.ID); err != nil {
			t.Fatalf("DeleteEntry attempt %d: %v", attempt+1, err)
		}
		if err := store.DeleteBlob(blob.Identity); err != nil {
			t.Fatalf("DeleteBlob attempt %d: %v", attempt+1, err)
		}
	}
	for _, path := range []string{blob.Path, filepath.Join(store.root, "entries", string(entryID)), filepath.Join(store.root, "materializations", string(materializationID))} {
		if _, err := os.Lstat(path); !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("deleted path %q still exists: %v", path, err)
		}
	}
}

func TestDeleteExactCacheObjectsRefusesSymlinkOrUnknownChildren(t *testing.T) {
	store := newDeletionStore(t)
	materializationID := cache.MaterializationID(strings.Repeat("b", 32))
	directory := filepath.Join(store.root, "materializations", string(materializationID))
	if err := os.Mkdir(directory, 0o700); err != nil {
		t.Fatal(err)
	}
	outside := filepath.Join(filepath.Dir(store.root), "outside")
	if err := os.WriteFile(outside, []byte("preserve"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(directory, "content")); err != nil {
		t.Fatal(err)
	}
	if err := store.DeleteMaterialization(materializationID, BlobIdentity{ID: cache.BlobID(strings.Repeat("a", 64)), Size: 8}); err == nil {
		t.Fatal("DeleteMaterialization accepted a symlink")
	}
	if got, err := os.ReadFile(outside); err != nil || string(got) != "preserve" {
		t.Fatalf("outside content = %q, %v", got, err)
	}
}

func TestDeleteExactCacheObjectsResumesPartialDirectoryRemoval(t *testing.T) {
	store := newDeletionStore(t)
	blob, err := store.PublishBlob(context.Background(), bytes.NewReader([]byte("resume me")), 9, nil)
	if err != nil {
		t.Fatal(err)
	}
	fingerprint := cache.Fingerprint{Strength: cache.FingerprintStrong, Canonical: []byte("resume-fingerprint")}
	entryID, _ := cache.DeriveEntryID("endpoint", []byte("/resume"), fingerprint.Canonical)
	if _, err := store.PublishEntryManifest(entryID, "endpoint", []byte("/resume"), fingerprint, blob.Identity); err != nil {
		t.Fatal(err)
	}
	materializationID := cache.MaterializationID(strings.Repeat("c", 32))
	if _, err := store.CreateMaterialization(context.Background(), materializationID, entryID, blob.Identity); err != nil {
		t.Fatal(err)
	}
	entryDirectory := filepath.Join(store.root, "entries", string(entryID))
	materializationDirectory := filepath.Join(store.root, "materializations", string(materializationID))
	if err := os.Remove(filepath.Join(materializationDirectory, "content")); err != nil {
		t.Fatal(err)
	}
	if err := store.DeleteMaterialization(materializationID, blob.Identity); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(filepath.Join(entryDirectory, ManifestFilename)); err != nil {
		t.Fatal(err)
	}
	if err := store.DeleteEntry(entryID, blob.Identity.ID); err != nil {
		t.Fatal(err)
	}
	for _, directory := range []string{entryDirectory, materializationDirectory} {
		if _, err := os.Lstat(directory); !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("partial directory %q remains: %v", directory, err)
		}
	}
}

func newDeletionStore(t *testing.T) *Store {
	t.Helper()
	root := filepath.Join(testkit.PersistentTempDir(t), "cache")
	if err := os.Mkdir(root, 0o700); err != nil {
		t.Fatal(err)
	}
	store, err := Initialize(root)
	if err != nil {
		t.Fatal(err)
	}
	return store
}
