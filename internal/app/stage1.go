package app

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"strings"
	"time"

	"github.com/TyrantLucifer/awesome-mac-sftp/internal/auth"
	"github.com/TyrantLucifer/awesome-mac-sftp/internal/buildinfo"
	"github.com/TyrantLucifer/awesome-mac-sftp/internal/daemon"
	"github.com/TyrantLucifer/awesome-mac-sftp/internal/domain"
	"github.com/TyrantLucifer/awesome-mac-sftp/internal/ipc"
	"github.com/TyrantLucifer/awesome-mac-sftp/internal/platform"
	"github.com/TyrantLucifer/awesome-mac-sftp/internal/provider"
	"github.com/TyrantLucifer/awesome-mac-sftp/internal/provider/localfs"
	sftpprovider "github.com/TyrantLucifer/awesome-mac-sftp/internal/provider/sftp"
	"github.com/TyrantLucifer/awesome-mac-sftp/internal/transport/openssh"
	"github.com/TyrantLucifer/awesome-mac-sftp/internal/tui"
	"github.com/gdamore/tcell/v3"
)

const daemonReadyTimeout = 5 * time.Second
const authenticationTimeout = 2 * time.Minute

func DefaultHandlers() Handlers {
	unsupported := func(context.Context, []string, io.Writer, io.Writer) error {
		return errors.New("role is not available in this stage")
	}
	return Handlers{Client: runClient, Daemon: runDaemon, Askpass: runAskpass, Helper: unsupported}
}

func runtimePaths() (platform.Paths, platform.ValidationPurpose, error) {
	paths, _, err := platform.ResolvePaths(platform.Overrides{})
	if err != nil {
		return platform.Paths{}, 0, err
	}
	paths, _, err = platform.PrepareRuntimeDirectory(paths, true)
	if err != nil {
		return platform.Paths{}, 0, err
	}
	return paths, platform.RuntimeValidationPurpose(paths), nil
}

func runDaemon(ctx context.Context, _ []string, _ io.Writer, _ io.Writer) error {
	paths, purpose, err := runtimePaths()
	if err != nil {
		return err
	}
	return runDaemonWithPaths(ctx, paths, purpose)
}

func runDaemonWithPaths(ctx context.Context, paths platform.Paths, purpose platform.ValidationPurpose) error {
	lock, err := platform.AcquireInstanceLock(paths.LockFile, purpose)
	if errors.Is(err, platform.ErrInstanceLocked) {
		return nil
	}
	if err != nil {
		return err
	}
	defer lock.Close()
	if _, err := os.Lstat(paths.ControlSocket); err == nil {
		probeCtx, cancel := context.WithTimeout(ctx, 300*time.Millisecond)
		connection, probeErr := platform.DialControlSocket(probeCtx, paths.ControlSocket, purpose)
		if probeErr == nil {
			client, helloErr := daemon.NewClient(probeCtx, connection, buildinfo.Current().String(), "daemon-stale-probe")
			if helloErr == nil {
				_ = client.Close()
				cancel()
				return errors.New("another healthy daemon owns the control socket")
			}
			_ = connection.Close()
		}
		cancel()
		if err := platform.RemoveLockedControlSocket(paths.ControlSocket, purpose, lock); err != nil {
			return err
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	listener, err := platform.ListenControlSocket(paths.ControlSocket, purpose, lock)
	if err != nil {
		return err
	}
	defer func() { _ = listener.Close(); _ = os.Remove(paths.ControlSocket) }()
	generator := &domain.RandomGenerator{}
	endpointID, err := domain.NewEndpointID(generator)
	if err != nil {
		return err
	}
	sessionID, err := domain.NewSessionID(generator)
	if err != nil {
		return err
	}
	local, err := localfs.New(localfs.Config{Endpoint: domain.Endpoint{ID: endpointID, Kind: domain.EndpointLocal, DisplayName: "local"}, SessionID: sessionID, Root: "/"})
	if err != nil {
		return err
	}
	sessions, err := daemon.NewProviderSessions([]provider.Provider{local}, tui.PreviewByteLimit)
	if err != nil {
		return err
	}
	authBroker, err := auth.NewBroker(auth.Config{MaxPrompts: 8})
	if err != nil {
		return err
	}
	sessions.SetAuthBroker(authBroker)
	sessions.SetSSHConnector(func(connectCtx context.Context, hostAlias string) (provider.Provider, error) {
		attempt, err := authBroker.BeginAttempt(connectCtx, hostAlias, authenticationTimeout)
		if err != nil {
			return nil, err
		}
		defer attempt.Close()
		executable, err := os.Executable()
		if err != nil {
			return nil, fmt.Errorf("find askpass executable: %w", err)
		}
		if err := platform.ValidateExecutable(executable); err != nil {
			return nil, fmt.Errorf("validate askpass executable: %w", err)
		}
		environment, err := auth.OpenSSHEnvironment(os.Environ(), executable, attempt.Token())
		if err != nil {
			return nil, err
		}
		transport, err := openssh.Dial(connectCtx, openssh.Config{HostAlias: hostAlias, Environment: environment, Redact: []string{string(attempt.Token())}})
		if err != nil {
			return nil, err
		}
		remoteEndpointID, err := domain.NewEndpointID(generator)
		if err != nil {
			_ = transport.Close()
			return nil, err
		}
		remoteSessionID, err := domain.NewSessionID(generator)
		if err != nil {
			_ = transport.Close()
			return nil, err
		}
		implementation, err := sftpprovider.New(sftpprovider.Config{Endpoint: domain.Endpoint{ID: remoteEndpointID, Kind: domain.EndpointSSH, DisplayName: hostAlias, SSHHostAlias: hostAlias}, SessionID: remoteSessionID, Client: transport.Client(), Close: transport.Close})
		if err != nil {
			_ = transport.Close()
			return nil, err
		}
		return implementation, nil
	})
	server, err := daemon.NewServer(daemon.ServerConfig{BuildVersion: buildinfo.Current().String(), Epoch: string(sessionID), Sessions: sessions, MaxInFlight: 16, HandshakeTimeout: 2 * time.Second, VerifyPeer: func(conn net.Conn) error {
		unix, ok := conn.(*net.UnixConn)
		if !ok {
			return fmt.Errorf("unexpected peer connection %T", conn)
		}
		return platform.VerifyPeerUID(unix)
	}})
	if err != nil {
		return err
	}
	return daemon.Serve(ctx, listener, server)
}

func connectDaemon(ctx context.Context, paths platform.Paths, purpose platform.ValidationPurpose) (*daemon.Client, error) {
	connect := func() (*daemon.Client, error) {
		connection, err := platform.DialControlSocket(ctx, paths.ControlSocket, purpose)
		if err != nil {
			return nil, err
		}
		return daemon.NewClient(ctx, connection, buildinfo.Current().String(), fmt.Sprintf("client-%d", os.Getpid()))
	}
	if client, err := connect(); err == nil {
		return client, nil
	}
	executable, err := os.Executable()
	if err != nil {
		return nil, err
	}
	// #nosec G204 -- os.Executable returns this already-running, trusted binary; no user command is accepted.
	command := exec.Command(executable, "daemon")
	command.Stdin, command.Stdout, command.Stderr = nil, nil, nil
	if err := command.Start(); err != nil {
		return nil, fmt.Errorf("start daemon: %w", err)
	}
	_ = command.Process.Release()
	deadline := time.Now().Add(daemonReadyTimeout)
	var lastErr error
	for time.Now().Before(deadline) {
		if client, err := connect(); err == nil {
			return client, nil
		} else {
			lastErr = err
		}
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(25 * time.Millisecond):
		}
	}
	return nil, fmt.Errorf("daemon did not become ready: %w", lastErr)
}

func runClient(ctx context.Context, args []string, _ io.Writer, _ io.Writer) error {
	paths, purpose, err := runtimePaths()
	if err != nil {
		return err
	}
	client, err := connectDaemon(ctx, paths, purpose)
	if err != nil {
		return err
	}
	defer client.Close()
	var endpoints ipc.ProviderEndpointsResponse
	if err := client.Call(ctx, daemon.ProviderEndpoints, struct{}{}, &endpoints); err != nil {
		return err
	}
	if len(endpoints.Endpoints) != 1 {
		return fmt.Errorf("expected one local endpoint, got %d", len(endpoints.Endpoints))
	}
	endpointID, err := domain.ParseEndpointID(endpoints.Endpoints[0].ID)
	if err != nil {
		return err
	}
	locations, err := startLocations(args)
	if err != nil {
		return err
	}
	localEndpoint := domain.Endpoint{ID: endpointID, Kind: domain.EndpointLocal, DisplayName: "local"}
	left, err := initialPaneState(localEndpoint, locations[0])
	if err != nil {
		return err
	}
	right, err := initialPaneState(localEndpoint, locations[1])
	if err != nil {
		return err
	}
	model := tui.NewModel(left, right)
	screen, err := tcell.NewScreen()
	if err != nil {
		return err
	}
	if err := screen.Init(); err != nil {
		return err
	}
	defer screen.Fini()
	runCtx, stop := context.WithCancel(ctx)
	authClient, err := connectDaemon(runCtx, paths, purpose)
	if err != nil {
		stop()
		return err
	}
	defer func() {
		stop()
		_ = authClient.Close()
	}()
	events := screen.EventQ()
	actions := make(chan tui.Action, 32)
	authResolutions := make(chan tui.Intent, 1)
	authErrors := make(chan error, 1)
	go func() {
		if err := runAuthClaimLoop(runCtx, authClient, actions, authResolutions); err != nil && runCtx.Err() == nil {
			authErrors <- err
		}
	}()
	var generations [2]uint64
	var cancels [2]context.CancelFunc
	var previewGeneration uint64
	var previewCancel context.CancelFunc
	startIntent := func(intent tui.Intent) {
		switch intent.Kind {
		case tui.IntentAuthResolve:
			select {
			case authResolutions <- intent:
			case <-runCtx.Done():
			}
			return
		case tui.IntentPreview:
			if previewCancel != nil {
				previewCancel()
			}
			requestCtx, cancel := context.WithCancel(runCtx)
			previewCancel = cancel
			previewGeneration++
			generation := previewGeneration
			actions <- tui.BeginPreview{Generation: generation, Location: intent.Location}
			go func() { defer cancel(); previewLocation(requestCtx, client, generation, intent.Location, actions) }()
			return
		case tui.IntentList:
		default:
			return
		}
		pane := intent.Pane
		if cancels[pane] != nil {
			cancels[pane]()
		}
		requestCtx, cancel := context.WithCancel(runCtx)
		cancels[pane] = cancel
		generations[pane]++
		generation := generations[pane]
		actions <- tui.BeginListing{Pane: pane, Generation: generation, Location: intent.Location}
		go func() { defer cancel(); listLocation(requestCtx, client, pane, generation, intent.Location, actions) }()
	}
	type connectionResult struct {
		pane     tui.PaneID
		endpoint domain.Endpoint
		location domain.Location
		host     string
		err      error
	}
	connections := make(chan connectionResult, 2)
	for index, start := range locations {
		pane := tui.PaneID(index)
		if start.host == "" {
			startIntent(tui.Intent{Kind: tui.IntentList, Pane: pane, Location: model.Panes[pane].Location})
			continue
		}
		go func() {
			endpoint, location, connectErr := resolveStartLocation(runCtx, client, localEndpoint, start)
			select {
			case connections <- connectionResult{pane: pane, endpoint: endpoint, location: location, host: start.host, err: connectErr}:
			case <-runCtx.Done():
			}
		}()
	}
	for {
		tui.Render(tui.NewTCellSurface(screen), model, tui.RenderOptions{Overscan: 8})
		screen.Show()
		select {
		case <-runCtx.Done():
			return nil
		case err := <-authErrors:
			return err
		case result := <-connections:
			if result.err != nil {
				return fmt.Errorf("connect SSH host %q: %w", result.host, result.err)
			}
			var intents []tui.Intent
			model, intents = tui.Reduce(model, tui.PaneConnected{Pane: result.pane, Endpoint: result.endpoint, Location: result.location})
			for _, intent := range intents {
				startIntent(intent)
			}
		case action := <-actions:
			var intents []tui.Intent
			model, intents = tui.Reduce(model, action)
			for _, intent := range intents {
				startIntent(intent)
			}
		case event, open := <-events:
			if !open {
				return nil
			}
			if key, ok := event.(*tcell.EventKey); ok && (key.Key() == tcell.KeyCtrlC || model.Mode == tui.ModeNormal && key.Str() == "q") {
				return nil
			}
			action, ok := tui.TranslateTCellEvent(event, model.Mode)
			if !ok {
				continue
			}
			var intents []tui.Intent
			model, intents = tui.Reduce(model, action)
			for _, intent := range intents {
				startIntent(intent)
			}
		}
	}
}

func initialPaneState(local domain.Endpoint, start startLocation) (tui.PaneState, error) {
	endpoint := local
	if start.host != "" {
		endpoint.DisplayName = "connecting " + start.host
	}
	location, err := domain.NewLocation(local.ID, domain.CanonicalPath(start.path))
	if err != nil {
		return tui.PaneState{}, err
	}
	pane := tui.NewPaneState(endpoint, location)
	if start.host != "" {
		pane.Listing = tui.ListingState{Loading: true, Message: "connecting"}
	}
	return pane, nil
}

type startLocation struct{ host, path string }

func startLocations(args []string) ([2]startLocation, error) {
	var result [2]startLocation
	cwd, err := os.Getwd()
	if err != nil {
		return result, err
	}
	result = [2]startLocation{{path: cwd}, {path: cwd}}
	if len(args) > 2 {
		return result, errors.New("client accepts at most two locations")
	}
	for index, raw := range args {
		if host, remote, ok := strings.Cut(raw, ":"); ok {
			if _, err := openssh.Arguments(host); err != nil {
				return result, err
			}
			if remote == "" {
				remote = "/"
			}
			if !strings.HasPrefix(remote, "/") {
				return result, errors.New("remote path must be absolute")
			}
			result[index] = startLocation{host: host, path: path.Clean(remote)}
			continue
		}
		absolute, err := filepath.Abs(raw)
		if err != nil {
			return result, err
		}
		result[index] = startLocation{path: filepath.Clean(absolute)}
	}
	return result, nil
}

func resolveStartLocation(ctx context.Context, client *daemon.Client, local domain.Endpoint, start startLocation) (domain.Endpoint, domain.Location, error) {
	endpoint := local
	if start.host != "" {
		var response ipc.ProviderConnectSSHResponse
		if err := client.Call(ctx, daemon.ProviderConnectSSH, ipc.ProviderConnectSSHRequest{HostAlias: start.host}, &response); err != nil {
			return domain.Endpoint{}, domain.Location{}, err
		}
		id, err := domain.ParseEndpointID(response.Endpoint.ID)
		if err != nil {
			return domain.Endpoint{}, domain.Location{}, err
		}
		endpoint = domain.Endpoint{ID: id, Kind: response.Endpoint.Kind, DisplayName: response.Endpoint.DisplayName, SSHHostAlias: response.Endpoint.SSHHostAlias}
	}
	location, err := domain.NewLocation(endpoint.ID, domain.CanonicalPath(start.path))
	return endpoint, location, err
}

func listLocation(ctx context.Context, client *daemon.Client, pane tui.PaneID, generation uint64, location domain.Location, actions chan<- tui.Action) {
	cursor := provider.PageCursor("")
	for {
		var response ipc.ProviderListResponse
		err := client.Call(ctx, daemon.ProviderList, ipc.ProviderListRequest{Location: ipc.EncodeLocation(location), Cursor: cursor, Limit: 256}, &response)
		if err != nil {
			if ctx.Err() == nil {
				actions <- tui.ListingFailed{Pane: pane, Generation: generation, Message: err.Error()}
			}
			return
		}
		entries := make([]domain.Entry, 0, len(response.Entries))
		for _, wire := range response.Entries {
			entry, err := ipc.DecodeEntry(wire)
			if err != nil {
				actions <- tui.ListingFailed{Pane: pane, Generation: generation, Message: err.Error()}
				return
			}
			entries = append(entries, entry)
		}
		actions <- tui.ListingPage{Pane: pane, Generation: generation, Entries: entries, Done: response.Done}
		if response.Done {
			return
		}
		cursor = response.NextCursor
	}
}

func previewLocation(ctx context.Context, client *daemon.Client, generation uint64, location domain.Location, actions chan<- tui.Action) {
	var response ipc.ProviderReadResponse
	err := client.Call(ctx, daemon.ProviderRead, ipc.ProviderReadRequest{Location: ipc.EncodeLocation(location), Limit: tui.PreviewByteLimit}, &response)
	if err != nil {
		if ctx.Err() == nil {
			actions <- tui.PreviewChunk{Generation: generation, Done: true, Message: err.Error()}
		}
		return
	}
	data, err := response.Data.Decode()
	if err != nil {
		actions <- tui.PreviewChunk{Generation: generation, Done: true, Message: err.Error()}
		return
	}
	actions <- tui.PreviewChunk{Generation: generation, Data: data, Done: true, Truncated: !response.EOF}
}
