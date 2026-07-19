package commandrun

import (
	"context"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/TyrantLucifer/awesome-sftp-cli/internal/testkit"
	"github.com/TyrantLucifer/awesome-sftp-cli/internal/transport/openssh"
)

func TestPlanLocalShellUsesFrozenDirectoryWithoutCommandText(t *testing.T) {
	shell, err := ResolveLocalShell("/bin/sh", "")
	if err != nil {
		t.Fatal(err)
	}
	plan, err := PlanLocalShell(shell, testkit.PersistentTempDir(t), os.Environ())
	if err != nil {
		t.Fatal(err)
	}
	if plan.Executable() == "" || len(plan.Arguments()) != 0 || plan.Directory() == "" {
		t.Fatalf("plan = %#v argv=%#v", plan, plan.Arguments())
	}
	if err := plan.Revalidate(); err != nil {
		t.Fatal(err)
	}
}

func TestPlanRemoteHomeShellUsesExactFreshTTYArgvWithoutCommand(t *testing.T) {
	binary := writeRemoteSSH(t)
	plan, err := PlanRemoteShell(context.Background(), RemoteConfig{OpenSSH: openssh.Config{Binary: binary, HostAlias: "prod"}}, RemoteShellHome, "", nil)
	if err != nil {
		t.Fatal(err)
	}
	want := append([]string{binary}, fixedRemoteShellArguments...)
	want = append(want, "prod")
	if got := plan.Argv(); !reflect.DeepEqual(got, want) {
		t.Fatalf("argv = %#v, want %#v", got, want)
	}
	if len(plan.Arguments()) != len(fixedRemoteShellArguments)+1 {
		t.Fatal("home shell unexpectedly has a remote command")
	}
}

func TestRemoteCurrentDirectoryShellRequiresMatchingSuccessfulProbe(t *testing.T) {
	capture := filepath.Join(t.TempDir(), "stdin")
	binary, environment := writeRemoteSSHHelper(t, "success", capture)
	config := RemoteConfig{
		OpenSSH:    openssh.Config{Binary: binary, HostAlias: "prod", Environment: environment},
		Attributes: trustedUtilityProber(),
	}
	if _, err := PlanRemoteShell(context.Background(), config, RemoteShellCurrentDirectory, "/srv/work", nil); err == nil {
		t.Fatal("current-directory shell planned without probe")
	}
	proof, err := ProbeRemoteShellCWD(context.Background(), config, "/srv/work")
	if err != nil {
		t.Fatal(err)
	}
	plan, err := PlanRemoteShell(context.Background(), config, RemoteShellCurrentDirectory, "/srv/work", &proof)
	if err != nil {
		t.Fatal(err)
	}
	wantBootstrap := `case ${SHELL-} in /*) cd '/srv/work' && exec "$SHELL" -l;; *) exit 126;; esac`
	if got := plan.Argv(); got[len(got)-1] != wantBootstrap || !strings.Contains(got[len(got)-1], "cd '/srv/work'") {
		t.Fatalf("current-directory argv = %#v", got)
	}
	if _, err := PlanRemoteShell(context.Background(), config, RemoteShellCurrentDirectory, "/srv/other", &proof); err == nil {
		t.Fatal("cwd-mismatched proof succeeded")
	}
}
