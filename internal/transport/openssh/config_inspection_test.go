package openssh

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/TyrantLucifer/awesome-mac-sftp/internal/testkit"
)

func TestInspectConfigUsesBoundedNonConnectingOpenSSHExpansion(t *testing.T) {
	t.Parallel()

	binary := configInspectionExecutable(t, `#!/bin/sh
test "$1" = "-G" || exit 21
test "$2" = "-T" || exit 22
printf '%s\n' 'hostname 127.0.0.1' 'port 2222' 'stricthostkeychecking ask' 'userknownhostsfile /seeded/private/known_hosts' 'proxyjump none' 'proxycommand none'
`)
	inspection, err := InspectConfig(context.Background(), binary, "work-alias")
	if err != nil {
		t.Fatalf("InspectConfig(): %v", err)
	}
	if inspection.Hostname != "127.0.0.1" || inspection.Port != "2222" {
		t.Fatalf("endpoint = %#v", inspection)
	}
	if inspection.StrictHostKeyChecking != "ask" || !inspection.KnownHostsConfigured || inspection.ProxyConfigured {
		t.Fatalf("policy = %#v", inspection)
	}
}

func TestInspectConfigFailsClosedOnUnboundedOrSecretBearingFailure(t *testing.T) {
	t.Parallel()

	secret := "stage6-openssh-config-secret"
	tooLarge := "hostname " + strings.Repeat("x", maxConfigInspectionBytes+1)
	for name, script := range map[string]string{
		"unbounded": "#!/bin/sh\nprintf '%s\\n' '" + tooLarge + "'\n",
		"failed":    "#!/bin/sh\nprintf '%s\\n' '" + secret + "' >&2\nexit 9\n",
	} {
		t.Run(name, func(t *testing.T) {
			binary := configInspectionExecutable(t, script)
			_, err := InspectConfig(context.Background(), binary, "work-alias")
			if err == nil {
				t.Fatal("InspectConfig() error = nil")
			}
			if strings.Contains(err.Error(), secret) || strings.Contains(err.Error(), tooLarge[:128]) {
				t.Fatalf("InspectConfig() exposed subprocess output: %v", err)
			}
		})
	}
}

func TestInspectConfigRejectsDisabledKnownHostsAndRecognizesProxy(t *testing.T) {
	t.Parallel()

	binary := configInspectionExecutable(t, `#!/bin/sh
printf '%s\n' 'hostname proxy-target' 'port 22' 'stricthostkeychecking no' 'userknownhostsfile none' 'proxyjump bastion' 'proxycommand none'
`)
	inspection, err := InspectConfig(context.Background(), binary, "work-alias")
	if err != nil {
		t.Fatalf("InspectConfig(): %v", err)
	}
	if inspection.StrictHostKeyChecking != "no" || inspection.KnownHostsConfigured || !inspection.ProxyConfigured {
		t.Fatalf("inspection = %#v", inspection)
	}
}

func TestInspectConfigRejectsAmbiguousOrUnknownHostKeyPolicy(t *testing.T) {
	t.Parallel()

	for name, lines := range map[string]string{
		"duplicate": "hostname first\nhostname second\nport 22\nstricthostkeychecking yes\nuserknownhostsfile /safe/known_hosts\nproxyjump none\nproxycommand none",
		"unknown":   "hostname host\nport 22\nstricthostkeychecking future-mode\nuserknownhostsfile /safe/known_hosts\nproxyjump none\nproxycommand none",
	} {
		t.Run(name, func(t *testing.T) {
			binary := configInspectionExecutable(t, "#!/bin/sh\nprintf '%s\\n' '"+strings.ReplaceAll(lines, "\n", "' '")+"'\n")
			if _, err := InspectConfig(context.Background(), binary, "work-alias"); err == nil {
				t.Fatal("InspectConfig() error = nil")
			}
		})
	}
}

func configInspectionExecutable(t *testing.T, content string) string {
	t.Helper()
	directory := testkit.PersistentTempDir(t)
	if err := os.Chmod(directory, 0o700); err != nil { //nolint:gosec // executable fixture directory is owner-only
		t.Fatalf("chmod fixture directory: %v", err)
	}
	path := filepath.Join(directory, "ssh")
	if err := os.WriteFile(path, []byte(content), 0o700); err != nil { //nolint:gosec // fixture must be executable
		t.Fatalf("write executable fixture: %v", err)
	}
	return path
}
