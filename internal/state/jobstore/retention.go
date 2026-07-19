package jobstore

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/TyrantLucifer/awesome-mac-sftp/internal/domain"
)

const (
	HistoryRetentionPolicyVersion = 1
	HistoryRetentionKeepNewest    = 1000
	HistoryRetentionMinimumAge    = 30 * 24 * time.Hour

	historyRetentionRowBatch        = 8
	historyRetentionMaxTransactions = 64
	historyRetentionChunkWALBudget  = uint64(4 * 1024 * 1024)
	historyRetentionFinalWALBudget  = uint64(1024 * 1024)
)

type HistoryRetentionResult struct {
	ClaimedJobs int
	DeletedJobs int
	DeletedRows int
	More        bool
}

type historyRetentionPolicy struct {
	keepNewestTerminal int
	minimumTerminalAge time.Duration
	rowBatchSize       int
	maxTransactions    int
}

type historyRetentionStep struct {
	claimed     bool
	deletedJob  bool
	deletedRows int
	done        bool
}

type historyChildTable struct {
	name    string
	key     string
	orderBy string
}

var historyChildTables = []historyChildTable{
	{name: "job_checkpoints", key: "step_index", orderBy: "step_index"},
	{name: "job_conflicts", key: "conflict_index", orderBy: "conflict_index"},
	{name: "job_events", key: "sequence", orderBy: "sequence"},
	{name: "job_results", key: "result_index", orderBy: "result_index"},
	{name: "job_steps", key: "step_index", orderBy: "step_index"},
	{name: "request_dedup", key: "request_id", orderBy: "request_id"},
}

// ReconcileHistory applies the fixed 1.0 durable Job-history policy. It keeps
// the newest 1000 terminal Jobs and every terminal Job younger than 30 days,
// never touches nonterminal or referenced Jobs, and performs at most 64
// bounded WAL transactions per call.
func (store *Store) ReconcileHistory(ctx context.Context, now time.Time) (HistoryRetentionResult, error) {
	return store.reconcileHistory(ctx, now, historyRetentionPolicy{
		keepNewestTerminal: HistoryRetentionKeepNewest,
		minimumTerminalAge: HistoryRetentionMinimumAge,
		rowBatchSize:       historyRetentionRowBatch,
		maxTransactions:    historyRetentionMaxTransactions,
	})
}

func (store *Store) reconcileHistory(ctx context.Context, now time.Time, policy historyRetentionPolicy) (HistoryRetentionResult, error) {
	if err := validateHistoryRetention(now, policy); err != nil {
		return HistoryRetentionResult{}, err
	}
	var result HistoryRetentionResult
	for range policy.maxTransactions {
		step, err := store.reconcileHistoryStep(ctx, now, policy)
		if err != nil {
			return HistoryRetentionResult{}, err
		}
		if step.claimed {
			result.ClaimedJobs++
		}
		if step.deletedJob {
			result.DeletedJobs++
		}
		result.DeletedRows += step.deletedRows
		if step.done {
			result.More = false
			return result, nil
		}
	}
	result.More = true
	return result, nil
}

func (store *Store) reconcileHistoryStep(ctx context.Context, now time.Time, policy historyRetentionPolicy) (historyRetentionStep, error) {
	var step historyRetentionStep
	budgets := []uint64{historyRetentionChunkWALBudget, historyRetentionFinalWALBudget, historyRetentionFinalWALBudget}
	err := store.immediate(ctx, budgets, func(connection *sql.Conn, writer *transactionWriter) error {
		jobID, found, err := loadHistoryRetentionClaim(ctx, connection)
		if err != nil {
			return err
		}
		if !found {
			candidate, exists, err := selectHistoryRetentionCandidate(ctx, connection, now, policy)
			if err != nil {
				return err
			}
			if !exists {
				step.done = true
				return nil
			}
			result, err := writer.ExecContext(ctx, "INSERT INTO job_history_retention(singleton,job_id,policy_version,claimed_at_unix) VALUES(1,?,?,?)", candidate, HistoryRetentionPolicyVersion, now.Unix())
			if err := requireExactRow("claim Job history retention", result, err); err != nil {
				return err
			}
			step.claimed = true
			step.deletedRows = 1
			return nil
		}
		if err := validateClaimedHistoryJob(ctx, connection, jobID); err != nil {
			return err
		}
		for _, child := range historyChildTables {
			present, err := historyChildPresent(ctx, connection, child.name, jobID)
			if err != nil {
				return err
			}
			if !present {
				continue
			}
			query := fmt.Sprintf("DELETE FROM %s WHERE %s IN (SELECT %s FROM %s WHERE job_id=? ORDER BY %s LIMIT ?) AND job_id=?", child.name, child.key, child.key, child.name, child.orderBy) //nolint:gosec // table metadata is compile-time-only
			result, err := writer.ExecContext(ctx, query, jobID, policy.rowBatchSize, jobID)
			if err != nil {
				return fmt.Errorf("delete retained Job %q %s chunk: %w", jobID, child.name, err)
			}
			changed, err := result.RowsAffected()
			if err != nil || changed < 1 || changed > int64(policy.rowBatchSize) {
				return fmt.Errorf("delete retained Job %q %s chunk changed %d rows: %w", jobID, child.name, changed, err)
			}
			step.deletedRows = int(changed)
			return nil
		}
		var planID string
		if err := connection.QueryRowContext(ctx, "SELECT plan_id FROM jobs WHERE job_id=?", jobID).Scan(&planID); err != nil {
			return fmt.Errorf("finalize Job history retention %q: %w", jobID, err)
		}
		result, err := writer.ExecContext(ctx, "DELETE FROM job_history_retention WHERE singleton=1 AND job_id=? AND policy_version=?", jobID, HistoryRetentionPolicyVersion)
		if err := requireExactRow("delete Job history retention claim", result, err); err != nil {
			return err
		}
		result, err = writer.ExecContext(ctx, "DELETE FROM jobs WHERE job_id=? AND state IN ('completed','completed_with_source_retained','failed','canceled')", jobID)
		if err := requireExactRow("delete terminal Job history", result, err); err != nil {
			return err
		}
		result, err = writer.ExecContext(ctx, "DELETE FROM operation_plans WHERE plan_id=?", planID)
		if err := requireExactRow("delete terminal Job plan", result, err); err != nil {
			return err
		}
		step.deletedJob = true
		step.deletedRows = 3
		return nil
	})
	if err != nil {
		return historyRetentionStep{}, fmt.Errorf("reconcile Job history: %w", err)
	}
	return step, nil
}

func selectHistoryRetentionCandidate(ctx context.Context, connection *sql.Conn, now time.Time, policy historyRetentionPolicy) (domain.JobID, bool, error) {
	cutoff := now.Add(-policy.minimumTerminalAge).Unix()
	row := connection.QueryRowContext(ctx, `SELECT j.job_id FROM jobs AS j
		WHERE j.state IN ('completed','completed_with_source_retained','failed','canceled')
		AND j.updated_at_unix<=?
		AND j.job_id NOT IN (
			SELECT newest.job_id FROM jobs AS newest
			WHERE newest.state IN ('completed','completed_with_source_retained','failed','canceled')
			ORDER BY newest.updated_at_unix DESC,newest.job_id DESC LIMIT ?
		)
		AND NOT EXISTS(SELECT 1 FROM edit_session_jobs AS e WHERE e.job_id=j.job_id)
		AND NOT EXISTS(SELECT 1 FROM cache_references AS r WHERE r.owner_kind='upload' AND r.owner_id=j.job_id)
		AND NOT EXISTS(SELECT 1 FROM cache_leases AS l WHERE l.owner_kind='upload' AND l.owner_id=j.job_id AND l.state IN ('active','uncertain'))
		ORDER BY j.updated_at_unix,j.job_id LIMIT 1`, cutoff, policy.keepNewestTerminal)
	var jobID domain.JobID
	if err := row.Scan(&jobID); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return "", false, nil
		}
		return "", false, fmt.Errorf("select Job history retention candidate: %w", err)
	}
	return jobID, true, nil
}

func loadHistoryRetentionClaim(ctx context.Context, connection *sql.Conn) (domain.JobID, bool, error) {
	var jobID domain.JobID
	var policyVersion int
	err := connection.QueryRowContext(ctx, "SELECT job_id,policy_version FROM job_history_retention WHERE singleton=1").Scan(&jobID, &policyVersion)
	if errors.Is(err, sql.ErrNoRows) {
		return "", false, nil
	}
	if err != nil {
		return "", false, fmt.Errorf("load Job history retention claim: %w", err)
	}
	if policyVersion != HistoryRetentionPolicyVersion {
		return "", false, fmt.Errorf("load Job history retention claim: unsupported policy version %d", policyVersion)
	}
	if _, err := domain.ParseJobID(string(jobID)); err != nil {
		return "", false, fmt.Errorf("load Job history retention claim: %w", err)
	}
	return jobID, true, nil
}

func validateClaimedHistoryJob(ctx context.Context, connection *sql.Conn, jobID domain.JobID) error {
	var terminal, editReferences, cacheReferences, cacheLeases int
	err := connection.QueryRowContext(ctx, `SELECT
		EXISTS(SELECT 1 FROM jobs WHERE job_id=? AND state IN ('completed','completed_with_source_retained','failed','canceled')),
		(SELECT count(*) FROM edit_session_jobs WHERE job_id=?),
		(SELECT count(*) FROM cache_references WHERE owner_kind='upload' AND owner_id=?),
		(SELECT count(*) FROM cache_leases WHERE owner_kind='upload' AND owner_id=? AND state IN ('active','uncertain'))`, jobID, jobID, jobID, jobID).Scan(&terminal, &editReferences, &cacheReferences, &cacheLeases)
	if err != nil {
		return fmt.Errorf("validate claimed Job history %q: %w", jobID, err)
	}
	if terminal != 1 {
		return fmt.Errorf("validate claimed Job history %q: Job is missing or nonterminal", jobID)
	}
	if editReferences != 0 || cacheReferences != 0 || cacheLeases != 0 {
		return fmt.Errorf("validate claimed Job history %q: gained recovery or audit reference", jobID)
	}
	return nil
}

func historyChildPresent(ctx context.Context, connection *sql.Conn, table string, jobID domain.JobID) (bool, error) {
	query := fmt.Sprintf("SELECT EXISTS(SELECT 1 FROM %s WHERE job_id=?)", table) //nolint:gosec // table is compile-time-only
	var present int
	if err := connection.QueryRowContext(ctx, query, jobID).Scan(&present); err != nil {
		return false, fmt.Errorf("inspect retained Job %q %s: %w", jobID, table, err)
	}
	return present == 1, nil
}

func validateHistoryRetention(now time.Time, policy historyRetentionPolicy) error {
	if now.IsZero() || now.Unix() <= 0 {
		return errors.New("reconcile Job history: current time must be positive")
	}
	if policy.keepNewestTerminal < 0 || policy.keepNewestTerminal > 1_000_000 {
		return errors.New("reconcile Job history: keep-newest bound is outside 0..1000000")
	}
	if policy.minimumTerminalAge < 0 || policy.minimumTerminalAge > 100*365*24*time.Hour {
		return errors.New("reconcile Job history: minimum age is outside 0..100 years")
	}
	if policy.rowBatchSize < 1 || policy.rowBatchSize > historyRetentionRowBatch {
		return fmt.Errorf("reconcile Job history: row batch is outside 1..%d", historyRetentionRowBatch)
	}
	if policy.maxTransactions < 1 || policy.maxTransactions > 256 {
		return errors.New("reconcile Job history: transaction bound is outside 1..256")
	}
	return nil
}
