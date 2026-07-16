//go:build darwin || linux

package platform

import (
	"context"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"syscall"
)

func RemoveLockedControlSocket(path string, purpose ValidationPurpose, lock *InstanceLock) error {
	if !lock.matchesRuntime(filepath.Dir(path)) {
		return fmt.Errorf("remove control socket requires the matching live instance lock")
	}
	if err := ValidatePrivateSocket(path, purpose); err != nil {
		return fmt.Errorf("validate stale control socket: %w", err)
	}
	before, err := os.Lstat(path)
	if err != nil {
		return fmt.Errorf("inspect stale control socket: %w", err)
	}
	after, err := os.Lstat(path)
	if err != nil {
		return fmt.Errorf("reinspect stale control socket: %w", err)
	}
	if !os.SameFile(before, after) {
		return fmt.Errorf("stale control socket changed during inspection")
	}
	if err := syscall.Unlink(path); err != nil {
		return fmt.Errorf("remove stale control socket: %w", err)
	}
	return nil
}

type ControlListener struct {
	listener *net.UnixListener
}

func ListenControlSocket(path string, purpose ValidationPurpose, lock *InstanceLock) (*ControlListener, error) {
	if err := validatePurpose(purpose); err != nil {
		return nil, err
	}
	if err := validateControlSocketPath(path); err != nil {
		return nil, err
	}
	if !lock.matchesRuntime(filepath.Dir(path)) {
		return nil, fmt.Errorf("control socket requires the matching live instance lock")
	}
	if err := ValidatePrivateDirectory(filepath.Dir(path), purpose); err != nil {
		return nil, fmt.Errorf("validate control socket directory: %w", err)
	}

	listener, err := net.ListenUnix("unix", &net.UnixAddr{Name: path, Net: "unix"})
	if err != nil {
		return nil, fmt.Errorf("listen on control socket: %w", err)
	}
	cleanup := func(cause error) (*ControlListener, error) {
		_ = listener.Close()
		return nil, cause
	}
	if err := os.Chmod(path, 0o600); err != nil {
		return cleanup(fmt.Errorf("set control socket mode: %w", err))
	}
	validator, err := currentTrustValidator()
	if err != nil {
		return cleanup(err)
	}
	if err := validator.validatePrivateSocket(path, purpose); err != nil {
		return cleanup(fmt.Errorf("validate control socket: %w", err))
	}
	return &ControlListener{listener: listener}, nil
}

func DialControlSocket(ctx context.Context, path string, purpose ValidationPurpose) (*net.UnixConn, error) {
	if err := validateControlSocketPath(path); err != nil {
		return nil, err
	}
	validator, err := currentTrustValidator()
	if err != nil {
		return nil, err
	}
	if err := validator.validatePrivateSocket(path, purpose); err != nil {
		return nil, fmt.Errorf("validate control socket: %w", err)
	}
	connection, err := (&net.Dialer{}).DialContext(ctx, "unix", path)
	if err != nil {
		return nil, fmt.Errorf("dial control socket: %w", err)
	}
	unixConnection, ok := connection.(*net.UnixConn)
	if !ok {
		_ = connection.Close()
		return nil, fmt.Errorf("control socket dial returned %T", connection)
	}
	if err := VerifyPeerUID(unixConnection); err != nil {
		_ = unixConnection.Close()
		return nil, err
	}
	return unixConnection, nil
}

func (l *ControlListener) Accept() (*net.UnixConn, error) {
	if l == nil || l.listener == nil {
		return nil, fmt.Errorf("control listener is nil")
	}
	connection, err := l.listener.AcceptUnix()
	if err != nil {
		return nil, err
	}
	if err := VerifyPeerUID(connection); err != nil {
		_ = connection.Close()
		return nil, err
	}
	return connection, nil
}

func (l *ControlListener) Close() error {
	if l == nil || l.listener == nil {
		return nil
	}
	return l.listener.Close()
}
