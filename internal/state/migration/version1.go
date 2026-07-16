package migration

import "fmt"

const (
	schemaMigrationsStatement  = "CREATE TABLE schema_migrations(version INTEGER PRIMARY KEY CHECK(version > 0), name TEXT NOT NULL, sha256 TEXT NOT NULL CHECK(length(sha256) = 64 AND sha256 NOT GLOB '*[^0-9a-f]*'), applied_at TEXT NOT NULL) STRICT"
	migrationControlStatement  = "CREATE TABLE migration_control(singleton INTEGER PRIMARY KEY CHECK(singleton = 1), upgrade_hold INTEGER NOT NULL CHECK(upgrade_hold IN (0, 1)), hold_reason TEXT, hold_attempt_id TEXT, CHECK((upgrade_hold = 0 AND hold_reason IS NULL AND hold_attempt_id IS NULL) OR (upgrade_hold = 1 AND hold_reason = 'restored_backup' AND hold_attempt_id IS NOT NULL AND length(hold_attempt_id) = 32 AND hold_attempt_id NOT GLOB '*[^0-9a-f]*'))) STRICT"
	migrationControlRow        = "INSERT INTO migration_control(singleton, upgrade_hold, hold_reason, hold_attempt_id) VALUES(1, 0, NULL, NULL)"
	migrationAttemptsStatement = "CREATE TABLE migration_attempts(singleton INTEGER PRIMARY KEY CHECK(singleton = 1), attempt_id TEXT NOT NULL CHECK(length(attempt_id) = 32 AND attempt_id NOT GLOB '*[^0-9a-f]*'), original_head INTEGER NOT NULL CHECK(original_head > 0), current_head INTEGER NOT NULL, target_head INTEGER NOT NULL, migration_set_sha256 TEXT NOT NULL CHECK(length(migration_set_sha256) = 64 AND migration_set_sha256 NOT GLOB '*[^0-9a-f]*'), reserved_backup_basename TEXT NOT NULL, backup_sha256 TEXT, status TEXT NOT NULL CHECK(status IN ('preparing', 'ready', 'running', 'interrupted', 'failed')), error_kind TEXT, CHECK(original_head <= current_head AND current_head <= target_head AND original_head < target_head), CHECK(reserved_backup_basename = '.amsftp-backup-v1-' || attempt_id || '.sqlite3'), CHECK(backup_sha256 IS NULL OR (length(backup_sha256) = 64 AND backup_sha256 NOT GLOB '*[^0-9a-f]*')), CHECK((status = 'preparing' AND backup_sha256 IS NULL) OR (status = 'failed' AND backup_sha256 IS NULL AND current_head = original_head) OR (status IN ('ready', 'running', 'interrupted', 'failed') AND backup_sha256 IS NOT NULL)), CHECK((status IN ('interrupted', 'failed') AND error_kind IS NOT NULL AND length(error_kind) BETWEEN 1 AND 64) OR (status NOT IN ('interrupted', 'failed') AND error_kind IS NULL))) STRICT"
	migrationBackupsStatement  = "CREATE TABLE migration_backups(attempt_id TEXT PRIMARY KEY CHECK(length(attempt_id) = 32 AND attempt_id NOT GLOB '*[^0-9a-f]*'), original_head INTEGER NOT NULL CHECK(original_head > 0), target_head INTEGER NOT NULL CHECK(target_head > original_head), migration_set_sha256 TEXT NOT NULL CHECK(length(migration_set_sha256) = 64 AND migration_set_sha256 NOT GLOB '*[^0-9a-f]*'), backup_basename TEXT NOT NULL UNIQUE, backup_sha256 TEXT NOT NULL CHECK(length(backup_sha256) = 64 AND backup_sha256 NOT GLOB '*[^0-9a-f]*'), created_at_unix INTEGER NOT NULL CHECK(created_at_unix > 0), status TEXT NOT NULL CHECK(status IN ('verified', 'deleting')), CHECK(backup_basename = '.amsftp-backup-v1-' || attempt_id || '.sqlite3')) STRICT"
)

var version1Statements = []string{
	schemaMigrationsStatement,
	applicationIDStatement,
	migrationControlStatement,
	migrationControlRow,
	migrationAttemptsStatement,
	migrationBackupsStatement,
	"CREATE TABLE operation_plans(plan_id TEXT PRIMARY KEY, request_id TEXT NOT NULL UNIQUE, kind TEXT NOT NULL CHECK(kind IN ('copy', 'move', 'rename', 'delete')), source_json TEXT NOT NULL, destination_json TEXT, route TEXT NOT NULL, verification TEXT NOT NULL, conflict_policy TEXT NOT NULL, risk_class TEXT NOT NULL, frozen_at_unix INTEGER NOT NULL CHECK(frozen_at_unix > 0)) STRICT",
	"CREATE TABLE jobs(job_id TEXT PRIMARY KEY, plan_id TEXT NOT NULL UNIQUE REFERENCES operation_plans(plan_id), state TEXT NOT NULL CHECK(state IN ('draft', 'awaiting_confirmation', 'queued', 'running', 'verifying', 'paused', 'waiting_auth', 'waiting_conflict', 'retry_wait', 'completed', 'completed_with_source_retained', 'failed', 'canceled')), state_version INTEGER NOT NULL CHECK(state_version > 0), next_event_sequence INTEGER NOT NULL CHECK(next_event_sequence > 0), pause_requested INTEGER NOT NULL CHECK(pause_requested IN (0, 1)), cancel_requested INTEGER NOT NULL CHECK(cancel_requested IN (0, 1)), retry_at_unix INTEGER, created_at_unix INTEGER NOT NULL CHECK(created_at_unix > 0), updated_at_unix INTEGER NOT NULL CHECK(updated_at_unix >= created_at_unix), terminal_summary TEXT, CHECK((state IN ('completed', 'completed_with_source_retained', 'failed', 'canceled') AND terminal_summary IS NOT NULL) OR (state NOT IN ('completed', 'completed_with_source_retained', 'failed', 'canceled') AND terminal_summary IS NULL)), CHECK((state = 'retry_wait' AND retry_at_unix IS NOT NULL) OR (state <> 'retry_wait' AND retry_at_unix IS NULL))) STRICT",
	"CREATE TABLE job_steps(job_id TEXT NOT NULL REFERENCES jobs(job_id), step_index INTEGER NOT NULL CHECK(step_index >= 0), kind TEXT NOT NULL, state TEXT NOT NULL CHECK(state IN ('pending', 'running', 'verifying', 'paused', 'waiting_auth', 'waiting_conflict', 'retry_wait', 'completed', 'failed', 'canceled', 'skipped')), attempt INTEGER NOT NULL CHECK(attempt >= 0), source_json TEXT, destination_json TEXT, created_at_unix INTEGER NOT NULL CHECK(created_at_unix > 0), updated_at_unix INTEGER NOT NULL CHECK(updated_at_unix >= created_at_unix), PRIMARY KEY(job_id, step_index)) STRICT, WITHOUT ROWID",
	"CREATE TABLE job_checkpoints(job_id TEXT NOT NULL, step_index INTEGER NOT NULL, phase TEXT NOT NULL, verified_offset INTEGER NOT NULL CHECK(verified_offset >= 0), source_fingerprint TEXT, part_location_json TEXT, checksum_state BLOB, updated_at_unix INTEGER NOT NULL CHECK(updated_at_unix > 0), PRIMARY KEY(job_id, step_index), FOREIGN KEY(job_id, step_index) REFERENCES job_steps(job_id, step_index)) STRICT, WITHOUT ROWID",
	"CREATE TABLE job_conflicts(job_id TEXT NOT NULL REFERENCES jobs(job_id), conflict_index INTEGER NOT NULL CHECK(conflict_index >= 0), step_index INTEGER NOT NULL CHECK(step_index >= 0), class TEXT NOT NULL, state TEXT NOT NULL CHECK(state IN ('waiting', 'resolved')), source_json TEXT NOT NULL, destination_json TEXT NOT NULL, resolution TEXT, apply_scope TEXT, created_at_unix INTEGER NOT NULL CHECK(created_at_unix > 0), resolved_at_unix INTEGER, PRIMARY KEY(job_id, conflict_index), CHECK((state = 'waiting' AND resolution IS NULL AND apply_scope IS NULL AND resolved_at_unix IS NULL) OR (state = 'resolved' AND resolution IS NOT NULL AND apply_scope IS NOT NULL AND resolved_at_unix IS NOT NULL))) STRICT, WITHOUT ROWID",
	"CREATE TABLE job_events(job_id TEXT NOT NULL REFERENCES jobs(job_id), sequence INTEGER NOT NULL CHECK(sequence > 0), event_id TEXT NOT NULL UNIQUE, kind TEXT NOT NULL, payload_json TEXT NOT NULL, created_at_unix INTEGER NOT NULL CHECK(created_at_unix > 0), PRIMARY KEY(job_id, sequence)) STRICT, WITHOUT ROWID",
	"CREATE TABLE job_results(job_id TEXT NOT NULL REFERENCES jobs(job_id), result_index INTEGER NOT NULL CHECK(result_index >= 0), step_index INTEGER NOT NULL CHECK(step_index >= 0), outcome TEXT NOT NULL, detail_json TEXT NOT NULL, created_at_unix INTEGER NOT NULL CHECK(created_at_unix > 0), PRIMARY KEY(job_id, result_index)) STRICT, WITHOUT ROWID",
	"CREATE TABLE request_dedup(request_id TEXT PRIMARY KEY, operation TEXT NOT NULL, job_id TEXT REFERENCES jobs(job_id), response_json TEXT NOT NULL, created_at_unix INTEGER NOT NULL CHECK(created_at_unix > 0)) STRICT",
	"CREATE INDEX jobs_by_state_updated ON jobs(state, updated_at_unix, job_id)",
	"CREATE INDEX job_steps_by_state ON job_steps(state, updated_at_unix, job_id, step_index)",
	"CREATE INDEX job_conflicts_by_state ON job_conflicts(state, job_id, conflict_index)",
	"CREATE INDEX job_events_by_event_id ON job_events(event_id)",
}

func Version1() Migration {
	statements := make([]string, len(version1Statements))
	copy(statements, version1Statements)
	return Migration{Version: 1, Name: "init", Statements: statements}
}

func validateVersion1(migration Migration) error {
	if migration.Name != "init" || len(migration.Statements) != len(version1Statements) {
		return fmt.Errorf("validate migration set: version 1 does not match the compiled schema")
	}
	for index, statement := range version1Statements {
		if migration.Statements[index] != statement {
			return fmt.Errorf("validate migration set: version 1 statement %d does not match the compiled schema", index)
		}
	}
	return nil
}
