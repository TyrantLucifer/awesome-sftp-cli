package main

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
)

var goProbeTargets = []string{
	"fmt-check",
	"vet",
	"lint",
	"test",
	"test-contract",
	"test-race",
	"test-scale",
	"bench-scale",
	"fuzz-smoke",
	"docs-check",
	"notice-check",
	"mod-check",
	"supply-chain",
	"build-all",
}

const makeContractRecipeGuard = ": $(MAKE_CONTRACT_RECIPE_GUARD)"

type probeShape struct {
	count    int
	prefixes []string
}

var probeShapes = map[string]probeShape{
	"fmt-check":     {count: 1, prefixes: []string{"run ./internal/tools/makecontract fmt cmd internal"}},
	"vet":           {count: 1, prefixes: []string{"vet "}},
	"lint":          {count: 1, prefixes: []string{"tool -modfile=tools/go.mod golangci-lint "}},
	"test":          {count: 1, prefixes: []string{"test "}},
	"test-contract": {count: 1, prefixes: []string{"test "}},
	"test-race":     {count: 1, prefixes: []string{"test "}},
	"test-scale":    {count: 1, prefixes: []string{"test "}},
	"bench-scale":   {count: 1, prefixes: []string{"test "}},
	"fuzz-smoke":    {count: 4, prefixes: []string{"test "}},
	"docs-check":    {count: 1, prefixes: []string{"run ./internal/tools/docscheck ."}},
	"notice-check":  {count: 1, prefixes: []string{"run ./internal/tools/releasenotice --check docs/release/runtime-dependencies.json docs/release/license-materials.json docs/release/NOTICE"}},
	"mod-check":     {count: 4, prefixes: []string{"mod ", "-C tools mod "}},
	"supply-chain":  {count: 2, prefixes: []string{"tool -modfile=tools/go.mod "}},
	"build-all":     {count: 4, prefixes: []string{"build "}},
}

func verifyContract(goExecutable string) error {
	root, err := os.Getwd()
	if err != nil {
		return fmt.Errorf("resolve current repository root: %w", err)
	}
	source, err := os.ReadFile("Makefile")
	if err != nil {
		return fmt.Errorf("read canonical Makefile: %w", err)
	}
	if findings := scanSource("Makefile", string(source)); len(findings) != 0 {
		return errors.New(strings.TrimSpace(formatFindings(findings)))
	}
	recipes, extractionFindings := extractRecipes("Makefile", string(source))
	if len(extractionFindings) != 0 {
		return errors.New(strings.TrimSpace(formatFindings(extractionFindings)))
	}
	if err := verifyRecipeGuardCoverage(recipes); err != nil {
		return err
	}
	if err := verifyProbeTargetCoverage(recipes); err != nil {
		return err
	}

	probeRoot, err := os.MkdirTemp("", "amsftp-make-contract.*")
	if err != nil {
		return fmt.Errorf("create Make probe directory: %w", err)
	}
	defer os.RemoveAll(probeRoot)

	probeGo := filepath.Join(probeRoot, "go-probe")
	pathBin := filepath.Join(probeRoot, "path")
	pathGo := filepath.Join(pathBin, "go")
	probeLog := filepath.Join(probeRoot, "go-probe.log")
	pathLog := filepath.Join(probeRoot, "path-go.log")
	if err := os.Mkdir(pathBin, 0o700); err != nil {
		return fmt.Errorf("create PATH probe directory: %w", err)
	}
	probeScript := "#!/bin/sh\n" +
		"printf '%s\\t%s\\n' \"$PWD\" \"$*\" >>\"$AMSFTP_GO_PROBE_LOG\"\n"
	pathScript := "#!/bin/sh\n" +
		"printf '%s\\t%s\\n' \"$PWD\" \"$*\" >>\"$AMSFTP_PATH_GO_LOG\"\n" +
		"exit 97\n"
	if err := writeProbeExecutable(probeGo, probeScript); err != nil {
		return fmt.Errorf("write Go probe: %w", err)
	}
	if err := writeProbeExecutable(pathGo, pathScript); err != nil {
		return fmt.Errorf("write PATH Go probe: %w", err)
	}

	baseEnvironment := cleanMakeEnvironment(os.Environ())
	baseEnvironment = setEnvironment(baseEnvironment, "PATH", pathBin+string(os.PathListSeparator)+os.Getenv("PATH"))
	baseEnvironment = setEnvironment(baseEnvironment, "AMSFTP_GO_PROBE_LOG", probeLog)
	baseEnvironment = setEnvironment(baseEnvironment, "AMSFTP_PATH_GO_LOG", pathLog)

	for _, target := range goProbeTargets {
		if err := os.WriteFile(probeLog, nil, 0o600); err != nil {
			return fmt.Errorf("clear Go probe log: %w", err)
		}
		if err := os.WriteFile(pathLog, nil, 0o600); err != nil {
			return fmt.Errorf("clear PATH Go probe log: %w", err)
		}
		arguments := []string{
			"--no-print-directory",
			"GO=" + probeGo,
			"BUILD_DIR=" + filepath.Join(probeRoot, "build outputs"),
			"COVERAGE_DIR=" + filepath.Join(probeRoot, "coverage outputs"),
			target,
		}
		output, runErr := runMake(root, baseEnvironment, arguments...)
		if runErr != nil {
			return fmt.Errorf("GO override probe for %s failed: %w\n%s", target, runErr, output)
		}
		entries, err := readProbeLog(probeLog)
		if err != nil {
			return err
		}
		if len(entries) == 0 {
			return fmt.Errorf("GO override did not reach target %s", target)
		}
		if err := validateProbeEntries(root, target, entries); err != nil {
			return err
		}
		pathEntries, err := readProbeLog(pathLog)
		if err != nil {
			return err
		}
		if len(pathEntries) != 0 {
			return fmt.Errorf("target %s invoked PATH go instead of $(GO): %s", target, strings.Join(pathEntries, "; "))
		}
		if target == "lint" || target == "supply-chain" {
			if err := verifyToolInvocations(root, target, entries); err != nil {
				return err
			}
		}
	}

	if err := verifyExecutionFlags(goExecutable, root, probeRoot, baseEnvironment); err != nil {
		return err
	}
	return nil
}

func verifyRecipeGuardCoverage(recipes []recipe) error {
	seen := make(map[string]struct{})
	missing := make(map[string]struct{})
	misplaced := make(map[string]struct{})
	for _, recipe := range recipes {
		text := strings.TrimSpace(recipe.text)
		usesGuard := strings.Contains(text, "$(MAKE_CONTRACT_RECIPE_GUARD)")
		for _, target := range strings.Fields(recipe.target) {
			_, alreadySeen := seen[target]
			if alreadySeen && usesGuard {
				misplaced[target] = struct{}{}
			}
			if alreadySeen {
				continue
			}
			seen[target] = struct{}{}
			if text != makeContractRecipeGuard {
				missing[target] = struct{}{}
			}
		}
		if usesGuard && text != makeContractRecipeGuard {
			return fmt.Errorf("make recipe guard has unexpected command shape for %s: %s", recipe.target, text)
		}
	}
	if len(missing) == 0 && len(misplaced) == 0 {
		return nil
	}
	missingTargets := sortedKeys(missing)
	misplacedTargets := sortedKeys(misplaced)
	return fmt.Errorf("make recipe guard coverage mismatch: missing-first=%v misplaced=%v", missingTargets, misplacedTargets)
}

func sortedKeys(values map[string]struct{}) []string {
	keys := make([]string, 0, len(values))
	for value := range values {
		keys = append(keys, value)
	}
	sort.Strings(keys)
	return keys
}

func validateProbeEntries(root, target string, entries []string) error {
	shape, ok := probeShapes[target]
	if !ok {
		return fmt.Errorf("no GO probe shape is defined for target %s", target)
	}
	if len(entries) != shape.count {
		return fmt.Errorf("GO probe target %s recorded %d invocations, want %d", target, len(entries), shape.count)
	}
	for _, entry := range entries {
		fields := strings.SplitN(entry, "\t", 2)
		if len(fields) != 2 {
			return fmt.Errorf("malformed Go probe entry for %s: %q", target, entry)
		}
		if filepath.Clean(fields[0]) != filepath.Clean(root) {
			return fmt.Errorf("GO probe target %s ran outside product root: %s", target, fields[0])
		}
		arguments := strings.TrimSpace(fields[1])
		words := strings.Fields(arguments)
		if len(words) == 0 {
			return fmt.Errorf("GO probe target %s recorded an empty invocation", target)
		}
		if strings.HasPrefix(words[0], "-") && (target != "mod-check" || words[0] != "-C") {
			return fmt.Errorf("GO probe target %s used fake Go as a recipe shell: %s", target, arguments)
		}
		matches := false
		for _, prefix := range shape.prefixes {
			if strings.HasPrefix(arguments, prefix) {
				matches = true
				break
			}
		}
		if !matches {
			return fmt.Errorf("GO probe target %s recorded unexpected invocation: %s", target, arguments)
		}
	}
	return nil
}

func writeProbeExecutable(path, content string) error {
	// #nosec G304 -- path is a checker-created file in a private temporary directory.
	file, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return err
	}
	if _, err = file.WriteString(content); err != nil {
		_ = file.Close()
		return err
	}
	if err = file.Close(); err != nil {
		return err
	}
	// #nosec G302 -- this private temporary probe must be executable by its owner.
	return os.Chmod(path, 0o700)
}

func verifyProbeTargetCoverage(recipes []recipe) error {
	actual := make(map[string]struct{})
	for _, recipe := range recipes {
		if !strings.Contains(recipe.text, "$(GO)") {
			continue
		}
		for _, target := range strings.Fields(recipe.target) {
			actual[target] = struct{}{}
		}
	}
	delete(actual, "make-contract")
	delete(actual, "make-contract-scan")

	expected := make(map[string]struct{}, len(goProbeTargets))
	for _, target := range goProbeTargets {
		expected[target] = struct{}{}
	}
	var missing []string
	var unlisted []string
	for target := range expected {
		if _, ok := actual[target]; !ok {
			missing = append(missing, target)
		}
	}
	for target := range actual {
		if _, ok := expected[target]; !ok {
			unlisted = append(unlisted, target)
		}
	}
	if len(missing) == 0 && len(unlisted) == 0 {
		return nil
	}
	sort.Strings(missing)
	sort.Strings(unlisted)
	return fmt.Errorf("GO runtime probe target set mismatch: missing=%v unlisted=%v", missing, unlisted)
}

func verifyToolInvocations(root, target string, entries []string) error {
	for _, entry := range entries {
		fields := strings.SplitN(entry, "\t", 2)
		if len(fields) != 2 {
			return fmt.Errorf("malformed Go probe entry for %s: %q", target, entry)
		}
		if filepath.Clean(fields[0]) != filepath.Clean(root) {
			return fmt.Errorf("third-party tool target %s ran outside product root: %s", target, fields[0])
		}
		if strings.Contains(fields[1], "-C tools") {
			return fmt.Errorf("third-party tool target %s changed into the tools module", target)
		}
	}
	joined := strings.Join(entries, "\n")
	if !strings.Contains(joined, "tool -modfile=tools/go.mod") {
		return fmt.Errorf("third-party tool target %s did not use the pinned tools module", target)
	}
	return nil
}

func verifyExecutionFlags(goExecutable, root, probeRoot string, environment []string) error {
	skipFlags := [][]string{
		{"-n"},
		{"-kn"},
		{"-i"},
		{"-ki"},
		{"-q"},
		{"-t"},
		{"--just-print"},
		{"--dry-run"},
		{"--recon"},
		{"--ignore-errors"},
		{"--question"},
		{"--touch"},
	}
	for _, flag := range skipFlags {
		arguments := append([]string{"--no-print-directory"}, flag...)
		arguments = append(arguments, "GO="+goExecutable, "make-contract-flags")
		output, err := runMake(root, environment, arguments...)
		if err == nil {
			return fmt.Errorf("execution-skipping Make flag %s bypassed the parse-time guard", strings.Join(flag, " "))
		}
		if !strings.Contains(output, "execution-skipping Make flags n/i/q/t") {
			return fmt.Errorf("execution-skipping Make flag %s failed unexpectedly: %s", strings.Join(flag, " "), output)
		}
	}

	overrideCases := []struct {
		name      string
		arguments []string
		want      string
	}{
		{name: "skip-letter helper", arguments: []string{"-n", "make_execution_skip_letters="}, want: "execution-skipping Make flags n/i/q/t"},
		{name: "skip-word helper", arguments: []string{"-n", "make_execution_skip_word="}, want: "execution-skipping Make flags n/i/q/t"},
		{name: "option-prefix helper", arguments: []string{"-n", "make_flag_option_prefix="}, want: "execution-skipping Make flags n/i/q/t"},
		{name: "flag-candidate helper", arguments: []string{"-n", "make_execution_flag_candidates="}, want: "execution-skipping Make flags n/i/q/t"},
		{name: "skip result", arguments: []string{"-n", "MAKE_EXECUTION_SKIP_FLAGS="}, want: "execution-skipping Make flags n/i/q/t"},
		{name: "flag inputs", arguments: []string{"-n", "MAKEFLAGS=", "MFLAGS="}, want: "command-line assignment of MAKEFLAGS or MFLAGS is forbidden"},
		{name: "flag-input result", arguments: []string{"-n", "MAKEFLAGS=", "MFLAGS=", "MAKE_CONTRACT_MAKE_INPUT_CHANGED="}, want: "command-line assignment of MAKEFLAGS or MFLAGS is forbidden"},
		{name: "shell comparison helper", arguments: []string{"SHELL=/usr/bin/true", "make_contract_value_changed="}, want: "recipe execution control SHELL"},
		{name: "shell result", arguments: []string{"SHELL=/usr/bin/true", "MAKE_CONTRACT_SHELL_CHANGED="}, want: "recipe execution control SHELL"},
		{name: "recipe guard", arguments: []string{"SHELL=/usr/bin/true", "MAKE_CONTRACT_RECIPE_GUARD="}, want: "recipe execution control SHELL"},
		{name: "shell flags result", arguments: []string{".SHELLFLAGS=-c", "MAKE_CONTRACT_SHELLFLAGS_CHANGED="}, want: "recipe execution control .SHELLFLAGS"},
		{name: "recipe prefix result", arguments: []string{".RECIPEPREFIX=", "MAKE_CONTRACT_RECIPEPREFIX_CHANGED="}, want: "recipe execution control .RECIPEPREFIX"},
	}
	for _, test := range overrideCases {
		arguments := append([]string{"--no-print-directory"}, test.arguments...)
		arguments = append(arguments, "GO="+goExecutable, "make-contract-flags")
		output, err := runMake(root, environment, arguments...)
		if err == nil {
			return fmt.Errorf("command-line guard override %s bypassed the Make contract", test.name)
		}
		if !strings.Contains(output, test.want) {
			return fmt.Errorf("command-line guard override %s failed unexpectedly: %s", test.name, output)
		}
	}

	environmentOverride := setEnvironment(environment, "MAKE_EXECUTION_SKIP_FLAGS", "")
	output, err := runMake(root, environmentOverride, "--no-print-directory", "-e", "-n", "GO="+goExecutable, "make-contract-flags")
	if err == nil {
		return errors.New("environment guard override bypassed the Make contract under -e")
	}
	if !strings.Contains(output, "execution-skipping Make flags n/i/q/t") {
		return fmt.Errorf("environment guard override failed unexpectedly: %s", output)
	}

	includeDirectory := filepath.Join(probeRoot, "include n")
	if err := os.Mkdir(includeDirectory, 0o700); err != nil {
		return fmt.Errorf("create safe include directory: %w", err)
	}
	safeFlags := [][]string{
		{"-k"},
		{"-r"},
		{"-s"},
		{"--keep-going"},
		{"--no-builtin-rules"},
		{"-C", root},
		{"-I", includeDirectory},
	}
	for _, flag := range safeFlags {
		arguments := append([]string{"--no-print-directory"}, flag...)
		arguments = append(arguments, "GO="+goExecutable, "make-contract-flags")
		output, err := runMake(root, environment, arguments...)
		if err != nil {
			return fmt.Errorf("safe Make flag %s was rejected: %w\n%s", strings.Join(flag, " "), err, output)
		}
	}
	return nil
}

func runMake(root string, environment []string, arguments ...string) (string, error) {
	// #nosec G204 -- the executable is constant and arguments are passed without a shell.
	command := exec.Command("make", arguments...)
	command.Dir = root
	command.Env = environment
	var output bytes.Buffer
	command.Stdout = &output
	command.Stderr = &output
	err := command.Run()
	return output.String(), err
}

func readProbeLog(path string) ([]string, error) {
	// #nosec G304 -- path is a checker-created file in a private temporary directory.
	content, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read probe log %s: %w", path, err)
	}
	lines := strings.Split(strings.TrimSpace(string(content)), "\n")
	if len(lines) == 1 && lines[0] == "" {
		return nil, nil
	}
	sort.Strings(lines)
	return lines, nil
}

func cleanMakeEnvironment(environment []string) []string {
	cleaned := make([]string, 0, len(environment))
	for _, entry := range environment {
		name := entry
		if equals := strings.IndexByte(entry, '='); equals >= 0 {
			name = entry[:equals]
		}
		switch name {
		case "MAKEFLAGS", "MFLAGS", "GNUMAKEFLAGS", "MAKEFILES":
			continue
		default:
			cleaned = append(cleaned, entry)
		}
	}
	return cleaned
}

func setEnvironment(environment []string, name, value string) []string {
	prefix := name + "="
	result := make([]string, 0, len(environment)+1)
	for _, entry := range environment {
		if !strings.HasPrefix(entry, prefix) {
			result = append(result, entry)
		}
	}
	return append(result, prefix+value)
}
