//go:build darwin || linux

package platform

import (
	"os"
	"path/filepath"
	"testing"
)

func TestValidateExecutableAcceptsSystemSSH(t *testing.T) {
	if err := ValidateExecutable("/usr/bin/ssh"); err != nil {
		t.Fatal(err)
	}
}

func TestValidateExecutableRejectsWritableAndSymlinkFiles(t *testing.T) {
	directory := privateTemporaryDirectory(t)
	path := filepath.Join(directory, "ssh")
	// #nosec G306 -- executable fixtures intentionally require execute permission.
	if err := os.WriteFile(path, []byte("fake"), 0o700); err != nil {
		t.Fatal(err)
	}
	// #nosec G302 -- deliberately unsafe group/other-write bits exercise fail-closed validation.
	if err := os.Chmod(path, 0o722); err != nil {
		t.Fatal(err)
	}
	if err := ValidateExecutable(path); err == nil {
		t.Fatal("writable executable accepted")
	}
	// #nosec G302 -- executable fixtures intentionally require execute permission.
	if err := os.Chmod(path, 0o700); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(directory, "ssh-link")
	if err := os.Symlink(path, link); err != nil {
		t.Fatal(err)
	}
	if err := ValidateExecutable(link); err == nil {
		t.Fatal("symlink executable accepted")
	}
}
