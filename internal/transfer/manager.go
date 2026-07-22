package transfer

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/TyrantLucifer/awesome-sftp-cli/internal/domain"
	"github.com/TyrantLucifer/awesome-sftp-cli/internal/edit"
	"github.com/TyrantLucifer/awesome-sftp-cli/internal/foundation"
	"github.com/TyrantLucifer/awesome-sftp-cli/internal/job"
	"github.com/TyrantLucifer/awesome-sftp-cli/internal/retrypolicy"
	"github.com/TyrantLucifer/awesome-sftp-cli/internal/state/jobstore"
)

const defaultQueuedJobs = 128

type SyncBackIntent struct {
	SyncBack edit.SyncBackRequest `json:"sync_back"`
	Source   FileRef              `json:"source"`
}

type ManagerConfig struct {
	Store           *jobstore.Store
	Resolver        Resolver
	Generator       domain.Generator
	Now             func() time.Time
	MaxConcurrent   int
	MaxQueued       int
	SameHostCopy    SameHostCopyBackend
	SchedulerClock  foundation.Clock
	SchedulerPolicy SchedulerPolicy
	ResourceLimits  ResourceLimits
	RetryDelay      time.Duration
}

type JobView struct {
	Snapshot       jobstore.Snapshot `json:"snapshot"`
	Kind           OperationKind     `json:"kind"`
	Route          Route             `json:"route"`
	PlannedRoute   Route             `json:"planned_route"`
	DowngradedFrom Route             `json:"downgraded_from,omitempty"`
	RouteReason    RouteReason       `json:"route_reason,omitempty"`
	RouteEvidence  *RouteEvidence    `json:"route_evidence,omitempty"`
	Source         domain.Location   `json:"source"`
	Final          domain.Location   `json:"final"`
	Phase          Phase             `json:"phase,omitempty"`
	Bytes          uint64            `json:"bytes"`
	BytesTotal     *uint64           `json:"bytes_total,omitempty"`
	Items          uint64            `json:"items"`
	WaitingReason  string            `json:"waiting_reason,omitempty"`
	RecentError    string            `json:"recent_error,omitempty"`
	RecoveryResult string            `json:"recovery_result,omitempty"`
}

// Manager owns transfer execution independently from any client connection.
type Manager struct {
	store      *jobstore.Store
	resolver   Resolver
	planner    *Planner
	sameHost   SameHostCopyBackend
	level2     level2DataBackend
	generator  domain.Generator
	now        func() time.Time
	retryDelay time.Duration
	queue      chan domain.JobID
	scheduler  *TransferScheduler

	ctx    context.Context
	cancel context.CancelFunc
	wg     sync.WaitGroup
	// queueMu serializes consumption of a queued item with a paused Job being
	// resumed onto that same still-pending item.
	queueMu sync.Mutex

	mu               sync.Mutex
	started          bool
	upgradePrepared  bool
	activeExecutions int
	waiters          map[domain.JobID][]chan struct{}
	leases           map[domain.JobID]func()
	queueLeases      map[domain.JobID]*ResourceLease
	transitionErrors map[domain.JobID]error
	workers          int
}

var ErrActiveJobs = errors.New("transfer manager has active Jobs")

func NewManager(config ManagerConfig) (*Manager, error) {
	if config.Store == nil || config.Resolver == nil || config.Generator == nil {
		return nil, errors.New("new transfer manager: store, resolver, and generator are required")
	}
	hard := HardResourceCeilings()
	limits := config.ResourceLimits
	if limits == (ResourceLimits{}) {
		limits = hard
	}
	limits, err := TightenResourceLimits(limits)
	if err != nil {
		return nil, fmt.Errorf("new transfer manager: resource limits: %w", err)
	}
	if config.MaxConcurrent < 1 {
		return nil, errors.New("new transfer manager: concurrency must be positive")
	}
	if uint32(config.MaxConcurrent) > limits.ActiveJobs { //nolint:gosec // positivity and hard ceiling are checked immediately above.
		return nil, fmt.Errorf("new transfer manager: concurrency %d exceeds configured ceiling %d: %w", config.MaxConcurrent, limits.ActiveJobs, ErrResourceLimitExpansion)
	}
	if config.MaxQueued == 0 {
		config.MaxQueued = defaultQueuedJobs
	}
	if config.MaxQueued < config.MaxConcurrent {
		return nil, errors.New("new transfer manager: queue budget is below concurrency")
	}
	if uint32(config.MaxQueued) > limits.QueuedJobs { //nolint:gosec // positivity and hard ceiling are checked immediately above.
		return nil, fmt.Errorf("new transfer manager: queue budget %d exceeds configured ceiling %d: %w", config.MaxQueued, limits.QueuedJobs, ErrResourceLimitExpansion)
	}
	now := config.Now
	if now == nil {
		now = time.Now
	}
	retryDelay := config.RetryDelay
	if retryDelay == 0 {
		retryDelay = retrypolicy.DefaultJobDelay
	}
	if retryDelay < retrypolicy.DefaultJobDelay || retryDelay > retrypolicy.MaxJobDelay {
		return nil, fmt.Errorf("new transfer manager: retry delay must be within %s..%s", retrypolicy.DefaultJobDelay, retrypolicy.MaxJobDelay)
	}
	schedulerClock := config.SchedulerClock
	if schedulerClock == nil {
		schedulerClock = foundation.RealClock{}
	}
	scheduler, err := NewTransferSchedulerWithLimits(schedulerClock, config.SchedulerPolicy, limits)
	if err != nil {
		return nil, fmt.Errorf("new transfer manager: scheduler: %w", err)
	}
	rootContext, cancel := context.WithCancel(context.Background())
	return &Manager{
		store:            config.Store,
		resolver:         config.Resolver,
		planner:          NewPlannerWithSameHost(config.Resolver, config.SameHostCopy),
		sameHost:         config.SameHostCopy,
		generator:        config.Generator,
		now:              now,
		retryDelay:       retryDelay,
		queue:            make(chan domain.JobID, config.MaxQueued),
		scheduler:        scheduler,
		ctx:              rootContext,
		cancel:           cancel,
		waiters:          make(map[domain.JobID][]chan struct{}),
		leases:           make(map[domain.JobID]func()),
		queueLeases:      make(map[domain.JobID]*ResourceLease),
		transitionErrors: make(map[domain.JobID]error),
		workers:          config.MaxConcurrent,
	}, nil
}

func (manager *Manager) UpdateBandwidthPolicy(policy SchedulerPolicy) error {
	if manager == nil || manager.scheduler == nil {
		return errors.New("update bandwidth policy: manager scheduler is unavailable")
	}
	return manager.scheduler.Update(policy)
}

func (manager *Manager) SchedulerSnapshot() SchedulerSnapshot {
	if manager == nil || manager.scheduler == nil {
		return SchedulerSnapshot{}
	}
	return manager.scheduler.Snapshot()
}

func (manager *Manager) Start(ctx context.Context) error {
	manager.mu.Lock()
	if manager.started {
		manager.mu.Unlock()
		return errors.New("start transfer manager: already started")
	}
	manager.started = true
	manager.mu.Unlock()

	if _, err := manager.store.RecoverInterrupted(ctx, manager.generator, manager.now()); err != nil {
		return fmt.Errorf("start transfer manager: recover interrupted Jobs: %w", err)
	}
	for range manager.workers {
		manager.wg.Add(1)
		go manager.workerLoop()
	}
	jobs, err := manager.store.List(ctx, manager.queueCapacity())
	if err != nil {
		manager.cancel()
		manager.wg.Wait()
		return fmt.Errorf("start transfer manager: list Jobs: %w", err)
	}
	for _, snapshot := range jobs {
		if snapshot.State == job.StateQueued {
			if err := manager.enqueue(snapshot.JobID); err != nil {
				return err
			}
		}
	}
	return nil
}

func (manager *Manager) Close() {
	if manager == nil {
		return
	}
	manager.cancel()
	manager.wg.Wait()
	manager.releaseAllLeases()
}

// PrepareUpgrade atomically prevents new execution admission and proves that
// no Job is already executing. Queued Jobs remain durable for the next daemon.
func (manager *Manager) PrepareUpgrade() error {
	if manager == nil {
		return errors.New("prepare transfer manager upgrade: manager is unavailable")
	}
	manager.queueMu.Lock()
	defer manager.queueMu.Unlock()
	manager.mu.Lock()
	defer manager.mu.Unlock()
	if manager.upgradePrepared {
		return nil
	}
	if manager.activeExecutions != 0 {
		return ErrActiveJobs
	}
	manager.upgradePrepared = true
	return nil
}

func (manager *Manager) CreateCopy(ctx context.Context, intent Intent) (jobstore.Snapshot, error) {
	if !manager.isStarted() {
		return jobstore.Snapshot{}, errors.New("create copy Job: transfer manager is not started")
	}
	requestID, err := domain.NewRequestID(manager.generator)
	if err != nil {
		return jobstore.Snapshot{}, err
	}
	jobID, err := domain.NewJobID(manager.generator)
	if err != nil {
		return jobstore.Snapshot{}, err
	}
	eventID, err := domain.NewEventID(manager.generator)
	if err != nil {
		return jobstore.Snapshot{}, err
	}
	planID, err := manager.generator.New("plan_")
	if err != nil {
		return jobstore.Snapshot{}, fmt.Errorf("create copy Job: generate plan ID: %w", err)
	}
	if manager.sameHost != nil && intent.Source.Kind == domain.EntryFile && intent.Source.Location.EndpointID != "" &&
		intent.Source.Location.EndpointID == intent.DestinationDirectory.EndpointID {
		releaseAdmission, err := manager.store.AcquireHelperJobAdmission(ctx)
		if err != nil {
			return jobstore.Snapshot{}, fmt.Errorf("create copy Job: acquire Helper admission lease: %w", err)
		}
		defer releaseAdmission()
	}
	plan, create, err := manager.planner.FreezeCopy(ctx, FreezeRequest{
		Intent:    intent,
		RequestID: requestID,
		PlanID:    planID,
		JobID:     jobID,
		EventID:   eventID,
		Now:       manager.now(),
	})
	if err != nil {
		return jobstore.Snapshot{}, err
	}
	var queueLease *ResourceLease
	var release func()
	if create.InitialState == job.StateQueued {
		queueLease, err = manager.acquireQueueLease(plan.JobID)
		if err != nil {
			return jobstore.Snapshot{}, err
		}
		if acquirer, ok := manager.resolver.(PlanAcquirer); ok {
			release, err = acquirer.Acquire(ctx, plan)
			if err != nil {
				queueLease.Release()
				return jobstore.Snapshot{}, err
			}
		}
	}
	snapshot, _, err := manager.store.Create(ctx, create)
	if err != nil {
		if queueLease != nil {
			queueLease.Release()
		}
		if release != nil {
			release()
		}
		return jobstore.Snapshot{}, err
	}
	if snapshot.State == job.StateQueued {
		if release != nil {
			manager.retainLease(snapshot.JobID, release)
		}
		if err := manager.enqueueWithLease(plan.JobID, queueLease); err != nil {
			manager.releaseLease(snapshot.JobID)
			return jobstore.Snapshot{}, err
		}
	} else {
		if queueLease != nil {
			queueLease.Release()
		}
		if release != nil {
			release()
		}
	}
	return snapshot, nil
}

func (manager *Manager) CreateSyncBack(ctx context.Context, intent SyncBackIntent) (jobstore.Snapshot, error) {
	if !manager.isStarted() {
		return jobstore.Snapshot{}, errors.New("create sync-back Job: transfer manager is not started")
	}
	requestID, err := domain.NewRequestID(manager.generator)
	if err != nil {
		return jobstore.Snapshot{}, err
	}
	jobID, err := domain.NewJobID(manager.generator)
	if err != nil {
		return jobstore.Snapshot{}, err
	}
	eventID, err := domain.NewEventID(manager.generator)
	if err != nil {
		return jobstore.Snapshot{}, err
	}
	bindingEventID, err := domain.NewEventID(manager.generator)
	if err != nil {
		return jobstore.Snapshot{}, err
	}
	planID, err := manager.generator.New("plan_")
	if err != nil {
		return jobstore.Snapshot{}, fmt.Errorf("create sync-back Job: generate plan ID: %w", err)
	}
	plan, create, err := manager.planner.FreezeSyncBack(ctx, SyncBackFreezeRequest{
		SyncBack: intent.SyncBack, Source: intent.Source, RequestID: requestID, PlanID: planID, JobID: jobID,
		EventID: eventID, BindingEventID: string(bindingEventID), Now: manager.now(),
	})
	if err != nil {
		return jobstore.Snapshot{}, err
	}
	queueLease, err := manager.acquireQueueLease(plan.JobID)
	if err != nil {
		return jobstore.Snapshot{}, err
	}
	var release func()
	if acquirer, ok := manager.resolver.(PlanAcquirer); ok {
		release, err = acquirer.Acquire(ctx, plan)
		if err != nil {
			queueLease.Release()
			return jobstore.Snapshot{}, err
		}
	}
	snapshot, _, err := manager.store.Create(ctx, create)
	if err != nil {
		queueLease.Release()
		if release != nil {
			release()
		}
		return jobstore.Snapshot{}, err
	}
	if release != nil {
		manager.retainLease(snapshot.JobID, release)
	}
	if err := manager.enqueueWithLease(plan.JobID, queueLease); err != nil {
		manager.releaseLease(snapshot.JobID)
		return jobstore.Snapshot{}, err
	}
	return snapshot, nil
}

func (manager *Manager) CreateDelete(ctx context.Context, intent DeleteIntent) (jobstore.Snapshot, error) {
	if !manager.isStarted() {
		return jobstore.Snapshot{}, errors.New("create delete Job: transfer manager is not started")
	}
	requestID, err := domain.NewRequestID(manager.generator)
	if err != nil {
		return jobstore.Snapshot{}, err
	}
	jobID, err := domain.NewJobID(manager.generator)
	if err != nil {
		return jobstore.Snapshot{}, err
	}
	eventID, err := domain.NewEventID(manager.generator)
	if err != nil {
		return jobstore.Snapshot{}, err
	}
	planID, err := manager.generator.New("plan_")
	if err != nil {
		return jobstore.Snapshot{}, err
	}
	plan, create, err := manager.planner.FreezeDelete(ctx, DeleteFreezeRequest{
		Intent: intent, RequestID: requestID, PlanID: planID, JobID: jobID, EventID: eventID, Now: manager.now(),
	})
	if err != nil {
		return jobstore.Snapshot{}, err
	}
	queueLease, err := manager.acquireQueueLease(plan.JobID)
	if err != nil {
		return jobstore.Snapshot{}, err
	}
	var release func()
	if acquirer, ok := manager.resolver.(PlanAcquirer); ok {
		release, err = acquirer.Acquire(ctx, plan)
		if err != nil {
			queueLease.Release()
			return jobstore.Snapshot{}, err
		}
	}
	snapshot, _, err := manager.store.Create(ctx, create)
	if err != nil {
		queueLease.Release()
		if release != nil {
			release()
		}
		return jobstore.Snapshot{}, err
	}
	if release != nil {
		manager.retainLease(snapshot.JobID, release)
	}
	if err := manager.enqueueWithLease(snapshot.JobID, queueLease); err != nil {
		manager.releaseLease(snapshot.JobID)
		return jobstore.Snapshot{}, err
	}
	return snapshot, nil
}

func (manager *Manager) Capture(ctx context.Context, location domain.Location) (FileRef, error) {
	return manager.planner.Capture(ctx, location)
}

func (manager *Manager) CaptureDelete(ctx context.Context, location domain.Location) (FileRef, error) {
	return manager.planner.CaptureDelete(ctx, location)
}

func (manager *Manager) Wait(ctx context.Context, jobID domain.JobID) (jobstore.Snapshot, error) {
	return manager.waitUntil(ctx, jobID, func(state job.State) bool { return state.Terminal() })
}

func (manager *Manager) WaitForState(ctx context.Context, jobID domain.JobID, state job.State) (jobstore.Snapshot, error) {
	if !state.Valid() {
		return jobstore.Snapshot{}, fmt.Errorf("wait for Job state: invalid state %q", state)
	}
	return manager.waitUntil(ctx, jobID, func(current job.State) bool { return current == state })
}

func (manager *Manager) Pause(ctx context.Context, jobID domain.JobID) (jobstore.Snapshot, error) {
	snapshot, err := manager.store.Get(ctx, jobID)
	if err != nil {
		return jobstore.Snapshot{}, err
	}
	if snapshot.State == job.StatePaused {
		return snapshot, nil
	}
	if snapshot.State == job.StateQueued {
		paused, err := manager.transition(snapshot, job.StatePaused, "job_paused", map[string]string{"reason": "user"})
		if err == nil {
			manager.releaseLease(jobID)
		}
		return paused, err
	}
	if snapshot.State != job.StateRunning && snapshot.State != job.StateVerifying {
		return jobstore.Snapshot{}, fmt.Errorf("pause Job: state %q cannot be paused", snapshot.State)
	}
	value := true
	return manager.updateControl(ctx, snapshot, &value, nil, "job_pause_requested")
}

func (manager *Manager) Resume(ctx context.Context, jobID domain.JobID) (jobstore.Snapshot, error) {
	snapshot, err := manager.store.Get(ctx, jobID)
	if err != nil {
		return jobstore.Snapshot{}, err
	}
	if snapshot.State != job.StatePaused && snapshot.State != job.StateWaitingAuth && snapshot.State != job.StateRetryWait {
		return jobstore.Snapshot{}, fmt.Errorf("resume Job: state %q cannot be resumed", snapshot.State)
	}
	if snapshot.State == job.StatePaused {
		manager.queueMu.Lock()
		manager.mu.Lock()
		pendingQueueItem := manager.queueLeases[jobID] != nil
		manager.mu.Unlock()
		if pendingQueueItem {
			queued, transitionErr := manager.transition(snapshot, job.StateQueued, "job_resumed", map[string]string{"reason": "user"})
			manager.queueMu.Unlock()
			return queued, transitionErr
		}
		manager.queueMu.Unlock()
	}
	queueLease, err := manager.acquireQueueLease(jobID)
	if err != nil {
		return jobstore.Snapshot{}, err
	}
	if snapshot.PauseRequested {
		value := false
		snapshot, err = manager.updateControl(ctx, snapshot, &value, nil, "job_pause_cleared")
		if err != nil {
			queueLease.Release()
			return jobstore.Snapshot{}, err
		}
	}
	eventKind := "job_resumed"
	if snapshot.State == job.StateRetryWait {
		eventKind = "job_retried"
	}
	queued, err := manager.transition(snapshot, job.StateQueued, eventKind, map[string]string{"reason": "user"})
	if err != nil {
		queueLease.Release()
		return jobstore.Snapshot{}, err
	}
	if err := manager.enqueueWithLease(jobID, queueLease); err != nil {
		return jobstore.Snapshot{}, err
	}
	return queued, nil
}

func (manager *Manager) Cancel(ctx context.Context, jobID domain.JobID) (jobstore.Snapshot, error) {
	snapshot, err := manager.store.Get(ctx, jobID)
	if err != nil {
		return jobstore.Snapshot{}, err
	}
	if snapshot.State.Terminal() {
		return snapshot, nil
	}
	if snapshot.State == job.StateRunning || snapshot.State == job.StateVerifying {
		value := true
		return manager.updateControl(ctx, snapshot, nil, &value, "job_cancel_requested")
	}
	canceled, err := manager.transitionTerminal(snapshot, job.StateCanceled, "job_canceled", "canceled before execution", map[string]string{"reason": "user"})
	if err == nil {
		manager.releaseLease(jobID)
	}
	return canceled, err
}

func (manager *Manager) ResolveConflict(ctx context.Context, jobID domain.JobID, resolution ConflictPolicy, applyAll bool) (jobstore.Snapshot, error) {
	if resolution != ConflictOverwrite && resolution != ConflictSkip && resolution != ConflictAutoRename {
		return jobstore.Snapshot{}, errors.New("resolve conflict: resolution must be overwrite, skip, or auto_rename")
	}
	snapshot, err := manager.store.Get(ctx, jobID)
	if err != nil {
		return jobstore.Snapshot{}, err
	}
	conflicts, err := manager.store.ListConflicts(ctx, jobID)
	if err != nil {
		return jobstore.Snapshot{}, err
	}
	conflictIndex := -1
	for _, conflict := range conflicts {
		if conflict.State == "waiting" {
			conflictIndex = conflict.ConflictIndex
		}
	}
	if conflictIndex < 0 {
		return jobstore.Snapshot{}, errors.New("resolve conflict: Job has no waiting conflict")
	}
	queueLease, err := manager.acquireQueueLease(jobID)
	if err != nil {
		return jobstore.Snapshot{}, err
	}
	eventID, err := domain.NewEventID(manager.generator)
	if err != nil {
		queueLease.Release()
		return jobstore.Snapshot{}, err
	}
	applyScope := "item"
	if applyAll {
		applyScope = "job"
	}
	queued, err := manager.store.ResolveConflict(ctx, jobstore.ResolveConflictRequest{
		JobID: jobID, ConflictIndex: conflictIndex, ExpectedVersion: snapshot.StateVersion,
		Resolution: string(resolution), ApplyScope: applyScope, EventID: eventID, Now: manager.now(),
	})
	if err != nil {
		queueLease.Release()
		return jobstore.Snapshot{}, err
	}
	manager.notify(jobID)
	if err := manager.enqueueWithLease(jobID, queueLease); err != nil {
		return jobstore.Snapshot{}, err
	}
	return queued, nil
}

func (manager *Manager) Events(ctx context.Context, jobID domain.JobID, afterSequence int64, limit int) ([]jobstore.EventRecord, error) {
	return manager.store.ListEvents(ctx, jobID, afterSequence, limit)
}

func (manager *Manager) Jobs(ctx context.Context, limit int) ([]jobstore.Snapshot, error) {
	return manager.store.List(ctx, limit)
}

func (manager *Manager) JobViews(ctx context.Context, limit int) ([]JobView, error) {
	snapshots, err := manager.store.List(ctx, limit)
	if err != nil {
		return nil, err
	}
	views := make([]JobView, 0, len(snapshots))
	for _, snapshot := range snapshots {
		record, err := manager.store.GetPlan(ctx, snapshot.JobID)
		if err != nil {
			return nil, err
		}
		plan, err := DecodePlan(record, snapshot.JobID)
		if err != nil {
			return nil, err
		}
		plannedRoute := plan.Route
		if plan.RouteEvidence != nil && plan.RouteEvidence.Selected.Route != "" {
			plannedRoute = plan.RouteEvidence.Selected.Route
		}
		view := JobView{
			Snapshot: snapshot, Kind: plan.Kind, Route: plannedRoute, PlannedRoute: plannedRoute, RouteEvidence: plan.RouteEvidence, Source: plan.Source.Location, Final: plan.Final,
			BytesTotal: plan.Source.Fingerprint.Size, Items: 1,
		}
		if plan.Source.Kind == domain.EntryDirectory {
			view.Items = 0
			view.BytesTotal = nil
		}
		checkpoint, err := (JobJournal{Store: manager.store, StepIndex: 0}).Load(ctx, snapshot.JobID)
		if err != nil {
			return nil, err
		}
		if checkpoint != nil {
			view.Phase = checkpoint.Phase
			view.Bytes = checkpoint.Offset
			view.Final = checkpoint.Final
			if checkpoint.ActualRoute != "" {
				view.Route = checkpoint.ActualRoute
			}
			view.DowngradedFrom = checkpoint.DowngradedFrom
			view.RouteReason = checkpoint.RouteReason
			if plan.Source.Kind == domain.EntryDirectory {
				view.Items = checkpoint.Items
			}
		}
		afterSequence := snapshot.NextEventSequence - 8
		if afterSequence < 0 {
			afterSequence = 0
		}
		events, err := manager.store.ListEvents(ctx, snapshot.JobID, afterSequence, 8)
		if err != nil {
			return nil, err
		}
		for _, event := range events {
			var payload map[string]string
			if json.Unmarshal([]byte(event.PayloadJSON), &payload) == nil {
				if message := payload["error"]; message != "" {
					view.RecentError = message
				}
				if event.Kind == "job_recovered" {
					view.RecoveryResult = payload["reason"]
				}
			}
		}
		switch snapshot.State {
		case job.StateAwaitingConfirmation, job.StateWaitingAuth, job.StateWaitingConflict, job.StateRetryWait, job.StatePaused:
			view.WaitingReason = string(snapshot.State)
		}
		views = append(views, view)
	}
	return views, nil
}

func (manager *Manager) waitUntil(ctx context.Context, jobID domain.JobID, done func(job.State) bool) (jobstore.Snapshot, error) {
	for {
		snapshot, err := manager.store.Get(ctx, jobID)
		if err != nil {
			return jobstore.Snapshot{}, err
		}
		if done(snapshot.State) {
			return snapshot, nil
		}
		if transitionErr := manager.transitionError(jobID); transitionErr != nil {
			return snapshot, transitionErr
		}
		changed := make(chan struct{}, 1)
		manager.mu.Lock()
		manager.waiters[jobID] = append(manager.waiters[jobID], changed)
		manager.mu.Unlock()
		latest, err := manager.store.Get(ctx, jobID)
		if err != nil {
			manager.removeWaiter(jobID, changed)
			return jobstore.Snapshot{}, err
		}
		if done(latest.State) {
			manager.removeWaiter(jobID, changed)
			return latest, nil
		}
		if transitionErr := manager.transitionError(jobID); transitionErr != nil {
			manager.removeWaiter(jobID, changed)
			return latest, transitionErr
		}
		select {
		case <-ctx.Done():
			manager.removeWaiter(jobID, changed)
			return jobstore.Snapshot{}, ctx.Err()
		case <-manager.ctx.Done():
			manager.removeWaiter(jobID, changed)
			return jobstore.Snapshot{}, errors.New("wait for Job: transfer manager stopped")
		case <-changed:
		}
	}
}

func (manager *Manager) updateControl(ctx context.Context, snapshot jobstore.Snapshot, pause, cancel *bool, kind string) (jobstore.Snapshot, error) {
	eventID, err := domain.NewEventID(manager.generator)
	if err != nil {
		return jobstore.Snapshot{}, err
	}
	payload, err := json.Marshal(struct {
		Pause  *bool `json:"pause_requested,omitempty"`
		Cancel *bool `json:"cancel_requested,omitempty"`
	}{Pause: pause, Cancel: cancel})
	if err != nil {
		return jobstore.Snapshot{}, err
	}
	updated, _, err := manager.store.UpdateControl(ctx, jobstore.ControlRequest{
		JobID:           snapshot.JobID,
		ExpectedVersion: snapshot.StateVersion,
		PauseRequested:  pause,
		CancelRequested: cancel,
		EventID:         eventID,
		EventKind:       kind,
		PayloadJSON:     string(payload),
		Now:             manager.now(),
	})
	if err == nil {
		manager.notify(snapshot.JobID)
	}
	return updated, err
}

func (manager *Manager) workerLoop() {
	defer manager.wg.Done()
	for {
		select {
		case <-manager.ctx.Done():
			return
		case jobID := <-manager.queue:
			manager.execute(jobID)
		}
	}
}

func (manager *Manager) execute(jobID domain.JobID) {
	release := manager.takeLease(jobID)
	if release != nil {
		defer release()
	}
	manager.queueMu.Lock()
	manager.releaseQueueLease(jobID)
	snapshot, err := manager.store.Get(manager.ctx, jobID)
	admitted := false
	manager.mu.Lock()
	if err == nil && snapshot.State == job.StateQueued && !manager.upgradePrepared {
		manager.activeExecutions++
		admitted = true
	}
	manager.mu.Unlock()
	manager.queueMu.Unlock()
	if !admitted {
		return
	}
	defer manager.finishExecution()
	record, err := manager.store.GetPlan(manager.ctx, jobID)
	if err != nil {
		manager.fail(snapshot, fmt.Errorf("execute Job: load durable plan: %w", err))
		return
	}
	plan, err := DecodePlan(record, jobID)
	if err != nil {
		manager.fail(snapshot, fmt.Errorf("execute Job: decode durable plan: %w", err))
		return
	}
	defer manager.scheduler.ReleaseJob(plan.JobID)
	if err := manager.applyConflictResolution(&plan); err != nil {
		manager.fail(snapshot, fmt.Errorf("execute Job: apply durable conflict resolution: %w", err))
		return
	}
	resourceLease, err := manager.scheduler.AcquireResources(manager.ctx, executionResourceRequest(plan))
	if err != nil {
		if manager.ctx.Err() == nil {
			manager.fail(snapshot, fmt.Errorf("execute Job: acquire execution resources: %w", err))
		}
		return
	}
	defer resourceLease.Release()
	snapshot, err = manager.transition(snapshot, job.StateRunning, "job_started", map[string]any{})
	if err != nil {
		return
	}
	if acquirer, ok := manager.resolver.(PlanAcquirer); ok {
		if release == nil {
			acquiredRelease, acquireErr := acquirer.Acquire(manager.ctx, plan)
			if acquireErr != nil {
				manager.handleExecutionError(snapshot, acquireErr, Result{})
				return
			}
			release = acquiredRelease
			defer release()
		}
	}
	var result Result
	var executeErr error
	if plan.Kind == OperationDelete {
		result, executeErr = manager.executeDelete(plan)
	} else if plan.Kind == OperationMove && plan.MoveStrategy == MoveAtomicRename {
		result, executeErr = manager.executeAtomicMove(plan)
	} else {
		journal := JobJournal{Store: manager.store, StepIndex: 0, Now: manager.now}
		worker := &Worker{
			resolver: manager.resolver, journal: journal, sameHost: manager.sameHost,
			level2: manager.level2, scheduler: manager.scheduler,
		}
		result, executeErr = worker.Execute(manager.ctx, plan, manager.control(jobID))
	}
	if manager.ctx.Err() != nil {
		return
	}
	current, getErr := manager.store.Get(manager.ctx, jobID)
	if getErr != nil {
		return
	}
	switch {
	case errors.Is(executeErr, ErrPaused):
		if _, transitionErr := manager.transition(current, job.StatePaused, "job_paused", map[string]any{"offset": result.Bytes}); transitionErr != nil {
			manager.fail(current, fmt.Errorf("persist paused state: %w", transitionErr))
		}
	case errors.Is(executeErr, ErrCanceled):
		if _, transitionErr := manager.persistTerminal(current, job.StateCanceled, "job_canceled", "canceled with resumable part retained", map[string]any{"offset": result.Bytes}); transitionErr != nil {
			manager.fail(current, fmt.Errorf("persist canceled state: %w", transitionErr))
		}
	case executeErr != nil:
		manager.handleExecutionError(current, executeErr, result)
	case result.Outcome == OutcomeWaitingConflict:
		reason := "destination_appeared"
		if result.PreservedDestination.Path != "" {
			reason = "destination changed after original was preserved at " + string(result.PreservedDestination.Path)
		}
		manager.openConflict(current, plan, result.Final, reason)
	default:
		verificationPayload := map[string]any{"sha256": result.SHA256}
		checkpoint, checkpointErr := (JobJournal{Store: manager.store, StepIndex: 0}).Load(manager.ctx, jobID)
		if checkpointErr != nil {
			manager.handleExecutionError(current, checkpointErr, result)
			return
		}
		if checkpoint != nil && checkpoint.ActualRoute != "" {
			verificationPayload["planned_route"] = plan.Route
			verificationPayload["actual_route"] = checkpoint.ActualRoute
			verificationPayload["route_reason"] = checkpoint.RouteReason
			if checkpoint.DowngradedFrom != "" {
				verificationPayload["downgraded_from"] = checkpoint.DowngradedFrom
			}
		}
		verifying, transitionErr := manager.transition(current, job.StateVerifying, "job_verifying", verificationPayload)
		if transitionErr == nil {
			terminalState := job.StateCompleted
			eventKind := "job_completed"
			summary := string(result.Outcome)
			if result.PreservedDestination.Path != "" {
				summary += "; original remote version preserved at " + string(result.PreservedDestination.Path)
			}
			moveReason := ""
			if plan.Kind == OperationMove {
				deleted := false
				reason := "copy was skipped; source retained"
				if result.Outcome != OutcomeSkipped {
					deleted, reason = manager.finishMove(plan, result)
				}
				moveReason = reason
				if !deleted {
					terminalState = job.StateCompletedWithSourceRetained
					eventKind = "job_completed_source_retained"
					summary = "destination completed and source retained: " + reason
				} else {
					eventKind = "job_move_completed"
					summary = "destination verified and source deletion proved"
				}
			}
			if _, terminalErr := manager.persistTerminal(verifying, terminalState, eventKind, summary, map[string]any{
				"bytes": result.Bytes, "items": result.Items, "succeeded": result.Succeeded, "skipped": result.Skipped,
				"failed": result.Failed, "manifest": result.Manifest, "manifest_truncated": result.ManifestTruncated,
				"final": result.Final, "sha256": result.SHA256, "outcome": result.Outcome, "move_reason": moveReason,
				"preserved_destination": result.PreservedDestination,
			}); terminalErr != nil {
				manager.fail(verifying, fmt.Errorf("persist completed state: %w", terminalErr))
			}
		} else {
			manager.fail(current, fmt.Errorf("persist verifying state: %w", transitionErr))
		}
	}
}

func (manager *Manager) openConflict(snapshot jobstore.Snapshot, plan Plan, final domain.Location, class string) {
	sourceJSON, err := json.Marshal(plan.Source)
	if err != nil {
		manager.fail(snapshot, err)
		return
	}
	destinationJSON, err := json.Marshal(struct {
		Final domain.Location `json:"final"`
	}{Final: final})
	if err != nil {
		manager.fail(snapshot, err)
		return
	}
	eventID, err := domain.NewEventID(manager.generator)
	if err != nil {
		manager.fail(snapshot, err)
		return
	}
	_, _, err = manager.store.OpenConflict(manager.ctx, jobstore.OpenConflictRequest{
		JobID: snapshot.JobID, ExpectedVersion: snapshot.StateVersion, StepIndex: 0, Class: class,
		SourceJSON: string(sourceJSON), DestinationJSON: string(destinationJSON), EventID: eventID, Now: manager.now(),
	})
	if err == nil {
		manager.notify(snapshot.JobID)
	}
}

func (manager *Manager) applyConflictResolution(plan *Plan) error {
	conflicts, err := manager.store.ListConflicts(manager.ctx, plan.JobID)
	if err != nil {
		return err
	}
	for _, conflict := range conflicts {
		if conflict.State != "resolved" {
			continue
		}
		resolution := ConflictPolicy(conflict.Resolution)
		if resolution != ConflictOverwrite && resolution != ConflictSkip && resolution != ConflictAutoRename {
			return fmt.Errorf("execute Job: invalid durable conflict resolution %q", conflict.Resolution)
		}
		plan.ConflictPolicy = resolution
	}
	return nil
}

func (manager *Manager) control(jobID domain.JobID) Control {
	return ControlFunc(func(Checkpoint) ControlAction {
		snapshot, err := manager.store.Get(manager.ctx, jobID)
		if err != nil || snapshot.CancelRequested {
			return ControlCancel
		}
		if snapshot.PauseRequested {
			return ControlPause
		}
		return ControlContinue
	})
}

func (manager *Manager) handleExecutionError(snapshot jobstore.Snapshot, executeErr error, result Result) {
	var partial *PartialItemsError
	if errors.As(executeErr, &partial) {
		retryAt := manager.nextRetryAt()
		if _, transitionErr := manager.transitionRetry(snapshot, retryAt, map[string]any{
			"error": "directory items require retry", "failed": result.Failed, "succeeded": result.Succeeded,
			"skipped": result.Skipped, "items": result.Items, "manifest": result.Manifest,
			"manifest_truncated": result.ManifestTruncated,
		}); transitionErr != nil {
			manager.fail(snapshot, fmt.Errorf("persist directory retry: %w", transitionErr))
		}
		return
	}
	var operationError *domain.OpError
	payload := errorPayload(executeErr)
	if result.PreservedDestination.Path != "" {
		payload["preserved_destination"] = string(result.PreservedDestination.Path)
		payload["preservation_unknown"] = fmt.Sprintf("%t", result.PreservationUnknown)
	}
	if errors.As(executeErr, &operationError) {
		switch operationError.Code {
		case domain.CodeAuthRequired:
			if _, transitionErr := manager.transition(snapshot, job.StateWaitingAuth, "job_waiting_auth", payload); transitionErr != nil {
				manager.fail(snapshot, fmt.Errorf("persist waiting-auth state: %w", transitionErr))
			}
			return
		case domain.CodeConflict, domain.CodeAlreadyExists:
			if _, transitionErr := manager.transition(snapshot, job.StateWaitingConflict, "job_waiting_conflict", payload); transitionErr != nil {
				manager.fail(snapshot, fmt.Errorf("persist waiting-conflict state: %w", transitionErr))
			}
			return
		case domain.CodeTransportInterrupted, domain.CodeTimeout, domain.CodeResourceExhausted:
			retryAt := manager.nextRetryAt()
			if _, transitionErr := manager.transitionRetry(snapshot, retryAt, payload); transitionErr != nil {
				manager.fail(snapshot, fmt.Errorf("persist retry state: %w", transitionErr))
			}
			return
		}
	}
	if result.PreservedDestination.Path != "" {
		manager.failWithPayload(snapshot, executeErr, payload)
		return
	}
	manager.fail(snapshot, executeErr)
}

func (manager *Manager) fail(snapshot jobstore.Snapshot, failure error) {
	manager.failWithPayload(snapshot, failure, errorPayload(failure))

}

func (manager *Manager) failWithPayload(snapshot jobstore.Snapshot, failure error, payload any) {
	if _, err := manager.persistTerminal(snapshot, job.StateFailed, "job_failed", safeErrorSummary(failure), payload); err != nil {
		manager.recordTransitionError(snapshot.JobID, fmt.Errorf("persist terminal failure state: %w", err))
	}
}

func (manager *Manager) recordTransitionError(jobID domain.JobID, failure error) {
	manager.mu.Lock()
	manager.transitionErrors[jobID] = failure
	manager.mu.Unlock()
	manager.notify(jobID)
}

func (manager *Manager) transitionError(jobID domain.JobID) error {
	manager.mu.Lock()
	defer manager.mu.Unlock()
	return manager.transitionErrors[jobID]
}

func (manager *Manager) transition(snapshot jobstore.Snapshot, to job.State, kind string, payload any) (jobstore.Snapshot, error) {
	return manager.transitionRequest(snapshot, to, kind, payload, nil, nil)
}

func (manager *Manager) transitionRetry(snapshot jobstore.Snapshot, retryAt time.Time, payload any) (jobstore.Snapshot, error) {
	return manager.transitionRequest(snapshot, job.StateRetryWait, "job_retry_wait", payload, &retryAt, nil)
}

func (manager *Manager) nextRetryAt() time.Time {
	return manager.now().Add(manager.retryDelay)
}

func (manager *Manager) transitionTerminal(snapshot jobstore.Snapshot, to job.State, kind, summary string, payload any) (jobstore.Snapshot, error) {
	return manager.transitionRequest(snapshot, to, kind, payload, nil, &summary)
}

func (manager *Manager) persistTerminal(snapshot jobstore.Snapshot, to job.State, kind, summary string, payload any) (jobstore.Snapshot, error) {
	updated, err := manager.transitionTerminal(snapshot, to, kind, summary, payload)
	if err == nil {
		return updated, nil
	}
	latest, getErr := manager.store.Get(manager.ctx, snapshot.JobID)
	if getErr != nil {
		return jobstore.Snapshot{}, errors.Join(err, fmt.Errorf("reload terminal transition: %w", getErr))
	}
	if latest.State.Terminal() {
		if latest.State == to {
			return latest, nil
		}
		return jobstore.Snapshot{}, fmt.Errorf("terminal transition raced with state %q: %w", latest.State, err)
	}
	fallback := map[string]any{
		"details_omitted": true,
		"reason":          "terminal event payload or persistence retry",
	}
	updated, fallbackErr := manager.transitionTerminal(latest, to, kind, summary, fallback)
	if fallbackErr != nil {
		return jobstore.Snapshot{}, errors.Join(err, fmt.Errorf("persist bounded terminal fallback: %w", fallbackErr))
	}
	return updated, nil
}

func (manager *Manager) transitionRequest(snapshot jobstore.Snapshot, to job.State, kind string, payload any, retryAt *time.Time, summary *string) (jobstore.Snapshot, error) {
	eventID, err := domain.NewEventID(manager.generator)
	if err != nil {
		return jobstore.Snapshot{}, err
	}
	encoded, err := json.Marshal(payload)
	if err != nil {
		return jobstore.Snapshot{}, err
	}
	updated, _, err := manager.store.Transition(manager.ctx, jobstore.TransitionRequest{
		JobID:           snapshot.JobID,
		ExpectedVersion: snapshot.StateVersion,
		To:              to,
		EventID:         eventID,
		EventKind:       kind,
		PayloadJSON:     string(encoded),
		RetryAt:         retryAt,
		TerminalSummary: summary,
		Now:             manager.now(),
	})
	if err == nil {
		manager.notify(snapshot.JobID)
	}
	return updated, err
}

func (manager *Manager) enqueue(jobID domain.JobID) error {
	lease, err := manager.acquireQueueLease(jobID)
	if err != nil {
		return err
	}
	return manager.enqueueWithLease(jobID, lease)
}

func (manager *Manager) acquireQueueLease(jobID domain.JobID) (*ResourceLease, error) {
	lease, err := manager.scheduler.TryAcquireResources(ResourceRequest{
		JobID: jobID,
		Usage: ResourceUsage{QueuedJobs: 1},
	})
	if err != nil {
		return nil, fmt.Errorf("enqueue Job: acquire queue resource: %w", err)
	}
	return lease, nil
}

func (manager *Manager) enqueueWithLease(jobID domain.JobID, lease *ResourceLease) error {
	if lease == nil {
		return errors.New("enqueue Job: queue resource lease is required")
	}
	manager.mu.Lock()
	if manager.queueLeases[jobID] != nil {
		manager.mu.Unlock()
		lease.Release()
		return fmt.Errorf("enqueue Job: Job %q is already queued", jobID)
	}
	manager.queueLeases[jobID] = lease
	manager.mu.Unlock()
	select {
	case <-manager.ctx.Done():
		manager.releaseQueueLease(jobID)
		return errors.New("enqueue Job: transfer manager stopped")
	case manager.queue <- jobID:
		return nil
	}
}

func (manager *Manager) releaseQueueLease(jobID domain.JobID) {
	manager.mu.Lock()
	lease := manager.queueLeases[jobID]
	delete(manager.queueLeases, jobID)
	manager.mu.Unlock()
	if lease != nil {
		lease.Release()
	}
}

func (manager *Manager) notify(jobID domain.JobID) {
	manager.mu.Lock()
	waiters := manager.waiters[jobID]
	delete(manager.waiters, jobID)
	manager.mu.Unlock()
	for _, waiter := range waiters {
		close(waiter)
	}
}

func (manager *Manager) retainLease(jobID domain.JobID, release func()) {
	manager.mu.Lock()
	previous := manager.leases[jobID]
	manager.leases[jobID] = release
	manager.mu.Unlock()
	if previous != nil {
		previous()
	}
}

func (manager *Manager) takeLease(jobID domain.JobID) func() {
	manager.mu.Lock()
	defer manager.mu.Unlock()
	release := manager.leases[jobID]
	delete(manager.leases, jobID)
	return release
}

func (manager *Manager) releaseLease(jobID domain.JobID) {
	if release := manager.takeLease(jobID); release != nil {
		release()
	}
}

func (manager *Manager) releaseAllLeases() {
	manager.mu.Lock()
	leases := manager.leases
	manager.leases = make(map[domain.JobID]func())
	queueLeases := manager.queueLeases
	manager.queueLeases = make(map[domain.JobID]*ResourceLease)
	manager.mu.Unlock()
	for _, release := range leases {
		release()
	}
	for _, lease := range queueLeases {
		lease.Release()
	}
}

func executionResourceUsage(plan Plan) ResourceUsage {
	sshEndpoints := make(map[domain.EndpointID]struct{}, 2)
	for _, endpoint := range []domain.Endpoint{plan.SourceEndpoint, plan.DestinationEndpoint} {
		if endpoint.ID != "" && endpoint.Kind == domain.EndpointSSH {
			sshEndpoints[endpoint.ID] = struct{}{}
		}
	}
	connections := uint32(len(sshEndpoints)) //nolint:gosec // the set has at most two entries.
	helperProcesses := uint32(0)
	if plan.Route == RouteHelperSameHost || plan.Route == RouteLevel2Direct {
		helperProcesses = 1
	}
	return ResourceUsage{
		ActiveJobs:      1,
		Connections:     connections,
		SSHProcesses:    connections,
		HelperProcesses: helperProcesses,
		FileDescriptors: 2 + 3*connections,
		Goroutines:      1,
		MemoryBytes:     uint64(plan.BufferBytes),
	}
}

func executionResourceRequest(plan Plan) ResourceRequest {
	endpointIDs := make([]domain.EndpointID, 0, 2)
	endpointUsage := make(map[domain.EndpointID]ResourceUsage, 2)
	for _, endpoint := range []domain.Endpoint{plan.SourceEndpoint, plan.DestinationEndpoint} {
		if endpoint.ID == "" {
			continue
		}
		usage, exists := endpointUsage[endpoint.ID]
		if !exists {
			endpointIDs = append(endpointIDs, endpoint.ID)
			usage.ActiveJobs = 1
		}
		if endpoint.Kind == domain.EndpointSSH {
			usage.Connections = 1
		}
		endpointUsage[endpoint.ID] = usage
	}
	return ResourceRequest{
		JobID: plan.JobID, EndpointIDs: endpointIDs, Usage: executionResourceUsage(plan),
		EndpointUsage: endpointUsage,
	}
}

func (manager *Manager) removeWaiter(jobID domain.JobID, target chan struct{}) {
	manager.mu.Lock()
	defer manager.mu.Unlock()
	waiters := manager.waiters[jobID]
	for index, waiter := range waiters {
		if waiter == target {
			manager.waiters[jobID] = append(waiters[:index], waiters[index+1:]...)
			break
		}
	}
}

func (manager *Manager) isStarted() bool {
	manager.mu.Lock()
	defer manager.mu.Unlock()
	return manager.started && !manager.upgradePrepared
}

func (manager *Manager) finishExecution() {
	manager.mu.Lock()
	if manager.activeExecutions > 0 {
		manager.activeExecutions--
	}
	manager.mu.Unlock()
}

func (manager *Manager) queueCapacity() int {
	capacity := cap(manager.queue)
	if capacity > 1000 {
		return 1000
	}
	return capacity
}

func errorPayload(err error) map[string]string {
	payload := map[string]string{"error": safeErrorSummary(err)}
	var operationError *domain.OpError
	if errors.As(err, &operationError) {
		payload["code"] = string(operationError.Code)
		payload["retry"] = string(operationError.Retry.Kind)
		payload["effect"] = string(operationError.Effect)
	}
	return payload
}

func safeErrorSummary(err error) string {
	var operationError *domain.OpError
	if errors.As(err, &operationError) {
		return string(operationError.Code)
	}
	return "internal transfer error"
}
