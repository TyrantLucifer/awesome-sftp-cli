package commandrun

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/TyrantLucifer/awesome-mac-sftp/internal/externalprocess"
)

func TestResolveLocalShellUsesAbsolutePrecedenceAndFailsClosed(t *testing.T) {
	dir := t.TempDir()
	explicit := writeShell(t, dir, "explicit")
	environment := writeShell(t, dir, "environment")
	resolved, err := ResolveLocalShell(explicit, environment)
	if err != nil {
		t.Fatal(err)
	}
	wantExplicit, err := filepath.EvalSymlinks(explicit)
	if err != nil {
		t.Fatal(err)
	}
	if resolved.Executable != wantExplicit {
		t.Fatalf("shell = %q, want %q", resolved.Executable, wantExplicit)
	}
	if _, err := ResolveLocalShell("relative", environment); err == nil {
		t.Fatal("invalid explicit shell fell through")
	}
	if _, err := ResolveLocalShell("", "relative"); err == nil {
		t.Fatal("invalid SHELL fell through")
	}
}

func TestPlanLocalCommandKeepsCanonicalCWDOutOfExactArgv(t *testing.T) {
	dir := t.TempDir()
	shell := writeShell(t, dir, "shell with spaces")
	resolved, err := externalprocess.ResolveCommand(externalprocess.Command{Executable: shell}, "")
	if err != nil {
		t.Fatal(err)
	}
	cwd := filepath.Join(dir, "cwd with spaces")
	if err := os.Mkdir(cwd, 0o700); err != nil {
		t.Fatal(err)
	}
	plan, err := PlanLocalCommand(resolved, cwd, `printf '%s' "$PWD"`)
	if err != nil {
		t.Fatal(err)
	}
	if plan.Dir != cwd || len(plan.Args) != 2 || plan.Args[0] != "-c" || plan.Args[1] != `printf '%s' "$PWD"` {
		t.Fatalf("plan = %#v", plan)
	}
	if strings.Contains(plan.Args[1], cwd) {
		t.Fatal("cwd was interpolated into command text")
	}
}

func TestPlanLocalCommandRejectsUnsafeOrOversizedTextAndCWD(t *testing.T) {
	dir := t.TempDir()
	resolved, err := externalprocess.ResolveCommand(externalprocess.Command{Executable: writeShell(t, dir, "shell")}, "")
	if err != nil {
		t.Fatal(err)
	}
	for _, text := range []string{"", "echo x\nwhoami", "echo x\rwhoami", "echo\x00x", string([]byte{0xff}), strings.Repeat("x", MaxCommandBytes+1)} {
		if _, err := PlanLocalCommand(resolved, dir, text); err == nil {
			t.Fatalf("unsafe command %q succeeded", text)
		}
	}
	link := filepath.Join(t.TempDir(), "cwd-link")
	if err := os.Symlink(dir, link); err != nil {
		t.Fatal(err)
	}
	if _, err := PlanLocalCommand(resolved, link, "true"); err == nil {
		t.Fatal("symlink cwd succeeded")
	}
}

func TestRunLocalCommandDrainsBothStreamsAndBoundsRetention(t *testing.T) {
	plan := testPlan(t, `i=0; while [ $i -lt 200 ]; do printf 1234567890; printf abcdefghij >&2; i=$((i+1)); done`)
	result, err := RunLocalCommand(context.Background(), plan, 64)
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Stdout.Data) != 64 || len(result.Stderr.Data) != 64 || result.Stdout.Discarded == 0 || result.Stderr.Discarded == 0 {
		t.Fatalf("result = %#v", result)
	}
	if result.ExitCode != 0 || result.Signaled {
		t.Fatalf("status = %#v", result)
	}
}

func TestRunLocalCommandReportsNonzeroAndCancellation(t *testing.T) {
	nonzero, err := RunLocalCommand(context.Background(), testPlan(t, "exit 7"), DefaultStreamBytes)
	if err != nil || nonzero.ExitCode != 7 || nonzero.Signaled {
		t.Fatalf("nonzero=%#v err=%v", nonzero, err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	canceled, err := RunLocalCommand(ctx, testPlan(t, "while :; do :; done"), DefaultStreamBytes)
	if !errors.Is(err, context.DeadlineExceeded) || !canceled.Signaled {
		t.Fatalf("canceled=%#v err=%v", canceled, err)
	}
}

func testPlan(t *testing.T, text string) LocalPlan {
	t.Helper()
	resolved, err := ResolveLocalShell("", "")
	if err != nil {
		t.Fatal(err)
	}
	plan, err := PlanLocalCommand(resolved, t.TempDir(), text)
	if err != nil {
		t.Fatal(err)
	}
	return plan
}

func writeShell(t *testing.T, directory, name string) string {
	t.Helper()
	path := filepath.Join(directory, name)
	if err := os.WriteFile(path, []byte("#!/bin/sh\nexec /bin/sh \"$@\"\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	return path
}
