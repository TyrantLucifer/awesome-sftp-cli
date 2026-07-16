// Package jobstore persists immutable plans, Jobs, steps, and ordered events.
package jobstore

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"regexp"
	"time"

	"github.com/TyrantLucifer/awesome-mac-sftp/internal/domain"
	"github.com/TyrantLucifer/awesome-mac-sftp/internal/job"
	"github.com/TyrantLucifer/awesome-mac-sftp/internal/state/wal"
)

const (
	maxEncodedRowBytes   = 256 * 1024
	planWALBudget        = uint64(1024 * 1024)
	jobWALBudget         = uint64(512 * 1024)
	stepWALBudget        = uint64(1024 * 1024)
	checkpointWALBudget  = uint64(512 * 1024)
	conflictWALBudget    = uint64(512 * 1024)
	eventWALBudget       = uint64(512 * 1024)
	dedupWALBudget       = uint64(512 * 1024)
	editBindingWALBudget = uint64(512 * 1024)
	maxJobSteps          = 5
)

var (
	ErrInvalidTransition = errors.New("invalid Job transition")
	ErrVersionConflict   = errors.New("job state version conflict")
	planIDPattern        = regexp.MustCompile(`^plan_[a-z2-7]{26}$`)
	eventKindPattern     = regexp.MustCompile(`^[a-z][a-z0-9_]{0,63}$`)
)

type Store struct {
	database *sql.DB
	walGuard *wal.FileGuard
}

type Step struct {
	Kind            string
	SourceJSON      *string
	DestinationJSON *string
}

type CreateRequest struct {
	PlanID          string
	RequestID       domain.RequestID
	JobID           domain.JobID
	Kind            string
	SourceJSON      string
	DestinationJSON *string
	Route           string
	Verification    string
	ConflictPolicy  string
	RiskClass       string
	InitialState    job.State
	EventID         domain.EventID
	Now             time.Time
	Steps           []Step
	InitialConflict *ConflictSeed
	EditSession     *EditSessionBinding
}

// EditSessionBinding moves a durable dirty edit session into uploading while
// binding it to the newly-created Job in the same BEGIN IMMEDIATE transaction.
type EditSessionBinding struct {
	SessionID       string
	ExpectedVersion int64
	EventID         string
	EventKind       string
}

type ConflictSeed struct {
	StepIndex       int
	Class           string
	SourceJSON      string
	DestinationJSON string
}

type ConflictRecord struct {
	JobID           domain.JobID
	ConflictIndex   int
	StepIndex       int
	Class           string
	State           string
	SourceJSON      string
	DestinationJSON string
	Resolution      string
	ApplyScope      string
	CreatedAt       time.Time
	ResolvedAt      *time.Time
}

type ResolveConflictRequest struct {
	JobID           domain.JobID
	ConflictIndex   int
	ExpectedVersion int64
	Resolution      string
	ApplyScope      string
	EventID         domain.EventID
	Now             time.Time
}

type OpenConflictRequest struct {
	JobID           domain.JobID
	ExpectedVersion int64
	StepIndex       int
	Class           string
	SourceJSON      string
	DestinationJSON string
	EventID         domain.EventID
	Now             time.Time
}

type TransitionRequest struct {
	JobID           domain.JobID
	ExpectedVersion int64
	To              job.State
	EventID         domain.EventID
	EventKind       string
	PayloadJSON     string
	RetryAt         *time.Time
	TerminalSummary *string
	Now             time.Time
}

type ControlRequest struct {
	JobID           domain.JobID
	ExpectedVersion int64
	PauseRequested  *bool
	CancelRequested *bool
	EventID         domain.EventID
	EventKind       string
	PayloadJSON     string
	Now             time.Time
}

type PlanRecord struct {
	PlanID          string
	RequestID       domain.RequestID
	Kind            string
	SourceJSON      string
	DestinationJSON *string
	Route           string
	Verification    string
	ConflictPolicy  string
	RiskClass       string
	FrozenAt        time.Time
}

type EventRecord struct {
	JobID       domain.JobID
	Sequence    int64
	EventID     domain.EventID
	Kind        string
	PayloadJSON string
	CreatedAt   time.Time
}

type CheckpointRequest struct {
	JobID             domain.JobID
	StepIndex         int
	Phase             string
	VerifiedOffset    uint64
	SourceFingerprint string
	PartLocationJSON  string
	ChecksumState     []byte
	Now               time.Time
}

type CheckpointRecord struct {
	JobID             domain.JobID
	StepIndex         int
	Phase             string
	VerifiedOffset    uint64
	SourceFingerprint string
	PartLocationJSON  string
	ChecksumState     []byte
	UpdatedAt         time.Time
}

type Snapshot struct {
	JobID             domain.JobID
	PlanID            string
	State             job.State
	StateVersion      int64
	NextEventSequence int64
	PauseRequested    bool
	CancelRequested   bool
	RetryAt           *time.Time
	TerminalSummary   *string
	CreatedAt         time.Time
	UpdatedAt         time.Time
}

func New(ctx context.Context, database *sql.DB) (*Store, error) {
	if database == nil {
		return nil, fmt.Errorf("new job store: nil database")
	}
	connection, err := database.Conn(ctx)
	if err != nil {
		return nil, fmt.Errorf("new job store: reserve WAL initializer: %w", err)
	}
	guard, guardErr := wal.OpenFileGuard(ctx, connection)
	closeErr := connection.Close()
	if err := errors.Join(guardErr, closeErr); err != nil {
		return nil, fmt.Errorf("new job store: %w", err)
	}
	return &Store{database: database, walGuard: guard}, nil
}

// CheckpointIdle truncates the WAL while the store has no active write batch.
func (store *Store) CheckpointIdle(ctx context.Context) (returnErr error) {
	if store == nil || store.database == nil || store.walGuard == nil {
		return fmt.Errorf("job store: nil database")
	}
	connection, err := store.database.Conn(ctx)
	if err != nil {
		return fmt.Errorf("job store: reserve checkpoint connection: %w", err)
	}
	defer func() {
		returnErr = errors.Join(returnErr, connection.Close())
	}()
	if err := store.walGuard.TruncateIdle(ctx, connection); err != nil {
		return fmt.Errorf("job store: truncate idle WAL: %w", err)
	}
	return nil
}

func (store *Store) Create(ctx context.Context, request CreateRequest) (Snapshot, bool, error) {
	if err := validateCreate(request); err != nil {
		return Snapshot{}, false, err
	}
	var snapshot Snapshot
	duplicate := false
	err := store.immediate(ctx, createWALBudgets(len(request.Steps), request.InitialConflict != nil, request.EditSession != nil), func(connection *sql.Conn, writer *transactionWriter) error {
		var operation, response string
		err := connection.QueryRowContext(ctx, "SELECT operation, response_json FROM request_dedup WHERE request_id=?", request.RequestID).Scan(&operation, &response)
		if err == nil {
			if operation != "create_job" {
				return fmt.Errorf("create Job: request %q already belongs to operation %q", request.RequestID, operation)
			}
			var stored struct {
				JobID domain.JobID `json:"job_id"`
			}
			if err := json.Unmarshal([]byte(response), &stored); err != nil {
				return fmt.Errorf("create Job: decode idempotent response: %w", err)
			}
			snapshot, err = getSnapshot(ctx, connection, stored.JobID)
			duplicate = true
			return err
		}
		if !errors.Is(err, sql.ErrNoRows) {
			return fmt.Errorf("create Job: inspect request: %w", err)
		}

		destination := nullableValue(request.DestinationJSON)
		if _, err := writer.ExecContext(ctx, "INSERT INTO operation_plans(plan_id, request_id, kind, source_json, destination_json, route, verification, conflict_policy, risk_class, frozen_at_unix) VALUES(?, ?, ?, ?, ?, ?, ?, ?, ?, ?)", request.PlanID, request.RequestID, request.Kind, request.SourceJSON, destination, request.Route, request.Verification, request.ConflictPolicy, request.RiskClass, request.Now.Unix()); err != nil {
			return fmt.Errorf("create Job plan: %w", err)
		}
		if _, err := writer.ExecContext(ctx, "INSERT INTO jobs(job_id, plan_id, state, state_version, next_event_sequence, pause_requested, cancel_requested, retry_at_unix, created_at_unix, updated_at_unix, terminal_summary) VALUES(?, ?, ?, 1, 2, 0, 0, NULL, ?, ?, NULL)", request.JobID, request.PlanID, request.InitialState, request.Now.Unix(), request.Now.Unix()); err != nil {
			return fmt.Errorf("create Job row: %w", err)
		}
		for index, step := range request.Steps {
			if _, err := writer.ExecContext(ctx, "INSERT INTO job_steps(job_id, step_index, kind, state, attempt, source_json, destination_json, created_at_unix, updated_at_unix) VALUES(?, ?, ?, 'pending', 0, ?, ?, ?, ?)", request.JobID, index, step.Kind, nullableValue(step.SourceJSON), nullableValue(step.DestinationJSON), request.Now.Unix(), request.Now.Unix()); err != nil {
				return fmt.Errorf("create Job step %d: %w", index, err)
			}
		}
		if conflict := request.InitialConflict; conflict != nil {
			if _, err := writer.ExecContext(ctx, "INSERT INTO job_conflicts(job_id, conflict_index, step_index, class, state, source_json, destination_json, resolution, apply_scope, created_at_unix, resolved_at_unix) VALUES(?, 0, ?, ?, 'waiting', ?, ?, NULL, NULL, ?, NULL)", request.JobID, conflict.StepIndex, conflict.Class, conflict.SourceJSON, conflict.DestinationJSON, request.Now.Unix()); err != nil {
				return fmt.Errorf("create Job conflict: %w", err)
			}
		}
		if binding := request.EditSession; binding != nil {
			var durableDetails int
			if err := connection.QueryRowContext(ctx, "SELECT count(*) FROM edit_session_details WHERE session_id=?", binding.SessionID).Scan(&durableDetails); err != nil {
				return fmt.Errorf("bind edit session recovery details: %w", err)
			}
			if durableDetails != 1 {
				return errors.New("bind edit session recovery details: session is not fully reconstructible")
			}
			result, err := writer.ExecContext(ctx, "UPDATE edit_sessions SET state='uploading',state_version=state_version+1,updated_at_unix=? WHERE session_id=? AND state_version=? AND local_state='dirty' AND state IN ('awaiting_decision','conflict')", request.Now.Unix(), binding.SessionID, binding.ExpectedVersion)
			if err != nil {
				return fmt.Errorf("bind edit session state: %w", err)
			}
			changed, err := result.RowsAffected()
			if err != nil || changed != 1 {
				return errors.New("bind edit session state: row not found or version changed")
			}
			var sequence int64
			if err := connection.QueryRowContext(ctx, "SELECT COALESCE(MAX(sequence),0)+1 FROM edit_session_events WHERE session_id=?", binding.SessionID).Scan(&sequence); err != nil {
				return fmt.Errorf("bind edit session event sequence: %w", err)
			}
			if _, err := writer.ExecContext(ctx, "INSERT INTO edit_session_events(session_id,sequence,event_id,kind,error_code,created_at_unix) VALUES(?,?,?,?,NULL,?)", binding.SessionID, sequence, binding.EventID, binding.EventKind, request.Now.Unix()); err != nil {
				return fmt.Errorf("bind edit session event: %w", err)
			}
			if _, err := writer.ExecContext(ctx, "INSERT INTO edit_session_jobs(session_id,job_id,created_at_unix) VALUES(?,?,?)", binding.SessionID, request.JobID, request.Now.Unix()); err != nil {
				return fmt.Errorf("bind edit session Job: %w", err)
			}
		}
		if _, err := writer.ExecContext(ctx, "INSERT INTO job_events(job_id, sequence, event_id, kind, payload_json, created_at_unix) VALUES(?, 1, ?, 'job_created', '{}', ?)", request.JobID, request.EventID, request.Now.Unix()); err != nil {
			return fmt.Errorf("create Job event: %w", err)
		}
		response, err = marshalJobResponse(request.JobID)
		if err != nil {
			return err
		}
		if _, err := writer.ExecContext(ctx, "INSERT INTO request_dedup(request_id, operation, job_id, response_json, created_at_unix) VALUES(?, 'create_job', ?, ?, ?)", request.RequestID, request.JobID, response, request.Now.Unix()); err != nil {
			return fmt.Errorf("create Job idempotency row: %w", err)
		}
		snapshot, err = getSnapshot(ctx, connection, request.JobID)
		return err
	})
	if err != nil {
		return Snapshot{}, false, err
	}
	return snapshot, duplicate, nil
}

func (store *Store) ListConflicts(ctx context.Context, jobID domain.JobID) ([]ConflictRecord, error) {
	if _, err := domain.ParseJobID(string(jobID)); err != nil {
		return nil, fmt.Errorf("list Job conflicts: %w", err)
	}
	if store == nil || store.database == nil {
		return nil, errors.New("list Job conflicts: nil store")
	}
	readContext, cancel := wal.ReaderContext(ctx)
	defer cancel()
	rows, err := store.database.QueryContext(readContext, `SELECT job_id, conflict_index, step_index, class, state, source_json, destination_json,
		resolution, apply_scope, created_at_unix, resolved_at_unix FROM job_conflicts WHERE job_id=? ORDER BY conflict_index`, jobID)
	if err != nil {
		return nil, fmt.Errorf("list Job conflicts: %w", err)
	}
	defer rows.Close()
	records := make([]ConflictRecord, 0, 4)
	for rows.Next() {
		var record ConflictRecord
		var resolution, applyScope sql.NullString
		var createdAt int64
		var resolvedAt sql.NullInt64
		if err := rows.Scan(&record.JobID, &record.ConflictIndex, &record.StepIndex, &record.Class, &record.State,
			&record.SourceJSON, &record.DestinationJSON, &resolution, &applyScope, &createdAt, &resolvedAt); err != nil {
			return nil, fmt.Errorf("list Job conflicts: scan: %w", err)
		}
		record.Resolution = resolution.String
		record.ApplyScope = applyScope.String
		record.CreatedAt = time.Unix(createdAt, 0)
		if resolvedAt.Valid {
			value := time.Unix(resolvedAt.Int64, 0)
			record.ResolvedAt = &value
		}
		records = append(records, record)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("list Job conflicts: finish: %w", err)
	}
	return records, nil
}

func (store *Store) OpenConflict(ctx context.Context, request OpenConflictRequest) (Snapshot, ConflictRecord, error) {
	if err := validateOpenConflict(request); err != nil {
		return Snapshot{}, ConflictRecord{}, err
	}
	var snapshot Snapshot
	var record ConflictRecord
	err := store.immediate(ctx, []uint64{conflictWALBudget, jobWALBudget, eventWALBudget}, func(connection *sql.Conn, writer *transactionWriter) error {
		current, err := getSnapshot(ctx, connection, request.JobID)
		if err != nil {
			return err
		}
		if current.StateVersion != request.ExpectedVersion {
			return fmt.Errorf("%w: Job %q has version %d, expected %d", ErrVersionConflict, request.JobID, current.StateVersion, request.ExpectedVersion)
		}
		if current.State != job.StateRunning && current.State != job.StateVerifying {
			return fmt.Errorf("%w: %q -> %q", ErrInvalidTransition, current.State, job.StateWaitingConflict)
		}
		var conflictIndex int
		if err := connection.QueryRowContext(ctx, "SELECT COALESCE(MAX(conflict_index), -1)+1 FROM job_conflicts WHERE job_id=?", request.JobID).Scan(&conflictIndex); err != nil {
			return fmt.Errorf("open Job conflict index: %w", err)
		}
		if _, err := writer.ExecContext(ctx, "INSERT INTO job_conflicts(job_id, conflict_index, step_index, class, state, source_json, destination_json, resolution, apply_scope, created_at_unix, resolved_at_unix) VALUES(?, ?, ?, ?, 'waiting', ?, ?, NULL, NULL, ?, NULL)", request.JobID, conflictIndex, request.StepIndex, request.Class, request.SourceJSON, request.DestinationJSON, request.Now.Unix()); err != nil {
			return fmt.Errorf("open Job conflict row: %w", err)
		}
		result, err := writer.ExecContext(ctx, "UPDATE jobs SET state='waiting_conflict', state_version=state_version+1, next_event_sequence=next_event_sequence+1, updated_at_unix=? WHERE job_id=? AND state_version=?", request.Now.Unix(), request.JobID, request.ExpectedVersion)
		if err != nil {
			return fmt.Errorf("open Job conflict state: %w", err)
		}
		changed, err := result.RowsAffected()
		if err != nil || changed != 1 {
			return fmt.Errorf("%w: Job %q changed concurrently", ErrVersionConflict, request.JobID)
		}
		payload, err := json.Marshal(struct {
			ConflictIndex int    `json:"conflict_index"`
			Class         string `json:"class"`
		}{conflictIndex, request.Class})
		if err != nil {
			return fmt.Errorf("open Job conflict payload: %w", err)
		}
		if _, err := writer.ExecContext(ctx, "INSERT INTO job_events(job_id, sequence, event_id, kind, payload_json, created_at_unix) VALUES(?, ?, ?, 'job_waiting_conflict', ?, ?)", request.JobID, current.NextEventSequence, request.EventID, string(payload), request.Now.Unix()); err != nil {
			return fmt.Errorf("open Job conflict event: %w", err)
		}
		snapshot, err = getSnapshot(ctx, connection, request.JobID)
		if err != nil {
			return err
		}
		record = ConflictRecord{
			JobID: request.JobID, ConflictIndex: conflictIndex, StepIndex: request.StepIndex,
			Class: request.Class, State: "waiting", SourceJSON: request.SourceJSON,
			DestinationJSON: request.DestinationJSON, CreatedAt: request.Now,
		}
		return nil
	})
	return snapshot, record, err
}

func (store *Store) ResolveConflict(ctx context.Context, request ResolveConflictRequest) (Snapshot, error) {
	if err := validateResolveConflict(request); err != nil {
		return Snapshot{}, err
	}
	payload, err := json.Marshal(struct {
		ConflictIndex int    `json:"conflict_index"`
		Resolution    string `json:"resolution"`
		ApplyScope    string `json:"apply_scope"`
	}{request.ConflictIndex, request.Resolution, request.ApplyScope})
	if err != nil {
		return Snapshot{}, fmt.Errorf("resolve Job conflict: encode event: %w", err)
	}
	var snapshot Snapshot
	err = store.immediate(ctx, []uint64{conflictWALBudget, jobWALBudget, eventWALBudget}, func(connection *sql.Conn, writer *transactionWriter) error {
		current, err := getSnapshot(ctx, connection, request.JobID)
		if err != nil {
			return err
		}
		if current.StateVersion != request.ExpectedVersion {
			return fmt.Errorf("%w: Job %q has version %d, expected %d", ErrVersionConflict, request.JobID, current.StateVersion, request.ExpectedVersion)
		}
		if current.State != job.StateAwaitingConfirmation && current.State != job.StateWaitingConflict {
			return fmt.Errorf("%w: %q -> %q", ErrInvalidTransition, current.State, job.StateQueued)
		}
		result, err := writer.ExecContext(ctx, "UPDATE job_conflicts SET state='resolved', resolution=?, apply_scope=?, resolved_at_unix=? WHERE job_id=? AND conflict_index=? AND state='waiting'", request.Resolution, request.ApplyScope, request.Now.Unix(), request.JobID, request.ConflictIndex)
		if err != nil {
			return fmt.Errorf("resolve Job conflict row: %w", err)
		}
		changed, err := result.RowsAffected()
		if err != nil {
			return fmt.Errorf("resolve Job conflict row count: %w", err)
		}
		if changed != 1 {
			return errors.New("resolve Job conflict: waiting conflict not found")
		}
		result, err = writer.ExecContext(ctx, "UPDATE jobs SET state='queued', state_version=state_version+1, next_event_sequence=next_event_sequence+1, updated_at_unix=? WHERE job_id=? AND state_version=?", request.Now.Unix(), request.JobID, request.ExpectedVersion)
		if err != nil {
			return fmt.Errorf("resolve Job conflict state: %w", err)
		}
		changed, err = result.RowsAffected()
		if err != nil || changed != 1 {
			return fmt.Errorf("%w: Job %q changed concurrently", ErrVersionConflict, request.JobID)
		}
		if _, err := writer.ExecContext(ctx, "INSERT INTO job_events(job_id, sequence, event_id, kind, payload_json, created_at_unix) VALUES(?, ?, ?, 'job_conflict_resolved', ?, ?)", request.JobID, current.NextEventSequence, request.EventID, string(payload), request.Now.Unix()); err != nil {
			return fmt.Errorf("resolve Job conflict event: %w", err)
		}
		snapshot, err = getSnapshot(ctx, connection, request.JobID)
		return err
	})
	return snapshot, err
}

func (store *Store) Transition(ctx context.Context, request TransitionRequest) (Snapshot, bool, error) {
	if err := validateTransitionRequest(request); err != nil {
		return Snapshot{}, false, err
	}
	var snapshot Snapshot
	duplicate := false
	err := store.immediate(ctx, []uint64{jobWALBudget, eventWALBudget}, func(connection *sql.Conn, writer *transactionWriter) error {
		var eventJobID domain.JobID
		var eventKind, payload string
		err := connection.QueryRowContext(ctx, "SELECT job_id, kind, payload_json FROM job_events WHERE event_id=?", request.EventID).Scan(&eventJobID, &eventKind, &payload)
		if err == nil {
			if eventJobID != request.JobID || eventKind != request.EventKind || payload != request.PayloadJSON {
				return fmt.Errorf("transition Job: event ID %q has different immutable content", request.EventID)
			}
			snapshot, err = getSnapshot(ctx, connection, request.JobID)
			duplicate = true
			return err
		}
		if !errors.Is(err, sql.ErrNoRows) {
			return fmt.Errorf("transition Job: inspect event ID: %w", err)
		}

		current, err := getSnapshot(ctx, connection, request.JobID)
		if err != nil {
			return err
		}
		if current.StateVersion != request.ExpectedVersion {
			return fmt.Errorf("%w: Job %q has version %d, expected %d", ErrVersionConflict, request.JobID, current.StateVersion, request.ExpectedVersion)
		}
		if !job.CanTransition(current.State, request.To) {
			return fmt.Errorf("%w: %q -> %q", ErrInvalidTransition, current.State, request.To)
		}
		retryAt := nullableTimeUnix(request.RetryAt)
		terminalSummary := nullableValue(request.TerminalSummary)
		result, err := writer.ExecContext(ctx, "UPDATE jobs SET state=?, state_version=state_version+1, next_event_sequence=next_event_sequence+1, retry_at_unix=?, terminal_summary=?, updated_at_unix=? WHERE job_id=? AND state_version=?", request.To, retryAt, terminalSummary, request.Now.Unix(), request.JobID, request.ExpectedVersion)
		if err != nil {
			return fmt.Errorf("transition Job row: %w", err)
		}
		changed, err := result.RowsAffected()
		if err != nil {
			return fmt.Errorf("transition Job row count: %w", err)
		}
		if changed != 1 {
			return fmt.Errorf("%w: Job %q changed concurrently", ErrVersionConflict, request.JobID)
		}
		if _, err := writer.ExecContext(ctx, "INSERT INTO job_events(job_id, sequence, event_id, kind, payload_json, created_at_unix) VALUES(?, ?, ?, ?, ?, ?)", request.JobID, current.NextEventSequence, request.EventID, request.EventKind, request.PayloadJSON, request.Now.Unix()); err != nil {
			return fmt.Errorf("transition Job event: %w", err)
		}
		snapshot, err = getSnapshot(ctx, connection, request.JobID)
		return err
	})
	if err != nil {
		return Snapshot{}, false, err
	}
	return snapshot, duplicate, nil
}

func (store *Store) Get(ctx context.Context, jobID domain.JobID) (Snapshot, error) {
	if _, err := domain.ParseJobID(string(jobID)); err != nil {
		return Snapshot{}, err
	}
	if store == nil || store.database == nil {
		return Snapshot{}, fmt.Errorf("get Job: nil store")
	}
	readContext, cancel := wal.ReaderContext(ctx)
	defer cancel()
	return getSnapshot(readContext, store.database, jobID)
}

func (store *Store) GetPlan(ctx context.Context, jobID domain.JobID) (PlanRecord, error) {
	if _, err := domain.ParseJobID(string(jobID)); err != nil {
		return PlanRecord{}, fmt.Errorf("get Job plan: %w", err)
	}
	if store == nil || store.database == nil {
		return PlanRecord{}, errors.New("get Job plan: nil store")
	}
	readContext, cancel := wal.ReaderContext(ctx)
	defer cancel()
	var record PlanRecord
	var destination sql.NullString
	var frozenAt int64
	err := store.database.QueryRowContext(readContext, `SELECT
		p.plan_id, p.request_id, p.kind, p.source_json, p.destination_json,
		p.route, p.verification, p.conflict_policy, p.risk_class, p.frozen_at_unix
		FROM operation_plans p JOIN jobs j ON j.plan_id=p.plan_id WHERE j.job_id=?`, jobID).Scan(
		&record.PlanID,
		&record.RequestID,
		&record.Kind,
		&record.SourceJSON,
		&destination,
		&record.Route,
		&record.Verification,
		&record.ConflictPolicy,
		&record.RiskClass,
		&frozenAt,
	)
	if err != nil {
		return PlanRecord{}, fmt.Errorf("get Job plan: %w", err)
	}
	if destination.Valid {
		value := destination.String
		record.DestinationJSON = &value
	}
	record.FrozenAt = time.Unix(frozenAt, 0)
	return record, nil
}

func (store *Store) List(ctx context.Context, limit int) ([]Snapshot, error) {
	if store == nil || store.database == nil {
		return nil, errors.New("list Jobs: nil store")
	}
	if limit < 1 || limit > 1000 {
		return nil, errors.New("list Jobs: limit is outside 1..1000")
	}
	readContext, cancel := wal.ReaderContext(ctx)
	defer cancel()
	rows, err := store.database.QueryContext(readContext, `SELECT
		job_id, plan_id, state, state_version, next_event_sequence,
		pause_requested, cancel_requested, retry_at_unix, terminal_summary,
		created_at_unix, updated_at_unix
		FROM jobs ORDER BY updated_at_unix DESC, job_id LIMIT ?`, limit)
	if err != nil {
		return nil, fmt.Errorf("list Jobs: %w", err)
	}
	defer rows.Close()
	result := make([]Snapshot, 0, limit)
	for rows.Next() {
		snapshot, err := scanSnapshot(rows)
		if err != nil {
			return nil, fmt.Errorf("list Jobs: %w", err)
		}
		result = append(result, snapshot)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("list Jobs: %w", err)
	}
	return result, nil
}

func (store *Store) ListEvents(ctx context.Context, jobID domain.JobID, afterSequence int64, limit int) ([]EventRecord, error) {
	if _, err := domain.ParseJobID(string(jobID)); err != nil {
		return nil, fmt.Errorf("list Job events: %w", err)
	}
	if store == nil || store.database == nil {
		return nil, errors.New("list Job events: nil store")
	}
	if afterSequence < 0 || limit < 1 || limit > 1000 {
		return nil, errors.New("list Job events: invalid cursor or limit")
	}
	readContext, cancel := wal.ReaderContext(ctx)
	defer cancel()
	rows, err := store.database.QueryContext(readContext, `SELECT
		job_id, sequence, event_id, kind, payload_json, created_at_unix
		FROM job_events WHERE job_id=? AND sequence>? ORDER BY sequence LIMIT ?`, jobID, afterSequence, limit)
	if err != nil {
		return nil, fmt.Errorf("list Job events: %w", err)
	}
	defer rows.Close()
	result := make([]EventRecord, 0, limit)
	for rows.Next() {
		var event EventRecord
		var createdAt int64
		if err := rows.Scan(&event.JobID, &event.Sequence, &event.EventID, &event.Kind, &event.PayloadJSON, &createdAt); err != nil {
			return nil, fmt.Errorf("list Job events: %w", err)
		}
		event.CreatedAt = time.Unix(createdAt, 0)
		result = append(result, event)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("list Job events: %w", err)
	}
	return result, nil
}

func (store *Store) UpdateControl(ctx context.Context, request ControlRequest) (Snapshot, bool, error) {
	if err := validateControlRequest(request); err != nil {
		return Snapshot{}, false, err
	}
	var snapshot Snapshot
	duplicate := false
	err := store.immediate(ctx, []uint64{jobWALBudget, eventWALBudget}, func(connection *sql.Conn, writer *transactionWriter) error {
		var eventJobID domain.JobID
		var eventKind, payload string
		err := connection.QueryRowContext(ctx, "SELECT job_id, kind, payload_json FROM job_events WHERE event_id=?", request.EventID).Scan(&eventJobID, &eventKind, &payload)
		if err == nil {
			if eventJobID != request.JobID || eventKind != request.EventKind || payload != request.PayloadJSON {
				return fmt.Errorf("update Job control: event ID %q has different immutable content", request.EventID)
			}
			snapshot, err = getSnapshot(ctx, connection, request.JobID)
			duplicate = true
			return err
		}
		if !errors.Is(err, sql.ErrNoRows) {
			return fmt.Errorf("update Job control: inspect event ID: %w", err)
		}
		current, err := getSnapshot(ctx, connection, request.JobID)
		if err != nil {
			return err
		}
		if current.StateVersion != request.ExpectedVersion {
			return fmt.Errorf("%w: Job %q has version %d, expected %d", ErrVersionConflict, request.JobID, current.StateVersion, request.ExpectedVersion)
		}
		if current.State.Terminal() {
			return errors.New("update Job control: terminal Job cannot be changed")
		}
		pauseRequested := current.PauseRequested
		cancelRequested := current.CancelRequested
		if request.PauseRequested != nil {
			pauseRequested = *request.PauseRequested
		}
		if request.CancelRequested != nil {
			cancelRequested = *request.CancelRequested
		}
		result, err := writer.ExecContext(ctx, `UPDATE jobs SET
			pause_requested=?, cancel_requested=?, state_version=state_version+1,
			next_event_sequence=next_event_sequence+1, updated_at_unix=?
			WHERE job_id=? AND state_version=?`, boolInt(pauseRequested), boolInt(cancelRequested), request.Now.Unix(), request.JobID, request.ExpectedVersion)
		if err != nil {
			return fmt.Errorf("update Job control: %w", err)
		}
		changed, err := result.RowsAffected()
		if err != nil {
			return fmt.Errorf("update Job control row count: %w", err)
		}
		if changed != 1 {
			return fmt.Errorf("%w: Job %q changed concurrently", ErrVersionConflict, request.JobID)
		}
		if _, err := writer.ExecContext(ctx, "INSERT INTO job_events(job_id, sequence, event_id, kind, payload_json, created_at_unix) VALUES(?, ?, ?, ?, ?, ?)", request.JobID, current.NextEventSequence, request.EventID, request.EventKind, request.PayloadJSON, request.Now.Unix()); err != nil {
			return fmt.Errorf("update Job control event: %w", err)
		}
		snapshot, err = getSnapshot(ctx, connection, request.JobID)
		return err
	})
	if err != nil {
		return Snapshot{}, false, err
	}
	return snapshot, duplicate, nil
}

func (store *Store) SaveCheckpoint(ctx context.Context, request CheckpointRequest) error {
	if err := validateCheckpoint(request); err != nil {
		return err
	}
	return store.immediate(ctx, []uint64{checkpointWALBudget}, func(_ *sql.Conn, writer *transactionWriter) error {
		result, err := writer.ExecContext(ctx, `INSERT INTO job_checkpoints(
			job_id, step_index, phase, verified_offset, source_fingerprint,
			part_location_json, checksum_state, updated_at_unix
		) VALUES(?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(job_id, step_index) DO UPDATE SET
			phase=excluded.phase,
			verified_offset=excluded.verified_offset,
			source_fingerprint=excluded.source_fingerprint,
			part_location_json=excluded.part_location_json,
			checksum_state=excluded.checksum_state,
			updated_at_unix=excluded.updated_at_unix
		WHERE excluded.verified_offset >= job_checkpoints.verified_offset
			AND excluded.updated_at_unix >= job_checkpoints.updated_at_unix
			AND excluded.source_fingerprint IS job_checkpoints.source_fingerprint`,
			request.JobID,
			request.StepIndex,
			request.Phase,
			request.VerifiedOffset,
			nullableString(request.SourceFingerprint),
			nullableString(request.PartLocationJSON),
			nullableBytes(request.ChecksumState),
			request.Now.Unix(),
		)
		if err != nil {
			return fmt.Errorf("save Job checkpoint: %w", err)
		}
		changed, err := result.RowsAffected()
		if err != nil {
			return fmt.Errorf("save Job checkpoint row count: %w", err)
		}
		if changed != 1 {
			return errors.New("save Job checkpoint: stale offset, time, or source identity")
		}
		return nil
	})
}

func (store *Store) GetCheckpoint(ctx context.Context, jobID domain.JobID, stepIndex int) (CheckpointRecord, error) {
	if _, err := domain.ParseJobID(string(jobID)); err != nil {
		return CheckpointRecord{}, fmt.Errorf("get Job checkpoint: %w", err)
	}
	if stepIndex < 0 || stepIndex >= maxJobSteps {
		return CheckpointRecord{}, errors.New("get Job checkpoint: step index is outside the supported range")
	}
	if store == nil || store.database == nil {
		return CheckpointRecord{}, errors.New("get Job checkpoint: nil store")
	}
	readContext, cancel := wal.ReaderContext(ctx)
	defer cancel()
	var record CheckpointRecord
	var sourceFingerprint, partLocation sql.NullString
	var checksumState []byte
	var updatedAt int64
	err := store.database.QueryRowContext(readContext, `SELECT
		job_id, step_index, phase, verified_offset, source_fingerprint,
		part_location_json, checksum_state, updated_at_unix
		FROM job_checkpoints WHERE job_id=? AND step_index=?`, jobID, stepIndex).Scan(
		&record.JobID,
		&record.StepIndex,
		&record.Phase,
		&record.VerifiedOffset,
		&sourceFingerprint,
		&partLocation,
		&checksumState,
		&updatedAt,
	)
	if err != nil {
		return CheckpointRecord{}, fmt.Errorf("get Job checkpoint: %w", err)
	}
	if sourceFingerprint.Valid {
		record.SourceFingerprint = sourceFingerprint.String
	}
	if partLocation.Valid {
		record.PartLocationJSON = partLocation.String
	}
	record.ChecksumState = append([]byte(nil), checksumState...)
	record.UpdatedAt = time.Unix(updatedAt, 0)
	return record, nil
}

func (store *Store) RecoverInterrupted(ctx context.Context, generator domain.Generator, now time.Time) (int, error) {
	if generator == nil {
		return 0, fmt.Errorf("recover Jobs: nil ID generator")
	}
	if now.Unix() <= 0 {
		return 0, fmt.Errorf("recover Jobs: invalid time")
	}
	recovered := 0
	for {
		readContext, cancel := wal.ReaderContext(ctx)
		var jobID domain.JobID
		err := store.database.QueryRowContext(readContext, "SELECT job_id FROM jobs WHERE state IN ('running', 'verifying') ORDER BY job_id LIMIT 1").Scan(&jobID)
		cancel()
		if errors.Is(err, sql.ErrNoRows) {
			return recovered, nil
		}
		if err != nil {
			return 0, fmt.Errorf("recover Jobs: select interrupted Job: %w", err)
		}
		eventID, err := domain.NewEventID(generator)
		if err != nil {
			return 0, fmt.Errorf("recover Job %q event ID: %w", jobID, err)
		}
		changed := false
		budgets := []uint64{jobWALBudget, stepWALBudget, stepWALBudget, stepWALBudget, stepWALBudget, stepWALBudget, eventWALBudget}
		err = store.immediate(ctx, budgets, func(connection *sql.Conn, writer *transactionWriter) error {
			var state job.State
			var stateVersion, nextSequence int64
			if err := connection.QueryRowContext(ctx, "SELECT state, state_version, next_event_sequence FROM jobs WHERE job_id=?", jobID).Scan(&state, &stateVersion, &nextSequence); err != nil {
				return fmt.Errorf("recover Job %q state: %w", jobID, err)
			}
			next, recoverable := job.ConservativeRestartState(state)
			if !recoverable {
				return nil
			}
			rows, err := connection.QueryContext(ctx, "SELECT step_index FROM job_steps WHERE job_id=? AND state IN ('running', 'verifying') ORDER BY step_index LIMIT 6", jobID)
			if err != nil {
				return fmt.Errorf("recover Job %q steps: %w", jobID, err)
			}
			stepIndexes := make([]int64, 0, maxJobSteps)
			for rows.Next() {
				var stepIndex int64
				if err := rows.Scan(&stepIndex); err != nil {
					_ = rows.Close()
					return fmt.Errorf("recover Job %q step index: %w", jobID, err)
				}
				stepIndexes = append(stepIndexes, stepIndex)
			}
			if err := errors.Join(rows.Err(), rows.Close()); err != nil {
				return fmt.Errorf("recover Job %q finish steps: %w", jobID, err)
			}
			if len(stepIndexes) > maxJobSteps {
				return fmt.Errorf("recover Job %q has more than %d active steps", jobID, maxJobSteps)
			}
			result, err := writer.ExecContext(ctx, "UPDATE jobs SET state=?, state_version=state_version+1, next_event_sequence=next_event_sequence+1, updated_at_unix=? WHERE job_id=? AND state_version=? AND state=?", next, now.Unix(), jobID, stateVersion, state)
			if err != nil {
				return fmt.Errorf("recover Job %q: %w", jobID, err)
			}
			changedRows, err := result.RowsAffected()
			if err != nil {
				return fmt.Errorf("recover Job %q row count: %w", jobID, err)
			}
			if changedRows != 1 {
				return fmt.Errorf("recover Job %q: concurrent state change", jobID)
			}
			for _, stepIndex := range stepIndexes {
				result, err := writer.ExecContext(ctx, "UPDATE job_steps SET state='paused', updated_at_unix=? WHERE job_id=? AND step_index=? AND state IN ('running', 'verifying')", now.Unix(), jobID, stepIndex)
				if err != nil {
					return fmt.Errorf("recover Job %q step %d: %w", jobID, stepIndex, err)
				}
				rows, err := result.RowsAffected()
				if err != nil {
					return fmt.Errorf("recover Job %q step %d row count: %w", jobID, stepIndex, err)
				}
				if rows != 1 {
					return fmt.Errorf("recover Job %q step %d changed concurrently", jobID, stepIndex)
				}
			}
			if _, err := writer.ExecContext(ctx, "INSERT INTO job_events(job_id, sequence, event_id, kind, payload_json, created_at_unix) VALUES(?, ?, ?, 'job_recovered', '{\"reason\":\"daemon_restart\"}', ?)", jobID, nextSequence, eventID, now.Unix()); err != nil {
				return fmt.Errorf("recover Job %q event: %w", jobID, err)
			}
			changed = true
			return nil
		})
		if err != nil {
			return 0, err
		}
		if changed {
			recovered++
		}
	}
}

type rowQueryer interface {
	QueryRowContext(context.Context, string, ...any) *sql.Row
}

func getSnapshot(ctx context.Context, queryer rowQueryer, jobID domain.JobID) (Snapshot, error) {
	snapshot, err := scanSnapshot(queryer.QueryRowContext(ctx, "SELECT job_id, plan_id, state, state_version, next_event_sequence, pause_requested, cancel_requested, retry_at_unix, terminal_summary, created_at_unix, updated_at_unix FROM jobs WHERE job_id=?", jobID))
	if err != nil {
		return Snapshot{}, fmt.Errorf("get Job %q: %w", jobID, err)
	}
	return snapshot, nil
}

type rowScanner interface{ Scan(...any) error }

func scanSnapshot(row rowScanner) (Snapshot, error) {
	var snapshot Snapshot
	var pauseRequested, cancelRequested int64
	var retryAt sql.NullInt64
	var terminalSummary sql.NullString
	var createdAt, updatedAt int64
	if err := row.Scan(
		&snapshot.JobID, &snapshot.PlanID, &snapshot.State, &snapshot.StateVersion, &snapshot.NextEventSequence,
		&pauseRequested, &cancelRequested, &retryAt, &terminalSummary, &createdAt, &updatedAt,
	); err != nil {
		return Snapshot{}, err
	}
	snapshot.PauseRequested = pauseRequested == 1
	snapshot.CancelRequested = cancelRequested == 1
	if retryAt.Valid {
		value := time.Unix(retryAt.Int64, 0)
		snapshot.RetryAt = &value
	}
	if terminalSummary.Valid {
		value := terminalSummary.String
		snapshot.TerminalSummary = &value
	}
	snapshot.CreatedAt = time.Unix(createdAt, 0)
	snapshot.UpdatedAt = time.Unix(updatedAt, 0)
	return snapshot, nil
}

type transactionWriter struct {
	connection  *sql.Conn
	transaction *wal.FileTransaction
	nextBudget  int
}

func (writer *transactionWriter) ExecContext(ctx context.Context, query string, args ...any) (sql.Result, error) {
	if writer == nil || writer.connection == nil || writer.transaction == nil {
		return nil, fmt.Errorf("job store writer: nil transaction")
	}
	result, execErr := writer.connection.ExecContext(ctx, query, args...)
	observeErr := writer.transaction.AfterStatement(writer.nextBudget)
	writer.nextBudget++
	if err := errors.Join(execErr, observeErr); err != nil {
		return result, err
	}
	return result, nil
}

func (store *Store) immediate(ctx context.Context, budgets []uint64, operation func(*sql.Conn, *transactionWriter) error) (returnErr error) {
	if store == nil || store.database == nil || store.walGuard == nil {
		return fmt.Errorf("job store: nil database")
	}
	connection, err := store.database.Conn(ctx)
	if err != nil {
		return fmt.Errorf("job store: reserve connection: %w", err)
	}
	defer func() {
		returnErr = errors.Join(returnErr, connection.Close())
	}()
	walTransaction, err := store.walGuard.Begin(budgets)
	if err != nil {
		return fmt.Errorf("job store: WAL preflight: %w", err)
	}
	if _, err := connection.ExecContext(ctx, "BEGIN IMMEDIATE"); err != nil {
		observeErr := walTransaction.AfterRollback()
		return fmt.Errorf("job store: begin immediate: %w", errors.Join(err, observeErr))
	}
	writer := &transactionWriter{connection: connection, transaction: walTransaction}
	if err := operation(connection, writer); err != nil {
		_, rollbackErr := connection.ExecContext(context.Background(), "ROLLBACK")
		observeErr := walTransaction.AfterRollback()
		return errors.Join(err, rollbackErr, observeErr)
	}
	if err := walTransaction.BeforeCommit(); err != nil {
		_, rollbackErr := connection.ExecContext(context.Background(), "ROLLBACK")
		observeErr := walTransaction.AfterRollback()
		return errors.Join(err, rollbackErr, observeErr)
	}
	if _, err := connection.ExecContext(ctx, "COMMIT"); err != nil {
		_, rollbackErr := connection.ExecContext(context.Background(), "ROLLBACK")
		observeErr := walTransaction.AfterRollback()
		return fmt.Errorf("job store: commit: %w", errors.Join(err, rollbackErr, observeErr))
	}
	if err := walTransaction.AfterCommit(); err != nil {
		return fmt.Errorf("job store: committed WAL observation: %w", err)
	}
	if _, err := store.walGuard.PassiveCheckpoint(ctx, connection); err != nil {
		return fmt.Errorf("job store: committed passive checkpoint: %w", err)
	}
	return nil
}

func createWALBudgets(stepCount int, conflict, editBinding bool) []uint64 {
	budgets := make([]uint64, 0, stepCount+8)
	budgets = append(budgets, planWALBudget, jobWALBudget)
	for range stepCount {
		budgets = append(budgets, stepWALBudget)
	}
	if conflict {
		budgets = append(budgets, conflictWALBudget)
	}
	if editBinding {
		budgets = append(budgets, editBindingWALBudget, eventWALBudget, editBindingWALBudget)
	}
	return append(budgets, eventWALBudget, dedupWALBudget)
}

func validateCreate(request CreateRequest) error {
	if !planIDPattern.MatchString(request.PlanID) {
		return fmt.Errorf("create Job: invalid plan ID %q", request.PlanID)
	}
	if _, err := domain.ParseRequestID(string(request.RequestID)); err != nil {
		return fmt.Errorf("create Job: %w", err)
	}
	if _, err := domain.ParseJobID(string(request.JobID)); err != nil {
		return fmt.Errorf("create Job: %w", err)
	}
	if _, err := domain.ParseEventID(string(request.EventID)); err != nil {
		return fmt.Errorf("create Job: %w", err)
	}
	if request.InitialState != job.StateDraft && request.InitialState != job.StateAwaitingConfirmation && request.InitialState != job.StateQueued {
		return fmt.Errorf("create Job: invalid initial state %q", request.InitialState)
	}
	if request.Now.Unix() <= 0 || len(request.Steps) == 0 || len(request.Steps) > maxJobSteps {
		return fmt.Errorf("create Job: invalid time or step count outside 1..%d", maxJobSteps)
	}
	if err := wal.ValidateTransactionBudgets(createWALBudgets(len(request.Steps), request.InitialConflict != nil, request.EditSession != nil)); err != nil {
		return fmt.Errorf("create Job: %w", err)
	}
	values := []string{request.Kind, request.SourceJSON, request.Route, request.Verification, request.ConflictPolicy, request.RiskClass}
	if request.DestinationJSON != nil {
		values = append(values, *request.DestinationJSON)
	}
	for index, step := range request.Steps {
		if step.Kind == "" {
			return fmt.Errorf("create Job: step %d has empty kind", index)
		}
		values = append(values, step.Kind)
		if step.SourceJSON != nil {
			if !json.Valid([]byte(*step.SourceJSON)) {
				return fmt.Errorf("create Job: step %d has invalid source JSON", index)
			}
			values = append(values, *step.SourceJSON)
		}
		if step.DestinationJSON != nil {
			if !json.Valid([]byte(*step.DestinationJSON)) {
				return fmt.Errorf("create Job: step %d has invalid destination JSON", index)
			}
			values = append(values, *step.DestinationJSON)
		}
	}
	if err := validateEncodedValues(values...); err != nil {
		return fmt.Errorf("create Job: %w", err)
	}
	if !json.Valid([]byte(request.SourceJSON)) || request.DestinationJSON != nil && !json.Valid([]byte(*request.DestinationJSON)) {
		return fmt.Errorf("create Job: invalid plan JSON")
	}
	if conflict := request.InitialConflict; conflict != nil {
		if request.InitialState != job.StateAwaitingConfirmation || conflict.StepIndex < 0 || conflict.StepIndex >= len(request.Steps) ||
			!eventKindPattern.MatchString(conflict.Class) || !json.Valid([]byte(conflict.SourceJSON)) || !json.Valid([]byte(conflict.DestinationJSON)) {
			return errors.New("create Job: invalid initial conflict")
		}
		if err := validateEncodedValues(conflict.Class, conflict.SourceJSON, conflict.DestinationJSON); err != nil {
			return fmt.Errorf("create Job: %w", err)
		}
	}
	if binding := request.EditSession; binding != nil {
		if err := validateLowerHex(binding.SessionID, 32); err != nil || binding.ExpectedVersion < 1 || len(binding.EventID) < 1 || len(binding.EventID) > 128 || !eventKindPattern.MatchString(binding.EventKind) {
			return errors.New("create Job: invalid edit session binding")
		}
		if request.DestinationJSON == nil {
			return errors.New("create Job: edit session binding requires a Plan Version 2 payload")
		}
		var metadata struct {
			Version       int    `json:"version"`
			Origin        string `json:"origin"`
			EditSessionID string `json:"edit_session_id"`
		}
		if err := json.Unmarshal([]byte(*request.DestinationJSON), &metadata); err != nil || metadata.Version != 2 || metadata.Origin != "sync_back" || metadata.EditSessionID != binding.SessionID {
			return errors.New("create Job: edit session binding disagrees with Plan Version 2 sync-back payload")
		}
	}
	return nil
}

func validateLowerHex(value string, length int) error {
	if len(value) != length {
		return errors.New("invalid lowercase hex identity")
	}
	for _, character := range value {
		if character < '0' || character > '9' && character < 'a' || character > 'f' {
			return errors.New("invalid lowercase hex identity")
		}
	}
	return nil
}

func validateResolveConflict(request ResolveConflictRequest) error {
	if _, err := domain.ParseJobID(string(request.JobID)); err != nil {
		return fmt.Errorf("resolve Job conflict: %w", err)
	}
	if _, err := domain.ParseEventID(string(request.EventID)); err != nil {
		return fmt.Errorf("resolve Job conflict: %w", err)
	}
	if request.ConflictIndex < 0 || request.ExpectedVersion <= 0 || request.Now.Unix() <= 0 {
		return errors.New("resolve Job conflict: invalid index, version, or time")
	}
	if request.Resolution != "overwrite" && request.Resolution != "skip" && request.Resolution != "auto_rename" {
		return errors.New("resolve Job conflict: invalid resolution")
	}
	if request.ApplyScope != "item" && request.ApplyScope != "job" {
		return errors.New("resolve Job conflict: invalid apply scope")
	}
	return nil
}

func validateOpenConflict(request OpenConflictRequest) error {
	if _, err := domain.ParseJobID(string(request.JobID)); err != nil {
		return fmt.Errorf("open Job conflict: %w", err)
	}
	if _, err := domain.ParseEventID(string(request.EventID)); err != nil {
		return fmt.Errorf("open Job conflict: %w", err)
	}
	if request.ExpectedVersion <= 0 || request.StepIndex < 0 || request.StepIndex >= maxJobSteps ||
		!eventKindPattern.MatchString(request.Class) || !json.Valid([]byte(request.SourceJSON)) ||
		!json.Valid([]byte(request.DestinationJSON)) || request.Now.Unix() <= 0 {
		return errors.New("open Job conflict: invalid version, step, class, payload, or time")
	}
	return validateEncodedValues(request.Class, request.SourceJSON, request.DestinationJSON)
}

func validateTransitionRequest(request TransitionRequest) error {
	if _, err := domain.ParseJobID(string(request.JobID)); err != nil {
		return fmt.Errorf("transition Job: %w", err)
	}
	if _, err := domain.ParseEventID(string(request.EventID)); err != nil {
		return fmt.Errorf("transition Job: %w", err)
	}
	if request.ExpectedVersion <= 0 || !request.To.Valid() || !eventKindPattern.MatchString(request.EventKind) || request.Now.Unix() <= 0 {
		return fmt.Errorf("transition Job: invalid version, state, event kind, or time")
	}
	if !json.Valid([]byte(request.PayloadJSON)) {
		return fmt.Errorf("transition Job: invalid event JSON")
	}
	if request.To == job.StateRetryWait {
		if request.RetryAt == nil || request.RetryAt.Unix() <= request.Now.Unix() {
			return fmt.Errorf("transition Job: retry_wait requires a future retry time")
		}
	} else if request.RetryAt != nil {
		return fmt.Errorf("transition Job: retry time is valid only for retry_wait")
	}
	if request.To.Terminal() {
		if request.TerminalSummary == nil || *request.TerminalSummary == "" {
			return fmt.Errorf("transition Job: terminal state requires a summary")
		}
	} else if request.TerminalSummary != nil {
		return fmt.Errorf("transition Job: non-terminal state cannot have a summary")
	}
	values := []string{request.EventKind, request.PayloadJSON}
	if request.TerminalSummary != nil {
		values = append(values, *request.TerminalSummary)
	}
	return validateEncodedValues(values...)
}

func validateControlRequest(request ControlRequest) error {
	if _, err := domain.ParseJobID(string(request.JobID)); err != nil {
		return fmt.Errorf("update Job control: %w", err)
	}
	if _, err := domain.ParseEventID(string(request.EventID)); err != nil {
		return fmt.Errorf("update Job control: %w", err)
	}
	if request.ExpectedVersion <= 0 || request.PauseRequested == nil && request.CancelRequested == nil ||
		!eventKindPattern.MatchString(request.EventKind) || !json.Valid([]byte(request.PayloadJSON)) || request.Now.Unix() <= 0 {
		return errors.New("update Job control: invalid version, flags, event, payload, or time")
	}
	return validateEncodedValues(request.EventKind, request.PayloadJSON)
}

func validateCheckpoint(request CheckpointRequest) error {
	if _, err := domain.ParseJobID(string(request.JobID)); err != nil {
		return fmt.Errorf("save Job checkpoint: %w", err)
	}
	if request.StepIndex < 0 || request.StepIndex >= maxJobSteps || !eventKindPattern.MatchString(request.Phase) ||
		request.Now.Unix() <= 0 || request.VerifiedOffset > math.MaxInt64 {
		return errors.New("save Job checkpoint: invalid step, phase, offset, or time")
	}
	if len(request.ChecksumState) > 64*1024 {
		return errors.New("save Job checkpoint: checksum state exceeds 64 KiB")
	}
	values := []string{request.Phase}
	if request.SourceFingerprint != "" {
		if !json.Valid([]byte(request.SourceFingerprint)) {
			return errors.New("save Job checkpoint: source fingerprint is invalid JSON")
		}
		values = append(values, request.SourceFingerprint)
	}
	if request.PartLocationJSON != "" {
		if !json.Valid([]byte(request.PartLocationJSON)) {
			return errors.New("save Job checkpoint: part location is invalid JSON")
		}
		values = append(values, request.PartLocationJSON)
	}
	if err := validateEncodedValues(values...); err != nil {
		return fmt.Errorf("save Job checkpoint: %w", err)
	}
	return nil
}

func validateEncodedValues(values ...string) error {
	total := 0
	for _, value := range values {
		if len(value) > maxEncodedRowBytes-total {
			return fmt.Errorf("encoded row exceeds %d bytes", maxEncodedRowBytes)
		}
		total += len(value)
	}
	return nil
}

func nullableValue(value *string) any {
	if value == nil {
		return nil
	}
	return *value
}

func nullableTimeUnix(value *time.Time) any {
	if value == nil {
		return nil
	}
	return value.Unix()
}

func nullableString(value string) any {
	if value == "" {
		return nil
	}
	return value
}

func nullableBytes(value []byte) any {
	if len(value) == 0 {
		return nil
	}
	return value
}

func boolInt(value bool) int {
	if value {
		return 1
	}
	return 0
}

func marshalJobResponse(jobID domain.JobID) (string, error) {
	encoded, err := json.Marshal(struct {
		JobID domain.JobID `json:"job_id"`
	}{JobID: jobID})
	if err != nil {
		return "", fmt.Errorf("create Job: encode idempotent response: %w", err)
	}
	return string(encoded), nil
}
