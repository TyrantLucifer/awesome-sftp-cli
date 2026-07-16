// Package commandrun implements explicit, user-authorized shell surfaces.
package commandrun

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"syscall"
	"time"
	"unicode/utf8"

	"github.com/TyrantLucifer/awesome-mac-sftp/internal/externalprocess"
)

const (
	MaxCommandBytes    = 32 * 1024
	DefaultStreamBytes = 1 * 1024 * 1024
	outputDrainWait    = 2 * time.Second
)

type LocalPlan struct {
	Executable string
	Args       []string
	Dir        string

	shell   externalprocess.ResolvedCommand
	dirInfo os.FileInfo
	command string
}

func ResolveLocalShell(explicit, environmentShell string) (externalprocess.ResolvedCommand, error) {
	selected := explicit
	source := "explicit shell"
	if selected == "" {
		selected = environmentShell
		source = "SHELL"
	}
	if selected == "" {
		selected = "/bin/sh"
		source = "fixed fallback"
	}
	if !filepath.IsAbs(selected) || filepath.Clean(selected) != selected {
		return externalprocess.ResolvedCommand{}, fmt.Errorf("resolve local shell from %s: path must be canonical and absolute", source)
	}
	resolved, err := externalprocess.ResolveCommand(externalprocess.Command{Executable: selected}, "")
	if err != nil {
		return externalprocess.ResolvedCommand{}, fmt.Errorf("resolve local shell from %s: %w", source, err)
	}
	return resolved, nil
}

func PlanLocalCommand(shell externalprocess.ResolvedCommand, cwd, userText string) (LocalPlan, error) {
	if err := shell.Revalidate(); err != nil {
		return LocalPlan{}, fmt.Errorf("plan local command shell: %w", err)
	}
	if userText == "" || len(userText) > MaxCommandBytes || !utf8.ValidString(userText) {
		return LocalPlan{}, fmt.Errorf("plan local command: text must be valid UTF-8 with length in [1,%d]", MaxCommandBytes)
	}
	for index := 0; index < len(userText); index++ {
		if userText[index] == 0 || userText[index] == '\r' || userText[index] == '\n' {
			return LocalPlan{}, fmt.Errorf("plan local command: NUL, CR, and LF are forbidden")
		}
	}
	if !filepath.IsAbs(cwd) || filepath.Clean(cwd) != cwd {
		return LocalPlan{}, fmt.Errorf("plan local command: cwd must be canonical and absolute")
	}
	dirInfo, err := os.Lstat(cwd)
	if err != nil {
		return LocalPlan{}, fmt.Errorf("plan local command cwd: %w", err)
	}
	if !dirInfo.IsDir() || dirInfo.Mode()&os.ModeSymlink != 0 {
		return LocalPlan{}, fmt.Errorf("plan local command: cwd must be a real directory")
	}
	return LocalPlan{
		Executable: shell.Executable,
		Args:       []string{"-c", userText},
		Dir:        cwd,
		shell:      shell,
		dirInfo:    dirInfo,
		command:    userText,
	}, nil
}

type StreamSnapshot struct {
	Data      []byte
	Discarded uint64
}

type Result struct {
	Stdout   StreamSnapshot
	Stderr   StreamSnapshot
	ExitCode int
	Signaled bool
	Signal   string
	Duration time.Duration
}

func RunLocalCommand(ctx context.Context, plan LocalPlan, streamBytes int) (Result, error) {
	if ctx == nil {
		return Result{}, fmt.Errorf("run local command: nil context")
	}
	if streamBytes <= 0 || streamBytes > DefaultStreamBytes {
		return Result{}, fmt.Errorf("run local command: stream budget must be in [1,%d]", DefaultStreamBytes)
	}
	if err := plan.shell.Revalidate(); err != nil {
		return Result{}, fmt.Errorf("run local command shell: %w", err)
	}
	currentDir, err := os.Lstat(plan.Dir)
	if err != nil || !os.SameFile(plan.dirInfo, currentDir) || !currentDir.IsDir() || currentDir.Mode()&os.ModeSymlink != 0 {
		return Result{}, fmt.Errorf("run local command: cwd identity changed")
	}
	if plan.Executable != plan.shell.Executable || len(plan.Args) != 2 || plan.Args[0] != "-c" || plan.Args[1] != plan.command {
		return Result{}, fmt.Errorf("run local command: invalid frozen argv")
	}

	// #nosec G204 -- Revalidate above verifies the frozen absolute shell identity, and argv is the fixed "-c", user-text plan.
	cmd := exec.CommandContext(ctx, plan.Executable, plan.Args...)
	cmd.Dir = plan.Dir
	cmd.Env = externalprocess.ScrubEnvironment(os.Environ())
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.Cancel = func() error {
		if cmd.Process == nil {
			return os.ErrProcessDone
		}
		err := syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
		if errors.Is(err, syscall.ESRCH) {
			return os.ErrProcessDone
		}
		return err
	}
	stdoutRing := newByteRing(streamBytes)
	stderrRing := newByteRing(streamBytes)
	cmd.Stdout = stdoutRing
	cmd.Stderr = stderrRing
	cmd.WaitDelay = outputDrainWait
	started := time.Now()
	if err := cmd.Start(); err != nil {
		return Result{}, fmt.Errorf("run local command start: %w", err)
	}
	waitErr := cmd.Wait()
	result := Result{Stdout: stdoutRing.Snapshot(), Stderr: stderrRing.Snapshot(), ExitCode: cmd.ProcessState.ExitCode(), Duration: time.Since(started)}
	if status, ok := cmd.ProcessState.Sys().(syscall.WaitStatus); ok && status.Signaled() {
		result.Signaled = true
		result.Signal = status.Signal().String()
	}
	if ctx.Err() != nil {
		return result, ctx.Err()
	}
	var exitError *exec.ExitError
	if waitErr != nil && !errors.As(waitErr, &exitError) {
		if errors.Is(waitErr, exec.ErrWaitDelay) && cmd.Process != nil {
			_ = syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
		}
		return result, fmt.Errorf("run local command wait: %w", waitErr)
	}
	return result, nil
}

type byteRing struct {
	buffer   []byte
	position int
	written  uint64
}

func newByteRing(capacity int) *byteRing { return &byteRing{buffer: make([]byte, capacity)} }

func (ring *byteRing) Write(data []byte) (int, error) {
	for _, value := range data {
		ring.buffer[ring.position] = value
		ring.position = (ring.position + 1) % len(ring.buffer)
		ring.written++
	}
	return len(data), nil
}

func (ring *byteRing) Snapshot() StreamSnapshot {
	retained := min(ring.written, uint64(len(ring.buffer)))
	result := make([]byte, retained)
	start := 0
	if ring.written >= uint64(len(ring.buffer)) {
		start = ring.position
	}
	for index := range result {
		result[index] = ring.buffer[(start+index)%len(ring.buffer)]
	}
	return StreamSnapshot{Data: result, Discarded: ring.written - retained}
}
