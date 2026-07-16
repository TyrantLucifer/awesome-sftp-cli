package wal

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestControllerFreezesOnlineWALLimitsAndPressure(t *testing.T) {
	t.Parallel()

	if StatementGrowthLimitBytes != 4<<20 || TransactionGrowthLimitBytes != 8<<20 || SoftLimitBytes != 64<<20 || HardStopBytes != 256<<20 || AbsoluteCeilingBytes != 264<<20 || MaxOperationsPerTransaction != 256 || MaxReaderAge != 2*time.Second {
		t.Fatalf("online WAL constants changed")
	}
	if _, err := NewController(AbsoluteCeilingBytes + 1); err == nil {
		t.Fatal("NewController(above absolute ceiling) error = nil")
	}
	controller, err := NewController(SoftLimitBytes)
	if err != nil {
		t.Fatalf("NewController(): %v", err)
	}
	pressure := controller.Pressure()
	if !pressure.PauseNewReaders || !pressure.PauseJobClaims || pressure.StopWrites || pressure.ReadOnly {
		t.Fatalf("soft pressure = %#v", pressure)
	}
	if err := controller.ObserveCheckpoint(HardStopBytes); err != nil {
		t.Fatalf("ObserveCheckpoint(hard): %v", err)
	}
	pressure = controller.Pressure()
	if !pressure.StopWrites || pressure.ReadOnly {
		t.Fatalf("hard pressure = %#v", pressure)
	}
	if err := controller.ObserveCheckpoint(AbsoluteCeilingBytes + 1); err == nil {
		t.Fatal("ObserveCheckpoint(above absolute) error = nil")
	}
	if !controller.Pressure().ReadOnly {
		t.Fatal("absolute-ceiling violation did not latch read-only")
	}
}

func TestControllerReservationsAndTransactionBudgetsAreBounded(t *testing.T) {
	t.Parallel()

	controller, err := NewController(0)
	if err != nil {
		t.Fatalf("NewController(): %v", err)
	}
	if err := controller.Reserve("worker-a", StatementGrowthLimitBytes); err != nil {
		t.Fatalf("Reserve(worker-a): %v", err)
	}
	if err := controller.Reserve("worker-b", StatementGrowthLimitBytes); err != nil {
		t.Fatalf("Reserve(worker-b): %v", err)
	}
	if err := controller.Reserve("worker-c", 1); err == nil {
		t.Fatal("Reserve(above global 8 MiB) error = nil")
	}
	if err := controller.Reserve("worker-a", 1); err == nil {
		t.Fatal("Reserve(duplicate worker) error = nil")
	}
	if err := controller.Release("worker-a"); err != nil {
		t.Fatalf("Release(worker-a): %v", err)
	}
	if err := controller.Release("worker-a"); err == nil {
		t.Fatal("Release(missing worker) error = nil")
	}
	if err := ValidateTransactionBudgets([]uint64{StatementGrowthLimitBytes, StatementGrowthLimitBytes}); err != nil {
		t.Fatalf("ValidateTransactionBudgets(exact): %v", err)
	}
	if err := controller.PreflightTransaction([]uint64{1024, 2048}); err != nil {
		t.Fatalf("PreflightTransaction(): %v", err)
	}
	if err := ValidateTransactionBudgets([]uint64{StatementGrowthLimitBytes + 1}); err == nil {
		t.Fatal("ValidateTransactionBudgets(statement overflow) error = nil")
	}
	tooMany := make([]uint64, MaxOperationsPerTransaction+1)
	for index := range tooMany {
		tooMany[index] = 1
	}
	if err := ValidateTransactionBudgets(tooMany); err == nil {
		t.Fatal("ValidateTransactionBudgets(operation overflow) error = nil")
	}

	nearHard, err := NewController(HardStopBytes - 1)
	if err != nil {
		t.Fatalf("NewController(near hard): %v", err)
	}
	if err := nearHard.Reserve("worker", 1); err == nil {
		t.Fatal("Reserve(reaches hard stop) error = nil")
	}
	if err := nearHard.PreflightTransaction([]uint64{1}); err == nil {
		t.Fatal("PreflightTransaction(reaches hard stop) error = nil")
	}
}

func TestControllerObservesActualGrowthAndCommittedViolation(t *testing.T) {
	t.Parallel()

	controller, err := NewController(1024)
	if err != nil {
		t.Fatalf("NewController(): %v", err)
	}
	if err := controller.ObserveWrite(1024, 2048, 1024, false); err != nil {
		t.Fatalf("ObserveWrite(exact): %v", err)
	}
	err = controller.ObserveWrite(2048, 4097, 2048, true)
	var violation *GrowthViolation
	if !errors.As(err, &violation) || !violation.Committed || violation.ActualBytes != 2049 || violation.DeclaredBytes != 2048 {
		t.Fatalf("ObserveWrite(committed violation) error = %#v", err)
	}
	if got := controller.Snapshot().WALBytes; got != 4097 {
		t.Fatalf("WAL bytes after violation = %d", got)
	}
	if err := controller.ObserveBoundary(4097, 4097+TransactionGrowthLimitBytes, TransactionGrowthLimitBytes, true); err != nil {
		t.Fatalf("ObserveBoundary(exact transaction remainder): %v", err)
	}
	if err := controller.ObserveWrite(100, 99, 1, false); err == nil {
		t.Fatal("ObserveWrite(stale before size) error = nil")
	}
}

func TestReaderContextHasFixedTwoSecondDeadline(t *testing.T) {
	t.Parallel()

	now := time.Now()
	ctx, cancel := ReaderContext(context.Background())
	defer cancel()
	deadline, ok := ctx.Deadline()
	if !ok {
		t.Fatal("ReaderContext() has no deadline")
	}
	if got := deadline.Sub(now); got < MaxReaderAge-100*time.Millisecond || got > MaxReaderAge+100*time.Millisecond {
		t.Fatalf("reader deadline delta = %s", got)
	}
}
