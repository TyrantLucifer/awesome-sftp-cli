package docscheck

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"
)

var (
	activeStagePattern   = regexp.MustCompile(`\*\*Active stage\*\*:\s*Stage\s+([0-6])\b`)
	planStagePattern     = regexp.MustCompile(`^## Stage\s+([0-9]+):`)
	planStatusPattern    = regexp.MustCompile(`^\*\*Status\*\*:\s*(.+?)\s*$`)
	lifecyclePattern     = regexp.MustCompile(`(?i)^\s*-\s+\*\*Lifecycle\*\*:\s*Stage\s+([0-6])\b.*\b(not started|in progress|complete)\s*$`)
	stageStatusPattern   = regexp.MustCompile(`^\s*-\s+\*\*状态\*\*：\s*(Not Started|In Progress|Complete)\s*$`)
	lifecycleCandidate   = "**Lifecycle**"
	stageStatusCandidate = "**状态**"
)

var requiredStageDocuments = []string{
	"docs/stages/00-foundation.md",
	"docs/stages/01-read-only-explorer.md",
	"docs/stages/02-durable-transfers.md",
	"docs/stages/03-preview-edit-cache.md",
	"docs/stages/04-search-helper.md",
	"docs/stages/05-direct-transfer-scale.md",
	"docs/stages/06-hardening-release.md",
}

func checkStageDocuments(root string) []Finding {
	var findings []Finding
	for _, path := range requiredStageDocuments {
		if _, err := readLines(root, path); err != nil {
			findings = append(findings, Finding{Path: path, Line: 1, Rule: "stage.missing", Message: "required Stage 0–6 document is missing"})
		}
	}
	return findings
}

func checkStateAndPlan(root string) []Finding {
	stateLines, err := readLines(root, "PROJECT_STATE.md")
	if err != nil {
		return []Finding{{Path: "PROJECT_STATE.md", Line: 1, Rule: "state.missing", Message: err.Error()}}
	}
	planLines, err := readLines(root, "IMPLEMENTATION_PLAN.md")
	if err != nil {
		return []Finding{{Path: "IMPLEMENTATION_PLAN.md", Line: 1, Rule: "plan.missing", Message: err.Error()}}
	}

	stateStage, stateLine, ok := parseActiveStage(stateLines)
	if !ok {
		return []Finding{{Path: "PROJECT_STATE.md", Line: 1, Rule: "state.active_stage", Message: "active Stage line is missing or malformed"}}
	}
	if finding, invalid := planStageOrderFinding(planLines); invalid {
		return []Finding{finding}
	}
	planStage, planStatus, planStatusLine, ok := currentPlanStageDetails(planLines)
	if !ok || planStatusLine <= 0 {
		return []Finding{{Path: "IMPLEMENTATION_PLAN.md", Line: 1, Rule: "plan.current_stage", Message: "cannot determine current Stage from plan statuses"}}
	}

	var findings []Finding
	if stateStage != planStage {
		findings = append(findings, Finding{
			Path:    "PROJECT_STATE.md",
			Line:    stateLine,
			Rule:    "state.stage_mismatch",
			Message: fmt.Sprintf("project state names Stage %d but implementation plan's current Stage is %d", stateStage, planStage),
		})
	}

	lifecycleStage, lifecycleStatus, lifecycleLine, lifecycleOK := parseLifecycle(stateLines)
	if !lifecycleOK {
		findings = append(findings, Finding{Path: "PROJECT_STATE.md", Line: lifecycleLine, Rule: "state.lifecycle", Message: "Lifecycle line is missing or malformed"})
	} else {
		if lifecycleStage != stateStage {
			findings = append(findings, Finding{
				Path: "PROJECT_STATE.md", Line: lifecycleLine, Rule: "state.lifecycle_stage_mismatch",
				Message: fmt.Sprintf("Lifecycle names Stage %d but Active stage names Stage %d", lifecycleStage, stateStage),
			})
		}
		if lifecycleStatus != planStatus {
			findings = append(findings, Finding{
				Path: "PROJECT_STATE.md", Line: lifecycleLine, Rule: "state.status_mismatch",
				Message: fmt.Sprintf("Lifecycle status %q does not match IMPLEMENTATION_PLAN.md Stage %d status %q", lifecycleStatus, planStage, planStatus),
			})
		}
	}

	stagePath := requiredStageDocuments[stateStage]
	stageLines, stageErr := readLines(root, stagePath)
	if stageErr == nil {
		stageStatus, stageStatusLine, stageStatusOK := parseStageStatus(stageLines)
		if !stageStatusOK {
			findings = append(findings, Finding{Path: stagePath, Line: stageStatusLine, Rule: "stage.status", Message: "active Stage document status line is missing or malformed"})
		} else if stageStatus != planStatus {
			findings = append(findings, Finding{
				Path: stagePath, Line: stageStatusLine, Rule: "stage.status_mismatch",
				Message: fmt.Sprintf("active Stage %d document status %q does not match IMPLEMENTATION_PLAN.md status %q", stateStage, stageStatus, planStatus),
			})
		}
	}
	return findings
}

func parseActiveStage(lines []string) (int, int, bool) {
	for index, line := range lines {
		match := activeStagePattern.FindStringSubmatch(line)
		if match == nil {
			continue
		}
		stage, _ := strconv.Atoi(match[1])
		return stage, index + 1, true
	}
	return 0, 0, false
}

func currentPlanStage(lines []string) (int, bool) {
	stage, _, _, ok := currentPlanStageDetails(lines)
	return stage, ok
}

func currentPlanStageDetails(lines []string) (int, string, int, bool) {
	statuses, statusLines, _, invalid := planStageStatuses(lines)
	if invalid {
		return 0, "", 0, false
	}
	if _, invalid := planStageOrderFindingFromStatuses(statuses, statusLines); invalid {
		return 0, "", 0, false
	}
	for stage := 0; stage <= 6; stage++ {
		status, exists := statuses[stage]
		if !exists || canonicalStageStatus(status) != status {
			return 0, "", 0, false
		}
		if status != "Complete" {
			return stage, status, statusLines[stage], true
		}
	}
	return 6, statuses[6], statusLines[6], true
}

func planStageStatuses(lines []string) (map[int]string, map[int]int, Finding, bool) {
	type stageHeader struct {
		stage int
		line  int
	}

	headerLines := make(map[int]int)
	headerAtLine := make(map[int]int)
	headerOrder := make([]stageHeader, 0, 7)
	for index, line := range lines {
		match := planStagePattern.FindStringSubmatch(strings.TrimSpace(line))
		if match == nil {
			continue
		}
		stageNumber, err := strconv.ParseUint(match[1], 10, 64)
		if err != nil || stageNumber > 6 {
			return nil, nil, Finding{
				Path: "IMPLEMENTATION_PLAN.md", Line: index + 1, Rule: "plan.stage_header",
				Message: fmt.Sprintf("Stage %s header is outside the allowed Stage 0–6 range", match[1]),
			}, true
		}
		stage := int(stageNumber)
		if _, exists := headerLines[stage]; exists {
			return nil, nil, Finding{
				Path: "IMPLEMENTATION_PLAN.md", Line: index + 1, Rule: "plan.stage_header",
				Message: fmt.Sprintf("Stage %d header appears more than once", stage),
			}, true
		}
		headerLines[stage] = index + 1
		headerAtLine[index+1] = stage
		headerOrder = append(headerOrder, stageHeader{stage: stage, line: index + 1})
	}
	for stage := 0; stage <= 6; stage++ {
		if _, exists := headerLines[stage]; !exists {
			return nil, nil, Finding{
				Path: "IMPLEMENTATION_PLAN.md", Line: 1, Rule: "plan.stage_header",
				Message: fmt.Sprintf("Stage %d header is missing", stage),
			}, true
		}
	}
	for expectedStage, header := range headerOrder {
		if header.stage != expectedStage {
			return nil, nil, Finding{
				Path: "IMPLEMENTATION_PLAN.md", Line: header.line, Rule: "plan.stage_header",
				Message: fmt.Sprintf("Stage %d header is out of order; expected Stage %d at this position", header.stage, expectedStage),
			}, true
		}
	}

	statuses := make(map[int]string)
	statusLines := make(map[int]int)
	currentHeader := -1
	for index, line := range lines {
		trimmed := strings.TrimSpace(line)
		if stage, isHeader := headerAtLine[index+1]; isHeader {
			currentHeader = stage
			continue
		}
		if !strings.HasPrefix(trimmed, "**Status**") {
			continue
		}
		if currentHeader < 0 {
			return nil, nil, Finding{
				Path: "IMPLEMENTATION_PLAN.md", Line: index + 1, Rule: "plan.stage_status",
				Message: "Status must follow a Stage 0–6 header",
			}, true
		}
		if _, exists := statuses[currentHeader]; exists {
			return nil, nil, Finding{
				Path: "IMPLEMENTATION_PLAN.md", Line: index + 1, Rule: "plan.stage_status",
				Message: fmt.Sprintf("Stage %d defines more than one Status", currentHeader),
			}, true
		}
		match := planStatusPattern.FindStringSubmatch(trimmed)
		if match == nil {
			return nil, nil, Finding{
				Path: "IMPLEMENTATION_PLAN.md", Line: index + 1, Rule: "plan.stage_status",
				Message: fmt.Sprintf("Stage %d must define exactly one canonical Status", currentHeader),
			}, true
		}
		status := strings.TrimSpace(match[1])
		if canonicalStageStatus(status) != status {
			return nil, nil, Finding{
				Path: "IMPLEMENTATION_PLAN.md", Line: index + 1, Rule: "plan.stage_status",
				Message: fmt.Sprintf("Stage %d status %q must be one of %q, %q, or %q", currentHeader, status, "Not Started", "In Progress", "Complete"),
			}, true
		}
		statuses[currentHeader] = status
		statusLines[currentHeader] = index + 1
	}
	for stage := 0; stage <= 6; stage++ {
		if _, exists := statuses[stage]; !exists {
			return nil, nil, Finding{
				Path: "IMPLEMENTATION_PLAN.md", Line: headerLines[stage], Rule: "plan.stage_status",
				Message: fmt.Sprintf("Stage %d must define exactly one canonical Status", stage),
			}, true
		}
	}
	return statuses, statusLines, Finding{}, false
}

func planStageOrderFinding(lines []string) (Finding, bool) {
	statuses, statusLines, finding, invalid := planStageStatuses(lines)
	if invalid {
		return finding, true
	}
	return planStageOrderFindingFromStatuses(statuses, statusLines)
}

func planStageOrderFindingFromStatuses(statuses map[int]string, statusLines map[int]int) (Finding, bool) {
	previousStage := -1
	previousStatus := ""
	phase := "Complete"
	for stage := 0; stage <= 6; stage++ {
		status, exists := statuses[stage]
		if !exists || canonicalStageStatus(status) != status {
			continue
		}

		valid := false
		switch status {
		case "Complete":
			valid = phase == "Complete"
		case "In Progress":
			valid = phase == "Complete"
			phase = "In Progress"
		case "Not Started":
			valid = true
			phase = "Not Started"
		}
		if !valid {
			return Finding{
				Path: "IMPLEMENTATION_PLAN.md", Line: statusLines[stage], Rule: "plan.stage_order",
				Message: fmt.Sprintf("Stage %d status %q is invalid after Stage %d status %q", stage, status, previousStage, previousStatus),
			}, true
		}
		previousStage = stage
		previousStatus = status
	}
	return Finding{}, false
}

func parseLifecycle(lines []string) (int, string, int, bool) {
	matchLine := 0
	matchStage := 0
	matchStatus := ""
	candidateLine := 0
	for index, line := range lines {
		if !strings.Contains(line, lifecycleCandidate) {
			continue
		}
		if candidateLine == 0 {
			candidateLine = index + 1
		}
		match := lifecyclePattern.FindStringSubmatch(line)
		if match == nil || matchLine != 0 {
			return 0, "", index + 1, false
		}
		matchLine = index + 1
		matchStage, _ = strconv.Atoi(match[1])
		matchStatus = canonicalStageStatus(match[2])
	}
	if matchLine == 0 {
		if candidateLine == 0 {
			candidateLine = 1
		}
		return 0, "", candidateLine, false
	}
	return matchStage, matchStatus, matchLine, true
}

func parseStageStatus(lines []string) (string, int, bool) {
	matchLine := 0
	matchStatus := ""
	candidateLine := 0
	for index, line := range lines {
		if !strings.Contains(line, stageStatusCandidate) {
			continue
		}
		if candidateLine == 0 {
			candidateLine = index + 1
		}
		match := stageStatusPattern.FindStringSubmatch(line)
		if match == nil || matchLine != 0 {
			return "", index + 1, false
		}
		matchLine = index + 1
		matchStatus = match[1]
	}
	if matchLine == 0 {
		if candidateLine == 0 {
			candidateLine = 1
		}
		return "", candidateLine, false
	}
	return matchStatus, matchLine, true
}

func canonicalStageStatus(status string) string {
	switch strings.ToLower(strings.TrimSpace(status)) {
	case "not started":
		return "Not Started"
	case "in progress":
		return "In Progress"
	case "complete":
		return "Complete"
	default:
		return ""
	}
}
