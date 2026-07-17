package transfer

import (
	"context"
	"errors"
	"fmt"
	"reflect"

	"github.com/TyrantLucifer/awesome-mac-sftp/internal/domain"
	providerapi "github.com/TyrantLucifer/awesome-mac-sftp/internal/provider"
)

type serverCopyProvider interface {
	ServerCopy(
		context.Context,
		domain.Location,
		domain.Location,
		domain.Fingerprint,
		uint64,
	) (domain.Entry, error)
}

func tryServerCopy(
	destinationProvider providerapi.Provider,
	sourceSnapshot domain.EndpointSnapshot,
	destinationSnapshot domain.EndpointSnapshot,
	plan *Plan,
) bool {
	if plan == nil || plan.Kind != OperationCopy || plan.Source.Kind != domain.EntryFile ||
		plan.SourceEndpoint.ID != plan.DestinationEndpoint.ID ||
		plan.SourceEndpoint.Kind != domain.EndpointSSH || plan.DestinationEndpoint.Kind != domain.EndpointSSH {
		return false
	}
	if _, ok := destinationProvider.(serverCopyProvider); !ok {
		return false
	}
	if sourceSnapshot.Capabilities.Revision != destinationSnapshot.Capabilities.Revision {
		return false
	}
	capability, ok := sourceSnapshot.Capabilities.Lookup("server_copy")
	if !ok || capability.Version != ServerCopyCapabilityVersion || plan.Source.Fingerprint.Size == nil || *plan.Source.Fingerprint.Size > MaxServerCopyBytes {
		return false
	}
	destinationCapability, ok := destinationSnapshot.Capabilities.Lookup("server_copy")
	if !ok || !reflect.DeepEqual(destinationCapability, capability) {
		return false
	}
	plan.ServerCopy = &ServerCopyBinding{
		Revision:   sourceSnapshot.Capabilities.Revision,
		Capability: capability,
		MaxBytes:   MaxServerCopyBytes,
	}
	plan.Route = RouteSFTPServerCopy

	return true
}

func validServerCopyBinding(binding ServerCopyBinding, plan Plan) bool {
	return binding.Revision != (domain.CapabilityRevision{}) && binding.Revision == plan.SourceCapability.Revision &&
		binding.Revision == plan.DestinationCapability.Revision &&
		binding.Capability.Name == "server_copy" && binding.Capability.Version == ServerCopyCapabilityVersion &&
		binding.MaxBytes == MaxServerCopyBytes && plan.Source.Fingerprint.Size != nil &&
		*plan.Source.Fingerprint.Size <= binding.MaxBytes
}

func (worker *Worker) executeServerCopy(
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
	if plan.ServerCopy == nil {
		return Result{}, planError(domain.CodeCapabilityLost, "stage_server_copy", plan.Part, "frozen server-copy capability is unavailable", domain.RetryAfterReplan)
	}
	serverCopy, ok := destinationProvider.(serverCopyProvider)
	if !ok {
		return Result{}, planError(domain.CodeCapabilityLost, "stage_server_copy", plan.Part, "server-copy facet disappeared", domain.RetryAfterReplan)
	}
	if err := requireFrozenCapability(ctx, destinationProvider, plan.Part, CapabilityBinding{
		Revision: plan.ServerCopy.Revision, Capability: plan.ServerCopy.Capability,
	}); err != nil {
		return Result{}, err
	}
	if !checkpointExists {
		if err := worker.journal.Save(ctx, *checkpoint); err != nil {
			return Result{}, fmt.Errorf("execute server copy: persist part intent: %w", err)
		}
	}
	partEntry, err := destinationProvider.Stat(ctx, providerapi.StatRequest{Location: plan.Part})
	if err == nil && !checkpointExists {
		return Result{}, planError(domain.CodeAlreadyExists, "stage_server_copy", plan.Part, "server-copy part appeared before durable staging began", domain.RetryAfterConflict)
	}
	if domain.IsCode(err, domain.CodeNotFound) {
		if action := controlAction(control, *checkpoint); action != ControlContinue {
			return controlledServerCopyResult(plan, *checkpoint, action)
		}
		returned, stageErr := serverCopy.ServerCopy(ctx, plan.Source.Location, plan.Part, cloneFingerprint(plan.Source.Fingerprint), plan.ServerCopy.MaxBytes)
		if stageErr == nil && (returned.Location != plan.Part || returned.Kind != domain.EntryFile) {
			return Result{}, planError(domain.CodeIntegrityFailed, "stage_server_copy", plan.Part, "server-copy facet returned an invalid staging result", domain.RetryNever)
		}
		partEntry, err = destinationProvider.Stat(ctx, providerapi.StatRequest{Location: plan.Part})
		if err != nil {
			if stageErr != nil {
				return Result{}, stageErr
			}
			return Result{}, err
		}
		if stageErr != nil && errors.Is(stageErr, context.Canceled) {
			return Result{}, stageErr
		}
		// Any other lost response is recoverable only through the same verification path as a successful stage.
	}
	if err != nil {
		return Result{}, err
	}
	if partEntry.Location != plan.Part || partEntry.Kind != domain.EntryFile || partEntry.Metadata.Size == nil ||
		plan.Source.Fingerprint.Size == nil || *partEntry.Metadata.Size != *plan.Source.Fingerprint.Size ||
		*partEntry.Metadata.Size > plan.ServerCopy.MaxBytes {
		return Result{}, planError(domain.CodeConflict, "adopt_server_copy_part", plan.Part, "durable server-copy part has an unexpected location, type, or size", domain.RetryAfterConflict)
	}
	sourceChecksum, err := verifyFile(ctx, source, plan.Source.Location, plan.Source.Fingerprint, buffer)
	if err != nil {
		return Result{}, err
	}
	destinationChecksum, err := verifyFile(ctx, destinationProvider, plan.Part, partEntry.Fingerprint, buffer)
	if err != nil {
		return Result{}, err
	}
	if sourceChecksum != destinationChecksum {
		return Result{}, planError(domain.CodeIntegrityFailed, "verify_server_copy_part", plan.Part, "server-copy part differs from the frozen source", domain.RetryNever)
	}
	checkpoint.Offset = *partEntry.Metadata.Size
	checkpoint.PartFingerprint = cloneFingerprint(partEntry.Fingerprint)
	checkpoint.ChecksumHex = destinationChecksum
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

func controlledServerCopyResult(plan Plan, checkpoint Checkpoint, action ControlAction) (Result, error) {
	result := Result{Final: plan.Final, Bytes: checkpoint.Offset, PartRetained: checkpoint.Offset != 0}
	if action == ControlPause {
		return result, ErrPaused
	}
	return result, ErrCanceled
}
