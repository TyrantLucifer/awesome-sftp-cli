package docscheck

import (
	"io"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strconv"
	"strings"
	"testing"
)

type expectedFinding struct {
	Path string
	Line int
	Rule string
}

func TestValidFixtureHasNoFindings(t *testing.T) {
	root := prepareFixture(t, "valid")
	if findings := checkFixture(root); len(findings) != 0 {
		t.Fatalf("Check() returned unexpected findings:\n%s", formatFindings(findings))
	}
}

func TestBrokenRelativeLink(t *testing.T) {
	assertFixtureFindings(t, "broken-link", []expectedFinding{
		{Path: "docs/guide.md", Line: 3, Rule: "link.missing"},
		{Path: "docs/guide.md", Line: 4, Rule: "link.escape"},
	})
}

func TestRelativeLinkRejectsFileSymlinkEscape(t *testing.T) {
	root := prepareFixture(t, "symlink-file-escape")
	target := writeOutsideFixtureFile(t, "outside.md")
	if err := os.Symlink(target, filepath.Join(root, "docs", "outside-file.md")); err != nil {
		t.Fatalf("create file symlink: %v", err)
	}

	assertCheckFindings(t, root, []expectedFinding{
		{Path: "docs/guide.md", Line: 3, Rule: "link.escape"},
	})
}

func TestRelativeLinkRejectsDirectorySymlinkEscape(t *testing.T) {
	root := prepareFixture(t, "symlink-directory-escape")
	target := writeOutsideFixtureFile(t, "outside.md")
	if err := os.Symlink(filepath.Dir(target), filepath.Join(root, "docs", "outside-directory")); err != nil {
		t.Fatalf("create directory symlink: %v", err)
	}

	assertCheckFindings(t, root, []expectedFinding{
		{Path: "docs/guide.md", Line: 3, Rule: "link.escape"},
	})
}

func TestVerificationRecordRejectsSymlinkEscape(t *testing.T) {
	root := prepareFixture(t, "verification-symlink-escape")
	target := writeOutsideFixtureFile(t, "record.md")
	if err := os.Symlink(filepath.Dir(target), filepath.Join(root, "docs", "verification", "outside")); err != nil {
		t.Fatalf("create verification directory symlink: %v", err)
	}

	assertCheckFindings(t, root, []expectedFinding{
		{Path: "docs/product/feature-matrix.md", Line: 5, Rule: "link.escape"},
		{Path: "docs/product/feature-matrix.md", Line: 5, Rule: "matrix.verification_record"},
	})
}

func TestRepositoryTruthRejectsSymlinkEscapes(t *testing.T) {
	t.Run("implementation plan final file", func(t *testing.T) {
		root := prepareFixture(t, "valid")
		replaceFixtureFileWithOutsideSymlink(t, root, "IMPLEMENTATION_PLAN.md")

		assertExactCheckFindings(t, root, []Finding{{
			Path: "IMPLEMENTATION_PLAN.md", Line: 1, Rule: "plan.missing",
			Message: `repository path "IMPLEMENTATION_PLAN.md" resolves outside the root`,
		}})
	})

	t.Run("active stage final file", func(t *testing.T) {
		root := prepareFixture(t, "valid")
		replaceFixtureFileWithOutsideSymlink(t, root, "docs/stages/00-foundation.md")

		assertExactCheckFindings(t, root, []Finding{{
			Path: "docs/stages/00-foundation.md", Line: 1, Rule: "stage.missing",
			Message: "required Stage 0–6 document is missing",
		}})
	})

	t.Run("stage parent directory", func(t *testing.T) {
		root := prepareFixture(t, "valid")
		replaceFixtureDirectoryWithOutsideSymlink(t, root, "docs/stages")

		want := make([]Finding, 0, 7)
		for _, path := range requiredStageDocuments {
			want = append(want, Finding{
				Path: path, Line: 1, Rule: "stage.missing",
				Message: "required Stage 0–6 document is missing",
			})
		}
		assertExactCheckFindings(t, root, want)
	})
}

func TestRequiredWorkflowsRejectSymlinks(t *testing.T) {
	for _, path := range []string{".github/workflows/ci.yml", ".github/workflows/nightly.yml"} {
		t.Run(filepath.Base(path)+" final file", func(t *testing.T) {
			root := prepareFixture(t, "valid")
			replaceFixtureFileWithOutsideSymlink(t, root, path)

			assertExactCheckFindings(t, root, []Finding{{
				Path: path, Line: 1, Rule: "workflow.required",
				Message: "required workflow file is missing or is not a regular file",
			}})
		})
	}

	t.Run("parent directory outside root", func(t *testing.T) {
		root := prepareFixture(t, "valid")
		replaceFixtureDirectoryWithOutsideSymlink(t, root, ".github/workflows")

		assertExactCheckFindings(t, root, requiredWorkflowFindings())
	})

	t.Run("parent directory inside root", func(t *testing.T) {
		root := prepareFixture(t, "valid")
		workflowRoot := filepath.Join(root, ".github", "workflows")
		relocated := filepath.Join(root, "relocated-workflows")
		if err := os.Rename(workflowRoot, relocated); err != nil {
			t.Fatalf("relocate workflow directory: %v", err)
		}
		if err := os.Symlink(relocated, workflowRoot); err != nil {
			t.Fatalf("create workflow directory symlink: %v", err)
		}

		assertExactCheckFindings(t, root, requiredWorkflowFindings())
	})
}

func TestDuplicateFeatureID(t *testing.T) {
	assertFixtureFindings(t, "duplicate-feature-id", []expectedFinding{
		{Path: "docs/product/feature-matrix.md", Line: 6, Rule: "matrix.id_duplicate"},
	})
}

func TestMalformedFeatureID(t *testing.T) {
	assertFixtureFindings(t, "invalid-feature-id", []expectedFinding{
		{Path: "docs/product/feature-matrix.md", Line: 5, Rule: "matrix.id_format"},
	})
}

func TestInvalidStageOrStatus(t *testing.T) {
	assertFixtureFindings(t, "invalid-stage-status", []expectedFinding{
		{Path: "docs/product/feature-matrix.md", Line: 5, Rule: "matrix.stage"},
		{Path: "docs/product/feature-matrix.md", Line: 6, Rule: "matrix.status"},
	})
}

func TestMissingFeatureEvidence(t *testing.T) {
	assertFixtureFindings(t, "missing-evidence", []expectedFinding{
		{Path: "docs/product/feature-matrix.md", Line: 5, Rule: "matrix.evidence_missing"},
	})
}

func TestNonPlannedFeatureRejectsStaleEvidence(t *testing.T) {
	assertFixtureFindings(t, "stale-evidence", []expectedFinding{
		{Path: "docs/product/feature-matrix.md", Line: 5, Rule: "matrix.evidence_stale"},
	})
}

func TestVerifiedFeatureRequiresVerificationRecord(t *testing.T) {
	assertFixtureFindings(t, "verified-no-record", []expectedFinding{
		{Path: "docs/product/feature-matrix.md", Line: 5, Rule: "matrix.verification_record"},
	})
}

func TestFeatureMatrixRequiresCanonicalHeader(t *testing.T) {
	assertFixtureFindings(t, "matrix-missing-header", []expectedFinding{
		{Path: "docs/product/feature-matrix.md", Line: 1, Rule: "matrix.schema"},
	})
}

func TestFeatureMatrixRejectsMalformedHeader(t *testing.T) {
	assertFixtureFindings(t, "matrix-malformed-header", []expectedFinding{
		{Path: "docs/product/feature-matrix.md", Line: 3, Rule: "matrix.schema"},
	})
}

func TestFeatureMatrixRejectsDuplicateHeader(t *testing.T) {
	assertFixtureFindings(t, "matrix-duplicate-header", []expectedFinding{
		{Path: "docs/product/feature-matrix.md", Line: 7, Rule: "matrix.schema"},
	})
}

func TestFeatureMatrixRejectsEmptyTable(t *testing.T) {
	assertFixtureFindings(t, "matrix-empty-table", []expectedFinding{
		{Path: "docs/product/feature-matrix.md", Line: 3, Rule: "matrix.schema"},
	})
}

func TestFeatureMatrixAllowsOneCanonicalTablePerSection(t *testing.T) {
	root := prepareFixture(t, "matrix-sectioned-valid")
	if findings := checkFixture(root); len(findings) != 0 {
		t.Fatalf("Check() returned unexpected findings:\n%s", formatFindings(findings))
	}
}

func TestMissingStageDocument(t *testing.T) {
	assertFixtureFindings(t, "missing-stage", []expectedFinding{
		{Path: "docs/stages/06-hardening-release.md", Line: 1, Rule: "stage.missing"},
	})
}

func TestStatePlanMismatch(t *testing.T) {
	assertFixtureFindings(t, "state-plan-mismatch", []expectedFinding{
		{Path: "PROJECT_STATE.md", Line: 3, Rule: "state.stage_mismatch"},
	})
}

func TestPlanRejectsCompleteStageAfterInProgressStage(t *testing.T) {
	assertExactCheckFindings(t, prepareFixture(t, "plan-stage-order"), []Finding{{
		Path: "IMPLEMENTATION_PLAN.md", Line: 6, Rule: "plan.stage_order",
		Message: `Stage 1 status "Complete" is invalid after Stage 0 status "In Progress"`,
	}})
}

func TestCurrentPlanStageDetailsAcceptsOrderedStatuses(t *testing.T) {
	tests := []struct {
		name       string
		statuses   []string
		wantStage  int
		wantStatus string
		wantLine   int
	}{
		{
			name:       "all not started",
			statuses:   []string{"Not Started", "Not Started", "Not Started", "Not Started", "Not Started", "Not Started", "Not Started"},
			wantStage:  0,
			wantStatus: "Not Started",
			wantLine:   4,
		},
		{
			name:       "single in progress after complete prefix",
			statuses:   []string{"Complete", "In Progress", "Not Started", "Not Started", "Not Started", "Not Started", "Not Started"},
			wantStage:  1,
			wantStatus: "In Progress",
			wantLine:   6,
		},
		{
			name:       "not started after complete prefix without in progress",
			statuses:   []string{"Complete", "Complete", "Not Started", "Not Started", "Not Started", "Not Started", "Not Started"},
			wantStage:  2,
			wantStatus: "Not Started",
			wantLine:   8,
		},
		{
			name:       "all complete",
			statuses:   []string{"Complete", "Complete", "Complete", "Complete", "Complete", "Complete", "Complete"},
			wantStage:  6,
			wantStatus: "Complete",
			wantLine:   16,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			stage, status, line, ok := currentPlanStageDetails(planLinesForStatuses(test.statuses))
			if !ok || stage != test.wantStage || status != test.wantStatus || line != test.wantLine {
				t.Fatalf("currentPlanStageDetails() = (%d, %q, %d, %t), want (%d, %q, %d, true)", stage, status, line, ok, test.wantStage, test.wantStatus, test.wantLine)
			}
		})
	}
}

func TestPlanStageOrderRejectsInvalidTransitions(t *testing.T) {
	tests := []struct {
		name     string
		statuses []string
		want     Finding
	}{
		{
			name:     "complete after in progress",
			statuses: []string{"In Progress", "Complete", "Not Started", "Not Started", "Not Started", "Not Started", "Not Started"},
			want: Finding{Path: "IMPLEMENTATION_PLAN.md", Line: 6, Rule: "plan.stage_order",
				Message: `Stage 1 status "Complete" is invalid after Stage 0 status "In Progress"`},
		},
		{
			name:     "in progress after not started",
			statuses: []string{"Not Started", "In Progress", "Not Started", "Not Started", "Not Started", "Not Started", "Not Started"},
			want: Finding{Path: "IMPLEMENTATION_PLAN.md", Line: 6, Rule: "plan.stage_order",
				Message: `Stage 1 status "In Progress" is invalid after Stage 0 status "Not Started"`},
		},
		{
			name:     "second in progress",
			statuses: []string{"Complete", "In Progress", "In Progress", "Not Started", "Not Started", "Not Started", "Not Started"},
			want: Finding{Path: "IMPLEMENTATION_PLAN.md", Line: 8, Rule: "plan.stage_order",
				Message: `Stage 2 status "In Progress" is invalid after Stage 1 status "In Progress"`},
		},
		{
			name:     "complete after not started",
			statuses: []string{"Complete", "Not Started", "Complete", "Not Started", "Not Started", "Not Started", "Not Started"},
			want: Finding{Path: "IMPLEMENTATION_PLAN.md", Line: 8, Rule: "plan.stage_order",
				Message: `Stage 2 status "Complete" is invalid after Stage 1 status "Not Started"`},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			lines := planLinesForStatuses(test.statuses)
			finding, invalid := planStageOrderFinding(lines)
			if !invalid || finding != test.want {
				t.Fatalf("planStageOrderFinding() = (%#v, %t), want (%#v, true)", finding, invalid, test.want)
			}
			if _, _, _, ok := currentPlanStageDetails(lines); ok {
				t.Fatal("currentPlanStageDetails() accepted invalid Stage order")
			}
		})
	}
}

func TestPlanStageSchemaRejectsMalformedSequence(t *testing.T) {
	tests := []struct {
		name    string
		old     string
		new     string
		finding Finding
	}{
		{
			name: "duplicate header",
			old:  "**Status**: In Progress\n## Stage 1: Explorer",
			new:  "**Status**: In Progress\n## Stage 0: Duplicate\n**Status**: In Progress\n## Stage 1: Explorer",
			finding: Finding{Path: "IMPLEMENTATION_PLAN.md", Line: 5, Rule: "plan.stage_header",
				Message: "Stage 0 header appears more than once"},
		},
		{
			name: "duplicate status",
			old:  "**Status**: In Progress\n## Stage 1: Explorer",
			new:  "**Status**: In Progress\n**Status**: In Progress\n## Stage 1: Explorer",
			finding: Finding{Path: "IMPLEMENTATION_PLAN.md", Line: 5, Rule: "plan.stage_status",
				Message: "Stage 0 defines more than one Status"},
		},
		{
			name: "missing status",
			old:  "## Stage 1: Explorer\n**Status**: Not Started",
			new:  "## Stage 1: Explorer",
			finding: Finding{Path: "IMPLEMENTATION_PLAN.md", Line: 5, Rule: "plan.stage_status",
				Message: "Stage 1 must define exactly one canonical Status"},
		},
		{
			name: "missing header",
			old:  "## Stage 1: Explorer\n**Status**: Not Started\n",
			new:  "",
			finding: Finding{Path: "IMPLEMENTATION_PLAN.md", Line: 1, Rule: "plan.stage_header",
				Message: "Stage 1 header is missing"},
		},
		{
			name: "noncanonical status",
			old:  "## Stage 1: Explorer\n**Status**: Not Started",
			new:  "## Stage 1: Explorer\n**Status**: Blocked",
			finding: Finding{Path: "IMPLEMENTATION_PLAN.md", Line: 6, Rule: "plan.stage_status",
				Message: `Stage 1 status "Blocked" must be one of "Not Started", "In Progress", or "Complete"`},
		},
		{
			name: "headers out of physical order",
			old:  "## Stage 1: Explorer\n**Status**: Not Started\n## Stage 2: Transfers\n**Status**: Not Started",
			new:  "## Stage 2: Transfers\n**Status**: Not Started\n## Stage 1: Explorer\n**Status**: Not Started",
			finding: Finding{Path: "IMPLEMENTATION_PLAN.md", Line: 5, Rule: "plan.stage_header",
				Message: "Stage 2 header is out of order; expected Stage 1 at this position"},
		},
		{
			name: "stage outside allowed range",
			old:  "## Stage 6: Release\n**Status**: Not Started",
			new:  "## Stage 6: Release\n**Status**: Not Started\n## Stage 7: Extra",
			finding: Finding{Path: "IMPLEMENTATION_PLAN.md", Line: 17, Rule: "plan.stage_header",
				Message: "Stage 7 header is outside the allowed Stage 0–6 range"},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			root := prepareFixture(t, "valid")
			replacePolicyFile(t, root, "IMPLEMENTATION_PLAN.md", test.old, test.new)
			assertExactCheckFindings(t, root, []Finding{test.finding})
		})
	}
}

func planLinesForStatuses(statuses []string) []string {
	lines := []string{"# Implementation Plan", ""}
	for stage, status := range statuses {
		lines = append(lines, "## Stage "+strconv.Itoa(stage)+": Test", "**Status**: "+status)
	}
	return lines
}

func TestDurableDocumentMarker(t *testing.T) {
	assertFixtureFindings(t, "durable-marker", []expectedFinding{
		{Path: "docs/guide.md", Line: 3, Rule: "docs.marker"},
	})
}

func TestUnpinnedWorkflowAction(t *testing.T) {
	assertFixtureFindings(t, "unpinned-workflow-action", []expectedFinding{
		{Path: ".github/workflows/attack.yml", Line: 11, Rule: "workflow.action_pin"},
		{Path: ".github/workflows/attack.yml", Line: 14, Rule: "workflow.action_pin"},
	})
}

func TestDangerousWorkflowPermissions(t *testing.T) {
	assertFixtureFindings(t, "dangerous-workflow", []expectedFinding{
		{Path: ".github/workflows/attack.yml", Line: 3, Rule: "workflow.pull_request_target"},
		{Path: ".github/workflows/attack.yml", Line: 4, Rule: "workflow.top_permissions"},
		{Path: ".github/workflows/attack.yml", Line: 5, Rule: "workflow.permissions_write"},
		{Path: ".github/workflows/attack.yml", Line: 13, Rule: "workflow.persist_credentials"},
	})
}

func TestQuotedWorkflowPolicyForms(t *testing.T) {
	assertFixtureFindings(t, "quoted-workflow-attacks", []expectedFinding{
		{Path: ".github/workflows/attack.yml", Line: 3, Rule: "workflow.pull_request_target"},
		{Path: ".github/workflows/attack.yml", Line: 4, Rule: "workflow.permissions_write"},
		{Path: ".github/workflows/attack.yml", Line: 4, Rule: "workflow.top_permissions"},
		{Path: ".github/workflows/attack.yml", Line: 9, Rule: "workflow.permissions_write"},
		{Path: ".github/workflows/attack.yml", Line: 12, Rule: "workflow.action_pin"},
		{Path: ".github/workflows/attack.yml", Line: 14, Rule: "workflow.persist_credentials"},
	})
}

func TestFlowWorkflowPolicyForms(t *testing.T) {
	assertFixtureFindings(t, "flow-workflow-attacks", []expectedFinding{
		{Path: ".github/workflows/attack.yml", Line: 2, Rule: "workflow.pull_request_target"},
		{Path: ".github/workflows/attack.yml", Line: 3, Rule: "workflow.permissions_write"},
		{Path: ".github/workflows/attack.yml", Line: 3, Rule: "workflow.top_permissions"},
		{Path: ".github/workflows/attack.yml", Line: 6, Rule: "workflow.permissions_write"},
		{Path: ".github/workflows/attack.yml", Line: 9, Rule: "workflow.action_pin"},
		{Path: ".github/workflows/attack.yml", Line: 9, Rule: "workflow.persist_credentials"},
	})
}

func TestFlowMappingPullRequestTarget(t *testing.T) {
	assertFixtureFindings(t, "flow-event-workflow-attack", []expectedFinding{
		{Path: ".github/workflows/attack.yml", Line: 2, Rule: "workflow.pull_request_target"},
	})
}

func TestWorkflowPolicyIgnoresBenignActionInputs(t *testing.T) {
	root := prepareFixture(t, "benign-workflow-inputs")
	if findings := checkFixture(root); len(findings) != 0 {
		t.Fatalf("Check() returned unexpected findings:\n%s", formatFindings(findings))
	}
}

func TestWorkflowPolicyAssociatesPersistCredentialsWithSameBlockStep(t *testing.T) {
	assertFixtureFindings(t, "persist-before-uses-workflow", []expectedFinding{
		{Path: ".github/workflows/attack.yml", Line: 12, Rule: "workflow.persist_credentials"},
	})
}

func assertFixtureFindings(t *testing.T, fixture string, want []expectedFinding) {
	t.Helper()
	assertCheckFindings(t, prepareFixture(t, fixture), want)
}

func assertCheckFindings(t *testing.T, root string, want []expectedFinding) {
	t.Helper()

	findings := checkFixture(root)
	if !sort.SliceIsSorted(findings, func(left, right int) bool {
		return findingLess(findings[left], findings[right])
	}) {
		t.Fatalf("Check() findings are not deterministically sorted:\n%s", formatFindings(findings))
	}

	got := make([]expectedFinding, 0, len(findings))
	for _, finding := range findings {
		if strings.TrimSpace(finding.Message) == "" {
			t.Fatalf("finding has an empty message: %#v", finding)
		}
		got = append(got, expectedFinding{Path: finding.Path, Line: finding.Line, Rule: finding.Rule})
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("Check() findings = %#v, want %#v\nfull findings:\n%s", got, want, formatFindings(findings))
	}
}

func assertExactCheckFindings(t *testing.T, root string, want []Finding) {
	t.Helper()

	findings := checkFixture(root)
	if !reflect.DeepEqual(findings, want) {
		t.Fatalf("Check() findings = %#v, want %#v\nfull findings:\n%s", findings, want, formatFindings(findings))
	}
}

func requiredWorkflowFindings() []Finding {
	return []Finding{
		{Path: ".github/workflows/ci.yml", Line: 1, Rule: "workflow.required", Message: "required workflow file is missing or is not a regular file"},
		{Path: ".github/workflows/nightly.yml", Line: 1, Rule: "workflow.required", Message: "required workflow file is missing or is not a regular file"},
	}
}

func writeOutsideFixtureFile(t *testing.T, name string) string {
	t.Helper()
	target := filepath.Join(t.TempDir(), name)
	if err := os.WriteFile(target, []byte("outside fixture\n"), 0o600); err != nil {
		t.Fatalf("write outside fixture file: %v", err)
	}
	return target
}

func replaceFixtureFileWithOutsideSymlink(t *testing.T, root, relativePath string) {
	t.Helper()

	source := filepath.Join(root, filepath.FromSlash(relativePath))
	//nolint:gosec // source is confined to the package-owned temporary fixture.
	content, err := os.ReadFile(source)
	if err != nil {
		t.Fatalf("read fixture file %q: %v", relativePath, err)
	}
	target := filepath.Join(t.TempDir(), filepath.Base(source))
	//nolint:gosec // target is confined to a testing.T-owned temporary directory.
	if err := os.WriteFile(target, content, 0o600); err != nil {
		t.Fatalf("write outside fixture file %q: %v", relativePath, err)
	}
	if err := os.Remove(source); err != nil {
		t.Fatalf("remove fixture file %q: %v", relativePath, err)
	}
	if err := os.Symlink(target, source); err != nil {
		t.Fatalf("create fixture file symlink %q: %v", relativePath, err)
	}
}

func replaceFixtureDirectoryWithOutsideSymlink(t *testing.T, root, relativePath string) {
	t.Helper()

	source := filepath.Join(root, filepath.FromSlash(relativePath))
	target := filepath.Join(t.TempDir(), filepath.Base(source))
	copyTree(t, source, target)
	if err := os.RemoveAll(source); err != nil {
		t.Fatalf("remove fixture directory %q: %v", relativePath, err)
	}
	if err := os.Symlink(target, source); err != nil {
		t.Fatalf("create fixture directory symlink %q: %v", relativePath, err)
	}
}

func findingLess(left, right Finding) bool {
	if left.Path != right.Path {
		return left.Path < right.Path
	}
	if left.Line != right.Line {
		return left.Line < right.Line
	}
	if left.Rule != right.Rule {
		return left.Rule < right.Rule
	}
	return left.Message < right.Message
}

func formatFindings(findings []Finding) string {
	var output strings.Builder
	for _, finding := range findings {
		output.WriteString(finding.Path)
		output.WriteString(":")
		output.WriteString(strconv.Itoa(finding.Line))
		output.WriteString(": ")
		output.WriteString(finding.Rule)
		output.WriteString(": ")
		output.WriteString(finding.Message)
		output.WriteString("\n")
	}
	return output.String()
}

func checkFixture(root string) []Finding {
	findings := Check(root)
	filtered := make([]Finding, 0, len(findings))
	for _, finding := range findings {
		// Generic repository fixtures intentionally isolate non-provenance policies.
		// The production workflows and every provenance mutation are covered by
		// workflow_provenance_policy_test.go against the live canonical files.
		if finding.Rule == workflowProvenanceRecordRule || finding.Rule == workflowProvenanceVerificationRule {
			continue
		}
		filtered = append(filtered, finding)
	}
	return filtered
}

func prepareFixture(t *testing.T, fixture string) string {
	t.Helper()

	root := t.TempDir()
	copyTree(t, filepath.Join("testdata", "valid"), root)
	if fixture == "valid" {
		return root
	}

	overlay := filepath.Join("testdata", fixture)
	copyTree(t, overlay, root)
	//nolint:gosec // overlay is a package-owned testdata directory selected by the test.
	removeList, err := os.ReadFile(filepath.Join(overlay, ".remove"))
	if err != nil && !os.IsNotExist(err) {
		t.Fatalf("read fixture remove list: %v", err)
	}
	for _, path := range strings.Split(string(removeList), "\n") {
		path = strings.TrimSpace(path)
		if path == "" {
			continue
		}
		//nolint:gosec // removal is confined to the temporary fixture root and package-owned .remove entries.
		if err := os.Remove(filepath.Join(root, filepath.FromSlash(path))); err != nil {
			t.Fatalf("remove fixture path %q: %v", path, err)
		}
	}
	return root
}

func copyTree(t *testing.T, source, destination string) {
	t.Helper()

	err := filepath.WalkDir(source, func(path string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		relative, err := filepath.Rel(source, path)
		if err != nil {
			return err
		}
		if relative == ".remove" {
			return nil
		}
		target := filepath.Join(destination, relative)
		if entry.IsDir() {
			return os.MkdirAll(target, 0o750)
		}

		//nolint:gosec // path comes from walking the package-owned fixture source tree.
		input, err := os.Open(path)
		if err != nil {
			return err
		}
		//nolint:gosec // target is derived from a relative path under the temporary fixture root.
		output, err := os.OpenFile(target, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o600)
		if err != nil {
			if closeErr := input.Close(); closeErr != nil {
				return closeErr
			}
			return err
		}
		_, copyErr := io.Copy(output, input)
		inputErr := input.Close()
		outputErr := output.Close()
		if copyErr != nil {
			return copyErr
		}
		if inputErr != nil {
			return inputErr
		}
		return outputErr
	})
	if err != nil {
		t.Fatalf("copy fixture tree %q: %v", source, err)
	}
}
