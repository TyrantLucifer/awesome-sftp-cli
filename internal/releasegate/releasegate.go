package releasegate

import (
	"errors"
	"fmt"
)

const (
	SchemaV1             = "amsftp-release-gate-record-v1"
	Physical100GiBBytes  = uint64(100) << 30
	completedStatus      = "completed"
	successfulConclusion = "success"
)

type GateID string

const (
	GateCurrentCI           GateID = "current_ci"
	GateOldstableCheck      GateID = "oldstable_check"
	GateRace                GateID = "race"
	GateFuzz                GateID = "fuzz"
	GateFaultInjection      GateID = "fault_injection"
	GateRealAuthentication  GateID = "real_authentication"
	GateNativeCompatibility GateID = "native_compatibility"
	GateMigrationRollback   GateID = "migration_rollback"
	GateDocumentation       GateID = "documentation"
	GateScale50K            GateID = "scale_50k"
	GateScaleMillion        GateID = "scale_million"
	GatePhysical100GiB      GateID = "physical_100gib"
	GateIsolatedLevel2      GateID = "isolated_level2"
	GateSoak                GateID = "soak"
	GateReproducibility     GateID = "reproducibility"
	GateSecurityReview      GateID = "security_review"
	GateSecretPollution     GateID = "secret_pollution"
)

type EvidenceKind string

const (
	KindMakeCI                 EvidenceKind = "make_ci"
	KindMakeCheck              EvidenceKind = "make_check"
	KindRace                   EvidenceKind = "race"
	KindFuzz                   EvidenceKind = "fuzz"
	KindFaultInjection         EvidenceKind = "fault_injection"
	KindRealAuthentication     EvidenceKind = "real_authentication"
	KindNativeCompatibility    EvidenceKind = "native_compatibility"
	KindMigrationRollback      EvidenceKind = "migration_rollback"
	KindDocumentation          EvidenceKind = "documentation"
	KindScale                  EvidenceKind = "scale"
	KindPhysical               EvidenceKind = "physical"
	KindProcessNetworkIsolated EvidenceKind = "process_network_isolated"
	KindSoak                   EvidenceKind = "soak"
	KindReproducibility        EvidenceKind = "reproducibility"
	KindSecurityReview         EvidenceKind = "security_review"
	KindSecretPollution        EvidenceKind = "secret_pollution"
	KindSynthetic              EvidenceKind = "synthetic"
)

type Requirement struct {
	ID   GateID       `json:"id"`
	Kind EvidenceKind `json:"kind"`
}

var requiredGates = [...]Requirement{
	{ID: GateCurrentCI, Kind: KindMakeCI},
	{ID: GateOldstableCheck, Kind: KindMakeCheck},
	{ID: GateRace, Kind: KindRace},
	{ID: GateFuzz, Kind: KindFuzz},
	{ID: GateFaultInjection, Kind: KindFaultInjection},
	{ID: GateRealAuthentication, Kind: KindRealAuthentication},
	{ID: GateNativeCompatibility, Kind: KindNativeCompatibility},
	{ID: GateMigrationRollback, Kind: KindMigrationRollback},
	{ID: GateDocumentation, Kind: KindDocumentation},
	{ID: GateScale50K, Kind: KindScale},
	{ID: GateScaleMillion, Kind: KindScale},
	{ID: GatePhysical100GiB, Kind: KindPhysical},
	{ID: GateIsolatedLevel2, Kind: KindProcessNetworkIsolated},
	{ID: GateSoak, Kind: KindSoak},
	{ID: GateReproducibility, Kind: KindReproducibility},
	{ID: GateSecurityReview, Kind: KindSecurityReview},
	{ID: GateSecretPollution, Kind: KindSecretPollution},
}

type Identity struct {
	Commit string `json:"commit"`
	Tree   string `json:"tree"`
}

type Metrics struct {
	Bytes                   uint64 `json:"bytes,omitempty"`
	IndependentProcesses    uint64 `json:"independent_processes,omitempty"`
	NetworkBoundary         bool   `json:"network_boundary,omitempty"`
	DaemonContentBytes      uint64 `json:"daemon_content_bytes,omitempty"`
	IndependentReviews      uint64 `json:"independent_reviews,omitempty"`
	DeclaredDurationSeconds uint64 `json:"declared_duration_seconds,omitempty"`
	ObservedDurationSeconds uint64 `json:"observed_duration_seconds,omitempty"`
	RaceFree                bool   `json:"race_free,omitempty"`
	DeadlockFree            bool   `json:"deadlock_free,omitempty"`
	LeakFree                bool   `json:"leak_free,omitempty"`
}

type GateEvidence struct {
	ID         GateID       `json:"id"`
	Kind       EvidenceKind `json:"kind"`
	Commit     string       `json:"commit"`
	Tree       string       `json:"tree"`
	Status     string       `json:"status"`
	Conclusion string       `json:"conclusion"`
	Reference  string       `json:"reference"`
	Metrics    Metrics      `json:"metrics,omitempty"`
}

type RunScope string

const (
	RunCandidatePush RunScope = "candidate_push"
	RunCandidatePR   RunScope = "candidate_pr"
	RunPostMergeMain RunScope = "postmerge_main"
)

type HostedRun struct {
	Scope       RunScope `json:"scope"`
	RunID       uint64   `json:"run_id"`
	HeadSHA     string   `json:"head_sha"`
	Status      string   `json:"status"`
	Conclusion  string   `json:"conclusion"`
	JobsTotal   uint64   `json:"jobs_total"`
	JobsSuccess uint64   `json:"jobs_success"`
	JobsSkipped uint64   `json:"jobs_skipped"`
	JobsFailed  uint64   `json:"jobs_failed"`
}

type Record struct {
	Schema    string         `json:"schema"`
	Candidate Identity       `json:"candidate"`
	Merge     *Identity      `json:"merge,omitempty"`
	Gates     []GateEvidence `json:"gates"`
	Hosted    []HostedRun    `json:"hosted"`
}

func RequiredGates() []Requirement {
	result := make([]Requirement, len(requiredGates))
	copy(result, requiredGates[:])
	return result
}

func ValidateCandidate(record Record) error {
	return validate(record, false)
}

func ValidateFinal(record Record) error {
	return validate(record, true)
}

func validate(record Record, final bool) error {
	var findings []error
	if record.Schema != SchemaV1 {
		findings = append(findings, fmt.Errorf("release gate schema must be %q", SchemaV1))
	}
	if !validIdentity(record.Candidate) {
		findings = append(findings, errors.New("candidate identity requires exact lowercase 40-hex commit and tree"))
	}
	if !final && record.Merge != nil {
		findings = append(findings, errors.New("candidate evidence cannot claim a post-merge identity"))
	}
	findings = append(findings, validateGates(record)...)
	findings = append(findings, validateHosted(record, final)...)
	return errors.Join(findings...)
}

func validateGates(record Record) []error {
	var findings []error
	requirements := make(map[GateID]EvidenceKind, len(requiredGates))
	for _, requirement := range requiredGates {
		requirements[requirement.ID] = requirement.Kind
	}
	seen := make(map[GateID]struct{}, len(record.Gates))
	for _, gate := range record.Gates {
		expectedKind, required := requirements[gate.ID]
		if !required {
			findings = append(findings, fmt.Errorf("unknown release gate %q", gate.ID))
			continue
		}
		if _, duplicate := seen[gate.ID]; duplicate {
			findings = append(findings, fmt.Errorf("duplicate release gate %q", gate.ID))
			continue
		}
		seen[gate.ID] = struct{}{}
		if gate.Kind != expectedKind {
			findings = append(findings, fmt.Errorf("gate %q evidence kind must be %q, got %q", gate.ID, expectedKind, gate.Kind))
		}
		if gate.Commit != record.Candidate.Commit || gate.Tree != record.Candidate.Tree {
			findings = append(findings, fmt.Errorf("gate %q is not bound to the exact candidate identity", gate.ID))
		}
		if gate.Status != completedStatus || gate.Conclusion != successfulConclusion {
			findings = append(findings, fmt.Errorf("gate %q must be completed/success", gate.ID))
		}
		if gate.Reference == "" {
			findings = append(findings, fmt.Errorf("gate %q requires an evidence reference", gate.ID))
		}
		findings = append(findings, validateGateMetrics(gate)...)
	}
	for _, requirement := range requiredGates {
		if _, present := seen[requirement.ID]; !present {
			findings = append(findings, fmt.Errorf("required release gate %q is missing", requirement.ID))
		}
	}
	return findings
}

func validateGateMetrics(gate GateEvidence) []error {
	var findings []error
	switch gate.ID {
	case GatePhysical100GiB:
		if gate.Kind != KindPhysical {
			findings = append(findings, errors.New("physical 100 GiB gate cannot use synthetic evidence"))
		}
		if gate.Metrics.Bytes < Physical100GiBBytes {
			findings = append(findings, errors.New("physical 100 GiB gate must transfer at least 100 GiB"))
		}
	case GateIsolatedLevel2:
		if gate.Metrics.IndependentProcesses < 2 {
			findings = append(findings, errors.New("isolated Level 2 gate requires at least two independent processes"))
		}
		if !gate.Metrics.NetworkBoundary {
			findings = append(findings, errors.New("isolated Level 2 gate requires a real network boundary"))
		}
		if gate.Metrics.DaemonContentBytes != 0 {
			findings = append(findings, errors.New("isolated Level 2 gate requires zero daemon content bytes"))
		}
	case GateSoak:
		if gate.Metrics.DeclaredDurationSeconds == 0 || gate.Metrics.ObservedDurationSeconds < gate.Metrics.DeclaredDurationSeconds {
			findings = append(findings, errors.New("soak gate must meet its positive declared duration"))
		}
		if !gate.Metrics.RaceFree || !gate.Metrics.DeadlockFree || !gate.Metrics.LeakFree {
			findings = append(findings, errors.New("soak gate requires race/deadlock/leak acceptance"))
		}
	case GateSecurityReview:
		if gate.Metrics.IndependentReviews < 2 {
			findings = append(findings, errors.New("security review gate requires two independent exact-candidate reviews"))
		}
	}
	return findings
}

func validateHosted(record Record, final bool) []error {
	var findings []error
	expected := map[RunScope]string{
		RunCandidatePush: record.Candidate.Commit,
		RunCandidatePR:   record.Candidate.Commit,
	}
	if final {
		if record.Merge == nil || !validIdentity(*record.Merge) {
			findings = append(findings, errors.New("final evidence requires an exact post-merge identity"))
		} else {
			expected[RunPostMergeMain] = record.Merge.Commit
		}
	}
	seen := make(map[RunScope]struct{}, len(record.Hosted))
	for _, run := range record.Hosted {
		expectedSHA, required := expected[run.Scope]
		if !required {
			if run.Scope == RunPostMergeMain && !final {
				findings = append(findings, errors.New("candidate evidence cannot claim a post-merge main run"))
			} else {
				findings = append(findings, fmt.Errorf("unknown Hosted run scope %q", run.Scope))
			}
			continue
		}
		if _, duplicate := seen[run.Scope]; duplicate {
			findings = append(findings, fmt.Errorf("duplicate Hosted run scope %q", run.Scope))
			continue
		}
		seen[run.Scope] = struct{}{}
		if run.RunID == 0 {
			findings = append(findings, fmt.Errorf("hosted run %q requires a positive run ID", run.Scope))
		}
		if run.HeadSHA != expectedSHA {
			boundary := "candidate commit"
			if run.Scope == RunPostMergeMain {
				boundary = "merge commit"
			}
			findings = append(findings, fmt.Errorf("hosted run %q must bind the exact %s", run.Scope, boundary))
		}
		if run.Status != completedStatus || run.Conclusion != successfulConclusion {
			findings = append(findings, fmt.Errorf("hosted run %q must be completed/success", run.Scope))
		}
		if run.JobsTotal == 0 || run.JobsSuccess != run.JobsTotal || run.JobsSkipped != 0 || run.JobsFailed != 0 {
			findings = append(findings, fmt.Errorf("hosted run %q requires every job to succeed with none skipped", run.Scope))
		}
	}
	for scope := range expected {
		if _, present := seen[scope]; !present {
			findings = append(findings, fmt.Errorf("required Hosted run %q is missing", scope))
		}
	}
	return findings
}

func validIdentity(identity Identity) bool {
	return validHexOID(identity.Commit) && validHexOID(identity.Tree)
}

func validHexOID(value string) bool {
	if len(value) != 40 {
		return false
	}
	for _, character := range value {
		if (character < '0' || character > '9') && (character < 'a' || character > 'f') {
			return false
		}
	}
	return true
}
