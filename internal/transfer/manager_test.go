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
	"github.com/TyrantLucifer/awesome-mac-sftp/internal/job"
	providerapi "github.com/TyrantLucifer/awesome-mac-sftp/internal/provider"
	"github.com/TyrantLucifer/awesome-mac-sftp/internal/state/jobstore"
	"github.com/TyrantLucifer/awesome-mac-sftp/internal/testkit"
)

func TestManagerContinuesCreatedJobAfterClientContextCancellation(t *testing.T) {
	fixture := newWorkerFixture(t, []byte("client-independent"), ConflictAsk)
	store, database := openTransferStore(t, context.Background(), testDatabasePath(t), true)
	t.Cleanup(func() { _ = database.Close() })
	manager, err := NewManager(ManagerConfig{
		Store:         store,
		Resolver:      fixture.resolver,
		Generator:     &testkit.SequenceGenerator{},
		Now:           func() time.Time { return time.Unix(1_800_000_100, 0) },
		MaxConcurrent: 1,
	})
	if err != nil {
		t.Fatalf("NewManager(): %v", err)
	}
	t.Cleanup(manager.Close)
	if err := manager.Start(context.Background()); err != nil {
		t.Fatalf("Start(): %v", err)
	}

	clientContext, cancelClient := context.WithCancel(context.Background())
	created, err := manager.CreateCopy(clientContext, Intent{
		Clipboard:            ClipboardCopy,
		Source:               fixture.plan.Source,
		DestinationDirectory: fixture.plan.DestinationDirectory,
		Name:                 fixture.plan.RequestedName,
		ConflictPolicy:       ConflictAsk,
	})
	if err != nil {
		t.Fatalf("CreateCopy(): %v", err)
	}
	cancelClient()

	completed := waitForTerminal(t, manager, created.JobID)
	if completed.State != job.StateCompleted {
		t.Fatalf("detached Job state = %q, want completed", completed.State)
	}
	assertWorkerBytes(t, fixture.destination, fixture.plan.Final, []byte("client-independent"))
	views, err := manager.JobViews(context.Background(), 20)
	if err != nil {
		t.Fatalf("JobViews(): %v", err)
	}
	if len(views) != 1 || views[0].Snapshot.JobID != created.JobID || views[0].Source != fixture.plan.Source.Location ||
		views[0].Final.Path != "/final" || views[0].Phase != PhaseCommitted || views[0].Bytes != uint64(len("client-independent")) {
		t.Fatalf("JobViews() = %#v", views)
	}
}

func TestManagerDoesNotPersistQueuedJobWhenQueueAdmissionIsExhausted(t *testing.T) {
	fixture := newWorkerFixture(t, []byte("queue admission"), ConflictAsk)
	ctx := context.Background()
	store, database := openTransferStore(t, ctx, testDatabasePath(t), true)
	t.Cleanup(func() { _ = database.Close() })
	limits := HardResourceCeilings()
	limits.QueuedJobs = 1
	manager, err := NewManager(ManagerConfig{
		Store: store, Resolver: fixture.resolver, Generator: &testkit.SequenceGenerator{},
		MaxConcurrent: 1, MaxQueued: 1, ResourceLimits: limits,
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(manager.Close)
	manager.mu.Lock()
	manager.started = true
	manager.mu.Unlock()
	occupied, err := manager.scheduler.TryAcquireResources(ResourceRequest{
		JobID: "job-occupies-queue", Usage: ResourceUsage{QueuedJobs: 1},
	})
	if err != nil {
		t.Fatal(err)
	}
	defer occupied.Release()

	_, err = manager.CreateCopy(ctx, Intent{
		Clipboard: ClipboardCopy, Source: fixture.plan.Source,
		DestinationDirectory: fixture.plan.DestinationDirectory,
		Name:                 fixture.plan.RequestedName, ConflictPolicy: ConflictAsk,
	})
	if !errors.Is(err, ErrResourceExhausted) {
		t.Fatalf("CreateCopy() error = %v, want ErrResourceExhausted", err)
	}
	jobs, listErr := store.List(ctx, 10)
	if listErr != nil {
		t.Fatal(listErr)
	}
	if len(jobs) != 0 {
		t.Fatalf("durable Jobs after rejected queue admission = %#v, want none", jobs)
	}
}

func TestManagerPermanentlyImpossibleExecutionResourceFailsDurableJob(t *testing.T) {
	fixture := newWorkerFixture(t, []byte("impossible execution"), ConflictAsk)
	ctx := context.Background()
	store, database := openTransferStore(t, ctx, testDatabasePath(t), true)
	t.Cleanup(func() { _ = database.Close() })
	created, _, err := store.Create(ctx, fixture.create)
	if err != nil {
		t.Fatal(err)
	}
	limits := HardResourceCeilings()
	limits.MemoryBytes = 1
	generator := &testkit.SequenceGenerator{}
	if _, err := generator.New("evt_"); err != nil {
		t.Fatal(err)
	}
	manager, err := NewManager(ManagerConfig{
		Store: store, Resolver: fixture.resolver, Generator: generator,
		Now:           func() time.Time { return fixture.create.Now.Add(time.Second) },
		MaxConcurrent: 1, MaxQueued: 1, ResourceLimits: limits,
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(manager.Close)
	manager.execute(created.JobID)
	got, err := store.Get(ctx, created.JobID)
	if err != nil {
		t.Fatal(err)
	}
	if got.State != job.StateFailed {
		t.Fatalf("state after permanently impossible execution admission = %q, transition error = %v, want failed", got.State, manager.transitionError(created.JobID))
	}
}

func TestManagerTerminalTransitionFallsBackToBoundedPayload(t *testing.T) {
	fixture := newWorkerFixture(t, []byte("payload"), ConflictAsk)
	store, database := openTransferStore(t, context.Background(), testDatabasePath(t), true)
	t.Cleanup(func() { _ = database.Close() })
	created, _, err := store.Create(context.Background(), fixture.create)
	if err != nil {
		t.Fatal(err)
	}
	generator := &testkit.SequenceGenerator{}
	for index := 0; index < 64; index++ {
		if _, err := generator.New("skip_"); err != nil {
			t.Fatal(err)
		}
	}
	manager, err := NewManager(ManagerConfig{
		Store: store, Resolver: fixture.resolver, Generator: generator,
		Now: func() time.Time { return time.Unix(1_800_000_125, 0) }, MaxConcurrent: 1,
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(manager.Close)
	running, err := manager.transition(created, job.StateRunning, "job_started", map[string]any{})
	if err != nil {
		t.Fatal(err)
	}
	verifying, err := manager.transition(running, job.StateVerifying, "job_verifying", map[string]any{})
	if err != nil {
		t.Fatal(err)
	}
	completed, err := manager.persistTerminal(
		verifying,
		job.StateCompleted,
		"job_completed",
		"completed",
		map[string]any{"manifest": strings.Repeat("x", jobstore.MaxEventPayloadBytes)},
	)
	if err != nil {
		t.Fatalf("persist terminal with fallback: %v", err)
	}
	if completed.State != job.StateCompleted {
		t.Fatalf("terminal state = %q, want completed", completed.State)
	}
	events, err := store.ListEvents(context.Background(), created.JobID, 0, 10)
	if err != nil {
		t.Fatal(err)
	}
	last := events[len(events)-1]
	if len(last.PayloadJSON) > jobstore.MaxEventPayloadBytes || !strings.Contains(last.PayloadJSON, "details_omitted") {
		t.Fatalf("fallback event payload = %d bytes: %s", len(last.PayloadJSON), last.PayloadJSON)
	}
}

func TestManagerSurfacesUnrecoverableTerminalTransitionFailure(t *testing.T) {
	fixture := newWorkerFixture(t, []byte("payload"), ConflictAsk)
	store, database := openTransferStore(t, context.Background(), testDatabasePath(t), true)
	t.Cleanup(func() { _ = database.Close() })
	created, _, err := store.Create(context.Background(), fixture.create)
	if err != nil {
		t.Fatal(err)
	}
	generator := &testkit.SequenceGenerator{}
	for index := 0; index < 64; index++ {
		if _, err := generator.New("skip_"); err != nil {
			t.Fatal(err)
		}
	}
	manager, err := NewManager(ManagerConfig{
		Store: store, Resolver: fixture.resolver, Generator: generator,
		Now: func() time.Time { return time.Unix(1_800_000_126, 0) }, MaxConcurrent: 1,
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(manager.Close)
	running, err := manager.transition(created, job.StateRunning, "job_started", map[string]any{})
	if err != nil {
		t.Fatal(err)
	}
	verifying, err := manager.transition(running, job.StateVerifying, "job_verifying", map[string]any{})
	if err != nil {
		t.Fatal(err)
	}
	manager.generator = failingIDGenerator{}
	manager.fail(verifying, errors.New("execution failed"))

	waitContext, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	_, waitErr := manager.Wait(waitContext, created.JobID)
	if waitErr == nil || errors.Is(waitErr, context.DeadlineExceeded) || !strings.Contains(waitErr.Error(), "persist bounded terminal fallback") {
		t.Fatalf("Wait() error = %v, want surfaced terminal transition failure", waitErr)
	}
}

type failingIDGenerator struct{}

func (failingIDGenerator) New(string) (string, error) {
	return "", errors.New("ID generator unavailable")
}

func TestManagerRunsDurableDirectoryJobAndReportsItemProgress(t *testing.T) {
	sourceRoot := t.TempDir()
	destinationRoot := t.TempDir()
	if err := os.MkdirAll(filepath.Join(sourceRoot, "tree", "nested"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(sourceRoot, "tree", "first"), []byte("first"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(sourceRoot, "tree", "nested", "second"), []byte("second"), 0o600); err != nil {
		t.Fatal(err)
	}
	source := newPlanTestProvider(t, "ep_aaaaaaaaaaaaaaaaaaaaaaaaaa", sourceRoot, domain.EndpointLocal)
	destination := newPlanTestProvider(t, "ep_bbbbbbbbbbbbbbbbbbbbbbbbbb", destinationRoot, domain.EndpointLocal)
	resolver := MapResolver{source.Descriptor().ID: source, destination.Descriptor().ID: destination}
	store, database := openTransferStore(t, context.Background(), testDatabasePath(t), true)
	t.Cleanup(func() { _ = database.Close() })
	manager, err := NewManager(ManagerConfig{
		Store: store, Resolver: resolver, Generator: &testkit.SequenceGenerator{},
		Now: func() time.Time { return time.Unix(1_800_000_150, 0) }, MaxConcurrent: 1,
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(manager.Close)
	if err := manager.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	reference, err := manager.Capture(context.Background(), normalizePlanTest(t, source, "/tree"))
	if err != nil {
		t.Fatal(err)
	}
	created, err := manager.CreateCopy(context.Background(), Intent{
		Clipboard: ClipboardCopy, Source: reference, DestinationDirectory: normalizePlanTest(t, destination, "/"),
		Name: "copied", ConflictPolicy: ConflictAsk,
	})
	if err != nil {
		t.Fatal(err)
	}
	completed := waitForTerminal(t, manager, created.JobID)
	if completed.State != job.StateCompleted {
		t.Fatalf("directory Job state = %q", completed.State)
	}
	views, err := manager.JobViews(context.Background(), 10)
	if err != nil || len(views) != 1 || views[0].Items != 3 || views[0].Bytes != 11 || views[0].BytesTotal != nil || views[0].Phase != PhaseCommitted {
		t.Fatalf("directory Job view = (%#v, %v)", views, err)
	}
	for relative, want := range map[string]string{"first": "first", "nested/second": "second"} {
		// #nosec G304 -- the relative names are fixed test literals below a private destination root.
		data, err := os.ReadFile(filepath.Join(destinationRoot, "copied", filepath.FromSlash(relative)))
		if err != nil || string(data) != want {
			t.Fatalf("directory output %s = %q, %v", relative, data, err)
		}
	}
}

func TestManagerRetriesOnlyFailedDirectoryItemsAfterPermissionRepair(t *testing.T) {
	sourceRoot := t.TempDir()
	destinationRoot := t.TempDir()
	if err := os.MkdirAll(filepath.Join(sourceRoot, "tree"), 0o700); err != nil {
		t.Fatal(err)
	}
	for name, data := range map[string]string{"denied": "denied", "ok": "ok"} {
		if err := os.WriteFile(filepath.Join(sourceRoot, "tree", name), []byte(data), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	source := newPlanTestProvider(t, "ep_aaaaaaaaaaaaaaaaaaaaaaaaaa", sourceRoot, domain.EndpointLocal)
	destination := newPlanTestProvider(t, "ep_bbbbbbbbbbbbbbbbbbbbbbbbbb", destinationRoot, domain.EndpointLocal)
	resolver := MapResolver{
		source.Descriptor().ID:      &pathReadFailureProvider{Provider: source, denied: "/tree/denied"},
		destination.Descriptor().ID: destination,
	}
	store, database := openTransferStore(t, context.Background(), testDatabasePath(t), true)
	t.Cleanup(func() { _ = database.Close() })
	manager, err := NewManager(ManagerConfig{
		Store: store, Resolver: resolver, Generator: &testkit.SequenceGenerator{},
		Now: func() time.Time { return time.Unix(1_800_000_160, 0) }, MaxConcurrent: 1,
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(manager.Close)
	if err := manager.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	reference, err := manager.Capture(context.Background(), normalizePlanTest(t, source, "/tree"))
	if err != nil {
		t.Fatal(err)
	}
	created, err := manager.CreateCopy(context.Background(), Intent{
		Clipboard: ClipboardCopy, Source: reference, DestinationDirectory: normalizePlanTest(t, destination, "/"),
		Name: "copied", ConflictPolicy: ConflictAsk,
	})
	if err != nil {
		t.Fatal(err)
	}
	waiting := waitForState(t, manager, created.JobID, job.StateRetryWait)
	resolver[source.Descriptor().ID] = source
	if _, err := manager.Resume(context.Background(), waiting.JobID); err != nil {
		t.Fatalf("Resume(repaired directory): %v", err)
	}
	completed := waitForTerminal(t, manager, created.JobID)
	if completed.State != job.StateCompleted {
		t.Fatalf("retried directory state = %q", completed.State)
	}
	for name, want := range map[string]string{"denied": "denied", "ok": "ok"} {
		// #nosec G304 -- names are fixed test literals below a private destination root.
		data, err := os.ReadFile(filepath.Join(destinationRoot, "copied", name))
		if err != nil || string(data) != want {
			t.Fatalf("retried output %s = %q, %v", name, data, err)
		}
	}
}

func TestManagerDirectoryMoveVerifiesEveryItemBeforeBoundedSourceDelete(t *testing.T) {
	sourceRoot := t.TempDir()
	destinationRoot := t.TempDir()
	if err := os.MkdirAll(filepath.Join(sourceRoot, "tree", "nested"), 0o700); err != nil {
		t.Fatal(err)
	}
	for relative, data := range map[string]string{"first": "first", "nested/second": "second"} {
		// #nosec G703 -- relative paths are fixed test literals below a private source root.
		if err := os.WriteFile(filepath.Join(sourceRoot, "tree", filepath.FromSlash(relative)), []byte(data), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	source := newPlanTestProvider(t, "ep_aaaaaaaaaaaaaaaaaaaaaaaaaa", sourceRoot, domain.EndpointLocal)
	destination := newPlanTestProvider(t, "ep_bbbbbbbbbbbbbbbbbbbbbbbbbb", destinationRoot, domain.EndpointLocal)
	resolver := MapResolver{source.Descriptor().ID: source, destination.Descriptor().ID: destination}
	store, database := openTransferStore(t, context.Background(), testDatabasePath(t), true)
	t.Cleanup(func() { _ = database.Close() })
	manager := newStartedTestManager(t, store, resolver, 1_800_000_170)
	reference, err := manager.Capture(context.Background(), normalizePlanTest(t, source, "/tree"))
	if err != nil {
		t.Fatal(err)
	}
	created, err := manager.CreateCopy(context.Background(), Intent{
		Clipboard: ClipboardCut, Source: reference, DestinationDirectory: normalizePlanTest(t, destination, "/"),
		Name: "moved", ConflictPolicy: ConflictAsk,
	})
	if err != nil {
		t.Fatal(err)
	}
	completed := waitForTerminal(t, manager, created.JobID)
	if completed.State != job.StateCompleted {
		t.Fatalf("directory move state = %q", completed.State)
	}
	if _, err := source.Stat(context.Background(), providerapi.StatRequest{Location: reference.Location}); !domain.IsCode(err, domain.CodeNotFound) {
		t.Fatalf("directory move source still exists: %v", err)
	}
	for relative, want := range map[string]string{"first": "first", "nested/second": "second"} {
		// #nosec G304 -- relative paths are fixed test literals below a private destination root.
		data, err := os.ReadFile(filepath.Join(destinationRoot, "moved", filepath.FromSlash(relative)))
		if err != nil || string(data) != want {
			t.Fatalf("moved output %s = %q, %v", relative, data, err)
		}
	}
}

func TestManagerReloadsQueuedFrozenPlanFromDurableStore(t *testing.T) {
	fixture := newWorkerFixture(t, []byte("durable-plan"), ConflictAsk)
	store, database := openTransferStore(t, context.Background(), testDatabasePath(t), true)
	t.Cleanup(func() { _ = database.Close() })
	if _, _, err := store.Create(context.Background(), fixture.create); err != nil {
		t.Fatalf("Create(): %v", err)
	}
	generator := &testkit.SequenceGenerator{}
	if _, err := generator.New("evt_"); err != nil {
		t.Fatal(err)
	}
	manager, err := NewManager(ManagerConfig{
		Store:         store,
		Resolver:      fixture.resolver,
		Generator:     generator,
		Now:           func() time.Time { return time.Unix(1_800_000_200, 0) },
		MaxConcurrent: 1,
	})
	if err != nil {
		t.Fatalf("NewManager(): %v", err)
	}
	t.Cleanup(manager.Close)
	if err := manager.Start(context.Background()); err != nil {
		t.Fatalf("Start(): %v", err)
	}

	completed := waitForTerminal(t, manager, fixture.plan.JobID)
	if completed.State != job.StateCompleted {
		t.Fatalf("reloaded Job state = %q, want completed", completed.State)
	}
	assertWorkerBytes(t, fixture.destination, fixture.plan.Final, []byte("durable-plan"))
}

func TestManagerRetainsFrozenEndpointsBeforeCreateReturns(t *testing.T) {
	fixture := newWorkerFixture(t, []byte("lease-before-return"), ConflictAsk)
	readStarted := make(chan struct{})
	readRelease := make(chan struct{})
	gatedSource := &gatedReadProvider{Provider: fixture.source, started: readStarted, release: readRelease}
	acquirer := &gatedPlanAcquirer{
		Resolver: MapResolver{
			fixture.source.Descriptor().ID:      gatedSource,
			fixture.destination.Descriptor().ID: fixture.destination,
		},
		acquireStarted: make(chan struct{}),
		acquireRelease: make(chan struct{}),
		leaseReleased:  make(chan struct{}),
	}
	store, database := openTransferStore(t, context.Background(), testDatabasePath(t), true)
	t.Cleanup(func() { _ = database.Close() })
	manager, err := NewManager(ManagerConfig{
		Store: store, Resolver: acquirer, Generator: &testkit.SequenceGenerator{},
		Now: func() time.Time { return time.Unix(1_800_000_250, 0) }, MaxConcurrent: 1,
	})
	if err != nil {
		t.Fatalf("NewManager(): %v", err)
	}
	t.Cleanup(manager.Close)
	if err := manager.Start(context.Background()); err != nil {
		t.Fatalf("Start(): %v", err)
	}
	created := make(chan jobstore.Snapshot, 1)
	createErrors := make(chan error, 1)
	go func() {
		snapshot, err := manager.CreateCopy(context.Background(), Intent{
			Clipboard: ClipboardCopy, Source: fixture.plan.Source, DestinationDirectory: fixture.plan.DestinationDirectory,
			Name: fixture.plan.RequestedName, ConflictPolicy: ConflictAsk,
		})
		if err != nil {
			createErrors <- err
			return
		}
		created <- snapshot
	}()
	select {
	case <-acquirer.acquireStarted:
	case <-time.After(5 * time.Second):
		t.Fatal("endpoint acquisition did not start")
	}
	select {
	case snapshot := <-created:
		t.Fatalf("CreateCopy returned before endpoint lease was retained: %#v", snapshot)
	case err := <-createErrors:
		t.Fatalf("CreateCopy failed: %v", err)
	default:
	}
	close(acquirer.acquireRelease)
	var snapshot jobstore.Snapshot
	select {
	case snapshot = <-created:
	case err := <-createErrors:
		t.Fatalf("CreateCopy failed: %v", err)
	case <-time.After(5 * time.Second):
		t.Fatal("CreateCopy did not return after endpoint acquisition")
	}
	select {
	case <-readStarted:
	case <-time.After(5 * time.Second):
		t.Fatal("worker did not start with retained endpoint lease")
	}
	if got := acquirer.activeLeases(); got != 1 {
		t.Fatalf("active endpoint leases = %d, want 1", got)
	}
	close(readRelease)
	if completed := waitForTerminal(t, manager, snapshot.JobID); completed.State != job.StateCompleted {
		t.Fatalf("completed state = %q", completed.State)
	}
	select {
	case <-acquirer.leaseReleased:
	case <-time.After(5 * time.Second):
		t.Fatalf("active endpoint leases after completion = %d, want 0", acquirer.activeLeases())
	}
}

func TestManagerPauseAndResumeAreDurableJobControls(t *testing.T) {
	fixture := newWorkerFixture(t, []byte("pause-and-resume"), ConflictAsk)
	started := make(chan struct{})
	release := make(chan struct{})
	gatedSource := &gatedReadProvider{Provider: fixture.source, started: started, release: release}
	resolver := MapResolver{
		fixture.source.Descriptor().ID:      gatedSource,
		fixture.destination.Descriptor().ID: fixture.destination,
	}
	store, database := openTransferStore(t, context.Background(), testDatabasePath(t), true)
	t.Cleanup(func() { _ = database.Close() })
	manager, err := NewManager(ManagerConfig{
		Store:         store,
		Resolver:      resolver,
		Generator:     &testkit.SequenceGenerator{},
		Now:           func() time.Time { return time.Unix(1_800_000_300, 0) },
		MaxConcurrent: 1,
	})
	if err != nil {
		t.Fatalf("NewManager(): %v", err)
	}
	t.Cleanup(manager.Close)
	if err := manager.Start(context.Background()); err != nil {
		t.Fatalf("Start(): %v", err)
	}
	created, err := manager.CreateCopy(context.Background(), Intent{
		Clipboard:            ClipboardCopy,
		Source:               fixture.plan.Source,
		DestinationDirectory: fixture.plan.DestinationDirectory,
		Name:                 fixture.plan.RequestedName,
		ConflictPolicy:       ConflictAsk,
	})
	if err != nil {
		t.Fatalf("CreateCopy(): %v", err)
	}
	select {
	case <-started:
	case <-time.After(5 * time.Second):
		t.Fatal("worker did not reach gated source read")
	}
	if _, err := manager.Pause(context.Background(), created.JobID); err != nil {
		t.Fatalf("Pause(): %v", err)
	}
	close(release)
	paused := waitForState(t, manager, created.JobID, job.StatePaused)
	if !paused.PauseRequested {
		t.Fatalf("paused snapshot = %#v, want durable pause flag", paused)
	}
	if _, err := manager.Resume(context.Background(), created.JobID); err != nil {
		t.Fatalf("Resume(): %v", err)
	}
	completed := waitForTerminal(t, manager, created.JobID)
	if completed.State != job.StateCompleted || completed.PauseRequested {
		t.Fatalf("resumed snapshot = %#v, want completed with cleared pause", completed)
	}
}

func TestManagerResumeRetriesDurableRetryWaitJob(t *testing.T) {
	fixture := newWorkerFixture(t, []byte("retry-wait"), ConflictAsk)
	store, database := openTransferStore(t, context.Background(), testDatabasePath(t), true)
	t.Cleanup(func() { _ = database.Close() })
	created, _, err := store.Create(context.Background(), fixture.create)
	if err != nil {
		t.Fatalf("Create(): %v", err)
	}
	generator := &testkit.SequenceGenerator{}
	if _, err := generator.New("evt_"); err != nil {
		t.Fatal(err)
	}
	startedEvent, err := domain.NewEventID(generator)
	if err != nil {
		t.Fatal(err)
	}
	running, _, err := store.Transition(context.Background(), jobstore.TransitionRequest{
		JobID: created.JobID, ExpectedVersion: created.StateVersion, To: job.StateRunning,
		EventID: startedEvent, EventKind: "job_started", PayloadJSON: `{}`, Now: time.Unix(1_800_000_350, 0),
	})
	if err != nil {
		t.Fatalf("start Job: %v", err)
	}
	retryEvent, err := domain.NewEventID(generator)
	if err != nil {
		t.Fatal(err)
	}
	retryAt := time.Unix(1_800_000_500, 0)
	retrying, _, err := store.Transition(context.Background(), jobstore.TransitionRequest{
		JobID: running.JobID, ExpectedVersion: running.StateVersion, To: job.StateRetryWait,
		EventID: retryEvent, EventKind: "job_retry_wait", PayloadJSON: `{}`, RetryAt: &retryAt,
		Now: time.Unix(1_800_000_351, 0),
	})
	if err != nil {
		t.Fatalf("enter retry_wait: %v", err)
	}
	manager, err := NewManager(ManagerConfig{
		Store: store, Resolver: fixture.resolver, Generator: generator,
		Now: func() time.Time { return time.Unix(1_800_000_400, 0) }, MaxConcurrent: 1,
	})
	if err != nil {
		t.Fatalf("NewManager(): %v", err)
	}
	t.Cleanup(manager.Close)
	if err := manager.Start(context.Background()); err != nil {
		t.Fatalf("Start(): %v", err)
	}
	queued, err := manager.Resume(context.Background(), retrying.JobID)
	if err != nil {
		t.Fatalf("Resume(retry_wait): %v", err)
	}
	if queued.State != job.StateQueued || queued.RetryAt != nil {
		t.Fatalf("resumed retry Job = %#v", queued)
	}
	if completed := waitForTerminal(t, manager, retrying.JobID); completed.State != job.StateCompleted {
		t.Fatalf("completed state = %q", completed.State)
	}
}

func TestManagerCancelRetainsPartAndReplaysOrderedEvents(t *testing.T) {
	fixture := newWorkerFixture(t, []byte("cancel-and-retain"), ConflictAsk)
	started := make(chan struct{})
	release := make(chan struct{})
	gatedSource := &gatedReadProvider{Provider: fixture.source, started: started, release: release}
	resolver := MapResolver{
		fixture.source.Descriptor().ID:      gatedSource,
		fixture.destination.Descriptor().ID: fixture.destination,
	}
	store, database := openTransferStore(t, context.Background(), testDatabasePath(t), true)
	t.Cleanup(func() { _ = database.Close() })
	generator := &testkit.SequenceGenerator{}
	manager, err := NewManager(ManagerConfig{
		Store: store, Resolver: resolver, Generator: generator,
		Now: func() time.Time { return time.Unix(1_800_000_400, 0) }, MaxConcurrent: 1,
	})
	if err != nil {
		t.Fatalf("NewManager(): %v", err)
	}
	t.Cleanup(manager.Close)
	if err := manager.Start(context.Background()); err != nil {
		t.Fatalf("Start(): %v", err)
	}
	created, err := manager.CreateCopy(context.Background(), Intent{
		Clipboard: ClipboardCopy, Source: fixture.plan.Source, DestinationDirectory: fixture.plan.DestinationDirectory,
		Name: fixture.plan.RequestedName, ConflictPolicy: ConflictAsk,
	})
	if err != nil {
		t.Fatalf("CreateCopy(): %v", err)
	}
	select {
	case <-started:
	case <-time.After(5 * time.Second):
		t.Fatal("worker did not reach gated source read")
	}
	if _, err := manager.Cancel(context.Background(), created.JobID); err != nil {
		t.Fatalf("Cancel(): %v", err)
	}
	close(release)
	canceled := waitForTerminal(t, manager, created.JobID)
	if canceled.State != job.StateCanceled || !canceled.CancelRequested {
		t.Fatalf("canceled snapshot = %#v", canceled)
	}
	record, err := store.GetPlan(context.Background(), created.JobID)
	if err != nil {
		t.Fatalf("GetPlan(): %v", err)
	}
	plan, err := DecodePlan(record, created.JobID)
	if err != nil {
		t.Fatalf("DecodePlan(): %v", err)
	}
	if _, err := fixture.destination.Stat(context.Background(), providerapi.StatRequest{Location: plan.Part}); err != nil {
		t.Fatalf("canceled part is not retained: %v", err)
	}
	events, err := manager.Events(context.Background(), created.JobID, 0, 20)
	if err != nil {
		t.Fatalf("Events(): %v", err)
	}
	if len(events) < 4 || events[len(events)-2].Kind != "job_cancel_requested" || events[len(events)-1].Kind != "job_canceled" {
		t.Fatalf("events = %#v", events)
	}
	replayed, err := manager.Events(context.Background(), created.JobID, events[len(events)-2].Sequence, 20)
	if err != nil || len(replayed) != 1 || replayed[0].EventID != events[len(events)-1].EventID {
		t.Fatalf("replayed events = (%#v, %v)", replayed, err)
	}
}

func TestManagerCutDeletesSourceOnlyAfterVerifiedCommit(t *testing.T) {
	fixture := newWorkerFixture(t, []byte("retain-source"), ConflictAsk)
	store, database := openTransferStore(t, context.Background(), testDatabasePath(t), true)
	t.Cleanup(func() { _ = database.Close() })
	manager, err := NewManager(ManagerConfig{
		Store: store, Resolver: fixture.resolver, Generator: &testkit.SequenceGenerator{},
		Now: func() time.Time { return time.Unix(1_800_000_500, 0) }, MaxConcurrent: 1,
	})
	if err != nil {
		t.Fatalf("NewManager(): %v", err)
	}
	t.Cleanup(manager.Close)
	if err := manager.Start(context.Background()); err != nil {
		t.Fatalf("Start(): %v", err)
	}
	created, err := manager.CreateCopy(context.Background(), Intent{
		Clipboard: ClipboardCut, Source: fixture.plan.Source, DestinationDirectory: fixture.plan.DestinationDirectory,
		Name: fixture.plan.RequestedName, ConflictPolicy: ConflictAsk,
	})
	if err != nil {
		t.Fatalf("CreateCopy(): %v", err)
	}
	completed := waitForTerminal(t, manager, created.JobID)
	if completed.State != job.StateCompleted {
		t.Fatalf("cut Job state = %q, want completed", completed.State)
	}
	if _, err := fixture.source.Stat(context.Background(), providerapi.StatRequest{Location: fixture.plan.Source.Location}); !domain.IsCode(err, domain.CodeNotFound) {
		t.Fatalf("cut source still exists after verified commit: %v", err)
	}
	assertWorkerBytes(t, fixture.destination, fixture.plan.Final, []byte("retain-source"))
}

func TestManagerMoveRetainsSourceWhenItChangesAfterCommit(t *testing.T) {
	fixture := newWorkerFixture(t, []byte("copied-version"), ConflictAsk)
	destination := &mutateSourceOnRenameProvider{
		mutableTestProvider: fixture.destination,
		sourcePath:          filepath.Join(fixture.sourceRoot, "source"),
	}
	resolver := MapResolver{fixture.source.Descriptor().ID: fixture.source, fixture.destination.Descriptor().ID: destination}
	store, database := openTransferStore(t, context.Background(), testDatabasePath(t), true)
	t.Cleanup(func() { _ = database.Close() })
	manager := newStartedTestManager(t, store, resolver, 1_800_000_510)
	created, err := manager.CreateCopy(context.Background(), Intent{
		Clipboard: ClipboardCut, Source: fixture.plan.Source, DestinationDirectory: fixture.plan.DestinationDirectory,
		Name: fixture.plan.RequestedName, ConflictPolicy: ConflictAsk,
	})
	if err != nil {
		t.Fatal(err)
	}
	completed := waitForTerminal(t, manager, created.JobID)
	if completed.State != job.StateCompletedWithSourceRetained {
		t.Fatalf("changed-source move state = %q", completed.State)
	}
	assertWorkerBytes(t, fixture.source, fixture.plan.Source.Location, []byte("changed-after-commit"))
	assertWorkerBytes(t, fixture.destination, fixture.plan.Final, []byte("copied-version"))
}

func TestManagerMoveProvesDeleteAfterResponseLoss(t *testing.T) {
	fixture := newWorkerFixture(t, []byte("delete-response-lost"), ConflictAsk)
	source := &removeResponseLostProvider{mutableTestProvider: fixture.source.(mutableTestProvider)}
	resolver := MapResolver{fixture.source.Descriptor().ID: source, fixture.destination.Descriptor().ID: fixture.destination}
	store, database := openTransferStore(t, context.Background(), testDatabasePath(t), true)
	t.Cleanup(func() { _ = database.Close() })
	manager := newStartedTestManager(t, store, resolver, 1_800_000_520)
	created, err := manager.CreateCopy(context.Background(), Intent{
		Clipboard: ClipboardCut, Source: fixture.plan.Source, DestinationDirectory: fixture.plan.DestinationDirectory,
		Name: fixture.plan.RequestedName, ConflictPolicy: ConflictAsk,
	})
	if err != nil {
		t.Fatal(err)
	}
	completed := waitForTerminal(t, manager, created.JobID)
	if completed.State != job.StateCompleted {
		t.Fatalf("delete-response-lost move state = %q", completed.State)
	}
	if _, err := fixture.source.Stat(context.Background(), providerapi.StatRequest{Location: fixture.plan.Source.Location}); !domain.IsCode(err, domain.CodeNotFound) {
		t.Fatalf("source deletion postcondition = %v", err)
	}
}

func TestManagerRestartBetweenCommitAndSourceDeleteRetainsThenFinishes(t *testing.T) {
	fixture := newWorkerFixture(t, []byte("restart-delete-boundary"), ConflictAsk)
	blockingSource := &blockingRemoveProvider{
		mutableTestProvider: fixture.source.(mutableTestProvider),
		started:             make(chan struct{}),
	}
	resolver := MapResolver{
		fixture.source.Descriptor().ID:      blockingSource,
		fixture.destination.Descriptor().ID: fixture.destination,
	}
	store, database := openTransferStore(t, context.Background(), testDatabasePath(t), true)
	t.Cleanup(func() { _ = database.Close() })
	generator := &testkit.SequenceGenerator{}
	manager, err := NewManager(ManagerConfig{
		Store: store, Resolver: resolver, Generator: generator,
		Now: func() time.Time { return time.Unix(1_800_000_530, 0) }, MaxConcurrent: 1,
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := manager.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	created, err := manager.CreateCopy(context.Background(), Intent{
		Clipboard: ClipboardCut, Source: fixture.plan.Source, DestinationDirectory: fixture.plan.DestinationDirectory,
		Name: fixture.plan.RequestedName, ConflictPolicy: ConflictAsk,
	})
	if err != nil {
		t.Fatal(err)
	}
	select {
	case <-blockingSource.started:
	case <-time.After(5 * time.Second):
		t.Fatal("move did not reach source delete boundary")
	}
	manager.Close()
	if _, err := os.Stat(filepath.Join(fixture.sourceRoot, "source")); err != nil {
		t.Fatalf("source was not retained at interrupted delete boundary: %v", err)
	}
	assertWorkerBytes(t, fixture.destination, fixture.plan.Final, []byte("restart-delete-boundary"))
	blockingSource.unblock()

	restarted, err := NewManager(ManagerConfig{
		Store: store, Resolver: resolver, Generator: generator,
		Now: func() time.Time { return time.Unix(1_800_000_531, 0) }, MaxConcurrent: 1,
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(restarted.Close)
	if err := restarted.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	current, err := store.Get(context.Background(), created.JobID)
	if err != nil {
		t.Fatal(err)
	}
	if current.State == job.StatePaused {
		if _, err := restarted.Resume(context.Background(), created.JobID); err != nil {
			t.Fatal(err)
		}
	}
	completed := waitForTerminal(t, restarted, created.JobID)
	if completed.State != job.StateCompleted {
		t.Fatalf("restarted move state = %q", completed.State)
	}
	if _, err := os.Stat(filepath.Join(fixture.sourceRoot, "source")); !os.IsNotExist(err) {
		t.Fatalf("restarted move did not prove source deletion: %v", err)
	}
}

func TestManagerUsesFrozenAtomicRenameFastPathWithoutStreaming(t *testing.T) {
	root := t.TempDir()
	if err := os.Mkdir(filepath.Join(root, "destination"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "source"), []byte("rename-only"), 0o600); err != nil {
		t.Fatal(err)
	}
	base := newPlanTestProvider(t, "ep_aaaaaaaaaaaaaaaaaaaaaaaaaa", root, domain.EndpointLocal)
	implementation := &atomicRenameProvider{endpointKindProvider: base, root: root}
	resolver := MapResolver{implementation.Descriptor().ID: implementation}
	store, database := openTransferStore(t, context.Background(), testDatabasePath(t), true)
	t.Cleanup(func() { _ = database.Close() })
	manager := newStartedTestManager(t, store, resolver, 1_800_000_530)
	reference, err := manager.Capture(context.Background(), normalizePlanTest(t, implementation, "/source"))
	if err != nil {
		t.Fatal(err)
	}
	created, err := manager.CreateCopy(context.Background(), Intent{
		Clipboard: ClipboardCut, Source: reference, DestinationDirectory: normalizePlanTest(t, implementation, "/destination"),
		Name: "moved", ConflictPolicy: ConflictAsk,
	})
	if err != nil {
		t.Fatal(err)
	}
	completed := waitForTerminal(t, manager, created.JobID)
	if completed.State != job.StateCompleted {
		t.Fatalf("atomic rename Job state = %q", completed.State)
	}
	views, err := manager.JobViews(context.Background(), 10)
	if err != nil || len(views) != 1 {
		t.Fatalf("atomic rename Job views = %+v, %v", views, err)
	}
	if views[0].PlannedRoute != RouteAtomicRename || views[0].Route != RouteAtomicRename {
		t.Fatalf("atomic rename Job view = %+v", views[0])
	}
	reads, writes := implementation.streamCounts()
	if reads != 0 || writes != 0 {
		t.Fatalf("atomic rename used streaming: reads=%d writes=%d", reads, writes)
	}
	if _, err := os.Stat(filepath.Join(root, "source")); !os.IsNotExist(err) {
		t.Fatalf("atomic rename source remains: %v", err)
	}
	// #nosec G304 -- path is fixed below this test's private root.
	data, err := os.ReadFile(filepath.Join(root, "destination", "moved"))
	if err != nil || string(data) != "rename-only" {
		t.Fatalf("atomic rename destination = %q, %v", data, err)
	}
}

func TestManagerExplicitDeleteRequiresConfirmationAndRejectsRoot(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "target"), []byte("delete-me"), 0o600); err != nil {
		t.Fatal(err)
	}
	implementation := newPlanTestProvider(t, "ep_aaaaaaaaaaaaaaaaaaaaaaaaaa", root, domain.EndpointLocal)
	resolver := MapResolver{implementation.Descriptor().ID: implementation}
	store, database := openTransferStore(t, context.Background(), testDatabasePath(t), true)
	t.Cleanup(func() { _ = database.Close() })
	manager := newStartedTestManager(t, store, resolver, 1_800_000_540)
	reference, err := manager.Capture(context.Background(), normalizePlanTest(t, implementation, "/target"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := manager.CreateDelete(context.Background(), DeleteIntent{Target: reference}); !domain.IsCode(err, domain.CodeInvalidArgument) {
		t.Fatalf("unconfirmed delete error = %v", err)
	}
	if _, err := implementation.Stat(context.Background(), providerapi.StatRequest{Location: reference.Location}); err != nil {
		t.Fatalf("unconfirmed delete changed target: %v", err)
	}
	rootReference, err := manager.Capture(context.Background(), normalizePlanTest(t, implementation, "/"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := manager.CreateDelete(context.Background(), DeleteIntent{Target: rootReference, Confirmed: true, IrreversibleConfirmed: true}); !domain.IsCode(err, domain.CodeInvalidArgument) {
		t.Fatalf("root delete error = %v", err)
	}
	created, err := manager.CreateDelete(context.Background(), DeleteIntent{Target: reference, Confirmed: true, IrreversibleConfirmed: true})
	if err != nil {
		t.Fatal(err)
	}
	completed := waitForTerminal(t, manager, created.JobID)
	if completed.State != job.StateCompleted {
		t.Fatalf("delete state = %q", completed.State)
	}
	if _, err := implementation.Stat(context.Background(), providerapi.StatRequest{Location: reference.Location}); !domain.IsCode(err, domain.CodeNotFound) {
		t.Fatalf("delete target still exists: %v", err)
	}
}

func TestManagerPrefersAdvertisedTrashWithoutIrreversibleConfirmation(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "target"), []byte("trash-me"), 0o600); err != nil {
		t.Fatal(err)
	}
	base := newPlanTestProvider(t, "ep_aaaaaaaaaaaaaaaaaaaaaaaaaa", root, domain.EndpointLocal)
	implementation := &trashTestProvider{endpointKindProvider: base, root: root}
	resolver := MapResolver{implementation.Descriptor().ID: implementation}
	store, database := openTransferStore(t, context.Background(), testDatabasePath(t), true)
	t.Cleanup(func() { _ = database.Close() })
	manager := newStartedTestManager(t, store, resolver, 1_800_000_545)
	reference, err := manager.CaptureDelete(context.Background(), normalizePlanTest(t, implementation, "/target"))
	if err != nil {
		t.Fatal(err)
	}
	created, err := manager.CreateDelete(context.Background(), DeleteIntent{Target: reference, Confirmed: true})
	if err != nil {
		t.Fatal(err)
	}
	completed := waitForTerminal(t, manager, created.JobID)
	if completed.State != job.StateCompleted || implementation.trashCalls != 1 {
		t.Fatalf("trash delete state=%q calls=%d", completed.State, implementation.trashCalls)
	}
	if _, err := os.Stat(filepath.Join(root, "target")); !os.IsNotExist(err) {
		t.Fatalf("trash source remains: %v", err)
	}
	// #nosec G304 -- the path is a fixed child of this test's private temporary root.
	data, err := os.ReadFile(filepath.Join(root, ".trash-target"))
	if err != nil || string(data) != "trash-me" {
		t.Fatalf("trash payload=%q err=%v", data, err)
	}
}

func TestManagerExplicitDeleteProvesUnknownResponseByPostcondition(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "target"), []byte("delete-me"), 0o600); err != nil {
		t.Fatal(err)
	}
	base := newPlanTestProvider(t, "ep_aaaaaaaaaaaaaaaaaaaaaaaaaa", root, domain.EndpointLocal)
	implementation := &removeResponseLostProvider{mutableTestProvider: base}
	store, database := openTransferStore(t, context.Background(), testDatabasePath(t), true)
	t.Cleanup(func() { _ = database.Close() })
	manager := newStartedTestManager(t, store, MapResolver{base.Descriptor().ID: implementation}, 1_800_000_547)
	reference, err := manager.CaptureDelete(context.Background(), normalizePlanTest(t, implementation, "/target"))
	if err != nil {
		t.Fatal(err)
	}
	created, err := manager.CreateDelete(context.Background(), DeleteIntent{Target: reference, Confirmed: true, IrreversibleConfirmed: true})
	if err != nil {
		t.Fatal(err)
	}
	if completed := waitForTerminal(t, manager, created.JobID); completed.State != job.StateCompleted {
		t.Fatalf("delete response-lost state = %q", completed.State)
	}
	if _, err := os.Stat(filepath.Join(root, "target")); !os.IsNotExist(err) {
		t.Fatalf("delete target remains: %v", err)
	}
}

func TestManagerRecursiveDeleteIsBoundedAndNeverFollowsSymlink(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "target", "nested"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "target", "nested", "file"), []byte("inside"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "outside"), []byte("outside"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("../outside", filepath.Join(root, "target", "link")); err != nil {
		t.Fatal(err)
	}
	implementation := newPlanTestProvider(t, "ep_aaaaaaaaaaaaaaaaaaaaaaaaaa", root, domain.EndpointLocal)
	resolver := MapResolver{implementation.Descriptor().ID: implementation}
	store, database := openTransferStore(t, context.Background(), testDatabasePath(t), true)
	t.Cleanup(func() { _ = database.Close() })
	manager := newStartedTestManager(t, store, resolver, 1_800_000_550)
	reference, err := manager.Capture(context.Background(), normalizePlanTest(t, implementation, "/target"))
	if err != nil {
		t.Fatal(err)
	}
	created, err := manager.CreateDelete(context.Background(), DeleteIntent{
		Target: reference, Recursive: true, Confirmed: true, IrreversibleConfirmed: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	completed := waitForTerminal(t, manager, created.JobID)
	if completed.State != job.StateCompleted {
		t.Fatalf("recursive delete state = %q", completed.State)
	}
	if _, err := os.Stat(filepath.Join(root, "target")); !os.IsNotExist(err) {
		t.Fatalf("recursive target remains: %v", err)
	}
	// #nosec G304 -- path is fixed below this test's private root.
	data, err := os.ReadFile(filepath.Join(root, "outside"))
	if err != nil || string(data) != "outside" {
		t.Fatalf("symlink target changed = %q, %v", data, err)
	}
}

func TestManagerDeletesFrozenSymlinkWithoutFollowingTarget(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "target"), []byte("keep"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("target", filepath.Join(root, "link")); err != nil {
		t.Fatal(err)
	}
	implementation := newPlanTestProvider(t, "ep_aaaaaaaaaaaaaaaaaaaaaaaaaa", root, domain.EndpointLocal)
	store, database := openTransferStore(t, context.Background(), testDatabasePath(t), true)
	t.Cleanup(func() { _ = database.Close() })
	manager := newStartedTestManager(t, store, MapResolver{implementation.Descriptor().ID: implementation}, 1_800_000_555)
	reference, err := manager.CaptureDelete(context.Background(), normalizePlanTest(t, implementation, "/link"))
	if err != nil {
		t.Fatal(err)
	}
	if reference.Kind != domain.EntrySymlink {
		t.Fatalf("captured kind = %q", reference.Kind)
	}
	created, err := manager.CreateDelete(context.Background(), DeleteIntent{Target: reference, Confirmed: true, IrreversibleConfirmed: true})
	if err != nil {
		t.Fatal(err)
	}
	if completed := waitForTerminal(t, manager, created.JobID); completed.State != job.StateCompleted {
		t.Fatalf("symlink delete state = %q", completed.State)
	}
	if _, err := os.Lstat(filepath.Join(root, "link")); !os.IsNotExist(err) {
		t.Fatalf("symlink remains: %v", err)
	}
	// #nosec G304 -- the path is a fixed child of this test's private temporary root.
	data, err := os.ReadFile(filepath.Join(root, "target"))
	if err != nil || string(data) != "keep" {
		t.Fatalf("symlink target=%q err=%v", data, err)
	}
}

func TestManagerResolvesInitialConflictAndRunsFrozenJob(t *testing.T) {
	fixture := newWorkerFixture(t, []byte("replacement"), ConflictAsk)
	if err := os.WriteFile(filepath.Join(fixture.destinationRoot, "final"), []byte("existing"), 0o600); err != nil {
		t.Fatal(err)
	}
	store, database := openTransferStore(t, context.Background(), testDatabasePath(t), true)
	t.Cleanup(func() { _ = database.Close() })
	manager, err := NewManager(ManagerConfig{
		Store: store, Resolver: fixture.resolver, Generator: &testkit.SequenceGenerator{},
		Now: func() time.Time { return time.Unix(1_800_000_600, 0) }, MaxConcurrent: 1,
	})
	if err != nil {
		t.Fatalf("NewManager(): %v", err)
	}
	t.Cleanup(manager.Close)
	if err := manager.Start(context.Background()); err != nil {
		t.Fatalf("Start(): %v", err)
	}
	created, err := manager.CreateCopy(context.Background(), Intent{
		Clipboard: ClipboardCopy, Source: fixture.plan.Source, DestinationDirectory: fixture.plan.DestinationDirectory,
		Name: fixture.plan.RequestedName, ConflictPolicy: ConflictAsk,
	})
	if err != nil {
		t.Fatalf("CreateCopy(): %v", err)
	}
	if created.State != job.StateAwaitingConfirmation {
		t.Fatalf("created state = %q, want awaiting_confirmation", created.State)
	}
	queued, err := manager.ResolveConflict(context.Background(), created.JobID, ConflictOverwrite, true)
	if err != nil {
		t.Fatalf("ResolveConflict(): %v", err)
	}
	if queued.State != job.StateQueued {
		t.Fatalf("resolved state = %q, want queued", queued.State)
	}
	completed := waitForTerminal(t, manager, created.JobID)
	if completed.State != job.StateCompleted {
		t.Fatalf("completed state = %q", completed.State)
	}
	assertWorkerBytes(t, fixture.destination, fixture.plan.Final, []byte("replacement"))
}

func TestManagerPersistsAndResolvesCommitTimeConflict(t *testing.T) {
	fixture := newWorkerFixture(t, []byte("raced-copy"), ConflictAsk)
	readStarted := make(chan struct{})
	readRelease := make(chan struct{})
	gatedSource := &gatedReadProvider{Provider: fixture.source, started: readStarted, release: readRelease}
	resolver := MapResolver{
		fixture.source.Descriptor().ID: gatedSource, fixture.destination.Descriptor().ID: fixture.destination,
	}
	store, database := openTransferStore(t, context.Background(), testDatabasePath(t), true)
	t.Cleanup(func() { _ = database.Close() })
	generator := &testkit.SequenceGenerator{}
	manager, err := NewManager(ManagerConfig{
		Store: store, Resolver: resolver, Generator: generator,
		Now: func() time.Time { return time.Unix(1_800_000_700, 0) }, MaxConcurrent: 1,
	})
	if err != nil {
		t.Fatalf("NewManager(): %v", err)
	}
	t.Cleanup(manager.Close)
	if err := manager.Start(context.Background()); err != nil {
		t.Fatalf("Start(): %v", err)
	}
	created, err := manager.CreateCopy(context.Background(), Intent{
		Clipboard: ClipboardCopy, Source: fixture.plan.Source, DestinationDirectory: fixture.plan.DestinationDirectory,
		Name: fixture.plan.RequestedName, ConflictPolicy: ConflictAsk,
	})
	if err != nil {
		t.Fatalf("CreateCopy(): %v", err)
	}
	select {
	case <-readStarted:
	case <-time.After(5 * time.Second):
		t.Fatal("worker did not reach gated source")
	}
	if err := os.WriteFile(filepath.Join(fixture.destinationRoot, "final"), []byte("racer"), 0o600); err != nil {
		t.Fatal(err)
	}
	close(readRelease)
	waiting := waitForState(t, manager, created.JobID, job.StateWaitingConflict)
	conflicts, err := store.ListConflicts(context.Background(), created.JobID)
	if err != nil {
		t.Fatalf("ListConflicts(): %v", err)
	}
	if len(conflicts) != 1 || conflicts[0].Class != "destination_appeared" || conflicts[0].State != "waiting" {
		t.Fatalf("commit-time conflicts = %#v", conflicts)
	}
	if _, err := manager.ResolveConflict(context.Background(), waiting.JobID, ConflictAutoRename, true); err != nil {
		t.Fatalf("ResolveConflict(): %v", err)
	}
	if completed := waitForTerminal(t, manager, created.JobID); completed.State != job.StateCompleted {
		t.Fatalf("completed state = %q", completed.State)
	}
	assertWorkerBytes(t, fixture.destination, domain.Location{EndpointID: fixture.plan.Final.EndpointID, Path: "/final (1)"}, []byte("raced-copy"))
}

func TestManagerClassifiesPermissionAndDiskFullWithoutFalseSuccess(t *testing.T) {
	tests := []struct {
		name      string
		code      domain.Code
		wantState job.State
	}{
		{name: "permission", code: domain.CodePermissionDenied, wantState: job.StateFailed},
		{name: "disk full", code: domain.CodeResourceExhausted, wantState: job.StateRetryWait},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			fixture := newWorkerFixture(t, []byte("must-not-complete"), ConflictAsk)
			destination := &openWriteFailureProvider{mutableTestProvider: fixture.destination, code: test.code}
			resolver := MapResolver{
				fixture.source.Descriptor().ID: fixture.source, fixture.destination.Descriptor().ID: destination,
			}
			store, database := openTransferStore(t, context.Background(), testDatabasePath(t), true)
			t.Cleanup(func() { _ = database.Close() })
			manager, err := NewManager(ManagerConfig{
				Store: store, Resolver: resolver, Generator: &testkit.SequenceGenerator{},
				Now: func() time.Time { return time.Unix(1_800_000_800, 0) }, MaxConcurrent: 1,
			})
			if err != nil {
				t.Fatalf("NewManager(): %v", err)
			}
			t.Cleanup(manager.Close)
			if err := manager.Start(context.Background()); err != nil {
				t.Fatalf("Start(): %v", err)
			}
			created, err := manager.CreateCopy(context.Background(), Intent{
				Clipboard: ClipboardCopy, Source: fixture.plan.Source, DestinationDirectory: fixture.plan.DestinationDirectory,
				Name: fixture.plan.RequestedName, ConflictPolicy: ConflictAsk,
			})
			if err != nil {
				t.Fatalf("CreateCopy(): %v", err)
			}
			var result jobstore.Snapshot
			if test.wantState.Terminal() {
				result = waitForTerminal(t, manager, created.JobID)
			} else {
				result = waitForState(t, manager, created.JobID, test.wantState)
			}
			if result.State != test.wantState {
				t.Fatalf("state = %q, want %q", result.State, test.wantState)
			}
			views, err := manager.JobViews(context.Background(), 10)
			if err != nil || len(views) != 1 || views[0].RecentError == "" {
				t.Fatalf("failure Job view = (%#v, %v)", views, err)
			}
			if _, err := fixture.destination.Stat(context.Background(), providerapi.StatRequest{Location: fixture.plan.Final}); !domain.IsCode(err, domain.CodeNotFound) {
				t.Fatalf("failure exposed final: %v", err)
			}
		})
	}
}

func TestManagerRestartRevalidatesPartAfterAbruptHandleClose(t *testing.T) {
	fixture := newWorkerFixture(t, []byte("daemon-restart"), ConflictAsk)
	readStarted := make(chan struct{})
	readRelease := make(chan struct{})
	gatedSource := &gatedReadProvider{Provider: fixture.source, started: readStarted, release: readRelease}
	closingDestination := &closeTimestampProvider{mutableTestProvider: fixture.destination, root: fixture.destinationRoot}
	resolver := MapResolver{
		fixture.source.Descriptor().ID: gatedSource, fixture.destination.Descriptor().ID: closingDestination,
	}
	store, database := openTransferStore(t, context.Background(), testDatabasePath(t), true)
	t.Cleanup(func() { _ = database.Close() })
	generator := &testkit.SequenceGenerator{}
	manager, err := NewManager(ManagerConfig{
		Store: store, Resolver: resolver, Generator: generator,
		Now: func() time.Time { return time.Unix(1_800_000_900, 0) }, MaxConcurrent: 1,
	})
	if err != nil {
		t.Fatalf("NewManager(): %v", err)
	}
	if err := manager.Start(context.Background()); err != nil {
		t.Fatalf("Start(): %v", err)
	}
	created, err := manager.CreateCopy(context.Background(), Intent{
		Clipboard: ClipboardCopy, Source: fixture.plan.Source, DestinationDirectory: fixture.plan.DestinationDirectory,
		Name: fixture.plan.RequestedName, ConflictPolicy: ConflictAsk,
	})
	if err != nil {
		t.Fatalf("CreateCopy(): %v", err)
	}
	select {
	case <-readStarted:
	case <-time.After(5 * time.Second):
		t.Fatal("worker did not reach gated read")
	}
	manager.Close()

	restarted, err := NewManager(ManagerConfig{
		Store: store, Resolver: fixture.resolver, Generator: generator,
		Now: func() time.Time { return time.Unix(1_800_000_901, 0) }, MaxConcurrent: 1,
	})
	if err != nil {
		t.Fatalf("NewManager(restart): %v", err)
	}
	t.Cleanup(restarted.Close)
	if err := restarted.Start(context.Background()); err != nil {
		t.Fatalf("Start(restart): %v", err)
	}
	paused := waitForState(t, restarted, created.JobID, job.StatePaused)
	if _, err := restarted.Resume(context.Background(), paused.JobID); err != nil {
		t.Fatalf("Resume(): %v", err)
	}
	waitContext, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	completed, err := restarted.Wait(waitContext, created.JobID)
	if err != nil {
		current, _ := store.Get(context.Background(), created.JobID)
		checkpoint, _ := (JobJournal{Store: store, StepIndex: 0}).Load(context.Background(), created.JobID)
		t.Fatalf("Wait(): %v; current=%#v checkpoint=%#v", err, current, checkpoint)
	}
	if completed.State != job.StateCompleted {
		t.Fatalf("completed state = %q", completed.State)
	}
	views, err := restarted.JobViews(context.Background(), 10)
	if err != nil || len(views) != 1 || views[0].RecoveryResult == "" {
		t.Fatalf("recovered Job view = (%#v, %v)", views, err)
	}
	assertWorkerBytes(t, fixture.destination, fixture.plan.Final, []byte("daemon-restart"))
}

func TestManagerRestartResumesDirectoryFromOwnedRoot(t *testing.T) {
	sourceRoot := t.TempDir()
	destinationRoot := t.TempDir()
	if err := os.MkdirAll(filepath.Join(sourceRoot, "tree"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(sourceRoot, "tree", "payload"), []byte("directory-restart"), 0o600); err != nil {
		t.Fatal(err)
	}
	source := newPlanTestProvider(t, "ep_aaaaaaaaaaaaaaaaaaaaaaaaaa", sourceRoot, domain.EndpointLocal)
	destination := newPlanTestProvider(t, "ep_bbbbbbbbbbbbbbbbbbbbbbbbbb", destinationRoot, domain.EndpointLocal)
	started := make(chan struct{})
	release := make(chan struct{})
	gated := &gatedReadProvider{Provider: source, started: started, release: release}
	resolver := MapResolver{source.Descriptor().ID: gated, destination.Descriptor().ID: destination}
	store, database := openTransferStore(t, context.Background(), testDatabasePath(t), true)
	t.Cleanup(func() { _ = database.Close() })
	generator := &testkit.SequenceGenerator{}
	manager, err := NewManager(ManagerConfig{
		Store: store, Resolver: resolver, Generator: generator,
		Now: func() time.Time { return time.Unix(1_800_000_910, 0) }, MaxConcurrent: 1,
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := manager.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	reference, err := manager.Capture(context.Background(), normalizePlanTest(t, source, "/tree"))
	if err != nil {
		t.Fatal(err)
	}
	created, err := manager.CreateCopy(context.Background(), Intent{
		Clipboard: ClipboardCopy, Source: reference, DestinationDirectory: normalizePlanTest(t, destination, "/"),
		Name: "copied", ConflictPolicy: ConflictAsk,
	})
	if err != nil {
		t.Fatal(err)
	}
	select {
	case <-started:
	case <-time.After(5 * time.Second):
		t.Fatal("directory Job did not reach file read")
	}
	manager.Close()

	restartedResolver := MapResolver{source.Descriptor().ID: source, destination.Descriptor().ID: destination}
	restarted, err := NewManager(ManagerConfig{
		Store: store, Resolver: restartedResolver, Generator: generator,
		Now: func() time.Time { return time.Unix(1_800_000_911, 0) }, MaxConcurrent: 1,
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(restarted.Close)
	if err := restarted.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	paused := waitForState(t, restarted, created.JobID, job.StatePaused)
	if _, err := restarted.Resume(context.Background(), paused.JobID); err != nil {
		t.Fatal(err)
	}
	completed := waitForTerminal(t, restarted, created.JobID)
	if completed.State != job.StateCompleted {
		t.Fatalf("restarted directory state = %q", completed.State)
	}
	// #nosec G304 -- path is fixed below this test's private destination root.
	data, err := os.ReadFile(filepath.Join(destinationRoot, "copied", "payload"))
	if err != nil || string(data) != "directory-restart" {
		t.Fatalf("restarted directory output = %q, %v", data, err)
	}
}

func TestManagerNeverPersistsProviderErrorDetails(t *testing.T) {
	const secret = "stage2-provider-secret-canary"
	fixture := newWorkerFixture(t, []byte("secret-scan"), ConflictAsk)
	destination := &openWriteFailureProvider{
		mutableTestProvider: fixture.destination, code: domain.CodePermissionDenied, message: secret,
	}
	resolver := MapResolver{
		fixture.source.Descriptor().ID: fixture.source, fixture.destination.Descriptor().ID: destination,
	}
	databasePath := testDatabasePath(t)
	store, database := openTransferStore(t, context.Background(), databasePath, true)
	t.Cleanup(func() { _ = database.Close() })
	manager, err := NewManager(ManagerConfig{
		Store: store, Resolver: resolver, Generator: &testkit.SequenceGenerator{},
		Now: func() time.Time { return time.Unix(1_800_001_000, 0) }, MaxConcurrent: 1,
	})
	if err != nil {
		t.Fatalf("NewManager(): %v", err)
	}
	t.Cleanup(manager.Close)
	if err := manager.Start(context.Background()); err != nil {
		t.Fatalf("Start(): %v", err)
	}
	created, err := manager.CreateCopy(context.Background(), Intent{
		Clipboard: ClipboardCopy, Source: fixture.plan.Source, DestinationDirectory: fixture.plan.DestinationDirectory,
		Name: fixture.plan.RequestedName, ConflictPolicy: ConflictAsk,
	})
	if err != nil {
		t.Fatalf("CreateCopy(): %v", err)
	}
	failed := waitForTerminal(t, manager, created.JobID)
	if failed.State != job.StateFailed {
		t.Fatalf("failed state = %q", failed.State)
	}
	if failed.TerminalSummary != nil && strings.Contains(*failed.TerminalSummary, secret) {
		t.Fatalf("terminal summary persisted secret: %q", *failed.TerminalSummary)
	}
	events, err := manager.Events(context.Background(), created.JobID, 0, 20)
	if err != nil {
		t.Fatalf("Events(): %v", err)
	}
	for _, event := range events {
		if strings.Contains(event.PayloadJSON, secret) {
			t.Fatalf("event persisted secret: %s", event.PayloadJSON)
		}
	}
	// Wait for the manager worker to leave its terminal transition before
	// asserting that the store is idle enough for a WAL truncate.
	manager.Close()
	if err := store.CheckpointIdle(context.Background()); err != nil {
		t.Fatalf("CheckpointIdle(): %v", err)
	}
	for _, path := range []string{databasePath, databasePath + "-wal", databasePath + "-shm"} {
		// #nosec G304 -- paths are fixed suffixes of this test's private database fixture.
		data, err := os.ReadFile(path)
		if err != nil && !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("read state artifact %s: %v", path, err)
		}
		if strings.Contains(string(data), secret) {
			t.Fatalf("state artifact %s persisted secret", path)
		}
	}
}

func waitForTerminal(t *testing.T, manager *Manager, jobID domain.JobID) jobstore.Snapshot {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	snapshot, err := manager.Wait(ctx, jobID)
	if err != nil {
		t.Fatalf("Wait(): %v", err)
	}
	return snapshot
}

func waitForState(t *testing.T, manager *Manager, jobID domain.JobID, state job.State) jobstore.Snapshot {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	snapshot, err := manager.WaitForState(ctx, jobID, state)
	if err != nil {
		t.Fatalf("WaitForState(%q): %v", state, err)
	}
	return snapshot
}

func testDatabasePath(t *testing.T) string {
	t.Helper()
	root := testkit.PersistentTempDir(t)
	if err := os.Chmod(root, 0o700); err != nil { //nolint:gosec // state root must be owner-private
		t.Fatal(err)
	}
	return filepath.Join(root, "state.sqlite3")
}

func newStartedTestManager(t *testing.T, store *jobstore.Store, resolver Resolver, unixTime int64) *Manager {
	t.Helper()
	manager, err := NewManager(ManagerConfig{
		Store: store, Resolver: resolver, Generator: &testkit.SequenceGenerator{},
		Now: func() time.Time { return time.Unix(unixTime, 0) }, MaxConcurrent: 1,
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(manager.Close)
	if err := manager.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	return manager
}

type mutateSourceOnRenameProvider struct {
	mutableTestProvider
	sourcePath string
}

func (provider *mutateSourceOnRenameProvider) Rename(ctx context.Context, request providerapi.RenameRequest) (providerapi.RenameResult, error) {
	result, err := provider.mutableTestProvider.Rename(ctx, request)
	if err != nil {
		return result, err
	}
	// #nosec G306 -- the private source fixture remains owner-only.
	if err := os.WriteFile(provider.sourcePath, []byte("changed-after-commit"), 0o600); err != nil {
		return result, err
	}
	return result, nil
}

type removeResponseLostProvider struct{ mutableTestProvider }

func (provider *removeResponseLostProvider) Remove(ctx context.Context, request providerapi.RemoveRequest) error {
	if err := provider.mutableTestProvider.Remove(ctx, request); err != nil {
		return err
	}
	location := request.Location
	return &domain.OpError{
		Code: domain.CodeTransportInterrupted, Message: "delete response lost", Operation: "remove",
		EndpointID: request.Location.EndpointID, Location: &location,
		Retry: domain.RetryAdvice{Kind: domain.RetryAfterReconnect}, Effect: domain.EffectUnknown,
	}
}

type blockingRemoveProvider struct {
	mutableTestProvider
	mu      sync.Mutex
	started chan struct{}
	once    sync.Once
	blocked bool
}

func (provider *blockingRemoveProvider) Remove(ctx context.Context, request providerapi.RemoveRequest) error {
	provider.mu.Lock()
	blocked := !provider.blocked
	provider.mu.Unlock()
	if blocked {
		provider.once.Do(func() { close(provider.started) })
		<-ctx.Done()
		return domain.FromContext("remove", request.Location.EndpointID, &request.Location, ctx.Err())
	}
	return provider.mutableTestProvider.Remove(ctx, request)
}

func (provider *blockingRemoveProvider) unblock() {
	provider.mu.Lock()
	provider.blocked = true
	provider.mu.Unlock()
}

type atomicRenameProvider struct {
	*endpointKindProvider
	root   string
	mu     sync.Mutex
	reads  int
	writes int
}

type trashTestProvider struct {
	*endpointKindProvider
	root       string
	trashCalls int
}

func (provider *trashTestProvider) Snapshot(ctx context.Context) (domain.EndpointSnapshot, error) {
	snapshot, err := provider.endpointKindProvider.Snapshot(ctx)
	if err != nil {
		return domain.EndpointSnapshot{}, err
	}
	items := append(snapshot.Capabilities.Items, domain.Capability{Name: "trash", Version: 1})
	snapshot.Capabilities, err = domain.NewCapabilitySnapshot(snapshot.Capabilities.Revision, true, items)
	return snapshot, err
}

func (provider *trashTestProvider) Trash(_ context.Context, request providerapi.TrashRequest) error {
	provider.trashCalls++
	source := filepath.Join(provider.root, filepath.FromSlash(strings.TrimPrefix(string(request.Location.Path), "/")))
	return os.Rename(source, filepath.Join(provider.root, ".trash-"+filepath.Base(source)))
}

func (provider *atomicRenameProvider) Snapshot(ctx context.Context) (domain.EndpointSnapshot, error) {
	snapshot, err := provider.endpointKindProvider.Snapshot(ctx)
	if err != nil {
		return domain.EndpointSnapshot{}, err
	}
	items := append(snapshot.Capabilities.Items, domain.Capability{Name: "atomic_rename", Version: 1})
	snapshot.Capabilities, err = domain.NewCapabilitySnapshot(snapshot.Capabilities.Revision, true, items)
	return snapshot, err
}

func (provider *atomicRenameProvider) OpenRead(ctx context.Context, request providerapi.OpenReadRequest) (providerapi.ReadHandle, error) {
	provider.mu.Lock()
	provider.reads++
	provider.mu.Unlock()
	return provider.endpointKindProvider.OpenRead(ctx, request)
}

func (provider *atomicRenameProvider) OpenWrite(ctx context.Context, request providerapi.OpenWriteRequest) (providerapi.WriteHandle, error) {
	provider.mu.Lock()
	provider.writes++
	provider.mu.Unlock()
	return provider.endpointKindProvider.OpenWrite(ctx, request)
}

func (provider *atomicRenameProvider) Rename(_ context.Context, request providerapi.RenameRequest) (providerapi.RenameResult, error) {
	source := filepath.Join(provider.root, filepath.FromSlash(strings.TrimPrefix(string(request.Source.Path), "/")))
	destination := filepath.Join(provider.root, filepath.FromSlash(strings.TrimPrefix(string(request.Destination.Path), "/")))
	if err := os.Rename(source, destination); err != nil {
		return providerapi.RenameResult{}, err
	}
	return providerapi.RenameResult{Atomic: true, Replaced: request.Replace}, nil
}

func (provider *atomicRenameProvider) streamCounts() (int, int) {
	provider.mu.Lock()
	defer provider.mu.Unlock()
	return provider.reads, provider.writes
}

type gatedPlanAcquirer struct {
	Resolver
	acquireStarted chan struct{}
	acquireRelease chan struct{}
	startedOnce    sync.Once
	releasedOnce   sync.Once
	mu             sync.Mutex
	active         int
	leaseReleased  chan struct{}
}

func (acquirer *gatedPlanAcquirer) Acquire(ctx context.Context, _ Plan) (func(), error) {
	acquirer.startedOnce.Do(func() { close(acquirer.acquireStarted) })
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	case <-acquirer.acquireRelease:
	}
	acquirer.mu.Lock()
	acquirer.active++
	acquirer.mu.Unlock()
	var once sync.Once
	return func() {
		once.Do(func() {
			acquirer.mu.Lock()
			acquirer.active--
			acquirer.mu.Unlock()
			acquirer.releasedOnce.Do(func() { close(acquirer.leaseReleased) })
		})
	}, nil
}

func (acquirer *gatedPlanAcquirer) activeLeases() int {
	acquirer.mu.Lock()
	defer acquirer.mu.Unlock()
	return acquirer.active
}

type gatedReadProvider struct {
	providerapi.Provider
	started chan struct{}
	release chan struct{}
	once    sync.Once
}

func (provider *gatedReadProvider) OpenRead(ctx context.Context, request providerapi.OpenReadRequest) (providerapi.ReadHandle, error) {
	handle, err := provider.Provider.OpenRead(ctx, request)
	if err != nil {
		return nil, err
	}
	return &gatedReadHandle{ReadHandle: handle, provider: provider}, nil
}

type gatedReadHandle struct {
	providerapi.ReadHandle
	provider *gatedReadProvider
}

func (handle *gatedReadHandle) Read(ctx context.Context, buffer []byte) (int, error) {
	handle.provider.once.Do(func() { close(handle.provider.started) })
	select {
	case <-ctx.Done():
		return 0, ctx.Err()
	case <-handle.provider.release:
		return handle.ReadHandle.Read(ctx, buffer)
	}
}
