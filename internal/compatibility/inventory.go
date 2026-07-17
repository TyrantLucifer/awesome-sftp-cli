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

func Inventory() []Boundary {
	return []Boundary{
		{Name: "cli contract", Current: fmt.Sprint(app.PublicCLIContractVersion), Reads: "1", Writes: "1", OnUnsupported: "unknown command or output version rejected", Owner: "internal/app"},
		{Name: "config document", Current: fmt.Sprint(config.SchemaVersion), Reads: "1", Writes: "1", OnUnsupported: "newer schema rejected before use", Owner: "internal/config"},
		{Name: "config effective output", Current: fmt.Sprint(config.EffectiveOutputVersion), Reads: "1", Writes: "1", OnUnsupported: "unknown output version rejected by consumers", Owner: "internal/config"},
		{Name: "workspace document", Current: fmt.Sprint(workspace.SchemaVersion), Reads: "1-2", Writes: "2", OnUnsupported: "newer schema rejected before write", Owner: "internal/workspace"},
		{Name: "sqlite state", Current: fmt.Sprint(migration.SchemaHead), Reads: "1-3", Writes: "3", OnUnsupported: "newer head rejected before runtime write", Owner: "internal/state/migration"},
		{Name: "cache filesystem manifest", Current: fmt.Sprint(cachefs.ManifestFormat), Reads: "1", Writes: "1", OnUnsupported: "unknown format rejected before content use", Owner: "internal/cachefs"},
		{Name: "client-daemon IPC", Current: fmt.Sprintf("%d.%d", ipc.ProtocolMajor, ipc.ProtocolMinor), Reads: "1.0", Writes: "1.0", OnUnsupported: "no shared major/minor fails handshake", Owner: "internal/ipc"},
		{Name: "helper release manifest", Current: fmt.Sprint(helper.ManifestFormatVersion), Reads: "1", Writes: "release-only", OnUnsupported: "unknown header rejected before install", Owner: "internal/helper"},
		{Name: "helper wire envelope", Current: fmt.Sprint(helper.EnvelopeVersion), Reads: "1", Writes: "1", OnUnsupported: "unknown envelope rejected before dispatch", Owner: "internal/helper"},
	}
}

func SnapshotText() string {
	lines := make([]string, 0, len(Inventory()))
	for _, boundary := range Inventory() {
		lines = append(lines, fmt.Sprintf("%s | current %s | reads %s | writes %s | %s | %s", boundary.Name, boundary.Current, boundary.Reads, boundary.Writes, boundary.OnUnsupported, boundary.Owner))
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
	output.WriteString("\nThe SQLite `1-3` read range includes forward migration to head 3; it is not a promise that arbitrary historical or newer databases are writable. Cache catalog tables were introduced by SQLite schema 2, while cache filesystem manifests remain an independently validated format 1. Helper release-manifest writes are release-only and production distribution remains CLOSED until the protected signing/notarization/byte-binding gates pass.\n")
	return output.String()
}
