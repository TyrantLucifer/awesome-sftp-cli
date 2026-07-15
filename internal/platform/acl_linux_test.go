//go:build linux

package platform

import (
	"errors"
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
