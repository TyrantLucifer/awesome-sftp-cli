package transfer

import (
	"context"
	"errors"
	"fmt"
	"reflect"

	"github.com/TyrantLucifer/awesome-mac-sftp/internal/domain"
	providerapi "github.com/TyrantLucifer/awesome-mac-sftp/internal/provider"
)

func (manager *Manager) executeAtomicMove(plan Plan) (Result, error) {
	implementation, err := manager.resolver.Resolve(plan.Source.Location.EndpointID)
	if err != nil {
		return Result{}, err
	}
	mutable, ok := implementation.(providerapi.MutableProvider)
	if !ok {
		return Result{}, planError(domain.CodeUnsupported, "atomic_move", plan.Source.Location, "atomic rename capability has no mutation facet", domain.RetryAfterReplan)
	}
	if err := requireFrozenCapability(manager.ctx, implementation, plan.Source.Location, *plan.MoveCapability); err != nil {
		return Result{}, err
	}
	final := plan.Final
	sourceEntry, sourceErr := implementation.Stat(manager.ctx, providerapi.StatRequest{Location: plan.Source.Location})
	if domain.IsCode(sourceErr, domain.CodeNotFound) {
		finalEntry, finalErr := implementation.Stat(manager.ctx, providerapi.StatRequest{Location: final})
		if finalErr == nil && reflect.DeepEqual(finalEntry.Fingerprint, plan.Source.Fingerprint) {
			return atomicMoveResult(plan, final), nil
		}
		return Result{}, planError(domain.CodeConflict, "atomic_move", plan.Source.Location, "source is absent and rename postcondition is unproved", domain.RetryAfterConflict)
	}
	if sourceErr != nil {
		return Result{}, sourceErr
	}
	if sourceEntry.Kind != plan.Source.Kind || !reflect.DeepEqual(sourceEntry.Fingerprint, plan.Source.Fingerprint) {
		return Result{}, planError(domain.CodeConflict, "atomic_move", plan.Source.Location, "source changed before atomic rename", domain.RetryAfterConflict)
	}
	finalEntry, finalErr := implementation.Stat(manager.ctx, providerapi.StatRequest{Location: final})
	finalExists := finalErr == nil
	if finalErr != nil && !domain.IsCode(finalErr, domain.CodeNotFound) {
		return Result{}, finalErr
	}
	if finalExists {
		switch plan.ConflictPolicy {
		case ConflictAsk:
			return Result{Outcome: OutcomeWaitingConflict, Final: final}, nil
		case ConflictSkip:
			return Result{Outcome: OutcomeSkipped, Final: final}, nil
		case ConflictAutoRename:
			final, err = chooseAutoRename(manager.ctx, implementation, plan.DestinationDirectory, plan.RequestedName)
			if err != nil {
				return Result{}, err
			}
			finalExists = false
		}
	}
	request := providerapi.RenameRequest{
		Source: plan.Source.Location, Destination: final, Replace: finalExists && plan.ConflictPolicy == ConflictOverwrite,
		ExpectedSource: &sourceEntry.Fingerprint,
	}
	if request.Replace {
		request.ExpectedDestination = &finalEntry.Fingerprint
	}
	renameResult, renameErr := mutable.Rename(manager.ctx, request)
	if renameErr != nil {
		proved, proofErr := proveAtomicMove(manager.ctx, implementation, plan, final)
		if proofErr != nil || !proved {
			return Result{}, renameErr
		}
		return atomicMoveResult(plan, final), nil
	}
	if !renameResult.Atomic {
		return Result{}, planError(domain.CodeIntegrityFailed, "atomic_move", final, "provider violated frozen atomic rename capability", domain.RetryNever)
	}
	proved, proofErr := proveAtomicMove(manager.ctx, implementation, plan, final)
	if proofErr != nil {
		return Result{}, proofErr
	}
	if !proved {
		return Result{}, planError(domain.CodeConflict, "atomic_move", final, "atomic rename postcondition is unproved", domain.RetryAfterConflict)
	}
	return atomicMoveResult(plan, final), nil
}

func proveAtomicMove(ctx context.Context, implementation providerapi.Provider, plan Plan, final domain.Location) (bool, error) {
	_, sourceErr := implementation.Stat(ctx, providerapi.StatRequest{Location: plan.Source.Location})
	if sourceErr == nil {
		return false, nil
	}
	if !domain.IsCode(sourceErr, domain.CodeNotFound) {
		return false, sourceErr
	}
	finalEntry, err := implementation.Stat(ctx, providerapi.StatRequest{Location: final})
	if err != nil {
		return false, err
	}
	return finalEntry.Kind == plan.Source.Kind && reflect.DeepEqual(finalEntry.Fingerprint, plan.Source.Fingerprint), nil
}

func atomicMoveResult(plan Plan, final domain.Location) Result {
	result := Result{Outcome: OutcomeCompleted, Final: final, Items: 1, Succeeded: 1}
	if plan.Source.Fingerprint.Size != nil {
		result.Bytes = *plan.Source.Fingerprint.Size
	}
	return result
}

func (manager *Manager) finishMove(plan Plan, result Result) (bool, string) {
	source, err := manager.resolver.Resolve(plan.Source.Location.EndpointID)
	if err != nil {
		return false, "source endpoint is unavailable for deletion"
	}
	mutable, ok := source.(providerapi.MutableProvider)
	if !ok {
		return false, "source endpoint has no deletion capability"
	}
	if plan.SourceDeleteCapability == nil {
		return false, "source deletion capability was not frozen"
	}
	if err := requireFrozenCapability(manager.ctx, source, plan.Source.Location, *plan.SourceDeleteCapability); err != nil {
		return false, safeMoveReason("source deletion capability changed", err)
	}
	if plan.Source.Kind == domain.EntryDirectory {
		if _, statErr := source.Stat(manager.ctx, providerapi.StatRequest{Location: plan.Source.Location}); domain.IsCode(statErr, domain.CodeNotFound) {
			return true, "source directory already absent after a prior delete response"
		} else if statErr != nil {
			return false, safeMoveReason("source directory revalidation failed", statErr)
		}
		destination, resolveErr := manager.resolver.Resolve(result.Final.EndpointID)
		if resolveErr != nil {
			return false, "destination endpoint is unavailable for move verification"
		}
		buffer := make([]byte, int(plan.BufferBytes))
		if verifyErr := verifyDirectoryMove(manager.ctx, source, destination, plan.Source.Location, result.Final, *plan.Discovery, buffer); verifyErr != nil {
			return false, safeMoveReason("directory verification failed", verifyErr)
		}
		if deleteErr := deleteVerifiedDirectory(manager.ctx, source, mutable, destination, plan.Source.Location, result.Final, *plan.Discovery, buffer, 0); deleteErr != nil {
			return false, safeMoveReason("directory source deletion was not proved", deleteErr)
		}
		return true, "source directory deletion proved"
	}
	entry, err := source.Stat(manager.ctx, providerapi.StatRequest{Location: plan.Source.Location})
	if domain.IsCode(err, domain.CodeNotFound) {
		return true, "source already absent after a prior delete response"
	}
	if err != nil {
		return false, safeMoveReason("source revalidation failed", err)
	}
	if entry.Kind != domain.EntryFile || !reflect.DeepEqual(entry.Fingerprint, plan.Source.Fingerprint) {
		return false, "source changed after destination commit"
	}
	if err := removeWithPostcondition(manager.ctx, source, mutable, entry); err != nil {
		return false, safeMoveReason("source deletion was not proved", err)
	}
	return true, "source file deletion proved"
}

func verifyDirectoryMove(
	ctx context.Context,
	source providerapi.Provider,
	destination providerapi.Provider,
	sourceRoot domain.Location,
	destinationRoot domain.Location,
	budget DiscoveryBudget,
	buffer []byte,
) error {
	discoveryContext, cancel := context.WithCancel(ctx)
	defer cancel()
	items, failures, err := DiscoverDirectory(discoveryContext, source, sourceRoot, budget)
	if err != nil {
		return err
	}
	for item := range items {
		destinationLocation := childLocation(destinationRoot, item.RelativePath)
		switch item.Entry.Kind {
		case domain.EntryDirectory:
			entry, statErr := destination.Stat(ctx, providerapi.StatRequest{Location: destinationLocation})
			if statErr != nil || entry.Kind != domain.EntryDirectory {
				if statErr != nil {
					return statErr
				}
				return planError(domain.CodeConflict, "verify_move", destinationLocation, "destination directory is missing or changed type", domain.RetryAfterConflict)
			}
		case domain.EntryFile:
			if err := verifyMoveFile(ctx, source, destination, item.Entry, destinationLocation, buffer); err != nil {
				return err
			}
		default:
			return planError(domain.CodeUnsupported, "verify_move", item.Entry.Location, "move retains trees containing symlinks or unsupported entries", domain.RetryAfterReplan)
		}
	}
	return <-failures
}

func verifyMoveFile(
	ctx context.Context,
	source providerapi.Provider,
	destination providerapi.Provider,
	sourceEntry domain.Entry,
	destinationLocation domain.Location,
	buffer []byte,
) error {
	destinationEntry, err := destination.Stat(ctx, providerapi.StatRequest{Location: destinationLocation})
	if err != nil {
		return err
	}
	sourceChecksum, err := verifyFile(ctx, source, sourceEntry.Location, sourceEntry.Fingerprint, buffer)
	if err != nil {
		return err
	}
	destinationChecksum, err := verifyFile(ctx, destination, destinationLocation, destinationEntry.Fingerprint, buffer)
	if err != nil {
		return err
	}
	if sourceChecksum != destinationChecksum {
		return planError(domain.CodeConflict, "verify_move", destinationLocation, "destination content does not match source", domain.RetryAfterConflict)
	}
	return nil
}

func deleteVerifiedDirectory(
	ctx context.Context,
	source providerapi.Provider,
	mutable providerapi.MutableProvider,
	destination providerapi.Provider,
	sourceDirectory domain.Location,
	destinationDirectory domain.Location,
	budget DiscoveryBudget,
	buffer []byte,
	depth uint32,
) error {
	if depth >= budget.MaxDepth {
		return planError(domain.CodeResourceExhausted, "delete_move_source", sourceDirectory, "directory delete depth budget exhausted", domain.RetryAfterReplan)
	}
	for {
		page, err := source.List(ctx, providerapi.ListRequest{Location: sourceDirectory, Limit: budget.PageItems})
		if err != nil {
			return err
		}
		if len(page.Entries) == 0 {
			break
		}
		for _, entry := range page.Entries {
			relativeName, validateErr := validateDiscoveredEntry(sourceDirectory, sourceDirectory, entry)
			if validateErr != nil {
				return validateErr
			}
			destinationLocation := childLocation(destinationDirectory, relativeName)
			switch entry.Kind {
			case domain.EntryFile:
				if err := verifyMoveFile(ctx, source, destination, entry, destinationLocation, buffer); err != nil {
					return err
				}
				if err := removeWithPostcondition(ctx, source, mutable, entry); err != nil {
					return err
				}
			case domain.EntryDirectory:
				if err := deleteVerifiedDirectory(ctx, source, mutable, destination, entry.Location, destinationLocation, budget, buffer, depth+1); err != nil {
					return err
				}
			default:
				return planError(domain.CodeUnsupported, "delete_move_source", entry.Location, "unsupported source entry appeared before delete", domain.RetryAfterReplan)
			}
		}
	}
	root, err := source.Stat(ctx, providerapi.StatRequest{Location: sourceDirectory})
	if err != nil {
		return err
	}
	return removeWithPostcondition(ctx, source, mutable, root)
}

func removeWithPostcondition(ctx context.Context, reader providerapi.Provider, writer providerapi.MutableProvider, entry domain.Entry) error {
	err := writer.Remove(ctx, providerapi.RemoveRequest{Location: entry.Location, Expected: &entry.Fingerprint})
	if err != nil {
		if !operationEffectUnknown(err) {
			return err
		}
	}
	_, statErr := reader.Stat(ctx, providerapi.StatRequest{Location: entry.Location})
	if domain.IsCode(statErr, domain.CodeNotFound) {
		return nil
	}
	if statErr != nil {
		return fmt.Errorf("prove source deletion: %w", statErr)
	}
	if err != nil {
		return err
	}
	return planError(domain.CodeConflict, "delete_move_source", entry.Location, "source still exists after delete response", domain.RetryAfterConflict)
}

func operationEffectUnknown(err error) bool {
	var operationError *domain.OpError
	return errors.As(err, &operationError) && operationError.Effect == domain.EffectUnknown
}

func safeMoveReason(prefix string, err error) string {
	var operationError *domain.OpError
	if errors.As(err, &operationError) {
		return prefix + ": " + string(operationError.Code)
	}
	return prefix
}
