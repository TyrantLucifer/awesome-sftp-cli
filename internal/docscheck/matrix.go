package docscheck

import (
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
)

const featureMatrixPath = "docs/product/feature-matrix.md"

var featureIDPattern = regexp.MustCompile(`^[A-Z]+-[0-9]{3}$`)

var canonicalFeatureHeader = []string{"ID", "能力", "Stage", "状态", "验收标准", "当前证据"}

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
		if len(cells) != 6 {
			findings = append(findings, Finding{Path: featureMatrixPath, Line: lineNumber, Rule: "matrix.row", Message: fmt.Sprintf("feature row has %d cells; want 6", len(cells))})
			continue
		}

		id, stageText, status, evidence := cells[0], cells[2], cells[3], cells[5]
		if !featureIDPattern.MatchString(id) {
			findings = append(findings, Finding{Path: featureMatrixPath, Line: lineNumber, Rule: "matrix.id_format", Message: fmt.Sprintf("feature ID %q must match PREFIX-NNN", id)})
		}
		if firstLine, exists := seenIDs[id]; exists {
			findings = append(findings, Finding{Path: featureMatrixPath, Line: lineNumber, Rule: "matrix.id_duplicate", Message: fmt.Sprintf("feature ID %q duplicates line %d", id, firstLine)})
		} else if id != "" {
			seenIDs[id] = lineNumber
		}

		stage, stageErr := strconv.Atoi(stageText)
		if stageErr != nil || stage < 0 || stage > 6 {
			findings = append(findings, Finding{Path: featureMatrixPath, Line: lineNumber, Rule: "matrix.stage", Message: fmt.Sprintf("feature stage %q must be an integer from 0 through 6", stageText)})
		}
		if _, legal := legalFeatureStatuses[status]; !legal {
			findings = append(findings, Finding{Path: featureMatrixPath, Line: lineNumber, Rule: "matrix.status", Message: fmt.Sprintf("feature status %q is not allowed", status)})
		}
		if strings.TrimSpace(evidence) == "" {
			findings = append(findings, Finding{Path: featureMatrixPath, Line: lineNumber, Rule: "matrix.evidence_missing", Message: fmt.Sprintf("feature %q has no evidence", id)})
		}
		if status != "Planned" && containsStaleEvidence(evidence) {
			findings = append(findings, Finding{Path: featureMatrixPath, Line: lineNumber, Rule: "matrix.evidence_stale", Message: fmt.Sprintf("feature %q is %s but still has not-implemented evidence", id, status)})
		}
		if status == "Verified" {
			records := verificationRecords(root, evidence)
			switch {
			case len(records) == 0:
				findings = append(findings, Finding{Path: featureMatrixPath, Line: lineNumber, Rule: "matrix.verification_record", Message: fmt.Sprintf("verified feature %q must link to an existing docs/verification record", id)})
			case !anyVerificationRecordPasses(root, records, id):
				findings = append(findings, Finding{Path: featureMatrixPath, Line: lineNumber, Rule: "matrix.verification_result", Message: fmt.Sprintf("verified feature %q must link to a verification table row with the exact feature ID and a separate PASS cell", id)})
			}
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
			line:      index + 1,
			section:   section,
			canonical: isFeatureHeader(cells),
		})
	}

	if len(occurrences) == 0 {
		return nil, &Finding{Path: featureMatrixPath, Line: 1, Rule: "matrix.schema", Message: "feature matrix must contain a canonical feature header"}
	}

	headerLines := make(map[int]struct{})
	sectionHeaders := make(map[int]int)
	for _, occurrence := range occurrences {
		if !occurrence.canonical {
			return nil, &Finding{Path: featureMatrixPath, Line: occurrence.line, Rule: "matrix.schema", Message: "feature matrix contains a malformed feature header"}
		}
		if _, duplicate := sectionHeaders[occurrence.section]; duplicate {
			return nil, &Finding{Path: featureMatrixPath, Line: occurrence.line, Rule: "matrix.schema", Message: "feature-bearing section contains more than one canonical header"}
		}
		sectionHeaders[occurrence.section] = occurrence.line
		headerLines[occurrence.line] = struct{}{}
	}

	for _, occurrence := range occurrences {
		if !featureTableHasRow(lines, occurrence.line) {
			return nil, &Finding{Path: featureMatrixPath, Line: occurrence.line, Rule: "matrix.schema", Message: "feature table must contain at least one row"}
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
	if len(cells) == 0 || cells[0] != "ID" {
		return false
	}
	for _, cell := range cells[1:] {
		if cell == "Stage" {
			return true
		}
	}
	return false
}

func isTableDelimiter(cells []string) bool {
	if len(cells) == 0 {
		return false
	}
	for _, cell := range cells {
		trimmed := strings.Trim(cell, ":-")
		if trimmed != "" {
			return false
		}
	}
	return true
}

func containsStaleEvidence(evidence string) bool {
	return strings.Contains(evidence, "未实施") || strings.Contains(strings.ToLower(evidence), "not implemented")
}

func verificationRecords(root, evidence string) []string {
	verificationRoot := filepath.Join(root, "docs", "verification")
	seen := make(map[string]struct{})
	var records []string
	for _, target := range markdownLinkTargets(evidence) {
		parsed, err := url.Parse(target)
		if err != nil || parsed.IsAbs() || filepath.IsAbs(parsed.Path) || parsed.Path == "" {
			continue
		}
		decoded, err := url.PathUnescape(parsed.Path)
		if err != nil {
			continue
		}
		resolved := filepath.Clean(filepath.Join(root, "docs", "product", filepath.FromSlash(decoded)))
		if !pathWithin(root, resolved) {
			continue
		}
		relative, err := filepath.Rel(root, resolved)
		if err != nil {
			continue
		}
		repositoryRelative := filepath.ToSlash(relative)
		if !strings.HasPrefix(repositoryRelative, "docs/verification/") {
			continue
		}
		info, err := os.Stat(resolved)
		if err == nil && info.Mode().IsRegular() &&
			resolvedPathWithin(root, resolved) &&
			resolvedPathWithin(verificationRoot, resolved) {
			if _, duplicate := seen[resolved]; !duplicate {
				seen[resolved] = struct{}{}
				records = append(records, resolved)
			}
		}
	}
	return records
}

func anyVerificationRecordPasses(root string, records []string, featureID string) bool {
	for _, record := range records {
		relative, err := filepath.Rel(root, record)
		if err != nil {
			continue
		}
		content, err := readRepositoryFile(root, filepath.ToSlash(relative))
		if err == nil && verificationTableHasPass(strings.Split(string(content), "\n"), featureID) {
			return true
		}
	}
	return false
}

func verificationTableHasPass(lines []string, featureID string) bool {
	inFence := false
	fenceMarker := byte(0)
	fenceLength := 0
	inComment := false
	pendingHeader := false
	inTable := false
	for _, rawLine := range lines {
		line := visibleMarkdownOutsideComments(rawLine, &inComment)
		marker, runLength, remainder, isFence := markdownFencePrefix(line)
		if inFence {
			if isFence && marker == fenceMarker && runLength >= fenceLength && strings.TrimSpace(remainder) == "" {
				inFence = false
				fenceMarker = 0
				fenceLength = 0
			}
			continue
		}
		if isFence {
			inFence = true
			fenceMarker = marker
			fenceLength = runLength
			continue
		}
		cells := markdownTableCells(line)
		if len(cells) == 0 {
			pendingHeader = false
			inTable = false
			continue
		}
		if !inTable {
			if pendingHeader && isTableDelimiter(cells) {
				inTable = true
				pendingHeader = false
				continue
			}
			pendingHeader = true
			continue
		}
		if isTableDelimiter(cells) {
			continue
		}
		featureCell := -1
		passCell := -1
		for index, cell := range cells {
			switch cell {
			case featureID:
				featureCell = index
			case "PASS":
				passCell = index
			}
		}
		if featureCell >= 0 && passCell >= 0 && featureCell != passCell {
			return true
		}
	}
	return false
}

func markdownFencePrefix(line string) (byte, int, string, bool) {
	indent := 0
	for indent < len(line) && line[indent] == ' ' {
		indent++
	}
	if indent > 3 {
		return 0, 0, "", false
	}
	text := line[indent:]
	if len(text) < 3 || (text[0] != '`' && text[0] != '~') {
		return 0, 0, "", false
	}
	marker := text[0]
	runLength := 1
	for runLength < len(text) && text[runLength] == marker {
		runLength++
	}
	if runLength < 3 {
		return 0, 0, "", false
	}
	return marker, runLength, text[runLength:], true
}

func visibleMarkdownOutsideComments(line string, inComment *bool) string {
	var visible strings.Builder
	remaining := line
	for remaining != "" {
		if *inComment {
			end := strings.Index(remaining, "-->")
			if end < 0 {
				return visible.String()
			}
			*inComment = false
			remaining = remaining[end+3:]
			continue
		}
		start := strings.Index(remaining, "<!--")
		if start < 0 {
			visible.WriteString(remaining)
			break
		}
		visible.WriteString(remaining[:start])
		remaining = remaining[start+4:]
		*inComment = true
	}
	return visible.String()
}
