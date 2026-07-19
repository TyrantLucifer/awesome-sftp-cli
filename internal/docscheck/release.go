package docscheck

import (
	"fmt"
	"path/filepath"
	"regexp"
	"strings"
)

const releaseGatesPath = "docs/release/RC-GATES.md"

var releaseCheckboxPattern = regexp.MustCompile(`^- \[([ xX])\] (.+)$`)

// CheckRelease runs the durable documentation checks plus the explicit public
// release checklist. Internal-preview publication does not require this audit
// to pass; a public release does.
func CheckRelease(root string) []Finding {
	findings := Check(root)
	for _, finding := range findings {
		if finding.Rule == "repository.root" {
			return findings
		}
	}
	absoluteRoot, err := filepath.Abs(root)
	if err != nil {
		return findings
	}
	resolvedRoot, err := filepath.EvalSymlinks(absoluteRoot)
	if err != nil {
		return findings
	}

	lines, err := readLines(resolvedRoot, releaseGatesPath)
	if err != nil {
		findings = append(findings, Finding{
			Path: releaseGatesPath, Line: 1, Rule: "release.gates", Message: err.Error(),
		})
		sortFindings(findings)
		return findings
	}

	gateCount := 0
	for index, line := range lines {
		match := releaseCheckboxPattern.FindStringSubmatch(strings.TrimSpace(line))
		if len(match) != 3 {
			continue
		}
		gateCount++
		if match[1] == " " {
			findings = append(findings, Finding{
				Path: releaseGatesPath, Line: index + 1, Rule: "release.gate_open",
				Message: fmt.Sprintf("release gate remains open: %s", match[2]),
			})
		}
	}
	if gateCount == 0 {
		findings = append(findings, Finding{
			Path: releaseGatesPath, Line: 1, Rule: "release.gates",
			Message: "release checklist must contain at least one checkbox gate",
		})
	}
	sortFindings(findings)
	return findings
}
