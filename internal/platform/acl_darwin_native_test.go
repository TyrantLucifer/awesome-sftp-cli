//go:build darwin

package platform

import (
	"encoding/binary"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/TyrantLucifer/awesome-mac-sftp/internal/testkit"
)

func TestDarwinNativeACLValidationAcceptsPrivateTemporaryDirectory(t *testing.T) {
	directory := testkit.PersistentTempDir(t)
	// #nosec G302 -- owner-private directories intentionally use 0700.
	if err := os.Chmod(directory, 0o700); err != nil {
		t.Fatalf("chmod temporary directory: %v", err)
	}
	if err := ValidatePrivateDirectory(directory, ValidateRuntime); err != nil {
		t.Fatalf("ValidatePrivateDirectory(%q): %v", directory, err)
	}
}

func TestDarwinKernelACLDenyDoesNotExpandPrivateDirectory(t *testing.T) {
	directory := privateTemporaryDirectory(t)
	setDarwinKernelACL(t, directory, "everyone deny delete")

	if err := ValidatePrivateDirectory(directory, ValidateRuntime); err != nil {
		t.Fatalf("ValidatePrivateDirectory() rejected deny-only ACL: %v", err)
	}
}

func TestDarwinKernelACLRejectsAllowEntriesByProfile(t *testing.T) {
	t.Run("owner private allow read", func(t *testing.T) {
		directory := privateTemporaryDirectory(t)
		setDarwinKernelACL(t, directory, "everyone allow list,search,readattr,readextattr,readsecurity")

		err := ValidatePrivateDirectory(directory, ValidateRuntime)
		if err == nil || !strings.Contains(err.Error(), "owner-private profile rejects Darwin allow ACL entries") {
			t.Fatalf("ValidatePrivateDirectory() error = %v", err)
		}
	})

	t.Run("integrity allow mutation", func(t *testing.T) {
		root := privateTemporaryDirectory(t)
		ancestor := filepath.Join(root, "ancestor")
		if err := os.Mkdir(ancestor, 0o700); err != nil {
			t.Fatalf("mkdir ancestor: %v", err)
		}
		setDarwinKernelACL(t, ancestor, "everyone allow add_file,add_subdirectory,delete_child")
		child := filepath.Join(ancestor, "child")
		if err := os.Mkdir(child, 0o700); err != nil {
			t.Fatalf("mkdir child: %v", err)
		}

		err := ValidatePrivateDirectory(child, ValidateRuntime)
		if err == nil || !strings.Contains(err.Error(), "darwin ACL grants mutating rights") {
			t.Fatalf("ValidatePrivateDirectory() error = %v", err)
		}
	})
}

func TestDarwinKernelACLRejectsInheritedAllowEntry(t *testing.T) {
	root := privateTemporaryDirectory(t)
	parent := filepath.Join(root, "parent")
	if err := os.Mkdir(parent, 0o700); err != nil {
		t.Fatalf("mkdir parent: %v", err)
	}
	setDarwinKernelACL(t, parent, "everyone allow list,search,readattr,readextattr,readsecurity,file_inherit,directory_inherit")
	child := filepath.Join(parent, "child")
	if err := os.Mkdir(child, 0o700); err != nil {
		t.Fatalf("mkdir child: %v", err)
	}

	validator, err := currentTrustValidator()
	if err != nil {
		t.Fatalf("currentTrustValidator(): %v", err)
	}
	resolvedChild, err := validator.resolveTrustedSystemAlias(child, ValidateRuntime)
	if err != nil {
		t.Fatalf("resolveTrustedSystemAlias(child): %v", err)
	}
	data, err := darwinExtendedSecurity(resolvedChild)
	if err != nil {
		t.Fatalf("darwinExtendedSecurity(child): %v", err)
	}
	if len(data) < darwinFilesecHeaderBytes || binary.LittleEndian.Uint32(data[darwinACLEntryCountOffset:]) == 0 {
		t.Fatalf("child kernel ACL is missing: %x", data)
	}
	if flags := binary.LittleEndian.Uint32(data[darwinFilesecHeaderBytes+16:]); flags&darwinACEInherited == 0 {
		t.Fatalf("child ACE flags = %#x, want inherited", flags)
	}
	err = ValidatePrivateDirectory(child, ValidateRuntime)
	if err == nil || !strings.Contains(err.Error(), "owner-private profile rejects Darwin allow ACL entries") {
		t.Fatalf("ValidatePrivateDirectory() error = %v", err)
	}
}

func setDarwinKernelACL(t *testing.T, path, entry string) {
	t.Helper()
	t.Cleanup(func() {
		// #nosec G204 -- path is a test-owned temporary directory.
		_ = exec.Command("/bin/chmod", "-RN", path).Run()
	})
	// #nosec G204 -- callers provide fixed ACL entries and test-owned temporary paths.
	output, err := exec.Command("/bin/chmod", "+a", entry, path).CombinedOutput()
	if err != nil {
		t.Fatalf("chmod +a %q: %v: %s", entry, err, output)
	}
}
