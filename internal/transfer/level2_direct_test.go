package transfer

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/TyrantLucifer/awesome-mac-sftp/internal/directprotocol"
	"github.com/TyrantLucifer/awesome-mac-sftp/internal/domain"
	providerapi "github.com/TyrantLucifer/awesome-mac-sftp/internal/provider"
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
}

type blockingLevel2DataFixture struct {
	*level2DataFixture
	started chan struct{}
}

type failingBeforeWriteLevel2Fixture struct{ *level2DataFixture }

func (fixture *failingBeforeWriteLevel2Fixture) Stage(context.Context, Level2StageRequest, func(Level2Progress) error) (Level2StageResult, error) {
	fixture.stageCalls++
	return Level2StageResult{}, errors.New("direct network unavailable before write")
}

type lostStageResponseLevel2Fixture struct{ *level2DataFixture }

func (fixture *lostStageResponseLevel2Fixture) Stage(ctx context.Context, request Level2StageRequest, acknowledge func(Level2Progress) error) (Level2StageResult, error) {
	result, err := fixture.level2DataFixture.Stage(ctx, request, acknowledge)
	if err != nil {
		return result, err
	}
	return Level2StageResult{}, errors.New("direct completion response lost")
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

func (fixture *level2DataFixture) Preflight(_ context.Context, request directprotocol.Request) (directprotocol.Result, error) {
	fixture.preflightCalls++
	result, err := passingLevel2PreflightResult(request)
	if fixture.preflightStatus != "" && fixture.preflightStatus != directprotocol.Pass {
		result.Checks[0].Status = fixture.preflightStatus
		result.Checks[0].Reason = "protocol_" + string(fixture.preflightStatus)
	}
	return result, err
}

func (fixture *level2DataFixture) Stage(ctx context.Context, request Level2StageRequest, acknowledge func(Level2Progress) error) (Level2StageResult, error) {
	fixture.stageCalls++
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
	buffer := make([]byte, 7)
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

func TestLevel2ExpiredPreflightIsFreshlyRevalidatedBeforeAnyTargetWrite(t *testing.T) {
	for _, status := range []directprotocol.Status{directprotocol.Pass, directprotocol.Fail, directprotocol.Unknown} {
		t.Run(string(status), func(t *testing.T) {
			_, plan, source, destination, sourceRoot, destinationRoot := newLevel2DirectPlanFixture(t)
			frozenAt := time.Now().Add(-time.Hour).UTC().Truncate(time.Second)
			plan.FrozenAt = frozenAt
			plan.Level2Preflight.Request.DeadlineUnix = frozenAt.Add(5 * time.Minute).Unix()
			plan.Level2Preflight.Result.CheckedAtUnix = frozenAt.Unix()
			plan.Level2Preflight.Result.ExpiresAtUnix = frozenAt.Add(time.Minute).Unix()
			backend := &level2DataFixture{
				sourceRoot: sourceRoot, destinationRoot: destinationRoot, preflightStatus: status,
			}
			resolver := MapResolver{plan.SourceEndpoint.ID: source, plan.DestinationEndpoint.ID: destination}
			result, err := newLevel2FixtureWorker(resolver, newMemoryJournal(), backend).Execute(context.Background(), plan, nil)
			if backend.preflightCalls != 1 {
				t.Fatalf("fresh preflight calls = %d, want 1", backend.preflightCalls)
			}
			if status == directprotocol.Pass {
				if err != nil || result.Outcome != OutcomeCompleted || backend.stageCalls != 1 {
					t.Fatalf("fresh pass result = (%#v, %v), stage calls %d", result, err, backend.stageCalls)
				}
				return
			}
			if err == nil || backend.stageCalls != 0 {
				t.Fatalf("fresh %s result = (%#v, %v), stage calls %d", status, result, err, backend.stageCalls)
			}
			if _, statErr := os.Stat(backend.localPath(plan.Part)); !errors.Is(statErr, os.ErrNotExist) {
				t.Fatalf("preflight %s created a direct part: %v", status, statErr)
			}
		})
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
	var controlCalls atomic.Uint32
	result, err := newLevel2FixtureWorker(resolver, journal, backend).Execute(context.Background(), plan, ControlFunc(func(Checkpoint) ControlAction {
		if controlCalls.Add(1) > 1 {
			return ControlCancel
		}
		return ControlContinue
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

func newLevel2DirectPlanFixture(t *testing.T) (*Planner, Plan, *endpointKindProvider, *endpointKindProvider, string, string) {
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
	planner := newLevel2FixturePlanner(resolver, &level2PreflightFixture{result: passingLevel2PreflightResult})
	reference, err := planner.Capture(context.Background(), normalizePlanTest(t, source, "/source.bin"))
	if err != nil {
		t.Fatal(err)
	}
	request := validFreezeRequest(reference, normalizePlanTest(t, destination, "/"))
	request.Intent.DirectPolicy = DirectPolicy{UserEnabled: true, WorkspaceEnabled: true, DataAllowed: true, Integrity: IntegrityRequireStrong}
	plan, _, err := planner.FreezeCopy(context.Background(), request)
	if err != nil {
		t.Fatal(err)
	}
	return planner, plan, source, destination, sourceRoot, destinationRoot
}
