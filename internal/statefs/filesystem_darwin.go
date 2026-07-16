//go:build darwin

package statefs

import (
	"fmt"
	"strings"

	"golang.org/x/sys/unix"
)

func detectApprovedFilesystem(path string) (Filesystem, error) {
	var status unix.Statfs_t
	if err := unix.Statfs(path, &status); err != nil {
		return "", fmt.Errorf("statfs %q: %w", path, err)
	}
	var name strings.Builder
	for _, value := range status.Fstypename {
		if value == 0 {
			break
		}
		name.WriteByte(value)
	}
	if name.String() != string(FilesystemAPFS) {
		return "", fmt.Errorf("filesystem %q is not approved; want APFS", name.String())
	}
	return FilesystemAPFS, nil
}
