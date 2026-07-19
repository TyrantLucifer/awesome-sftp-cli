package helper

import (
	"bytes"
	"io/fs"
	"os"
	"path/filepath"
	"testing"

	"github.com/TyrantLucifer/awesome-sftp-cli/internal/domain"
	"github.com/TyrantLucifer/awesome-sftp-cli/internal/testkit"
)

func TestCurrentHelperOwnerReadsPinnedHistoricalManifestAndStateV1(t *testing.T) {
	manifestRaw := readHistoricalHelperFixture(t, "helper-release-manifest-v1-stage4.txt")
	manifest, err := ParseManifestV1(manifestRaw)
	if err != nil {
		t.Fatal(err)
	}
	root := testkit.PersistentTempDir(t)
	indexPath := filepath.Join(root, "state.json")
	if err := os.WriteFile(indexPath, readHistoricalHelperFixture(t, "helper-state-index-v1-stage4.json"), 0o600); err != nil {
		t.Fatal(err)
	}
	var index stateIndex
	if err := decodeBoundedStateFile(indexPath, &index); err != nil {
		t.Fatal(err)
	}
	if len(index.Records) != 1 {
		t.Fatalf("historical index records = %d, want 1", len(index.Records))
	}
	record := index.Records[0]
	if err := os.WriteFile(filepath.Join(root, "metadata-"+record.MetadataID+".json"), readHistoricalHelperFixture(t, "helper-metadata-v1-stage4.json"), 0o600); err != nil {
		t.Fatal(err)
	}
	store, err := NewStateStore(root)
	if err != nil {
		t.Fatal(err)
	}
	endpointID, err := domain.ParseEndpointID(record.EndpointID)
	if err != nil {
		t.Fatal(err)
	}
	enabled, err := store.LoadEnabled(endpointID, record.ProtocolMajor, manifest.Target())
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(enabled.RawManifest, manifestRaw) || enabled.FinalPath != record.FinalPath {
		t.Fatalf("historical enabled record = %#v", enabled)
	}
}

func readHistoricalHelperFixture(t *testing.T, name string) []byte {
	t.Helper()
	raw, err := fs.ReadFile(os.DirFS(filepath.Join("..", "compatibility", "testdata", "historical")), name)
	if err != nil {
		t.Fatal(err)
	}
	return raw
}
