//go:build darwin || linux

package statefs

import (
	"errors"
	"fmt"
	"os"
	"syscall"

	"golang.org/x/sys/unix"
)

type retentionDirectory struct {
	path       string
	descriptor int
	file       *os.File
}

func openRetentionDirectory(path string) (*retentionDirectory, error) {
	descriptor, err := unix.Open(path, unix.O_RDONLY|unix.O_DIRECTORY|unix.O_CLOEXEC|unix.O_NOFOLLOW, 0)
	if err != nil {
		return nil, fmt.Errorf("open retention directory: %w", err)
	}
	file := os.NewFile(uintptr(descriptor), path) //nolint:gosec // descriptor was just returned by unix.Open
	if file == nil {
		_ = unix.Close(descriptor)
		return nil, fmt.Errorf("wrap retention directory descriptor")
	}
	handleInfo, err := file.Stat()
	if err != nil {
		_ = file.Close()
		return nil, fmt.Errorf("stat retention directory handle: %w", err)
	}
	pathInfo, err := os.Lstat(path) //nolint:gosec // caller supplies the validated state root
	if err != nil {
		_ = file.Close()
		return nil, fmt.Errorf("restat retention directory: %w", err)
	}
	if !handleInfo.IsDir() || !os.SameFile(handleInfo, pathInfo) {
		_ = file.Close()
		return nil, fmt.Errorf("retention directory identity changed")
	}
	return &retentionDirectory{path: path, descriptor: descriptor, file: file}, nil
}

func (directory *retentionDirectory) open(name string) (*os.File, error) {
	descriptor, err := unix.Openat(directory.descriptor, name, unix.O_RDONLY|unix.O_CLOEXEC|unix.O_NOFOLLOW, 0)
	if err != nil {
		return nil, err
	}
	file := os.NewFile(uintptr(descriptor), name) //nolint:gosec // descriptor was just returned by unix.Openat
	if file == nil {
		_ = unix.Close(descriptor)
		return nil, fmt.Errorf("wrap retained file descriptor")
	}
	return file, nil
}

func (directory *retentionDirectory) validateRootIdentity() error {
	handleInfo, err := directory.file.Stat()
	if err != nil {
		return fmt.Errorf("stat retained directory handle: %w", err)
	}
	pathInfo, err := os.Lstat(directory.path) //nolint:gosec // exact validated state root
	if err != nil {
		return fmt.Errorf("restat retained directory path: %w", err)
	}
	if !os.SameFile(handleInfo, pathInfo) {
		return fmt.Errorf("retention directory path was replaced")
	}
	return nil
}

func (directory *retentionDirectory) exists(name string) (bool, error) {
	var status unix.Stat_t
	err := unix.Fstatat(directory.descriptor, name, &status, unix.AT_SYMLINK_NOFOLLOW)
	if errors.Is(err, unix.ENOENT) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	return true, nil
}

func (directory *retentionDirectory) matches(name string, info os.FileInfo) (bool, error) {
	var pathStatus unix.Stat_t
	if err := unix.Fstatat(directory.descriptor, name, &pathStatus, unix.AT_SYMLINK_NOFOLLOW); err != nil {
		return false, err
	}
	handleStatus, ok := info.Sys().(*syscall.Stat_t)
	if !ok {
		return false, fmt.Errorf("file info has unexpected stat type %T", info.Sys())
	}
	return handleStatus.Dev == pathStatus.Dev && handleStatus.Ino == pathStatus.Ino, nil
}

func (directory *retentionDirectory) unlink(name string) error {
	return unix.Unlinkat(directory.descriptor, name, 0)
}

func (directory *retentionDirectory) sync() error {
	if err := directory.file.Sync(); err != nil {
		return fmt.Errorf("sync retention directory: %w", err)
	}
	return nil
}

func (directory *retentionDirectory) close() error {
	return directory.file.Close()
}
