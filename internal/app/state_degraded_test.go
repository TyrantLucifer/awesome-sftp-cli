package app

import (
	"context"
	"database/sql"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/TyrantLucifer/awesome-mac-sftp/internal/daemon"
	"github.com/TyrantLucifer/awesome-mac-sftp/internal/domain"
	"github.com/TyrantLucifer/awesome-mac-sftp/internal/ipc"
	"github.com/TyrantLucifer/awesome-mac-sftp/internal/platform"
	"github.com/TyrantLucifer/awesome-mac-sftp/internal/statefs"
	"github.com/TyrantLucifer/awesome-mac-sftp/internal/testkit"
	"github.com/TyrantLucifer/awesome-mac-sftp/internal/workspace"
)

func TestDaemonKeepsStage1ReadOnlyWhenPersistentStateIsUnsafe(t *testing.T) {
	fixtures := []struct {
		name   string
		mutate func(*testing.T, string)
	}{
		{name: "corrupt project database", mutate: corruptProjectDatabase},
		{name: "newer schema", mutate: addNewerSchemaHistory},
		{name: "database not owner writable", mutate: func(t *testing.T, path string) {
			t.Helper()
			if err := os.Chmod(path, 0o400); err != nil { //nolint:gosec // deliberately read-only negative fixture
				t.Fatalf("make database read-only: %v", err)
			}
			t.Cleanup(func() { _ = os.Chmod(path, 0o600) }) //nolint:gosec // restore private cleanup mode
		}},
	}
	for _, fixture := range fixtures {
		t.Run(fixture.name, func(t *testing.T) {
			paths, purpose := degradedStatePaths(t)
			if err := platform.PreparePrivateDirectory(paths.StateDir, platform.ValidatePersistent); err != nil {
				t.Fatalf("prepare state directory: %v", err)
			}
			database, _, err := statefs.Initialize(context.Background(), statefs.InitializeConfig{
				Root: paths.StateDir, DatabasePath: paths.DatabaseFile, Now: time.Unix(2_300, 0),
			})
			if err != nil {
				t.Fatalf("initialize fixture database: %v", err)
			}
			if err := database.Close(); err != nil {
				t.Fatalf("close fixture database: %v", err)
			}
			fixture.mutate(t, paths.DatabaseFile)
			before, err := os.ReadFile(paths.DatabaseFile) //nolint:gosec // exact test-owned database path
			if err != nil {
				t.Fatalf("read unsafe database before daemon: %v", err)
			}
			beforeInfo, err := os.Lstat(paths.DatabaseFile)
			if err != nil {
				t.Fatalf("stat unsafe database before daemon: %v", err)
			}

			ctx, cancel := context.WithCancel(context.Background())
			done := make(chan error, 1)
			go func() { done <- runDaemonWithPaths(ctx, paths, purpose) }()
			client := waitForTestDaemon(t, paths, purpose)
			var endpoints ipc.ProviderEndpointsResponse
			if err := client.Call(context.Background(), daemon.ProviderEndpoints, struct{}{}, &endpoints); err != nil {
				t.Fatalf("list endpoints in degraded mode: %v", err)
			}
			if len(endpoints.Endpoints) != 1 || endpoints.Endpoints[0].Kind != "local" {
				t.Fatalf("degraded endpoints = %#v", endpoints.Endpoints)
			}
			var workspaces workspace.ListResponse
			err = client.Call(context.Background(), daemon.WorkspaceList, workspace.ListRequest{}, &workspaces)
			var remoteError *daemon.RemoteError
			if !errors.As(err, &remoteError) || remoteError.RPC.Code != domain.CodeUnsupported {
				t.Fatalf("degraded WorkspaceList error = %v, want unsupported mutation store", err)
			}
			_ = client.Close()
			cancel()
			select {
			case err := <-done:
				if err != nil {
					t.Fatalf("stop degraded daemon: %v", err)
				}
			case <-time.After(5 * time.Second):
				t.Fatal("degraded daemon did not stop")
			}

			after, err := os.ReadFile(paths.DatabaseFile) //nolint:gosec // exact test-owned database path
			if err != nil {
				t.Fatalf("read unsafe database after daemon: %v", err)
			}
			afterInfo, err := os.Lstat(paths.DatabaseFile)
			if err != nil {
				t.Fatalf("stat unsafe database after daemon: %v", err)
			}
			if !reflect.DeepEqual(after, before) || afterInfo.Mode() != beforeInfo.Mode() || afterInfo.Size() != beforeInfo.Size() || !afterInfo.ModTime().Equal(beforeInfo.ModTime()) {
				t.Fatalf("degraded daemon mutated unsafe database: before=%v after=%v", beforeInfo, afterInfo)
			}
			for _, forbidden := range []string{paths.LogFile, filepath.Join(paths.StateDir, "workspaces")} {
				if _, err := os.Lstat(forbidden); !errors.Is(err, os.ErrNotExist) {
					t.Fatalf("degraded daemon created persistent path %q: %v", forbidden, err)
				}
			}
		})
	}
}

func corruptProjectDatabase(t *testing.T, path string) {
	t.Helper()
	file, err := os.OpenFile(path, os.O_RDWR, 0) //nolint:gosec // exact test-owned database path
	if err != nil {
		t.Fatalf("open database for corruption: %v", err)
	}
	if _, err := file.WriteAt(make([]byte, 256), 100); err != nil {
		_ = file.Close()
		t.Fatalf("corrupt database page: %v", err)
	}
	if err := file.Sync(); err != nil {
		_ = file.Close()
		t.Fatalf("sync corrupt database: %v", err)
	}
	if err := file.Close(); err != nil {
		t.Fatalf("close corrupt database: %v", err)
	}
}

func addNewerSchemaHistory(t *testing.T, path string) {
	t.Helper()
	database, err := sql.Open("sqlite", "file:"+path)
	if err != nil {
		t.Fatalf("open database for newer history: %v", err)
	}
	if _, err := database.Exec("INSERT INTO schema_migrations(version, name, sha256, applied_at) VALUES(2, 'future', ?, '2026-07-16T00:50:00Z')", strings.Repeat("a", 64)); err != nil {
		_ = database.Close()
		t.Fatalf("insert newer history: %v", err)
	}
	if _, err := database.Exec("PRAGMA wal_checkpoint(TRUNCATE)"); err != nil {
		_ = database.Close()
		t.Fatalf("truncate newer-history WAL: %v", err)
	}
	if err := database.Close(); err != nil {
		t.Fatalf("close newer-history database: %v", err)
	}
}

func degradedStatePaths(t *testing.T) (platform.Paths, platform.ValidationPurpose) {
	t.Helper()
	runtimeRoot, err := os.MkdirTemp("/tmp", "amsftp-degraded-")
	if err != nil {
		t.Fatalf("create degraded runtime root: %v", err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(runtimeRoot) })
	if err := os.Chmod(runtimeRoot, 0o700); err != nil { //nolint:gosec // private runtime root
		t.Fatalf("set degraded runtime mode: %v", err)
	}
	persistent := testkit.PersistentTempDir(t)
	stateDir := filepath.Join(persistent, "state")
	paths := platform.Paths{
		StateDir: stateDir, DatabaseFile: filepath.Join(stateDir, "amsftp.db"),
		LogFile: filepath.Join(persistent, "log", "daemon.jsonl"), RuntimeDir: runtimeRoot,
		ControlSocket: filepath.Join(runtimeRoot, "control-v1.sock"), LockFile: filepath.Join(runtimeRoot, "daemon.lock"),
	}
	return paths, platform.ValidateRuntimeFallback
}

func waitForTestDaemon(t *testing.T, paths platform.Paths, purpose platform.ValidationPurpose) *daemon.Client {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	var client *daemon.Client
	var err error
	for time.Now().Before(deadline) {
		attemptCtx, stop := context.WithTimeout(context.Background(), 200*time.Millisecond)
		client, err = connectExisting(attemptCtx, paths, purpose)
		stop()
		if err == nil {
			return client
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("connect degraded daemon: %v", err)
	return nil
}
