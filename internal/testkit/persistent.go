package testkit

import (
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"testing"
)

// PersistentTempDir returns a temporary directory whose ancestor chain is
// suitable for testing owner-private persistent storage. os.TempDir commonly
// resolves beneath sticky /tmp on Linux, which persistent storage must reject.
func PersistentTempDir(t testing.TB) string {
	t.Helper()
	base := os.Getenv("AMSFTP_TEST_PERSISTENT_ROOT")
	if base == "" && runtime.GOOS == "linux" {
		candidate := filepath.Join("/var/lib/amsftp-tests", strconv.Itoa(os.Geteuid()))
		if info, err := os.Stat(candidate); err == nil && info.IsDir() {
			base = candidate
		}
	}
	if base == "" {
		var err error
		base, err = os.UserHomeDir()
		if err != nil {
			t.Fatalf("resolve home directory for persistent test storage: %v", err)
		}
	}
	if !filepath.IsAbs(base) || filepath.Clean(base) != base {
		t.Fatalf("persistent test root must be canonical absolute: %q", base)
	}
	path, err := os.MkdirTemp(base, ".amsftp-test-")
	if err != nil {
		t.Fatalf("create persistent test directory: %v", err)
	}
	t.Cleanup(func() {
		if err := os.RemoveAll(path); err != nil {
			t.Errorf("remove persistent test directory: %v", err)
		}
	})
	return path
}
