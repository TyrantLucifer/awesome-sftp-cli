package docscheck

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

type Finding struct {
	Path    string
	Line    int
	Rule    string
	Message string
}

var requiredDocumentationEntryPoints = []string{
	"README.md",
	"AGENTS.md",
	"docs/README.md",
	"docs/development/status.md",
	"docs/product/roadmap.md",
}

func Check(root string) []Finding {
	absoluteRoot, err := filepath.Abs(root)
	if err != nil {
		return []Finding{{Path: ".", Line: 1, Rule: "repository.root", Message: fmt.Sprintf("resolve repository root: %v", err)}}
	}
	resolvedRoot, err := filepath.EvalSymlinks(absoluteRoot)
	if err != nil {
		return []Finding{{Path: ".", Line: 1, Rule: "repository.root", Message: fmt.Sprintf("resolve repository root: %v", err)}}
	}
	rootInfo, err := os.Stat(resolvedRoot)
	if err != nil || !rootInfo.IsDir() {
		return []Finding{{Path: ".", Line: 1, Rule: "repository.root", Message: "repository root is missing or is not a directory"}}
	}

	var findings []Finding
	findings = append(findings, checkRequiredDocumentationEntryPoints(resolvedRoot)...)
	findings = append(findings, checkDurableDocuments(resolvedRoot)...)
	findings = append(findings, checkFeatureMatrix(resolvedRoot)...)
	findings = append(findings, checkWorkflows(resolvedRoot)...)

	sortFindings(findings)
	return findings
}

func checkRequiredDocumentationEntryPoints(root string) []Finding {
	var findings []Finding
	for _, path := range requiredDocumentationEntryPoints {
		if _, err := readRepositoryFile(root, path); err != nil {
			findings = append(findings, Finding{
				Path: path, Line: 1, Rule: "docs.required",
				Message: "required documentation entry point is missing or is not a regular file",
			})
		}
	}
	return findings
}

func sortFindings(findings []Finding) {
	sort.Slice(findings, func(left, right int) bool {
		if findings[left].Path != findings[right].Path {
			return findings[left].Path < findings[right].Path
		}
		if findings[left].Line != findings[right].Line {
			return findings[left].Line < findings[right].Line
		}
		if findings[left].Rule != findings[right].Rule {
			return findings[left].Rule < findings[right].Rule
		}
		return findings[left].Message < findings[right].Message
	})
}

func readLines(root, relativePath string) ([]string, error) {
	content, err := readRepositoryFile(root, relativePath)
	if err != nil {
		return nil, err
	}
	return strings.Split(string(content), "\n"), nil
}

func readRepositoryFile(root, relativePath string) ([]byte, error) {
	target, err := resolveRepositoryPath(root, relativePath)
	if err != nil {
		return nil, err
	}
	info, err := os.Stat(target)
	if err != nil {
		return nil, fmt.Errorf("stat repository path %q: %w", relativePath, err)
	}
	if !info.Mode().IsRegular() {
		return nil, fmt.Errorf("repository path %q is not a regular file", relativePath)
	}
	//nolint:gosec // target is a resolved regular file proven inside the resolved repository root.
	content, err := os.ReadFile(target)
	if err != nil {
		return nil, fmt.Errorf("read repository path %q: %w", relativePath, err)
	}
	return content, nil
}

func resolveRepositoryPath(root, relativePath string) (string, error) {
	target := filepath.Clean(filepath.Join(root, filepath.FromSlash(relativePath)))
	if filepath.IsAbs(relativePath) || !pathWithin(root, target) {
		return "", fmt.Errorf("repository path %q escapes the root", relativePath)
	}
	resolvedRoot, err := filepath.EvalSymlinks(root)
	if err != nil {
		return "", fmt.Errorf("resolve repository root: %w", err)
	}
	resolvedTarget, err := filepath.EvalSymlinks(target)
	if err != nil {
		return "", fmt.Errorf("resolve repository path %q: %w", relativePath, err)
	}
	if !pathWithin(resolvedRoot, resolvedTarget) {
		return "", fmt.Errorf("repository path %q resolves outside the root", relativePath)
	}
	return resolvedTarget, nil
}

func repositoryPath(root, path string) string {
	relative, err := filepath.Rel(root, path)
	if err != nil {
		return filepath.ToSlash(path)
	}
	return filepath.ToSlash(relative)
}

func readFailure(path string, err error) Finding {
	return Finding{Path: path, Line: 1, Rule: "repository.read", Message: err.Error()}
}
