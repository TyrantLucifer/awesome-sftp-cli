//go:build darwin || linux

package platform

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"testing"

	"github.com/TyrantLucifer/awesome-sftp-cli/internal/testkit"
)

func TestAcquireInstanceLockIsExclusiveAndReleases(t *testing.T) {
	directory := privateTemporaryDirectory(t)
	path := filepath.Join(directory, lockFileName)

	first, err := AcquireInstanceLock(path, ValidateRuntimeFallback)
	if err != nil {
		t.Fatalf("AcquireInstanceLock(first): %v", err)
	}
	defer first.Close()

	if _, err := AcquireInstanceLock(path, ValidateRuntimeFallback); !errors.Is(err, ErrInstanceLocked) {
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
	second, err := AcquireInstanceLock(path, ValidateRuntimeFallback)
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
	if err := os.WriteFile(path, []byte("sentinel"), 0o600); err != nil {
		t.Fatalf("write unsafe lock: %v", err)
	}
	// #nosec G302 -- the deliberately unsafe fixture proves fail-closed behavior.
	if err := os.Chmod(path, 0o644); err != nil {
		t.Fatalf("chmod unsafe lock: %v", err)
	}

	if _, err := AcquireInstanceLock(path, ValidateRuntimeFallback); err == nil {
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

func TestAcquireInstanceLockIsExclusiveAcrossProcesses(t *testing.T) {
	path := processLockPath(os.Getpid())
	directory := filepath.Dir(path)
	if err := os.Mkdir(directory, 0o700); err != nil {
		t.Fatalf("mkdir process lock fixture: %v", err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(directory) })
	lock, err := AcquireInstanceLock(path, ValidateRuntimeFallback)
	if err != nil {
		t.Fatalf("AcquireInstanceLock(parent): %v", err)
	}
	defer lock.Close()

	runInstanceLockHelper(t, "TestInstanceLockHeldHelperProcess")
	if err := lock.Close(); err != nil {
		t.Fatalf("Close(parent): %v", err)
	}
	runInstanceLockHelper(t, "TestInstanceLockAvailableHelperProcess")
}

func TestInstanceLockHeldHelperProcess(t *testing.T) {
	if os.Getenv("AMSFTP_TEST_INSTANCE_LOCK_HELPER") != "held" {
		t.Skip("subprocess helper")
	}
	lock, err := AcquireInstanceLock(processLockPath(os.Getppid()), ValidateRuntimeFallback)
	if !errors.Is(err, ErrInstanceLocked) {
		if lock != nil {
			_ = lock.Close()
		}
		t.Fatalf("AcquireInstanceLock() error = %v, want ErrInstanceLocked", err)
	}
}

func TestInstanceLockAvailableHelperProcess(t *testing.T) {
	if os.Getenv("AMSFTP_TEST_INSTANCE_LOCK_HELPER") != "available" {
		t.Skip("subprocess helper")
	}
	lock, err := AcquireInstanceLock(processLockPath(os.Getppid()), ValidateRuntimeFallback)
	if err != nil {
		t.Fatalf("AcquireInstanceLock(): %v", err)
	}
	if closeErr := lock.Close(); closeErr != nil {
		t.Fatalf("Close(): %v", closeErr)
	}
}

func runInstanceLockHelper(t *testing.T, testName string) {
	t.Helper()
	// #nosec G204,G702 -- the executable is the current test binary and the name is a fixed test identifier.
	command := exec.Command(os.Args[0], "-test.run=^"+testName+"$")
	marker := "held"
	if testName == "TestInstanceLockAvailableHelperProcess" {
		marker = "available"
	}
	command.Env = append(os.Environ(), "AMSFTP_TEST_INSTANCE_LOCK_HELPER="+marker)
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("instance lock helper (%s): %v\n%s", testName, err, output)
	}
}

func processLockPath(pid int) string {
	return filepath.Join("/tmp", "amsftp-lock-process-"+strconv.Itoa(pid), lockFileName)
}

func privateTemporaryDirectory(t *testing.T) string {
	t.Helper()
	directory := testkit.PersistentTempDir(t)
	// #nosec G302 -- owner-private directories intentionally use 0700.
	if err := os.Chmod(directory, 0o700); err != nil {
		t.Fatalf("chmod temporary directory: %v", err)
	}
	return directory
}
