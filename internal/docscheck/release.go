package docscheck

import (
	"fmt"
	"path/filepath"
	"regexp"
	"strings"
)

const stageSixSpecPath = "docs/stages/06-hardening-release.md"

var (
	expectedStageSixFeatureIDs = []string{
		"WORK-006", "VIM-013", "VIM-014", "JOB-010", "HELP-013", "SEC-012", "SEC-014",
		"OBS-009", "OBS-010", "PLAT-003", "PLAT-009", "REL-001", "REL-002", "REL-003",
		"REL-004", "REL-005", "REL-006", "REL-007", "REL-008", "REL-009", "REL-010", "REL-011", "REL-012",
	}
	expectedStageSixExitCriteria = []string{
		"配置、键位、CLI、退出码、IPC 和机器输出的 1.0 兼容边界有文档和测试。",
		"`--help`、man、补全、配置参考和实际命令一致。",
		"SQLite、工作区、缓存和 Helper 元数据从每个支持来源版本升级通过；中断与失败可恢复。",
		"支持矩阵内每个平台/架构都有可校验发行物和干净安装证据。",
		"守护进程升级、并发旧客户端、Socket 残留和版本不兼容均有安全行为。",
		"安全威胁模型完成，无未处置高风险项；中低风险有归属和理由。",
		"OpenSSH/Kerberos、ProxyCommand、SFTP Server、终端和 Helper 兼容矩阵完成。",
		"doctor/支持包可在不暴露秘密和完整内容的情况下定位主要故障类别。",
		"50k/1M/100GB、故障注入、race、fuzz 和发布候选长稳重新在最终候选版本通过。",
		"功能矩阵、所有 Stage Verification、`IMPLEMENTATION_PLAN.md` 和 `PROJECT_STATE.md` 与实际发行一致。",
		"新用户只依赖正式文档即可完成安装、SSH 连接、双栏浏览、一次安全传输、远端编辑和故障恢复。",
		"撤回/回滚流程在发布前演练，并明确哪些存储迁移不可直接降级。",
	}
	releaseDecisionADRPattern = regexp.MustCompile(`\]\([^)]*architecture/adr/[0-9]{4}-[^)#]+\.md(?:#[^)]*)?\)`)
	releaseCheckboxPattern    = regexp.MustCompile(`^- \[([ xX])\] (.+)$`)
)

type releaseFeatureRow struct {
	line     int
	stage    string
	status   string
	evidence string
}

// CheckRelease runs ordinary documentation checks plus the final-only REL-010
// readiness audit. It is expected to fail while Stage 6 rows or exits remain open.
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

	matrixLines, err := readLines(resolvedRoot, featureMatrixPath)
	if err != nil {
		findings = append(findings, Finding{Path: featureMatrixPath, Line: 1, Rule: "release.stage6_features", Message: err.Error()})
	} else {
		findings = append(findings, checkStageSixFeatureReadiness(matrixLines)...)
	}
	stageLines, err := readLines(resolvedRoot, stageSixSpecPath)
	if err != nil {
		findings = append(findings, Finding{Path: stageSixSpecPath, Line: 1, Rule: "release.exit_criteria", Message: err.Error()})
	} else {
		findings = append(findings, checkStageSixExitReadiness(stageLines)...)
	}
	sortFindings(findings)
	return findings
}

func checkStageSixFeatureReadiness(lines []string) []Finding {
	headerLines, schemaFinding := validateFeatureMatrixSchema(lines)
	if schemaFinding != nil {
		return []Finding{{Path: featureMatrixPath, Line: schemaFinding.Line, Rule: "release.stage6_features", Message: "feature matrix schema is not release-auditable"}}
	}
	rows := make(map[string]releaseFeatureRow)
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
		if isTableDelimiter(cells) || len(cells) != 6 || !featureIDPattern.MatchString(cells[0]) {
			continue
		}
		if _, exists := rows[cells[0]]; !exists {
			rows[cells[0]] = releaseFeatureRow{line: index + 1, stage: cells[2], status: cells[3], evidence: cells[5]}
		}
	}

	expected := make(map[string]struct{}, len(expectedStageSixFeatureIDs))
	var findings []Finding
	for _, id := range expectedStageSixFeatureIDs {
		expected[id] = struct{}{}
		row, present := rows[id]
		if !present {
			findings = append(findings, Finding{Path: featureMatrixPath, Line: 1, Rule: "release.stage6_features", Message: fmt.Sprintf("required Stage 6 feature %q is missing", id)})
			continue
		}
		if row.stage != "6" {
			findings = append(findings, Finding{Path: featureMatrixPath, Line: row.line, Rule: "release.stage6_features", Message: fmt.Sprintf("required Stage 6 feature %q moved to Stage %s", id, row.stage)})
		}
		switch row.status {
		case "Verified":
		case "Deferred", "Removed":
			if !releaseDecisionADRPattern.MatchString(row.evidence) {
				findings = append(findings, Finding{Path: featureMatrixPath, Line: row.line, Rule: "release.stage6_decision", Message: fmt.Sprintf("Stage 6 feature %q is %s without a linked ADR decision", id, row.status)})
			}
		default:
			findings = append(findings, Finding{Path: featureMatrixPath, Line: row.line, Rule: "release.stage6_status", Message: fmt.Sprintf("Stage 6 feature %q remains %s at release", id, row.status)})
		}
	}
	for id, row := range rows {
		if row.stage != "6" {
			continue
		}
		if _, required := expected[id]; !required {
			findings = append(findings, Finding{Path: featureMatrixPath, Line: row.line, Rule: "release.stage6_features", Message: fmt.Sprintf("unexpected Stage 6 feature %q is outside the frozen 23-row scope", id)})
		}
	}
	return findings
}

func checkStageSixExitReadiness(lines []string) []Finding {
	const heading = "## 6. 可验证退出标准"
	headingLine := 0
	var criterionLines []int
	var marks, criteria []string
	for index, line := range lines {
		trimmed := strings.TrimSpace(line)
		if headingLine == 0 {
			if trimmed == heading {
				headingLine = index + 1
			}
			continue
		}
		if strings.HasPrefix(trimmed, "## ") {
			break
		}
		matches := releaseCheckboxPattern.FindStringSubmatch(trimmed)
		if len(matches) == 3 {
			criterionLines = append(criterionLines, index+1)
			marks = append(marks, matches[1])
			criteria = append(criteria, matches[2])
		}
	}
	if headingLine == 0 {
		return []Finding{{Path: stageSixSpecPath, Line: 1, Rule: "release.exit_criteria", Message: "Stage 6 exit-criteria heading is missing"}}
	}

	var findings []Finding
	if len(criteria) != len(expectedStageSixExitCriteria) {
		findings = append(findings, Finding{Path: stageSixSpecPath, Line: headingLine, Rule: "release.exit_criteria", Message: fmt.Sprintf("Stage 6 must contain exactly 12 exit criteria; got %d", len(criteria))})
	}
	limit := min(len(criteria), len(expectedStageSixExitCriteria))
	for index := 0; index < limit; index++ {
		if criteria[index] != expectedStageSixExitCriteria[index] {
			findings = append(findings, Finding{Path: stageSixSpecPath, Line: criterionLines[index], Rule: "release.exit_criteria", Message: fmt.Sprintf("exit criterion %d text or order changed", index+1)})
		}
		if marks[index] == " " {
			findings = append(findings, Finding{Path: stageSixSpecPath, Line: criterionLines[index], Rule: "release.exit_criteria", Message: fmt.Sprintf("exit criterion %d is unchecked", index+1)})
		}
	}
	return findings
}
