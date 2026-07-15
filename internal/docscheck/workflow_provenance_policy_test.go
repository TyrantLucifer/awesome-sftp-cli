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

func TestWorkflowProvenancePolicyAcceptsCanonicalWorkflows(t *testing.T) {
	for _, path := range provenanceWorkflowPaths() {
		t.Run(filepath.Base(path), func(t *testing.T) {
			content := readCanonicalWorkflow(t, path)
			assertNoWorkflowRule(t, path, content, provenanceRecordRule)
			assertNoWorkflowRule(t, path, content, provenanceVerificationRule)
		})
	}
}

func TestWorkflowProvenanceProfilesPinLegAndFileCounts(t *testing.T) {
	tests := []struct {
		path      string
		legCount  int
		fileCount int
	}{
		{path: ".github/workflows/ci.yml", legCount: 22, fileCount: 44},
		{path: ".github/workflows/nightly.yml", legCount: 19, fileCount: 38},
	}
	for _, test := range tests {
		t.Run(filepath.Base(test.path), func(t *testing.T) {
			profile, ok := canonicalProvenanceWorkflowProfile(test.path)
			if !ok {
				t.Fatalf("missing provenance profile for %s", test.path)
			}
			if got := len(profile.expectedLegs); got != test.legCount {
				t.Fatalf("producer leg count = %d, want %d", got, test.legCount)
			}
			if profile.fileCount != test.fileCount {
				t.Fatalf("producer file count = %d, want %d", profile.fileCount, test.fileCount)
			}
			if !producerLegsMatchManifest(profile) {
				t.Fatal("producer leg expansion and collector manifest differ")
			}
			if !artifactGroupsMatchProducerLegs(profile) {
				t.Fatal("artifact hash bindings and artifact-producing legs differ")
			}
		})
	}
}

func TestWorkflowProvenancePolicyRejectsMissingWholeSteps(t *testing.T) {
	tests := []struct {
		name     string
		path     string
		job      string
		stepName string
		rule     string
	}{
		{name: "ci record", path: ".github/workflows/ci.yml", job: "quality", stepName: "Record provenance", rule: provenanceRecordRule},
		{name: "ci upload", path: ".github/workflows/ci.yml", job: "quality", stepName: "Upload provenance", rule: provenanceRecordRule},
		{name: "ci verify", path: ".github/workflows/ci.yml", job: "compare", stepName: "Verify prerequisite provenance", rule: provenanceVerificationRule},
		{name: "ci download", path: ".github/workflows/ci.yml", job: "compare", stepName: "Download provenance", rule: provenanceVerificationRule},
		{name: "ci accepted download", path: ".github/workflows/ci.yml", job: "compare", stepName: "Download reproducibility comparison", rule: provenanceVerificationRule},
		{name: "ci comparison record", path: ".github/workflows/ci.yml", job: "compare", stepName: "Record comparison provenance", rule: provenanceVerificationRule},
		{name: "ci comparison upload", path: ".github/workflows/ci.yml", job: "compare", stepName: "Upload comparison provenance", rule: provenanceVerificationRule},
		{name: "nightly record", path: ".github/workflows/nightly.yml", job: "concurrency", stepName: "Record provenance", rule: provenanceRecordRule},
		{name: "nightly upload", path: ".github/workflows/nightly.yml", job: "concurrency", stepName: "Upload provenance", rule: provenanceRecordRule},
		{name: "nightly verify", path: ".github/workflows/nightly.yml", job: "compare", stepName: "Verify prerequisite provenance", rule: provenanceVerificationRule},
		{name: "nightly download", path: ".github/workflows/nightly.yml", job: "compare", stepName: "Download provenance", rule: provenanceVerificationRule},
		{name: "nightly accepted download", path: ".github/workflows/nightly.yml", job: "compare", stepName: "Download reproducibility comparison", rule: provenanceVerificationRule},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			content := removeWorkflowStep(t, readCanonicalWorkflow(t, test.path), test.job, test.stepName)
			assertWorkflowRule(t, test.path, content, test.rule)
		})
	}
}

func TestWorkflowProvenancePolicyRequiresDisabledVCSStamping(t *testing.T) {
	tests := []struct {
		name string
		path string
		job  string
		step string
	}{
		{name: "ci build", path: ".github/workflows/ci.yml", job: "build", step: "Cross-build"},
		{name: "ci reproducibility", path: ".github/workflows/ci.yml", job: "reproducibility", step: "Reproducible build"},
		{name: "nightly reproducibility", path: ".github/workflows/nightly.yml", job: "reproducibility", step: "Reproducible build"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			content := replaceInWorkflowStep(t, readCanonicalWorkflow(t, test.path), test.job, test.step,
				"go build -trimpath -buildvcs=false -o", "go build -trimpath -o")
			assertWorkflowRule(t, test.path, content, provenanceRecordRule)
		})
	}
}

func TestWorkflowProvenancePolicyRejectsNightlyWorkloadDrift(t *testing.T) {
	tests := []struct {
		name string
		job  string
		step string
		old  string
	}{
		{
			name: "fuzz workload replaced with no-op",
			job:  "fuzz",
			step: "Fuzz one target",
			old:  `go test -run='^$' -fuzz="^${{ matrix.target }}$" -fuzztime=10m "${{ matrix.package }}"`,
		},
		{
			name: "concurrency workload replaced with no-op",
			job:  "concurrency",
			step: "Repeat concurrency contracts",
			old:  `go test -count=100 -run='^(TestNthCallFaultIsConsumedOnce|TestDelayUsesManualClock|TestGateHonorsContextCancellation|TestFaultCallSequencesAreLinearized)$' ./internal/provider/fake`,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			content := replaceInWorkflowStep(
				t,
				readCanonicalWorkflow(t, ".github/workflows/nightly.yml"),
				test.job,
				test.step,
				test.old,
				`":"`,
			)
			assertWorkflowRule(t, ".github/workflows/nightly.yml", content, provenanceRecordRule)
		})
	}
}

func TestWorkflowProvenancePolicyRejectsCanonicalShellWhitespaceDrift(t *testing.T) {
	tests := []struct {
		name string
		path string
		job  string
		step string
		old  string
		new  string
		rule string
	}{
		{
			name: "ci manifest terminator extra indent",
			path: ".github/workflows/ci.yml",
			job:  "compare",
			step: "Verify prerequisite provenance",
			old:  "          LEGS\n",
			new:  "           LEGS\n",
			rule: provenanceVerificationRule,
		},
		{
			name: "nightly bindings terminator extra indent",
			path: ".github/workflows/nightly.yml",
			job:  "compare",
			step: "Verify prerequisite provenance",
			old:  "          BINDINGS\n",
			new:  "           BINDINGS\n",
			rule: provenanceVerificationRule,
		},
		{
			name: "ci manifest content trailing space",
			path: ".github/workflows/ci.yml",
			job:  "compare",
			step: "Verify prerequisite provenance",
			old:  "          quality\n",
			new:  "          quality \n",
			rule: provenanceVerificationRule,
		},
		{
			name: "nightly comparison continuation trailing space",
			path: ".github/workflows/nightly.yml",
			job:  "reproducibility-compare",
			step: "Compare independent builds",
			old:  "          for name in \\\n",
			new:  "          for name in \\ \n",
			rule: provenanceRecordRule,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			content := replaceInWorkflowStep(t, readCanonicalWorkflow(t, test.path), test.job, test.step, test.old, test.new)
			assertWorkflowRule(t, test.path, content, test.rule)
		})
	}
}

func TestWorkflowProvenancePolicyPreservesCommonBlockScalarIndent(t *testing.T) {
	path := ".github/workflows/ci.yml"
	lines := canonicalPrepareReproCacheLines()
	oldBlock := "        run: |\n          " + strings.Join(lines, "\n          ") + "\n"
	newBlock := "        run: |\n           " + strings.Join(lines, "\n           ") + "\n"
	content := replaceInWorkflowStep(t, readCanonicalWorkflow(t, path), "reproducibility", "Prepare independent caches", oldBlock, newBlock)
	assertNoWorkflowRule(t, path, content, provenanceRecordRule)
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

func TestWorkflowProvenancePolicyRejectsEarlySuccessAndBrokenRecordDataflow(t *testing.T) {
	tests := []struct {
		name string
		path string
		job  string
		step string
		old  string
		new  string
		rule string
	}{
		{
			name: "record exits after opening",
			path: ".github/workflows/ci.yml",
			job:  "quality",
			step: "Record provenance",
			old:  "          set -euo pipefail\n",
			new:  "          set -euo pipefail\n          exit 0\n",
			rule: provenanceRecordRule,
		},
		{
			name: "verify exits after opening",
			path: ".github/workflows/nightly.yml",
			job:  "compare",
			step: "Verify prerequisite provenance",
			old:  "          set -euo pipefail\n",
			new:  "          set -euo pipefail\n          exit 0\n",
			rule: provenanceVerificationRule,
		},
		{
			name: "record block is not redirected",
			path: ".github/workflows/ci.yml",
			job:  "quality",
			step: "Record provenance",
			old:  "          } >\"${record}\"\n",
			new:  "          }\n",
			rule: provenanceRecordRule,
		},
		{
			name: "leg verification becomes no-op",
			path: ".github/workflows/ci.yml",
			job:  "compare",
			step: "Verify prerequisite provenance",
			old:  "            test \"$(field_value leg \"${record}\")\" = \"${leg}\"\n",
			new:  "            : \"$(field_value leg \"${record}\")\"\n",
			rule: provenanceVerificationRule,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			content := replaceInWorkflowStep(t, readCanonicalWorkflow(t, test.path), test.job, test.step, test.old, test.new)
			assertWorkflowRule(t, test.path, content, test.rule)
		})
	}
}

func TestWorkflowProvenancePolicyRejectsUnboundArtifactHashes(t *testing.T) {
	tests := []struct {
		name string
		path string
		job  string
		step string
		old  string
		new  string
	}{
		{
			name: "build metadata hash is forged",
			path: ".github/workflows/ci.yml",
			job:  "build",
			step: "Record build metadata",
			old:  "          sha256sum \"${binary}\" | awk '{print $1}' >\"${directory}/${{ matrix.artifact }}.sha256\"\n",
			new:  "          printf '%064d\\n' 0 >\"${directory}/${{ matrix.artifact }}.sha256\"\n",
		},
		{
			name: "accepted hash is forged",
			path: ".github/workflows/nightly.yml",
			job:  "reproducibility-compare",
			step: "Compare independent builds",
			old:  "            printf '%s=%s\\n' \"${name}\" \"${actual_a}\" >>\"${comparison}\"\n",
			new:  "            printf '%s=%064d\\n' \"${name}\" 0 >>\"${comparison}\"\n",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			content := replaceInWorkflowStep(t, readCanonicalWorkflow(t, test.path), test.job, test.step, test.old, test.new)
			assertWorkflowRule(t, test.path, content, provenanceRecordRule)
		})
	}
}

func TestWorkflowProvenancePolicyRequiresAcceptedHashDataflow(t *testing.T) {
	for _, path := range provenanceWorkflowPaths() {
		t.Run(filepath.Base(path), func(t *testing.T) {
			content := removeWorkflowStep(t, readCanonicalWorkflow(t, path), "reproducibility-compare", "Upload comparison evidence")
			assertWorkflowRule(t, path, content, provenanceRecordRule)
		})
	}
}

func TestWorkflowProvenancePolicyRequiresTargetAndHashBindings(t *testing.T) {
	tests := []struct {
		name string
		path string
		job  string
		step string
		old  string
		new  string
		rule string
	}{
		{
			name: "producer target GOOS missing",
			path: ".github/workflows/ci.yml",
			job:  "build",
			step: "Record provenance",
			old:  "            printf 'target_goos=%s\\n' '${{ matrix.goos }}'\n",
			new:  "",
			rule: provenanceRecordRule,
		},
		{
			name: "producer target GOARCH forged",
			path: ".github/workflows/nightly.yml",
			job:  "reproducibility",
			step: "Record provenance",
			old:  "            printf 'target_goarch=%s\\n' '${{ matrix.goarch }}'\n",
			new:  "            printf 'target_goarch=%s\\n' amd64\n",
			rule: provenanceRecordRule,
		},
		{
			name: "producer artifact name unbound",
			path: ".github/workflows/ci.yml",
			job:  "build",
			step: "Record provenance",
			old:  "            printf 'artifact=%s\\n' '${{ matrix.artifact }}'\n",
			new:  "            printf 'artifact=%s\\n' amsftp-linux-amd64\n",
			rule: provenanceRecordRule,
		},
		{
			name: "collector target check missing",
			path: ".github/workflows/ci.yml",
			job:  "compare",
			step: "Verify prerequisite provenance",
			old:  "            test \"$(field_value target_goos \"${record}\")\" = \"${expected_goos}\" || return 1\n",
			new:  "",
			rule: provenanceVerificationRule,
		},
		{
			name: "collector accepts non-hex hash",
			path: ".github/workflows/nightly.yml",
			job:  "compare",
			step: "Verify prerequisite provenance",
			old:  "            printf '%s\\n' \"${hash}\" | grep -Eq '^[0-9a-f]{64}$' || return 1\n",
			new:  "            test -n \"${hash}\"\n",
			rule: provenanceVerificationRule,
		},
		{
			name: "collector repro hash unbound from accepted",
			path: ".github/workflows/ci.yml",
			job:  "compare",
			step: "Verify prerequisite provenance",
			old:  "            test \"${repro_a_hash}\" = \"${accepted_hash}\"\n",
			new:  "            test -n \"${repro_a_hash}\"\n",
			rule: provenanceVerificationRule,
		},
		{
			name: "collector build hash unbound from accepted",
			path: ".github/workflows/ci.yml",
			job:  "compare",
			step: "Verify prerequisite provenance",
			old:  "              test \"${build_hash}\" = \"${accepted_hash}\"\n",
			new:  "              test -n \"${build_hash}\"\n",
			rule: provenanceVerificationRule,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			content := replaceInWorkflowStep(t, readCanonicalWorkflow(t, test.path), test.job, test.step, test.old, test.new)
			assertWorkflowRule(t, test.path, content, test.rule)
		})
	}
}

func TestWorkflowProvenancePolicyRejectsLegMatrixManifestAndCountDrift(t *testing.T) {
	tests := []struct {
		name string
		path string
		job  string
		old  string
		new  string
		rule string
	}{
		{
			name: "missing producer leg",
			path: ".github/workflows/ci.yml",
			job:  "quality",
			old:  "      LEG: quality\n",
			new:  "",
			rule: provenanceRecordRule,
		},
		{
			name: "wrong producer leg",
			path: ".github/workflows/ci.yml",
			job:  "quality",
			old:  "      LEG: quality\n",
			new:  "      LEG: not-quality\n",
			rule: provenanceRecordRule,
		},
		{
			name: "duplicate leg from matrix",
			path: ".github/workflows/ci.yml",
			job:  "native",
			old:  "          - macos-15-intel\n",
			new:  "          - macos-15\n",
			rule: provenanceRecordRule,
		},
		{
			name: "nightly matrix leg drift",
			path: ".github/workflows/nightly.yml",
			job:  "concurrency",
			old:  "            arch: arm64\n",
			new:  "            arch: amd64\n",
			rule: provenanceRecordRule,
		},
		{
			name: "ci manifest leg missing",
			path: ".github/workflows/ci.yml",
			job:  "compare",
			old:  "          quality\n",
			new:  "",
			rule: provenanceVerificationRule,
		},
		{
			name: "nightly manifest leg duplicated",
			path: ".github/workflows/nightly.yml",
			job:  "compare",
			old:  "          concurrency-arm64\n",
			new:  "          concurrency-amd64\n",
			rule: provenanceVerificationRule,
		},
		{
			name: "ci leg count drift",
			path: ".github/workflows/ci.yml",
			job:  "compare",
			old:  "          test \"$(wc -l <\"${manifest}\" | tr -d '[:space:]')\" -eq 22\n",
			new:  "          test \"$(wc -l <\"${manifest}\" | tr -d '[:space:]')\" -eq 21\n",
			rule: provenanceVerificationRule,
		},
		{
			name: "nightly file count drift",
			path: ".github/workflows/nightly.yml",
			job:  "compare",
			old:  "          test \"$(find \"${root}\" -mindepth 1 -print | wc -l | tr -d '[:space:]')\" -eq 38\n",
			new:  "          test \"$(find \"${root}\" -mindepth 1 -print | wc -l | tr -d '[:space:]')\" -eq 36\n",
			rule: provenanceVerificationRule,
		},
		{
			name: "ci compare needs drift",
			path: ".github/workflows/ci.yml",
			job:  "compare",
			old:  "      - oldstable\n",
			new:  "",
			rule: provenanceVerificationRule,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			content := replaceInWorkflowJob(t, readCanonicalWorkflow(t, test.path), test.job, test.old, test.new)
			assertWorkflowRule(t, test.path, content, test.rule)
		})
	}
}

func TestWorkflowProvenancePolicyRequiresBoundPinnedArtifactSteps(t *testing.T) {
	tests := []struct {
		name string
		path string
		job  string
		step string
		old  string
		new  string
		rule string
	}{
		{
			name: "producer upload action SHA",
			path: ".github/workflows/ci.yml",
			job:  "quality",
			step: "Upload provenance",
			old:  approvedActionCommits["actions/upload-artifact"],
			new:  "1111111111111111111111111111111111111111",
			rule: provenanceRecordRule,
		},
		{
			name: "producer upload name binding",
			path: ".github/workflows/nightly.yml",
			job:  "concurrency",
			step: "Upload provenance",
			old:  "          name: provenance-${{ env.LEG }}\n",
			new:  "          name: provenance-unbound\n",
			rule: provenanceRecordRule,
		},
		{
			name: "producer upload path binding",
			path: ".github/workflows/ci.yml",
			job:  "oldstable",
			step: "Upload provenance",
			old:  "          path: ${{ runner.temp }}/provenance/${{ env.LEG }}.*\n",
			new:  "          path: ${{ runner.temp }}/provenance/*\n",
			rule: provenanceRecordRule,
		},
		{
			name: "collector download action SHA",
			path: ".github/workflows/ci.yml",
			job:  "compare",
			step: "Download provenance",
			old:  approvedActionCommits["actions/download-artifact"],
			new:  "2222222222222222222222222222222222222222",
			rule: provenanceVerificationRule,
		},
		{
			name: "accepted hash download name binding",
			path: ".github/workflows/nightly.yml",
			job:  "compare",
			step: "Download reproducibility comparison",
			old:  "          name: nightly-reproducibility-comparison\n",
			new:  "          name: unrelated-comparison\n",
			rule: provenanceVerificationRule,
		},
		{
			name: "accepted hash upload path binding",
			path: ".github/workflows/ci.yml",
			job:  "reproducibility-compare",
			step: "Upload comparison evidence",
			old:  "          path: ${{ runner.temp }}/accepted-sha256.txt\n",
			new:  "          path: ${{ runner.temp }}/unbound-sha256.txt\n",
			rule: provenanceRecordRule,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			content := replaceInWorkflowStep(t, readCanonicalWorkflow(t, test.path), test.job, test.step, test.old, test.new)
			assertWorkflowRule(t, test.path, content, test.rule)
		})
	}
}

func TestWorkflowProvenancePolicyRequiresEveryIdentityExactlyOnce(t *testing.T) {
	recordLines := []string{
		"            printf 'workflow=%s\\n' \"${GITHUB_WORKFLOW}\"\n",
		"            printf 'job=%s\\n' \"${GITHUB_JOB}\"\n",
		"            printf 'leg=%s\\n' \"${LEG}\"\n",
		"            printf 'head=%s\\n' \"$(git rev-parse HEAD)\"\n",
		"            printf 'tree=%s\\n' \"$(git rev-parse 'HEAD^{tree}')\"\n",
		"            printf 'go_version=%s\\n' \"$(go version)\"\n",
		"            printf 'runner_os=%s\\n' \"${RUNNER_OS}\"\n",
		"            printf 'runner_arch=%s\\n' \"${RUNNER_ARCH}\"\n",
		"            printf 'runner_image_os=%s\\n' \"${ImageOS}\"\n",
		"            printf 'runner_image_version=%s\\n' \"${ImageVersion}\"\n",
		"            printf 'go_env_goos=%s\\n' \"${go_env_goos}\"\n",
		"            printf 'go_env_goarch=%s\\n' \"${go_env_goarch}\"\n",
	}
	for _, line := range recordLines {
		t.Run(strings.TrimSpace(line), func(t *testing.T) {
			path := ".github/workflows/ci.yml"
			content := replaceInWorkflowStep(t, readCanonicalWorkflow(t, path), "quality", "Record provenance", line, "")
			assertWorkflowRule(t, path, content, provenanceRecordRule)
		})
	}

	verificationLines := []string{
		"            test \"$(field_value schema \"${record}\")\" = amsftp-provenance-v1\n",
		"            test \"$(field_value leg \"${record}\")\" = \"${leg}\"\n",
		"            test \"$(field_value head \"${record}\")\" = \"${expected_head}\"\n",
		"            test \"$(field_value tree \"${record}\")\" = \"${expected_tree}\"\n",
		"            test -n \"$(field_value go_version \"${record}\")\"\n",
		"            test -n \"$(field_value runner_os \"${record}\")\"\n",
		"            test -n \"$(field_value runner_arch \"${record}\")\"\n",
		"            test -n \"$(field_value runner_image_os \"${record}\")\"\n",
		"            test -n \"$(field_value runner_image_version \"${record}\")\"\n",
		"            test -n \"$(field_value go_env_goos \"${record}\")\"\n",
		"            test -n \"$(field_value go_env_goarch \"${record}\")\"\n",
		"            test \"$(field_value status \"${record}\")\" = clean\n",
	}
	for _, line := range verificationLines {
		t.Run(strings.TrimSpace(line), func(t *testing.T) {
			path := ".github/workflows/nightly.yml"
			content := replaceInWorkflowStep(t, readCanonicalWorkflow(t, path), "compare", "Verify prerequisite provenance", line, "")
			assertWorkflowRule(t, path, content, provenanceVerificationRule)
		})
	}
}

func TestWorkflowProvenancePolicyRequiresComparisonIdentityFields(t *testing.T) {
	fields := []string{
		"            printf 'workflow_ref=%s\\n' \"${GITHUB_WORKFLOW_REF}\"\n",
		"            printf 'workflow_sha=%s\\n' \"${GITHUB_WORKFLOW_SHA}\"\n",
		"            printf 'event=%s\\n' \"${GITHUB_EVENT_NAME}\"\n",
		"            printf 'run_id=%s\\n' \"${GITHUB_RUN_ID}\"\n",
		"            printf 'run_attempt=%s\\n' \"${GITHUB_RUN_ATTEMPT}\"\n",
	}
	for _, path := range provenanceWorkflowPaths() {
		for _, line := range fields {
			t.Run(filepath.Base(path)+"/"+strings.TrimSpace(line), func(t *testing.T) {
				content := replaceInWorkflowStep(t, readCanonicalWorkflow(t, path), "compare", "Record comparison provenance", line, "")
				assertWorkflowRule(t, path, content, provenanceVerificationRule)
			})
		}
	}
}

func TestWorkflowProvenancePolicyDocumentsCanonicalShellRequirement(t *testing.T) {
	path := ".github/workflows/ci.yml"
	content := replaceInWorkflowStep(
		t,
		readCanonicalWorkflow(t, path),
		"quality",
		"Record provenance",
		"          test -n \"${go_env_goos}\"\n",
		"          [ -n \"${go_env_goos}\" ]\n",
	)
	finding := requireWorkflowRule(t, path, content, provenanceRecordRule)
	if !strings.Contains(strings.ToLower(finding.Message), "canonical") {
		t.Fatalf("provenance policy must explain intentional canonical-shell rejection, got %q", finding.Message)
	}
}

func provenanceWorkflowPaths() []string {
	return []string{
		".github/workflows/ci.yml",
		".github/workflows/nightly.yml",
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

func replaceInWorkflowJob(t *testing.T, content, job, old, new string) string {
	t.Helper()
	start, end := workflowJobBounds(t, content, job)
	section := content[start:end]
	if count := strings.Count(section, old); count != 1 {
		t.Fatalf("replace %q in job %q: got %d occurrences, want 1", old, job, count)
	}
	section = strings.Replace(section, old, new, 1)
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
