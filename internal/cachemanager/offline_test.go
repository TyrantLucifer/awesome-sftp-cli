package cachemanager

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/TyrantLucifer/awesome-mac-sftp/internal/cache"
	"github.com/TyrantLucifer/awesome-mac-sftp/internal/domain"
)

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
