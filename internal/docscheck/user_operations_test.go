package docscheck

import (
	"os"
	"strings"
	"testing"
)

func TestREL009DocumentationIndexCoversEveryUserAndOperationsDomain(t *testing.T) {
	t.Parallel()
	index := readREL009Document(t, "../../docs/README.md")
	for _, required := range []string{
		"## User and operations path",
		"[Install](release/INSTALL.md)",
		"[First run](user/getting-started.md)",
		"[Upgrade and rollback](release/UPGRADE.md)",
		"[Operations and recovery](operations/runbook.md)",
		"[Uninstall](release/UNINSTALL.md)",
		"SSH and Kerberos prerequisites",
		"default keymap",
		"workspaces",
		"transfer safety",
		"Helper",
		"direct transfer",
		"recovery",
		"doctor",
		"troubleshooting",
	} {
		if !strings.Contains(index, required) {
			t.Errorf("documentation index missing %q", required)
		}
	}
}

func TestREL009GettingStartedUsesExactSafeCLIAndAuthenticationPrerequisites(t *testing.T) {
	t.Parallel()
	guide := readREL009Document(t, "../../docs/user/getting-started.md")
	assertREL009Order(t, guide, []string{
		"## Before the first run",
		"`/usr/bin/ssh -V`",
		"`/usr/bin/ssh work`",
		"`klist`",
		"`amsftp --version`",
		"`amsftp config validate`",
		"`amsftp daemon start --format json`",
		"`amsftp doctor --endpoint work --format json`",
		"`amsftp --workspace <name>`",
		"## Default keymap and transfer safety",
	})
	for _, required := range []string{
		"does not replace or bypass system OpenSSH",
		"does not copy Kerberos tickets",
		"strict host-key verification",
		"durable Job",
		"preview and explicit confirmation",
	} {
		if !strings.Contains(guide, required) {
			t.Errorf("getting-started guide missing %q", required)
		}
	}
}

func TestREL009OperationsRunbookKeepsDiagnosticsAndFallbacksBounded(t *testing.T) {
	t.Parallel()
	runbook := readREL009Document(t, "../../docs/operations/runbook.md")
	assertREL009Order(t, runbook, []string{
		"## Read-only triage",
		"`amsftp daemon status --format json`",
		"`amsftp doctor --format json`",
		"`amsftp support-bundle preview --format json`",
		"## Job recovery",
		"`amsftp job list --format json`",
		"`amsftp job events <job-id> --format json`",
		"`amsftp job resume <job-id> --format json`",
		"## Helper and direct-transfer fallback",
		"`amsftp helper status work --format json`",
		"## Escalation and rollback",
	})
	for _, required := range []string{
		"Level 0",
		"local relay",
		"Production Helper remains **CLOSED**",
		"Production Level 2 remains **CLOSED**",
		"no automatic upload",
		"do not delete the database, cache, socket, part file, or recovery record",
	} {
		if !strings.Contains(runbook, required) {
			t.Errorf("operations runbook missing %q", required)
		}
	}
}

func TestREL009ReleaseLifecycleDocumentsExactUpgradeRollbackAndUninstall(t *testing.T) {
	t.Parallel()
	upgrade := readREL009Document(t, "../../docs/release/UPGRADE.md")
	assertREL009Order(t, upgrade, []string{
		"## Prepare",
		"`amsftp daemon status --format json`",
		"`amsftp daemon stop --confirm stop --format json`",
		"## Upgrade",
		"`amsftp --version`",
		"`amsftp daemon start --format json`",
		"`amsftp doctor --format json`",
		"## Roll back",
		"## Withdraw a release",
	})
	for _, required := range []string{
		"checksums.txt",
		"VERSION.json",
		"newer database",
		"read-only diagnosis",
		"previous extraction",
		"Do not delete or downgrade persistent state",
	} {
		if !strings.Contains(upgrade, required) {
			t.Errorf("upgrade guide missing %q", required)
		}
	}
	uninstall := readREL009Document(t, "../../docs/release/UNINSTALL.md")
	for _, required := range []string{
		"keeps user data",
		"amsftp daemon stop --confirm stop --format json",
		"Do not use wildcard service deletion",
		"Optional data deletion is destructive",
		"Production Helper and Level 2 remain closed",
	} {
		if !strings.Contains(uninstall, required) {
			t.Errorf("uninstall guide missing %q", required)
		}
	}
}

func readREL009Document(t *testing.T, path string) string {
	t.Helper()
	contents, err := os.ReadFile(path) //nolint:gosec // every caller passes one fixed repository documentation path from this test file.
	if err != nil {
		t.Fatal(err)
	}
	return string(contents)
}

func assertREL009Order(t *testing.T, document string, ordered []string) {
	t.Helper()
	position := 0
	for _, required := range ordered {
		next := strings.Index(document[position:], required)
		if next < 0 {
			t.Fatalf("document missing ordered contract %q", required)
		}
		position += next + len(required)
	}
}
