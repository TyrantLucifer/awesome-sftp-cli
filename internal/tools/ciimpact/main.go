package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"
)

const modulePath = "github.com/TyrantLucifer/awesome-sftp-cli"

type packageInfo struct {
	ImportPath   string
	Dir          string
	Imports      []string
	TestImports  []string
	XTestImports []string
}

type plan struct {
	Docs         bool
	Code         bool
	Workflow     bool
	Dependencies bool
	Core         bool
	Native       bool
	Auth         bool
	Release      bool
	Full         bool
	Packages     []string
}

func main() {
	os.Exit(run(os.Args[1:], os.Stdout, os.Stderr))
}

func run(arguments []string, stdout, stderr io.Writer) int {
	flags := flag.NewFlagSet("ciimpact", flag.ContinueOnError)
	flags.SetOutput(stderr)
	base := flags.String("base", "", "base Git object ID")
	head := flags.String("head", "", "head Git object ID")
	root := flags.String("root", ".", "repository root")
	goExecutable := flags.String("go", "go", "Go executable")
	if err := flags.Parse(arguments); err != nil || flags.NArg() != 0 || *base == "" || *head == "" {
		fmt.Fprintln(stderr, "usage: ciimpact --base <object-id> --head <object-id> [--root <path>] [--go <path>]")
		return 2
	}

	absoluteRoot, err := filepath.Abs(*root)
	if err != nil {
		fmt.Fprintf(stderr, "ciimpact: resolve repository root: %v\n", err)
		return 1
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()

	changed, err := changedFiles(ctx, absoluteRoot, *base, *head)
	if err != nil {
		fmt.Fprintf(stderr, "ciimpact: Git diff unavailable; selecting full checks: %v\n", err)
		return writePlan(stdout, stderr, fullPlan())
	}

	var packages []packageInfo
	if needsPackageGraph(changed) {
		packages, err = listPackages(ctx, absoluteRoot, *goExecutable)
		if err != nil {
			fmt.Fprintf(stderr, "ciimpact: package graph unavailable; selecting full checks: %v\n", err)
			return writePlan(stdout, stderr, fullPlan())
		}
	}
	return writePlan(stdout, stderr, buildPlan(absoluteRoot, changed, packages))
}

func writePlan(output, errorOutput io.Writer, selected plan) int {
	formatted, err := formatGitHubOutputs(selected)
	if err != nil {
		fmt.Fprintf(errorOutput, "ciimpact: format plan: %v\n", err)
		return 1
	}
	if _, err := io.WriteString(output, formatted); err != nil {
		fmt.Fprintf(errorOutput, "ciimpact: write plan: %v\n", err)
		return 1
	}
	return 0
}

func changedFiles(ctx context.Context, root, base, head string) ([]string, error) {
	if !isGitObjectID(base) || !isGitObjectID(head) {
		return nil, errors.New("base and head must be full hexadecimal Git object IDs")
	}
	// #nosec G204 -- git is fixed, revisions are full hexadecimal object IDs, and arguments bypass a shell.
	command := exec.CommandContext(ctx, "git", "-C", root, "diff", "--name-only", "--no-renames", "-z", "--diff-filter=ACMRD", base, head, "--")
	output, err := command.Output()
	if err != nil {
		return nil, err
	}
	parts := bytes.Split(output, []byte{0})
	files := make([]string, 0, len(parts))
	for _, part := range parts {
		if len(part) == 0 {
			continue
		}
		path := filepath.ToSlash(filepath.Clean(string(part)))
		if path == "." || filepath.IsAbs(path) || path == ".." || strings.HasPrefix(path, "../") {
			return nil, fmt.Errorf("unsafe changed path %q", path)
		}
		files = append(files, path)
	}
	sort.Strings(files)
	return files, nil
}

func isGitObjectID(value string) bool {
	if len(value) != 40 && len(value) != 64 {
		return false
	}
	for _, character := range value {
		if character < '0' || character > '9' {
			lower := character | 0x20
			if lower < 'a' || lower > 'f' {
				return false
			}
		}
	}
	return true
}

func needsPackageGraph(paths []string) bool {
	for _, path := range paths {
		if path == "go.mod" || path == "go.sum" || path == "Makefile" ||
			strings.HasPrefix(path, "cmd/") || strings.HasPrefix(path, "internal/") ||
			strings.HasPrefix(path, "tools/") {
			return true
		}
	}
	return false
}

func listPackages(ctx context.Context, root, goExecutable string) ([]packageInfo, error) {
	command := exec.CommandContext(ctx, goExecutable, "list", "-json", "./...")
	command.Dir = root
	output, err := command.Output()
	if err != nil {
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) {
			return nil, fmt.Errorf("go list: %s", strings.TrimSpace(string(exitErr.Stderr)))
		}
		return nil, err
	}
	decoder := json.NewDecoder(bytes.NewReader(output))
	var packages []packageInfo
	for {
		var item packageInfo
		err := decoder.Decode(&item)
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return nil, fmt.Errorf("decode go list output: %w", err)
		}
		if item.ImportPath == "" || item.Dir == "" || !strings.HasPrefix(item.ImportPath, modulePath) {
			continue
		}
		packages = append(packages, item)
	}
	if len(packages) == 0 {
		return nil, errors.New("go list returned no repository packages")
	}
	return packages, nil
}

func buildPlan(root string, changed []string, packages []packageInfo) plan {
	if len(changed) == 0 {
		return fullPlan()
	}

	selected := plan{}
	direct := make(map[string]struct{})
	propagate := make(map[string]struct{})
	known := true
	byImport := make(map[string]packageInfo, len(packages))
	for _, item := range packages {
		byImport[item.ImportPath] = item
	}

	for _, path := range changed {
		path = filepath.ToSlash(filepath.Clean(path))
		pathKnown := classifyRisk(path, &selected)
		if path == "go.mod" || path == "go.sum" || path == "tools/go.mod" || path == "tools/go.sum" {
			selected.Code = true
			selected.Dependencies = true
			selected.Full = true
			continue
		}
		if path == "Makefile" || strings.HasPrefix(path, "internal/tools/ciimpact/") {
			selected.Code = true
			selected.Workflow = true
			selected.Full = true
			continue
		}
		if isDocumentation(path) {
			selected.Docs = true
			continue
		}
		if strings.HasPrefix(path, ".github/") {
			selected.Workflow = true
			continue
		}
		if strings.HasPrefix(path, "cmd/") || strings.HasPrefix(path, "internal/") {
			selected.Code = true
			owner, ok := owningPackage(root, path, packages)
			if !ok {
				selected.Full = true
				continue
			}
			direct[owner] = struct{}{}
			if !strings.HasSuffix(path, "_test.go") && !strings.Contains(path, "/testdata/") {
				propagate[owner] = struct{}{}
			}
			continue
		}
		if !pathKnown {
			known = false
		}
	}

	if !known {
		selected.Full = true
		selected.Code = true
	}
	if selected.Full {
		selected.Packages = []string{"./..."}
		return selected
	}

	affected := reverseClosure(propagate, byImport)
	for importPath := range direct {
		affected[importPath] = struct{}{}
	}
	for importPath := range affected {
		item, ok := byImport[importPath]
		if !ok {
			continue
		}
		relative, err := filepath.Rel(root, item.Dir)
		if err != nil || relative == "." || relative == ".." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
			return fullPlanWith(selected)
		}
		selected.Packages = append(selected.Packages, "./"+filepath.ToSlash(relative))
	}
	sort.Strings(selected.Packages)
	if selected.Code && len(selected.Packages) == 0 {
		return fullPlanWith(selected)
	}
	if len(selected.Packages) > 32 {
		return fullPlanWith(selected)
	}
	return selected
}

func classifyRisk(path string, selected *plan) bool {
	known := false
	for _, prefix := range []string{
		"internal/provider/", "internal/transfer/", "internal/state/", "internal/statefs/",
		"internal/cache/", "internal/cachefs/", "internal/cachemanager/", "internal/cacheprocess/",
		"internal/daemon/", "internal/ipc/",
	} {
		if strings.HasPrefix(path, prefix) {
			selected.Core = true
			known = true
		}
	}
	for _, prefix := range []string{"internal/platform/", "internal/terminalhandoff/", "internal/externalprocess/"} {
		if strings.HasPrefix(path, prefix) {
			selected.Native = true
			known = true
		}
	}
	for _, prefix := range []string{
		"internal/auth/", "internal/sshconfig/", "internal/provider/sftp/", "internal/transport/openssh/",
		"internal/integration/hosted-auth", "internal/integration/hosted-kerberos", "internal/integration/hosted-vendor-sftp",
	} {
		if strings.HasPrefix(path, prefix) {
			selected.Auth = true
			known = true
		}
	}
	for _, prefix := range []string{
		"internal/buildinfo/", "internal/release", "internal/tools/release", "docs/release/", "docs/man/",
	} {
		if strings.HasPrefix(path, prefix) {
			selected.Release = true
			known = true
		}
	}
	if strings.HasPrefix(path, ".github/") || strings.HasPrefix(path, "internal/docscheck/") ||
		strings.HasPrefix(path, "internal/tools/makecontract/") {
		selected.Workflow = true
		known = true
	}
	if strings.HasPrefix(path, "cmd/") || strings.HasPrefix(path, "internal/") || strings.HasPrefix(path, "tools/") {
		known = true
	}
	return known
}

func isDocumentation(path string) bool {
	base := filepath.Base(path)
	return strings.HasPrefix(path, "docs/") || base == "README.md" || base == "AGENTS.md" ||
		base == "SECURITY.md" || base == "CONTRIBUTING.md" || base == "LICENSE" ||
		strings.HasSuffix(path, ".md")
}

func owningPackage(root, path string, packages []packageInfo) (string, bool) {
	absolute := filepath.Join(root, filepath.FromSlash(path))
	bestImport := ""
	bestLength := -1
	for _, item := range packages {
		relative, err := filepath.Rel(item.Dir, absolute)
		if err != nil || relative == ".." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
			continue
		}
		if len(item.Dir) > bestLength {
			bestImport = item.ImportPath
			bestLength = len(item.Dir)
		}
	}
	return bestImport, bestImport != ""
}

func reverseClosure(seeds map[string]struct{}, packages map[string]packageInfo) map[string]struct{} {
	affected := make(map[string]struct{}, len(seeds))
	queue := make([]string, 0, len(seeds))
	for seed := range seeds {
		affected[seed] = struct{}{}
		queue = append(queue, seed)
	}
	for len(queue) > 0 {
		current := queue[0]
		queue = queue[1:]
		for importPath, item := range packages {
			if _, exists := affected[importPath]; exists || !importsPackage(item, current) {
				continue
			}
			affected[importPath] = struct{}{}
			queue = append(queue, importPath)
		}
	}
	return affected
}

func importsPackage(item packageInfo, target string) bool {
	for _, imports := range [][]string{item.Imports, item.TestImports, item.XTestImports} {
		for _, imported := range imports {
			if imported == target {
				return true
			}
		}
	}
	return false
}

func fullPlan() plan {
	return plan{Code: true, Full: true, Packages: []string{"./..."}}
}

func fullPlanWith(selected plan) plan {
	selected.Code = true
	selected.Full = true
	selected.Packages = []string{"./..."}
	return selected
}

func formatGitHubOutputs(selected plan) (string, error) {
	packages := append([]string{}, selected.Packages...)
	sort.Strings(packages)
	encodedPackages, err := json.Marshal(packages)
	if err != nil {
		return "", err
	}
	values := []struct {
		key   string
		value bool
	}{
		{key: "docs", value: selected.Docs},
		{key: "code", value: selected.Code},
		{key: "workflow", value: selected.Workflow},
		{key: "dependencies", value: selected.Dependencies},
		{key: "core", value: selected.Core},
		{key: "native", value: selected.Native},
		{key: "auth", value: selected.Auth},
		{key: "release", value: selected.Release},
		{key: "full", value: selected.Full},
	}
	var output strings.Builder
	for _, item := range values {
		fmt.Fprintf(&output, "%s=%s\n", item.key, strconv.FormatBool(item.value))
	}
	fmt.Fprintf(&output, "packages=%s\n", encodedPackages)
	return output.String(), nil
}
