package docscheck

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

const (
	provenanceRecordRule       = "workflow.provenance_record"
	provenanceVerificationRule = "workflow.provenance_verification"
)

func TestWorkflowProvenancePolicyAcceptsCanonicalNightly(t *testing.T) {
	path := ".github/workflows/nightly.yml"
	content := readCanonicalWorkflow(t, path)
	assertNoWorkflowRule(t, path, content, provenanceRecordRule)
	assertNoWorkflowRule(t, path, content, provenanceVerificationRule)
}

func TestWorkflowProvenanceProfilePinsNightlyLegs(t *testing.T) {
	profile, ok := canonicalProvenanceWorkflowProfile(".github/workflows/nightly.yml")
	if !ok {
		t.Fatal("missing nightly provenance profile")
	}
	if got := len(profile.expectedLegs); got != 19 {
		t.Fatalf("producer leg count = %d, want 19", got)
	}
	if profile.fileCount != 38 {
		t.Fatalf("producer file count = %d, want 38", profile.fileCount)
	}
	if !producerLegsMatchManifest(profile) {
		t.Fatal("producer leg expansion and collector manifest differ")
	}
	if !artifactGroupsMatchProducerLegs(profile) {
		t.Fatal("artifact hash bindings and artifact-producing legs differ")
	}
}

func TestWorkflowProvenancePolicyRejectsMissingNightlySteps(t *testing.T) {
	path := ".github/workflows/nightly.yml"
	tests := []struct {
		name string
		job  string
		step string
		rule string
	}{
		{name: "producer record", job: "concurrency", step: "Record provenance", rule: provenanceRecordRule},
		{name: "producer upload", job: "concurrency", step: "Upload provenance", rule: provenanceRecordRule},
		{name: "collector verify", job: "compare", step: "Verify prerequisite provenance", rule: provenanceVerificationRule},
		{name: "collector download", job: "compare", step: "Download provenance", rule: provenanceVerificationRule},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			content := removeWorkflowStep(t, readCanonicalWorkflow(t, path), test.job, test.step)
			assertWorkflowRule(t, path, content, test.rule)
		})
	}
}

func TestWorkflowProvenancePolicyRejectsNightlyWorkloadDrift(t *testing.T) {
	path := ".github/workflows/nightly.yml"
	tests := []struct {
		name string
		job  string
		step string
		old  string
	}{
		{
			name: "fuzz",
			job:  "fuzz",
			step: "Fuzz one target",
			old:  `go test -run='^$' -fuzz="^${{ matrix.target }}$" -fuzztime=10m "${{ matrix.package }}"`,
		},
		{
			name: "concurrency",
			job:  "concurrency",
			step: "Repeat concurrency contracts",
			old:  `go test -count=100 -run='^(TestNthCallFaultIsConsumedOnce|TestDelayUsesManualClock|TestGateHonorsContextCancellation|TestFaultCallSequencesAreLinearized)$' ./internal/provider/fake`,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			content := replaceInWorkflowStep(t, readCanonicalWorkflow(t, path), test.job, test.step, test.old, `":"`)
			assertWorkflowRule(t, path, content, provenanceRecordRule)
		})
	}
}

func TestWorkflowProvenancePolicyRejectsNightlyArtifactDrift(t *testing.T) {
	path := ".github/workflows/nightly.yml"

	t.Run("reproducible build changes", func(t *testing.T) {
		content := replaceInWorkflowStep(
			t,
			readCanonicalWorkflow(t, path),
			"reproducibility",
			"Reproducible build",
			"-buildvcs=false",
			"-buildvcs=true",
		)
		assertWorkflowRule(t, path, content, provenanceRecordRule)
	})

	t.Run("accepted hash binding disappears", func(t *testing.T) {
		content := replaceInWorkflowStep(
			t,
			readCanonicalWorkflow(t, path),
			"compare",
			"Verify prerequisite provenance",
			`            test "${repro_a_hash}" = "${accepted_hash}"`,
			`            : "${repro_a_hash}" "${accepted_hash}"`,
		)
		assertWorkflowRule(t, path, content, provenanceVerificationRule)
	})
}

func TestWorkflowProvenancePolicyDocumentsCanonicalShellRequirement(t *testing.T) {
	path := ".github/workflows/nightly.yml"
	content := replaceInWorkflowStep(
		t,
		readCanonicalWorkflow(t, path),
		"concurrency",
		"Record provenance",
		`          test -n "${go_env_goos}"`,
		`          [ -n "${go_env_goos}" ]`,
	)
	finding := requireWorkflowRule(t, path, content, provenanceRecordRule)
	if !strings.Contains(strings.ToLower(finding.Message), "canonical") {
		t.Fatalf("provenance policy must explain intentional canonical-shell rejection, got %q", finding.Message)
	}
}

func TestNormalizedShellLinesOnlyDropsOneTerminalChompNewline(t *testing.T) {
	tests := []struct {
		name   string
		script string
		want   []string
	}{
		{name: "no final newline", script: "first\nsecond", want: []string{"first", "second"}},
		{name: "one final newline", script: "first\nsecond\n", want: []string{"first", "second"}},
		{name: "content whitespace", script: " first \nsecond", want: []string{" first ", "second"}},
		{name: "extra final blank line", script: "first\nsecond\n\n", want: []string{"first", "second", ""}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got := normalizedShellLines(test.script)
			if !stringSlicesEqual(got, test.want) {
				t.Fatalf("normalizedShellLines(%q) = %#v, want %#v", test.script, got, test.want)
			}
		})
	}
}

func readCanonicalWorkflow(t *testing.T, workflowPath string) string {
	t.Helper()
	path := filepath.Join("..", "..", filepath.FromSlash(workflowPath))
	content, err := os.ReadFile(path) //nolint:gosec // path is selected from test-owned literals.
	if err != nil {
		t.Fatalf("read canonical workflow %s: %v", path, err)
	}
	return string(content)
}

func removeWorkflowStep(t *testing.T, content, job, stepName string) string {
	t.Helper()
	start, end := workflowJobBounds(t, content, job)
	section := content[start:end]
	marker := "      - name: " + stepName + "\n"
	stepStart := strings.Index(section, marker)
	if stepStart < 0 {
		t.Fatalf("step %q not found in job %q", stepName, job)
	}
	remaining := section[stepStart+len(marker):]
	next := strings.Index(remaining, "\n      - ")
	stepEnd := len(section)
	if next >= 0 {
		stepEnd = stepStart + len(marker) + next + 1
	}
	return content[:start] + section[:stepStart] + section[stepEnd:] + content[end:]
}

func replaceInWorkflowStep(t *testing.T, content, job, stepName, old, new string) string {
	t.Helper()
	start, end := workflowJobBounds(t, content, job)
	section := content[start:end]
	marker := "      - name: " + stepName + "\n"
	stepStart := strings.Index(section, marker)
	if stepStart < 0 {
		t.Fatalf("step %q not found in job %q", stepName, job)
	}
	remaining := section[stepStart+len(marker):]
	next := strings.Index(remaining, "\n      - ")
	stepEnd := len(section)
	if next >= 0 {
		stepEnd = stepStart + len(marker) + next + 1
	}
	step := section[stepStart:stepEnd]
	if count := strings.Count(step, old); count != 1 {
		t.Fatalf("replace %q in step %q of job %q: got %d occurrences, want 1", old, stepName, job, count)
	}
	step = strings.Replace(step, old, new, 1)
	section = section[:stepStart] + step + section[stepEnd:]
	return content[:start] + section + content[end:]
}

func workflowJobBounds(t *testing.T, content, job string) (int, int) {
	t.Helper()
	pattern := regexp.MustCompile(`(?m)^  ` + regexp.QuoteMeta(job) + `:\n`)
	match := pattern.FindStringIndex(content)
	if match == nil {
		t.Fatalf("job %q not found", job)
	}
	start := match[0]
	nextPattern := regexp.MustCompile(`(?m)^  [a-zA-Z0-9_-]+:\n`)
	next := nextPattern.FindStringIndex(content[match[1]:])
	end := len(content)
	if next != nil {
		end = match[1] + next[0]
	}
	return start, end
}

func assertWorkflowRule(t *testing.T, path, content, rule string) {
	t.Helper()
	_ = requireWorkflowRule(t, path, content, rule)
}

func requireWorkflowRule(t *testing.T, path, content, rule string) Finding {
	t.Helper()
	findings := checkWorkflowPolicy(path, strings.Split(content, "\n"))
	for _, finding := range findings {
		if finding.Rule == rule {
			return finding
		}
	}
	t.Fatalf("missing %s finding\nfull findings:\n%s", rule, formatFindings(findings))
	return Finding{}
}

func assertNoWorkflowRule(t *testing.T, path, content, rule string) {
	t.Helper()
	findings := checkWorkflowPolicy(path, strings.Split(content, "\n"))
	for _, finding := range findings {
		if finding.Rule == rule {
			t.Fatalf("unexpected %s finding %#v\nfull findings:\n%s", rule, finding, formatFindings(findings))
		}
	}
}
