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

	"github.com/TyrantLucifer/awesome-mac-sftp/internal/domain"
)

func TestStateStorePersistsExactMetadataAndMonotonicEnabledHighWater(t *testing.T) {
	root := filepath.Join(t.TempDir(), "helper-state")
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
	store, err := NewStateStore(filepath.Join(t.TempDir(), "helper-state"))
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
	root := filepath.Join(t.TempDir(), "helper-state")
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
	root := filepath.Join(t.TempDir(), "helper-state")
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
	root := filepath.Join(t.TempDir(), "helper-state")
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

func fixtureEndpointID(t *testing.T) domain.EndpointID {
	t.Helper()
	result, err := domain.ParseEndpointID("ep_aaaaaaaaaaaaaaaaaaaaaaaaaa")
	if err != nil {
		t.Fatal(err)
	}
	return result
}
