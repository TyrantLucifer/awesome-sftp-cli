package docscheck

import (
	"fmt"
	"regexp"
	"strings"
)

const featureMatrixPath = "docs/product/feature-matrix.md"

var featureIDPattern = regexp.MustCompile(`^[A-Z]+-[0-9]{3}$`)

var canonicalFeatureHeader = []string{"ID", "能力", "状态", "用户契约", "实现与测试依据"}

var legalFeatureStatuses = map[string]struct{}{
	"Planned":     {},
	"In Progress": {},
	"Implemented": {},
	"Verified":    {},
	"Deferred":    {},
	"Removed":     {},
}

func checkFeatureMatrix(root string) []Finding {
	lines, err := readLines(root, featureMatrixPath)
	if err != nil {
		return []Finding{{Path: featureMatrixPath, Line: 1, Rule: "matrix.missing", Message: err.Error()}}
	}
	headerLines, schemaFinding := validateFeatureMatrixSchema(lines)
	if schemaFinding != nil {
		return []Finding{*schemaFinding}
	}

	var findings []Finding
	seenIDs := make(map[string]int)
	inFeatureTable := false
	for index, line := range lines {
		cells := markdownTableCells(line)
		if _, isHeader := headerLines[index+1]; isHeader {
			inFeatureTable = true
			continue
		}
		if !inFeatureTable {
			continue
		}
		if len(cells) == 0 {
			inFeatureTable = false
			continue
		}
		if isTableDelimiter(cells) {
			continue
		}

		lineNumber := index + 1
		if len(cells) != len(canonicalFeatureHeader) {
			findings = append(findings, Finding{
				Path: featureMatrixPath, Line: lineNumber, Rule: "matrix.row",
				Message: fmt.Sprintf("feature row has %d cells; want %d", len(cells), len(canonicalFeatureHeader)),
			})
			continue
		}

		id, status, evidence := cells[0], cells[2], cells[4]
		if !featureIDPattern.MatchString(id) {
			findings = append(findings, Finding{
				Path: featureMatrixPath, Line: lineNumber, Rule: "matrix.id_format",
				Message: fmt.Sprintf("feature ID %q must match PREFIX-NNN", id),
			})
		}
		if firstLine, exists := seenIDs[id]; exists {
			findings = append(findings, Finding{
				Path: featureMatrixPath, Line: lineNumber, Rule: "matrix.id_duplicate",
				Message: fmt.Sprintf("feature ID %q duplicates line %d", id, firstLine),
			})
		} else if id != "" {
			seenIDs[id] = lineNumber
		}
		if _, legal := legalFeatureStatuses[status]; !legal {
			findings = append(findings, Finding{
				Path: featureMatrixPath, Line: lineNumber, Rule: "matrix.status",
				Message: fmt.Sprintf("feature status %q is not allowed", status),
			})
		}
		if strings.TrimSpace(evidence) == "" {
			findings = append(findings, Finding{
				Path: featureMatrixPath, Line: lineNumber, Rule: "matrix.evidence_missing",
				Message: fmt.Sprintf("feature %q has no implementation or test evidence", id),
			})
		}
		if status != "Planned" && containsStaleEvidence(evidence) {
			findings = append(findings, Finding{
				Path: featureMatrixPath, Line: lineNumber, Rule: "matrix.evidence_stale",
				Message: fmt.Sprintf("feature %q is %s but still has not-implemented evidence", id, status),
			})
		}
	}
	return findings
}

type featureHeaderOccurrence struct {
	line      int
	section   int
	canonical bool
}

func validateFeatureMatrixSchema(lines []string) (map[int]struct{}, *Finding) {
	var occurrences []featureHeaderOccurrence
	section := 0
	for index, line := range lines {
		if strings.HasPrefix(strings.TrimSpace(line), "## ") {
			section++
		}
		cells := markdownTableCells(line)
		if !isFeatureHeaderCandidate(cells) {
			continue
		}
		occurrences = append(occurrences, featureHeaderOccurrence{
			line: index + 1, section: section, canonical: isFeatureHeader(cells),
		})
	}

	if len(occurrences) == 0 {
		return nil, &Finding{Path: featureMatrixPath, Line: 1, Rule: "matrix.schema", Message: "feature matrix must contain a canonical capability header"}
	}

	headerLines := make(map[int]struct{})
	sectionHeaders := make(map[int]int)
	for _, occurrence := range occurrences {
		if !occurrence.canonical {
			return nil, &Finding{Path: featureMatrixPath, Line: occurrence.line, Rule: "matrix.schema", Message: "feature matrix contains a malformed capability header"}
		}
		if _, duplicate := sectionHeaders[occurrence.section]; duplicate {
			return nil, &Finding{Path: featureMatrixPath, Line: occurrence.line, Rule: "matrix.schema", Message: "capability section contains more than one canonical header"}
		}
		sectionHeaders[occurrence.section] = occurrence.line
		headerLines[occurrence.line] = struct{}{}
	}

	for _, occurrence := range occurrences {
		if !featureTableHasRow(lines, occurrence.line) {
			return nil, &Finding{Path: featureMatrixPath, Line: occurrence.line, Rule: "matrix.schema", Message: "capability table must contain at least one row"}
		}
	}
	return headerLines, nil
}

func featureTableHasRow(lines []string, headerLine int) bool {
	for index := headerLine; index < len(lines); index++ {
		cells := markdownTableCells(lines[index])
		if len(cells) == 0 {
			return false
		}
		if !isTableDelimiter(cells) {
			return true
		}
	}
	return false
}

func markdownTableCells(line string) []string {
	trimmed := strings.TrimSpace(line)
	if len(trimmed) < 2 || trimmed[0] != '|' || trimmed[len(trimmed)-1] != '|' || markdownPipeIsEscaped(trimmed, len(trimmed)-1) {
		return nil
	}
	body := trimmed[1 : len(trimmed)-1]
	var parts []string
	start := 0
	for index := 0; index < len(body); index++ {
		if body[index] != '|' || markdownPipeIsEscaped(body, index) {
			continue
		}
		parts = append(parts, body[start:index])
		start = index + 1
	}
	parts = append(parts, body[start:])
	for index := range parts {
		parts[index] = strings.TrimSpace(parts[index])
	}
	return parts
}

func markdownPipeIsEscaped(text string, index int) bool {
	backslashes := 0
	for index--; index >= 0 && text[index] == '\\'; index-- {
		backslashes++
	}
	return backslashes%2 == 1
}

func isFeatureHeader(cells []string) bool {
	if len(cells) != len(canonicalFeatureHeader) {
		return false
	}
	for index, expected := range canonicalFeatureHeader {
		if cells[index] != expected {
			return false
		}
	}
	return true
}

func isFeatureHeaderCandidate(cells []string) bool {
	return len(cells) > 0 && cells[0] == "ID"
}

func isTableDelimiter(cells []string) bool {
	if len(cells) == 0 {
		return false
	}
	for _, cell := range cells {
		if strings.Trim(cell, ":-") != "" {
			return false
		}
	}
	return true
}

func containsStaleEvidence(evidence string) bool {
	return strings.Contains(evidence, "未实施") || strings.Contains(strings.ToLower(evidence), "not implemented")
}
