package migration

import (
	"context"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"time"
)

const (
	applicationID     = int64(0x414d5346)
	statePageSize     = int64(4096)
	stateMaxPageCount = int64(2_097_152)
	runnerSavepoint   = "amsftp_runner_statement"
)

// Runner applies already-compiled migrations on one reserved connection. It
// deliberately does not expose transaction controls to migration data.
type Runner struct {
	AttemptID  string
	WALMonitor MigrationWALMonitor
}

type MigrationWALMonitor interface {
	Prepare(context.Context, *sql.Conn, Migration) error
	AfterStatement(context.Context, int) error
	BeforeCommit(context.Context) error
	AfterCommit(context.Context) error
	Checkpoint(context.Context) error
}

type CommittedMigrationWALViolation struct {
	BudgetBytes uint64
	ActualBytes uint64
}

func (violation *CommittedMigrationWALViolation) Error() string {
	return fmt.Sprintf("committed migration WAL growth %d exceeds budget %d", violation.ActualBytes, violation.BudgetBytes)
}

func (runner Runner) Apply(ctx context.Context, connection *sql.Conn, migration Migration, appliedAt string) error {
	if connection == nil {
		return fmt.Errorf("apply migration: nil connection")
	}
	if _, err := time.Parse(time.RFC3339Nano, appliedAt); err != nil {
		return fmt.Errorf("apply migration: invalid applied_at: %w", err)
	}
	digest, err := Checksum(migration)
	if err != nil {
		return fmt.Errorf("apply migration: %w", err)
	}
	if migration.Version > 1 {
		if !attemptIDPattern.MatchString(runner.AttemptID) {
			return fmt.Errorf("apply migration version %d: exact active attempt ID is required", migration.Version)
		}
		if migration.MaxMigrationWalBytes == 0 || migration.MaxMigrationWalBytes > maxMigrationWalBytes {
			return fmt.Errorf("apply migration version %d: WAL budget %d is outside 1..%d", migration.Version, migration.MaxMigrationWalBytes, maxMigrationWalBytes)
		}
	}
	admitted := make([]AdmittedStatement, len(migration.Statements))
	for index, statement := range migration.Statements {
		admitted[index], err = AdmitStatement(migration.Version, index, statement)
		if err != nil {
			return fmt.Errorf("apply migration version %d statement %d: %w", migration.Version, index, err)
		}
	}
	if migration.Version == 1 {
		if err := validateVersion1(migration); err != nil {
			return err
		}
		if err := configureVersion1Bootstrap(ctx, connection); err != nil {
			return err
		}
	}
	if err := checkMigrationConnection(ctx, connection); err != nil {
		return err
	}
	monitor := runner.WALMonitor
	if migration.Version > 1 {
		if monitor == nil {
			monitor = &fileMigrationWALMonitor{}
		}
		if err := monitor.Prepare(ctx, connection, migration); err != nil {
			return fmt.Errorf("apply migration version %d WAL prepare: %w", migration.Version, err)
		}
	}
	if _, err := connection.ExecContext(ctx, "BEGIN IMMEDIATE"); err != nil {
		return fmt.Errorf("apply migration version %d: begin immediate: %w", migration.Version, err)
	}
	committed := false
	defer func() {
		if !committed {
			_, _ = connection.ExecContext(context.Background(), "ROLLBACK")
		}
	}()

	for index, statement := range migration.Statements {
		if admitted[index].Kind == StatementApplicationID {
			if _, err := connection.ExecContext(ctx, applicationIDStatement); err != nil {
				return fmt.Errorf("apply migration version %d application ID: %w", migration.Version, err)
			}
			continue
		}
		if err := checkMigrationConnection(ctx, connection); err != nil {
			return fmt.Errorf("apply migration version %d statement %d preflight: %w", migration.Version, index, err)
		}
		if _, err := connection.ExecContext(ctx, "SAVEPOINT "+runnerSavepoint); err != nil {
			return fmt.Errorf("apply migration version %d statement %d savepoint: %w", migration.Version, index, err)
		}
		if _, err := connection.ExecContext(ctx, statement); err != nil {
			return fmt.Errorf("apply migration version %d statement %d: %w", migration.Version, index, err)
		}
		if _, err := connection.ExecContext(ctx, "RELEASE "+runnerSavepoint); err != nil {
			return fmt.Errorf("apply migration version %d statement %d release: %w", migration.Version, index, err)
		}
		if err := checkMigrationConnection(ctx, connection); err != nil {
			return fmt.Errorf("apply migration version %d statement %d postflight: %w", migration.Version, index, err)
		}
		if monitor != nil {
			if err := monitor.AfterStatement(ctx, index); err != nil {
				return fmt.Errorf("apply migration version %d statement %d WAL budget: %w", migration.Version, index, err)
			}
		}
	}
	if _, err := connection.ExecContext(
		ctx,
		"INSERT INTO schema_migrations(version, name, sha256, applied_at) VALUES(?, ?, ?, ?)",
		migration.Version,
		migration.Name,
		hex.EncodeToString(digest[:]),
		appliedAt,
	); err != nil {
		return fmt.Errorf("apply migration version %d history: %w", migration.Version, err)
	}
	if migration.Version > 1 {
		updated, err := connection.ExecContext(ctx, "UPDATE migration_attempts SET current_head=? WHERE singleton=1 AND attempt_id=? AND current_head=? AND target_head>=? AND status='running' AND backup_sha256 IS NOT NULL", migration.Version, runner.AttemptID, migration.Version-1, migration.Version)
		if err != nil {
			return fmt.Errorf("apply migration version %d attempt head: %w", migration.Version, err)
		}
		rows, err := updated.RowsAffected()
		if err != nil || rows != 1 {
			return fmt.Errorf("apply migration version %d attempt head: active attempt is not the required running prefix", migration.Version)
		}
	}
	if monitor != nil {
		if err := monitor.BeforeCommit(ctx); err != nil {
			return fmt.Errorf("apply migration version %d pre-commit WAL budget: %w", migration.Version, err)
		}
	}
	if _, err := connection.ExecContext(ctx, "COMMIT"); err != nil {
		return fmt.Errorf("apply migration version %d commit: %w", migration.Version, err)
	}
	committed = true
	if monitor != nil {
		if err := monitor.AfterCommit(ctx); err != nil {
			return fmt.Errorf("apply migration version %d committed WAL budget: %w", migration.Version, err)
		}
		if err := monitor.Checkpoint(ctx); err != nil {
			return fmt.Errorf("apply migration version %d checkpoint: %w", migration.Version, err)
		}
	}
	return nil
}

func configureVersion1Bootstrap(ctx context.Context, connection *sql.Conn) error {
	for _, command := range []string{
		"PRAGMA page_size=4096",
		"PRAGMA max_page_count=2097152",
	} {
		if _, err := connection.ExecContext(ctx, command); err != nil {
			return fmt.Errorf("configure version 1 bootstrap %q: %w", command, err)
		}
	}
	for _, expectation := range []struct {
		name string
		want int64
	}{
		{name: "page_size", want: statePageSize},
		{name: "max_page_count", want: stateMaxPageCount},
	} {
		var got int64
		if err := connection.QueryRowContext(ctx, "PRAGMA "+expectation.name).Scan(&got); err != nil {
			return fmt.Errorf("read version 1 PRAGMA %s: %w", expectation.name, err)
		}
		if got != expectation.want {
			return fmt.Errorf("read version 1 PRAGMA %s: got %d, want %d", expectation.name, got, expectation.want)
		}
	}
	return nil
}

func checkMigrationConnection(ctx context.Context, connection *sql.Conn) error {
	rows, err := connection.QueryContext(ctx, "PRAGMA database_list")
	if err != nil {
		return fmt.Errorf("inspect migration database list: %w", err)
	}
	for rows.Next() {
		var sequence int64
		var name, path string
		if err := rows.Scan(&sequence, &name, &path); err != nil {
			_ = rows.Close()
			return fmt.Errorf("read migration database list: %w", err)
		}
		if name != "main" && name != "temp" {
			_ = rows.Close()
			return fmt.Errorf("migration connection has forbidden attached schema %q", name)
		}
	}
	if err := errors.Join(rows.Err(), rows.Close()); err != nil {
		return fmt.Errorf("finish migration database list: %w", err)
	}
	var tempObjects int64
	if err := connection.QueryRowContext(ctx, "SELECT count(*) FROM temp.sqlite_schema").Scan(&tempObjects); err != nil {
		return fmt.Errorf("inspect temporary migration schema: %w", err)
	}
	if tempObjects != 0 {
		return fmt.Errorf("temporary migration schema contains %d objects", tempObjects)
	}
	return nil
}
