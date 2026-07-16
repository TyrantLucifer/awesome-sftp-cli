//go:build darwin

package app

import "golang.org/x/sys/unix"

func getProbeTermios(descriptor int) (*unix.Termios, error) {
	return unix.IoctlGetTermios(descriptor, unix.TIOCGETA)
}

func setProbeTermios(descriptor int, state *unix.Termios) error {
	return unix.IoctlSetTermios(descriptor, unix.TIOCSETA, state)
}
