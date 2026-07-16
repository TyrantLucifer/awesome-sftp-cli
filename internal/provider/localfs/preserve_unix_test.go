//go:build darwin || linux

package localfs

import (
	"os"
	"path/filepath"
	"testing"
)

func TestPreserveNoReplaceStaysBoundToOpenedParentAfterSymlinkSwap(t *testing.T) {
	root := t.TempDir()
	parent := filepath.Join(root, "parent")
	outside := filepath.Join(root, "outside")
	if err := os.Mkdir(parent, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(outside, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(parent, "source"), []byte("inside"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(outside, "source"), []byte("outside"), 0o600); err != nil {
		t.Fatal(err)
	}
	// #nosec G304 -- parent is a test-owned path below t.TempDir().
	handle, err := os.Open(parent)
	if err != nil {
		t.Fatal(err)
	}
	defer handle.Close()
	movedParent := filepath.Join(root, "opened-parent")
	if err := os.Rename(parent, movedParent); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, parent); err != nil {
		t.Fatal(err)
	}
	if err := preserveNoReplace(handle, "source", "backup"); err != nil {
		t.Fatal(err)
	}
	// #nosec G304 -- movedParent is a test-owned path below t.TempDir().
	if got, err := os.ReadFile(filepath.Join(movedParent, "backup")); err != nil || string(got) != "inside" {
		t.Fatalf("opened-parent backup = %q, %v", got, err)
	}
	// #nosec G304 -- outside is a test-owned path below t.TempDir().
	if got, err := os.ReadFile(filepath.Join(outside, "source")); err != nil || string(got) != "outside" {
		t.Fatalf("outside source changed = %q, %v", got, err)
	}
	if _, err := os.Stat(filepath.Join(outside, "backup")); !os.IsNotExist(err) {
		t.Fatalf("outside backup exists: %v", err)
	}
}
