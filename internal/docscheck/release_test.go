package docscheck

import (
	"fmt"
	"strings"
	"testing"
)

var rel010FeatureIDs = []string{
	"WORK-006", "VIM-013", "VIM-014", "JOB-010", "HELP-013", "SEC-012", "SEC-014",
	"OBS-009", "OBS-010", "PLAT-003", "PLAT-009", "REL-001", "REL-002", "REL-003",
	"REL-004", "REL-005", "REL-006", "REL-007", "REL-008", "REL-009", "REL-010", "REL-011", "REL-012",
}

func TestREL010ReleaseAuditRequiresExactStageSixFeatureSet(t *testing.T) {
	valid := rel010MatrixLines("Verified", "[evidence](../verification/stage-06.md)")
	if findings := checkStageSixFeatureReadiness(valid); len(findings) != 0 {
		t.Fatalf("valid feature set findings = %#v", findings)
	}

	tests := []struct {
		name  string
		lines []string
		want  string
	}{
		{name: "missing", lines: append([]string(nil), valid[:len(valid)-1]...), want: "REL-012"},
		{name: "unexpected", lines: append(append([]string(nil), valid...), "| REL-999 | Extra | 6 | Verified | Exact. | [evidence](../verification/stage-06.md) |"), want: "REL-999"},
		{name: "stage drift", lines: replaceREL010Line(valid, "REL-001", "| REL-001 | Feature | 5 | Verified | Exact. | [evidence](../verification/stage-06.md) |"), want: "REL-001"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			findings := checkStageSixFeatureReadiness(test.lines)
			if !rel010FindingContains(findings, "release.stage6_features", test.want) {
				t.Fatalf("findings = %#v, want stage6 feature finding containing %q", findings, test.want)
			}
		})
	}
}

func TestREL010ReleaseAuditRequiresTerminalStatusAndDecisionADR(t *testing.T) {
	tests := []struct {
		name     string
		status   string
		evidence string
		wantRule string
	}{
		{name: "planned", status: "Planned", evidence: "pending", wantRule: "release.stage6_status"},
		{name: "in progress", status: "In Progress", evidence: "pending", wantRule: "release.stage6_status"},
		{name: "implemented", status: "Implemented", evidence: "pending", wantRule: "release.stage6_status"},
		{name: "deferred without ADR", status: "Deferred", evidence: "decision pending", wantRule: "release.stage6_decision"},
		{name: "removed without ADR", status: "Removed", evidence: "decision pending", wantRule: "release.stage6_decision"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			findings := checkStageSixFeatureReadiness(rel010MatrixLines(test.status, test.evidence))
			if !rel010FindingContains(findings, test.wantRule, "WORK-006") {
				t.Fatalf("findings = %#v, want %s for WORK-006", findings, test.wantRule)
			}
		})
	}

	for _, status := range []string{"Deferred", "Removed"} {
		lines := rel010MatrixLines(status, "[ADR-0010](../architecture/adr/0010-helper-artifact-trust-and-distribution.md)")
		if findings := checkStageSixFeatureReadiness(lines); len(findings) != 0 {
			t.Fatalf("%s with ADR findings = %#v", status, findings)
		}
	}
}

func TestREL010ReleaseAuditRequiresExactCheckedExitCriteria(t *testing.T) {
	valid := rel010ExitLines("x")
	if findings := checkStageSixExitReadiness(valid); len(findings) != 0 {
		t.Fatalf("valid exit criteria findings = %#v", findings)
	}
	missing := append([]string(nil), valid[:len(valid)-2]...)
	missing = append(missing, valid[len(valid)-1])
	extra := append([]string(nil), valid[:len(valid)-1]...)
	extra = append(extra, "- [x] Extra release shortcut.", valid[len(valid)-1])

	tests := []struct {
		name  string
		lines []string
		want  string
	}{
		{name: "unchecked", lines: rel010ExitLines(" "), want: "unchecked"},
		{name: "missing", lines: missing, want: "12"},
		{name: "reordered", lines: rel010SwapLastTwo(valid), want: "order"},
		{name: "weakened", lines: replaceREL010Line(valid, "50k/1M/100GB", "- [x] 50k only."), want: "criterion 9"},
		{name: "extra", lines: extra, want: "12"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			findings := checkStageSixExitReadiness(test.lines)
			if !rel010FindingContains(findings, "release.exit_criteria", test.want) {
				t.Fatalf("findings = %#v, want exit finding containing %q", findings, test.want)
			}
		})
	}
}

func rel010MatrixLines(status, evidence string) []string {
	lines := []string{
		"# Feature Matrix",
		"| ID | 能力 | Stage | 状态 | 验收标准 | 当前证据 |",
		"|---|---|---:|---|---|---|",
	}
	for _, id := range rel010FeatureIDs {
		lines = append(lines, fmt.Sprintf("| %s | Feature | 6 | %s | Exact. | %s |", id, status, evidence))
	}
	return lines
}

func rel010ExitLines(mark string) []string {
	criteria := []string{
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
	lines := []string{"# Stage 6", "## 6. 可验证退出标准"}
	for _, criterion := range criteria {
		lines = append(lines, fmt.Sprintf("- [%s] %s", mark, criterion))
	}
	lines = append(lines, "## 7. 测试矩阵")
	return lines
}

func replaceREL010Line(lines []string, contains, replacement string) []string {
	result := append([]string(nil), lines...)
	for index, line := range result {
		if strings.Contains(line, contains) {
			result[index] = replacement
			return result
		}
	}
	return result
}

func rel010SwapLastTwo(lines []string) []string {
	result := append([]string(nil), lines...)
	result[len(result)-3], result[len(result)-2] = result[len(result)-2], result[len(result)-3]
	return result
}

func rel010FindingContains(findings []Finding, rule, text string) bool {
	for _, finding := range findings {
		if finding.Rule == rule && strings.Contains(finding.Message, text) {
			return true
		}
	}
	return false
}
