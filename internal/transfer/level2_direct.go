package transfer

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"reflect"
	"sync"
	"time"

	"github.com/TyrantLucifer/awesome-mac-sftp/internal/directprotocol"
	"github.com/TyrantLucifer/awesome-mac-sftp/internal/domain"
	providerapi "github.com/TyrantLucifer/awesome-mac-sftp/internal/provider"
)

const Level2DirectFormatVersion uint16 = 1

var errLevel2SafeRelayFallback = errors.New("direct transfer may safely fall back to relay")

type Level2StageRequest struct {
	FormatVersion   uint16          `json:"format_version"`
	JobID           domain.JobID    `json:"job_id"`
	Source          domain.Location `json:"source"`
	Part            domain.Location `json:"part"`
	Final           domain.Location `json:"final"`
	TargetHostAlias string          `json:"target_host_alias"`
	Nonce           string          `json:"nonce"`
	ExpectedSize    uint64          `json:"expected_size"`
	ExpectedSHA256  string          `json:"expected_sha256"`
	ResumeOffset    uint64          `json:"resume_offset"`
	PrefixSHA256    string          `json:"prefix_sha256,omitempty"`
}

type Level2Progress struct {
	Part         domain.Location `json:"part"`
	Offset       uint64          `json:"offset"`
	PrefixSHA256 string          `json:"prefix_sha256"`
	Durable      bool            `json:"durable"`
}

type Level2StageResult struct {
	Part      domain.Location `json:"part"`
	Size      uint64          `json:"size"`
	SHA256    string          `json:"sha256"`
	Committed bool            `json:"committed"`
}

type Level2VerifyRequest struct {
	FormatVersion   uint16          `json:"format_version"`
	JobID           domain.JobID    `json:"job_id"`
	Location        domain.Location `json:"location"`
	TargetHostAlias string          `json:"target_host_alias"`
	Nonce           string          `json:"nonce"`
	ExpectedSize    uint64          `json:"expected_size"`
	ExpectedSHA256  string          `json:"expected_sha256"`
}

type Level2VerifyResult struct {
	Location domain.Location `json:"location"`
	Size     uint64          `json:"size"`
	SHA256   string          `json:"sha256"`
}

type level2DataBackend interface {
	Preflight(context.Context, directprotocol.Request) (directprotocol.Result, error)
	Stage(context.Context, Level2StageRequest, func(Level2Progress) error) (Level2StageResult, error)
	Verify(context.Context, Level2VerifyRequest) (Level2VerifyResult, error)
}

func (worker *Worker) refreshExpiredLevel2Preflight(ctx context.Context, plan Plan, now time.Time) (Plan, error) {
	if plan.Level2Preflight == nil || plan.Level2Preflight.Result == nil || worker.level2 == nil {
		return Plan{}, planError(domain.CodeCapabilityLost, "preflight_direct", plan.Part, "Level 2 fixture or evidence is unavailable", domain.RetryAfterReplan)
	}
	if time.Unix(plan.Level2Preflight.Result.ExpiresAtUnix, 0).After(now) && time.Unix(plan.Level2Preflight.Request.DeadlineUnix, 0).After(now) {
		return plan, nil
	}
	request := plan.Level2Preflight.Request
	requestID, err := domain.NewRequestID(&domain.RandomGenerator{})
	if err != nil {
		return Plan{}, planError(domain.CodeCapabilityLost, "preflight_direct", plan.Part, "fresh request correlation could not be created", domain.RetryBackoff)
	}
	nonceBytes := make([]byte, 16)
	if _, err := rand.Read(nonceBytes); err != nil {
		return Plan{}, planError(domain.CodeCapabilityLost, "preflight_direct", plan.Part, "fresh request nonce could not be created", domain.RetryBackoff)
	}
	request.RequestID = requestID
	request.DeadlineUnix = now.Add(directprotocol.MaxRequestDuration / 2).Unix()
	request.Nonce = hex.EncodeToString(nonceBytes)
	result, err := worker.level2.Preflight(ctx, request)
	if err != nil {
		return Plan{}, planError(domain.CodeCapabilityLost, "preflight_direct", plan.Part, "fresh Level 2 preflight was unavailable", domain.RetryAfterReplan)
	}
	passed, _, err := directprotocol.Evaluate(request, result, now)
	if err != nil || !passed {
		return Plan{}, planError(domain.CodeCapabilityLost, "preflight_direct", plan.Part, "fresh Level 2 preflight did not pass", domain.RetryAfterReplan)
	}
	binding := *plan.Level2Preflight
	binding.Request = request
	binding.Result = &result
	binding.SourceSize = result.SourceSize
	binding.SourceSHA256 = result.SourceSHA256
	binding.Outcome = Level2PreflightPassed
	binding.FirstCheck = directprotocol.Check{}
	plan.Level2Preflight = &binding
	return plan, nil
}

func (worker *Worker) executeLevel2Direct(
	ctx context.Context,
	plan Plan,
	source providerapi.Provider,
	destinationProvider providerapi.Provider,
	destination providerapi.MutableProvider,
	checkpoint *Checkpoint,
	checkpointExists bool,
	control Control,
	buffer []byte,
) (Result, error) {
	if worker.level2 == nil || plan.Level2Preflight == nil || plan.Level2Preflight.Result == nil {
		return Result{}, planError(domain.CodeCapabilityLost, "stage_direct", plan.Part, "Level 2 data-plane fixture is not attached", domain.RetryAfterReplan)
	}
	expectedSize := plan.Level2Preflight.Result.SourceSize
	expectedSHA := plan.Level2Preflight.Result.SourceSHA256
	if !checkpointExists {
		if err := worker.journal.Save(ctx, *checkpoint); err != nil {
			return Result{}, fmt.Errorf("execute direct: persist part intent: %w", err)
		}
	}
	partEntry, partErr := destinationProvider.Stat(ctx, providerapi.StatRequest{Location: plan.Part})
	if partErr == nil {
		if !checkpointExists || checkpoint.Offset == 0 || partEntry.Kind != domain.EntryFile || partEntry.Metadata.Size == nil ||
			*partEntry.Metadata.Size != checkpoint.Offset || !reflect.DeepEqual(partEntry.Fingerprint, checkpoint.PartFingerprint) {
			return Result{}, planError(domain.CodeConflict, "adopt_direct_part", plan.Part, "direct part does not match its durable acknowledged checkpoint", domain.RetryAfterConflict)
		}
	} else if !domain.IsCode(partErr, domain.CodeNotFound) {
		return Result{}, partErr
	} else if checkpoint.Offset != 0 {
		return Result{}, planError(domain.CodeConflict, "adopt_direct_part", plan.Part, "acknowledged direct part is absent", domain.RetryAfterConflict)
	}
	if action := controlAction(control, *checkpoint); action != ControlContinue {
		return controlledSameHostResult(plan, *checkpoint, action)
	}

	stageCtx, cancelStage := context.WithCancel(ctx)
	stageDone := make(chan struct{})
	controlled := make(chan ControlAction, 1)
	if control != nil {
		go monitorStagedCopyControl(stageCtx, control, *checkpoint, cancelStage, controlled, stageDone)
	}
	var progressMu sync.Mutex
	progressCheckpoint := cloneCheckpoint(*checkpoint)
	result, stageErr := worker.level2.Stage(stageCtx, Level2StageRequest{
		FormatVersion: Level2DirectFormatVersion, JobID: plan.JobID, Source: plan.Source.Location, Part: plan.Part, Final: plan.Final,
		TargetHostAlias: plan.DestinationEndpoint.SSHHostAlias, Nonce: plan.Level2Preflight.Request.Nonce,
		ExpectedSize: expectedSize, ExpectedSHA256: expectedSHA, ResumeOffset: checkpoint.Offset, PrefixSHA256: checkpoint.ChecksumHex,
	}, func(progress Level2Progress) error {
		progressMu.Lock()
		defer progressMu.Unlock()
		if !progress.Durable || progress.Part != plan.Part || progress.Offset <= progressCheckpoint.Offset || progress.Offset > expectedSize ||
			validateLowerHexIdentity(progress.PrefixSHA256, 64) != nil {
			return errors.New("execute direct: invalid target progress acknowledgement")
		}
		entry, err := destinationProvider.Stat(ctx, providerapi.StatRequest{Location: plan.Part})
		if err != nil {
			return err
		}
		if entry.Kind != domain.EntryFile || entry.Metadata.Size == nil || *entry.Metadata.Size != progress.Offset {
			return errors.New("execute direct: target acknowledgement does not match durable part size")
		}
		progressCheckpoint.Phase = PhaseStreaming
		progressCheckpoint.Offset = progress.Offset
		progressCheckpoint.ChecksumHex = progress.PrefixSHA256
		progressCheckpoint.PartFingerprint = cloneFingerprint(entry.Fingerprint)
		progressCheckpoint.DirectFormatVersion = Level2DirectFormatVersion
		progressCheckpoint.DirectNonce = plan.Level2Preflight.Request.Nonce
		return worker.journal.Save(ctx, progressCheckpoint)
	})
	close(stageDone)
	cancelStage()
	select {
	case action := <-controlled:
		return controlledSameHostResult(plan, progressCheckpoint, action)
	default:
	}
	if action := controlAction(control, progressCheckpoint); action != ControlContinue {
		return controlledSameHostResult(plan, progressCheckpoint, action)
	}
	if stageErr != nil {
		if progressCheckpoint.Offset == 0 && ctx.Err() == nil {
			_, statErr := destinationProvider.Stat(ctx, providerapi.StatRequest{Location: plan.Part})
			if domain.IsCode(statErr, domain.CodeNotFound) {
				*checkpoint = progressCheckpoint
				return Result{}, errLevel2SafeRelayFallback
			}
		}
		return Result{Final: plan.Final, Bytes: progressCheckpoint.Offset, PartRetained: progressCheckpoint.Offset > 0}, stageErr
	}
	if result.Part != plan.Part || result.Size != expectedSize || result.SHA256 != expectedSHA || result.Committed {
		return Result{}, planError(domain.CodeIntegrityFailed, "stage_direct", plan.Part, "direct result violates the frozen Plan", domain.RetryNever)
	}
	if expectedSize > 0 && progressCheckpoint.Offset != expectedSize {
		return Result{}, planError(domain.CodeIntegrityFailed, "stage_direct", plan.Part, "direct completion lacks a durable final target acknowledgement", domain.RetryNever)
	}
	partEntry, err := destinationProvider.Stat(ctx, providerapi.StatRequest{Location: plan.Part})
	if err != nil {
		return Result{}, err
	}
	if partEntry.Kind != domain.EntryFile || partEntry.Metadata.Size == nil || *partEntry.Metadata.Size != expectedSize {
		return Result{}, planError(domain.CodeConflict, "verify_direct_part", plan.Part, "direct part has an unexpected type or size", domain.RetryAfterConflict)
	}
	checksum, err := worker.verifyLevel2(ctx, plan, plan.Part, expectedSize, expectedSHA)
	if err != nil {
		return Result{}, err
	}
	currentSource, err := source.Stat(ctx, providerapi.StatRequest{Location: plan.Source.Location})
	if err != nil {
		return Result{}, err
	}
	if currentSource.Kind != plan.Source.Kind || !reflect.DeepEqual(currentSource.Fingerprint, plan.Source.Fingerprint) {
		return Result{}, planError(domain.CodeConflict, "verify_direct_source", plan.Source.Location, "source changed during direct transfer", domain.RetryAfterConflict)
	}
	progressCheckpoint.Offset = expectedSize
	progressCheckpoint.ChecksumHex = checksum
	progressCheckpoint.PartFingerprint = cloneFingerprint(partEntry.Fingerprint)
	progressCheckpoint.Phase = PhaseTransferred
	if err := worker.journal.Save(ctx, progressCheckpoint); err != nil {
		return Result{}, err
	}
	progressCheckpoint.Phase = PhaseVerified
	if err := worker.journal.Save(ctx, progressCheckpoint); err != nil {
		return Result{}, err
	}
	*checkpoint = progressCheckpoint
	return worker.commit(ctx, plan, destinationProvider, destination, progressCheckpoint, buffer)
}

func (worker *Worker) verifyLevel2(ctx context.Context, plan Plan, location domain.Location, expectedSize uint64, expectedSHA string) (string, error) {
	if worker.level2 == nil || plan.Level2Preflight == nil {
		return "", errors.New("verify direct: fixture is not attached")
	}
	result, err := worker.level2.Verify(ctx, Level2VerifyRequest{
		FormatVersion: Level2DirectFormatVersion, JobID: plan.JobID, Location: location,
		TargetHostAlias: plan.DestinationEndpoint.SSHHostAlias, Nonce: plan.Level2Preflight.Request.Nonce,
		ExpectedSize: expectedSize, ExpectedSHA256: expectedSHA,
	})
	if err != nil {
		return "", err
	}
	if result.Location != location || result.Size != expectedSize || result.SHA256 != expectedSHA {
		return "", planError(domain.CodeIntegrityFailed, "verify_direct", location, "remote strong-hash result violates the frozen Plan", domain.RetryNever)
	}
	return result.SHA256, nil
}
