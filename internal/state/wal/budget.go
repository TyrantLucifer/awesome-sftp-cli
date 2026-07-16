// Package wal owns the fixed online SQLite WAL budgets from ADR-0008.
package wal

import (
	"context"
	"fmt"
	"math"
	"sync"
	"time"
)

const (
	StatementGrowthLimitBytes   = uint64(4 * 1024 * 1024)
	TransactionGrowthLimitBytes = uint64(8 * 1024 * 1024)
	SoftLimitBytes              = uint64(64 * 1024 * 1024)
	HardStopBytes               = uint64(256 * 1024 * 1024)
	AbsoluteCeilingBytes        = uint64(264 * 1024 * 1024)
	MaxOperationsPerTransaction = 256
	MaxReaderAge                = 2 * time.Second
)

type Pressure struct {
	PauseNewReaders bool
	PauseJobClaims  bool
	StopWrites      bool
	ReadOnly        bool
}

type Snapshot struct {
	WALBytes      uint64
	ReservedBytes uint64
	Reservations  int
	Pressure      Pressure
}

type Controller struct {
	mu            sync.Mutex
	walBytes      uint64
	reservedBytes uint64
	reservations  map[string]uint64
	readOnly      bool
}

type GrowthViolation struct {
	DeclaredBytes uint64
	ActualBytes   uint64
	Committed     bool
}

func (violation *GrowthViolation) Error() string {
	return fmt.Sprintf("WAL growth %d exceeds declared budget %d (committed=%t)", violation.ActualBytes, violation.DeclaredBytes, violation.Committed)
}

func NewController(initialWALBytes uint64) (*Controller, error) {
	if initialWALBytes > AbsoluteCeilingBytes {
		return nil, fmt.Errorf("new WAL controller: initial WAL bytes %d exceed absolute ceiling %d", initialWALBytes, AbsoluteCeilingBytes)
	}
	return &Controller{
		walBytes:     initialWALBytes,
		reservations: make(map[string]uint64),
	}, nil
}

func (controller *Controller) Reserve(workerID string, bytes uint64) error {
	if controller == nil || workerID == "" || len(workerID) > 128 || bytes == 0 || bytes > TransactionGrowthLimitBytes {
		return fmt.Errorf("reserve WAL budget: invalid worker or byte count")
	}
	controller.mu.Lock()
	defer controller.mu.Unlock()
	if controller.readOnly {
		return fmt.Errorf("reserve WAL budget: controller is read-only")
	}
	if _, exists := controller.reservations[workerID]; exists {
		return fmt.Errorf("reserve WAL budget: worker %q already has a reservation", workerID)
	}
	total, ok := add(controller.reservedBytes, bytes)
	if !ok || total > TransactionGrowthLimitBytes {
		return fmt.Errorf("reserve WAL budget: global reservations would exceed %d", TransactionGrowthLimitBytes)
	}
	projected, ok := add(controller.walBytes, total)
	if !ok || projected >= HardStopBytes {
		return fmt.Errorf("reserve WAL budget: projected WAL bytes reach the hard stop")
	}
	controller.reservations[workerID] = bytes
	controller.reservedBytes = total
	return nil
}

func (controller *Controller) Release(workerID string) error {
	if controller == nil || workerID == "" {
		return fmt.Errorf("release WAL budget: invalid worker")
	}
	controller.mu.Lock()
	defer controller.mu.Unlock()
	bytes, exists := controller.reservations[workerID]
	if !exists {
		return fmt.Errorf("release WAL budget: worker %q has no reservation", workerID)
	}
	delete(controller.reservations, workerID)
	controller.reservedBytes -= bytes
	return nil
}

func (controller *Controller) ObserveWrite(beforeBytes, afterBytes, declaredBytes uint64, committed bool) error {
	return controller.observeGrowth(beforeBytes, afterBytes, declaredBytes, StatementGrowthLimitBytes, committed)
}

func (controller *Controller) ObserveBoundary(beforeBytes, afterBytes, declaredBytes uint64, committed bool) error {
	return controller.observeGrowth(beforeBytes, afterBytes, declaredBytes, TransactionGrowthLimitBytes, committed)
}

func (controller *Controller) observeGrowth(beforeBytes, afterBytes, declaredBytes, maximumDeclared uint64, committed bool) error {
	if controller == nil || declaredBytes == 0 || declaredBytes > maximumDeclared {
		return fmt.Errorf("observe WAL write: invalid controller or declared budget")
	}
	controller.mu.Lock()
	defer controller.mu.Unlock()
	if beforeBytes != controller.walBytes {
		return fmt.Errorf("observe WAL write: stale before size %d, current is %d", beforeBytes, controller.walBytes)
	}
	if afterBytes < beforeBytes {
		return fmt.Errorf("observe WAL write: WAL shrank from %d to %d outside a checkpoint", beforeBytes, afterBytes)
	}
	controller.walBytes = afterBytes
	if afterBytes > AbsoluteCeilingBytes {
		controller.readOnly = true
		return fmt.Errorf("observe WAL write: WAL bytes %d exceed absolute ceiling %d", afterBytes, AbsoluteCeilingBytes)
	}
	actual := afterBytes - beforeBytes
	if actual > declaredBytes {
		return &GrowthViolation{DeclaredBytes: declaredBytes, ActualBytes: actual, Committed: committed}
	}
	return nil
}

func (controller *Controller) PreflightTransaction(statementBudgets []uint64) error {
	total, err := transactionBudget(statementBudgets)
	if err != nil {
		return err
	}
	if controller == nil {
		return fmt.Errorf("preflight WAL transaction: nil controller")
	}
	controller.mu.Lock()
	defer controller.mu.Unlock()
	if controller.readOnly {
		return fmt.Errorf("preflight WAL transaction: controller is read-only")
	}
	projected, ok := add(controller.walBytes, controller.reservedBytes)
	if ok {
		projected, ok = add(projected, total)
	}
	if !ok || projected >= HardStopBytes {
		return fmt.Errorf("preflight WAL transaction: projected WAL bytes reach the hard stop")
	}
	return nil
}

func (controller *Controller) ObserveCheckpoint(walBytes uint64) error {
	if controller == nil {
		return fmt.Errorf("observe WAL checkpoint: nil controller")
	}
	controller.mu.Lock()
	defer controller.mu.Unlock()
	controller.walBytes = walBytes
	if walBytes > AbsoluteCeilingBytes {
		controller.readOnly = true
		return fmt.Errorf("observe WAL checkpoint: WAL bytes %d exceed absolute ceiling %d", walBytes, AbsoluteCeilingBytes)
	}
	return nil
}

func (controller *Controller) Pressure() Pressure {
	if controller == nil {
		return Pressure{PauseNewReaders: true, PauseJobClaims: true, StopWrites: true, ReadOnly: true}
	}
	controller.mu.Lock()
	defer controller.mu.Unlock()
	return controller.pressureLocked()
}

func (controller *Controller) Snapshot() Snapshot {
	if controller == nil {
		return Snapshot{Pressure: Pressure{PauseNewReaders: true, PauseJobClaims: true, StopWrites: true, ReadOnly: true}}
	}
	controller.mu.Lock()
	defer controller.mu.Unlock()
	return Snapshot{
		WALBytes:      controller.walBytes,
		ReservedBytes: controller.reservedBytes,
		Reservations:  len(controller.reservations),
		Pressure:      controller.pressureLocked(),
	}
}

func (controller *Controller) pressureLocked() Pressure {
	projected, ok := add(controller.walBytes, controller.reservedBytes)
	readOnly := controller.readOnly || !ok || controller.walBytes > AbsoluteCeilingBytes
	if !ok {
		projected = math.MaxUint64
	}
	stop := readOnly || projected >= HardStopBytes
	pause := stop || projected >= SoftLimitBytes
	return Pressure{
		PauseNewReaders: pause,
		PauseJobClaims:  pause,
		StopWrites:      stop,
		ReadOnly:        readOnly,
	}
}

func ValidateTransactionBudgets(statementBudgets []uint64) error {
	_, err := transactionBudget(statementBudgets)
	return err
}

func transactionBudget(statementBudgets []uint64) (uint64, error) {
	if len(statementBudgets) == 0 || len(statementBudgets) > MaxOperationsPerTransaction {
		return 0, fmt.Errorf("validate WAL transaction: operation count %d is outside 1..%d", len(statementBudgets), MaxOperationsPerTransaction)
	}
	var total uint64
	for index, budget := range statementBudgets {
		if budget == 0 || budget > StatementGrowthLimitBytes {
			return 0, fmt.Errorf("validate WAL transaction: statement %d budget %d is outside 1..%d", index, budget, StatementGrowthLimitBytes)
		}
		var ok bool
		total, ok = add(total, budget)
		if !ok || total > TransactionGrowthLimitBytes {
			return 0, fmt.Errorf("validate WAL transaction: total budget exceeds %d", TransactionGrowthLimitBytes)
		}
	}
	return total, nil
}

func ReaderContext(parent context.Context) (context.Context, context.CancelFunc) {
	return context.WithTimeout(parent, MaxReaderAge)
}

func add(left, right uint64) (uint64, bool) {
	if left > math.MaxUint64-right {
		return 0, false
	}
	return left + right, true
}
