package transfer

import (
	"context"
	"reflect"

	"github.com/TyrantLucifer/awesome-sftp-cli/internal/domain"
	providerapi "github.com/TyrantLucifer/awesome-sftp-cli/internal/provider"
)

func (manager *Manager) executeDelete(plan Plan) (Result, error) {
	implementation, err := manager.resolver.Resolve(plan.Source.Location.EndpointID)
	if err != nil {
		return Result{}, err
	}
	mutable, ok := implementation.(providerapi.MutableProvider)
	if !ok {
		return Result{}, planError(domain.CodeUnsupported, "delete", plan.Source.Location, "delete capability has no mutation facet", domain.RetryAfterReplan)
	}
	if err := requireFrozenCapability(manager.ctx, implementation, plan.Source.Location, plan.SourceCapability); err != nil {
		return Result{}, err
	}
	if err := requireFrozenCapability(manager.ctx, implementation, plan.Source.Location, plan.DestinationCapability); err != nil {
		return Result{}, err
	}
	entry, err := implementation.Stat(manager.ctx, providerapi.StatRequest{Location: plan.Source.Location})
	if domain.IsCode(err, domain.CodeNotFound) {
		return Result{Outcome: OutcomeCompleted, Final: plan.Source.Location, Items: 1, Succeeded: 1}, nil
	}
	if err != nil {
		return Result{}, err
	}
	if entry.Kind != plan.Source.Kind || !reflect.DeepEqual(entry.Fingerprint, plan.Source.Fingerprint) {
		return Result{}, planError(domain.CodeConflict, "delete", plan.Source.Location, "delete target changed after confirmation", domain.RetryAfterConflict)
	}
	if plan.DeleteTrash {
		trash, ok := implementation.(providerapi.TrashProvider)
		if !ok || plan.DeleteCapability == nil {
			return Result{}, planError(domain.CodeUnsupported, "trash", plan.Source.Location, "frozen trash capability is unavailable", domain.RetryAfterReplan)
		}
		if err := requireFrozenCapability(manager.ctx, implementation, plan.Source.Location, *plan.DeleteCapability); err != nil {
			return Result{}, err
		}
		err := trash.Trash(manager.ctx, providerapi.TrashRequest{Location: entry.Location, Expected: &entry.Fingerprint})
		if err != nil && !operationEffectUnknown(err) {
			return Result{}, err
		}
		if _, statErr := implementation.Stat(manager.ctx, providerapi.StatRequest{Location: entry.Location}); !domain.IsCode(statErr, domain.CodeNotFound) {
			if statErr == nil {
				return Result{}, planError(domain.CodeConflict, "trash", entry.Location, "trash postcondition did not remove source identity", domain.RetryAfterConflict)
			}
			return Result{}, statErr
		}
		return Result{Outcome: OutcomeCompleted, Final: plan.Source.Location, Items: 1, Succeeded: 1}, nil
	}
	items := uint64(1)
	if entry.Kind == domain.EntryDirectory && plan.DeleteRecursive {
		items, err = deleteExplicitTree(manager.ctx, implementation, mutable, entry.Location, *plan.Discovery, 0)
	} else {
		err = removeWithPostcondition(manager.ctx, implementation, mutable, entry)
	}
	if err != nil {
		return Result{}, err
	}
	return Result{Outcome: OutcomeCompleted, Final: plan.Source.Location, Items: items, Succeeded: items}, nil
}

func deleteExplicitTree(
	ctx context.Context,
	reader providerapi.Provider,
	writer providerapi.MutableProvider,
	directory domain.Location,
	budget DiscoveryBudget,
	depth uint32,
) (uint64, error) {
	if depth >= budget.MaxDepth {
		return 0, planError(domain.CodeResourceExhausted, "recursive_delete", directory, "delete depth budget exhausted", domain.RetryAfterReplan)
	}
	var removed uint64
	for {
		page, err := reader.List(ctx, providerapi.ListRequest{Location: directory, Limit: budget.PageItems})
		if err != nil {
			return removed, err
		}
		if len(page.Entries) == 0 {
			break
		}
		for _, entry := range page.Entries {
			if _, err := validateDiscoveredEntry(directory, directory, entry); err != nil {
				return removed, err
			}
			if entry.Kind == domain.EntryDirectory {
				children, err := deleteExplicitTree(ctx, reader, writer, entry.Location, budget, depth+1)
				removed += children
				if err != nil {
					return removed, err
				}
				continue
			}
			if err := removeWithPostcondition(ctx, reader, writer, entry); err != nil {
				return removed, err
			}
			removed++
		}
	}
	current, err := reader.Stat(ctx, providerapi.StatRequest{Location: directory})
	if err != nil {
		return removed, err
	}
	if err := removeWithPostcondition(ctx, reader, writer, current); err != nil {
		return removed, err
	}
	return removed + 1, nil
}
