//go:build linux

package statefs

import (
	"fmt"
	"os"

	"golang.org/x/sys/unix"
)

func fullSyncFile(file *os.File) error {
	if file == nil {
		return fmt.Errorf("full sync: nil file")
	}
	if err := unix.Fsync(int(file.Fd())); err != nil {
		return fmt.Errorf("fsync %q: %w", file.Name(), err)
	}
	return nil
}
