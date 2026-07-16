package app

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"io"
	"log/slog"
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
	"github.com/TyrantLucifer/awesome-mac-sftp/internal/diagnostic"
	"github.com/TyrantLucifer/awesome-mac-sftp/internal/domain"
	"github.com/TyrantLucifer/awesome-mac-sftp/internal/ipc"
	"github.com/TyrantLucifer/awesome-mac-sftp/internal/platform"
	"github.com/TyrantLucifer/awesome-mac-sftp/internal/provider"
	"github.com/TyrantLucifer/awesome-mac-sftp/internal/provider/localfs"
	sftpprovider "github.com/TyrantLucifer/awesome-mac-sftp/internal/provider/sftp"
	"github.com/TyrantLucifer/awesome-mac-sftp/internal/sshconfig"
	"github.com/TyrantLucifer/awesome-mac-sftp/internal/state/jobstore"
	"github.com/TyrantLucifer/awesome-mac-sftp/internal/statefs"
	"github.com/TyrantLucifer/awesome-mac-sftp/internal/transfer"
	"github.com/TyrantLucifer/awesome-mac-sftp/internal/transport/openssh"
	"github.com/TyrantLucifer/awesome-mac-sftp/internal/tui"
	"github.com/TyrantLucifer/awesome-mac-sftp/internal/workspace"
	"github.com/gdamore/tcell/v3"
)

const daemonReadyTimeout = 5 * time.Second
const authenticationTimeout = 2 * time.Minute
const durableLocalEndpointID domain.EndpointID = "ep_aaaaaaaaaaaaaaaaaaaaaaaaaa"

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

type daemonOptions struct {
	explicitMigrationResume bool
}

func parseDaemonArgs(args []string) (daemonOptions, error) {
	if len(args) == 0 {
		return daemonOptions{}, nil
	}
	if len(args) == 1 && args[0] == "--resume-migration" {
		return daemonOptions{explicitMigrationResume: true}, nil
	}
	return daemonOptions{}, fmt.Errorf("daemon accepts only the optional --resume-migration flag")
}

func runDaemon(ctx context.Context, args []string, _ io.Writer, _ io.Writer) error {
	options, err := parseDaemonArgs(args)
	if err != nil {
		return err
	}
	paths, purpose, err := runtimePaths()
	if err != nil {
		return err
	}
	return runDaemonWithPathsAndOptions(ctx, paths, purpose, options)
}

func runDaemonWithPaths(ctx context.Context, paths platform.Paths, purpose platform.ValidationPurpose) (returnErr error) {
	return runDaemonWithPathsAndOptions(ctx, paths, purpose, daemonOptions{})
}

func runDaemonWithPathsAndOptions(ctx context.Context, paths platform.Paths, purpose platform.ValidationPurpose, options daemonOptions) (returnErr error) {
	lock, err := platform.AcquireInstanceLock(paths.LockFile, purpose)
	if errors.Is(err, platform.ErrInstanceLocked) {
		return nil
	}
	if err != nil {
		return err
	}
	defer lock.Close()
	generator := &domain.RandomGenerator{}
	var stateDatabase *sql.DB
	var jobStore *jobstore.Store
	var stateOpenErr error
	if err := platform.PreparePrivateDirectory(paths.StateDir, platform.ValidatePersistent); err != nil {
		stateOpenErr = fmt.Errorf("prepare persistent state: %w", err)
	} else {
		databasePath := paths.DatabaseFile
		if databasePath == "" {
			databasePath = filepath.Join(paths.StateDir, "amsftp.db")
		}
		stateDatabase, _, stateOpenErr = statefs.Initialize(ctx, statefs.InitializeConfig{
			Root: paths.StateDir, DatabasePath: databasePath,
			ExplicitMigrationResume: options.explicitMigrationResume,
		})
		if stateOpenErr == nil {
			store, err := jobstore.New(ctx, stateDatabase)
			if err == nil {
				_, err = store.RecoverInterrupted(ctx, generator, time.Now())
			}
			if err == nil {
				err = store.CheckpointIdle(ctx)
			}
			if err == nil {
				jobStore = store
			}
			if err != nil {
				stateOpenErr = fmt.Errorf("recover durable Jobs: %w", err)
				_ = stateDatabase.Close()
				stateDatabase = nil
			}
		}
	}
	if options.explicitMigrationResume && stateOpenErr != nil {
		return fmt.Errorf("explicit migration resume failed: %w", stateOpenErr)
	}
	if stateDatabase != nil {
		defer func() {
			returnErr = errors.Join(returnErr, stateDatabase.Close())
		}()
	}
	var logger *slog.Logger
	var diagnosticRecords *diagnostic.Ring
	if stateOpenErr == nil {
		daemonLog, err := diagnostic.OpenDaemon(paths.LogFile, diagnostic.Config{})
		if err != nil {
			return err
		}
		logger = daemonLog.Logger
		diagnosticRecords = daemonLog.Records
		defer func() {
			returnErr = errors.Join(returnErr, daemonLog.Close())
		}()
	} else {
		// A rejected/corrupt/newer state database must not trigger any further
		// persistent writes. Stage 1 browsing remains available with an
		// in-memory diagnostic logger and no mutation store.
		diagnosticRecords = diagnostic.NewRing(0)
		logger = slog.New(diagnostic.NewRingHandler(diagnosticRecords, nil))
		logger.Error("persistent state unavailable; mutation disabled", diagnostic.Component("state"), diagnostic.Event("read_only_degraded"), diagnostic.ErrorCode(domain.CodeIntegrityFailed))
	}
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
	sessionID, err := domain.NewSessionID(generator)
	if err != nil {
		return err
	}
	local, err := localfs.New(localfs.Config{Endpoint: domain.Endpoint{ID: durableLocalEndpointID, Kind: domain.EndpointLocal, DisplayName: "local"}, SessionID: sessionID, Root: "/"})
	if err != nil {
		return err
	}
	sessions, err := daemon.NewProviderSessions([]provider.Provider{local}, tui.PreviewByteLimit)
	if err != nil {
		return err
	}
	sessions.SetDiagnosticSource(diagnosticRecords)
	if stateOpenErr == nil {
		workspaceStore, err := workspace.NewStore(filepath.Join(paths.StateDir, "workspaces"))
		if err != nil {
			return err
		}
		sessions.SetWorkspaceStore(workspaceStore)
	}
	authBroker, err := auth.NewBroker(auth.Config{MaxPrompts: 8})
	if err != nil {
		return err
	}
	sessions.SetAuthBroker(authBroker)
	connectEndpoint := func(connectCtx context.Context, endpoint domain.Endpoint) (provider.Provider, error) {
		if endpoint.Kind != domain.EndpointSSH || endpoint.ID == "" || endpoint.SSHHostAlias == "" {
			return nil, sshConnectStageError("invalid frozen SSH endpoint", domain.CodeInvalidArgument, domain.RetryNever, nil)
		}
		attempt, err := authBroker.BeginAttempt(connectCtx, endpoint.SSHHostAlias, authenticationTimeout)
		if err != nil {
			return nil, sshConnectStageError("start authentication attempt", domain.CodeInternal, domain.RetryNever, err)
		}
		defer attempt.Close()
		executable, err := os.Executable()
		if err != nil {
			return nil, sshConnectStageError("find authentication helper", domain.CodeInternal, domain.RetryNever, err)
		}
		if err := platform.ValidateExecutable(executable); err != nil {
			return nil, sshConnectStageError("validate authentication helper", domain.CodeIntegrityFailed, domain.RetryNever, err)
		}
		environment, err := auth.OpenSSHEnvironment(os.Environ(), executable, attempt.Token())
		if err != nil {
			return nil, sshConnectStageError("prepare OpenSSH authentication", domain.CodeInternal, domain.RetryNever, err)
		}
		transport, err := openssh.Dial(connectCtx, openssh.Config{HostAlias: endpoint.SSHHostAlias, Environment: environment, Redact: []string{string(attempt.Token())}})
		if err != nil {
			code, retry := classifySSHConnectError(err)
			return nil, sshConnectStageError(sshConnectMessage(code), code, retry, err)
		}
		remoteSessionID, err := domain.NewSessionID(generator)
		if err != nil {
			_ = transport.Close()
			return nil, sshConnectStageError("create SSH provider session", domain.CodeInternal, domain.RetryNever, err)
		}
		implementation, err := sftpprovider.New(sftpprovider.Config{Endpoint: endpoint, SessionID: remoteSessionID, Client: transport.Client(), Close: transport.Close})
		if err != nil {
			_ = transport.Close()
			return nil, sshConnectStageError("initialize SSH provider", domain.CodeInternal, domain.RetryNever, err)
		}
		return implementation, nil
	}
	sessions.SetSSHConnector(func(connectCtx context.Context, hostAlias string) (provider.Provider, error) {
		remoteEndpointID, err := domain.NewEndpointID(generator)
		if err != nil {
			return nil, sshConnectStageError("create SSH endpoint", domain.CodeInternal, domain.RetryNever, err)
		}
		return connectEndpoint(connectCtx, domain.Endpoint{ID: remoteEndpointID, Kind: domain.EndpointSSH, DisplayName: hostAlias, SSHHostAlias: hostAlias})
	})
	sessions.SetEndpointConnector(connectEndpoint)
	if jobStore != nil {
		manager, err := transfer.NewManager(transfer.ManagerConfig{
			Store: jobStore, Resolver: sessions, Generator: generator, MaxConcurrent: 4, MaxQueued: 128,
		})
		if err != nil {
			return err
		}
		if err := manager.Start(ctx); err != nil {
			manager.Close()
			return err
		}
		defer manager.Close()
		sessions.SetTransferService(manager)
	}
	server, err := daemon.NewServer(daemon.ServerConfig{BuildVersion: buildinfo.Current().String(), Epoch: string(sessionID), Sessions: sessions, MaxInFlight: 16, HandshakeTimeout: 2 * time.Second, Logger: logger, VerifyPeer: func(conn net.Conn) error {
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

func sshConnectMessage(code domain.Code) string {
	switch code {
	case domain.CodeAuthRequired:
		return "OpenSSH authentication failed; check credentials or retry explicitly"
	case domain.CodePermissionDenied:
		return "OpenSSH host-key verification failed; inspect known_hosts"
	case domain.CodeUnsupported:
		return "remote SFTP subsystem is unavailable"
	case domain.CodeTransportInterrupted, domain.CodeTimeout:
		return "OpenSSH transport is unavailable; reconnect is safe"
	default:
		return "establish OpenSSH SFTP session"
	}
}

func sshConnectStageError(message string, code domain.Code, retry domain.RetryKind, cause error) error {
	return &domain.OpError{
		Code:      code,
		Message:   message,
		Operation: "connect_ssh",
		Retry:     domain.RetryAdvice{Kind: retry},
		Effect:    domain.EffectNone,
		Cause:     cause,
	}
}

type paneConnectionAttempts struct {
	epochs  [2]uint64
	cancels [2]context.CancelFunc
}

func (attempts *paneConnectionAttempts) Begin(parent context.Context, pane tui.PaneID) (context.Context, uint64) {
	if pane > tui.Right {
		canceled, cancel := context.WithCancel(parent)
		cancel()
		return canceled, 0
	}
	if attempts.cancels[pane] != nil {
		attempts.cancels[pane]()
	}
	attempts.epochs[pane]++
	if attempts.epochs[pane] == 0 {
		attempts.epochs[pane] = 1
	}
	// #nosec G118 -- cancel is retained per pane and called on supersession, acceptance, or invalidation.
	requestCtx, cancel := context.WithCancel(parent)
	attempts.cancels[pane] = cancel
	return requestCtx, attempts.epochs[pane]
}

func (attempts *paneConnectionAttempts) Accept(pane tui.PaneID, epoch uint64) bool {
	if pane > tui.Right || epoch == 0 || attempts.epochs[pane] != epoch || attempts.cancels[pane] == nil {
		return false
	}
	attempts.cancels[pane]()
	attempts.cancels[pane] = nil
	return true
}

func (attempts *paneConnectionAttempts) Invalidate(pane tui.PaneID) {
	if pane > tui.Right {
		return
	}
	if attempts.cancels[pane] != nil {
		attempts.cancels[pane]()
		attempts.cancels[pane] = nil
	}
	attempts.epochs[pane]++
	if attempts.epochs[pane] == 0 {
		attempts.epochs[pane] = 1
	}
}

func listingResultCurrent(model tui.Model, pane tui.PaneID, generation uint64) bool {
	return pane <= tui.Right && generation != 0 && model.Panes[pane].Listing.Generation == generation
}

func connectionFailureState(model tui.Model, pane tui.PaneID, switching bool) domain.ConnectionState {
	if switching && pane <= tui.Right {
		return model.Panes[pane].Connection
	}
	return domain.StateFailed
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
	invocation, err := parseClientInvocation(args)
	if err != nil {
		return err
	}
	paths, purpose, err := runtimePaths()
	if err != nil {
		return err
	}
	client, err := connectDaemon(ctx, paths, purpose)
	if err != nil {
		return err
	}
	defer func() { _ = client.Close() }()
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
	screen, err := tcell.NewScreen()
	if err != nil {
		return err
	}
	if err := screen.Init(); err != nil {
		return err
	}
	defer screen.Fini()
	events := screen.EventQ()
	var restored *workspace.Document
	var locations [2]startLocation
	if invocation.Workspace != "" {
		var response workspace.LoadResponse
		if err := client.Call(ctx, daemon.WorkspaceLoad, workspace.LoadRequest{Name: invocation.Workspace}, &response); err != nil {
			locations, restored, err = pickStartLocations(ctx, screen, events, client, "Cannot open workspace "+invocation.Workspace+": "+clientErrorMessage(err))
			if errors.Is(err, errPickerCanceled) {
				return nil
			}
			if err != nil {
				return err
			}
		} else {
			locations, err = workspaceStartLocations(response.Document)
			if err != nil {
				return err
			}
			restored = &response.Document
		}
	} else if invocation.Pick {
		locations, restored, err = pickStartLocations(ctx, screen, events, client, "")
		if errors.Is(err, errPickerCanceled) {
			return nil
		}
		if err != nil {
			return err
		}
	} else {
		locations, err = startLocations(invocation.Locations)
		if err != nil {
			return err
		}
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
	if restored != nil {
		applyWorkspacePanePreferences(&left, restored.Panes[0])
		applyWorkspacePanePreferences(&right, restored.Panes[1])
	}
	model := tui.NewModel(left, right)
	cachePolicy := workspace.CacheEphemeral
	if restored != nil {
		applyWorkspaceLayout(&model, restored.Layout)
		cachePolicy = restored.CachePolicy
		for index, paneState := range restored.Panes {
			model, _ = tui.Reduce(model, tui.SetFilter{Pane: tui.PaneID(index), Query: paneState.Filter})
		}
	}
	runCtx, stop := context.WithCancel(ctx)
	authClient, err := connectDaemon(runCtx, paths, purpose)
	if err != nil {
		stop()
		return err
	}
	actions := make(chan tui.Action, 32)
	authResolutions := make(chan tui.Intent, 1)
	authErrors := make(chan error, 1)
	connectRequests := make(chan tui.Intent, 2)
	var authCancel context.CancelFunc
	startAuthLoop := func(activeClient *daemon.Client) {
		// #nosec G118 -- cancel is retained in authCancel and called on replacement and shutdown.
		claimCtx, cancel := context.WithCancel(runCtx)
		authCancel = cancel
		go func() {
			if err := runAuthClaimLoop(claimCtx, activeClient, actions, authResolutions); err != nil && claimCtx.Err() == nil {
				select {
				case authErrors <- err:
				case <-runCtx.Done():
				}
			}
		}()
	}
	startAuthLoop(authClient)
	defer func() {
		if authCancel != nil {
			authCancel()
		}
		stop()
		_ = authClient.Close()
	}()
	var generations [2]uint64
	var cancels [2]context.CancelFunc
	var paneRecoveries [2]paneRecovery
	var previewGeneration uint64
	var previewCancel context.CancelFunc
	startIntent := func(intent tui.Intent) {
		switch intent.Kind {
		case tui.IntentConnectEndpoint:
			select {
			case connectRequests <- intent:
			case <-runCtx.Done():
			}
			return
		case tui.IntentReleaseEndpoint:
			activeClient := client
			go func() {
				var response ipc.ProviderReleaseResponse
				releaseErr := activeClient.Call(runCtx, daemon.ProviderRelease, ipc.ProviderReleaseRequest{EndpointID: string(intent.EndpointID)}, &response)
				if releaseErr != nil && runCtx.Err() == nil {
					select {
					case actions <- tui.WorkspaceSaveResult{Message: "old endpoint cleanup failed: " + clientErrorMessage(releaseErr)}:
					case <-runCtx.Done():
					}
				}
			}()
			return
		case tui.IntentWorkspaceSave:
			document, documentErr := workspaceDocument(model, time.Now().UTC(), cachePolicy)
			if documentErr != nil {
				actions <- tui.WorkspaceSaveResult{Name: intent.Name, Message: "workspace save failed: " + documentErr.Error()}
				return
			}
			activeClient := client
			go func() {
				var response workspace.SaveResponse
				saveErr := activeClient.Call(runCtx, daemon.WorkspaceSave, workspace.SaveRequest{Name: intent.Name, Document: document}, &response)
				result := tui.WorkspaceSaveResult{Name: intent.Name}
				if saveErr != nil {
					result.Message = "workspace save failed: " + clientErrorMessage(saveErr)
				}
				select {
				case actions <- result:
				case <-runCtx.Done():
				}
			}()
			return
		case tui.IntentAuthResolve:
			select {
			case authResolutions <- intent:
			case <-runCtx.Done():
			}
			return
		case tui.IntentTransferCapture, tui.IntentPrepareDelete, tui.IntentPrepareRename:
			activeClient := client
			go func() {
				locations := intent.Locations
				if len(locations) == 0 {
					locations = []domain.Location{intent.Location}
				}
				references := make([]transfer.FileRef, 0, len(locations))
				route := daemon.JobCapture
				if intent.Kind == tui.IntentPrepareDelete {
					route = daemon.JobCaptureDelete
				}
				var captureErr error
				for _, location := range locations {
					var response daemon.JobCaptureResponse
					captureErr = activeClient.Call(runCtx, route, daemon.JobCaptureRequest{Location: ipc.EncodeLocation(location)}, &response)
					if captureErr != nil {
						break
					}
					references = append(references, response.Reference)
				}
				message := ""
				if captureErr != nil {
					message = "capture failed: " + clientErrorMessage(captureErr)
				}
				var result tui.Action
				switch intent.Kind {
				case tui.IntentPrepareDelete:
					result = tui.DeletePrepared{References: references, Message: message}
				case tui.IntentPrepareRename:
					var reference transfer.FileRef
					if len(references) != 0 {
						reference = references[0]
					}
					result = tui.RenamePrepared{Reference: reference, Message: message}
				default:
					result = tui.ClipboardCaptured{Clipboard: intent.Clipboard, References: references, Message: message}
				}
				select {
				case actions <- result:
				case <-runCtx.Done():
				}
			}()
			return
		case tui.IntentCreateDeleteJob:
			activeClient := client
			go func() {
				var response daemon.JobSnapshotResponse
				createErr := activeClient.Call(runCtx, daemon.JobCreateDelete, daemon.JobCreateDeleteRequest{Intent: transfer.DeleteIntent{
					Target: intent.Target, Recursive: intent.Recursive, Confirmed: intent.Confirmed,
					IrreversibleConfirmed: intent.IrreversibleConfirmed,
				}}, &response)
				result := tui.JobCreated{JobID: response.Snapshot.JobID, State: response.Snapshot.State}
				if createErr != nil {
					result.Message = "create delete Job failed: " + clientErrorMessage(createErr)
				}
				select {
				case actions <- result:
				case <-runCtx.Done():
				}
			}()
			return
		case tui.IntentCreateCopyJob:
			activeClient := client
			go func() {
				var response daemon.JobSnapshotResponse
				createErr := activeClient.Call(runCtx, daemon.JobCreateCopy, daemon.JobCreateCopyRequest{Intent: transfer.Intent{
					Clipboard: intent.Clipboard, Source: intent.Source, DestinationDirectory: intent.Location,
					Name: intent.Name, ConflictPolicy: transfer.ConflictAsk,
				}}, &response)
				result := tui.JobCreated{JobID: response.Snapshot.JobID, State: response.Snapshot.State}
				if createErr != nil {
					result.Message = "create Job failed: " + clientErrorMessage(createErr)
				}
				select {
				case actions <- result:
				case <-runCtx.Done():
				}
			}()
			return
		case tui.IntentJobList:
			activeClient := client
			go func() {
				var response daemon.JobListResponse
				listErr := activeClient.Call(runCtx, daemon.JobList, daemon.JobListRequest{Limit: 100}, &response)
				result := tui.JobsLoaded{Jobs: response.Jobs}
				if listErr != nil {
					result.Message = "list Jobs failed: " + clientErrorMessage(listErr)
				}
				select {
				case actions <- result:
				case <-runCtx.Done():
				}
			}()
			return
		case tui.IntentDiagnosticList:
			activeClient := client
			go func() {
				var response daemon.DiagnosticListResponse
				listErr := activeClient.Call(runCtx, daemon.DiagnosticList, daemon.DiagnosticListRequest{
					AfterSequence: intent.AfterSequence,
					Limit:         intent.Limit,
					EndpointID:    intent.EndpointID,
					JobID:         intent.JobID,
				}, &response)
				result := tui.DiagnosticsLoaded{Records: response.Records}
				if listErr != nil {
					result.Message = "list diagnostics failed: " + clientErrorMessage(listErr)
				}
				select {
				case actions <- result:
				case <-runCtx.Done():
				}
			}()
			return
		case tui.IntentJobPause, tui.IntentJobResume, tui.IntentJobCancel, tui.IntentJobResolveConflict:
			activeClient := client
			go func() {
				route := daemon.JobPause
				request := any(daemon.JobControlRequest{JobID: intent.JobID})
				switch intent.Kind {
				case tui.IntentJobResume:
					route = daemon.JobResume
				case tui.IntentJobCancel:
					route = daemon.JobCancel
				case tui.IntentJobResolveConflict:
					route = daemon.JobResolveConflict
					request = daemon.JobResolveConflictRequest{JobID: intent.JobID, Resolution: intent.Resolution, ApplyAll: intent.ApplyAll}
				}
				var response daemon.JobSnapshotResponse
				controlErr := activeClient.Call(runCtx, route, request, &response)
				result := tui.JobUpdated{Snapshot: response.Snapshot}
				if controlErr != nil {
					result.Message = "Job control failed: " + clientErrorMessage(controlErr)
				}
				select {
				case actions <- result:
				case <-runCtx.Done():
				}
			}()
			return
		case tui.IntentPreview:
			if previewCancel != nil {
				previewCancel()
			}
			requestCtx, cancel := context.WithCancel(runCtx)
			previewCancel = cancel
			previewGeneration++
			generation := previewGeneration
			activeClient := client
			actions <- tui.BeginPreview{Generation: generation, Location: intent.Location}
			go func() {
				defer cancel()
				previewLocation(requestCtx, activeClient, generation, intent.Location, actions)
			}()
			return
		case tui.IntentPreviewCancel:
			if previewCancel != nil {
				previewCancel()
				previewCancel = nil
			}
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
		paneRecoveries[pane].listingStarted(generation, intent)
		activeClient := client
		actions <- tui.BeginListing{
			Pane:                 pane,
			Generation:           generation,
			Location:             intent.Location,
			Endpoint:             intent.Endpoint,
			Connection:           intent.Connection,
			CapabilityGeneration: intent.CapabilityGeneration,
			Capabilities:         intent.Capabilities,
			CommitEndpoint:       intent.CommitEndpoint,
		}
		go func() {
			defer cancel()
			listLocation(requestCtx, activeClient, pane, generation, intent.Location, actions)
		}()
	}
	type connectionResult struct {
		pane                 tui.PaneID
		epoch                uint64
		endpoint             domain.Endpoint
		location             domain.Location
		host                 string
		state                domain.ConnectionState
		capabilityGeneration uint64
		capabilities         domain.CapabilitySnapshot
		recovery             bool
		switching            bool
		err                  error
	}
	connections := make(chan connectionResult, 4)
	var connectionAttempts paneConnectionAttempts
	startConnection := func(pane tui.PaneID, start startLocation, recovery, switching bool, activeClient *daemon.Client) {
		if recovery {
			paneRecoveries[pane].beginConnection()
			if !switching {
				model, _ = tui.Reduce(model, tui.PaneConnectionChanged{Pane: pane, State: domain.StateDisconnected, Message: "connection lost; reconnecting"})
			}
		}
		activeLocal := localEndpoint
		connectionCtx, epoch := connectionAttempts.Begin(runCtx, pane)
		go func() {
			result := connectionResult{pane: pane, epoch: epoch, host: start.host, recovery: recovery, switching: switching}
			result.err = runReconnect(connectionCtx, defaultReconnectPolicy(), func() error {
				var connectErr error
				result.endpoint, result.location, result.state, result.capabilities, connectErr = resolveStartLocation(connectionCtx, activeClient, activeLocal, start)
				result.capabilityGeneration = result.capabilities.Revision.Generation
				return connectErr
			})
			select {
			case connections <- result:
			case <-runCtx.Done():
			}
		}()
	}
	type daemonRecoveryResult struct {
		client     *daemon.Client
		authClient *daemon.Client
		local      domain.Endpoint
		err        error
	}
	daemonRecoveries := make(chan daemonRecoveryResult, 1)
	var daemonRecoveryStarts [2]startLocation
	recoveringDaemon := false
	startDaemonRecovery := func() {
		if recoveringDaemon {
			return
		}
		recoveringDaemon = true
		for index, paneState := range model.Panes {
			start := startLocation{path: string(paneState.Location.Path)}
			if paneState.Endpoint.Kind == domain.EndpointSSH {
				start.host = paneState.Endpoint.SSHHostAlias
			}
			daemonRecoveryStarts[index] = start
			paneRecoveries[index].beginConnection()
			model, _ = tui.Reduce(model, tui.PaneConnectionChanged{Pane: tui.PaneID(index), State: domain.StateConnecting, Message: "restarting daemon session"})
			if cancels[index] != nil {
				cancels[index]()
			}
			connectionAttempts.Invalidate(tui.PaneID(index))
		}
		if previewCancel != nil {
			previewCancel()
		}
		go func() {
			result := daemonRecoveryResult{}
			result.client, result.err = connectDaemonAfterLoss(runCtx, defaultReconnectPolicy(), func(ctx context.Context) (*daemon.Client, error) {
				return connectDaemon(ctx, paths, purpose)
			})
			if result.err == nil {
				result.local, result.err = daemonLocalEndpoint(runCtx, result.client)
			}
			if result.err == nil {
				result.authClient, result.err = connectDaemon(runCtx, paths, purpose)
			}
			if result.err != nil {
				if result.client != nil {
					_ = result.client.Close()
				}
				if result.authClient != nil {
					_ = result.authClient.Close()
				}
			}
			select {
			case daemonRecoveries <- result:
			case <-runCtx.Done():
			}
		}()
	}
	for index, start := range locations {
		pane := tui.PaneID(index)
		if start.host == "" {
			startIntent(tui.Intent{Kind: tui.IntentList, Pane: pane, Location: model.Panes[pane].Location})
			continue
		}
		startConnection(pane, start, false, false, client)
	}
	jobRefreshTicker := time.NewTicker(500 * time.Millisecond)
	defer jobRefreshTicker.Stop()
	for {
		tui.Render(tui.NewTCellSurface(screen), model, tui.RenderOptions{Overscan: 8})
		screen.Show()
		select {
		case <-runCtx.Done():
			return nil
		case <-jobRefreshTicker.C:
			if model.Drawer.Mode == tui.DrawerJobs {
				startIntent(tui.Intent{Kind: tui.IntentJobList})
			} else if model.Drawer.Mode == tui.DrawerLog {
				startIntent(tui.Intent{Kind: tui.IntentDiagnosticList, Limit: 256})
			}
		case err := <-authErrors:
			if authFailureLostDaemon(err, func() error {
				probeCtx, cancel := context.WithTimeout(runCtx, daemonReadyTimeout)
				defer cancel()
				_, probeErr := daemonLocalEndpoint(probeCtx, client)
				return probeErr
			}) {
				startDaemonRecovery()
				continue
			}
			return err
		case request := <-connectRequests:
			paneState := model.Panes[request.Pane]
			start := startLocation{path: string(paneState.Location.Path)}
			if request.Name == "local" {
				location, locationErr := domain.NewLocation(localEndpoint.ID, domain.CanonicalPath(start.path))
				if locationErr != nil {
					model, _ = tui.Reduce(model, tui.PaneConnectionChanged{Pane: request.Pane, State: domain.StateFailed, Message: locationErr.Error()})
					continue
				}
				var intents []tui.Intent
				paneRecoveries[request.Pane].connected()
				model, intents = tui.Reduce(model, tui.PaneConnected{Pane: request.Pane, Endpoint: localEndpoint, Location: location, State: domain.StateReady, PreserveCommitted: true})
				for _, intent := range intents {
					startIntent(intent)
				}
				continue
			}
			if _, validateErr := openssh.Arguments(request.Name); validateErr != nil {
				model, _ = tui.Reduce(model, tui.PaneConnectionChanged{Pane: request.Pane, State: paneState.Connection, Message: "invalid SSH host alias: " + validateErr.Error()})
				continue
			}
			start.host = request.Name
			startConnection(request.Pane, start, true, true, client)
		case result := <-daemonRecoveries:
			recoveringDaemon = false
			if result.err != nil {
				for pane := tui.Left; pane <= tui.Right; pane++ {
					paneRecoveries[pane].connectionFailed()
					model, _ = tui.Reduce(model, tui.PaneConnectionChanged{Pane: pane, State: domain.StateFailed, Message: "daemon recovery failed: " + clientErrorMessage(result.err)})
				}
				continue
			}
			oldClient := client
			client = result.client
			localEndpoint = result.local
			_ = oldClient.Close()
			if authCancel != nil {
				authCancel()
			}
			_ = authClient.Close()
			authClient = result.authClient
			startAuthLoop(authClient)
			for index, start := range daemonRecoveryStarts {
				pane := tui.PaneID(index)
				if start.host == "" {
					location, locationErr := domain.NewLocation(localEndpoint.ID, domain.CanonicalPath(start.path))
					if locationErr != nil {
						model, _ = tui.Reduce(model, tui.PaneConnectionChanged{Pane: pane, State: domain.StateFailed, Message: locationErr.Error()})
						continue
					}
					var intents []tui.Intent
					paneRecoveries[pane].connected()
					model, intents = tui.Reduce(model, tui.PaneConnected{Pane: pane, Endpoint: localEndpoint, Location: location, State: domain.StateReady, PreserveCommitted: true})
					for _, intent := range intents {
						startIntent(intent)
					}
					continue
				}
				startConnection(pane, start, true, false, client)
			}
		case result := <-connections:
			if !connectionAttempts.Accept(result.pane, result.epoch) {
				continue
			}
			if result.err != nil {
				paneRecoveries[result.pane].connectionFailed()
				state := connectionFailureState(model, result.pane, result.switching)
				model, _ = tui.Reduce(model, tui.PaneConnectionChanged{Pane: result.pane, State: state, Message: "connect " + result.host + " failed: " + clientErrorMessage(result.err)})
				continue
			}
			var intents []tui.Intent
			if result.recovery {
				paneRecoveries[result.pane].connected()
			}
			model, intents = tui.Reduce(model, tui.PaneConnected{Pane: result.pane, Endpoint: result.endpoint, Location: result.location, State: result.state, CapabilityGeneration: result.capabilityGeneration, Capabilities: result.capabilities, PreserveCommitted: result.recovery})
			for _, intent := range intents {
				startIntent(intent)
			}
		case action := <-actions:
			listingCurrent := true
			switch result := action.(type) {
			case tui.ListingPage:
				listingCurrent = listingResultCurrent(model, result.Pane, result.Generation)
			case tui.ListingFailed:
				listingCurrent = listingResultCurrent(model, result.Pane, result.Generation)
			}
			var intents []tui.Intent
			model, intents = tui.Reduce(model, action)
			if challenge, ok := action.(tui.AuthChallengeReceived); ok {
				for pane, paneState := range model.Panes {
					if paneState.Endpoint.SSHHostAlias == challenge.Endpoint || paneState.Endpoint.DisplayName == "connecting "+challenge.Endpoint {
						model, _ = tui.Reduce(model, tui.PaneConnectionChanged{Pane: tui.PaneID(pane), State: domain.StateAuthRequired, Message: "waiting for authentication"})
					}
				}
			}
			if page, ok := action.(tui.ListingPage); ok && listingCurrent && page.Done {
				if paneRecoveries[page.Pane].listingCompleted(page) {
					model, _ = tui.Reduce(model, tui.PaneConnectionChanged{Pane: page.Pane, State: domain.StateReady, Message: "reconnected at nearest accessible parent"})
				}
			}
			if failure, ok := action.(tui.ListingFailed); ok && listingCurrent {
				if fallback, ok := paneRecoveries[failure.Pane].listingFailed(failure); ok {
					startIntent(fallback)
					continue
				}
			}
			if failure, ok := action.(tui.ListingFailed); ok && listingCurrent && !failure.DaemonLost && failure.Retry == domain.RetryAfterReconnect && !paneRecoveries[failure.Pane].connecting() {
				pane := model.Panes[failure.Pane]
				if pane.Endpoint.Kind == domain.EndpointSSH && pane.Endpoint.SSHHostAlias != "" {
					startConnection(failure.Pane, startLocation{host: pane.Endpoint.SSHHostAlias, path: string(pane.Location.Path)}, true, false, client)
				}
			}
			if failure, ok := action.(tui.ListingFailed); ok && listingCurrent && failure.DaemonLost {
				startDaemonRecovery()
			}
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
			if _, ok := event.(*tcell.EventResize); ok {
				screen.Sync()
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

func workspaceDocument(model tui.Model, updatedAt time.Time, cachePolicy workspace.CachePolicy) (workspace.Document, error) {
	document := workspace.Document{
		SchemaVersion: workspace.SchemaVersion,
		UpdatedAt:     updatedAt.UTC(),
		Layout: workspace.LayoutState{ActivePane: int(model.Active), Drawer: workspace.DrawerState{
			Mode: workspace.DrawerMode(model.Drawer.Mode), Focus: workspace.FocusTarget(model.Drawer.Focus), Rows: model.Drawer.Rows,
		}},
		CachePolicy: cachePolicy,
	}
	for index, paneState := range model.Panes {
		endpoint := workspace.EndpointRef{Kind: paneState.Endpoint.Kind}
		if paneState.Endpoint.Kind == domain.EndpointSSH {
			endpoint.SSHHostAlias = paneState.Endpoint.SSHHostAlias
		}
		document.Panes[index] = workspace.Pane{
			Endpoint: endpoint,
			Path:     string(paneState.Location.Path),
			Filter:   paneState.Filter,
			Sort: workspace.SortState{
				Key:              workspace.SortKey(paneState.Sort.Key),
				Direction:        workspaceSortDirection(paneState.Sort.Descending),
				DirectoriesFirst: true,
			},
			ShowHidden: paneState.ShowHidden,
		}
	}
	if err := document.Validate(); err != nil {
		return workspace.Document{}, err
	}
	return document, nil
}

func applyWorkspaceLayout(model *tui.Model, layout workspace.LayoutState) {
	if layout.ActivePane == int(tui.Right) {
		model.Active = tui.Right
	} else {
		model.Active = tui.Left
	}
	model.Drawer = tui.DrawerState{
		Mode:  tui.DrawerMode(layout.Drawer.Mode),
		Focus: tui.FocusTarget(layout.Drawer.Focus),
		Rows:  layout.Drawer.Rows,
	}
}

func applyWorkspacePanePreferences(target *tui.PaneState, saved workspace.Pane) {
	target.Sort = tui.SortState{Key: tui.SortKey(saved.Sort.Key), Descending: saved.Sort.Direction == workspace.SortDescending}
	target.ShowHidden = saved.ShowHidden
}

func workspaceSortDirection(descending bool) workspace.SortDirection {
	if descending {
		return workspace.SortDescending
	}
	return workspace.SortAscending
}

var errPickerCanceled = errors.New("startup picker canceled")

func pickStartLocations(
	ctx context.Context,
	screen tcell.Screen,
	events <-chan tcell.Event,
	client *daemon.Client,
	initialMessage string,
) ([2]startLocation, *workspace.Document, error) {
	var empty [2]startLocation
	message := initialMessage
	var listed workspace.ListResponse
	if err := client.Call(ctx, daemon.WorkspaceList, workspace.ListRequest{}, &listed); err != nil {
		message = appendPickerMessage(message, "Cannot list workspaces: "+clientErrorMessage(err))
	}
	var aliases []string
	home, err := os.UserHomeDir()
	if err != nil {
		message = appendPickerMessage(message, "Cannot locate SSH config: "+err.Error())
	} else {
		sshDirectory := filepath.Join(home, ".ssh")
		aliases, err = sshconfig.DiscoverAliases(filepath.Join(sshDirectory, "config"), sshDirectory)
		if err != nil {
			message = appendPickerMessage(message, "Cannot read SSH config: "+err.Error())
		}
	}
	picker := tui.NewPicker(startupPickerChoices(listed.Workspaces, aliases))
	for {
		tui.RenderPicker(tui.NewTCellSurface(screen), picker, message)
		screen.Show()
		select {
		case <-ctx.Done():
			return empty, nil, ctx.Err()
		case event, open := <-events:
			if !open {
				return empty, nil, errPickerCanceled
			}
			switch value := event.(type) {
			case *tcell.EventResize:
				screen.Sync()
			case *tcell.EventKey:
				switch value.Key() {
				case tcell.KeyEscape, tcell.KeyCtrlC:
					return empty, nil, errPickerCanceled
				case tcell.KeyUp:
					picker.Move(-1)
				case tcell.KeyDown:
					picker.Move(1)
				case tcell.KeyBackspace, tcell.KeyBackspace2:
					picker.SetQuery(removeLastRune(picker.Query()))
					message = ""
				case tcell.KeyEnter:
					choice, ok := picker.Selected()
					if !ok {
						message = "Type an SSH alias to continue"
						continue
					}
					if choice.Kind == tui.PickerWorkspace {
						if choice.Problem != "" {
							message = "Workspace " + choice.Name + " needs repair: " + choice.Problem
							continue
						}
						var loaded workspace.LoadResponse
						if err := client.Call(ctx, daemon.WorkspaceLoad, workspace.LoadRequest{Name: choice.Name}, &loaded); err != nil {
							message = "Cannot open workspace " + choice.Name + ": " + clientErrorMessage(err)
							continue
						}
						locations, err := workspaceStartLocations(loaded.Document)
						if err != nil {
							message = "Cannot restore workspace " + choice.Name + ": " + err.Error()
							continue
						}
						return locations, &loaded.Document, nil
					}
					locations, err := startLocations([]string{choice.Name + ":/"})
					if err != nil {
						message = "Invalid SSH host alias: " + err.Error()
						continue
					}
					return locations, nil, nil
				case tcell.KeyRune:
					picker.SetQuery(picker.Query() + value.Str())
					message = ""
				}
			}
		}
	}
}

func startupPickerChoices(summaries []workspace.Summary, aliases []string) []tui.PickerChoice {
	choices := make([]tui.PickerChoice, 0, len(summaries)+len(aliases))
	for _, summary := range summaries {
		choices = append(choices, tui.PickerChoice{Kind: tui.PickerWorkspace, Name: summary.Name, Recent: summary.UpdatedAt, Problem: summary.Problem})
	}
	for _, alias := range aliases {
		choices = append(choices, tui.PickerChoice{Kind: tui.PickerHost, Name: alias})
	}
	return choices
}

func appendPickerMessage(current, addition string) string {
	if current == "" {
		return addition
	}
	return current + " | " + addition
}

func removeLastRune(value string) string {
	runes := []rune(value)
	if len(runes) == 0 {
		return value
	}
	return string(runes[:len(runes)-1])
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
		pane.Connection = domain.StateConnecting
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
		if host, remote, ok := remoteLocationParts(raw); ok {
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

func workspaceStartLocations(document workspace.Document) ([2]startLocation, error) {
	var result [2]startLocation
	if err := document.Validate(); err != nil {
		return result, err
	}
	for index, paneState := range document.Panes {
		result[index] = startLocation{path: paneState.Path}
		if paneState.Endpoint.Kind == domain.EndpointSSH {
			result[index].host = paneState.Endpoint.SSHHostAlias
		}
	}
	return result, nil
}

func remoteLocationParts(raw string) (string, string, bool) {
	if filepath.IsAbs(raw) || strings.HasPrefix(raw, "./") || strings.HasPrefix(raw, "../") {
		return "", "", false
	}
	host, remote, found := strings.Cut(raw, ":")
	if !found || strings.ContainsAny(host, `/\\`) {
		return "", "", false
	}
	return host, remote, true
}

func resolveStartLocation(ctx context.Context, client *daemon.Client, local domain.Endpoint, start startLocation) (domain.Endpoint, domain.Location, domain.ConnectionState, domain.CapabilitySnapshot, error) {
	endpoint := local
	state := domain.StateReady
	var capabilities domain.CapabilitySnapshot
	if start.host != "" {
		var response ipc.ProviderConnectSSHResponse
		if err := client.Call(ctx, daemon.ProviderConnectSSH, ipc.ProviderConnectSSHRequest{HostAlias: start.host}, &response); err != nil {
			return domain.Endpoint{}, domain.Location{}, "", domain.CapabilitySnapshot{}, err
		}
		id, err := domain.ParseEndpointID(response.Endpoint.ID)
		if err != nil {
			return domain.Endpoint{}, domain.Location{}, "", domain.CapabilitySnapshot{}, err
		}
		endpoint = domain.Endpoint{ID: id, Kind: response.Endpoint.Kind, DisplayName: response.Endpoint.DisplayName, SSHHostAlias: response.Endpoint.SSHHostAlias}
		var snapshot ipc.ProviderSnapshotResponse
		if err := client.Call(ctx, daemon.ProviderSnapshot, ipc.ProviderSnapshotRequest{EndpointID: response.Endpoint.ID}, &snapshot); err != nil {
			return domain.Endpoint{}, domain.Location{}, "", domain.CapabilitySnapshot{}, err
		}
		capabilities, err = capabilitySnapshotFromWire(snapshot)
		if err != nil {
			return domain.Endpoint{}, domain.Location{}, "", domain.CapabilitySnapshot{}, err
		}
		state = snapshot.State
	}
	location, err := domain.NewLocation(endpoint.ID, domain.CanonicalPath(start.path))
	return endpoint, location, state, capabilities, err
}

func capabilitySnapshotFromWire(response ipc.ProviderSnapshotResponse) (domain.CapabilitySnapshot, error) {
	endpointID, err := domain.ParseEndpointID(response.EndpointID)
	if err != nil {
		return domain.CapabilitySnapshot{}, fmt.Errorf("decode provider capability endpoint: %w", err)
	}
	sessionID, err := domain.ParseSessionID(response.SessionID)
	if err != nil {
		return domain.CapabilitySnapshot{}, fmt.Errorf("decode provider capability session: %w", err)
	}
	items := make([]domain.Capability, len(response.Items))
	for index, item := range response.Items {
		items[index] = domain.Capability{Name: item.Name, Version: item.Version, Constraints: append([]domain.CapabilityConstraint(nil), item.Constraints...)}
	}
	snapshot, err := domain.NewCapabilitySnapshot(domain.CapabilityRevision{SessionID: sessionID, Generation: response.Generation}, response.Complete, items)
	if err != nil {
		return domain.CapabilitySnapshot{}, fmt.Errorf("decode provider capability snapshot for %s: %w", endpointID, err)
	}
	return snapshot, nil
}

func listLocation(ctx context.Context, client *daemon.Client, pane tui.PaneID, generation uint64, location domain.Location, actions chan<- tui.Action) {
	cursor := provider.PageCursor("")
	for {
		var response ipc.ProviderListResponse
		err := client.Call(ctx, daemon.ProviderList, ipc.ProviderListRequest{Location: ipc.EncodeLocation(location), Cursor: cursor, Limit: 256}, &response)
		if err != nil {
			if ctx.Err() == nil {
				code, retry, daemonLost := providerCallFailure(err)
				actions <- tui.ListingFailed{Pane: pane, Generation: generation, Message: clientErrorMessage(err), Code: code, Retry: retry, DaemonLost: daemonLost, Location: location}
			}
			return
		}
		entries := make([]domain.Entry, 0, len(response.Entries))
		for _, wire := range response.Entries {
			entry, err := ipc.DecodeEntry(wire)
			if err != nil {
				actions <- tui.ListingFailed{Pane: pane, Generation: generation, Message: clientErrorMessage(err)}
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

func providerCallFailure(err error) (domain.Code, domain.RetryKind, bool) {
	var remote *daemon.RemoteError
	if errors.As(err, &remote) {
		return remote.RPC.Code, remote.RPC.Retry.Kind, false
	}
	return domain.CodeTransportInterrupted, domain.RetryAfterReconnect, true
}

func clientErrorMessage(err error) string {
	if err == nil {
		return ""
	}
	message := err.Error()
	summary := daemon.DiagnosticSummary(err)
	if summary.RequestID == "" {
		return message
	}
	return message + " [" + summary.String() + "]"
}

func daemonConnectionLost(err error) bool {
	if err == nil || errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return false
	}
	var remote *daemon.RemoteError
	return !errors.As(err, &remote)
}

func authFailureLostDaemon(err error, probe func() error) bool {
	if daemonConnectionLost(err) {
		return true
	}
	var remote *daemon.RemoteError
	if !errors.As(err, &remote) || remote.RPC.Code != domain.CodeCanceled {
		return false
	}
	return probe() != nil
}

func daemonLocalEndpoint(ctx context.Context, client *daemon.Client) (domain.Endpoint, error) {
	var endpoints ipc.ProviderEndpointsResponse
	if err := client.Call(ctx, daemon.ProviderEndpoints, struct{}{}, &endpoints); err != nil {
		return domain.Endpoint{}, err
	}
	if len(endpoints.Endpoints) != 1 || endpoints.Endpoints[0].Kind != domain.EndpointLocal {
		return domain.Endpoint{}, fmt.Errorf("expected one local endpoint, got %d", len(endpoints.Endpoints))
	}
	id, err := domain.ParseEndpointID(endpoints.Endpoints[0].ID)
	if err != nil {
		return domain.Endpoint{}, err
	}
	return domain.Endpoint{ID: id, Kind: domain.EndpointLocal, DisplayName: endpoints.Endpoints[0].DisplayName}, nil
}

func recoveryParent(location domain.Location) (domain.Location, bool) {
	parent := path.Dir(string(location.Path))
	if parent == "." || parent == string(location.Path) {
		return domain.Location{}, false
	}
	return domain.Location{EndpointID: location.EndpointID, Path: domain.CanonicalPath(parent)}, true
}

func previewLocation(ctx context.Context, client *daemon.Client, generation uint64, location domain.Location, actions chan<- tui.Action) {
	var response ipc.ProviderReadResponse
	err := client.Call(ctx, daemon.ProviderRead, ipc.ProviderReadRequest{Location: ipc.EncodeLocation(location), Limit: tui.PreviewByteLimit}, &response)
	if err != nil {
		if ctx.Err() == nil {
			actions <- tui.PreviewChunk{Generation: generation, Done: true, Message: clientErrorMessage(err)}
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
