// Package compatibility publishes the frozen public format and protocol
// inventory without adding a second compatibility decision path.
package compatibility

import (
	"fmt"
	"strings"

	"github.com/TyrantLucifer/awesome-mac-sftp/internal/app"
	"github.com/TyrantLucifer/awesome-mac-sftp/internal/cachefs"
	"github.com/TyrantLucifer/awesome-mac-sftp/internal/config"
	"github.com/TyrantLucifer/awesome-mac-sftp/internal/helper"
	"github.com/TyrantLucifer/awesome-mac-sftp/internal/ipc"
	"github.com/TyrantLucifer/awesome-mac-sftp/internal/state/migration"
	"github.com/TyrantLucifer/awesome-mac-sftp/internal/workspace"
)

type Boundary struct {
	Name          string
	Current       string
	Reads         string
	Writes        string
	OnUnsupported string
	Owner         string
}

type HistoricalSourceStatus string

const (
	HistoricalSourceCaptured        HistoricalSourceStatus = "captured"
	HistoricalSourceCaptureRequired HistoricalSourceStatus = "capture-required-before-M6.2-mutation"
)

type HistoricalSource struct {
	Boundary         string
	Version          string
	ProvenanceCommit string
	Status           HistoricalSourceStatus
	Fixture          string
	SHA256           string
	Owner            string
}

func Inventory() []Boundary {
	return []Boundary{
		{Name: "cli contract", Current: fmt.Sprint(app.PublicCLIContractVersion), Reads: "1", Writes: "1", OnUnsupported: "unknown command or output version rejected", Owner: "internal/app"},
		{Name: "config document", Current: fmt.Sprint(config.SchemaVersion), Reads: "1", Writes: "1", OnUnsupported: "newer schema rejected before use", Owner: "internal/config"},
		{Name: "config effective output", Current: fmt.Sprint(config.EffectiveOutputVersion), Reads: "1", Writes: "1", OnUnsupported: "unknown output version rejected by consumers", Owner: "internal/config"},
		{Name: "workspace document", Current: fmt.Sprint(workspace.SchemaVersion), Reads: "1-2", Writes: "2", OnUnsupported: "newer schema rejected before write", Owner: "internal/workspace"},
		{Name: "sqlite state", Current: fmt.Sprint(migration.SchemaHead), Reads: "1-4", Writes: "4", OnUnsupported: "newer head rejected before runtime write", Owner: "internal/state/migration"},
		{Name: "cache filesystem manifest", Current: fmt.Sprint(cachefs.ManifestFormat), Reads: "1", Writes: "1", OnUnsupported: "unknown format rejected before content use", Owner: "internal/cachefs"},
		{Name: "client-daemon IPC", Current: fmt.Sprintf("%d.%d", ipc.ProtocolMajor, ipc.ProtocolMinor), Reads: "1.0", Writes: "1.0", OnUnsupported: "no shared major/minor fails handshake", Owner: "internal/ipc"},
		{Name: "helper release manifest", Current: fmt.Sprint(helper.ManifestFormatVersion), Reads: "1", Writes: "release-only", OnUnsupported: "unknown header rejected before install", Owner: "internal/helper"},
		{Name: "helper wire envelope", Current: fmt.Sprint(helper.EnvelopeVersion), Reads: "1", Writes: "1", OnUnsupported: "unknown envelope rejected before dispatch", Owner: "internal/helper"},
	}
}

func HistoricalSources() []HistoricalSource {
	return []HistoricalSource{
		{Boundary: "config document", Version: "1", ProvenanceCommit: "312bcccbcbd54246bbe5ff9babf4f14560449176", Status: HistoricalSourceCaptured, Fixture: "internal/compatibility/testdata/historical/config-v1-exact-main.json", SHA256: "8c7c60ffcb676a47669b45fbb01334dde662984d6fdfcf5a25983d226cf24e04", Owner: "internal/config"},
		{Boundary: "workspace document", Version: "1", ProvenanceCommit: "e07413d46f516f8b0f92c61d006927c1aa319f0f", Status: HistoricalSourceCaptured, Fixture: "internal/compatibility/testdata/historical/workspace-v1-stage1.json", SHA256: "9b8b085174b455805cd38a899702cad1363e6b1cf19a4bc98b5b715ebf9c8220", Owner: "internal/workspace"},
		{Boundary: "workspace document", Version: "2", ProvenanceCommit: "8bbb0f144583bbff10746ebdb22f82f86b4655e6", Status: HistoricalSourceCaptured, Fixture: "internal/compatibility/testdata/historical/workspace-v2-stage3.json", SHA256: "1f137d8470e2d005d1672df39fb3c8bf6c7107b766ce9b62d3581c92680cdd40", Owner: "internal/workspace"},
		{Boundary: "sqlite state", Version: "1", ProvenanceCommit: "486a63f90be51c0d79a454bef52e9e3302df5250", Status: HistoricalSourceCaptured, Fixture: "internal/compatibility/testdata/historical/sqlite-v1-stage2.sqlite", SHA256: "51f218895205098523be59d6ce58ac87d93d5f61746caae3c9c4e01ed18ce080", Owner: "internal/state/migration"},
		{Boundary: "sqlite state", Version: "2", ProvenanceCommit: "4eb1961b7b3b5495620fb1f6fcb3b88c52a4fba9", Status: HistoricalSourceCaptured, Fixture: "internal/compatibility/testdata/historical/sqlite-v2-stage3.sqlite", SHA256: "d3f5fb72368d0d6b0aa82c9dca19f883c6acfda36050e028ac98822cffd489de", Owner: "internal/state/migration"},
		{Boundary: "sqlite state", Version: "3", ProvenanceCommit: "939ba9c5d978b8ea5bf1ae060ff62a0769d0d6c0", Status: HistoricalSourceCaptured, Fixture: "internal/compatibility/testdata/historical/sqlite-v3-stage3.sqlite", SHA256: "deabc90d2f3699eb10c520f71fbc691e10eac65153bd1a3c2ae3f78fe41213cf", Owner: "internal/state/migration"},
		{Boundary: "sqlite verified backup", Version: "1", ProvenanceCommit: "4eb1961b7b3b5495620fb1f6fcb3b88c52a4fba9", Status: HistoricalSourceCaptured, Fixture: "internal/compatibility/testdata/historical/sqlite-backup-v1-stage3.sqlite", SHA256: "0cc96fdafb32ab94d7d3dcef8fb4225ba67df5d504482c5e10b0a79a1cd2c3bb", Owner: "internal/statefs"},
		{Boundary: "sqlite verified backup", Version: "2", ProvenanceCommit: "939ba9c5d978b8ea5bf1ae060ff62a0769d0d6c0", Status: HistoricalSourceCaptured, Fixture: "internal/compatibility/testdata/historical/sqlite-backup-v2-stage3.sqlite", SHA256: "4baec16549566416c959fe5b75f85b7e0c94cfff069a93c59fdca422eba079c2", Owner: "internal/statefs"},
		{Boundary: "cache entry manifest", Version: "1", ProvenanceCommit: "8a4ada06836b9ed71c72b40949d6b87d8e1f849a", Status: HistoricalSourceCaptured, Fixture: "internal/compatibility/testdata/historical/cache-entry-manifest-v1-stage3.json", SHA256: "9979ce7f860182d4553c482a91c05e2d30bc81a540cc0188351ee068781ff1e0", Owner: "internal/cachefs"},
		{Boundary: "cache materialization manifest", Version: "1", ProvenanceCommit: "8a4ada06836b9ed71c72b40949d6b87d8e1f849a", Status: HistoricalSourceCaptured, Fixture: "internal/compatibility/testdata/historical/cache-materialization-manifest-v1-stage3.json", SHA256: "b2992fbc5fe52198d1738ac42d3dd165289632e2103e1f760d2652f058a5272c", Owner: "internal/cachefs"},
		{Boundary: "helper release manifest", Version: "1", ProvenanceCommit: "145b50ae871aa91f8acc0505d2b6b9bd19bae742", Status: HistoricalSourceCaptured, Fixture: "internal/compatibility/testdata/historical/helper-release-manifest-v1-stage4.txt", SHA256: "fdaa89f1dc9fa60458b8cec81f19dfd3c028fee21f056b1c0f4650fcf4556c6f", Owner: "internal/helper"},
		{Boundary: "helper state index", Version: "1", ProvenanceCommit: "145b50ae871aa91f8acc0505d2b6b9bd19bae742", Status: HistoricalSourceCaptured, Fixture: "internal/compatibility/testdata/historical/helper-state-index-v1-stage4.json", SHA256: "ed71e086bdd008dd959afa5b14b02a1713bd2b811ebc2d4be44027e4acdfb9fa", Owner: "internal/helper"},
		{Boundary: "helper metadata", Version: "1", ProvenanceCommit: "145b50ae871aa91f8acc0505d2b6b9bd19bae742", Status: HistoricalSourceCaptured, Fixture: "internal/compatibility/testdata/historical/helper-metadata-v1-stage4.json", SHA256: "8062c2e12178bd0e0f002f4e0901198cd2363e1317b21d199bf57dd34f1016b1", Owner: "internal/helper"},
	}
}

func SnapshotText() string {
	lines := make([]string, 0, len(Inventory()))
	for _, boundary := range Inventory() {
		lines = append(lines, fmt.Sprintf("%s | current %s | reads %s | writes %s | %s | %s", boundary.Name, boundary.Current, boundary.Reads, boundary.Writes, boundary.OnUnsupported, boundary.Owner))
	}
	return strings.Join(lines, "\n")
}

func HistoricalSourceSnapshotText() string {
	lines := make([]string, 0, len(HistoricalSources()))
	for _, source := range HistoricalSources() {
		digest := source.SHA256
		if digest == "" {
			digest = "-"
		}
		lines = append(lines, fmt.Sprintf("%s v%s | %s | %s | %s | %s | %s", source.Boundary, source.Version, source.ProvenanceCommit, source.Status, source.Fixture, digest, source.Owner))
	}
	return strings.Join(lines, "\n")
}

func Markdown() string {
	var output strings.Builder
	output.WriteString("# Compatibility Boundaries\n\n")
	output.WriteString("This is the frozen AMSFTP 1.0 public version inventory. The owning package remains the sole runtime decision point; this registry and its exact snapshot test make drift visible before migration or compatibility code changes. `Reads` lists formats accepted directly or through the explicit migration path.\n\n")
	output.WriteString("| Boundary | Current | Reads | Writes | Unsupported/newer behavior | Owner |\n")
	output.WriteString("|---|---:|---:|---:|---|---|\n")
	for _, boundary := range Inventory() {
		fmt.Fprintf(&output, "| %s | %s | %s | %s | %s | `%s` |\n", boundary.Name, boundary.Current, boundary.Reads, boundary.Writes, boundary.OnUnsupported, boundary.Owner)
	}
	output.WriteString("\nThe SQLite `1-4` read range includes forward migration to head 4; it is not a promise that arbitrary historical or newer databases are writable. Cache catalog tables were introduced by SQLite schema 2, while cache filesystem manifests remain an independently validated format 1. Helper release-manifest writes are release-only and production distribution remains CLOSED until the protected signing/notarization/byte-binding gates pass.\n")
	output.WriteString("\n## Frozen historical source inventory\n\n")
	output.WriteString("This inventory freezes the complete set of persistent source formats before M6.2 migration code changes. Every row is captured as repository bytes pinned by SHA-256 and exercised by a current-owner reader test. Provenance names the commit that first wrote the source format, except the config sample which intentionally comes from the frozen exact-main baseline.\n\n")
	output.WriteString("| Source | Version | Provenance commit | Capture status | Fixture | SHA-256 | Owner |\n")
	output.WriteString("|---|---:|---|---|---|---|---|\n")
	for _, source := range HistoricalSources() {
		digest := source.SHA256
		if digest == "" {
			digest = "-"
		}
		fmt.Fprintf(&output, "| %s | %s | `%s` | %s | `%s` | `%s` | `%s` |\n", source.Boundary, source.Version, source.ProvenanceCommit, source.Status, source.Fixture, digest, source.Owner)
	}
	return output.String()
}
