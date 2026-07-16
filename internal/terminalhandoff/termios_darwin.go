//go:build darwin

package terminalhandoff

import (
	"runtime"
	"syscall"
	"unsafe"

	"golang.org/x/sys/unix"
)

func getTermios(fd int) (*unix.Termios, error) {
	return unix.IoctlGetTermios(fd, unix.TIOCGETA)
}

func setTermios(fd int, termios *unix.Termios) error {
	return unix.IoctlSetTermios(fd, unix.TIOCSETA, termios)
}

func setForegroundProcessGroup(fd, processGroup int) error {
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()

	const (
		darwinSignalBlock   = 1
		darwinSignalSetMask = 3
	)
	blocked := uint32(1) << uint(unix.SIGTTOU-1)
	var previous uint32
	if _, _, errno := syscall.RawSyscall(
		syscall.SYS_SIGPROCMASK,
		darwinSignalBlock,
		// #nosec G103 -- sigprocmask requires the address of a fixed uint32 signal mask.
		uintptr(unsafe.Pointer(&blocked)),
		// #nosec G103 -- sigprocmask writes the prior fixed uint32 signal mask here.
		uintptr(unsafe.Pointer(&previous)),
	); errno != 0 {
		return errno
	}
	defer func() {
		_, _, _ = syscall.RawSyscall(
			syscall.SYS_SIGPROCMASK,
			darwinSignalSetMask,
			// #nosec G103 -- sigprocmask reads the saved fixed uint32 signal mask.
			uintptr(unsafe.Pointer(&previous)),
			0,
		)
	}()
	return unix.IoctlSetPointerInt(fd, unix.TIOCSPGRP, processGroup)
}
