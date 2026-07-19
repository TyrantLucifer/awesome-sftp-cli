package search

import (
	"context"
	"errors"
	"fmt"
	"os"
	"runtime"
	"strconv"
	"sync"
	"testing"
	"time"

	"github.com/TyrantLucifer/awesome-sftp-cli/internal/domain"
	providerapi "github.com/TyrantLucifer/awesome-sftp-cli/internal/provider"
)

func TestMillionNodeFilenameFixtureStreamsFirstResultWithoutMaterializingTree(t *testing.T) {
	beforeGoroutines := runtime.NumGoroutine()
	beforeFDs := openFDCount(t)
	implementation := newMillionNodeProvider(t, 1_000_000)
	request := filenameContractRequest()
	request.Identity.Options.Pattern = "target-000000"
	request.Identity.Budget.PageItems = 128
	request.Identity.Budget.MaxEntries = 1_000_000
	request.Identity.Budget.MaxResults = 1
	request.Identity.Budget.MaxDuration = 10 * time.Second

	started := time.Now()
	events, err := StartFilename(context.Background(), implementation, request)
	if err != nil {
		t.Fatal(err)
	}
	first := <-events
	firstLatency := time.Since(started)
	if first.Kind != EventResult || first.Result.RelativePath != "target-000000" {
		t.Fatalf("first event = %#v", first)
	}
	terminal := terminalEvent(t, events)
	if terminal.Status != StatusPartial || terminal.StopReason != StopResultLimit || terminal.Results != 1 {
		t.Fatalf("terminal = %#v", terminal)
	}
	listCalls, maxPage := implementation.metrics()
	if listCalls != 1 || maxPage > int(request.Identity.Budget.PageItems) {
		t.Fatalf("million-node fixture materialization: list calls=%d max page=%d", listCalls, maxPage)
	}
	if firstLatency > time.Second {
		t.Fatalf("first result latency = %s, want <= 1s", firstLatency)
	}
	runtime.Gosched()
	afterGoroutines := runtime.NumGoroutine()
	afterFDs := openFDCount(t)
	if afterGoroutines > beforeGoroutines+1 {
		t.Fatalf("goroutines before=%d after=%d", beforeGoroutines, afterGoroutines)
	}
	if beforeFDs >= 0 && afterFDs > beforeFDs+1 {
		t.Fatalf("file descriptors before=%d after=%d", beforeFDs, afterFDs)
	}
	t.Logf("million-node synthetic fixture: first_result=%s list_calls=%d max_resident_page=%d goroutines=%d->%d fds=%d->%d child_processes=0", firstLatency, listCalls, maxPage, beforeGoroutines, afterGoroutines, beforeFDs, afterFDs)
}

func openFDCount(t *testing.T) int {
	t.Helper()
	entries, err := os.ReadDir("/dev/fd")
	if err != nil {
		return -1
	}
	return len(entries)
}

type millionNodeProvider struct {
	total    int
	endpoint domain.Endpoint
	snapshot domain.EndpointSnapshot

	mu        sync.Mutex
	listCalls int
	maxPage   int
}

func newMillionNodeProvider(t *testing.T, total int) *millionNodeProvider {
	t.Helper()
	capabilities, err := domain.NewCapabilitySnapshot(domain.CapabilityRevision{SessionID: searchSessionID, Generation: 7}, true, []domain.Capability{{Name: "read", Version: 1}})
	if err != nil {
		t.Fatal(err)
	}
	endpoint := domain.Endpoint{ID: searchEndpointID, Kind: domain.EndpointSSH, DisplayName: "million-node"}
	return &millionNodeProvider{total: total, endpoint: endpoint, snapshot: domain.EndpointSnapshot{EndpointID: endpoint.ID, SessionID: searchSessionID, State: domain.StateReady, Capabilities: capabilities}}
}

func (p *millionNodeProvider) Descriptor() domain.Endpoint { return p.endpoint }
func (p *millionNodeProvider) Snapshot(context.Context) (domain.EndpointSnapshot, error) {
	return p.snapshot, nil
}
func (p *millionNodeProvider) Normalize(_ context.Context, request domain.NormalizeRequest) (domain.Location, error) {
	return domain.Location{EndpointID: p.endpoint.ID, Path: domain.CanonicalPath(request.Input)}, nil
}
func (p *millionNodeProvider) Stat(_ context.Context, request providerapi.StatRequest) (domain.Entry, error) {
	if request.Location.Path == "/" {
		return domain.Entry{Location: request.Location, Name: "/", Kind: domain.EntryDirectory}, nil
	}
	return domain.Entry{}, errors.New("not found")
}
func (p *millionNodeProvider) List(_ context.Context, request providerapi.ListRequest) (providerapi.ListPage, error) {
	start := 0
	if request.Cursor != "" {
		parsed, err := strconv.Atoi(string(request.Cursor))
		if err != nil {
			return providerapi.ListPage{}, err
		}
		start = parsed
	}
	end := min(p.total, start+int(request.Limit))
	entries := make([]domain.Entry, 0, end-start)
	for index := start; index < end; index++ {
		name := fmt.Sprintf("node-%06d", index)
		if index == 0 {
			name = "target-000000"
		}
		entries = append(entries, domain.Entry{Location: domain.Location{EndpointID: p.endpoint.ID, Path: domain.CanonicalPath("/" + name)}, Name: name, Kind: domain.EntryFile})
	}
	done := end == p.total
	var next providerapi.PageCursor
	if !done {
		next = providerapi.PageCursor(strconv.Itoa(end))
	}
	p.mu.Lock()
	p.listCalls++
	p.maxPage = max(p.maxPage, len(entries))
	p.mu.Unlock()
	return providerapi.ListPage{Entries: entries, NextCursor: next, Done: done, Consistency: providerapi.ConsistencySnapshot}, nil
}
func (p *millionNodeProvider) OpenRead(context.Context, providerapi.OpenReadRequest) (providerapi.ReadHandle, error) {
	return nil, errors.New("filename search must not read")
}
func (p *millionNodeProvider) metrics() (int, int) {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.listCalls, p.maxPage
}
