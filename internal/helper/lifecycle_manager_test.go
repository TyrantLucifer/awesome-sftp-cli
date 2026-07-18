package helper

import (
	"context"
	"errors"
	"path/filepath"
	"testing"

	"github.com/TyrantLucifer/awesome-mac-sftp/internal/testkit"
)

type fixtureLifecycleRelease struct {
	manifest     Manifest
	rawManifest  []byte
	rawSignature []byte
	artifact     ArtifactOpener
}

func (release fixtureLifecycleRelease) VerifiedManifest() Manifest { return release.manifest }

func (release fixtureLifecycleRelease) BindInstallRequest(request InstallRequest, verifier Verifier, policy Policy) (InstallRequest, error) {
	request.RawManifest = append([]byte(nil), release.rawManifest...)
	request.RawSignature = append([]byte(nil), release.rawSignature...)
	request.Verifier = verifier
	request.Policy = policy
	request.Artifact = release.artifact
	request.ArtifactSource = "fixture://canonical-release"
	return request, nil
}

func TestLifecycleManagerRejectsReleaseBeforeEndpointStateOrRemoteOpen(t *testing.T) {
	store, err := NewStateStore(filepath.Join(testkit.PersistentTempDir(t), "helper-state"))
	if err != nil {
		t.Fatal(err)
	}
	remoteCalls := 0
	resolveCalls := 0
	manager, err := NewLifecycleManager(LifecycleManagerConfig{
		Version: "1.0.0", Target: Target{OS: "linux", Arch: "amd64"}, State: store,
		Verifier: NewProductionVerifier(), Policy: NewProductionPolicy(), Leaser: allowHelperRemoval{},
		ResolveRelease: func(context.Context, string, Target, Verifier, Policy) (LifecycleRelease, error) {
			resolveCalls++
			return nil, errors.New("production trust is closed")
		},
		OpenRemote: func(context.Context, string) (LifecycleRemoteLease, error) {
			remoteCalls++
			return LifecycleRemoteLease{}, errors.New("must not open")
		},
		Consent: func(LifecycleRequest) InstallConsent { return nil },
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := manager.Execute(context.Background(), LifecycleRequest{
		Command: LifecycleInstall, HostAlias: "production-sftp", AcceptSharedSessionStableHome: true,
	}); err == nil {
		t.Fatal("closed production release trust was accepted")
	}
	if resolveCalls != 0 || remoteCalls != 0 {
		t.Fatalf("closed trust performed resolve/remote work = %d/%d", resolveCalls, remoteCalls)
	}
	if _, exists, err := store.LookupEndpoint("production-sftp"); err != nil || exists {
		t.Fatalf("failed release created endpoint mapping: %t, %v", exists, err)
	}
}

func TestLifecycleManagerOwnsInstallDisableAndRemoteLeaseOrdering(t *testing.T) {
	fixture := newInstallerFixture(t)
	store, err := NewStateStore(filepath.Join(testkit.PersistentTempDir(t), "helper-state"))
	if err != nil {
		t.Fatal(err)
	}
	closed := false
	handshakeWhileOpen := false
	manager := newFixtureLifecycleManager(t, fixture, store, func() { closed = true }, func() {
		handshakeWhileOpen = !closed
	})
	result, err := manager.Execute(context.Background(), LifecycleRequest{
		Command: LifecycleInstall, HostAlias: "production-sftp", AcceptSharedSessionStableHome: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.State != LifecycleStateEnabled || result.EndpointID == "" || !handshakeWhileOpen || !closed {
		t.Fatalf("install result/order = %#v, handshake while open %t, closed %t", result, handshakeWhileOpen, closed)
	}
	record, err := store.LoadEnabled(result.EndpointID, fixture.manifest.ProtocolMajor, fixture.manifest.Target())
	if err != nil || record.ArtifactID != fixture.manifest.ArtifactID() {
		t.Fatalf("enabled record = %#v, %v", record, err)
	}

	closed = false
	result, err = manager.Execute(context.Background(), LifecycleRequest{Command: LifecycleDisable, HostAlias: "production-sftp"})
	if err != nil {
		t.Fatal(err)
	}
	if result.State != LifecycleStateDisabled || closed {
		t.Fatalf("disable result/remote close = %#v/%t", result, closed)
	}
	if _, err := store.LoadEnabled(result.EndpointID, fixture.manifest.ProtocolMajor, fixture.manifest.Target()); err == nil {
		t.Fatal("disabled Helper remained enabled")
	}
}

func TestLifecycleManagerRecoversExactPendingRemovalAfterRestart(t *testing.T) {
	fixture := newInstallerFixture(t)
	store, err := NewStateStore(filepath.Join(testkit.PersistentTempDir(t), "helper-state"))
	if err != nil {
		t.Fatal(err)
	}
	manager := newFixtureLifecycleManager(t, fixture, store, func() {}, func() {})
	installed, err := manager.Execute(context.Background(), LifecycleRequest{
		Command: LifecycleInstall, HostAlias: "production-sftp", AcceptSharedSessionStableHome: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	claim, err := store.BeginRemoval(installed.EndpointID, fixture.manifest.ArtifactID())
	if err != nil {
		t.Fatal(err)
	}
	if _, exists := fixture.remote.files[claim.FinalPath]; !exists {
		t.Fatal("fixture artifact disappeared before recovery")
	}
	fixture.remote.probeResults = []Observation{{UID: 1001, Home: "/home/alice", Target: fixture.manifest.Target()}}
	closed := false
	restarted := newFixtureLifecycleManager(t, fixture, store, func() { closed = true }, func() {})
	if err := restarted.Recover(context.Background()); err != nil {
		t.Fatal(err)
	}
	if !closed {
		t.Fatal("recovery did not close its independently owned remote lease")
	}
	if _, exists := fixture.remote.files[claim.FinalPath]; exists {
		t.Fatal("exact claimed Helper artifact survived recovery")
	}
	if _, exists, err := store.PendingRemoval(); err != nil || exists {
		t.Fatalf("pending removal after recovery = %t, %v", exists, err)
	}
}

func newFixtureLifecycleManager(t *testing.T, fixture *installerFixture, store *StateStore, closeRemote func(), onHandshake func()) *LifecycleManager {
	t.Helper()
	release := fixtureLifecycleRelease{
		manifest: fixture.manifest, rawManifest: fixture.request.RawManifest,
		rawSignature: fixture.request.RawSignature, artifact: fixture.request.Artifact,
	}
	manager, err := NewLifecycleManager(LifecycleManagerConfig{
		Version: fixture.manifest.Version.String(), Target: fixture.manifest.Target(), State: store,
		Verifier: fixture.request.Verifier, Policy: fixture.request.Policy, Leaser: allowHelperRemoval{},
		ResolveRelease: func(context.Context, string, Target, Verifier, Policy) (LifecycleRelease, error) {
			return release, nil
		},
		OpenRemote: func(context.Context, string) (LifecycleRemoteLease, error) {
			return LifecycleRemoteLease{
				Remote: fixture.remote,
				Handshake: func(ctx context.Context, finalPath string, manifest Manifest) error {
					onHandshake()
					return fixture.request.Handshake(ctx, finalPath, manifest)
				},
				Close: func() error { closeRemote(); return nil },
			}, nil
		},
		Consent: func(LifecycleRequest) InstallConsent { return fixture.request.Consent },
	})
	if err != nil {
		t.Fatal(err)
	}
	return manager
}
