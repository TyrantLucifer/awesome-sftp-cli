package main

import (
	"reflect"
	"strings"
	"testing"
)

func TestBuildPlanRoutesDocumentationWithoutCode(t *testing.T) {
	t.Parallel()

	got := buildPlan("/repo", []string{"README.md", "docs/user/getting-started.md"}, nil)
	if !got.Docs || got.Code || got.Full {
		t.Fatalf("buildPlan() = %#v, want docs-only plan", got)
	}
	if len(got.Packages) != 0 {
		t.Fatalf("packages = %v, want none", got.Packages)
	}
}

func TestGitObjectIDValidation(t *testing.T) {
	t.Parallel()

	for _, value := range []string{
		"0123456789abcdef0123456789abcdef01234567",
		"0123456789ABCDEF0123456789ABCDEF01234567",
		"0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef",
	} {
		if !isGitObjectID(value) {
			t.Fatalf("isGitObjectID(%q) = false, want true", value)
		}
	}
	for _, value := range []string{"main", "--output=/tmp/result", "abc123", strings.Repeat("g", 40)} {
		if isGitObjectID(value) {
			t.Fatalf("isGitObjectID(%q) = true, want false", value)
		}
	}
}

func TestBuildPlanUsesReversePackageDependencies(t *testing.T) {
	t.Parallel()

	packages := []packageInfo{
		{ImportPath: modulePath + "/internal/tui", Dir: "/repo/internal/tui"},
		{ImportPath: modulePath + "/internal/app", Dir: "/repo/internal/app", Imports: []string{modulePath + "/internal/tui"}},
		{ImportPath: modulePath + "/cmd/amsftp", Dir: "/repo/cmd/amsftp", Imports: []string{modulePath + "/internal/app"}},
		{ImportPath: modulePath + "/internal/daemon", Dir: "/repo/internal/daemon"},
	}

	got := buildPlan("/repo", []string{"internal/tui/model.go"}, packages)
	want := []string{"./cmd/amsftp", "./internal/app", "./internal/tui"}
	if !got.Code || got.Full || !reflect.DeepEqual(got.Packages, want) {
		t.Fatalf("buildPlan() = %#v, want packages %v", got, want)
	}
}

func TestBuildPlanDoesNotPropagateTestOnlyChanges(t *testing.T) {
	t.Parallel()

	packages := []packageInfo{
		{ImportPath: modulePath + "/internal/tui", Dir: "/repo/internal/tui"},
		{ImportPath: modulePath + "/internal/app", Dir: "/repo/internal/app", Imports: []string{modulePath + "/internal/tui"}},
	}

	got := buildPlan("/repo", []string{"internal/tui/model_test.go"}, packages)
	want := []string{"./internal/tui"}
	if !reflect.DeepEqual(got.Packages, want) {
		t.Fatalf("packages = %v, want %v", got.Packages, want)
	}
}

func TestBuildPlanAddsRiskOverlays(t *testing.T) {
	t.Parallel()

	packages := []packageInfo{
		{ImportPath: modulePath + "/internal/transfer", Dir: "/repo/internal/transfer"},
		{ImportPath: modulePath + "/internal/provider/sftp", Dir: "/repo/internal/provider/sftp"},
		{ImportPath: modulePath + "/internal/platform", Dir: "/repo/internal/platform"},
		{ImportPath: modulePath + "/internal/integration", Dir: "/repo/internal/integration"},
	}

	got := buildPlan("/repo", []string{
		"internal/transfer/execute.go",
		"internal/provider/sftp/provider.go",
		"internal/platform/paths_darwin.go",
		"internal/integration/hosted-auth.sh",
		"docs/user/installation.md",
	}, packages)
	if !got.Core || !got.Auth || !got.Native || !got.Release || !got.Docs || !got.Code {
		t.Fatalf("buildPlan() = %#v, want all risk overlays", got)
	}
}

func TestBuildPlanFailsOpenForGlobalOrUnknownChanges(t *testing.T) {
	t.Parallel()

	for _, path := range []string{
		"go.mod",
		"Makefile",
		"internal/tools/ciimpact/main.go",
		"internal/removed-package/file.go",
		"mystery/config.bin",
	} {
		path := path
		t.Run(path, func(t *testing.T) {
			t.Parallel()
			got := buildPlan("/repo", []string{path}, nil)
			if !got.Full || !got.Code || !reflect.DeepEqual(got.Packages, []string{"./..."}) {
				t.Fatalf("buildPlan(%q) = %#v, want fail-open full plan", path, got)
			}
		})
	}
}

func TestBuildPlanRoutesWorkflowChanges(t *testing.T) {
	t.Parallel()

	got := buildPlan("/repo", []string{".github/workflows/fast-ci.yml"}, nil)
	if !got.Workflow || got.Full || got.Code {
		t.Fatalf("buildPlan() = %#v, want workflow-only plan", got)
	}
}

func TestBuildPlanMapsPackageOwnedTestdata(t *testing.T) {
	t.Parallel()

	packages := []packageInfo{{ImportPath: modulePath + "/internal/tui", Dir: "/repo/internal/tui"}}
	got := buildPlan("/repo", []string{"internal/tui/testdata/screen.txt"}, packages)
	if !got.Code || !reflect.DeepEqual(got.Packages, []string{"./internal/tui"}) {
		t.Fatalf("buildPlan() = %#v, want TUI package plan", got)
	}
}

func TestBuildPlanFailsOpenWhenThereAreNoChanges(t *testing.T) {
	t.Parallel()

	got := buildPlan("/repo", nil, nil)
	if !got.Full || !reflect.DeepEqual(got.Packages, []string{"./..."}) {
		t.Fatalf("buildPlan() = %#v, want full plan", got)
	}
}

func TestFormatGitHubOutputsIsDeterministic(t *testing.T) {
	t.Parallel()

	got, err := formatGitHubOutputs(plan{
		Docs:     true,
		Code:     true,
		Core:     true,
		Packages: []string{"./internal/transfer", "./internal/app"},
	})
	if err != nil {
		t.Fatal(err)
	}
	wantLines := []string{
		"docs=true",
		"code=true",
		"workflow=false",
		"dependencies=false",
		"core=true",
		"native=false",
		"auth=false",
		"release=false",
		"full=false",
		`packages=["./internal/app","./internal/transfer"]`,
	}
	if got != strings.Join(wantLines, "\n")+"\n" {
		t.Fatalf("formatGitHubOutputs() =\n%s\nwant\n%s", got, strings.Join(wantLines, "\n"))
	}
}

func TestFormatGitHubOutputsUsesEmptyPackageArray(t *testing.T) {
	t.Parallel()

	got, err := formatGitHubOutputs(plan{Docs: true})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(got, "packages=[]\n") {
		t.Fatalf("formatGitHubOutputs() =\n%s\nwant an empty package array", got)
	}
}
