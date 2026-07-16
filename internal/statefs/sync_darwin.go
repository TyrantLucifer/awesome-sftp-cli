//go:build darwin

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
	if _, err := unix.FcntlInt(file.Fd(), unix.F_FULLFSYNC, 0); err != nil {
		return fmt.Errorf("F_FULLFSYNC %q: %w", file.Name(), err)
	}
	return nil
}
