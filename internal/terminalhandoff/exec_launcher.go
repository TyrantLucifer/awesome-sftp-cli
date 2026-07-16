//go:build darwin || linux

package terminalhandoff

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"syscall"

	"golang.org/x/sys/unix"
)

// ExecLauncher starts one absolute executable directly on the controlling
// terminal. Arguments are passed as argv entries and are never shell text.
type ExecLauncher struct {
	executable  string
	arguments   []string
	environment []string
	directory   string
	openTTY     func() (*os.File, error)
}

// NewExecLauncher constructs a direct-exec launcher. executable must be an
// absolute path resolved by the caller before terminal suspension begins.
func NewExecLauncher(executable string, arguments, environment []string, directory string) (*ExecLauncher, error) {
	if executable == "" {
		return nil, errors.New("create terminal exec launcher: executable is empty")
	}
	if !filepath.IsAbs(executable) {
		return nil, fmt.Errorf("create terminal exec launcher: executable %q is not absolute", executable)
	}
	return &ExecLauncher{
		executable:  executable,
		arguments:   append([]string(nil), arguments...),
		environment: append([]string(nil), environment...),
		directory:   directory,
		openTTY: func() (*os.File, error) {
			return os.OpenFile("/dev/tty", os.O_RDWR, 0)
		},
	}, nil
}

func (launcher *ExecLauncher) Start(ctx context.Context) (Process, error) {
	if ctx == nil {
		return nil, errors.New("start terminal exec process: context is nil")
	}
	select {
	case <-ctx.Done():
		return nil, fmt.Errorf("start terminal exec process: %w", ctx.Err())
	default:
	}
	tty, err := launcher.openTTY()
	if err != nil {
		return nil, fmt.Errorf("start terminal exec process: open controlling TTY: %w", err)
	}
	defer tty.Close()

	command := exec.Command(launcher.executable, launcher.arguments...)
	command.Env = append([]string(nil), launcher.environment...)
	command.Dir = launcher.directory
	command.Stdin = tty
	command.Stdout = tty
	command.Stderr = tty
	command.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	if err := command.Start(); err != nil {
		return nil, fmt.Errorf("start terminal exec process: %w", err)
	}
	process := &execProcess{
		command:      command,
		processGroup: command.Process.Pid,
		waitDone:     make(chan struct{}),
	}
	go process.cancelProcessGroup(ctx)
	return process, nil
}

type execProcess struct {
	command      *exec.Cmd
	processGroup int
	waitOnce     sync.Once
	waitDone     chan struct{}
	result       Result
	waitErr      error
}

func (process *execProcess) ProcessGroup() int {
	return process.processGroup
}

func (process *execProcess) Wait() (Result, error) {
	process.waitOnce.Do(func() {
		process.result, process.waitErr = classifyProcessExit(process.command.Wait())
		close(process.waitDone)
	})
	<-process.waitDone
	return process.result, process.waitErr
}

func (process *execProcess) cancelProcessGroup(ctx context.Context) {
	select {
	case <-ctx.Done():
		_ = unix.Kill(-process.processGroup, unix.SIGKILL)
	case <-process.waitDone:
	}
}

func classifyProcessExit(err error) (Result, error) {
	if err == nil {
		return Result{Kind: ExitNormal}, nil
	}
	if errors.Is(err, syscall.EIO) {
		return Result{Kind: ExitPTYLoss, Err: err}, nil
	}
	var exitErr *exec.ExitError
	if !errors.As(err, &exitErr) {
		return Result{}, err
	}
	status, ok := exitErr.Sys().(syscall.WaitStatus)
	if !ok {
		return Result{}, fmt.Errorf("classify terminal exec process exit: %w", err)
	}
	if status.Signaled() {
		signal := strings.TrimPrefix(unix.SignalName(status.Signal()), "SIG")
		return Result{Kind: ExitSignaled, Signal: signal}, nil
	}
	return Result{Kind: ExitNonZero, ExitCode: status.ExitStatus()}, nil
}
