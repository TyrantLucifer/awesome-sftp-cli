package cachemanager

import (
	"bytes"
	"context"
	"errors"
	"io"
	"strings"
	"testing"
	"time"

	"github.com/TyrantLucifer/awesome-sftp-cli/internal/cache"
	"github.com/TyrantLucifer/awesome-sftp-cli/internal/domain"
)

type gatedOfflineReader struct {
	reader  io.Reader
	started chan struct{}
	release chan struct{}
	passed  bool
}

func (reader *gatedOfflineReader) Read(destination []byte) (int, error) {
	if !reader.passed {
		close(reader.started)
		<-reader.release
		reader.passed = true
	}
	return reader.reader.Read(destination)
}

func TestResolvePinnedOfflineRevalidatesDurableIdentityAfterManagerRestart(t *testing.T) {
	ctx := context.Background()
	manager, files := newManager(t)
	content := []byte("durable pinned bytes")
	location := testLocation(t, "/offline")
	published, err := manager.PublishComplete(ctx, PublishRequest{
		Location: location, SourceFingerprint: testSourceFingerprint(uint64(len(content))), WorkspaceID: "workspace",
		Policy: cache.PolicyPinnedOffline, Pinned: true, Source: bytes.NewReader(content),
		MaxBytes: int64(len(content)), ExpectedSize: sizePointer(int64(len(content))),
	})
	if err != nil {
		t.Fatal(err)
	}
	restarted, err := New(files, manager.catalog, fixedClock{now: time.Unix(1_700_000_100, 0).UTC()}, strings.Repeat("e", 32), cache.DefaultLimits())
	if err != nil {
		t.Fatal(err)
	}
	resolved, err := restarted.ResolvePinnedOffline(ctx, PinnedOfflineRequest{Location: location, WorkspaceID: "workspace"})
	if err != nil || resolved.Entry.ID != published.Entry.ID || resolved.SourceFingerprint.Strength() == domain.FingerprintWeak {
		t.Fatalf("resolved = %#v, %v", resolved, err)
	}
	if resolved.Entry.Freshness != cache.EntryUnknown {
		t.Fatalf("offline freshness = %q, want unknown", resolved.Entry.Freshness)
	}
	stored, err := restarted.catalog.GetEntry(ctx, published.Entry.ID)
	if err != nil || stored.Freshness != cache.EntryUnknown {
		t.Fatalf("durable offline freshness = %q, %v; want unknown", stored.Freshness, err)
	}
	if _, err := restarted.ResolvePinnedOffline(ctx, PinnedOfflineRequest{Location: location, WorkspaceID: "other"}); !errors.Is(err, ErrPinnedOfflineUnavailable) {
		t.Fatalf("cross-workspace lookup = %v", err)
	}
	second := []byte("replacement pinned bytes with another identity")
	if _, err := restarted.PublishComplete(ctx, PublishRequest{
		Location: location, SourceFingerprint: testSourceFingerprint(uint64(len(second))), WorkspaceID: "workspace",
		Policy: cache.PolicyPinnedOffline, Pinned: true, Source: bytes.NewReader(second),
		MaxBytes: int64(len(second)), ExpectedSize: sizePointer(int64(len(second))),
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := restarted.ResolvePinnedOffline(ctx, PinnedOfflineRequest{Location: location, WorkspaceID: "workspace"}); !errors.Is(err, ErrPinnedOfflineAmbiguous) {
		t.Fatalf("multiple path-bound fingerprints = %v, want ambiguous", err)
	}
}

func TestPinnedOfflineHandoffRejectsIdentityPublishedAfterResolution(t *testing.T) {
	ctx := context.Background()
	manager, _ := newManager(t)
	location := testLocation(t, "/offline-race")
	first := []byte("first pinned bytes")
	published, err := manager.PublishComplete(ctx, PublishRequest{
		Location: location, SourceFingerprint: testSourceFingerprint(uint64(len(first))), WorkspaceID: "workspace",
		Policy: cache.PolicyPinnedOffline, Pinned: true, Source: bytes.NewReader(first),
		MaxBytes: int64(len(first)), ExpectedSize: sizePointer(int64(len(first))),
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := manager.ResolvePinnedOffline(ctx, PinnedOfflineRequest{Location: location, WorkspaceID: "workspace"}); err != nil {
		t.Fatal(err)
	}
	second := []byte("second pinned identity")
	gate := &gatedOfflineReader{reader: bytes.NewReader(second), started: make(chan struct{}), release: make(chan struct{})}
	publishedSecond := make(chan error, 1)
	go func() {
		_, err := manager.PublishComplete(ctx, PublishRequest{
			Location: location, SourceFingerprint: testSourceFingerprint(uint64(len(second))), WorkspaceID: "workspace",
			Policy: cache.PolicyPinnedOffline, Pinned: true, Source: gate,
			MaxBytes: int64(len(second)), ExpectedSize: sizePointer(int64(len(second))),
		})
		publishedSecond <- err
	}()
	<-gate.started
	handoffResult := make(chan error, 1)
	go func() {
		_, err := manager.PrepareHandoff(ctx, HandoffRequest{
			EntryID: published.Entry.ID, MaterializationID: cache.MaterializationID(strings.Repeat("1", 32)),
			ReferenceID: cache.ReferenceID(strings.Repeat("2", 32)), LeaseID: cache.LeaseID(strings.Repeat("3", 32)),
			OwnerKind: cache.LeaseOwnerPreview, OwnerID: "offline-preview", Pinned: true, RequireUniquePinnedOffline: true,
		})
		handoffResult <- err
	}()
	select {
	case err := <-handoffResult:
		close(gate.release)
		t.Fatalf("handoff bypassed concurrent publication: %v", err)
	case <-time.After(20 * time.Millisecond):
	}
	close(gate.release)
	if err := <-publishedSecond; err != nil {
		t.Fatal(err)
	}
	err = <-handoffResult
	if !errors.Is(err, ErrPinnedOfflineAmbiguous) {
		t.Fatalf("handoff error = %v, want ambiguous", err)
	}
}
