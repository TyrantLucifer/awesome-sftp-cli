package compatibility

import (
	"crypto/sha256"
	"encoding/hex"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/TyrantLucifer/awesome-mac-sftp/internal/app"
	"github.com/TyrantLucifer/awesome-mac-sftp/internal/cachefs"
	"github.com/TyrantLucifer/awesome-mac-sftp/internal/config"
	"github.com/TyrantLucifer/awesome-mac-sftp/internal/helper"
	"github.com/TyrantLucifer/awesome-mac-sftp/internal/ipc"
	"github.com/TyrantLucifer/awesome-mac-sftp/internal/state/migration"
	"github.com/TyrantLucifer/awesome-mac-sftp/internal/workspace"
)

func TestInventorySnapshotFreezesEveryPublicCompatibilityBoundary(t *testing.T) {
	want := strings.TrimSpace(`
cli contract | current 1 | reads 1 | writes 1 | unknown command or output version rejected | internal/app
config document | current 1 | reads 1 | writes 1 | newer schema rejected before use | internal/config
config effective output | current 1 | reads 1 | writes 1 | unknown output version rejected by consumers | internal/config
workspace document | current 2 | reads 1-2 | writes 2 | newer schema rejected before write | internal/workspace
sqlite state | current 3 | reads 1-3 | writes 3 | newer head rejected before runtime write | internal/state/migration
cache filesystem manifest | current 1 | reads 1 | writes 1 | unknown format rejected before content use | internal/cachefs
client-daemon IPC | current 1.0 | reads 1.0 | writes 1.0 | no shared major/minor fails handshake | internal/ipc
helper release manifest | current 1 | reads 1 | writes release-only | unknown header rejected before install | internal/helper
helper wire envelope | current 1 | reads 1 | writes 1 | unknown envelope rejected before dispatch | internal/helper`)
	if got := SnapshotText(); got != want {
		t.Fatalf("compatibility inventory changed:\n%s\nwant:\n%s", got, want)
	}
}

func TestInventoryValuesComeFromOwningPackages(t *testing.T) {
	if app.PublicCLIContractVersion != 1 || config.SchemaVersion != 1 || config.EffectiveOutputVersion != 1 || workspace.SchemaVersion != 2 ||
		migration.SchemaHead != 3 || cachefs.ManifestFormat != 1 || ipc.ProtocolMajor != 1 || ipc.ProtocolMinor != 0 ||
		helper.ManifestFormatVersion != 1 || helper.EnvelopeVersion != 1 {
		t.Fatal("an owning package version changed without updating the compatibility inventory")
	}
}

func TestCommittedCompatibilityReferenceMatchesRegistry(t *testing.T) {
	raw, err := os.ReadFile("../../docs/product/compatibility-boundaries.md")
	if err != nil {
		t.Fatal(err)
	}
	if got, want := string(raw), Markdown(); got != want {
		t.Fatalf("committed compatibility reference drifted from registry")
	}
}

func TestHistoricalSourceSnapshotFreezesEveryPersistentMigrationInput(t *testing.T) {
	want := strings.TrimSpace(`
config document v1 | 312bcccbcbd54246bbe5ff9babf4f14560449176 | captured | internal/compatibility/testdata/historical/config-v1-exact-main.json | 8c7c60ffcb676a47669b45fbb01334dde662984d6fdfcf5a25983d226cf24e04 | internal/config
workspace document v1 | e07413d46f516f8b0f92c61d006927c1aa319f0f | captured | internal/compatibility/testdata/historical/workspace-v1-stage1.json | 9b8b085174b455805cd38a899702cad1363e6b1cf19a4bc98b5b715ebf9c8220 | internal/workspace
workspace document v2 | 8bbb0f144583bbff10746ebdb22f82f86b4655e6 | captured | internal/compatibility/testdata/historical/workspace-v2-stage3.json | 1f137d8470e2d005d1672df39fb3c8bf6c7107b766ce9b62d3581c92680cdd40 | internal/workspace
sqlite state v1 | 486a63f90be51c0d79a454bef52e9e3302df5250 | capture-required-before-M6.2-mutation | internal/compatibility/testdata/historical/sqlite-v1-stage2.sqlite | - | internal/state/migration
sqlite state v2 | 4eb1961b7b3b5495620fb1f6fcb3b88c52a4fba9 | capture-required-before-M6.2-mutation | internal/compatibility/testdata/historical/sqlite-v2-stage3.sqlite | - | internal/state/migration
sqlite state v3 | 939ba9c5d978b8ea5bf2aadd3485831d78b533c2e | capture-required-before-M6.2-mutation | internal/compatibility/testdata/historical/sqlite-v3-stage3.sqlite | - | internal/state/migration
cache entry manifest v1 | 8a4ada06836b9ed71c72b40949d6b87d8e1f849a | capture-required-before-M6.2-mutation | internal/compatibility/testdata/historical/cache-entry-manifest-v1-stage3.json | - | internal/cachefs
cache materialization manifest v1 | 8a4ada06836b9ed71c72b40949d6b87d8e1f849a | capture-required-before-M6.2-mutation | internal/compatibility/testdata/historical/cache-materialization-manifest-v1-stage3.json | - | internal/cachefs
helper release manifest v1 | 145b50ae871aa91f8acc0505d2b6b9bd19bae742 | capture-required-before-M6.2-mutation | internal/compatibility/testdata/historical/helper-release-manifest-v1-stage4.txt | - | internal/helper
helper state index v1 | 145b50ae871aa91f8acc0505d2b6b9bd19bae742 | capture-required-before-M6.2-mutation | internal/compatibility/testdata/historical/helper-state-index-v1-stage4.json | - | internal/helper
helper metadata v1 | 145b50ae871aa91f8acc0505d2b6b9bd19bae742 | capture-required-before-M6.2-mutation | internal/compatibility/testdata/historical/helper-metadata-v1-stage4.json | - | internal/helper`)
	if got := HistoricalSourceSnapshotText(); got != want {
		t.Fatalf("historical source inventory changed:\n%s\nwant:\n%s", got, want)
	}
}

func TestCapturedHistoricalSourcesAreImmutableAndReadableByCurrentOwners(t *testing.T) {
	repositoryRoot := os.DirFS(filepath.Clean(filepath.Join("..", "..")))
	for _, source := range HistoricalSources() {
		if source.Status != HistoricalSourceCaptured {
			continue
		}
		raw, err := fs.ReadFile(repositoryRoot, source.Fixture)
		if err != nil {
			t.Fatalf("read %s: %v", source.Fixture, err)
		}
		digest := sha256.Sum256(raw)
		if got := hex.EncodeToString(digest[:]); got != source.SHA256 {
			t.Fatalf("%s SHA-256 = %s, want %s", source.Fixture, got, source.SHA256)
		}
		switch source.Boundary {
		case "config document":
			if _, err := config.Decode(strings.NewReader(string(raw))); err != nil {
				t.Fatalf("current config reader rejected %s: %v", source.Fixture, err)
			}
		case "workspace document":
			if _, err := workspace.Decode(strings.NewReader(string(raw))); err != nil {
				t.Fatalf("current workspace reader rejected %s: %v", source.Fixture, err)
			}
		default:
			t.Fatalf("captured fixture %s has no owner reader assertion", source.Fixture)
		}
	}
}
