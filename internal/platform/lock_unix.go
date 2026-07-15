//go:build darwin || linux

package platform

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"syscall"
)

var ErrInstanceLocked = errors.New("daemon instance lock is already held")

type InstanceLock struct {
	mu     sync.Mutex
	file   *os.File
	path   string
	closed bool
}

func AcquireInstanceLock(path string, purpose ValidationPurpose) (*InstanceLock, error) {
	if err := validatePurpose(purpose); err != nil {
		return nil, err
	}
	if err := validateCanonicalAbsolutePath(path); err != nil {
		return nil, fmt.Errorf("instance lock path: %w", err)
	}
	if err := ValidatePrivateDirectory(filepath.Dir(path), purpose); err != nil {
		return nil, fmt.Errorf("validate instance lock directory: %w", err)
	}

	file, created, err := openInstanceLock(path, purpose)
	if err != nil {
		return nil, err
	}
	cleanup := func() {
		_ = file.Close()
		if created {
			_ = os.Remove(path)
		}
	}
	if created {
		if err := file.Chmod(0o600); err != nil {
			cleanup()
			return nil, fmt.Errorf("set instance lock mode: %w", err)
		}
	}
	if err := ValidatePrivateFile(path, purpose); err != nil {
		cleanup()
		return nil, fmt.Errorf("validate instance lock file: %w", err)
	}
	if err := syscall.Flock(int(file.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		_ = file.Close()
		if errors.Is(err, syscall.EWOULDBLOCK) || errors.Is(err, syscall.EAGAIN) {
			return nil, fmt.Errorf("%w: %s", ErrInstanceLocked, path)
		}
		return nil, fmt.Errorf("acquire instance lock: %w", err)
	}
	return &InstanceLock{file: file, path: path}, nil
}

func (l *InstanceLock) matchesRuntime(directory string) bool {
	if l == nil {
		return false
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	return !l.closed && filepath.Dir(l.path) == directory
}

func openInstanceLock(path string, purpose ValidationPurpose) (*os.File, bool, error) {
	flags := syscall.O_RDWR | syscall.O_CLOEXEC | syscall.O_NOFOLLOW
	fd, err := syscall.Open(path, flags|syscall.O_CREAT|syscall.O_EXCL, 0o600)
	created := err == nil
	if errors.Is(err, syscall.EEXIST) {
		if validateErr := ValidatePrivateFile(path, purpose); validateErr != nil {
			return nil, false, fmt.Errorf("validate existing instance lock: %w", validateErr)
		}
		fd, err = syscall.Open(path, flags, 0)
	}
	if err != nil {
		return nil, false, fmt.Errorf("open instance lock: %w", err)
	}
	return os.NewFile(uintptr(fd), path), created, nil
}

func (l *InstanceLock) Close() error {
	if l == nil {
		return nil
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.closed {
		return nil
	}
	l.closed = true
	unlockErr := syscall.Flock(int(l.file.Fd()), syscall.LOCK_UN)
	closeErr := l.file.Close()
	if unlockErr != nil {
		return fmt.Errorf("release instance lock: %w", unlockErr)
	}
	if closeErr != nil {
		return fmt.Errorf("close instance lock: %w", closeErr)
	}
	return nil
}
