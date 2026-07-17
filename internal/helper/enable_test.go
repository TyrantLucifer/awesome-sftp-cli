package helper

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/TyrantLucifer/awesome-mac-sftp/internal/testkit"
)

func TestPrepareEnableReloadsCurrentPolicyAndFreshRemoteStateEveryTime(t *testing.T) {
	fixture := newInstallerFixture(t)
	store, err := NewStateStore(filepath.Join(testkit.PersistentTempDir(t), "state"))
	if err != nil {
		t.Fatal(err)
	}
	fixture.request.State = store
	fixture.request.HighWater = nil
	result, err := fixture.installer.Install(context.Background(), fixture.request)
	if err != nil {
		t.Fatal(err)
	}
	fixture.remote.probeResults = []Observation{{UID: 1001, Home: "/home/alice", Target: fixture.manifest.Target()}}
	plan, err := PrepareEnable(context.Background(), EnableRequest{
		EndpointID: fixture.request.EndpointID, ProtocolMajor: fixture.manifest.ProtocolMajor, Target: fixture.manifest.Target(),
		Verifier: fixture.request.Verifier, Policy: fixture.request.Policy, State: store, Remote: fixture.remote,
	})
	if err != nil {
		t.Fatal(err)
	}
	if plan.FinalPath != result.FinalPath || plan.Manifest.ArtifactID() != fixture.manifest.ArtifactID() {
		t.Fatalf("enable plan = %#v", plan)
	}

	probes := fixture.remote.probes
	revoked := fixture.request.Policy
	revoked.revokedKeys = map[string]struct{}{fixture.manifest.KeyID: {}}
	if _, err := PrepareEnable(context.Background(), EnableRequest{
		EndpointID: fixture.request.EndpointID, ProtocolMajor: fixture.manifest.ProtocolMajor, Target: fixture.manifest.Target(),
		Verifier: fixture.request.Verifier, Policy: revoked, State: store, Remote: fixture.remote,
	}); err == nil {
		t.Fatal("current revoked-key policy accepted enabled Helper")
	}
	if fixture.remote.probes != probes {
		t.Fatal("remote probe ran before current policy rejection")
	}

	fixture.remote.probeResults = []Observation{{UID: 1001, Home: "/home/alice", Target: fixture.manifest.Target()}}
	fixture.remote.files[result.FinalPath][0] ^= 0xff
	if _, err := PrepareEnable(context.Background(), EnableRequest{
		EndpointID: fixture.request.EndpointID, ProtocolMajor: fixture.manifest.ProtocolMajor, Target: fixture.manifest.Target(),
		Verifier: fixture.request.Verifier, Policy: fixture.request.Policy, State: store, Remote: fixture.remote,
	}); err == nil {
		t.Fatal("tampered remote artifact remained enabled")
	}
}

func TestPrepareEnableFailsClosedWhenPersistentMetadataDisappears(t *testing.T) {
	fixture := newInstallerFixture(t)
	store, err := NewStateStore(filepath.Join(testkit.PersistentTempDir(t), "state"))
	if err != nil {
		t.Fatal(err)
	}
	fixture.request.State = store
	fixture.request.HighWater = nil
	if _, err := fixture.installer.Install(context.Background(), fixture.request); err != nil {
		t.Fatal(err)
	}
	record, err := store.LoadEnabled(fixture.request.EndpointID, fixture.manifest.ProtocolMajor, fixture.manifest.Target())
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(record.MetadataPath); err != nil {
		t.Fatal(err)
	}
	fixture.remote.probeResults = []Observation{{UID: 1001, Home: "/home/alice", Target: fixture.manifest.Target()}}
	if _, err := PrepareEnable(context.Background(), EnableRequest{
		EndpointID: fixture.request.EndpointID, ProtocolMajor: fixture.manifest.ProtocolMajor, Target: fixture.manifest.Target(),
		Verifier: fixture.request.Verifier, Policy: fixture.request.Policy, State: store, Remote: fixture.remote,
	}); err == nil {
		t.Fatal("missing metadata remained enabled")
	}
	if len(fixture.remote.probeResults) != 1 {
		t.Fatal("metadata failure consumed a remote probe")
	}
}

func TestValidateEnabledClientBindsProtocolVersionAndRequiredCapabilities(t *testing.T) {
	manifest, err := ParseManifestV1(fixtureManifest)
	if err != nil {
		t.Fatal(err)
	}
	client := &Client{negotiated: Negotiated{Protocol: 1, HelperVersion: "4.0.0", Capabilities: []Capability{{Name: CapabilityFilenameSearch, Version: 1}}}}
	if err := ValidateEnabledClient(EnablePlan{Manifest: manifest}, client, []CapabilityName{CapabilityFilenameSearch}); err != nil {
		t.Fatal(err)
	}
	if err := ValidateEnabledClient(EnablePlan{Manifest: manifest}, client, []CapabilityName{CapabilityContentSearch}); err == nil {
		t.Fatal("missing required capability passed enabled-client validation")
	}
	client.negotiated.HelperVersion = "4.0.1"
	if err := ValidateEnabledClient(EnablePlan{Manifest: manifest}, client, nil); err == nil {
		t.Fatal("different helper version passed enabled-client validation")
	}
}
