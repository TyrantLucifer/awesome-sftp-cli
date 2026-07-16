//go:build darwin || linux

package terminalhandoff

import (
	"errors"
	"fmt"
	"os"
	"sync"
	"syscall"

	"golang.org/x/sys/unix"
)

var ErrInvalidTerminalState = errors.New("terminal state does not belong to this Unix adapter")

// UnixPlatform implements Platform for a controlling terminal on Darwin and
// Linux. The caller retains ownership of tty and must keep it open for the
// lifetime of the adapter.
type UnixPlatform struct {
	tty *os.File
	fd  int
	mu  sync.Mutex
}

type unixTerminalState struct {
	owner                  *UnixPlatform
	termios                unix.Termios
	foregroundProcessGroup int
}

func NewUnixPlatform(tty *os.File) (*UnixPlatform, error) {
	if tty == nil {
		return nil, errors.New("create Unix terminal adapter: TTY is nil")
	}
	fd := int(tty.Fd())
	if fd < 0 {
		return nil, errors.New("create Unix terminal adapter: invalid TTY descriptor")
	}
	if _, err := unix.FcntlInt(uintptr(fd), unix.F_GETFD, 0); err != nil {
		return nil, fmt.Errorf("create Unix terminal adapter: inspect TTY descriptor: %w", err)
	}
	return &UnixPlatform{tty: tty, fd: fd}, nil
}

func (platform *UnixPlatform) SaveTerminal() (TerminalState, error) {
	platform.mu.Lock()
	defer platform.mu.Unlock()

	termios, err := getTermios(platform.fd)
	if err != nil {
		return TerminalState{}, fmt.Errorf("save Unix terminal termios: %w", err)
	}
	foregroundProcessGroup, err := unix.IoctlGetInt(platform.fd, unix.TIOCGPGRP)
	if err != nil {
		return TerminalState{}, fmt.Errorf("save Unix terminal foreground process group: %w", err)
	}
	if foregroundProcessGroup <= 0 {
		return TerminalState{}, fmt.Errorf("save Unix terminal foreground process group: invalid group %d", foregroundProcessGroup)
	}
	return NewTerminalState(unixTerminalState{
		owner:                  platform,
		termios:                *termios,
		foregroundProcessGroup: foregroundProcessGroup,
	}), nil
}

func (platform *UnixPlatform) GiveForeground(processGroup int) error {
	if processGroup <= 0 {
		return fmt.Errorf("give Unix terminal foreground: invalid process group %d", processGroup)
	}
	platform.mu.Lock()
	defer platform.mu.Unlock()
	if err := giveUnixForeground(platform.fd, processGroup, setForegroundProcessGroup, unix.Kill); err != nil {
		return fmt.Errorf("give Unix terminal foreground to process group %d: %w", processGroup, err)
	}
	return nil
}

func giveUnixForeground(
	fd int,
	processGroup int,
	setForeground func(int, int) error,
	signalProcessGroup func(int, syscall.Signal) error,
) error {
	if err := setForeground(fd, processGroup); err != nil {
		if errors.Is(err, syscall.ESRCH) && errors.Is(signalProcessGroup(-processGroup, 0), syscall.ESRCH) {
			return nil
		}
		return err
	}
	if err := signalProcessGroup(-processGroup, syscall.SIGCONT); err != nil && !errors.Is(err, syscall.ESRCH) {
		return fmt.Errorf("continue process group after terminal foreground handoff: %w", err)
	}
	return nil
}

func (platform *UnixPlatform) ReclaimForeground(state TerminalState) error {
	native, err := platform.nativeState(state)
	if err != nil {
		return err
	}
	platform.mu.Lock()
	defer platform.mu.Unlock()
	if err := setForegroundProcessGroup(platform.fd, native.foregroundProcessGroup); err != nil {
		return fmt.Errorf("reclaim Unix terminal foreground for process group %d: %w", native.foregroundProcessGroup, err)
	}
	return nil
}

func (platform *UnixPlatform) RestoreTerminal(state TerminalState) error {
	native, err := platform.nativeState(state)
	if err != nil {
		return err
	}
	platform.mu.Lock()
	defer platform.mu.Unlock()
	termios := native.termios
	if err := setTermios(platform.fd, &termios); err != nil {
		return fmt.Errorf("restore Unix terminal termios: %w", err)
	}
	return nil
}

func (platform *UnixPlatform) nativeState(state TerminalState) (unixTerminalState, error) {
	native, ok := state.Opaque().(unixTerminalState)
	if !ok || native.owner != platform || native.foregroundProcessGroup <= 0 {
		return unixTerminalState{}, ErrInvalidTerminalState
	}
	return native, nil
}
