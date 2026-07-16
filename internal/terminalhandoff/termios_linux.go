//go:build linux

package terminalhandoff

import (
	"runtime"

	"golang.org/x/sys/unix"
)

func getTermios(fd int) (*unix.Termios, error) {
	return unix.IoctlGetTermios(fd, unix.TCGETS)
}

func setTermios(fd int, termios *unix.Termios) error {
	return unix.IoctlSetTermios(fd, unix.TCSETS, termios)
}

func setForegroundProcessGroup(fd, processGroup int) error {
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()

	var blocked unix.Sigset_t
	signalIndex := uint(unix.SIGTTOU - 1)
	blocked.Val[signalIndex/64] |= uint64(1) << (signalIndex % 64)
	var previous unix.Sigset_t
	if err := unix.PthreadSigmask(unix.SIG_BLOCK, &blocked, &previous); err != nil {
		return err
	}
	defer func() {
		_ = unix.PthreadSigmask(unix.SIG_SETMASK, &previous, nil)
	}()
	return unix.IoctlSetPointerInt(fd, unix.TIOCSPGRP, processGroup)
}
