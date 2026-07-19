package docscheck

import (
	"os"
	"strings"
	"testing"
)

func TestSecurityDocumentsFreezeThreatAndFindingContracts(t *testing.T) {
	threat, err := os.ReadFile("../../docs/security/threat-model.md")
	if err != nil {
		t.Fatal(err)
	}
	for _, heading := range []string{
		"## Assets", "## Trust boundaries", "## Threats and controls", "## Residual and open scope", "## Verification ownership",
	} {
		if !strings.Contains(string(threat), heading) {
			t.Errorf("threat model missing %q", heading)
		}
	}
	ledger, err := os.ReadFile("../../docs/security/finding-ledger.md")
	if err != nil {
		t.Fatal(err)
	}
	text := string(ledger)
	for _, required := range []string{
		"| ID | Severity | Status | Boundary | Finding | Disposition | Evidence | Owner |",
		"M6-SEC-001", "safe-shaped", "fixed", "ba334b8d8968f5b09c91c0185996994b9a307ff4",
		"## Review coverage", "## Open release boundaries", "Production Helper", "Production Level 2",
	} {
		if !strings.Contains(text, required) {
			t.Errorf("finding ledger missing %q", required)
		}
	}
	for _, forbidden := range []string{"| high | open |", "| critical | open |"} {
		if strings.Contains(strings.ToLower(text), forbidden) {
			t.Errorf("finding ledger contains unresolved release-blocking row %q", forbidden)
		}
	}
}
