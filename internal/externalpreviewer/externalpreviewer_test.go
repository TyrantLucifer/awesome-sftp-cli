//go:build darwin || linux

package externalpreviewer

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"runtime"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/TyrantLucifer/awesome-mac-sftp/internal/externalprocess"
)

func TestRunnerUsesFirstMatchingStructuredRuleAndCanonicalFinalArgument(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	capture := filepath.Join(dir, "capture")
	runner := newTestRunner(t, []Rule{
		{
			Name:          "first",
			Match:         Match{MediaTypes: []string{"image/*"}},
			Command:       helperCommand(t, "capture", capture, "first;literal"),
			Timeout:       5 * time.Second,
			MaxInputBytes: 1024,
		},
		{
			Name:          "second",
			Match:         Match{Extensions: []string{".PNG"}},
			Command:       helperCommand(t, "capture", capture, "second"),
			Timeout:       5 * time.Second,
			MaxInputBytes: 1024,
		},
	})

	materialization := writeMaterialization(t, dir, "asset with spaces;$(ignored).png", []byte("png"))
	alias := filepath.Join(dir, "alias.png")
	if err := os.Symlink(materialization, alias); err != nil {
		t.Fatal(err)
	}
	result := runner.Run(context.Background(), Request{
		Path:                "/remote/ASSET.PNG",
		MediaType:           "image/png; charset=binary",
		Complete:            true,
		MaterializationPath: alias,
	})
	if result.Status != StatusSucceeded || result.Rule != "first" {
		t.Fatalf("result = %+v", result)
	}

	lines := strings.Split(strings.TrimSpace(string(mustReadFile(t, capture))), "\n")
	want := []string{"first;literal", materialization}
	if !reflect.DeepEqual(lines, want) {
		t.Fatalf("captured argv = %#v, want %#v", lines, want)
	}
	if !filepath.IsAbs(lines[len(lines)-1]) {
		t.Fatalf("final materialization argument is not absolute: %q", lines[len(lines)-1])
	}
}

func TestRunnerFallsBackWhenNoRuleMatchesAndRejectsInvalidRules(t *testing.T) {
	t.Parallel()

	command := helperCommand(t, "exit", "0")
	for name, rules := range map[string][]Rule{
		"empty name":             {{Match: Match{Extensions: []string{".png"}}, Command: command, Timeout: time.Second, MaxInputBytes: 1}},
		"empty matcher":          {{Name: "bad", Command: command, Timeout: time.Second, MaxInputBytes: 1}},
		"invalid extension":      {{Name: "bad", Match: Match{Extensions: []string{"png"}}, Command: command, Timeout: time.Second, MaxInputBytes: 1}},
		"invalid media wildcard": {{Name: "bad", Match: Match{MediaTypes: []string{"*/png"}}, Command: command, Timeout: time.Second, MaxInputBytes: 1}},
		"missing timeout":        {{Name: "bad", Match: Match{Extensions: []string{".png"}}, Command: command, MaxInputBytes: 1}},
		"missing budget":         {{Name: "bad", Match: Match{Extensions: []string{".png"}}, Command: command, Timeout: time.Second}},
	} {
		name, rules := name, rules
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			if _, err := New(rules, nil); err == nil {
				t.Fatal("invalid rules unexpectedly accepted")
			}
		})
	}

	runner := newTestRunner(t, []Rule{{
		Name: "images", Match: Match{Extensions: []string{".png"}}, Command: command,
		Timeout: 5 * time.Second, MaxInputBytes: 1,
	}})
	result := runner.Run(context.Background(), Request{Path: "notes.txt"})
	if result.Status != StatusNoMatch || result.Rule != "" || result.Diagnostic != "" {
		t.Fatalf("no-match result = %+v", result)
	}
}

func TestRunnerEnforcesCompleteAndActualInputSizeBeforeStarting(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	capture := filepath.Join(dir, "capture")
	runner := newTestRunner(t, []Rule{{
		Name: "image", Match: Match{Extensions: []string{".png"}},
		Command: helperCommand(t, "capture", capture), Timeout: 5 * time.Second,
		MaxInputBytes: 3, RequireComplete: true,
	}})
	materialization := writeMaterialization(t, dir, "asset.png", []byte("four"))

	incomplete := runner.Run(context.Background(), Request{Path: "asset.png", MaterializationPath: materialization})
	if incomplete.Status != StatusRejected || incomplete.Code != CodeIncompleteInput {
		t.Fatalf("incomplete result = %+v", incomplete)
	}
	tooLarge := runner.Run(context.Background(), Request{Path: "asset.png", Complete: true, MaterializationPath: materialization})
	if tooLarge.Status != StatusRejected || tooLarge.Code != CodeInputTooLarge {
		t.Fatalf("oversized result = %+v", tooLarge)
	}
	if _, err := os.Stat(capture); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("previewer started despite rejected input: %v", err)
	}
}

func TestRunnerRevalidatesFrozenExecutableAndIsolatesStartFailure(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	materialization := writeMaterialization(t, dir, "asset.bin", []byte("x"))
	executable := copyExecutable(t, dir, "previewer")
	command, err := externalprocess.ResolveCommand(externalprocess.Command{
		Executable: executable,
		Args:       []string{"-test.run=^TestExternalPreviewerHelperProcess$", "--", helperMarker, "exit", "0"},
	}, "")
	if err != nil {
		t.Fatal(err)
	}
	runner := newTestRunner(t, []Rule{{
		Name: "all", Match: Match{Extensions: []string{".bin"}}, Command: command,
		Timeout: 5 * time.Second, MaxInputBytes: 10,
	}})
	if err := os.Remove(executable); err != nil {
		t.Fatal(err)
	}
	copyExecutable(t, dir, "previewer")
	result := runner.Run(context.Background(), Request{Path: "asset.bin", Complete: true, MaterializationPath: materialization})
	if result.Status != StatusRejected || result.Code != CodeExecutableChanged {
		t.Fatalf("replacement result = %+v", result)
	}

	invalid := filepath.Join(dir, "invalid-format")
	// #nosec G306 -- the invalid fixture must be executable to exercise exec-format failure.
	if err := os.WriteFile(invalid, []byte("not an executable format"), 0o700); err != nil {
		t.Fatal(err)
	}
	invalidCommand, err := externalprocess.ResolveCommand(externalprocess.Command{Executable: invalid}, "")
	if err != nil {
		t.Fatal(err)
	}
	startFailure := newTestRunner(t, []Rule{{
		Name: "all", Match: Match{Extensions: []string{".bin"}}, Command: invalidCommand,
		Timeout: 5 * time.Second, MaxInputBytes: 10,
	}}).Run(context.Background(), Request{Path: "asset.bin", Complete: true, MaterializationPath: materialization})
	if startFailure.Status != StatusStartFailed || startFailure.Code != CodeStartFailed {
		t.Fatalf("start-failure result = %+v", startFailure)
	}
}

func TestRunnerBoundsRedactsDiagnosticsAndDiscardsStdout(t *testing.T) {
	t.Parallel()

	const secret = "preview-secret-canary"
	dir := t.TempDir()
	materialization := writeMaterialization(t, dir, "asset.bin", []byte("body-secret-canary"))
	runner, err := New([]Rule{{
		Name: "diagnostic", Match: Match{Extensions: []string{".bin"}},
		Command: helperCommand(t, "diagnostic", secret), Timeout: 5 * time.Second,
		MaxInputBytes: 1024, Redact: []string{secret},
	}}, nil)
	if err != nil {
		t.Fatal(err)
	}
	result := runner.Run(context.Background(), Request{Path: "asset.bin", Complete: true, MaterializationPath: materialization})
	if result.Status != StatusNonZero || result.Code != CodeNonZero {
		t.Fatalf("result = %+v", result)
	}
	if len(result.Diagnostic) > MaxDiagnosticBytes || strings.ContainsAny(result.Diagnostic, "\r\n") || strings.Contains(result.Diagnostic, "\u2028") {
		t.Fatalf("diagnostic is not bounded and single-line: len=%d %q", len(result.Diagnostic), result.Diagnostic)
	}
	if strings.Contains(result.Diagnostic, secret) || !strings.Contains(result.Diagnostic, "[redacted]") {
		t.Fatalf("diagnostic was not redacted: %q", result.Diagnostic)
	}
	if strings.Contains(result.Diagnostic, "stdout-must-be-discarded") || strings.Contains(result.Diagnostic, "body-secret-canary") || strings.Contains(result.Diagnostic, materialization) {
		t.Fatalf("result retained output, body, or materialization path: %+v", result)
	}
}

func TestRunnerIsolatesNonZeroAndSignalExit(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	materialization := writeMaterialization(t, dir, "asset.bin", []byte("x"))
	for name, command := range map[string]externalprocess.ResolvedCommand{
		"nonzero": helperCommand(t, "exit", "23"),
		"signal":  helperCommand(t, "signal"),
	} {
		name, command := name, command
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			result := newTestRunner(t, []Rule{{
				Name: "all", Match: Match{Extensions: []string{".bin"}}, Command: command,
				Timeout: 5 * time.Second, MaxInputBytes: 10,
			}}).Run(context.Background(), Request{Path: "asset.bin", Complete: true, MaterializationPath: materialization})
			if name == "nonzero" && (result.Status != StatusNonZero || result.ExitCode != 23) {
				t.Fatalf("nonzero result = %+v", result)
			}
			if name == "signal" && (result.Status != StatusSignaled || result.Signal == "") {
				t.Fatalf("signal result = %+v", result)
			}
		})
	}
}

func TestRunnerTimeoutAndCancellationTerminateProcessGroup(t *testing.T) {
	for _, name := range []string{"timeout", "cancel"} {
		name := name
		t.Run(name, func(t *testing.T) {
			dir := t.TempDir()
			pidFile := filepath.Join(dir, "descendant.pid")
			materialization := writeMaterialization(t, dir, "asset.bin", []byte("x"))
			rule := Rule{
				Name: "hang", Match: Match{Extensions: []string{".bin"}},
				Command: helperCommand(t, "spawn-and-hang", pidFile), Timeout: time.Second,
				MaxInputBytes: 10,
			}
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			runner := newTestRunner(t, []Rule{rule})
			request := Request{Path: "asset.bin", Complete: true, MaterializationPath: materialization}
			var result Result
			if name == "cancel" {
				rule.Timeout = 5 * time.Second
				runner = newTestRunner(t, []Rule{rule})
				done := make(chan Result, 1)
				go func() { done <- runner.Run(ctx, request) }()
				_ = mustReadFile(t, pidFile)
				cancel()
				result = <-done
			} else {
				result = runner.Run(ctx, request)
			}
			if name == "timeout" && (result.Status != StatusTimedOut || result.Code != CodeTimeout) {
				t.Fatalf("timeout result = %+v", result)
			}
			if name == "cancel" && (result.Status != StatusCanceled || result.Code != CodeCanceled) {
				t.Fatalf("cancel result = %+v", result)
			}
			pid, err := strconv.Atoi(strings.TrimSpace(string(mustReadFile(t, pidFile))))
			if err != nil {
				t.Fatal(err)
			}
			waitForProcessExit(t, pid)
		})
	}
}

const helperMarker = "externalpreviewer-helper"

func TestExternalPreviewerHelperProcess(t *testing.T) {
	separator := -1
	for index, value := range os.Args {
		if value == "--" {
			separator = index
			break
		}
	}
	if separator < 0 || separator+2 >= len(os.Args) || os.Args[separator+1] != helperMarker {
		return
	}
	arguments := os.Args[separator+2:]
	mode := arguments[0]
	switch mode {
	case "capture":
		capture := arguments[1]
		// #nosec G703 -- capture is supplied only by this test binary's isolated helper protocol.
		if err := os.WriteFile(capture, []byte(strings.Join(arguments[2:], "\n")+"\n"), 0o600); err != nil {
			os.Exit(90)
		}
	case "diagnostic":
		secret := arguments[1]
		_, _ = fmt.Fprint(os.Stdout, strings.Repeat("stdout-must-be-discarded\n", 1024))
		_, _ = fmt.Fprint(os.Stderr, strings.Repeat("diagnostic "+secret+"\r\n\u2028", 1024))
		os.Exit(17)
	case "ansi-diagnostic":
		secret := arguments[1]
		_, _ = fmt.Fprint(os.Stderr, "\x1b[2Jdiagnostic "+secret+"\r\n")
		os.Exit(17)
	case "exit":
		code, _ := strconv.Atoi(arguments[1])
		os.Exit(code)
	case "signal":
		_ = syscall.Kill(os.Getpid(), syscall.SIGKILL)
		time.Sleep(time.Second)
	case "spawn-and-hang":
		// #nosec G204 G702 -- this direct exec always launches the current test binary with fixed arguments.
		child := exec.Command(os.Args[0], "-test.run=^TestExternalPreviewerHelperProcess$", "--", helperMarker, "hang")
		if err := child.Start(); err != nil {
			os.Exit(91)
		}
		// #nosec G703 -- the PID capture path is supplied only by the parent test helper.
		if err := os.WriteFile(arguments[1], []byte(strconv.Itoa(child.Process.Pid)), 0o600); err != nil {
			os.Exit(92)
		}
		for {
			time.Sleep(time.Hour)
		}
	case "hang":
		for {
			time.Sleep(time.Hour)
		}
	default:
		os.Exit(93)
	}
	os.Exit(0)
}

func newTestRunner(t *testing.T, rules []Rule) *Runner {
	t.Helper()
	runner, err := New(rules, os.Environ())
	if err != nil {
		t.Fatal(err)
	}
	return runner
}

func helperCommand(t *testing.T, arguments ...string) externalprocess.ResolvedCommand {
	t.Helper()
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	args := []string{"-test.run=^TestExternalPreviewerHelperProcess$", "--", helperMarker}
	args = append(args, arguments...)
	command, err := externalprocess.ResolveCommand(externalprocess.Command{Executable: executable, Args: args}, "")
	if err != nil {
		t.Fatal(err)
	}
	return command
}

func copyExecutable(t *testing.T, dir, name string) string {
	t.Helper()
	source, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	destination := filepath.Join(dir, name)
	// #nosec G304 -- source is os.Executable for this test process.
	data, err := os.ReadFile(source)
	if err != nil {
		t.Fatal(err)
	}
	// #nosec G306 G703 -- destination is test-owned and the copied binary must remain executable.
	if err := os.WriteFile(destination, data, 0o700); err != nil {
		t.Fatal(err)
	}
	return destination
}

func writeMaterialization(t *testing.T, dir, name string, content []byte) string {
	t.Helper()
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, content, 0o600); err != nil {
		t.Fatal(err)
	}
	canonical, err := filepath.EvalSymlinks(path)
	if err != nil {
		t.Fatal(err)
	}
	return canonical
}

func mustReadFile(t *testing.T, path string) []byte {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for {
		// #nosec G304 -- path is a test-owned helper capture path.
		value, err := os.ReadFile(path)
		if err == nil {
			return value
		}
		if !errors.Is(err, os.ErrNotExist) || time.Now().After(deadline) {
			t.Fatal(err)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func waitForProcessExit(t *testing.T, pid int) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for {
		err := syscall.Kill(pid, 0)
		if errors.Is(err, syscall.ESRCH) {
			return
		}
		if err != nil && !errors.Is(err, syscall.EPERM) {
			t.Fatalf("probe descendant %d: %v", pid, err)
		}
		if time.Now().After(deadline) {
			t.Fatalf("descendant %d remained alive after process-group cleanup on %s", pid, runtime.GOOS)
		}
		time.Sleep(10 * time.Millisecond)
	}
}
