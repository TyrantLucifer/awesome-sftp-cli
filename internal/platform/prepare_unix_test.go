//go:build darwin || linux

package platform

import (
	"os"
	"path/filepath"
	"testing"
)

func TestPreparePrivateDirectoryCreatesAndValidatesPrivateRoot(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "one", "two")
	if err := PreparePrivateDirectory(path, ValidateRuntimeFallback); err != nil {
		t.Fatal(err)
	}
	info, err := os.Lstat(path)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o700 {
		t.Fatalf("mode = %o", info.Mode().Perm())
	}
	if err := ValidatePrivateDirectory(path, ValidateRuntimeFallback); err != nil {
		t.Fatal(err)
	}
}

func TestPreparePrivateDirectoryRejectsSymlinkComponent(t *testing.T) {
	root := t.TempDir()
	target := filepath.Join(root, "target")
	if err := os.Mkdir(target, 0o700); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(root, "link")
	if err := os.Symlink(target, link); err != nil {
		t.Fatal(err)
	}
	if err := PreparePrivateDirectory(filepath.Join(link, "child"), ValidateRuntimeFallback); err == nil {
		t.Fatal("PreparePrivateDirectory() error = nil")
	}
}
