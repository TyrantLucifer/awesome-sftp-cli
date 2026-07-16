//go:build darwin

package statefs

import (
	"fmt"

	"golang.org/x/sys/unix"
)

func availableFilesystemBytes(path string) (uint64, error) {
	var status unix.Statfs_t
	if err := unix.Statfs(path, &status); err != nil {
		return 0, fmt.Errorf("statfs available bytes for %q: %w", path, err)
	}
	available, ok := checkedMultiply(status.Bavail, uint64(status.Bsize))
	if !ok {
		return 0, fmt.Errorf("statfs available bytes overflow for %q", path)
	}
	return available, nil
}
