package docscheck

import (
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

const (
	checkoutSHA = "9c091bb21b7c1c1d1991bb908d89e4e9dddfe3e0"
	setupGoSHA  = "924ae3a1cded613372ab5595356fb5720e22ba16"
)

type policyFinding struct {
	Path    string
	Line    int
	Rule    string
	Message string
}

func TestWorkflowPolicyRequiresCIAndNightly(t *testing.T) {
	tests := []struct {
		name string
		path string
	}{
		{name: "ci", path: ".github/workflows/ci.yml"},
		{name: "nightly", path: ".github/workflows/nightly.yml"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			root := prepareFixture(t, "valid")
			if err := os.Remove(filepath.Join(root, filepath.FromSlash(test.path))); err != nil {
				t.Fatalf("remove workflow: %v", err)
			}
			assertPolicyFinding(t, root, policyFinding{
				Path: test.path, Line: 1, Rule: "workflow.required",
				Message: "required workflow file is missing or is not a regular file",
			})
		})
	}
}

func TestWorkflowGenericPolicy(t *testing.T) {
	tests := []struct {
		name       string
		path       string
		old        string
		new        string
		occurrence int
		finding    policyFinding
	}{
		{
			name: "top permissions missing", path: ".github/workflows/nightly.yml",
			old: "permissions:\n  contents: read\n", new: "",
			finding: policyFinding{Path: ".github/workflows/nightly.yml", Line: 1, Rule: "workflow.top_permissions", Message: "top-level permissions must be exactly contents: read"},
		},
		{
			name: "top permissions not exact", path: ".github/workflows/nightly.yml",
			old: "permissions:\n  contents: read\n", new: "permissions:\n  contents: read\n  issues: read\n",
			finding: policyFinding{Path: ".github/workflows/nightly.yml", Line: 4, Rule: "workflow.top_permissions", Message: "top-level permissions must be exactly contents: read"},
		},
		{
			name: "checkout persist missing", path: ".github/workflows/ci.yml",
			old: "        with:\n          persist-credentials: false\n", new: "",
			occurrence: 1,
			finding:    policyFinding{Path: ".github/workflows/ci.yml", Line: 11, Rule: "workflow.persist_credentials", Message: "checkout must explicitly set persist-credentials to false"},
		},
		{
			name: "checkout persist wrong", path: ".github/workflows/ci.yml",
			old: "          persist-credentials: false\n", new: "          persist-credentials: true\n",
			occurrence: 1,
			finding:    policyFinding{Path: ".github/workflows/ci.yml", Line: 13, Rule: "workflow.persist_credentials", Message: "checkout persist-credentials must be false"},
		},
		{
			name: "second checkout omission", path: ".github/workflows/ci.yml",
			old: "          cache-dependency-path: \"go.sum\\ntools/go.sum\"\n      - run: make check\n", new: "          cache-dependency-path: \"go.sum\\ntools/go.sum\"\n      - uses: actions/checkout@" + checkoutSHA + "\n      - run: make check\n",
			occurrence: 1,
			finding:    policyFinding{Path: ".github/workflows/ci.yml", Line: 19, Rule: "workflow.persist_credentials", Message: "checkout must explicitly set persist-credentials to false"},
		},
		{
			name: "job timeout missing", path: ".github/workflows/ci.yml",
			old: "    timeout-minutes: 20\n", new: "",
			finding: policyFinding{Path: ".github/workflows/ci.yml", Line: 7, Rule: "workflow.job_timeout", Message: "job \"quality\" must set timeout-minutes to a positive integer"},
		},
		{
			name: "job timeout invalid", path: ".github/workflows/ci.yml",
			old: "    timeout-minutes: 20\n", new: "    timeout-minutes: 0\n",
			finding: policyFinding{Path: ".github/workflows/ci.yml", Line: 9, Rule: "workflow.job_timeout", Message: "job \"quality\" must set timeout-minutes to a positive integer"},
		},
		{
			name: "matrix fail fast missing", path: ".github/workflows/ci.yml",
			old: "      fail-fast: false\n", new: "",
			occurrence: 1,
			finding:    policyFinding{Path: ".github/workflows/ci.yml", Line: 26, Rule: "workflow.matrix_fail_fast", Message: "matrix job \"native\" must set strategy.fail-fast to false"},
		},
		{
			name: "matrix fail fast true", path: ".github/workflows/ci.yml",
			old: "      fail-fast: false\n", new: "      fail-fast: true\n",
			occurrence: 1,
			finding:    policyFinding{Path: ".github/workflows/ci.yml", Line: 30, Rule: "workflow.matrix_fail_fast", Message: "matrix job \"native\" must set strategy.fail-fast to false"},
		},
		{
			name: "job continue on error", path: ".github/workflows/ci.yml",
			old: "    timeout-minutes: 20\n", new: "    timeout-minutes: 20\n    continue-on-error: false\n",
			finding: policyFinding{Path: ".github/workflows/ci.yml", Line: 10, Rule: "workflow.continue_on_error", Message: "continue-on-error is forbidden"},
		},
		{
			name: "step continue on error", path: ".github/workflows/ci.yml",
			old: "      - run: make check\n", new: "      - run: make check\n        continue-on-error: false\n",
			occurrence: 1,
			finding:    policyFinding{Path: ".github/workflows/ci.yml", Line: 20, Rule: "workflow.continue_on_error", Message: "continue-on-error is forbidden"},
		},
		{
			name: "approved action wrong sha", path: ".github/workflows/ci.yml",
			old: "actions/checkout@" + checkoutSHA, new: "actions/checkout@1111111111111111111111111111111111111111",
			occurrence: 1,
			finding:    policyFinding{Path: ".github/workflows/ci.yml", Line: 11, Rule: "workflow.action_version", Message: "action \"actions/checkout\" must use approved commit \"" + checkoutSHA + "\""},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			root := prepareFixture(t, "valid")
			if test.occurrence > 0 {
				replacePolicyFileOccurrence(t, root, test.path, test.old, test.new, test.occurrence)
			} else {
				replacePolicyFile(t, root, test.path, test.old, test.new)
			}
			assertPolicyFinding(t, root, test.finding)
		})
	}
}

func TestCISetupGoRequiresBothModuleLocks(t *testing.T) {
	const (
		path      = ".github/workflows/ci.yml"
		canonical = "          cache-dependency-path: \"go.sum\\ntools/go.sum\"\n"
	)
	tests := []struct {
		name        string
		replacement string
		occurrence  int
		finding     policyFinding
	}{
		{
			name: "current root only", replacement: "          cache-dependency-path: go.sum\n", occurrence: 1,
			finding: policyFinding{Path: path, Line: 7, Rule: "workflow.ci_quality", Message: "quality job must run on ubuntu-24.04 and execute make check and make supply-chain"},
		},
		{
			name: "current tools only", replacement: "          cache-dependency-path: tools/go.sum\n", occurrence: 1,
			finding: policyFinding{Path: path, Line: 7, Rule: "workflow.ci_quality", Message: "quality job must run on ubuntu-24.04 and execute make check and make supply-chain"},
		},
		{
			name: "oldstable root only", replacement: "          cache-dependency-path: go.sum\n", occurrence: 3,
			finding: policyFinding{Path: path, Line: 57, Rule: "workflow.ci_oldstable_toolchain", Message: "oldstable job must select Go 1.25.12 with actions/setup-go"},
		},
		{
			name: "oldstable tools only", replacement: "          cache-dependency-path: tools/go.sum\n", occurrence: 3,
			finding: policyFinding{Path: path, Line: 57, Rule: "workflow.ci_oldstable_toolchain", Message: "oldstable job must select Go 1.25.12 with actions/setup-go"},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			root := prepareFixture(t, "valid")
			replacePolicyFileOccurrence(t, root, path, canonical, test.replacement, test.occurrence)
			assertPolicyFinding(t, root, test.finding)
		})
	}
}

func TestWorkflowFlowPolicyAndBenignLookalikes(t *testing.T) {
	root := prepareFixture(t, "valid")
	writePolicyFile(t, root, ".github/workflows/flow.yml", strings.Join([]string{
		"name: flow policy",
		"on: [push]",
		"permissions: {contents: read}",
		"jobs: {test: {runs-on: ubuntu-24.04, timeout-minutes: 5, steps: [{uses: 'actions/checkout@" + checkoutSHA + "', with: {persist-credentials: false, continue-on-error: true, timeout-minutes: 0, permissions: write-all}}, {run: 'echo uses: actions/checkout@v7 continue-on-error: true'}]}}",
		"",
	}, "\n"))
	assertNoPolicyFindings(t, root)

	writePolicyFile(t, root, ".github/workflows/flow.yml", strings.Join([]string{
		"name: flow attack",
		"on: [push]",
		"permissions: {contents: read, issues: read}",
		"jobs: {test: {runs-on: ubuntu-24.04, timeout-minutes: 0, continue-on-error: false, steps: [{uses: 'actions/checkout@" + checkoutSHA + "', with: {persist-credentials: true}}]}}",
		"",
	}, "\n"))
	for _, finding := range []policyFinding{
		{Path: ".github/workflows/flow.yml", Line: 3, Rule: "workflow.top_permissions", Message: "top-level permissions must be exactly contents: read"},
		{Path: ".github/workflows/flow.yml", Line: 4, Rule: "workflow.job_timeout", Message: "job \"test\" must set timeout-minutes to a positive integer"},
		{Path: ".github/workflows/flow.yml", Line: 4, Rule: "workflow.continue_on_error", Message: "continue-on-error is forbidden"},
		{Path: ".github/workflows/flow.yml", Line: 4, Rule: "workflow.persist_credentials", Message: "checkout persist-credentials must be false"},
	} {
		assertPolicyFinding(t, root, finding)
	}
}

func TestWorkflowPolicyRejectsNonRunBlockScalar(t *testing.T) {
	root := prepareFixture(t, "valid")
	writePolicyFile(t, root, ".github/workflows/artifact.yml", strings.Join([]string{
		"name: artifact",
		"on: [push]",
		"permissions: {contents: read}",
		"jobs:",
		"  upload:",
		"    runs-on: ubuntu-24.04",
		"    timeout-minutes: 5",
		"    steps:",
		"      - uses: actions/upload-artifact@043fb46d1a93c77aae656e7c1c64a875d1fc6a0a",
		"        with:",
		"          path: |",
		"            dist/amsftp:darwin",
		"            # literal block content is opaque",
		"",
	}, "\n"))
	assertPolicyFinding(t, root, policyFinding{
		Path: ".github/workflows/artifact.yml", Line: 11, Rule: "workflow.syntax", Message: workflowSyntaxMessage,
	})
}

func TestWorkflowPolicyAllowsRunBlockScalar(t *testing.T) {
	root := prepareFixture(t, "valid")
	replacePolicyFile(t, root, ".github/workflows/ci.yml",
		"      - run: make supply-chain\n",
		"      - run: make supply-chain\n      - name: Record provenance\n        run: |\n          printf '%s\\n' provenance\n",
	)
	assertNoPolicyFindings(t, root)
}

func TestCIRejectsBlockScalarsOutsideRun(t *testing.T) {
	const path = ".github/workflows/ci.yml"
	tests := []struct {
		name       string
		old        string
		new        string
		occurrence int
		line       int
	}{
		{name: "permissions", old: "  contents: read\n", new: "  contents: >\n    read\n", occurrence: 1, line: 5},
		{name: "runs on", old: "    runs-on: ubuntu-24.04\n", new: "    runs-on: >\n      ubuntu-24.04\n", occurrence: 1, line: 8},
		{name: "fail fast", old: "      fail-fast: false\n", new: "      fail-fast: >\n        false\n", occurrence: 1, line: 30},
		{name: "matrix os", old: "        os: [ubuntu-22.04, ubuntu-24.04, macos-15, macos-15-intel]\n", new: "        os: |\n          ubuntu-22.04\n", occurrence: 1, line: 32},
		{name: "persist credentials", old: "          persist-credentials: false\n", new: "          persist-credentials: >\n            false\n", occurrence: 1, line: 13},
		{name: "setup go version", old: "          go-version: 1.25.12\n", new: "          go-version: >\n            1.25.12\n", occurrence: 1, line: 72},
		{name: "cgo enabled", old: "          CGO_ENABLED: \"0\"\n", new: "          CGO_ENABLED: >\n            0\n", occurrence: 1, line: 110},
		{name: "matrix artifact", old: "            artifact: amsftp-darwin-arm64\n", new: "            artifact: >\n              amsftp-darwin-arm64\n", occurrence: 1, line: 88},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			root := prepareFixture(t, "valid")
			replacePolicyFileOccurrence(t, root, path, test.old, test.new, test.occurrence)
			assertPolicyFinding(t, root, policyFinding{
				Path: path, Line: test.line, Rule: "workflow.syntax", Message: workflowSyntaxMessage,
			})
		})
	}
}

func TestWorkflowUnsafeYAMLIsRejected(t *testing.T) {
	base := "name: syntax\non: [push]\npermissions: {contents: read}\njobs:\n  test:\n    runs-on: ubuntu-24.04\n    timeout-minutes: 5\n    steps: []\n"
	tests := []struct {
		name    string
		content string
		line    int
	}{
		{name: "tab indentation", content: strings.Replace(base, "  test:", "\ttest:", 1), line: 5},
		{name: "unterminated quote", content: strings.Replace(base, "name: syntax", "name: 'syntax", 1), line: 1},
		{name: "unterminated flow", content: strings.Replace(base, "permissions: {contents: read}", "permissions: {contents: read", 1), line: 3},
		{name: "anchor", content: strings.Replace(base, "permissions: {contents: read}", "permissions: &policy {contents: read}", 1), line: 3},
		{name: "alias", content: strings.Replace(base, "permissions: {contents: read}", "permissions: *policy", 1), line: 3},
		{name: "tag", content: strings.Replace(base, "permissions: {contents: read}", "permissions: !policy {contents: read}", 1), line: 3},
		{name: "merge", content: strings.Replace(base, "    runs-on: ubuntu-24.04", "    <<: *defaults\n    runs-on: ubuntu-24.04", 1), line: 6},
		{name: "duplicate policy key", content: strings.Replace(base, "    timeout-minutes: 5", "    timeout-minutes: 5\n    timeout-minutes: 6", 1), line: 8},
		{name: "duplicate job", content: strings.Replace(base, "    steps: []", "    steps: []\n  test: {runs-on: ubuntu-24.04, timeout-minutes: 5, steps: []}", 1), line: 9},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			root := prepareFixture(t, "valid")
			writePolicyFile(t, root, ".github/workflows/syntax.yml", test.content)
			assertPolicyFinding(t, root, policyFinding{
				Path: ".github/workflows/syntax.yml", Line: test.line, Rule: "workflow.syntax",
				Message: "workflow policy cannot safely interpret this YAML construct",
			})
		})
	}
}

func TestCIRequiresFixedJobs(t *testing.T) {
	root := prepareFixture(t, "valid")
	writePolicyFile(t, root, ".github/workflows/ci.yml", "name: ci\non: [push]\npermissions: {contents: read}\njobs: {}\n")
	for _, job := range []string{"quality", "native", "oldstable", "build"} {
		assertPolicyFinding(t, root, policyFinding{
			Path: ".github/workflows/ci.yml", Line: 1, Rule: "workflow.ci_job",
			Message: fmt.Sprintf("ci workflow must define job %q", job),
		})
	}
}

func TestCIRequiredJobsCannotBeConditional(t *testing.T) {
	const (
		path    = ".github/workflows/ci.yml"
		rule    = "workflow.ci_job_condition"
		message = "required ci job %q must not define job-level if"
	)
	tests := []struct {
		name       string
		job        string
		line       int
		condition  string
		occurrence int
	}{
		{name: "quality false", job: "quality", line: 7, condition: "false", occurrence: 1},
		{name: "native false", job: "native", line: 26, condition: "false", occurrence: 2},
		{name: "oldstable false", job: "oldstable", line: 57, condition: "false", occurrence: 3},
		{name: "build false", job: "build", line: 79, condition: "false", occurrence: 4},
		{name: "empty key", job: "quality", line: 7, condition: "", occurrence: 1},
		{name: "expression", job: "native", line: 26, condition: "${{ always() }}", occurrence: 2},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			root := prepareFixture(t, "valid")
			old := "    timeout-minutes: "
			content := readPolicyFile(t, root, path)
			start := 0
			for count := 0; count < test.occurrence; count++ {
				relative := strings.Index(content[start:], old)
				if relative < 0 {
					t.Fatalf("timeout occurrence %d not found", test.occurrence)
				}
				start += relative + len(old)
			}
			lineEnd := strings.IndexByte(content[start:], '\n')
			if lineEnd < 0 {
				t.Fatal("timeout line ending not found")
			}
			insert := start + lineEnd + 1
			content = content[:insert] + "    if: " + test.condition + "\n" + content[insert:]
			writePolicyFile(t, root, path, content)
			assertPolicyFinding(t, root, policyFinding{
				Path: path, Line: test.line, Rule: rule, Message: fmt.Sprintf(message, test.job),
			})
		})
	}
}

func TestCIRequiredJobsCannotDependOnSkippedJobs(t *testing.T) {
	const (
		path       = ".github/workflows/ci.yml"
		rule       = "workflow.ci_job_needs"
		jobsAnchor = "jobs:\n"
	)
	skippedJob := strings.Join([]string{
		"jobs:",
		"  skipped:",
		"    if: false",
		"    runs-on: ubuntu-24.04",
		"    timeout-minutes: 5",
		"    steps:",
		"      - run: exit 0",
	}, "\n") + "\n"
	tests := []struct {
		job  string
		line int
	}{
		{job: "quality", line: 13},
		{job: "native", line: 32},
		{job: "oldstable", line: 63},
		{job: "build", line: 85},
	}
	for _, test := range tests {
		t.Run(test.job, func(t *testing.T) {
			root := prepareFixture(t, "valid")
			replacePolicyFile(t, root, path, jobsAnchor, skippedJob)
			content := readPolicyFile(t, root, path)
			jobAnchor := "  " + test.job + ":\n"
			jobStart := strings.Index(content, jobAnchor)
			if jobStart < 0 {
				t.Fatalf("job %q not found", test.job)
			}
			timeoutStart := strings.Index(content[jobStart:], "    timeout-minutes:")
			if timeoutStart < 0 {
				t.Fatalf("job %q timeout not found", test.job)
			}
			timeoutStart += jobStart
			timeoutEnd := strings.IndexByte(content[timeoutStart:], '\n')
			if timeoutEnd < 0 {
				t.Fatalf("job %q timeout ending not found", test.job)
			}
			insert := timeoutStart + timeoutEnd + 1
			content = content[:insert] + "    needs: skipped\n" + content[insert:]
			writePolicyFile(t, root, path, content)
			assertPolicyFinding(t, root, policyFinding{
				Path: path, Line: test.line, Rule: rule,
				Message: fmt.Sprintf("required ci job %q must be independent and must not define needs", test.job),
			})
		})
	}
}

func TestCIRequiredJobsRejectUnapprovedExecutionContexts(t *testing.T) {
	const (
		path    = ".github/workflows/ci.yml"
		rule    = "workflow.ci_job_context"
		message = "required ci job %q must use only the approved job-level execution shape"
	)
	tests := []struct {
		name       string
		job        string
		line       int
		occurrence int
		content    string
	}{
		{
			name: "quality container makeflags", job: "quality", line: 7, occurrence: 1,
			content: "    container:\n      image: ubuntu:24.04\n      env:\n        MAKEFLAGS: -n\n",
		},
		{
			name: "native container path", job: "native", line: 26, occurrence: 1,
			content: "    container:\n      image: ubuntu:24.04\n      env:\n        PATH: /tmp/ci-shims\n",
		},
		{
			name: "oldstable services", job: "oldstable", line: 57, occurrence: 2,
			content: "    services:\n      helper:\n        image: alpine:3.20\n",
		},
		{
			name: "build environment", job: "build", line: 79, occurrence: 3,
			content: "    environment: production\n",
		},
		{
			name: "quality defaults", job: "quality", line: 7, occurrence: 1,
			content: "    defaults:\n      run:\n        shell: /bin/true {0}\n",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			root := prepareFixture(t, "valid")
			anchor := "    timeout-minutes: 20\n"
			if test.job != "quality" {
				anchor = "    timeout-minutes: 30\n"
			}
			replacePolicyFileOccurrence(t, root, path, anchor, anchor+test.content, test.occurrence)
			assertPolicyFinding(t, root, policyFinding{
				Path: path, Line: test.line, Rule: rule, Message: fmt.Sprintf(message, test.job),
			})
		})
	}
}

func TestCIMatrixStrategyRejectsUnapprovedKeys(t *testing.T) {
	const path = ".github/workflows/ci.yml"
	root := prepareFixture(t, "valid")
	replacePolicyFileOccurrence(t, root, path,
		"      fail-fast: false\n",
		"      fail-fast: false\n      max-parallel: 1\n",
		1,
	)
	assertPolicyFinding(t, root, policyFinding{
		Path: path, Line: 26, Rule: "workflow.ci_job_context",
		Message: `required ci job "native" must use only the approved job-level execution shape`,
	})
}

func TestCIRequiredJobsRejectGitHubEnvironmentMutation(t *testing.T) {
	const (
		path    = ".github/workflows/ci.yml"
		rule    = "workflow.ci_environment_mutation"
		message = "required ci job %q must not reference GITHUB_ENV or GITHUB_PATH in run steps"
	)
	tests := []struct {
		job        string
		line       int
		occurrence int
	}{
		{job: "quality", line: 7, occurrence: 1},
		{job: "native", line: 26, occurrence: 2},
		{job: "oldstable", line: 57, occurrence: 3},
		{job: "build", line: 79, occurrence: 4},
	}
	for _, test := range tests {
		for _, variable := range []string{"GITHUB_ENV", "GITHUB_PATH"} {
			t.Run(test.job+"_"+variable, func(t *testing.T) {
				root := prepareFixture(t, "valid")
				replacement := "    steps:\n      - run: echo 'MAKEFLAGS=-n' >>\"${" + variable + "}\"\n"
				replacePolicyFileOccurrence(t, root, path, "    steps:\n", replacement, test.occurrence)
				assertPolicyFinding(t, root, policyFinding{
					Path: path, Line: test.line, Rule: rule, Message: fmt.Sprintf(message, test.job),
				})
			})
		}
	}
}

func TestCIRequiredCommandCreditRejectsUntrustedPredecessors(t *testing.T) {
	const path = ".github/workflows/ci.yml"
	predecessors := []struct {
		name string
		step string
	}{
		{
			name: "indirect GITHUB_ENV mutation",
			step: "      - env:\n          OUT: GITHUB_ENV\n        run: echo MAKEFLAGS=-n >> \"${!OUT}\"\n",
		},
		{
			name: "indirect GITHUB_PATH mutation",
			step: "      - env:\n          OUT: GITHUB_PATH\n        run: echo /tmp/ci-shims >> \"${!OUT}\"\n",
		},
		{
			name: "unknown run",
			step: "      - run: echo untrusted predecessor\n",
		},
		{
			name: "unknown pinned action",
			step: "      - uses: example/untrusted@1111111111111111111111111111111111111111\n",
		},
	}
	jobs := []struct {
		name       string
		anchor     string
		prefix     string
		suffix     string
		line       int
		occurrence int
		rule       string
		message    string
	}{
		{
			name: "quality", anchor: "          cache-dependency-path: \"go.sum\\ntools/go.sum\"\n      - run: make check\n",
			prefix: "          cache-dependency-path: \"go.sum\\ntools/go.sum\"\n", suffix: "      - run: make check\n",
			line: 7, occurrence: 1, rule: "workflow.ci_quality", message: "quality job must run on ubuntu-24.04 and execute make check and make supply-chain",
		},
		{
			name: "native", anchor: "          cache-dependency-path: \"go.sum\\ntools/go.sum\"\n      - run: make fmt-check\n",
			prefix: "          cache-dependency-path: \"go.sum\\ntools/go.sum\"\n", suffix: "      - run: make fmt-check\n",
			line: 26, occurrence: 1, rule: "workflow.ci_native_command", message: `native job is missing unconditional command "make fmt-check"`,
		},
		{
			name: "oldstable", anchor: "          cache-dependency-path: \"go.sum\\ntools/go.sum\"\n      - run: make check\n",
			prefix: "          cache-dependency-path: \"go.sum\\ntools/go.sum\"\n", suffix: "      - run: make check\n",
			line: 57, occurrence: 2, rule: "workflow.ci_oldstable_check", message: "oldstable job must execute unconditional make check",
		},
		{
			name: "build", anchor: "          cache-dependency-path: \"go.sum\\ntools/go.sum\"\n      - run: mkdir -p \"${{ runner.temp }}/build/${{ matrix.artifact }}\"\n",
			prefix: "          cache-dependency-path: \"go.sum\\ntools/go.sum\"\n", suffix: "      - run: mkdir -p \"${{ runner.temp }}/build/${{ matrix.artifact }}\"\n",
			line: 79, occurrence: 1, rule: "workflow.ci_build_command", message: "build job must cross-build each matrix tuple with CGO_ENABLED=0, -trimpath, and -buildvcs=false into runner.temp",
		},
	}
	for _, job := range jobs {
		for _, predecessor := range predecessors {
			t.Run(job.name+"_"+predecessor.name, func(t *testing.T) {
				root := prepareFixture(t, "valid")
				replacePolicyFileOccurrence(t, root, path, job.anchor, job.prefix+predecessor.step+job.suffix, job.occurrence)
				assertPolicyFinding(t, root, policyFinding{
					Path: path, Line: job.line, Rule: job.rule, Message: job.message,
				})
			})
		}
	}
}

func TestCIRequiredCommandCreditRejectsRunBlockScalars(t *testing.T) {
	const (
		path    = ".github/workflows/ci.yml"
		rule    = "workflow.ci_quality"
		message = "quality job must run on ubuntu-24.04 and execute make check and make supply-chain"
	)
	tests := []struct {
		name        string
		old         string
		replacement string
	}{
		{
			name: "folded scalar with a blank line is two shell commands",
			old:  "      - run: make check\n",
			replacement: "      - run: >\n" +
				"          make\n" +
				"\n" +
				"          check\n",
		},
		{
			name: "literal scalar",
			old:  "      - run: make lint\n",
			replacement: "      - run: |\n" +
				"          make lint\n",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			root := prepareFixture(t, "valid")
			replacePolicyFileOccurrence(t, root, path, test.old, test.replacement, 1)
			assertPolicyFinding(t, root, policyFinding{
				Path: path, Line: 7, Rule: rule, Message: message,
			})
		})
	}
}

func TestCIMatrixFailFastMustBePlainBooleanFalse(t *testing.T) {
	const path = ".github/workflows/ci.yml"
	root := prepareFixture(t, "valid")
	replacePolicyFileOccurrence(t, root, path, "      fail-fast: false\n", "      fail-fast: \"false\"\n", 1)
	assertPolicyFinding(t, root, policyFinding{
		Path: path, Line: 26, Rule: "workflow.ci_job_context",
		Message: `required ci job "native" must use only the approved job-level execution shape`,
	})
}

func TestCIRequiredMakeOutputPathsMustBeExplicitAndExact(t *testing.T) {
	const path = ".github/workflows/ci.yml"
	steps := []struct {
		name              string
		step              string
		jobLine           int
		blockCoverageLine int
		rule              string
		message           string
	}{
		{
			name: "quality check",
			step: "      - run: make check\n" +
				"        env:\n" +
				"          BUILD_DIR: ${{ runner.temp }}/quality/build\n" +
				"          COVERAGE_DIR: ${{ runner.temp }}/quality/coverage\n",
			jobLine: 7, blockCoverageLine: 22, rule: "workflow.ci_quality",
			message: "quality job must run on ubuntu-24.04 and execute make check and make supply-chain",
		},
		{
			name: "native test",
			step: "      - run: make test\n" +
				"        env:\n" +
				"          BUILD_DIR: ${{ runner.temp }}/native/build\n" +
				"          COVERAGE_DIR: ${{ runner.temp }}/native/coverage\n",
			jobLine: 26, blockCoverageLine: 47, rule: "workflow.ci_native_command",
			message: `native job is missing unconditional command "make test"`,
		},
		{
			name: "native contract",
			step: "      - run: make test-contract\n" +
				"        env:\n" +
				"          BUILD_DIR: ${{ runner.temp }}/native/build\n" +
				"          COVERAGE_DIR: ${{ runner.temp }}/native/coverage\n",
			jobLine: 26, blockCoverageLine: 51, rule: "workflow.ci_native_command",
			message: `native job is missing unconditional command "make test-contract"`,
		},
		{
			name: "oldstable check",
			step: "      - run: make check\n" +
				"        env:\n" +
				"          BUILD_DIR: ${{ runner.temp }}/oldstable/build\n" +
				"          COVERAGE_DIR: ${{ runner.temp }}/oldstable/coverage\n",
			jobLine: 57, blockCoverageLine: 78, rule: "workflow.ci_oldstable_check",
			message: "oldstable job must execute unconditional make check",
		},
	}
	for _, step := range steps {
		step := step
		for _, mutation := range []struct {
			name        string
			replacement func(string) string
			block       bool
		}{
			{
				name: "missing",
				replacement: func(value string) string {
					start := strings.Index(value, "          BUILD_DIR:")
					end := start + strings.IndexByte(value[start:], '\n') + 1
					return value[:start] + value[end:]
				},
			},
			{
				name: "empty",
				replacement: func(value string) string {
					start := strings.Index(value, "          COVERAGE_DIR:")
					end := start + strings.IndexByte(value[start:], '\n')
					return value[:start] + "          COVERAGE_DIR: \"\"" + value[end:]
				},
			},
			{
				name: "wrong",
				replacement: func(value string) string {
					return strings.Replace(value, "/build\n", "/other\n", 1)
				},
			},
			{
				name:  "block scalar",
				block: true,
				replacement: func(value string) string {
					start := strings.Index(value, "          COVERAGE_DIR:")
					end := start + strings.IndexByte(value[start:], '\n')
					coverage := strings.TrimPrefix(value[start:end], "          COVERAGE_DIR: ")
					return value[:start] + "          COVERAGE_DIR: >\n            " + coverage + value[end:]
				},
			},
		} {
			mutation := mutation
			t.Run(step.name+"_"+mutation.name, func(t *testing.T) {
				root := prepareFixture(t, "valid")
				replacePolicyFile(t, root, path, step.step, mutation.replacement(step.step))
				if mutation.block {
					assertPolicyFinding(t, root, policyFinding{
						Path: path, Line: step.blockCoverageLine, Rule: "workflow.syntax", Message: workflowSyntaxMessage,
					})
					return
				}
				assertPolicyFinding(t, root, policyFinding{
					Path: path, Line: step.jobLine, Rule: step.rule, Message: step.message,
				})
			})
		}
	}
}

func TestCIQualityContract(t *testing.T) {
	tests := []struct {
		name       string
		old        string
		new        string
		occurrence int
	}{
		{name: "wrong runner", old: "    runs-on: ubuntu-24.04\n", new: "    runs-on: macos-15\n", occurrence: 1},
		{name: "missing check", old: "      - run: make check\n", new: "      - run: echo make check\n", occurrence: 1},
		{name: "missing supply chain", old: "      - run: make supply-chain\n", new: "      - run: echo make supply-chain\n"},
		{name: "prefix target spoof", old: "      - run: make check\n", new: "      - run: make check-disabled\n", occurrence: 1},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			root := prepareFixture(t, "valid")
			if test.occurrence > 0 {
				replacePolicyFileOccurrence(t, root, ".github/workflows/ci.yml", test.old, test.new, test.occurrence)
			} else {
				replacePolicyFile(t, root, ".github/workflows/ci.yml", test.old, test.new)
			}
			assertPolicyFinding(t, root, policyFinding{
				Path: ".github/workflows/ci.yml", Line: 7, Rule: "workflow.ci_quality",
				Message: "quality job must run on ubuntu-24.04 and execute make check and make supply-chain",
			})
		})
	}
}

func TestMakeTargetCreditRequiresExactExecution(t *testing.T) {
	tests := []struct {
		name   string
		script string
		want   bool
	}{
		{name: "exact", script: "make test-race", want: true},
		{name: "quoted exact", script: "make 'test-race'", want: true},
		{name: "short dry run", script: "make -n test-race"},
		{name: "just print", script: "make --just-print test-race"},
		{name: "long dry run", script: "make --dry-run test-race"},
		{name: "recon", script: "make --recon test-race"},
		{name: "question", script: "make -q test-race"},
		{name: "touch", script: "make -t test-race"},
		{name: "combined short options", script: "make -ksn test-race"},
		{name: "option after target", script: "make test-race -n"},
		{name: "directory separate", script: "make -C other test-race"},
		{name: "directory joined", script: "make -Cother test-race"},
		{name: "directory long separate", script: "make --directory other test-race"},
		{name: "directory long equals", script: "make --directory=other test-race"},
		{name: "file separate", script: "make -f other.mk test-race"},
		{name: "file joined", script: "make -fother.mk test-race"},
		{name: "file long separate", script: "make --file other.mk test-race"},
		{name: "file long equals", script: "make --file=other.mk test-race"},
		{name: "makefile long separate", script: "make --makefile other.mk test-race"},
		{name: "makefile long equals", script: "make --makefile=other.mk test-race"},
		{name: "ignored failure", script: "make test-race || true"},
		{name: "multiple commands", script: "make test-race\necho done"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			job := workflowJob{steps: []workflowStep{{run: &policyYAMLScalar{value: test.script}}}}
			if got := jobHasMakeTarget(job, "test-race"); got != test.want {
				t.Fatalf("jobHasMakeTarget(%q) = %t, want %t", test.script, got, test.want)
			}
		})
	}
}

func TestSingleSafeShellCommandRequiresPlainRunScalar(t *testing.T) {
	plainRun := &policyYAMLNode{
		kind:   policyYAMLScalarNode,
		scalar: policyYAMLScalar{value: "make check"},
	}
	blockRun := &policyYAMLNode{
		kind:        policyYAMLScalarNode,
		scalar:      policyYAMLScalar{value: "make  check"},
		blockScalar: true,
	}
	tests := []struct {
		name string
		run  *policyYAMLNode
		want bool
	}{
		{name: "plain scalar", run: plainRun, want: true},
		{name: "block scalar", run: blockRun},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			step := workflowStep{
				node: &policyYAMLNode{kind: policyYAMLMappingNode, mappings: []policyYAMLMapping{{
					key: policyYAMLScalar{value: "run"}, value: test.run,
				}}},
				run: &test.run.scalar,
			}
			_, got := singleSafeShellCommand(workflowJob{}, step)
			if got != test.want {
				t.Fatalf("singleSafeShellCommand() accepted = %t, want %t", got, test.want)
			}
		})
	}
}

func TestMakeTargetCreditRejectsWorkflowBypasses(t *testing.T) {
	tests := []struct {
		name       string
		old        string
		new        string
		occurrence int
		finding    policyFinding
	}{
		{
			name: "native dry run", old: "      - run: make test-race\n", new: "      - run: make -n test-race\n",
			finding: policyFinding{Path: ".github/workflows/ci.yml", Line: 26, Rule: "workflow.ci_native_command", Message: `native job is missing unconditional command "make test-race"`},
		},
		{
			name: "quality alternate directory", old: "      - run: make check\n", new: "      - run: make -C other check\n", occurrence: 1,
			finding: policyFinding{Path: ".github/workflows/ci.yml", Line: 7, Rule: "workflow.ci_quality", Message: "quality job must run on ubuntu-24.04 and execute make check and make supply-chain"},
		},
		{
			name: "oldstable alternate makefile", old: "      - run: make check\n", new: "      - run: make --file=other.mk check\n", occurrence: 2,
			finding: policyFinding{Path: ".github/workflows/ci.yml", Line: 57, Rule: "workflow.ci_oldstable_check", Message: "oldstable job must execute unconditional make check"},
		},
		{
			name: "quality job makeflags", old: "    timeout-minutes: 20\n", new: "    timeout-minutes: 20\n    env:\n      MAKEFLAGS: -n\n", occurrence: 1,
			finding: policyFinding{Path: ".github/workflows/ci.yml", Line: 7, Rule: "workflow.ci_quality", Message: "quality job must run on ubuntu-24.04 and execute make check and make supply-chain"},
		},
		{
			name: "quality job makeflags expression", old: "    timeout-minutes: 20\n", new: "    timeout-minutes: 20\n    env:\n      MAKEFLAGS: ${{ vars.MAKEFLAGS }}\n", occurrence: 1,
			finding: policyFinding{Path: ".github/workflows/ci.yml", Line: 7, Rule: "workflow.ci_quality", Message: "quality job must run on ubuntu-24.04 and execute make check and make supply-chain"},
		},
		{
			name: "native step makeflags", old: "      - run: make test-race\n", new: "      - run: make test-race\n        env:\n          MAKEFLAGS: --dry-run\n",
			finding: policyFinding{Path: ".github/workflows/ci.yml", Line: 26, Rule: "workflow.ci_native_command", Message: `native job is missing unconditional command "make test-race"`},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			root := prepareFixture(t, "valid")
			if test.occurrence > 0 {
				replacePolicyFileOccurrence(t, root, ".github/workflows/ci.yml", test.old, test.new, test.occurrence)
			} else {
				replacePolicyFile(t, root, ".github/workflows/ci.yml", test.old, test.new)
			}
			assertPolicyFinding(t, root, test.finding)
		})
	}
}

func TestCIMakeFlagsEnvironmentPrecedence(t *testing.T) {
	const (
		path        = ".github/workflows/ci.yml"
		topAnchor   = "permissions:\n  contents: read\n"
		qualityEnv  = "    timeout-minutes: 20\n    env:\n      MAKEFLAGS: %s\n"
		qualityRule = "workflow.ci_quality"
		qualityMsg  = "quality job must run on ubuntu-24.04 and execute make check and make supply-chain"
	)
	qualityFinding := func(line int) policyFinding {
		return policyFinding{Path: path, Line: line, Rule: qualityRule, Message: qualityMsg}
	}

	t.Run("top-level unsafe value is inherited", func(t *testing.T) {
		root := prepareFixture(t, "valid")
		replacePolicyFile(t, root, path, topAnchor, topAnchor+"env:\n  MAKEFLAGS: -n\n")
		assertPolicyFinding(t, root, qualityFinding(9))
	})

	t.Run("job empty value overrides top-level unsafe value", func(t *testing.T) {
		root := prepareFixture(t, "valid")
		replacePolicyFile(t, root, path, topAnchor, topAnchor+"env:\n  MAKEFLAGS: -n\n")
		replacePolicyFileOccurrence(t, root, path, "    timeout-minutes: 20\n", fmt.Sprintf(qualityEnv, `""`), 1)
		assertCIQualityRuleAbsent(t, root)
		assertPolicyFinding(t, root, policyFinding{
			Path: path, Line: 30, Rule: "workflow.ci_native_command",
			Message: `native job is missing unconditional command "make fmt-check"`,
		})
	})

	t.Run("job unsafe value overrides top-level empty value", func(t *testing.T) {
		root := prepareFixture(t, "valid")
		replacePolicyFile(t, root, path, topAnchor, topAnchor+"env:\n  MAKEFLAGS: \"\"\n")
		replacePolicyFileOccurrence(t, root, path, "    timeout-minutes: 20\n", fmt.Sprintf(qualityEnv, "--dry-run"), 1)
		assertPolicyFinding(t, root, qualityFinding(9))
	})

	t.Run("step empty values override job unsafe value", func(t *testing.T) {
		root := prepareFixture(t, "valid")
		replacePolicyFileOccurrence(t, root, path, "    timeout-minutes: 20\n", fmt.Sprintf(qualityEnv, "-n"), 1)
		for _, target := range []string{"check", "lint", "fuzz-smoke", "supply-chain"} {
			if target == "check" {
				replacePolicyFileOccurrence(t, root, path,
					"      - run: make check\n        env:\n",
					"      - run: make check\n        env:\n          MAKEFLAGS: \"\"\n",
					1,
				)
				continue
			}
			replacePolicyFileOccurrence(t, root, path, "      - run: make "+target+"\n", "      - run: make "+target+"\n        env:\n          MAKEFLAGS: \"\"\n", 1)
		}
		assertCIQualityRuleAbsent(t, root)
	})

	t.Run("step unsafe value overrides job empty value", func(t *testing.T) {
		root := prepareFixture(t, "valid")
		replacePolicyFileOccurrence(t, root, path, "    timeout-minutes: 20\n", fmt.Sprintf(qualityEnv, `""`), 1)
		replacePolicyFileOccurrence(t, root, path,
			"      - run: make check\n        env:\n",
			"      - run: make check\n        env:\n          MAKEFLAGS: --dry-run\n",
			1,
		)
		assertPolicyFinding(t, root, qualityFinding(7))
	})
}

func TestCIRequiredCommandCreditRejectsUnsafeRunContexts(t *testing.T) {
	const (
		path       = ".github/workflows/ci.yml"
		qualityOld = "      - run: make check\n"
		qualityMsg = "quality job must run on ubuntu-24.04 and execute make check and make supply-chain"
	)
	tests := []struct {
		name       string
		old        string
		new        string
		occurrence int
		line       int
		rule       string
		message    string
	}{
		{
			name: "step custom shell", old: qualityOld,
			new: "      - shell: /bin/true {0}\n        run: make check\n", occurrence: 1,
			line: 7, rule: "workflow.ci_quality", message: qualityMsg,
		},
		{
			name: "step working directory", old: qualityOld,
			new: "      - working-directory: other\n        run: make check\n", occurrence: 1,
			line: 7, rule: "workflow.ci_quality", message: qualityMsg,
		},
		{
			name: "workflow default custom shell", old: "jobs:\n",
			new: "defaults:\n  run:\n    shell: /bin/true {0}\njobs:\n", occurrence: 1,
			line: 10, rule: "workflow.ci_quality", message: qualityMsg,
		},
		{
			name: "workflow default working directory", old: "jobs:\n",
			new: "defaults:\n  run:\n    working-directory: other\njobs:\n", occurrence: 1,
			line: 10, rule: "workflow.ci_quality", message: qualityMsg,
		},
		{
			name: "job default custom shell", old: "    timeout-minutes: 20\n",
			new: "    timeout-minutes: 20\n    defaults:\n      run:\n        shell: /bin/true {0}\n", occurrence: 1,
			line: 7, rule: "workflow.ci_quality", message: qualityMsg,
		},
		{
			name: "job default working directory", old: "    timeout-minutes: 20\n",
			new: "    timeout-minutes: 20\n    defaults:\n      run:\n        working-directory: other\n", occurrence: 1,
			line: 7, rule: "workflow.ci_quality", message: qualityMsg,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			root := prepareFixture(t, "valid")
			replacePolicyFileOccurrence(t, root, path, test.old, test.new, test.occurrence)
			assertPolicyFinding(t, root, policyFinding{
				Path: path, Line: test.line, Rule: test.rule, Message: test.message,
			})
		})
	}
}

func TestCIRequiredCommandCreditRejectsUnapprovedEnvironmentKeys(t *testing.T) {
	const (
		path    = ".github/workflows/ci.yml"
		rule    = "workflow.ci_quality"
		message = "quality job must run on ubuntu-24.04 and execute make check and make supply-chain"
	)
	tests := []struct {
		name string
		old  string
		new  string
		line int
	}{
		{
			name: "workflow PATH", old: "permissions:\n  contents: read\n",
			new: "permissions:\n  contents: read\nenv:\n  PATH: /tmp/ci-shims\n", line: 9,
		},
		{
			name: "job BASH_ENV", old: "    timeout-minutes: 20\n",
			new: "    timeout-minutes: 20\n    env:\n      BASH_ENV: /tmp/ci-env\n", line: 7,
		},
		{
			name: "step PATH", old: "      - run: make check\n        env:\n",
			new: "      - run: make check\n        env:\n          PATH: /tmp/ci-shims\n", line: 7,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			root := prepareFixture(t, "valid")
			replacePolicyFileOccurrence(t, root, path, test.old, test.new, 1)
			assertPolicyFinding(t, root, policyFinding{Path: path, Line: test.line, Rule: rule, Message: message})
		})
	}
}

func TestCIQualityAllowsExactTrustedPersistentTestRootPreparation(t *testing.T) {
	const step = `      - name: Prepare trusted persistent test root
        run: |
          set -euo pipefail
          if test "${RUNNER_OS}" = Linux; then
            sudo install -d -o root -g root -m 0755 /var/lib/amsftp-tests
            sudo install -d -o "$(id -u)" -g "$(id -g)" -m 0700 "/var/lib/amsftp-tests/$(id -u)"
          fi
`
	root := prepareFixture(t, "valid")
	path := filepath.Join(root, ".github", "workflows", "ci.yml")
	content, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	updated := strings.Replace(string(content), "      - run: make check\n", step+"      - run: make check\n", 1)
	if updated == string(content) {
		t.Fatal("quality make check anchor not found")
	}
	assertNoWorkflowRule(t, ".github/workflows/ci.yml", updated, "workflow.ci_quality")
}

func TestCIMakeControlEnvironmentPrecedence(t *testing.T) {
	const (
		path        = ".github/workflows/ci.yml"
		topAnchor   = "permissions:\n  contents: read\n"
		qualityRule = "workflow.ci_quality"
		qualityMsg  = "quality job must run on ubuntu-24.04 and execute make check and make supply-chain"
	)
	for _, variable := range []string{"GNUMAKEFLAGS", "MAKEFILES"} {
		t.Run(variable, func(t *testing.T) {
			t.Run("top-level unsafe value is inherited", func(t *testing.T) {
				root := prepareFixture(t, "valid")
				replacePolicyFile(t, root, path, topAnchor, topAnchor+"env:\n  "+variable+": -n\n")
				assertPolicyFinding(t, root, policyFinding{Path: path, Line: 9, Rule: qualityRule, Message: qualityMsg})
			})

			t.Run("job empty value overrides top-level unsafe value", func(t *testing.T) {
				root := prepareFixture(t, "valid")
				replacePolicyFile(t, root, path, topAnchor, topAnchor+"env:\n  "+variable+": -n\n")
				replacement := "    timeout-minutes: 20\n    env:\n      " + variable + ": \"\"\n"
				replacePolicyFileOccurrence(t, root, path, "    timeout-minutes: 20\n", replacement, 1)
				assertCIQualityRuleAbsent(t, root)
			})

			t.Run("step empty value overrides job unsafe value", func(t *testing.T) {
				root := prepareFixture(t, "valid")
				replacement := "    timeout-minutes: 20\n    env:\n      " + variable + ": -n\n"
				replacePolicyFileOccurrence(t, root, path, "    timeout-minutes: 20\n", replacement, 1)
				for _, target := range []string{"check", "lint", "fuzz-smoke", "supply-chain"} {
					if target == "check" {
						old := "      - run: make check\n        env:\n"
						newValue := old + "          " + variable + ": \"\"\n"
						replacePolicyFileOccurrence(t, root, path, old, newValue, 1)
						continue
					}
					old := "      - run: make " + target + "\n"
					newValue := old + "        env:\n          " + variable + ": \"\"\n"
					replacePolicyFileOccurrence(t, root, path, old, newValue, 1)
				}
				assertCIQualityRuleAbsent(t, root)
			})

			t.Run("step unsafe value overrides job empty value", func(t *testing.T) {
				root := prepareFixture(t, "valid")
				replacement := "    timeout-minutes: 20\n    env:\n      " + variable + ": \"\"\n"
				replacePolicyFileOccurrence(t, root, path, "    timeout-minutes: 20\n", replacement, 1)
				old := "      - run: make check\n        env:\n"
				newValue := old + "          " + variable + ": -n\n"
				replacePolicyFileOccurrence(t, root, path, old, newValue, 1)
				assertPolicyFinding(t, root, policyFinding{Path: path, Line: 7, Rule: qualityRule, Message: qualityMsg})
			})
		})
	}
}

func TestCINativeMatrix(t *testing.T) {
	const (
		exactMatrix = "        os: [ubuntu-22.04, ubuntu-24.04, macos-15, macos-15-intel]"
		message     = "native job must run exactly the ubuntu-22.04, ubuntu-24.04, macos-15, and macos-15-intel matrix via matrix.os"
	)
	tests := []struct {
		name string
		old  string
		new  string
	}{
		{name: "missing ubuntu 22", old: exactMatrix, new: "        os: [ubuntu-24.04, macos-15, macos-15-intel]"},
		{name: "missing ubuntu 24", old: exactMatrix, new: "        os: [ubuntu-22.04, macos-15, macos-15-intel]"},
		{name: "missing macos arm64", old: exactMatrix, new: "        os: [ubuntu-22.04, ubuntu-24.04, macos-15-intel]"},
		{name: "missing macos intel", old: exactMatrix, new: "        os: [ubuntu-22.04, ubuntu-24.04, macos-15]"},
		{name: "extra os", old: exactMatrix, new: "        os: [ubuntu-22.04, ubuntu-24.04, macos-15, macos-15-intel, ubuntu-26.04]"},
		{name: "duplicate os", old: exactMatrix, new: "        os: [ubuntu-22.04, ubuntu-24.04, macos-15, macos-15]"},
		{name: "quoted os", old: exactMatrix, new: "        os: [ubuntu-22.04, ubuntu-24.04, macos-15, 'macos-15-intel']"},
		{name: "mapping instead of sequence", old: exactMatrix, new: "        os: {ubuntu-22.04: true, ubuntu-24.04: true, macos-15: true, macos-15-intel: true}"},
		{name: "quoted runs on", old: "    runs-on: ${{ matrix.os }}\n", new: "    runs-on: \"${{ matrix.os }}\"\n"},
		{name: "wrong runs on", old: "    runs-on: ${{ matrix.os }}\n", new: "    runs-on: ubuntu-24.04\n"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			root := prepareFixture(t, "valid")
			replacePolicyFileOccurrence(t, root, ".github/workflows/ci.yml", test.old, test.new, 1)
			assertPolicyFinding(t, root, policyFinding{
				Path: ".github/workflows/ci.yml", Line: 26, Rule: "workflow.ci_native_matrix",
				Message: message,
			})
		})
	}
}

func TestCIExactOSMatricesAreAccepted(t *testing.T) {
	root := prepareFixture(t, "valid")
	assertNoPolicyFindings(t, root)
}

func TestCINativeCommands(t *testing.T) {
	tests := []struct {
		name    string
		old     string
		new     string
		command string
	}{
		{name: "fmt", old: "      - run: make fmt-check\n", new: "      - run: echo make fmt-check\n", command: "make fmt-check"},
		{name: "vet", old: "      - run: make vet\n", new: "      - run: echo make vet\n", command: "make vet"},
		{name: "test", old: "      - run: make test\n", new: "      - run: echo make test\n", command: "make test"},
		{name: "contract", old: "      - run: make test-contract\n", new: "      - run: echo make test-contract\n", command: "make test-contract"},
		{name: "race", old: "      - run: make test-race\n", new: "      - run: make test-race-disabled\n", command: "make test-race"},
		{name: "conditional race", old: "      - run: make test-race\n", new: "      - if: always()\n        run: make test-race\n", command: "make test-race"},
		{name: "build", old: "      - run: go build -trimpath -o \"${{ runner.temp }}/native/bin/amsftp\" ./cmd/amsftp\n", new: "      - run: echo go build -trimpath -o \"${{ runner.temp }}/native/bin/amsftp\" ./cmd/amsftp\n", command: "go build ./cmd/amsftp to runner.temp"},
		{name: "help", old: "      - run: '\"${{ runner.temp }}/native/bin/amsftp\" --help'\n", new: "      - run: echo \"${{ runner.temp }}/native/bin/amsftp\" --help\n", command: "runner.temp binary --help"},
		{name: "version", old: "      - run: '\"${{ runner.temp }}/native/bin/amsftp\" --version'\n", new: "      - run: echo \"${{ runner.temp }}/native/bin/amsftp\" --version\n", command: "runner.temp binary --version"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			root := prepareFixture(t, "valid")
			replacePolicyFile(t, root, ".github/workflows/ci.yml",
				canonicalCICommandMutation(test.old), canonicalCICommandMutation(test.new))
			assertPolicyFinding(t, root, policyFinding{
				Path: ".github/workflows/ci.yml", Line: 26, Rule: "workflow.ci_native_command",
				Message: fmt.Sprintf("native job is missing unconditional command %q", test.command),
			})
		})
	}
}

func TestCIOldstableContract(t *testing.T) {
	const (
		exactMatrix = "        os: [ubuntu-22.04, ubuntu-24.04, macos-15, macos-15-intel]"
		message     = "oldstable job must run exactly the ubuntu-22.04, ubuntu-24.04, macos-15, and macos-15-intel matrix via matrix.os"
	)
	tests := []struct {
		name    string
		old     string
		new     string
		rule    string
		message string
	}{
		{name: "missing ubuntu 22", old: exactMatrix, new: "        os: [ubuntu-24.04, macos-15, macos-15-intel]", rule: "workflow.ci_oldstable_matrix", message: message},
		{name: "missing macos intel", old: exactMatrix, new: "        os: [ubuntu-22.04, ubuntu-24.04, macos-15]", rule: "workflow.ci_oldstable_matrix", message: message},
		{name: "extra os", old: exactMatrix, new: "        os: [ubuntu-22.04, ubuntu-24.04, macos-15, macos-15-intel, ubuntu-26.04]", rule: "workflow.ci_oldstable_matrix", message: message},
		{name: "duplicate os", old: exactMatrix, new: "        os: [ubuntu-22.04, ubuntu-24.04, macos-15, macos-15]", rule: "workflow.ci_oldstable_matrix", message: message},
		{name: "quoted os", old: exactMatrix, new: "        os: [ubuntu-22.04, ubuntu-24.04, macos-15, 'macos-15-intel']", rule: "workflow.ci_oldstable_matrix", message: message},
		{name: "mapping instead of sequence", old: exactMatrix, new: "        os: {ubuntu-22.04: true, ubuntu-24.04: true, macos-15: true, macos-15-intel: true}", rule: "workflow.ci_oldstable_matrix", message: message},
		{name: "wrong version", old: "          go-version: 1.25.12\n", new: "          go-version: 1.26.5\n", rule: "workflow.ci_oldstable_toolchain", message: "oldstable job must select Go 1.25.12 with actions/setup-go"},
		{name: "local only at step", old: "    env:\n      GOTOOLCHAIN: local\n", new: "", rule: "workflow.ci_oldstable_local", message: "oldstable job must set job-level GOTOOLCHAIN to local"},
		{name: "missing check", old: "      - run: make check\n", new: "      - run: echo make check\n", rule: "workflow.ci_oldstable_check", message: "oldstable job must execute unconditional make check"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			root := prepareFixture(t, "valid")
			content := readPolicyFile(t, root, ".github/workflows/ci.yml")
			if strings.Count(content, test.old) > 1 && strings.Contains(test.old, "os:") {
				first := strings.Index(content, test.old)
				secondRelative := strings.Index(content[first+len(test.old):], test.old)
				if secondRelative < 0 {
					t.Fatalf("oldstable matrix occurrence not found")
				}
				second := first + len(test.old) + secondRelative
				content = content[:second] + strings.Replace(content[second:], test.old, test.new, 1)
				writePolicyFile(t, root, ".github/workflows/ci.yml", content)
			} else if test.name == "missing check" {
				replacePolicyFileOccurrence(t, root, ".github/workflows/ci.yml", test.old, test.new, 2)
			} else {
				replacePolicyFile(t, root, ".github/workflows/ci.yml", test.old, test.new)
			}
			assertPolicyFinding(t, root, policyFinding{Path: ".github/workflows/ci.yml", Line: 57, Rule: test.rule, Message: test.message})
		})
	}
}

func TestCIOldstableSetupMustBeUnconditionalAndPrecedeCheck(t *testing.T) {
	setup := "      - uses: actions/setup-go@" + setupGoSHA + "\n        with:\n          go-version: 1.25.12\n          cache: true\n          cache-dependency-path: \"go.sum\\ntools/go.sum\"\n"
	check := "      - run: make check\n        env:\n          BUILD_DIR: ${{ runner.temp }}/oldstable/build\n          COVERAGE_DIR: ${{ runner.temp }}/oldstable/coverage\n"
	tests := []struct {
		name        string
		replacement string
	}{
		{
			name: "false condition before uses",
			replacement: "      - if: false\n        uses: actions/setup-go@" + setupGoSHA +
				"\n        with:\n          go-version: 1.25.12\n          cache: true\n          cache-dependency-path: \"go.sum\\ntools/go.sum\"\n" + check,
		},
		{
			name:        "empty condition after with",
			replacement: setup + "        if:\n" + check,
		},
		{
			name:        "setup after check",
			replacement: check + setup,
		},
		{
			name: "wrong version before correct setup",
			replacement: "      - uses: actions/setup-go@" + setupGoSHA +
				"\n        with:\n          go-version: 1.26.5\n          cache: true\n          cache-dependency-path: \"go.sum\\ntools/go.sum\"\n" + setup + check,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			root := prepareFixture(t, "valid")
			replacePolicyFile(t, root, ".github/workflows/ci.yml", setup+check, test.replacement)
			want := policyFinding{
				Path: ".github/workflows/ci.yml", Line: 57, Rule: "workflow.ci_oldstable_toolchain",
				Message: "oldstable job must select Go 1.25.12 with actions/setup-go",
			}
			assertPolicyFinding(t, root, want)
		})
	}
}

func TestCIOldstableTrustedPrefixAllowsOnlySuffixExtensions(t *testing.T) {
	setup := "      - uses: actions/setup-go@" + setupGoSHA + "\n        with:\n          go-version: 1.25.12\n          cache: true\n          cache-dependency-path: \"go.sum\\ntools/go.sum\"\n"
	check := "      - run: make check\n        env:\n          BUILD_DIR: ${{ runner.temp }}/oldstable/build\n          COVERAGE_DIR: ${{ runner.temp }}/oldstable/coverage\n"
	tests := []struct {
		name        string
		replacement string
		wantFinding bool
	}{
		{name: "unknown run before check", replacement: setup + "      - run: echo prefix\n" + check, wantFinding: true},
		{name: "unknown action before check", replacement: setup + "      - uses: actions/setup-go@" + setupGoSHA + "\n        with:\n          go-version: 1.26.5\n" + check, wantFinding: true},
		{name: "run after check", replacement: setup + check + "      - run: echo suffix\n"},
		{name: "pinned action after check", replacement: setup + check + "      - uses: actions/setup-go@" + setupGoSHA + "\n        with:\n          go-version: 1.26.5\n"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			root := prepareFixture(t, "valid")
			replacePolicyFile(t, root, ".github/workflows/ci.yml", setup+check, test.replacement)
			want := policyFinding{
				Path: ".github/workflows/ci.yml", Line: 57, Rule: "workflow.ci_oldstable_toolchain",
				Message: "oldstable job must select Go 1.25.12 with actions/setup-go",
			}
			if test.wantFinding {
				assertPolicyFinding(t, root, want)
				return
			}
			assertNoPolicyFindings(t, root)
		})
	}
}

func TestCIOldstableCheckRequiresEffectiveLocalToolchain(t *testing.T) {
	const (
		path    = ".github/workflows/ci.yml"
		oldRun  = "      - run: make check\n        env:\n          BUILD_DIR: ${{ runner.temp }}/oldstable/build\n          COVERAGE_DIR: ${{ runner.temp }}/oldstable/coverage\n"
		message = "oldstable job must execute unconditional make check"
	)
	tests := []struct {
		name  string
		value string
	}{
		{name: "auto", value: "auto"},
		{name: "expression", value: "${{ vars.GOTOOLCHAIN }}"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			root := prepareFixture(t, "valid")
			newRun := strings.Replace(oldRun, "        env:\n", "        env:\n          GOTOOLCHAIN: "+test.value+"\n", 1)
			replacePolicyFile(t, root, path, oldRun, newRun)
			assertPolicyFinding(t, root, policyFinding{
				Path: path, Line: 57, Rule: "workflow.ci_oldstable_check", Message: message,
			})
		})
	}

	t.Run("explicit local remains credited", func(t *testing.T) {
		root := prepareFixture(t, "valid")
		newRun := strings.Replace(oldRun, "        env:\n", "        env:\n          GOTOOLCHAIN: local\n", 1)
		replacePolicyFile(t, root, path, oldRun, newRun)
		assertNoPolicyFindings(t, root)
	})
}

func TestCIBuildMatrix(t *testing.T) {
	tests := []struct {
		name string
		old  string
		new  string
	}{
		{name: "missing darwin arm64", old: "          - goos: darwin\n            goarch: arm64\n            artifact: amsftp-darwin-arm64\n", new: ""},
		{name: "missing darwin amd64", old: "          - goos: darwin\n            goarch: amd64\n            artifact: amsftp-darwin-amd64\n", new: ""},
		{name: "missing linux arm64", old: "          - goos: linux\n            goarch: arm64\n            artifact: amsftp-linux-arm64\n", new: ""},
		{name: "missing linux amd64", old: "          - goos: linux\n            goarch: amd64\n            artifact: amsftp-linux-amd64\n", new: ""},
		{name: "artifact prefix", old: "artifact: amsftp-darwin-arm64", new: "artifact: amsftp-darwin-arm64-debug"},
		{name: "duplicate tuple", old: "          - goos: linux\n            goarch: amd64\n            artifact: amsftp-linux-amd64\n", new: "          - goos: linux\n            goarch: arm64\n            artifact: amsftp-linux-arm64\n"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			root := prepareFixture(t, "valid")
			replacePolicyFile(t, root, ".github/workflows/ci.yml", test.old, test.new)
			assertPolicyFinding(t, root, policyFinding{
				Path: ".github/workflows/ci.yml", Line: 79, Rule: "workflow.ci_build_matrix",
				Message: "build job matrix must contain exactly the four approved GOOS/GOARCH/artifact tuples",
			})
		})
	}
}

func TestCIBuildCommand(t *testing.T) {
	tests := []struct {
		name string
		old  string
		new  string
	}{
		{name: "missing cgo", old: "          CGO_ENABLED: \"0\"\n", new: ""},
		{name: "wrong goos", old: "          GOOS: ${{ matrix.goos }}\n", new: "          GOOS: linux\n"},
		{name: "wrong goarch", old: "          GOARCH: ${{ matrix.goarch }}\n", new: "          GOARCH: amd64\n"},
		{name: "missing trimpath", old: "go build -trimpath -buildvcs=false -o \"${{ runner.temp }}/build/${{ matrix.artifact }}/${{ matrix.artifact }}\"", new: "go build -buildvcs=false -o \"${{ runner.temp }}/build/${{ matrix.artifact }}/${{ matrix.artifact }}\""},
		{name: "missing disabled vcs stamping", old: "go build -trimpath -buildvcs=false -o \"${{ runner.temp }}/build/${{ matrix.artifact }}/${{ matrix.artifact }}\"", new: "go build -trimpath -o \"${{ runner.temp }}/build/${{ matrix.artifact }}/${{ matrix.artifact }}\""},
		{name: "enabled vcs stamping", old: "go build -trimpath -buildvcs=false -o \"${{ runner.temp }}/build/${{ matrix.artifact }}/${{ matrix.artifact }}\"", new: "go build -trimpath -buildvcs=true -o \"${{ runner.temp }}/build/${{ matrix.artifact }}/${{ matrix.artifact }}\""},
		{name: "split vcs stamping value", old: "go build -trimpath -buildvcs=false -o \"${{ runner.temp }}/build/${{ matrix.artifact }}/${{ matrix.artifact }}\"", new: "go build -trimpath -buildvcs false -o \"${{ runner.temp }}/build/${{ matrix.artifact }}/${{ matrix.artifact }}\""},
		{name: "duplicate disabled vcs stamping", old: "go build -trimpath -buildvcs=false -o \"${{ runner.temp }}/build/${{ matrix.artifact }}/${{ matrix.artifact }}\"", new: "go build -trimpath -buildvcs=false -buildvcs=false -o \"${{ runner.temp }}/build/${{ matrix.artifact }}/${{ matrix.artifact }}\""},
		{name: "missing runner temp", old: "${{ runner.temp }}/build/${{ matrix.artifact }}/${{ matrix.artifact }}", new: "dist/${{ matrix.artifact }}"},
		{name: "echo spoof", old: "      - run: go build -trimpath -buildvcs=false -o \"${{ runner.temp }}/build/${{ matrix.artifact }}/${{ matrix.artifact }}\"", new: "      - run: echo go build -trimpath -buildvcs=false -o \"${{ runner.temp }}/build/${{ matrix.artifact }}/${{ matrix.artifact }}\""},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			root := prepareFixture(t, "valid")
			replacePolicyFile(t, root, ".github/workflows/ci.yml", test.old, test.new)
			assertPolicyFinding(t, root, policyFinding{
				Path: ".github/workflows/ci.yml", Line: 79, Rule: "workflow.ci_build_command",
				Message: "build job must cross-build each matrix tuple with CGO_ENABLED=0, -trimpath, and -buildvcs=false into runner.temp",
			})
		})
	}
}

func TestCIBuildCommandAcceptsDisabledVCSStamping(t *testing.T) {
	root := prepareFixture(t, "valid")
	assertCIPolicyRuleAbsent(t, root, "workflow.ci_build_command")
}

func TestCINativeAndBuildCommandsRejectUnsafeExecution(t *testing.T) {
	tests := []struct {
		name    string
		old     string
		new     string
		line    int
		rule    string
		message string
	}{
		{
			name: "native build masks failure",
			old:  "      - run: go build -o \"${{ runner.temp }}/amsftp\" ./cmd/amsftp\n",
			new:  "      - run: go build -o \"${{ runner.temp }}/amsftp\" ./cmd/amsftp || true\n",
			line: 26, rule: "workflow.ci_native_command", message: `native job is missing unconditional command "go build ./cmd/amsftp to runner.temp"`,
		},
		{
			name: "native smoke masks failure",
			old:  "      - run: '\"${{ runner.temp }}/amsftp\" --help'\n",
			new:  "      - run: '\"${{ runner.temp }}/amsftp\" --help || true'\n",
			line: 26, rule: "workflow.ci_native_command", message: `native job is missing unconditional command "runner.temp binary --help"`,
		},
		{
			name: "cross build masks failure",
			old:  "      - run: go build -trimpath -buildvcs=false -o \"${{ runner.temp }}/${{ matrix.artifact }}\" ./cmd/amsftp\n",
			new:  "      - run: go build -trimpath -buildvcs=false -o \"${{ runner.temp }}/${{ matrix.artifact }}\" ./cmd/amsftp || true\n",
			line: 79, rule: "workflow.ci_build_command", message: "build job must cross-build each matrix tuple with CGO_ENABLED=0, -trimpath, and -buildvcs=false into runner.temp",
		},
		{
			name: "native build custom shell",
			old:  "      - run: go build -o \"${{ runner.temp }}/amsftp\" ./cmd/amsftp\n",
			new:  "      - shell: /bin/true {0}\n        run: go build -o \"${{ runner.temp }}/amsftp\" ./cmd/amsftp\n",
			line: 26, rule: "workflow.ci_native_command", message: `native job is missing unconditional command "go build ./cmd/amsftp to runner.temp"`,
		},
		{
			name: "native smoke working directory",
			old:  "      - run: '\"${{ runner.temp }}/amsftp\" --help'\n",
			new:  "      - working-directory: other\n        run: '\"${{ runner.temp }}/amsftp\" --help'\n",
			line: 26, rule: "workflow.ci_native_command", message: `native job is missing unconditional command "runner.temp binary --help"`,
		},
		{
			name: "cross build custom shell",
			old:  "      - run: go build -trimpath -buildvcs=false -o \"${{ runner.temp }}/${{ matrix.artifact }}\" ./cmd/amsftp\n",
			new:  "      - shell: /bin/true {0}\n        run: go build -trimpath -buildvcs=false -o \"${{ runner.temp }}/${{ matrix.artifact }}\" ./cmd/amsftp\n",
			line: 79, rule: "workflow.ci_build_command", message: "build job must cross-build each matrix tuple with CGO_ENABLED=0, -trimpath, and -buildvcs=false into runner.temp",
		},
		{
			name: "native build pipeline masks failure",
			old:  "      - run: go build -o \"${{ runner.temp }}/amsftp\" ./cmd/amsftp\n",
			new:  "      - run: go build -o \"${{ runner.temp }}/amsftp\" ./cmd/amsftp | true\n",
			line: 26, rule: "workflow.ci_native_command", message: `native job is missing unconditional command "go build ./cmd/amsftp to runner.temp"`,
		},
		{
			name: "native smoke background masks failure",
			old:  "      - run: '\"${{ runner.temp }}/amsftp\" --help'\n",
			new:  "      - run: '\"${{ runner.temp }}/amsftp\" --help &'\n",
			line: 26, rule: "workflow.ci_native_command", message: `native job is missing unconditional command "runner.temp binary --help"`,
		},
		{
			name: "native build dry run short flag",
			old:  "      - run: go build -o \"${{ runner.temp }}/amsftp\" ./cmd/amsftp\n",
			new:  "      - run: go build -n -o \"${{ runner.temp }}/amsftp\" ./cmd/amsftp\n",
			line: 26, rule: "workflow.ci_native_command", message: `native job is missing unconditional command "go build ./cmd/amsftp to runner.temp"`,
		},
		{
			name: "native build dry run equals flag",
			old:  "      - run: go build -o \"${{ runner.temp }}/amsftp\" ./cmd/amsftp\n",
			new:  "      - run: go build -n=true -o \"${{ runner.temp }}/amsftp\" ./cmd/amsftp\n",
			line: 26, rule: "workflow.ci_native_command", message: `native job is missing unconditional command "go build ./cmd/amsftp to runner.temp"`,
		},
		{
			name: "native build rejects unapproved flag",
			old:  "      - run: go build -o \"${{ runner.temp }}/amsftp\" ./cmd/amsftp\n",
			new:  "      - run: go build -v -o \"${{ runner.temp }}/amsftp\" ./cmd/amsftp\n",
			line: 26, rule: "workflow.ci_native_command", message: `native job is missing unconditional command "go build ./cmd/amsftp to runner.temp"`,
		},
		{
			name: "native build rejects disabled vcs stamping",
			old:  "      - run: go build -trimpath -o \"${{ runner.temp }}/amsftp\" ./cmd/amsftp\n",
			new:  "      - run: go build -trimpath -buildvcs=false -o \"${{ runner.temp }}/amsftp\" ./cmd/amsftp\n",
			line: 26, rule: "workflow.ci_native_command", message: `native job is missing unconditional command "go build ./cmd/amsftp to runner.temp"`,
		},
		{
			name: "native build rejects duplicate output",
			old:  "      - run: go build -o \"${{ runner.temp }}/amsftp\" ./cmd/amsftp\n",
			new:  "      - run: go build -o \"${{ runner.temp }}/first\" -o \"${{ runner.temp }}/amsftp\" ./cmd/amsftp\n",
			line: 26, rule: "workflow.ci_native_command", message: `native job is missing unconditional command "go build ./cmd/amsftp to runner.temp"`,
		},
		{
			name: "native build rejects extra target",
			old:  "      - run: go build -o \"${{ runner.temp }}/amsftp\" ./cmd/amsftp\n",
			new:  "      - run: go build -o \"${{ runner.temp }}/amsftp\" ./cmd/amsftp ./cmd/other\n",
			line: 26, rule: "workflow.ci_native_command", message: `native job is missing unconditional command "go build ./cmd/amsftp to runner.temp"`,
		},
		{
			name: "cross build dry run short flag",
			old:  "      - run: go build -trimpath -buildvcs=false -o \"${{ runner.temp }}/${{ matrix.artifact }}\" ./cmd/amsftp\n",
			new:  "      - run: go build -n -trimpath -buildvcs=false -o \"${{ runner.temp }}/${{ matrix.artifact }}\" ./cmd/amsftp\n",
			line: 79, rule: "workflow.ci_build_command", message: "build job must cross-build each matrix tuple with CGO_ENABLED=0, -trimpath, and -buildvcs=false into runner.temp",
		},
		{
			name: "cross build dry run equals flag",
			old:  "      - run: go build -trimpath -buildvcs=false -o \"${{ runner.temp }}/${{ matrix.artifact }}\" ./cmd/amsftp\n",
			new:  "      - run: go build -n=true -trimpath -buildvcs=false -o \"${{ runner.temp }}/${{ matrix.artifact }}\" ./cmd/amsftp\n",
			line: 79, rule: "workflow.ci_build_command", message: "build job must cross-build each matrix tuple with CGO_ENABLED=0, -trimpath, and -buildvcs=false into runner.temp",
		},
		{
			name: "cross build rejects duplicate output",
			old:  "      - run: go build -trimpath -buildvcs=false -o \"${{ runner.temp }}/${{ matrix.artifact }}\" ./cmd/amsftp\n",
			new:  "      - run: go build -trimpath -buildvcs=false -o \"${{ runner.temp }}/first\" -o \"${{ runner.temp }}/${{ matrix.artifact }}\" ./cmd/amsftp\n",
			line: 79, rule: "workflow.ci_build_command", message: "build job must cross-build each matrix tuple with CGO_ENABLED=0, -trimpath, and -buildvcs=false into runner.temp",
		},
		{
			name: "cross build rejects extra target",
			old:  "      - run: go build -trimpath -buildvcs=false -o \"${{ runner.temp }}/${{ matrix.artifact }}\" ./cmd/amsftp\n",
			new:  "      - run: go build -trimpath -buildvcs=false -o \"${{ runner.temp }}/${{ matrix.artifact }}\" ./cmd/amsftp ./cmd/other\n",
			line: 79, rule: "workflow.ci_build_command", message: "build job must cross-build each matrix tuple with CGO_ENABLED=0, -trimpath, and -buildvcs=false into runner.temp",
		},
		{
			name: "combined smoke cannot receive duplicate credit",
			old:  "      - run: '\"${{ runner.temp }}/amsftp\" --help'\n      - run: '\"${{ runner.temp }}/amsftp\" --version'\n",
			new:  "      - run: '\"${{ runner.temp }}/amsftp\" --help --version'\n",
			line: 26, rule: "workflow.ci_native_command", message: `native job is missing unconditional command "runner.temp binary --help"`,
		},
		{
			name: "smoke rejects extra flag",
			old:  "      - run: '\"${{ runner.temp }}/amsftp\" --help'\n",
			new:  "      - run: '\"${{ runner.temp }}/amsftp\" --help --verbose'\n",
			line: 26, rule: "workflow.ci_native_command", message: `native job is missing unconditional command "runner.temp binary --help"`,
		},
		{
			name: "smoke rejects repeated flag",
			old:  "      - run: '\"${{ runner.temp }}/amsftp\" --help'\n",
			new:  "      - run: '\"${{ runner.temp }}/amsftp\" --help --help'\n",
			line: 26, rule: "workflow.ci_native_command", message: `native job is missing unconditional command "runner.temp binary --help"`,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			root := prepareFixture(t, "valid")
			replacePolicyFile(t, root, ".github/workflows/ci.yml",
				canonicalCICommandMutation(test.old), canonicalCICommandMutation(test.new))
			assertPolicyFinding(t, root, policyFinding{
				Path: ".github/workflows/ci.yml", Line: test.line, Rule: test.rule, Message: test.message,
			})
		})
	}
}

func canonicalCICommandMutation(value string) string {
	const nativeOutput = `"${{ runner.temp }}/native/bin/amsftp"`
	value = strings.ReplaceAll(value, `"${{ runner.temp }}/amsftp"`, nativeOutput)
	value = strings.ReplaceAll(value,
		`"${{ runner.temp }}/${{ matrix.artifact }}"`,
		`"${{ runner.temp }}/build/${{ matrix.artifact }}/${{ matrix.artifact }}"`)
	if strings.Contains(value, "go build") && strings.Contains(value, nativeOutput) && !strings.Contains(value, "-trimpath") {
		value = strings.Replace(value, " -o "+nativeOutput, " -trimpath -o "+nativeOutput, 1)
	}
	return value
}

func TestCINativeBuildAndSmokeRequireOneOrderedTrustedSequence(t *testing.T) {
	const (
		path    = ".github/workflows/ci.yml"
		build   = "      - run: go build -trimpath -o \"${{ runner.temp }}/native/bin/amsftp\" ./cmd/amsftp\n"
		help    = "      - run: '\"${{ runner.temp }}/native/bin/amsftp\" --help'\n"
		version = "      - run: '\"${{ runner.temp }}/native/bin/amsftp\" --version'\n"
	)
	tests := []struct {
		name        string
		replacement string
		command     string
	}{
		{
			name: "build and smoke different paths",
			replacement: build +
				"      - run: '\"${{ runner.temp }}/other/amsftp\" --help'\n" +
				"      - run: '\"${{ runner.temp }}/other/amsftp\" --version'\n",
			command: "runner.temp binary --help",
		},
		{
			name:        "smoke before build",
			replacement: help + version + build,
			command:     "runner.temp binary --help",
		},
		{
			name: "help and version different paths",
			replacement: build + help +
				"      - run: '\"${{ runner.temp }}/other/amsftp\" --version'\n",
			command: "runner.temp binary --version",
		},
		{
			name: "build output replaced before smoke",
			replacement: build +
				"      - run: install -m 755 /bin/true \"${{ runner.temp }}/native/bin/amsftp\"\n" +
				help + version,
			command: "runner.temp binary --help",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			root := prepareFixture(t, "valid")
			replacePolicyFile(t, root, path, build+help+version, test.replacement)
			assertPolicyFinding(t, root, policyFinding{
				Path: path, Line: 26, Rule: "workflow.ci_native_command",
				Message: fmt.Sprintf("native job is missing unconditional command %q", test.command),
			})
		})
	}
}

func TestCIGoFlagsEnvironmentPrecedence(t *testing.T) {
	const (
		path          = ".github/workflows/ci.yml"
		topAnchor     = "permissions:\n  contents: read\n"
		nativeBuild   = "      - run: go build -trimpath -o \"${{ runner.temp }}/native/bin/amsftp\" ./cmd/amsftp\n"
		crossBuild    = "      - run: go build -trimpath -buildvcs=false -o \"${{ runner.temp }}/build/${{ matrix.artifact }}/${{ matrix.artifact }}\" ./cmd/amsftp\n"
		nativeRule    = "workflow.ci_native_command"
		nativeMessage = `native job is missing unconditional command "go build ./cmd/amsftp to runner.temp"`
		buildRule     = "workflow.ci_build_command"
		buildMessage  = "build job must cross-build each matrix tuple with CGO_ENABLED=0, -trimpath, and -buildvcs=false into runner.temp"
	)

	t.Run("workflow value is inherited by native and cross build", func(t *testing.T) {
		root := prepareFixture(t, "valid")
		replacePolicyFile(t, root, path, topAnchor, topAnchor+"env:\n  GOFLAGS: -n\n")
		assertPolicyFinding(t, root, policyFinding{Path: path, Line: 28, Rule: nativeRule, Message: nativeMessage})
		assertPolicyFinding(t, root, policyFinding{Path: path, Line: 81, Rule: buildRule, Message: buildMessage})
	})

	t.Run("native job value is rejected", func(t *testing.T) {
		root := prepareFixture(t, "valid")
		replacement := "    timeout-minutes: 30\n    env:\n      GOFLAGS: -n\n"
		replacePolicyFileOccurrence(t, root, path, "    timeout-minutes: 30\n", replacement, 1)
		assertPolicyFinding(t, root, policyFinding{Path: path, Line: 26, Rule: nativeRule, Message: nativeMessage})
	})

	t.Run("native job empty overrides workflow value", func(t *testing.T) {
		root := prepareFixture(t, "valid")
		replacePolicyFile(t, root, path, topAnchor, topAnchor+"env:\n  GOFLAGS: -n\n")
		replacement := "    timeout-minutes: 30\n    env:\n      GOFLAGS: \"\"\n"
		replacePolicyFileOccurrence(t, root, path, "    timeout-minutes: 30\n", replacement, 1)
		assertCIPolicyRuleAbsent(t, root, nativeRule)
	})

	t.Run("native step value is rejected", func(t *testing.T) {
		root := prepareFixture(t, "valid")
		replacePolicyFile(t, root, path, nativeBuild, nativeBuild+"        env:\n          GOFLAGS: -n\n")
		assertPolicyFinding(t, root, policyFinding{Path: path, Line: 26, Rule: nativeRule, Message: nativeMessage})
	})

	t.Run("native build override cannot repair earlier required commands", func(t *testing.T) {
		root := prepareFixture(t, "valid")
		replacement := "    timeout-minutes: 30\n    env:\n      GOFLAGS: -n\n"
		replacePolicyFileOccurrence(t, root, path, "    timeout-minutes: 30\n", replacement, 1)
		replacePolicyFile(t, root, path, nativeBuild, nativeBuild+"        env:\n          GOFLAGS: \"\"\n")
		assertPolicyFinding(t, root, policyFinding{Path: path, Line: 26, Rule: nativeRule, Message: nativeMessage})
	})

	t.Run("cross job value is rejected", func(t *testing.T) {
		root := prepareFixture(t, "valid")
		replacement := "    timeout-minutes: 30\n    env:\n      GOFLAGS: -n\n"
		replacePolicyFileOccurrence(t, root, path, "    timeout-minutes: 30\n", replacement, 3)
		assertPolicyFinding(t, root, policyFinding{Path: path, Line: 79, Rule: buildRule, Message: buildMessage})
	})

	t.Run("cross job empty overrides workflow value", func(t *testing.T) {
		root := prepareFixture(t, "valid")
		replacePolicyFile(t, root, path, topAnchor, topAnchor+"env:\n  GOFLAGS: -n\n")
		replacement := "    timeout-minutes: 30\n    env:\n      GOFLAGS: \"\"\n"
		replacePolicyFileOccurrence(t, root, path, "    timeout-minutes: 30\n", replacement, 3)
		assertCIPolicyRuleAbsent(t, root, buildRule)
	})

	t.Run("cross step value is rejected", func(t *testing.T) {
		root := prepareFixture(t, "valid")
		replacePolicyFile(t, root, path, crossBuild+"        env:\n", crossBuild+"        env:\n          GOFLAGS: -n\n")
		assertPolicyFinding(t, root, policyFinding{Path: path, Line: 79, Rule: buildRule, Message: buildMessage})
	})

	t.Run("cross step empty overrides job value", func(t *testing.T) {
		root := prepareFixture(t, "valid")
		replacement := "    timeout-minutes: 30\n    env:\n      GOFLAGS: -n\n"
		replacePolicyFileOccurrence(t, root, path, "    timeout-minutes: 30\n", replacement, 3)
		replacePolicyFile(t, root, path, crossBuild+"        env:\n", crossBuild+"        env:\n          GOFLAGS: \"\"\n")
		assertCIPolicyRuleAbsent(t, root, buildRule)
	})
}

func TestCIBuildAndSmokeAcceptNestedRunnerTempPaths(t *testing.T) {
	root := prepareFixture(t, "valid")
	assertNoPolicyFindings(t, root)
}

func TestRunnerTempPathRequiresCanonicalSegments(t *testing.T) {
	tests := []struct {
		name  string
		path  string
		valid bool
	}{
		{name: "canonical nested", path: "${{ runner.temp }}/native/bin/amsftp", valid: true},
		{name: "parent traversal", path: "${{ runner.temp }}/../outside/amsftp"},
		{name: "current directory", path: "${{ runner.temp }}/native/./amsftp"},
		{name: "empty middle segment", path: "${{ runner.temp }}/native//amsftp"},
		{name: "empty final segment", path: "${{ runner.temp }}/native/bin/"},
		{name: "backslash", path: `${{ runner.temp }}/native\bin/amsftp`},
		{name: "expression traversal", path: "${{ runner.temp }}/${{ '..' }}/amsftp"},
		{name: "expression variable", path: "${{ runner.temp }}/${{ vars.OUT }}/amsftp"},
		{name: "shell variable", path: "${{ runner.temp }}/$OUT/amsftp"},
		{name: "braced shell variable", path: "${{ runner.temp }}/${OUT}/amsftp"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := isRunnerTempPath(test.path); got != test.valid {
				t.Fatalf("isRunnerTempPath(%q) = %t, want %t", test.path, got, test.valid)
			}
		})
	}
}

func TestCINativeRequiresExactRunnerTempPaths(t *testing.T) {
	const (
		path    = ".github/workflows/ci.yml"
		oldPath = "${{ runner.temp }}/native/bin"
		message = `native job is missing unconditional command "go build ./cmd/amsftp to runner.temp"`
	)
	for _, test := range []struct {
		name    string
		newPath string
	}{
		{name: "different static directory", newPath: "${{ runner.temp }}/other/bin"},
		{name: "expression variable directory", newPath: "${{ runner.temp }}/${{ vars.OUT }}/bin"},
		{name: "shell variable directory", newPath: "${{ runner.temp }}/$OUT/bin"},
	} {
		t.Run(test.name, func(t *testing.T) {
			root := prepareFixture(t, "valid")
			content := readPolicyFile(t, root, path)
			if count := strings.Count(content, oldPath); count != 4 {
				t.Fatalf("native path occurrence count = %d, want 4", count)
			}
			writePolicyFile(t, root, path, strings.ReplaceAll(content, oldPath, test.newPath))
			assertPolicyFinding(t, root, policyFinding{
				Path: path, Line: 26, Rule: "workflow.ci_native_command", Message: message,
			})
		})
	}
}

func TestCINativeRejectsNonCanonicalMkdirPath(t *testing.T) {
	const (
		path    = ".github/workflows/ci.yml"
		oldStep = "      - run: mkdir -p \"${{ runner.temp }}/native/bin\"\n"
		newStep = "      - run: mkdir -p \"${{ runner.temp }}/native/bin/\"\n"
	)
	root := prepareFixture(t, "valid")
	replacePolicyFile(t, root, path, oldStep, newStep)
	assertPolicyFinding(t, root, policyFinding{
		Path: path, Line: 26, Rule: "workflow.ci_native_command",
		Message: `native job is missing unconditional command "go build ./cmd/amsftp to runner.temp"`,
	})
}

func TestCIBuildRequiresExactRunnerTempPaths(t *testing.T) {
	const (
		path         = ".github/workflows/ci.yml"
		oldDirectory = "${{ runner.temp }}/build/${{ matrix.artifact }}"
		message      = "build job must cross-build each matrix tuple with CGO_ENABLED=0, -trimpath, and -buildvcs=false into runner.temp"
	)
	for _, test := range []struct {
		name         string
		newDirectory string
	}{
		{name: "different static prefix", newDirectory: "${{ runner.temp }}/other/${{ matrix.artifact }}"},
		{name: "expression traversal prefix", newDirectory: "${{ runner.temp }}/${{ '..' }}/${{ matrix.artifact }}"},
		{name: "expression variable prefix", newDirectory: "${{ runner.temp }}/build/${{ vars.OUT }}"},
		{name: "shell variable prefix", newDirectory: "${{ runner.temp }}/build/$OUT"},
	} {
		t.Run(test.name, func(t *testing.T) {
			root := prepareFixture(t, "valid")
			content := readPolicyFile(t, root, path)
			if count := strings.Count(content, oldDirectory); count != 2 {
				t.Fatalf("cross-build directory occurrence count = %d, want 2", count)
			}
			writePolicyFile(t, root, path, strings.ReplaceAll(content, oldDirectory, test.newDirectory))
			assertPolicyFinding(t, root, policyFinding{
				Path: path, Line: 79, Rule: "workflow.ci_build_command", Message: message,
			})
		})
	}
}

func TestCINativeRejectsRunnerTempTraversal(t *testing.T) {
	const (
		path    = ".github/workflows/ci.yml"
		oldPath = "${{ runner.temp }}/native/bin"
		newPath = "${{ runner.temp }}/../outside/bin"
	)
	root := prepareFixture(t, "valid")
	content := readPolicyFile(t, root, path)
	if count := strings.Count(content, oldPath); count != 4 {
		t.Fatalf("native path occurrence count = %d, want 4", count)
	}
	writePolicyFile(t, root, path, strings.ReplaceAll(content, oldPath, newPath))
	assertPolicyFinding(t, root, policyFinding{
		Path: path, Line: 26, Rule: "workflow.ci_native_command",
		Message: `native job is missing unconditional command "go build ./cmd/amsftp to runner.temp"`,
	})
}

func TestCIBuildOutputBasenameMustEqualArtifact(t *testing.T) {
	const (
		path    = ".github/workflows/ci.yml"
		oldPath = "${{ runner.temp }}/build/${{ matrix.artifact }}/${{ matrix.artifact }}"
		message = "build job must cross-build each matrix tuple with CGO_ENABLED=0, -trimpath, and -buildvcs=false into runner.temp"
	)
	for _, test := range []struct {
		name    string
		newPath string
	}{
		{name: "artifact prefix", newPath: "${{ runner.temp }}/build/${{ matrix.artifact }}/${{ matrix.artifact }}-debug"},
		{name: "artifact suffix", newPath: "${{ runner.temp }}/build/${{ matrix.artifact }}/not-${{ matrix.artifact }}"},
	} {
		t.Run(test.name, func(t *testing.T) {
			root := prepareFixture(t, "valid")
			replacePolicyFile(t, root, path, oldPath, test.newPath)
			assertPolicyFinding(t, root, policyFinding{
				Path: path, Line: 79, Rule: "workflow.ci_build_command", Message: message,
			})
		})
	}
}

func TestDockerActionsRequireImmutablePinsInBothPolicies(t *testing.T) {
	const path = ".github/workflows/docker.yml"
	lines := []string{
		"name: docker",
		"on: [push]",
		"permissions: {contents: read}",
		"jobs:",
		"  test:",
		"    runs-on: ubuntu-24.04",
		"    timeout-minutes: 5",
		"    steps:",
		"      - uses: docker://alpine:latest",
	}
	want := policyFinding{
		Path: path, Line: 9, Rule: "workflow.action_pin",
		Message: `action "docker://alpine:latest" must use a 40-character lowercase commit SHA`,
	}
	assertFinding := func(t *testing.T, findings []Finding) {
		t.Helper()
		for _, finding := range findings {
			if finding.Path == want.Path && finding.Line == want.Line && finding.Rule == want.Rule && finding.Message == want.Message {
				return
			}
		}
		t.Fatalf("missing exact finding %#v\nfull findings:\n%s", want, formatFindings(findings))
	}

	t.Run("semantic", func(t *testing.T) {
		assertFinding(t, checkWorkflowPolicy(path, lines))
	})
	t.Run("legacy", func(t *testing.T) {
		assertFinding(t, checkWorkflowLines(path, lines))
	})
}

func TestDockerActionsCannotMasqueradeAsCommitPinnedActions(t *testing.T) {
	const (
		path   = ".github/workflows/docker.yml"
		action = "docker://alpine@1111111111111111111111111111111111111111"
	)
	lines := []string{
		"name: docker",
		"on: [push]",
		"permissions: {contents: read}",
		"jobs:",
		"  test:",
		"    runs-on: ubuntu-24.04",
		"    timeout-minutes: 5",
		"    steps:",
		"      - uses: " + action,
	}
	want := policyFinding{
		Path: path, Line: 9, Rule: "workflow.action_pin",
		Message: `action "docker://alpine@1111111111111111111111111111111111111111" must use a 40-character lowercase commit SHA`,
	}
	for name, findings := range map[string][]Finding{
		"semantic": checkWorkflowPolicy(path, lines),
		"legacy":   checkWorkflowLines(path, lines),
	} {
		t.Run(name, func(t *testing.T) {
			for _, finding := range findings {
				if finding.Path == want.Path && finding.Line == want.Line && finding.Rule == want.Rule && finding.Message == want.Message {
					return
				}
			}
			t.Fatalf("missing exact finding %#v\nfull findings:\n%s", want, formatFindings(findings))
		})
	}
}

func TestStageStatusCoherence(t *testing.T) {
	tests := []struct {
		name    string
		path    string
		old     string
		new     string
		finding policyFinding
	}{
		{name: "lifecycle missing", path: "PROJECT_STATE.md", old: "- **Lifecycle**: Stage 0 foundation in progress\n", new: "", finding: policyFinding{Path: "PROJECT_STATE.md", Line: 1, Rule: "state.lifecycle", Message: "Lifecycle line is missing or malformed"}},
		{name: "lifecycle malformed", path: "PROJECT_STATE.md", old: "- **Lifecycle**: Stage 0 foundation in progress", new: "- **Lifecycle**: Stage zero unknown", finding: policyFinding{Path: "PROJECT_STATE.md", Line: 4, Rule: "state.lifecycle", Message: "Lifecycle line is missing or malformed"}},
		{name: "lifecycle active mismatch", path: "PROJECT_STATE.md", old: "- **Lifecycle**: Stage 0 foundation in progress", new: "- **Lifecycle**: Stage 1 explorer in progress", finding: policyFinding{Path: "PROJECT_STATE.md", Line: 4, Rule: "state.lifecycle_stage_mismatch", Message: "Lifecycle names Stage 1 but Active stage names Stage 0"}},
		{name: "plan status mismatch", path: "IMPLEMENTATION_PLAN.md", old: "**Status**: In Progress", new: "**Status**: Not Started", finding: policyFinding{Path: "PROJECT_STATE.md", Line: 4, Rule: "state.status_mismatch", Message: "Lifecycle status \"In Progress\" does not match IMPLEMENTATION_PLAN.md Stage 0 status \"Not Started\""}},
		{name: "stage status missing", path: "docs/stages/00-foundation.md", old: "- **状态**：In Progress\n", new: "", finding: policyFinding{Path: "docs/stages/00-foundation.md", Line: 1, Rule: "stage.status", Message: "active Stage document status line is missing or malformed"}},
		{name: "stage status malformed", path: "docs/stages/00-foundation.md", old: "- **状态**：In Progress", new: "- **状态**：Started", finding: policyFinding{Path: "docs/stages/00-foundation.md", Line: 3, Rule: "stage.status", Message: "active Stage document status line is missing or malformed"}},
		{name: "stage status mismatch", path: "docs/stages/00-foundation.md", old: "- **状态**：In Progress", new: "- **状态**：Not Started", finding: policyFinding{Path: "docs/stages/00-foundation.md", Line: 3, Rule: "stage.status_mismatch", Message: "active Stage 0 document status \"Not Started\" does not match IMPLEMENTATION_PLAN.md status \"In Progress\""}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			root := prepareFixture(t, "valid")
			replacePolicyFile(t, root, test.path, test.old, test.new)
			assertPolicyFinding(t, root, test.finding)
		})
	}
}

func TestStageOneCoherentStatus(t *testing.T) {
	root := prepareFixture(t, "valid")
	replacePolicyFile(t, root, "PROJECT_STATE.md", "Stage 0 — Foundation", "Stage 1 — Explorer")
	replacePolicyFile(t, root, "PROJECT_STATE.md", "Stage 0 foundation", "Stage 1 explorer")
	replacePolicyFile(t, root, "IMPLEMENTATION_PLAN.md", "## Stage 0: Foundation\n**Status**: In Progress\n## Stage 1: Explorer\n**Status**: Not Started", "## Stage 0: Foundation\n**Status**: Complete\n## Stage 1: Explorer\n**Status**: In Progress")
	replacePolicyFile(t, root, "docs/stages/01-read-only-explorer.md", "# Stage 1\n", "# Stage 1\n\n- **状态**：In Progress\n")
	assertNoPolicyFindings(t, root)
}

func TestVerifiedEvidenceRequiresExactSeparatePassCell(t *testing.T) {
	tests := []struct {
		name string
		body string
	}{
		{name: "wrong id", body: "| Feature | Result |\n|---|---|\n| CORE-003 | PASS |\n"},
		{name: "prefix id", body: "| Feature | Result |\n|---|---|\n| CORE-002-extra | PASS |\n"},
		{name: "lowercase pass", body: "| Feature | Result |\n|---|---|\n| CORE-002 | pass |\n"},
		{name: "passed", body: "| Feature | Result |\n|---|---|\n| CORE-002 | Passed |\n"},
		{name: "bold pass", body: "| Feature | Result |\n|---|---|\n| CORE-002 | **PASS** |\n"},
		{name: "same cell", body: "| Evidence | Result |\n|---|---|\n| CORE-002 PASS | |\n"},
		{name: "split rows", body: "| Feature | Result |\n|---|---|\n| CORE-002 | FAIL |\n| CORE-003 | PASS |\n"},
		{name: "prose", body: "CORE-002 passed with PASS.\n"},
		{name: "fenced", body: "```text\n| CORE-002 | PASS |\n```\n"},
		{name: "commented", body: "<!--\n| CORE-002 | PASS |\n-->\n"},
		{name: "escaped pipe", body: "| Feature | Result |\n|---|---|\n| CORE-002 | details \\| PASS |\n"},
		{name: "short backtick closing fence", body: "````text\n| Feature | Result |\n|---|---|\n```\n| Feature | Result |\n|---|---|\n| CORE-002 | PASS |\n````\n"},
		{name: "short tilde closing fence", body: "~~~~text\n| Feature | Result |\n|---|---|\n~~~\n| Feature | Result |\n|---|---|\n| CORE-002 | PASS |\n~~~~\n"},
		{name: "closing fence with info text", body: "```text\nignored\n```not-a-close\n| Feature | Result |\n|---|---|\n| CORE-002 | PASS |\n```\n"},
		{name: "four-space closing fence", body: "```text\nignored\n    ```\n| Feature | Result |\n|---|---|\n| CORE-002 | PASS |\n```\n"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			root := prepareFixture(t, "valid")
			writePolicyFile(t, root, "docs/verification/stage-00.md", "# Stage 0 Verification\n\n"+test.body)
			assertPolicyFinding(t, root, policyFinding{
				Path: "docs/product/feature-matrix.md", Line: 6, Rule: "matrix.verification_result",
				Message: "verified feature \"CORE-002\" must link to a verification table row with the exact feature ID and a separate PASS cell",
			})
		})
	}
}

func TestMarkdownTableCellsHonorsEscapedPipeParity(t *testing.T) {
	tests := []struct {
		name string
		line string
		want []string
	}{
		{name: "one backslash", line: `| CORE-002 | details \| PASS |`, want: []string{"CORE-002", `details \| PASS`}},
		{name: "two backslashes", line: `| CORE-002 | details \\| PASS |`, want: []string{"CORE-002", `details \\`, "PASS"}},
		{name: "three backslashes", line: `| CORE-002 | details \\\| PASS |`, want: []string{"CORE-002", `details \\\| PASS`}},
		{name: "four backslashes", line: `| CORE-002 | details \\\\| PASS |`, want: []string{"CORE-002", `details \\\\`, "PASS"}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := markdownTableCells(test.line); !reflect.DeepEqual(got, test.want) {
				t.Fatalf("markdownTableCells(%q) = %#v, want %#v", test.line, got, test.want)
			}
		})
	}
}

func TestVerifiedEvidenceAcceptsLongerClosingFence(t *testing.T) {
	for _, markers := range [][2]string{{"```text", "````"}, {"~~~text", "~~~~"}} {
		root := prepareFixture(t, "valid")
		body := markers[0] + "\nignored\n" + markers[1] + "\n| Feature | Result |\n|---|---|\n| CORE-002 | PASS |\n"
		writePolicyFile(t, root, "docs/verification/stage-00.md", "# Stage 0 Verification\n\n"+body)
		assertNoPolicyFindings(t, root)
	}
}

func TestVerifiedEvidenceAcceptsSecondQualifyingLink(t *testing.T) {
	root := prepareFixture(t, "valid")
	writePolicyFile(t, root, "docs/verification/stage-00.md", "# First Record\n\n| Feature | Result |\n|---|---|\n| CORE-002 | FAIL |\n")
	writePolicyFile(t, root, "docs/verification/stage-00-second.md", "# Second Record\n\n| Feature | Result |\n|---|---|\n| CORE-002 | PASS |\n")
	replacePolicyFile(t, root, "docs/product/feature-matrix.md", "[record](../verification/stage-00.md)", "[first](../verification/stage-00.md) [second](../verification/stage-00-second.md)")
	assertNoPolicyFindings(t, root)
}

func assertPolicyFinding(t *testing.T, root string, want policyFinding) {
	t.Helper()
	findings := Check(root)
	for _, finding := range findings {
		if finding.Path == want.Path && finding.Line == want.Line && finding.Rule == want.Rule && finding.Message == want.Message {
			return
		}
	}
	t.Fatalf("missing exact finding %#v\nfull findings:\n%s", want, formatFindings(findings))
}

func assertNoPolicyFindings(t *testing.T, root string) {
	t.Helper()
	if findings := checkFixture(root); len(findings) != 0 {
		t.Fatalf("Check() returned unexpected findings:\n%s", formatFindings(findings))
	}
}

func assertCIQualityRuleAbsent(t *testing.T, root string) {
	t.Helper()
	const (
		path = ".github/workflows/ci.yml"
		rule = "workflow.ci_quality"
	)
	findings := Check(root)
	for _, finding := range findings {
		if finding.Path == path && finding.Rule == rule {
			t.Fatalf("unexpected %s finding %#v\nfull findings:\n%s", rule, finding, formatFindings(findings))
		}
	}
}

func assertCIPolicyRuleAbsent(t *testing.T, root, rule string) {
	t.Helper()
	const path = ".github/workflows/ci.yml"
	findings := Check(root)
	for _, finding := range findings {
		if finding.Path == path && finding.Rule == rule {
			t.Fatalf("unexpected %s finding %#v\nfull findings:\n%s", rule, finding, formatFindings(findings))
		}
	}
}

func readPolicyFile(t *testing.T, root, path string) string {
	t.Helper()
	//nolint:gosec // root is a temporary fixture and path is a test-owned repository-relative literal.
	content, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(path)))
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	return string(content)
}

func replacePolicyFile(t *testing.T, root, path, old, new string) {
	t.Helper()
	content := readPolicyFile(t, root, path)
	if count := strings.Count(content, old); count != 1 {
		t.Fatalf("replace %q in %s: got %d occurrences, want 1", old, path, count)
	}
	writePolicyFile(t, root, path, strings.Replace(content, old, new, 1))
}

func replacePolicyFileOccurrence(t *testing.T, root, path, old, new string, occurrence int) {
	t.Helper()
	content := readPolicyFile(t, root, path)
	if occurrence < 1 {
		t.Fatalf("replace %q in %s: occurrence must be positive", old, path)
	}
	start := 0
	for index := 1; index <= occurrence; index++ {
		relative := strings.Index(content[start:], old)
		if relative < 0 {
			t.Fatalf("replace %q in %s: occurrence %d not found", old, path, occurrence)
		}
		start += relative
		if index < occurrence {
			start += len(old)
		}
	}
	writePolicyFile(t, root, path, content[:start]+new+content[start+len(old):])
}

func writePolicyFile(t *testing.T, root, path, content string) {
	t.Helper()
	target := filepath.Join(root, filepath.FromSlash(path))
	if err := os.MkdirAll(filepath.Dir(target), 0o750); err != nil {
		t.Fatalf("create parent for %s: %v", path, err)
	}
	if err := os.WriteFile(target, []byte(content), 0o600); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}
