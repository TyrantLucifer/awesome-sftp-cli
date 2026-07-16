package wal

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"sync"
)

type FileGuard struct {
	mu         sync.Mutex
	controller *Controller
	walPath    string
	active     bool
}

type FileTransaction struct {
	guard          *FileGuard
	budgets        []uint64
	totalBudget    uint64
	nextStatement  int
	currentWALSize uint64
	observedGrowth uint64
	finished       bool
}

type CheckpointReport struct {
	Busy               int64
	LogFrames          int64
	CheckpointedFrames int64
	WALBytes           uint64
}

func OpenFileGuard(ctx context.Context, connection *sql.Conn) (*FileGuard, error) {
	if connection == nil {
		return nil, fmt.Errorf("open WAL file guard: nil connection")
	}
	var journalMode string
	if err := connection.QueryRowContext(ctx, "PRAGMA journal_mode").Scan(&journalMode); err != nil {
		return nil, fmt.Errorf("open WAL file guard: read journal mode: %w", err)
	}
	if journalMode != "wal" {
		return nil, fmt.Errorf("open WAL file guard: journal mode %q, want wal", journalMode)
	}
	var autoCheckpoint int64
	if err := connection.QueryRowContext(ctx, "PRAGMA wal_autocheckpoint").Scan(&autoCheckpoint); err != nil {
		return nil, fmt.Errorf("open WAL file guard: read autocheckpoint: %w", err)
	}
	if autoCheckpoint != 1000 {
		return nil, fmt.Errorf("open WAL file guard: wal_autocheckpoint = %d, want 1000", autoCheckpoint)
	}
	mainPath, err := mainDatabasePath(ctx, connection)
	if err != nil {
		return nil, err
	}
	walPath := mainPath + "-wal"
	size, err := fileSizeNoFollow(walPath)
	if err != nil {
		return nil, fmt.Errorf("open WAL file guard: inspect WAL: %w", err)
	}
	controller, err := NewController(size)
	if err != nil {
		return nil, err
	}
	return &FileGuard{controller: controller, walPath: walPath}, nil
}

func (guard *FileGuard) Begin(statementBudgets []uint64) (*FileTransaction, error) {
	if guard == nil || guard.controller == nil {
		return nil, fmt.Errorf("begin guarded WAL transaction: nil guard")
	}
	total, err := transactionBudget(statementBudgets)
	if err != nil {
		return nil, err
	}
	guard.mu.Lock()
	defer guard.mu.Unlock()
	if guard.active {
		return nil, fmt.Errorf("begin guarded WAL transaction: another transaction is active")
	}
	size, err := fileSizeNoFollow(guard.walPath)
	if err != nil {
		return nil, fmt.Errorf("begin guarded WAL transaction: inspect WAL: %w", err)
	}
	if err := guard.controller.ObserveCheckpoint(size); err != nil {
		return nil, fmt.Errorf("begin guarded WAL transaction: synchronize observation: %w", err)
	}
	if err := guard.controller.PreflightTransaction(statementBudgets); err != nil {
		return nil, err
	}
	guard.active = true
	budgets := append([]uint64(nil), statementBudgets...)
	return &FileTransaction{
		guard: guard, budgets: budgets, totalBudget: total, currentWALSize: size,
	}, nil
}

func (transaction *FileTransaction) AfterStatement(index int) error {
	if transaction == nil || transaction.guard == nil || transaction.finished || index != transaction.nextStatement || index >= len(transaction.budgets) {
		return fmt.Errorf("observe guarded WAL statement: invalid transaction or statement index")
	}
	size, err := fileSizeNoFollow(transaction.guard.walPath)
	if err != nil {
		return fmt.Errorf("observe guarded WAL statement %d: %w", index, err)
	}
	before := transaction.currentWALSize
	if err := transaction.guard.controller.ObserveWrite(before, size, transaction.budgets[index], false); err != nil {
		return fmt.Errorf("observe guarded WAL statement %d: %w", index, err)
	}
	transaction.observedGrowth += size - before
	transaction.currentWALSize = size
	transaction.nextStatement++
	return nil
}

func (transaction *FileTransaction) BeforeCommit() error {
	if transaction == nil || transaction.guard == nil || transaction.finished {
		return fmt.Errorf("preflight guarded WAL commit: invalid transaction")
	}
	if transaction.guard.controller.Pressure().StopWrites {
		return fmt.Errorf("preflight guarded WAL commit: hard stop reached")
	}
	return nil
}

func (transaction *FileTransaction) AfterCommit() error {
	return transaction.afterBoundary(true)
}

func (transaction *FileTransaction) AfterRollback() error {
	return transaction.afterBoundary(false)
}

func (transaction *FileTransaction) afterBoundary(committed bool) error {
	if transaction == nil || transaction.guard == nil || transaction.finished {
		return fmt.Errorf("observe guarded WAL boundary: invalid transaction")
	}
	defer transaction.finish()
	size, err := fileSizeNoFollow(transaction.guard.walPath)
	if err != nil {
		return fmt.Errorf("observe guarded WAL boundary: %w", err)
	}
	if size < transaction.currentWALSize {
		return fmt.Errorf("observe guarded WAL boundary: WAL shrank without checkpoint")
	}
	actual := size - transaction.currentWALSize
	if transaction.observedGrowth > transaction.totalBudget {
		return fmt.Errorf("observe guarded WAL boundary: prior growth exceeds transaction budget")
	}
	remaining := transaction.totalBudget - transaction.observedGrowth
	if remaining == 0 {
		if actual != 0 {
			return &GrowthViolation{DeclaredBytes: 0, ActualBytes: actual, Committed: committed}
		}
		return nil
	}
	if err := transaction.guard.controller.ObserveBoundary(transaction.currentWALSize, size, remaining, committed); err != nil {
		return err
	}
	transaction.observedGrowth += actual
	transaction.currentWALSize = size
	return nil
}

func (transaction *FileTransaction) finish() {
	transaction.finished = true
	transaction.guard.mu.Lock()
	transaction.guard.active = false
	transaction.guard.mu.Unlock()
}

func (guard *FileGuard) Snapshot() Snapshot {
	if guard == nil {
		return Snapshot{Pressure: Pressure{PauseNewReaders: true, PauseJobClaims: true, StopWrites: true, ReadOnly: true}}
	}
	return guard.controller.Snapshot()
}

func (guard *FileGuard) PassiveCheckpoint(ctx context.Context, connection *sql.Conn) (CheckpointReport, error) {
	return guard.checkpoint(ctx, connection, "PASSIVE")
}

func (guard *FileGuard) TruncateIdle(ctx context.Context, connection *sql.Conn) error {
	report, err := guard.checkpoint(ctx, connection, "TRUNCATE")
	if err != nil {
		return err
	}
	if report.Busy != 0 || report.LogFrames != 0 || report.CheckpointedFrames != 0 || report.WALBytes != 0 {
		return fmt.Errorf("truncate idle WAL: result = (%d,%d,%d) bytes=%d, want all zero", report.Busy, report.LogFrames, report.CheckpointedFrames, report.WALBytes)
	}
	return nil
}

func (guard *FileGuard) checkpoint(ctx context.Context, connection *sql.Conn, mode string) (CheckpointReport, error) {
	var report CheckpointReport
	if guard == nil || guard.controller == nil || connection == nil {
		return report, fmt.Errorf("checkpoint guarded WAL: invalid guard or connection")
	}
	guard.mu.Lock()
	defer guard.mu.Unlock()
	if guard.active {
		return report, fmt.Errorf("checkpoint guarded WAL: transaction is active")
	}
	query := "PRAGMA wal_checkpoint(PASSIVE)"
	if mode == "TRUNCATE" {
		query = "PRAGMA wal_checkpoint(TRUNCATE)"
	} else if mode != "PASSIVE" {
		return report, fmt.Errorf("checkpoint guarded WAL: invalid mode %q", mode)
	}
	if err := connection.QueryRowContext(ctx, query).Scan(&report.Busy, &report.LogFrames, &report.CheckpointedFrames); err != nil {
		return report, fmt.Errorf("checkpoint guarded WAL %s: %w", mode, err)
	}
	if report.Busy < 0 || report.LogFrames < 0 || report.CheckpointedFrames < 0 || report.CheckpointedFrames > report.LogFrames {
		return report, fmt.Errorf("checkpoint guarded WAL %s: invalid result (%d,%d,%d)", mode, report.Busy, report.LogFrames, report.CheckpointedFrames)
	}
	size, err := fileSizeNoFollow(guard.walPath)
	if err != nil {
		return report, fmt.Errorf("checkpoint guarded WAL %s: inspect WAL: %w", mode, err)
	}
	report.WALBytes = size
	if err := guard.controller.ObserveCheckpoint(size); err != nil {
		return report, fmt.Errorf("checkpoint guarded WAL %s: %w", mode, err)
	}
	return report, nil
}

func mainDatabasePath(ctx context.Context, connection *sql.Conn) (string, error) {
	rows, err := connection.QueryContext(ctx, "PRAGMA database_list")
	if err != nil {
		return "", fmt.Errorf("open WAL file guard: database list: %w", err)
	}
	var mainPath string
	for rows.Next() {
		var sequence int64
		var name, path string
		if err := rows.Scan(&sequence, &name, &path); err != nil {
			_ = rows.Close()
			return "", fmt.Errorf("open WAL file guard: scan database list: %w", err)
		}
		if name == "main" {
			mainPath = path
		}
	}
	if err := errors.Join(rows.Err(), rows.Close()); err != nil {
		return "", fmt.Errorf("open WAL file guard: finish database list: %w", err)
	}
	if mainPath == "" {
		return "", fmt.Errorf("open WAL file guard: main database path is empty")
	}
	return mainPath, nil
}
