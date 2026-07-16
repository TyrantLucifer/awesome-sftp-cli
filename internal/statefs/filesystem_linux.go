//go:build linux

package statefs

import (
	"fmt"
	"os"

	"golang.org/x/sys/unix"
)

const (
	extFilesystemMagic = 0xEF53
	xfsFilesystemMagic = 0x58465342
)

func detectApprovedFilesystem(path string) (Filesystem, error) {
	var status unix.Statfs_t
	if err := unix.Statfs(path, &status); err != nil {
		return "", fmt.Errorf("statfs %q: %w", path, err)
	}
	mountInfo, err := os.Open("/proc/self/mountinfo")
	if err != nil {
		return "", fmt.Errorf("open mountinfo: %w", err)
	}
	filesystemType, parseErr := filesystemTypeForPath(mountInfo, path)
	closeErr := mountInfo.Close()
	if parseErr != nil {
		return "", parseErr
	}
	if closeErr != nil {
		return "", fmt.Errorf("close mountinfo: %w", closeErr)
	}
	switch status.Type {
	case extFilesystemMagic:
		if filesystemType != string(FilesystemExt4) {
			return "", fmt.Errorf("ext-family magic has mount type %q, want ext4", filesystemType)
		}
		return FilesystemExt4, nil
	case xfsFilesystemMagic:
		if filesystemType != string(FilesystemXFS) {
			return "", fmt.Errorf("XFS magic has mount type %q, want xfs", filesystemType)
		}
		return FilesystemXFS, nil
	default:
		return "", fmt.Errorf("filesystem magic %#x/type %q is not approved", status.Type, filesystemType)
	}
}
