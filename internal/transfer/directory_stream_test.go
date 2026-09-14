package transfer

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path"
	"path/filepath"
	"sort"
	"sync"
	"testing"
	"time"

	"github.com/TyrantLucifer/awesome-sftp-cli/internal/domain"
	providerapi "github.com/TyrantLucifer/awesome-sftp-cli/internal/provider"
)

func TestDirectoryStreamsIndependentFilesWithBoundedConcurrency(t *testing.T) {
	t.Run("new", func(t *testing.T) { testDirectoryStreamConcurrency(t, false) })
	t.Run("resume_with_completed_file", func(t *testing.T) { testDirectoryStreamConcurrency(t, true) })
}

func testDirectoryStreamConcurrency(t *testing.T, resume bool) {
	root := t.TempDir()
	if err := os.Mkdir(filepath.Join(root, "tree"), 0700); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"a", "b", "c"} {
		if err := os.WriteFile(filepath.Join(root, "tree", name), []byte(name), 0600); err != nil {
			t.Fatal(err)
		}
	}
	source := newPlanTestProvider(t, "ep_aaaaaaaaaaaaaaaaaaaaaaaaaa", root, domain.EndpointLocal)
	destinationRoot := t.TempDir()
	destination := newPlanTestProvider(t, "ep_bbbbbbbbbbbbbbbbbbbbbbbbbb", destinationRoot, domain.EndpointLocal)
	gate := &directoryReadGate{Provider: source, started: make(chan string, 3), release: map[string]chan struct{}{"a": make(chan struct{}), "b": make(chan struct{}), "c": make(chan struct{})}}
	resolver := MapResolver{source.Descriptor().ID: gate, destination.Descriptor().ID: destination}
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
	plan.StreamPolicy.DirectoryWorkers = 2
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	journal := newMemoryJournal()
	if resume {
		_, err := NewWorker(resolver, journal).Execute(ctx, plan, ControlFunc(func(Checkpoint) ControlAction { return ControlPause }))
		if !errors.Is(err, ErrPaused) {
			t.Fatalf("prepare interrupted directory: %v", err)
		}
		if err := os.WriteFile(filepath.Join(destinationRoot, "copied", "c"), []byte("c"), 0600); err != nil {
			t.Fatal(err)
		}
	}
	done := make(chan error, 1)
	go func() { _, err := NewWorker(resolver, journal).Execute(ctx, plan, nil); done <- err }()
	released := make(map[string]bool)
	releaseAll := func() {
		for name, ch := range gate.release {
			if !released[name] {
				close(ch)
				released[name] = true
			}
		}
	}
	defer func() { releaseAll(); cancel(); <-done }()
	var running []string
	for range 2 {
		select {
		case name := <-gate.started:
			running = append(running, name)
		case <-time.After(time.Second):
			t.Fatal("independent files did not enter the stream concurrently")
		}
	}
	select {
	case <-gate.started:
		t.Fatal("directory exceeded two active streams including recovery validation")
	case <-time.After(50 * time.Millisecond):
	}
	saved := journal.latest()
	if len(saved.DirectoryChildren) != 2 {
		t.Fatalf("unfinished file checkpoints=%d, want two isolated identities", len(saved.DirectoryChildren))
	}
	for _, child := range saved.DirectoryChildren {
		if child.Checkpoint.JobID != plan.JobID || child.RelativePath == "" {
			t.Fatal("child lost its durable identity")
		}
	}
	close(gate.release[running[0]])
	released[running[0]] = true
	select {
	case <-gate.started:
	case <-time.After(time.Second):
		t.Fatal("completed slot did not admit the next file")
	}
	if journal.latest().Items != 1 {
		t.Fatalf("committed item count=%d, want one while the other streams are active", journal.latest().Items)
	}
	releaseAll()
	select {
	case err := <-done:
		done <- err
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("directory did not finish")
	}
	for _, name := range []string{"a", "b", "c"} {
		assertWorkerBytes(t, destination, childLocation(plan.Final, name), []byte(name))
	}
}

type directoryReadGate struct {
	providerapi.Provider
	started chan string
	release map[string]chan struct{}
}

func (p *directoryReadGate) List(ctx context.Context, request providerapi.ListRequest) (providerapi.ListPage, error) {
	page, err := p.Provider.List(ctx, request)
	sort.Slice(page.Entries, func(i, j int) bool { return page.Entries[i].Location.Path < page.Entries[j].Location.Path })
	return page, err
}

func (p *directoryReadGate) OpenRead(ctx context.Context, request providerapi.OpenReadRequest) (providerapi.ReadHandle, error) {
	h, err := p.Provider.OpenRead(ctx, request)
	if err != nil {
		return nil, err
	}
	return &directoryReadGateHandle{ReadHandle: h, parent: p, name: path.Base(string(request.Location.Path))}, nil
}

type directoryReadGateHandle struct {
	name string
	providerapi.ReadHandle
	parent *directoryReadGate
	once   sync.Once
}

func (h *directoryReadGateHandle) Read(ctx context.Context, b []byte) (int, error) {
	h.once.Do(func() { h.parent.started <- h.name })
	select {
	case <-ctx.Done():
		return 0, ctx.Err()
	case <-h.parent.release[h.name]:
	}
	return h.ReadHandle.Read(ctx, b)
}

func TestDirectoryStreamResourceUsageIncludesBothWindowsAndValidation(t *testing.T) {
	plan := Plan{Source: FileRef{Kind: domain.EntryDirectory}, BufferBytes: 4 << 20, Route: RouteSFTPRelay, StreamPolicy: DefaultStreamPolicy(), SourceEndpoint: domain.Endpoint{ID: "ep_aaaaaaaaaaaaaaaaaaaaaaaaaa", Kind: domain.EndpointLocal}, DestinationEndpoint: domain.Endpoint{ID: "ep_bbbbbbbbbbbbbbbbbbbbbbbbbb", Kind: domain.EndpointSSH}}
	upload := executionResourceUsage(plan)
	if upload.MemoryBytes != 12<<20+streamPacketBytes || upload.Goroutines != 138 || upload.Connections != 1 {
		t.Fatalf("two upload slots=%+v", upload)
	}
	plan.SourceEndpoint.Kind = domain.EndpointSSH
	relay := executionResourceUsage(plan)
	if relay.MemoryBytes != 8<<20+streamPacketBytes || relay.Goroutines != 136 || relay.Connections != 2 {
		t.Fatalf("bounded relay slot=%+v", relay)
	}
	if relay.Goroutines > HardResourceCeilings().Goroutines || upload.MemoryBytes > HardResourceCeilings().MemoryBytes {
		t.Fatal("directory expanded hard resource limits")
	}
}

func TestDirectorySharesRequestBudgetAcrossSmallAndLargeFiles(t *testing.T) {
	for _, test := range []struct {
		name  string
		size  int64
		relay bool
		slots int
	}{
		{"small_upload", 4096, false, 8}, {"large_upload", 4 << 20, false, 2}, {"large_relay", 4 << 20, true, 1},
	} {
		t.Run(test.name, func(t *testing.T) {
			root := t.TempDir()
			if err := os.Mkdir(filepath.Join(root, "tree"), 0700); err != nil {
				t.Fatal(err)
			}
			gate := &directoryReadGate{started: make(chan string, 9), release: make(map[string]chan struct{})}
			for i := range 9 {
				name := fmt.Sprint(i)
				gate.release[name] = make(chan struct{})
				// #nosec G304 -- numbered sparse fixtures inside this test-owned temporary directory.
				file, err := os.OpenFile(filepath.Join(root, "tree", name), os.O_CREATE|os.O_RDWR, 0600)
				if err != nil {
					t.Fatal(err)
				}
				if err := file.Truncate(test.size); err != nil {
					t.Fatal(err)
				}
				_ = file.Close()
			}
			sourceKind := domain.EndpointLocal
			if test.relay {
				sourceKind = domain.EndpointSSH
			}
			source := newPlanTestProvider(t, "ep_aaaaaaaaaaaaaaaaaaaaaaaaaa", root, sourceKind)
			gate.Provider = source
			destination := newPlanTestProvider(t, "ep_bbbbbbbbbbbbbbbbbbbbbbbbbb", t.TempDir(), domain.EndpointSSH)
			resolver := MapResolver{source.Descriptor().ID: gate, destination.Descriptor().ID: destination}
			planner := NewPlanner(resolver)
			reference, err := planner.Capture(t.Context(), normalizePlanTest(t, source, "/tree"))
			if err != nil {
				t.Fatal(err)
			}
			plan, _, err := planner.FreezeCopy(t.Context(), validFreezeRequest(reference, normalizePlanTest(t, destination, "/")))
			if err != nil {
				t.Fatal(err)
			}
			journal := newMemoryJournal()
			ctx, cancel := context.WithCancel(t.Context())
			done := make(chan error, 1)
			go func() { _, err := NewWorker(resolver, journal).Execute(ctx, plan, nil); done <- err }()
			defer func() {
				for _, ch := range gate.release {
					close(ch)
				}
				cancel()
				<-done
			}()
			for range test.slots {
				select {
				case <-gate.started:
				case <-time.After(time.Second):
					t.Fatal("request budget did not admit expected independent files")
				}
			}
			select {
			case <-gate.started:
				t.Fatal("request or file budget exceeded")
			case <-time.After(50 * time.Millisecond):
			}
			if got := len(journal.latest().DirectoryChildren); got != test.slots {
				t.Fatalf("active checkpoints=%d want %d", got, test.slots)
			}
		})
	}
}
