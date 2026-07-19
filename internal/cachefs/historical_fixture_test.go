package cachefs

import (
	"context"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/TyrantLucifer/awesome-sftp-cli/internal/cache"
	"github.com/TyrantLucifer/awesome-sftp-cli/internal/testkit"
)

func TestCurrentCacheOwnerReadsPinnedHistoricalManifestV1(t *testing.T) {
	root := testkit.PersistentTempDir(t)
	store, err := Initialize(root)
	if err != nil {
		t.Fatal(err)
	}
	content := []byte("stage6 historical cache content")
	blob, err := store.PublishBlob(context.Background(), strings.NewReader(string(content)), int64(len(content)), nil)
	if err != nil {
		t.Fatal(err)
	}
	entryID, err := cache.ParseEntryID("28f656a969e2d43877b21fdfc9af35541db538acc10a1afdf87d9658e36b2171")
	if err != nil {
		t.Fatal(err)
	}
	entryDirectory := filepath.Join(root, ContentRootName, "entries", string(entryID))
	if err := os.MkdirAll(entryDirectory, 0o700); err != nil {
		t.Fatal(err)
	}
	copyHistoricalCacheFixture(t, "cache-entry-manifest-v1-stage3.json", entryDirectory)
	entry, err := store.ReadEntryManifest(entryID)
	if err != nil {
		t.Fatal(err)
	}
	if entry.Blob.Identity != blob.Identity || string(entry.CanonicalPath) != "/历史/cache-file.txt" {
		t.Fatalf("historical entry = %#v", entry)
	}

	materializationID, err := cache.ParseMaterializationID(strings.Repeat("6", 32))
	if err != nil {
		t.Fatal(err)
	}
	materializationDirectory := filepath.Join(root, ContentRootName, "materializations", string(materializationID))
	if err := os.MkdirAll(materializationDirectory, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(materializationDirectory, "content"), content, 0o600); err != nil {
		t.Fatal(err)
	}
	copyHistoricalCacheFixture(t, "cache-materialization-manifest-v1-stage3.json", materializationDirectory)
	materialization, err := store.ReadMaterializationManifest(materializationID)
	if err != nil {
		t.Fatal(err)
	}
	if materialization.ContentSHA256 != blob.Identity.ID || materialization.EntryID != entryID {
		t.Fatalf("historical materialization = %#v", materialization)
	}
}

func copyHistoricalCacheFixture(t *testing.T, source, destinationDirectory string) {
	t.Helper()
	raw, err := fs.ReadFile(os.DirFS(filepath.Join("..", "compatibility", "testdata", "historical")), source)
	if err != nil {
		t.Fatal(err)
	}
	destination, err := os.OpenRoot(destinationDirectory)
	if err != nil {
		t.Fatal(err)
	}
	defer destination.Close()
	if err := destination.WriteFile(ManifestFilename, raw, 0o600); err != nil {
		t.Fatal(err)
	}
}
