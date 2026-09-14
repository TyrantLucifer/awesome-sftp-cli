package transfer

import (
	"context"
	"github.com/TyrantLucifer/awesome-sftp-cli/internal/domain"
	"github.com/TyrantLucifer/awesome-sftp-cli/internal/job"
)

// TransferProgress distinguishes in-flight work from the durable restart point.
// Reports are bounded, volatile observations; only Journal.Save advances recovery.
type TransferProgress struct {
	Phase         Phase
	Bytes         uint64
	DurableBytes  uint64
	VerifiedBytes uint64
	Performance   *TransferPerformance
}

type progressJournal interface {
	ReportProgress(context.Context, TransferProgress) error
}

func reportProgress(ctx context.Context, journal Journal, progress TransferProgress) error {
	if observer, ok := journal.(progressJournal); ok {
		return observer.ReportProgress(ctx, progress)
	}
	return nil
}

func (manager *Manager) observeProgress(ctx context.Context, jobID domain.JobID, plannedRoute Route, directory bool, progress TransferProgress) error {
	progress.Performance = cloneTransferPerformance(progress.Performance)
	manager.mu.Lock()
	previous := manager.progress[jobID]
	if progress.Performance == nil {
		progress.Performance = previous.Performance
	}
	manager.progress[jobID] = progress
	manager.mu.Unlock()
	if directory || (progress.Phase != PhaseTransferred && progress.Phase != PhaseVerified && progress.Phase != PhaseCommitting) {
		return nil
	}
	if previous.Phase == progress.Phase {
		return nil
	}
	snapshot, err := manager.store.Get(ctx, jobID)
	if err != nil {
		return err
	}
	if snapshot.State != job.StateRunning {
		return nil
	}
	payload := map[string]any{}
	checkpoint, err := (JobJournal{Store: manager.store, StepIndex: 0}).Load(ctx, jobID)
	if err != nil {
		return err
	}
	if checkpoint != nil && checkpoint.ActualRoute != "" {
		payload["planned_route"] = plannedRoute
		payload["actual_route"] = checkpoint.ActualRoute
		payload["route_reason"] = checkpoint.RouteReason
		if checkpoint.DowngradedFrom != "" {
			payload["downgraded_from"] = checkpoint.DowngradedFrom
		}
	}
	_, err = manager.transition(snapshot, job.StateVerifying, "job_verifying", payload)
	return err
}

func (manager *Manager) clearProgress(jobID domain.JobID) {
	manager.mu.Lock()
	defer manager.mu.Unlock()
	delete(manager.progress, jobID)
}

func (journal JobJournal) ReportProgress(ctx context.Context, progress TransferProgress) error {
	if journal.Observer != nil {
		return journal.Observer(ctx, progress)
	}
	return nil
}

func (journal *directoryItemJournal) ReportProgress(ctx context.Context, progress TransferProgress) error {
	progress.Phase = PhaseStreaming
	progress.Bytes = saturatingAdd(journal.baseOffset, progress.Bytes, ^uint64(0))
	progress.DurableBytes = saturatingAdd(journal.baseOffset, progress.DurableBytes, ^uint64(0))
	progress.Bytes = max(progress.Bytes, journal.floorOffset)
	progress.DurableBytes = max(progress.DurableBytes, journal.floorOffset)
	return reportProgress(ctx, journal.parent, progress)
}
