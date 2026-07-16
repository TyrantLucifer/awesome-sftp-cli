//go:build linux

package terminalhandoff

import (
	"fmt"
	"os"
	"testing"

	"golang.org/x/sys/unix"
)

func openTestPTY(t *testing.T) (*os.File, *os.File) {
	t.Helper()

	masterFD, err := unix.Open("/dev/ptmx", unix.O_RDWR|unix.O_NOCTTY|unix.O_CLOEXEC, 0)
	if err != nil {
		t.Fatalf("open /dev/ptmx: %v", err)
	}
	master := os.NewFile(uintptr(masterFD), "/dev/ptmx")
	number, err := unix.IoctlGetInt(masterFD, unix.TIOCGPTN)
	if err != nil {
		if closeErr := master.Close(); closeErr != nil {
			t.Errorf("close PTY master after number query failure: %v", closeErr)
		}
		t.Fatalf("query PTY number: %v", err)
	}
	if err := unix.IoctlSetPointerInt(masterFD, unix.TIOCSPTLCK, 0); err != nil {
		if closeErr := master.Close(); closeErr != nil {
			t.Errorf("close PTY master after unlock failure: %v", closeErr)
		}
		t.Fatalf("unlock PTY: %v", err)
	}
	name := fmt.Sprintf("/dev/pts/%d", number)
	// #nosec G304 -- name is derived from the kernel-reported PTY number.
	slave, err := os.OpenFile(name, os.O_RDWR|unix.O_NOCTTY, 0)
	if err != nil {
		if closeErr := master.Close(); closeErr != nil {
			t.Errorf("close PTY master after slave open failure: %v", closeErr)
		}
		t.Fatalf("open PTY slave %q: %v", name, err)
	}
	return master, slave
}
