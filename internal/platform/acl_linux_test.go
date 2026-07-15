//go:build linux

package platform

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
)

func TestNormalizeLinuxACLErrorPreservesSentinelAndCause(t *testing.T) {
	tests := []struct{ cause, sentinel error }{
		{cause: syscall.ENODATA, sentinel: errACLNoData},
		{cause: syscall.ENOTSUP, sentinel: errACLUnsupported},
	}
	for _, test := range tests {
		got := normalizeLinuxACLError(test.cause)
		if !errors.Is(got, test.sentinel) || !errors.Is(got, test.cause) {
			t.Fatalf("normalizeLinuxACLError(%v) = %v; sentinel/cause not preserved", test.cause, got)
		}
	}
}

func TestLinuxKernelACLRejectsOwnerPrivateNamedRead(t *testing.T) {
	directory := privateTemporaryDirectory(t)
	acl := posixACLBytes(posixExtendedACL(
		// #nosec G115 -- supported Linux effective UIDs fit in uint32 and adding one selects a test-only peer.
		posixACLTestEntry{tag: posixACLNamedUser, permissions: 0o4, id: uint32(os.Geteuid() + 1)},
		0o4,
	)...)
	if err := syscall.Setxattr(directory, posixACLAccessXattr, acl, 0); err != nil {
		t.Fatalf("set kernel access ACL: %v", err)
	}

	err := newPlatformACLValidator().validateACL(directory, aclOwnerPrivate, true)
	if err == nil || !strings.Contains(err.Error(), "ACL grants effective permission outside the owner") {
		t.Fatalf("validateACL() error = %v", err)
	}
}

func TestLinuxKernelACLRejectsDefaultACL(t *testing.T) {
	root := privateTemporaryDirectory(t)
	directory := filepath.Join(root, "inheriting")
	if err := os.Mkdir(directory, 0o700); err != nil {
		t.Fatalf("mkdir fixture: %v", err)
	}
	acl := posixACLBytes(
		posixACLTestEntry{tag: posixACLUserObject, permissions: 0o7},
		posixACLTestEntry{tag: posixACLGroupObject, permissions: 0},
		posixACLTestEntry{tag: posixACLOther, permissions: 0},
	)
	if err := syscall.Setxattr(directory, posixACLDefaultXattr, acl, 0); err != nil {
		t.Fatalf("set kernel default ACL: %v", err)
	}

	err := ValidatePrivateDirectory(directory, ValidateRuntimeFallback)
	if err == nil || !strings.Contains(err.Error(), "POSIX default ACL is not permitted") {
		t.Fatalf("ValidatePrivateDirectory() error = %v", err)
	}
}
