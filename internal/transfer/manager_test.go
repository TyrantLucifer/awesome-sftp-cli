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
