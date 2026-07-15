package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestScanSourceRejectsHardCodedGoExecutables(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		recipe string
	}{
		{name: "plain", recipe: "go version"},
		{name: "absolute", recipe: "/opt/go/bin/go version"},
		{name: "single quoted", recipe: "'go' version"},
		{name: "double quoted", recipe: `"/opt/go/bin/go" version`},
		{name: "joined quotes", recipe: `g'o' version`},
		{name: "escaped", recipe: `g\o version`},
		{name: "silent prefix", recipe: `@go version`},
		{name: "ignore prefix", recipe: `-go version`},
		{name: "recursive prefix", recipe: `+go version`},
		{name: "combined prefixes", recipe: `@-+go version`},
		{name: "after semicolon", recipe: `printf x; go version`},
		{name: "after and", recipe: `printf x && go version`},
		{name: "after pipe", recipe: `printf x | /usr/bin/go version`},
		{name: "after assignment", recipe: `MODE=test go version`},
		{name: "after redirection", recipe: `2>/dev/null go version`},
		{name: "command substitution", recipe: `value="$$(go version)"; printf '%s\n' "$$$$value"`},
	}

	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			source := "probe:\n\t" + test.recipe + "\n"
			assertFindingContains(t, scanSource("Makefile", source), "hard-coded Go executable")
		})
	}
}

func TestScanSourceRejectsExecutionWrappers(t *testing.T) {
	t.Parallel()

	wrappers := []string{
		"env go version",
		"/usr/bin/env MODE=test go version",
		"command go version",
		"exec /usr/bin/go version",
		"nice go version",
		"nohup go version",
		"timeout 1 go version",
		"sudo go version",
		"doas go version",
		"custom-wrapper go version",
		"sh -c 'go version'",
		"xargs go version",
		"find . -exec go version \\;",
	}

	for _, recipe := range wrappers {
		recipe := recipe
		t.Run(recipe, func(t *testing.T) {
			t.Parallel()
			findings := scanSource("Makefile", "probe:\n\t"+recipe+"\n")
			if len(findings) == 0 {
				t.Fatalf("scanSource() accepted execution wrapper %q", recipe)
			}
		})
	}
}

func TestScanSourceRemovesShellBackslashNewline(t *testing.T) {
	t.Parallel()

	for _, continuation := range []string{"o version", "\to version"} {
		continuation := continuation
		t.Run(continuation, func(t *testing.T) {
			t.Parallel()
			source := "probe:\n\tg\\\n" + continuation + "\n"
			findings := scanSource("Makefile", source)
			assertFindingContains(t, findings, "hard-coded Go executable")
		})
	}
}

func TestScanSourceAllowsGoAsDataAndApprovedReferences(t *testing.T) {
	t.Parallel()

	source := strings.Join([]string{
		"GO ?= go",
		"safe:",
		"\tprintf '%s\\n' go /usr/bin/go 'go version'",
		"\t\"$(GO)\" version",
		"\tMODE=test \"$(GO)\" test ./...",
		"\t\"$(GO)\" tool -modfile=tools/go.mod actionlint .github/workflows/ci.yml",
		"",
	}, "\n")

	if findings := scanSource("Makefile", source); len(findings) != 0 {
		t.Fatalf("scanSource() returned unexpected findings:\n%s", formatFindings(findings))
	}
}

func TestScanSourceRejectsDynamicCommandReferences(t *testing.T) {
	t.Parallel()

	tests := []string{
		"$(RUN_GO) version",
		`"$$$$command" version`,
		"$$(subst X,,gXo) version",
		"$${GO_COMMAND} version",
		"`go version`",
	}
	for _, recipe := range tests {
		recipe := recipe
		t.Run(recipe, func(t *testing.T) {
			t.Parallel()
			assertFindingContains(t, scanSource("Makefile", "probe:\n\t"+recipe+"\n"), "dynamic or indirect executable")
		})
	}
}

func TestScanSourceRejectsDynamicReferencesInArguments(t *testing.T) {
	t.Parallel()

	tests := []string{
		`printf '%s\n' "$(UNAPPROVED)"`,
		`printf '%s\n' "$X"`,
		`printf '%s\n' "$$VALUE"`,
		`printf '%s\n' "$${VALUE:-$$(go version)}"`,
	}
	for _, recipe := range tests {
		recipe := recipe
		t.Run(recipe, func(t *testing.T) {
			t.Parallel()
			assertFindingContains(t, scanSource("Makefile", "probe:\n\t"+recipe+"\n"), "dynamic or indirect")
		})
	}
}

func TestScanSourceHandlesInlineAndContinuedRecipes(t *testing.T) {
	t.Parallel()

	source := strings.Join([]string{
		"inline: ; go version",
		"continued:",
		"\tprintf x; \\",
		"go version",
		"",
	}, "\n")
	findings := scanSource("Makefile", source)
	if got := countFindings(findings, "hard-coded Go executable"); got != 2 {
		t.Fatalf("hard-coded Go findings = %d, want 2:\n%s", got, formatFindings(findings))
	}
}

func TestScanSourceRejectsUnsupportedMakeExecution(t *testing.T) {
	t.Parallel()

	tests := []string{
		"include extra.mk\n",
		"-include generated.mk\n",
		"VALUE := $(shell go version)\n",
		"$(eval generated: ; go version)\n",
		".RECIPEPREFIX := >\n",
	}
	for _, source := range tests {
		source := source
		t.Run(strings.TrimSpace(source), func(t *testing.T) {
			t.Parallel()
			if findings := scanSource("Makefile", source); len(findings) == 0 {
				t.Fatalf("scanSource() accepted unsupported Make source %q", source)
			}
		})
	}
}

func TestScanSourceRejectsContinuedNonRecipeMakeSyntaxAtFirstLine(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		source  string
		line    int
		finding string
	}{
		{
			name: "global execution control",
			source: strings.Join([]string{
				"# preamble",
				"MAKEFLAGS \\",
				"+= -n",
				"",
			}, "\n"),
			line:    2,
			finding: "recipe execution control MAKEFLAGS",
		},
		{
			name: "target-specific execution control",
			source: strings.Join([]string{
				"probe: private SHELL \\",
				"= $(GO)",
				"",
			}, "\n"),
			line:    1,
			finding: "recipe execution control SHELL",
		},
		{
			name: "include directive",
			source: strings.Join([]string{
				"include\\",
				" extra.mk",
				"",
			}, "\n"),
			line:    1,
			finding: "included Make sources",
		},
		{
			name: "shell execution",
			source: strings.Join([]string{
				"# preamble",
				"VALUE := \\",
				"$(shell go version)",
				"",
			}, "\n"),
			line:    2,
			finding: "parse-time shell/eval execution",
		},
		{
			name: "eval execution",
			source: strings.Join([]string{
				"VALUE := \\",
				"$(eval generated: ; go version)",
				"",
			}, "\n"),
			line:    1,
			finding: "parse-time shell/eval execution",
		},
	}

	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			assertFindingAtLineContains(t, scanSource("Makefile", test.source), test.line, test.finding)
		})
	}
}

func TestScanSourceRejectsRecipeExecutionControls(t *testing.T) {
	t.Parallel()

	tests := []string{
		"SHELL = $(GO)\n",
		"override .SHELLFLAGS := -c\n",
		"fmt-check vet lint test build-all: SHELL = $(GO)\n",
		"fmt-check: private SHELL = /bin/true\n",
		"fmt-check: private .SHELLFLAGS := -c\n",
		"MAKEFLAGS += -n\n",
		"target: GNUMAKEFLAGS = --touch\n",
		"MAKEFILES := preload.mk\n",
		".ONESHELL:\n",
		".IGNORE: fmt-check test\n",
	}
	for _, source := range tests {
		source := source
		t.Run(strings.TrimSpace(source), func(t *testing.T) {
			t.Parallel()
			assertFindingContains(t, scanSource("Makefile", source), "recipe execution control")
		})
	}
}

func TestCanonicalMakefileDeferredGuards(t *testing.T) {
	t.Parallel()

	workingDirectory, err := os.Getwd()
	if err != nil {
		t.Fatalf("os.Getwd() returned %v", err)
	}
	canonicalPath := filepath.Join(workingDirectory, "..", "..", "..", "Makefile")
	canonical, err := os.ReadFile(canonicalPath) //nolint:gosec // path uses only the package cwd and fixed repository-relative components.
	if err != nil {
		t.Fatalf("read canonical Makefile: %v", err)
	}

	tests := []struct {
		name            string
		addition        string
		beforeCanonical bool
		afterCanonical  bool
		wantFailure     string
	}{
		{name: "normal"},
		{name: "pre-baseline recipe shell", addition: "SHELL = /bin/true\n", beforeCanonical: true, wantFailure: "recipe execution control SHELL"},
		{name: "pre-baseline shell flags", addition: ".SHELLFLAGS = -c\n", beforeCanonical: true, wantFailure: "recipe execution control .SHELLFLAGS"},
		{name: "pre-baseline recipe prefix", addition: "make-contract-flags: .RECIPEPREFIX = >\n", beforeCanonical: true, wantFailure: "recipe execution control .RECIPEPREFIX"},
		{name: "continued dry run", addition: "MAKEFLAGS \\\n+= -n\n", wantFailure: "execution-skipping Make flags n/i/q/t"},
		{name: "continued ignore errors", addition: "MAKEFLAGS \\\n+= -i\n", wantFailure: "execution-skipping Make flags n/i/q/t"},
		{name: "continued question", addition: "MAKEFLAGS \\\n+= -q\n", wantFailure: "execution-skipping Make flags n/i/q/t"},
		{name: "continued touch", addition: "MAKEFLAGS \\\n+= -t\n", wantFailure: "execution-skipping Make flags n/i/q/t"},
		{name: "late touch", addition: "MAKEFLAGS += -t\n", afterCanonical: true, wantFailure: "execution-skipping Make flags n/i/q/t"},
		{name: "continued late touch", addition: "MAKEFLAGS \\\n+= -t\n", afterCanonical: true, wantFailure: "execution-skipping Make flags n/i/q/t"},
		{name: "target-specific recipe shell", addition: "make-contract-flags: SHELL = /bin/true\n", wantFailure: "recipe execution control SHELL"},
	}

	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			source := string(canonical)
			if test.afterCanonical {
				source += "\n" + test.addition
			} else if test.beforeCanonical {
				source = test.addition + "\n" + source
			} else if test.addition != "" {
				source = insertAfterInitialMakeGuard(t, source, test.addition)
			}
			output, runErr := runTemporaryMakefile(t, source, "make-contract-flags")
			if test.wantFailure == "" {
				if runErr != nil {
					t.Fatalf("normal Makefile failed: %v\n%s", runErr, output)
				}
				return
			}
			if runErr == nil {
				t.Fatalf("malicious Makefile succeeded; output:\n%s", output)
			}
			if !strings.Contains(output, test.wantFailure) {
				t.Fatalf("failure output did not contain %q:\n%s", test.wantFailure, output)
			}
		})
	}
}

func TestCanonicalMakefileForcesEveryRecipeGuard(t *testing.T) {
	t.Parallel()

	workingDirectory, err := os.Getwd()
	if err != nil {
		t.Fatalf("os.Getwd() returned %v", err)
	}
	canonicalPath := filepath.Join(workingDirectory, "..", "..", "..", "Makefile")
	canonical, err := os.ReadFile(canonicalPath) //nolint:gosec // path uses only the package cwd and fixed repository-relative components.
	if err != nil {
		t.Fatalf("read canonical Makefile: %v", err)
	}

	const forcedGuard = "\t+@: $(MAKE_CONTRACT_RECIPE_GUARD)"
	guardCount := 0
	for _, line := range strings.Split(string(canonical), "\n") {
		if !strings.HasPrefix(line, "\t") || !strings.Contains(line, "$(MAKE_CONTRACT_RECIPE_GUARD)") {
			continue
		}
		guardCount++
		if line != forcedGuard {
			t.Fatalf("canonical Make recipe guard is not forced: %q", line)
		}
	}
	if guardCount != 14 {
		t.Fatalf("canonical forced Make recipe guards = %d, want 14", guardCount)
	}
}

func TestCanonicalMakefileAcceptsCommandLineOutputDirectoriesWithSpaces(t *testing.T) {
	workingDirectory, err := os.Getwd()
	if err != nil {
		t.Fatalf("os.Getwd() returned %v", err)
	}
	canonicalPath := filepath.Join(workingDirectory, "..", "..", "..", "Makefile")
	canonical, err := os.ReadFile(canonicalPath) //nolint:gosec // path uses only the package cwd and fixed repository-relative components.
	if err != nil {
		t.Fatalf("read canonical Makefile: %v", err)
	}

	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "Makefile"), canonical, 0o600); err != nil { //nolint:gosec // root is a testing.T-owned temporary directory and the filename is fixed.
		t.Fatalf("write temporary Makefile: %v", err)
	}
	outputRoot := t.TempDir()
	//nolint:gosec // executable, target, and directory arguments are test-owned values passed without a shell.
	command := exec.Command(
		"make",
		"BUILD_DIR="+filepath.Join(outputRoot, "build -n outputs"),
		"COVERAGE_DIR="+filepath.Join(outputRoot, "coverage outputs"),
		"make-contract-flags",
	)
	command.Dir = root
	command.Env = cleanMakeEnvironment(os.Environ())
	if output, runErr := command.CombinedOutput(); runErr != nil {
		t.Fatalf("canonical Makefile rejected safe command-line output directories: %v\n%s", runErr, output)
	}
}

func TestCanonicalMakefileRejectsCommandLineGuardOverrides(t *testing.T) {
	workingDirectory, err := os.Getwd()
	if err != nil {
		t.Fatalf("os.Getwd() returned %v", err)
	}
	repositoryRoot := filepath.Clean(filepath.Join(workingDirectory, "..", "..", ".."))
	tests := []struct {
		name      string
		arguments []string
		want      string
	}{
		{
			name:      "execution result",
			arguments: []string{"-n", "MAKE_EXECUTION_SKIP_FLAGS="},
			want:      "execution-skipping Make flags n/i/q/t",
		},
		{
			name:      "execution helper",
			arguments: []string{"-n", "make_execution_skip_word="},
			want:      "execution-skipping Make flags n/i/q/t",
		},
		{
			name:      "flag inputs",
			arguments: []string{"-n", "MAKEFLAGS=", "MFLAGS="},
			want:      "command-line assignment of MAKEFLAGS or MFLAGS is forbidden",
		},
		{
			name:      "recipe guard",
			arguments: []string{"SHELL=/usr/bin/true", "MAKE_CONTRACT_RECIPE_GUARD="},
			want:      "recipe execution control SHELL",
		},
		{
			name:      "shell helper",
			arguments: []string{"SHELL=/usr/bin/true", "make_contract_value_changed="},
			want:      "recipe execution control SHELL",
		},
		{
			name:      "shell result",
			arguments: []string{"SHELL=/usr/bin/true", "MAKE_CONTRACT_SHELL_CHANGED="},
			want:      "recipe execution control SHELL",
		},
		{
			name:      "shell flags result",
			arguments: []string{".SHELLFLAGS=-c", "MAKE_CONTRACT_SHELLFLAGS_CHANGED="},
			want:      "recipe execution control .SHELLFLAGS",
		},
		{
			name:      "recipe prefix result",
			arguments: []string{".RECIPEPREFIX=", "MAKE_CONTRACT_RECIPEPREFIX_CHANGED="},
			want:      "recipe execution control .RECIPEPREFIX",
		},
	}

	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			arguments := append([]string{"--no-print-directory"}, test.arguments...)
			arguments = append(arguments, "make-contract-flags")
			//nolint:gosec // executable, target, and assignments are fixed test-owned values passed without a shell.
			command := exec.Command("make", arguments...)
			command.Dir = repositoryRoot
			command.Env = cleanMakeEnvironment(os.Environ())
			output, runErr := command.CombinedOutput()
			if runErr == nil {
				t.Fatalf("command-line guard override succeeded; output:\n%s", output)
			}
			if !strings.Contains(string(output), test.want) {
				t.Fatalf("failure output did not contain %q:\n%s", test.want, output)
			}
		})
	}
}

func TestVerifyRecipeGuardCoverage(t *testing.T) {
	t.Parallel()

	guarded := []recipe{
		{target: "first second", text: ": $(MAKE_CONTRACT_RECIPE_GUARD)"},
		{target: "first second", text: `"$(GO)" version`},
	}
	if err := verifyRecipeGuardCoverage(guarded); err != nil {
		t.Fatalf("verifyRecipeGuardCoverage() rejected guarded targets: %v", err)
	}

	unguarded := []recipe{
		{target: "first", text: ": $(MAKE_CONTRACT_RECIPE_GUARD)"},
		{target: "first", text: `"$(GO)" version`},
		{target: "second", text: `"$(GO)" version`},
		{target: "second", text: ": $(MAKE_CONTRACT_RECIPE_GUARD)"},
	}
	err := verifyRecipeGuardCoverage(unguarded)
	if err == nil || !strings.Contains(err.Error(), "second") {
		t.Fatalf("verifyRecipeGuardCoverage() error = %v, want missing first guard for second", err)
	}
}

func TestVerifyContractRejectsTargetSpecificRecipeShell(t *testing.T) {
	workingDirectory, err := os.Getwd()
	if err != nil {
		t.Fatalf("os.Getwd() returned %v", err)
	}
	t.Cleanup(func() {
		if err := os.Chdir(workingDirectory); err != nil {
			t.Errorf("restore working directory: %v", err)
		}
	})

	root := t.TempDir()
	malicious := "fmt-check vet lint test test-contract test-race fuzz-smoke docs-check mod-check supply-chain build-all: SHELL = $(GO)\n"
	if err := os.WriteFile(filepath.Join(root, "Makefile"), []byte(malicious), 0o600); err != nil {
		t.Fatalf("write malicious Makefile: %v", err)
	}
	if err := os.Chdir(root); err != nil {
		t.Fatalf("change to malicious repository: %v", err)
	}

	err = verifyContract("go")
	if err == nil || !strings.Contains(err.Error(), "recipe execution control SHELL") {
		t.Fatalf("verifyContract() error = %v, want SHELL control rejection", err)
	}
}

func TestScanSourceReportsMalformedRecipeContinuation(t *testing.T) {
	t.Parallel()

	findings := scanSource("Makefile", "probe:\n\tprintf x; \\")
	assertFindingContains(t, findings, "unterminated recipe continuation")
}

func TestScanSourcePreservesContinuedRecipeStartLine(t *testing.T) {
	t.Parallel()

	source := strings.Join([]string{
		"probe:",
		"\tprintf x; \\",
		"go version",
		"",
	}, "\n")
	assertFindingAtLineContains(t, scanSource("Makefile", source), 2, "hard-coded Go executable")
}

func TestVerifyProbeTargetCoverageRejectsUnlistedGoTarget(t *testing.T) {
	t.Parallel()

	var recipes []recipe
	for _, target := range goProbeTargets {
		recipes = append(recipes, recipe{target: target, text: `"$(GO)" version`})
	}
	recipes = append(recipes,
		recipe{target: "make-contract-scan", text: `"$(GO)" run checker`},
		recipe{target: "make-contract", text: `"$(GO)" run verifier`},
		recipe{target: "new-go-target", text: `"$(GO)" test ./...`},
	)

	err := verifyProbeTargetCoverage(recipes)
	if err == nil || !strings.Contains(err.Error(), "new-go-target") {
		t.Fatalf("verifyProbeTargetCoverage() error = %v, want unlisted target", err)
	}
}

func TestVerifyProbeTargetCoverageAcceptsExactTargetSet(t *testing.T) {
	t.Parallel()

	var recipes []recipe
	for _, target := range goProbeTargets {
		recipes = append(recipes, recipe{target: target, text: `"$(GO)" version`})
	}
	recipes = append(recipes,
		recipe{target: "make-contract-scan", text: `"$(GO)" run checker`},
		recipe{target: "make-contract", text: `"$(GO)" run verifier`},
	)
	if err := verifyProbeTargetCoverage(recipes); err != nil {
		t.Fatalf("verifyProbeTargetCoverage() returned %v", err)
	}
}

func TestValidateProbeEntriesRejectsGoUsedAsRecipeShell(t *testing.T) {
	t.Parallel()

	root := "/repo"
	valid := []string{root + "\trun ./internal/tools/makecontract fmt cmd internal"}
	if err := validateProbeEntries(root, "fmt-check", valid); err != nil {
		t.Fatalf("validateProbeEntries() rejected direct Go invocation: %v", err)
	}

	shell := []string{root + "\t-c \"/tmp/go-probe run ./internal/tools/makecontract fmt cmd internal\""}
	if err := validateProbeEntries(root, "fmt-check", shell); err == nil || !strings.Contains(err.Error(), "recipe shell") {
		t.Fatalf("validateProbeEntries() error = %v, want recipe-shell rejection", err)
	}
}

func TestValidateProbeEntriesRejectsWrongInvocationShape(t *testing.T) {
	t.Parallel()

	root := "/repo"
	tests := []struct {
		name    string
		target  string
		entries []string
	}{
		{name: "wrong verb", target: "vet", entries: []string{root + "\ttest ./..."}},
		{name: "wrong count", target: "fuzz-smoke", entries: []string{root + "\ttest -run=^$ -fuzz=FuzzFrameDecoder"}},
		{name: "wrong directory", target: "lint", entries: []string{"/repo/tools\ttool -modfile=tools/go.mod golangci-lint run ./..."}},
	}
	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			if err := validateProbeEntries(root, test.target, test.entries); err == nil {
				t.Fatalf("validateProbeEntries() accepted %s", test.name)
			}
		})
	}
}

func assertFindingContains(t *testing.T, findings []finding, substring string) {
	t.Helper()
	for _, finding := range findings {
		if strings.Contains(finding.Message, substring) {
			return
		}
	}
	t.Fatalf("findings did not contain %q:\n%s", substring, formatFindings(findings))
}

func insertAfterInitialMakeGuard(t *testing.T, source, addition string) string {
	t.Helper()
	const marker = "endif\n\nGO ?= go"
	if !strings.Contains(source, marker) {
		t.Fatalf("canonical Makefile does not contain the initial guard marker")
	}
	return strings.Replace(source, marker, "endif\n\n"+addition+"\nGO ?= go", 1)
}

func runTemporaryMakefile(t *testing.T, source string, target string) (string, error) {
	t.Helper()
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "Makefile"), []byte(source), 0o600); err != nil { //nolint:gosec // root is a testing.T-owned temporary directory and the filename is fixed.
		t.Fatalf("write temporary Makefile: %v", err)
	}
	command := exec.Command("make", "--no-print-directory", target) //nolint:gosec // make is constant and target is selected by package-owned tests without a shell.
	command.Dir = root
	command.Env = cleanMakeEnvironment(os.Environ())
	output, err := command.CombinedOutput()
	return string(output), err
}

func assertFindingAtLineContains(t *testing.T, findings []finding, line int, substring string) {
	t.Helper()
	for _, finding := range findings {
		if finding.Line == line && strings.Contains(finding.Message, substring) {
			return
		}
	}
	t.Fatalf("findings did not contain %q at line %d:\n%s", substring, line, formatFindings(findings))
}

func countFindings(findings []finding, substring string) int {
	count := 0
	for _, finding := range findings {
		if strings.Contains(finding.Message, substring) {
			count++
		}
	}
	return count
}
