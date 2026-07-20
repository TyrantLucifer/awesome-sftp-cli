//go:build darwin || linux

package platform

import (
	"os"
	"path/filepath"
	"testing"
	"time"
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

func TestResolveTrustedExecutableFreezesValidatedSymlinkTarget(t *testing.T) {
	directory := privateTemporaryDirectory(t)
	target := filepath.Join(directory, "amsftp")
	// #nosec G306 -- executable fixtures intentionally require execute permission.
	if err := os.WriteFile(target, []byte("fake"), 0o700); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(directory, "amsftp-link")
	if err := os.Symlink(target, link); err != nil {
		t.Fatal(err)
	}
	resolved, err := ResolveTrustedExecutable(link)
	if err != nil {
		t.Fatal(err)
	}
	if resolved != target {
		t.Fatalf("resolved executable = %q, want %q", resolved, target)
	}
	if err := os.Remove(target); err != nil {
		t.Fatal(err)
	}
	// #nosec G306 -- deliberately unsafe replacement exercises fail-closed validation.
	if err := os.WriteFile(target, []byte("unsafe"), 0o722); err != nil {
		t.Fatal(err)
	}
	// #nosec G302 -- deliberately unsafe group/other-write bits exercise fail-closed validation.
	if err := os.Chmod(target, 0o722); err != nil {
		t.Fatal(err)
	}
	if _, err := ResolveTrustedExecutable(link); err == nil {
		t.Fatal("symlink to writable executable accepted")
	}
}

func TestSameExecutableIdentityRejectsSameInodeRewrite(t *testing.T) {
	directory := privateTemporaryDirectory(t)
	path := filepath.Join(directory, "ssh")
	if err := os.WriteFile(path, []byte("before"), 0o700); err != nil { // #nosec G306 -- executable fixture intentionally requires owner execute permission.
		t.Fatal(err)
	}
	before, err := ExecutableIdentity(path)
	if err != nil {
		t.Fatal(err)
	}
	originalModTime := before.ModTime()
	time.Sleep(time.Millisecond)
	if err := os.WriteFile(path, []byte("after!"), 0o700); err != nil { // #nosec G306 -- executable fixture intentionally requires owner execute permission.
		t.Fatal(err)
	}
	if err := os.Chtimes(path, originalModTime, originalModTime); err != nil {
		t.Fatal(err)
	}
	after, err := ExecutableIdentity(path)
	if err != nil {
		t.Fatal(err)
	}
	if os.SameFile(before, after) != true {
		t.Fatal("fixture did not preserve the inode")
	}
	if SameExecutableIdentity(before, after) {
		t.Fatal("same-inode executable rewrite unexpectedly matched")
	}
}
