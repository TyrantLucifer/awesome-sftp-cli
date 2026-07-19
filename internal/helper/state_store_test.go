package helper

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/TyrantLucifer/awesome-sftp-cli/internal/domain"
	"github.com/TyrantLucifer/awesome-sftp-cli/internal/platform"
	"github.com/TyrantLucifer/awesome-sftp-cli/internal/testkit"
)

func TestStateStorePersistsExactMetadataAndMonotonicEnabledHighWater(t *testing.T) {
	root := filepath.Join(testkit.PersistentTempDir(t), "helper-state")
	store, err := NewStateStore(root)
	if err != nil {
		t.Fatal(err)
	}
	endpointID := fixtureEndpointID(t)
	signature := fixtureSignature(t, fixtureManifest)
	if err := store.StageMetadata(endpointID, fixtureManifest, signature); err != nil {
		t.Fatal(err)
	}
	manifest, err := ParseManifestV1(fixtureManifest)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.CommitEnabled(endpointID, manifest, signature, "/home/alice/.local/lib/amsftp/helper"); err != nil {
		t.Fatal(err)
	}

	reopened, err := NewStateStore(root)
	if err != nil {
		t.Fatal(err)
	}
	record, err := reopened.LoadEnabled(endpointID, manifest.ProtocolMajor, manifest.Target())
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(record.RawManifest, fixtureManifest) || !bytes.Equal(record.RawSignature, signature) || record.FinalPath != "/home/alice/.local/lib/amsftp/helper" {
		t.Fatalf("enabled record = %#v", record)
	}
	if decision, err := reopened.Check(endpointID, manifest, false); err != nil || decision != HighWaterNoop {
		t.Fatalf("reopened high-water = %q, %v", decision, err)
	}

	downgradeRaw := []byte(strings.Replace(string(fixtureManifest), "version=4.0.0", "version=3.9.0", 1))
	downgrade, err := ParseManifestV1(downgradeRaw)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := reopened.Check(endpointID, downgrade, false); err == nil {
		t.Fatal("reopened store accepted downgrade")
	}
	republish := manifest
	republish.SHA256 = strings.Repeat("a", 64)
	if _, err := reopened.Check(endpointID, republish, false); err == nil {
		t.Fatal("reopened store accepted same-version republish")
	}

	rootInfo, err := os.Stat(root)
	if err != nil || !rootInfo.IsDir() || rootInfo.Mode().Perm() != 0o700 {
		t.Fatalf("state root mode = %v, %v", rootInfo, err)
	}
	entries, err := os.ReadDir(root)
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range entries {
		info, err := entry.Info()
		if err != nil || !info.Mode().IsRegular() || info.Mode().Perm() != 0o600 {
			t.Fatalf("state entry %q mode = %v, %v", entry.Name(), info, err)
		}
	}
}

func TestStateStoreDisableRetainsHighWaterAndFreshRemoveDeletesOnlyVerifiedArtifact(t *testing.T) {
	fixture := newInstallerFixture(t)
	store, err := NewStateStore(filepath.Join(testkit.PersistentTempDir(t), "helper-state"))
	if err != nil {
		t.Fatal(err)
	}
	fixture.request.State = store
	fixture.request.HighWater = nil
	result, err := fixture.installer.Install(context.Background(), fixture.request)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.Disable(fixture.request.EndpointID, fixture.manifest.ProtocolMajor, fixture.manifest.Target()); err != nil {
		t.Fatal(err)
	}
	if _, err := store.LoadEnabled(fixture.request.EndpointID, fixture.manifest.ProtocolMajor, fixture.manifest.Target()); err == nil {
		t.Fatal("disabled Helper remained enabled")
	}
	if decision, err := store.Check(fixture.request.EndpointID, fixture.manifest, false); err != nil || decision != HighWaterNoop {
		t.Fatalf("disable lost high-water: %q, %v", decision, err)
	}
	if err := store.CommitEnabled(fixture.request.EndpointID, fixture.manifest, fixture.request.RawSignature, result.FinalPath); err != nil {
		t.Fatal(err)
	}
	fixture.remote.probeResults = []Observation{{UID: 1001, Home: "/home/alice", Target: fixture.manifest.Target()}}
	removeRequest := EnableRequest{
		EndpointID: fixture.request.EndpointID, ProtocolMajor: fixture.manifest.ProtocolMajor, Target: fixture.manifest.Target(),
		Verifier: fixture.request.Verifier, Policy: fixture.request.Policy, State: store, Remote: fixture.remote,
	}
	if err := RemoveEnabled(context.Background(), removeRequest, denyHelperRemoval{}); err == nil {
		t.Fatal("remove ignored an active durable Helper reference")
	}
	if _, exists := fixture.remote.files[result.FinalPath]; !exists {
		t.Fatal("referenced Helper artifact was removed")
	}
	fixture.remote.probeResults = []Observation{{UID: 1001, Home: "/home/alice", Target: fixture.manifest.Target()}}
	if err := RemoveEnabled(context.Background(), EnableRequest{
		EndpointID: fixture.request.EndpointID, ProtocolMajor: fixture.manifest.ProtocolMajor, Target: fixture.manifest.Target(),
		Verifier: fixture.request.Verifier, Policy: fixture.request.Policy, State: store, Remote: fixture.remote,
	}, allowHelperRemoval{}); err != nil {
		t.Fatal(err)
	}
	if fixture.remote.removed != result.FinalPath {
		t.Fatalf("removed path = %q, want %q", fixture.remote.removed, result.FinalPath)
	}
	if _, exists := fixture.remote.files[result.FinalPath]; exists {
		t.Fatal("verified Helper artifact survived remove")
	}
	if _, err := store.LoadEnabled(fixture.request.EndpointID, fixture.manifest.ProtocolMajor, fixture.manifest.Target()); err == nil {
		t.Fatal("removed Helper remained enabled")
	}
	if decision, err := store.Check(fixture.request.EndpointID, fixture.manifest, false); err != nil || decision != HighWaterNoop {
		t.Fatalf("remove lost high-water: %q, %v", decision, err)
	}
}

type allowHelperRemoval struct{}

func (allowHelperRemoval) AcquireHelperRemoval(context.Context, domain.EndpointID, ArtifactID) (func(), error) {
	return func() {}, nil
}

type denyHelperRemoval struct{}

func (denyHelperRemoval) AcquireHelperRemoval(context.Context, domain.EndpointID, ArtifactID) (func(), error) {
	return nil, errors.New("artifact is referenced by a durable Job")
}

func TestStateStoreBoundsUnreferencedStagedMetadata(t *testing.T) {
	root := filepath.Join(testkit.PersistentTempDir(t), "helper-state")
	store, err := NewStateStore(root)
	if err != nil {
		t.Fatal(err)
	}
	for index := 0; index < maxHelperMetadataFiles; index++ {
		name := filepath.Join(root, "metadata-"+strings.Repeat("a", 60)+fmt.Sprintf("%04x", index)+".json")
		if err := os.WriteFile(name, []byte("{}"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	endpointID := fixtureEndpointID(t)
	if err := store.StageMetadata(endpointID, fixtureManifest, fixtureSignature(t, fixtureManifest)); err == nil {
		t.Fatal("metadata staging exceeded persistent file cap")
	}
}

func TestStateStoreFailsClosedOnMissingCorruptOrReplacedMetadata(t *testing.T) {
	root := filepath.Join(testkit.PersistentTempDir(t), "helper-state")
	store, err := NewStateStore(root)
	if err != nil {
		t.Fatal(err)
	}
	endpointID := fixtureEndpointID(t)
	manifest, err := ParseManifestV1(fixtureManifest)
	if err != nil {
		t.Fatal(err)
	}
	signature := fixtureSignature(t, fixtureManifest)
	if err := store.StageMetadata(endpointID, fixtureManifest, signature); err != nil {
		t.Fatal(err)
	}
	if err := store.CommitEnabled(endpointID, manifest, signature, "/home/alice/helper"); err != nil {
		t.Fatal(err)
	}
	record, err := store.LoadEnabled(endpointID, manifest.ProtocolMajor, manifest.Target())
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(record.MetadataPath); err != nil {
		t.Fatal(err)
	}
	if _, err := store.LoadEnabled(endpointID, manifest.ProtocolMajor, manifest.Target()); err == nil {
		t.Fatal("missing metadata remained enabled")
	}
	if err := os.WriteFile(record.MetadataPath, []byte("{}"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := store.LoadEnabled(endpointID, manifest.ProtocolMajor, manifest.Target()); err == nil {
		t.Fatal("corrupt metadata remained enabled")
	}
	if err := os.Remove(record.MetadataPath); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("state.json", record.MetadataPath); err != nil {
		t.Fatal(err)
	}
	if _, err := store.LoadEnabled(endpointID, manifest.ProtocolMajor, manifest.Target()); err == nil {
		t.Fatal("symlink metadata remained enabled")
	}
}

func TestStateStoreInterruptedIndexReplacePreservesPreviousEnabledRecord(t *testing.T) {
	root := filepath.Join(testkit.PersistentTempDir(t), "helper-state")
	store, err := NewStateStore(root)
	if err != nil {
		t.Fatal(err)
	}
	endpointID := fixtureEndpointID(t)
	manifest, _ := ParseManifestV1(fixtureManifest)
	signature := fixtureSignature(t, fixtureManifest)
	if err := store.StageMetadata(endpointID, fixtureManifest, signature); err != nil {
		t.Fatal(err)
	}
	if err := store.CommitEnabled(endpointID, manifest, signature, "/home/alice/helper"); err != nil {
		t.Fatal(err)
	}
	store.beforeIndexRename = func() error { return errors.New("injected interruption") }
	if err := store.CommitEnabled(endpointID, manifest, signature, "/home/alice/other"); err == nil {
		t.Fatal("interrupted commit succeeded")
	}
	store.beforeIndexRename = nil
	record, err := store.LoadEnabled(endpointID, manifest.ProtocolMajor, manifest.Target())
	if err != nil {
		t.Fatal(err)
	}
	if record.FinalPath != "/home/alice/helper" {
		t.Fatalf("interrupted replace changed record to %q", record.FinalPath)
	}
}

func TestStateStoreMigratesV1AndRetainsParallelVersionsAcrossSwitch(t *testing.T) {
	root := testkit.PersistentTempDir(t)
	if err := os.WriteFile(filepath.Join(root, "state.json"), readHistoricalHelperFixture(t, "helper-state-index-v1-stage4.json"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "metadata-68cf7ed591b19b0f5e8247734ead9b1183fa20a211ffadd8ed3238e1ac6e95cf.json"), readHistoricalHelperFixture(t, "helper-metadata-v1-stage4.json"), 0o600); err != nil {
		t.Fatal(err)
	}
	store, err := NewStateStore(root)
	if err != nil {
		t.Fatal(err)
	}
	endpointID := fixtureEndpointID(t)
	oldManifest, err := ParseManifestV1(readHistoricalHelperFixture(t, "helper-release-manifest-v1-stage4.txt"))
	if err != nil {
		t.Fatal(err)
	}
	newRaw := []byte(strings.Replace(string(fixtureManifest), "version=4.0.0", "version=5.0.0", 1))
	newManifest, err := ParseManifestV1(newRaw)
	if err != nil {
		t.Fatal(err)
	}
	newSignature := fixtureSignature(t, newRaw)
	if err := store.StageMetadata(endpointID, newRaw, newSignature); err != nil {
		t.Fatal(err)
	}
	if err := store.CommitEnabled(endpointID, newManifest, newSignature, "/home/alice/.local/lib/amsftp/helpers/p1/5.0.0/linux-amd64-new/amsftp"); err != nil {
		t.Fatal(err)
	}
	if _, err := store.LoadArtifact(endpointID, oldManifest.ArtifactID()); err != nil {
		t.Fatalf("old installed artifact disappeared after switch: %v", err)
	}
	if enabled, err := store.LoadEnabled(endpointID, 1, oldManifest.Target()); err != nil || !bytes.Equal(enabled.RawManifest, newRaw) {
		t.Fatalf("enabled selection after switch = %#v, %v", enabled, err)
	}
	if err := store.Disable(endpointID, newManifest.ProtocolMajor, newManifest.Target()); err != nil {
		t.Fatal(err)
	}
	if err := store.Activate(endpointID, newManifest.ArtifactID()); err != nil {
		t.Fatal(err)
	}
	if err := store.Activate(endpointID, oldManifest.ArtifactID()); err == nil {
		t.Fatal("persistent high-water allowed old artifact reactivation")
	}
	reopened, err := NewStateStore(root)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := reopened.LoadArtifact(endpointID, newManifest.ArtifactID()); err != nil {
		t.Fatalf("new installed artifact disappeared after restart: %v", err)
	}
	var index stateIndex
	if err := decodeBoundedStateFile(filepath.Join(root, "state.json"), &index); err != nil || index.Schema != HelperStateSchemaVersion {
		t.Fatalf("migrated schema = %d, %v", index.Schema, err)
	}
}

func TestStateStoreInterruptedSwitchKeepsPreviousEnabledArtifact(t *testing.T) {
	root := filepath.Join(testkit.PersistentTempDir(t), "helper-state")
	store, err := NewStateStore(root)
	if err != nil {
		t.Fatal(err)
	}
	endpointID := fixtureEndpointID(t)
	oldManifest, _ := ParseManifestV1(fixtureManifest)
	oldSignature := fixtureSignature(t, fixtureManifest)
	if err := store.StageMetadata(endpointID, fixtureManifest, oldSignature); err != nil {
		t.Fatal(err)
	}
	if err := store.CommitEnabled(endpointID, oldManifest, oldSignature, "/home/alice/old"); err != nil {
		t.Fatal(err)
	}
	newRaw := []byte(strings.Replace(string(fixtureManifest), "version=4.0.0", "version=5.0.0", 1))
	newManifest, _ := ParseManifestV1(newRaw)
	newSignature := fixtureSignature(t, newRaw)
	if err := store.StageMetadata(endpointID, newRaw, newSignature); err != nil {
		t.Fatal(err)
	}
	store.beforeIndexRename = func() error { return errors.New("injected switch interruption") }
	if err := store.CommitEnabled(endpointID, newManifest, newSignature, "/home/alice/new"); err == nil {
		t.Fatal("interrupted switch succeeded")
	}
	store.beforeIndexRename = nil
	reopened, err := NewStateStore(root)
	if err != nil {
		t.Fatal(err)
	}
	enabled, err := reopened.LoadEnabled(endpointID, oldManifest.ProtocolMajor, oldManifest.Target())
	if err != nil || !bytes.Equal(enabled.RawManifest, fixtureManifest) {
		t.Fatalf("enabled artifact changed across interrupted switch: %#v, %v", enabled, err)
	}
	if _, err := reopened.LoadArtifact(endpointID, newManifest.ArtifactID()); err == nil {
		t.Fatal("interrupted switch published a new installed artifact")
	}
}

func TestStateStoreRemovalClaimResumesAndBoundsOrphanMetadataCleanup(t *testing.T) {
	root := filepath.Join(testkit.PersistentTempDir(t), "helper-state")
	store, err := NewStateStore(root)
	if err != nil {
		t.Fatal(err)
	}
	endpointID := fixtureEndpointID(t)
	oldManifest, _ := ParseManifestV1(fixtureManifest)
	oldSignature := fixtureSignature(t, fixtureManifest)
	if err := store.StageMetadata(endpointID, fixtureManifest, oldSignature); err != nil {
		t.Fatal(err)
	}
	if err := store.CommitEnabled(endpointID, oldManifest, oldSignature, "/home/alice/old"); err != nil {
		t.Fatal(err)
	}
	newRaw := []byte(strings.Replace(string(fixtureManifest), "version=4.0.0", "version=5.0.0", 1))
	newManifest, _ := ParseManifestV1(newRaw)
	newSignature := fixtureSignature(t, newRaw)
	if err := store.StageMetadata(endpointID, newRaw, newSignature); err != nil {
		t.Fatal(err)
	}
	if err := store.CommitEnabled(endpointID, newManifest, newSignature, "/home/alice/new"); err != nil {
		t.Fatal(err)
	}
	claim, err := store.BeginRemoval(endpointID, oldManifest.ArtifactID())
	if err != nil || claim.ArtifactID != oldManifest.ArtifactID() || claim.FinalPath != "/home/alice/old" {
		t.Fatalf("removal claim = %#v, %v", claim, err)
	}
	reopened, err := NewStateStore(root)
	if err != nil {
		t.Fatal(err)
	}
	pending, ok, err := reopened.PendingRemoval()
	if err != nil || !ok || pending != claim {
		t.Fatalf("pending removal after restart = %#v/%t, %v", pending, ok, err)
	}
	if _, err := reopened.LoadArtifact(endpointID, oldManifest.ArtifactID()); err != nil {
		t.Fatalf("claimed artifact stopped being recoverable before remote postcondition: %v", err)
	}
	if err := reopened.CompleteRemoval(endpointID, oldManifest.ArtifactID()); err != nil {
		t.Fatal(err)
	}
	if _, err := reopened.LoadArtifact(endpointID, oldManifest.ArtifactID()); err == nil {
		t.Fatal("completed removal remained installed")
	}
	stagedRaw := []byte(strings.Replace(string(fixtureManifest), "version=4.0.0", "version=6.0.0", 1))
	stagedSignature := fixtureSignature(t, stagedRaw)
	if err := reopened.StageMetadata(endpointID, stagedRaw, stagedSignature); err != nil {
		t.Fatal(err)
	}
	stagedMetadataPath := reopened.metadataPath(helperMetadataID(endpointID, stagedRaw, stagedSignature))
	removed, err := reopened.ReconcileMetadata(1)
	if err != nil || removed != 1 {
		t.Fatalf("bounded metadata cleanup = %d, %v", removed, err)
	}
	if _, err := reopened.LoadArtifact(endpointID, newManifest.ArtifactID()); err != nil {
		t.Fatalf("cleanup removed active metadata: %v", err)
	}
	if err := platform.ValidatePrivateFile(stagedMetadataPath, platform.ValidatePersistent); err != nil {
		t.Fatalf("cleanup removed uncommitted candidate metadata: %v", err)
	}
}

func TestRemoveEnabledResponseLossLeavesDurableClaimAndRestartCompletesAbsentPostcondition(t *testing.T) {
	fixture := newInstallerFixture(t)
	root := filepath.Join(testkit.PersistentTempDir(t), "helper-state")
	store, err := NewStateStore(root)
	if err != nil {
		t.Fatal(err)
	}
	fixture.request.State = store
	fixture.request.HighWater = nil
	result, err := fixture.installer.Install(context.Background(), fixture.request)
	if err != nil {
		t.Fatal(err)
	}
	request := EnableRequest{
		EndpointID: fixture.request.EndpointID, ProtocolMajor: fixture.manifest.ProtocolMajor, Target: fixture.manifest.Target(),
		Verifier: fixture.request.Verifier, Policy: fixture.request.Policy, State: store, Remote: fixture.remote,
	}
	fixture.remote.probeResults = []Observation{{UID: 1001, Home: "/home/alice", Target: fixture.manifest.Target()}}
	fixture.remote.removeErr = errors.New("remove response lost")
	if err := RemoveEnabled(context.Background(), request, allowHelperRemoval{}); err == nil {
		t.Fatal("lost remove response was reported successful")
	}
	if _, exists := fixture.remote.files[result.FinalPath]; exists {
		t.Fatal("response-loss fixture did not remove remote artifact")
	}
	if _, ok, err := store.PendingRemoval(); err != nil || !ok {
		t.Fatalf("pending claim after response loss = %t, %v", ok, err)
	}
	reopened, err := NewStateStore(root)
	if err != nil {
		t.Fatal(err)
	}
	request.State = reopened
	fixture.remote.removeErr = nil
	if err := ResumePendingRemoval(context.Background(), request, allowHelperRemoval{}); err != nil {
		t.Fatal(err)
	}
	if _, ok, err := reopened.PendingRemoval(); err != nil || ok {
		t.Fatalf("pending claim after absent-postcondition resume = %t, %v", ok, err)
	}
	if _, err := reopened.LoadArtifact(fixture.request.EndpointID, fixture.manifest.ArtifactID()); err == nil {
		t.Fatal("resumed removal remained installed")
	}
}

func TestRemoveArtifactProtectsFrozenOldJobsAndKeepsNewArtifactEnabled(t *testing.T) {
	fixture := newInstallerFixture(t)
	store, err := NewStateStore(filepath.Join(testkit.PersistentTempDir(t), "helper-state"))
	if err != nil {
		t.Fatal(err)
	}
	fixture.request.State = store
	fixture.request.HighWater = nil
	oldResult, err := fixture.installer.Install(context.Background(), fixture.request)
	if err != nil {
		t.Fatal(err)
	}
	oldArtifact := fixture.manifest.ArtifactID()
	newArtifactBytes := []byte("amsftp Stage 6 installed upgrade fixture only\n")
	newRaw := []byte(strings.Replace(string(manifestForArtifact(t, newArtifactBytes)), "version=4.0.0", "version=5.0.0", 1))
	newManifest, err := ParseManifestV1(newRaw)
	if err != nil {
		t.Fatal(err)
	}
	fixture.remote.probeResults = append(fixture.remote.probeResults,
		Observation{UID: 1001, Home: "/home/alice", Target: newManifest.Target()},
		Observation{UID: 1001, Home: "/home/alice", Target: newManifest.Target()},
	)
	installRequest := fixture.request
	installRequest.RawManifest = newRaw
	installRequest.RawSignature = fixtureSignature(t, newRaw)
	installRequest.Policy = fixturePolicyForManifest(t, newManifest)
	installRequest.Artifact = ReopenBytes(newArtifactBytes)
	installRequest.Handshake = func(_ context.Context, finalPath string, got Manifest) error {
		if got.ArtifactID() != newManifest.ArtifactID() || !bytes.Equal(fixture.remote.files[finalPath], newArtifactBytes) {
			return errors.New("new handshake saw wrong artifact")
		}
		return nil
	}
	newResult, err := (Installer{entropy: strings.NewReader("fedcba9876543210")}).Install(context.Background(), installRequest)
	if err != nil {
		t.Fatal(err)
	}

	removeRequest := EnableRequest{
		EndpointID: fixture.request.EndpointID, ProtocolMajor: oldArtifact.ProtocolMajor, Target: Target{OS: oldArtifact.OS, Arch: oldArtifact.Arch},
		Verifier: fixture.request.Verifier, Policy: fixturePolicyForManifest(t, fixture.manifest), State: store, Remote: fixture.remote,
	}
	fixture.remote.probeResults = append(fixture.remote.probeResults, Observation{UID: 1001, Home: "/home/alice", Target: fixture.manifest.Target()})
	if err := RemoveArtifact(context.Background(), removeRequest, oldArtifact, denyHelperRemoval{}); err == nil {
		t.Fatal("old Helper artifact ignored a durable Job reference")
	}
	if _, exists := fixture.remote.files[oldResult.FinalPath]; !exists {
		t.Fatal("reference-protected old Helper artifact was removed")
	}
	fixture.remote.probeResults = append(fixture.remote.probeResults, Observation{UID: 1001, Home: "/home/alice", Target: fixture.manifest.Target()})
	if err := RemoveArtifact(context.Background(), removeRequest, oldArtifact, allowHelperRemoval{}); err != nil {
		t.Fatal(err)
	}
	if _, exists := fixture.remote.files[oldResult.FinalPath]; exists {
		t.Fatal("unreferenced old Helper artifact survived exact removal")
	}
	enabled, err := store.LoadEnabled(fixture.request.EndpointID, newManifest.ProtocolMajor, newManifest.Target())
	if err != nil || enabled.ArtifactID != newManifest.ArtifactID() || enabled.FinalPath != newResult.FinalPath {
		t.Fatalf("new Helper changed during old-artifact removal: %#v, %v", enabled, err)
	}
}

func fixtureEndpointID(t *testing.T) domain.EndpointID {
	t.Helper()
	result, err := domain.ParseEndpointID("ep_aaaaaaaaaaaaaaaaaaaaaaaaaa")
	if err != nil {
		t.Fatal(err)
	}
	return result
}
