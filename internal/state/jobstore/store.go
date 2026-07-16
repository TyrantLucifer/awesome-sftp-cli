// Package jobstore persists immutable plans, Jobs, steps, and ordered events.
package jobstore

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"time"

	"github.com/TyrantLucifer/awesome-mac-sftp/internal/domain"
	"github.com/TyrantLucifer/awesome-mac-sftp/internal/job"
)

const maxEncodedRowBytes = 256 * 1024

var (
	ErrInvalidTransition = errors.New("invalid Job transition")
	ErrVersionConflict   = errors.New("job state version conflict")
	planIDPattern        = regexp.MustCompile(`^plan_[a-z2-7]{26}$`)
	eventKindPattern     = regexp.MustCompile(`^[a-z][a-z0-9_]{0,63}$`)
)

type Store struct {
	database *sql.DB
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

func New(database *sql.DB) *Store {
	return &Store{database: database}
}

func (store *Store) Create(ctx context.Context, request CreateRequest) (Snapshot, bool, error) {
	if err := validateCreate(request); err != nil {
		return Snapshot{}, false, err
	}
	var snapshot Snapshot
	duplicate := false
	err := store.immediate(ctx, func(connection *sql.Conn) error {
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
		if _, err := connection.ExecContext(ctx, "INSERT INTO operation_plans(plan_id, request_id, kind, source_json, destination_json, route, verification, conflict_policy, risk_class, frozen_at_unix) VALUES(?, ?, ?, ?, ?, ?, ?, ?, ?, ?)", request.PlanID, request.RequestID, request.Kind, request.SourceJSON, destination, request.Route, request.Verification, request.ConflictPolicy, request.RiskClass, request.Now.Unix()); err != nil {
			return fmt.Errorf("create Job plan: %w", err)
		}
		if _, err := connection.ExecContext(ctx, "INSERT INTO jobs(job_id, plan_id, state, state_version, next_event_sequence, pause_requested, cancel_requested, retry_at_unix, created_at_unix, updated_at_unix, terminal_summary) VALUES(?, ?, ?, 1, 2, 0, 0, NULL, ?, ?, NULL)", request.JobID, request.PlanID, request.InitialState, request.Now.Unix(), request.Now.Unix()); err != nil {
			return fmt.Errorf("create Job row: %w", err)
		}
		for index, step := range request.Steps {
			if _, err := connection.ExecContext(ctx, "INSERT INTO job_steps(job_id, step_index, kind, state, attempt, source_json, destination_json, created_at_unix, updated_at_unix) VALUES(?, ?, ?, 'pending', 0, ?, ?, ?, ?)", request.JobID, index, step.Kind, nullableValue(step.SourceJSON), nullableValue(step.DestinationJSON), request.Now.Unix(), request.Now.Unix()); err != nil {
				return fmt.Errorf("create Job step %d: %w", index, err)
			}
		}
		if _, err := connection.ExecContext(ctx, "INSERT INTO job_events(job_id, sequence, event_id, kind, payload_json, created_at_unix) VALUES(?, 1, ?, 'job_created', '{}', ?)", request.JobID, request.EventID, request.Now.Unix()); err != nil {
			return fmt.Errorf("create Job event: %w", err)
		}
		response, err = marshalJobResponse(request.JobID)
		if err != nil {
			return err
		}
		if _, err := connection.ExecContext(ctx, "INSERT INTO request_dedup(request_id, operation, job_id, response_json, created_at_unix) VALUES(?, 'create_job', ?, ?, ?)", request.RequestID, request.JobID, response, request.Now.Unix()); err != nil {
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

func (store *Store) Transition(ctx context.Context, request TransitionRequest) (Snapshot, bool, error) {
	if err := validateTransitionRequest(request); err != nil {
		return Snapshot{}, false, err
	}
	var snapshot Snapshot
	duplicate := false
	err := store.immediate(ctx, func(connection *sql.Conn) error {
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
		result, err := connection.ExecContext(ctx, "UPDATE jobs SET state=?, state_version=state_version+1, next_event_sequence=next_event_sequence+1, retry_at_unix=?, terminal_summary=?, updated_at_unix=? WHERE job_id=? AND state_version=?", request.To, retryAt, terminalSummary, request.Now.Unix(), request.JobID, request.ExpectedVersion)
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
		if _, err := connection.ExecContext(ctx, "INSERT INTO job_events(job_id, sequence, event_id, kind, payload_json, created_at_unix) VALUES(?, ?, ?, ?, ?, ?)", request.JobID, current.NextEventSequence, request.EventID, request.EventKind, request.PayloadJSON, request.Now.Unix()); err != nil {
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
	return getSnapshot(ctx, store.database, jobID)
}

func (store *Store) RecoverInterrupted(ctx context.Context, generator domain.Generator, now time.Time) (int, error) {
	if generator == nil {
		return 0, fmt.Errorf("recover Jobs: nil ID generator")
	}
	if now.Unix() <= 0 {
		return 0, fmt.Errorf("recover Jobs: invalid time")
	}
	recovered := 0
	err := store.immediate(ctx, func(connection *sql.Conn) error {
		rows, err := connection.QueryContext(ctx, "SELECT job_id, state, state_version, next_event_sequence FROM jobs WHERE state IN ('running', 'verifying') ORDER BY job_id")
		if err != nil {
			return fmt.Errorf("recover Jobs: list interrupted: %w", err)
		}
		type interrupted struct {
			jobID        domain.JobID
			state        job.State
			stateVersion int64
			nextSequence int64
		}
		var jobs []interrupted
		for rows.Next() {
			var candidate interrupted
			if err := rows.Scan(&candidate.jobID, &candidate.state, &candidate.stateVersion, &candidate.nextSequence); err != nil {
				_ = rows.Close()
				return fmt.Errorf("recover Jobs: scan interrupted: %w", err)
			}
			jobs = append(jobs, candidate)
		}
		if err := errors.Join(rows.Err(), rows.Close()); err != nil {
			return fmt.Errorf("recover Jobs: finish interrupted list: %w", err)
		}
		for _, candidate := range jobs {
			next, changed := job.ConservativeRestartState(candidate.state)
			if !changed {
				continue
			}
			eventID, err := domain.NewEventID(generator)
			if err != nil {
				return fmt.Errorf("recover Job %q event ID: %w", candidate.jobID, err)
			}
			result, err := connection.ExecContext(ctx, "UPDATE jobs SET state=?, state_version=state_version+1, next_event_sequence=next_event_sequence+1, updated_at_unix=? WHERE job_id=? AND state_version=? AND state=?", next, now.Unix(), candidate.jobID, candidate.stateVersion, candidate.state)
			if err != nil {
				return fmt.Errorf("recover Job %q: %w", candidate.jobID, err)
			}
			changedRows, err := result.RowsAffected()
			if err != nil || changedRows != 1 {
				return fmt.Errorf("recover Job %q: concurrent state change", candidate.jobID)
			}
			if _, err := connection.ExecContext(ctx, "UPDATE job_steps SET state='paused', updated_at_unix=? WHERE job_id=? AND state IN ('running', 'verifying')", now.Unix(), candidate.jobID); err != nil {
				return fmt.Errorf("recover Job %q steps: %w", candidate.jobID, err)
			}
			if _, err := connection.ExecContext(ctx, "INSERT INTO job_events(job_id, sequence, event_id, kind, payload_json, created_at_unix) VALUES(?, ?, ?, 'job_recovered', '{\"reason\":\"daemon_restart\"}', ?)", candidate.jobID, candidate.nextSequence, eventID, now.Unix()); err != nil {
				return fmt.Errorf("recover Job %q event: %w", candidate.jobID, err)
			}
			recovered++
		}
		return nil
	})
	if err != nil {
		return 0, err
	}
	return recovered, nil
}

type rowQueryer interface {
	QueryRowContext(context.Context, string, ...any) *sql.Row
}

func getSnapshot(ctx context.Context, queryer rowQueryer, jobID domain.JobID) (Snapshot, error) {
	var snapshot Snapshot
	var pauseRequested, cancelRequested int64
	var retryAt sql.NullInt64
	var terminalSummary sql.NullString
	var createdAt, updatedAt int64
	err := queryer.QueryRowContext(ctx, "SELECT job_id, plan_id, state, state_version, next_event_sequence, pause_requested, cancel_requested, retry_at_unix, terminal_summary, created_at_unix, updated_at_unix FROM jobs WHERE job_id=?", jobID).Scan(
		&snapshot.JobID, &snapshot.PlanID, &snapshot.State, &snapshot.StateVersion, &snapshot.NextEventSequence,
		&pauseRequested, &cancelRequested, &retryAt, &terminalSummary, &createdAt, &updatedAt,
	)
	if err != nil {
		return Snapshot{}, fmt.Errorf("get Job %q: %w", jobID, err)
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

func (store *Store) immediate(ctx context.Context, operation func(*sql.Conn) error) error {
	if store == nil || store.database == nil {
		return fmt.Errorf("job store: nil database")
	}
	connection, err := store.database.Conn(ctx)
	if err != nil {
		return fmt.Errorf("job store: reserve connection: %w", err)
	}
	defer connection.Close()
	if _, err := connection.ExecContext(ctx, "BEGIN IMMEDIATE"); err != nil {
		return fmt.Errorf("job store: begin immediate: %w", err)
	}
	committed := false
	defer func() {
		if !committed {
			_, _ = connection.ExecContext(context.Background(), "ROLLBACK")
		}
	}()
	if err := operation(connection); err != nil {
		return err
	}
	if _, err := connection.ExecContext(ctx, "COMMIT"); err != nil {
		return fmt.Errorf("job store: commit: %w", err)
	}
	committed = true
	return nil
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
	if request.Now.Unix() <= 0 || len(request.Steps) == 0 {
		return fmt.Errorf("create Job: invalid time or empty steps")
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
	return nil
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

func marshalJobResponse(jobID domain.JobID) (string, error) {
	encoded, err := json.Marshal(struct {
		JobID domain.JobID `json:"job_id"`
	}{JobID: jobID})
	if err != nil {
		return "", fmt.Errorf("create Job: encode idempotent response: %w", err)
	}
	return string(encoded), nil
}
