//go:build linux

package integration

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

const physicalReleaseBytes int64 = 100 << 30

func TestPhysicalReleaseEnvironmentRequiresExplicitSafeExternalPaths(t *testing.T) {
	repoRoot := t.TempDir()
	externalRoot := t.TempDir()
	valid := physicalReleaseEnvironment{
		RepoRoot:        repoRoot,
		LabRoot:         filepath.Join(externalRoot, "lab"),
		EvidencePath:    filepath.Join(externalRoot, "evidence", "physical-100gib.json"),
		CandidateCommit: strings.Repeat("a", 40),
		CandidateTree:   strings.Repeat("b", 40),
		Bytes:           physicalReleaseBytes,
		CancelAfter:     1 << 30,
	}
	if err := validatePhysicalReleaseEnvironment(valid); err != nil {
		t.Fatalf("valid environment: %v", err)
	}

	tests := []struct {
		name   string
		mutate func(*physicalReleaseEnvironment)
		want   string
	}{
		{name: "relative lab", mutate: func(env *physicalReleaseEnvironment) { env.LabRoot = "relative" }, want: "lab root must be absolute"},
		{name: "lab inside repository", mutate: func(env *physicalReleaseEnvironment) { env.LabRoot = filepath.Join(repoRoot, "lab") }, want: "outside repository"},
		{name: "evidence inside lab", mutate: func(env *physicalReleaseEnvironment) { env.EvidencePath = filepath.Join(env.LabRoot, "evidence.json") }, want: "outside lab root"},
		{name: "wrong size", mutate: func(env *physicalReleaseEnvironment) { env.Bytes-- }, want: "exactly 100 GiB"},
		{name: "bad checkpoint", mutate: func(env *physicalReleaseEnvironment) { env.CancelAfter = env.Bytes }, want: "cancel checkpoint"},
		{name: "bad commit", mutate: func(env *physicalReleaseEnvironment) { env.CandidateCommit = "HEAD" }, want: "40 lowercase hexadecimal"},
		{name: "bad tree", mutate: func(env *physicalReleaseEnvironment) { env.CandidateTree = strings.Repeat("G", 40) }, want: "40 lowercase hexadecimal"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			candidate := valid
			test.mutate(&candidate)
			err := validatePhysicalReleaseEnvironment(candidate)
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("error = %v, want containing %q", err, test.want)
			}
		})
	}
}

func TestPhysicalAllocationRejectsSparseFiles(t *testing.T) {
	sparse := filepath.Join(t.TempDir(), "sparse.bin")
	// #nosec G304 -- destination is fixed inside this test's private TempDir.
	file, err := os.OpenFile(sparse, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	if err := file.Truncate(8 << 20); err != nil {
		_ = file.Close()
		t.Fatal(err)
	}
	if err := file.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := inspectPhysicalAllocation(sparse, 8<<20); err == nil || !strings.Contains(err.Error(), "sparse") {
		t.Fatalf("sparse allocation error = %v", err)
	}

	dense := filepath.Join(t.TempDir(), "dense.bin")
	if err := os.WriteFile(dense, make([]byte, 1<<20), 0o600); err != nil {
		t.Fatal(err)
	}
	allocation, err := inspectPhysicalAllocation(dense, 1<<20)
	if err != nil {
		t.Fatalf("dense allocation: %v", err)
	}
	if allocation.LogicalBytes != 1<<20 || allocation.PhysicalBytes < allocation.LogicalBytes {
		t.Fatalf("allocation = %#v", allocation)
	}
}

func TestPhysicalReleaseReportBindsCandidateAndRoundTripMetrics(t *testing.T) {
	report := physicalReleaseReport{
		Schema:             physicalReleaseReportSchema,
		Purpose:            physicalReleasePurposePreRC,
		CandidateCommit:    strings.Repeat("a", 40),
		CandidateTree:      strings.Repeat("b", 40),
		StartedAt:          time.Unix(1_700_000_000, 0).UTC(),
		CompletedAt:        time.Unix(1_700_000_010, 0).UTC(),
		Filesystem:         "ext4",
		BytesPerDirection:  uint64(physicalReleaseBytes),
		TotalBytes:         uint64(physicalReleaseBytes * 2),
		ResumeOffset:       1 << 30,
		UploadSHA256:       strings.Repeat("c", 64),
		DownloadSHA256:     strings.Repeat("c", 64),
		SourceAllocation:   physicalAllocation{LogicalBytes: physicalReleaseBytes, PhysicalBytes: physicalReleaseBytes},
		RemoteAllocation:   physicalAllocation{LogicalBytes: physicalReleaseBytes, PhysicalBytes: physicalReleaseBytes},
		DownloadAllocation: physicalAllocation{LogicalBytes: physicalReleaseBytes, PhysicalBytes: physicalReleaseBytes},
	}
	if err := validatePhysicalReleaseReport(report); err != nil {
		t.Fatalf("valid report: %v", err)
	}

	bad := report
	bad.DownloadSHA256 = strings.Repeat("d", 64)
	if err := validatePhysicalReleaseReport(bad); err == nil || !strings.Contains(err.Error(), "SHA-256") {
		t.Fatalf("hash mismatch error = %v", err)
	}
	bad = report
	bad.TotalBytes--
	if err := validatePhysicalReleaseReport(bad); err == nil || !strings.Contains(err.Error(), "total bytes") {
		t.Fatalf("total mismatch error = %v", err)
	}
	bad = report
	bad.RemoteAllocation.PhysicalBytes--
	if err := validatePhysicalReleaseReport(bad); err == nil || !strings.Contains(err.Error(), "remote allocation") {
		t.Fatalf("allocation mismatch error = %v", err)
	}
}

func TestReleasePhysical100GiBLocalFSSFTPRoundTrip(t *testing.T) {
	if os.Getenv("AMSFTP_RELEASE_PHYSICAL_100GIB") != "1" {
		t.Skip("set AMSFTP_RELEASE_PHYSICAL_100GIB=1 with explicit lab/evidence/candidate variables")
	}
	runPhysicalReleaseRoundTrip(t)
}
