//go:build darwin || linux

package platform

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestAcquireInstanceLockIsExclusiveAndReleases(t *testing.T) {
	directory := privateTemporaryDirectory(t)
	path := filepath.Join(directory, lockFileName)

	first, err := AcquireInstanceLock(path, ValidateRuntime)
	if err != nil {
		t.Fatalf("AcquireInstanceLock(first): %v", err)
	}
	defer first.Close()

	if _, err := AcquireInstanceLock(path, ValidateRuntime); !errors.Is(err, ErrInstanceLocked) {
		t.Fatalf("AcquireInstanceLock(second) error = %v, want ErrInstanceLocked", err)
	}
	info, err := os.Lstat(path)
	if err != nil {
		t.Fatalf("lstat lock: %v", err)
	}
	if info.Mode() != 0o600 {
		t.Fatalf("lock mode = %v, want 0600", info.Mode())
	}

	if err := first.Close(); err != nil {
		t.Fatalf("Close(first): %v", err)
	}
	if err := first.Close(); err != nil {
		t.Fatalf("Close(first again): %v", err)
	}
	second, err := AcquireInstanceLock(path, ValidateRuntime)
	if err != nil {
		t.Fatalf("AcquireInstanceLock(after release): %v", err)
	}
	if err := second.Close(); err != nil {
		t.Fatalf("Close(second): %v", err)
	}
}

func TestAcquireInstanceLockDoesNotTakeOverUnsafeExistingFile(t *testing.T) {
	directory := privateTemporaryDirectory(t)
	path := filepath.Join(directory, lockFileName)
	// #nosec G306 -- the deliberately unsafe fixture proves fail-closed behavior.
	if err := os.WriteFile(path, []byte("sentinel"), 0o644); err != nil {
		t.Fatalf("write unsafe lock: %v", err)
	}

	if _, err := AcquireInstanceLock(path, ValidateRuntime); err == nil {
		t.Fatal("AcquireInstanceLock() error = nil")
	}
	info, err := os.Lstat(path)
	if err != nil {
		t.Fatalf("lstat unsafe lock: %v", err)
	}
	if info.Mode() != 0o644 {
		t.Fatalf("unsafe lock mode changed to %v", info.Mode())
	}
	// #nosec G304 -- path is a test-owned file inside t.TempDir.
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read unsafe lock: %v", err)
	}
	if string(data) != "sentinel" {
		t.Fatalf("unsafe lock content = %q", data)
	}
}

func privateTemporaryDirectory(t *testing.T) string {
	t.Helper()
	directory := t.TempDir()
	// #nosec G302 -- owner-private directories intentionally use 0700.
	if err := os.Chmod(directory, 0o700); err != nil {
		t.Fatalf("chmod temporary directory: %v", err)
	}
	return directory
}
