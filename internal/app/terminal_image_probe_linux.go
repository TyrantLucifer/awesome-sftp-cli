//go:build linux

package app

import "golang.org/x/sys/unix"

func getProbeTermios(descriptor int) (*unix.Termios, error) {
	return unix.IoctlGetTermios(descriptor, unix.TCGETS)
}

func setProbeTermios(descriptor int, state *unix.Termios) error {
	return unix.IoctlSetTermios(descriptor, unix.TCSETS, state)
}
