package transfer

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/TyrantLucifer/awesome-mac-sftp/internal/domain"
	"github.com/TyrantLucifer/awesome-mac-sftp/internal/job"
	"github.com/TyrantLucifer/awesome-mac-sftp/internal/state/jobstore"
)

const defaultQueuedJobs = 128

type ManagerConfig struct {
	Store         *jobstore.Store
	Resolver      Resolver
	Generator     domain.Generator
	Now           func() time.Time
	MaxConcurrent int
	MaxQueued     int
}

// Manager owns transfer execution independently from any client connection.
type Manager struct {
	store     *jobstore.Store
	resolver  Resolver
	planner   *Planner
	generator domain.Generator
	now       func() time.Time
	queue     chan domain.JobID

	ctx    context.Context
	cancel context.CancelFunc
	wg     sync.WaitGroup

	mu      sync.Mutex
	started bool
	waiters map[domain.JobID][]chan struct{}
	workers int
}

func NewManager(config ManagerConfig) (*Manager, error) {
	if config.Store == nil || config.Resolver == nil || config.Generator == nil {
		return nil, errors.New("new transfer manager: store, resolver, and generator are required")
	}
	if config.MaxConcurrent < 1 || config.MaxConcurrent > 32 {
		return nil, errors.New("new transfer manager: concurrency is outside 1..32")
	}
	if config.MaxQueued == 0 {
		config.MaxQueued = defaultQueuedJobs
	}
	if config.MaxQueued < config.MaxConcurrent || config.MaxQueued > 4096 {
		return nil, errors.New("new transfer manager: queue budget is outside concurrency..4096")
	}
	now := config.Now
	if now == nil {
		now = time.Now
	}
	rootContext, cancel := context.WithCancel(context.Background())
	return &Manager{
		store:     config.Store,
		resolver:  config.Resolver,
		planner:   NewPlanner(config.Resolver),
		generator: config.Generator,
		now:       now,
		queue:     make(chan domain.JobID, config.MaxQueued),
		ctx:       rootContext,
		cancel:    cancel,
		waiters:   make(map[domain.JobID][]chan struct{}),
		workers:   config.MaxConcurrent,
	}, nil
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
	snapshot, _, err := manager.store.Create(ctx, create)
	if err != nil {
		return jobstore.Snapshot{}, err
	}
	if snapshot.State == job.StateQueued {
		if err := manager.enqueue(plan.JobID); err != nil {
			return jobstore.Snapshot{}, err
		}
	}
	return snapshot, nil
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
		return manager.transition(snapshot, job.StatePaused, "job_paused", map[string]string{"reason": "user"})
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
	if snapshot.State != job.StatePaused {
		return jobstore.Snapshot{}, fmt.Errorf("resume Job: state %q cannot be resumed", snapshot.State)
	}
	if snapshot.PauseRequested {
		value := false
		snapshot, err = manager.updateControl(ctx, snapshot, &value, nil, "job_pause_cleared")
		if err != nil {
			return jobstore.Snapshot{}, err
		}
	}
	queued, err := manager.transition(snapshot, job.StateQueued, "job_resumed", map[string]string{"reason": "user"})
	if err != nil {
		return jobstore.Snapshot{}, err
	}
	if err := manager.enqueue(jobID); err != nil {
		return jobstore.Snapshot{}, err
	}
	return queued, nil
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
	snapshot, err := manager.store.Get(manager.ctx, jobID)
	if err != nil || snapshot.State != job.StateQueued {
		return
	}
	snapshot, err = manager.transition(snapshot, job.StateRunning, "job_started", map[string]any{})
	if err != nil {
		return
	}
	record, err := manager.store.GetPlan(manager.ctx, jobID)
	if err != nil {
		manager.fail(snapshot, err)
		return
	}
	plan, err := DecodePlan(record, jobID)
	if err != nil {
		manager.fail(snapshot, err)
		return
	}
	journal := JobJournal{Store: manager.store, StepIndex: 0, Now: manager.now}
	result, executeErr := NewWorker(manager.resolver, journal).Execute(manager.ctx, plan, manager.control(jobID))
	if manager.ctx.Err() != nil {
		return
	}
	current, getErr := manager.store.Get(manager.ctx, jobID)
	if getErr != nil {
		return
	}
	switch {
	case errors.Is(executeErr, ErrPaused):
		_, _ = manager.transition(current, job.StatePaused, "job_paused", map[string]any{"offset": result.Bytes})
	case errors.Is(executeErr, ErrCanceled):
		_, _ = manager.transitionTerminal(current, job.StateCanceled, "job_canceled", "canceled with resumable part retained", map[string]any{"offset": result.Bytes})
	case executeErr != nil:
		manager.handleExecutionError(current, executeErr)
	case result.Outcome == OutcomeWaitingConflict:
		_, _ = manager.transition(current, job.StateWaitingConflict, "job_waiting_conflict", map[string]any{"final": result.Final})
	default:
		verifying, transitionErr := manager.transition(current, job.StateVerifying, "job_verifying", map[string]any{"sha256": result.SHA256})
		if transitionErr == nil {
			_, _ = manager.transitionTerminal(verifying, job.StateCompleted, "job_completed", string(result.Outcome), map[string]any{
				"bytes": result.Bytes, "final": result.Final, "sha256": result.SHA256, "outcome": result.Outcome,
			})
		}
	}
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

func (manager *Manager) handleExecutionError(snapshot jobstore.Snapshot, executeErr error) {
	var operationError *domain.OpError
	if errors.As(executeErr, &operationError) {
		switch operationError.Code {
		case domain.CodeAuthRequired:
			_, _ = manager.transition(snapshot, job.StateWaitingAuth, "job_waiting_auth", errorPayload(executeErr))
			return
		case domain.CodeConflict, domain.CodeAlreadyExists:
			_, _ = manager.transition(snapshot, job.StateWaitingConflict, "job_waiting_conflict", errorPayload(executeErr))
			return
		case domain.CodeTransportInterrupted, domain.CodeTimeout, domain.CodeResourceExhausted:
			retryAt := manager.now().Add(time.Minute)
			_, _ = manager.transitionRetry(snapshot, retryAt, errorPayload(executeErr))
			return
		}
	}
	manager.fail(snapshot, executeErr)
}

func (manager *Manager) fail(snapshot jobstore.Snapshot, failure error) {
	_, _ = manager.transitionTerminal(snapshot, job.StateFailed, "job_failed", failure.Error(), errorPayload(failure))
}

func (manager *Manager) transition(snapshot jobstore.Snapshot, to job.State, kind string, payload any) (jobstore.Snapshot, error) {
	return manager.transitionRequest(snapshot, to, kind, payload, nil, nil)
}

func (manager *Manager) transitionRetry(snapshot jobstore.Snapshot, retryAt time.Time, payload any) (jobstore.Snapshot, error) {
	return manager.transitionRequest(snapshot, job.StateRetryWait, "job_retry_wait", payload, &retryAt, nil)
}

func (manager *Manager) transitionTerminal(snapshot jobstore.Snapshot, to job.State, kind, summary string, payload any) (jobstore.Snapshot, error) {
	return manager.transitionRequest(snapshot, to, kind, payload, nil, &summary)
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
	select {
	case <-manager.ctx.Done():
		return errors.New("enqueue Job: transfer manager stopped")
	case manager.queue <- jobID:
		return nil
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
	return manager.started
}

func (manager *Manager) queueCapacity() int {
	capacity := cap(manager.queue)
	if capacity > 1000 {
		return 1000
	}
	return capacity
}

func errorPayload(err error) map[string]string {
	return map[string]string{"error": err.Error()}
}
