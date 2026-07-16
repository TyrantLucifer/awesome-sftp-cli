package externalprocess

import (
	"context"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"testing"
)

func TestParseEnvironmentCommandQuotesBackslashesAndEmptyArguments(t *testing.T) {
	t.Parallel()

	command, err := ParseEnvironmentCommand(`vim -f "two words" 'single quoted' escaped\ space ""`)
	if err != nil {
		t.Fatalf("ParseEnvironmentCommand: %v", err)
	}
	want := Command{
		Executable: "vim",
		Args:       []string{"-f", "two words", "single quoted", "escaped space", ""},
	}
	if !reflect.DeepEqual(command, want) {
		t.Fatalf("command = %#v, want %#v", command, want)
	}
}

func TestParseEnvironmentCommandRejectsShellSyntaxAndMalformedInput(t *testing.T) {
	t.Parallel()

	tests := map[string]string{
		"empty":            " \t ",
		"empty executable": `"" -f`,
		"dollar":           `vim "$HOME/file"`,
		"substitution":     "vim `touch /tmp/pwned`",
		"glob":             "vim *.go",
		"question glob":    "vim file?.go",
		"bracket glob":     "vim file[0]",
		"pipe":             "vim x | tee y",
		"and":              "vim x && echo y",
		"semicolon":        "vim x; echo y",
		"redirect input":   "vim < x",
		"redirect output":  "vim > x",
		"newline":          "vim x\necho y",
		"carriage return":  "vim x\recho y",
		"nul":              "vim\x00x",
		"control":          "vim\x1fx",
		"unclosed single":  "vim 'x",
		"unclosed double":  `vim "x`,
		"unclosed escape":  "vim x\\",
		"escaped operator": `vim \$HOME`,
		"quoted operator":  `vim '$HOME'`,
		"invalid UTF-8":    string([]byte{'v', 'i', 'm', ' ', 0xff}),
	}
	for name, input := range tests {
		name, input := name, input
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			if _, err := ParseEnvironmentCommand(input); err == nil {
				t.Fatalf("ParseEnvironmentCommand(%q) unexpectedly succeeded", input)
			}
		})
	}
}

func TestParseEnvironmentCommandEnforcesBudgets(t *testing.T) {
	t.Parallel()

	if _, err := ParseEnvironmentCommand("vim " + strings.Repeat("x", MaxArgumentBytes+1)); err == nil {
		t.Fatal("oversized argument unexpectedly succeeded")
	}

	tooMany := "vim " + strings.Repeat("x ", MaxArguments+1)
	if _, err := ParseEnvironmentCommand(tooMany); err == nil {
		t.Fatal("too many arguments unexpectedly succeeded")
	}

	if _, err := ParseEnvironmentCommand("vim " + strings.Repeat("x ", MaxCommandBytes)); err == nil {
		t.Fatal("oversized command unexpectedly succeeded")
	}
}

func TestResolveEditorUsesStrictPrecedenceAndFailsClosed(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	amsftp := writeExecutable(t, dir, "amsftp-editor")
	visual := writeExecutable(t, dir, "visual-editor")
	editor := writeExecutable(t, dir, "editor")

	resolved, err := ResolveEditor(&Command{Executable: filepath.Base(amsftp), Args: []string{"--wait", "$literal"}}, map[string]string{
		"VISUAL": filepath.Base(visual),
		"EDITOR": filepath.Base(editor),
	}, dir)
	if err != nil {
		t.Fatalf("ResolveEditor: %v", err)
	}
	if resolved.Executable != amsftp || !reflect.DeepEqual(resolved.Args, []string{"--wait", "$literal"}) {
		t.Fatalf("resolved = %#v", resolved)
	}

	_, err = ResolveEditor(nil, map[string]string{
		"VISUAL": "broken | command",
		"EDITOR": filepath.Base(editor),
	}, dir)
	if err == nil {
		t.Fatal("invalid higher-priority editor unexpectedly fell back")
	}

	_, err = ResolveEditor(&Command{Executable: "missing"}, map[string]string{
		"VISUAL": filepath.Base(visual),
	}, dir)
	if err == nil {
		t.Fatal("invalid explicit editor unexpectedly fell back")
	}
}

func TestResolveEditorSkipsEmptyValuesAndUsesDefaults(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	visual := writeExecutable(t, dir, "visual-editor")
	resolved, err := ResolveEditor(nil, map[string]string{
		"VISUAL": filepath.Base(visual),
		"EDITOR": "missing",
	}, dir)
	if err != nil {
		t.Fatalf("ResolveEditor: %v", err)
	}
	if resolved.Executable != visual {
		t.Fatalf("executable = %q, want %q", resolved.Executable, visual)
	}

	dir = t.TempDir()
	vim := writeExecutable(t, dir, "vim")
	resolved, err = ResolveEditor(nil, nil, dir)
	if err != nil {
		t.Fatalf("ResolveEditor defaults: %v", err)
	}
	if resolved.Executable != vim {
		t.Fatalf("executable = %q, want %q", resolved.Executable, vim)
	}
}

func TestResolveCommandFreezesAbsoluteCanonicalExecutable(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	real := writeExecutable(t, dir, "real-editor")
	alias := filepath.Join(dir, "alias-editor")
	if err := os.Symlink(real, alias); err != nil {
		t.Fatal(err)
	}

	resolved, err := ResolveCommand(Command{Executable: "alias-editor", Args: []string{"--wait"}}, dir)
	if err != nil {
		t.Fatalf("ResolveCommand: %v", err)
	}
	if resolved.Executable != real {
		t.Fatalf("executable = %q, want canonical %q", resolved.Executable, real)
	}
	if !filepath.IsAbs(resolved.Executable) {
		t.Fatalf("executable is not absolute: %q", resolved.Executable)
	}

	if _, err := ResolveCommand(Command{Executable: "relative/editor"}, dir); err == nil {
		t.Fatal("relative executable containing slash unexpectedly succeeded")
	}
}

func TestResolveCommandPATHOrderAndControlRejection(t *testing.T) {
	t.Parallel()

	firstDir := t.TempDir()
	secondDir := t.TempDir()
	first := writeExecutable(t, firstDir, "editor")
	writeExecutable(t, secondDir, "editor")

	resolved, err := ResolveCommand(Command{Executable: "editor"}, strings.Join([]string{firstDir, secondDir}, string(os.PathListSeparator)))
	if err != nil {
		t.Fatalf("ResolveCommand: %v", err)
	}
	if resolved.Executable != first {
		t.Fatalf("executable = %q, want first PATH candidate %q", resolved.Executable, first)
	}

	badDir := filepath.Join(t.TempDir(), "bad\npath")
	if err := os.Mkdir(badDir, 0o700); err != nil {
		t.Fatal(err)
	}
	writeExecutable(t, badDir, "bad-editor")
	if _, err := ResolveCommand(Command{Executable: "bad-editor"}, badDir); err == nil {
		t.Fatal("PATH candidate containing a control byte unexpectedly succeeded")
	}
}

func TestResolvedCommandRejectsSpecialNonExecutableAndReplacement(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	nonExecutable := filepath.Join(dir, "not-executable")
	if err := os.WriteFile(nonExecutable, []byte("data"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := ResolveCommand(Command{Executable: nonExecutable}, dir); err == nil {
		t.Fatal("non-executable file unexpectedly succeeded")
	}
	if _, err := ResolveCommand(Command{Executable: dir}, dir); err == nil {
		t.Fatal("directory unexpectedly succeeded")
	}

	executable := writeExecutable(t, dir, "editor")
	resolved, err := ResolveCommand(Command{Executable: executable}, dir)
	if err != nil {
		t.Fatal(err)
	}
	holdExecutableInode(t, executable)
	if err := os.Remove(executable); err != nil {
		t.Fatal(err)
	}
	writeExecutable(t, dir, "editor")
	if err := resolved.Revalidate(); err == nil {
		t.Fatal("replaced executable unexpectedly revalidated")
	}
}

func TestDefaultOpenerPlatformTable(t *testing.T) {
	t.Parallel()

	tests := []struct {
		goos string
		want string
		ok   bool
	}{
		{goos: "darwin", want: "/usr/bin/open", ok: true},
		{goos: "linux", want: "/usr/bin/xdg-open", ok: true},
		{goos: "windows", ok: false},
		{goos: "freebsd", ok: false},
	}
	for _, tt := range tests {
		t.Run(tt.goos, func(t *testing.T) {
			got, ok := DefaultOpenerPath(tt.goos)
			if got != tt.want || ok != tt.ok {
				t.Fatalf("DefaultOpenerPath(%q) = %q, %v; want %q, %v", tt.goos, got, ok, tt.want, tt.ok)
			}
		})
	}
}

func TestResolveOpenerPrefersExplicitAndDoesNotPATHDiscoverDefault(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	custom := writeExecutable(t, dir, "custom-opener")
	resolved, err := ResolveOpener(&Command{Executable: "custom-opener", Args: []string{"--new"}}, "unsupported", dir)
	if err != nil {
		t.Fatalf("ResolveOpener explicit: %v", err)
	}
	if resolved.Executable != custom {
		t.Fatalf("executable = %q, want %q", resolved.Executable, custom)
	}

	writeExecutable(t, dir, "xdg-open")
	if _, err := ResolveOpener(nil, "unsupported", dir); err == nil {
		t.Fatal("unsupported platform unexpectedly PATH-discovered an opener")
	}
}

func TestPlanAppendsCanonicalFileAsFinalSeparateArgument(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	executable := writeExecutable(t, dir, "editor")
	resolved, err := ResolveCommand(Command{Executable: executable, Args: []string{"--wait", "literal;argument"}}, dir)
	if err != nil {
		t.Fatal(err)
	}

	fileDir := filepath.Join(dir, "directory with spaces")
	if err := os.Mkdir(fileDir, 0o700); err != nil {
		t.Fatal(err)
	}
	file := filepath.Join(fileDir, "-leading dash 'quoted'.txt")
	if err := os.WriteFile(file, []byte("content"), 0o600); err != nil {
		t.Fatal(err)
	}
	file, err = filepath.EvalSymlinks(file)
	if err != nil {
		t.Fatal(err)
	}

	plan, err := NewPlan(resolved, file, []string{
		"PATH=/usr/bin:/bin",
		"LANG=en_US.UTF-8",
		"DISPLAY=:0",
		"AMSFTP_EDITOR=secret",
		"SSH_ASKPASS=/tmp/steal",
		"LD_PRELOAD=/tmp/inject.so",
		"MALFORMED",
		"TERM=bad\nvalue",
	})
	if err != nil {
		t.Fatalf("NewPlan: %v", err)
	}
	wantArgs := []string{"--wait", "literal;argument", file}
	if !reflect.DeepEqual(plan.Args, wantArgs) {
		t.Fatalf("args = %#v, want %#v", plan.Args, wantArgs)
	}
	wantEnv := []string{"PATH=/usr/bin:/bin", "LANG=en_US.UTF-8", "DISPLAY=:0"}
	if !reflect.DeepEqual(plan.Env, wantEnv) {
		t.Fatalf("environment = %#v, want %#v", plan.Env, wantEnv)
	}
	plan.Env = append(plan.Env, "LD_PRELOAD=/tmp/late-injection.so")

	cmd, err := plan.CommandContext(context.Background())
	if err != nil {
		t.Fatalf("CommandContext: %v", err)
	}
	if cmd.Path != executable || !reflect.DeepEqual(cmd.Args, append([]string{executable}, wantArgs...)) {
		t.Fatalf("exec.Cmd path/args = %q, %#v", cmd.Path, cmd.Args)
	}
	if !reflect.DeepEqual(cmd.Env, wantEnv) {
		t.Fatalf("exec.Cmd environment = %#v, want %#v", cmd.Env, wantEnv)
	}
}

func TestPlanRejectsNonRegularMaterializationAndExecutableReplacement(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	executable := writeExecutable(t, dir, "editor")
	resolved, err := ResolveCommand(Command{Executable: executable}, dir)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := NewPlan(resolved, dir, nil); err == nil {
		t.Fatal("directory materialization unexpectedly succeeded")
	}

	file := filepath.Join(dir, "file")
	if err := os.WriteFile(file, []byte("content"), 0o600); err != nil {
		t.Fatal(err)
	}
	plan, err := NewPlan(resolved, file, nil)
	if err != nil {
		t.Fatal(err)
	}
	holdExecutableInode(t, executable)
	if err := os.Remove(executable); err != nil {
		t.Fatal(err)
	}
	writeExecutable(t, dir, "editor")
	if _, err := plan.CommandContext(context.Background()); err == nil {
		t.Fatal("plan built command after executable replacement")
	}
}

func TestNativeDefaultOpenerIsExactWhenAvailable(t *testing.T) {
	t.Parallel()

	want, ok := DefaultOpenerPath(runtime.GOOS)
	if !ok {
		t.Skip("no Stage 3 default opener on this platform")
	}
	if _, err := os.Stat(want); err != nil {
		t.Skipf("native default opener is unavailable: %v", err)
	}
	resolved, err := ResolveOpener(nil, runtime.GOOS, os.Getenv("PATH"))
	if err != nil {
		t.Fatalf("ResolveOpener: %v", err)
	}
	if resolved.Executable != want {
		t.Fatalf("executable = %q, want exact %q", resolved.Executable, want)
	}
}

func writeExecutable(t *testing.T, dir, name string) string {
	t.Helper()
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, []byte("#!/bin/sh\nexit 0\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	abs, err := filepath.Abs(path)
	if err != nil {
		t.Fatal(err)
	}
	canonical, err := filepath.EvalSymlinks(abs)
	if err != nil {
		t.Fatal(err)
	}
	return canonical
}

func holdExecutableInode(t *testing.T, path string) {
	t.Helper()
	// Keep the original inode allocated after unlink. Some Linux filesystems can
	// otherwise reuse both the inode and timestamp in the same clock tick, which
	// makes a remove-and-recreate fixture indistinguishable from the original.
	if err := os.Link(path, path+".held"); err != nil {
		t.Fatalf("hold executable inode: %v", err)
	}
}
