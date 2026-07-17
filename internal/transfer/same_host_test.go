package transfer

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
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
	"github.com/TyrantLucifer/awesome-mac-sftp/internal/testkit"
)

func TestPlannerSelectsSameHostHelperOnlyForFrozenEligibleCopy(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "destination"), 0o700); err != nil {
		t.Fatal(err)
	}
	payload := []byte("planner-owned same-host route")
	if err := os.WriteFile(filepath.Join(root, "source"), payload, 0o600); err != nil {
		t.Fatal(err)
	}
	implementation := newPlanTestProvider(t, "ep_aaaaaaaaaaaaaaaaaaaaaaaaaa", root, domain.EndpointSSH)
	resolver := MapResolver{implementation.Descriptor().ID: implementation}
	backend := &recordingSameHostBackend{root: root, payload: payload}
	planner := NewPlannerWithSameHost(resolver, backend)
	reference, err := planner.Capture(context.Background(), normalizePlanTest(t, implementation, "/source"))
	if err != nil {
		t.Fatal(err)
	}
	request := validFreezeRequest(reference, normalizePlanTest(t, implementation, "/destination"))
	request.Intent.Name = "copied"
	plan, _, err := planner.FreezeCopy(context.Background(), request)
	if err != nil {
		t.Fatal(err)
	}
	if plan.Route != RouteHelperSameHost || plan.SameHostCopy == nil {
		t.Fatalf("route/binding = %q/%#v", plan.Route, plan.SameHostCopy)
	}
	if backend.prepareCalls != 1 || backend.stageCalls != 0 {
		t.Fatalf("prepare/stage calls = %d/%d", backend.prepareCalls, backend.stageCalls)
	}
	digest := sha256.Sum256(payload)
	if plan.SameHostCopy.SourceSHA256 != hex.EncodeToString(digest[:]) || plan.SameHostCopy.SourceSize != uint64(len(payload)) {
		t.Fatalf("frozen same-host binding = %#v", plan.SameHostCopy)
	}

	backend.modifiedOffset = time.Second
	identityMismatch, _, err := planner.FreezeCopy(context.Background(), withNewJobIdentity(request))
	if err != nil {
		t.Fatal(err)
	}
	if identityMismatch.Route != RouteSFTPRelay || identityMismatch.SameHostCopy != nil {
		t.Fatalf("Provider/Helper identity mismatch did not fall back: %#v", identityMismatch)
	}
	backend.modifiedOffset = 0
	backend.prepareErr = errors.New("capability removed")
	fallback, _, err := planner.FreezeCopy(context.Background(), withNewJobIdentity(request))
	if err != nil {
		t.Fatal(err)
	}
	if fallback.Route != RouteSFTPRelay || fallback.SameHostCopy != nil {
		t.Fatalf("fallback route/binding = %q/%#v", fallback.Route, fallback.SameHostCopy)
	}
}

func TestWorkerStagesSameHostPartThenUsesExistingVerifyAndCommit(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "destination"), 0o700); err != nil {
		t.Fatal(err)
	}
	payload := []byte("stage, verify, commit")
	if err := os.WriteFile(filepath.Join(root, "source"), payload, 0o600); err != nil {
		t.Fatal(err)
	}
	implementation := newPlanTestProvider(t, "ep_aaaaaaaaaaaaaaaaaaaaaaaaaa", root, domain.EndpointSSH)
	resolver := MapResolver{implementation.Descriptor().ID: implementation}
	backend := &recordingSameHostBackend{root: root, payload: payload}
	planner := NewPlannerWithSameHost(resolver, backend)
	reference, err := planner.Capture(context.Background(), normalizePlanTest(t, implementation, "/source"))
	if err != nil {
		t.Fatal(err)
	}
	request := validFreezeRequest(reference, normalizePlanTest(t, implementation, "/destination"))
	request.Intent.Name = "copied"
	plan, _, err := planner.FreezeCopy(context.Background(), request)
	if err != nil {
		t.Fatal(err)
	}
	journal := newMemoryJournal()
	result, err := NewWorkerWithSameHost(resolver, journal, backend).Execute(context.Background(), plan, nil)
	if err != nil {
		t.Fatal(err)
	}
	if result.Outcome != OutcomeCompleted || result.PartRetained || backend.stageCalls != 1 {
		t.Fatalf("result=%#v stage calls=%d", result, backend.stageCalls)
	}
	data, err := os.ReadFile(filepath.Join(root, "destination", "copied")) // #nosec G304 -- path is inside t.TempDir.
	if err != nil || string(data) != string(payload) {
		t.Fatalf("final = %q, %v", data, err)
	}
	if _, err := os.Lstat(filepath.Join(root, "destination", ".copied.part-"+string(plan.JobID))); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("part survived commit: %v", err)
	}
}

func TestWorkerAdoptsExactDurableSameHostPartAfterRestartWithoutRestaging(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "destination"), 0o700); err != nil {
		t.Fatal(err)
	}
	payload := []byte("response lost after durable part")
	if err := os.WriteFile(filepath.Join(root, "source"), payload, 0o600); err != nil {
		t.Fatal(err)
	}
	implementation := newPlanTestProvider(t, "ep_aaaaaaaaaaaaaaaaaaaaaaaaaa", root, domain.EndpointSSH)
	resolver := MapResolver{implementation.Descriptor().ID: implementation}
	backend := &recordingSameHostBackend{root: root, payload: payload}
	planner := NewPlannerWithSameHost(resolver, backend)
	reference, err := planner.Capture(context.Background(), normalizePlanTest(t, implementation, "/source"))
	if err != nil {
		t.Fatal(err)
	}
	request := validFreezeRequest(reference, normalizePlanTest(t, implementation, "/destination"))
	request.Intent.Name = "copied"
	plan, _, err := planner.FreezeCopy(context.Background(), request)
	if err != nil {
		t.Fatal(err)
	}
	partPath := filepath.Join(root, "destination", ".copied.part-"+string(plan.JobID))
	if err := os.WriteFile(partPath, payload, 0o600); err != nil {
		t.Fatal(err)
	}
	journal := newMemoryJournal()
	if err := journal.Save(context.Background(), Checkpoint{
		JobID: plan.JobID, Phase: PhasePrepared, SourceFingerprint: cloneFingerprint(plan.Source.Fingerprint),
		Part: plan.Part, Final: plan.Final,
	}); err != nil {
		t.Fatal(err)
	}
	result, err := NewWorkerWithSameHost(resolver, journal, backend).Execute(context.Background(), plan, nil)
	if err != nil {
		t.Fatal(err)
	}
	if result.Outcome != OutcomeCompleted || backend.stageCalls != 0 {
		t.Fatalf("result=%#v stage calls=%d", result, backend.stageCalls)
	}
}

func TestSameHostRouteKeepsExistingConflictAndOverwriteCommitSemantics(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "destination"), 0o700); err != nil {
		t.Fatal(err)
	}
	payload := []byte("replacement")
	if err := os.WriteFile(filepath.Join(root, "source"), payload, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "destination", "copied"), []byte("original"), 0o600); err != nil {
		t.Fatal(err)
	}
	implementation := newPlanTestProvider(t, "ep_aaaaaaaaaaaaaaaaaaaaaaaaaa", root, domain.EndpointSSH)
	resolver := MapResolver{implementation.Descriptor().ID: implementation}
	backend := &recordingSameHostBackend{root: root, payload: payload}
	planner := NewPlannerWithSameHost(resolver, backend)
	reference, err := planner.Capture(context.Background(), normalizePlanTest(t, implementation, "/source"))
	if err != nil {
		t.Fatal(err)
	}
	request := validFreezeRequest(reference, normalizePlanTest(t, implementation, "/destination"))
	request.Intent.Name = "copied"
	request.Intent.ConflictPolicy = ConflictOverwrite
	request.Intent.ConflictConfirmed = true
	plan, _, err := planner.FreezeCopy(context.Background(), request)
	if err != nil {
		t.Fatal(err)
	}
	result, err := NewWorkerWithSameHost(resolver, newMemoryJournal(), backend).Execute(context.Background(), plan, nil)
	if err != nil {
		t.Fatal(err)
	}
	if result.Outcome != OutcomeCompleted || backend.stageCalls != 1 {
		t.Fatalf("result=%#v stage calls=%d", result, backend.stageCalls)
	}
	data, err := os.ReadFile(filepath.Join(root, "destination", "copied")) // #nosec G304 -- path is inside t.TempDir.
	if err != nil || string(data) != string(payload) {
		t.Fatalf("committed final = %q, %v", data, err)
	}
}

func TestManagerPersistsAndExecutesSameHostRouteThroughDurableJob(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "destination"), 0o700); err != nil {
		t.Fatal(err)
	}
	payload := []byte("durable helper route")
	if err := os.WriteFile(filepath.Join(root, "source"), payload, 0o600); err != nil {
		t.Fatal(err)
	}
	implementation := newPlanTestProvider(t, "ep_aaaaaaaaaaaaaaaaaaaaaaaaaa", root, domain.EndpointSSH)
	resolver := MapResolver{implementation.Descriptor().ID: implementation}
	backend := &recordingSameHostBackend{root: root, payload: payload}
	store, database := openTransferStore(t, context.Background(), testDatabasePath(t), true)
	t.Cleanup(func() { _ = database.Close() })
	manager, err := NewManager(ManagerConfig{
		Store: store, Resolver: resolver, Generator: &testkit.SequenceGenerator{}, SameHostCopy: backend,
		Now: func() time.Time { return time.Unix(1_800_000_000, 0) }, MaxConcurrent: 1,
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(manager.Close)
	if err := manager.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	reference, err := manager.Capture(context.Background(), normalizePlanTest(t, implementation, "/source"))
	if err != nil {
		t.Fatal(err)
	}
	created, err := manager.CreateCopy(context.Background(), Intent{
		Clipboard: ClipboardCopy, Source: reference, DestinationDirectory: normalizePlanTest(t, implementation, "/destination"),
		Name: "copied", ConflictPolicy: ConflictAsk,
	})
	if err != nil {
		t.Fatal(err)
	}
	completed := waitForTerminal(t, manager, created.JobID)
	if completed.State != job.StateCompleted || backend.prepareCalls != 1 || backend.stageCalls != 1 {
		t.Fatalf("state=%q prepare/stage=%d/%d", completed.State, backend.prepareCalls, backend.stageCalls)
	}
	record, err := store.GetPlan(context.Background(), created.JobID)
	if err != nil {
		t.Fatal(err)
	}
	plan, err := DecodePlan(record, created.JobID)
	if err != nil {
		t.Fatal(err)
	}
	if plan.Route != RouteHelperSameHost || plan.SameHostCopy == nil {
		t.Fatalf("durable route = %q/%#v", plan.Route, plan.SameHostCopy)
	}
	views, err := manager.JobViews(context.Background(), 10)
	if err != nil || len(views) != 1 || views[0].Route != RouteHelperSameHost {
		t.Fatalf("observable durable route = %#v, %v", views, err)
	}
}

func TestManagerHoldsHelperAdmissionFromBeforePrepareThroughDurableCreate(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "destination"), 0o700); err != nil {
		t.Fatal(err)
	}
	payload := []byte("atomic helper admission")
	if err := os.WriteFile(filepath.Join(root, "source"), payload, 0o600); err != nil {
		t.Fatal(err)
	}
	implementation := newPlanTestProvider(t, "ep_aaaaaaaaaaaaaaaaaaaaaaaaaa", root, domain.EndpointSSH)
	resolver := MapResolver{implementation.Descriptor().ID: implementation}
	backend := &blockingPrepareSameHostBackend{
		recordingSameHostBackend: recordingSameHostBackend{root: root, payload: payload},
		started:                  make(chan struct{}),
		continuePrepare:          make(chan struct{}),
	}
	defer func() {
		select {
		case <-backend.continuePrepare:
		default:
			close(backend.continuePrepare)
		}
	}()
	store, database := openTransferStore(t, context.Background(), testDatabasePath(t), true)
	t.Cleanup(func() { _ = database.Close() })
	manager, err := NewManager(ManagerConfig{
		Store: store, Resolver: resolver, Generator: &testkit.SequenceGenerator{}, SameHostCopy: backend,
		Now: func() time.Time { return time.Unix(1_800_000_000, 0) }, MaxConcurrent: 1,
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(manager.Close)
	if err := manager.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	reference, err := manager.Capture(context.Background(), normalizePlanTest(t, implementation, "/source"))
	if err != nil {
		t.Fatal(err)
	}
	created := make(chan error, 1)
	go func() {
		_, createErr := manager.CreateCopy(context.Background(), Intent{
			Clipboard: ClipboardCopy, Source: reference, DestinationDirectory: normalizePlanTest(t, implementation, "/destination"),
			Name: "copied", ConflictPolicy: ConflictAsk,
		})
		created <- createErr
	}()
	select {
	case <-backend.started:
	case <-time.After(time.Second):
		t.Fatal("Helper prepare did not start")
	}
	blockedContext, cancel := context.WithTimeout(context.Background(), 25*time.Millisecond)
	_, err = store.AcquireHelperRemoval(blockedContext, implementation.Descriptor().ID, domain.HelperArtifactID{
		ProtocolMajor: 1, Version: "4.0.0", OS: "linux", Arch: "amd64", SHA256: strings.Repeat("a", 64),
	})
	cancel()
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("removal overlapped Helper prepare-to-create window: %v", err)
	}
	close(backend.continuePrepare)
	if err := <-created; err != nil {
		t.Fatal(err)
	}
}

func TestSameHostWorkerPropagatesCancelToBackendWithoutBreakingProvider(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "destination"), 0o700); err != nil {
		t.Fatal(err)
	}
	payload := []byte("cancel helper only")
	if err := os.WriteFile(filepath.Join(root, "source"), payload, 0o600); err != nil {
		t.Fatal(err)
	}
	implementation := newPlanTestProvider(t, "ep_aaaaaaaaaaaaaaaaaaaaaaaaaa", root, domain.EndpointSSH)
	resolver := MapResolver{implementation.Descriptor().ID: implementation}
	backend := &blockingSameHostBackend{
		recordingSameHostBackend: recordingSameHostBackend{root: root, payload: payload}, started: make(chan struct{}),
	}
	planner := NewPlannerWithSameHost(resolver, backend)
	reference, err := planner.Capture(context.Background(), normalizePlanTest(t, implementation, "/source"))
	if err != nil {
		t.Fatal(err)
	}
	request := validFreezeRequest(reference, normalizePlanTest(t, implementation, "/destination"))
	request.Intent.Name = "copied"
	plan, _, err := planner.FreezeCopy(context.Background(), request)
	if err != nil {
		t.Fatal(err)
	}
	cancelRequested := make(chan struct{})
	done := make(chan error, 1)
	go func() {
		_, executeErr := NewWorkerWithSameHost(resolver, newMemoryJournal(), backend).Execute(context.Background(), plan, ControlFunc(func(Checkpoint) ControlAction {
			select {
			case <-cancelRequested:
				return ControlCancel
			default:
				return ControlContinue
			}
		}))
		done <- executeErr
	}()
	select {
	case <-backend.started:
	case <-time.After(time.Second):
		t.Fatal("same-host backend did not start")
	}
	started := time.Now()
	close(cancelRequested)
	select {
	case err := <-done:
		if !errors.Is(err, ErrCanceled) {
			t.Fatalf("cancel error = %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("same-host cancellation did not propagate")
	}
	if time.Since(started) > 250*time.Millisecond {
		t.Fatalf("same-host cancellation latency = %s", time.Since(started))
	}
	if _, err := implementation.Stat(context.Background(), providerapi.StatRequest{Location: normalizePlanTest(t, implementation, "/source")}); err != nil {
		t.Fatalf("Provider failed after Helper cancel: %v", err)
	}
}

type recordingSameHostBackend struct {
	root           string
	payload        []byte
	prepareErr     error
	prepareCalls   int
	stageCalls     int
	modifiedOffset time.Duration
}

type blockingSameHostBackend struct {
	recordingSameHostBackend
	started chan struct{}
	once    sync.Once
}

type blockingPrepareSameHostBackend struct {
	recordingSameHostBackend
	started         chan struct{}
	continuePrepare chan struct{}
	once            sync.Once
}

func (backend *blockingPrepareSameHostBackend) PrepareCopy(ctx context.Context, request SameHostCopyPrepareRequest) (SameHostCopyBinding, error) {
	backend.once.Do(func() { close(backend.started) })
	select {
	case <-ctx.Done():
		return SameHostCopyBinding{}, ctx.Err()
	case <-backend.continuePrepare:
		return backend.recordingSameHostBackend.PrepareCopy(ctx, request)
	}
}

func (backend *blockingSameHostBackend) StageCopy(ctx context.Context, _ SameHostCopyStageRequest) (SameHostCopyStageResult, error) {
	backend.once.Do(func() { close(backend.started) })
	<-ctx.Done()
	return SameHostCopyStageResult{}, ctx.Err()
}

func (backend *recordingSameHostBackend) PrepareCopy(_ context.Context, request SameHostCopyPrepareRequest) (SameHostCopyBinding, error) {
	backend.prepareCalls++
	if backend.prepareErr != nil {
		return SameHostCopyBinding{}, backend.prepareErr
	}
	digest := sha256.Sum256(backend.payload)
	modified := int64(0)
	fileID := ""
	if request.ExpectedFingerprint.ModifiedAt != nil {
		modified = request.ExpectedFingerprint.ModifiedAt.Add(backend.modifiedOffset).UnixNano()
	}
	if request.ExpectedFingerprint.FileID != nil {
		fileID = *request.ExpectedFingerprint.FileID
	}
	return SameHostCopyBinding{
		EndpointID: request.Source.EndpointID,
		ArtifactID: domain.HelperArtifactID{ProtocolMajor: 1, Version: "4.0.0", OS: "linux", Arch: "amd64", SHA256: strings.Repeat("a", 64)},
		Protocol:   1, HelperVersion: "4.0.0", CapabilityVersion: 1,
		SourceSHA256: hex.EncodeToString(digest[:]), SourceSize: uint64(len(backend.payload)),
		SourceIdentity: SameHostSourceIdentity{Size: uint64(len(backend.payload)), Mode: 0o600, ModifiedUnixNS: modified, FileID: fileID},
	}, nil
}

func (backend *recordingSameHostBackend) StageCopy(_ context.Context, request SameHostCopyStageRequest) (SameHostCopyStageResult, error) {
	backend.stageCalls++
	part := filepath.Join(backend.root, filepath.FromSlash(string(request.Part.Path)))
	if err := os.WriteFile(part, backend.payload, 0o600); err != nil {
		return SameHostCopyStageResult{}, err
	}
	return SameHostCopyStageResult{Part: request.Part, Size: uint64(len(backend.payload)), SHA256: request.Binding.SourceSHA256, Committed: false}, nil
}

func withNewJobIdentity(request FreezeRequest) FreezeRequest {
	request.JobID = "job_bbbbbbbbbbbbbbbbbbbbbbbbbb"
	request.RequestID = "req_bbbbbbbbbbbbbbbbbbbbbbbbbb"
	request.EventID = "evt_bbbbbbbbbbbbbbbbbbbbbbbbbb"
	request.PlanID = "plan_bbbbbbbbbbbbbbbbbbbbbbbbbb"
	return request
}
