package transfer

import (
	"context"
	"github.com/TyrantLucifer/awesome-sftp-cli/internal/job"
	providerapi "github.com/TyrantLucifer/awesome-sftp-cli/internal/provider"
	"github.com/TyrantLucifer/awesome-sftp-cli/internal/testkit"
	"sync"
	"testing"
	"time"
)

func TestManagerPublishesVerifyingStateBeforeVerificationFinishes(t *testing.T) {
	fixture := newWorkerFixture(t, []byte("verify-stage"), ConflictAsk)
	started := make(chan struct{})
	release := make(chan struct{})
	defer close(release)
	fixture.resolver[fixture.destination.Descriptor().ID] = &verificationGateProvider{mutableTestProvider: fixture.destination, started: started, release: release}
	store, db := openTransferStore(t, context.Background(), testDatabasePath(t), true)
	t.Cleanup(func() { _ = db.Close() })
	manager, err := NewManager(ManagerConfig{Store: store, Resolver: fixture.resolver, Generator: &testkit.SequenceGenerator{}, MaxConcurrent: 1})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(manager.Close)
	if err := manager.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	created, err := manager.CreateCopy(context.Background(), Intent{Verification: VerifySHA256, Clipboard: ClipboardCopy, Source: fixture.plan.Source, DestinationDirectory: fixture.plan.DestinationDirectory, Name: fixture.plan.RequestedName, ConflictPolicy: ConflictAsk})
	if err != nil {
		t.Fatal(err)
	}
	select {
	case <-started:
	case <-time.After(3 * time.Second):
		t.Fatal("verification did not start")
	}
	snapshot, err := store.Get(context.Background(), created.JobID)
	if err != nil || snapshot.State != job.StateVerifying {
		t.Fatalf("state during verification=%s, error=%v", snapshot.State, err)
	}
}

type verificationGateProvider struct {
	mutableTestProvider
	started chan struct{}
	release <-chan struct{}
	once    sync.Once
}

func (p *verificationGateProvider) OpenRead(ctx context.Context, request providerapi.OpenReadRequest) (providerapi.ReadHandle, error) {
	p.once.Do(func() {
		close(p.started)
		select {
		case <-p.release:
		case <-ctx.Done():
		}
	})
	return p.mutableTestProvider.OpenRead(ctx, request)
}
