package transfer

import (
	"context"
	"fmt"
	"github.com/TyrantLucifer/awesome-sftp-cli/internal/domain"
	"github.com/TyrantLucifer/awesome-sftp-cli/internal/testkit"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	providerapi "github.com/TyrantLucifer/awesome-sftp-cli/internal/provider"
)

func TestOrdinaryCopyCompletesWithoutDestinationReadbackOrForcedSync(t *testing.T) {
	f := newWorkerFixture(t, make([]byte, 128<<10), ConflictAsk)
	p := &completionCountingProvider{mutableTestProvider: f.destination}
	f.resolver[f.destination.Descriptor().ID] = p
	plan, _, err := NewPlanner(f.resolver).FreezeCopy(t.Context(), validFreezeRequest(f.plan.Source, f.plan.DestinationDirectory))
	if err != nil {
		t.Fatal(err)
	}
	journal := newMemoryJournal()
	result, err := NewWorker(f.resolver, journal).Execute(t.Context(), plan, nil)
	if err != nil || result.Outcome != OutcomeCompleted {
		t.Fatalf("copy = %#v, %v", result, err)
	}
	if p.reads.Load() != 0 || p.syncs.Load() != 0 {
		t.Errorf("ordinary copy issued %d readbacks and %d syncs", p.reads.Load(), p.syncs.Load())
	}
	if result.SHA256 != "" {
		t.Error("protocol confirmation advertised a verified destination checksum")
	}
	assertWorkerBytes(t, f.destination, result.Final, make([]byte, 128<<10))
}

func TestPlannerUsesProtocolConfirmationOnlyForOrdinaryCopy(t *testing.T) {
	f := newWorkerFixture(t, []byte("completion"), ConflictAsk)
	for _, cut := range []bool{false, true} {
		req := validFreezeRequest(f.plan.Source, f.plan.DestinationDirectory)
		if cut {
			req.Intent.Clipboard = ClipboardCut
		}
		plan, _, err := NewPlanner(f.resolver).FreezeCopy(t.Context(), req)
		if err != nil {
			t.Fatal(err)
		}
		want := Verification("protocol")
		if cut {
			want = VerifySHA256
		}
		if plan.Verification != want {
			t.Errorf("cut=%t: verification=%q, want %q", cut, plan.Verification, want)
		}
	}
}

type completionCountingProvider struct {
	mutableTestProvider
	reads, syncs, stats atomic.Uint64
}

func (p *completionCountingProvider) OpenRead(ctx context.Context, req providerapi.OpenReadRequest) (providerapi.ReadHandle, error) {
	p.reads.Add(1)
	return p.mutableTestProvider.OpenRead(ctx, req)
}
func (p *completionCountingProvider) OpenWrite(ctx context.Context, req providerapi.OpenWriteRequest) (providerapi.WriteHandle, error) {
	h, err := p.mutableTestProvider.OpenWrite(ctx, req)
	if err != nil {
		return nil, err
	}
	return &completionCountingHandle{WriteHandle: h, syncs: &p.syncs}, nil
}

type completionCountingHandle struct {
	providerapi.WriteHandle
	syncs *atomic.Uint64
}

func (h *completionCountingHandle) Sync(ctx context.Context) error {
	h.syncs.Add(1)
	return h.WriteHandle.Sync(ctx)
}

var _ providerapi.Provider = (*completionCountingProvider)(nil)

func TestCompletionPoliciesKeepAcknowledgedDurableAndVerifiedEvidenceSeparate(t *testing.T) {
	for _, verification := range []Verification{VerifyProtocol, VerifySHA256} {
		for _, durability := range []Durability{DurabilityNone, DurabilityCompletion, DurabilityCheckpoint} {
			t.Run(string(verification)+"/"+string(durability), func(t *testing.T) {
				f := newWorkerFixture(t, []byte("twenty bytes of data"), ConflictAsk)
				p := &completionCountingProvider{mutableTestProvider: f.destination}
				f.resolver[f.destination.Descriptor().ID] = p
				request := validFreezeRequest(f.plan.Source, f.plan.DestinationDirectory)
				request.Intent.Verification = verification
				request.Intent.Durability = durability
				plan, _, err := NewPlanner(f.resolver).FreezeCopy(t.Context(), request)
				if err != nil {
					t.Fatal(err)
				}
				plan.StreamPolicy.CheckpointBytes = 8
				journal := newMemoryJournal()
				journal.afterSave = func(c Checkpoint) {
					if c.Phase == PhaseStreaming && c.Offset > 0 && durability != DurabilityCheckpoint && p.syncs.Load() == 0 && c.durableBytes() != 0 {
						t.Error("candidate offset advertised as durable")
					}
				}
				result, err := NewWorker(f.resolver, journal).Execute(t.Context(), plan, nil)
				if err != nil {
					t.Fatal(err)
				}
				c := journal.latest()
				if c.Offset != uint64(len("twenty bytes of data")) {
					t.Fatalf("acknowledged=%d", c.Offset)
				}
				wantSyncs := uint64(0)
				if durability == DurabilityCompletion {
					wantSyncs = 1
				}
				if durability == DurabilityCheckpoint {
					wantSyncs = 4
				}
				if got := p.syncs.Load(); got != wantSyncs {
					t.Errorf("syncs=%d want %d", got, wantSyncs)
				}
				wantDurable := c.Offset
				if durability == DurabilityNone {
					wantDurable = 0
				}
				if c.durableBytes() != wantDurable {
					t.Errorf("durable=%d want %d", c.durableBytes(), wantDurable)
				}
				if verification == VerifyProtocol && (p.reads.Load() != 0 || result.SHA256 != "" || c.Completion.ContentVerified) {
					t.Error("protocol completion invented content proof")
				}
				if verification == VerifySHA256 && (p.reads.Load() != 2 || result.SHA256 == "") {
					t.Error("strict content verification missing")
				}
			})
		}
	}
}

func TestProtocolCopyStillProvesAmbiguousPublish(t *testing.T) {
	f := newWorkerFixture(t, []byte("lost publish acknowledgment"), ConflictAsk)
	p := &completionCountingProvider{mutableTestProvider: &renameResponseLostProvider{mutableTestProvider: f.destination}}
	f.resolver[f.destination.Descriptor().ID] = p
	plan, _, err := NewPlanner(f.resolver).FreezeCopy(t.Context(), validFreezeRequest(f.plan.Source, f.plan.DestinationDirectory))
	if err != nil {
		t.Fatal(err)
	}
	journal := newMemoryJournal()
	result, err := NewWorker(f.resolver, journal).Execute(t.Context(), plan, nil)
	if err != nil || result.Outcome != OutcomeCompleted {
		t.Fatalf("publish=%#v: %v", result, err)
	}
	if p.reads.Load() != 1 || !journal.latest().Completion.ContentVerified {
		t.Fatal("ambiguous publish did not require exactly one destination proof")
	}
}

func TestProtocolCopyRecoveryChecksCandidateBeforeAppending(t *testing.T) {
	for _, changed := range []bool{false, true} {
		t.Run(fmt.Sprint(changed), func(t *testing.T) {
			data := []byte("first-prefix-and-the-rest")
			f := newWorkerFixture(t, data, ConflictAsk)
			plan, _, err := NewPlanner(f.resolver).FreezeCopy(t.Context(), validFreezeRequest(f.plan.Source, f.plan.DestinationDirectory))
			if err != nil {
				t.Fatal(err)
			}
			plan.StreamPolicy.CheckpointBytes = 8
			journal := newMemoryJournal()
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			journal.afterSave = func(c Checkpoint) {
				if c.Phase == PhaseStreaming && c.Offset == 8 {
					cancel()
				}
			}
			if _, err := NewWorker(f.resolver, journal).Execute(ctx, plan, nil); err == nil {
				t.Fatal("missing injected interruption")
			}
			saved := journal.latest()
			if saved.Offset != 8 || saved.durableBytes() != 0 {
				t.Fatalf("candidate=%d durable=%d", saved.Offset, saved.durableBytes())
			}
			journal.afterSave = nil
			if changed {
				part := filepath.Join(f.destinationRoot, filepath.Base(string(plan.Part.Path)))
				if err := os.WriteFile(part, []byte("tampered"), 0600); err != nil {
					t.Fatal(err)
				}
			}
			p := &completionCountingProvider{mutableTestProvider: f.destination}
			f.resolver[f.destination.Descriptor().ID] = p
			result, err := NewWorker(f.resolver, journal).Execute(t.Context(), plan, nil)
			if changed {
				if !domain.IsCode(err, domain.CodeConflict) {
					t.Fatalf("changed candidate=%v", err)
				}
				assertWorkerBytes(t, f.destination, plan.Part, []byte("tampered"))
				if _, err := f.destination.Stat(t.Context(), providerapi.StatRequest{Location: plan.Final}); !domain.IsCode(err, domain.CodeNotFound) {
					t.Fatal("changed candidate was published")
				}
			} else {
				if err != nil || result.Outcome != OutcomeCompleted {
					t.Fatalf("resume=%#v: %v", result, err)
				}
				if p.reads.Load() == 0 {
					t.Fatal("candidate was reused without content proof")
				}
				assertWorkerBytes(t, f.destination, plan.Final, data)
			}
		})
	}
}

func TestExplicitRequireStrongStillForcesContentVerification(t *testing.T) {
	f := newWorkerFixture(t, []byte("explicit integrity choice"), ConflictAsk)
	request := validFreezeRequest(f.plan.Source, f.plan.DestinationDirectory)
	request.Intent.Verification = VerifyProtocol
	request.Intent.DirectPolicy.Integrity = domain.IntegrityRequireStrong
	plan, _, err := NewPlanner(f.resolver).FreezeCopy(t.Context(), request)
	if err != nil {
		t.Fatal(err)
	}
	if plan.Verification != VerifySHA256 {
		t.Fatal("explicit require_strong was weakened by the default completion policy")
	}
}

func TestProtocolRecoveryProvesTheRecordedAutoRenameDestination(t *testing.T) {
	f := newWorkerFixture(t, []byte("published bytes"), ConflictAutoRename)
	request := validFreezeRequest(f.plan.Source, f.plan.DestinationDirectory)
	request.Intent.ConflictPolicy = ConflictAutoRename
	plan, _, err := NewPlanner(f.resolver).FreezeCopy(t.Context(), request)
	if err != nil {
		t.Fatal(err)
	}
	journal := newMemoryJournal()
	journal.afterSave = func(c Checkpoint) {
		if c.Phase == PhaseTransferred {
			if err := os.WriteFile(filepath.Join(f.destinationRoot, filepath.Base(string(plan.Final.Path))), []byte("existing winner"), 0600); err != nil {
				t.Fatal(err)
			}
			journal.afterSave = nil
		}
	}
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	f.resolver[f.destination.Descriptor().ID] = &completionLostRenameProvider{mutableTestProvider: f.destination, cancel: cancel}
	if _, err := NewWorker(f.resolver, journal).Execute(ctx, plan, nil); err == nil {
		t.Fatal("missing interrupted publication")
	}
	saved := journal.latest()
	if saved.Phase != PhaseCommitting || saved.Final == plan.Final {
		t.Fatalf("recorded publication=%s %s", saved.Phase, saved.Final.Path)
	}
	f.resolver[f.destination.Descriptor().ID] = f.destination
	result, err := NewWorker(f.resolver, journal).Execute(t.Context(), plan, nil)
	if err != nil || result.Outcome != OutcomeCompleted || result.Final != saved.Final {
		t.Fatalf("recover exact published name=%#v: %v", result, err)
	}
	assertWorkerBytes(t, f.destination, plan.Final, []byte("existing winner"))
	assertWorkerBytes(t, f.destination, saved.Final, []byte("published bytes"))
}

type completionLostRenameProvider struct {
	mutableTestProvider
	cancel context.CancelFunc
}

func (p *completionLostRenameProvider) Rename(ctx context.Context, req providerapi.RenameRequest) (providerapi.RenameResult, error) {
	result, err := (&renameResponseLostProvider{mutableTestProvider: p.mutableTestProvider}).Rename(ctx, req)
	p.cancel()
	return result, err
}

func TestCandidateCheckpointsDoNotRequireRemoteMetadataRoundTrips(t *testing.T) {
	f := newWorkerFixture(t, []byte("checkpoint metadata budget"), ConflictAsk)
	p := &completionCountingProvider{mutableTestProvider: f.destination}
	f.resolver[f.destination.Descriptor().ID] = p
	plan, _, err := NewPlanner(f.resolver).FreezeCopy(t.Context(), validFreezeRequest(f.plan.Source, f.plan.DestinationDirectory))
	if err != nil {
		t.Fatal(err)
	}
	plan.StreamPolicy.CheckpointBytes = 8
	journal := newMemoryJournal()
	p.stats.Store(0)
	journal.afterSave = func(c Checkpoint) {
		if c.Phase == PhaseStreaming && c.Offset > 0 && p.stats.Load() != 1 {
			t.Errorf("checkpoint requires extra remote stat: %d", p.stats.Load())
		}
	}
	if _, err := NewWorker(f.resolver, journal).Execute(t.Context(), plan, nil); err != nil {
		t.Fatal(err)
	}
}

func (p *completionCountingProvider) Stat(ctx context.Context, request providerapi.StatRequest) (domain.Entry, error) {
	p.stats.Add(1)
	return p.mutableTestProvider.Stat(ctx, request)
}

func TestCandidateWindowBudgetRemainsBoundedWithSmallRatePackets(t *testing.T) {
	size := uint64(4096)
	plan := Plan{Durability: DurabilityNone, Source: FileRef{Fingerprint: domain.Fingerprint{Size: &size}}}
	worker := &Worker{scheduler: newTransferScheduler(t, testkit.NewManualClock(time.Unix(1, 0)), SchedulerPolicy{QuantumBytes: 4})}
	options := worker.streamReadOptions(plan, &size)
	if options.MaxRequestBytes != 4 || options.MaxBytes != 4 {
		t.Fatalf("small packet inflated file request window: %#v", options)
	}
	usage := windowedExecutionResourceUsage(Plan{Durability: DurabilityNone, Source: FileRef{Kind: domain.EntryDirectory}, StreamPolicy: StreamPolicy{DirectoryWorkers: 8}, BufferBytes: 4 << 20, SourceEndpoint: domain.Endpoint{ID: "source", Kind: domain.EndpointSSH}, DestinationEndpoint: domain.Endpoint{ID: "destination", Kind: domain.EndpointSSH}})
	if usage.Goroutines > HardResourceCeilings().Goroutines || usage.MemoryBytes > 8<<20 || usage.FileDescriptors < 22 {
		t.Fatalf("shared request budget admission=%+v", usage)
	}
}

func TestProtocolRouteEvidenceDoesNotClaimSHA256OrDurableProgress(t *testing.T) {
	f := newWorkerFixture(t, []byte("route evidence"), ConflictAsk)
	plan, _, err := NewPlanner(f.resolver).FreezeCopy(t.Context(), validFreezeRequest(f.plan.Source, f.plan.DestinationDirectory))
	if err != nil {
		t.Fatal(err)
	}
	if plan.RouteEvidence.Integrity.Algorithm != "" || plan.RouteEvidence.ProgressSemantics != "acknowledged_bytes" {
		t.Fatalf("misleading route evidence: %#v", plan.RouteEvidence)
	}
}

func TestCompletionEvidenceSurvivesSQLiteAndJobViews(t *testing.T) {
	f := newWorkerFixture(t, []byte("persistent acknowledgment"), ConflictAsk)
	plan, create, err := NewPlanner(f.resolver).FreezeCopy(t.Context(), validFreezeRequest(f.plan.Source, f.plan.DestinationDirectory))
	if err != nil {
		t.Fatal(err)
	}
	store, db := openTransferStore(t, t.Context(), testDatabasePath(t), true)
	defer db.Close()
	if _, _, err := store.Create(t.Context(), create); err != nil {
		t.Fatal(err)
	}
	journal := JobJournal{Store: store}
	if _, err := NewWorker(f.resolver, journal).Execute(t.Context(), plan, nil); err != nil {
		t.Fatal(err)
	}
	saved, err := journal.Load(t.Context(), plan.JobID)
	if err != nil {
		t.Fatal(err)
	}
	if saved == nil || saved.Completion.Version != 1 || saved.Offset == 0 || saved.durableBytes() != 0 || saved.Completion.ContentVerified {
		t.Fatalf("stored completion=%#v", saved)
	}
	manager, err := NewManager(ManagerConfig{Store: store, Resolver: f.resolver, Generator: &testkit.SequenceGenerator{}, MaxConcurrent: 1})
	if err != nil {
		t.Fatal(err)
	}
	defer manager.Close()
	views, err := manager.JobViews(t.Context(), 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(views) != 1 || views[0].AcknowledgedBytes != saved.Offset || views[0].DurableBytes != 0 || views[0].ContentVerified || views[0].Verification != VerifyProtocol || views[0].Durability != DurabilityNone {
		t.Fatalf("job views=%#v", views)
	}
}
