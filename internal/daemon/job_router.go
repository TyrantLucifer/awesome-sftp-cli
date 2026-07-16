package daemon

import (
	"context"
	"encoding/json"

	"github.com/TyrantLucifer/awesome-mac-sftp/internal/domain"
	"github.com/TyrantLucifer/awesome-mac-sftp/internal/ipc"
	"github.com/TyrantLucifer/awesome-mac-sftp/internal/state/jobstore"
	"github.com/TyrantLucifer/awesome-mac-sftp/internal/transfer"
)

const (
	JobCapture    = "transfer.capture"
	JobCreateCopy = "job.create_copy"
	JobList       = "job.list"
	JobEvents     = "job.events"
	JobPause      = "job.pause"
	JobResume     = "job.resume"
	JobCancel     = "job.cancel"
)

type TransferService interface {
	Capture(context.Context, domain.Location) (transfer.FileRef, error)
	CreateCopy(context.Context, transfer.Intent) (jobstore.Snapshot, error)
	JobViews(context.Context, int) ([]transfer.JobView, error)
	Events(context.Context, domain.JobID, int64, int) ([]jobstore.EventRecord, error)
	Pause(context.Context, domain.JobID) (jobstore.Snapshot, error)
	Resume(context.Context, domain.JobID) (jobstore.Snapshot, error)
	Cancel(context.Context, domain.JobID) (jobstore.Snapshot, error)
}

type JobCaptureRequest struct {
	Location ipc.WireLocation `json:"location"`
}

type JobCaptureResponse struct {
	Reference transfer.FileRef `json:"reference"`
}

type JobCreateCopyRequest struct {
	Intent transfer.Intent `json:"intent"`
}

type JobSnapshotResponse struct {
	Snapshot jobstore.Snapshot `json:"snapshot"`
}

type JobListRequest struct {
	Limit int `json:"limit"`
}

type JobListResponse struct {
	Jobs []transfer.JobView `json:"jobs"`
}

type JobEventsRequest struct {
	JobID         domain.JobID `json:"job_id"`
	AfterSequence int64        `json:"after_sequence"`
	Limit         int          `json:"limit"`
}

type JobEventsResponse struct {
	Events []jobstore.EventRecord `json:"events"`
}

type JobControlRequest struct {
	JobID domain.JobID `json:"job_id"`
}

func (session *providerSession) handleJob(ctx context.Context, name string, payload json.RawMessage) (any, error) {
	if session.transfer == nil {
		return nil, &domain.OpError{Code: domain.CodeUnsupported, Message: "durable transfer service is unavailable", Retry: domain.RetryAdvice{Kind: domain.RetryNever}, Effect: domain.EffectNone}
	}
	switch name {
	case JobCapture:
		var request JobCaptureRequest
		if err := decodePayload(payload, &request); err != nil {
			return nil, invalidArgument("decode transfer capture request", err)
		}
		location, err := ipc.DecodeLocation(request.Location)
		if err != nil {
			return nil, invalidArgument("decode transfer capture location", err)
		}
		reference, err := session.transfer.Capture(ctx, location)
		return JobCaptureResponse{Reference: reference}, err
	case JobCreateCopy:
		var request JobCreateCopyRequest
		if err := decodePayload(payload, &request); err != nil {
			return nil, invalidArgument("decode copy Job request", err)
		}
		snapshot, err := session.transfer.CreateCopy(ctx, request.Intent)
		return JobSnapshotResponse{Snapshot: snapshot}, err
	case JobList:
		var request JobListRequest
		if err := decodePayload(payload, &request); err != nil {
			return nil, invalidArgument("decode Job list request", err)
		}
		jobs, err := session.transfer.JobViews(ctx, request.Limit)
		return JobListResponse{Jobs: jobs}, err
	case JobEvents:
		var request JobEventsRequest
		if err := decodePayload(payload, &request); err != nil {
			return nil, invalidArgument("decode Job events request", err)
		}
		events, err := session.transfer.Events(ctx, request.JobID, request.AfterSequence, request.Limit)
		return JobEventsResponse{Events: events}, err
	case JobPause, JobResume, JobCancel:
		var request JobControlRequest
		if err := decodePayload(payload, &request); err != nil {
			return nil, invalidArgument("decode Job control request", err)
		}
		var snapshot jobstore.Snapshot
		var err error
		switch name {
		case JobPause:
			snapshot, err = session.transfer.Pause(ctx, request.JobID)
		case JobResume:
			snapshot, err = session.transfer.Resume(ctx, request.JobID)
		case JobCancel:
			snapshot, err = session.transfer.Cancel(ctx, request.JobID)
		}
		return JobSnapshotResponse{Snapshot: snapshot}, err
	default:
		return nil, &domain.OpError{Code: domain.CodeUnsupported, Message: "unsupported Job request", Retry: domain.RetryAdvice{Kind: domain.RetryNever}, Effect: domain.EffectNone}
	}
}
