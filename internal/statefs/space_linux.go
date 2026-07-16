//go:build linux

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
	if status.Bsize <= 0 {
		return 0, fmt.Errorf("statfs block size %d is invalid for %q", status.Bsize, path)
	}
	available, ok := checkedMultiply(status.Bavail, uint64(status.Bsize)) //nolint:gosec // positivity checked above
	if !ok {
		return 0, fmt.Errorf("statfs available bytes overflow for %q", path)
	}
	return available, nil
}
