package transfer

import (
	"context"
	"os"
	"path/filepath"
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
	manager, err := NewManager(ManagerConfig{
		Store: store, Resolver: resolver, Generator: &testkit.SequenceGenerator{},
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

func TestManagerCutCompletesWithSourceRetainedUntilDeleteStepExists(t *testing.T) {
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
	if completed.State != job.StateCompletedWithSourceRetained {
		t.Fatalf("cut Job state = %q, want completed_with_source_retained", completed.State)
	}
	if _, err := fixture.source.Stat(context.Background(), providerapi.StatRequest{Location: fixture.plan.Source.Location}); err != nil {
		t.Fatalf("cut source was not retained: %v", err)
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
	manager, err := NewManager(ManagerConfig{
		Store: store, Resolver: resolver, Generator: &testkit.SequenceGenerator{},
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
