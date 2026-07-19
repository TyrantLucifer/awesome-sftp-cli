package app

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"

	"github.com/TyrantLucifer/awesome-mac-sftp/internal/testkit"
)

func TestCompletionWorkspaceQueryUsesPrivateStateWithoutStartingRuntime(t *testing.T) {
	root := filepath.Join(testkit.PersistentTempDir(t), "workspaces")
	if err := os.Mkdir(root, 0o700); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"z-last", "a-first"} {
		if err := os.WriteFile(filepath.Join(root, name+".json"), []byte("corrupt but name-completable"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	resolverCalls := 0
	resolver := func() (string, error) {
		resolverCalls++
		return root, nil
	}
	var stdout bytes.Buffer
	if err := runCompletionWithWorkspaceRoot(t.Context(), []string{"__workspaces"}, &stdout, resolver); err != nil {
		t.Fatal(err)
	}
	if got, want := stdout.String(), "a-first\nz-last\n"; got != want {
		t.Fatalf("workspace completion output = %q, want %q", got, want)
	}
	if resolverCalls != 1 {
		t.Fatalf("workspace root resolver calls = %d, want 1", resolverCalls)
	}

	resolverCalls = 0
	stdout.Reset()
	if err := runCompletionWithWorkspaceRoot(t.Context(), []string{"bash"}, &stdout, resolver); err != nil {
		t.Fatal(err)
	}
	if resolverCalls != 0 {
		t.Fatalf("static script resolved workspace state %d times, want zero", resolverCalls)
	}
	if _, err := os.Lstat(filepath.Join(root, "amsftp.db")); !os.IsNotExist(err) {
		t.Fatalf("completion started or created runtime state: %v", err)
	}

	if err := runCompletionWithWorkspaceRoot(t.Context(), []string{"__workspaces", "extra"}, &bytes.Buffer{}, resolver); err == nil || exitCode(err) != ExitUsage {
		t.Fatalf("extra workspace query argument error = %v, exit = %d", err, exitCode(err))
	}
}
