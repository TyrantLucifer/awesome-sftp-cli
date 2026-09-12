package compatibility

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/TyrantLucifer/awesome-sftp-cli/internal/cachefs"
	"github.com/TyrantLucifer/awesome-sftp-cli/internal/config"
	"github.com/TyrantLucifer/awesome-sftp-cli/internal/helper"
	"github.com/TyrantLucifer/awesome-sftp-cli/internal/state/migration"
	_ "github.com/TyrantLucifer/awesome-sftp-cli/internal/state/sqlite"
	"github.com/TyrantLucifer/awesome-sftp-cli/internal/workspace"
)

type historicalSource struct {
	Boundary         string
	Version          string
	ProvenanceCommit string
	Fixture          string
	SHA256           string
}

var historicalSources = []historicalSource{
	{Boundary: "config document", Version: "1", ProvenanceCommit: "312bcccbcbd54246bbe5ff9babf4f14560449176", Fixture: "internal/compatibility/testdata/historical/config-v1-exact-main.json", SHA256: "8c7c60ffcb676a47669b45fbb01334dde662984d6fdfcf5a25983d226cf24e04"},
	{Boundary: "workspace document", Version: "1", ProvenanceCommit: "e07413d46f516f8b0f92c61d006927c1aa319f0f", Fixture: "internal/compatibility/testdata/historical/workspace-v1-stage1.json", SHA256: "9b8b085174b455805cd38a899702cad1363e6b1cf19a4bc98b5b715ebf9c8220"},
	{Boundary: "workspace document", Version: "2", ProvenanceCommit: "8bbb0f144583bbff10746ebdb22f82f86b4655e6", Fixture: "internal/compatibility/testdata/historical/workspace-v2-stage3.json", SHA256: "1f137d8470e2d005d1672df39fb3c8bf6c7107b766ce9b62d3581c92680cdd40"},
	{Boundary: "sqlite state", Version: "1", ProvenanceCommit: "486a63f90be51c0d79a454bef52e9e3302df5250", Fixture: "internal/compatibility/testdata/historical/sqlite-v1-stage2.sqlite", SHA256: "51f218895205098523be59d6ce58ac87d93d5f61746caae3c9c4e01ed18ce080"},
	{Boundary: "sqlite state", Version: "2", ProvenanceCommit: "4eb1961b7b3b5495620fb1f6fcb3b88c52a4fba9", Fixture: "internal/compatibility/testdata/historical/sqlite-v2-stage3.sqlite", SHA256: "d3f5fb72368d0d6b0aa82c9dca19f883c6acfda36050e028ac98822cffd489de"},
	{Boundary: "sqlite state", Version: "3", ProvenanceCommit: "939ba9c5d978b8ea5bf1ae060ff62a0769d0d6c0", Fixture: "internal/compatibility/testdata/historical/sqlite-v3-stage3.sqlite", SHA256: "deabc90d2f3699eb10c520f71fbc691e10eac65153bd1a3c2ae3f78fe41213cf"},
	{Boundary: "sqlite verified backup", Version: "1", ProvenanceCommit: "4eb1961b7b3b5495620fb1f6fcb3b88c52a4fba9", Fixture: "internal/compatibility/testdata/historical/sqlite-backup-v1-stage3.sqlite", SHA256: "0cc96fdafb32ab94d7d3dcef8fb4225ba67df5d504482c5e10b0a79a1cd2c3bb"},
	{Boundary: "sqlite verified backup", Version: "2", ProvenanceCommit: "939ba9c5d978b8ea5bf1ae060ff62a0769d0d6c0", Fixture: "internal/compatibility/testdata/historical/sqlite-backup-v2-stage3.sqlite", SHA256: "4baec16549566416c959fe5b75f85b7e0c94cfff069a93c59fdca422eba079c2"},
	{Boundary: "cache entry manifest", Version: "1", ProvenanceCommit: "8a4ada06836b9ed71c72b40949d6b87d8e1f849a", Fixture: "internal/compatibility/testdata/historical/cache-entry-manifest-v1-stage3.json", SHA256: "9979ce7f860182d4553c482a91c05e2d30bc81a540cc0188351ee068781ff1e0"},
	{Boundary: "cache materialization manifest", Version: "1", ProvenanceCommit: "8a4ada06836b9ed71c72b40949d6b87d8e1f849a", Fixture: "internal/compatibility/testdata/historical/cache-materialization-manifest-v1-stage3.json", SHA256: "b2992fbc5fe52198d1738ac42d3dd165289632e2103e1f760d2652f058a5272c"},
	{Boundary: "helper release manifest", Version: "1", ProvenanceCommit: "145b50ae871aa91f8acc0505d2b6b9bd19bae742", Fixture: "internal/compatibility/testdata/historical/helper-release-manifest-v1-stage4.txt", SHA256: "fdaa89f1dc9fa60458b8cec81f19dfd3c028fee21f056b1c0f4650fcf4556c6f"},
	{Boundary: "helper state index", Version: "1", ProvenanceCommit: "145b50ae871aa91f8acc0505d2b6b9bd19bae742", Fixture: "internal/compatibility/testdata/historical/helper-state-index-v1-stage4.json", SHA256: "ed71e086bdd008dd959afa5b14b02a1713bd2b811ebc2d4be44027e4acdfb9fa"},
	{Boundary: "helper metadata", Version: "1", ProvenanceCommit: "145b50ae871aa91f8acc0505d2b6b9bd19bae742", Fixture: "internal/compatibility/testdata/historical/helper-metadata-v1-stage4.json", SHA256: "8062c2e12178bd0e0f002f4e0901198cd2363e1317b21d199bf57dd34f1016b1"},
}

func TestCapturedHistoricalSourcesAreImmutableAndReadableByCurrentOwners(t *testing.T) {
	repositoryRoot := os.DirFS(filepath.Clean(filepath.Join("..", "..")))
	for _, source := range historicalSources {
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
		case "sqlite state", "sqlite verified backup":
			validateHistoricalSQLiteFixture(t, source)
		case "cache entry manifest":
			var manifest cachefs.EntryManifest
			if err := json.Unmarshal(raw, &manifest); err != nil || manifest.Format != cachefs.ManifestFormat {
				t.Fatalf("current cache entry type rejected %s: %#v, %v", source.Fixture, manifest, err)
			}
		case "cache materialization manifest":
			var manifest cachefs.MaterializationManifest
			if err := json.Unmarshal(raw, &manifest); err != nil || manifest.Format != cachefs.ManifestFormat {
				t.Fatalf("current cache materialization type rejected %s: %#v, %v", source.Fixture, manifest, err)
			}
		case "helper release manifest":
			if _, err := helper.ParseManifestV1(raw); err != nil {
				t.Fatalf("current Helper manifest reader rejected %s: %v", source.Fixture, err)
			}
		case "helper state index", "helper metadata":
			if !json.Valid(raw) {
				t.Fatalf("historical Helper state is not JSON: %s", source.Fixture)
			}
		default:
			t.Fatalf("captured fixture %s has no owner reader assertion", source.Fixture)
		}
	}
}

func validateHistoricalSQLiteFixture(t *testing.T, source historicalSource) {
	t.Helper()
	allMigrations, contracts := migration.CompiledSet()
	var head uint64
	var migrations []migration.Migration
	switch source.Version {
	case "1":
		head, migrations = 1, allMigrations[:1]
	case "2":
		head, migrations = 2, allMigrations[:2]
	case "3":
		head, migrations = 3, allMigrations[:3]
	default:
		t.Fatalf("invalid historical SQLite head %q", source.Version)
	}
	path := filepath.Clean(filepath.Join("..", "..", source.Fixture))
	database, err := sql.Open("sqlite", "file:"+path+"?mode=ro&immutable=1")
	if err != nil {
		t.Fatalf("open historical SQLite %s: %v", source.Fixture, err)
	}
	database.SetMaxOpenConns(1)
	defer database.Close()
	connection, err := database.Conn(context.Background())
	if err != nil {
		t.Fatalf("reserve historical SQLite %s: %v", source.Fixture, err)
	}
	defer connection.Close()
	if err := migration.ValidateHead(context.Background(), connection, migrations, contracts, head); err != nil {
		t.Fatalf("current SQLite owner rejected %s: %v", source.Fixture, err)
	}
}
