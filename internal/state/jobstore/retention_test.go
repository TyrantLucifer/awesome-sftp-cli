package jobstore

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/TyrantLucifer/awesome-mac-sftp/internal/job"
	"github.com/TyrantLucifer/awesome-mac-sftp/internal/state/migration"
)

func TestHistoryRetentionKeepsNewestRecentAndNonterminalJobs(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	store, database := newVersion4TestStore(t, ctx)
	now := time.Unix(2_000_000, 0).UTC()
	requests := make([]CreateRequest, 5)
	for index := range requests {
		requests[index] = createRequest(t, byte(index+1))
		if _, _, err := store.Create(ctx, requests[index]); err != nil {
			t.Fatal(err)
		}
	}
	terminalizeJob(t, ctx, database, requests[0].JobID, now.Add(-100*time.Hour))
	terminalizeJob(t, ctx, database, requests[1].JobID, now.Add(-90*time.Hour))
	terminalizeJob(t, ctx, database, requests[2].JobID, now.Add(-80*time.Hour))
	terminalizeJob(t, ctx, database, requests[3].JobID, now.Add(-time.Hour))

	result, err := store.reconcileHistory(ctx, now, historyRetentionPolicy{
		keepNewestTerminal: 2,
		minimumTerminalAge: 24 * time.Hour,
		rowBatchSize:       2,
		maxTransactions:    64,
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.DeletedJobs != 2 || result.More {
		t.Fatalf("retention result = %#v, want two complete deletions", result)
	}
	assertJobPresence(t, ctx, database, requests[0].JobID, false)
	assertJobPresence(t, ctx, database, requests[1].JobID, false)
	assertJobPresence(t, ctx, database, requests[2].JobID, true)
	assertJobPresence(t, ctx, database, requests[3].JobID, true)
	assertJobPresence(t, ctx, database, requests[4].JobID, true)
	assertCount(t, ctx, database, "job_history_retention", 0)
	assertCount(t, ctx, database, "operation_plans", 3)
	assertCount(t, ctx, database, "request_dedup", 3)
}

func TestHistoryRetentionProtectsRecoveryAndAuditReferences(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	store, database := newVersion4TestStore(t, ctx)
	now := time.Unix(3_000_000, 0).UTC()
	plain := createRequest(t, 31)
	bound := createRequest(t, 32)
	referenced := createRequest(t, 33)
	leased := createRequest(t, 34)
	released := createRequest(t, 35)
	for _, request := range []CreateRequest{plain, bound, referenced, leased, released} {
		if _, _, err := store.Create(ctx, request); err != nil {
			t.Fatal(err)
		}
		terminalizeJob(t, ctx, database, request.JobID, now.Add(-100*time.Hour))
	}
	seedRetentionCacheGraph(t, ctx, database)
	if _, err := database.ExecContext(ctx, "INSERT INTO edit_session_jobs(session_id,job_id,created_at_unix) VALUES(?,?,1)", strings.Repeat("4", 32), bound.JobID); err != nil {
		t.Fatal(err)
	}
	if _, err := database.ExecContext(ctx, "INSERT INTO cache_references(reference_id,owner_kind,owner_id,materialization_id,created_at_unix) VALUES(?,'upload',?,?,1)", strings.Repeat("7", 32), referenced.JobID, strings.Repeat("3", 32)); err != nil {
		t.Fatal(err)
	}
	insertRetentionLease(t, ctx, database, strings.Repeat("8", 32), leased.JobID, "uncertain")
	insertRetentionLease(t, ctx, database, strings.Repeat("9", 32), released.JobID, "released")

	result, err := store.reconcileHistory(ctx, now, historyRetentionPolicy{
		keepNewestTerminal: 0,
		minimumTerminalAge: time.Hour,
		rowBatchSize:       2,
		maxTransactions:    64,
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.DeletedJobs != 2 || result.More {
		t.Fatalf("retention result = %#v, want plain and released-lease deletion", result)
	}
	assertJobPresence(t, ctx, database, plain.JobID, false)
	assertJobPresence(t, ctx, database, bound.JobID, true)
	assertJobPresence(t, ctx, database, referenced.JobID, true)
	assertJobPresence(t, ctx, database, leased.JobID, true)
	assertJobPresence(t, ctx, database, released.JobID, false)
}

func TestHistoryRetentionResumesExactClaimAfterStoreRestart(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	store, database := newVersion4TestStore(t, ctx)
	now := time.Unix(4_000_000, 0).UTC()
	request := createRequest(t, 41)
	other := createRequest(t, 42)
	for _, item := range []CreateRequest{request, other} {
		if _, _, err := store.Create(ctx, item); err != nil {
			t.Fatal(err)
		}
		terminalizeJob(t, ctx, database, item.JobID, now.Add(-100*time.Hour))
	}
	for sequence := int64(2); sequence <= 19; sequence++ {
		if _, err := database.ExecContext(ctx, "INSERT INTO job_events(job_id,sequence,event_id,kind,payload_json,created_at_unix) VALUES(?,?,?, 'retention_fixture','{}',?)", request.JobID, sequence, fmt.Sprintf("evt_%032x", sequence), sequence); err != nil {
			t.Fatal(err)
		}
	}
	policy := historyRetentionPolicy{keepNewestTerminal: 1, minimumTerminalAge: time.Hour, rowBatchSize: 2, maxTransactions: 2}
	first, err := store.reconcileHistory(ctx, now, policy)
	if err != nil {
		t.Fatal(err)
	}
	if !first.More || first.DeletedJobs != 0 {
		t.Fatalf("first bounded retention pass = %#v", first)
	}
	assertCount(t, ctx, database, "job_history_retention", 1)
	assertJobPresence(t, ctx, database, request.JobID, true)

	restarted, err := New(ctx, database)
	if err != nil {
		t.Fatal(err)
	}
	policy.maxTransactions = 64
	second, err := restarted.reconcileHistory(ctx, now, policy)
	if err != nil {
		t.Fatal(err)
	}
	if second.More || second.DeletedJobs != 1 {
		t.Fatalf("resumed retention pass = %#v", second)
	}
	assertJobPresence(t, ctx, database, request.JobID, false)
	assertJobPresence(t, ctx, database, other.JobID, true)
	assertCount(t, ctx, database, "job_history_retention", 0)
}

func TestHistoryRetentionFailsClosedIfClaimGainsUnmanagedReference(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	store, database := newVersion4TestStore(t, ctx)
	now := time.Unix(5_000_000, 0).UTC()
	request := createRequest(t, 51)
	if _, _, err := store.Create(ctx, request); err != nil {
		t.Fatal(err)
	}
	terminalizeJob(t, ctx, database, request.JobID, now.Add(-100*time.Hour))
	seedRetentionCacheGraph(t, ctx, database)
	policy := historyRetentionPolicy{keepNewestTerminal: 0, minimumTerminalAge: time.Hour, rowBatchSize: 1, maxTransactions: 1}
	if first, err := store.reconcileHistory(ctx, now, policy); err != nil || !first.More {
		t.Fatalf("claim retention candidate = (%#v, %v)", first, err)
	}
	if _, err := database.ExecContext(ctx, "INSERT INTO cache_references(reference_id,owner_kind,owner_id,materialization_id,created_at_unix) VALUES(?,'upload',?,?,1)", strings.Repeat("a", 32), request.JobID, strings.Repeat("3", 32)); err != nil {
		t.Fatal(err)
	}
	var before int
	if err := database.QueryRowContext(ctx, "SELECT count(*) FROM job_events WHERE job_id=?", request.JobID).Scan(&before); err != nil {
		t.Fatal(err)
	}
	policy.maxTransactions = 8
	if _, err := store.reconcileHistory(ctx, now, policy); err == nil || !strings.Contains(err.Error(), "reference") {
		t.Fatalf("retention after unmanaged reference error = %v", err)
	}
	var after int
	if err := database.QueryRowContext(ctx, "SELECT count(*) FROM job_events WHERE job_id=?", request.JobID).Scan(&after); err != nil {
		t.Fatal(err)
	}
	if before != after {
		t.Fatalf("fail-closed retention changed events from %d to %d", before, after)
	}
	assertJobPresence(t, ctx, database, request.JobID, true)
	assertCount(t, ctx, database, "job_history_retention", 1)
}

func newVersion4TestStore(t *testing.T, ctx context.Context) (*Store, *sql.DB) {
	t.Helper()
	store, database := newTestStore(t, ctx)
	for _, item := range []migration.Migration{migration.Version2(), migration.Version3(), migration.Version4()} {
		for _, statement := range item.Statements {
			if _, err := database.ExecContext(ctx, statement); err != nil {
				t.Fatalf("apply Version %d statement: %v", item.Version, err)
			}
		}
	}
	return store, database
}

func terminalizeJob(t *testing.T, ctx context.Context, database *sql.DB, jobID any, updated time.Time) {
	t.Helper()
	result, err := database.ExecContext(ctx, "UPDATE jobs SET state=?,terminal_summary='retained summary',updated_at_unix=? WHERE job_id=?", job.StateCompleted, updated.Unix(), jobID)
	if err != nil {
		t.Fatal(err)
	}
	if changed, err := result.RowsAffected(); err != nil || changed != 1 {
		t.Fatalf("terminalize Job rows = %d, error = %v", changed, err)
	}
}

func assertJobPresence(t *testing.T, ctx context.Context, database *sql.DB, jobID any, want bool) {
	t.Helper()
	var count int
	if err := database.QueryRowContext(ctx, "SELECT count(*) FROM jobs WHERE job_id=?", jobID).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if (count == 1) != want {
		t.Fatalf("Job %v presence = %d, want %t", jobID, count, want)
	}
}

func seedRetentionCacheGraph(t *testing.T, ctx context.Context, database *sql.DB) {
	t.Helper()
	seedBoundEditSession(t, ctx, database)
}

func insertRetentionLease(t *testing.T, ctx context.Context, database *sql.DB, leaseID string, ownerID any, state string) {
	t.Helper()
	_, err := database.ExecContext(ctx, "INSERT INTO cache_leases(lease_id,materialization_id,owner_kind,owner_id,daemon_instance_id,heartbeat_at_unix,expires_at_unix,grace_until_unix,state) VALUES(?,?,'upload',?,?,1,2,3,?)", leaseID, strings.Repeat("3", 32), ownerID, strings.Repeat("b", 32), state)
	if err != nil {
		t.Fatal(err)
	}
}
