package transfer

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
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

func TestWorkerCopiesDirectoryTreeWithBoundedRelayAndNoSymlinkTraversal(t *testing.T) {
	sourceRoot := t.TempDir()
	destinationRoot := t.TempDir()
	if err := os.MkdirAll(filepath.Join(sourceRoot, "tree", "nested"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(sourceRoot, "tree", "root.txt"), []byte("root"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(sourceRoot, "tree", "nested", "child.txt"), []byte("child"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("../root.txt", filepath.Join(sourceRoot, "tree", "nested", "link")); err != nil {
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
	plan.BufferBytes = 3
	journal := newMemoryJournal()
	result, err := NewWorker(resolver, journal).Execute(context.Background(), plan, nil)
	if err != nil {
		t.Fatalf("Execute(directory): %v", err)
	}
	if result.Outcome != OutcomeCompleted || result.Items != 4 || result.Bytes != 9 {
		t.Fatalf("directory result = %#v", result)
	}
	for relative, want := range map[string]string{"root.txt": "root", "nested/child.txt": "child"} {
		// #nosec G304 -- the relative names are fixed test literals below a private destination root.
		data, err := os.ReadFile(filepath.Join(destinationRoot, "copied", filepath.FromSlash(relative)))
		if err != nil || string(data) != want {
			t.Fatalf("copied %s = %q, %v", relative, data, err)
		}
	}
	if _, err := os.Lstat(filepath.Join(destinationRoot, "copied", "nested", "link")); !os.IsNotExist(err) {
		t.Fatalf("symlink was copied or followed: %v", err)
	}
	if journal.maxBufferBytes > 2*int(plan.BufferBytes) {
		t.Fatalf("observed directory relay buffer = %d, ceiling = %d", journal.maxBufferBytes, 2*plan.BufferBytes)
	}
	if matches, err := filepath.Glob(filepath.Join(destinationRoot, "copied", "**", "*.part-*")); err != nil || len(matches) != 0 {
		t.Fatalf("part residue = %v, %v", matches, err)
	}
}

func TestWorkerResumesDirectoryAfterAbruptStopWithoutConflictingWithOwnedRoot(t *testing.T) {
	sourceRoot := t.TempDir()
	destinationRoot := t.TempDir()
	if err := os.MkdirAll(filepath.Join(sourceRoot, "tree"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(sourceRoot, "tree", "payload"), []byte("restart-safe"), 0o600); err != nil {
		t.Fatal(err)
	}
	source := newPlanTestProvider(t, "ep_aaaaaaaaaaaaaaaaaaaaaaaaaa", sourceRoot, domain.EndpointLocal)
	destination := newPlanTestProvider(t, "ep_bbbbbbbbbbbbbbbbbbbbbbbbbb", destinationRoot, domain.EndpointLocal)
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
	started := make(chan struct{})
	release := make(chan struct{})
	resolver[source.Descriptor().ID] = &gatedReadProvider{Provider: source, started: started, release: release}
	journal := newMemoryJournal()
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		_, executeErr := NewWorker(resolver, journal).Execute(ctx, plan, nil)
		done <- executeErr
	}()
	select {
	case <-started:
	case <-time.After(5 * time.Second):
		t.Fatal("directory worker did not reach file read")
	}
	cancel()
	if err := <-done; !errors.Is(err, context.Canceled) {
		t.Fatalf("abrupt directory stop error = %v, want canceled", err)
	}
	resolver[source.Descriptor().ID] = source
	result, err := NewWorker(resolver, journal).Execute(context.Background(), plan, nil)
	if err != nil {
		t.Fatalf("resume directory: %v", err)
	}
	if result.Outcome != OutcomeCompleted || result.Items != 1 || result.Bytes != uint64(len("restart-safe")) {
		t.Fatalf("resumed directory result = %#v", result)
	}
	// #nosec G304 -- path is fixed below this test's private destination root.
	data, err := os.ReadFile(filepath.Join(destinationRoot, "copied", "payload"))
	if err != nil || string(data) != "restart-safe" {
		t.Fatalf("resumed destination = %q, %v", data, err)
	}
}

func TestWorkerRefreshesCheckpointAfterPauseClosesWriteHandle(t *testing.T) {
	fixture := newWorkerFixture(t, []byte("close-finalizes-mtime"), ConflictAsk)
	fixture.plan.BufferBytes = 4
	destination := &closeTimestampProvider{mutableTestProvider: fixture.destination, root: fixture.destinationRoot}
	fixture.resolver[fixture.destination.Descriptor().ID] = destination
	journal := newMemoryJournal()
	control := ControlFunc(func(checkpoint Checkpoint) ControlAction {
		if checkpoint.Offset >= 4 {
			return ControlPause
		}
		return ControlContinue
	})
	if _, err := NewWorker(fixture.resolver, journal).Execute(context.Background(), fixture.plan, control); !errors.Is(err, ErrPaused) {
		t.Fatalf("paused Execute() error = %v, want ErrPaused", err)
	}
	result, err := NewWorker(fixture.resolver, journal).Execute(context.Background(), fixture.plan, nil)
	if err != nil {
		t.Fatalf("resumed Execute(): %v", err)
	}
	if result.Outcome != OutcomeCompleted {
		t.Fatalf("resumed result = %#v", result)
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

func TestWorkerProvesCommitAfterRenameResponseIsLost(t *testing.T) {
	fixture := newWorkerFixture(t, []byte("committed-before-disconnect"), ConflictAsk)
	destination := &renameResponseLostProvider{mutableTestProvider: fixture.destination}
	fixture.resolver[fixture.destination.Descriptor().ID] = destination

	result, err := NewWorker(fixture.resolver, newMemoryJournal()).Execute(context.Background(), fixture.plan, nil)
	if err != nil {
		t.Fatalf("Execute(): %v", err)
	}
	if result.Outcome != OutcomeCompleted {
		t.Fatalf("result = %#v", result)
	}
	assertWorkerBytes(t, fixture.destination, fixture.plan.Final, []byte("committed-before-disconnect"))
}

func TestWorkerHandlesShortWritesWithinFixedBufferBudget(t *testing.T) {
	fixture := newWorkerFixture(t, []byte("short-write-payload"), ConflictAsk)
	fixture.plan.BufferBytes = 7
	fixture.resolver[fixture.source.Descriptor().ID] = &shortReadProvider{Provider: fixture.source, maxRead: 3}
	destination := &shortWriteProvider{mutableTestProvider: fixture.destination, maxWrite: 2}
	fixture.resolver[fixture.destination.Descriptor().ID] = destination
	journal := newMemoryJournal()

	result, err := NewWorker(fixture.resolver, journal).Execute(context.Background(), fixture.plan, nil)
	if err != nil || result.Outcome != OutcomeCompleted {
		t.Fatalf("short-write Execute() = (%#v, %v)", result, err)
	}
	if journal.maxBufferBytes > int(fixture.plan.BufferBytes) {
		t.Fatalf("observed buffer = %d, budget = %d", journal.maxBufferBytes, fixture.plan.BufferBytes)
	}
	assertWorkerBytes(t, fixture.destination, fixture.plan.Final, []byte("short-write-payload"))
}

func TestWorkerHundredGiBSyntheticSourceStopsAtBoundedCheckpoint(t *testing.T) {
	fixture := newWorkerFixture(t, []byte("placeholder"), ConflictAsk)
	const size = uint64(100 * 1024 * 1024 * 1024)
	fileID := "synthetic-100g"
	fixture.plan.Source.Fingerprint = domain.Fingerprint{Size: uint64Pointer(size), FileID: &fileID}
	fixture.plan.BufferBytes = 64 * 1024
	fixture.resolver[fixture.source.Descriptor().ID] = &syntheticSparseProvider{
		Provider: fixture.source,
		entry: domain.Entry{
			Location:    fixture.plan.Source.Location,
			Name:        "source",
			Kind:        domain.EntryFile,
			Metadata:    domain.Metadata{Size: uint64Pointer(size), FileID: &fileID},
			Fingerprint: cloneFingerprint(fixture.plan.Source.Fingerprint),
		},
	}
	journal := newMemoryJournal()
	control := ControlFunc(func(checkpoint Checkpoint) ControlAction {
		if checkpoint.Offset >= uint64(fixture.plan.BufferBytes) {
			return ControlCancel
		}
		return ControlContinue
	})
	result, err := NewWorker(fixture.resolver, journal).Execute(context.Background(), fixture.plan, control)
	if !errors.Is(err, ErrCanceled) {
		t.Fatalf("100GiB synthetic Execute() error = %v, want canceled", err)
	}
	if result.Bytes != uint64(fixture.plan.BufferBytes) || !result.PartRetained {
		t.Fatalf("100GiB synthetic result = %#v", result)
	}
	if journal.maxBufferBytes != int(fixture.plan.BufferBytes) {
		t.Fatalf("100GiB synthetic max buffer = %d, want %d", journal.maxBufferBytes, fixture.plan.BufferBytes)
	}
}

func TestWorkerResumesAfterTransportInterruptAtDurableOffset(t *testing.T) {
	fixture := newWorkerFixture(t, []byte("disconnect-and-resume"), ConflictAsk)
	fixture.plan.BufferBytes = 4
	destination := &interruptingWriteProvider{mutableTestProvider: fixture.destination, interruptAfter: 4}
	fixture.resolver[fixture.destination.Descriptor().ID] = destination
	journal := newMemoryJournal()

	_, err := NewWorker(fixture.resolver, journal).Execute(context.Background(), fixture.plan, nil)
	if !domain.IsCode(err, domain.CodeTransportInterrupted) {
		t.Fatalf("interrupted Execute() error = %v", err)
	}
	if checkpoint := journal.latest(); checkpoint.Offset != 4 || checkpoint.Phase != PhaseStreaming {
		t.Fatalf("interrupted checkpoint = %#v", checkpoint)
	}
	if _, err := fixture.destination.Stat(context.Background(), providerapi.StatRequest{Location: fixture.plan.Final}); !domain.IsCode(err, domain.CodeNotFound) {
		t.Fatalf("interrupt exposed final: %v", err)
	}
	fixture.resolver[fixture.destination.Descriptor().ID] = fixture.destination
	result, err := NewWorker(fixture.resolver, journal).Execute(context.Background(), fixture.plan, nil)
	if err != nil || result.Outcome != OutcomeCompleted {
		t.Fatalf("resumed Execute() = (%#v, %v)", result, err)
	}
	assertWorkerBytes(t, fixture.destination, fixture.plan.Final, []byte("disconnect-and-resume"))
}

func TestWorkerPartOpenFailuresNeverExposeFinal(t *testing.T) {
	for _, code := range []domain.Code{domain.CodePermissionDenied, domain.CodeResourceExhausted} {
		t.Run(string(code), func(t *testing.T) {
			fixture := newWorkerFixture(t, []byte("must-not-publish"), ConflictAsk)
			destination := &openWriteFailureProvider{mutableTestProvider: fixture.destination, code: code}
			fixture.resolver[fixture.destination.Descriptor().ID] = destination
			_, err := NewWorker(fixture.resolver, newMemoryJournal()).Execute(context.Background(), fixture.plan, nil)
			if !domain.IsCode(err, code) {
				t.Fatalf("Execute() error = %v, want %s", err, code)
			}
			if _, err := fixture.destination.Stat(context.Background(), providerapi.StatRequest{Location: fixture.plan.Final}); !domain.IsCode(err, domain.CodeNotFound) {
				t.Fatalf("failed part open exposed final: %v", err)
			}
		})
	}
}

type workerFixture struct {
	plan            Plan
	create          jobstore.CreateRequest
	resolver        MapResolver
	source          providerapi.Provider
	destination     mutableTestProvider
	destinationRoot string
}

type mutableTestProvider interface {
	providerapi.Provider
	providerapi.MutableProvider
}

type closeTimestampProvider struct {
	mutableTestProvider
	root string
}

type renameResponseLostProvider struct{ mutableTestProvider }

func (provider *renameResponseLostProvider) Rename(ctx context.Context, request providerapi.RenameRequest) (providerapi.RenameResult, error) {
	result, err := provider.mutableTestProvider.Rename(ctx, request)
	if err != nil {
		return result, err
	}
	location := request.Destination
	return result, &domain.OpError{
		Code: domain.CodeTransportInterrupted, Message: "rename response lost", Operation: "rename",
		EndpointID: request.Destination.EndpointID, Location: &location,
		Retry: domain.RetryAdvice{Kind: domain.RetryAfterReconnect}, Effect: domain.EffectUnknown,
	}
}

type shortWriteProvider struct {
	mutableTestProvider
	maxWrite int
}

type shortReadProvider struct {
	providerapi.Provider
	maxRead int
}

func (provider *shortReadProvider) OpenRead(ctx context.Context, request providerapi.OpenReadRequest) (providerapi.ReadHandle, error) {
	handle, err := provider.Provider.OpenRead(ctx, request)
	if err != nil {
		return nil, err
	}
	return &shortReadHandle{ReadHandle: handle, maxRead: provider.maxRead}, nil
}

type shortReadHandle struct {
	providerapi.ReadHandle
	maxRead int
}

func (handle *shortReadHandle) Read(ctx context.Context, data []byte) (int, error) {
	if len(data) > handle.maxRead {
		data = data[:handle.maxRead]
	}
	return handle.ReadHandle.Read(ctx, data)
}

func (provider *shortWriteProvider) OpenWrite(ctx context.Context, request providerapi.OpenWriteRequest) (providerapi.WriteHandle, error) {
	handle, err := provider.mutableTestProvider.OpenWrite(ctx, request)
	if err != nil {
		return nil, err
	}
	return &shortWriteHandle{WriteHandle: handle, maxWrite: provider.maxWrite}, nil
}

type shortWriteHandle struct {
	providerapi.WriteHandle
	maxWrite int
}

func (handle *shortWriteHandle) Write(ctx context.Context, data []byte) (int, error) {
	if len(data) > handle.maxWrite {
		data = data[:handle.maxWrite]
	}
	return handle.WriteHandle.Write(ctx, data)
}

type interruptingWriteProvider struct {
	mutableTestProvider
	interruptAfter int
}

func (provider *interruptingWriteProvider) OpenWrite(ctx context.Context, request providerapi.OpenWriteRequest) (providerapi.WriteHandle, error) {
	handle, err := provider.mutableTestProvider.OpenWrite(ctx, request)
	if err != nil {
		return nil, err
	}
	location := request.Location
	return &interruptingWriteHandle{WriteHandle: handle, remaining: provider.interruptAfter, location: &location}, nil
}

type interruptingWriteHandle struct {
	providerapi.WriteHandle
	remaining int
	location  *domain.Location
}

func (handle *interruptingWriteHandle) Write(ctx context.Context, data []byte) (int, error) {
	if handle.remaining <= 0 {
		return 0, &domain.OpError{
			Code: domain.CodeTransportInterrupted, Message: "injected transport interrupt", Operation: "write",
			EndpointID: handle.location.EndpointID, Location: handle.location,
			Retry: domain.RetryAdvice{Kind: domain.RetryAfterReconnect}, Effect: domain.EffectNone,
		}
	}
	if len(data) > handle.remaining {
		data = data[:handle.remaining]
	}
	n, err := handle.WriteHandle.Write(ctx, data)
	handle.remaining -= n
	return n, err
}

type openWriteFailureProvider struct {
	mutableTestProvider
	code    domain.Code
	message string
}

func (provider *openWriteFailureProvider) OpenWrite(_ context.Context, request providerapi.OpenWriteRequest) (providerapi.WriteHandle, error) {
	location := request.Location
	retry := domain.RetryNever
	if provider.code == domain.CodeResourceExhausted {
		retry = domain.RetryBackoff
	}
	message := provider.message
	if message == "" {
		message = "injected part open failure"
	}
	return nil, &domain.OpError{
		Code: provider.code, Message: message, Operation: "open_write",
		EndpointID: location.EndpointID, Location: &location,
		Retry: domain.RetryAdvice{Kind: retry}, Effect: domain.EffectNone,
	}
}

func (provider *closeTimestampProvider) OpenWrite(ctx context.Context, request providerapi.OpenWriteRequest) (providerapi.WriteHandle, error) {
	handle, err := provider.mutableTestProvider.OpenWrite(ctx, request)
	if err != nil {
		return nil, err
	}
	return &closeTimestampHandle{
		WriteHandle: handle,
		hostPath:    filepath.Join(provider.root, strings.TrimPrefix(string(request.Location.Path), "/")),
	}, nil
}

type closeTimestampHandle struct {
	providerapi.WriteHandle
	hostPath string
	once     sync.Once
	err      error
}

func (handle *closeTimestampHandle) Close(ctx context.Context) error {
	closeErr := handle.WriteHandle.Close(ctx)
	handle.once.Do(func() {
		changed := time.Unix(1_900_000_000, 123)
		handle.err = os.Chtimes(handle.hostPath, changed, changed)
	})
	return errors.Join(closeErr, handle.err)
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
	return workerFixture{plan: plan, create: create, resolver: resolver, source: source, destination: destination, destinationRoot: destinationRoot}
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

type syntheticSparseProvider struct {
	providerapi.Provider
	entry domain.Entry
}

func (provider *syntheticSparseProvider) Stat(ctx context.Context, request providerapi.StatRequest) (domain.Entry, error) {
	if request.Location == provider.entry.Location {
		return provider.entry, nil
	}
	return provider.Provider.Stat(ctx, request)
}

func (provider *syntheticSparseProvider) OpenRead(ctx context.Context, request providerapi.OpenReadRequest) (providerapi.ReadHandle, error) {
	if request.Location == provider.entry.Location {
		return &syntheticSparseReadHandle{info: providerapi.ReadInfo{Entry: provider.entry, Fingerprint: provider.entry.Fingerprint}}, nil
	}
	return provider.Provider.OpenRead(ctx, request)
}

type syntheticSparseReadHandle struct{ info providerapi.ReadInfo }

func (handle *syntheticSparseReadHandle) Info() providerapi.ReadInfo { return handle.info }

func (handle *syntheticSparseReadHandle) Read(ctx context.Context, buffer []byte) (int, error) {
	if err := ctx.Err(); err != nil {
		return 0, err
	}
	clear(buffer)
	return len(buffer), nil
}

func (handle *syntheticSparseReadHandle) Close(context.Context) error { return nil }

func uint64Pointer(value uint64) *uint64 { return &value }

var _ = time.Time{}
