package helper

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"regexp"
	"strings"
	"testing"

	"github.com/TyrantLucifer/awesome-mac-sftp/internal/domain"
)

func TestInstallerRequiresTwoBoundConsentsAndPublishesWithoutReplacement(t *testing.T) {
	fixture := newInstallerFixture(t)
	result, err := fixture.installer.Install(context.Background(), fixture.request)
	if err != nil {
		t.Fatal(err)
	}
	if result.FinalPath == "" || result.Decision != HighWaterInstall || !result.Enabled {
		t.Fatalf("result = %#v", result)
	}
	if fixture.consent.probesAtPreliminary != 0 {
		t.Fatalf("probe occurred before preliminary consent: %d", fixture.consent.probesAtPreliminary)
	}
	if fixture.consent.mutationsAtFinal != 0 {
		t.Fatalf("remote mutation occurred before final consent: %d", fixture.consent.mutationsAtFinal)
	}
	if fixture.remote.publishReplace {
		t.Fatal("installer requested replacement publication")
	}
	wantOrder := []string{"open_exclusive", "handle_chmod:0600", "handle_stat", "path_lstat", "write", "handle_chmod:0700", "publish_no_replace", "handshake"}
	if !orderedSubsequence(fixture.remote.events, wantOrder) {
		t.Fatalf("events = %#v, want ordered %#v", fixture.remote.events, wantOrder)
	}
	if !regexp.MustCompile(`/\.amsftp\.tmp-[0-9a-f]{32}$`).MatchString(fixture.remote.openedTemp) || len(pathBase(fixture.remote.openedTemp)) != 44 {
		t.Fatalf("temp path = %q", fixture.remote.openedTemp)
	}
	if !bytes.Equal(fixture.remote.files[result.FinalPath], fixture.artifact) {
		t.Fatal("published bytes differ from signed fixture")
	}
	if decision, err := fixture.highWater.Check(fixture.request.EndpointID, fixture.manifest, false); err != nil || decision != HighWaterNoop {
		t.Fatalf("high-water after handshake = %q, %v", decision, err)
	}
}

func TestInstallerPreliminaryRejectionPerformsZeroProbeOrInstallMutation(t *testing.T) {
	fixture := newInstallerFixture(t)
	fixture.consent.preliminary = PreliminaryApproval{}
	_, err := fixture.installer.Install(context.Background(), fixture.request)
	if !errors.Is(err, ErrConsentDeclined) {
		t.Fatalf("error = %v", err)
	}
	if fixture.remote.probes != 0 || fixture.remote.mutations != 0 || fixture.remote.contentWrites != 0 {
		t.Fatalf("side effects: probes=%d mutations=%d writes=%d", fixture.remote.probes, fixture.remote.mutations, fixture.remote.contentWrites)
	}
	if fixture.consent.preliminaryView.ObservedUID != nil || fixture.consent.preliminaryView.ActualHome != "" {
		t.Fatalf("preliminary consent claimed actual data: %#v", fixture.consent.preliminaryView)
	}
}

func TestInstallerFinalRejectionPerformsZeroAppTreeMutationOrContentWrite(t *testing.T) {
	fixture := newInstallerFixture(t)
	fixture.consent.finalApprove = false
	_, err := fixture.installer.Install(context.Background(), fixture.request)
	if !errors.Is(err, ErrConsentDeclined) {
		t.Fatalf("error = %v", err)
	}
	if fixture.remote.probes != 1 || fixture.remote.mutations != 0 || fixture.remote.contentWrites != 0 {
		t.Fatalf("side effects: probes=%d mutations=%d writes=%d", fixture.remote.probes, fixture.remote.mutations, fixture.remote.contentWrites)
	}
	if fixture.consent.finalView.Observation.UID != 1001 || fixture.consent.finalView.Observation.Home != "/home/alice" || len(fixture.consent.finalView.CreateDirectories) == 0 {
		t.Fatalf("final view does not contain actual plan: %#v", fixture.consent.finalView)
	}
}

func TestInstallerFreshProbeOrAttributeDriftRequiresNewConsentBeforeMutation(t *testing.T) {
	for _, test := range []struct {
		name   string
		mutate func(*installerFixture)
	}{
		{name: "probe", mutate: func(f *installerFixture) {
			f.remote.probeResults[1] = Observation{UID: 1001, Home: "/home/bob", Target: Target{OS: "linux", Arch: "amd64"}}
		}},
		{name: "attrs", mutate: func(f *installerFixture) {
			f.consent.afterFinal = func() { f.remote.attrs["/home/alice"].Mode = 0755 }
		}},
	} {
		t.Run(test.name, func(t *testing.T) {
			fixture := newInstallerFixture(t)
			test.mutate(fixture)
			_, err := fixture.installer.Install(context.Background(), fixture.request)
			if !errors.Is(err, ErrPlanChanged) {
				t.Fatalf("error = %v", err)
			}
			if fixture.remote.mutations != 0 || fixture.remote.contentWrites != 0 {
				t.Fatalf("drift caused mutation: mutations=%d writes=%d", fixture.remote.mutations, fixture.remote.contentWrites)
			}
		})
	}
}

func TestInstallerRejectsTargetNamespaceAndArtifactFailuresBeforeAppTreeMutation(t *testing.T) {
	for _, test := range []struct {
		name   string
		mutate func(*installerFixture)
	}{
		{name: "target mismatch", mutate: func(f *installerFixture) {
			f.remote.probeResults[0].Target.Arch = "arm64"
		}},
		{name: "namespace mismatch", mutate: func(f *installerFixture) {
			f.remote.realPath = "/srv/other"
		}},
		{name: "artifact extra byte", mutate: func(f *installerFixture) {
			f.request.Artifact = ReopenBytes(append(append([]byte(nil), f.artifact...), 'x'))
		}},
		{name: "unsafe ancestor", mutate: func(f *installerFixture) {
			f.remote.attrs["/home/alice"].Mode = 0777
		}},
	} {
		t.Run(test.name, func(t *testing.T) {
			fixture := newInstallerFixture(t)
			test.mutate(fixture)
			if _, err := fixture.installer.Install(context.Background(), fixture.request); err == nil {
				t.Fatal("Install succeeded")
			}
			if fixture.remote.mutations != 0 || fixture.remote.contentWrites != 0 {
				t.Fatalf("rejection caused mutation: mutations=%d writes=%d", fixture.remote.mutations, fixture.remote.contentWrites)
			}
		})
	}
}

func TestInstallerNeverWritesFirstByteWhenExclusiveHandleGateFails(t *testing.T) {
	fixture := newInstallerFixture(t)
	fixture.remote.failHandleStat = true
	_, err := fixture.installer.Install(context.Background(), fixture.request)
	if err == nil {
		t.Fatal("Install succeeded")
	}
	if fixture.remote.contentWrites != 0 {
		t.Fatalf("content writes = %d", fixture.remote.contentWrites)
	}
	if fixture.remote.openedTemp == "" || fixture.remote.removed != fixture.remote.openedTemp {
		t.Fatalf("cleanup was not exact: temp=%q removed=%q", fixture.remote.openedTemp, fixture.remote.removed)
	}
}

type installerFixture struct {
	installer Installer
	request   InstallRequest
	manifest  Manifest
	artifact  []byte
	remote    *fakeInstallRemote
	consent   *fakeInstallConsent
	highWater *HighWater
}

func newInstallerFixture(t *testing.T) *installerFixture {
	t.Helper()
	artifact := []byte("amsftp Stage 4 fixture only\n")
	rawManifest := manifestForArtifact(t, artifact)
	manifest, err := ParseManifestV1(rawManifest)
	if err != nil {
		t.Fatal(err)
	}
	remote := &fakeInstallRemote{
		realPath: "/home/alice",
		probeResults: []Observation{
			{UID: 1001, Home: "/home/alice", Target: Target{OS: "linux", Arch: "amd64"}},
			{UID: 1001, Home: "/home/alice", Target: Target{OS: "linux", Arch: "amd64"}},
		},
		attrs: map[string]*RemoteAttrs{
			"/usr":            {Kind: RemoteDirectory, UID: 0, Mode: 0755},
			"/usr/bin":        {Kind: RemoteDirectory, UID: 0, Mode: 0755},
			"/usr/bin/printf": {Kind: RemoteRegular, UID: 0, Mode: 0755},
			"/usr/bin/id":     {Kind: RemoteRegular, UID: 0, Mode: 0755},
			"/usr/bin/uname":  {Kind: RemoteRegular, UID: 0, Mode: 0755},
			"/home/alice":     {Kind: RemoteDirectory, UID: 1001, Mode: 0700},
		},
		files: make(map[string][]byte),
	}
	consent := &fakeInstallConsent{
		preliminary:  PreliminaryApproval{Approved: true, SharedSessionStableHome: true},
		finalApprove: true,
		remote:       remote,
	}
	highWater := NewHighWater()
	request := InstallRequest{
		EndpointID:     domain.EndpointID("ep_stage4fixture00000000000000"),
		EndpointLabel:  "fixture endpoint",
		RawManifest:    rawManifest,
		RawSignature:   fixtureSignature(t, rawManifest),
		Verifier:       newFixtureVerifier(t),
		Policy:         fixturePolicyForManifest(t, manifest),
		HighWater:      highWater,
		Consent:        consent,
		Remote:         remote,
		Artifact:       ReopenBytes(artifact),
		ArtifactSource: "testdata/nonrelease-helper-fixture.txt",
		Handshake: func(_ context.Context, finalPath string, got Manifest) error {
			remote.events = append(remote.events, "handshake")
			if got.ArtifactID() != manifest.ArtifactID() || !bytes.Equal(remote.files[finalPath], artifact) {
				return errors.New("handshake saw wrong artifact")
			}
			return nil
		},
	}
	return &installerFixture{installer: Installer{entropy: strings.NewReader("0123456789abcdef")}, request: request, manifest: manifest, artifact: artifact, remote: remote, consent: consent, highWater: highWater}
}

func fixturePolicyForManifest(t *testing.T, manifest Manifest) Policy {
	t.Helper()
	return Policy{
		clientVersion: mustVersion(t, "4.0.0"),
		floors:        map[FloorKey]Version{manifest.FloorKey(): mustVersion(t, "4.0.0")},
		revokedKeys:   make(map[string]struct{}),
		denied:        make(map[ArtifactID]struct{}),
	}
}

type fakeInstallConsent struct {
	preliminary         PreliminaryApproval
	finalApprove        bool
	preliminaryView     PreliminaryConsent
	finalView           FinalConsent
	remote              *fakeInstallRemote
	probesAtPreliminary int
	mutationsAtFinal    int
	afterFinal          func()
}

func (c *fakeInstallConsent) ApprovePreliminary(_ context.Context, view PreliminaryConsent) (PreliminaryApproval, error) {
	c.preliminaryView = view
	c.probesAtPreliminary = c.remote.probes
	return c.preliminary, nil
}

func (c *fakeInstallConsent) ApproveFinal(_ context.Context, view FinalConsent) (FinalApproval, error) {
	c.finalView = view
	c.mutationsAtFinal = c.remote.mutations
	if c.afterFinal != nil {
		c.afterFinal()
	}
	return FinalApproval{Approved: c.finalApprove, PlanDigest: view.PlanDigest}, nil
}

type fakeInstallRemote struct {
	realPath       string
	probeResults   []Observation
	attrs          map[string]*RemoteAttrs
	files          map[string][]byte
	events         []string
	probes         int
	mutations      int
	contentWrites  int
	openedTemp     string
	removed        string
	publishReplace bool
	failHandleStat bool
}

func (r *fakeInstallRemote) Probe(context.Context) (Observation, error) {
	r.probes++
	if len(r.probeResults) == 0 {
		return Observation{}, errors.New("unexpected probe")
	}
	result := r.probeResults[0]
	r.probeResults = r.probeResults[1:]
	return result, nil
}

func (r *fakeInstallRemote) RealPath(context.Context, string) (string, error) { return r.realPath, nil }

func (r *fakeInstallRemote) Lstat(_ context.Context, path string) (RemoteAttrs, error) {
	if path == r.openedTemp {
		r.events = append(r.events, "path_lstat")
	}
	attrs := r.attrs[path]
	if attrs == nil {
		return RemoteAttrs{}, ErrRemoteNotExist
	}
	return *attrs, nil
}

func (r *fakeInstallRemote) Mkdir(_ context.Context, path string, mode uint32) error {
	r.mutations++
	r.events = append(r.events, fmt.Sprintf("mkdir:%04o", mode))
	if _, exists := r.attrs[path]; exists {
		return ErrRemoteAlreadyExists
	}
	r.attrs[path] = &RemoteAttrs{Kind: RemoteDirectory, UID: 1001, Mode: mode}
	return nil
}

func (r *fakeInstallRemote) OpenExclusive(_ context.Context, path string) (RemoteWriteHandle, error) {
	r.mutations++
	r.openedTemp = path
	r.events = append(r.events, "open_exclusive")
	if _, exists := r.attrs[path]; exists {
		return nil, ErrRemoteAlreadyExists
	}
	r.attrs[path] = &RemoteAttrs{Kind: RemoteRegular, UID: 1001, Mode: 0666}
	r.files[path] = nil
	return &fakeInstallHandle{remote: r, path: path}, nil
}

func (r *fakeInstallRemote) OpenRead(_ context.Context, path string) (io.ReadCloser, error) {
	data, exists := r.files[path]
	if !exists {
		return nil, ErrRemoteNotExist
	}
	return io.NopCloser(bytes.NewReader(data)), nil
}

func (r *fakeInstallRemote) PublishNoReplace(_ context.Context, source, destination string) error {
	r.mutations++
	r.events = append(r.events, "publish_no_replace")
	if _, exists := r.attrs[destination]; exists {
		return ErrRemoteAlreadyExists
	}
	attrs := *r.attrs[source]
	r.attrs[destination] = &attrs
	r.files[destination] = append([]byte(nil), r.files[source]...)
	delete(r.attrs, source)
	delete(r.files, source)
	return nil
}

func (r *fakeInstallRemote) RemoveExact(_ context.Context, path string) error {
	r.mutations++
	r.removed = path
	delete(r.attrs, path)
	delete(r.files, path)
	return nil
}

type fakeInstallHandle struct {
	remote *fakeInstallRemote
	path   string
}

func (h *fakeInstallHandle) Chmod(_ context.Context, mode uint32) error {
	h.remote.events = append(h.remote.events, fmt.Sprintf("handle_chmod:%04o", mode))
	h.remote.attrs[h.path].Mode = mode
	return nil
}

func (h *fakeInstallHandle) Stat(context.Context) (RemoteAttrs, error) {
	h.remote.events = append(h.remote.events, "handle_stat")
	if h.remote.failHandleStat {
		return RemoteAttrs{}, errors.New("injected handle stat failure")
	}
	return *h.remote.attrs[h.path], nil
}

func (h *fakeInstallHandle) Write(_ context.Context, value []byte) (int, error) {
	h.remote.contentWrites++
	h.remote.events = append(h.remote.events, "write")
	h.remote.files[h.path] = append(h.remote.files[h.path], value...)
	h.remote.attrs[h.path].Size = uint64(len(h.remote.files[h.path]))
	return len(value), nil
}

func (h *fakeInstallHandle) Close(context.Context) error { return nil }

func orderedSubsequence(actual, expected []string) bool {
	index := 0
	for _, value := range actual {
		if index < len(expected) && value == expected[index] {
			index++
		}
	}
	return index == len(expected)
}

func pathBase(value string) string {
	index := strings.LastIndexByte(value, '/')
	if index < 0 {
		return value
	}
	return value[index+1:]
}
