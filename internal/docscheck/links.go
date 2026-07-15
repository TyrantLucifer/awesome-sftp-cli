package docscheck

import (
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
)

var unresolvedMarkerPattern = regexp.MustCompile(`(?i)\b(TODO|TBD|FIXME|XXX)\b`)

func checkDurableDocuments(root string) []Finding {
	paths, findings := durableDocumentPaths(root)
	for _, path := range paths {
		lines, err := readLines(root, path)
		if err != nil {
			findings = append(findings, readFailure(path, err))
			continue
		}

		inFence := false
		for index, line := range lines {
			trimmed := strings.TrimSpace(line)
			if strings.HasPrefix(trimmed, "```") || strings.HasPrefix(trimmed, "~~~") {
				inFence = !inFence
				continue
			}
			if inFence {
				continue
			}

			if marker := unresolvedMarkerPattern.FindString(line); marker != "" {
				findings = append(findings, Finding{
					Path:    path,
					Line:    index + 1,
					Rule:    "docs.marker",
					Message: fmt.Sprintf("unresolved durable marker %q", marker),
				})
			}
			for _, target := range markdownLinkTargets(line) {
				if finding := checkRelativeLink(root, path, index+1, target); finding != nil {
					findings = append(findings, *finding)
				}
			}
		}
	}
	return findings
}

func durableDocumentPaths(root string) ([]string, []Finding) {
	var paths []string
	var findings []Finding

	entries, err := os.ReadDir(root)
	if err != nil {
		return nil, []Finding{readFailure(".", err)}
	}
	for _, entry := range entries {
		if entry.Type().IsRegular() && strings.EqualFold(filepath.Ext(entry.Name()), ".md") {
			paths = append(paths, entry.Name())
		}
	}

	docsRoot := filepath.Join(root, "docs")
	err = filepath.WalkDir(docsRoot, func(path string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			findings = append(findings, readFailure(repositoryPath(root, path), walkErr))
			return nil
		}
		if !entry.Type().IsRegular() || !strings.EqualFold(filepath.Ext(entry.Name()), ".md") {
			return nil
		}
		paths = append(paths, repositoryPath(root, path))
		return nil
	})
	if err != nil && !os.IsNotExist(err) {
		findings = append(findings, readFailure("docs", err))
	}
	sort.Strings(paths)
	return paths, findings
}

func markdownLinkTargets(line string) []string {
	var targets []string
	remaining := line
	for {
		marker := strings.Index(remaining, "](")
		if marker < 0 {
			return targets
		}
		contentStart := marker + 2
		contentEnd := strings.IndexByte(remaining[contentStart:], ')')
		if contentEnd < 0 {
			return targets
		}
		contentEnd += contentStart
		raw := strings.TrimSpace(remaining[contentStart:contentEnd])
		if strings.HasPrefix(raw, "<") {
			if closing := strings.IndexByte(raw, '>'); closing > 0 {
				raw = raw[1:closing]
			}
		} else if fields := strings.Fields(raw); len(fields) > 0 {
			raw = fields[0]
		}
		if raw != "" {
			targets = append(targets, raw)
		}
		remaining = remaining[contentEnd+1:]
	}
}

func checkRelativeLink(root, document string, line int, rawTarget string) *Finding {
	if strings.HasPrefix(rawTarget, "#") || strings.HasPrefix(rawTarget, "//") {
		return nil
	}
	parsed, err := url.Parse(rawTarget)
	if err != nil {
		return &Finding{Path: document, Line: line, Rule: "link.invalid", Message: fmt.Sprintf("invalid link target %q", rawTarget)}
	}
	if parsed.IsAbs() {
		return nil
	}
	if filepath.IsAbs(parsed.Path) {
		return &Finding{Path: document, Line: line, Rule: "link.escape", Message: fmt.Sprintf("absolute repository link %q is not allowed", rawTarget)}
	}
	if parsed.Path == "" {
		return nil
	}
	decodedPath, err := url.PathUnescape(parsed.Path)
	if err != nil {
		return &Finding{Path: document, Line: line, Rule: "link.invalid", Message: fmt.Sprintf("invalid escaped link target %q", rawTarget)}
	}

	documentDirectory := filepath.Dir(filepath.FromSlash(document))
	target := filepath.Clean(filepath.Join(root, documentDirectory, filepath.FromSlash(decodedPath)))
	if !pathWithin(root, target) {
		return &Finding{Path: document, Line: line, Rule: "link.escape", Message: fmt.Sprintf("relative link %q escapes the repository", rawTarget)}
	}
	if _, err := os.Stat(target); err != nil {
		return &Finding{Path: document, Line: line, Rule: "link.missing", Message: fmt.Sprintf("relative link %q does not resolve", rawTarget)}
	}
	if !resolvedPathWithin(root, target) {
		return &Finding{Path: document, Line: line, Rule: "link.escape", Message: fmt.Sprintf("relative link %q resolves outside the repository", rawTarget)}
	}
	return nil
}

func pathWithin(root, target string) bool {
	relative, err := filepath.Rel(root, target)
	return err == nil && relative != ".." && !strings.HasPrefix(relative, ".."+string(filepath.Separator))
}

func resolvedPathWithin(root, target string) bool {
	resolvedRoot, err := filepath.EvalSymlinks(root)
	if err != nil {
		return false
	}
	resolvedTarget, err := filepath.EvalSymlinks(target)
	if err != nil {
		return false
	}
	return pathWithin(resolvedRoot, resolvedTarget)
}
