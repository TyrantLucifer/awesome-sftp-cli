//go:build linux

package platform

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"syscall"
)

func newPlatformACLValidator() aclValidator {
	return posixACLValidator{system: linuxACLSystem{}}
}

type linuxACLSystem struct{}

func (linuxACLSystem) getxattr(path string, name string) ([]byte, error) {
	size, err := syscall.Getxattr(path, name, nil)
	if err != nil {
		return nil, normalizeLinuxACLError(err)
	}
	if size == 0 {
		return []byte{}, nil
	}

	for attempts := 0; attempts < 2; attempts++ {
		buffer := make([]byte, size)
		read, readErr := syscall.Getxattr(path, name, buffer)
		if readErr == nil {
			return buffer[:read], nil
		}
		if !errors.Is(readErr, syscall.ERANGE) {
			return nil, normalizeLinuxACLError(readErr)
		}
		size, err = syscall.Getxattr(path, name, nil)
		if err != nil {
			return nil, normalizeLinuxACLError(err)
		}
	}
	return nil, fmt.Errorf("ACL xattr changed size repeatedly")
}

func (linuxACLSystem) lstatMode(path string) (fs.FileMode, error) {
	info, err := os.Lstat(path)
	if err != nil {
		return 0, err
	}
	return info.Mode(), nil
}

func (linuxACLSystem) filesystemType(path string) (int64, error) {
	var stat syscall.Statfs_t
	if err := syscall.Statfs(path, &stat); err != nil {
		return 0, err
	}
	return stat.Type, nil
}

func normalizeLinuxACLError(err error) error {
	switch {
	case errors.Is(err, syscall.ENODATA):
		return fmt.Errorf("%w: %w", errACLNoData, err)
	case errors.Is(err, syscall.ENOTSUP), errors.Is(err, syscall.EOPNOTSUPP):
		return fmt.Errorf("%w: %w", errACLUnsupported, err)
	default:
		return err
	}
}

var _ posixACLSystem = linuxACLSystem{}
