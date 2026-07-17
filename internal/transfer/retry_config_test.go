package transfer

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/TyrantLucifer/awesome-mac-sftp/internal/testkit"
)

func TestManagerRetryAtUsesFrozenDelay(t *testing.T) {
	now := time.Unix(1_800_000_000, 0)
	manager := &Manager{now: func() time.Time { return now }, retryDelay: 2 * time.Minute}
	if got, want := manager.nextRetryAt(), now.Add(2*time.Minute); !got.Equal(want) {
		t.Fatalf("retry at = %v, want %v", got, want)
	}
}

func TestNewManagerFreezesConfiguredRetryDelay(t *testing.T) {
	fixture := newWorkerFixture(t, []byte("retry config"), ConflictOverwrite)
	store, database := openTransferStore(t, context.Background(), filepath.Join(t.TempDir(), "state.db"), true)
	t.Cleanup(func() { _ = database.Close() })
	manager, err := NewManager(ManagerConfig{
		Store: store, Resolver: fixture.resolver, Generator: &testkit.SequenceGenerator{},
		MaxConcurrent: 1, RetryDelay: 2 * time.Minute,
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(manager.Close)
	if manager.retryDelay != 2*time.Minute {
		t.Fatalf("manager retry delay = %v, want %v", manager.retryDelay, 2*time.Minute)
	}

	_, err = NewManager(ManagerConfig{
		Store: store, Resolver: fixture.resolver, Generator: &testkit.SequenceGenerator{},
		MaxConcurrent: 1, RetryDelay: time.Minute - time.Millisecond,
	})
	if err == nil {
		t.Fatal("NewManager accepted a retry delay more aggressive than the frozen default")
	}
}
