package app

import (
	"context"
	"errors"
	"os"
	"sync"
	"time"

	"github.com/TyrantLucifer/awesome-sftp-cli/internal/cache"
	"github.com/TyrantLucifer/awesome-sftp-cli/internal/cachemanager"
	"github.com/TyrantLucifer/awesome-sftp-cli/internal/config"
	"github.com/TyrantLucifer/awesome-sftp-cli/internal/daemon"
	"github.com/TyrantLucifer/awesome-sftp-cli/internal/domain"
	"github.com/TyrantLucifer/awesome-sftp-cli/internal/ipc"
	"github.com/TyrantLucifer/awesome-sftp-cli/internal/platform"
	builtinpreview "github.com/TyrantLucifer/awesome-sftp-cli/internal/preview"
	"github.com/TyrantLucifer/awesome-sftp-cli/internal/search"
	"github.com/TyrantLucifer/awesome-sftp-cli/internal/transfer"
	"github.com/TyrantLucifer/awesome-sftp-cli/internal/transport/openssh"
	"github.com/TyrantLucifer/awesome-sftp-cli/internal/tui"
	"github.com/TyrantLucifer/awesome-sftp-cli/internal/workspace"
	"github.com/gdamore/tcell/v3"
)

type clientConnectionResult struct {
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

type daemonRecoveryResult struct {
	client     *daemon.Client
	authClient *daemon.Client
	local      domain.Endpoint
	err        error
}

type clientRuntime struct {
	paths                   platform.Paths
	purpose                 platform.ValidationPurpose
	appConfig               config.Config
	userKeymap              tui.Keymap
	clientVersion           string
	externalRuntime         externalRuntimeConfig
	previewRenderLimits     builtinpreview.Limits
	previewImageLimits      builtinpreview.ImageOutputLimits
	filenameSearchBudget    search.Budget
	contentSearchBudget     search.ContentBudget
	clientReconnectPolicy   reconnectPolicy
	directPolicy            transfer.DirectPolicy
	terminalImageCapability *terminalImageCapabilityState
	reprobeTerminalImages   func()
	screen                  tcell.Screen
	events                  <-chan tcell.Event
	editWorkspace           cache.WorkspaceID
	requestGenerator        *domain.RandomGenerator

	ctx    context.Context
	stop   context.CancelFunc
	client *daemon.Client

	authClient *daemon.Client
	authCancel context.CancelFunc

	localEndpoint domain.Endpoint
	model         tui.Model
	cachePolicy   workspace.CachePolicy
	editFlow      *editCoordinator

	generations        [2]uint64
	cancels            [2]context.CancelFunc
	paneRecoveries     [2]paneRecovery
	connectionAttempts paneConnectionAttempts

	previewGeneration uint64
	previewCancel     context.CancelFunc
	searchGeneration  uint64
	searchCancel      context.CancelFunc

	commandMu         sync.Mutex
	commandCancel     context.CancelFunc
	commandGeneration uint64

	daemonLosses     chan *daemon.Client
	actions          chan tui.Action
	authResolutions  chan tui.Intent
	authErrors       chan error
	connectRequests  chan tui.Intent
	connections      chan clientConnectionResult
	daemonRecoveries chan daemonRecoveryResult

	daemonRecoveryStarts [2]startLocation
	recoveringDaemon     bool
	pendingTerminalImage *tui.PreviewTerminalImage
}

func (r *clientRuntime) sendAction(action tui.Action) {
	select {
	case r.actions <- action:
	case <-r.ctx.Done():
	}
}

func (r *clientRuntime) goClientAction(fn func(*daemon.Client) tui.Action) {
	activeClient := r.client
	go func() {
		r.sendAction(fn(activeClient))
	}()
}

func (r *clientRuntime) reduceAndDispatch(action tui.Action) {
	var intents []tui.Intent
	r.model, intents = tui.Reduce(r.model, action)
	for _, intent := range intents {
		r.dispatchIntent(intent)
	}
}

func (r *clientRuntime) watchDaemonConnection(activeClient *daemon.Client) {
	go func() {
		select {
		case <-activeClient.Done():
			select {
			case r.daemonLosses <- activeClient:
			case <-r.ctx.Done():
			}
		case <-r.ctx.Done():
		}
	}()
}

func (r *clientRuntime) startAuthLoop(activeClient *daemon.Client) {
	// #nosec G118 -- cancel is retained in authCancel and called on replacement and shutdown.
	claimCtx, cancel := context.WithCancel(r.ctx)
	r.authCancel = cancel
	go func() {
		if err := runAuthClaimLoop(claimCtx, activeClient, r.actions, r.authResolutions); err != nil && claimCtx.Err() == nil {
			select {
			case r.authErrors <- err:
			case <-r.ctx.Done():
			}
		}
	}()
}

func (r *clientRuntime) close() {
	if r.authCancel != nil {
		r.authCancel()
	}
	r.stop()
	if r.authClient != nil {
		_ = r.authClient.Close()
	}
	if r.client != nil {
		_ = r.client.Close()
	}
}

func (r *clientRuntime) dispatchIntent(intent tui.Intent) {
	switch intent.Kind {
	case tui.IntentEdit, tui.IntentOpenExternal:
		r.actions <- r.editFlow.Begin(r.ctx, intent)
	case tui.IntentEditLaunch:
		r.actions <- r.editFlow.Launch(r.ctx, intent.EditSessionID)
	case tui.IntentEditCheck:
		r.actions <- r.editFlow.Check(r.ctx, intent.EditSessionID)
	case tui.IntentEditDecision:
		r.actions <- r.editFlow.Decide(r.ctx, intent)
	case tui.IntentEditResume:
		r.actions <- r.editFlow.Resume(r.ctx, intent.EditSessionID, intent.Pane)
	case tui.IntentEditRecoverable:
		r.actions <- r.editFlow.Recoverable(r.ctx, tui.MaxRecoverableEditSessions)
	case tui.IntentCachePolicy:
		if err := r.editFlow.SetPolicy(intent.CachePolicy); err != nil {
			r.model.Notice = "cache policy change refused: " + err.Error()
			return
		}
		r.cachePolicy = workspace.CachePolicy(intent.CachePolicy)
	case tui.IntentCacheClear:
		r.goClientAction(func(activeClient *daemon.Client) tui.Action {
			scope := cachemanager.ClearWorkspace
			workspaceID := r.editWorkspace
			if intent.CacheClearScope == tui.CacheClearAll {
				scope, workspaceID = cachemanager.ClearAll, ""
			}
			var response daemon.CacheClearResponse
			clearErr := activeClient.Call(r.ctx, daemon.CacheClear, daemon.CacheClearRequest{
				Scope: scope, WorkspaceID: workspaceID, MaxTargets: cache.DefaultMaxCandidates, MaxVisited: 10_000,
			}, &response)
			result := tui.CacheCleared{Deleted: response.Deleted, Protected: response.Protected, RemainingBytes: response.Remaining.Bytes}
			if clearErr != nil {
				result.Message = "cache clear refused safely: " + clientErrorMessage(clearErr)
			}
			return result
		})
	case tui.IntentConnectEndpoint:
		select {
		case r.connectRequests <- intent:
		case <-r.ctx.Done():
		}
	case tui.IntentReleaseEndpoint:
		activeClient := r.client
		go func() {
			var response ipc.ProviderReleaseResponse
			releaseErr := activeClient.Call(r.ctx, daemon.ProviderRelease, ipc.ProviderReleaseRequest{EndpointID: string(intent.EndpointID)}, &response)
			if releaseErr != nil && r.ctx.Err() == nil {
				r.sendAction(tui.WorkspaceSaveResult{Message: "old endpoint cleanup failed: " + clientErrorMessage(releaseErr)})
			}
		}()
	case tui.IntentWorkspaceSave:
		document, documentErr := workspaceDocument(r.model, time.Now().UTC(), r.cachePolicy)
		if documentErr != nil {
			r.actions <- tui.WorkspaceSaveResult{Name: intent.Name, Message: "workspace save failed: " + documentErr.Error()}
			return
		}
		r.goClientAction(func(activeClient *daemon.Client) tui.Action {
			var response workspace.SaveResponse
			saveErr := activeClient.Call(r.ctx, daemon.WorkspaceSave, workspace.SaveRequest{Name: intent.Name, Document: document}, &response)
			result := tui.WorkspaceSaveResult{Name: intent.Name}
			if saveErr != nil {
				result.Message = "workspace save failed: " + clientErrorMessage(saveErr)
			}
			return result
		})
	case tui.IntentAuthResolve:
		select {
		case r.authResolutions <- intent:
		case <-r.ctx.Done():
		}
	case tui.IntentTransferCapture, tui.IntentPrepareDelete, tui.IntentPrepareRename:
		r.startCaptureIntent(intent)
	case tui.IntentCreateDeleteJob:
		r.goClientAction(func(activeClient *daemon.Client) tui.Action {
			var response daemon.JobSnapshotResponse
			createErr := activeClient.Call(r.ctx, daemon.JobCreateDelete, daemon.JobCreateDeleteRequest{Intent: transfer.DeleteIntent{
				Target: intent.Target, Recursive: intent.Recursive, Confirmed: intent.Confirmed,
				IrreversibleConfirmed: intent.IrreversibleConfirmed,
			}}, &response)
			result := tui.JobCreated{JobID: response.Snapshot.JobID, State: response.Snapshot.State}
			if createErr != nil {
				result.Message = "create delete Job failed: " + clientErrorMessage(createErr)
			}
			return result
		})
	case tui.IntentCreateCopyJob:
		r.goClientAction(func(activeClient *daemon.Client) tui.Action {
			var response daemon.JobSnapshotResponse
			createErr := activeClient.Call(r.ctx, daemon.JobCreateCopy, daemon.JobCreateCopyRequest{
				Intent: configuredCopyIntent(intent, r.directPolicy, r.appConfig.Transfer),
			}, &response)
			result := tui.JobCreated{JobID: response.Snapshot.JobID, State: response.Snapshot.State}
			if createErr != nil {
				result.Message = "create Job failed: " + clientErrorMessage(createErr)
			}
			return result
		})
	case tui.IntentJobList:
		r.goClientAction(func(activeClient *daemon.Client) tui.Action {
			var response daemon.JobListResponse
			listErr := activeClient.Call(r.ctx, daemon.JobList, daemon.JobListRequest{Limit: 100}, &response)
			result := tui.JobsLoaded{Jobs: response.Jobs, ObservedAt: time.Now()}
			if listErr != nil {
				result.Message = "list Jobs failed: " + clientErrorMessage(listErr)
			}
			return result
		})
	case tui.IntentDiagnosticList:
		r.goClientAction(func(activeClient *daemon.Client) tui.Action {
			var response daemon.DiagnosticListResponse
			listErr := activeClient.Call(r.ctx, daemon.DiagnosticList, daemon.DiagnosticListRequest{
				AfterSequence: intent.AfterSequence,
				Limit:         intent.Limit,
				EndpointID:    intent.EndpointID,
				JobID:         intent.JobID,
			}, &response)
			result := tui.DiagnosticsLoaded{Records: response.Records}
			if listErr != nil {
				result.Message = "list diagnostics failed: " + clientErrorMessage(listErr)
			}
			return result
		})
	case tui.IntentJobPause, tui.IntentJobResume, tui.IntentJobCancel, tui.IntentJobResolveConflict:
		r.startJobControlIntent(intent)
	case tui.IntentPreview:
		r.startPreviewIntent(intent)
	case tui.IntentPreviewCancel:
		if r.previewCancel != nil {
			r.previewCancel()
			r.previewCancel = nil
		}
	case tui.IntentSearchFilename:
		r.startFilenameSearchIntent(intent)
	case tui.IntentSearchContent:
		r.startContentSearchIntent(intent)
	case tui.IntentSearchCancel:
		r.startSearchCancelIntent(intent)
	case tui.IntentRunCommand:
		r.startCommandIntent(intent)
	case tui.IntentCommandCancel:
		r.commandMu.Lock()
		cancel := r.commandCancel
		r.commandMu.Unlock()
		if cancel != nil {
			cancel()
		}
	case tui.IntentShell:
		r.model.Notice = runShellIntent(r.ctx, intent, append([]string(nil), os.Environ()...), r.screen, r.reprobeTerminalImages)
		r.dispatchIntent(tui.Intent{Kind: tui.IntentList, Pane: intent.Pane, Location: intent.Location})
	case tui.IntentList:
		r.startListingIntent(intent)
	}
}

func (r *clientRuntime) startCaptureIntent(intent tui.Intent) {
	r.goClientAction(func(activeClient *daemon.Client) tui.Action {
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
			captureErr = activeClient.Call(r.ctx, route, daemon.JobCaptureRequest{Location: ipc.EncodeLocation(location)}, &response)
			if captureErr != nil {
				break
			}
			references = append(references, response.Reference)
		}
		message := ""
		if captureErr != nil {
			message = "capture failed: " + clientErrorMessage(captureErr)
		}
		switch intent.Kind {
		case tui.IntentPrepareDelete:
			return tui.DeletePrepared{References: references, Message: message}
		case tui.IntentPrepareRename:
			var reference transfer.FileRef
			if len(references) != 0 {
				reference = references[0]
			}
			return tui.RenamePrepared{Reference: reference, Message: message}
		default:
			return tui.ClipboardCaptured{
				Clipboard: intent.Clipboard, References: references,
				Generation: intent.ClipboardGeneration, Message: message,
			}
		}
	})
}

func (r *clientRuntime) startJobControlIntent(intent tui.Intent) {
	r.goClientAction(func(activeClient *daemon.Client) tui.Action {
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
		controlErr := activeClient.Call(r.ctx, route, request, &response)
		result := tui.JobUpdated{Snapshot: response.Snapshot}
		if controlErr != nil {
			result.Message = "Job control failed: " + clientErrorMessage(controlErr)
		}
		return result
	})
}

func (r *clientRuntime) startPreviewIntent(intent tui.Intent) {
	if r.previewCancel != nil {
		r.previewCancel()
	}
	requestCtx, cancel := context.WithCancel(r.ctx)
	r.previewCancel = cancel
	r.previewGeneration++
	generation := r.previewGeneration
	requestID, requestIDErr := domain.NewRequestID(r.requestGenerator)
	if requestIDErr != nil {
		r.model.Notice = "preview request failed: " + requestIDErr.Error()
		return
	}
	activeClient := r.client
	mode := intent.PreviewMode
	if mode == "" {
		mode = builtinpreview.ReadHead
	}
	view := intent.PreviewView
	if view == "" {
		view = builtinpreview.ViewAuto
	}
	identity := tui.PreviewRequestIdentity{
		RequestID: requestID, Pane: intent.Pane, EndpointSession: intent.EndpointSession,
		EndpointGeneration: intent.EndpointGeneration, Mode: mode, Offset: intent.PreviewOffset,
		RequestedLimit: builtinpreview.ReadChunkBytes, UIGeneration: generation,
	}
	policy := cache.Policy(r.cachePolicy)
	go func() {
		defer cancel()
		previewLocation(requestCtx, activeClient, identity, view, intent.Location, r.editWorkspace, policy, r.externalRuntime.previewer, r.terminalImageCapability.Current(), r.previewRenderLimits, r.previewImageLimits, r.actions)
	}()
}

func (r *clientRuntime) beginSearchRequest(label string) (context.Context, context.CancelFunc, domain.RequestID, uint64, bool) {
	if r.searchCancel != nil {
		r.searchCancel()
	}
	requestCtx, cancel := context.WithCancel(r.ctx)
	r.searchCancel = cancel
	r.searchGeneration++
	generation := r.searchGeneration
	requestID, err := domain.NewRequestID(r.requestGenerator)
	if err != nil {
		cancel()
		r.searchCancel = nil
		r.model.Notice = label + " search request failed: " + err.Error()
		return nil, nil, "", 0, false
	}
	return requestCtx, cancel, requestID, generation, true
}

func (r *clientRuntime) startFilenameSearchIntent(intent tui.Intent) {
	requestCtx, cancel, requestID, generation, ok := r.beginSearchRequest("filename")
	if !ok {
		return
	}
	activeClient := r.client
	pendingIdentity := pendingFilenameSearchIdentity(requestID, generation, intent, r.filenameSearchBudget)
	go func() {
		defer cancel()
		select {
		case r.actions <- tui.BeginSearch{Identity: pendingIdentity}:
		case <-requestCtx.Done():
			return
		}
		identity, identityErr := filenameSearchIdentity(requestCtx, activeClient, pendingIdentity)
		if identityErr != nil {
			if requestCtx.Err() == nil {
				r.actions <- tui.SearchFailed{Identity: pendingIdentity, Message: "filename search snapshot failed: " + clientErrorMessage(identityErr)}
			}
			return
		}
		select {
		case r.actions <- tui.BeginSearch{Identity: identity}:
		case <-requestCtx.Done():
			return
		}
		streamFilenameSearch(requestCtx, activeClient, identity, r.actions)
	}()
}

func (r *clientRuntime) startContentSearchIntent(intent tui.Intent) {
	requestCtx, cancel, requestID, generation, ok := r.beginSearchRequest("content")
	if !ok {
		return
	}
	activeClient := r.client
	pendingIdentity := pendingContentSearchIdentity(requestID, generation, intent, r.contentSearchBudget)
	go func() {
		defer cancel()
		select {
		case r.actions <- tui.BeginContentSearch{Identity: pendingIdentity}:
		case <-requestCtx.Done():
			return
		}
		identity, identityErr := contentSearchIdentity(requestCtx, activeClient, pendingIdentity)
		if identityErr != nil {
			if requestCtx.Err() == nil {
				r.actions <- tui.ContentSearchFailed{Identity: pendingIdentity, Message: "content search snapshot failed: " + clientErrorMessage(identityErr)}
			}
			return
		}
		select {
		case r.actions <- tui.BeginContentSearch{Identity: identity}:
		case <-requestCtx.Done():
			return
		}
		streamContentSearch(requestCtx, activeClient, identity, r.actions)
	}()
}

func (r *clientRuntime) startSearchCancelIntent(intent tui.Intent) {
	if r.searchCancel != nil {
		r.searchCancel()
		r.searchCancel = nil
	}
	requestID := string(intent.SearchIdentity.RequestID)
	if requestID == "" {
		requestID = string(intent.ContentSearchIdentity.RequestID)
	}
	if requestID == "" {
		return
	}
	activeClient := r.client
	go func() {
		cancelCtx, cancel := context.WithTimeout(r.ctx, 2*time.Second)
		defer cancel()
		var response ipc.SearchCancelResponse
		_ = activeClient.Call(cancelCtx, daemon.SearchCancel, ipc.SearchCancelRequest{RequestID: requestID}, &response)
	}()
}

func (r *clientRuntime) startCommandIntent(intent tui.Intent) {
	environment := append([]string(nil), os.Environ()...)
	r.commandMu.Lock()
	if r.commandCancel != nil {
		r.commandMu.Unlock()
		r.actions <- tui.CommandCompleted{Pane: intent.Pane, Location: intent.Location, ExitCode: -1, Message: "command rejected: another one-time command is still running"}
		return
	}
	commandCtx, cancel := context.WithCancel(r.ctx)
	r.commandCancel = cancel
	r.commandGeneration++
	generation := r.commandGeneration
	r.commandMu.Unlock()
	go func() {
		defer cancel()
		result := runCommandIntent(commandCtx, intent, environment)
		r.commandMu.Lock()
		if r.commandGeneration == generation {
			r.commandCancel = nil
		}
		r.commandMu.Unlock()
		r.sendAction(result)
	}()
}

func (r *clientRuntime) startListingIntent(intent tui.Intent) {
	pane := intent.Pane
	if r.cancels[pane] != nil {
		r.cancels[pane]()
	}
	requestCtx, cancel := context.WithCancel(r.ctx)
	r.cancels[pane] = cancel
	r.generations[pane]++
	generation := r.generations[pane]
	r.paneRecoveries[pane].listingStarted(generation, intent)
	activeClient := r.client
	r.actions <- tui.BeginListing{
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
		listLocation(requestCtx, activeClient, pane, generation, intent.Location, r.appConfig.Listing.DefaultPageSize, r.actions)
	}()
}

func (r *clientRuntime) startConnection(pane tui.PaneID, start startLocation, recovery, switching bool, activeClient *daemon.Client) {
	if recovery {
		r.paneRecoveries[pane].beginConnection()
		if !switching {
			r.model, _ = tui.Reduce(r.model, tui.PaneConnectionChanged{Pane: pane, State: domain.StateDisconnected, Message: "connection lost; reconnecting"})
		}
	}
	activeLocal := r.localEndpoint
	connectionCtx, epoch := r.connectionAttempts.Begin(r.ctx, pane)
	go func() {
		result := clientConnectionResult{pane: pane, epoch: epoch, host: start.host, recovery: recovery, switching: switching}
		result.err = runReconnect(connectionCtx, r.clientReconnectPolicy, func() error {
			var connectErr error
			result.endpoint, result.location, result.state, result.capabilities, connectErr = resolveStartLocation(connectionCtx, activeClient, activeLocal, start)
			result.capabilityGeneration = result.capabilities.Revision.Generation
			return connectErr
		})
		select {
		case r.connections <- result:
		case <-r.ctx.Done():
		}
	}()
}

func (r *clientRuntime) startDaemonRecovery() {
	if r.recoveringDaemon {
		return
	}
	r.recoveringDaemon = true
	for index, paneState := range r.model.Panes {
		start := startLocation{path: string(paneState.Location.Path)}
		if paneState.Endpoint.Kind == domain.EndpointSSH {
			start.host = paneState.Endpoint.SSHHostAlias
		}
		r.daemonRecoveryStarts[index] = start
		r.paneRecoveries[index].beginConnection()
		r.model, _ = tui.Reduce(r.model, tui.PaneConnectionChanged{Pane: tui.PaneID(index), State: domain.StateConnecting, Message: "restarting daemon session"})
		if r.cancels[index] != nil {
			r.cancels[index]()
		}
		r.connectionAttempts.Invalidate(tui.PaneID(index))
	}
	if r.previewCancel != nil {
		r.previewCancel()
	}
	if r.searchCancel != nil {
		r.searchCancel()
		r.searchCancel = nil
	}
	go func() {
		result := daemonRecoveryResult{}
		recoveryCtx, cancelRecovery := context.WithTimeout(r.ctx, daemonRecoveryTimeout)
		result.client, result.err = connectDaemonAfterLoss(recoveryCtx, r.clientReconnectPolicy, func(ctx context.Context) (*daemon.Client, error) {
			return connectDaemonRecovering(ctx, r.paths, r.purpose)
		})
		cancelRecovery()
		if result.err == nil {
			result.local, result.err = daemonLocalEndpoint(r.ctx, result.client)
		}
		if result.err == nil {
			result.authClient, result.err = connectDaemon(r.ctx, r.paths, r.purpose)
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
		case r.daemonRecoveries <- result:
		case <-r.ctx.Done():
		}
	}()
}

func (r *clientRuntime) connectLocalPane(pane tui.PaneID, rawPath string) {
	location, locationErr := domain.NewLocation(r.localEndpoint.ID, domain.CanonicalPath(rawPath))
	if locationErr != nil {
		r.model, _ = tui.Reduce(r.model, tui.PaneConnectionChanged{Pane: pane, State: domain.StateFailed, Message: locationErr.Error()})
		return
	}
	r.paneRecoveries[pane].connected()
	r.reduceAndDispatch(tui.PaneConnected{Pane: pane, Endpoint: r.localEndpoint, Location: location, State: domain.StateReady, PreserveCommitted: true})
}

func (r *clientRuntime) handleConnectRequest(request tui.Intent) {
	paneState := r.model.Panes[request.Pane]
	start := startLocation{path: string(paneState.Location.Path)}
	if request.Name == "local" {
		r.connectLocalPane(request.Pane, start.path)
		return
	}
	if _, validateErr := openssh.Arguments(request.Name); validateErr != nil {
		r.model, _ = tui.Reduce(r.model, tui.PaneConnectionChanged{Pane: request.Pane, State: paneState.Connection, Message: "invalid SSH host alias: " + validateErr.Error()})
		return
	}
	start.host = request.Name
	r.startConnection(request.Pane, start, true, true, r.client)
}

func (r *clientRuntime) handleDaemonRecovery(result daemonRecoveryResult) {
	r.recoveringDaemon = false
	if result.err != nil {
		for pane := tui.Left; pane <= tui.Right; pane++ {
			r.paneRecoveries[pane].connectionFailed()
			r.model, _ = tui.Reduce(r.model, tui.PaneConnectionChanged{Pane: pane, State: domain.StateFailed, Message: "daemon recovery failed: " + clientErrorMessage(result.err)})
		}
		return
	}
	oldClient := r.client
	r.client = result.client
	r.watchDaemonConnection(r.client)
	r.localEndpoint = result.local
	r.editFlow.client = r.client
	r.editFlow.localEndpoint = r.localEndpoint.ID
	_ = oldClient.Close()
	if r.authCancel != nil {
		r.authCancel()
	}
	_ = r.authClient.Close()
	r.authClient = result.authClient
	r.startAuthLoop(r.authClient)
	for index, start := range r.daemonRecoveryStarts {
		pane := tui.PaneID(index)
		if start.host == "" {
			r.connectLocalPane(pane, start.path)
			continue
		}
		r.startConnection(pane, start, true, false, r.client)
	}
}

func (r *clientRuntime) handleConnectionResult(result clientConnectionResult) {
	if !r.connectionAttempts.Accept(result.pane, result.epoch) {
		return
	}
	if result.err != nil {
		r.paneRecoveries[result.pane].connectionFailed()
		state := connectionFailureState(r.model, result.pane, result.switching)
		r.model, _ = tui.Reduce(r.model, tui.PaneConnectionChanged{Pane: result.pane, State: state, Message: "connect " + result.host + " failed: " + clientErrorMessage(result.err)})
		return
	}
	if result.recovery {
		r.paneRecoveries[result.pane].connected()
	}
	r.reduceAndDispatch(tui.PaneConnected{
		Pane: result.pane, Endpoint: result.endpoint, Location: result.location, State: result.state,
		CapabilityGeneration: result.capabilityGeneration, Capabilities: result.capabilities, PreserveCommitted: result.recovery,
	})
}

func (r *clientRuntime) handleAction(action tui.Action) {
	if image, ok := action.(tui.PreviewTerminalImage); ok {
		if terminalImageCurrent(r.model, image) {
			copyImage := image
			copyImage.Data = append([]byte(nil), image.Data...)
			r.pendingTerminalImage = &copyImage
		}
		return
	}
	listingCurrent := true
	switch result := action.(type) {
	case tui.ListingPage:
		listingCurrent = listingResultCurrent(r.model, result.Pane, result.Generation)
	case tui.ListingFailed:
		listingCurrent = listingResultCurrent(r.model, result.Pane, result.Generation)
	}
	var intents []tui.Intent
	r.model, intents = tui.Reduce(r.model, action)
	switch typed := action.(type) {
	case tui.AuthChallengeReceived:
		for pane, paneState := range r.model.Panes {
			if paneState.Endpoint.SSHHostAlias == typed.Endpoint || paneState.Endpoint.DisplayName == "connecting "+typed.Endpoint {
				r.model, _ = tui.Reduce(r.model, tui.PaneConnectionChanged{Pane: tui.PaneID(pane), State: domain.StateAuthRequired, Message: "waiting for authentication"})
			}
		}
	case tui.ListingPage:
		if listingCurrent && typed.Done && r.paneRecoveries[typed.Pane].listingCompleted(typed) {
			r.model, _ = tui.Reduce(r.model, tui.PaneConnectionChanged{Pane: typed.Pane, State: domain.StateReady, Message: "reconnected at nearest accessible parent"})
		}
	case tui.ListingFailed:
		if listingCurrent {
			if fallback, ok := r.paneRecoveries[typed.Pane].listingFailed(typed); ok {
				r.dispatchIntent(fallback)
				return
			}
			if !typed.DaemonLost && typed.Retry == domain.RetryAfterReconnect && !r.paneRecoveries[typed.Pane].connecting() {
				pane := r.model.Panes[typed.Pane]
				if pane.Endpoint.Kind == domain.EndpointSSH && pane.Endpoint.SSHHostAlias != "" {
					r.startConnection(typed.Pane, startLocation{host: pane.Endpoint.SSHHostAlias, path: string(pane.Location.Path)}, true, false, r.client)
				}
			}
			if typed.DaemonLost {
				r.startDaemonRecovery()
			}
		}
	}
	for _, intent := range intents {
		r.dispatchIntent(intent)
	}
}

func (r *clientRuntime) run(locations [2]startLocation) error {
	for index, start := range locations {
		pane := tui.PaneID(index)
		if start.host == "" {
			r.dispatchIntent(tui.Intent{Kind: tui.IntentList, Pane: pane, Location: r.model.Panes[pane].Location})
			continue
		}
		r.startConnection(pane, start, false, false, r.client)
	}
	r.dispatchIntent(tui.Intent{Kind: tui.IntentEditRecoverable})

	jobRefreshTicker := time.NewTicker(500 * time.Millisecond)
	defer jobRefreshTicker.Stop()
	helperStatusTicker := time.NewTicker(time.Second)
	defer helperStatusTicker.Stop()
	helperStatusRefresh := make(chan struct{}, 1)
	leaseHeartbeatTicker := time.NewTicker(cache.DefaultLeaseHeartbeat)
	defer leaseHeartbeatTicker.Stop()

	for {
		tui.Render(tui.NewTCellSurface(r.screen), r.model, tui.RenderOptions{Overscan: 8, Keymap: r.userKeymap, Version: r.clientVersion})
		r.screen.Show()
		if r.pendingTerminalImage != nil {
			if err := writeTerminalImage(r.pendingTerminalImage.Data); err != nil {
				r.model.Notice = "terminal image output failed safely: " + err.Error()
			}
			r.pendingTerminalImage = nil
		}
		select {
		case <-r.ctx.Done():
			return nil
		case <-jobRefreshTicker.C:
			if jobListRefreshRequired(r.model) {
				r.dispatchIntent(tui.Intent{Kind: tui.IntentJobList})
			}
			if r.model.Drawer.Mode == tui.DrawerLog {
				r.dispatchIntent(tui.Intent{Kind: tui.IntentDiagnosticList, Limit: 256})
			}
		case <-helperStatusTicker.C:
			r.scheduleHelperStatusRefresh(helperStatusRefresh)
		case <-leaseHeartbeatTicker.C:
			if err := r.editFlow.Heartbeat(r.ctx); err != nil {
				r.model.Notice = "cache lease heartbeat failed; durable edit recovery remains retained: " + clientErrorMessage(err)
			}
		case lostClient := <-r.daemonLosses:
			if lostClient == r.client {
				r.startDaemonRecovery()
			}
		case err := <-r.authErrors:
			if authFailureLostDaemon(err, func() error {
				probeCtx, cancel := context.WithTimeout(r.ctx, daemonReadyTimeout)
				defer cancel()
				_, probeErr := daemonLocalEndpoint(probeCtx, r.client)
				return probeErr
			}) {
				r.startDaemonRecovery()
				continue
			}
			return err
		case request := <-r.connectRequests:
			r.handleConnectRequest(request)
		case result := <-r.daemonRecoveries:
			r.handleDaemonRecovery(result)
		case result := <-r.connections:
			r.handleConnectionResult(result)
		case action := <-r.actions:
			r.handleAction(action)
		case event, open := <-r.events:
			if !open {
				return nil
			}
			if key, ok := event.(*tcell.EventKey); ok && (key.Key() == tcell.KeyCtrlC || r.model.Mode == tui.ModeNormal && key.Str() == "q") {
				return nil
			}
			if _, ok := event.(*tcell.EventResize); ok {
				r.screen.Sync()
			}
			if action, ok := tui.TranslateTCellEventWithKeymap(event, r.model.Mode, r.userKeymap); ok {
				r.reduceAndDispatch(action)
			}
		}
	}
}

func (r *clientRuntime) scheduleHelperStatusRefresh(inflight chan struct{}) {
	targets := make([]helperStatusRefreshTarget, 0, 2)
	for pane := tui.Left; pane <= tui.Right; pane++ {
		state := r.model.Panes[pane]
		if state.Endpoint.Kind == domain.EndpointSSH && state.Connection == domain.StateReady && state.Capabilities.Revision.SessionID != "" {
			targets = append(targets, helperStatusRefreshTarget{pane: pane, endpointID: state.Endpoint.ID})
		}
	}
	if len(targets) == 0 {
		return
	}
	select {
	case inflight <- struct{}{}:
		go func(activeClient *daemon.Client, targets []helperStatusRefreshTarget) {
			defer func() { <-inflight }()
			refreshHelperStatus(r.ctx, activeClient, targets, r.actions)
		}(r.client, targets)
	default:
	}
}

func resolveStartupLocations(
	ctx context.Context,
	invocation clientInvocation,
	screen tcell.Screen,
	events <-chan tcell.Event,
	client *daemon.Client,
) ([2]startLocation, *workspace.Document, bool, error) {
	switch {
	case invocation.Workspace != "":
		var response workspace.LoadResponse
		if err := client.Call(ctx, daemon.WorkspaceLoad, workspace.LoadRequest{Name: invocation.Workspace}, &response); err != nil {
			locations, restored, pickErr := pickStartLocations(ctx, screen, events, client, "Cannot open workspace "+invocation.Workspace+": "+clientErrorMessage(err))
			if errors.Is(pickErr, errPickerCanceled) {
				return [2]startLocation{}, nil, true, nil
			}
			return locations, restored, false, pickErr
		}
		locations, err := workspaceStartLocations(response.Document)
		if err != nil {
			return [2]startLocation{}, nil, false, err
		}
		return locations, &response.Document, false, nil
	case invocation.Pick:
		locations, restored, err := pickStartLocations(ctx, screen, events, client, "")
		if errors.Is(err, errPickerCanceled) {
			return [2]startLocation{}, nil, true, nil
		}
		return locations, restored, false, err
	default:
		locations, err := startLocations(invocation.Locations)
		return locations, nil, false, err
	}
}
