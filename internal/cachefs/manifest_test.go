package cachefs

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/TyrantLucifer/awesome-mac-sftp/internal/cache"
)

func TestEntryManifestPublishesCanonicalRawPathAndRevalidatesBlob(t *testing.T) {
	store := newStore(t)
	blob := publishTestBlob(t, store, []byte("verified content"))
	path := []byte{'/', 'r', 'a', 'w', '-', 0xff}
	fingerprint := cache.Fingerprint{Strength: cache.FingerprintStrong, Canonical: []byte{0x00, 0xff, 0x01}}
	entryID, err := cache.DeriveEntryID("endpoint", path, fingerprint.Canonical)
	if err != nil {
		t.Fatal(err)
	}

	info, err := store.PublishEntryManifest(entryID, "endpoint", path, fingerprint, blob.Identity)
	if err != nil {
		t.Fatalf("PublishEntryManifest() error = %v", err)
	}
	if info.Manifest.EntryID != entryID || !bytes.Equal(info.CanonicalPath, path) {
		t.Fatalf("manifest = %#v", info)
	}
	raw, err := os.ReadFile(info.Path)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(raw, path) || bytes.Contains(raw, blobBytes(t, store, blob.Identity.ID)) {
		t.Fatalf("manifest leaked raw path/content: %q", raw)
	}
	if len(raw) == 0 || raw[len(raw)-1] == '\n' {
		t.Fatalf("manifest is not compact canonical JSON: %q", raw)
	}
	read, err := store.ReadEntryManifest(entryID)
	if err != nil {
		t.Fatalf("ReadEntryManifest() error = %v", err)
	}
	if !bytes.Equal(read.CanonicalPath, path) || !bytes.Equal(read.Fingerprint.Canonical, fingerprint.Canonical) {
		t.Fatalf("decoded manifest = %#v", read)
	}
}

func TestEntryManifestRejectsWrongIdentityAndConcurrentConflict(t *testing.T) {
	store := newStore(t)
	blob := publishTestBlob(t, store, []byte("one"))
	path := []byte("/file")
	fingerprint := cache.Fingerprint{Strength: cache.FingerprintStrong, Canonical: []byte("fingerprint")}
	wrong, _ := cache.DeriveEntryID("other", path, fingerprint.Canonical)
	if _, err := store.PublishEntryManifest(wrong, "endpoint", path, fingerprint, blob.Identity); !errors.Is(err, ErrIdentityMismatch) {
		t.Fatalf("wrong entry error = %v", err)
	}
	entryID, _ := cache.DeriveEntryID("endpoint", path, fingerprint.Canonical)
	weak := fingerprint
	weak.Strength = cache.FingerprintWeak
	if _, err := store.PublishEntryManifest(entryID, "endpoint", path, weak, blob.Identity); err == nil {
		t.Fatal("weak-only fingerprint bound a complete blob")
	}
	if _, err := store.PublishEntryManifest(entryID, "endpoint", path, fingerprint, blob.Identity); err != nil {
		t.Fatal(err)
	}
	other := publishTestBlob(t, store, []byte("two"))
	if _, err := store.PublishEntryManifest(entryID, "endpoint", path, fingerprint, other.Identity); !errors.Is(err, ErrPublicationConflict) {
		t.Fatalf("conflict error = %v", err)
	}
}

func TestEntryManifestRejectsValuesOutsideVersion2CatalogBounds(t *testing.T) {
	store := newStore(t)
	blob := publishTestBlob(t, store, []byte("content"))
	fingerprint := cache.Fingerprint{Strength: cache.FingerprintStrong, Canonical: []byte("fingerprint")}

	for name, testCase := range map[string]struct {
		endpointID    string
		canonicalPath []byte
	}{
		"endpoint": {endpointID: strings.Repeat("e", 256), canonicalPath: []byte("/file")},
		"path":     {endpointID: "endpoint", canonicalPath: append([]byte{'/'}, bytes.Repeat([]byte{'p'}, 4096)...)},
	} {
		t.Run(name, func(t *testing.T) {
			entryID, err := cache.DeriveEntryID(testCase.endpointID, testCase.canonicalPath, fingerprint.Canonical)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := store.PublishEntryManifest(entryID, testCase.endpointID, testCase.canonicalPath, fingerprint, blob.Identity); err == nil {
				t.Fatal("PublishEntryManifest() accepted a value the Version 2 catalog cannot persist")
			}
		})
	}
}

func TestReadEntryManifestRejectsUnknownOversizeNoncanonicalSecretsAndSymlink(t *testing.T) {
	store := newStore(t)
	blob := publishTestBlob(t, store, []byte("content"))
	path := []byte("/file")
	fingerprint := cache.Fingerprint{Strength: cache.FingerprintStrong, Canonical: []byte("fingerprint")}
	entryID, _ := cache.DeriveEntryID("endpoint", path, fingerprint.Canonical)
	info, err := store.PublishEntryManifest(entryID, "endpoint", path, fingerprint, blob.Identity)
	if err != nil {
		t.Fatal(err)
	}
	original, _ := os.ReadFile(info.Path)

	cases := map[string][]byte{
		"unknown":      append(original[:len(original)-1], []byte(",\"credential\":\"secret\"}")...),
		"noncanonical": append([]byte(" \n"), original...),
		"malformed":    []byte("{"),
		"secret field": []byte(`{"format":1,"command":"cat /secret"}`),
		"oversize":     bytes.Repeat([]byte("x"), maxManifestBytes+1),
	}
	for name, content := range cases {
		t.Run(name, func(t *testing.T) {
			if err := os.WriteFile(info.Path, content, 0o600); err != nil {
				t.Fatal(err)
			}
			if _, err := store.ReadEntryManifest(entryID); err == nil {
				t.Fatal("ReadEntryManifest() succeeded")
			}
			if err := os.WriteFile(info.Path, original, 0o600); err != nil {
				t.Fatal(err)
			}
		})
	}
	if err := os.Remove(info.Path); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(store.root, "missing"), info.Path); err != nil {
		t.Fatal(err)
	}
	if _, err := store.ReadEntryManifest(entryID); err == nil {
		t.Fatal("symlink manifest accepted")
	}
}

func TestCreateMaterializationCopiesVerifiedBlobAndDeduplicatesExactTarget(t *testing.T) {
	store := newStore(t)
	blob := publishTestBlob(t, store, []byte("editor copy"))
	entryID, _ := cache.DeriveEntryID("endpoint", []byte("/file"), []byte("fp"))
	publishTestEntry(t, store, entryID, blob, []byte("/file"), []byte("fp"))
	id, _ := cache.ParseMaterializationID(strings.Repeat("a", 32))

	result, err := store.CreateMaterialization(context.Background(), id, entryID, blob.Identity)
	if err != nil {
		t.Fatalf("CreateMaterialization() error = %v", err)
	}
	if result.Deduplicated {
		t.Fatal("first publication reported deduplicated")
	}
	if result.Info.SHA256 != blob.Identity.ID || result.Info.Size != blob.Identity.Size {
		t.Fatalf("result = %#v", result)
	}
	for _, path := range []string{result.Info.Path, filepath.Join(filepath.Dir(result.Info.Path), ManifestFilename)} {
		metadata, err := os.Lstat(path)
		if err != nil {
			t.Fatal(err)
		}
		if !metadata.Mode().IsRegular() || metadata.Mode().Perm() != 0o600 {
			t.Fatalf("%s mode = %v", path, metadata.Mode())
		}
	}
	metadata, _ := os.Lstat(filepath.Dir(result.Info.Path))
	if metadata.Mode().Perm() != 0o700 {
		t.Fatalf("directory mode = %v", metadata.Mode())
	}
	second, err := store.CreateMaterialization(context.Background(), id, entryID, blob.Identity)
	if err != nil || !second.Deduplicated {
		t.Fatalf("dedup = %#v, %v", second, err)
	}
	manifest, err := store.ReadMaterializationManifest(id)
	if err != nil || manifest.ContentSHA256 != blob.Identity.ID {
		t.Fatalf("manifest = %#v, %v", manifest, err)
	}
}

func TestReadMaterializationManifestRequiresItsEntryBinding(t *testing.T) {
	store := newStore(t)
	blob := publishTestBlob(t, store, []byte("editor copy"))
	entryID, _ := cache.DeriveEntryID("endpoint", []byte("/file"), []byte("fp"))
	publishTestEntry(t, store, entryID, blob, []byte("/file"), []byte("fp"))
	id, _ := cache.ParseMaterializationID(strings.Repeat("9", 32))
	if _, err := store.CreateMaterialization(context.Background(), id, entryID, blob.Identity); err != nil {
		t.Fatal(err)
	}
	entryManifest := filepath.Join(store.root, "entries", string(entryID), ManifestFilename)
	if err := os.Remove(entryManifest); err != nil {
		t.Fatal(err)
	}
	if _, err := store.ReadMaterializationManifest(id); err == nil {
		t.Fatal("ReadMaterializationManifest() accepted a missing entry binding")
	}
}

func TestCreateMaterializationFailsClosedForHashMismatchSymlinkCrashAndConcurrentConflict(t *testing.T) {
	store := newStore(t)
	blob := publishTestBlob(t, store, []byte("editor copy"))
	entryID, _ := cache.DeriveEntryID("endpoint", []byte("/file"), []byte("fp"))
	publishTestEntry(t, store, entryID, blob, []byte("/file"), []byte("fp"))
	id, _ := cache.ParseMaterializationID(strings.Repeat("b", 32))
	wrong := blob.Identity
	wrong.Size++
	if _, err := store.CreateMaterialization(context.Background(), id, entryID, wrong); !errors.Is(err, ErrIdentityMismatch) {
		t.Fatalf("hash/size mismatch error = %v", err)
	}
	target := filepath.Join(store.root, "materializations", string(id))
	if err := os.Mkdir(target, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(blob.Path, filepath.Join(target, "content")); err != nil {
		t.Fatal(err)
	}
	if _, err := store.CreateMaterialization(context.Background(), id, entryID, blob.Identity); !errors.Is(err, ErrPublicationConflict) {
		t.Fatalf("unsafe target error = %v", err)
	}
	if _, err := store.InspectMaterialization(id); err == nil {
		t.Fatal("symlink materialization accepted")
	}
	if err := os.WriteFile(blob.Path, []byte("corrupt blob"), 0o600); err != nil {
		t.Fatal(err)
	}
	corruptID, _ := cache.ParseMaterializationID(strings.Repeat("e", 32))
	if _, err := store.CreateMaterialization(context.Background(), corruptID, entryID, blob.Identity); !errors.Is(err, ErrIdentityMismatch) {
		t.Fatalf("corrupt baseline error = %v", err)
	}
}

func TestCreateMaterializationConcurrentExactRequestsConverge(t *testing.T) {
	store := newStore(t)
	blob := publishTestBlob(t, store, bytes.Repeat([]byte("x"), 1<<20))
	entryID, _ := cache.DeriveEntryID("endpoint", []byte("/file"), []byte("fp"))
	publishTestEntry(t, store, entryID, blob, []byte("/file"), []byte("fp"))
	id, _ := cache.ParseMaterializationID(strings.Repeat("c", 32))
	var wg sync.WaitGroup
	errs := make(chan error, 2)
	results := make(chan MaterializationResult, 2)
	for range 2 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			result, err := store.CreateMaterialization(context.Background(), id, entryID, blob.Identity)
			results <- result
			errs <- err
		}()
	}
	wg.Wait()
	close(errs)
	close(results)
	for err := range errs {
		if err != nil {
			t.Fatalf("concurrent create error = %v", err)
		}
	}
	dedup := 0
	for result := range results {
		if result.Deduplicated {
			dedup++
		}
	}
	if dedup != 1 {
		t.Fatalf("deduplicated results = %d, want 1", dedup)
	}
}

func TestScanRecognizesValidManifestsAndPreservesCorruptUnknown(t *testing.T) {
	store := newStore(t)
	blob := publishTestBlob(t, store, []byte("content"))
	path := []byte("/file")
	fingerprint := cache.Fingerprint{Strength: cache.FingerprintStrong, Canonical: []byte("fp")}
	entryID, _ := cache.DeriveEntryID("endpoint", path, fingerprint.Canonical)
	if _, err := store.PublishEntryManifest(entryID, "endpoint", path, fingerprint, blob.Identity); err != nil {
		t.Fatal(err)
	}
	id, _ := cache.ParseMaterializationID(strings.Repeat("d", 32))
	if _, err := store.CreateMaterialization(context.Background(), id, entryID, blob.Identity); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(store.root, "materializations", string(id), "content"), []byte("locally edited"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(store.root, "entries", "unknown"), []byte("preserve"), 0o600); err != nil {
		t.Fatal(err)
	}
	corruptID, _ := cache.DeriveEntryID("endpoint", []byte("/corrupt"), []byte("fp"))
	corruptDir := filepath.Join(store.root, "entries", string(corruptID))
	if err := os.Mkdir(corruptDir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(corruptDir, ManifestFilename), []byte("{}"), 0o600); err != nil {
		t.Fatal(err)
	}
	crash := filepath.Join(store.root, "staging", ".amsftp-cache-manifest-"+strings.Repeat("f", 32)+".tmp")
	if err := os.WriteFile(crash, []byte("preserve"), 0o600); err != nil {
		t.Fatal(err)
	}
	report, err := store.Scan(100)
	if err != nil {
		t.Fatal(err)
	}
	if len(report.VerifiedEntries) != 1 || len(report.VerifiedMaterializations) != 1 {
		t.Fatalf("scan report = %#v", report)
	}
	if len(report.Unknown) == 0 {
		t.Fatalf("unknown entry not preserved/reported: %#v", report)
	}
	assertFinding(t, report.Orphans, filepath.ToSlash(filepath.Join("entries", string(corruptID), ManifestFilename)), FindingCorruptManifest)
	assertFinding(t, report.Orphans, "staging/"+filepath.Base(crash), FindingCrashTemp)
	if _, err := os.Lstat(crash); err != nil {
		t.Fatalf("crash artifact was not preserved: %v", err)
	}
}

func publishTestBlob(t *testing.T, store *Store, content []byte) BlobInfo {
	t.Helper()
	result, err := store.PublishBlob(context.Background(), bytes.NewReader(content), int64(len(content)), nil)
	if err != nil {
		t.Fatal(err)
	}
	return result.BlobInfo
}

func publishTestEntry(t *testing.T, store *Store, entryID cache.EntryID, blob BlobInfo, path, fingerprint []byte) {
	t.Helper()
	if _, err := store.PublishEntryManifest(entryID, "endpoint", path, cache.Fingerprint{Strength: cache.FingerprintStrong, Canonical: fingerprint}, blob.Identity); err != nil {
		t.Fatal(err)
	}
}

func blobBytes(t *testing.T, store *Store, id cache.BlobID) []byte {
	t.Helper()
	file, _, err := store.OpenBlob(id)
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	value, err := os.ReadFile(file.Name())
	if err != nil {
		t.Fatal(err)
	}
	return value
}
