//go:build darwin || linux

package app

import (
	"os/exec"
	"syscall"
)

func configureDaemonAutostart(command *exec.Cmd) {
	command.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
}
