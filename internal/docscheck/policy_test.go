package docscheck

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const checkoutSHA = "9c091bb21b7c1c1d1991bb908d89e4e9dddfe3e0"

func TestWorkflowPolicyRequiresCanonicalWorkflows(t *testing.T) {
	for _, path := range []string{
		".github/workflows/fast-ci.yml",
		".github/workflows/nightly.yml",
	} {
		t.Run(filepath.Base(path), func(t *testing.T) {
			root := prepareFixture(t, "valid")
			if err := os.Remove(filepath.Join(root, filepath.FromSlash(path))); err != nil {
				t.Fatalf("remove workflow: %v", err)
			}
			assertPolicyRule(t, root, path, "workflow.required")
		})
	}
}

func TestFastWorkflowRoutingPolicy(t *testing.T) {
	t.Run("fast ci keeps pull request and main routing", func(t *testing.T) {
		root := prepareFixture(t, "valid")
		replacePolicyFile(
			t,
			root,
			".github/workflows/fast-ci.yml",
			"on:\n  pull_request:\n    branches-ignore:\n      - \"release/**\"\n  push:\n    branches:\n      - main\n",
			"on:\n  push:\n    branches:\n      - release/**\n",
		)
		assertPolicyRule(t, root, ".github/workflows/fast-ci.yml", "workflow.fast_routing")
	})

	t.Run("fast ci keeps stable aggregator", func(t *testing.T) {
		root := prepareFixture(t, "valid")
		replacePolicyFile(t, root, ".github/workflows/fast-ci.yml", "    name: required\n", "    name: optional\n")
		assertPolicyRule(t, root, ".github/workflows/fast-ci.yml", "workflow.fast_required")
	})
}

func TestWorkflowGenericPolicy(t *testing.T) {
	tests := []struct {
		name       string
		path       string
		old        string
		new        string
		occurrence int
		rule       string
	}{
		{
			name: "top permissions missing", path: ".github/workflows/nightly.yml",
			old: "permissions:\n  contents: read\n", new: "", rule: "workflow.top_permissions",
		},
		{
			name: "checkout credentials persist", path: ".github/workflows/fast-ci.yml",
			old: "          persist-credentials: false\n", new: "          persist-credentials: true\n",
			occurrence: 1, rule: "workflow.persist_credentials",
		},
		{
			name: "job timeout is zero", path: ".github/workflows/fast-ci.yml",
			old: "    timeout-minutes: 5\n", new: "    timeout-minutes: 0\n",
			occurrence: 1, rule: "workflow.job_timeout",
		},
		{
			name: "job continue on error", path: ".github/workflows/fast-ci.yml",
			old: "    timeout-minutes: 5\n", new: "    timeout-minutes: 5\n    continue-on-error: false\n",
			occurrence: 1, rule: "workflow.continue_on_error",
		},
		{
			name: "step continue on error", path: ".github/workflows/fast-ci.yml",
			old: "      - run: exit 0\n", new: "      - run: exit 0\n        continue-on-error: false\n",
			occurrence: 1, rule: "workflow.continue_on_error",
		},
		{
			name: "approved action wrong sha", path: ".github/workflows/fast-ci.yml",
			old:        "actions/checkout@" + checkoutSHA,
			new:        "actions/checkout@1111111111111111111111111111111111111111",
			occurrence: 1, rule: "workflow.action_version",
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
			assertPolicyRule(t, root, test.path, test.rule)
		})
	}
}

func TestWorkflowFlowPolicyAndBenignLookalikes(t *testing.T) {
	root := prepareFixture(t, "valid")
	writePolicyFile(t, root, ".github/workflows/flow.yml", strings.Join([]string{
		"name: flow policy",
		"on: [push]",
		"permissions: {contents: read}",
		"jobs: {test: {runs-on: ubuntu-24.04, timeout-minutes: 5, steps: [{uses: 'actions/checkout@" + checkoutSHA + "', with: {persist-credentials: false, continue-on-error: true, timeout-minutes: 0, permissions: write-all}}, {run: 'echo continue-on-error: true'}]}}",
		"",
	}, "\n"))
	assertNoPolicyFindingsForPath(t, root, ".github/workflows/flow.yml")

	writePolicyFile(t, root, ".github/workflows/flow.yml", strings.Join([]string{
		"name: flow attack",
		"on: [push]",
		"permissions: {contents: read, issues: read}",
		"jobs: {test: {runs-on: ubuntu-24.04, timeout-minutes: 0, continue-on-error: false, steps: [{uses: 'actions/checkout@" + checkoutSHA + "', with: {persist-credentials: true}}]}}",
		"",
	}, "\n"))
	for _, rule := range []string{
		"workflow.top_permissions",
		"workflow.job_timeout",
		"workflow.continue_on_error",
		"workflow.persist_credentials",
	} {
		assertPolicyRule(t, root, ".github/workflows/flow.yml", rule)
	}
}

func TestWorkflowUnsafeYAMLIsRejected(t *testing.T) {
	base := "name: syntax\non: [push]\npermissions: {contents: read}\njobs:\n  test:\n    runs-on: ubuntu-24.04\n    timeout-minutes: 5\n    steps: []\n"
	tests := []struct {
		name    string
		content string
	}{
		{name: "tab indentation", content: strings.Replace(base, "  test:", "\ttest:", 1)},
		{name: "unterminated quote", content: strings.Replace(base, "name: syntax", "name: 'syntax", 1)},
		{name: "anchor", content: strings.Replace(base, "permissions: {contents: read}", "permissions: &policy {contents: read}", 1)},
		{name: "duplicate job", content: strings.Replace(base, "    steps: []", "    steps: []\n  test: {runs-on: ubuntu-24.04, timeout-minutes: 5, steps: []}", 1)},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			root := prepareFixture(t, "valid")
			writePolicyFile(t, root, ".github/workflows/syntax.yml", test.content)
			assertPolicyRule(t, root, ".github/workflows/syntax.yml", "workflow.syntax")
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
	for name, findings := range map[string][]Finding{
		"semantic": checkWorkflowPolicy(path, lines),
		"legacy":   checkWorkflowLines(path, lines),
	} {
		t.Run(name, func(t *testing.T) {
			for _, finding := range findings {
				if finding.Path == path && finding.Rule == "workflow.action_pin" {
					return
				}
			}
			t.Fatalf("missing workflow.action_pin finding:\n%s", formatFindings(findings))
		})
	}
}

func assertPolicyRule(t *testing.T, root, path, rule string) {
	t.Helper()
	findings := Check(root)
	for _, finding := range findings {
		if finding.Path == path && finding.Rule == rule {
			return
		}
	}
	t.Fatalf("missing %s for %s\nfull findings:\n%s", rule, path, formatFindings(findings))
}

func assertNoPolicyFindingsForPath(t *testing.T, root, path string) {
	t.Helper()
	findings := Check(root)
	for _, finding := range findings {
		if finding.Path == path {
			t.Fatalf("%s returned unexpected findings:\n%s", path, formatFindings(findings))
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
