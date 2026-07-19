package daemon

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/TyrantLucifer/awesome-sftp-cli/internal/cache"
	"github.com/TyrantLucifer/awesome-sftp-cli/internal/cachefs"
	"github.com/TyrantLucifer/awesome-sftp-cli/internal/cachemanager"
	"github.com/TyrantLucifer/awesome-sftp-cli/internal/cacheprocess"
	"github.com/TyrantLucifer/awesome-sftp-cli/internal/domain"
	"github.com/TyrantLucifer/awesome-sftp-cli/internal/foundation"
	"github.com/TyrantLucifer/awesome-sftp-cli/internal/ipc"
	"github.com/TyrantLucifer/awesome-sftp-cli/internal/provider"
	"github.com/TyrantLucifer/awesome-sftp-cli/internal/provider/localfs"
	"github.com/TyrantLucifer/awesome-sftp-cli/internal/state/cachestore"
	"github.com/TyrantLucifer/awesome-sftp-cli/internal/statefs"
	"github.com/TyrantLucifer/awesome-sftp-cli/internal/testkit"
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
	heartbeatPayload, err := json.Marshal(CacheHeartbeatRequest{
		MaterializationID: response.MaterializationID, LeaseID: response.LeaseID,
		OwnerKind: cache.LeaseOwnerEditor, OwnerID: "edit-session", Process: identity,
	})
	if err != nil {
		t.Fatal(err)
	}
	value, err = session.Handle(ctx, CacheHeartbeat, heartbeatPayload)
	if err != nil {
		t.Fatal(err)
	}
	if heartbeat, ok := value.(CacheHeartbeatResponse); !ok || !heartbeat.Renewed || heartbeat.GraceUntilUnix != heartbeat.ExpiresAtUnix {
		t.Fatalf("heartbeat response = %#v", value)
	}
	reused := identity
	reused.BirthID += "-reused"
	heartbeatPayload, _ = json.Marshal(CacheHeartbeatRequest{
		MaterializationID: response.MaterializationID, LeaseID: response.LeaseID,
		OwnerKind: cache.LeaseOwnerEditor, OwnerID: "edit-session", Process: reused,
	})
	if _, err := session.Handle(ctx, CacheHeartbeat, heartbeatPayload); err == nil {
		t.Fatal("heartbeat accepted a reused process identity")
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
	clearPayload, err := json.Marshal(CacheClearRequest{Scope: cachemanager.ClearAll, MaxTargets: 16, MaxVisited: 100})
	if err != nil {
		t.Fatal(err)
	}
	value, err = session.Handle(ctx, CacheClear, clearPayload)
	if err != nil {
		t.Fatal(err)
	}
	cleared, ok := value.(CacheClearResponse)
	if !ok || cleared.Deleted != 0 || cleared.Protected == 0 || cleared.Status.Dirty != 1 || cleared.Status.NeedsAttention {
		t.Fatalf("clear response = %#v", value)
	}
	lifecyclePayload, _ := json.Marshal(CacheLifecycleRequest{MaxVisited: 100, MaxBatches: 2})
	value, err = session.Handle(ctx, CacheLifecycle, lifecyclePayload)
	if err != nil {
		t.Fatal(err)
	}
	if lifecycle, ok := value.(CacheLifecycleResponse); !ok || lifecycle.Status.Dirty != 1 || lifecycle.Status.NeedsAttention {
		t.Fatalf("lifecycle response = %#v", value)
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

func TestCacheMaterializeReopensOneVerifiedPinnedEntryOfflineAndRevalidatesOnline(t *testing.T) {
	ctx := context.Background()
	root := testkit.PersistentTempDir(t)
	sourcePath := filepath.Join(root, "offline.txt")
	if err := os.WriteFile(sourcePath, []byte("first verified version"), 0o600); err != nil {
		t.Fatal(err)
	}
	endpointID := domain.EndpointID("ep_aaaaaaaaaaaaaaaaaaaaaaaaaa")
	local, err := localfs.New(localfs.Config{Endpoint: domain.Endpoint{ID: endpointID, Kind: domain.EndpointLocal}, SessionID: "sess_aaaaaaaaaaaaaaaaaaaaaaaaaa", Root: "/"})
	if err != nil {
		t.Fatal(err)
	}
	implementation := &disconnectingProvider{Provider: local}
	sessions, err := NewProviderSessions([]provider.Provider{implementation}, 64<<10)
	if err != nil {
		t.Fatal(err)
	}
	sessions.SetCacheManager(newDaemonCacheManager(t, ctx, root))
	session := sessions.NewSession()
	defer session.Close()
	location, _ := domain.NewLocation(endpointID, domain.CanonicalPath(sourcePath))
	request := CacheMaterializeRequest{
		Location: ipc.EncodeLocation(location), WorkspaceID: "workspace", Policy: cache.PolicyPinnedOffline, Pinned: true,
		OwnerKind: cache.LeaseOwnerPreview, OwnerID: "preview-online",
	}
	materialize := func(request CacheMaterializeRequest) (CacheMaterializeResponse, error) {
		payload, marshalErr := json.Marshal(request)
		if marshalErr != nil {
			t.Fatal(marshalErr)
		}
		value, handleErr := session.Handle(ctx, CacheMaterialize, payload)
		if handleErr != nil {
			return CacheMaterializeResponse{}, handleErr
		}
		return value.(CacheMaterializeResponse), nil
	}
	online, err := materialize(request)
	if err != nil || online.Offline || online.Freshness != cache.EntryFresh {
		t.Fatalf("online materialize = %#v, %v", online, err)
	}
	implementation.disconnected = true
	request.OwnerID = "preview-offline"
	offline, err := materialize(request)
	if err != nil || !offline.Offline || offline.Freshness != cache.EntryUnknown || offline.EntryID != online.EntryID || offline.MaterializationID == online.MaterializationID {
		t.Fatalf("offline materialize = %#v, %v; online=%#v", offline, err, online)
	}
	if content, readErr := os.ReadFile(offline.Path); readErr != nil || string(content) != "first verified version" {
		t.Fatalf("offline content = %q, %v", content, readErr)
	}
	request.Policy, request.Pinned, request.OwnerID = cache.PolicyLRU, false, "preview-unpinned"
	if _, err := materialize(request); !domain.IsCode(err, domain.CodeTransportInterrupted) {
		t.Fatalf("unpin/disconnected error = %v, want transport_interrupted", err)
	}
	implementation.disconnected = false
	if err := os.WriteFile(sourcePath, []byte("second remote version"), 0o600); err != nil {
		t.Fatal(err)
	}
	request.Policy, request.Pinned, request.OwnerID = cache.PolicyPinnedOffline, true, "preview-reconnected"
	reconnected, err := materialize(request)
	if err != nil || reconnected.Offline || reconnected.Freshness != cache.EntryFresh || reconnected.EntryID == online.EntryID {
		t.Fatalf("reconnected materialize = %#v, %v; first=%#v", reconnected, err, online)
	}
	implementation.disconnected = true
	request.OwnerID = "preview-ambiguous"
	if _, err := materialize(request); err == nil || domain.IsCode(err, domain.CodeTransportInterrupted) {
		t.Fatalf("ambiguous historical versions were reused by path: %v", err)
	}
}

func TestCacheMaterializeReturnsTypedResourceExhaustedWhenLiveQuotaCannotAdmit(t *testing.T) {
	ctx := context.Background()
	root := testkit.PersistentTempDir(t)
	sourcePath := filepath.Join(root, "too-large.txt")
	if err := os.WriteFile(sourcePath, []byte("two bytes"), 0o600); err != nil {
		t.Fatal(err)
	}
	endpointID := domain.EndpointID("ep_aaaaaaaaaaaaaaaaaaaaaaaaaa")
	implementation, err := localfs.New(localfs.Config{Endpoint: domain.Endpoint{ID: endpointID, Kind: domain.EndpointLocal}, SessionID: "sess_aaaaaaaaaaaaaaaaaaaaaaaaaa", Root: "/"})
	if err != nil {
		t.Fatal(err)
	}
	sessions, err := NewProviderSessions([]provider.Provider{implementation}, 64<<10)
	if err != nil {
		t.Fatal(err)
	}
	sessions.SetCacheManager(newDaemonCacheManagerWithLimits(t, ctx, root, cache.Limits{GlobalBytes: 1, GlobalEntries: 1, WorkspaceBytes: 1, MaxCandidates: 16}))
	session := sessions.NewSession()
	defer session.Close()
	location, _ := domain.NewLocation(endpointID, domain.CanonicalPath(sourcePath))
	payload, _ := json.Marshal(CacheMaterializeRequest{
		Location: ipc.EncodeLocation(location), WorkspaceID: "workspace", Policy: cache.PolicyLRU,
		OwnerKind: cache.LeaseOwnerPreview, OwnerID: "preview-owner",
	})
	if _, err := session.Handle(ctx, CacheMaterialize, payload); !domain.IsCode(err, domain.CodeResourceExhausted) {
		t.Fatalf("materialize error = %v, want resource_exhausted", err)
	}
}

type disconnectingProvider struct {
	provider.Provider
	disconnected bool
}

func (implementation *disconnectingProvider) OpenRead(ctx context.Context, request provider.OpenReadRequest) (provider.ReadHandle, error) {
	if implementation.disconnected {
		location := request.Location
		return nil, &domain.OpError{
			Code: domain.CodeTransportInterrupted, Message: "fixture disconnected", Operation: "open_read",
			EndpointID: location.EndpointID, Location: &location, Retry: domain.RetryAdvice{Kind: domain.RetryAfterReconnect}, Effect: domain.EffectNone,
			Cause: errors.New("fixture transport unavailable"),
		}
	}
	return implementation.Provider.OpenRead(ctx, request)
}

func newDaemonCacheManager(t *testing.T, ctx context.Context, root string) *cachemanager.Manager {
	return newDaemonCacheManagerWithLimits(t, ctx, root, cache.DefaultLimits())
}

func newDaemonCacheManagerWithLimits(t *testing.T, ctx context.Context, root string, limits cache.Limits) *cachemanager.Manager {
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
	manager, err := cachemanager.New(files, catalog, foundation.RealClock{}, strings.Repeat("d", 32), limits)
	if err != nil {
		t.Fatal(err)
	}
	return manager
}
