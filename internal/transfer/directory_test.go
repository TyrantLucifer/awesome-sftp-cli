package transfer

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"sync"
	"testing"

	"github.com/TyrantLucifer/awesome-mac-sftp/internal/domain"
	providerapi "github.com/TyrantLucifer/awesome-mac-sftp/internal/provider"
)

func TestDiscoverDirectoryStreamsMillionEntriesWithinFrozenBudgets(t *testing.T) {
	const entries = 1_000_000
	implementation := newSyntheticDirectoryProvider(entries)
	items, failures, err := DiscoverDirectory(context.Background(), implementation, implementation.root, DiscoveryBudget{
		QueueItems: 17,
		PageItems:  31,
		MaxDepth:   8,
	})
	if err != nil {
		t.Fatalf("DiscoverDirectory(): %v", err)
	}
	count := 0
	for item := range items {
		count++
		if item.Depth != 1 || item.Entry.Kind != domain.EntryFile || item.RelativePath == "" {
			t.Fatalf("item %d = %#v", count, item)
		}
	}
	if err := <-failures; err != nil {
		t.Fatalf("discovery failed: %v", err)
	}
	if count != entries {
		t.Fatalf("discovered %d entries, want %d", count, entries)
	}
	if cap(items) != 17 {
		t.Fatalf("queue capacity = %d, want 17", cap(items))
	}
	if got := implementation.maximumListLimit(); got != 31 {
		t.Fatalf("maximum List limit = %d, want 31", got)
	}
}

func TestDiscoverDirectoryDoesNotFollowSymlinksAndRejectsDepthOverflow(t *testing.T) {
	implementation := newSyntheticDirectoryProvider(0)
	implementation.nested = true
	items, failures, err := DiscoverDirectory(context.Background(), implementation, implementation.root, DiscoveryBudget{
		QueueItems: 4,
		PageItems:  4,
		MaxDepth:   1,
	})
	if err != nil {
		t.Fatalf("DiscoverDirectory(): %v", err)
	}
	for range items {
	}
	if err := <-failures; !domain.IsCode(err, domain.CodeResourceExhausted) {
		t.Fatalf("depth overflow error = %v, want resource_exhausted", err)
	}
}

func TestDiscoverDirectoryConfigurationCanOnlyTightenFrozenHardCeilings(t *testing.T) {
	t.Parallel()
	implementation := newSyntheticDirectoryProvider(0)
	for name, budget := range map[string]DiscoveryBudget{
		"frontier": {QueueItems: 65, PageItems: 256, MaxDepth: 128},
		"page":     {QueueItems: 64, PageItems: 257, MaxDepth: 128},
		"depth":    {QueueItems: 64, PageItems: 256, MaxDepth: 129},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			if _, _, err := DiscoverDirectory(context.Background(), implementation, implementation.root, budget); err == nil {
				t.Fatalf("DiscoverDirectory(%+v) succeeded above hard ceiling", budget)
			}
		})
	}
}

func TestDirectoryResultManifestHasHardPersistenceBound(t *testing.T) {
	var result Result
	for index := 0; index < maximumManifestItems+44; index++ {
		appendItemResult(&result, ItemResult{RelativePath: strconv.Itoa(index), Status: ItemSucceeded})
	}
	var retained uint64
	for range result.Manifest {
		retained++
	}
	if len(result.Manifest) > maximumManifestItems || retained+result.ManifestTruncated != 300 {
		t.Fatalf("manifest size/truncated = %d/%d", len(result.Manifest), result.ManifestTruncated)
	}
	encoded, err := json.Marshal(result.Manifest)
	if err != nil || len(encoded) > maximumManifestJSONBytes {
		t.Fatalf("manifest JSON = %d bytes, %v", len(encoded), err)
	}
}

func TestDirectoryResultManifestHasHardJSONByteBound(t *testing.T) {
	var result Result
	longPath := strings.Repeat("x", 4*1024)
	for index := 0; index < maximumManifestItems; index++ {
		appendItemResult(&result, ItemResult{
			RelativePath: longPath + strconv.Itoa(index),
			Source:       domain.Location{EndpointID: "ep_aaaaaaaaaaaaaaaaaaaaaaaaaa", Path: domain.CanonicalPath("/" + longPath)},
			Destination:  domain.Location{EndpointID: "ep_bbbbbbbbbbbbbbbbbbbbbbbbbb", Path: domain.CanonicalPath("/" + longPath)},
			Status:       ItemSucceeded,
		})
	}
	encoded, err := json.Marshal(result.Manifest)
	if err != nil {
		t.Fatal(err)
	}
	if len(encoded) > maximumManifestJSONBytes {
		t.Fatalf("manifest JSON bytes = %d, ceiling = %d", len(encoded), maximumManifestJSONBytes)
	}
	if result.ManifestTruncated == 0 {
		t.Fatal("long manifest was not truncated")
	}
}

type syntheticDirectoryProvider struct {
	root       domain.Location
	endpoint   domain.Endpoint
	snapshot   domain.EndpointSnapshot
	entryCount int
	nested     bool

	mu       sync.Mutex
	maxLimit uint32
}

func newSyntheticDirectoryProvider(entryCount int) *syntheticDirectoryProvider {
	endpoint := domain.Endpoint{ID: "ep_zzzzzzzzzzzzzzzzzzzzzzzzzz", Kind: domain.EndpointLocal, DisplayName: "synthetic"}
	capabilities, err := domain.NewCapabilitySnapshot(domain.CapabilityRevision{SessionID: "sess_zzzzzzzzzzzzzzzzzzzzzzzzzz", Generation: 1}, true, []domain.Capability{{Name: "read", Version: 1}})
	if err != nil {
		panic(err)
	}
	return &syntheticDirectoryProvider{
		root:       domain.Location{EndpointID: endpoint.ID, Path: "/"},
		endpoint:   endpoint,
		snapshot:   domain.EndpointSnapshot{EndpointID: endpoint.ID, SessionID: capabilities.Revision.SessionID, State: domain.StateReady, Capabilities: capabilities},
		entryCount: entryCount,
	}
}

func (provider *syntheticDirectoryProvider) Descriptor() domain.Endpoint { return provider.endpoint }

func (provider *syntheticDirectoryProvider) Snapshot(context.Context) (domain.EndpointSnapshot, error) {
	return provider.snapshot, nil
}

func (provider *syntheticDirectoryProvider) Normalize(_ context.Context, request domain.NormalizeRequest) (domain.Location, error) {
	return domain.Location{EndpointID: provider.endpoint.ID, Path: domain.CanonicalPath(request.Input)}, nil
}

func (provider *syntheticDirectoryProvider) Stat(_ context.Context, request providerapi.StatRequest) (domain.Entry, error) {
	if request.Location != provider.root {
		return domain.Entry{}, fmt.Errorf("unexpected Stat location %q", request.Location.Path)
	}
	return domain.Entry{Location: provider.root, Name: "/", Kind: domain.EntryDirectory}, nil
}

func (provider *syntheticDirectoryProvider) List(_ context.Context, request providerapi.ListRequest) (providerapi.ListPage, error) {
	provider.mu.Lock()
	if request.Limit > provider.maxLimit {
		provider.maxLimit = request.Limit
	}
	provider.mu.Unlock()
	if provider.nested {
		var entries []domain.Entry
		switch request.Location.Path {
		case "/":
			entries = []domain.Entry{
				{Location: domain.Location{EndpointID: provider.endpoint.ID, Path: "/link"}, Name: "link", Kind: domain.EntrySymlink},
				{Location: domain.Location{EndpointID: provider.endpoint.ID, Path: "/dir"}, Name: "dir", Kind: domain.EntryDirectory},
			}
		case "/dir":
			entries = []domain.Entry{{Location: domain.Location{EndpointID: provider.endpoint.ID, Path: "/dir/child"}, Name: "child", Kind: domain.EntryDirectory}}
		default:
			entries = nil
		}
		return providerapi.ListPage{Entries: entries, Done: true, Consistency: providerapi.ConsistencySnapshot}, nil
	}
	start := 0
	if request.Cursor != "" {
		var err error
		start, err = strconv.Atoi(string(request.Cursor))
		if err != nil {
			return providerapi.ListPage{}, err
		}
	}
	end := start + int(request.Limit)
	if end > provider.entryCount {
		end = provider.entryCount
	}
	entries := make([]domain.Entry, 0, end-start)
	for index := start; index < end; index++ {
		name := fmt.Sprintf("item-%07d", index)
		entries = append(entries, domain.Entry{
			Location: domain.Location{EndpointID: provider.endpoint.ID, Path: domain.CanonicalPath("/" + name)},
			Name:     name,
			Kind:     domain.EntryFile,
		})
	}
	done := end == provider.entryCount
	next := providerapi.PageCursor("")
	if !done {
		next = providerapi.PageCursor(strconv.Itoa(end))
	}
	return providerapi.ListPage{Entries: entries, NextCursor: next, Done: done, Consistency: providerapi.ConsistencySnapshot}, nil
}

func (provider *syntheticDirectoryProvider) OpenRead(context.Context, providerapi.OpenReadRequest) (providerapi.ReadHandle, error) {
	return nil, fmt.Errorf("OpenRead is not used by discovery")
}

func (provider *syntheticDirectoryProvider) maximumListLimit() uint32 {
	provider.mu.Lock()
	defer provider.mu.Unlock()
	return provider.maxLimit
}
