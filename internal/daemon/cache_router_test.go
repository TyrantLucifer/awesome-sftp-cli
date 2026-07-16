package daemon

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/TyrantLucifer/awesome-mac-sftp/internal/cache"
	"github.com/TyrantLucifer/awesome-mac-sftp/internal/cachefs"
	"github.com/TyrantLucifer/awesome-mac-sftp/internal/cachemanager"
	"github.com/TyrantLucifer/awesome-mac-sftp/internal/cacheprocess"
	"github.com/TyrantLucifer/awesome-mac-sftp/internal/domain"
	"github.com/TyrantLucifer/awesome-mac-sftp/internal/foundation"
	"github.com/TyrantLucifer/awesome-mac-sftp/internal/ipc"
	"github.com/TyrantLucifer/awesome-mac-sftp/internal/provider"
	"github.com/TyrantLucifer/awesome-mac-sftp/internal/provider/localfs"
	"github.com/TyrantLucifer/awesome-mac-sftp/internal/state/cachestore"
	"github.com/TyrantLucifer/awesome-mac-sftp/internal/statefs"
	"github.com/TyrantLucifer/awesome-mac-sftp/internal/testkit"
)

func TestCacheMaterializeReadsCompleteProviderFileAndReturnsLeasedPrivatePath(t *testing.T) {
	ctx := context.Background()
	root := testkit.PersistentTempDir(t)
	content := []byte("materialized through daemon")
	sourcePath := filepath.Join(root, "source.txt")
	if err := os.WriteFile(sourcePath, content, 0o600); err != nil {
		t.Fatal(err)
	}
	endpointID := domain.EndpointID("ep_aaaaaaaaaaaaaaaaaaaaaaaaaa")
	sessionID := domain.SessionID("sess_aaaaaaaaaaaaaaaaaaaaaaaaaa")
	implementation, err := localfs.New(localfs.Config{Endpoint: domain.Endpoint{ID: endpointID, Kind: domain.EndpointLocal, DisplayName: "local"}, SessionID: sessionID, Root: "/"})
	if err != nil {
		t.Fatal(err)
	}
	sessions, err := NewProviderSessions([]provider.Provider{implementation}, 64<<10)
	if err != nil {
		t.Fatal(err)
	}
	manager := newDaemonCacheManager(t, ctx, root)
	sessions.SetCacheManager(manager)
	session := sessions.NewSession()
	defer session.Close()
	location, err := domain.NewLocation(endpointID, domain.CanonicalPath(sourcePath))
	if err != nil {
		t.Fatal(err)
	}
	identity, err := cacheprocess.CurrentIdentity()
	if err != nil {
		t.Fatal(err)
	}
	payload, err := json.Marshal(CacheMaterializeRequest{
		Location: ipc.EncodeLocation(location), WorkspaceID: "workspace", Policy: cache.PolicyLRU,
		OwnerKind: cache.LeaseOwnerEditor, OwnerID: "edit-session", Process: &identity,
	})
	if err != nil {
		t.Fatal(err)
	}
	value, err := session.Handle(ctx, CacheMaterialize, payload)
	if err != nil {
		t.Fatal(err)
	}
	response, ok := value.(CacheMaterializeResponse)
	if !ok {
		t.Fatalf("response type = %T", value)
	}
	got, err := os.ReadFile(response.Path)
	if err != nil || string(got) != string(content) {
		t.Fatalf("materialized = %q, %v", got, err)
	}
	metadata, err := os.Lstat(response.Path)
	if err != nil || !metadata.Mode().IsRegular() || metadata.Mode().Perm() != 0o600 {
		t.Fatalf("materialized metadata = %#v, %v", metadata, err)
	}
	if _, err := cache.ParseEntryID(string(response.EntryID)); err != nil {
		t.Fatal(err)
	}
	if _, err := cache.ParseMaterializationID(string(response.MaterializationID)); err != nil {
		t.Fatal(err)
	}
	if _, err := cache.ParseLeaseID(string(response.LeaseID)); err != nil {
		t.Fatal(err)
	}
	reconcile, err := manager.Reconcile(ctx, 100)
	if err != nil || len(reconcile.Snapshot.Leases) != 1 || reconcile.Snapshot.Leases[0].Process == nil || *reconcile.Snapshot.Leases[0].Process != identity {
		t.Fatalf("caller process identity was not persisted: %#v, %v", reconcile.Snapshot.Leases, err)
	}
	changed := []byte("edited through external process")
	if err := os.WriteFile(response.Path, changed, 0o600); err != nil {
		t.Fatal(err)
	}
	markPayload, err := json.Marshal(CacheMarkDirtyRequest{
		MaterializationID: response.MaterializationID, ReferenceID: response.ReferenceID, LeaseID: response.LeaseID,
		OwnerKind: cache.LeaseOwnerEditor, OwnerID: "edit-session",
	})
	if err != nil {
		t.Fatal(err)
	}
	value, err = session.Handle(ctx, CacheMarkDirty, markPayload)
	if err != nil {
		t.Fatal(err)
	}
	marked, ok := value.(CacheMarkDirtyResponse)
	if !ok || !marked.Dirty || marked.Size != int64(len(changed)) {
		t.Fatalf("mark dirty response = %#v", value)
	}
	releasePayload, err := json.Marshal(CacheReleaseHandoffRequest{
		MaterializationID: response.MaterializationID, ReferenceID: response.ReferenceID, LeaseID: response.LeaseID,
		OwnerKind: cache.LeaseOwnerEditor, OwnerID: "edit-session",
	})
	if err != nil {
		t.Fatal(err)
	}
	for attempt := 0; attempt < 2; attempt++ {
		value, err := session.Handle(ctx, CacheReleaseHandoff, releasePayload)
		if err != nil {
			t.Fatalf("release attempt %d: %v", attempt+1, err)
		}
		if released, ok := value.(CacheReleaseHandoffResponse); !ok || !released.Released {
			t.Fatalf("release attempt %d response = %#v", attempt+1, value)
		}
	}
}

func TestCacheMaterializeFailsClosedWhenCacheUnavailableOrTargetIsDirectory(t *testing.T) {
	ctx := context.Background()
	root := testkit.PersistentTempDir(t)
	endpointID := domain.EndpointID("ep_aaaaaaaaaaaaaaaaaaaaaaaaaa")
	implementation, err := localfs.New(localfs.Config{Endpoint: domain.Endpoint{ID: endpointID, Kind: domain.EndpointLocal}, SessionID: "sess_aaaaaaaaaaaaaaaaaaaaaaaaaa", Root: "/"})
	if err != nil {
		t.Fatal(err)
	}
	sessions, err := NewProviderSessions([]provider.Provider{implementation}, 64<<10)
	if err != nil {
		t.Fatal(err)
	}
	location, _ := domain.NewLocation(endpointID, domain.CanonicalPath(root))
	payload, _ := json.Marshal(CacheMaterializeRequest{Location: ipc.EncodeLocation(location), WorkspaceID: "workspace", Policy: cache.PolicyLRU, OwnerKind: cache.LeaseOwnerEditor, OwnerID: "edit-session"})
	session := sessions.NewSession()
	defer session.Close()
	if _, err := session.Handle(ctx, CacheMaterialize, payload); err == nil {
		t.Fatal("materialize succeeded without cache manager")
	}
	sessions.SetCacheManager(newDaemonCacheManager(t, ctx, root))
	second := sessions.NewSession()
	defer second.Close()
	if _, err := second.Handle(ctx, CacheMaterialize, payload); err == nil {
		t.Fatal("materialize accepted a directory")
	}
}

func newDaemonCacheManager(t *testing.T, ctx context.Context, root string) *cachemanager.Manager {
	t.Helper()
	stateRoot := filepath.Join(root, "state-"+strings.Repeat("s", 4))
	if err := os.Mkdir(stateRoot, 0o700); err != nil {
		t.Fatal(err)
	}
	database, _, err := statefs.Initialize(ctx, statefs.InitializeConfig{Root: stateRoot, DatabasePath: filepath.Join(stateRoot, "state.db")})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = database.Close() })
	cacheRoot := filepath.Join(root, "cache-"+strings.Repeat("c", 4))
	if err := os.Mkdir(cacheRoot, 0o700); err != nil && !os.IsExist(err) {
		t.Fatal(err)
	}
	files, err := cachefs.Initialize(cacheRoot)
	if err != nil {
		t.Fatal(err)
	}
	catalog, err := cachestore.New(ctx, database)
	if err != nil {
		t.Fatal(err)
	}
	manager, err := cachemanager.New(files, catalog, foundation.RealClock{}, strings.Repeat("d", 32), cache.DefaultLimits())
	if err != nil {
		t.Fatal(err)
	}
	return manager
}
