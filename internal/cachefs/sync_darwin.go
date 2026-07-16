//go:build darwin

package cachefs

import (
	"fmt"
	"os"
	"syscall"
)

const darwinFullFsync = 51

func fullSyncFile(file *os.File) error {
	if file == nil {
		return fmt.Errorf("full sync cache file: nil file")
	}
	_, _, errno := syscall.Syscall(syscall.SYS_FCNTL, file.Fd(), darwinFullFsync, 0)
	if errno != 0 {
		return fmt.Errorf("F_FULLFSYNC cache file: %w", errno)
	}
	return nil
}
