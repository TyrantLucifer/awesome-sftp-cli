//go:build darwin || linux

package terminalhandoff

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"
)

func TestExecLauncherClassifiesNormalAndNonZeroExit(t *testing.T) {
	t.Parallel()

	for _, test := range []struct {
		name     string
		exitCode string
		want     Result
	}{
		{name: "normal", exitCode: "0", want: Result{Kind: ExitNormal}},
		{name: "nonzero", exitCode: "23", want: Result{Kind: ExitNonZero, ExitCode: 23}},
	} {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			launcher, err := NewExecLauncher(
				os.Args[0],
				[]string{"-test.run=^TestExecLauncherHelperProcess$"},
				append(os.Environ(), "AMSFTP_EXEC_LAUNCHER_HELPER=exit", "AMSFTP_EXEC_LAUNCHER_EXIT_CODE="+test.exitCode),
				"",
			)
			if err != nil {
				t.Fatalf("NewExecLauncher() error = %v", err)
			}
			launcher.openTTY = func() (*os.File, error) { return os.OpenFile(os.DevNull, os.O_RDWR, 0) }

			process, err := launcher.Start(context.Background())
			if err != nil {
				t.Fatalf("Start() error = %v", err)
			}
			if process.ProcessGroup() <= 0 {
				t.Fatalf("ProcessGroup() = %d, want positive process group", process.ProcessGroup())
			}
			got, err := process.Wait()
			if err != nil {
				t.Fatalf("Wait() error = %v", err)
			}
			if got.Kind != test.want.Kind || got.ExitCode != test.want.ExitCode {
				t.Fatalf("Wait() result = %#v, want %#v", got, test.want)
			}
		})
	}
}

func TestExecLauncherClassifiesSignalExit(t *testing.T) {
	t.Parallel()

	launcher, err := NewExecLauncher(
		os.Args[0],
		[]string{"-test.run=^TestExecLauncherHelperProcess$"},
		append(os.Environ(), "AMSFTP_EXEC_LAUNCHER_HELPER=wait"),
		"",
	)
	if err != nil {
		t.Fatalf("NewExecLauncher() error = %v", err)
	}
	launcher.openTTY = func() (*os.File, error) { return os.OpenFile(os.DevNull, os.O_RDWR, 0) }
	process, err := launcher.Start(context.Background())
	if err != nil {
		t.Fatalf("Start() error = %v", err)
	}
	if err := syscall.Kill(-process.ProcessGroup(), syscall.SIGTERM); err != nil {
		t.Fatalf("signal process group: %v", err)
	}
	got, err := process.Wait()
	if err != nil {
		t.Fatalf("Wait() error = %v", err)
	}
	if got.Kind != ExitSignaled || got.Signal != "TERM" {
		t.Fatalf("Wait() result = %#v, want TERM signal exit", got)
	}
}

func TestExecLauncherClassifiesPTYLoss(t *testing.T) {
	t.Parallel()

	got, err := classifyProcessExit(&os.PathError{Op: "wait", Path: "/dev/tty", Err: syscall.EIO})
	if err != nil {
		t.Fatalf("classifyProcessExit() error = %v", err)
	}
	if got.Kind != ExitPTYLoss || !errors.Is(got.Err, syscall.EIO) {
		t.Fatalf("classifyProcessExit() = %#v, want PTY loss carrying EIO", got)
	}
}

func TestExecLauncherCancellationTerminatesTheProcessGroup(t *testing.T) {
	t.Parallel()

	readyPath := filepath.Join(t.TempDir(), "child.pid")
	ctx, cancel := context.WithCancel(context.Background())
	launcher, err := NewExecLauncher(
		os.Args[0],
		[]string{"-test.run=^TestExecLauncherHelperProcess$"},
		append(os.Environ(), "AMSFTP_EXEC_LAUNCHER_HELPER=child", "AMSFTP_EXEC_LAUNCHER_READY="+readyPath),
		"",
	)
	if err != nil {
		t.Fatalf("NewExecLauncher() error = %v", err)
	}
	launcher.openTTY = func() (*os.File, error) { return os.OpenFile(os.DevNull, os.O_RDWR, 0) }
	process, err := launcher.Start(ctx)
	if err != nil {
		t.Fatalf("Start() error = %v", err)
	}
	processGroup := process.ProcessGroup()
	t.Cleanup(func() { _ = syscall.Kill(-processGroup, syscall.SIGKILL) })

	deadline := time.Now().Add(5 * time.Second)
	for {
		if _, err := os.Stat(readyPath); err == nil {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("helper child did not become ready")
		}
		time.Sleep(5 * time.Millisecond)
	}
	cancel()
	got, err := process.Wait()
	if err != nil {
		t.Fatalf("Wait() error = %v", err)
	}
	if got.Kind != ExitSignaled {
		t.Fatalf("Wait() result = %#v, want signal exit after cancellation", got)
	}

	deadline = time.Now().Add(2 * time.Second)
	for {
		err := syscall.Kill(-processGroup, 0)
		if errors.Is(err, syscall.ESRCH) {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("process group %d still exists after cancellation: %v", processGroup, err)
		}
		time.Sleep(5 * time.Millisecond)
	}
}

func TestExecLauncherHelperProcess(t *testing.T) {
	switch os.Getenv("AMSFTP_EXEC_LAUNCHER_HELPER") {
	case "":
		return
	case "wait":
		time.Sleep(time.Hour)
		return
	case "child":
		child := exec.Command("/bin/sleep", "3600") //nolint:gosec // fixed native test helper
		if err := child.Start(); err != nil {
			os.Exit(126)
		}
		if err := os.WriteFile(os.Getenv("AMSFTP_EXEC_LAUNCHER_READY"), []byte(strconv.Itoa(child.Process.Pid)), 0o600); err != nil {
			_ = child.Process.Kill()
			os.Exit(126)
		}
		time.Sleep(time.Hour)
		return
	case "write":
		_, _ = os.Stdout.WriteString("exec-launcher-ready\n")
		return
	case "exit":
	default:
		os.Exit(125)
	}
	exitCode := os.Getenv("AMSFTP_EXEC_LAUNCHER_EXIT_CODE")
	if exitCode == "0" {
		return
	}
	if exitCode == "23" {
		os.Exit(23)
	}
	_, _ = os.Stderr.WriteString("unexpected helper exit code " + strings.TrimSpace(exitCode))
	os.Exit(125)
}
