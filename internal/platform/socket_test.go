//go:build darwin || linux

package platform

import (
	"context"
	"io/fs"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestControlSocketUsesPrivateModeAndVerifiedPeers(t *testing.T) {
	directory := shortPrivateTemporaryDirectory(t)
	lock, err := AcquireInstanceLock(filepath.Join(directory, lockFileName), ValidateRuntimeFallback)
	if err != nil {
		t.Fatalf("AcquireInstanceLock(): %v", err)
	}
	defer lock.Close()

	path := filepath.Join(directory, controlSocketName)
	listener, err := ListenControlSocket(path, ValidateRuntimeFallback, lock)
	if err != nil {
		t.Fatalf("ListenControlSocket(): %v", err)
	}
	defer listener.Close()
	info, err := os.Lstat(path)
	if err != nil {
		t.Fatalf("lstat socket: %v", err)
	}
	if info.Mode() != fs.ModeSocket|0o600 {
		t.Fatalf("socket mode = %v, want socket 0600", info.Mode())
	}

	accepted := make(chan error, 1)
	go func() {
		connection, acceptErr := listener.Accept()
		if connection != nil {
			_ = connection.Close()
		}
		accepted <- acceptErr
	}()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	connection, err := DialControlSocket(ctx, path, ValidateRuntimeFallback)
	if err != nil {
		t.Fatalf("DialControlSocket(): %v", err)
	}
	_ = connection.Close()
	select {
	case err := <-accepted:
		if err != nil {
			t.Fatalf("Accept(): %v", err)
		}
	case <-ctx.Done():
		t.Fatalf("Accept() did not return: %v", ctx.Err())
	}
}

func TestListenControlSocketRequiresMatchingLiveLock(t *testing.T) {
	directory := shortPrivateTemporaryDirectory(t)
	path := filepath.Join(directory, controlSocketName)
	if _, err := ListenControlSocket(path, ValidateRuntimeFallback, nil); err == nil {
		t.Fatal("ListenControlSocket() accepted nil lock")
	}

	otherDirectory := shortPrivateTemporaryDirectory(t)
	lock, err := AcquireInstanceLock(filepath.Join(otherDirectory, lockFileName), ValidateRuntimeFallback)
	if err != nil {
		t.Fatalf("AcquireInstanceLock(): %v", err)
	}
	defer lock.Close()
	if _, err := ListenControlSocket(path, ValidateRuntimeFallback, lock); err == nil {
		t.Fatal("ListenControlSocket() accepted lock for another runtime")
	}
}

func shortPrivateTemporaryDirectory(t *testing.T) string {
	t.Helper()
	directory, err := os.MkdirTemp("/tmp", "amsftp-p-")
	if err != nil {
		t.Fatalf("create short temporary directory: %v", err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(directory) })
	// #nosec G302 -- owner-private runtime directories intentionally use 0700.
	if err := os.Chmod(directory, 0o700); err != nil {
		t.Fatalf("chmod short temporary directory: %v", err)
	}
	return directory
}
