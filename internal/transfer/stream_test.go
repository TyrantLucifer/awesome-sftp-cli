package transfer

import (
	"bytes"
	"context"
	"errors"
	"github.com/TyrantLucifer/awesome-sftp-cli/internal/domain"
	"github.com/TyrantLucifer/awesome-sftp-cli/internal/foundation"
	providerapi "github.com/TyrantLucifer/awesome-sftp-cli/internal/provider"
	"github.com/TyrantLucifer/awesome-sftp-cli/internal/testkit"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"
)

func TestWorkerHighBandwidthLimitDoesNotShrinkCheckpoints(t *testing.T) {
	const size = 4 << 20
	fixture := newWorkerFixture(t, make([]byte, size), ConflictAsk)
	fixture.plan.Bandwidth.JobBytesPerSecond = 1 << 30
	journal := newMemoryJournal()
	scheduler, err := NewTransferScheduler(foundation.RealClock{}, SchedulerPolicy{})
	if err != nil {
		t.Fatal(err)
	}
	worker := NewWorker(fixture.resolver, journal)
	worker.scheduler = scheduler
	result, err := worker.Execute(context.Background(), fixture.plan, nil)
	if err != nil || result.Outcome != OutcomeCompleted {
		t.Fatalf("Execute() = (%#v, %v)", result, err)
	}
	checkpoint := journal.latest()
	if checkpoint.Performance == nil || checkpoint.Performance.Chunks != 1 {
		t.Fatalf("performance = %#v, want one durable checkpoint independent of rate quantum", checkpoint.Performance)
	}
}

func TestWorkerMetersDestinationVerificationReads(t *testing.T) {
	const size = 64 << 10
	fixture := newWorkerFixture(t, make([]byte, size), ConflictAsk)
	fixture.destination.(*endpointKindProvider).descriptor.Kind = domain.EndpointSSH
	fixture.destination.(*endpointKindProvider).descriptor.SSHHostAlias = "fixture"
	planner := NewPlanner(fixture.resolver)
	plan, _, err := planner.FreezeCopy(context.Background(), validFreezeRequest(fixture.plan.Source, fixture.plan.DestinationDirectory))
	if err != nil {
		t.Fatal(err)
	}
	scheduler, err := NewTransferScheduler(foundation.RealClock{}, SchedulerPolicy{})
	if err != nil {
		t.Fatal(err)
	}
	worker := NewWorker(fixture.resolver, newMemoryJournal())
	worker.scheduler = scheduler
	if _, err := worker.Execute(context.Background(), plan, nil); err != nil {
		t.Fatal(err)
	}
	if got := scheduler.Snapshot().GrantedBytes; got != 3*size {
		t.Fatalf("admitted bytes=%d, want upload plus both verification reads (%d)", got, 3*size)
	}
}

func TestPlannerFreezesIndependentStreamBudgets(t *testing.T) {
	fixture := newWorkerFixture(t, []byte("policy"), ConflictAsk)
	planner := NewPlanner(fixture.resolver)
	plan, _, err := planner.FreezeCopy(context.Background(), validFreezeRequest(fixture.plan.Source, fixture.plan.DestinationDirectory))
	if err != nil {
		t.Fatal(err)
	}
	if plan.StreamPolicy != DefaultStreamPolicy() {
		t.Fatalf("stream policy=%#v", plan.StreamPolicy)
	}
}

func TestWorkerRecoversMatchingUncheckpointedSuffix(t *testing.T) {
	data := bytes.Repeat([]byte("recoverable-stream"), 1<<18)
	fixture := newWorkerFixture(t, data, ConflictAsk)
	fixture.plan.StreamPolicy = DefaultStreamPolicy()
	journal := newMemoryJournal()
	fixture.resolver[fixture.destination.Descriptor().ID] = &interruptingWriteProvider{
		mutableTestProvider: fixture.destination, interruptAfter: 2 << 20,
	}
	_, err := NewWorker(fixture.resolver, journal).Execute(context.Background(), fixture.plan, nil)
	if err == nil {
		t.Fatal("missing injected interruption")
	}
	saved := journal.latest()
	if saved.Offset != 0 {
		t.Fatalf("durable offset=%d, want zero before first epoch", saved.Offset)
	}
	part, err := fixture.destination.Stat(context.Background(), providerapi.StatRequest{Location: fixture.plan.Part})
	if err != nil || part.Metadata.Size == nil || *part.Metadata.Size != 2<<20 {
		t.Fatalf("part=%#v,%v", part, err)
	}
	fixture.resolver[fixture.destination.Descriptor().ID] = fixture.destination
	result, err := NewWorker(fixture.resolver, journal).Execute(context.Background(), fixture.plan, nil)
	if err != nil || result.Outcome != OutcomeCompleted {
		t.Fatalf("resume=%#v,%v", result, err)
	}
	assertWorkerBytes(t, fixture.destination, fixture.plan.Final, data)
}

func TestWorkerRejectsChangedUncheckpointedSuffixWithoutTruncation(t *testing.T) {
	data := bytes.Repeat([]byte("matching-tail"), 1<<17)
	fixture := newWorkerFixture(t, data, ConflictAsk)
	fixture.plan.StreamPolicy = DefaultStreamPolicy()
	journal := newMemoryJournal()
	fixture.resolver[fixture.destination.Descriptor().ID] = &interruptingWriteProvider{mutableTestProvider: fixture.destination, interruptAfter: 1 << 20}
	if _, err := NewWorker(fixture.resolver, journal).Execute(context.Background(), fixture.plan, nil); err == nil {
		t.Fatal("missing interruption")
	}
	partPath := filepath.Join(fixture.destinationRoot, filepath.Base(string(fixture.plan.Part.Path)))
	changed := bytes.Repeat([]byte("x"), 1<<20)
	if err := os.WriteFile(partPath, changed, 0600); err != nil {
		t.Fatal(err)
	}
	fixture.resolver[fixture.destination.Descriptor().ID] = fixture.destination
	_, err := NewWorker(fixture.resolver, journal).Execute(context.Background(), fixture.plan, nil)
	if !domain.IsCode(err, domain.CodeConflict) {
		t.Fatalf("resume=%v", err)
	}
	after, err := os.ReadFile(partPath) //nolint:gosec // fixed Job-owned part inside this test temporary directory.
	if err != nil || !bytes.Equal(after, changed) {
		t.Fatal("unproved part was changed", err)
	}
}

func TestWorkerStreamPolicySeparatesMemoryAndCheckpointBudgets(t *testing.T) {
	fixture := newWorkerFixture(t, make([]byte, 12<<20), ConflictAsk)
	fixture.plan.StreamPolicy = DefaultStreamPolicy()
	fixture.plan.StreamPolicy.CheckpointIntervalMS = 5000
	journal := newMemoryJournal()
	result, err := NewWorker(fixture.resolver, journal).Execute(context.Background(), fixture.plan, nil)
	if err != nil || result.Outcome != OutcomeCompleted {
		t.Fatalf("Execute=%#v,%v", result, err)
	}
	if got := journal.latest().Performance.Chunks; got != 1 {
		t.Fatalf("checkpoints=%d, want one for a file below the independent durability budget", got)
	}
	if journal.maxBufferBytes > 4<<20 {
		t.Fatalf("buffer=%d", journal.maxBufferBytes)
	}
}

func TestStreamControlInterruptsBandwidthWaitBeforeCheckpoint(t *testing.T) {
	for _, action := range []ControlAction{ControlPause, ControlCancel} {
		t.Run(fmtControlAction(action), func(t *testing.T) {
			fixture := newWorkerFixture(t, make([]byte, 128<<10), ConflictAsk)
			fixture.plan.StreamPolicy = DefaultStreamPolicy()
			clock := testkit.NewManualClock(time.Unix(100, 0))
			scheduler := newTransferScheduler(t, clock, SchedulerPolicy{GlobalBytesPerSecond: 32 << 10, BurstBytes: 32 << 10, QuantumBytes: 32 << 10})
			journal := newMemoryJournal()
			worker := NewWorker(fixture.resolver, journal)
			worker.scheduler = scheduler
			var requested atomic.Uint32
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			done := make(chan error, 1)
			go func() {
				_, err := worker.Execute(ctx, fixture.plan, ControlFunc(func(Checkpoint) ControlAction {
					if requested.Load() == uint32(action) {
						return action
					}
					return ControlContinue
				}))
				done <- err
			}()
			waitForSchedulerGrantAndWaiter(t, scheduler, 32<<10, 1)
			requested.Store(uint32(action))
			expected := ErrPaused
			if action == ControlCancel {
				expected = ErrCanceled
			}
			select {
			case err := <-done:
				if !errors.Is(err, expected) {
					t.Fatalf("control returned %v, want %v", err, expected)
				}
			case <-time.After(time.Second):
				cancel()
				<-done
				t.Fatal("control waited for the blocked bandwidth admission")
			}
			if journal.latest().Offset != 0 {
				t.Fatal("uncheckpointed bytes were reported durable")
			}
			// The paused stream remains recoverable from its last proven checkpoint.
			if action == ControlPause {
				result, err := NewWorker(fixture.resolver, journal).Execute(context.Background(), fixture.plan, nil)
				if err != nil || result.Outcome != OutcomeCompleted {
					t.Fatalf("resume=%#v,%v", result, err)
				}
			}
		})
	}
}
func fmtControlAction(action ControlAction) string {
	if action == ControlPause {
		return "pause"
	}
	return "cancel"
}

func TestStreamPauseInterruptsRateLimitedVerification(t *testing.T) {
	fixture := newWorkerFixture(t, make([]byte, 32<<10), ConflictAsk)
	fixture.destination.(*endpointKindProvider).descriptor.Kind = domain.EndpointSSH
	fixture.destination.(*endpointKindProvider).descriptor.SSHHostAlias = "fixture"
	plan, _, err := NewPlanner(fixture.resolver).FreezeCopy(context.Background(), validFreezeRequest(fixture.plan.Source, fixture.plan.DestinationDirectory))
	if err != nil {
		t.Fatal(err)
	}
	clock := testkit.NewManualClock(time.Unix(100, 0))
	scheduler := newTransferScheduler(t, clock, SchedulerPolicy{GlobalBytesPerSecond: 32 << 10, BurstBytes: 32 << 10, QuantumBytes: 32 << 10})
	journal := newMemoryJournal()
	worker := NewWorker(fixture.resolver, journal)
	worker.scheduler = scheduler
	var pause atomic.Bool
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() {
		_, err := worker.Execute(ctx, plan, ControlFunc(func(Checkpoint) ControlAction {
			if pause.Load() {
				return ControlPause
			}
			return ControlContinue
		}))
		done <- err
	}()
	waitForSchedulerGrantAndWaiter(t, scheduler, 32<<10, 1)
	if journal.latest().Phase != PhaseTransferred {
		cancel()
		<-done
		t.Fatal("did not reach destination verification")
	}
	pause.Store(true)
	select {
	case err := <-done:
		if !errors.Is(err, ErrPaused) {
			t.Fatalf("pause=%v", err)
		}
	case <-time.After(time.Second):
		cancel()
		<-done
		t.Fatal("pause blocked during verification")
	}
	if result, err := NewWorker(fixture.resolver, journal).Execute(context.Background(), plan, nil); err != nil || result.Outcome != OutcomeCompleted {
		t.Fatalf("verification resume=%#v,%v", result, err)
	}
}

func TestWorkerReusesFullCommitProofAfterLostAcknowledgement(t *testing.T) {
	fixture := newWorkerFixture(t, []byte("published content"), ConflictAsk)
	counted := &verificationReadCounter{mutableTestProvider: &renameResponseLostProvider{mutableTestProvider: fixture.destination}, location: fixture.plan.Final}
	fixture.resolver[fixture.destination.Descriptor().ID] = counted
	result, err := NewWorker(fixture.resolver, newMemoryJournal()).Execute(context.Background(), fixture.plan, nil)
	if err != nil || result.Outcome != OutcomeCompleted {
		t.Fatalf("execute=%#v,%v", result, err)
	}
	if counted.reads != 1 {
		t.Fatalf("final readbacks=%d, want one complete SHA-256 proof", counted.reads)
	}
}

type verificationReadCounter struct {
	mutableTestProvider
	location domain.Location
	reads    int
}

func (p *verificationReadCounter) OpenRead(ctx context.Context, request providerapi.OpenReadRequest) (providerapi.ReadHandle, error) {
	if request.Location == p.location {
		p.reads++
	}
	return p.mutableTestProvider.OpenRead(ctx, request)
}

func TestStreamPolicyRejectsExpandedOrIncompleteBudgets(t *testing.T) {
	for _, policy := range []StreamPolicy{
		{CheckpointBytes: 65 << 20, CheckpointIntervalMS: 1000, DirectoryWorkers: 2},
		{CheckpointBytes: 64 << 20, CheckpointIntervalMS: 5001, DirectoryWorkers: 2},
		{CheckpointBytes: 64 << 20, CheckpointIntervalMS: 1000, DirectoryWorkers: 3},
		{CheckpointBytes: 64 << 20},
	} {
		fixture := newWorkerFixture(t, []byte("bounded"), ConflictAsk)
		fixture.plan.StreamPolicy = policy
		journal := newMemoryJournal()
		if _, err := NewWorker(fixture.resolver, journal).Execute(context.Background(), fixture.plan, nil); err == nil {
			t.Fatalf("accepted invalid stream policy: %+v", policy)
		}
		saved, err := journal.Load(context.Background(), fixture.plan.JobID)
		if err != nil || saved != nil {
			t.Fatal("invalid policy reached execution", err)
		}
	}
}
