package app

import (
	"context"
	"os"
	"path/filepath"
	"strconv"
	"testing"
	"time"

	"github.com/TyrantLucifer/awesome-mac-sftp/internal/daemon"
	"github.com/TyrantLucifer/awesome-mac-sftp/internal/ipc"
	"github.com/TyrantLucifer/awesome-mac-sftp/internal/platform"
)

func TestDaemonRoleServesLocalProviderAndStopsCleanly(t *testing.T) {
	base := filepath.Join("/tmp", "amsftp-test-"+strconv.Itoa(os.Getpid()))
	if err := os.Mkdir(base, 0o700); err != nil && !os.IsExist(err) {
		t.Fatal(err)
	}
	defer os.RemoveAll(base)
	// #nosec G302 -- private runtime directories intentionally require mode 0700.
	if err := os.Chmod(base, 0o700); err != nil {
		t.Fatal(err)
	}
	paths := platform.Paths{RuntimeDir: base, ControlSocket: filepath.Join(base, "control-v1.sock"), LockFile: filepath.Join(base, "daemon.lock")}
	purpose := platform.ValidateRuntimeFallback
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- runDaemonWithPaths(ctx, paths, purpose) }()
	var client *daemon.Client
	var err error
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		attemptCtx, stop := context.WithTimeout(context.Background(), 200*time.Millisecond)
		client, err = connectExisting(attemptCtx, paths, purpose)
		stop()
		if err == nil {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if err != nil {
		cancel()
		t.Fatalf("connect daemon: %v", err)
	}
	var endpoints ipc.ProviderEndpointsResponse
	if err := client.Call(context.Background(), daemon.ProviderEndpoints, struct{}{}, &endpoints); err != nil {
		t.Fatal(err)
	}
	if len(endpoints.Endpoints) != 1 || endpoints.Endpoints[0].Kind != "local" {
		t.Fatalf("endpoints = %#v", endpoints.Endpoints)
	}
	_ = client.Close()
	for index := 0; index < 5; index++ {
		reconnect, err := connectExisting(context.Background(), paths, purpose)
		if err != nil {
			t.Fatalf("reconnect %d: %v", index, err)
		}
		_ = reconnect.Close()
	}
	secondDone := make(chan error, 1)
	go func() { secondDone <- runDaemonWithPaths(context.Background(), paths, purpose) }()
	select {
	case err := <-secondDone:
		if err != nil {
			t.Fatalf("second daemon: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("second daemon did not converge on held lock")
	}
	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("daemon did not stop")
	}
	if _, err := os.Lstat(paths.ControlSocket); !os.IsNotExist(err) {
		t.Fatalf("socket remains after shutdown: %v", err)
	}
}

func connectExisting(ctx context.Context, paths platform.Paths, purpose platform.ValidationPurpose) (*daemon.Client, error) {
	connection, err := platform.DialControlSocket(ctx, paths.ControlSocket, purpose)
	if err != nil {
		return nil, err
	}
	return daemon.NewClient(ctx, connection, "test", "test-client")
}
