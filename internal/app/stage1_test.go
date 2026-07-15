package app

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"testing"
	"time"

	"github.com/TyrantLucifer/awesome-mac-sftp/internal/daemon"
	"github.com/TyrantLucifer/awesome-mac-sftp/internal/domain"
	"github.com/TyrantLucifer/awesome-mac-sftp/internal/ipc"
	"github.com/TyrantLucifer/awesome-mac-sftp/internal/platform"
)

func TestSSHConnectStageErrorPreservesSafeStageAndClassification(t *testing.T) {
	cause := errors.New("private transport detail")
	err := sshConnectStageError("establish OpenSSH SFTP session", domain.CodeTransportInterrupted, domain.RetryAfterReconnect, cause)
	var operationError *domain.OpError
	if !errors.As(err, &operationError) {
		t.Fatalf("error = %T, want *domain.OpError", err)
	}
	if operationError.Operation != "connect_ssh" || operationError.Message != "establish OpenSSH SFTP session" {
		t.Fatalf("public error = %#v", operationError)
	}
	if operationError.Code != domain.CodeTransportInterrupted || operationError.Retry.Kind != domain.RetryAfterReconnect || operationError.Effect != domain.EffectNone {
		t.Fatalf("classification = %#v", operationError)
	}
	if !errors.Is(err, cause) {
		t.Fatal("private cause was not retained for daemon-local diagnostics")
	}
	if operationError.Error() == cause.Error() {
		t.Fatal("public error exposed the private cause")
	}
}

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

func TestStartLocationsParsesLocalAndRemote(t *testing.T) {
	locations, err := startLocations([]string{".", "work-alias:/srv/data"})
	if err != nil {
		t.Fatal(err)
	}
	if locations[0].host != "" || !filepath.IsAbs(locations[0].path) {
		t.Fatalf("local = %#v", locations[0])
	}
	if locations[1] != (startLocation{host: "work-alias", path: "/srv/data"}) {
		t.Fatalf("remote = %#v", locations[1])
	}
	for _, value := range []string{"-bad:/", "host:relative", "host\nname:/"} {
		if _, err := startLocations([]string{value}); err == nil {
			t.Fatalf("startLocations(%q) error = nil", value)
		}
	}
}

func TestInitialPaneStateRepresentsRemoteWithoutConnectingIt(t *testing.T) {
	local := domain.Endpoint{ID: "ep_aaaaaaaaaaaaaaaaaaaaaaaaaa", Kind: domain.EndpointLocal, DisplayName: "local"}
	pane, err := initialPaneState(local, startLocation{host: "work-alias", path: "/srv/data"})
	if err != nil {
		t.Fatal(err)
	}
	if pane.Endpoint.ID != local.ID || pane.Endpoint.Kind != domain.EndpointLocal || pane.Endpoint.DisplayName != "connecting work-alias" {
		t.Fatalf("placeholder endpoint = %#v", pane.Endpoint)
	}
	if pane.Location.EndpointID != local.ID || pane.Location.Path != "/srv/data" || !pane.Listing.Loading {
		t.Fatalf("placeholder pane = %#v", pane)
	}
}
