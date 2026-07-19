//go:build darwin || linux

package migration

import (
	"errors"
	"fmt"
	"os"

	"github.com/TyrantLucifer/awesome-sftp-cli/internal/platform"
	"golang.org/x/sys/unix"
)

func migrationWALSize(path string) (uint64, error) {
	if _, err := os.Lstat(path); errors.Is(err, os.ErrNotExist) { //nolint:gosec // exact SQLite main path plus fixed -wal suffix
		return 0, nil
	} else if err != nil {
		return 0, err
	}
	descriptor, err := unix.Open(path, unix.O_RDONLY|unix.O_CLOEXEC|unix.O_NOFOLLOW, 0)
	if err != nil {
		if errors.Is(err, unix.ENOENT) {
			return 0, nil
		}
		return 0, err
	}
	file := os.NewFile(uintptr(descriptor), path) //nolint:gosec // descriptor was just returned by unix.Open
	if file == nil {
		_ = unix.Close(descriptor)
		return 0, fmt.Errorf("wrap WAL descriptor")
	}
	info, statErr := file.Stat()
	closeErr := file.Close()
	if statErr != nil {
		return 0, errors.Join(statErr, closeErr)
	}
	if closeErr != nil {
		return 0, closeErr
	}
	if !info.Mode().IsRegular() || info.Mode().Perm() != 0o600 || info.Size() < 0 {
		return 0, fmt.Errorf("WAL must be a non-negative regular 0600 file")
	}
	if err := platform.ValidatePrivateFile(path, platform.ValidatePersistent); err != nil {
		return 0, fmt.Errorf("validate WAL attributes: %w", err)
	}
	return uint64(info.Size()), nil //nolint:gosec // non-negative size checked above
}
