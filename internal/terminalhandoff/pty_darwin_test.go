//go:build darwin

package terminalhandoff

import (
	"bytes"
	"os"
	"syscall"
	"testing"
	"unsafe"

	"golang.org/x/sys/unix"
)

func openTestPTY(t *testing.T) (*os.File, *os.File) {
	t.Helper()

	master, err := os.OpenFile("/dev/ptmx", os.O_RDWR|syscall.O_NOCTTY, 0)
	if err != nil {
		t.Fatalf("open /dev/ptmx: %v", err)
	}
	if _, _, errno := syscall.Syscall(syscall.SYS_IOCTL, master.Fd(), unix.TIOCPTYGRANT, 0); errno != 0 {
		_ = master.Close()
		t.Fatalf("grant PTY: %v", errno)
	}
	if _, _, errno := syscall.Syscall(syscall.SYS_IOCTL, master.Fd(), unix.TIOCPTYUNLK, 0); errno != 0 {
		_ = master.Close()
		t.Fatalf("unlock PTY: %v", errno)
	}
	name := make([]byte, 128)
	if _, _, errno := syscall.Syscall(
		syscall.SYS_IOCTL,
		master.Fd(),
		uintptr(unix.TIOCPTYGNAME),
		// #nosec G103 -- TIOCPTYGNAME writes at most the fixed 128-byte kernel ABI buffer.
		uintptr(unsafe.Pointer(&name[0])),
	); errno != 0 {
		_ = master.Close()
		t.Fatalf("query PTY name: %v", errno)
	}
	name = bytes.TrimRight(name, "\x00")
	slave, err := os.OpenFile(string(name), os.O_RDWR|syscall.O_NOCTTY, 0)
	if err != nil {
		_ = master.Close()
		t.Fatalf("open PTY slave %q: %v", name, err)
	}
	return master, slave
}
