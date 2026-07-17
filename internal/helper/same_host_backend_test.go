package helper

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/TyrantLucifer/awesome-mac-sftp/internal/domain"
	"github.com/TyrantLucifer/awesome-mac-sftp/internal/transfer"
)

func TestSameHostBackendUsesStructuredHelperRequestsAndStagesOnlyPlannerPart(t *testing.T) {
	root := t.TempDir()
	source := filepath.Join(root, "source")
	final := filepath.Join(root, "final")
	jobID := domain.JobID("job_aaaaaaaaaaaaaaaaaaaaaaaaaa")
	part := filepath.Join(root, ".final.part-"+string(jobID))
	payload := []byte("real framed same-host backend")
	if err := os.WriteFile(source, payload, 0o600); err != nil {
		t.Fatal(err)
	}
	info, err := os.Lstat(source)
	if err != nil {
		t.Fatal(err)
	}
	client, closeClient := newLocalSameHostClient(t, true)
	defer closeClient()
	endpointID := domain.EndpointID("ep_aaaaaaaaaaaaaaaaaaaaaaaaaa")
	backend, err := NewSameHostCopyBackend(EnablePlan{EndpointID: endpointID, Manifest: sameHostTestManifest()}, client, &domain.RandomGenerator{})
	if err != nil {
		t.Fatal(err)
	}
	modified := info.ModTime().UTC()
	precision := domain.TimePrecision("nanosecond")
	size := uint64(info.Size()) // #nosec G115 -- this regular test file has a non-negative in-memory payload size.
	prepare, err := backend.PrepareCopy(context.Background(), transfer.SameHostCopyPrepareRequest{
		Source:              domain.Location{EndpointID: endpointID, Path: domain.CanonicalPath(source)},
		Part:                domain.Location{EndpointID: endpointID, Path: domain.CanonicalPath(part)},
		Final:               domain.Location{EndpointID: endpointID, Path: domain.CanonicalPath(final)},
		ExpectedFingerprint: domain.Fingerprint{Size: &size, ModifiedAt: &modified, ModifiedPrecision: &precision}, MaxBytes: 1024,
	})
	if err != nil {
		t.Fatal(err)
	}
	digest := sha256.Sum256(payload)
	if prepare.SourceSHA256 != hex.EncodeToString(digest[:]) || prepare.SourceSize != uint64(len(payload)) || prepare.SourceIdentity.Size != uint64(len(payload)) {
		t.Fatalf("prepare binding = %#v", prepare)
	}
	result, err := backend.StageCopy(context.Background(), transfer.SameHostCopyStageRequest{
		Source: domain.Location{EndpointID: endpointID, Path: domain.CanonicalPath(source)},
		Part:   domain.Location{EndpointID: endpointID, Path: domain.CanonicalPath(part)},
		Final:  domain.Location{EndpointID: endpointID, Path: domain.CanonicalPath(final)},
		JobID:  jobID, Binding: prepare, MaxBytes: 1024,
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.Part.Path != domain.CanonicalPath(part) || result.SHA256 != prepare.SourceSHA256 || result.Committed {
		t.Fatalf("stage result = %#v", result)
	}
	if data, err := os.ReadFile(part); err != nil || string(data) != string(payload) { // #nosec G304 -- part is inside t.TempDir.
		t.Fatalf("part = %q, %v", data, err)
	}
	if _, err := os.Lstat(final); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("backend touched final: %v", err)
	}
}

func TestSameHostBackendFailsClosedWhenIndependentCapabilityIsRemoved(t *testing.T) {
	client, closeClient := newLocalSameHostClient(t, false)
	defer closeClient()
	endpointID := domain.EndpointID("ep_aaaaaaaaaaaaaaaaaaaaaaaaaa")
	backend, err := NewSameHostCopyBackend(EnablePlan{EndpointID: endpointID, Manifest: sameHostTestManifest()}, client, &domain.RandomGenerator{})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := backend.PrepareCopy(context.Background(), transfer.SameHostCopyPrepareRequest{
		Source: domain.Location{EndpointID: endpointID, Path: "/source"},
		Part:   domain.Location{EndpointID: endpointID, Path: "/.final.part-job_aaaaaaaaaaaaaaaaaaaaaaaaaa"},
		Final:  domain.Location{EndpointID: endpointID, Path: "/final"}, MaxBytes: 1024,
	}); err == nil {
		t.Fatal("backend accepted missing same_host_copy capability")
	}
}

func TestSameHostBackendRejectsWrongEndpointAndChangedProviderIdentity(t *testing.T) {
	root := t.TempDir()
	source := filepath.Join(root, "source")
	if err := os.WriteFile(source, []byte("same-size"), 0o600); err != nil {
		t.Fatal(err)
	}
	info, err := os.Lstat(source)
	if err != nil {
		t.Fatal(err)
	}
	client, closeClient := newLocalSameHostClient(t, true)
	defer closeClient()
	boundEndpoint := domain.EndpointID("ep_aaaaaaaaaaaaaaaaaaaaaaaaaa")
	backend, err := NewSameHostCopyBackend(EnablePlan{EndpointID: boundEndpoint, Manifest: sameHostTestManifest()}, client, &domain.RandomGenerator{})
	if err != nil {
		t.Fatal(err)
	}
	wrongEndpoint := domain.EndpointID("ep_bbbbbbbbbbbbbbbbbbbbbbbbbb")
	wrong := transfer.SameHostCopyPrepareRequest{
		Source: domain.Location{EndpointID: wrongEndpoint, Path: domain.CanonicalPath(source)},
		Part:   domain.Location{EndpointID: wrongEndpoint, Path: domain.CanonicalPath(filepath.Join(root, "part"))},
		Final:  domain.Location{EndpointID: wrongEndpoint, Path: domain.CanonicalPath(filepath.Join(root, "final"))}, MaxBytes: 1024,
	}
	if _, err := backend.PrepareCopy(context.Background(), wrong); err == nil {
		t.Fatal("backend accepted a request for another endpoint")
	}
	modified := info.ModTime().Add(-time.Second).UTC()
	precision := domain.TimePrecision("nanosecond")
	size := uint64(info.Size()) // #nosec G115 -- this regular test file has a non-negative in-memory payload size.
	changed := wrong
	changed.Source.EndpointID, changed.Part.EndpointID, changed.Final.EndpointID = boundEndpoint, boundEndpoint, boundEndpoint
	changed.ExpectedFingerprint = domain.Fingerprint{Size: &size, ModifiedAt: &modified, ModifiedPrecision: &precision}
	if _, err := backend.PrepareCopy(context.Background(), changed); err == nil {
		t.Fatal("backend rebound a changed same-size source to an older Provider identity")
	}
	actualModified := info.ModTime().UTC()
	wrongFileID := "different-file-id"
	changed.ExpectedFingerprint = domain.Fingerprint{Size: &size, ModifiedAt: &actualModified, ModifiedPrecision: &precision, FileID: &wrongFileID}
	if _, err := backend.PrepareCopy(context.Background(), changed); err == nil {
		t.Fatal("backend accepted a changed Provider file ID")
	}
	algorithm := "sha256"
	wrongHash := strings.Repeat("0", 64)
	changed.ExpectedFingerprint = domain.Fingerprint{
		Size: &size, ModifiedAt: &actualModified, ModifiedPrecision: &precision, HashAlgorithm: &algorithm, HashHex: &wrongHash,
	}
	if _, err := backend.PrepareCopy(context.Background(), changed); err == nil {
		t.Fatal("backend accepted a changed preexisting Provider digest")
	}
}

func sameHostTestManifest() Manifest {
	return Manifest{
		Raw: []byte("persisted"), ProtocolMajor: 1, Version: Version{Major: 4},
		OS: "linux", Arch: "amd64", SHA256: strings.Repeat("a", 64),
	}
}

func newLocalSameHostClient(t *testing.T, includeCopy bool) (*Client, func()) {
	t.Helper()
	serverSide, clientSide := net.Pipe()
	capabilities := []Capability{{Name: CapabilityStrongHash, Version: 1}}
	requests := []CapabilityRequest{{Name: CapabilityStrongHash, MaximumVersion: 1}}
	config := NewLocalServiceConfig(Version{Major: 4})
	if !includeCopy {
		capabilities = capabilities[:1]
		requests = requests[:1]
	} else {
		capabilities = append(capabilities, Capability{Name: CapabilitySameHostCopy, Version: 1})
		requests = append(requests, CapabilityRequest{Name: CapabilitySameHostCopy, MaximumVersion: 1})
	}
	config.Server.Capabilities = capabilities
	config.Server.MaximumConcurrent = 1
	config.MaximumRequestDuration = time.Second
	done := make(chan error, 1)
	go func() { done <- Serve(context.Background(), serverSide, serverSide, config) }()
	client, err := NewClient(context.Background(), clientSide, clientSide, ClientHello{
		MinimumProtocol: 1, MaximumProtocol: 1, MaximumFrame: MaxHelperFrameBytes, MaximumConcurrent: 1,
		ClientVersion: Version{Major: 4}, Capabilities: requests,
	})
	if err != nil {
		t.Fatal(err)
	}
	return client, func() {
		_ = client.Close()
		_ = serverSide.Close()
		_ = clientSide.Close()
		if err := <-done; err != nil && !errors.Is(err, io.ErrClosedPipe) && !errors.Is(err, net.ErrClosed) {
			t.Errorf("serve local helper: %v", err)
		}
	}
}
