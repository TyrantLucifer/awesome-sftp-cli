package transfer

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/TyrantLucifer/awesome-mac-sftp/internal/domain"
	providerapi "github.com/TyrantLucifer/awesome-mac-sftp/internal/provider"
	"github.com/TyrantLucifer/awesome-mac-sftp/internal/state/jobstore"
)

func TestWorkerPublishesOnlyAfterDestinationVerification(t *testing.T) {
	fixture := newWorkerFixture(t, []byte("durable payload"), ConflictAsk)
	fixture.plan.BufferBytes = 4
	journal := newMemoryJournal()
	journal.afterSave = func(checkpoint Checkpoint) {
		if checkpoint.Phase != PhaseVerified {
			return
		}
		if _, err := fixture.destination.Stat(context.Background(), providerapi.StatRequest{Location: fixture.plan.Final}); !domain.IsCode(err, domain.CodeNotFound) {
			t.Fatalf("final became visible before commit: %v", err)
		}
		if _, err := fixture.destination.Stat(context.Background(), providerapi.StatRequest{Location: fixture.plan.Part}); err != nil {
			t.Fatalf("verified part is missing: %v", err)
		}
	}

	result, err := NewWorker(fixture.resolver, journal).Execute(context.Background(), fixture.plan, nil)
	if err != nil {
		t.Fatalf("Execute(): %v", err)
	}
	if result.Outcome != OutcomeCompleted || result.Bytes != uint64(len("durable payload")) || result.SHA256 == "" {
		t.Fatalf("result = %#v", result)
	}
	assertWorkerBytes(t, fixture.destination, result.Final, []byte("durable payload"))
	if _, err := fixture.destination.Stat(context.Background(), providerapi.StatRequest{Location: fixture.plan.Part}); !domain.IsCode(err, domain.CodeNotFound) {
		t.Fatalf("part remains after commit: %v", err)
	}
	if got := journal.latest().Phase; got != PhaseCommitted {
		t.Fatalf("latest checkpoint phase = %q, want committed", got)
	}
	if journal.maxBufferBytes > int(fixture.plan.BufferBytes) {
		t.Fatalf("observed buffer = %d, budget = %d", journal.maxBufferBytes, fixture.plan.BufferBytes)
	}
}

func TestWorkerPauseAndResumeUsesDurableOffsetAndChecksumState(t *testing.T) {
	fixture := newWorkerFixture(t, []byte("0123456789abcdef"), ConflictAsk)
	fixture.plan.BufferBytes = 4
	journal := newMemoryJournal()
	control := ControlFunc(func(checkpoint Checkpoint) ControlAction {
		if checkpoint.Offset >= 4 {
			return ControlPause
		}
		return ControlContinue
	})
	worker := NewWorker(fixture.resolver, journal)
	_, err := worker.Execute(context.Background(), fixture.plan, control)
	if !errors.Is(err, ErrPaused) {
		t.Fatalf("paused Execute() error = %v, want ErrPaused", err)
	}
	paused := journal.latest()
	if paused.Offset != 4 || len(paused.ChecksumState) == 0 || paused.Phase != PhaseStreaming {
		t.Fatalf("paused checkpoint = %#v", paused)
	}
	if _, err := fixture.destination.Stat(context.Background(), providerapi.StatRequest{Location: fixture.plan.Part}); err != nil {
		t.Fatalf("part missing while paused: %v", err)
	}

	result, err := worker.Execute(context.Background(), fixture.plan, nil)
	if err != nil {
		t.Fatalf("resume Execute(): %v", err)
	}
	if result.Outcome != OutcomeCompleted {
		t.Fatalf("resume result = %#v", result)
	}
	assertWorkerBytes(t, fixture.destination, fixture.plan.Final, []byte("0123456789abcdef"))
}

func TestWorkerCancelPreservesPartAndAuditableCheckpoint(t *testing.T) {
	fixture := newWorkerFixture(t, []byte("cancel payload"), ConflictAsk)
	fixture.plan.BufferBytes = 4
	journal := newMemoryJournal()
	control := ControlFunc(func(checkpoint Checkpoint) ControlAction {
		if checkpoint.Offset >= 4 {
			return ControlCancel
		}
		return ControlContinue
	})
	_, err := NewWorker(fixture.resolver, journal).Execute(context.Background(), fixture.plan, control)
	if !errors.Is(err, ErrCanceled) {
		t.Fatalf("canceled Execute() error = %v, want ErrCanceled", err)
	}
	if journal.latest().Offset != 4 {
		t.Fatalf("cancel checkpoint = %#v", journal.latest())
	}
	if _, err := fixture.destination.Stat(context.Background(), providerapi.StatRequest{Location: fixture.plan.Part}); err != nil {
		t.Fatalf("cancel removed resumable part: %v", err)
	}
	if _, err := fixture.destination.Stat(context.Background(), providerapi.StatRequest{Location: fixture.plan.Final}); !domain.IsCode(err, domain.CodeNotFound) {
		t.Fatalf("cancel exposed final: %v", err)
	}
}

func TestWorkerConflictPoliciesAreRecheckedAtCommit(t *testing.T) {
	tests := []struct {
		name          string
		policy        ConflictPolicy
		wantOutcome   Outcome
		wantFinalData string
		wantName      string
		wantPart      bool
	}{
		{name: "ask", policy: ConflictAsk, wantOutcome: OutcomeWaitingConflict, wantFinalData: "racer", wantPart: true},
		{name: "skip", policy: ConflictSkip, wantOutcome: OutcomeSkipped, wantFinalData: "racer"},
		{name: "overwrite", policy: ConflictOverwrite, wantOutcome: OutcomeCompleted, wantFinalData: "payload"},
		{name: "auto rename", policy: ConflictAutoRename, wantOutcome: OutcomeCompleted, wantFinalData: "racer", wantName: "/final (1)"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			fixture := newWorkerFixture(t, []byte("payload"), test.policy)
			journal := newMemoryJournal()
			journal.afterSave = func(checkpoint Checkpoint) {
				if checkpoint.Phase != PhaseVerified {
					return
				}
				handle, err := fixture.destination.OpenWrite(context.Background(), providerapi.OpenWriteRequest{
					Location:    fixture.plan.Final,
					Disposition: providerapi.WriteCreateNew,
				})
				if err != nil {
					t.Fatalf("create racing final: %v", err)
				}
				writeWorkerAll(t, handle, []byte("racer"))
				if err := handle.Close(context.Background()); err != nil {
					t.Fatal(err)
				}
				journal.afterSave = nil
			}
			result, err := NewWorker(fixture.resolver, journal).Execute(context.Background(), fixture.plan, nil)
			if err != nil {
				t.Fatalf("Execute(): %v", err)
			}
			if result.Outcome != test.wantOutcome {
				t.Fatalf("outcome = %q, want %q", result.Outcome, test.wantOutcome)
			}
			assertWorkerBytes(t, fixture.destination, fixture.plan.Final, []byte(test.wantFinalData))
			if test.wantName != "" {
				if result.Final.Path != domain.CanonicalPath(test.wantName) {
					t.Fatalf("auto-rename final = %q, want %q", result.Final.Path, test.wantName)
				}
				assertWorkerBytes(t, fixture.destination, result.Final, []byte("payload"))
			}
			_, partErr := fixture.destination.Stat(context.Background(), providerapi.StatRequest{Location: fixture.plan.Part})
			if test.wantPart && partErr != nil {
				t.Fatalf("part missing: %v", partErr)
			}
			if !test.wantPart && !domain.IsCode(partErr, domain.CodeNotFound) {
				t.Fatalf("part remains: %v", partErr)
			}
		})
	}
}

type workerFixture struct {
	plan        Plan
	create      jobstore.CreateRequest
	resolver    MapResolver
	source      providerapi.Provider
	destination mutableTestProvider
}

type mutableTestProvider interface {
	providerapi.Provider
	providerapi.MutableProvider
}

func newWorkerFixture(t *testing.T, data []byte, policy ConflictPolicy) workerFixture {
	t.Helper()
	sourceRoot := t.TempDir()
	destinationRoot := t.TempDir()
	if err := os.WriteFile(filepath.Join(sourceRoot, "source"), data, 0o600); err != nil {
		t.Fatal(err)
	}
	source := newPlanTestProvider(t, "ep_aaaaaaaaaaaaaaaaaaaaaaaaaa", sourceRoot, domain.EndpointLocal)
	destination := newPlanTestProvider(t, "ep_bbbbbbbbbbbbbbbbbbbbbbbbbb", destinationRoot, domain.EndpointLocal)
	resolver := MapResolver{source.Descriptor().ID: source, destination.Descriptor().ID: destination}
	planner := NewPlanner(resolver)
	reference, err := planner.Capture(context.Background(), normalizePlanTest(t, source, "/source"))
	if err != nil {
		t.Fatal(err)
	}
	request := validFreezeRequest(reference, normalizePlanTest(t, destination, "/"))
	request.Intent.ConflictPolicy = policy
	plan, create, err := planner.FreezeCopy(context.Background(), request)
	if err != nil {
		t.Fatal(err)
	}
	return workerFixture{plan: plan, create: create, resolver: resolver, source: source, destination: destination}
}

type memoryJournal struct {
	mu             sync.Mutex
	checkpoints    []Checkpoint
	afterSave      func(Checkpoint)
	maxBufferBytes int
}

func newMemoryJournal() *memoryJournal { return &memoryJournal{} }

func (journal *memoryJournal) Load(context.Context, domain.JobID) (*Checkpoint, error) {
	journal.mu.Lock()
	defer journal.mu.Unlock()
	if len(journal.checkpoints) == 0 {
		return nil, nil
	}
	checkpoint := cloneCheckpoint(journal.checkpoints[len(journal.checkpoints)-1])
	return &checkpoint, nil
}

func (journal *memoryJournal) Save(_ context.Context, checkpoint Checkpoint) error {
	journal.mu.Lock()
	journal.checkpoints = append(journal.checkpoints, cloneCheckpoint(checkpoint))
	callback := journal.afterSave
	journal.mu.Unlock()
	if callback != nil {
		callback(checkpoint)
	}
	return nil
}

func (journal *memoryJournal) ObserveBuffer(bytes int) {
	journal.mu.Lock()
	defer journal.mu.Unlock()
	if bytes > journal.maxBufferBytes {
		journal.maxBufferBytes = bytes
	}
}

func (journal *memoryJournal) latest() Checkpoint {
	journal.mu.Lock()
	defer journal.mu.Unlock()
	return cloneCheckpoint(journal.checkpoints[len(journal.checkpoints)-1])
}

func assertWorkerBytes(t *testing.T, implementation interface {
	OpenRead(context.Context, providerapi.OpenReadRequest) (providerapi.ReadHandle, error)
}, location domain.Location, want []byte) {
	t.Helper()
	handle, err := implementation.OpenRead(context.Background(), providerapi.OpenReadRequest{Location: location})
	if err != nil {
		t.Fatalf("OpenRead(%q): %v", location.Path, err)
	}
	defer func() { _ = handle.Close(context.Background()) }()
	buffer := make([]byte, len(want)+8)
	n, err := handle.Read(context.Background(), buffer)
	if err != nil && !errors.Is(err, os.ErrClosed) {
		// LocalFS may return EOF with data; that is a valid final read.
		if n == 0 {
			t.Fatalf("Read(%q): %v", location.Path, err)
		}
	}
	if string(buffer[:n]) != string(want) {
		t.Fatalf("read %q = %q, want %q", location.Path, buffer[:n], want)
	}
}

func writeWorkerAll(t *testing.T, handle providerapi.WriteHandle, data []byte) {
	t.Helper()
	for len(data) != 0 {
		n, err := handle.Write(context.Background(), data)
		if err != nil {
			t.Fatal(err)
		}
		if n == 0 {
			t.Fatal("write made no progress")
		}
		data = data[n:]
	}
}

var _ = time.Time{}
