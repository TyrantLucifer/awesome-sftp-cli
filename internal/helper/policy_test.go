package helper

import (
	"testing"

	"github.com/TyrantLucifer/awesome-sftp-cli/internal/domain"
)

func TestCurrentPolicyRejectsRevokedDeniedIncompatibleAndBelowFloor(t *testing.T) {
	manifest, err := ParseManifestV1(fixtureManifest)
	if err != nil {
		t.Fatal(err)
	}
	valid := fixturePolicy(t)
	if err := valid.Check(manifest); err != nil {
		t.Fatalf("valid policy: %v", err)
	}
	tests := map[string]func(*Policy){
		"revoked key":          func(policy *Policy) { policy.revokedKeys[manifest.KeyID] = struct{}{} },
		"denied artifact":      func(policy *Policy) { policy.denied[manifest.ArtifactID()] = struct{}{} },
		"unsupported protocol": func(policy *Policy) { delete(policy.floors, manifest.FloorKey()) },
		"below release floor":  func(policy *Policy) { policy.floors[manifest.FloorKey()] = mustVersion(t, "4.0.1") },
		"client too old":       func(policy *Policy) { policy.clientVersion = mustVersion(t, "3.9.9") },
	}
	for name, mutate := range tests {
		t.Run(name, func(t *testing.T) {
			policy := fixturePolicy(t)
			mutate(&policy)
			if err := policy.Check(manifest); err == nil {
				t.Fatal("policy accepted forbidden manifest")
			}
		})
	}
}

func TestEndpointHighWaterIsMonotonicAndRejectsSameVersionRepublish(t *testing.T) {
	manifest, err := ParseManifestV1(fixtureManifest)
	if err != nil {
		t.Fatal(err)
	}
	endpointID := domain.EndpointID("ep_jjjjjjjjjjjjjjjjjjjjjjjjjj")
	highWater := NewHighWater()
	decision, err := highWater.Check(endpointID, manifest, false)
	if err != nil || decision != HighWaterInstall {
		t.Fatalf("fresh decision = %q, %v", decision, err)
	}
	if err := highWater.Commit(endpointID, manifest); err != nil {
		t.Fatal(err)
	}
	decision, err = highWater.Check(endpointID, manifest, false)
	if err != nil || decision != HighWaterNoop {
		t.Fatalf("same decision = %q, %v", decision, err)
	}
	decision, err = highWater.Check(endpointID, manifest, true)
	if err != nil || decision != HighWaterReinstall {
		t.Fatalf("repair decision = %q, %v", decision, err)
	}
	republished := manifest
	republished.SHA256 = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	if _, err := highWater.Check(endpointID, republished, false); err == nil {
		t.Fatal("same version with different hash was accepted")
	}
	older := manifest
	older.Version = mustVersion(t, "3.9.9")
	if _, err := highWater.Check(endpointID, older, false); err == nil {
		t.Fatal("version downgrade was accepted")
	}
	newer := manifest
	newer.Version = mustVersion(t, "4.1.0")
	if decision, err := highWater.Check(endpointID, newer, false); err != nil || decision != HighWaterInstall {
		t.Fatalf("newer decision = %q, %v", decision, err)
	}
}

func fixturePolicy(t *testing.T) Policy {
	t.Helper()
	manifest, err := ParseManifestV1(fixtureManifest)
	if err != nil {
		t.Fatal(err)
	}
	return Policy{
		clientVersion: mustVersion(t, "4.0.0"),
		floors:        map[FloorKey]Version{manifest.FloorKey(): mustVersion(t, "4.0.0")},
		revokedKeys:   make(map[string]struct{}),
		denied:        make(map[ArtifactID]struct{}),
	}
}

func mustVersion(t *testing.T, value string) Version {
	t.Helper()
	version, err := parseReleaseVersion(value)
	if err != nil {
		t.Fatal(err)
	}
	return version
}
