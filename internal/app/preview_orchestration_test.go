//go:build darwin || linux

package app

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/TyrantLucifer/awesome-mac-sftp/internal/cache"
	"github.com/TyrantLucifer/awesome-mac-sftp/internal/daemon"
	"github.com/TyrantLucifer/awesome-mac-sftp/internal/domain"
	"github.com/TyrantLucifer/awesome-mac-sftp/internal/externalpreviewer"
	"github.com/TyrantLucifer/awesome-mac-sftp/internal/externalprocess"
	"github.com/TyrantLucifer/awesome-mac-sftp/internal/ipc"
	builtinpreview "github.com/TyrantLucifer/awesome-mac-sftp/internal/preview"
	"github.com/TyrantLucifer/awesome-mac-sftp/internal/tui"
)

func TestPreviewMaterializerBindsAndReleasesExactPreviewLease(t *testing.T) {
	version := "v1"
	location := domain.Location{EndpointID: "ep_bbbbbbbbbbbbbbbbbbbbbbbbbb", Path: "/image.png"}
	fingerprint := domain.Fingerprint{VersionID: &version}
	source, err := builtinpreview.FreezeSource(location, fingerprint)
	if err != nil {
		t.Fatal(err)
	}
	fixture := &previewMaterializeFixture{fingerprint: fingerprint}
	materialize := previewMaterializer(fixture, location, source, true, 68, "workspace", cache.PolicyLRU, "preview-request")
	leased, err := materialize(context.Background(), 1024)
	if err != nil || !leased.Complete || !leased.Verified || leased.Release == nil {
		t.Fatalf("materialize = %#v, %v", leased, err)
	}
	if err := leased.Release(context.Background()); err != nil {
		t.Fatal(err)
	}
	if fixture.materializes != 1 || fixture.releases != 1 {
		t.Fatalf("calls = materialize:%d release:%d", fixture.materializes, fixture.releases)
	}
}

func TestPreviewMaterializerRejectsKnownOversizeBeforeRPCAndReleasesMismatch(t *testing.T) {
	version := "v1"
	location := domain.Location{EndpointID: "ep_bbbbbbbbbbbbbbbbbbbbbbbbbb", Path: "/image.png"}
	fingerprint := domain.Fingerprint{VersionID: &version}
	source, _ := builtinpreview.FreezeSource(location, fingerprint)
	fixture := &previewMaterializeFixture{fingerprint: fingerprint}
	if _, err := previewMaterializer(fixture, location, source, true, 2048, "workspace", cache.PolicyLRU, "preview-request")(context.Background(), 1024); err == nil || fixture.materializes != 0 {
		t.Fatalf("oversize was materialized: calls=%d err=%v", fixture.materializes, err)
	}
	other := "v2"
	fixture.fingerprint.VersionID = &other
	leased, err := previewMaterializer(fixture, location, source, true, 68, "workspace", cache.PolicyLRU, "preview-request")(context.Background(), 1024)
	if err == nil || leased.Release == nil {
		t.Fatalf("fingerprint mismatch = %#v, %v", leased, err)
	}
	if err := leased.Release(context.Background()); err != nil {
		t.Fatal(err)
	}
	if fixture.releases != 1 {
		t.Fatalf("mismatch releases = %d", fixture.releases)
	}
}

func TestPreviewLocationRunsConfiguredExternalFallbackUnderAReleasedCacheLease(t *testing.T) {
	directory := t.TempDir()
	materialization := filepath.Join(directory, "content")
	data := []byte{0, 1, 2, 3}
	if err := os.WriteFile(materialization, data, 0o600); err != nil {
		t.Fatal(err)
	}
	materialization, err := filepath.EvalSymlinks(materialization)
	if err != nil {
		t.Fatal(err)
	}
	location := domain.Location{EndpointID: "ep_bbbbbbbbbbbbbbbbbbbbbbbbbb", Path: "/file.bin"}
	version := "v1"
	fingerprint := domain.Fingerprint{VersionID: &version}
	fixture := &previewLocationFixture{location: location, fingerprint: fingerprint, data: data, materialization: materialization}
	resolved, err := externalprocess.ResolveCommand(externalprocess.Command{Executable: "/usr/bin/true"}, "")
	if err != nil {
		t.Fatal(err)
	}
	runner, err := externalpreviewer.New([]externalpreviewer.Rule{{
		Name: "binary", Match: externalpreviewer.Match{Extensions: []string{".bin"}}, Command: resolved,
		Timeout: time.Second, MaxInputBytes: 1024, RequireComplete: true,
	}}, os.Environ())
	if err != nil {
		t.Fatal(err)
	}
	actions := make(chan tui.Action, 4)
	identity := tui.PreviewRequestIdentity{RequestID: "req_aaaaaaaaaaaaaaaaaaaaaaaaaa", Mode: builtinpreview.ReadHead, UIGeneration: 7}
	previewLocation(context.Background(), fixture, identity, builtinpreview.ViewAuto, location, "workspace", cache.PolicyLRU, runner, builtinpreview.ImageCapabilityProof{}, actions)
	if begin, ok := (<-actions).(tui.BeginPreview); !ok || begin.Generation != 7 {
		t.Fatalf("begin = %#v", begin)
	}
	chunk, ok := (<-actions).(tui.PreviewChunk)
	if !ok || chunk.Kind != string(builtinpreview.KindBinary) || !strings.Contains(chunk.Summary, "external previewer binary completed") {
		t.Fatalf("chunk = %#v", chunk)
	}
	if fixture.materializes != 1 || fixture.releases != 1 {
		t.Fatalf("cache calls = materialize:%d release:%d", fixture.materializes, fixture.releases)
	}
}

type previewMaterializeFixture struct {
	fingerprint  domain.Fingerprint
	materializes int
	releases     int
}

type previewLocationFixture struct {
	location        domain.Location
	fingerprint     domain.Fingerprint
	data            []byte
	materialization string
	materializes    int
	releases        int
}

func (fixture *previewLocationFixture) Call(_ context.Context, route string, request any, response any) error {
	size := uint64(len(fixture.data))
	switch route {
	case daemon.ProviderStat:
		response.(*ipc.ProviderStatResponse).Entry = ipc.EncodeEntry(domain.Entry{
			Location: fixture.location, Name: "file.bin", Kind: domain.EntryFile,
			Metadata: domain.Metadata{Size: &size}, Fingerprint: fixture.fingerprint,
		})
		return nil
	case daemon.ProviderRead:
		got := request.(ipc.ProviderReadRequest)
		if got.Location != ipc.EncodeLocation(fixture.location) || got.Offset != 0 {
			return errors.New("invalid preview read")
		}
		*response.(*ipc.ProviderReadResponse) = ipc.ProviderReadResponse{
			Info: ipc.ReadInfoWire{Fingerprint: ipc.EncodeFingerprint(fixture.fingerprint)},
			Data: ipc.EncodeWireBytes(fixture.data), EOF: true,
		}
		return nil
	case daemon.CacheMaterialize:
		got := request.(daemon.CacheMaterializeRequest)
		if got.OwnerKind != cache.LeaseOwnerPreview || got.OwnerID != "req_aaaaaaaaaaaaaaaaaaaaaaaaaa" {
			return errors.New("invalid fallback lease owner")
		}
		fixture.materializes++
		*response.(*daemon.CacheMaterializeResponse) = daemon.CacheMaterializeResponse{
			EntryID: "1111111111111111111111111111111111111111111111111111111111111111", MaterializationID: "22222222222222222222222222222222",
			ReferenceID: "33333333333333333333333333333333", LeaseID: "44444444444444444444444444444444",
			Path: fixture.materialization, SourceFingerprint: ipc.EncodeFingerprint(fixture.fingerprint),
		}
		return nil
	case daemon.CacheReleaseHandoff:
		fixture.releases++
		response.(*daemon.CacheReleaseHandoffResponse).Released = true
		return nil
	default:
		return errors.New("unexpected route")
	}
}

func (fixture *previewMaterializeFixture) Call(_ context.Context, route string, request any, response any) error {
	switch route {
	case daemon.CacheMaterialize:
		got := request.(daemon.CacheMaterializeRequest)
		if got.OwnerKind != cache.LeaseOwnerPreview || got.Process == nil {
			return errors.New("invalid preview materialize owner")
		}
		fixture.materializes++
		*response.(*daemon.CacheMaterializeResponse) = daemon.CacheMaterializeResponse{
			EntryID: "1111111111111111111111111111111111111111111111111111111111111111", MaterializationID: "22222222222222222222222222222222",
			ReferenceID: "33333333333333333333333333333333", LeaseID: "44444444444444444444444444444444",
			Path: "/private/cache/content", SourceFingerprint: ipc.EncodeFingerprint(fixture.fingerprint),
		}
		return nil
	case daemon.CacheReleaseHandoff:
		got := request.(daemon.CacheReleaseHandoffRequest)
		if got.OwnerKind != cache.LeaseOwnerPreview || got.OwnerID != "preview-request" {
			return errors.New("invalid preview release owner")
		}
		fixture.releases++
		response.(*daemon.CacheReleaseHandoffResponse).Released = true
		return nil
	default:
		return errors.New("unexpected route")
	}
}
