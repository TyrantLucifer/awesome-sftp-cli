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
	"path/filepath"
	"reflect"
	"strings"
	"time"

	"github.com/TyrantLucifer/awesome-mac-sftp/internal/cache"
	"github.com/TyrantLucifer/awesome-mac-sftp/internal/cachefs"
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
	recovery      editstore.RecoveryRecord
	process       *cache.ProcessIdentity
}

type recoverableEdit struct {
	record     editstore.RecoveryRecord
	path       string
	usable     bool
	diagnostic string
}

type editCoordinator struct {
	client                      editRPCCaller
	localEndpoint               domain.EndpointID
	workspaceID                 cache.WorkspaceID
	policy                      cache.Policy
	now                         func() time.Time
	prepare                     externalEditPreparer
	sessions                    map[edit.SessionID]*liveEditSession
	recoverable                 map[edit.SessionID]recoverableEdit
	recoveryMaterializationPath func(cache.MaterializationID) (string, error)
	heartbeatEvery              time.Duration
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
	return &editCoordinator{client: client, localEndpoint: localEndpoint, workspaceID: workspaceID, policy: policy, now: time.Now, prepare: prepare, sessions: make(map[edit.SessionID]*liveEditSession), recoverable: make(map[edit.SessionID]recoverableEdit), heartbeatEvery: cache.DefaultLeaseHeartbeat}, nil
}

func (coordinator *editCoordinator) SetPolicy(policy cache.Policy) error {
	if coordinator == nil {
		return errors.New("set edit cache policy: nil coordinator")
	}
	switch policy {
	case cache.PolicyLRU, cache.PolicyEphemeral, cache.PolicyPinnedOffline:
		coordinator.policy = policy
		return nil
	default:
		return fmt.Errorf("set edit cache policy: invalid policy %q", policy)
	}
}

// ConfigureRecoveryCacheRoot installs the deterministic, read-only resolver
// used to reopen an exact durable materialization after a client restart.
func (coordinator *editCoordinator) ConfigureRecoveryCacheRoot(cacheRoot string) error {
	if coordinator == nil || !filepath.IsAbs(cacheRoot) || filepath.Clean(cacheRoot) != cacheRoot {
		return errors.New("configure edit recovery: cache root must be canonical and absolute")
	}
	coordinator.recoveryMaterializationPath = func(id cache.MaterializationID) (string, error) {
		parsed, err := cache.ParseMaterializationID(string(id))
		if err != nil {
			return "", err
		}
		return filepath.Join(cacheRoot, cachefs.ContentRootName, "materializations", string(parsed), "content"), nil
	}
	return nil
}

func (coordinator *editCoordinator) Recoverable(ctx context.Context, limit int) tui.Action {
	if limit < 1 || limit > tui.MaxRecoverableEditSessions {
		return tui.EditRecoveryLoaded{Message: "list recoverable edits failed: limit must be in [1,64]"}
	}
	var response daemon.EditSessionRecoverableResponse
	if err := coordinator.client.Call(ctx, daemon.EditSessionRecoverable, daemon.EditSessionRecoverableRequest{Limit: limit}, &response); err != nil {
		return tui.EditRecoveryLoaded{Message: "list recoverable edits failed: " + clientErrorMessage(err)}
	}
	coordinator.recoverable = make(map[edit.SessionID]recoverableEdit, len(response.Sessions))
	items := make([]tui.EditRecoveryItem, 0, len(response.Sessions))
	for _, record := range response.Sessions {
		recovery, item := coordinator.inspectRecoverable(record)
		items = append(items, item)
		if item.SessionID != "" {
			coordinator.recoverable[item.SessionID] = recovery
		}
	}
	return tui.EditRecoveryLoaded{Count: len(items), Sessions: items}
}

func (coordinator *editCoordinator) inspectRecoverable(record editstore.RecoveryRecord) (recoverableEdit, tui.EditRecoveryItem) {
	item := tui.EditRecoveryItem{Location: record.Target, Lifecycle: string(record.State), UpdatedAt: record.UpdatedAt, Decision: record.DecisionKind}
	sessionID, idErr := edit.ParseSessionID(record.SessionID)
	if idErr == nil {
		item.SessionID = sessionID
	}
	machine, machineErr := edit.RestorePersistent(record.Persistent)
	if machineErr == nil {
		item.Purpose, item.State = machine.Purpose(), machine.State()
		if item.Location.EndpointID == "" {
			item.Location = machine.Baseline().Target
		}
	}
	recovery := recoverableEdit{record: record}
	fail := func(message string) (recoverableEdit, tui.EditRecoveryItem) {
		recovery.diagnostic = boundedRecoveryDiagnostic(message)
		item.Diagnostic = recovery.diagnostic
		return recovery, item
	}
	if idErr != nil || machineErr != nil || machine.ID() != sessionID {
		return fail("durable edit machine metadata is invalid")
	}
	baseline := machine.Baseline()
	if baseline.SourceEntryID != record.SourceEntryID || baseline.MaterializationID != record.MaterializationID || baseline.Target.EndpointID == "" {
		return fail("durable edit and cache identities disagree")
	}
	if _, err := cache.ParseEntryID(string(record.SourceEntryID)); err != nil {
		return fail("durable cache entry identity is invalid")
	}
	if _, err := cache.ParseMaterializationID(string(record.MaterializationID)); err != nil {
		return fail("durable materialization identity is invalid")
	}
	if _, err := cache.ParseReferenceID(string(record.ReferenceID)); err != nil {
		return fail("durable cache reference identity is invalid")
	}
	if _, err := cache.ParseLeaseID(string(record.LeaseID)); err != nil {
		return fail("durable cache lease identity is invalid")
	}
	if coordinator.recoveryMaterializationPath == nil {
		return fail("client cache root is unavailable; session remains retained")
	}
	path, err := coordinator.recoveryMaterializationPath(record.MaterializationID)
	if err != nil || !filepath.IsAbs(path) || filepath.Clean(path) != path {
		return fail("exact materialization path could not be resolved")
	}
	recovery.path = path
	currentSHA, _, err := hashRegularFile(path)
	if err != nil {
		return fail("exact materialization is unavailable: " + err.Error())
	}
	switch machine.State() {
	case edit.StateReady, edit.StateObserving:
	case edit.StateNoChanges, edit.StateAwaitingUploadConfirmation, edit.StateRemoteChanged, edit.StateConflict, edit.StateSyncBackFrozen:
		if machine.CurrentLocalSHA256() == "" || currentSHA != machine.CurrentLocalSHA256() {
			return fail("materialization changed after the durable observation; automatic recovery is refused")
		}
	default:
		return fail("durable lifecycle requires manual retention and inspection")
	}
	if !recoverableLifecycleMatches(record.State, machine.State()) {
		return fail("durable lifecycle and edit machine state disagree")
	}
	if record.State == editstore.StateUploading {
		return fail("sync-back is already bound to a durable Job; inspect Jobs before retrying")
	}
	if machine.State() == edit.StateSyncBackFrozen {
		switch record.DecisionKind {
		case edit.DecisionUpload:
			if record.Target != baseline.Target || !reflect.DeepEqual(record.ExpectedRemote, baseline.ExpectedRemote) || record.AuditReason != "" {
				return fail("prepared upload metadata disagrees with the durable baseline")
			}
		case edit.DecisionSaveAs:
			if record.ExpectedRemote != (edit.RemotePrecondition{Presence: edit.ExpectedAbsent}) || record.AuditReason != "" {
				return fail("prepared save-as metadata is invalid")
			}
		case edit.DecisionOverwrite:
			if record.Target != baseline.Target || record.ExpectedRemote.Presence != edit.ExpectedPresent || len(record.AuditReason) < 1 || len(record.AuditReason) > 256 || strings.IndexFunc(record.AuditReason, func(value rune) bool { return value < 0x20 || value == 0x7f }) >= 0 {
				return fail("prepared overwrite metadata is invalid")
			}
		default:
			return fail("prepared sync-back decision metadata is invalid")
		}
	}
	recovery.usable, item.Usable = true, true
	return recovery, item
}

func recoverableLifecycleMatches(lifecycle editstore.State, state edit.State) bool {
	switch state {
	case edit.StateReady:
		return lifecycle == editstore.StatePreparing || lifecycle == editstore.StateEditing
	case edit.StateObserving:
		return lifecycle == editstore.StateObserving
	case edit.StateNoChanges, edit.StateAwaitingUploadConfirmation, edit.StateRemoteChanged:
		return lifecycle == editstore.StateAwaitingDecision
	case edit.StateConflict:
		return lifecycle == editstore.StateConflict
	case edit.StateSyncBackFrozen:
		return lifecycle == editstore.StateAwaitingDecision || lifecycle == editstore.StateConflict || lifecycle == editstore.StateUploading
	default:
		return false
	}
}

func (coordinator *editCoordinator) Resume(ctx context.Context, sessionID edit.SessionID, pane tui.PaneID) tui.Action {
	if pane != tui.Left && pane != tui.Right {
		return tui.EditSessionFailed{Message: "resume edit session: invalid pane; recovery metadata retained"}
	}
	if live := coordinator.sessions[sessionID]; live != nil {
		return coordinator.recoveredSessionAction(ctx, live, "edit session is already rebound in this client")
	}
	recovery, ok := coordinator.recoverable[sessionID]
	if !ok || !recovery.usable {
		message := "recoverable session is not loaded; recovery metadata retained"
		if ok && recovery.diagnostic != "" {
			message = recovery.diagnostic
		}
		return tui.EditSessionFailed{Pane: pane, Location: recovery.record.Target, Message: "resume edit session: " + message}
	}
	machine, err := edit.RestorePersistent(recovery.record.Persistent)
	if err != nil {
		return tui.EditSessionFailed{Pane: pane, Location: recovery.record.Target, Message: "resume edit session: durable machine is invalid; recovery metadata retained"}
	}
	ownerKind := cache.LeaseOwnerEditor
	if machine.Purpose() == edit.PurposeOpener {
		ownerKind = cache.LeaseOwnerOpener
	}
	baseline := machine.Baseline()
	live := &liveEditSession{
		machine: machine, recordVersion: recovery.record.StateVersion,
		materialized: daemon.CacheMaterializeResponse{EntryID: recovery.record.SourceEntryID, MaterializationID: recovery.record.MaterializationID,
			ReferenceID: recovery.record.ReferenceID, LeaseID: recovery.record.LeaseID, Path: recovery.path, SourceFingerprint: ipc.EncodeFingerprint(baseline.ExpectedRemote.Fingerprint)},
		ownerKind: ownerKind, ownerID: string(sessionID), pane: pane, target: baseline.Target, recovery: recovery.record,
	}
	coordinator.sessions[sessionID] = live
	return coordinator.recoveredSessionAction(ctx, live, "cold-start edit session rebound to its exact retained materialization; no upload was started")
}

func (coordinator *editCoordinator) recoveredSessionAction(ctx context.Context, live *liveEditSession, message string) tui.Action {
	state := live.machine.State()
	if state == edit.StateObserving {
		state = edit.StateReady
		message += "; Enter completes the interrupted observation"
	}
	return tui.EditSessionObserved{
		SessionID: live.machine.ID(), Pane: live.pane, Location: live.target, State: state,
		Message: message, Decision: live.recovery.DecisionKind, ConflictView: coordinator.conflictView(ctx, live),
	}
}

func boundedRecoveryDiagnostic(message string) string {
	const limit = 240
	runes := []rune(message)
	if len(runes) > limit {
		return string(runes[:limit]) + "…"
	}
	return message
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
	live := &liveEditSession{machine: machine, recordVersion: created.Session.StateVersion, materialized: materialized, ownerKind: ownerKind, ownerID: string(sessionID), pane: intent.Pane, target: intent.Location, process: &processIdentity}
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
	result, heartbeatErr, err := coordinator.runWithHeartbeat(ctx, live, run)
	live.lastExit = result
	launchMessage := externalExitMessage(live.lastExit, err)
	if heartbeatErr != nil {
		launchMessage += "; cache lease heartbeat failed: " + clientErrorMessage(heartbeatErr)
	}
	if live.machine.Purpose() == edit.PurposeOpener {
		return tui.EditSessionObserved{SessionID: sessionID, Pane: live.pane, Location: live.target, State: edit.StateReady, Message: launchMessage + "; lease retained until explicit change check"}
	}
	return coordinator.observe(ctx, live, launchMessage)
}

func (coordinator *editCoordinator) runWithHeartbeat(ctx context.Context, live *liveEditSession, run func(context.Context) (terminalhandoff.Result, error)) (terminalhandoff.Result, error, error) {
	if live.process == nil || coordinator.heartbeatEvery <= 0 {
		result, err := run(ctx)
		return result, nil, err
	}
	done := make(chan struct{})
	heartbeatErrors := make(chan error, 1)
	heartbeatFinished := make(chan struct{})
	go func() {
		defer close(heartbeatFinished)
		ticker := time.NewTicker(coordinator.heartbeatEvery)
		defer ticker.Stop()
		for {
			select {
			case <-done:
				return
			case <-ctx.Done():
				return
			case <-ticker.C:
				if err := coordinator.heartbeat(ctx, live); err != nil {
					select {
					case heartbeatErrors <- err:
					default:
					}
					return
				}
			}
		}
	}()
	result, err := run(ctx)
	close(done)
	<-heartbeatFinished
	select {
	case heartbeatErr := <-heartbeatErrors:
		return result, heartbeatErr, err
	default:
		return result, nil, err
	}
}

func (coordinator *editCoordinator) Heartbeat(ctx context.Context) error {
	for _, live := range coordinator.sessions {
		if live.process != nil {
			if err := coordinator.heartbeat(ctx, live); err != nil {
				return err
			}
		}
	}
	return nil
}

func (coordinator *editCoordinator) heartbeat(ctx context.Context, live *liveEditSession) error {
	var response daemon.CacheHeartbeatResponse
	return coordinator.client.Call(ctx, daemon.CacheHeartbeat, daemon.CacheHeartbeatRequest{
		MaterializationID: live.materialized.MaterializationID, LeaseID: live.materialized.LeaseID,
		OwnerKind: live.ownerKind, OwnerID: live.ownerID, Process: *live.process,
	}, &response)
}

func (coordinator *editCoordinator) Check(ctx context.Context, sessionID edit.SessionID) tui.Action {
	live := coordinator.sessions[sessionID]
	if live == nil {
		return tui.EditSessionFailed{Message: "edit check failed: session is not active in this client; retained recovery metadata is unchanged"}
	}
	return coordinator.observe(ctx, live, "change check completed")
}

func (coordinator *editCoordinator) observe(ctx context.Context, live *liveEditSession, message string) tui.Action {
	if live.machine.State() == edit.StateReady {
		next, err := live.machine.BeginObservation(live.machine.Version())
		if err != nil {
			return tui.EditSessionFailed{Pane: live.pane, Location: live.target, Message: "begin edit observation: " + err.Error()}
		}
		if err := coordinator.transition(ctx, live, next, editstore.LocalUnknown, editstore.RemoteUnknown, editstore.StateObserving, "observation_started", "", edit.Decision{}, nil); err != nil {
			return tui.EditSessionFailed{Pane: live.pane, Location: live.target, Message: "persist edit observation: " + clientErrorMessage(err)}
		}
	} else if live.machine.State() != edit.StateObserving {
		return tui.EditSessionFailed{Pane: live.pane, Location: live.target, Message: "edit check is not valid for durable state " + string(live.machine.State()) + "; recovery metadata retained"}
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
	return tui.EditSessionObserved{
		SessionID: live.machine.ID(), Pane: live.pane, Location: live.target,
		State: live.machine.State(), Message: message, ConflictView: coordinator.conflictView(ctx, live),
	}
}

func (coordinator *editCoordinator) conflictView(ctx context.Context, live *liveEditSession) edit.ConflictView {
	if live == nil || live.machine.Evaluation().Action != edit.ActionResolveConflict {
		return edit.ConflictView{}
	}
	local, localTruncated, err := readConflictLocal(live.materialized.Path, live.machine.CurrentLocalSHA256())
	if err != nil {
		return unavailableConflictView("retained local edit could not be revalidated: " + err.Error())
	}
	observation := live.machine.LastRemoteObservation()
	request := edit.ConflictViewRequest{Local: local, LocalTruncated: localTruncated, RemoteObservation: observation}
	if observation.Status != edit.RemotePresent || observation.Kind != domain.EntryFile {
		return edit.BuildConflictView(request)
	}
	expected := ipc.EncodeFingerprint(observation.Fingerprint)
	var response ipc.ProviderReadResponse
	if err := coordinator.client.Call(ctx, daemon.ProviderRead, ipc.ProviderReadRequest{
		Location: ipc.EncodeLocation(live.target), Limit: edit.ConflictViewInputByteLimit, ExpectedFingerprint: &expected,
	}, &response); err != nil {
		return unavailableConflictView("exact remote version could not be read: " + clientErrorMessage(err))
	}
	if !reflect.DeepEqual(ipc.DecodeFingerprint(response.Info.Fingerprint), observation.Fingerprint) {
		return unavailableConflictView("remote identity changed while building the conflict diff")
	}
	remote, err := response.Data.Decode()
	if err != nil || len(remote) > edit.ConflictViewInputByteLimit {
		return unavailableConflictView("remote conflict bytes violated the bounded read contract")
	}
	request.Remote = remote
	request.RemoteTruncated = !response.EOF
	return edit.BuildConflictView(request)
}

func unavailableConflictView(message string) edit.ConflictView {
	diagnostic := boundedRecoveryDiagnostic(message)
	return edit.ConflictView{
		Text:    "--- remote (diff unavailable)\n+++ local (retained; no upload started)\n " + diagnostic + "\n",
		Summary: "remote → local conflict diff unavailable",
	}
}

func readConflictLocal(path string, expected edit.SHA256) ([]byte, bool, error) {
	if _, err := edit.ParseSHA256(string(expected)); err != nil {
		return nil, false, errors.New("current local identity is uncertain")
	}
	descriptor, err := unix.Open(path, unix.O_RDONLY|unix.O_CLOEXEC|unix.O_NOFOLLOW, 0)
	if err != nil {
		return nil, false, err
	}
	file := os.NewFile(uintptr(descriptor), path)
	if file == nil {
		_ = unix.Close(descriptor)
		return nil, false, errors.New("invalid materialization descriptor")
	}
	defer file.Close()
	before, err := file.Stat()
	if err != nil || !before.Mode().IsRegular() || before.Mode().Perm()&0o077 != 0 {
		return nil, false, errors.New("materialization is not a private regular file")
	}
	bounded, err := io.ReadAll(io.LimitReader(file, edit.ConflictViewInputByteLimit+1))
	if err != nil {
		return nil, false, err
	}
	truncated := len(bounded) > edit.ConflictViewInputByteLimit
	if truncated {
		bounded = bounded[:edit.ConflictViewInputByteLimit]
	}
	if _, err := file.Seek(0, io.SeekStart); err != nil {
		return nil, false, err
	}
	hash := sha256.New()
	if _, err := io.Copy(hash, file); err != nil {
		return nil, false, err
	}
	after, err := file.Stat()
	if err != nil || !os.SameFile(before, after) || before.Size() != after.Size() || !before.ModTime().Equal(after.ModTime()) {
		return nil, false, errors.New("materialization changed while building the conflict diff")
	}
	if edit.SHA256(hex.EncodeToString(hash.Sum(nil))) != expected {
		return nil, false, errors.New("materialization no longer matches the durable edit session")
	}
	return bounded, truncated, nil
}

func (coordinator *editCoordinator) Decide(ctx context.Context, intent tui.Intent) tui.Action {
	live := coordinator.sessions[intent.EditSessionID]
	if live == nil {
		return tui.EditSessionFailed{Pane: intent.Pane, Location: intent.Location, Message: "edit decision failed: session is not active; retained recovery metadata is unchanged"}
	}
	if live.machine.State() == edit.StateSyncBackFrozen {
		if intent.EditDecision == "" || intent.EditDecision != live.recovery.DecisionKind {
			return tui.EditSessionObserved{
				SessionID: live.machine.ID(), Pane: live.pane, Location: live.target, State: live.machine.State(),
				Decision: live.recovery.DecisionKind, Message: "prepared sync-back requires explicit confirmation of the retained decision",
				ConflictView: coordinator.conflictView(ctx, live),
			}
		}
		return coordinator.enqueueFrozenSyncBack(ctx, live)
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
		return tui.EditSessionObserved{
			SessionID: live.machine.ID(), Pane: live.pane, Location: live.target, State: live.machine.State(),
			Message: "edit decision rejected: " + err.Error(), ConflictView: coordinator.conflictView(ctx, live),
		}
	}
	localState, remoteState, lifecycle := durableObservationState(live.machine.State(), live.machine.Evaluation(), edit.LocalObservation{Status: edit.LocalPresent, SHA256: live.machine.CurrentLocalSHA256()}, live.machine.LastRemoteObservation())
	if lifecycle != editstore.StateConflict {
		lifecycle = editstore.StateAwaitingDecision
	}
	if err := coordinator.transition(ctx, live, next, localState, remoteState, lifecycle, "sync_back_frozen", "", decision, syncBack); err != nil {
		return tui.EditSessionFailed{Pane: live.pane, Location: live.target, Message: "persist frozen sync-back: " + clientErrorMessage(err)}
	}
	return coordinator.createSyncBackJob(ctx, live, *syncBack)
}

func (coordinator *editCoordinator) enqueueFrozenSyncBack(ctx context.Context, live *liveEditSession) tui.Action {
	details := live.recovery.Details
	machine := live.machine
	request := edit.SyncBackRequest{
		PlanVersion: edit.SyncBackPlanVersion, Origin: edit.SyncBackOrigin, SessionID: machine.ID(), SessionVersion: machine.Version(),
		SourceEntryID: machine.Baseline().SourceEntryID, MaterializationID: machine.Baseline().MaterializationID,
		Target: details.Target, LocalSHA256: machine.CurrentLocalSHA256(), Decision: details.DecisionKind,
		OriginalDestinationPrecondition: machine.Baseline().ExpectedRemote, DestinationPrecondition: details.ExpectedRemote,
	}
	if details.DecisionKind == edit.DecisionOverwrite {
		request.Renewal = &edit.PreconditionRenewal{Reason: details.AuditReason, FromVersion: machine.Version() - 1, ToVersion: machine.Version(),
			Previous: machine.Baseline().ExpectedRemote, Renewed: details.ExpectedRemote}
	}
	return coordinator.createSyncBackJob(ctx, live, request)
}

func (coordinator *editCoordinator) createSyncBackJob(ctx context.Context, live *liveEditSession, syncBack edit.SyncBackRequest) tui.Action {
	localLocation, err := domain.NewLocation(coordinator.localEndpoint, domain.CanonicalPath(live.materialized.Path))
	if err != nil {
		return tui.EditSessionFailed{Pane: live.pane, Location: live.target, Message: "capture sync-back source: " + err.Error()}
	}
	var captured daemon.JobCaptureResponse
	if err := coordinator.client.Call(ctx, daemon.JobCapture, daemon.JobCaptureRequest{Location: ipc.EncodeLocation(localLocation)}, &captured); err != nil {
		return tui.EditSessionFailed{Pane: live.pane, Location: live.target, Message: "capture sync-back source: " + clientErrorMessage(err)}
	}
	var created daemon.JobSnapshotResponse
	if err := coordinator.client.Call(ctx, daemon.JobCreateSyncBack, daemon.JobCreateSyncBackRequest{Intent: transfer.SyncBackIntent{SyncBack: syncBack, Source: captured.Reference}}, &created); err != nil {
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

func prepareExternalEdit(ctx context.Context, materialization string, resolved externalprocess.ResolvedCommand, environment []string, screen handoffTCellScreen, beforeInputResume func()) (preparedExternalEdit, error) {
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
		controller, err := terminalhandoff.NewController(newTCellHandoffScreen(screen, beforeInputResume), platform)
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
