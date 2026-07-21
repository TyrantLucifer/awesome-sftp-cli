package transfer

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/TyrantLucifer/awesome-sftp-cli/internal/directprotocol"
	"github.com/TyrantLucifer/awesome-sftp-cli/internal/domain"
	"github.com/TyrantLucifer/awesome-sftp-cli/internal/job"
	providerapi "github.com/TyrantLucifer/awesome-sftp-cli/internal/provider"
	"github.com/TyrantLucifer/awesome-sftp-cli/internal/testkit"
)

type countingContentProvider struct {
	*endpointKindProvider
	openReads int
}

func (provider *countingContentProvider) OpenRead(ctx context.Context, request providerapi.OpenReadRequest) (providerapi.ReadHandle, error) {
	provider.openReads++
	return provider.endpointKindProvider.OpenRead(ctx, request)
}

type level2DataFixture struct {
	sourceRoot      string
	destinationRoot string
	stageCalls      int
	verifyCalls     []domain.Location
	stagedBytes     uint64
	preflightCalls  int
	preflightStatus directprotocol.Status
	preflightNonces []string
	stageNonces     []string
}

type blockingLevel2DataFixture struct {
	*level2DataFixture
	started chan struct{}
}

type silentLevel2DataFixture struct{ *level2DataFixture }

type failingBeforeWriteLevel2Fixture struct{ *level2DataFixture }

type typedFailureBeforeWriteLevel2Fixture struct {
	*level2DataFixture
	code domain.Code
}

type disconnectAfterProgressLevel2Fixture struct{ *level2DataFixture }

func (fixture *failingBeforeWriteLevel2Fixture) Stage(context.Context, Level2StageRequest, func(Level2Progress) error) (Level2StageResult, error) {
	fixture.stageCalls++
	return Level2StageResult{}, errors.New("direct network unavailable before write")
}

func (fixture *typedFailureBeforeWriteLevel2Fixture) Stage(_ context.Context, request Level2StageRequest, _ func(Level2Progress) error) (Level2StageResult, error) {
	fixture.stageCalls++
	return Level2StageResult{}, &domain.OpError{
		Code: fixture.code, Message: "injected post-preflight direct failure", Operation: "stage_direct",
		EndpointID: request.Part.EndpointID, Location: &request.Part,
		Retry: domain.RetryAdvice{Kind: domain.RetryAfterReplan}, Effect: domain.EffectNone,
	}
}

func (fixture *disconnectAfterProgressLevel2Fixture) Stage(ctx context.Context, request Level2StageRequest, acknowledge func(Level2Progress) error) (Level2StageResult, error) {
	stop := errors.New("stop after first durable acknowledgement")
	_, err := fixture.level2DataFixture.Stage(ctx, request, func(progress Level2Progress) error {
		if err := acknowledge(progress); err != nil {
			return err
		}
		return stop
	})
	if !errors.Is(err, stop) {
		return Level2StageResult{}, err
	}
	return Level2StageResult{}, &domain.OpError{
		Code: domain.CodeTransportInterrupted, Message: "injected disconnect", Operation: "stage_direct",
		EndpointID: request.Part.EndpointID, Location: &request.Part,
		Retry: domain.RetryAdvice{Kind: domain.RetryAfterReconnect}, Effect: domain.EffectNone,
	}
}

type lostStageResponseLevel2Fixture struct{ *level2DataFixture }

func (fixture *lostStageResponseLevel2Fixture) Stage(ctx context.Context, request Level2StageRequest, acknowledge func(Level2Progress) error) (Level2StageResult, error) {
	result, err := fixture.level2DataFixture.Stage(ctx, request, acknowledge)
	if err != nil {
		return result, err
	}
	return Level2StageResult{}, errors.New("direct completion response lost")
}

type corruptStageResultLevel2Fixture struct{ *level2DataFixture }

func (fixture *corruptStageResultLevel2Fixture) Stage(ctx context.Context, request Level2StageRequest, acknowledge func(Level2Progress) error) (Level2StageResult, error) {
	result, err := fixture.level2DataFixture.Stage(ctx, request, acknowledge)
	if err != nil {
		return result, err
	}
	result.SHA256 = strings.Repeat("b", 64)
	return result, nil
}

type corruptPartVerifyLevel2Fixture struct{ *level2DataFixture }

func (fixture *corruptPartVerifyLevel2Fixture) Verify(ctx context.Context, request Level2VerifyRequest) (Level2VerifyResult, error) {
	result, err := fixture.level2DataFixture.Verify(ctx, request)
	if err == nil && strings.Contains(string(request.Location.Path), ".part-") {
		result.SHA256 = strings.Repeat("b", 64)
	}
	return result, err
}

type mutateSourceAfterStageLevel2Fixture struct{ *level2DataFixture }

type mutateSourceAfterFinalVerifyLevel2Fixture struct {
	*level2DataFixture
	mutated bool
}

func (fixture *mutateSourceAfterStageLevel2Fixture) Stage(ctx context.Context, request Level2StageRequest, acknowledge func(Level2Progress) error) (Level2StageResult, error) {
	result, err := fixture.level2DataFixture.Stage(ctx, request, acknowledge)
	if err != nil {
		return result, err
	}
	if err := os.WriteFile(fixture.localPath(request.Source), []byte("source changed after stage"), 0o600); err != nil { // #nosec G304 -- isolated fixture path.
		return Level2StageResult{}, err
	}
	return result, nil
}

func (fixture *mutateSourceAfterFinalVerifyLevel2Fixture) Verify(ctx context.Context, request Level2VerifyRequest) (Level2VerifyResult, error) {
	result, err := fixture.level2DataFixture.Verify(ctx, request)
	if err != nil || fixture.mutated || strings.Contains(string(request.Location.Path), ".part-") {
		return result, err
	}
	fixture.mutated = true
	if err := os.WriteFile(filepath.Join(fixture.sourceRoot, "source.bin"), []byte("source changed after commit"), 0o600); err != nil {
		return Level2VerifyResult{}, err
	}
	return result, nil
}

func (fixture *blockingLevel2DataFixture) Stage(ctx context.Context, request Level2StageRequest, acknowledge func(Level2Progress) error) (Level2StageResult, error) {
	fixture.stageCalls++
	part, err := os.OpenFile(fixture.localPath(request.Part), os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600) // #nosec G304 -- isolated test fixture path.
	if err != nil {
		return Level2StageResult{}, err
	}
	payload := []byte("partial")
	if _, err := part.Write(payload); err != nil {
		_ = part.Close()
		return Level2StageResult{}, err
	}
	if err := part.Sync(); err != nil {
		_ = part.Close()
		return Level2StageResult{}, err
	}
	if err := part.Close(); err != nil {
		return Level2StageResult{}, err
	}
	digest, err := fixture.digest(request.Part)
	if err != nil {
		return Level2StageResult{}, err
	}
	if err := acknowledge(Level2Progress{Part: request.Part, Offset: uint64(len(payload)), PrefixSHA256: digest, Durable: true}); err != nil {
		return Level2StageResult{}, err
	}
	close(fixture.started)
	<-ctx.Done()
	return Level2StageResult{}, ctx.Err()
}

func (fixture *silentLevel2DataFixture) Stage(ctx context.Context, _ Level2StageRequest, _ func(Level2Progress) error) (Level2StageResult, error) {
	fixture.stageCalls++
	<-ctx.Done()
	return Level2StageResult{}, ctx.Err()
}

func (fixture *level2DataFixture) Preflight(_ context.Context, request directprotocol.Request) (directprotocol.Result, error) {
	fixture.preflightCalls++
	fixture.preflightNonces = append(fixture.preflightNonces, request.Nonce)
	result, err := passingLevel2PreflightResult(request)
	if fixture.preflightStatus != "" && fixture.preflightStatus != directprotocol.Pass {
		result.Checks[0].Status = fixture.preflightStatus
		result.Checks[0].Reason = "protocol_" + string(fixture.preflightStatus)
	}
	return result, err
}

func (fixture *level2DataFixture) Stage(ctx context.Context, request Level2StageRequest, acknowledge func(Level2Progress) error) (Level2StageResult, error) {
	fixture.stageCalls++
	fixture.stageNonces = append(fixture.stageNonces, request.Nonce)
	source, err := os.Open(fixture.localPath(request.Source)) // #nosec G304 -- test fixture roots are isolated t.TempDir paths.
	if err != nil {
		return Level2StageResult{}, err
	}
	defer source.Close()
	flags := os.O_WRONLY | os.O_CREATE
	if request.ResumeOffset == 0 {
		flags |= os.O_EXCL
	}
	part, err := os.OpenFile(fixture.localPath(request.Part), flags, 0o600) // #nosec G304 -- test fixture roots are isolated t.TempDir paths.
	if err != nil {
		return Level2StageResult{}, err
	}
	defer part.Close()
	if _, err := source.Seek(int64(request.ResumeOffset), io.SeekStart); err != nil { //nolint:gosec // direct protocol caps offsets at 1 TiB.
		return Level2StageResult{}, err
	}
	if _, err := part.Seek(int64(request.ResumeOffset), io.SeekStart); err != nil { //nolint:gosec // direct protocol caps offsets at 1 TiB.
		return Level2StageResult{}, err
	}
	buffer := make([]byte, 64*1024)
	offset := request.ResumeOffset
	for {
		n, readErr := source.Read(buffer)
		if n > 0 {
			if _, err := part.Write(buffer[:n]); err != nil {
				return Level2StageResult{}, err
			}
			offset += uint64(n)
			fixture.stagedBytes += uint64(n)
			if err := part.Sync(); err != nil {
				return Level2StageResult{}, err
			}
			prefixDigest, err := fixture.digest(request.Part)
			if err != nil {
				return Level2StageResult{}, err
			}
			if err := acknowledge(Level2Progress{Part: request.Part, Offset: offset, PrefixSHA256: prefixDigest, Durable: true}); err != nil {
				return Level2StageResult{}, err
			}
		}
		if readErr != nil {
			if !errors.Is(readErr, io.EOF) {
				return Level2StageResult{}, readErr
			}
			break
		}
		select {
		case <-ctx.Done():
			return Level2StageResult{}, ctx.Err()
		default:
		}
	}
	digest, err := fixture.digest(request.Part)
	if err != nil {
		return Level2StageResult{}, err
	}
	return Level2StageResult{Part: request.Part, Size: offset, SHA256: digest, Committed: false}, nil
}

func (fixture *level2DataFixture) Verify(_ context.Context, request Level2VerifyRequest) (Level2VerifyResult, error) {
	fixture.verifyCalls = append(fixture.verifyCalls, request.Location)
	digest, err := fixture.digest(request.Location)
	if err != nil {
		return Level2VerifyResult{}, err
	}
	info, err := os.Stat(fixture.localPath(request.Location))
	if err != nil {
		return Level2VerifyResult{}, err
	}
	return Level2VerifyResult{Location: request.Location, Size: uint64(info.Size()), SHA256: digest}, nil // #nosec G115 -- isolated fixture files are non-negative and bounded by the Plan.
}

func (fixture *level2DataFixture) digest(location domain.Location) (string, error) {
	handle, err := os.Open(fixture.localPath(location)) // #nosec G304 -- test fixture roots are isolated t.TempDir paths.
	if err != nil {
		return "", err
	}
	defer handle.Close()
	hasher := sha256.New()
	if _, err := io.Copy(hasher, handle); err != nil {
		return "", err
	}
	return hex.EncodeToString(hasher.Sum(nil)), nil
}

func (fixture *level2DataFixture) localPath(location domain.Location) string {
	root := fixture.destinationRoot
	if location.EndpointID == "ep_aaaaaaaaaaaaaaaaaaaaaaaaaa" {
		root = fixture.sourceRoot
	}
	return filepath.Join(root, filepath.FromSlash(strings.TrimPrefix(string(location.Path), "/")))
}

// newLevel2FixtureWorker is intentionally test-only: production constructors
// cannot attach the non-release Level 2 data-plane facet.
func newLevel2FixtureWorker(resolver Resolver, journal Journal, backend level2DataBackend) *Worker {
	return &Worker{resolver: resolver, journal: journal, level2: backend}
}

// attachLevel2FixtureManager is intentionally test-only. It keeps both the
// planner and worker facets unreachable from production configuration.
func attachLevel2FixtureManager(manager *Manager, planner *Planner, backend level2DataBackend) {
	manager.planner = planner
	manager.level2 = backend
}

func TestLevel2DurableManagerJobPreservesRouteEvidenceAndMoveSemantics(t *testing.T) {
	tests := []struct {
		name               string
		clipboard          ClipboardKind
		mutateAfterCommit  bool
		removeResponseLost bool
		wantState          job.State
		wantSource         bool
	}{
		{name: "copy", clipboard: ClipboardCopy, wantState: job.StateCompleted, wantSource: true},
		{name: "move deletes unchanged source", clipboard: ClipboardCut, wantState: job.StateCompleted},
		{name: "move proves source delete response loss", clipboard: ClipboardCut, removeResponseLost: true, wantState: job.StateCompleted},
		{name: "move retains changed source", clipboard: ClipboardCut, mutateAfterCommit: true, wantState: job.StateCompletedWithSourceRetained, wantSource: true},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			planner, plan, source, destination, sourceRoot, destinationRoot := newLevel2DirectPlanFixture(t)
			baseBackend := &level2DataFixture{sourceRoot: sourceRoot, destinationRoot: destinationRoot}
			var backend level2DataBackend = baseBackend
			if test.mutateAfterCommit {
				backend = &mutateSourceAfterFinalVerifyLevel2Fixture{level2DataFixture: baseBackend}
			}
			store, database := openTransferStore(t, context.Background(), testDatabasePath(t), true)
			t.Cleanup(func() { _ = database.Close() })
			var sourceProvider providerapi.Provider = source
			if test.removeResponseLost {
				sourceProvider = &removeResponseLostProvider{mutableTestProvider: source}
			}
			manager, err := NewManager(ManagerConfig{
				Store: store, Resolver: MapResolver{source.Descriptor().ID: sourceProvider, destination.Descriptor().ID: destination},
				Generator: &testkit.SequenceGenerator{}, Now: time.Now, MaxConcurrent: 1,
			})
			if err != nil {
				t.Fatal(err)
			}
			attachLevel2FixtureManager(manager, planner, backend)
			t.Cleanup(manager.Close)
			if err := manager.Start(context.Background()); err != nil {
				t.Fatal(err)
			}
			created, err := manager.CreateCopy(context.Background(), Intent{
				Clipboard: test.clipboard, Source: plan.Source, DestinationDirectory: plan.DestinationDirectory,
				Name: plan.RequestedName, ConflictPolicy: ConflictAsk, DirectPolicy: plan.DirectPolicy,
			})
			if err != nil {
				t.Fatal(err)
			}
			completed := waitForTerminal(t, manager, created.JobID)
			if completed.State != test.wantState {
				t.Fatalf("terminal state = %q, want %q", completed.State, test.wantState)
			}
			if baseBackend.stageCalls != 1 {
				t.Fatalf("direct stage calls = %d, want 1", baseBackend.stageCalls)
			}
			if _, err := os.Stat(filepath.Join(sourceRoot, "source.bin")); (err == nil) != test.wantSource {
				t.Fatalf("source existence = %v, want %v (error %v)", err == nil, test.wantSource, err)
			}
			assertWorkerBytes(t, destination, domain.Location{EndpointID: destination.Descriptor().ID, Path: "/final"}, []byte("direct payload"))

			views, err := manager.JobViews(context.Background(), 10)
			if err != nil || len(views) != 1 {
				t.Fatalf("JobViews() = (%#v, %v)", views, err)
			}
			if views[0].PlannedRoute != RouteLevel2Direct || views[0].Route != RouteLevel2Direct ||
				views[0].RouteReason != ReasonLevel2PreflightPassed {
				t.Fatalf("durable direct route view = %#v", views[0])
			}
			events, err := manager.Events(context.Background(), created.JobID, 0, 32)
			if err != nil {
				t.Fatal(err)
			}
			createdEvidence, actualEvidence := false, false
			for _, event := range events {
				if event.Kind == "job_created" && strings.Contains(event.PayloadJSON, `"selected_route":"level2_direct"`) &&
					strings.Contains(event.PayloadJSON, `"route_reason":"level2_preflight_passed"`) {
					createdEvidence = true
				}
				if event.Kind == "job_verifying" && strings.Contains(event.PayloadJSON, `"planned_route":"level2_direct"`) &&
					strings.Contains(event.PayloadJSON, `"actual_route":"level2_direct"`) {
					actualEvidence = true
				}
			}
			if !createdEvidence || !actualEvidence {
				t.Fatalf("durable direct route events missing: created=%v actual=%v events=%#v", createdEvidence, actualEvidence, events)
			}
		})
	}
}

func TestLevel2DirectFixtureStagesRemoteToRemoteWithDurableProgressAndRemoteVerification(t *testing.T) {
	_, plan, sourceBase, destinationBase, sourceRoot, destinationRoot := newLevel2DirectPlanFixture(t)
	source := &countingContentProvider{endpointKindProvider: sourceBase}
	destination := &countingContentProvider{endpointKindProvider: destinationBase}
	resolver := MapResolver{plan.SourceEndpoint.ID: source, plan.DestinationEndpoint.ID: destination}
	backend := &level2DataFixture{sourceRoot: sourceRoot, destinationRoot: destinationRoot}
	journal := newMemoryJournal()

	result, err := newLevel2FixtureWorker(resolver, journal, backend).Execute(context.Background(), plan, nil)
	if err != nil {
		t.Fatalf("Execute(Level 2): %v", err)
	}
	if result.Outcome != OutcomeCompleted || result.SHA256 != plan.Level2Preflight.Result.SourceSHA256 || result.Bytes != *plan.Source.Fingerprint.Size {
		t.Fatalf("direct result = %#v", result)
	}
	if backend.stageCalls != 1 || backend.stagedBytes != result.Bytes || len(backend.verifyCalls) < 2 {
		t.Fatalf("direct backend stage/bytes/verifies = %d/%d/%#v", backend.stageCalls, backend.stagedBytes, backend.verifyCalls)
	}
	if source.openReads != 0 || destination.openReads != 0 {
		t.Fatalf("daemon Provider content reads = source %d, destination %d", source.openReads, destination.openReads)
	}
	data, err := os.ReadFile(backend.localPath(result.Final)) // #nosec G304 -- isolated fixture path.
	if err != nil || string(data) != "direct payload" {
		t.Fatalf("direct final = %q, %v", data, err)
	}
	seenStreaming := false
	for _, checkpoint := range journal.checkpoints {
		if checkpoint.Phase == PhaseStreaming && checkpoint.DirectFormatVersion == Level2DirectFormatVersion && checkpoint.Offset > 0 {
			seenStreaming = true
		}
	}
	if !seenStreaming {
		t.Fatal("no durable target-acknowledged direct progress was persisted")
	}
}

func TestProductionWorkerCannotExecuteFixtureOnlyLevel2Plan(t *testing.T) {
	planner, plan, _, _, _, _ := newLevel2DirectPlanFixture(t)
	_, err := NewWorker(planner.resolver, newMemoryJournal()).Execute(context.Background(), plan, nil)
	if err == nil || !strings.Contains(err.Error(), "fixture is not attached") {
		t.Fatalf("production Execute error = %v", err)
	}
}

func TestLevel2CheckpointRejectsDifferentValidRequestNonce(t *testing.T) {
	_, plan, _, _, _, _ := newLevel2DirectPlanFixture(t)
	checkpoint := Checkpoint{
		JobID: plan.JobID, Phase: PhaseStreaming,
		SourceFingerprint: cloneFingerprint(plan.Source.Fingerprint),
		Part:              plan.Part, Final: plan.Final,
		ActualRoute: RouteLevel2Direct, RouteReason: ReasonLevel2PreflightPassed,
		DirectFormatVersion: Level2DirectFormatVersion,
		DirectNonce:         "fedcba9876543210fedcba9876543210",
	}
	if checkpoint.DirectNonce == plan.Level2Preflight.Request.Nonce {
		t.Fatal("test nonce unexpectedly equals frozen request nonce")
	}
	if checkpointMatchesPlan(checkpoint, plan) {
		t.Fatal("checkpoint from a different valid direct correlation matched the frozen Plan")
	}
}

func TestLevel2ExpiredPreflightIsFreshlyRevalidatedBeforeAnyTargetWrite(t *testing.T) {
	for _, status := range []directprotocol.Status{directprotocol.Pass, directprotocol.Fail, directprotocol.Unknown} {
		t.Run(string(status), func(t *testing.T) {
			_, plan, sourceBase, destinationBase, sourceRoot, destinationRoot := newLevel2DirectPlanFixture(t)
			originalNonce := plan.Level2Preflight.Request.Nonce
			source := &countingContentProvider{endpointKindProvider: sourceBase}
			destination := &countingContentProvider{endpointKindProvider: destinationBase}
			frozenAt := time.Now().Add(-time.Hour).UTC().Truncate(time.Second)
			plan.FrozenAt = frozenAt
			plan.Level2Preflight.Request.DeadlineUnix = frozenAt.Add(5 * time.Minute).Unix()
			plan.Level2Preflight.Result.CheckedAtUnix = frozenAt.Unix()
			plan.Level2Preflight.Result.ExpiresAtUnix = frozenAt.Add(time.Minute).Unix()
			backend := &level2DataFixture{
				sourceRoot: sourceRoot, destinationRoot: destinationRoot, preflightStatus: status,
			}
			resolver := MapResolver{plan.SourceEndpoint.ID: source, plan.DestinationEndpoint.ID: destination}
			journal := newMemoryJournal()
			result, err := newLevel2FixtureWorker(resolver, journal, backend).Execute(context.Background(), plan, nil)
			if backend.preflightCalls != 1 {
				t.Fatalf("fresh preflight calls = %d, want 1", backend.preflightCalls)
			}
			if status == directprotocol.Pass {
				if err != nil || result.Outcome != OutcomeCompleted || backend.stageCalls != 1 {
					t.Fatalf("fresh pass result = (%#v, %v), stage calls %d", result, err, backend.stageCalls)
				}
				if len(backend.preflightNonces) != 1 || backend.preflightNonces[0] != originalNonce ||
					len(backend.stageNonces) != 1 || backend.stageNonces[0] != originalNonce {
					t.Fatalf("refreshed preflight/stage nonce = %#v/%#v, want durable correlation %q", backend.preflightNonces, backend.stageNonces, originalNonce)
				}
				return
			}
			if err != nil || result.Outcome != OutcomeCompleted || backend.stageCalls != 0 || source.openReads == 0 || destination.openReads == 0 {
				t.Fatalf("fresh %s relay result = (%#v, %v), stage/reads %d/%d/%d", status, result, err, backend.stageCalls, source.openReads, destination.openReads)
			}
			checkpoint := journal.latest()
			if checkpoint.ActualRoute != RouteSFTPRelay || checkpoint.DowngradedFrom != RouteLevel2Direct || checkpoint.RouteReason != RouteReason("direct_revalidation_failed") {
				t.Fatalf("fresh %s route checkpoint = %#v", status, checkpoint)
			}
			for _, saved := range journal.checkpoints {
				if saved.RouteReason == RouteReason("direct_revalidation_failed") &&
					(saved.DirectFormatVersion != Level2DirectFormatVersion || saved.DirectNonce != plan.Level2Preflight.Request.Nonce) {
					t.Fatalf("fresh %s downgrade was not restart-safe before I/O: %#v", status, saved)
				}
			}
		})
	}
}

func TestLevel2RestartHonorsPreparedRevalidationFallbackWithoutRetryingDirect(t *testing.T) {
	_, plan, source, destination, sourceRoot, destinationRoot := newLevel2DirectPlanFixture(t)
	frozenAt := time.Now().Add(-time.Hour).UTC().Truncate(time.Second)
	plan.FrozenAt = frozenAt
	plan.Level2Preflight.Request.DeadlineUnix = frozenAt.Add(5 * time.Minute).Unix()
	plan.Level2Preflight.Result.CheckedAtUnix = frozenAt.Unix()
	plan.Level2Preflight.Result.ExpiresAtUnix = frozenAt.Add(time.Minute).Unix()
	backend := &level2DataFixture{sourceRoot: sourceRoot, destinationRoot: destinationRoot, preflightStatus: directprotocol.Fail}
	journal := newMemoryJournal()
	if err := journal.Save(context.Background(), Checkpoint{
		JobID: plan.JobID, Phase: PhasePrepared, SourceFingerprint: cloneFingerprint(plan.Source.Fingerprint),
		Part: plan.Part, Final: plan.Final, ActualRoute: RouteSFTPRelay, DowngradedFrom: RouteLevel2Direct,
		RouteReason: ReasonLevel2RevalidationFailed, DirectFormatVersion: Level2DirectFormatVersion,
		DirectNonce: plan.Level2Preflight.Request.Nonce,
	}); err != nil {
		t.Fatal(err)
	}
	result, err := newLevel2FixtureWorker(MapResolver{plan.SourceEndpoint.ID: source, plan.DestinationEndpoint.ID: destination}, journal, backend).Execute(context.Background(), plan, nil)
	if err != nil || result.Outcome != OutcomeCompleted {
		t.Fatalf("prepared relay restart = (%#v, %v)", result, err)
	}
	if backend.preflightCalls != 0 || backend.stageCalls != 0 {
		t.Fatalf("prepared relay restart retried direct: preflight/stage=%d/%d", backend.preflightCalls, backend.stageCalls)
	}
}

func TestLevel2FailedRevalidationCleansExactAcknowledgedPartThenRelays(t *testing.T) {
	_, plan, source, destination, sourceRoot, destinationRoot := newLevel2DirectPlanFixture(t)
	frozenAt := time.Now().Add(-time.Hour).UTC().Truncate(time.Second)
	plan.FrozenAt = frozenAt
	plan.Level2Preflight.Request.DeadlineUnix = frozenAt.Add(5 * time.Minute).Unix()
	plan.Level2Preflight.Result.CheckedAtUnix = frozenAt.Unix()
	plan.Level2Preflight.Result.ExpiresAtUnix = frozenAt.Add(time.Minute).Unix()
	partial := []byte("partial")
	partPath := filepath.Join(destinationRoot, strings.TrimPrefix(string(plan.Part.Path), "/"))
	if err := os.WriteFile(partPath, partial, 0o600); err != nil {
		t.Fatal(err)
	}
	partEntry, err := destination.Stat(context.Background(), providerapi.StatRequest{Location: plan.Part})
	if err != nil {
		t.Fatal(err)
	}
	prefix := sha256.Sum256(partial)
	journal := newMemoryJournal()
	if err := journal.Save(context.Background(), Checkpoint{
		JobID: plan.JobID, Phase: PhaseStreaming, Offset: uint64(len(partial)), SourceFingerprint: cloneFingerprint(plan.Source.Fingerprint),
		Part: plan.Part, PartFingerprint: cloneFingerprint(partEntry.Fingerprint), ChecksumHex: hex.EncodeToString(prefix[:]), Final: plan.Final,
		ActualRoute: RouteLevel2Direct, RouteReason: ReasonLevel2PreflightPassed,
		DirectFormatVersion: Level2DirectFormatVersion, DirectNonce: plan.Level2Preflight.Request.Nonce,
	}); err != nil {
		t.Fatal(err)
	}
	backend := &level2DataFixture{sourceRoot: sourceRoot, destinationRoot: destinationRoot, preflightStatus: directprotocol.Fail}
	result, err := newLevel2FixtureWorker(MapResolver{plan.SourceEndpoint.ID: source, plan.DestinationEndpoint.ID: destination}, journal, backend).Execute(context.Background(), plan, nil)
	if err != nil || result.Outcome != OutcomeCompleted || backend.stageCalls != 0 {
		t.Fatalf("failed-revalidation cleanup relay = (%#v, %v), stage calls %d", result, err, backend.stageCalls)
	}
	checkpoint := journal.latest()
	if checkpoint.ActualRoute != RouteSFTPRelay || checkpoint.DowngradedFrom != RouteLevel2Direct || checkpoint.RouteReason != ReasonLevel2PartCleanedForRelay {
		t.Fatalf("failed-revalidation cleanup checkpoint = %#v", checkpoint)
	}
}

func TestLevel2FailedRevalidationNeverCleansDriftedPart(t *testing.T) {
	_, plan, source, destination, sourceRoot, destinationRoot := newLevel2DirectPlanFixture(t)
	frozenAt := time.Now().Add(-time.Hour).UTC().Truncate(time.Second)
	plan.FrozenAt = frozenAt
	plan.Level2Preflight.Request.DeadlineUnix = frozenAt.Add(5 * time.Minute).Unix()
	plan.Level2Preflight.Result.CheckedAtUnix = frozenAt.Unix()
	plan.Level2Preflight.Result.ExpiresAtUnix = frozenAt.Add(time.Minute).Unix()
	partPath := filepath.Join(destinationRoot, strings.TrimPrefix(string(plan.Part.Path), "/"))
	if err := os.WriteFile(partPath, []byte("partial"), 0o600); err != nil {
		t.Fatal(err)
	}
	partEntry, err := destination.Stat(context.Background(), providerapi.StatRequest{Location: plan.Part})
	if err != nil {
		t.Fatal(err)
	}
	journal := newMemoryJournal()
	if err := journal.Save(context.Background(), Checkpoint{
		JobID: plan.JobID, Phase: PhaseStreaming, Offset: 7, SourceFingerprint: cloneFingerprint(plan.Source.Fingerprint),
		Part: plan.Part, PartFingerprint: cloneFingerprint(partEntry.Fingerprint), ChecksumHex: strings.Repeat("a", 64), Final: plan.Final,
		ActualRoute: RouteLevel2Direct, RouteReason: ReasonLevel2PreflightPassed,
		DirectFormatVersion: Level2DirectFormatVersion, DirectNonce: plan.Level2Preflight.Request.Nonce,
	}); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(partPath, []byte("drifted foreign bytes"), 0o600); err != nil {
		t.Fatal(err)
	}
	backend := &level2DataFixture{sourceRoot: sourceRoot, destinationRoot: destinationRoot, preflightStatus: directprotocol.Fail}
	_, err = newLevel2FixtureWorker(MapResolver{plan.SourceEndpoint.ID: source, plan.DestinationEndpoint.ID: destination}, journal, backend).Execute(context.Background(), plan, nil)
	if err == nil || backend.stageCalls != 0 {
		t.Fatalf("drifted part result = %v, stage calls %d", err, backend.stageCalls)
	}
	if data, readErr := os.ReadFile(partPath); readErr != nil || string(data) != "drifted foreign bytes" { // #nosec G304 -- isolated fixture path.
		t.Fatalf("drifted part was changed or removed: %q, %v", data, readErr)
	}
	if _, statErr := os.Stat(backend.localPath(plan.Final)); !errors.Is(statErr, os.ErrNotExist) {
		t.Fatalf("drifted part failure published final: %v", statErr)
	}
}

func TestLevel2InFlightCancelPropagatesAndNeverCommitsOrDeletesSource(t *testing.T) {
	_, plan, sourceBase, destinationBase, sourceRoot, destinationRoot := newLevel2DirectPlanFixture(t)
	source := &countingContentProvider{endpointKindProvider: sourceBase}
	destination := &countingContentProvider{endpointKindProvider: destinationBase}
	backend := &blockingLevel2DataFixture{
		level2DataFixture: &level2DataFixture{sourceRoot: sourceRoot, destinationRoot: destinationRoot},
		started:           make(chan struct{}),
	}
	resolver := MapResolver{plan.SourceEndpoint.ID: source, plan.DestinationEndpoint.ID: destination}
	journal := newMemoryJournal()
	result, err := newLevel2FixtureWorker(resolver, journal, backend).Execute(context.Background(), plan, ControlFunc(func(Checkpoint) ControlAction {
		select {
		case <-backend.started:
			return ControlCancel
		default:
			return ControlContinue
		}
	}))
	if !errors.Is(err, ErrCanceled) || !result.PartRetained || result.Bytes == 0 {
		t.Fatalf("canceled direct result = (%#v, %v)", result, err)
	}
	if _, err := os.Stat(backend.localPath(plan.Final)); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("canceled direct committed final: %v", err)
	}
	if data, err := os.ReadFile(backend.localPath(plan.Source.Location)); err != nil || string(data) != "direct payload" { // #nosec G304 -- isolated fixture path.
		t.Fatalf("canceled direct changed source: %q, %v", data, err)
	}
	checkpoint := journal.latest()
	if checkpoint.Phase != PhaseStreaming || checkpoint.Offset != result.Bytes || checkpoint.ActualRoute != RouteLevel2Direct {
		t.Fatalf("canceled direct checkpoint = %#v", checkpoint)
	}
}

func TestLevel2SilentStageFailsClosedAtHeartbeatTimeout(t *testing.T) {
	_, plan, source, destination, sourceRoot, destinationRoot := newLevel2DirectPlanFixture(t)
	backend := &silentLevel2DataFixture{level2DataFixture: &level2DataFixture{sourceRoot: sourceRoot, destinationRoot: destinationRoot}}
	worker := newLevel2FixtureWorker(
		MapResolver{plan.SourceEndpoint.ID: source, plan.DestinationEndpoint.ID: destination},
		newMemoryJournal(),
		backend,
	)
	worker.level2HeartbeatTimeout = 20 * time.Millisecond
	started := time.Now()
	_, err := worker.Execute(context.Background(), plan, nil)
	if !domain.IsCode(err, domain.CodeTimeout) {
		t.Fatalf("silent direct stage error = %v, want timeout", err)
	}
	if elapsed := time.Since(started); elapsed > time.Second {
		t.Fatalf("heartbeat timeout took %s", elapsed)
	}
	if backend.stageCalls != 1 {
		t.Fatalf("stage calls = %d, want 1", backend.stageCalls)
	}
	if _, statErr := os.Stat(backend.localPath(plan.Final)); !errors.Is(statErr, os.ErrNotExist) {
		t.Fatalf("heartbeat timeout published final: %v", statErr)
	}
}

func TestLevel2SilentStageFailsClosedAtFrozenRequestDeadline(t *testing.T) {
	_, plan, source, destination, sourceRoot, destinationRoot := newLevel2DirectPlanFixture(t)
	now := time.Now()
	plan.FrozenAt = now
	deadline := now.Add(2 * time.Second).Unix()
	plan.Level2Preflight.Request.DeadlineUnix = deadline
	plan.Level2Preflight.Result.CheckedAtUnix = now.Unix()
	plan.Level2Preflight.Result.ExpiresAtUnix = deadline
	backend := &silentLevel2DataFixture{level2DataFixture: &level2DataFixture{sourceRoot: sourceRoot, destinationRoot: destinationRoot}}
	worker := newLevel2FixtureWorker(
		MapResolver{plan.SourceEndpoint.ID: source, plan.DestinationEndpoint.ID: destination},
		newMemoryJournal(),
		backend,
	)
	worker.level2HeartbeatTimeout = 5 * time.Second
	started := time.Now()
	_, err := worker.Execute(context.Background(), plan, nil)
	if !domain.IsCode(err, domain.CodeTimeout) {
		t.Fatalf("silent direct stage error = %v, want request deadline timeout", err)
	}
	if elapsed := time.Since(started); elapsed > 3*time.Second {
		t.Fatalf("request deadline took %s", elapsed)
	}
	if backend.stageCalls != 1 {
		t.Fatalf("stage calls = %d, want 1", backend.stageCalls)
	}
	if _, statErr := os.Stat(backend.localPath(plan.Final)); !errors.Is(statErr, os.ErrNotExist) {
		t.Fatalf("request deadline published final: %v", statErr)
	}
}

func TestLevel2FailureBeforeTargetWriteDurablyDowngradesToRelay(t *testing.T) {
	_, plan, sourceBase, destinationBase, sourceRoot, destinationRoot := newLevel2DirectPlanFixture(t)
	source := &countingContentProvider{endpointKindProvider: sourceBase}
	destination := &countingContentProvider{endpointKindProvider: destinationBase}
	backend := &failingBeforeWriteLevel2Fixture{level2DataFixture: &level2DataFixture{sourceRoot: sourceRoot, destinationRoot: destinationRoot}}
	resolver := MapResolver{plan.SourceEndpoint.ID: source, plan.DestinationEndpoint.ID: destination}
	journal := newMemoryJournal()
	result, err := newLevel2FixtureWorker(resolver, journal, backend).Execute(context.Background(), plan, nil)
	if err != nil || result.Outcome != OutcomeCompleted || result.SHA256 != plan.Level2Preflight.SourceSHA256 {
		t.Fatalf("direct-to-relay result = (%#v, %v)", result, err)
	}
	checkpoint := journal.latest()
	if checkpoint.ActualRoute != RouteSFTPRelay || checkpoint.DowngradedFrom != RouteLevel2Direct || checkpoint.RouteReason != ReasonLevel2FailedBeforeWrite {
		t.Fatalf("direct-to-relay checkpoint = %#v", checkpoint)
	}
	if backend.stageCalls != 1 || source.openReads == 0 || destination.openReads == 0 {
		t.Fatalf("direct/relay data calls = stage %d, source reads %d, destination reads %d", backend.stageCalls, source.openReads, destination.openReads)
	}
}

func TestLevel2PostPreflightFailuresBeforeWriteAreAuditedRelayFallbacks(t *testing.T) {
	for _, code := range []domain.Code{domain.CodeAuthRequired, domain.CodePermissionDenied, domain.CodeResourceExhausted} {
		t.Run(string(code), func(t *testing.T) {
			_, plan, source, destination, sourceRoot, destinationRoot := newLevel2DirectPlanFixture(t)
			backend := &typedFailureBeforeWriteLevel2Fixture{
				level2DataFixture: &level2DataFixture{sourceRoot: sourceRoot, destinationRoot: destinationRoot}, code: code,
			}
			journal := newMemoryJournal()
			result, err := newLevel2FixtureWorker(
				MapResolver{plan.SourceEndpoint.ID: source, plan.DestinationEndpoint.ID: destination}, journal, backend,
			).Execute(context.Background(), plan, nil)
			if err != nil || result.Outcome != OutcomeCompleted || result.SHA256 != plan.Level2Preflight.SourceSHA256 {
				t.Fatalf("post-preflight %s fallback = (%#v, %v)", code, result, err)
			}
			checkpoint := journal.latest()
			if checkpoint.ActualRoute != RouteSFTPRelay || checkpoint.DowngradedFrom != RouteLevel2Direct ||
				checkpoint.RouteReason != ReasonLevel2FailedBeforeWrite || backend.stageCalls != 1 {
				t.Fatalf("post-preflight %s route evidence = %#v, calls=%d", code, checkpoint, backend.stageCalls)
			}
			if _, err := destination.Stat(context.Background(), providerapi.StatRequest{Location: plan.Part}); !domain.IsCode(err, domain.CodeNotFound) {
				t.Fatalf("post-preflight %s left an orphan part: %v", code, err)
			}
		})
	}
}

func TestLevel2DisconnectAfterDurableProgressRetainsExactPartAndResumes(t *testing.T) {
	_, plan, source, destination, sourceRoot, destinationRoot := newLevel2DirectPlanFixture(t)
	base := &level2DataFixture{sourceRoot: sourceRoot, destinationRoot: destinationRoot}
	journal := newMemoryJournal()
	resolver := MapResolver{plan.SourceEndpoint.ID: source, plan.DestinationEndpoint.ID: destination}
	first, err := newLevel2FixtureWorker(resolver, journal, &disconnectAfterProgressLevel2Fixture{level2DataFixture: base}).Execute(context.Background(), plan, nil)
	if !domain.IsCode(err, domain.CodeTransportInterrupted) || first.Bytes == 0 || !first.PartRetained {
		t.Fatalf("disconnect result = (%#v, %v)", first, err)
	}
	checkpoint := journal.latest()
	if checkpoint.Phase != PhaseStreaming || checkpoint.Offset != first.Bytes || checkpoint.ActualRoute != RouteLevel2Direct {
		t.Fatalf("disconnect checkpoint = %#v", checkpoint)
	}
	if _, err := destination.Stat(context.Background(), providerapi.StatRequest{Location: plan.Final}); !domain.IsCode(err, domain.CodeNotFound) {
		t.Fatalf("disconnect exposed final: %v", err)
	}
	stagedBeforeResume := base.stagedBytes
	completed, err := newLevel2FixtureWorker(resolver, journal, base).Execute(context.Background(), plan, nil)
	if err != nil || completed.Outcome != OutcomeCompleted || base.stagedBytes-stagedBeforeResume != *plan.Source.Fingerprint.Size-first.Bytes {
		t.Fatalf("disconnect resume = (%#v, %v), newly staged=%d", completed, err, base.stagedBytes-stagedBeforeResume)
	}
}

func TestLevel2DirectAndRelayShareCancellationContract(t *testing.T) {
	relay := observeRouteCancellationContract(t, false)
	_, plan, source, destination, sourceRoot, destinationRoot := newLevel2DirectPlanFixture(t)
	result, executeErr := newLevel2FixtureWorker(
		MapResolver{plan.SourceEndpoint.ID: source, plan.DestinationEndpoint.ID: destination}, newMemoryJournal(),
		&level2DataFixture{sourceRoot: sourceRoot, destinationRoot: destinationRoot},
	).Execute(context.Background(), plan, ControlFunc(func(Checkpoint) ControlAction { return ControlCancel }))
	_, finalErr := destination.Stat(context.Background(), providerapi.StatRequest{Location: plan.Final})
	payload, sourceErr := os.ReadFile(filepath.Join(sourceRoot, "source.bin")) // #nosec G304 -- isolated fixture path.
	if sourceErr != nil {
		t.Fatal(sourceErr)
	}
	direct := routeCancellationObservation{
		Canceled: errors.Is(executeErr, ErrCanceled), Bytes: result.Bytes,
		FinalAbsent: domain.IsCode(finalErr, domain.CodeNotFound), Source: string(payload),
	}
	if relay.Source == "" || direct.Source != "direct payload" {
		t.Fatalf("cancellation did not retain source: direct=%q relay=%q", direct.Source, relay.Source)
	}
	relay.Source = "retained"
	direct.Source = "retained"
	if !reflect.DeepEqual(direct, relay) {
		t.Fatalf("direct cancellation = %+v, relay cancellation = %+v", direct, relay)
	}
}

func TestLevel2RestartAdoptsExactAcknowledgedPartAfterLostStageResponse(t *testing.T) {
	_, plan, source, destination, sourceRoot, destinationRoot := newLevel2DirectPlanFixture(t)
	base := &level2DataFixture{sourceRoot: sourceRoot, destinationRoot: destinationRoot}
	journal := newMemoryJournal()
	resolver := MapResolver{plan.SourceEndpoint.ID: source, plan.DestinationEndpoint.ID: destination}
	first := &lostStageResponseLevel2Fixture{level2DataFixture: base}
	result, err := newLevel2FixtureWorker(resolver, journal, first).Execute(context.Background(), plan, nil)
	if err == nil || result.Bytes != *plan.Source.Fingerprint.Size || !result.PartRetained {
		t.Fatalf("lost-response result = (%#v, %v)", result, err)
	}
	checkpoint := journal.latest()
	if checkpoint.Phase != PhaseStreaming || checkpoint.Offset != *plan.Source.Fingerprint.Size || checkpoint.ChecksumHex != plan.Level2Preflight.SourceSHA256 {
		t.Fatalf("lost-response checkpoint = %#v", checkpoint)
	}
	stagedBeforeRestart := base.stagedBytes
	completed, err := newLevel2FixtureWorker(resolver, journal, base).Execute(context.Background(), plan, nil)
	if err != nil || completed.Outcome != OutcomeCompleted {
		t.Fatalf("restart adoption = (%#v, %v)", completed, err)
	}
	if base.stagedBytes != stagedBeforeRestart {
		t.Fatalf("restart retransmitted acknowledged bytes: before=%d after=%d", stagedBeforeRestart, base.stagedBytes)
	}
}

func TestLevel2CommitResponseLossProvesRemoteFinalBeforeSuccess(t *testing.T) {
	_, plan, source, destination, sourceRoot, destinationRoot := newLevel2DirectPlanFixture(t)
	lost := &renameResponseLostProvider{mutableTestProvider: destination}
	backend := &level2DataFixture{sourceRoot: sourceRoot, destinationRoot: destinationRoot}
	journal := newMemoryJournal()
	result, err := newLevel2FixtureWorker(MapResolver{plan.SourceEndpoint.ID: source, plan.DestinationEndpoint.ID: lost}, journal, backend).Execute(context.Background(), plan, nil)
	if err != nil || result.Outcome != OutcomeCompleted || result.SHA256 != plan.Level2Preflight.SourceSHA256 {
		t.Fatalf("direct commit response loss = (%#v, %v)", result, err)
	}
	checkpoint := journal.latest()
	if checkpoint.Phase != PhaseCommitted || checkpoint.Outcome != OutcomeCompleted || checkpoint.ActualRoute != RouteLevel2Direct {
		t.Fatalf("direct commit response-loss checkpoint = %#v", checkpoint)
	}
	foundFinalProof := false
	for _, location := range backend.verifyCalls {
		if location == plan.Final {
			foundFinalProof = true
		}
	}
	if !foundFinalProof {
		t.Fatalf("commit response loss did not remotely prove final: %#v", backend.verifyCalls)
	}
}

func TestLevel2CorruptStageResultCleansOnlyExactPartThenRelaysFromZero(t *testing.T) {
	_, plan, sourceBase, destinationBase, sourceRoot, destinationRoot := newLevel2DirectPlanFixture(t)
	source := &countingContentProvider{endpointKindProvider: sourceBase}
	destination := &countingContentProvider{endpointKindProvider: destinationBase}
	backend := &corruptStageResultLevel2Fixture{level2DataFixture: &level2DataFixture{sourceRoot: sourceRoot, destinationRoot: destinationRoot}}
	foreign := filepath.Join(destinationRoot, "foreign.part")
	if err := os.WriteFile(foreign, []byte("must survive"), 0o600); err != nil {
		t.Fatal(err)
	}
	journal := newMemoryJournal()
	result, err := newLevel2FixtureWorker(MapResolver{plan.SourceEndpoint.ID: source, plan.DestinationEndpoint.ID: destination}, journal, backend).Execute(context.Background(), plan, nil)
	if err != nil || result.Outcome != OutcomeCompleted || result.SHA256 != plan.Level2Preflight.SourceSHA256 {
		t.Fatalf("corrupt-result relay = (%#v, %v)", result, err)
	}
	checkpoint := journal.latest()
	if checkpoint.ActualRoute != RouteSFTPRelay || checkpoint.DowngradedFrom != RouteLevel2Direct || checkpoint.RouteReason != RouteReason("direct_part_cleaned_for_relay") {
		t.Fatalf("corrupt-result route checkpoint = %#v", checkpoint)
	}
	if backend.stageCalls != 1 || source.openReads == 0 || destination.openReads == 0 {
		t.Fatalf("corrupt-result stage/relay reads = %d/%d/%d", backend.stageCalls, source.openReads, destination.openReads)
	}
	if data, readErr := os.ReadFile(foreign); readErr != nil || string(data) != "must survive" { // #nosec G304 -- isolated fixture path.
		t.Fatalf("foreign path changed during exact cleanup: %q, %v", data, readErr)
	}
	if _, statErr := os.Stat(backend.localPath(plan.Part)); !errors.Is(statErr, os.ErrNotExist) {
		t.Fatalf("exact Job part survived relay commit: %v", statErr)
	}
}

func TestLevel2CorruptRemotePartProofCleansExactPartThenRelays(t *testing.T) {
	_, plan, sourceBase, destinationBase, sourceRoot, destinationRoot := newLevel2DirectPlanFixture(t)
	source := &countingContentProvider{endpointKindProvider: sourceBase}
	destination := &countingContentProvider{endpointKindProvider: destinationBase}
	backend := &corruptPartVerifyLevel2Fixture{level2DataFixture: &level2DataFixture{sourceRoot: sourceRoot, destinationRoot: destinationRoot}}
	journal := newMemoryJournal()
	result, err := newLevel2FixtureWorker(MapResolver{plan.SourceEndpoint.ID: source, plan.DestinationEndpoint.ID: destination}, journal, backend).Execute(context.Background(), plan, nil)
	if err != nil || result.Outcome != OutcomeCompleted || result.SHA256 != plan.Level2Preflight.SourceSHA256 {
		t.Fatalf("corrupt-part-proof relay = (%#v, %v)", result, err)
	}
	checkpoint := journal.latest()
	if checkpoint.ActualRoute != RouteSFTPRelay || checkpoint.DowngradedFrom != RouteLevel2Direct || checkpoint.RouteReason != ReasonLevel2PartCleanedForRelay {
		t.Fatalf("corrupt-part-proof checkpoint = %#v", checkpoint)
	}
	if backend.stageCalls != 1 || source.openReads == 0 || destination.openReads == 0 {
		t.Fatalf("corrupt-part-proof stage/relay reads = %d/%d/%d", backend.stageCalls, source.openReads, destination.openReads)
	}
}

func TestLevel2SourceChangeAfterStageNeverCommitsOrDeletesSource(t *testing.T) {
	_, plan, source, destination, sourceRoot, destinationRoot := newLevel2DirectPlanFixture(t)
	backend := &mutateSourceAfterStageLevel2Fixture{level2DataFixture: &level2DataFixture{sourceRoot: sourceRoot, destinationRoot: destinationRoot}}
	result, err := newLevel2FixtureWorker(MapResolver{plan.SourceEndpoint.ID: source, plan.DestinationEndpoint.ID: destination}, newMemoryJournal(), backend).Execute(context.Background(), plan, nil)
	if err == nil || !result.PartRetained {
		t.Fatalf("source-change direct result = (%#v, %v)", result, err)
	}
	if _, statErr := os.Stat(backend.localPath(plan.Final)); !errors.Is(statErr, os.ErrNotExist) {
		t.Fatalf("source-change direct published final: %v", statErr)
	}
	if data, readErr := os.ReadFile(backend.localPath(plan.Source.Location)); readErr != nil || string(data) != "source changed after stage" { // #nosec G304 -- isolated fixture path.
		t.Fatalf("source-change direct deleted or rewrote source: %q, %v", data, readErr)
	}
}

func TestLevel2DirectAndRelayShareConflictCommitContract(t *testing.T) {
	for _, policy := range []ConflictPolicy{ConflictAsk, ConflictOverwrite, ConflictSkip, ConflictAutoRename} {
		t.Run(string(policy), func(t *testing.T) {
			relay := executeLevel2ConflictContract(t, false, policy)
			direct := executeLevel2ConflictContract(t, true, policy)
			if !reflect.DeepEqual(direct, relay) {
				t.Fatalf("direct result = %+v, relay result = %+v", direct, relay)
			}
		})
	}
}

func TestLevel2DirectAndRelayGoldenContentCommitAndIntegrityEquivalence(t *testing.T) {
	randomPayload := make([]byte, 257*1024+19)
	state := uint32(0x5eed1234)
	for index := range randomPayload {
		state ^= state << 13
		state ^= state >> 17
		state ^= state << 5
		randomPayload[index] = byte(state) // #nosec G115 -- the low byte is the intentional deterministic fixture output.
	}
	sparsePayload := make([]byte, 2*1024*1024)
	copy(sparsePayload, "sparse-prefix")
	copy(sparsePayload[len(sparsePayload)/2:], "sparse-middle")
	copy(sparsePayload[len(sparsePayload)-13:], "sparse-suffix")
	largePayload := make([]byte, 8*1024*1024)
	for offset := 0; offset < len(largePayload); offset += 4096 {
		largePayload[offset] = byte(offset / 4096)                // #nosec G115 -- intentional repeating byte fixture.
		largePayload[offset+4095] = byte(255 - (offset/4096)%256) // #nosec G115,G602 -- fixed 4096-byte blocks exactly divide the allocation.
	}

	for _, fixture := range []struct {
		name    string
		payload []byte
	}{
		{name: "deterministic random", payload: randomPayload},
		{name: "sparse-content shape", payload: sparsePayload},
		{name: "multi-chunk large", payload: largePayload},
	} {
		t.Run(fixture.name, func(t *testing.T) {
			direct, directCheckpoint := observeLevel2Golden(t, fixture.payload, true)
			relay, relayCheckpoint := observeLevel2Golden(t, fixture.payload, false)
			if !reflect.DeepEqual(direct, relay) {
				t.Fatalf("direct golden = %#v, relay golden = %#v", direct, relay)
			}
			if directCheckpoint.ActualRoute != RouteLevel2Direct || directCheckpoint.RouteReason != ReasonLevel2PreflightPassed ||
				relayCheckpoint.ActualRoute != RouteSFTPRelay || relayCheckpoint.DowngradedFrom != "" {
				t.Fatalf("route-specific audit evidence = direct %#v, relay %#v", directCheckpoint, relayCheckpoint)
			}
		})
	}
}

type level2GoldenObservation struct {
	Outcome      Outcome
	Bytes        uint64
	SHA256       string
	Final        domain.Location
	PartRetained bool
	Payload      string
}

func observeLevel2Golden(t *testing.T, payload []byte, direct bool) (level2GoldenObservation, Checkpoint) {
	t.Helper()
	_, plan, source, destination, sourceRoot, destinationRoot := newLevel2PlanFixtureWithPayload(t, payload, direct)
	journal := newMemoryJournal()
	resolver := MapResolver{plan.SourceEndpoint.ID: source, plan.DestinationEndpoint.ID: destination}
	worker := NewWorker(resolver, journal)
	if direct {
		worker = newLevel2FixtureWorker(resolver, journal, &level2DataFixture{sourceRoot: sourceRoot, destinationRoot: destinationRoot})
	}
	result, err := worker.Execute(context.Background(), plan, nil)
	if err != nil {
		t.Fatal(err)
	}
	finalPayload, err := os.ReadFile(filepath.Join(destinationRoot, strings.TrimPrefix(string(result.Final.Path), "/"))) // #nosec G304 -- isolated fixture path.
	if err != nil {
		t.Fatal(err)
	}
	return level2GoldenObservation{
		Outcome: result.Outcome, Bytes: result.Bytes, SHA256: result.SHA256, Final: result.Final,
		PartRetained: result.PartRetained, Payload: string(finalPayload),
	}, journal.latest()
}

func executeLevel2ConflictContract(t *testing.T, direct bool, policy ConflictPolicy) routeConflictObservation {
	t.Helper()
	sourceRoot := t.TempDir()
	destinationRoot := t.TempDir()
	if err := os.WriteFile(filepath.Join(sourceRoot, "source.bin"), []byte("direct payload"), 0o600); err != nil {
		t.Fatal(err)
	}
	source := newPlanTestProvider(t, "ep_aaaaaaaaaaaaaaaaaaaaaaaaaa", sourceRoot, domain.EndpointSSH)
	destination := newPlanTestProvider(t, "ep_bbbbbbbbbbbbbbbbbbbbbbbbbb", destinationRoot, domain.EndpointSSH)
	destination.descriptor.SSHHostAlias = "trusted-target"
	resolver := MapResolver{source.Descriptor().ID: source, destination.Descriptor().ID: destination}
	planner := NewPlanner(resolver)
	if direct {
		planner = newLevel2FixturePlanner(resolver, &level2PreflightFixture{result: passingLevel2PreflightResult})
	}
	reference, err := planner.Capture(context.Background(), normalizePlanTest(t, source, "/source.bin"))
	if err != nil {
		t.Fatal(err)
	}
	request := validFreezeRequest(reference, normalizePlanTest(t, destination, "/"))
	request.Intent.ConflictPolicy = policy
	request.Intent.ConflictConfirmed = policy == ConflictOverwrite
	if direct {
		request.Intent.DirectPolicy = DirectPolicy{UserEnabled: true, WorkspaceEnabled: true, DataAllowed: true, Integrity: IntegrityRequireStrong}
	}
	plan, _, err := planner.FreezeCopy(context.Background(), request)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(destinationRoot, "final"), []byte("commit race"), 0o600); err != nil {
		t.Fatal(err)
	}
	journal := newMemoryJournal()
	var result Result
	if direct {
		result, err = newLevel2FixtureWorker(resolver, journal, &level2DataFixture{sourceRoot: sourceRoot, destinationRoot: destinationRoot}).Execute(context.Background(), plan, nil)
	} else {
		result, err = NewWorker(resolver, journal).Execute(context.Background(), plan, nil)
	}
	if err != nil {
		t.Fatal(err)
	}
	payload, err := os.ReadFile(filepath.Join(destinationRoot, strings.TrimPrefix(string(result.Final.Path), "/"))) // #nosec G304 -- isolated fixture path.
	if err != nil {
		t.Fatal(err)
	}
	return routeConflictObservation{
		Outcome: result.Outcome, Bytes: result.Bytes, SHA256: result.SHA256, FinalPath: result.Final.Path,
		PartRetained: result.PartRetained, FinalPayload: string(payload),
	}
}

func newLevel2DirectPlanFixture(t *testing.T) (*Planner, Plan, *endpointKindProvider, *endpointKindProvider, string, string) {
	return newLevel2PlanFixtureWithPayload(t, []byte("direct payload"), true)
}

func newLevel2PlanFixtureWithPayload(t *testing.T, payload []byte, direct bool) (*Planner, Plan, *endpointKindProvider, *endpointKindProvider, string, string) {
	t.Helper()
	sourceRoot := t.TempDir()
	destinationRoot := t.TempDir()
	if err := os.WriteFile(filepath.Join(sourceRoot, "source.bin"), payload, 0o600); err != nil {
		t.Fatal(err)
	}
	source := newPlanTestProvider(t, "ep_aaaaaaaaaaaaaaaaaaaaaaaaaa", sourceRoot, domain.EndpointSSH)
	destination := newPlanTestProvider(t, "ep_bbbbbbbbbbbbbbbbbbbbbbbbbb", destinationRoot, domain.EndpointSSH)
	destination.descriptor.SSHHostAlias = "trusted-target"
	resolver := MapResolver{source.Descriptor().ID: source, destination.Descriptor().ID: destination}
	planner := NewPlanner(resolver)
	if direct {
		planner = newLevel2FixturePlanner(resolver, &level2PreflightFixture{result: func(request directprotocol.Request) (directprotocol.Result, error) {
			result, err := passingLevel2PreflightResult(request)
			digest := sha256.Sum256(payload)
			result.SourceSHA256 = hex.EncodeToString(digest[:])
			return result, err
		}})
	}
	reference, err := planner.Capture(context.Background(), normalizePlanTest(t, source, "/source.bin"))
	if err != nil {
		t.Fatal(err)
	}
	request := validFreezeRequest(reference, normalizePlanTest(t, destination, "/"))
	if direct {
		request.Intent.DirectPolicy = DirectPolicy{UserEnabled: true, WorkspaceEnabled: true, DataAllowed: true, Integrity: IntegrityRequireStrong}
	}
	plan, _, err := planner.FreezeCopy(context.Background(), request)
	if err != nil {
		t.Fatal(err)
	}
	return planner, plan, source, destination, sourceRoot, destinationRoot
}
