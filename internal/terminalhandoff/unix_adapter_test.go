//go:build darwin || linux

package terminalhandoff

import (
	"bytes"
	"errors"
	"io"
	"os"
	"os/exec"
	"reflect"
	"syscall"
	"testing"

	"golang.org/x/sys/unix"
)

func TestNewUnixPlatformRejectsNilTTY(t *testing.T) {
	t.Parallel()

	if _, err := NewUnixPlatform(nil); err == nil {
		t.Fatal("NewUnixPlatform(nil) succeeded")
	}
}

func TestUnixPlatformRejectsInvalidStateAndProcessGroup(t *testing.T) {
	t.Parallel()

	readEnd, writeEnd, err := os.Pipe()
	if err != nil {
		t.Fatalf("os.Pipe() error = %v", err)
	}
	defer readEnd.Close()
	defer writeEnd.Close()
	platform, err := NewUnixPlatform(readEnd)
	if err != nil {
		t.Fatalf("NewUnixPlatform() error = %v", err)
	}
	if err := platform.GiveForeground(0); err == nil {
		t.Fatal("GiveForeground(0) succeeded")
	}
	for name, operation := range map[string]func() error{
		"reclaim": func() error { return platform.ReclaimForeground(NewTerminalState("forged")) },
		"restore": func() error { return platform.RestoreTerminal(NewTerminalState("forged")) },
	} {
		if err := operation(); !errors.Is(err, ErrInvalidTerminalState) {
			t.Errorf("%s forged state error = %v, want ErrInvalidTerminalState", name, err)
		}
	}
	if _, err := platform.SaveTerminal(); err == nil {
		t.Fatal("SaveTerminal() on a pipe succeeded")
	}
}

func TestUnixPlatformPTYRoundTrip(t *testing.T) {
	if os.Getenv("AMSFTP_TERMINAL_HANDOFF_PTY_HELPER") == "1" {
		runUnixPlatformPTYHelper(t)
		return
	}

	master, slave := openTestPTY(t)
	defer master.Close()
	defer slave.Close()

	command := exec.Command(os.Args[0], "-test.run=^TestUnixPlatformPTYRoundTrip$") //nolint:gosec // exact current test binary and fixed test selector
	command.Env = append(os.Environ(), "AMSFTP_TERMINAL_HANDOFF_PTY_HELPER=1")
	command.Stdin = slave
	command.Stdout = slave
	command.Stderr = slave
	command.SysProcAttr = &syscall.SysProcAttr{Setsid: true, Setctty: true, Ctty: 0}
	var transcript bytes.Buffer
	copyDone := make(chan struct{})
	go func() {
		_, _ = io.Copy(&transcript, master)
		close(copyDone)
	}()
	if err := command.Run(); err != nil {
		_ = master.Close()
		<-copyDone
		t.Fatalf("PTY helper error = %v\n%s", err, transcript.String())
	}
	_ = master.Close()
	<-copyDone
}

func runUnixPlatformPTYHelper(t *testing.T) {
	t.Helper()

	tty, err := os.OpenFile("/dev/tty", os.O_RDWR, 0)
	if err != nil {
		t.Fatalf("open controlling TTY: %v", err)
	}
	defer tty.Close()
	platform, err := NewUnixPlatform(tty)
	if err != nil {
		t.Fatalf("NewUnixPlatform() error = %v", err)
	}
	state, err := platform.SaveTerminal()
	if err != nil {
		t.Fatalf("SaveTerminal() error = %v", err)
	}
	native, ok := state.Opaque().(unixTerminalState)
	if !ok {
		t.Fatalf("saved state type = %T, want unixTerminalState", state.Opaque())
	}
	mutated := native.termios
	mutated.Lflag ^= unix.ECHO
	if err := setTermios(int(tty.Fd()), &mutated); err != nil {
		t.Fatalf("mutate termios: %v", err)
	}
	if err := platform.RestoreTerminal(state); err != nil {
		t.Fatalf("RestoreTerminal() error = %v", err)
	}
	restored, err := getTermios(int(tty.Fd()))
	if err != nil {
		t.Fatalf("read restored termios: %v", err)
	}
	if !reflect.DeepEqual(*restored, native.termios) {
		t.Fatalf("restored termios = %#v, want %#v", *restored, native.termios)
	}
	child := exec.Command("/bin/sleep", "30") //nolint:gosec // fixed native test utility and fixed argument
	child.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	if err := child.Start(); err != nil {
		t.Fatalf("start foreground fixture: %v", err)
	}
	defer func() {
		_ = unix.Kill(-child.Process.Pid, unix.SIGKILL)
		_ = child.Wait()
	}()
	if err := platform.GiveForeground(child.Process.Pid); err != nil {
		t.Fatalf("GiveForeground(child group) error = %v", err)
	}
	foreground, err := unix.IoctlGetInt(int(tty.Fd()), unix.TIOCGPGRP)
	if err != nil {
		t.Fatalf("read child foreground group: %v", err)
	}
	if foreground != child.Process.Pid {
		t.Fatalf("foreground group = %d, want child group %d", foreground, child.Process.Pid)
	}
	if err := platform.ReclaimForeground(state); err != nil {
		t.Fatalf("ReclaimForeground() error = %v", err)
	}
	foreground, err = unix.IoctlGetInt(int(tty.Fd()), unix.TIOCGPGRP)
	if err != nil {
		t.Fatalf("read reclaimed foreground group: %v", err)
	}
	if foreground != native.foregroundProcessGroup {
		t.Fatalf("reclaimed foreground group = %d, want %d", foreground, native.foregroundProcessGroup)
	}
}
