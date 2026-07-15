package testkit

import (
	"os"
	"path/filepath"
	"testing"
)

func TestPersistentTempDirIsPrivateAndHomeBacked(t *testing.T) {
	home, err := os.UserHomeDir()
	if err != nil {
		t.Fatal(err)
	}
	path := PersistentTempDir(t)
	if filepath.Dir(path) != home {
		t.Fatalf("persistent temp parent = %q, want %q", filepath.Dir(path), home)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if !info.IsDir() || info.Mode().Perm() != 0o700 {
		t.Fatalf("persistent temp mode = %v, want directory 0700", info.Mode())
	}
}
