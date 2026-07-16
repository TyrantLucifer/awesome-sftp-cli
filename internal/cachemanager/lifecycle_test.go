package cachemanager

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/TyrantLucifer/awesome-mac-sftp/internal/cache"
	"github.com/TyrantLucifer/awesome-mac-sftp/internal/state/cachestore"
)

type lifecycleProcessClassifier struct{ status cache.ProcessStatus }

func (classifier lifecycleProcessClassifier) Classify(cache.ProcessIdentity) cache.ProcessStatus {
	return classifier.status
}

func TestHeartbeatHandoffRequiresExactLiveProcessAndPreservesOpenerGrace(t *testing.T) {
	manager, _ := newManager(t)
	ctx := context.Background()
	identity := cache.ProcessIdentity{PID: 42, BirthID: "birth-42"}
	manager.processes = lifecycleProcessClassifier{status: cache.ProcessMatches}
	leaseState, err := cache.NewLeaseManager(manager.clock, manager.processes, cache.DefaultLeaseExpiry, cache.DefaultOpenerGrace)
	if err != nil {
		t.Fatal(err)
	}
	manager.leaseState = leaseState

	published := publishLifecycleEntry(t, manager, cache.PolicyEphemeral, false, "heartbeat")
	handoff, err := manager.PrepareHandoff(ctx, HandoffRequest{
		EntryID: published.Entry.ID, MaterializationID: cache.MaterializationID(strings.Repeat("1", 32)),
		ReferenceID: cache.ReferenceID(strings.Repeat("2", 32)), LeaseID: cache.LeaseID(strings.Repeat("3", 32)),
		OwnerKind: cache.LeaseOwnerOpener, OwnerID: "open-owner", Process: &identity,
	})
	if err != nil {
		t.Fatal(err)
	}
	originalGrace := handoff.Lease.GraceUntil.Sub(handoff.Lease.ExpiresAt)
	manager.clock = fixedClock{now: handoff.Lease.HeartbeatAt.Add(time.Minute)}
	manager.leaseState, _ = cache.NewLeaseManager(manager.clock, manager.processes, cache.DefaultLeaseExpiry, cache.DefaultOpenerGrace)
	got, err := manager.HeartbeatHandoff(ctx, HeartbeatHandoffRequest{
		MaterializationID: handoff.Materialization.ID, LeaseID: handoff.Lease.ID,
		OwnerKind: cache.LeaseOwnerOpener, OwnerID: "open-owner", Process: identity,
	})
	if err != nil {
		t.Fatal(err)
	}
	if got.GraceUntil.Sub(got.ExpiresAt) != originalGrace || !got.HeartbeatAt.Equal(manager.now()) {
		t.Fatalf("heartbeat = %#v", got)
	}
	otherIdentity := cache.ProcessIdentity{PID: 43, BirthID: "birth-43"}
	if _, err := manager.HeartbeatHandoff(ctx, HeartbeatHandoffRequest{
		MaterializationID: handoff.Materialization.ID, LeaseID: handoff.Lease.ID,
		OwnerKind: cache.LeaseOwnerOpener, OwnerID: "open-owner", Process: otherIdentity,
	}); err == nil {
		t.Fatal("heartbeat accepted a different process identity")
	}
	if _, err := manager.HeartbeatHandoff(ctx, HeartbeatHandoffRequest{
		MaterializationID: handoff.Materialization.ID, LeaseID: handoff.Lease.ID,
		OwnerKind: cache.LeaseOwnerOpener, OwnerID: "different-owner", Process: identity,
	}); err == nil {
		t.Fatal("heartbeat accepted a different owner")
	}

	manager.processes = lifecycleProcessClassifier{status: cache.ProcessBirthMismatch}
	if _, err := manager.HeartbeatHandoff(ctx, HeartbeatHandoffRequest{
		MaterializationID: handoff.Materialization.ID, LeaseID: handoff.Lease.ID,
		OwnerKind: cache.LeaseOwnerOpener, OwnerID: "open-owner", Process: identity,
	}); err == nil {
		t.Fatal("heartbeat accepted a reused process identity")
	}
	manager.processes = lifecycleProcessClassifier{status: cache.ProcessUncertain}
	if _, err := manager.HeartbeatHandoff(ctx, HeartbeatHandoffRequest{
		MaterializationID: handoff.Materialization.ID, LeaseID: handoff.Lease.ID,
		OwnerKind: cache.LeaseOwnerOpener, OwnerID: "open-owner", Process: identity,
	}); err == nil {
		t.Fatal("heartbeat accepted an uncertain process identity")
	}
}

func TestHeartbeatHandoffAtomicallyAdoptsExactLiveLeaseAfterDaemonRestart(t *testing.T) {
	manager, _ := newManager(t)
	ctx := context.Background()
	identity := cache.ProcessIdentity{PID: 52, BirthID: "birth-52"}
	manager.processes = lifecycleProcessClassifier{status: cache.ProcessMatches}
	manager.leaseState, _ = cache.NewLeaseManager(manager.clock, manager.processes, cache.DefaultLeaseExpiry, cache.DefaultOpenerGrace)
	published := publishLifecycleEntry(t, manager, cache.PolicyEphemeral, false, "restart-heartbeat")
	handoff, err := manager.PrepareHandoff(ctx, HandoffRequest{
		EntryID: published.Entry.ID, MaterializationID: cache.MaterializationID(strings.Repeat("7", 32)),
		ReferenceID: cache.ReferenceID(strings.Repeat("8", 32)), LeaseID: cache.LeaseID(strings.Repeat("9", 32)),
		OwnerKind: cache.LeaseOwnerEditor, OwnerID: "edit-owner", Process: &identity,
	})
	if err != nil {
		t.Fatal(err)
	}
	oldDaemon := manager.daemonID
	manager.daemonID = strings.Repeat("e", 32)
	manager.clock = fixedClock{now: handoff.Lease.HeartbeatAt.Add(time.Minute)}
	manager.leaseState, _ = cache.NewLeaseManager(manager.clock, manager.processes, cache.DefaultLeaseExpiry, cache.DefaultOpenerGrace)
	renewed, err := manager.HeartbeatHandoff(ctx, HeartbeatHandoffRequest{
		MaterializationID: handoff.Materialization.ID, LeaseID: handoff.Lease.ID,
		OwnerKind: cache.LeaseOwnerEditor, OwnerID: "edit-owner", Process: identity,
	})
	if err != nil {
		t.Fatal(err)
	}
	if renewed.DaemonInstanceID != manager.daemonID || renewed.DaemonInstanceID == oldDaemon {
		t.Fatalf("lease was not adopted: %#v", renewed)
	}
	stored, err := manager.catalog.GetLease(ctx, handoff.Lease.ID)
	if err != nil || stored.DaemonInstanceID != manager.daemonID || !stored.HeartbeatAt.Equal(manager.now()) {
		t.Fatalf("stored lease = %#v, %v", stored, err)
	}
}

func TestHeartbeatHandoffRefusesRestartAdoptionWithoutExactLiveProcess(t *testing.T) {
	for _, test := range []struct {
		name          string
		storedProcess *cache.ProcessIdentity
		requested     cache.ProcessIdentity
		status        cache.ProcessStatus
	}{
		{name: "pid mismatch", storedProcess: &cache.ProcessIdentity{PID: 61, BirthID: "birth-61"}, requested: cache.ProcessIdentity{PID: 62, BirthID: "birth-62"}, status: cache.ProcessMatches},
		{name: "uncertain", storedProcess: &cache.ProcessIdentity{PID: 63, BirthID: "birth-63"}, requested: cache.ProcessIdentity{PID: 63, BirthID: "birth-63"}, status: cache.ProcessUncertain},
		{name: "processless", requested: cache.ProcessIdentity{PID: 64, BirthID: "birth-64"}, status: cache.ProcessMatches},
	} {
		t.Run(test.name, func(t *testing.T) {
			manager, _ := newManager(t)
			ctx := context.Background()
			manager.processes = lifecycleProcessClassifier{status: cache.ProcessMatches}
			manager.leaseState, _ = cache.NewLeaseManager(manager.clock, manager.processes, cache.DefaultLeaseExpiry, cache.DefaultOpenerGrace)
			published := publishLifecycleEntry(t, manager, cache.PolicyEphemeral, false, "restart-refuse-"+strings.ReplaceAll(test.name, " ", "-"))
			handoff, err := manager.PrepareHandoff(ctx, HandoffRequest{
				EntryID: published.Entry.ID, MaterializationID: cache.MaterializationID(strings.Repeat("a", 31) + string(rune('1'+len(test.name)%8))),
				ReferenceID: cache.ReferenceID(strings.Repeat("b", 31) + string(rune('1'+len(test.name)%8))), LeaseID: cache.LeaseID(strings.Repeat("c", 31) + string(rune('1'+len(test.name)%8))),
				OwnerKind: cache.LeaseOwnerEditor, OwnerID: "edit-owner", Process: test.storedProcess,
			})
			if err != nil {
				t.Fatal(err)
			}
			oldDaemon := manager.daemonID
			manager.daemonID = strings.Repeat("e", 32)
			manager.processes = lifecycleProcessClassifier{status: test.status}
			manager.leaseState, _ = cache.NewLeaseManager(manager.clock, manager.processes, cache.DefaultLeaseExpiry, cache.DefaultOpenerGrace)
			if _, err := manager.HeartbeatHandoff(ctx, HeartbeatHandoffRequest{
				MaterializationID: handoff.Materialization.ID, LeaseID: handoff.Lease.ID,
				OwnerKind: cache.LeaseOwnerEditor, OwnerID: "edit-owner", Process: test.requested,
			}); err == nil {
				t.Fatal("heartbeat adopted an ineligible lease")
			}
			stored, err := manager.catalog.GetLease(ctx, handoff.Lease.ID)
			if err != nil || stored.DaemonInstanceID != oldDaemon {
				t.Fatalf("lease daemon changed: %#v, %v", stored, err)
			}
		})
	}
}

func TestStartupLifecycleReclaimsOnlyDeadPreviewHandoffs(t *testing.T) {
	manager, _ := newManager(t)
	ctx := context.Background()
	identity := cache.ProcessIdentity{PID: 71, BirthID: "birth-71"}
	manager.processes = lifecycleProcessClassifier{status: cache.ProcessMatches}
	manager.leaseState, _ = cache.NewLeaseManager(manager.clock, manager.processes, cache.DefaultLeaseExpiry, cache.DefaultOpenerGrace)

	previewEntry := publishLifecycleEntry(t, manager, cache.PolicyEphemeral, false, "dead-preview")
	preview, err := manager.PrepareHandoff(ctx, HandoffRequest{
		EntryID: previewEntry.Entry.ID, MaterializationID: cache.MaterializationID(strings.Repeat("d", 32)),
		ReferenceID: cache.ReferenceID(strings.Repeat("e", 32)), LeaseID: cache.LeaseID(strings.Repeat("f", 32)),
		OwnerKind: cache.LeaseOwnerPreview, OwnerID: "preview-owner", Process: &identity,
	})
	if err != nil {
		t.Fatal(err)
	}
	editorEntry := publishLifecycleEntry(t, manager, cache.PolicyEphemeral, false, "dead-editor")
	editor, err := manager.PrepareHandoff(ctx, HandoffRequest{
		EntryID: editorEntry.Entry.ID, MaterializationID: cache.MaterializationID(strings.Repeat("1", 32)),
		ReferenceID: cache.ReferenceID(strings.Repeat("2", 32)), LeaseID: cache.LeaseID(strings.Repeat("3", 32)),
		OwnerKind: cache.LeaseOwnerEditor, OwnerID: "editor-owner", Process: &identity,
	})
	if err != nil {
		t.Fatal(err)
	}

	manager.clock = fixedClock{now: preview.Lease.GraceUntil.Add(time.Second)}
	manager.processes = lifecycleProcessClassifier{status: cache.ProcessGone}
	manager.leaseState, _ = cache.NewLeaseManager(manager.clock, manager.processes, cache.DefaultLeaseExpiry, cache.DefaultOpenerGrace)
	if _, err := manager.RunStartupLifecycle(ctx, StartupLifecycleRequest{MaxVisited: 100, MaxBatches: 2}); err != nil {
		t.Fatal(err)
	}
	previewLease, err := manager.catalog.GetLease(ctx, preview.Lease.ID)
	if err != nil || previewLease.State != cache.LeaseReleased {
		t.Fatalf("preview lease = %#v, %v; want released", previewLease, err)
	}
	if _, err := manager.catalog.BeginEviction(ctx, cache.MaterializationEviction(preview.Materialization.ID), manager.now()); err != nil {
		t.Fatalf("dead preview materialization remained protected: %v", err)
	}
	editorLease, err := manager.catalog.GetLease(ctx, editor.Lease.ID)
	if err != nil || editorLease.State != cache.LeaseActive {
		t.Fatalf("editor lease = %#v, %v; want retained active", editorLease, err)
	}
	if _, err := manager.catalog.BeginEviction(ctx, cache.MaterializationEviction(editor.Materialization.ID), manager.now()); !errors.Is(err, cachestore.ErrEvictionProtected) {
		t.Fatalf("dead editor materialization eviction error = %v, want protected", err)
	}
}

func TestStartupLifecycleRecoversPendingThenFailsClosedOnUnknownBytes(t *testing.T) {
	manager, files := newManager(t)
	ctx := context.Background()
	published := publishLifecycleEntry(t, manager, cache.PolicyEphemeral, false, "pending")
	claim, err := manager.catalog.BeginEviction(ctx, cache.EntryEviction(published.Entry.ID), manager.now())
	if err != nil {
		t.Fatal(err)
	}
	unknown := filepath.Join(files.Root(), "unknown-preserved")
	if err := os.WriteFile(unknown, []byte("do not delete"), 0o600); err != nil {
		t.Fatal(err)
	}
	result, err := manager.RunStartupLifecycle(ctx, StartupLifecycleRequest{MaxVisited: 100, MaxBatches: 2})
	if !errors.Is(err, ErrCacheNeedsAttention) || !result.Reconcile.NeedsAttention || len(result.Recovered) != 1 {
		t.Fatalf("result = %#v, error = %v", result, err)
	}
	if _, err := files.InspectBlob(claim.BlobID); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("pending blob still exists: %v", err)
	}
	// #nosec G304 -- unknown is a test-owned path under the isolated cache root.
	if got, err := os.ReadFile(unknown); err != nil || string(got) != "do not delete" {
		t.Fatalf("unknown bytes changed: %q, %v", got, err)
	}
}

func TestClearEligibleIsPolicyAwareAndRefusesAnyUnreconciledFilesystem(t *testing.T) {
	manager, files := newManager(t)
	ctx := context.Background()
	eph := publishLifecycleEntry(t, manager, cache.PolicyEphemeral, false, "ephemeral")
	lru := publishLifecycleEntry(t, manager, cache.PolicyLRU, false, "lru")
	pinned := publishLifecycleEntry(t, manager, cache.PolicyPinnedOffline, true, "pinned")
	protected := publishLifecycleEntry(t, manager, cache.PolicyEphemeral, false, "protected")
	identity := cache.ProcessIdentity{PID: 9, BirthID: "birth-9"}
	manager.processes = lifecycleProcessClassifier{status: cache.ProcessMatches}
	manager.leaseState, _ = cache.NewLeaseManager(manager.clock, manager.processes, cache.DefaultLeaseExpiry, cache.DefaultOpenerGrace)
	_, err := manager.PrepareHandoff(ctx, HandoffRequest{
		EntryID: protected.Entry.ID, MaterializationID: cache.MaterializationID(strings.Repeat("4", 32)),
		ReferenceID: cache.ReferenceID(strings.Repeat("5", 32)), LeaseID: cache.LeaseID(strings.Repeat("6", 32)),
		OwnerKind: cache.LeaseOwnerEditor, OwnerID: "edit-owner", Process: &identity,
	})
	if err != nil {
		t.Fatal(err)
	}
	unknown := filepath.Join(files.Root(), "unknown-preserved")
	if err := os.WriteFile(unknown, []byte("unknown"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := manager.ClearEligible(ctx, ClearRequest{Scope: ClearEphemeral, MaxTargets: 16, MaxVisited: 100}); !errors.Is(err, ErrCacheNeedsAttention) {
		t.Fatalf("clear with unknown bytes error = %v", err)
	}
	if err := os.Remove(unknown); err != nil {
		t.Fatal(err)
	}
	result, err := manager.ClearEligible(ctx, ClearRequest{Scope: ClearEphemeral, MaxTargets: 16, MaxVisited: 100})
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Deleted) != 1 || result.Deleted[0].EntryID != eph.Entry.ID || result.Protected == 0 || result.Remaining.Entries != 3 {
		t.Fatalf("clear result = %#v", result)
	}
	for _, id := range []cache.EntryID{lru.Entry.ID, pinned.Entry.ID, protected.Entry.ID} {
		if _, err := manager.catalog.GetEntry(ctx, id); err != nil {
			t.Fatalf("protected entry %s missing: %v", id, err)
		}
	}
}

func TestClearEphemeralSharedEntryPreservesTheSharedBlob(t *testing.T) {
	manager, files := newManager(t)
	ctx := context.Background()
	content := []byte("shared-content")
	publish := func(path string, policy cache.Policy) PublishResult {
		result, err := manager.PublishComplete(ctx, PublishRequest{
			Location: testLocation(t, path), SourceFingerprint: testSourceFingerprint(uint64(len(content))),
			WorkspaceID: "workspace", Policy: policy, Source: bytes.NewReader(content),
			MaxBytes: int64(len(content)), ExpectedSize: sizePointer(int64(len(content))),
		})
		if err != nil {
			t.Fatal(err)
		}
		return result
	}
	eph := publish("/shared-ephemeral", cache.PolicyEphemeral)
	lru := publish("/shared-lru", cache.PolicyLRU)
	if eph.Blob.ID != lru.Blob.ID {
		t.Fatalf("blobs were not deduplicated: %s != %s", eph.Blob.ID, lru.Blob.ID)
	}
	result, err := manager.ClearEligible(ctx, ClearRequest{Scope: ClearEphemeral, MaxTargets: 16, MaxVisited: 100})
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Deleted) != 1 || result.Deleted[0].SharedEntryReferenceID == "" {
		t.Fatalf("clear result = %#v", result)
	}
	if _, err := files.InspectBlob(lru.Blob.ID); err != nil {
		t.Fatalf("shared blob was removed: %v", err)
	}
	if _, err := manager.catalog.GetEntry(ctx, lru.Entry.ID); err != nil {
		t.Fatalf("LRU entry was removed: %v", err)
	}
}

func publishLifecycleEntry(t *testing.T, manager *Manager, policy cache.Policy, pinned bool, text string) PublishResult {
	t.Helper()
	content := []byte(text)
	result, err := manager.PublishComplete(context.Background(), PublishRequest{
		Location: testLocation(t, "/"+text), SourceFingerprint: testSourceFingerprint(uint64(len(content))),
		WorkspaceID: "workspace", Policy: policy, Pinned: pinned, Source: bytes.NewReader(content),
		MaxBytes: int64(len(content)), ExpectedSize: sizePointer(int64(len(content))),
	})
	if err != nil {
		t.Fatal(err)
	}
	return result
}
