package transfer

import (
	"context"
	"errors"
	"fmt"
	"path"
	"reflect"
	"strings"
	"sync"

	"github.com/TyrantLucifer/awesome-mac-sftp/internal/domain"
	providerapi "github.com/TyrantLucifer/awesome-mac-sftp/internal/provider"
)

// DiscoveryBudget freezes the memory and recursion limits for one directory
// plan. PageItems bounds Provider-owned listing pages; QueueItems bounds the
// handoff to execution; MaxDepth prevents an adversarial tree from growing the
// traversal stack without limit.
type DiscoveryBudget struct {
	QueueItems uint32 `json:"queue_items"`
	PageItems  uint32 `json:"page_items"`
	MaxDepth   uint32 `json:"max_depth"`
}

var DefaultDiscoveryBudget = DiscoveryBudget{QueueItems: 64, PageItems: 256, MaxDepth: 128}

const maximumManifestItems = 256

type DiscoveredItem struct {
	Entry        domain.Entry `json:"entry"`
	RelativePath string       `json:"relative_path"`
	Depth        uint32       `json:"depth"`
}

// DiscoverDirectory streams a conservative, no-follow walk. The returned
// failure channel produces exactly one value after items closes. Callers that
// stop consuming must cancel ctx so the bounded producer can terminate.
func DiscoverDirectory(
	ctx context.Context,
	implementation providerapi.Provider,
	root domain.Location,
	budget DiscoveryBudget,
) (<-chan DiscoveredItem, <-chan error, error) {
	if implementation == nil {
		return nil, nil, errors.New("discover directory: provider is required")
	}
	if budget.QueueItems < 1 || budget.QueueItems > 4096 ||
		budget.PageItems < 1 || budget.PageItems > 4096 ||
		budget.MaxDepth < 1 || budget.MaxDepth > 256 {
		return nil, nil, errors.New("discover directory: budget is outside queue/page/depth limits")
	}
	if root.EndpointID == "" || root.EndpointID != implementation.Descriptor().ID || root.Path == "" {
		return nil, nil, errors.New("discover directory: root does not belong to provider")
	}
	entry, err := implementation.Stat(ctx, providerapi.StatRequest{Location: root})
	if err != nil {
		return nil, nil, err
	}
	if entry.Kind != domain.EntryDirectory {
		return nil, nil, planError(domain.CodeInvalidArgument, "discover_directory", root, "source is not a directory", domain.RetryNever)
	}

	items := make(chan DiscoveredItem, int(budget.QueueItems))
	failures := make(chan error, 1)
	go func() {
		defer close(items)
		failure := walkDirectory(ctx, implementation, root, root, 0, budget, items)
		failures <- failure
		close(failures)
	}()
	return items, failures, nil
}

func walkDirectory(
	ctx context.Context,
	implementation providerapi.Provider,
	root domain.Location,
	directory domain.Location,
	depth uint32,
	budget DiscoveryBudget,
	items chan<- DiscoveredItem,
) error {
	var cursor providerapi.PageCursor
	for {
		page, err := implementation.List(ctx, providerapi.ListRequest{
			Location: directory,
			Cursor:   cursor,
			Limit:    budget.PageItems,
		})
		if err != nil {
			return err
		}
		if len(page.Entries) > int(budget.PageItems) || page.Done != (page.NextCursor == "") {
			return planError(domain.CodeIntegrityFailed, "discover_directory", directory, "provider returned an invalid listing page", domain.RetryNever)
		}
		for _, entry := range page.Entries {
			relative, err := validateDiscoveredEntry(root, directory, entry)
			if err != nil {
				return err
			}
			itemDepth := depth + 1
			select {
			case items <- DiscoveredItem{Entry: entry, RelativePath: relative, Depth: itemDepth}:
			case <-ctx.Done():
				return ctx.Err()
			}
			if entry.Kind != domain.EntryDirectory {
				continue
			}
			if itemDepth >= budget.MaxDepth {
				return planError(domain.CodeResourceExhausted, "discover_directory", entry.Location, "directory depth budget exhausted", domain.RetryAfterReplan)
			}
			if err := walkDirectory(ctx, implementation, root, entry.Location, itemDepth, budget, items); err != nil {
				return err
			}
		}
		if page.Done {
			return nil
		}
		cursor = page.NextCursor
	}
}

func validateDiscoveredEntry(root, directory domain.Location, entry domain.Entry) (string, error) {
	if entry.Location.EndpointID != root.EndpointID || entry.Location.Path == "" ||
		entry.Name == "" || entry.Name == "." || entry.Name == ".." || path.Base(entry.Name) != entry.Name ||
		path.Dir(string(entry.Location.Path)) != string(directory.Path) || path.Base(string(entry.Location.Path)) != entry.Name {
		return "", planError(domain.CodeIntegrityFailed, "discover_directory", directory, "provider returned an entry outside the listed directory", domain.RetryNever)
	}
	rootPath := strings.TrimSuffix(string(root.Path), "/")
	prefix := rootPath + "/"
	if rootPath == "" {
		prefix = "/"
	}
	relative := strings.TrimPrefix(string(entry.Location.Path), prefix)
	if relative == string(entry.Location.Path) || relative == "" || relative == "." || relative == ".." || strings.HasPrefix(relative, "../") || path.IsAbs(relative) {
		return "", planError(domain.CodeIntegrityFailed, "discover_directory", entry.Location, "entry escapes the frozen source root", domain.RetryNever)
	}
	return relative, nil
}

func (worker *Worker) executeDirectory(ctx context.Context, plan Plan, control Control) (Result, error) {
	source, err := worker.resolver.Resolve(plan.Source.Location.EndpointID)
	if err != nil {
		return Result{}, err
	}
	destinationProvider, err := worker.resolver.Resolve(plan.DestinationDirectory.EndpointID)
	if err != nil {
		return Result{}, err
	}
	destination, ok := destinationProvider.(providerapi.MutableProvider)
	if !ok {
		return Result{}, planError(domain.CodeUnsupported, "execute_directory", plan.Final, "destination mutation facet disappeared", domain.RetryAfterReplan)
	}
	if err := requireFrozenCapability(ctx, source, plan.Source.Location, plan.SourceCapability); err != nil {
		return Result{}, err
	}
	if err := requireFrozenCapability(ctx, destinationProvider, plan.Final, plan.DestinationCapability); err != nil {
		return Result{}, err
	}
	currentSource, err := source.Stat(ctx, providerapi.StatRequest{Location: plan.Source.Location})
	if err != nil {
		return Result{}, err
	}
	if currentSource.Kind != domain.EntryDirectory || !reflect.DeepEqual(currentSource.Fingerprint, plan.Source.Fingerprint) {
		return Result{}, planError(domain.CodeConflict, "execute_directory", plan.Source.Location, "frozen directory root changed", domain.RetryAfterConflict)
	}

	stored, err := worker.journal.Load(ctx, plan.JobID)
	if err != nil {
		return Result{}, fmt.Errorf("execute directory: load checkpoint: %w", err)
	}
	checkpoint := Checkpoint{
		JobID:             plan.JobID,
		Phase:             PhasePrepared,
		SourceFingerprint: cloneFingerprint(plan.Source.Fingerprint),
		Part:              plan.Part,
		Final:             plan.Final,
	}
	if stored == nil {
		if err := worker.journal.Save(ctx, checkpoint); err != nil {
			return Result{}, fmt.Errorf("execute directory: persist root intent: %w", err)
		}
	} else {
		checkpoint = cloneCheckpoint(*stored)
		if checkpoint.JobID != plan.JobID || !reflect.DeepEqual(checkpoint.SourceFingerprint, plan.Source.Fingerprint) {
			return Result{}, errors.New("execute directory: checkpoint does not match frozen plan")
		}
		if checkpoint.Phase == PhaseCommitted {
			return Result{Outcome: checkpoint.Outcome, Final: checkpoint.Final, Bytes: checkpoint.Offset, Items: checkpoint.Items}, nil
		}
	}
	resuming := stored != nil
	final := checkpoint.Final
	if checkpoint.DirectoryRootOwned || plan.ConflictPolicy == ConflictOverwrite {
		if err := ensureDirectory(ctx, destinationProvider, destination, final); err != nil {
			return Result{}, err
		}
	} else {
		var outcome Outcome
		var owned bool
		final, outcome, owned, err = prepareDirectoryRoot(ctx, destinationProvider, destination, plan)
		if err != nil {
			return Result{}, err
		}
		checkpoint.Final = final
		checkpoint.DirectoryRootOwned = owned
		if outcome != "" {
			checkpoint.Phase = PhaseWaitingConflict
			checkpoint.Outcome = outcome
			_ = worker.journal.Save(ctx, checkpoint)
			return Result{Outcome: outcome, Final: final}, nil
		}
	}
	checkpoint.Phase = PhaseStreaming
	checkpoint.Outcome = ""
	if err := worker.journal.Save(ctx, checkpoint); err != nil {
		return Result{}, err
	}
	discoveryContext, cancelDiscovery := context.WithCancel(ctx)
	defer cancelDiscovery()
	items, failures, err := DiscoverDirectory(discoveryContext, source, plan.Source.Location, *plan.Discovery)
	if err != nil {
		return Result{}, err
	}
	if observer, ok := worker.journal.(bufferObserver); ok {
		bufferCeiling := int(plan.BufferBytes)
		if resuming && checkpoint.DirectoryRootOwned {
			bufferCeiling *= 2
		}
		observer.ObserveBuffer(bufferCeiling)
	}
	var result Result
	result.Outcome = OutcomeCompleted
	result.Final = final
	var ordinal uint64
	var validationBuffer []byte
	if resuming && checkpoint.DirectoryRootOwned {
		validationBuffer = make([]byte, int(plan.BufferBytes))
	}
	for item := range items {
		ordinal++
		destinationLocation := childLocation(final, item.RelativePath)
		if resuming && checkpoint.DirectoryRootOwned {
			completed, bytes, err := validateOwnedDirectoryItem(ctx, source, destinationProvider, item, destinationLocation, validationBuffer)
			if err != nil {
				return Result{}, err
			}
			if completed {
				result.Items++
				result.Succeeded++
				result.Bytes += bytes
				appendItemResult(&result, ItemResult{RelativePath: item.RelativePath, Source: item.Entry.Location, Destination: destinationLocation, Status: ItemSucceeded, Bytes: bytes})
				checkpoint.Items = result.Items
				checkpoint.Offset = result.Bytes
				checkpoint.CurrentPath = item.RelativePath
				if err := worker.journal.Save(ctx, checkpoint); err != nil {
					return Result{}, err
				}
				continue
			}
		}
		if control != nil {
			switch control.Action(cloneCheckpoint(checkpoint)) {
			case ControlPause:
				_ = worker.journal.Save(ctx, checkpoint)
				return result, ErrPaused
			case ControlCancel:
				_ = worker.journal.Save(ctx, checkpoint)
				return result, ErrCanceled
			}
		}
		checkpoint.CurrentPath = item.RelativePath
		itemResultRecord := ItemResult{RelativePath: item.RelativePath, Source: item.Entry.Location, Destination: destinationLocation}
		switch item.Entry.Kind {
		case domain.EntryDirectory:
			if err := ensureDirectory(ctx, destinationProvider, destination, destinationLocation); err != nil {
				return Result{}, err
			}
			itemResultRecord.Status = ItemSucceeded
			result.Succeeded++
		case domain.EntryFile:
			itemPlan := plan
			itemPlan.Source = FileRef{
				Location:           item.Entry.Location,
				Kind:               domain.EntryFile,
				Fingerprint:        cloneFingerprint(item.Entry.Fingerprint),
				CapabilityRevision: plan.SourceCapability.Revision,
			}
			itemPlan.DestinationDirectory = domain.Location{EndpointID: final.EndpointID, Path: domain.CanonicalPath(path.Dir(string(destinationLocation.Path)))}
			itemPlan.RequestedName = path.Base(item.RelativePath)
			itemPlan.Final = destinationLocation
			itemPlan.Part = childLocation(itemPlan.DestinationDirectory, "."+itemPlan.RequestedName+".part-"+string(plan.JobID)+fmt.Sprintf("-%d", ordinal))
			itemPlan.Discovery = nil
			if resuming {
				partEntry, statErr := destinationProvider.Stat(ctx, providerapi.StatRequest{Location: itemPlan.Part})
				if statErr == nil {
					if removeErr := destination.Remove(ctx, providerapi.RemoveRequest{Location: itemPlan.Part, Expected: &partEntry.Fingerprint}); removeErr != nil {
						return Result{}, removeErr
					}
				} else if !domain.IsCode(statErr, domain.CodeNotFound) {
					return Result{}, statErr
				}
			}
			itemResult, executeErr := NewWorker(worker.resolver, &volatileJournal{}).Execute(ctx, itemPlan, control)
			if executeErr != nil {
				if code, ok := continuableDirectoryItemError(executeErr); ok {
					itemResultRecord.Status = ItemFailed
					itemResultRecord.ErrorCode = code
					result.Failed++
					break
				}
				return result, executeErr
			}
			if itemResult.Outcome == OutcomeWaitingConflict {
				checkpoint.Phase = PhaseWaitingConflict
				checkpoint.Outcome = OutcomeWaitingConflict
				_ = worker.journal.Save(ctx, checkpoint)
				return Result{Outcome: OutcomeWaitingConflict, Final: itemResult.Final, Bytes: result.Bytes, Items: checkpoint.Items, PartRetained: true}, nil
			}
			result.Bytes += itemResult.Bytes
			itemResultRecord.Status = ItemSucceeded
			itemResultRecord.Bytes = itemResult.Bytes
			result.Succeeded++
		default:
			itemResultRecord.Status = ItemSkipped
			result.Skipped++
		}
		appendItemResult(&result, itemResultRecord)
		result.Items++
		checkpoint.Items = result.Items
		checkpoint.Offset = result.Bytes
		if err := worker.journal.Save(ctx, checkpoint); err != nil {
			return Result{}, err
		}
	}
	if err := <-failures; err != nil {
		return result, err
	}
	if result.Failed != 0 {
		result.Outcome = OutcomeCompletedPartial
		checkpoint.Outcome = result.Outcome
		checkpoint.CurrentPath = ""
		if err := worker.journal.Save(ctx, checkpoint); err != nil {
			return Result{}, err
		}
		return result, &PartialItemsError{Failed: result.Failed}
	}
	checkpoint.Phase = PhaseCommitted
	checkpoint.Outcome = result.Outcome
	checkpoint.CurrentPath = ""
	if err := worker.journal.Save(ctx, checkpoint); err != nil {
		return Result{}, err
	}
	return result, nil
}

func continuableDirectoryItemError(err error) (domain.Code, bool) {
	var operationError *domain.OpError
	if !errors.As(err, &operationError) {
		return "", false
	}
	if operationError.Code != domain.CodePermissionDenied {
		return "", false
	}
	return operationError.Code, true
}

func appendItemResult(result *Result, item ItemResult) {
	if len(result.Manifest) < maximumManifestItems {
		result.Manifest = append(result.Manifest, item)
		return
	}
	result.ManifestTruncated++
}

func validateOwnedDirectoryItem(
	ctx context.Context,
	source providerapi.Provider,
	destination providerapi.Provider,
	item DiscoveredItem,
	destinationLocation domain.Location,
	buffer []byte,
) (bool, uint64, error) {
	switch item.Entry.Kind {
	case domain.EntryDirectory:
		entry, err := destination.Stat(ctx, providerapi.StatRequest{Location: destinationLocation})
		if domain.IsCode(err, domain.CodeNotFound) {
			return false, 0, nil
		}
		if err != nil {
			return false, 0, err
		}
		if entry.Kind != domain.EntryDirectory {
			return false, 0, planError(domain.CodeConflict, "resume_directory", destinationLocation, "completed directory item changed type", domain.RetryAfterConflict)
		}
		return true, 0, nil
	case domain.EntryFile:
		destinationEntry, err := destination.Stat(ctx, providerapi.StatRequest{Location: destinationLocation})
		if domain.IsCode(err, domain.CodeNotFound) {
			return false, 0, nil
		}
		if err != nil {
			return false, 0, err
		}
		sourceChecksum, err := verifyFile(ctx, source, item.Entry.Location, item.Entry.Fingerprint, buffer)
		if err != nil {
			return false, 0, err
		}
		destinationChecksum, err := verifyFile(ctx, destination, destinationLocation, destinationEntry.Fingerprint, buffer)
		if err != nil {
			return false, 0, err
		}
		if sourceChecksum != destinationChecksum {
			return false, 0, planError(domain.CodeConflict, "resume_directory", destinationLocation, "completed file no longer matches source", domain.RetryAfterConflict)
		}
		if item.Entry.Metadata.Size != nil {
			return true, *item.Entry.Metadata.Size, nil
		}
		if item.Entry.Fingerprint.Size != nil {
			return true, *item.Entry.Fingerprint.Size, nil
		}
		return true, 0, nil
	default:
		return true, 0, nil
	}
}

func prepareDirectoryRoot(
	ctx context.Context,
	destinationProvider providerapi.Provider,
	destination providerapi.MutableProvider,
	plan Plan,
) (domain.Location, Outcome, bool, error) {
	final := plan.Final
	entry, err := destinationProvider.Stat(ctx, providerapi.StatRequest{Location: final})
	if err == nil {
		switch plan.ConflictPolicy {
		case ConflictAsk:
			return final, OutcomeWaitingConflict, false, nil
		case ConflictSkip:
			return final, OutcomeSkipped, false, nil
		case ConflictAutoRename:
			final, err = chooseAutoRename(ctx, destinationProvider, plan.DestinationDirectory, plan.RequestedName)
			if err != nil {
				return domain.Location{}, "", false, err
			}
		case ConflictOverwrite:
			if entry.Kind != domain.EntryDirectory {
				return domain.Location{}, "", false, planError(domain.CodeConflict, "execute_directory", final, "cannot merge a directory into a non-directory", domain.RetryAfterConflict)
			}
			return final, "", false, nil
		}
	} else if !domain.IsCode(err, domain.CodeNotFound) {
		return domain.Location{}, "", false, err
	}
	if _, err := destination.Mkdir(ctx, providerapi.MkdirRequest{Location: final, Exclusive: true}); err != nil {
		return domain.Location{}, "", false, err
	}
	return final, "", true, nil
}

func ensureDirectory(ctx context.Context, reader providerapi.Provider, writer providerapi.MutableProvider, location domain.Location) error {
	entry, err := reader.Stat(ctx, providerapi.StatRequest{Location: location})
	if err == nil {
		if entry.Kind != domain.EntryDirectory {
			return planError(domain.CodeConflict, "execute_directory", location, "destination path is not a directory", domain.RetryAfterConflict)
		}
		return nil
	}
	if !domain.IsCode(err, domain.CodeNotFound) {
		return err
	}
	_, err = writer.Mkdir(ctx, providerapi.MkdirRequest{Location: location, Exclusive: true})
	return err
}

type volatileJournal struct {
	mu         sync.Mutex
	checkpoint *Checkpoint
}

func (journal *volatileJournal) Load(context.Context, domain.JobID) (*Checkpoint, error) {
	journal.mu.Lock()
	defer journal.mu.Unlock()
	if journal.checkpoint == nil {
		return nil, nil
	}
	checkpoint := cloneCheckpoint(*journal.checkpoint)
	return &checkpoint, nil
}

func (journal *volatileJournal) Save(_ context.Context, checkpoint Checkpoint) error {
	journal.mu.Lock()
	defer journal.mu.Unlock()
	owned := cloneCheckpoint(checkpoint)
	journal.checkpoint = &owned
	return nil
}
