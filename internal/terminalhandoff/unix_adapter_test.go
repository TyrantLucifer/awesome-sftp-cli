//go:build darwin || linux

package terminalhandoff

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"reflect"
	"syscall"
	"testing"
	"time"

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

func TestGiveUnixForegroundReplaysContinueAfterHandoff(t *testing.T) {
	t.Parallel()

	var calls []string
	err := giveUnixForeground(
		11,
		712,
		func(fd, processGroup int) error {
			calls = append(calls, fmt.Sprintf("foreground:%d:%d", fd, processGroup))
			return nil
		},
		func(processGroup int, signal syscall.Signal) error {
			calls = append(calls, fmt.Sprintf("signal:%d:%d", processGroup, signal))
			return nil
		},
	)
	if err != nil {
		t.Fatalf("giveUnixForeground() error = %v", err)
	}
	want := []string{
		"foreground:11:712",
		fmt.Sprintf("signal:-712:%d", syscall.SIGCONT),
	}
	if !reflect.DeepEqual(calls, want) {
		t.Fatalf("calls = %q, want %q", calls, want)
	}
}

func TestGiveUnixForegroundAcceptsProcessGroupThatExitedBeforeHandoff(t *testing.T) {
	t.Parallel()

	err := giveUnixForeground(
		11,
		713,
		func(int, int) error { return syscall.ESRCH },
		func(processGroup int, signal syscall.Signal) error {
			if processGroup != -713 || signal != 0 {
				t.Fatalf("probe = kill(%d, %d), want kill(-713, 0)", processGroup, signal)
			}
			return syscall.ESRCH
		},
	)
	if err != nil {
		t.Fatalf("giveUnixForeground() fast-exit error = %v, want nil", err)
	}
}

func TestUnixPlatformPTYRoundTrip(t *testing.T) {
	if os.Getenv("AMSFTP_TERMINAL_HANDOFF_PTY_HELPER") == "1" {
		runUnixPlatformPTYHelper(t)
		return
	}

	master, slave := openTestPTY(t)
	defer master.Close()

	command := exec.Command(os.Args[0], "-test.run=^TestUnixPlatformPTYRoundTrip$") //nolint:gosec // exact current test binary and fixed test selector
	command.Env = append(os.Environ(), "AMSFTP_TERMINAL_HANDOFF_PTY_HELPER=1")
	command.Stdin = slave
	command.Stdout = slave
	command.Stderr = slave
	command.SysProcAttr = &syscall.SysProcAttr{Setsid: true, Setctty: true, Ctty: 0}
	var transcript bytes.Buffer
	if err := command.Start(); err != nil {
		_ = slave.Close()
		t.Fatalf("start PTY helper: %v", err)
	}
	// Drop the parent's slave descriptor once the helper has inherited it. When
	// the helper exits, closing its last slave descriptor causes the master read
	// to finish (EOF on Darwin, EIO on Linux), so copyDone is causally tied to
	// process cleanup instead of relying on a concurrent master Close to unblock.
	if err := slave.Close(); err != nil {
		_ = command.Process.Kill()
		_ = command.Wait()
		t.Fatalf("close parent PTY slave: %v", err)
	}
	copyDone := make(chan struct{})
	go func() {
		_, _ = io.Copy(&transcript, master)
		close(copyDone)
	}()
	if err := command.Wait(); err != nil {
		<-copyDone
		t.Fatalf("PTY helper error = %v\n%s", err, transcript.String())
	}
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

	tostop := native.termios
	tostop.Lflag |= unix.TOSTOP
	if err := setTermios(int(tty.Fd()), &tostop); err != nil {
		t.Fatalf("enable TOSTOP foreground-race fixture: %v", err)
	}
	defer func() { _ = setTermios(int(tty.Fd()), &native.termios) }()
	for iteration := range 20 {
		recorder := &callRecorder{}
		screen := newFakeScreen(recorder, NewSnapshot("native", Size{Columns: 80, Rows: 24}))
		controller, err := NewController(screen, platform)
		if err != nil {
			t.Fatalf("NewController() error = %v", err)
		}
		launcher, err := NewExecLauncher(
			os.Args[0],
			[]string{"-test.run=^TestExecLauncherHelperProcess$"},
			append(os.Environ(), "AMSFTP_EXEC_LAUNCHER_HELPER=write"),
			"",
		)
		if err != nil {
			t.Fatalf("NewExecLauncher() error = %v", err)
		}
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		result, runErr := controller.Run(ctx, launcher)
		cancel()
		if runErr != nil {
			t.Fatalf("native foreground-race iteration %d: Run() error = %v", iteration, runErr)
		}
		if result.Kind != ExitNormal {
			t.Fatalf("native foreground-race iteration %d: result = %#v, want normal", iteration, result)
		}
	}
}
