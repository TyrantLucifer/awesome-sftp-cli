package daemon

import (
	"context"
	"testing"
	"time"

	"github.com/TyrantLucifer/awesome-mac-sftp/internal/domain"
	"github.com/TyrantLucifer/awesome-mac-sftp/internal/edit"
	"github.com/TyrantLucifer/awesome-mac-sftp/internal/ipc"
	"github.com/TyrantLucifer/awesome-mac-sftp/internal/job"
	providerapi "github.com/TyrantLucifer/awesome-mac-sftp/internal/provider"
	"github.com/TyrantLucifer/awesome-mac-sftp/internal/state/jobstore"
	"github.com/TyrantLucifer/awesome-mac-sftp/internal/transfer"
)

func TestProviderSessionExposesOnlyHighLevelTransferAndJobRoutes(t *testing.T) {
	implementation := testLocalProvider(t)
	factory, err := NewProviderSessions([]providerapi.Provider{implementation}, 4)
	if err != nil {
		t.Fatal(err)
	}
	service := &recordingTransferService{snapshot: jobstore.Snapshot{
		JobID: "job_aaaaaaaaaaaaaaaaaaaaaaaaaa", PlanID: "plan_aaaaaaaaaaaaaaaaaaaaaaaaaa",
		State: job.StateQueued, StateVersion: 1, NextEventSequence: 2,
	}}
	factory.SetTransferService(service)
	session := factory.NewSession()
	t.Cleanup(func() { _ = session.Close() })
	location := domain.Location{EndpointID: implementation.Descriptor().ID, Path: "/file"}

	captured := handlePayload[JobCaptureResponse](t, session, JobCapture, JobCaptureRequest{Location: ipc.EncodeLocation(location)})
	if captured.Reference.Location != location || service.captured != location {
		t.Fatalf("captured = %#v, service location = %#v", captured, service.captured)
	}
	deleteCaptured := handlePayload[JobCaptureResponse](t, session, JobCaptureDelete, JobCaptureRequest{Location: ipc.EncodeLocation(location)})
	if deleteCaptured.Reference.Location != location || service.captured != location {
		t.Fatalf("delete captured = %#v, service location = %#v", deleteCaptured, service.captured)
	}
	created := handlePayload[JobSnapshotResponse](t, session, JobCreateCopy, JobCreateCopyRequest{Intent: transfer.Intent{
		Clipboard: transfer.ClipboardCopy, Source: captured.Reference,
		DestinationDirectory: domain.Location{EndpointID: implementation.Descriptor().ID, Path: "/"},
		Name:                 "copy", ConflictPolicy: transfer.ConflictAsk,
	}})
	if created.Snapshot.JobID != service.snapshot.JobID || service.created.Name != "copy" {
		t.Fatalf("created = %#v, intent = %#v", created, service.created)
	}
	deleted := handlePayload[JobSnapshotResponse](t, session, JobCreateDelete, JobCreateDeleteRequest{Intent: transfer.DeleteIntent{
		Target: captured.Reference, Confirmed: true, IrreversibleConfirmed: true,
	}})
	if deleted.Snapshot.JobID != service.snapshot.JobID || !service.deleted.IrreversibleConfirmed {
		t.Fatalf("deleted = %#v, intent = %#v", deleted, service.deleted)
	}
	syncBack := handlePayload[JobSnapshotResponse](t, session, JobCreateSyncBack, JobCreateSyncBackRequest{Intent: transfer.SyncBackIntent{
		SyncBack: edit.SyncBackRequest{SessionID: "44444444444444444444444444444444"}, Source: captured.Reference,
	}})
	if syncBack.Snapshot.JobID != service.snapshot.JobID || service.syncBack.SyncBack.SessionID != "44444444444444444444444444444444" {
		t.Fatalf("sync-back = %#v, intent = %#v", syncBack, service.syncBack)
	}
	listed := handlePayload[JobListResponse](t, session, JobList, JobListRequest{Limit: 20})
	if len(listed.Jobs) != 1 || listed.Jobs[0].Snapshot.JobID != service.snapshot.JobID {
		t.Fatalf("listed = %#v", listed)
	}
	events := handlePayload[JobEventsResponse](t, session, JobEvents, JobEventsRequest{JobID: service.snapshot.JobID, AfterSequence: 1, Limit: 20})
	if len(events.Events) != 1 || events.Events[0].Sequence != 2 {
		t.Fatalf("events = %#v", events)
	}
	for _, route := range []string{JobPause, JobResume, JobCancel} {
		controlled := handlePayload[JobSnapshotResponse](t, session, route, JobControlRequest{JobID: service.snapshot.JobID})
		if controlled.Snapshot.JobID != service.snapshot.JobID {
			t.Fatalf("%s response = %#v", route, controlled)
		}
	}
	resolved := handlePayload[JobSnapshotResponse](t, session, JobResolveConflict, JobResolveConflictRequest{
		JobID: service.snapshot.JobID, Resolution: transfer.ConflictAutoRename, ApplyAll: true,
	})
	if resolved.Snapshot.JobID != service.snapshot.JobID || service.resolution != transfer.ConflictAutoRename || !service.applyAll {
		t.Fatalf("conflict resolution = (%#v, %q, %t)", resolved, service.resolution, service.applyAll)
	}
	if service.pauseCalls != 1 || service.resumeCalls != 1 || service.cancelCalls != 1 {
		t.Fatalf("control calls = pause:%d resume:%d cancel:%d", service.pauseCalls, service.resumeCalls, service.cancelCalls)
	}
	if _, err := session.Handle(context.Background(), "provider.open_write", []byte(`{}`)); !domain.IsCode(err, domain.CodeUnsupported) {
		t.Fatalf("raw mutation error = %v, want unsupported", err)
	}
}

type recordingTransferService struct {
	snapshot    jobstore.Snapshot
	captured    domain.Location
	created     transfer.Intent
	deleted     transfer.DeleteIntent
	syncBack    transfer.SyncBackIntent
	pauseCalls  int
	resumeCalls int
	cancelCalls int
	resolution  transfer.ConflictPolicy
	applyAll    bool
}

func (service *recordingTransferService) Capture(_ context.Context, location domain.Location) (transfer.FileRef, error) {
	service.captured = location
	return transfer.FileRef{Location: location, Kind: domain.EntryFile}, nil
}

func (service *recordingTransferService) CaptureDelete(_ context.Context, location domain.Location) (transfer.FileRef, error) {
	service.captured = location
	return transfer.FileRef{Location: location, Kind: domain.EntryFile}, nil
}

func (service *recordingTransferService) CreateCopy(_ context.Context, intent transfer.Intent) (jobstore.Snapshot, error) {
	service.created = intent
	return service.snapshot, nil
}

func (service *recordingTransferService) CreateDelete(_ context.Context, intent transfer.DeleteIntent) (jobstore.Snapshot, error) {
	service.deleted = intent
	return service.snapshot, nil
}

func (service *recordingTransferService) CreateSyncBack(_ context.Context, intent transfer.SyncBackIntent) (jobstore.Snapshot, error) {
	service.syncBack = intent
	return service.snapshot, nil
}

func (service *recordingTransferService) JobViews(context.Context, int) ([]transfer.JobView, error) {
	return []transfer.JobView{{Snapshot: service.snapshot}}, nil
}

func (service *recordingTransferService) Events(context.Context, domain.JobID, int64, int) ([]jobstore.EventRecord, error) {
	return []jobstore.EventRecord{{JobID: service.snapshot.JobID, Sequence: 2, EventID: "evt_aaaaaaaaaaaaaaaaaaaaaaaaaa", Kind: "job_started", PayloadJSON: `{}`, CreatedAt: time.Unix(1, 0)}}, nil
}

func (service *recordingTransferService) Pause(context.Context, domain.JobID) (jobstore.Snapshot, error) {
	service.pauseCalls++
	return service.snapshot, nil
}

func (service *recordingTransferService) Resume(context.Context, domain.JobID) (jobstore.Snapshot, error) {
	service.resumeCalls++
	return service.snapshot, nil
}

func (service *recordingTransferService) Cancel(context.Context, domain.JobID) (jobstore.Snapshot, error) {
	service.cancelCalls++
	return service.snapshot, nil
}

func (service *recordingTransferService) ResolveConflict(_ context.Context, _ domain.JobID, resolution transfer.ConflictPolicy, applyAll bool) (jobstore.Snapshot, error) {
	service.resolution = resolution
	service.applyAll = applyAll
	return service.snapshot, nil
}
