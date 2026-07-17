package compatibility

import (
	"os"
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
