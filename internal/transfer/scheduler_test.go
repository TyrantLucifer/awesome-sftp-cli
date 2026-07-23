package transfer

import (
	"bytes"
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/TyrantLucifer/awesome-sftp-cli/internal/domain"
	"github.com/TyrantLucifer/awesome-sftp-cli/internal/foundation"
	providerapi "github.com/TyrantLucifer/awesome-sftp-cli/internal/provider"
	"github.com/TyrantLucifer/awesome-sftp-cli/internal/testkit"
)

func TestHardResourceCeilingsAreExactAndCannotBeExpanded(t *testing.T) {
	t.Parallel()

	hard := HardResourceCeilings()
	want := ResourceLimits{
		ActiveJobs:             16,
		ActiveJobsPerEndpoint:  8,
		QueuedJobs:             512,
		Endpoints:              64,
		Connections:            32,
		ConnectionsPerEndpoint: 4,
		SSHProcesses:           32,
		HelperProcesses:        4,
		FileDescriptors:        512,
		Goroutines:             256,
		MemoryBytes:            64 << 20,
		EventBytes:             32 << 10,
		LogBytes:               32 << 10,
	}
	if hard != want {
		t.Fatalf("hard ceilings = %+v, want %+v", hard, want)
	}

	tightened := hard
	tightened.ActiveJobs = 4
	tightened.MemoryBytes = 8 << 20
	if got, err := TightenResourceLimits(tightened); err != nil || got != tightened {
		t.Fatalf("tighten = (%+v, %v), want (%+v, nil)", got, err, tightened)
	}

	expanded := hard
	expanded.FileDescriptors++
	if _, err := TightenResourceLimits(expanded); !errors.Is(err, ErrResourceLimitExpansion) {
		t.Fatalf("expansion error = %v, want ErrResourceLimitExpansion", err)
	}
}

func TestTransferSchedulerAllowsReadAheadOnlyWithoutRateControl(t *testing.T) {
	request := BandwidthRequest{
		JobID: "job-read-ahead", EndpointID: "source", PeerEndpointID: "destination", Class: ScheduleBulk,
		Bytes: TransferScheduleQuantum,
	}
	tests := []struct {
		name    string
		policy  SchedulerPolicy
		request BandwidthRequest
		want    bool
	}{
		{name: "unlimited", request: request, want: true},
		{name: "global rate", policy: SchedulerPolicy{GlobalBytesPerSecond: 1}, request: request},
		{name: "endpoint rate", policy: SchedulerPolicy{EndpointBytesPerSecond: 1}, request: request},
		{name: "default job rate", policy: SchedulerPolicy{JobBytesPerSecond: 1}, request: request},
		{name: "frozen job rate", request: func() BandwidthRequest {
			rated := request
			rated.JobBytesPerSecond = 1
			return rated
		}()},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			scheduler := newTransferScheduler(t, foundation.NewManualClock(time.Unix(1_000, 0)), test.policy)
			if got := scheduler.AllowsReadAhead(test.request); got != test.want {
				t.Fatalf("AllowsReadAhead() = %t, want %t", got, test.want)
			}
		})
	}
}

func TestResourceLedgerCountsGlobalEndpointAndJobUsageAndReclaimsIt(t *testing.T) {
	limits := HardResourceCeilings()
	limits.ActiveJobs = 2
	limits.ActiveJobsPerEndpoint = 1
	limits.Connections = 2
	limits.ConnectionsPerEndpoint = 1
	limits.SSHProcesses = 1
	limits.HelperProcesses = 1
	limits.FileDescriptors = 4
	limits.Goroutines = 4
	limits.MemoryBytes = 1024
	ledger, err := NewResourceLedger(limits)
	if err != nil {
		t.Fatal(err)
	}

	request := ResourceRequest{
		JobID: domain.JobID("job-a"),
		EndpointIDs: []domain.EndpointID{
			domain.EndpointID("endpoint-a"), domain.EndpointID("endpoint-b"),
		},
		Usage: ResourceUsage{
			ActiveJobs: 1, Connections: 1, SSHProcesses: 1, HelperProcesses: 1,
			FileDescriptors: 2, Goroutines: 1, MemoryBytes: 512,
		},
	}
	lease, err := ledger.TryAcquire(request)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := ledger.TryAcquire(ResourceRequest{
		JobID: domain.JobID("job-b"), EndpointIDs: []domain.EndpointID{domain.EndpointID("endpoint-a")},
		Usage: ResourceUsage{ActiveJobs: 1},
	}); !errors.Is(err, ErrResourceExhausted) {
		t.Fatalf("same-endpoint admission error = %v, want ErrResourceExhausted", err)
	}

	snapshot := ledger.Snapshot()
	if snapshot.Total != request.Usage || snapshot.PerEndpoint[domain.EndpointID("endpoint-a")].ActiveJobs != 1 || snapshot.PerJob[request.JobID] != request.Usage {
		t.Fatalf("resource snapshot = %+v", snapshot)
	}
	lease.Release()
	lease.Release()
	snapshot = ledger.Snapshot()
	if snapshot.Total != (ResourceUsage{}) || len(snapshot.PerEndpoint) != 0 || len(snapshot.PerJob) != 0 {
		t.Fatalf("released resource snapshot = %+v, want empty", snapshot)
	}
}

func TestResourceLedgerCanceledWaiterDoesNotLeakAdmission(t *testing.T) {
	limits := HardResourceCeilings()
	limits.ActiveJobs = 1
	ledger, err := NewResourceLedger(limits)
	if err != nil {
		t.Fatal(err)
	}
	lease, err := ledger.TryAcquire(ResourceRequest{
		JobID: domain.JobID("job-a"), Usage: ResourceUsage{ActiveJobs: 1},
	})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		_, err := ledger.Acquire(ctx, ResourceRequest{
			JobID: domain.JobID("job-b"), Usage: ResourceUsage{ActiveJobs: 1},
		})
		done <- err
	}()
	cancel()
	if err := <-done; !errors.Is(err, context.Canceled) {
		t.Fatalf("Acquire() error = %v, want canceled", err)
	}
	lease.Release()
	if snapshot := ledger.Snapshot(); snapshot.Total != (ResourceUsage{}) || snapshot.Waiters != 0 {
		t.Fatalf("canceled resource snapshot = %+v, want empty", snapshot)
	}
}

func TestResourceLedgerRejectsPermanentlyImpossibleWaitWithoutBlocking(t *testing.T) {
	limits := HardResourceCeilings()
	limits.ActiveJobs = 1
	ledger, err := NewResourceLedger(limits)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	_, err = ledger.Acquire(ctx, ResourceRequest{
		JobID: domain.JobID("job-too-large"), Usage: ResourceUsage{ActiveJobs: 2},
	})
	if !errors.Is(err, ErrResourceExhausted) {
		t.Fatalf("Acquire(impossible) error = %v, want ErrResourceExhausted", err)
	}
}

func TestFreezeRequestRejectsBandwidthAboveFrozenHardCeiling(t *testing.T) {
	request := validFreezeRequest(FileRef{
		Kind: domain.EntryFile,
		Location: domain.Location{
			EndpointID: domain.EndpointID("ep_aaaaaaaaaaaaaaaaaaaaaaaaaa"),
			Path:       "/source",
		},
	}, domain.Location{
		EndpointID: domain.EndpointID("ep_bbbbbbbbbbbbbbbbbbbbbbbbbb"),
		Path:       "/",
	})
	request.Intent.Bandwidth = BandwidthPolicy{
		Required:          true,
		JobBytesPerSecond: MaxBandwidthBytesPerSecond + 1,
	}

	if err := validateFreezeRequest(request); err == nil {
		t.Fatal("validateFreezeRequest() error = nil, want bandwidth hard-ceiling rejection")
	}
}

func TestTransferSchedulerUsesLayeredIntegerTokenBuckets(t *testing.T) {
	clock := foundation.NewManualClock(time.Unix(1_000, 0))
	scheduler := newTransferScheduler(t, clock, SchedulerPolicy{
		GlobalBytesPerSecond:   8,
		EndpointBytesPerSecond: 4,
		JobBytesPerSecond:      2,
		BurstBytes:             8,
		QuantumBytes:           2,
	})
	request := BandwidthRequest{
		JobID:      domain.JobID("job-a"),
		EndpointID: domain.EndpointID("endpoint-a"),
		Class:      ScheduleBulk,
		Bytes:      2,
	}

	if err := scheduler.Wait(context.Background(), request); err != nil {
		t.Fatalf("initial grant: %v", err)
	}
	done := waitForGrant(t, scheduler, request)
	assertNoGrant(t, done)

	clock.Advance(999 * time.Millisecond)
	assertNoGrant(t, done)
	clock.Advance(time.Millisecond)
	assertGrant(t, done)

	snapshot := scheduler.Snapshot()
	if snapshot.GrantedBytes != 4 || snapshot.Waiters != 0 {
		t.Fatalf("snapshot = %+v, want 4 granted bytes and zero waiters", snapshot)
	}
}

func TestTransferSchedulerAccumulatesLowRateTokensUpToOneBoundedQuantum(t *testing.T) {
	clock := foundation.NewManualClock(time.Unix(1_500, 0))
	scheduler := newTransferScheduler(t, clock, SchedulerPolicy{BurstBytes: 8, QuantumBytes: 4})
	done := waitForGrant(t, scheduler, BandwidthRequest{
		JobID: domain.JobID("slow-job"), Class: ScheduleBulk, Bytes: 4, JobBytesPerSecond: 2,
	})
	assertNoGrant(t, done)
	clock.Advance(999 * time.Millisecond)
	assertNoGrant(t, done)
	clock.Advance(time.Millisecond)
	assertGrant(t, done)
}

func TestTransferSchedulerWeightedRoundRobinNeverStarvesBulk(t *testing.T) {
	clock := foundation.NewManualClock(time.Unix(2_000, 0))
	scheduler := newTransferScheduler(t, clock, SchedulerPolicy{
		GlobalBytesPerSecond: 1,
		BurstBytes:           1,
		QuantumBytes:         1,
		InteractiveWeight:    4,
		BulkWeight:           1,
	})

	if err := scheduler.Wait(context.Background(), BandwidthRequest{
		JobID: domain.JobID("prime"), Class: ScheduleBulk, Bytes: 1,
	}); err != nil {
		t.Fatalf("consume initial burst: %v", err)
	}

	grants := make(chan string, 5)
	enqueueGrant(t, scheduler, grants, "interactive-1", ScheduleInteractive)
	enqueueGrant(t, scheduler, grants, "interactive-2", ScheduleInteractive)
	enqueueGrant(t, scheduler, grants, "interactive-3", ScheduleInteractive)
	enqueueGrant(t, scheduler, grants, "interactive-4", ScheduleInteractive)
	enqueueGrant(t, scheduler, grants, "bulk-1", ScheduleBulk)

	want := []string{"interactive-1", "interactive-2", "interactive-3", "interactive-4", "bulk-1"}
	for index, expected := range want {
		waitForManualClockTimerAtOrBefore(t, clock, clock.Now().Add(time.Second))
		clock.Advance(time.Second)
		waitForSchedulerGrantAndWaiter(t, scheduler, uint64(index+2), len(want)-index-1)
		select {
		case got := <-grants:
			if got != expected {
				t.Fatalf("grant %d = %q, want %q", index, got, expected)
			}
		case <-time.After(time.Second):
			t.Fatalf("grant %d timed out; snapshot=%+v", index, scheduler.Snapshot())
		}
	}
}

func TestTransferSchedulerGrantableInteractiveBypassesRateLimitedBulkHead(t *testing.T) {
	clock := foundation.NewManualClock(time.Unix(2_500, 0))
	scheduler := newTransferScheduler(t, clock, SchedulerPolicy{BurstBytes: 8, QuantumBytes: 8})
	bulk := waitForGrant(t, scheduler, BandwidthRequest{
		JobID: "slow-bulk", Class: ScheduleBulk, Bytes: 8, JobBytesPerSecond: 1,
	})
	assertNoGrant(t, bulk)

	scheduler.mu.Lock()
	scheduler.cycleIndex = len(scheduler.cycle) - 1
	scheduler.mu.Unlock()
	interactive := waitForGrant(t, scheduler, BandwidthRequest{
		JobID: "interactive", Class: ScheduleInteractive, Bytes: 1,
	})
	assertGrant(t, interactive)
	assertNoGrant(t, bulk)
}

func TestTransferSchedulerHotRateUpdateOnlyAffectsFutureTokens(t *testing.T) {
	clock := foundation.NewManualClock(time.Unix(3_000, 0))
	scheduler := newTransferScheduler(t, clock, SchedulerPolicy{
		GlobalBytesPerSecond: 4,
		BurstBytes:           8,
		QuantumBytes:         8,
	})
	if err := scheduler.Wait(context.Background(), BandwidthRequest{
		JobID: domain.JobID("prime"), Class: ScheduleBulk, Bytes: 8,
	}); err != nil {
		t.Fatalf("consume initial burst: %v", err)
	}

	clock.Advance(250 * time.Millisecond)
	if err := scheduler.Update(SchedulerPolicy{
		GlobalBytesPerSecond: 8,
		BurstBytes:           8,
		QuantumBytes:         8,
	}); err != nil {
		t.Fatalf("update: %v", err)
	}
	done := waitForGrant(t, scheduler, BandwidthRequest{
		JobID: domain.JobID("future"), Class: ScheduleBulk, Bytes: 3,
	})
	assertNoGrant(t, done)
	clock.Advance(249 * time.Millisecond)
	assertNoGrant(t, done)
	clock.Advance(time.Millisecond)
	assertGrant(t, done)
}

func TestTransferSchedulerHotUpdateTightensExplicitJobBucketCapacity(t *testing.T) {
	clock := foundation.NewManualClock(time.Unix(3_250, 0))
	scheduler := newTransferScheduler(t, clock, SchedulerPolicy{BurstBytes: 8, QuantumBytes: 8})
	request := BandwidthRequest{JobID: "explicit-rate", Class: ScheduleBulk, Bytes: 8, JobBytesPerSecond: 8}
	if err := scheduler.Wait(context.Background(), request); err != nil {
		t.Fatalf("initial explicit-rate grant: %v", err)
	}
	if err := scheduler.Update(SchedulerPolicy{BurstBytes: 4, QuantumBytes: 4}); err != nil {
		t.Fatalf("tighten policy: %v", err)
	}
	scheduler.mu.Lock()
	capacity := scheduler.jobs[request.JobID].capacity
	rate := scheduler.jobs[request.JobID].rate
	scheduler.mu.Unlock()
	if capacity != 4 || rate != request.JobBytesPerSecond {
		t.Fatalf("explicit Job bucket rate/capacity = %d/%d, want %d/4", rate, capacity, request.JobBytesPerSecond)
	}
}

func TestTransferSchedulerRejectsHotUpdateThatWouldStrandQueuedGrant(t *testing.T) {
	clock := foundation.NewManualClock(time.Unix(3_500, 0))
	scheduler := newTransferScheduler(t, clock, SchedulerPolicy{
		GlobalBytesPerSecond: 8,
		BurstBytes:           8,
		QuantumBytes:         8,
	})
	request := BandwidthRequest{JobID: "job-hot-update", Class: ScheduleBulk, Bytes: 8}
	if err := scheduler.Wait(context.Background(), request); err != nil {
		t.Fatalf("consume initial burst: %v", err)
	}
	done := waitForGrant(t, scheduler, request)
	if err := scheduler.Update(SchedulerPolicy{
		GlobalBytesPerSecond: 8,
		BurstBytes:           4,
		QuantumBytes:         4,
	}); !errors.Is(err, ErrSchedulerUpdateStrandsWaiter) {
		t.Fatalf("tightening update error = %v, want ErrSchedulerUpdateStrandsWaiter", err)
	}
	if scheduler.Snapshot().Policy.BurstBytes != 8 {
		t.Fatalf("rejected update changed policy: %+v", scheduler.Snapshot().Policy)
	}
	clock.Advance(time.Second)
	assertGrant(t, done)
}

func TestTransferSchedulerReleaseJobReclaimsUnusedIdentityBuckets(t *testing.T) {
	scheduler := newTransferScheduler(t, foundation.NewManualClock(time.Unix(3_750, 0)), SchedulerPolicy{})
	for _, jobID := range []domain.JobID{"job-release-a", "job-release-b"} {
		if err := scheduler.Wait(context.Background(), BandwidthRequest{
			JobID: jobID, EndpointID: "endpoint-shared", PeerEndpointID: "endpoint-peer", Class: ScheduleBulk, Bytes: 1,
		}); err != nil {
			t.Fatalf("grant %s: %v", jobID, err)
		}
	}
	scheduler.ReleaseJob("job-release-a")
	if _, ok := scheduler.jobs["job-release-a"]; ok {
		t.Fatal("released job bucket remains")
	}
	if len(scheduler.endpoints) != 2 {
		t.Fatalf("shared endpoint buckets after first release = %d, want 2", len(scheduler.endpoints))
	}
	scheduler.ReleaseJob("job-release-b")
	if len(scheduler.jobs) != 0 || len(scheduler.jobRates) != 0 || len(scheduler.endpoints) != 0 {
		t.Fatalf("identity buckets after release = jobs:%d rates:%d endpoints:%d", len(scheduler.jobs), len(scheduler.jobRates), len(scheduler.endpoints))
	}
}

func TestTransferSchedulerCancellationRemovesWaiter(t *testing.T) {
	clock := foundation.NewManualClock(time.Unix(4_000, 0))
	scheduler := newTransferScheduler(t, clock, SchedulerPolicy{
		GlobalBytesPerSecond: 1,
		BurstBytes:           1,
		QuantumBytes:         1,
	})
	if err := scheduler.Wait(context.Background(), BandwidthRequest{
		JobID: domain.JobID("prime"), Class: ScheduleBulk, Bytes: 1,
	}); err != nil {
		t.Fatalf("consume initial burst: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	done := waitForGrant(t, scheduler, BandwidthRequest{
		JobID: domain.JobID("cancel"), Class: ScheduleBulk, Bytes: 1,
	}, ctx)
	cancel()
	if err := <-done; !errors.Is(err, context.Canceled) {
		t.Fatalf("wait error = %v, want context.Canceled", err)
	}
	if snapshot := scheduler.Snapshot(); snapshot.Waiters != 0 {
		t.Fatalf("waiters after cancel = %d, want 0", snapshot.Waiters)
	}
}

func TestPlannerDisablesUncontrolledFastPathWhenBandwidthControlIsRequired(t *testing.T) {
	root := t.TempDir()
	if err := os.Mkdir(filepath.Join(root, "destination"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "source"), []byte("controlled route"), 0o600); err != nil {
		t.Fatal(err)
	}
	base := newPlanTestProvider(t, "ep_aaaaaaaaaaaaaaaaaaaaaaaaaa", root, domain.EndpointSSH)
	implementation := &routeServerCopyProvider{endpointKindProvider: base, root: root, advertise: true}
	planner := NewPlanner(MapResolver{implementation.Descriptor().ID: implementation})
	reference, err := planner.Capture(context.Background(), normalizePlanTest(t, implementation, "/source"))
	if err != nil {
		t.Fatal(err)
	}
	request := validFreezeRequest(reference, normalizePlanTest(t, implementation, "/destination"))
	request.Intent.Bandwidth = BandwidthPolicy{Required: true, JobBytesPerSecond: 1 << 20}
	plan, _, err := planner.FreezeCopy(context.Background(), request)
	if err != nil {
		t.Fatal(err)
	}
	if plan.Route != RouteSFTPRelay {
		t.Fatalf("route = %q, want controlled relay", plan.Route)
	}
	if plan.Bandwidth != request.Intent.Bandwidth {
		t.Fatalf("frozen bandwidth = %+v, want %+v", plan.Bandwidth, request.Intent.Bandwidth)
	}
	if plan.RouteEvidence.BandwidthControl != BandwidthControlled {
		t.Fatalf("bandwidth control = %q, want %q", plan.RouteEvidence.BandwidthControl, BandwidthControlled)
	}
	assertRouteCandidateJSON(t, routeEvidenceJSON(t, plan), "sftp_server_copy", "bandwidth_control_required")
	if implementation.copyCalls != 0 {
		t.Fatalf("Planner executed server copy %d time(s)", implementation.copyCalls)
	}
}

func TestRelayWorkerAppliesSchedulerAtFixedSizeIndependentQuantum(t *testing.T) {
	data := bytes.Repeat([]byte("q"), TransferScheduleQuantum+1)
	fixture := newWorkerFixture(t, data, ConflictOverwrite)
	fixture.plan.Bandwidth = BandwidthPolicy{Required: true, JobBytesPerSecond: TransferScheduleQuantum}
	freezeRouteEvidence(&fixture.plan)

	clock := foundation.NewManualClock(time.Unix(5_000, 0))
	scheduler := newTransferScheduler(t, clock, SchedulerPolicy{
		GlobalBytesPerSecond: TransferScheduleQuantum,
		BurstBytes:           TransferScheduleQuantum,
	})
	worker := NewWorker(fixture.resolver, newMemoryJournal())
	worker.scheduler = scheduler

	type execution struct {
		result Result
		err    error
	}
	done := make(chan execution, 1)
	go func() {
		result, err := worker.Execute(context.Background(), fixture.plan, nil)
		done <- execution{result: result, err: err}
	}()
	waitForSchedulerGrantAndWaiter(t, scheduler, TransferScheduleQuantum, 1)
	clock.Advance(time.Second)

	select {
	case got := <-done:
		if got.err != nil {
			t.Fatalf("execute: %v", got.err)
		}
		if got.result.Bytes != uint64(len(data)) {
			t.Fatalf("result bytes = %d, want %d", got.result.Bytes, len(data))
		}
	case <-time.After(3 * time.Second):
		t.Fatalf("relay did not finish; scheduler=%+v", scheduler.Snapshot())
	}
	if snapshot := scheduler.Snapshot(); snapshot.GrantedBytes != uint64(len(data)) || snapshot.Waiters != 0 {
		t.Fatalf("final scheduler snapshot = %+v", snapshot)
	}
}

func TestWorkerHundredGiBContractLifecycleUsesSameCheckpointHashAndRateStateMachine(t *testing.T) {
	data := bytes.Repeat([]byte("sparse-shaped\x00"), (2*TransferScheduleQuantum)/14)
	data = append(data, make([]byte, 2*TransferScheduleQuantum-len(data))...)
	fixture := newWorkerFixture(t, data, ConflictOverwrite)
	fixture.plan.Bandwidth = BandwidthPolicy{Required: true, JobBytesPerSecond: TransferScheduleQuantum}
	freezeRouteEvidence(&fixture.plan)

	clock := foundation.NewManualClock(time.Unix(5_250, 0))
	scheduler := newTransferScheduler(t, clock, SchedulerPolicy{
		GlobalBytesPerSecond: TransferScheduleQuantum,
		BurstBytes:           TransferScheduleQuantum,
	})
	journal := newMemoryJournal()
	firstWorker := NewWorker(fixture.resolver, journal)
	firstWorker.scheduler = scheduler
	_, err := firstWorker.Execute(context.Background(), fixture.plan, ControlFunc(func(checkpoint Checkpoint) ControlAction {
		if checkpoint.Offset >= TransferScheduleQuantum {
			return ControlPause
		}
		return ControlContinue
	}))
	if !errors.Is(err, ErrPaused) {
		t.Fatalf("pause error = %v, want ErrPaused", err)
	}
	paused := journal.latest()
	if paused.Offset != TransferScheduleQuantum || len(paused.ChecksumState) == 0 || paused.Phase != PhaseStreaming {
		t.Fatalf("paused checkpoint = %#v", paused)
	}

	type execution struct {
		result Result
		err    error
	}
	done := make(chan execution, 1)
	restartedWorker := NewWorker(fixture.resolver, journal)
	restartedWorker.scheduler = scheduler
	go func() {
		result, executeErr := restartedWorker.Execute(context.Background(), fixture.plan, nil)
		done <- execution{result: result, err: executeErr}
	}()
	waitForSchedulerGrantAndWaiter(t, scheduler, TransferScheduleQuantum, 1)
	clock.Advance(time.Second)

	select {
	case got := <-done:
		if got.err != nil {
			t.Fatalf("restart resume: %v", got.err)
		}
		wantSHA256 := fmt.Sprintf("%x", sha256.Sum256(data))
		if got.result.Outcome != OutcomeCompleted || got.result.Bytes != uint64(len(data)) || got.result.SHA256 != wantSHA256 {
			t.Fatalf("restart resume result = %#v, want bytes=%d sha256=%s", got.result, len(data), wantSHA256)
		}
	case <-time.After(3 * time.Second):
		t.Fatalf("restart resume did not finish; scheduler=%+v", scheduler.Snapshot())
	}
	if snapshot := scheduler.Snapshot(); snapshot.GrantedBytes != uint64(len(data)) || snapshot.Waiters != 0 {
		t.Fatalf("final scheduler snapshot = %+v", snapshot)
	}
	assertWorkerBytes(t, fixture.destination, fixture.plan.Final, data)
}

func TestRelayWorkerHonorsTightenedSchedulerQuantum(t *testing.T) {
	fixture := newWorkerFixture(t, []byte("123456789"), ConflictOverwrite)
	scheduler := newTransferScheduler(t, foundation.NewManualClock(time.Unix(5_500, 0)), SchedulerPolicy{
		BurstBytes: 4, QuantumBytes: 4,
	})
	worker := NewWorker(fixture.resolver, newMemoryJournal())
	worker.scheduler = scheduler
	result, err := worker.Execute(context.Background(), fixture.plan, nil)
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	if result.Bytes != 9 || scheduler.Snapshot().GrantedBytes != 9 {
		t.Fatalf("result/scheduled bytes = %d/%d, want 9/9", result.Bytes, scheduler.Snapshot().GrantedBytes)
	}
}

func TestDirectoryWorkerAppliesSchedulerToFileChildren(t *testing.T) {
	sourceRoot := t.TempDir()
	destinationRoot := t.TempDir()
	if err := os.MkdirAll(filepath.Join(sourceRoot, "tree"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(sourceRoot, "tree", "payload"), []byte("12345"), 0o600); err != nil {
		t.Fatal(err)
	}
	source := newPlanTestProvider(t, "ep_aaaaaaaaaaaaaaaaaaaaaaaaaa", sourceRoot, domain.EndpointSSH)
	destination := newPlanTestProvider(t, "ep_bbbbbbbbbbbbbbbbbbbbbbbbbb", destinationRoot, domain.EndpointSSH)
	resolver := MapResolver{source.Descriptor().ID: source, destination.Descriptor().ID: destination}
	planner := NewPlanner(resolver)
	reference, err := planner.Capture(context.Background(), normalizePlanTest(t, source, "/tree"))
	if err != nil {
		t.Fatal(err)
	}
	request := validFreezeRequest(reference, normalizePlanTest(t, destination, "/"))
	request.Intent.Name = "copied"
	plan, _, err := planner.FreezeCopy(context.Background(), request)
	if err != nil {
		t.Fatal(err)
	}
	plan.BufferBytes = 5

	clock := foundation.NewManualClock(time.Unix(5_750, 0))
	scheduler := newTransferScheduler(t, clock, SchedulerPolicy{
		GlobalBytesPerSecond: 4,
		BurstBytes:           4,
		QuantumBytes:         4,
	})
	worker := NewWorker(resolver, newMemoryJournal())
	worker.scheduler = scheduler

	type execution struct {
		result Result
		err    error
	}
	done := make(chan execution, 1)
	go func() {
		result, executeErr := worker.Execute(context.Background(), plan, nil)
		done <- execution{result: result, err: executeErr}
	}()
	waitForSchedulerGrantAndWaiter(t, scheduler, 4, 1)
	clock.Advance(time.Second)

	select {
	case got := <-done:
		if got.err != nil {
			t.Fatalf("execute directory: %v", got.err)
		}
		if got.result.Bytes != 5 {
			t.Fatalf("result bytes = %d, want 5", got.result.Bytes)
		}
	case <-time.After(3 * time.Second):
		t.Fatalf("directory transfer did not finish; scheduler=%+v", scheduler.Snapshot())
	}
	if snapshot := scheduler.Snapshot(); snapshot.GrantedBytes != 5 || snapshot.Waiters != 0 {
		t.Fatalf("final scheduler snapshot = %+v", snapshot)
	}
}

func TestManagerOwnsOneSharedHotUpdatableSchedulerWithinHardAdmissionCeilings(t *testing.T) {
	fixture := newWorkerFixture(t, []byte("manager scheduler"), ConflictOverwrite)
	store, database := openTransferStore(t, context.Background(), filepath.Join(t.TempDir(), "state.db"), true)
	t.Cleanup(func() { _ = database.Close() })
	clock := foundation.NewManualClock(time.Unix(6_000, 0))
	manager, err := NewManager(ManagerConfig{
		Store: store, Resolver: fixture.resolver, Generator: &testkit.SequenceGenerator{},
		MaxConcurrent: 4, MaxQueued: 128, SchedulerClock: clock,
		SchedulerPolicy: SchedulerPolicy{GlobalBytesPerSecond: 4, BurstBytes: 8, QuantumBytes: 8},
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(manager.Close)
	if got := manager.SchedulerSnapshot().Policy.GlobalBytesPerSecond; got != 4 {
		t.Fatalf("initial global rate = %d, want 4", got)
	}
	if err := manager.UpdateBandwidthPolicy(SchedulerPolicy{GlobalBytesPerSecond: 8, BurstBytes: 8, QuantumBytes: 8}); err != nil {
		t.Fatalf("hot update: %v", err)
	}
	if got := manager.SchedulerSnapshot().Policy.GlobalBytesPerSecond; got != 8 {
		t.Fatalf("updated global rate = %d, want 8", got)
	}

	_, err = NewManager(ManagerConfig{
		Store: store, Resolver: fixture.resolver, Generator: &testkit.SequenceGenerator{},
		MaxConcurrent: int(HardResourceCeilings().ActiveJobs) + 1,
	})
	if !errors.Is(err, ErrResourceLimitExpansion) {
		t.Fatalf("expanded manager admission error = %v, want ErrResourceLimitExpansion", err)
	}
}

func TestManagerQueueUsesSharedResourceLedgerAndReleasesOnDequeue(t *testing.T) {
	fixture := newWorkerFixture(t, []byte("queued"), ConflictOverwrite)
	store, database := openTransferStore(t, context.Background(), filepath.Join(t.TempDir(), "state.db"), true)
	t.Cleanup(func() { _ = database.Close() })
	manager, err := NewManager(ManagerConfig{
		Store: store, Resolver: fixture.resolver, Generator: &testkit.SequenceGenerator{},
		MaxConcurrent: 1, MaxQueued: 2,
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(manager.Close)
	jobID := domain.JobID("job-queued-resource")
	if err := manager.enqueue(jobID); err != nil {
		t.Fatal(err)
	}
	if got := manager.SchedulerSnapshot().Resources.Total.QueuedJobs; got != 1 {
		t.Fatalf("queued resource usage = %d, want 1", got)
	}
	if dequeued := <-manager.queue; dequeued != jobID {
		t.Fatalf("dequeued Job = %q, want %q", dequeued, jobID)
	}
	manager.releaseQueueLease(jobID)
	if got := manager.SchedulerSnapshot().Resources.Total.QueuedJobs; got != 0 {
		t.Fatalf("queued resource usage after dequeue = %d, want 0", got)
	}
}

func TestExecutionResourceUsageAccountsSSHConnectionsAndDirectHelper(t *testing.T) {
	plan := Plan{
		Route:               RouteHelperSameHost,
		SourceEndpoint:      domain.Endpoint{ID: "endpoint-a", Kind: domain.EndpointSSH},
		DestinationEndpoint: domain.Endpoint{ID: "endpoint-b", Kind: domain.EndpointSSH},
		BufferBytes:         4096,
	}
	usage := executionResourceUsage(plan)
	if usage.ActiveJobs != 1 || usage.Connections != 2 || usage.SSHProcesses != 2 ||
		usage.HelperProcesses != 1 || usage.FileDescriptors != 8 || usage.Goroutines != 2 || usage.MemoryBytes != 4096 {
		t.Fatalf("execution resource usage = %+v", usage)
	}
}

func TestExecutionResourceUsageAccountsBoundedSFTPReadAhead(t *testing.T) {
	t.Parallel()

	usage := executionResourceUsage(Plan{
		Route:          RouteSFTPRelay,
		BufferBytes:    4096,
		SourceEndpoint: domain.Endpoint{Kind: domain.EndpointSSH},
	})
	wantGoroutines := 2 + providerapi.MaxReadAheadBytes/(32<<10) + 2
	if usage.Goroutines != wantGoroutines {
		t.Fatalf("Goroutines = %d, want %d", usage.Goroutines, wantGoroutines)
	}
	wantMemory := uint64(4096 + providerapi.MaxReadAheadBytes)
	if usage.MemoryBytes != wantMemory {
		t.Fatalf("MemoryBytes = %d, want %d", usage.MemoryBytes, wantMemory)
	}
}

func TestExecutionResourceRequestChargesOneConnectionToEachEndpoint(t *testing.T) {
	plan := Plan{
		JobID:               "job-two-endpoints",
		SourceEndpoint:      domain.Endpoint{ID: "endpoint-a", Kind: domain.EndpointSSH},
		DestinationEndpoint: domain.Endpoint{ID: "endpoint-b", Kind: domain.EndpointSSH},
		BufferBytes:         4096,
	}
	request := executionResourceRequest(plan)
	if request.Usage.Connections != 2 {
		t.Fatalf("global connections = %d, want 2", request.Usage.Connections)
	}
	for endpointID, usage := range request.EndpointUsage {
		if usage.Connections != 1 || usage.ActiveJobs != 1 {
			t.Fatalf("endpoint %s usage = %+v, want one connection and one active Job", endpointID, usage)
		}
	}
	limits := HardResourceCeilings()
	limits.ConnectionsPerEndpoint = 1
	ledger, err := NewResourceLedger(limits)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := ledger.TryAcquire(request); err != nil {
		t.Fatalf("two-endpoint Job with one connection per endpoint: %v", err)
	}
}

func TestExecutionResourceRequestDoesNotChargeConnectionsToLocalEndpoints(t *testing.T) {
	plan := Plan{
		JobID:               "job-local-endpoints",
		SourceEndpoint:      domain.Endpoint{ID: "endpoint-local-a", Kind: domain.EndpointLocal},
		DestinationEndpoint: domain.Endpoint{ID: "endpoint-local-b", Kind: domain.EndpointLocal},
		BufferBytes:         4096,
	}
	request := executionResourceRequest(plan)
	if request.Usage.Connections != 0 {
		t.Fatalf("global connections = %d, want 0", request.Usage.Connections)
	}
	limits := HardResourceCeilings()
	limits.ConnectionsPerEndpoint = 0
	ledger, err := NewResourceLedger(limits)
	if err != nil {
		t.Fatal(err)
	}
	lease, err := ledger.TryAcquire(request)
	if err != nil {
		t.Fatalf("local-only Job with zero per-endpoint connection budget: %v", err)
	}
	defer lease.Release()
	got := ledger.Snapshot()
	for endpointID, usage := range got.PerEndpoint {
		if usage.Connections != 0 || usage.ActiveJobs != 1 {
			t.Fatalf("endpoint %s usage = %+v, want one active Job and zero connections", endpointID, usage)
		}
	}
}

func newTransferScheduler(t *testing.T, clock foundation.Clock, policy SchedulerPolicy) *TransferScheduler {
	t.Helper()
	scheduler, err := NewTransferScheduler(clock, policy)
	if err != nil {
		t.Fatalf("new scheduler: %v", err)
	}
	return scheduler
}

func waitForGrant(
	t *testing.T,
	scheduler *TransferScheduler,
	request BandwidthRequest,
	contexts ...context.Context,
) <-chan error {
	t.Helper()
	ctx := context.Background()
	if len(contexts) > 0 {
		ctx = contexts[0]
	}
	done := make(chan error, 1)
	go func() { done <- scheduler.Wait(ctx, request) }()
	waitForSchedulerWaiters(t, scheduler, 1)
	return done
}

func enqueueGrant(
	t *testing.T,
	scheduler *TransferScheduler,
	grants chan<- string,
	name string,
	class ScheduleClass,
) {
	t.Helper()
	before := scheduler.Snapshot().Waiters
	go func() {
		err := scheduler.Wait(context.Background(), BandwidthRequest{
			JobID: domain.JobID(name), Class: class, Bytes: 1,
		})
		if err != nil {
			grants <- "error:" + err.Error()
			return
		}
		grants <- name
	}()
	waitForSchedulerWaiters(t, scheduler, before+1)
}

func waitForSchedulerWaiters(t *testing.T, scheduler *TransferScheduler, want int) {
	t.Helper()
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		if scheduler.Snapshot().Waiters == want {
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatalf("scheduler waiters = %d, want %d", scheduler.Snapshot().Waiters, want)
}

func waitForSchedulerGrantAndWaiter(t *testing.T, scheduler *TransferScheduler, bytes uint64, waiters int) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		snapshot := scheduler.Snapshot()
		if snapshot.GrantedBytes == bytes && snapshot.Waiters == waiters {
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatalf("scheduler snapshot = %+v, want %d granted bytes and %d waiters", scheduler.Snapshot(), bytes, waiters)
}

func waitForManualClockTimerAtOrBefore(t *testing.T, clock *foundation.ManualClock, want time.Time) {
	t.Helper()
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		if next, ok := clock.NextTimerDeadline(); ok && !next.After(want) {
			return
		}
		time.Sleep(time.Millisecond)
	}
	next, ok := clock.NextTimerDeadline()
	t.Fatalf("manual clock next timer = %s (present=%t), want at or before %s", next, ok, want)
}

func assertNoGrant(t *testing.T, done <-chan error) {
	t.Helper()
	select {
	case err := <-done:
		t.Fatalf("unexpected grant: %v", err)
	default:
	}
}

func assertGrant(t *testing.T, done <-chan error) {
	t.Helper()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("grant error: %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("grant timed out")
	}
}
