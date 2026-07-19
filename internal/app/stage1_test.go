package app

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/TyrantLucifer/awesome-mac-sftp/internal/daemon"
	"github.com/TyrantLucifer/awesome-mac-sftp/internal/domain"
	"github.com/TyrantLucifer/awesome-mac-sftp/internal/ipc"
	"github.com/TyrantLucifer/awesome-mac-sftp/internal/platform"
	builtinpreview "github.com/TyrantLucifer/awesome-mac-sftp/internal/preview"
	"github.com/TyrantLucifer/awesome-mac-sftp/internal/testkit"
	"github.com/TyrantLucifer/awesome-mac-sftp/internal/tui"
	"github.com/TyrantLucifer/awesome-mac-sftp/internal/workspace"
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

func TestClientErrorMessageAppendsSafeRemoteCorrelation(t *testing.T) {
	requestID := domain.RequestID("req_aaaaaaaaaaaaaaaaaaaaaaaaaa")
	remote := &daemon.RemoteError{RequestID: requestID, RPC: ipc.RPCError{
		Code:    domain.CodePermissionDenied,
		Message: "access denied",
		Retry:   domain.RetryAdvice{Kind: domain.RetryNever},
		Effect:  domain.EffectNone,
	}}
	message := clientErrorMessage(remote)
	for _, want := range []string{"access denied", string(requestID), "error_code=permission_denied"} {
		if !strings.Contains(message, want) {
			t.Fatalf("clientErrorMessage() = %q, want %q", message, want)
		}
	}
	local := errors.New("local failure")
	if got := clientErrorMessage(local); got != local.Error() {
		t.Fatalf("local clientErrorMessage() = %q", got)
	}
}

func TestPaneConnectionAttemptsCancelAndRejectOlderResult(t *testing.T) {
	var attempts paneConnectionAttempts
	ctx := context.Background()
	firstCtx, firstEpoch := attempts.Begin(ctx, tui.Left)
	secondCtx, secondEpoch := attempts.Begin(ctx, tui.Left)

	select {
	case <-firstCtx.Done():
	default:
		t.Fatal("new connection attempt did not cancel the older attempt")
	}
	if secondCtx.Err() != nil {
		t.Fatalf("new connection context is already canceled: %v", secondCtx.Err())
	}
	if attempts.Accept(tui.Left, firstEpoch) {
		t.Fatal("older connection result was accepted")
	}
	if !attempts.Accept(tui.Left, secondEpoch) {
		t.Fatal("current connection result was rejected")
	}
	if attempts.Accept(tui.Left, secondEpoch) {
		t.Fatal("completed connection result was accepted twice")
	}
}

func TestListingResultCurrentRejectsStaleGeneration(t *testing.T) {
	endpointID := domain.EndpointID("ep_aaaaaaaaaaaaaaaaaaaaaaaaaa")
	location, err := domain.NewLocation(endpointID, "/tmp")
	if err != nil {
		t.Fatal(err)
	}
	model := tui.NewModel(
		tui.NewPaneState(domain.Endpoint{ID: endpointID, Kind: domain.EndpointLocal}, location),
		tui.NewPaneState(domain.Endpoint{ID: endpointID, Kind: domain.EndpointLocal}, location),
	)
	model, _ = tui.Reduce(model, tui.BeginListing{Pane: tui.Left, Generation: 8, Location: location})
	if listingResultCurrent(model, tui.Left, 7) {
		t.Fatal("stale listing result was accepted")
	}
	if !listingResultCurrent(model, tui.Left, 8) {
		t.Fatal("current listing result was rejected")
	}
}

func TestPlanPreviewReadRoutesHeadTailAndAbsoluteRange(t *testing.T) {
	const fileSize = uint64(100 * 1024 * 1024 * 1024)
	const rangeOffset = uint64(50 * 1024 * 1024 * 1024)
	wantRange, err := builtinpreview.PlanRange(fileSize, rangeOffset, builtinpreview.ReadChunkBytes)
	if err != nil {
		t.Fatal(err)
	}
	tests := []struct {
		mode   builtinpreview.ReadMode
		offset uint64
		want   builtinpreview.ReadPlan
	}{
		{mode: builtinpreview.ReadHead, want: builtinpreview.PlanHead(fileSize)},
		{mode: builtinpreview.ReadTail, want: builtinpreview.PlanTail(fileSize)},
		{mode: builtinpreview.ReadRange, offset: rangeOffset, want: wantRange},
	}
	for _, test := range tests {
		got, err := planPreviewRead(fileSize, test.mode, test.offset)
		if err != nil || got != test.want {
			t.Fatalf("planPreviewRead(%q,%d) = %#v, %v; want %#v", test.mode, test.offset, got, err, test.want)
		}
	}
	if _, err := planPreviewRead(fileSize, "invalid", 0); err == nil {
		t.Fatal("invalid preview mode succeeded")
	}
}

func TestTerminalImageOutputAcceptsOnlyTheCurrentVisiblePreviewIdentity(t *testing.T) {
	endpoint := domain.Endpoint{ID: "ep_aaaaaaaaaaaaaaaaaaaaaaaaaa", Kind: domain.EndpointLocal}
	location := domain.Location{EndpointID: endpoint.ID, Path: "/image.png"}
	model := tui.NewModel(tui.NewPaneState(endpoint, location), tui.NewPaneState(endpoint, location))
	model.Drawer.Mode = tui.DrawerPreview
	version := "v1"
	source, err := builtinpreview.FreezeSource(location, domain.Fingerprint{VersionID: &version})
	if err != nil {
		t.Fatal(err)
	}
	identity := tui.PreviewRequestIdentity{RequestID: "req_aaaaaaaaaaaaaaaaaaaaaaaaaa", Pane: tui.Left, Source: source, UIGeneration: 7}
	model, _ = tui.Reduce(model, tui.BeginPreview{Generation: 7, Location: location, Identity: identity, View: builtinpreview.ViewAuto})
	current := tui.PreviewTerminalImage{Generation: 7, Identity: identity, Protocol: builtinpreview.ImageProtocolKitty, Data: []byte("bounded")}
	if !terminalImageCurrent(model, current) {
		t.Fatal("current terminal image was rejected")
	}
	stale := current
	stale.Generation = 6
	if terminalImageCurrent(model, stale) {
		t.Fatal("stale terminal image was accepted")
	}
	model.Drawer.Mode = tui.DrawerClosed
	if terminalImageCurrent(model, current) {
		t.Fatal("hidden preview terminal image was accepted")
	}
}

func TestEndpointSwitchFailurePreservesCommittedConnectionState(t *testing.T) {
	endpointID := domain.EndpointID("ep_aaaaaaaaaaaaaaaaaaaaaaaaaa")
	location, err := domain.NewLocation(endpointID, "/tmp")
	if err != nil {
		t.Fatal(err)
	}
	model := tui.NewModel(
		tui.NewPaneState(domain.Endpoint{ID: endpointID, Kind: domain.EndpointLocal}, location),
		tui.NewPaneState(domain.Endpoint{ID: endpointID, Kind: domain.EndpointLocal}, location),
	)
	if got := connectionFailureState(model, tui.Left, true); got != domain.StateReady {
		t.Fatalf("switch failure state = %q, want committed ready", got)
	}
	if got := connectionFailureState(model, tui.Left, false); got != domain.StateFailed {
		t.Fatalf("recovery failure state = %q, want failed", got)
	}
}

func TestCapabilitySnapshotFromWirePreservesCompleteSessionState(t *testing.T) {
	response := ipc.ProviderSnapshotResponse{
		EndpointID: "ep_aaaaaaaaaaaaaaaaaaaaaaaaaa",
		SessionID:  "sess_aaaaaaaaaaaaaaaaaaaaaaaaaa",
		Generation: 4,
		Complete:   false,
		Items: []ipc.WireCapability{
			{Name: "metadata", Version: 2, Constraints: []domain.CapabilityConstraint{{Name: "precision", Value: "second"}}},
			{Name: "read", Version: 1},
		},
	}
	snapshot, err := capabilitySnapshotFromWire(response)
	if err != nil {
		t.Fatal(err)
	}
	if snapshot.Revision.SessionID != "sess_aaaaaaaaaaaaaaaaaaaaaaaaaa" || snapshot.Revision.Generation != 4 || snapshot.Complete || len(snapshot.Items) != 2 {
		t.Fatalf("snapshot = %#v", snapshot)
	}
	metadata, ok := snapshot.Lookup("metadata")
	if !ok || metadata.Version != 2 || len(metadata.Constraints) != 1 || metadata.Constraints[0].Value != "second" {
		t.Fatalf("metadata capability = %#v, %t", metadata, ok)
	}
}

func TestWorkspaceDocumentCapturesStableTwoPaneState(t *testing.T) {
	leftID := domain.EndpointID("ep_aaaaaaaaaaaaaaaaaaaaaaaaaa")
	rightID := domain.EndpointID("ep_bbbbbbbbbbbbbbbbbbbbbbbbbb")
	leftLocation, err := domain.NewLocation(leftID, "/Users/test")
	if err != nil {
		t.Fatal(err)
	}
	rightLocation, err := domain.NewLocation(rightID, "/srv/release")
	if err != nil {
		t.Fatal(err)
	}
	model := tui.NewModel(
		tui.NewPaneState(domain.Endpoint{ID: leftID, Kind: domain.EndpointLocal, DisplayName: "local"}, leftLocation),
		tui.NewPaneState(domain.Endpoint{ID: rightID, Kind: domain.EndpointSSH, DisplayName: "prod", SSHHostAlias: "prod"}, rightLocation),
	)
	model.Active = tui.Right
	model, _ = tui.Reduce(model, tui.SetFilter{Pane: tui.Left, Query: "report"})
	rightPane := model.Panes[tui.Right]
	rightPane.Sort = tui.SortState{Key: tui.SortModified, Descending: true}
	rightPane.ShowHidden = true
	model.Panes[tui.Right] = rightPane
	model.Drawer = tui.DrawerState{Mode: tui.DrawerLog, Focus: tui.FocusPane, Rows: 8}
	now := time.Date(2026, 7, 15, 12, 30, 0, 0, time.UTC)
	document, err := workspaceDocument(model, now, workspace.CachePinnedOffline)
	if err != nil {
		t.Fatal(err)
	}
	if document.UpdatedAt != now || document.Layout.ActivePane != 1 || document.Panes[0].Path != "/Users/test" || document.Panes[0].Filter != "report" {
		t.Fatalf("document = %#v", document)
	}
	if document.Panes[1].Endpoint.Kind != domain.EndpointSSH || document.Panes[1].Endpoint.SSHHostAlias != "prod" || document.Panes[1].Path != "/srv/release" {
		t.Fatalf("remote pane = %#v", document.Panes[1])
	}
	if document.Panes[1].Sort.Key != workspace.SortModified || document.Panes[1].Sort.Direction != workspace.SortDescending || !document.Panes[1].ShowHidden {
		t.Fatalf("remote pane preferences = %#v", document.Panes[1])
	}
	if document.Layout.Drawer != (workspace.DrawerState{Mode: workspace.DrawerLog, Focus: workspace.FocusPane, Rows: 8}) || document.CachePolicy != workspace.CachePinnedOffline {
		t.Fatalf("workspace drawer/cache = %#v/%q", document.Layout.Drawer, document.CachePolicy)
	}
	restored := tui.NewPaneState(domain.Endpoint{ID: rightID, Kind: domain.EndpointSSH, SSHHostAlias: "prod"}, rightLocation)
	applyWorkspacePanePreferences(&restored, document.Panes[1])
	if restored.Sort != rightPane.Sort || !restored.ShowHidden {
		t.Fatalf("restored preferences = %#v", restored)
	}
}

func TestApplyWorkspaceLayoutRestoresDrawerWithoutSensitiveContent(t *testing.T) {
	model := testModelForWorkspaceLayout(t)
	applyWorkspaceLayout(&model, workspace.LayoutState{
		ActivePane: 1,
		Drawer:     workspace.DrawerState{Mode: workspace.DrawerJobs, Focus: workspace.FocusDrawer, Rows: 7},
	})
	if model.Active != tui.Right || model.Drawer != (tui.DrawerState{Mode: tui.DrawerJobs, Focus: tui.FocusDrawer, Rows: 7}) {
		t.Fatalf("restored layout = active:%v drawer:%#v", model.Active, model.Drawer)
	}
	if len(model.Jobs) != 0 || len(model.Diagnostics) != 0 || len(model.Preview.Data) != 0 {
		t.Fatalf("workspace restored transient drawer bodies: %#v", model)
	}
}

func testModelForWorkspaceLayout(t *testing.T) tui.Model {
	t.Helper()
	leftID := domain.EndpointID("ep_aaaaaaaaaaaaaaaaaaaaaaaaaa")
	rightID := domain.EndpointID("ep_bbbbbbbbbbbbbbbbbbbbbbbbbb")
	left, err := domain.NewLocation(leftID, "/left")
	if err != nil {
		t.Fatal(err)
	}
	right, err := domain.NewLocation(rightID, "/right")
	if err != nil {
		t.Fatal(err)
	}
	return tui.NewModel(
		tui.NewPaneState(domain.Endpoint{ID: leftID, Kind: domain.EndpointLocal}, left),
		tui.NewPaneState(domain.Endpoint{ID: rightID, Kind: domain.EndpointLocal}, right),
	)
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
	persistent := testkit.PersistentTempDir(t)
	paths := platform.Paths{StateDir: filepath.Join(persistent, "state"), LogFile: filepath.Join(persistent, "log", "daemon.jsonl"), CacheDir: filepath.Join(persistent, "cache"), RuntimeDir: base, ControlSocket: filepath.Join(base, "control-v1.sock"), LockFile: filepath.Join(base, "daemon.lock")}
	purpose := platform.ValidateRuntimeFallback
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- runDaemonWithPaths(ctx, paths, purpose) }()
	var client *daemon.Client
	var err error
	deadline := time.Now().Add(30 * time.Second)
	for time.Now().Before(deadline) {
		select {
		case daemonErr := <-done:
			cancel()
			t.Fatalf("daemon exited before readiness: %v", daemonErr)
		default:
		}
		attemptCtx, stop := context.WithTimeout(context.Background(), 200*time.Millisecond)
		client, err = connectExistingAs(attemptCtx, paths, purpose, "0.9.0-stage5", "stage5-compatible-client")
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
	if client.Info().Protocol != (ipc.ProtocolVersion{Major: ipc.ProtocolMajor, Minor: ipc.ProtocolMinor}) {
		t.Fatalf("prior-version client protocol = %#v", client.Info().Protocol)
	}
	socketBefore, err := os.Lstat(paths.ControlSocket)
	if err != nil {
		t.Fatal(err)
	}
	rejectFutureDaemonProtocol(t, paths, purpose)
	var endpointsAfterRejectedUpgrade ipc.ProviderEndpointsResponse
	if err := client.Call(context.Background(), daemon.ProviderEndpoints, struct{}{}, &endpointsAfterRejectedUpgrade); err != nil {
		t.Fatalf("prior-version client after rejected future protocol: %v", err)
	}
	if len(endpointsAfterRejectedUpgrade.Endpoints) != 1 || endpointsAfterRejectedUpgrade.Endpoints[0].Kind != "local" {
		t.Fatalf("endpoints after rejected future protocol = %#v", endpointsAfterRejectedUpgrade.Endpoints)
	}
	socketAfter, err := os.Lstat(paths.ControlSocket)
	if err != nil {
		t.Fatal(err)
	}
	if !os.SameFile(socketBefore, socketAfter) {
		t.Fatal("daemon control socket was replaced after an incompatible hello")
	}
	for _, cachePath := range []string{paths.CacheDir, filepath.Join(paths.CacheDir, "content-v1", "blobs", "sha256")} {
		metadata, statErr := os.Lstat(cachePath)
		if statErr != nil || !metadata.IsDir() || metadata.Mode().Perm() != 0o700 {
			t.Fatalf("cache path %q = %#v, %v", cachePath, metadata, statErr)
		}
	}
	var diagnostics daemon.DiagnosticListResponse
	if err := client.Call(context.Background(), daemon.DiagnosticList, daemon.DiagnosticListRequest{Limit: 256}, &diagnostics); err != nil {
		t.Fatalf("list bounded diagnostics: %v", err)
	}
	if len(diagnostics.Records) == 0 || len(diagnostics.Records) > 256 {
		t.Fatalf("diagnostic records = %d, want 1..256", len(diagnostics.Records))
	}
	var workspaces workspace.ListResponse
	if err := client.Call(context.Background(), daemon.WorkspaceList, workspace.ListRequest{}, &workspaces); err != nil {
		t.Fatalf("list daemon workspaces: %v", err)
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
	controlRuntime := daemonCommandRuntime{
		probe: func(probeCtx context.Context) (daemonControlClient, bool, error) {
			return probeDaemon(probeCtx, paths, purpose)
		},
		waitStopped: func(waitCtx context.Context) error {
			return waitForDaemonShutdown(waitCtx, paths, purpose)
		},
	}
	var statusOutput strings.Builder
	if err := runDaemonCommandWithRuntime(context.Background(), []string{"status", "--format", "json"}, &statusOutput, controlRuntime); err != nil {
		cancel()
		t.Fatalf("daemon status command: %v", err)
	}
	if !strings.Contains(statusOutput.String(), `"running":true`) || !strings.Contains(statusOutput.String(), `"state":"running"`) {
		cancel()
		t.Fatalf("daemon status output = %q", statusOutput.String())
	}
	var stopOutput strings.Builder
	if err := runDaemonCommandWithRuntime(context.Background(), []string{"stop", "--confirm", "stop", "--format", "json"}, &stopOutput, controlRuntime); err != nil {
		cancel()
		t.Fatalf("daemon stop command: %v", err)
	}
	if !strings.Contains(stopOutput.String(), `"running":false`) || !strings.Contains(stopOutput.String(), `"state":"stopped"`) {
		cancel()
		t.Fatalf("daemon stop output = %q", stopOutput.String())
	}
	replacementLock, err := platform.AcquireInstanceLock(paths.LockFile, purpose)
	if err != nil {
		cancel()
		t.Fatalf("daemon stop returned before instance lock release: %v", err)
	}
	if err := replacementLock.Close(); err != nil {
		cancel()
		t.Fatalf("release replacement instance lock: %v", err)
	}
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(5 * time.Second):
		cancel()
		t.Fatal("daemon did not stop")
	}
	cancel()
	if _, err := os.Lstat(paths.ControlSocket); !os.IsNotExist(err) {
		t.Fatalf("socket remains after shutdown: %v", err)
	}
}

func TestDaemonAutostartRequiresProvenSocketAbsence(t *testing.T) {
	root := testkit.PersistentTempDir(t)
	existing := filepath.Join(root, "existing-control.sock")
	if err := os.WriteFile(existing, []byte("preserve"), 0o600); err != nil {
		t.Fatal(err)
	}
	connectErr := errors.New("existing daemon handshake failed")
	if err := requireAbsentControlSocketForAutostart(existing, connectErr); !errors.Is(err, connectErr) {
		t.Fatalf("existing socket error = %v, want wrapped connect failure", err)
	}
	content, err := os.ReadFile(existing) //nolint:gosec // exact test-owned path proves failed autostart preserves bytes
	if err != nil {
		t.Fatal(err)
	}
	if string(content) != "preserve" {
		t.Fatalf("existing socket stand-in changed to %q", content)
	}

	absent := filepath.Join(root, "absent-control.sock")
	if err := requireAbsentControlSocketForAutostart(absent, connectErr); err != nil {
		t.Fatalf("proven-absent socket rejected: %v", err)
	}

	uninspectable := filepath.Join(root, strings.Repeat("x", 5000))
	if err := requireAbsentControlSocketForAutostart(uninspectable, connectErr); err == nil || errors.Is(err, connectErr) {
		t.Fatalf("uninspectable socket error = %v, want independent inspection failure", err)
	}
}

func TestDaemonRejectsInvalidConfigurationBeforePersistentStateMutation(t *testing.T) {
	runtimeRoot := t.TempDir()
	persistent := testkit.PersistentTempDir(t)
	for _, directory := range []string{runtimeRoot, persistent} {
		if err := os.Chmod(directory, 0o700); err != nil { // #nosec G302 -- owner-only roots are required by the daemon contract.
			t.Fatal(err)
		}
	}
	configPath := filepath.Join(persistent, "config.json")
	if err := os.WriteFile(configPath, []byte(`{"schema_version":1,"cache":{"global_entries":5000}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	paths := platform.Paths{
		ConfigFile: configPath, StateDir: filepath.Join(persistent, "state"), CacheDir: filepath.Join(persistent, "cache"),
		LogFile: filepath.Join(persistent, "log", "daemon.jsonl"), RuntimeDir: runtimeRoot,
		ControlSocket: filepath.Join(runtimeRoot, "control-v1.sock"), LockFile: filepath.Join(runtimeRoot, "daemon.lock"),
	}
	err := runDaemonWithPaths(context.Background(), paths, platform.ValidateRuntimeFallback)
	if err == nil || !strings.Contains(err.Error(), configPath) || !strings.Contains(err.Error(), "cache.global_entries") {
		t.Fatalf("daemon config error = %v", err)
	}
	if _, err := os.Lstat(paths.StateDir); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("state directory mutated before config validation: %v", err)
	}
}

func TestParseDaemonArgsRequiresOneExactExplicitMigrationResumeFlag(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		args       []string
		wantResume bool
		wantError  bool
	}{
		{name: "ordinary daemon"},
		{name: "explicit resume", args: []string{"--resume-migration"}, wantResume: true},
		{name: "unknown flag", args: []string{"--resume"}, wantError: true},
		{name: "duplicate flag", args: []string{"--resume-migration", "--resume-migration"}, wantError: true},
		{name: "positional argument", args: []string{"resume-migration"}, wantError: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			options, err := parseDaemonArgs(tt.args)
			if (err != nil) != tt.wantError {
				t.Fatalf("parseDaemonArgs(%q) error = %v, wantError=%t", tt.args, err, tt.wantError)
			}
			if options.explicitMigrationResume != tt.wantResume {
				t.Fatalf("parseDaemonArgs(%q) options = %#v", tt.args, options)
			}
		})
	}
}

func connectExisting(ctx context.Context, paths platform.Paths, purpose platform.ValidationPurpose) (*daemon.Client, error) {
	return connectExistingAs(ctx, paths, purpose, "test", "test-client")
}

func connectExistingAs(ctx context.Context, paths platform.Paths, purpose platform.ValidationPurpose, clientVersion, instanceID string) (*daemon.Client, error) {
	connection, err := platform.DialControlSocket(ctx, paths.ControlSocket, purpose)
	if err != nil {
		return nil, err
	}
	return daemon.NewClient(ctx, connection, clientVersion, instanceID)
}

func rejectFutureDaemonProtocol(t *testing.T, paths platform.Paths, purpose platform.ValidationPurpose) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	connection, err := platform.DialControlSocket(ctx, paths.ControlSocket, purpose)
	if err != nil {
		t.Fatal(err)
	}
	defer connection.Close()
	writer, err := ipc.NewWriter(connection, ipc.MaxFrameBytes)
	if err != nil {
		t.Fatal(err)
	}
	reader, err := ipc.NewReader(connection, ipc.MaxFrameBytes)
	if err != nil {
		t.Fatal(err)
	}
	payload, err := json.Marshal(ipc.HelloRequest{
		ClientVersion:    "2.0.0-future",
		ClientInstanceID: "future-client",
		Protocols:        []ipc.VersionRange{{Major: ipc.ProtocolMajor + 1, MinMinor: 0, MaxMinor: 0}},
	})
	if err != nil {
		t.Fatal(err)
	}
	request, err := ipc.EncodeEnvelope(ipc.Envelope{
		Protocol:  ipc.ProtocolVersion{Major: ipc.ProtocolMajor, Minor: ipc.ProtocolMinor},
		Kind:      ipc.KindRequest,
		Name:      daemon.RequestHello,
		RequestID: domain.RequestID("req_dddddddddddddddddddddddddd"),
		Payload:   payload,
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := writer.WriteFrame(request); err != nil {
		t.Fatal(err)
	}
	frame, err := reader.ReadFrame()
	if err != nil {
		t.Fatal(err)
	}
	response, err := ipc.DecodeEnvelope(frame)
	if err != nil {
		t.Fatal(err)
	}
	if response.Error == nil || response.Error.Code != domain.CodeProtocolIncompatible || response.Error.Retry.Kind != domain.RetryNever || response.Error.Effect != domain.EffectNone {
		t.Fatalf("future protocol response = %#v", response.Error)
	}
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
	colonPaths := []string{"/tmp/local:archive", "./local:archive", "../local:archive"}
	for _, value := range colonPaths {
		parsed, err := startLocations([]string{value})
		if err != nil {
			t.Fatalf("startLocations(%q): %v", value, err)
		}
		if parsed[0].host != "" || !filepath.IsAbs(parsed[0].path) {
			t.Fatalf("colon local %q = %#v", value, parsed[0])
		}
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

func TestWorkspaceStartLocationsUseStableEndpointReferences(t *testing.T) {
	document := workspace.Document{
		SchemaVersion: workspace.SchemaVersion,
		UpdatedAt:     time.Now().UTC(),
		Panes: [2]workspace.Pane{
			{Endpoint: workspace.EndpointRef{Kind: domain.EndpointLocal}, Path: "/local", Sort: workspace.SortState{Key: workspace.SortName, Direction: workspace.SortAscending}},
			{Endpoint: workspace.EndpointRef{Kind: domain.EndpointSSH, SSHHostAlias: "work"}, Path: "/remote", Sort: workspace.SortState{Key: workspace.SortName, Direction: workspace.SortAscending}},
		},
		CachePolicy: workspace.CacheEphemeral,
	}
	got, err := workspaceStartLocations(document)
	if err != nil {
		t.Fatal(err)
	}
	want := [2]startLocation{{path: "/local"}, {host: "work", path: "/remote"}}
	if got != want {
		t.Fatalf("workspace locations = %#v, want %#v", got, want)
	}
}
