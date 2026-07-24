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

func TestMissingRequiredDocumentationEntryPoint(t *testing.T) {
	root := prepareFixture(t, "valid")
	if err := os.Remove(filepath.Join(root, "README.md")); err != nil {
		t.Fatalf("remove README fixture: %v", err)
	}

	assertExactCheckFindings(t, root, []Finding{
		{
			Path: "README.md", Line: 1, Rule: "docs.required",
			Message: "required documentation entry point is missing or is not a regular file",
		},
		{
			Path: "README.zh-CN.md", Line: 3, Rule: "link.missing",
			Message: `relative link "README.md" does not resolve`,
		},
	})
}

func TestBrokenRelativeLink(t *testing.T) {
	assertFixtureFindings(t, "broken-link", []expectedFinding{
		{Path: "docs/user/getting-started.md", Line: 5, Rule: "link.missing"},
		{Path: "docs/user/getting-started.md", Line: 6, Rule: "link.escape"},
	})
}

func TestRelativeLinkRejectsFileSymlinkEscape(t *testing.T) {
	root := prepareFixture(t, "symlink-file-escape")
	target := writeOutsideFixtureFile(t, "outside.md")
	if err := os.Symlink(target, filepath.Join(root, "docs", "user", "outside-file.md")); err != nil {
		t.Fatalf("create file symlink: %v", err)
	}

	assertCheckFindings(t, root, []expectedFinding{
		{Path: "docs/user/getting-started.md", Line: 5, Rule: "link.escape"},
		{Path: "docs/user/outside-file.md", Line: 1, Rule: "docs.layout"},
	})
}

func TestRelativeLinkRejectsDirectorySymlinkEscape(t *testing.T) {
	root := prepareFixture(t, "symlink-directory-escape")
	target := writeOutsideFixtureFile(t, "outside.md")
	if err := os.Symlink(filepath.Dir(target), filepath.Join(root, "docs", "user", "outside-directory")); err != nil {
		t.Fatalf("create directory symlink: %v", err)
	}

	assertCheckFindings(t, root, []expectedFinding{
		{Path: "docs/user/getting-started.md", Line: 5, Rule: "link.escape"},
		{Path: "docs/user/outside-directory", Line: 1, Rule: "docs.layout"},
	})
}

func TestRepositoryTruthRejectsSymlinkEscapes(t *testing.T) {
	t.Run("required README final file", func(t *testing.T) {
		root := prepareFixture(t, "valid")
		replaceFixtureFileWithOutsideSymlink(t, root, "README.md")

		assertExactCheckFindings(t, root, []Finding{
			{
				Path: "README.md", Line: 1, Rule: "docs.required",
				Message: "required documentation entry point is missing or is not a regular file",
			},
			{
				Path: "README.zh-CN.md", Line: 3, Rule: "link.escape",
				Message: `relative link "README.md" resolves outside the repository`,
			},
		})
	})
}

func TestRequiredWorkflowsRejectSymlinks(t *testing.T) {
	for _, path := range []string{".github/workflows/ci.yml", ".github/workflows/fast-ci.yml", ".github/workflows/nightly.yml"} {
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

func TestDurableDocumentMarker(t *testing.T) {
	assertFixtureFindings(t, "durable-marker", []expectedFinding{
		{Path: "docs/user/getting-started.md", Line: 5, Rule: "docs.marker"},
	})
}

func TestMissingTranslatedPage(t *testing.T) {
	root := prepareFixture(t, "valid")
	path := filepath.Join(root, "docs", "zh-CN", "help", "recovery.md")
	if err := os.Remove(path); err != nil {
		t.Fatalf("remove translated page fixture: %v", err)
	}

	assertExactCheckFindings(t, root, []Finding{
		{
			Path: "docs/help/recovery.md", Line: 3, Rule: "link.missing",
			Message: `relative link "../zh-CN/help/recovery.md" does not resolve`,
		},
		{
			Path: "docs/zh-CN/help/recovery.md", Line: 1, Rule: "docs.required",
			Message: "required documentation entry point is missing or is not a regular file",
		},
	})
}

func TestTranslationPairRequiresReciprocalLinks(t *testing.T) {
	root := prepareFixture(t, "valid")
	writePolicyFile(t, root, "docs/user/transfers.md", "# Transfers\n")

	assertExactCheckFindings(t, root, []Finding{{
		Path: "docs/user/transfers.md", Line: 1, Rule: "docs.translation_link",
		Message: `English page must link to its Chinese translation "../zh-CN/user/transfers.md"`,
	}})
}

func TestTranslationPairRequiresMatchingHeadingStructure(t *testing.T) {
	root := prepareFixture(t, "valid")
	writePolicyFile(t, root, "docs/zh-CN/user/transfers.md", "# 传输\n\n[English](../../user/transfers.md)\n\n## 多余章节\n")

	assertExactCheckFindings(t, root, []Finding{{
		Path: "docs/zh-CN/user/transfers.md", Line: 1, Rule: "docs.translation_structure",
		Message: "Chinese heading levels [1 2] must match English source [1]",
	}})
}

func TestDocumentationTreeRejectsMachineMaterial(t *testing.T) {
	root := prepareFixture(t, "valid")
	writePolicyFile(t, root, "docs/user/runtime-dependencies.json", "{}\n")

	assertExactCheckFindings(t, root, []Finding{{
		Path: "docs/user/runtime-dependencies.json", Line: 1, Rule: "docs.layout",
		Message: "docs may contain only the approved human-readable pages and docs/man/amsftp.1",
	}})
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
		{Path: ".github/workflows/fast-ci.yml", Line: 1, Rule: "workflow.required", Message: "required workflow file is missing or is not a regular file"},
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
