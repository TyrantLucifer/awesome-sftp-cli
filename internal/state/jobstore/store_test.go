package jobstore

import (
	"context"
	"database/sql"
	"errors"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/TyrantLucifer/awesome-mac-sftp/internal/domain"
	"github.com/TyrantLucifer/awesome-mac-sftp/internal/job"
	"github.com/TyrantLucifer/awesome-mac-sftp/internal/state/migration"
	_ "modernc.org/sqlite"
)

func TestCreateJobIsTransactionalAndRequestIdempotent(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	store, database := newTestStore(t, ctx)
	request := createRequest(t, 1)
	created, duplicate, err := store.Create(ctx, request)
	if err != nil {
		t.Fatalf("create job: %v", err)
	}
	if duplicate {
		t.Fatal("first create reported duplicate")
	}
	if created.State != job.StateQueued || created.StateVersion != 1 || created.NextEventSequence != 2 {
		t.Fatalf("created snapshot = %#v", created)
	}

	other := createRequest(t, 9)
	other.RequestID = request.RequestID
	duplicateJob, duplicate, err := store.Create(ctx, other)
	if err != nil {
		t.Fatalf("repeat create: %v", err)
	}
	if !duplicate || duplicateJob.JobID != created.JobID {
		t.Fatalf("repeat create = (%#v, %t), want original duplicate", duplicateJob, duplicate)
	}

	assertCount(t, ctx, database, "operation_plans", 1)
	assertCount(t, ctx, database, "jobs", 1)
	assertCount(t, ctx, database, "job_steps", 2)
	assertCount(t, ctx, database, "job_events", 1)
	assertCount(t, ctx, database, "request_dedup", 1)
}

func TestTransitionIsTransactionalMonotonicAndRejectsIllegalState(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	store, database := newTestStore(t, ctx)
	created, _, err := store.Create(ctx, createRequest(t, 2))
	if err != nil {
		t.Fatalf("create job: %v", err)
	}
	eventID := testEventID(t, 20)
	running, duplicate, err := store.Transition(ctx, TransitionRequest{
		JobID:           created.JobID,
		ExpectedVersion: 1,
		To:              job.StateRunning,
		EventID:         eventID,
		EventKind:       "job_started",
		PayloadJSON:     `{"source":"scheduler"}`,
		Now:             time.Unix(200, 0),
	})
	if err != nil {
		t.Fatalf("transition to running: %v", err)
	}
	if duplicate || running.StateVersion != 2 || running.NextEventSequence != 3 {
		t.Fatalf("running transition = (%#v, %t)", running, duplicate)
	}

	repeated, duplicate, err := store.Transition(ctx, TransitionRequest{
		JobID:           created.JobID,
		ExpectedVersion: 1,
		To:              job.StateRunning,
		EventID:         eventID,
		EventKind:       "job_started",
		PayloadJSON:     `{"source":"scheduler"}`,
		Now:             time.Unix(200, 0),
	})
	if err != nil {
		t.Fatalf("repeat transition: %v", err)
	}
	if !duplicate || repeated.StateVersion != 2 {
		t.Fatalf("repeat transition = (%#v, %t), want version 2 duplicate", repeated, duplicate)
	}

	_, _, err = store.Transition(ctx, TransitionRequest{
		JobID:           created.JobID,
		ExpectedVersion: 2,
		To:              job.StateCompleted,
		EventID:         testEventID(t, 21),
		EventKind:       "job_completed",
		PayloadJSON:     `{}`,
		TerminalSummary: stringPointer("not allowed from running"),
		Now:             time.Unix(201, 0),
	})
	if !errors.Is(err, ErrInvalidTransition) {
		t.Fatalf("illegal transition error = %v, want ErrInvalidTransition", err)
	}
	assertCount(t, ctx, database, "job_events", 2)
}

func TestRecoverInterruptedJobsPausesEffectsExactlyOnce(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	store, database := newTestStore(t, ctx)
	created, _, err := store.Create(ctx, createRequest(t, 3))
	if err != nil {
		t.Fatalf("create job: %v", err)
	}
	_, _, err = store.Transition(ctx, TransitionRequest{
		JobID: created.JobID, ExpectedVersion: 1, To: job.StateRunning,
		EventID: testEventID(t, 30), EventKind: "job_started", PayloadJSON: `{}`, Now: time.Unix(300, 0),
	})
	if err != nil {
		t.Fatalf("transition to running: %v", err)
	}
	if _, err := database.ExecContext(ctx, "UPDATE job_steps SET state='running' WHERE job_id=? AND step_index=0", created.JobID); err != nil {
		t.Fatalf("mark step running: %v", err)
	}

	generator := &domain.RandomGenerator{Reader: strings.NewReader(strings.Repeat("r", 32))}
	recovered, err := store.RecoverInterrupted(ctx, generator, time.Unix(301, 0))
	if err != nil {
		t.Fatalf("recover interrupted jobs: %v", err)
	}
	if recovered != 1 {
		t.Fatalf("recovered jobs = %d, want 1", recovered)
	}
	snapshot, err := store.Get(ctx, created.JobID)
	if err != nil {
		t.Fatalf("get recovered job: %v", err)
	}
	if snapshot.State != job.StatePaused || snapshot.StateVersion != 3 || snapshot.NextEventSequence != 4 {
		t.Fatalf("recovered snapshot = %#v", snapshot)
	}
	var stepState string
	if err := database.QueryRowContext(ctx, "SELECT state FROM job_steps WHERE job_id=? AND step_index=0", created.JobID).Scan(&stepState); err != nil {
		t.Fatalf("read recovered step: %v", err)
	}
	if stepState != "paused" {
		t.Fatalf("recovered step state = %q, want paused", stepState)
	}
	recovered, err = store.RecoverInterrupted(ctx, generator, time.Unix(302, 0))
	if err != nil {
		t.Fatalf("repeat recovery: %v", err)
	}
	if recovered != 0 {
		t.Fatalf("repeat recovered jobs = %d, want 0", recovered)
	}
	assertCount(t, ctx, database, "job_events", 3)
}

func newTestStore(t *testing.T, ctx context.Context) (*Store, *sql.DB) {
	t.Helper()
	database, err := sql.Open("sqlite", "file:"+filepath.Join(t.TempDir(), "state.sqlite3"))
	if err != nil {
		t.Fatalf("open database: %v", err)
	}
	database.SetMaxOpenConns(4)
	t.Cleanup(func() {
		if err := database.Close(); err != nil {
			t.Errorf("close database: %v", err)
		}
	})
	connection, err := database.Conn(ctx)
	if err != nil {
		t.Fatalf("reserve migration connection: %v", err)
	}
	if err := (migration.Runner{}).Apply(ctx, connection, migration.Version1(), "2026-07-16T00:00:00Z"); err != nil {
		_ = connection.Close()
		t.Fatalf("apply version 1: %v", err)
	}
	if err := connection.Close(); err != nil {
		t.Fatalf("close migration connection: %v", err)
	}
	return New(database), database
}

func createRequest(t *testing.T, seed byte) CreateRequest {
	t.Helper()
	generator := &domain.RandomGenerator{Reader: strings.NewReader(strings.Repeat(string([]byte{seed}), 64))}
	requestID, err := domain.NewRequestID(generator)
	if err != nil {
		t.Fatalf("new request ID: %v", err)
	}
	jobID, err := domain.NewJobID(generator)
	if err != nil {
		t.Fatalf("new job ID: %v", err)
	}
	eventID, err := domain.NewEventID(generator)
	if err != nil {
		t.Fatalf("new event ID: %v", err)
	}
	return CreateRequest{
		PlanID: "plan_" + string(jobID)[4:], RequestID: requestID, JobID: jobID,
		Kind: "copy", SourceJSON: `{"path":"/source"}`, DestinationJSON: stringPointer(`{"path":"/destination"}`),
		Route: "local_relay", Verification: "size", ConflictPolicy: "ask", RiskClass: "ordinary",
		InitialState: job.StateQueued, EventID: eventID, Now: time.Unix(int64(seed)+100, 0),
		Steps: []Step{{Kind: "copy", SourceJSON: stringPointer(`{"path":"/source"}`), DestinationJSON: stringPointer(`{"path":"/destination.part"}`)}, {Kind: "verify"}},
	}
}

func testEventID(t *testing.T, seed byte) domain.EventID {
	t.Helper()
	generator := &domain.RandomGenerator{Reader: strings.NewReader(strings.Repeat(string([]byte{seed}), 16))}
	eventID, err := domain.NewEventID(generator)
	if err != nil {
		t.Fatalf("new event ID: %v", err)
	}
	return eventID
}

func assertCount(t *testing.T, ctx context.Context, database *sql.DB, table string, want int) {
	t.Helper()
	var got int
	if err := database.QueryRowContext(ctx, "SELECT count(*) FROM "+table).Scan(&got); err != nil { //nolint:gosec // fixed test table names
		t.Fatalf("count %s: %v", table, err)
	}
	if got != want {
		t.Fatalf("%s count = %d, want %d", table, got, want)
	}
}

func stringPointer(value string) *string { return &value }
