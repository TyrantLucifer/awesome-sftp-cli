package testkit

import (
	"os"
	"testing"
)

// PersistentTempDir returns a temporary directory whose ancestor chain is
// suitable for testing owner-private persistent storage. os.TempDir commonly
// resolves beneath sticky /tmp on Linux, which persistent storage must reject.
func PersistentTempDir(t testing.TB) string {
	t.Helper()
	home, err := os.UserHomeDir()
	if err != nil {
		t.Fatalf("resolve home directory for persistent test storage: %v", err)
	}
	path, err := os.MkdirTemp(home, ".amsftp-test-")
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
