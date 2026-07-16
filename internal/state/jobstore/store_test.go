package jobstore

import (
	"context"
	"database/sql"
	"errors"
	"net/url"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/TyrantLucifer/awesome-mac-sftp/internal/domain"
	"github.com/TyrantLucifer/awesome-mac-sftp/internal/job"
	"github.com/TyrantLucifer/awesome-mac-sftp/internal/state/migration"
	"github.com/TyrantLucifer/awesome-mac-sftp/internal/testkit"
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
	if err := store.CheckpointIdle(ctx); err != nil {
		t.Fatalf("checkpoint idle store: %v", err)
	}
	if size := store.walGuard.Snapshot().WALBytes; size != 0 {
		t.Fatalf("idle checkpoint WAL bytes = %d, want 0", size)
	}
}

func TestCreateSyncBackAtomicallyBindsEditSessionAndSurvivesRestart(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	store, database := newVersion2TestStore(t, ctx)
	seedBoundEditSession(t, ctx, database)
	request := createRequest(t, 13)
	request.DestinationJSON = stringPointer(`{"version":2,"origin":"sync_back","edit_session_id":"44444444444444444444444444444444"}`)
	request.EditSession = &EditSessionBinding{
		SessionID: strings.Repeat("4", 32), ExpectedVersion: 1,
		EventID: "sync-back-bound", EventKind: "sync_back_job_bound",
	}
	created, duplicate, err := store.Create(ctx, request)
	if err != nil || duplicate {
		t.Fatalf("create bound Job = (%#v, %t, %v)", created, duplicate, err)
	}

	restarted, err := New(ctx, database)
	if err != nil {
		t.Fatalf("restart Job store: %v", err)
	}
	reloaded, err := restarted.Get(ctx, created.JobID)
	if err != nil || reloaded != created {
		t.Fatalf("reloaded Job = (%#v, %v), want %#v", reloaded, err, created)
	}
	var boundJob domain.JobID
	if err := database.QueryRowContext(ctx, "SELECT job_id FROM edit_session_jobs WHERE session_id=?", request.EditSession.SessionID).Scan(&boundJob); err != nil || boundJob != created.JobID {
		t.Fatalf("durable binding = (%q, %v)", boundJob, err)
	}
	var state string
	var version int64
	if err := database.QueryRowContext(ctx, "SELECT state,state_version FROM edit_sessions WHERE session_id=?", request.EditSession.SessionID).Scan(&state, &version); err != nil || state != "uploading" || version != 2 {
		t.Fatalf("bound session = (%q, %d, %v)", state, version, err)
	}
}

func TestCreateSyncBackRollsBackPlanAndJobWhenBindingFails(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	store, database := newVersion2TestStore(t, ctx)
	request := createRequest(t, 14)
	request.DestinationJSON = stringPointer(`{"version":2,"origin":"sync_back","edit_session_id":"99999999999999999999999999999999"}`)
	request.EditSession = &EditSessionBinding{
		SessionID: strings.Repeat("9", 32), ExpectedVersion: 1,
		EventID: "sync-back-bound", EventKind: "sync_back_job_bound",
	}
	if _, _, err := store.Create(ctx, request); err == nil {
		t.Fatal("create with missing edit session succeeded")
	}
	for _, table := range []string{"operation_plans", "jobs", "job_steps", "job_events", "request_dedup", "edit_session_jobs"} {
		assertCount(t, ctx, database, table, 0)
	}
}

func TestCreateAndResolveConflictAtomicallyQueuesJob(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	store, database := newTestStore(t, ctx)
	request := createRequest(t, 11)
	request.InitialState = job.StateAwaitingConfirmation
	request.InitialConflict = &ConflictSeed{
		StepIndex: 0, Class: "destination_exists", SourceJSON: `{"path":"/source"}`,
		DestinationJSON: `{"path":"/final"}`,
	}
	created, _, err := store.Create(ctx, request)
	if err != nil {
		t.Fatalf("create conflicting Job: %v", err)
	}
	conflicts, err := store.ListConflicts(ctx, created.JobID)
	if err != nil {
		t.Fatalf("list conflicts: %v", err)
	}
	if len(conflicts) != 1 || conflicts[0].State != "waiting" || conflicts[0].Class != "destination_exists" {
		t.Fatalf("initial conflicts = %#v", conflicts)
	}
	queued, err := store.ResolveConflict(ctx, ResolveConflictRequest{
		JobID: created.JobID, ConflictIndex: 0, ExpectedVersion: created.StateVersion,
		Resolution: "overwrite", ApplyScope: "job", EventID: testEventID(t, 110),
		Now: time.Unix(1_100, 0),
	})
	if err != nil {
		t.Fatalf("resolve conflict: %v", err)
	}
	if queued.State != job.StateQueued || queued.StateVersion != created.StateVersion+1 {
		t.Fatalf("resolved snapshot = %#v", queued)
	}
	conflicts, err = store.ListConflicts(ctx, created.JobID)
	if err != nil {
		t.Fatalf("list resolved conflicts: %v", err)
	}
	if len(conflicts) != 1 || conflicts[0].State != "resolved" || conflicts[0].Resolution != "overwrite" || conflicts[0].ApplyScope != "job" {
		t.Fatalf("resolved conflicts = %#v", conflicts)
	}
	assertCount(t, ctx, database, "job_events", 2)
}

func TestOpenConflictAtomicallyMovesRunningJobToWaiting(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	store, _ := newTestStore(t, ctx)
	created, _, err := store.Create(ctx, createRequest(t, 12))
	if err != nil {
		t.Fatalf("create Job: %v", err)
	}
	running, _, err := store.Transition(ctx, TransitionRequest{
		JobID: created.JobID, ExpectedVersion: created.StateVersion, To: job.StateRunning,
		EventID: testEventID(t, 120), EventKind: "job_started", PayloadJSON: `{}`, Now: time.Unix(1_200, 0),
	})
	if err != nil {
		t.Fatalf("start Job: %v", err)
	}
	waiting, conflict, err := store.OpenConflict(ctx, OpenConflictRequest{
		JobID: running.JobID, ExpectedVersion: running.StateVersion, StepIndex: 0,
		Class: "destination_appeared", SourceJSON: `{"path":"/source"}`, DestinationJSON: `{"path":"/final"}`,
		EventID: testEventID(t, 121), Now: time.Unix(1_201, 0),
	})
	if err != nil {
		t.Fatalf("open conflict: %v", err)
	}
	if waiting.State != job.StateWaitingConflict || conflict.ConflictIndex != 0 || conflict.State != "waiting" {
		t.Fatalf("opened conflict = (%#v, %#v)", waiting, conflict)
	}
	conflicts, err := store.ListConflicts(ctx, created.JobID)
	if err != nil || !reflect.DeepEqual(conflicts, []ConflictRecord{conflict}) {
		t.Fatalf("durable conflicts = (%#v, %v)", conflicts, err)
	}
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

func TestCheckpointUpsertIsDurableBoundedAndJobStepScoped(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	store, database := newTestStore(t, ctx)
	created, _, err := store.Create(ctx, createRequest(t, 4))
	if err != nil {
		t.Fatalf("create job: %v", err)
	}
	request := CheckpointRequest{
		JobID:             created.JobID,
		StepIndex:         0,
		Phase:             "streaming",
		VerifiedOffset:    4096,
		SourceFingerprint: `{"size":8192}`,
		PartLocationJSON:  `{"path":"/target.part"}`,
		ChecksumState:     []byte{1, 2, 3, 4},
		Now:               time.Unix(400, 0),
	}
	if err := store.SaveCheckpoint(ctx, request); err != nil {
		t.Fatalf("save checkpoint: %v", err)
	}
	checkpoint, err := store.GetCheckpoint(ctx, created.JobID, 0)
	if err != nil {
		t.Fatalf("get checkpoint: %v", err)
	}
	if checkpoint.Phase != request.Phase || checkpoint.VerifiedOffset != request.VerifiedOffset ||
		checkpoint.SourceFingerprint != request.SourceFingerprint || checkpoint.PartLocationJSON != request.PartLocationJSON ||
		!reflect.DeepEqual(checkpoint.ChecksumState, request.ChecksumState) {
		t.Fatalf("checkpoint = %#v, want %#v", checkpoint, request)
	}

	request.Phase = "verified"
	request.VerifiedOffset = 8192
	request.ChecksumState[0] = 9
	request.Now = time.Unix(401, 0)
	if err := store.SaveCheckpoint(ctx, request); err != nil {
		t.Fatalf("replace checkpoint: %v", err)
	}
	checkpoint, err = store.GetCheckpoint(ctx, created.JobID, 0)
	if err != nil {
		t.Fatalf("get replaced checkpoint: %v", err)
	}
	if checkpoint.Phase != "verified" || checkpoint.VerifiedOffset != 8192 || checkpoint.UpdatedAt.Unix() != 401 || checkpoint.ChecksumState[0] != 9 {
		t.Fatalf("replaced checkpoint = %#v", checkpoint)
	}
	assertCount(t, ctx, database, "job_checkpoints", 1)
	request.VerifiedOffset = 1
	request.Now = time.Unix(402, 0)
	if err := store.SaveCheckpoint(ctx, request); err == nil {
		t.Fatal("regressing a checkpoint offset succeeded")
	}
	checkpoint, err = store.GetCheckpoint(ctx, created.JobID, 0)
	if err != nil || checkpoint.VerifiedOffset != 8192 {
		t.Fatalf("checkpoint after rejected regression = (%#v, %v)", checkpoint, err)
	}

	request.StepIndex = 99
	if err := store.SaveCheckpoint(ctx, request); err == nil {
		t.Fatal("save checkpoint for unknown step succeeded")
	}
	if _, err := store.GetCheckpoint(ctx, created.JobID, 1); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("missing checkpoint error = %v, want sql.ErrNoRows", err)
	}
}

func TestPlanReloadControlAndEventReplayAreTransactional(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	store, _ := newTestStore(t, ctx)
	request := createRequest(t, 5)
	created, _, err := store.Create(ctx, request)
	if err != nil {
		t.Fatalf("create job: %v", err)
	}
	plan, err := store.GetPlan(ctx, created.JobID)
	if err != nil {
		t.Fatalf("get plan: %v", err)
	}
	if plan.PlanID != request.PlanID || plan.RequestID != request.RequestID || plan.Kind != request.Kind ||
		plan.SourceJSON != request.SourceJSON || plan.DestinationJSON == nil || *plan.DestinationJSON != *request.DestinationJSON ||
		plan.Route != request.Route || plan.Verification != request.Verification || plan.ConflictPolicy != request.ConflictPolicy {
		t.Fatalf("plan = %#v, want request %#v", plan, request)
	}

	pause := true
	controlRequest := ControlRequest{
		JobID:           created.JobID,
		ExpectedVersion: created.StateVersion,
		PauseRequested:  &pause,
		EventID:         testEventID(t, 50),
		EventKind:       "pause_requested",
		PayloadJSON:     `{"source":"test"}`,
		Now:             time.Unix(500, 0),
	}
	controlled, duplicate, err := store.UpdateControl(ctx, controlRequest)
	if err != nil {
		t.Fatalf("update control: %v", err)
	}
	if duplicate || !controlled.PauseRequested || controlled.CancelRequested || controlled.StateVersion != 2 || controlled.NextEventSequence != 3 {
		t.Fatalf("controlled snapshot = (%#v, %t)", controlled, duplicate)
	}
	repeated, duplicate, err := store.UpdateControl(ctx, controlRequest)
	if err != nil || !duplicate || repeated.StateVersion != controlled.StateVersion {
		t.Fatalf("repeat control = (%#v, %t, %v)", repeated, duplicate, err)
	}

	events, err := store.ListEvents(ctx, created.JobID, 0, 10)
	if err != nil {
		t.Fatalf("list events: %v", err)
	}
	if len(events) != 2 || events[0].Sequence != 1 || events[0].Kind != "job_created" || events[1].Sequence != 2 || events[1].Kind != "pause_requested" {
		t.Fatalf("events = %#v", events)
	}
	replayed, err := store.ListEvents(ctx, created.JobID, 1, 10)
	if err != nil || len(replayed) != 1 || replayed[0].EventID != controlRequest.EventID {
		t.Fatalf("replayed events = (%#v, %v)", replayed, err)
	}
	jobs, err := store.List(ctx, 10)
	if err != nil || len(jobs) != 1 || jobs[0].JobID != created.JobID || !jobs[0].PauseRequested {
		t.Fatalf("jobs = (%#v, %v)", jobs, err)
	}
}

func newTestStore(t *testing.T, ctx context.Context) (*Store, *sql.DB) {
	t.Helper()
	root := testkit.PersistentTempDir(t)
	if err := os.Chmod(root, 0o700); err != nil { //nolint:gosec // directory requires owner traversal
		t.Fatalf("set private state root: %v", err)
	}
	path := filepath.Join(root, "state.sqlite3")
	uri := &url.URL{Scheme: "file", Path: path, RawQuery: "_pragma=" + url.QueryEscape("wal_autocheckpoint(1000)")}
	database, err := sql.Open("sqlite", uri.String())
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
	if err := os.Chmod(path, 0o600); err != nil {
		_ = connection.Close()
		t.Fatalf("set private state database: %v", err)
	}
	var journalMode string
	if err := connection.QueryRowContext(ctx, "PRAGMA journal_mode=WAL").Scan(&journalMode); err != nil || journalMode != "wal" {
		_ = connection.Close()
		t.Fatalf("enable WAL: mode=%q error=%v", journalMode, err)
	}
	if err := connection.Close(); err != nil {
		t.Fatalf("close migration connection: %v", err)
	}
	store, err := New(ctx, database)
	if err != nil {
		t.Fatalf("new guarded store: %v", err)
	}
	return store, database
}

func newVersion2TestStore(t *testing.T, ctx context.Context) (*Store, *sql.DB) {
	t.Helper()
	store, database := newTestStore(t, ctx)
	for _, item := range []migration.Migration{migration.Version2(), migration.Version3()} {
		for _, statement := range item.Statements {
			if _, err := database.ExecContext(ctx, statement); err != nil {
				t.Fatalf("apply Version %d statement: %v", item.Version, err)
			}
		}
	}
	return store, database
}

func seedBoundEditSession(t *testing.T, ctx context.Context, database *sql.DB) {
	t.Helper()
	created := int64(1)
	blobID := strings.Repeat("1", 64)
	entryID := strings.Repeat("2", 64)
	materializationID := strings.Repeat("3", 32)
	if _, err := database.ExecContext(ctx, "INSERT INTO cache_blobs(sha256,size_bytes,basename,state,created_at_unix,last_access_unix) VALUES(?,7,?,'published',?,?)", blobID, "blobs/sha256/11/"+blobID+".blob", created, created); err != nil {
		t.Fatal(err)
	}
	if _, err := database.ExecContext(ctx, "INSERT INTO cache_entries(entry_id,endpoint_id,path_bytes,fingerprint_strength,freshness,policy,pinned,blob_sha256,complete,created_at_unix,last_access_unix) VALUES(?, 'remote', X'2f66696c65', 'strong', 'fresh', 'lru', 0, ?, 1, ?, ?)", entryID, blobID, created, created); err != nil {
		t.Fatal(err)
	}
	if _, err := database.ExecContext(ctx, "INSERT INTO cache_materializations(materialization_id,entry_id,baseline_blob_sha256,basename,size_bytes,current_sha256,state,pinned,created_at_unix,updated_at_unix,last_access_unix) VALUES(?,?,?,?,7,?,'dirty',0,?,?,?)", materializationID, entryID, blobID, "materializations/"+materializationID+"/content", blobID, created, created, created); err != nil {
		t.Fatal(err)
	}
	if _, err := database.ExecContext(ctx, "INSERT INTO edit_sessions(session_id,source_entry_id,materialization_id,local_state,remote_state,state,state_version,created_at_unix,updated_at_unix) VALUES(?,?,?,'dirty','unchanged','awaiting_decision',1,?,?)", strings.Repeat("4", 32), entryID, materializationID, created, created); err != nil {
		t.Fatal(err)
	}
	if _, err := database.ExecContext(ctx, "INSERT INTO edit_session_details(session_id,purpose,reference_id,lease_id,machine_json,target_endpoint_id,target_path_bytes,baseline_local_sha256,original_presence,original_kind,original_fingerprint_json,expected_presence,expected_kind,expected_fingerprint_json,created_at_unix,updated_at_unix) VALUES(?,'editor',?,?,X'7b7d','remote',X'2f66696c65',?,'present','file',X'7b7d','present','file',X'7b7d',?,?)", strings.Repeat("4", 32), strings.Repeat("5", 32), strings.Repeat("6", 32), blobID, created, created); err != nil {
		t.Fatal(err)
	}
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
