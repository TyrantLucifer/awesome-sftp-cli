//go:build darwin || linux

package app

import (
	"context"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/TyrantLucifer/awesome-mac-sftp/internal/cache"
	"github.com/TyrantLucifer/awesome-mac-sftp/internal/daemon"
	"github.com/TyrantLucifer/awesome-mac-sftp/internal/domain"
	"github.com/TyrantLucifer/awesome-mac-sftp/internal/edit"
	"github.com/TyrantLucifer/awesome-mac-sftp/internal/ipc"
	"github.com/TyrantLucifer/awesome-mac-sftp/internal/job"
	"github.com/TyrantLucifer/awesome-mac-sftp/internal/state/editstore"
	"github.com/TyrantLucifer/awesome-mac-sftp/internal/state/jobstore"
	"github.com/TyrantLucifer/awesome-mac-sftp/internal/terminalhandoff"
	"github.com/TyrantLucifer/awesome-mac-sftp/internal/transfer"
	"github.com/TyrantLucifer/awesome-mac-sftp/internal/tui"
)

func TestEditWorkflowPersistsDirtyObservationBeforeExplicitSyncBackJob(t *testing.T) {
	fixture := newEditRPCFixture(t)
	coordinator, err := newEditCoordinator(fixture, fixture.localEndpoint, "workspace", cache.PolicyEphemeral, func(_ context.Context, path string, purpose edit.Purpose) (preparedExternalEdit, error) {
		if purpose != edit.PurposeEditor {
			t.Fatalf("purpose = %q", purpose)
		}
		return preparedExternalEdit{command: "/usr/bin/vi " + path, run: func(context.Context) (terminalhandoff.Result, error) {
			if err := os.WriteFile(path, []byte("changed locally"), 0o600); err != nil {
				t.Fatal(err)
			}
			return terminalhandoff.Result{Kind: terminalhandoff.ExitNonZero, ExitCode: 7}, nil
		}}, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	action := coordinator.Begin(context.Background(), tui.Intent{Kind: tui.IntentEdit, Pane: tui.Left, Location: fixture.target})
	ready, ok := action.(tui.EditLaunchReady)
	if !ok || ready.Command == "" {
		t.Fatalf("Begin() = %#v", action)
	}
	action = coordinator.Launch(context.Background(), ready.SessionID)
	observed, ok := action.(tui.EditSessionObserved)
	if !ok || observed.State != edit.StateAwaitingUploadConfirmation {
		t.Fatalf("Begin() = %#v", action)
	}
	if fixture.jobCreates != 0 {
		t.Fatal("edit observation silently created a sync-back Job")
	}
	if replay := coordinator.Launch(context.Background(), ready.SessionID); replay == nil {
		t.Fatal("replayed launch unexpectedly succeeded")
	} else if _, ok := replay.(tui.EditSessionFailed); !ok {
		t.Fatalf("replayed launch = %#v", replay)
	}
	wantPrefix := []string{daemon.CacheMaterialize, daemon.EditSessionCreate, daemon.EditSessionTransition, daemon.ProviderStat, daemon.CacheMarkDirty, daemon.EditSessionTransition}
	if !reflect.DeepEqual(fixture.routes, wantPrefix) {
		t.Fatalf("routes before confirmation = %#v, want %#v", fixture.routes, wantPrefix)
	}
	action = coordinator.Decide(context.Background(), tui.Intent{Kind: tui.IntentEditDecision, Pane: tui.Left, Location: fixture.target, EditSessionID: observed.SessionID, EditDecision: edit.DecisionUpload})
	created, ok := action.(tui.JobCreated)
	if !ok || created.JobID == "" || fixture.jobCreates != 1 {
		t.Fatalf("Decide() = %#v, jobs = %d", action, fixture.jobCreates)
	}
	if fixture.frozenVersion != fixture.boundVersion || fixture.frozenVersion != 4 {
		t.Fatalf("frozen version = %d, bound version = %d", fixture.frozenVersion, fixture.boundVersion)
	}
	if got := fixture.routes[len(fixture.routes)-3:]; !reflect.DeepEqual(got, []string{daemon.EditSessionTransition, daemon.JobCapture, daemon.JobCreateSyncBack}) {
		t.Fatalf("confirmation tail = %#v", got)
	}
}

func TestOpenerRetainsLeaseUntilExplicitChangeCheck(t *testing.T) {
	fixture := newEditRPCFixture(t)
	coordinator, err := newEditCoordinator(fixture, fixture.localEndpoint, "workspace", cache.PolicyLRU, func(context.Context, string, edit.Purpose) (preparedExternalEdit, error) {
		return preparedExternalEdit{command: "/usr/bin/open file", run: func(context.Context) (terminalhandoff.Result, error) {
			return terminalhandoff.Result{Kind: terminalhandoff.ExitNormal}, nil
		}}, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	action := coordinator.Begin(context.Background(), tui.Intent{Kind: tui.IntentOpenExternal, Pane: tui.Right, Location: fixture.target})
	ready := action.(tui.EditLaunchReady)
	action = coordinator.Launch(context.Background(), ready.SessionID)
	opened, ok := action.(tui.EditSessionObserved)
	if !ok || opened.State != edit.StateReady {
		t.Fatalf("Begin(opener) = %#v", action)
	}
	if fixture.releases != 0 || len(fixture.routes) != 2 {
		t.Fatalf("opener returned early: releases %d routes %#v", fixture.releases, fixture.routes)
	}
	action = coordinator.Check(context.Background(), opened.SessionID)
	if finished, ok := action.(tui.EditSessionFinished); !ok || finished.Message == "" {
		t.Fatalf("Check() unchanged = %#v", action)
	}
	if fixture.releases != 1 {
		t.Fatalf("releases after explicit check = %d", fixture.releases)
	}
}

func TestEditWorkflowHeartbeatsTheExactLiveProcessLease(t *testing.T) {
	fixture := newEditRPCFixture(t)
	coordinator, err := newEditCoordinator(fixture, fixture.localEndpoint, "workspace", cache.PolicyLRU, func(context.Context, string, edit.Purpose) (preparedExternalEdit, error) {
		return preparedExternalEdit{command: "/usr/bin/vi file", run: func(context.Context) (terminalhandoff.Result, error) {
			return terminalhandoff.Result{Kind: terminalhandoff.ExitNormal}, nil
		}}, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	action := coordinator.Begin(context.Background(), tui.Intent{Kind: tui.IntentEdit, Pane: tui.Left, Location: fixture.target})
	if _, ok := action.(tui.EditLaunchReady); !ok {
		t.Fatalf("Begin() = %#v", action)
	}
	if err := coordinator.Heartbeat(context.Background()); err != nil {
		t.Fatal(err)
	}
	if fixture.heartbeats != 1 {
		t.Fatalf("heartbeats = %d", fixture.heartbeats)
	}
}

func TestEditWorkflowTreatsConcurrentRemoteReplacementAsConflict(t *testing.T) {
	fixture := newEditRPCFixture(t)
	replacement := "replacement-version"
	fixture.remoteFingerprint.VersionID = &replacement
	coordinator, err := newEditCoordinator(fixture, fixture.localEndpoint, "workspace", cache.PolicyEphemeral, func(_ context.Context, path string, _ edit.Purpose) (preparedExternalEdit, error) {
		return preparedExternalEdit{command: "/usr/bin/vi " + path, run: func(context.Context) (terminalhandoff.Result, error) {
			return terminalhandoff.Result{Kind: terminalhandoff.ExitNormal}, os.WriteFile(path, []byte("local edit"), 0o600)
		}}, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	action := coordinator.Begin(context.Background(), tui.Intent{Kind: tui.IntentEdit, Pane: tui.Left, Location: fixture.target})
	action = coordinator.Launch(context.Background(), action.(tui.EditLaunchReady).SessionID)
	observed, ok := action.(tui.EditSessionObserved)
	if !ok || observed.State != edit.StateConflict || fixture.jobCreates != 0 {
		t.Fatalf("conflict observation = %#v, jobs = %d", action, fixture.jobCreates)
	}
	action = coordinator.Decide(context.Background(), tui.Intent{Kind: tui.IntentEditDecision, Pane: tui.Left, Location: fixture.target, EditSessionID: observed.SessionID, EditDecision: edit.DecisionOverwrite})
	if _, ok := action.(tui.JobCreated); !ok || fixture.frozenExpected.Fingerprint.VersionID == nil || *fixture.frozenExpected.Fingerprint.VersionID != replacement {
		t.Fatalf("overwrite decision = %#v, expected = %#v", action, fixture.frozenExpected)
	}
}

func TestColdStartRecoveryRebindsExactHandoffAndRequiresExplicitSyncBack(t *testing.T) {
	fixture := newEditRPCFixture(t)
	baselineSHA, _, err := hashRegularFile(fixture.path)
	if err != nil {
		t.Fatal(err)
	}
	machine, err := edit.NewSession(edit.NewSessionRequest{ID: "55555555555555555555555555555555", Purpose: edit.PurposeEditor, Baseline: edit.Baseline{
		SourceEntryID:     "1111111111111111111111111111111111111111111111111111111111111111",
		MaterializationID: "22222222222222222222222222222222", Target: fixture.target,
		ExpectedRemote: edit.RemotePrecondition{Presence: edit.ExpectedPresent, Kind: domain.EntryFile, Fingerprint: fixture.baseline}, LocalSHA256: baselineSHA,
	}})
	if err != nil {
		t.Fatal(err)
	}
	machine, err = machine.BeginObservation(machine.Version())
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(fixture.path, []byte("recovered local edit"), 0o600); err != nil {
		t.Fatal(err)
	}
	changedSHA, _, err := hashRegularFile(fixture.path)
	if err != nil {
		t.Fatal(err)
	}
	machine, _, err = machine.Observe(machine.Version(), edit.PostEditorObservation{
		Local:  edit.LocalObservation{Status: edit.LocalPresent, SHA256: changedSHA},
		Remote: edit.RemoteObservation{Status: edit.RemotePresent, Kind: domain.EntryFile, Fingerprint: fixture.baseline},
	})
	if err != nil {
		t.Fatal(err)
	}
	fixture.recoveries = []editstore.RecoveryRecord{{
		Record: editstore.Record{SessionID: string(machine.ID()), SourceEntryID: machine.Baseline().SourceEntryID, MaterializationID: machine.Baseline().MaterializationID,
			LocalState: editstore.LocalDirty, RemoteState: editstore.RemoteUnchanged, State: editstore.StateAwaitingDecision, StateVersion: 3},
		Details: editstore.Details{ReferenceID: "33333333333333333333333333333333", LeaseID: "44444444444444444444444444444444",
			Persistent: machine.Persistent(), Target: fixture.target, ExpectedRemote: machine.Baseline().ExpectedRemote},
	}}
	fixture.version = 3
	coordinator, err := newEditCoordinator(fixture, fixture.localEndpoint, "workspace", cache.PolicyEphemeral, func(context.Context, string, edit.Purpose) (preparedExternalEdit, error) {
		return preparedExternalEdit{}, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	coordinator.recoveryMaterializationPath = func(id cache.MaterializationID) (string, error) {
		if id != machine.Baseline().MaterializationID {
			t.Fatalf("materialization ID = %q", id)
		}
		return fixture.path, nil
	}
	loaded, ok := coordinator.Recoverable(context.Background(), 64).(tui.EditRecoveryLoaded)
	if !ok || len(loaded.Sessions) != 1 || !loaded.Sessions[0].Usable {
		t.Fatalf("Recoverable() = %#v", loaded)
	}
	resumed := coordinator.Resume(context.Background(), machine.ID(), tui.Left)
	observed, ok := resumed.(tui.EditSessionObserved)
	if !ok || observed.State != edit.StateAwaitingUploadConfirmation || fixture.jobCreates != 0 {
		t.Fatalf("Resume() = %#v, jobs = %d", resumed, fixture.jobCreates)
	}
	created := coordinator.Decide(context.Background(), tui.Intent{Kind: tui.IntentEditDecision, Pane: tui.Left, Location: fixture.target, EditSessionID: machine.ID(), EditDecision: edit.DecisionUpload})
	if _, ok := created.(tui.JobCreated); !ok || fixture.jobCreates != 1 {
		t.Fatalf("Decide() = %#v, jobs = %d", created, fixture.jobCreates)
	}
}

func TestColdStartRecoveryRetainsUnusableMaterializationWithDiagnostic(t *testing.T) {
	fixture := newEditRPCFixture(t)
	fixture.recoveries = []editstore.RecoveryRecord{{
		Record: editstore.Record{SessionID: "55555555555555555555555555555555", SourceEntryID: "1111111111111111111111111111111111111111111111111111111111111111", MaterializationID: "22222222222222222222222222222222", State: editstore.StateRecovery, StateVersion: 3},
	}}
	coordinator, err := newEditCoordinator(fixture, fixture.localEndpoint, "workspace", cache.PolicyLRU, func(context.Context, string, edit.Purpose) (preparedExternalEdit, error) {
		return preparedExternalEdit{}, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	loaded := coordinator.Recoverable(context.Background(), 64).(tui.EditRecoveryLoaded)
	if len(loaded.Sessions) != 1 || loaded.Sessions[0].Usable || loaded.Sessions[0].Diagnostic == "" {
		t.Fatalf("unusable recovery = %#v", loaded.Sessions)
	}
	if action := coordinator.Resume(context.Background(), "55555555555555555555555555555555", tui.Left); action == nil {
		t.Fatal("unusable recovery returned no diagnostic action")
	} else if failed, ok := action.(tui.EditSessionFailed); !ok || failed.Message == "" {
		t.Fatalf("Resume() = %#v", action)
	}
}

func TestColdStartFrozenSyncBackQueuesOnlyAfterExactPersistedDecision(t *testing.T) {
	fixture := newEditRPCFixture(t)
	baselineSHA, _, err := hashRegularFile(fixture.path)
	if err != nil {
		t.Fatal(err)
	}
	machine, err := edit.NewSession(edit.NewSessionRequest{ID: "66666666666666666666666666666666", Purpose: edit.PurposeEditor, Baseline: edit.Baseline{
		SourceEntryID:     "1111111111111111111111111111111111111111111111111111111111111111",
		MaterializationID: "22222222222222222222222222222222", Target: fixture.target,
		ExpectedRemote: edit.RemotePrecondition{Presence: edit.ExpectedPresent, Kind: domain.EntryFile, Fingerprint: fixture.baseline}, LocalSHA256: baselineSHA,
	}})
	if err != nil {
		t.Fatal(err)
	}
	machine, _ = machine.BeginObservation(machine.Version())
	if err := os.WriteFile(fixture.path, []byte("frozen local edit"), 0o600); err != nil {
		t.Fatal(err)
	}
	changedSHA, _, _ := hashRegularFile(fixture.path)
	machine, _, err = machine.Observe(machine.Version(), edit.PostEditorObservation{
		Local:  edit.LocalObservation{Status: edit.LocalPresent, SHA256: changedSHA},
		Remote: edit.RemoteObservation{Status: edit.RemotePresent, Kind: domain.EntryFile, Fingerprint: fixture.baseline},
	})
	if err != nil {
		t.Fatal(err)
	}
	machine, frozen, err := machine.Decide(machine.Version(), edit.Decision{Kind: edit.DecisionUpload})
	if err != nil {
		t.Fatal(err)
	}
	fixture.recoveries = []editstore.RecoveryRecord{{
		Record: editstore.Record{SessionID: string(machine.ID()), SourceEntryID: machine.Baseline().SourceEntryID, MaterializationID: machine.Baseline().MaterializationID,
			LocalState: editstore.LocalDirty, RemoteState: editstore.RemoteUnchanged, State: editstore.StateAwaitingDecision, StateVersion: 4},
		Details: editstore.Details{ReferenceID: "33333333333333333333333333333333", LeaseID: "44444444444444444444444444444444",
			Persistent: machine.Persistent(), Target: frozen.Target, ExpectedRemote: frozen.DestinationPrecondition, DecisionKind: edit.DecisionUpload},
	}}
	coordinator, err := newEditCoordinator(fixture, fixture.localEndpoint, "workspace", cache.PolicyLRU, func(context.Context, string, edit.Purpose) (preparedExternalEdit, error) {
		return preparedExternalEdit{}, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	coordinator.recoveryMaterializationPath = func(cache.MaterializationID) (string, error) { return fixture.path, nil }
	loaded := coordinator.Recoverable(context.Background(), 64).(tui.EditRecoveryLoaded)
	if len(loaded.Sessions) != 1 || !loaded.Sessions[0].Usable {
		t.Fatalf("Recoverable() = %#v", loaded)
	}
	action := coordinator.Resume(context.Background(), machine.ID(), tui.Left)
	if observed, ok := action.(tui.EditSessionObserved); !ok || observed.State != edit.StateSyncBackFrozen || observed.Decision != edit.DecisionUpload {
		t.Fatalf("Resume() = %#v", action)
	}
	action = coordinator.Decide(context.Background(), tui.Intent{Kind: tui.IntentEditDecision, EditSessionID: machine.ID(), EditDecision: edit.DecisionOverwrite})
	if _, ok := action.(tui.EditSessionObserved); !ok || fixture.jobCreates != 0 {
		t.Fatalf("wrong confirmation = %#v, jobs = %d", action, fixture.jobCreates)
	}
	action = coordinator.Decide(context.Background(), tui.Intent{Kind: tui.IntentEditDecision, EditSessionID: machine.ID(), EditDecision: edit.DecisionUpload})
	if _, ok := action.(tui.JobCreated); !ok || fixture.jobCreates != 1 || fixture.boundVersion != machine.Version() {
		t.Fatalf("exact confirmation = %#v, jobs = %d version = %d", action, fixture.jobCreates, fixture.boundVersion)
	}
}

type editRPCFixture struct {
	t                 *testing.T
	path              string
	target            domain.Location
	localEndpoint     domain.EndpointID
	baseline          domain.Fingerprint
	remoteFingerprint domain.Fingerprint
	routes            []string
	version           int64
	releases          int
	heartbeats        int
	jobCreates        int
	frozenVersion     edit.Version
	boundVersion      edit.Version
	frozenExpected    edit.RemotePrecondition
	recoveries        []editstore.RecoveryRecord
}

func newEditRPCFixture(t *testing.T) *editRPCFixture {
	t.Helper()
	directory := t.TempDir()
	path := filepath.Join(directory, "materialized")
	if err := os.WriteFile(path, []byte("baseline"), 0o600); err != nil {
		t.Fatal(err)
	}
	endpoint := domain.EndpointID("ep_bbbbbbbbbbbbbbbbbbbbbbbbbb")
	target, err := domain.NewLocation(endpoint, "/file.txt")
	if err != nil {
		t.Fatal(err)
	}
	version := "baseline-version"
	fingerprint := domain.Fingerprint{VersionID: &version}
	return &editRPCFixture{t: t, path: path, target: target, localEndpoint: "ep_aaaaaaaaaaaaaaaaaaaaaaaaaa", baseline: fingerprint, remoteFingerprint: fingerprint}
}

func (fixture *editRPCFixture) Call(_ context.Context, route string, request any, response any) error {
	fixture.t.Helper()
	fixture.routes = append(fixture.routes, route)
	switch route {
	case daemon.EditSessionRecoverable:
		response.(*daemon.EditSessionRecoverableResponse).Sessions = append([]editstore.RecoveryRecord(nil), fixture.recoveries...)
	case daemon.CacheMaterialize:
		got := request.(daemon.CacheMaterializeRequest)
		if got.OwnerID == "" || got.OwnerKind == "" || got.Process == nil || got.Process.PID <= 0 || got.Process.BirthID == "" {
			fixture.t.Fatalf("materialize owner = %#v", got)
		}
		*(response.(*daemon.CacheMaterializeResponse)) = daemon.CacheMaterializeResponse{
			EntryID: "1111111111111111111111111111111111111111111111111111111111111111", MaterializationID: "22222222222222222222222222222222",
			ReferenceID: "33333333333333333333333333333333", LeaseID: "44444444444444444444444444444444",
			Path: fixture.path, SourceFingerprint: ipc.EncodeFingerprint(fixture.baseline),
		}
	case daemon.CacheHeartbeat:
		got := request.(daemon.CacheHeartbeatRequest)
		if got.MaterializationID == "" || got.LeaseID == "" || got.OwnerID == "" || got.Process.PID <= 0 || got.Process.BirthID == "" {
			fixture.t.Fatalf("heartbeat = %#v", got)
		}
		fixture.heartbeats++
		response.(*daemon.CacheHeartbeatResponse).Renewed = true
	case daemon.EditSessionCreate:
		got := request.(daemon.EditSessionCreateRequest).Request
		if got.Details.Persistent.Version != 1 || got.State != editstore.StateEditing {
			fixture.t.Fatalf("create request = %#v", got)
		}
		fixture.version = 1
		response.(*daemon.EditSessionResponse).Session = editstore.Record{SessionID: got.SessionID, StateVersion: fixture.version}
	case daemon.EditSessionTransition:
		got := request.(daemon.EditSessionTransitionRequest).Request
		if got.ExpectedVersion != fixture.version || got.Persistent.Version != edit.Version(fixture.version+1) {
			fixture.t.Fatalf("transition = %#v at version %d", got, fixture.version)
		}
		fixture.version++
		if got.EventKind == "sync_back_frozen" {
			fixture.frozenVersion = got.Persistent.Version
			fixture.frozenExpected = got.ExpectedRemote
			if got.State != editstore.StateAwaitingDecision && got.State != editstore.StateConflict {
				fixture.t.Fatalf("frozen lifecycle state = %q", got.State)
			}
		}
		response.(*daemon.EditSessionResponse).Session = editstore.Record{SessionID: got.SessionID, StateVersion: fixture.version}
	case daemon.CacheMarkDirty:
		got := request.(daemon.CacheMarkDirtyRequest)
		if got.OwnerID == "" || got.MaterializationID == "" {
			fixture.t.Fatalf("mark dirty = %#v", got)
		}
		sha, size, err := hashRegularFile(fixture.path)
		if err != nil {
			fixture.t.Fatal(err)
		}
		response.(*daemon.CacheMarkDirtyResponse).Dirty = true
		response.(*daemon.CacheMarkDirtyResponse).CurrentSHA256 = cache.BlobID(sha)
		response.(*daemon.CacheMarkDirtyResponse).Size = int64(size)
	case daemon.ProviderStat:
		entry := domain.Entry{Location: fixture.target, Name: "file.txt", Kind: domain.EntryFile, Fingerprint: fixture.remoteFingerprint}
		response.(*ipc.ProviderStatResponse).Entry = ipc.EncodeEntry(entry)
	case daemon.CacheReleaseHandoff:
		fixture.releases++
		response.(*daemon.CacheReleaseHandoffResponse).Released = true
	case daemon.JobCapture:
		response.(*daemon.JobCaptureResponse).Reference = transfer.FileRef{Location: domain.Location{EndpointID: fixture.localEndpoint, Path: domain.CanonicalPath(fixture.path)}, Kind: domain.EntryFile, Fingerprint: fixture.remoteFingerprint}
	case daemon.JobCreateSyncBack:
		got := request.(daemon.JobCreateSyncBackRequest).Intent
		fixture.jobCreates++
		fixture.boundVersion = got.SyncBack.SessionVersion
		response.(*daemon.JobSnapshotResponse).Snapshot = jobstore.Snapshot{JobID: "job_aaaaaaaaaaaaaaaaaaaaaaaaaa", State: job.StateQueued}
	default:
		fixture.t.Fatalf("unexpected route %q", route)
	}
	return nil
}
