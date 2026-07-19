package cachefs

import (
	"bytes"
	"context"
	"crypto/sha256"
	"errors"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/TyrantLucifer/awesome-sftp-cli/internal/cache"
	"github.com/TyrantLucifer/awesome-sftp-cli/internal/testkit"
)

func TestInitializeCreatesFrozenPrivateLayout(t *testing.T) {
	t.Parallel()

	cacheRoot := privateRoot(t)
	store, err := Initialize(cacheRoot)
	if err != nil {
		t.Fatalf("Initialize(): %v", err)
	}
	wantRoot := filepath.Join(cacheRoot, ContentRootName)
	if store.Root() != wantRoot {
		t.Fatalf("Root() = %q, want %q", store.Root(), wantRoot)
	}

	wantDirectories := []string{
		".", "blobs", filepath.Join("blobs", "sha256"), "entries",
		"materializations", "quarantine", "staging",
	}
	for _, relative := range wantDirectories {
		path := filepath.Join(wantRoot, relative)
		metadata, statErr := os.Lstat(path)
		if statErr != nil {
			t.Fatalf("Lstat(%q): %v", relative, statErr)
		}
		if !metadata.IsDir() || metadata.Mode().Perm() != 0o700 || metadata.Mode()&os.ModeSymlink != 0 {
			t.Fatalf("layout %q mode = %v, want real directory 0700", relative, metadata.Mode())
		}
	}

	digest := sha256.Sum256([]byte("layout"))
	id := cache.BlobIDFromDigest(digest)
	wantBlobPath := filepath.Join(wantRoot, "blobs", "sha256", string(id)[:2], string(id)+".blob")
	if got, pathErr := store.BlobPath(id); pathErr != nil || got != wantBlobPath {
		t.Fatalf("BlobPath() = %q, %v, want %q", got, pathErr, wantBlobPath)
	}

	materializationID, err := cache.ParseMaterializationID("0123456789abcdef0123456789abcdef")
	if err != nil {
		t.Fatalf("ParseMaterializationID(): %v", err)
	}
	wantMaterializationPath := filepath.Join(wantRoot, "materializations", string(materializationID), "content")
	if got, pathErr := store.MaterializationPath(materializationID); pathErr != nil || got != wantMaterializationPath {
		t.Fatalf("MaterializationPath() = %q, %v, want %q", got, pathErr, wantMaterializationPath)
	}
}

func TestInitializeRejectsNonCanonicalUnsafeOrExistingForeignLayout(t *testing.T) {
	t.Parallel()

	t.Run("relative", func(t *testing.T) {
		if _, err := Initialize("relative/cache"); err == nil {
			t.Fatal("Initialize(relative) error = nil")
		}
	})

	t.Run("unclean", func(t *testing.T) {
		root := privateRoot(t)
		if _, err := Initialize(root + string(filepath.Separator) + "nested" + string(filepath.Separator) + ".."); err == nil {
			t.Fatal("Initialize(unclean) error = nil")
		}
	})

	t.Run("content root symlink", func(t *testing.T) {
		root := privateRoot(t)
		target := privateRoot(t)
		if err := os.Symlink(target, filepath.Join(root, ContentRootName)); err != nil {
			t.Fatalf("Symlink(): %v", err)
		}
		if _, err := Initialize(root); err == nil {
			t.Fatal("Initialize(symlink) error = nil")
		}
		if metadata, err := os.Lstat(filepath.Join(root, ContentRootName)); err != nil || metadata.Mode()&os.ModeSymlink == 0 {
			t.Fatalf("foreign symlink changed: mode=%v error=%v", metadata.Mode(), err)
		}
	})

	t.Run("wrong mode", func(t *testing.T) {
		root := privateRoot(t)
		contentRoot := filepath.Join(root, ContentRootName)
		if err := os.Mkdir(contentRoot, 0o755); err != nil { //nolint:gosec // deliberate unsafe existing-directory fixture
			t.Fatalf("Mkdir(): %v", err)
		}
		if _, err := Initialize(root); err == nil {
			t.Fatal("Initialize(wrong mode) error = nil")
		}
		metadata, err := os.Lstat(contentRoot)
		if err != nil {
			t.Fatalf("Lstat(): %v", err)
		}
		if metadata.Mode().Perm() != 0o755 {
			t.Fatalf("foreign mode changed to %04o", metadata.Mode().Perm())
		}
	})
}

func TestPublishBlobVerifiesIdentityDurabilityAndOpen(t *testing.T) {
	t.Parallel()

	store := newStore(t)
	content := []byte("complete verified content\n")
	digest := sha256.Sum256(content)
	expected := BlobIdentity{ID: cache.BlobIDFromDigest(digest), Size: int64(len(content))}

	result, err := store.PublishBlob(context.Background(), bytes.NewReader(content), int64(len(content)), &expected)
	if err != nil {
		t.Fatalf("PublishBlob(): %v", err)
	}
	if result.Identity != expected || result.Deduplicated {
		t.Fatalf("PublishBlob() result = %#v, want new %#v", result, expected)
	}
	metadata, err := os.Lstat(result.Path)
	if err != nil {
		t.Fatalf("Lstat(blob): %v", err)
	}
	if !metadata.Mode().IsRegular() || metadata.Mode().Perm() != 0o600 {
		t.Fatalf("blob mode = %v, want regular 0600", metadata.Mode())
	}

	opened, inspected, err := store.OpenBlob(expected.ID)
	if err != nil {
		t.Fatalf("OpenBlob(): %v", err)
	}
	defer opened.Close()
	if inspected.Identity != expected || inspected.Path != result.Path {
		t.Fatalf("OpenBlob() info = %#v, want %#v", inspected, result)
	}
	got, err := io.ReadAll(opened)
	if err != nil {
		t.Fatalf("ReadAll(opened): %v", err)
	}
	if !bytes.Equal(got, content) {
		t.Fatalf("opened bytes = %q, want %q", got, content)
	}
	assertDirectoryEmpty(t, filepath.Join(store.Root(), "staging"))
}

func TestPublishBlobRejectsOversizeAndIdentityMismatchWithoutPublication(t *testing.T) {
	t.Parallel()

	t.Run("oversize", func(t *testing.T) {
		store := newStore(t)
		reader := &countingReader{reader: strings.NewReader("0123456789")}
		if _, err := store.PublishBlob(context.Background(), reader, 4, nil); !errors.Is(err, ErrLimitExceeded) {
			t.Fatalf("PublishBlob() error = %v, want ErrLimitExceeded", err)
		}
		if got := reader.read.Load(); got > 5 {
			t.Fatalf("source bytes read = %d, want at most limit+1", got)
		}
		assertNoBlobFiles(t, store.Root())
		assertDirectoryEmpty(t, filepath.Join(store.Root(), "staging"))
	})

	t.Run("digest mismatch", func(t *testing.T) {
		store := newStore(t)
		content := []byte("actual")
		wrong := sha256.Sum256([]byte("different"))
		expected := BlobIdentity{ID: cache.BlobIDFromDigest(wrong), Size: int64(len(content))}
		if _, err := store.PublishBlob(context.Background(), bytes.NewReader(content), int64(len(content)), &expected); !errors.Is(err, ErrIdentityMismatch) {
			t.Fatalf("PublishBlob() error = %v, want ErrIdentityMismatch", err)
		}
		assertNoBlobFiles(t, store.Root())
		assertDirectoryEmpty(t, filepath.Join(store.Root(), "staging"))
	})

	t.Run("size mismatch", func(t *testing.T) {
		store := newStore(t)
		content := []byte("actual")
		digest := sha256.Sum256(content)
		expected := BlobIdentity{ID: cache.BlobIDFromDigest(digest), Size: int64(len(content) + 1)}
		if _, err := store.PublishBlob(context.Background(), bytes.NewReader(content), int64(len(content)+1), &expected); !errors.Is(err, ErrIdentityMismatch) {
			t.Fatalf("PublishBlob() error = %v, want ErrIdentityMismatch", err)
		}
		assertNoBlobFiles(t, store.Root())
		assertDirectoryEmpty(t, filepath.Join(store.Root(), "staging"))
	})

	t.Run("size-only expectation", func(t *testing.T) {
		store := newStore(t)
		content := []byte("actual")
		expected := BlobIdentity{Size: int64(len(content) + 1)}
		if _, err := store.PublishBlob(context.Background(), bytes.NewReader(content), int64(len(content)+1), &expected); !errors.Is(err, ErrIdentityMismatch) {
			t.Fatalf("PublishBlob() error = %v, want ErrIdentityMismatch", err)
		}
		assertNoBlobFiles(t, store.Root())
		assertDirectoryEmpty(t, filepath.Join(store.Root(), "staging"))
	})

	t.Run("source failure", func(t *testing.T) {
		store := newStore(t)
		sourceErr := errors.New("injected source failure")
		reader := io.MultiReader(strings.NewReader("partial"), errorReader{err: sourceErr})
		if _, err := store.PublishBlob(context.Background(), reader, 64, nil); !errors.Is(err, sourceErr) {
			t.Fatalf("PublishBlob() error = %v, want injected source failure", err)
		}
		assertNoBlobFiles(t, store.Root())
		assertDirectoryEmpty(t, filepath.Join(store.Root(), "staging"))
	})
}

func TestPublishBlobCancellationLeavesNoPartialPublication(t *testing.T) {
	t.Parallel()

	store := newStore(t)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := store.PublishBlob(ctx, strings.NewReader("not published"), 64, nil); !errors.Is(err, context.Canceled) {
		t.Fatalf("PublishBlob() error = %v, want context.Canceled", err)
	}
	assertNoBlobFiles(t, store.Root())
	assertDirectoryEmpty(t, filepath.Join(store.Root(), "staging"))
}

func TestPublishBlobConcurrentDeduplication(t *testing.T) {
	t.Parallel()

	store := newStore(t)
	content := bytes.Repeat([]byte("deduplicate-me"), 1024)
	digest := sha256.Sum256(content)
	expected := BlobIdentity{ID: cache.BlobIDFromDigest(digest), Size: int64(len(content))}

	const publishers = 24
	results := make(chan PublishResult, publishers)
	errorsChannel := make(chan error, publishers)
	var group sync.WaitGroup
	for range publishers {
		group.Add(1)
		go func() {
			defer group.Done()
			result, err := store.PublishBlob(context.Background(), bytes.NewReader(content), int64(len(content)), &expected)
			if err != nil {
				errorsChannel <- err
				return
			}
			results <- result
		}()
	}
	group.Wait()
	close(errorsChannel)
	close(results)
	for err := range errorsChannel {
		t.Errorf("PublishBlob(): %v", err)
	}
	newPublications := 0
	for result := range results {
		if result.Identity != expected {
			t.Errorf("result identity = %#v, want %#v", result.Identity, expected)
		}
		if !result.Deduplicated {
			newPublications++
		}
	}
	if newPublications != 1 {
		t.Fatalf("new publications = %d, want 1", newPublications)
	}
	assertDirectoryEmpty(t, filepath.Join(store.Root(), "staging"))
	if _, err := store.InspectBlob(expected.ID); err != nil {
		t.Fatalf("InspectBlob(): %v", err)
	}
}

func TestPublishBlobRejectsUnsafeShardAndStagingPermissions(t *testing.T) {
	t.Parallel()

	t.Run("symlink shard", func(t *testing.T) {
		store := newStore(t)
		content := []byte("symlink shard")
		digest := sha256.Sum256(content)
		id := cache.BlobIDFromDigest(digest)
		target := privateRoot(t)
		shard := filepath.Join(store.Root(), "blobs", "sha256", string(id)[:2])
		if err := os.Symlink(target, shard); err != nil {
			t.Fatalf("Symlink(): %v", err)
		}
		if _, err := store.PublishBlob(context.Background(), bytes.NewReader(content), int64(len(content)), nil); err == nil {
			t.Fatal("PublishBlob(symlink shard) error = nil")
		}
		assertDirectoryEmpty(t, target)
	})

	t.Run("wrong staging mode", func(t *testing.T) {
		store := newStore(t)
		staging := filepath.Join(store.Root(), "staging")
		if err := os.Chmod(staging, 0o500); err != nil { //nolint:gosec // deliberate negative fixture
			t.Fatalf("Chmod(): %v", err)
		}
		if _, err := store.PublishBlob(context.Background(), strings.NewReader("content"), 7, nil); err == nil {
			t.Fatal("PublishBlob(wrong staging mode) error = nil")
		}
		metadata, err := os.Lstat(staging)
		if err != nil {
			t.Fatalf("Lstat(staging): %v", err)
		}
		if metadata.Mode().Perm() != 0o500 {
			t.Fatalf("staging mode changed to %04o", metadata.Mode().Perm())
		}
	})
}

func TestPublishBlobNeverOverwritesExistingDerivedBasename(t *testing.T) {
	t.Parallel()

	store := newStore(t)
	content := []byte("verified publication")
	digest := sha256.Sum256(content)
	id := cache.BlobIDFromDigest(digest)
	path := createBlobParent(t, store, id)
	foreign := []byte("foreign bytes")
	if err := os.WriteFile(path, foreign, 0o600); err != nil {
		t.Fatalf("WriteFile(foreign): %v", err)
	}

	if _, err := store.PublishBlob(context.Background(), bytes.NewReader(content), int64(len(content)), nil); err == nil {
		t.Fatal("PublishBlob(existing corrupt basename) error = nil")
	}
	got, err := os.ReadFile(path) //nolint:gosec // exact test-owned derived cache path
	if err != nil {
		t.Fatalf("ReadFile(foreign): %v", err)
	}
	if !bytes.Equal(got, foreign) {
		t.Fatalf("existing derived basename was overwritten: got %q want %q", got, foreign)
	}
	assertDirectoryEmpty(t, filepath.Join(store.Root(), "staging"))
}

func TestInspectBlobRejectsSymlinkPermissionsHardlinksAndWrongDigest(t *testing.T) {
	t.Parallel()

	content := []byte("expected bytes")
	digest := sha256.Sum256(content)
	id := cache.BlobIDFromDigest(digest)

	tests := []struct {
		name  string
		setup func(t *testing.T, path string)
	}{
		{
			name: "symlink",
			setup: func(t *testing.T, path string) {
				t.Helper()
				target := filepath.Join(privateRoot(t), "target")
				if err := os.WriteFile(target, content, 0o600); err != nil {
					t.Fatalf("WriteFile(target): %v", err)
				}
				if err := os.Symlink(target, path); err != nil {
					t.Fatalf("Symlink(): %v", err)
				}
			},
		},
		{
			name: "public mode",
			setup: func(t *testing.T, path string) {
				t.Helper()
				if err := os.WriteFile(path, content, 0o644); err != nil { //nolint:gosec // deliberate negative fixture
					t.Fatalf("WriteFile(): %v", err)
				}
			},
		},
		{
			name: "hard link",
			setup: func(t *testing.T, path string) {
				t.Helper()
				if err := os.WriteFile(path, content, 0o600); err != nil {
					t.Fatalf("WriteFile(): %v", err)
				}
				if err := os.Link(path, path+".alias"); err != nil {
					t.Fatalf("Link(): %v", err)
				}
			},
		},
		{
			name: "wrong digest",
			setup: func(t *testing.T, path string) {
				t.Helper()
				if err := os.WriteFile(path, []byte("wrong bytes"), 0o600); err != nil {
					t.Fatalf("WriteFile(): %v", err)
				}
			},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			store := newStore(t)
			path := createBlobParent(t, store, id)
			test.setup(t, path)
			if _, err := store.InspectBlob(id); err == nil {
				t.Fatal("InspectBlob() error = nil")
			}
			if _, _, err := store.OpenBlob(id); err == nil {
				t.Fatal("OpenBlob() error = nil")
			}
		})
	}
}

func TestInspectMaterializationRevalidatesRandomIdentityAndContent(t *testing.T) {
	t.Parallel()

	store := newStore(t)
	id, err := cache.ParseMaterializationID("abcdef0123456789abcdef0123456789")
	if err != nil {
		t.Fatalf("ParseMaterializationID(): %v", err)
	}
	directory := filepath.Join(store.Root(), "materializations", string(id))
	if err := os.Mkdir(directory, 0o700); err != nil {
		t.Fatalf("Mkdir(materialization): %v", err)
	}
	content := []byte("mutable materialization")
	path := filepath.Join(directory, "content")
	if err := os.WriteFile(path, content, 0o600); err != nil {
		t.Fatalf("WriteFile(content): %v", err)
	}

	info, err := store.InspectMaterialization(id)
	if err != nil {
		t.Fatalf("InspectMaterialization(): %v", err)
	}
	digest := sha256.Sum256(content)
	if info.ID != id || info.Size != int64(len(content)) || info.SHA256 != cache.BlobIDFromDigest(digest) || info.Path != path {
		t.Fatalf("materialization info = %#v", info)
	}
	opened, openedInfo, err := store.OpenMaterialization(id)
	if err != nil {
		t.Fatalf("OpenMaterialization(): %v", err)
	}
	openedContent, readErr := io.ReadAll(opened)
	closeErr := opened.Close()
	if readErr != nil || closeErr != nil || !bytes.Equal(openedContent, content) || openedInfo != info {
		t.Fatalf("opened materialization content=%q info=%#v read-error=%v close-error=%v", openedContent, openedInfo, readErr, closeErr)
	}

	if err := os.Remove(path); err != nil {
		t.Fatalf("Remove(content): %v", err)
	}
	target := filepath.Join(privateRoot(t), "target")
	if err := os.WriteFile(target, content, 0o600); err != nil {
		t.Fatalf("WriteFile(target): %v", err)
	}
	if err := os.Symlink(target, path); err != nil {
		t.Fatalf("Symlink(content): %v", err)
	}
	if _, err := store.InspectMaterialization(id); err == nil {
		t.Fatal("InspectMaterialization(symlink) error = nil")
	}
}

func TestScanReportsCrashTempsUnknownOrphansAndSymlinksWithoutMutation(t *testing.T) {
	t.Parallel()

	store := newStore(t)
	staging := filepath.Join(store.Root(), "staging")
	crashTemp := filepath.Join(staging, ".amsftp-cache-blob-0123456789abcdef0123456789abcdef.tmp")
	unknown := filepath.Join(staging, "unknown")
	symlink := filepath.Join(staging, "link")
	if err := os.WriteFile(crashTemp, []byte("partial"), 0o600); err != nil {
		t.Fatalf("WriteFile(crash temp): %v", err)
	}
	if err := os.WriteFile(unknown, []byte("unknown"), 0o600); err != nil {
		t.Fatalf("WriteFile(unknown): %v", err)
	}
	if err := os.Symlink(unknown, symlink); err != nil {
		t.Fatalf("Symlink(): %v", err)
	}

	materializationID := "fedcba9876543210fedcba9876543210"
	materialization := filepath.Join(store.Root(), "materializations", materializationID)
	if err := os.Mkdir(materialization, 0o700); err != nil {
		t.Fatalf("Mkdir(materialization): %v", err)
	}
	if err := os.WriteFile(filepath.Join(materialization, "content"), []byte("orphan"), 0o600); err != nil {
		t.Fatalf("WriteFile(materialization): %v", err)
	}
	reserved := filepath.Join(store.Root(), "quarantine", "preserved")
	if err := os.WriteFile(reserved, []byte("reserved"), 0o600); err != nil {
		t.Fatalf("WriteFile(quarantine): %v", err)
	}

	report, err := store.Scan(256)
	if err != nil {
		t.Fatalf("Scan(): %v", err)
	}
	assertFinding(t, report.Orphans, "staging/.amsftp-cache-blob-0123456789abcdef0123456789abcdef.tmp", FindingCrashTemp)
	assertFinding(t, report.Orphans, "materializations/"+materializationID, FindingOrphanMaterialization)
	assertFinding(t, report.Unknown, "staging/unknown", FindingUnknown)
	assertFinding(t, report.Unknown, "quarantine/preserved", FindingReservedQuarantine)
	assertFinding(t, report.Symlinks, "staging/link", FindingSymlink)
	for _, path := range []string{crashTemp, unknown, symlink, materialization, reserved} {
		if _, err := os.Lstat(path); err != nil {
			t.Fatalf("reported path %q was changed or deleted: %v", path, err)
		}
	}

	limited, err := store.Scan(2)
	if err != nil {
		t.Fatalf("Scan(2): %v", err)
	}
	if !limited.Truncated || limited.Visited != 2 {
		t.Fatalf("limited report = %#v, want truncated after 2", limited)
	}
}

func privateRoot(t *testing.T) string {
	t.Helper()
	directory := testkit.PersistentTempDir(t)
	if err := os.Chmod(directory, 0o700); err != nil { //nolint:gosec // exact owner-private directory mode
		t.Fatalf("Chmod(temp root): %v", err)
	}
	return directory
}

func newStore(t *testing.T) *Store {
	t.Helper()
	store, err := Initialize(privateRoot(t))
	if err != nil {
		t.Fatalf("Initialize(): %v", err)
	}
	return store
}

func createBlobParent(t *testing.T, store *Store, id cache.BlobID) string {
	t.Helper()
	path, err := store.BlobPath(id)
	if err != nil {
		t.Fatalf("BlobPath(): %v", err)
	}
	if err := os.Mkdir(filepath.Dir(path), 0o700); err != nil {
		t.Fatalf("Mkdir(blob shard): %v", err)
	}
	return path
}

func assertDirectoryEmpty(t *testing.T, path string) {
	t.Helper()
	entries, err := os.ReadDir(path)
	if err != nil {
		t.Fatalf("ReadDir(%q): %v", path, err)
	}
	if len(entries) != 0 {
		names := make([]string, 0, len(entries))
		for _, entry := range entries {
			names = append(names, entry.Name())
		}
		sort.Strings(names)
		t.Fatalf("directory %q is not empty: %v", path, names)
	}
}

func assertNoBlobFiles(t *testing.T, root string) {
	t.Helper()
	var files []string
	err := filepath.WalkDir(filepath.Join(root, "blobs", "sha256"), func(path string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if !entry.IsDir() {
			files = append(files, path)
		}
		return nil
	})
	if err != nil {
		t.Fatalf("WalkDir(blobs): %v", err)
	}
	if len(files) != 0 {
		t.Fatalf("unexpected blob files: %v", files)
	}
}

func assertFinding(t *testing.T, findings []Finding, relative string, kind FindingKind) {
	t.Helper()
	for _, finding := range findings {
		if finding.RelativePath == relative && finding.Kind == kind {
			return
		}
	}
	t.Fatalf("finding %q/%q missing from %#v", relative, kind, findings)
}

type countingReader struct {
	reader io.Reader
	read   atomic.Int64
}

type errorReader struct {
	err error
}

func (reader errorReader) Read([]byte) (int, error) {
	return 0, reader.err
}

func (reader *countingReader) Read(destination []byte) (int, error) {
	count, err := reader.reader.Read(destination)
	reader.read.Add(int64(count))
	return count, err
}
