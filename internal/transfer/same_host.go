package transfer

import (
	"context"
	"fmt"
	"time"

	"github.com/TyrantLucifer/awesome-mac-sftp/internal/domain"
	providerapi "github.com/TyrantLucifer/awesome-mac-sftp/internal/provider"
)

const MaxSameHostCopyBytes uint64 = 2 << 30

// SameHostCopyBackend is an optional Level 1 staging facet. It may create only
// the frozen Part path. The transfer Worker remains the sole owner of verify,
// conflict handling, final commit, restart recovery, and Job transitions.
type SameHostCopyBackend interface {
	PrepareCopy(context.Context, SameHostCopyPrepareRequest) (SameHostCopyBinding, error)
	StageCopy(context.Context, SameHostCopyStageRequest) (SameHostCopyStageResult, error)
}

type SameHostCopyPrepareRequest struct {
	Source              domain.Location
	Part                domain.Location
	Final               domain.Location
	ExpectedFingerprint domain.Fingerprint
	MaxBytes            uint64
}

type SameHostSourceIdentity struct {
	Size           uint64 `json:"size"`
	Mode           uint32 `json:"mode"`
	ModifiedUnixNS int64  `json:"modified_unix_ns"`
	FileID         string `json:"file_id,omitempty"`
}

type SameHostCopyBinding struct {
	EndpointID        domain.EndpointID       `json:"endpoint_id"`
	ArtifactID        domain.HelperArtifactID `json:"artifact_id"`
	Protocol          uint16                  `json:"protocol"`
	HelperVersion     string                  `json:"helper_version"`
	CapabilityVersion uint16                  `json:"capability_version"`
	SourceSHA256      string                  `json:"source_sha256"`
	SourceSize        uint64                  `json:"source_size"`
	SourceIdentity    SameHostSourceIdentity  `json:"source_identity"`
}

type SameHostCopyStageRequest struct {
	Source   domain.Location
	Part     domain.Location
	Final    domain.Location
	JobID    domain.JobID
	Binding  SameHostCopyBinding
	MaxBytes uint64
}

type SameHostCopyStageResult struct {
	Part      domain.Location
	Size      uint64
	SHA256    string
	Committed bool
}

func validSameHostCopyBinding(binding SameHostCopyBinding, endpointID domain.EndpointID, expected domain.Fingerprint) bool {
	if binding.EndpointID == "" || binding.EndpointID != endpointID || binding.ArtifactID.ProtocolMajor == 0 || binding.ArtifactID.Version == "" ||
		binding.ArtifactID.OS == "" || binding.ArtifactID.Arch == "" || validateLowerHexIdentity(binding.ArtifactID.SHA256, 64) != nil ||
		binding.Protocol == 0 || binding.HelperVersion == "" || len(binding.HelperVersion) > 64 ||
		binding.CapabilityVersion == 0 || validateLowerHexIdentity(binding.SourceSHA256, 64) != nil ||
		binding.SourceSize > MaxSameHostCopyBytes || binding.SourceIdentity.Size != binding.SourceSize {
		return false
	}
	if expected.Size == nil || expected.ModifiedAt == nil || expected.ModifiedPrecision == nil || *expected.Size != binding.SourceSize {
		return false
	}
	observed := time.Unix(0, binding.SourceIdentity.ModifiedUnixNS).UTC()
	want := expected.ModifiedAt.UTC()
	switch *expected.ModifiedPrecision {
	case "second":
		if !observed.Truncate(time.Second).Equal(want.Truncate(time.Second)) {
			return false
		}
	case "nanosecond":
		if !observed.Equal(want) {
			return false
		}
	default:
		return false
	}
	if expected.FileID != nil && *expected.FileID != binding.SourceIdentity.FileID || expected.VersionID != nil && *expected.VersionID != "" {
		return false
	}
	if expected.HashAlgorithm != nil || expected.HashHex != nil {
		if expected.HashAlgorithm == nil || *expected.HashAlgorithm != "sha256" || expected.HashHex == nil || *expected.HashHex != binding.SourceSHA256 {
			return false
		}
	}
	return true
}

func (planner *Planner) trySameHostCopy(ctx context.Context, plan *Plan) {
	if planner.sameHost == nil || plan == nil || plan.Kind != OperationCopy || plan.Source.Kind != domain.EntryFile ||
		plan.SourceEndpoint.ID != plan.DestinationEndpoint.ID || plan.SourceEndpoint.Kind != domain.EndpointSSH ||
		plan.DestinationEndpoint.Kind != domain.EndpointSSH {
		return
	}
	binding, err := planner.sameHost.PrepareCopy(ctx, SameHostCopyPrepareRequest{
		Source: plan.Source.Location, Part: plan.Part, Final: plan.Final,
		ExpectedFingerprint: cloneFingerprint(plan.Source.Fingerprint), MaxBytes: MaxSameHostCopyBytes,
	})
	if err != nil || !validSameHostCopyBinding(binding, plan.SourceEndpoint.ID, plan.Source.Fingerprint) {
		return
	}
	plan.Route = RouteHelperSameHost
	owned := binding
	plan.SameHostCopy = &owned
}

func (worker *Worker) executeSameHostCopy(
	ctx context.Context,
	plan Plan,
	destinationProvider providerapi.Provider,
	destination providerapi.MutableProvider,
	checkpoint *Checkpoint,
	checkpointExists bool,
	control Control,
	buffer []byte,
) (Result, error) {
	if worker.sameHost == nil || plan.SameHostCopy == nil {
		return Result{}, planError(domain.CodeCapabilityLost, "stage_same_host_copy", plan.Part, "frozen Helper same-host capability is unavailable", domain.RetryAfterReplan)
	}
	if !checkpointExists {
		if err := worker.journal.Save(ctx, *checkpoint); err != nil {
			return Result{}, fmt.Errorf("execute same-host copy: persist part intent: %w", err)
		}
	}
	partEntry, err := destinationProvider.Stat(ctx, providerapi.StatRequest{Location: plan.Part})
	if err == nil && !checkpointExists {
		return Result{}, planError(domain.CodeAlreadyExists, "stage_same_host_copy", plan.Part, "Helper part appeared before durable staging began", domain.RetryAfterConflict)
	}
	if domain.IsCode(err, domain.CodeNotFound) {
		if action := controlAction(control, *checkpoint); action != ControlContinue {
			return controlledSameHostResult(plan, *checkpoint, action)
		}
		stageCtx, cancelStage := context.WithCancel(ctx)
		stageDone := make(chan struct{})
		controlled := make(chan ControlAction, 1)
		if control != nil {
			go monitorSameHostControl(stageCtx, control, *checkpoint, cancelStage, controlled, stageDone)
		}
		result, stageErr := worker.sameHost.StageCopy(stageCtx, SameHostCopyStageRequest{
			Source: plan.Source.Location, Part: plan.Part, Final: plan.Final, JobID: plan.JobID,
			Binding: *plan.SameHostCopy, MaxBytes: MaxSameHostCopyBytes,
		})
		close(stageDone)
		cancelStage()
		select {
		case action := <-controlled:
			return controlledSameHostResult(plan, *checkpoint, action)
		default:
		}
		if action := controlAction(control, *checkpoint); action != ControlContinue {
			return controlledSameHostResult(plan, *checkpoint, action)
		}
		if stageErr != nil {
			return Result{}, stageErr
		}
		if result.Part != plan.Part || result.Size != plan.SameHostCopy.SourceSize ||
			result.SHA256 != plan.SameHostCopy.SourceSHA256 || result.Committed {
			return Result{}, planError(domain.CodeIntegrityFailed, "stage_same_host_copy", plan.Part, "Helper returned an invalid staging result", domain.RetryNever)
		}
		partEntry, err = destinationProvider.Stat(ctx, providerapi.StatRequest{Location: plan.Part})
	}
	if err != nil {
		return Result{}, err
	}
	if partEntry.Kind != domain.EntryFile || partEntry.Metadata.Size == nil || *partEntry.Metadata.Size != plan.SameHostCopy.SourceSize {
		return Result{}, planError(domain.CodeConflict, "adopt_same_host_part", plan.Part, "durable Helper part has an unexpected type or size", domain.RetryAfterConflict)
	}
	checksum, err := verifyFile(ctx, destinationProvider, plan.Part, partEntry.Fingerprint, buffer)
	if err != nil {
		return Result{}, err
	}
	if checksum != plan.SameHostCopy.SourceSHA256 {
		return Result{}, planError(domain.CodeConflict, "adopt_same_host_part", plan.Part, "durable Helper part differs from the frozen source digest", domain.RetryAfterConflict)
	}
	checkpoint.Offset = plan.SameHostCopy.SourceSize
	checkpoint.PartFingerprint = cloneFingerprint(partEntry.Fingerprint)
	checkpoint.ChecksumHex = checksum
	checkpoint.Phase = PhaseTransferred
	if err := worker.journal.Save(ctx, *checkpoint); err != nil {
		return Result{}, err
	}
	checkpoint.Phase = PhaseVerified
	if err := worker.journal.Save(ctx, *checkpoint); err != nil {
		return Result{}, err
	}
	return worker.commit(ctx, plan, destinationProvider, destination, *checkpoint, buffer)
}

func controlAction(control Control, checkpoint Checkpoint) ControlAction {
	if control == nil {
		return ControlContinue
	}
	return control.Action(cloneCheckpoint(checkpoint))
}

func monitorSameHostControl(ctx context.Context, control Control, checkpoint Checkpoint, cancel context.CancelFunc, result chan<- ControlAction, done <-chan struct{}) {
	ticker := time.NewTicker(25 * time.Millisecond)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-done:
			return
		case <-ticker.C:
			action := controlAction(control, checkpoint)
			if action != ControlContinue {
				select {
				case result <- action:
				default:
				}
				cancel()
				return
			}
		}
	}
}

func controlledSameHostResult(plan Plan, checkpoint Checkpoint, action ControlAction) (Result, error) {
	result := Result{Final: plan.Final, Bytes: checkpoint.Offset, PartRetained: true}
	if action == ControlPause {
		return result, ErrPaused
	}
	return result, ErrCanceled
}
