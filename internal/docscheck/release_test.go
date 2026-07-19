package docscheck

import "testing"

func TestReleaseAuditReportsOpenStableGate(t *testing.T) {
	root := prepareFixture(t, "valid")
	writePolicyFile(t, root, "docs/release/RC-GATES.md", `# Release Gates

- [x] Documentation is current.
- [ ] Signed public artifacts are published.
`)

	var releaseFindings []Finding
	for _, finding := range CheckRelease(root) {
		if finding.Rule == "release.gate_open" {
			releaseFindings = append(releaseFindings, finding)
		}
	}
	want := []Finding{{
		Path: "docs/release/RC-GATES.md", Line: 4, Rule: "release.gate_open",
		Message: "release gate remains open: Signed public artifacts are published.",
	}}
	if len(releaseFindings) != len(want) || releaseFindings[0] != want[0] {
		t.Fatalf("release findings = %#v, want %#v", releaseFindings, want)
	}
}

func TestReleaseAuditAcceptsClosedStableGates(t *testing.T) {
	root := prepareFixture(t, "valid")
	writePolicyFile(t, root, "docs/release/RC-GATES.md", `# Release Gates

- [x] Documentation is current.
- [x] Signed public artifacts are published.
`)
	for _, finding := range CheckRelease(root) {
		if finding.Rule == "release.gate_open" || finding.Rule == "release.gates" {
			t.Fatalf("unexpected release finding: %#v", finding)
		}
	}
}
