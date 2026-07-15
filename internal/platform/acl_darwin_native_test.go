//go:build darwin

package platform

import (
	"os"
	"testing"
)

func TestDarwinNativeACLValidationAcceptsPrivateTemporaryDirectory(t *testing.T) {
	directory := t.TempDir()
	// #nosec G302 -- owner-private directories intentionally use 0700.
	if err := os.Chmod(directory, 0o700); err != nil {
		t.Fatalf("chmod temporary directory: %v", err)
	}
	if err := ValidatePrivateDirectory(directory, ValidateRuntime); err != nil {
		t.Fatalf("ValidatePrivateDirectory(%q): %v", directory, err)
	}
}
