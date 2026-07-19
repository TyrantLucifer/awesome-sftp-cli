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

func TestREL002DocumentsBoundedReadOnlyInstalledWorkspaceCompletion(t *testing.T) {
	cli := readREL009Document(t, "../../docs/user/cli.md")
	for _, required := range []string{
		"bash, zsh, and fish",
		"value immediately following `--workspace`",
		"at most 1,024 entries",
		"at most 256 valid private-file names",
		"without creating it",
		"does not read workspace contents",
		"does not read workspace contents, start or contact the daemon",
	} {
		if !strings.Contains(cli, required) {
			t.Errorf("CLI completion contract missing %q", required)
		}
	}
	install := readREL009Document(t, "../../docs/release/INSTALL.md")
	for _, required := range []string{
		"`amsftp completion bash`",
		"`amsftp completion zsh`",
		"`amsftp completion fish`",
		"completes saved names only after `--workspace`",
		"does not start the daemon or create a missing state directory",
	} {
		if !strings.Contains(install, required) {
			t.Errorf("install completion contract missing %q", required)
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

func TestWORK006RunbookDocumentsNonDestructiveWorkspaceExportAndRepair(t *testing.T) {
	t.Parallel()
	runbook := readREL009Document(t, "../../docs/operations/runbook.md")
	assertREL009Order(t, runbook, []string{
		"## Workspace migration recovery",
		"`amsftp daemon stop --confirm stop --format json`",
		"`~/Library/Application Support/io.github.tyrantlucifer.amsftp/state/workspaces/`",
		"`${XDG_STATE_HOME:-~/.local/state}/amsftp/workspaces/`",
		"`<name>.json`",
		"`<name>.schema-v1.backup`",
		"mode `0600`",
		"new workspace name",
	})
	for _, required := range []string{
		"invalid source is never overwritten",
		"migration backup is never overwritten",
		"Never edit the original or migration backup in place",
		"copy the exact bytes to a separate owner-private location",
	} {
		if !strings.Contains(runbook, required) {
			t.Errorf("workspace recovery guide missing %q", required)
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

func TestREL011DaemonUpgradeCompatibilityIsFailClosed(t *testing.T) {
	t.Parallel()
	upgrade := readREL009Document(t, "../../docs/release/UPGRADE.md")
	for _, required := range []string{
		"A probe error is not proof that no daemon exists",
		"must not invoke the starter",
		"never delete or replace the control socket",
		"stop the verified daemon explicitly",
	} {
		if !strings.Contains(upgrade, required) {
			t.Errorf("upgrade guide missing daemon compatibility contract %q", required)
		}
	}

	boundaries := readREL009Document(t, "../../docs/product/compatibility-boundaries.md")
	for _, required := range []string{
		"protocol-incompatible client fails before session allocation",
		"compatible clients already connected continue",
		"does not replace the daemon or its control socket",
	} {
		if !strings.Contains(boundaries, required) {
			t.Errorf("compatibility boundaries missing daemon contract %q", required)
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
