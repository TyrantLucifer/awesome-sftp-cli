package releasegate

import (
	"reflect"
	"strings"
	"testing"
)

func TestREL008CatalogCoversEveryExactCandidateGate(t *testing.T) {
	want := []GateID{
		GateCurrentCI,
		GateOldstableCheck,
		GateRace,
		GateFuzz,
		GateFaultInjection,
		GateRealAuthentication,
		GateNativeCompatibility,
		GateMigrationRollback,
		GateDocumentation,
		GateScale50K,
		GateScaleMillion,
		GatePhysical100GiB,
		GateIsolatedLevel2,
		GateSoak,
		GateReproducibility,
		GateSecurityReview,
		GateSecretPollution,
	}
	requirements := RequiredGates()
	got := make([]GateID, 0, len(requirements))
	for _, requirement := range requirements {
		got = append(got, requirement.ID)
		if requirement.Kind == "" {
			t.Fatalf("gate %q has no exact evidence kind", requirement.ID)
		}
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("required gates = %#v, want %#v", got, want)
	}
}

func TestREL008CandidateEvidenceIsCompleteSuccessfulAndIdentityBound(t *testing.T) {
	record := validCandidateRecord()
	if err := ValidateCandidate(record); err != nil {
		t.Fatalf("valid candidate: %v", err)
	}

	tests := []struct {
		name string
		edit func(*Record)
		want string
	}{
		{name: "mixed commit", edit: func(record *Record) { record.Gates[0].Commit = strings.Repeat("9", 40) }, want: "candidate identity"},
		{name: "mixed tree", edit: func(record *Record) { record.Gates[0].Tree = strings.Repeat("8", 40) }, want: "candidate identity"},
		{name: "failed gate", edit: func(record *Record) { record.Gates[0].Conclusion = "failure" }, want: "completed/success"},
		{name: "missing gate", edit: func(record *Record) { record.Gates = record.Gates[1:] }, want: string(GateCurrentCI)},
		{name: "duplicate gate", edit: func(record *Record) { record.Gates = append(record.Gates, record.Gates[0]) }, want: "duplicate"},
		{name: "wrong evidence kind", edit: func(record *Record) { record.Gates[0].Kind = KindSynthetic }, want: "evidence kind"},
		{name: "premature merge identity", edit: func(record *Record) {
			record.Merge = &Identity{Commit: strings.Repeat("3", 40), Tree: strings.Repeat("4", 40)}
		}, want: "post-merge identity"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			candidate := cloneRecord(record)
			test.edit(&candidate)
			err := ValidateCandidate(candidate)
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("ValidateCandidate() error = %v, want %q", err, test.want)
			}
		})
	}
}

func TestREL008PhysicalAndIsolatedGatesRejectSyntheticSubstitutes(t *testing.T) {
	record := validCandidateRecord()
	tests := []struct {
		name string
		gate GateID
		edit func(*GateEvidence)
		want string
	}{
		{name: "physical mode", gate: GatePhysical100GiB, edit: func(gate *GateEvidence) { gate.Kind = KindSynthetic }, want: "physical"},
		{name: "physical bytes", gate: GatePhysical100GiB, edit: func(gate *GateEvidence) { gate.Metrics.Bytes = Physical100GiBBytes - 1 }, want: "100 GiB"},
		{name: "level2 processes", gate: GateIsolatedLevel2, edit: func(gate *GateEvidence) { gate.Metrics.IndependentProcesses = 1 }, want: "independent processes"},
		{name: "level2 network", gate: GateIsolatedLevel2, edit: func(gate *GateEvidence) { gate.Metrics.NetworkBoundary = false }, want: "network boundary"},
		{name: "level2 daemon bytes", gate: GateIsolatedLevel2, edit: func(gate *GateEvidence) { gate.Metrics.DaemonContentBytes = 1 }, want: "daemon content"},
		{name: "soak duration", gate: GateSoak, edit: func(gate *GateEvidence) {
			gate.Metrics.ObservedDurationSeconds = gate.Metrics.DeclaredDurationSeconds - 1
		}, want: "declared duration"},
		{name: "soak acceptance", gate: GateSoak, edit: func(gate *GateEvidence) { gate.Metrics.LeakFree = false }, want: "race/deadlock/leak"},
		{name: "independent reviews", gate: GateSecurityReview, edit: func(gate *GateEvidence) { gate.Metrics.IndependentReviews = 1 }, want: "two independent"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			candidate := cloneRecord(record)
			gate := findGate(t, &candidate, test.gate)
			test.edit(gate)
			err := ValidateCandidate(candidate)
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("ValidateCandidate() error = %v, want %q", err, test.want)
			}
		})
	}
}

func TestREL008HostedEvidenceRequiresExactPushPRAndPostMergeMainSuccess(t *testing.T) {
	record := validCandidateRecord()
	if err := ValidateCandidate(record); err != nil {
		t.Fatalf("valid candidate: %v", err)
	}
	if err := ValidateFinal(record); err == nil || !strings.Contains(err.Error(), "post-merge") {
		t.Fatalf("ValidateFinal() error = %v, want post-merge boundary", err)
	}

	merge := Identity{Commit: strings.Repeat("3", 40), Tree: strings.Repeat("4", 40)}
	record.Merge = &merge
	record.Hosted = append(record.Hosted, HostedRun{
		Scope: RunPostMergeMain, RunID: 103, HeadSHA: merge.Commit,
		Status: "completed", Conclusion: "success", JobsTotal: 24, JobsSuccess: 24,
	})
	if err := ValidateFinal(record); err != nil {
		t.Fatalf("valid final record: %v", err)
	}

	tests := []struct {
		name string
		edit func(*Record)
		want string
	}{
		{name: "push failed", edit: func(record *Record) { record.Hosted[0].Conclusion = "failure" }, want: "completed/success"},
		{name: "pr mixed SHA", edit: func(record *Record) { record.Hosted[1].HeadSHA = strings.Repeat("9", 40) }, want: "candidate commit"},
		{name: "skipped job", edit: func(record *Record) { record.Hosted[0].JobsSuccess--; record.Hosted[0].JobsSkipped = 1 }, want: "every job"},
		{name: "main mixed SHA", edit: func(record *Record) { record.Hosted[2].HeadSHA = record.Candidate.Commit }, want: "merge commit"},
		{name: "missing PR", edit: func(record *Record) { record.Hosted = append(record.Hosted[:1], record.Hosted[2:]...) }, want: string(RunCandidatePR)},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			candidate := cloneRecord(record)
			test.edit(&candidate)
			err := ValidateFinal(candidate)
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("ValidateFinal() error = %v, want %q", err, test.want)
			}
		})
	}
}

func validCandidateRecord() Record {
	candidate := Identity{Commit: strings.Repeat("1", 40), Tree: strings.Repeat("2", 40)}
	record := Record{Schema: SchemaV1, Candidate: candidate}
	for _, requirement := range RequiredGates() {
		evidence := GateEvidence{
			ID: requirement.ID, Kind: requirement.Kind, Commit: candidate.Commit, Tree: candidate.Tree,
			Status: "completed", Conclusion: "success", Reference: "evidence://" + string(requirement.ID),
		}
		switch requirement.ID {
		case GatePhysical100GiB:
			evidence.Metrics.Bytes = Physical100GiBBytes
		case GateIsolatedLevel2:
			evidence.Metrics.IndependentProcesses = 2
			evidence.Metrics.NetworkBoundary = true
		case GateSoak:
			evidence.Metrics.DeclaredDurationSeconds = 3_600
			evidence.Metrics.ObservedDurationSeconds = 3_600
			evidence.Metrics.RaceFree = true
			evidence.Metrics.DeadlockFree = true
			evidence.Metrics.LeakFree = true
		case GateSecurityReview:
			evidence.Metrics.IndependentReviews = 2
		}
		record.Gates = append(record.Gates, evidence)
	}
	record.Hosted = []HostedRun{
		{Scope: RunCandidatePush, RunID: 101, HeadSHA: candidate.Commit, Status: "completed", Conclusion: "success", JobsTotal: 24, JobsSuccess: 24},
		{Scope: RunCandidatePR, RunID: 102, HeadSHA: candidate.Commit, Status: "completed", Conclusion: "success", JobsTotal: 24, JobsSuccess: 24},
	}
	return record
}

func cloneRecord(record Record) Record {
	clone := record
	clone.Gates = append([]GateEvidence(nil), record.Gates...)
	clone.Hosted = append([]HostedRun(nil), record.Hosted...)
	if record.Merge != nil {
		merge := *record.Merge
		clone.Merge = &merge
	}
	return clone
}

func findGate(t *testing.T, record *Record, id GateID) *GateEvidence {
	t.Helper()
	for index := range record.Gates {
		if record.Gates[index].ID == id {
			return &record.Gates[index]
		}
	}
	t.Fatalf("gate %q not found", id)
	return nil
}
