//go:build darwin || linux

package app

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"runtime"
	"time"

	"github.com/TyrantLucifer/awesome-mac-sftp/internal/cache"
	"github.com/TyrantLucifer/awesome-mac-sftp/internal/cacheprocess"
	"github.com/TyrantLucifer/awesome-mac-sftp/internal/daemon"
	"github.com/TyrantLucifer/awesome-mac-sftp/internal/domain"
	"github.com/TyrantLucifer/awesome-mac-sftp/internal/edit"
	"github.com/TyrantLucifer/awesome-mac-sftp/internal/externalprocess"
	"github.com/TyrantLucifer/awesome-mac-sftp/internal/ipc"
	"github.com/TyrantLucifer/awesome-mac-sftp/internal/state/editstore"
	"github.com/TyrantLucifer/awesome-mac-sftp/internal/terminalhandoff"
	"github.com/TyrantLucifer/awesome-mac-sftp/internal/transfer"
	"github.com/TyrantLucifer/awesome-mac-sftp/internal/tui"
	"golang.org/x/sys/unix"
)

type editRPCCaller interface {
	Call(context.Context, string, any, any) error
}

type preparedExternalEdit struct {
	command string
	run     func(context.Context) (terminalhandoff.Result, error)
}

type externalEditPreparer func(context.Context, string, edit.Purpose) (preparedExternalEdit, error)

type liveEditSession struct {
	machine       edit.Session
	recordVersion int64
	materialized  daemon.CacheMaterializeResponse
	ownerKind     cache.LeaseOwnerKind
	ownerID       string
	pane          tui.PaneID
	target        domain.Location
	lastExit      terminalhandoff.Result
	prepared      preparedExternalEdit
}

type editCoordinator struct {
	client        editRPCCaller
	localEndpoint domain.EndpointID
	workspaceID   cache.WorkspaceID
	policy        cache.Policy
	now           func() time.Time
	prepare       externalEditPreparer
	sessions      map[edit.SessionID]*liveEditSession
}

func newEditCoordinator(client editRPCCaller, localEndpoint domain.EndpointID, workspaceID cache.WorkspaceID, policy cache.Policy, prepare externalEditPreparer) (*editCoordinator, error) {
	if client == nil || localEndpoint == "" || workspaceID == "" || len(workspaceID) > 128 || prepare == nil {
		return nil, errors.New("create edit coordinator: client, local endpoint, workspace, and external preparer are required")
	}
	switch policy {
	case cache.PolicyLRU, cache.PolicyEphemeral, cache.PolicyPinnedOffline:
	default:
		return nil, fmt.Errorf("create edit coordinator: invalid cache policy %q", policy)
	}
	return &editCoordinator{client: client, localEndpoint: localEndpoint, workspaceID: workspaceID, policy: policy, now: time.Now, prepare: prepare, sessions: make(map[edit.SessionID]*liveEditSession)}, nil
}

func (coordinator *editCoordinator) Begin(ctx context.Context, intent tui.Intent) tui.Action {
	purpose, ownerKind, err := editIntentPurpose(intent.Kind)
	if err != nil {
		return tui.EditSessionFailed{Pane: intent.Pane, Location: intent.Location, Message: err.Error()}
	}
	sessionID, err := randomEditSessionID()
	if err != nil {
		return tui.EditSessionFailed{Pane: intent.Pane, Location: intent.Location, Message: "edit failed: " + err.Error()}
	}
	processIdentity, err := cacheprocess.CurrentIdentity()
	if err != nil {
		return tui.EditSessionFailed{Pane: intent.Pane, Location: intent.Location, Message: "materialize edit target: " + err.Error()}
	}
	var materialized daemon.CacheMaterializeResponse
	err = coordinator.client.Call(ctx, daemon.CacheMaterialize, daemon.CacheMaterializeRequest{
		Location: ipc.EncodeLocation(intent.Location), WorkspaceID: coordinator.workspaceID, Policy: coordinator.policy,
		Pinned: coordinator.policy == cache.PolicyPinnedOffline, OwnerKind: ownerKind, OwnerID: string(sessionID), Process: &processIdentity,
	}, &materialized)
	if err != nil {
		return tui.EditSessionFailed{Pane: intent.Pane, Location: intent.Location, Message: "materialize edit target: " + clientErrorMessage(err)}
	}
	releaseOnFailure := true
	defer func() {
		if releaseOnFailure {
			_ = coordinator.release(context.Background(), materialized, ownerKind, string(sessionID))
		}
	}()
	localSHA, _, err := hashRegularFile(materialized.Path)
	if err != nil {
		return tui.EditSessionFailed{Pane: intent.Pane, Location: intent.Location, Message: "verify materialized edit target: " + err.Error()}
	}
	machine, err := edit.NewSession(edit.NewSessionRequest{ID: sessionID, Purpose: purpose, Baseline: edit.Baseline{
		SourceEntryID: materialized.EntryID, MaterializationID: materialized.MaterializationID, Target: intent.Location,
		ExpectedRemote: edit.RemotePrecondition{Presence: edit.ExpectedPresent, Kind: domain.EntryFile, Fingerprint: ipc.DecodeFingerprint(materialized.SourceFingerprint)},
		LocalSHA256:    localSHA,
	}})
	if err != nil {
		return tui.EditSessionFailed{Pane: intent.Pane, Location: intent.Location, Message: "create edit baseline: " + err.Error()}
	}
	eventID, err := randomEditEventID()
	if err != nil {
		return tui.EditSessionFailed{Pane: intent.Pane, Location: intent.Location, Message: "create edit event: " + err.Error()}
	}
	var created daemon.EditSessionResponse
	err = coordinator.client.Call(ctx, daemon.EditSessionCreate, daemon.EditSessionCreateRequest{Request: editstore.CreateRequest{
		SessionID: string(sessionID), SourceEntryID: materialized.EntryID, MaterializationID: materialized.MaterializationID,
		LocalState: editstore.LocalClean, RemoteState: editstore.RemoteUnchanged, State: editstore.StateEditing,
		EventID: eventID, EventKind: "external_started", Now: coordinator.now(), Details: editstore.Details{
			ReferenceID: materialized.ReferenceID, LeaseID: materialized.LeaseID, Persistent: machine.Persistent(),
			Target: intent.Location, ExpectedRemote: machine.Baseline().ExpectedRemote,
		},
	}}, &created)
	if err != nil {
		return tui.EditSessionFailed{Pane: intent.Pane, Location: intent.Location, Message: "persist edit session: " + clientErrorMessage(err)}
	}
	live := &liveEditSession{machine: machine, recordVersion: created.Session.StateVersion, materialized: materialized, ownerKind: ownerKind, ownerID: string(sessionID), pane: intent.Pane, target: intent.Location}
	coordinator.sessions[sessionID] = live
	releaseOnFailure = false
	live.prepared, err = coordinator.prepare(ctx, materialized.Path, purpose)
	if err != nil {
		return tui.EditSessionFailed{Pane: intent.Pane, Location: intent.Location, Message: "prepare external process: " + err.Error() + "; materialization retained for recovery"}
	}
	return tui.EditLaunchReady{SessionID: sessionID, Pane: intent.Pane, Location: intent.Location, Command: live.prepared.command}
}

func (coordinator *editCoordinator) Launch(ctx context.Context, sessionID edit.SessionID) tui.Action {
	live := coordinator.sessions[sessionID]
	if live == nil || live.prepared.run == nil {
		return tui.EditSessionFailed{Message: "external launch failed: session is not active; retained recovery metadata is unchanged"}
	}
	run := live.prepared.run
	live.prepared.run = nil
	result, err := run(ctx)
	live.lastExit = result
	launchMessage := externalExitMessage(live.lastExit, err)
	if live.machine.Purpose() == edit.PurposeOpener {
		return tui.EditSessionObserved{SessionID: sessionID, Pane: live.pane, Location: live.target, State: edit.StateReady, Message: launchMessage + "; lease retained until explicit change check"}
	}
	return coordinator.observe(ctx, live, launchMessage)
}

func (coordinator *editCoordinator) Check(ctx context.Context, sessionID edit.SessionID) tui.Action {
	live := coordinator.sessions[sessionID]
	if live == nil {
		return tui.EditSessionFailed{Message: "edit check failed: session is not active in this client; retained recovery metadata is unchanged"}
	}
	return coordinator.observe(ctx, live, "change check completed")
}

func (coordinator *editCoordinator) observe(ctx context.Context, live *liveEditSession, message string) tui.Action {
	next, err := live.machine.BeginObservation(live.machine.Version())
	if err != nil {
		return tui.EditSessionFailed{Pane: live.pane, Location: live.target, Message: "begin edit observation: " + err.Error()}
	}
	if err := coordinator.transition(ctx, live, next, editstore.LocalUnknown, editstore.RemoteUnknown, editstore.StateObserving, "observation_started", "", edit.Decision{}, nil); err != nil {
		return tui.EditSessionFailed{Pane: live.pane, Location: live.target, Message: "persist edit observation: " + clientErrorMessage(err)}
	}
	local, _ := observeLocal(live.materialized.Path)
	remote := coordinator.observeRemote(ctx, live.target)
	if local.Status == edit.LocalPresent && local.SHA256 != live.machine.Baseline().LocalSHA256 {
		authoritativeSHA, _, markErr := coordinator.markDirty(ctx, live)
		if markErr != nil {
			return tui.EditSessionFailed{Pane: live.pane, Location: live.target, Message: "retain dirty edit materialization: " + clientErrorMessage(markErr)}
		}
		local.SHA256 = authoritativeSHA
	}
	next, evaluation, err := live.machine.Observe(live.machine.Version(), edit.PostEditorObservation{Local: local, Remote: remote})
	if err != nil {
		return tui.EditSessionFailed{Pane: live.pane, Location: live.target, Message: "evaluate edit result: " + err.Error()}
	}
	localState, remoteState, durableState := durableObservationState(next.State(), evaluation, local, remote)
	if err := coordinator.transition(ctx, live, next, localState, remoteState, durableState, "observation_completed", "", edit.Decision{}, nil); err != nil {
		return tui.EditSessionFailed{Pane: live.pane, Location: live.target, Message: "persist edit result: " + clientErrorMessage(err)}
	}
	if next.State() == edit.StateNoChanges {
		if err := coordinator.finishWithoutUpload(ctx, live, edit.Decision{Kind: edit.DecisionSkip}, "no local or remote changes"); err != nil {
			return tui.EditSessionFailed{Pane: live.pane, Location: live.target, Message: "finish unchanged edit: " + clientErrorMessage(err)}
		}
		return tui.EditSessionFinished{Pane: live.pane, Location: live.target, Message: message + "; no changes detected and no upload started"}
	}
	return tui.EditSessionObserved{SessionID: live.machine.ID(), Pane: live.pane, Location: live.target, State: live.machine.State(), Message: message}
}

func (coordinator *editCoordinator) Decide(ctx context.Context, intent tui.Intent) tui.Action {
	live := coordinator.sessions[intent.EditSessionID]
	if live == nil {
		return tui.EditSessionFailed{Pane: intent.Pane, Location: intent.Location, Message: "edit decision failed: session is not active; retained recovery metadata is unchanged"}
	}
	decision := edit.Decision{Kind: intent.EditDecision, SaveAsTarget: intent.SaveAsTarget, ObservedRemote: live.machine.LastRemoteObservation()}
	if decision.Kind == edit.DecisionOverwrite {
		decision.AuditReason = "user explicitly confirmed overwrite after concurrent remote change"
	}
	if decision.Kind == edit.DecisionSkip {
		if err := coordinator.finishWithoutUpload(ctx, live, decision, "user skipped upload"); err != nil {
			return tui.EditSessionFailed{Pane: live.pane, Location: live.target, Message: "skip edit upload: " + clientErrorMessage(err)}
		}
		message := "edit upload skipped"
		if intent.RefreshAfterEdit {
			message = "remote change accepted; pane refresh requested"
		}
		return tui.EditSessionFinished{Pane: live.pane, Location: live.target, Message: message}
	}
	next, syncBack, err := live.machine.Decide(live.machine.Version(), decision)
	if err != nil {
		return tui.EditSessionObserved{SessionID: live.machine.ID(), Pane: live.pane, Location: live.target, State: live.machine.State(), Message: "edit decision rejected: " + err.Error()}
	}
	localState, remoteState, lifecycle := durableObservationState(live.machine.State(), live.machine.Evaluation(), edit.LocalObservation{Status: edit.LocalPresent, SHA256: live.machine.CurrentLocalSHA256()}, live.machine.LastRemoteObservation())
	if lifecycle != editstore.StateConflict {
		lifecycle = editstore.StateAwaitingDecision
	}
	if err := coordinator.transition(ctx, live, next, localState, remoteState, lifecycle, "sync_back_frozen", "", decision, syncBack); err != nil {
		return tui.EditSessionFailed{Pane: live.pane, Location: live.target, Message: "persist frozen sync-back: " + clientErrorMessage(err)}
	}
	localLocation, err := domain.NewLocation(coordinator.localEndpoint, domain.CanonicalPath(live.materialized.Path))
	if err != nil {
		return tui.EditSessionFailed{Pane: live.pane, Location: live.target, Message: "capture sync-back source: " + err.Error()}
	}
	var captured daemon.JobCaptureResponse
	if err := coordinator.client.Call(ctx, daemon.JobCapture, daemon.JobCaptureRequest{Location: ipc.EncodeLocation(localLocation)}, &captured); err != nil {
		return tui.EditSessionFailed{Pane: live.pane, Location: live.target, Message: "capture sync-back source: " + clientErrorMessage(err)}
	}
	var created daemon.JobSnapshotResponse
	if err := coordinator.client.Call(ctx, daemon.JobCreateSyncBack, daemon.JobCreateSyncBackRequest{Intent: transfer.SyncBackIntent{SyncBack: *syncBack, Source: captured.Reference}}, &created); err != nil {
		return tui.EditSessionFailed{Pane: live.pane, Location: live.target, Message: "create sync-back Job: " + clientErrorMessage(err)}
	}
	delete(coordinator.sessions, live.machine.ID())
	return tui.JobCreated{JobID: created.Snapshot.JobID, State: created.Snapshot.State, Message: "sync-back Job created after explicit confirmation"}
}

func (coordinator *editCoordinator) finishWithoutUpload(ctx context.Context, live *liveEditSession, decision edit.Decision, reason string) error {
	next, _, err := live.machine.Decide(live.machine.Version(), decision)
	if err != nil {
		return err
	}
	localState, remoteState, _ := durableObservationState(live.machine.State(), live.machine.Evaluation(), edit.LocalObservation{Status: edit.LocalPresent, SHA256: live.machine.CurrentLocalSHA256()}, live.machine.LastRemoteObservation())
	if err := coordinator.transition(ctx, live, next, localState, remoteState, editstore.StateAbandoned, "upload_skipped", "", decision, nil); err != nil {
		return err
	}
	if err := coordinator.release(ctx, live.materialized, live.ownerKind, live.ownerID); err != nil {
		return err
	}
	delete(coordinator.sessions, live.machine.ID())
	_ = reason
	return nil
}

func (coordinator *editCoordinator) transition(ctx context.Context, live *liveEditSession, next edit.Session, local editstore.LocalState, remote editstore.RemoteState, state editstore.State, eventKind, errorCode string, decision edit.Decision, frozen *edit.SyncBackRequest) error {
	eventID, err := randomEditEventID()
	if err != nil {
		return err
	}
	request := editstore.TransitionRequest{
		SessionID: string(live.machine.ID()), ExpectedVersion: live.recordVersion, LocalState: local, RemoteState: remote,
		State: state, EventID: eventID, EventKind: eventKind, ErrorCode: errorCode, Persistent: next.Persistent(), Now: coordinator.now(),
	}
	if decision.Kind != "" {
		request.DecisionKind, request.AuditReason = decision.Kind, decision.AuditReason
		if frozen != nil {
			request.Target = frozen.Target
			request.ExpectedRemote = frozen.DestinationPrecondition
		}
	}
	var response daemon.EditSessionResponse
	if err := coordinator.client.Call(ctx, daemon.EditSessionTransition, daemon.EditSessionTransitionRequest{Request: request}, &response); err != nil {
		return err
	}
	live.machine = next
	live.recordVersion = response.Session.StateVersion
	return nil
}

func (coordinator *editCoordinator) markDirty(ctx context.Context, live *liveEditSession) (edit.SHA256, uint64, error) {
	var response daemon.CacheMarkDirtyResponse
	err := coordinator.client.Call(ctx, daemon.CacheMarkDirty, daemon.CacheMarkDirtyRequest{
		MaterializationID: live.materialized.MaterializationID, ReferenceID: live.materialized.ReferenceID, LeaseID: live.materialized.LeaseID,
		OwnerKind: live.ownerKind, OwnerID: live.ownerID,
	}, &response)
	if err != nil {
		return "", 0, err
	}
	sha, err := edit.ParseSHA256(string(response.CurrentSHA256))
	if err != nil || response.Size < 0 {
		return "", 0, errors.New("mark cache materialization dirty: daemon returned an invalid identity")
	}
	return sha, uint64(response.Size), nil
}

func (coordinator *editCoordinator) release(ctx context.Context, materialized daemon.CacheMaterializeResponse, owner cache.LeaseOwnerKind, ownerID string) error {
	var response daemon.CacheReleaseHandoffResponse
	return coordinator.client.Call(ctx, daemon.CacheReleaseHandoff, daemon.CacheReleaseHandoffRequest{
		MaterializationID: materialized.MaterializationID, ReferenceID: materialized.ReferenceID, LeaseID: materialized.LeaseID,
		OwnerKind: owner, OwnerID: ownerID,
	}, &response)
}

func (coordinator *editCoordinator) observeRemote(ctx context.Context, location domain.Location) edit.RemoteObservation {
	var response ipc.ProviderStatResponse
	err := coordinator.client.Call(ctx, daemon.ProviderStat, ipc.ProviderStatRequest{Location: ipc.EncodeLocation(location)}, &response)
	if domain.IsCode(err, domain.CodeNotFound) {
		return edit.RemoteObservation{Status: edit.RemoteDeleted}
	}
	if err != nil {
		return edit.RemoteObservation{Status: edit.RemoteUnavailable}
	}
	entry, err := ipc.DecodeEntry(response.Entry)
	if err != nil {
		return edit.RemoteObservation{Status: edit.RemoteUnknown}
	}
	return edit.RemoteObservation{Status: edit.RemotePresent, Kind: entry.Kind, Fingerprint: entry.Fingerprint}
}

func observeLocal(path string) (edit.LocalObservation, uint64) {
	sha, size, err := hashRegularFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return edit.LocalObservation{Status: edit.LocalDeleted}, 0
	}
	if err != nil {
		return edit.LocalObservation{Status: edit.LocalUnavailable}, 0
	}
	return edit.LocalObservation{Status: edit.LocalPresent, SHA256: sha}, size
}

func hashRegularFile(path string) (edit.SHA256, uint64, error) {
	descriptor, err := unix.Open(path, unix.O_RDONLY|unix.O_CLOEXEC|unix.O_NOFOLLOW, 0)
	if err != nil {
		return "", 0, err
	}
	file := os.NewFile(uintptr(descriptor), path)
	if file == nil {
		_ = unix.Close(descriptor)
		return "", 0, errors.New("hash regular file: invalid descriptor")
	}
	defer file.Close()
	before, err := file.Stat()
	if err != nil || !before.Mode().IsRegular() || before.Mode().Perm()&0o077 != 0 {
		return "", 0, errors.New("hash regular file: materialization is not a private regular file")
	}
	hash := sha256.New()
	if _, err := io.Copy(hash, file); err != nil {
		return "", 0, err
	}
	after, err := file.Stat()
	if err != nil || !os.SameFile(before, after) || before.Size() != after.Size() || !before.ModTime().Equal(after.ModTime()) {
		return "", 0, errors.New("hash regular file: materialization changed while hashing")
	}
	value, err := edit.ParseSHA256(hex.EncodeToString(hash.Sum(nil)))
	return value, uint64(after.Size()), err
}

func durableObservationState(state edit.State, evaluation edit.Evaluation, local edit.LocalObservation, remote edit.RemoteObservation) (editstore.LocalState, editstore.RemoteState, editstore.State) {
	localState := editstore.LocalUnknown
	if local.Status == edit.LocalDeleted {
		localState = editstore.LocalDeleted
	} else if local.Status == edit.LocalPresent {
		localState = editstore.LocalClean
		if evaluation.LocalChanged {
			localState = editstore.LocalDirty
		}
	}
	remoteState := editstore.RemoteUnavailable
	switch remote.Status {
	case edit.RemoteDeleted:
		remoteState = editstore.RemoteDeleted
	case edit.RemotePresent:
		remoteState = editstore.RemoteUnchanged
		if evaluation.RemoteChanged {
			remoteState = editstore.RemoteModified
		}
	case edit.RemoteUnknown:
		remoteState = editstore.RemoteUnknown
	}
	lifecycle := editstore.StateAwaitingDecision
	switch state {
	case edit.StateConflict:
		lifecycle = editstore.StateConflict
	case edit.StateRecoveryRequired:
		lifecycle = editstore.StateRecovery
	}
	return localState, remoteState, lifecycle
}

func editIntentPurpose(kind tui.IntentKind) (edit.Purpose, cache.LeaseOwnerKind, error) {
	switch kind {
	case tui.IntentEdit:
		return edit.PurposeEditor, cache.LeaseOwnerEditor, nil
	case tui.IntentOpenExternal:
		return edit.PurposeOpener, cache.LeaseOwnerOpener, nil
	default:
		return "", "", fmt.Errorf("edit failed: unsupported intent %q", kind)
	}
}

func randomEditSessionID() (edit.SessionID, error) {
	var value [16]byte
	if _, err := rand.Read(value[:]); err != nil {
		return "", err
	}
	return edit.ParseSessionID(hex.EncodeToString(value[:]))
}

func randomEditEventID() (string, error) {
	var value [16]byte
	if _, err := rand.Read(value[:]); err != nil {
		return "", err
	}
	return hex.EncodeToString(value[:]), nil
}

func externalExitMessage(result terminalhandoff.Result, err error) string {
	if err != nil {
		return "external process failed after terminal recovery: " + err.Error()
	}
	switch result.Kind {
	case terminalhandoff.ExitNormal:
		return "external process exited"
	case terminalhandoff.ExitNonZero:
		return fmt.Sprintf("external process exited %d; local changes were still checked", result.ExitCode)
	case terminalhandoff.ExitSignaled:
		return "external process terminated by " + result.Signal + "; local changes were still checked"
	default:
		return "external process lost its terminal; local changes were still checked"
	}
}

func prepareExternalEdit(ctx context.Context, materialization string, purpose edit.Purpose, environment []string, screen handoffTCellScreen) (preparedExternalEdit, error) {
	environmentMap := make(map[string]string)
	for _, entry := range environment {
		for index := range entry {
			if entry[index] == '=' {
				environmentMap[entry[:index]] = entry[index+1:]
				break
			}
		}
	}
	var resolved externalprocess.ResolvedCommand
	var err error
	if purpose == edit.PurposeEditor {
		resolved, err = externalprocess.ResolveEditor(nil, environmentMap, environmentMap["PATH"])
	} else {
		resolved, err = externalprocess.ResolveOpener(nil, runtime.GOOS, environmentMap["PATH"])
	}
	if err != nil {
		return preparedExternalEdit{}, err
	}
	plan, err := externalprocess.NewPlan(resolved, materialization, environment)
	if err != nil {
		return preparedExternalEdit{}, err
	}
	command, err := plan.CommandContext(ctx)
	if err != nil {
		return preparedExternalEdit{}, err
	}
	display := command.Path
	for _, argument := range command.Args[1:] {
		display += " " + fmt.Sprintf("%q", argument)
	}
	return preparedExternalEdit{command: display, run: func(runCtx context.Context) (terminalhandoff.Result, error) {
		// Revalidate the frozen executable and materialization identities at the
		// final pre-exec boundary after user confirmation.
		command, err := plan.CommandContext(runCtx)
		if err != nil {
			return terminalhandoff.Result{}, err
		}
		tty, err := os.OpenFile("/dev/tty", os.O_RDWR, 0)
		if err != nil {
			return terminalhandoff.Result{}, err
		}
		defer tty.Close()
		platform, err := terminalhandoff.NewUnixPlatform(tty)
		if err != nil {
			return terminalhandoff.Result{}, err
		}
		controller, err := terminalhandoff.NewController(newTCellHandoffScreen(screen), platform)
		if err != nil {
			return terminalhandoff.Result{}, err
		}
		launcher, err := terminalhandoff.NewExecLauncher(command.Path, command.Args[1:], command.Env, command.Dir)
		if err != nil {
			return terminalhandoff.Result{}, err
		}
		return controller.Run(runCtx, launcher)
	}}, nil
}
