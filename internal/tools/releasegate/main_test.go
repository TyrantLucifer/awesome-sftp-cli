package main

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/TyrantLucifer/awesome-sftp-cli/internal/releasegate"
)

func TestREL008CLIRejectsUnknownJSONAndValidatesRequestedPhase(t *testing.T) {
	root := t.TempDir()
	unknown := filepath.Join(root, "unknown.json")
	if err := os.WriteFile(unknown, []byte(`{"schema":"amsftp-release-gate-record-v1","unknown":true}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := run([]string{"candidate", unknown}, &bytes.Buffer{}); err == nil || !strings.Contains(err.Error(), "unknown field") {
		t.Fatalf("unknown-field error = %v", err)
	}

	if err := run([]string{"unsupported", unknown}, &bytes.Buffer{}); err == nil || !strings.Contains(err.Error(), "usage") {
		t.Fatalf("unsupported-mode error = %v", err)
	}
	if err := run(nil, &bytes.Buffer{}); err == nil || !strings.Contains(err.Error(), "usage") {
		t.Fatalf("missing-argument error = %v", err)
	}

	record := validToolRecord()
	recordPath := filepath.Join(root, "candidate.json")
	writeRecord(t, recordPath, record)
	var stdout bytes.Buffer
	if err := run([]string{"candidate", recordPath}, &stdout); err != nil {
		t.Fatalf("candidate record: %v", err)
	}
	if stdout.String() != "candidate "+record.Candidate.Commit+"\n" {
		t.Fatalf("candidate stdout = %q", stdout.String())
	}
	if err := run([]string{"final", recordPath}, &bytes.Buffer{}); err == nil || !strings.Contains(err.Error(), "post-merge") {
		t.Fatalf("premature final error = %v", err)
	}

	merge := releasegate.Identity{Commit: strings.Repeat("3", 40), Tree: strings.Repeat("4", 40)}
	record.Merge = &merge
	record.Hosted = append(record.Hosted, releasegate.HostedRun{
		Scope: releasegate.RunPostMergeMain, RunID: 203, HeadSHA: merge.Commit,
		Status: "completed", Conclusion: "success", JobsTotal: 24, JobsSuccess: 24,
	})
	writeRecord(t, recordPath, record)
	if err := run([]string{"final", recordPath}, &bytes.Buffer{}); err != nil {
		t.Fatalf("final record: %v", err)
	}
}

func validToolRecord() releasegate.Record {
	candidate := releasegate.Identity{Commit: strings.Repeat("1", 40), Tree: strings.Repeat("2", 40)}
	record := releasegate.Record{Schema: releasegate.SchemaV1, Candidate: candidate}
	for _, requirement := range releasegate.RequiredGates() {
		evidence := releasegate.GateEvidence{
			ID: requirement.ID, Kind: requirement.Kind, Commit: candidate.Commit, Tree: candidate.Tree,
			Status: "completed", Conclusion: "success", Reference: "evidence://" + string(requirement.ID),
		}
		switch requirement.ID {
		case releasegate.GatePhysical100GiB:
			evidence.Metrics.Bytes = releasegate.Physical100GiBBytes
		case releasegate.GateIsolatedLevel2:
			evidence.Metrics.IndependentProcesses = 2
			evidence.Metrics.NetworkBoundary = true
		case releasegate.GateSoak:
			evidence.Metrics.DeclaredDurationSeconds = 1
			evidence.Metrics.ObservedDurationSeconds = 1
			evidence.Metrics.RaceFree = true
			evidence.Metrics.DeadlockFree = true
			evidence.Metrics.LeakFree = true
		case releasegate.GateSecurityReview:
			evidence.Metrics.IndependentReviews = 2
		}
		record.Gates = append(record.Gates, evidence)
	}
	record.Hosted = []releasegate.HostedRun{
		{Scope: releasegate.RunCandidatePush, RunID: 201, HeadSHA: candidate.Commit, Status: "completed", Conclusion: "success", JobsTotal: 24, JobsSuccess: 24},
		{Scope: releasegate.RunCandidatePR, RunID: 202, HeadSHA: candidate.Commit, Status: "completed", Conclusion: "success", JobsTotal: 24, JobsSuccess: 24},
	}
	return record
}

func writeRecord(t *testing.T, path string, record releasegate.Record) {
	t.Helper()
	raw, err := json.Marshal(record)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, raw, 0o600); err != nil {
		t.Fatal(err)
	}
}
