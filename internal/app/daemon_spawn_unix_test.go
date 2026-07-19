//go:build darwin || linux

package app

import (
	"os/exec"
	"testing"
)

func TestDaemonAutostartDetachesFromClientSession(t *testing.T) {
	command := exec.Command("ignored")
	configureDaemonAutostart(command)
	if command.SysProcAttr == nil || !command.SysProcAttr.Setsid {
		t.Fatal("daemon autostart must create a new session so client terminal exit cannot kill it")
	}
}
