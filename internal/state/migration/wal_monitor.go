package migration

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
)

type fileMigrationWALMonitor struct {
	connection *sql.Conn
	walPath    string
	baseline   uint64
	budget     uint64
}

func (monitor *fileMigrationWALMonitor) Prepare(ctx context.Context, connection *sql.Conn, item Migration) error {
	if monitor == nil || connection == nil || item.Version <= 1 || item.MaxMigrationWalBytes == 0 {
		return fmt.Errorf("prepare migration WAL monitor: invalid input")
	}
	var journalMode string
	if err := connection.QueryRowContext(ctx, "PRAGMA journal_mode").Scan(&journalMode); err != nil {
		return fmt.Errorf("prepare migration WAL monitor: read journal mode: %w", err)
	}
	if journalMode != "wal" {
		return fmt.Errorf("prepare migration WAL monitor: journal mode %q, want wal", journalMode)
	}
	if err := truncateMigrationWAL(ctx, connection); err != nil {
		return fmt.Errorf("prepare migration WAL monitor: %w", err)
	}
	mainPath, err := migrationMainPath(ctx, connection)
	if err != nil {
		return err
	}
	monitor.connection = connection
	monitor.walPath = mainPath + "-wal"
	monitor.budget = item.MaxMigrationWalBytes
	monitor.baseline, err = migrationWALSize(monitor.walPath)
	if err != nil {
		return fmt.Errorf("prepare migration WAL monitor: inspect truncated WAL: %w", err)
	}
	if monitor.baseline != 0 {
		return fmt.Errorf("prepare migration WAL monitor: truncated WAL size = %d, want 0", monitor.baseline)
	}
	return nil
}

func (monitor *fileMigrationWALMonitor) AfterStatement(_ context.Context, _ int) error {
	return monitor.observe(false)
}

func (monitor *fileMigrationWALMonitor) BeforeCommit(context.Context) error {
	return monitor.observe(false)
}

func (monitor *fileMigrationWALMonitor) AfterCommit(context.Context) error {
	return monitor.observe(true)
}

func (monitor *fileMigrationWALMonitor) Checkpoint(ctx context.Context) error {
	if monitor == nil || monitor.connection == nil {
		return fmt.Errorf("checkpoint migration WAL monitor: not prepared")
	}
	if err := truncateMigrationWAL(ctx, monitor.connection); err != nil {
		return err
	}
	size, err := migrationWALSize(monitor.walPath)
	if err != nil {
		return fmt.Errorf("checkpoint migration WAL monitor: inspect WAL: %w", err)
	}
	if size != 0 {
		return fmt.Errorf("checkpoint migration WAL monitor: WAL size = %d, want 0", size)
	}
	return nil
}

func (monitor *fileMigrationWALMonitor) observe(committed bool) error {
	if monitor == nil || monitor.connection == nil || monitor.walPath == "" || monitor.budget == 0 {
		return fmt.Errorf("observe migration WAL: monitor is not prepared")
	}
	size, err := migrationWALSize(monitor.walPath)
	if err != nil {
		return fmt.Errorf("observe migration WAL: %w", err)
	}
	if size < monitor.baseline {
		return fmt.Errorf("observe migration WAL: size %d is below baseline %d without checkpoint", size, monitor.baseline)
	}
	actual := size - monitor.baseline
	if actual <= monitor.budget {
		return nil
	}
	if committed {
		return &CommittedMigrationWALViolation{BudgetBytes: monitor.budget, ActualBytes: actual}
	}
	return fmt.Errorf("migration WAL growth %d exceeds budget %d before commit", actual, monitor.budget)
}

func truncateMigrationWAL(ctx context.Context, connection *sql.Conn) error {
	var busy, logFrames, checkpointed int64
	if err := connection.QueryRowContext(ctx, "PRAGMA wal_checkpoint(TRUNCATE)").Scan(&busy, &logFrames, &checkpointed); err != nil {
		return fmt.Errorf("truncate migration WAL: %w", err)
	}
	if busy != 0 || logFrames != 0 || checkpointed != 0 {
		return fmt.Errorf("truncate migration WAL: result = (%d,%d,%d), want (0,0,0)", busy, logFrames, checkpointed)
	}
	return nil
}

func migrationMainPath(ctx context.Context, connection *sql.Conn) (string, error) {
	rows, err := connection.QueryContext(ctx, "PRAGMA database_list")
	if err != nil {
		return "", fmt.Errorf("migration WAL main path: %w", err)
	}
	var mainPath string
	for rows.Next() {
		var sequence int64
		var name, path string
		if err := rows.Scan(&sequence, &name, &path); err != nil {
			_ = rows.Close()
			return "", fmt.Errorf("migration WAL main path: scan: %w", err)
		}
		if name == "main" {
			mainPath = path
		}
	}
	if err := errors.Join(rows.Err(), rows.Close()); err != nil {
		return "", fmt.Errorf("migration WAL main path: finish: %w", err)
	}
	if mainPath == "" {
		return "", fmt.Errorf("migration WAL main path is empty")
	}
	return mainPath, nil
}
