//go:build darwin || linux

package openssh

import (
	"os/exec"
	"testing"
)

func TestConfigureProcessGroupIsolatesOpenSSHChildren(t *testing.T) {
	command := exec.Command("/usr/bin/true")

	configureProcessGroup(command)

	if command.SysProcAttr == nil || !command.SysProcAttr.Setpgid {
		t.Fatal("OpenSSH command is not configured in an isolated process group")
	}
	if command.Cancel == nil {
		t.Fatal("OpenSSH command cancellation does not terminate its process group")
	}
}
