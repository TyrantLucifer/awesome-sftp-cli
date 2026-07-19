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

	"github.com/TyrantLucifer/awesome-sftp-cli/internal/cache"
	"github.com/TyrantLucifer/awesome-sftp-cli/internal/config"
	"github.com/TyrantLucifer/awesome-sftp-cli/internal/daemon"
	"github.com/TyrantLucifer/awesome-sftp-cli/internal/domain"
	"github.com/TyrantLucifer/awesome-sftp-cli/internal/externalpreviewer"
	"github.com/TyrantLucifer/awesome-sftp-cli/internal/externalprocess"
	"github.com/TyrantLucifer/awesome-sftp-cli/internal/ipc"
	builtinpreview "github.com/TyrantLucifer/awesome-sftp-cli/internal/preview"
	"github.com/TyrantLucifer/awesome-sftp-cli/internal/tui"
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
	renderLimits, imageLimits := runtimePreviewLimits(config.Default().Preview)
	previewLocation(context.Background(), fixture, identity, builtinpreview.ViewAuto, location, "workspace", cache.PolicyLRU, runner, builtinpreview.ImageCapabilityProof{}, renderLimits, imageLimits, actions)
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

func TestPreviewLocationRendersObjectMetadataWithoutContentRead(t *testing.T) {
	for _, testCase := range []struct {
		name        string
		kind        domain.EntryKind
		symlink     *domain.SymlinkInfo
		fingerprint func() domain.Fingerprint
		wantTarget  string
		view        builtinpreview.ViewMode
	}{
		{name: "directory with empty fingerprint", kind: domain.EntryDirectory, fingerprint: func() domain.Fingerprint { return domain.Fingerprint{} }, view: builtinpreview.ViewAuto},
		{name: "symlink with empty fingerprint", kind: domain.EntrySymlink, symlink: &domain.SymlinkInfo{RawTarget: "../actual"}, fingerprint: func() domain.Fingerprint { return domain.Fingerprint{} }, wantTarget: "link target: ../actual", view: builtinpreview.ViewAuto},
		{name: "file with weak fingerprint", kind: domain.EntryFile, fingerprint: func() domain.Fingerprint { size := uint64(0); return domain.Fingerprint{Size: &size} }, view: builtinpreview.ViewMetadata},
		{name: "file with empty fingerprint", kind: domain.EntryFile, fingerprint: func() domain.Fingerprint { return domain.Fingerprint{} }, view: builtinpreview.ViewMetadata},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			location, _ := domain.NewLocation("ep_aaaaaaaaaaaaaaaaaaaaaaaaaa", "/srv/item")
			modified := time.Date(2026, 7, 16, 1, 2, 3, 0, time.UTC)
			mode := uint32(0o755)
			fixture := &previewMetadataFixture{entry: domain.Entry{
				Location: location, Name: "item", Kind: testCase.kind,
				Metadata:    domain.Metadata{Mode: &mode, ModifiedAt: &modified},
				Fingerprint: testCase.fingerprint(), Symlink: testCase.symlink,
			}}
			identity := tui.PreviewRequestIdentity{RequestID: "req_aaaaaaaaaaaaaaaaaaaaaaaaaa", Pane: tui.Left, UIGeneration: 11, Mode: builtinpreview.ReadHead}
			actions := make(chan tui.Action, 3)
			renderLimits, imageLimits := runtimePreviewLimits(config.Default().Preview)
			previewLocation(context.Background(), fixture, identity, testCase.view, location, "workspace", cache.PolicyLRU, nil, builtinpreview.ImageCapabilityProof{}, renderLimits, imageLimits, actions)
			begin, ok := (<-actions).(tui.BeginPreview)
			if !ok || begin.View != builtinpreview.ViewMetadata || begin.Identity.Source.Location != location {
				t.Fatalf("begin = %#v", begin)
			}
			chunk, ok := (<-actions).(tui.PreviewChunk)
			if !ok || chunk.Kind != string(builtinpreview.KindMetadata) || !strings.Contains(string(chunk.Data), "endpoint: "+string(location.EndpointID)) || !strings.Contains(string(chunk.Data), "object kind: "+string(testCase.kind)) || !strings.Contains(string(chunk.Data), testCase.wantTarget) {
				t.Fatalf("metadata chunk = %#v", chunk)
			}
			if fixture.reads != 0 || fixture.materializes != 0 {
				t.Fatalf("metadata preview read/materialized content: %#v", fixture)
			}
			model := tui.NewModel(tui.PaneState{}, tui.PaneState{})
			model, _ = tui.Reduce(model, begin)
			model, _ = tui.Reduce(model, chunk)
			if model.Preview.Identity != begin.Identity || model.Preview.Loading || model.Preview.Kind != string(builtinpreview.KindMetadata) || !strings.Contains(model.Preview.DisplayText(), "canonical path: "+string(location.Path)) {
				t.Fatalf("reduced metadata preview = %#v, text = %q", model.Preview, model.Preview.DisplayText())
			}
		})
	}
}

func TestPreviewLocationUsesConfiguredRenderLimits(t *testing.T) {
	location, _ := domain.NewLocation("ep_aaaaaaaaaaaaaaaaaaaaaaaaaa", "/srv/item")
	fixture := &previewMetadataFixture{entry: domain.Entry{
		Location: location, Name: strings.Repeat("x", 256), Kind: domain.EntryDirectory,
	}}
	identity := tui.PreviewRequestIdentity{RequestID: "req_aaaaaaaaaaaaaaaaaaaaaaaaaa", Pane: tui.Left, UIGeneration: 12, Mode: builtinpreview.ReadHead}
	actions := make(chan tui.Action, 3)
	renderLimits, imageLimits := runtimePreviewLimits(config.Default().Preview)
	renderLimits.MaxOutputBytes = 64
	previewLocation(context.Background(), fixture, identity, builtinpreview.ViewMetadata, location, "workspace", cache.PolicyLRU, nil, builtinpreview.ImageCapabilityProof{}, renderLimits, imageLimits, actions)
	<-actions
	chunk, ok := (<-actions).(tui.PreviewChunk)
	if !ok || !chunk.Truncated || len(chunk.Data) > renderLimits.MaxOutputBytes {
		t.Fatalf("configured preview chunk = %#v", chunk)
	}
}

type previewMaterializeFixture struct {
	fingerprint  domain.Fingerprint
	materializes int
	releases     int
}

type previewMetadataFixture struct {
	entry        domain.Entry
	reads        int
	materializes int
}

func (fixture *previewMetadataFixture) Call(_ context.Context, route string, _ any, response any) error {
	switch route {
	case daemon.ProviderStat:
		response.(*ipc.ProviderStatResponse).Entry = ipc.EncodeEntry(fixture.entry)
		return nil
	case daemon.ProviderRead:
		fixture.reads++
		return errors.New("metadata preview must not read content")
	case daemon.CacheMaterialize:
		fixture.materializes++
		return errors.New("metadata preview must not materialize content")
	default:
		return errors.New("unexpected route")
	}
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
