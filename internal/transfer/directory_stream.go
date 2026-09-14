package transfer

import (
	"context"
	"errors"
	"path"
	"sort"
	"sync"

	"github.com/TyrantLucifer/awesome-sftp-cli/internal/domain"
	providerapi "github.com/TyrantLucifer/awesome-sftp-cli/internal/provider"
)

// DirectoryChildCheckpoint is a bounded set of unfinished file transactions.
// Completed files are rediscovered and verified on restart, rather than keeping
// an unbounded list of all names in the parent checkpoint.
type DirectoryChildCheckpoint struct {
	RelativePath string     `json:"relative_path"`
	Checkpoint   Checkpoint `json:"checkpoint"`
}

func directoryStreamWorkers(plan Plan) int {
	if plan.StreamPolicy.DirectoryWorkers < 2 || plan.SourceEndpoint.Kind == domain.EndpointSSH && plan.DestinationEndpoint.Kind == domain.EndpointSSH {
		return 1
	}
	return 2
}

func directoryFilePlan(plan Plan, entry domain.Entry, relative string, final domain.Location) Plan {
	child := plan
	child.Source = FileRef{Location: entry.Location, Kind: domain.EntryFile, Fingerprint: cloneFingerprint(entry.Fingerprint), CapabilityRevision: plan.SourceCapability.Revision}
	child.Final = childLocation(final, relative)
	child.DestinationDirectory = domain.Location{EndpointID: final.EndpointID, Path: domain.CanonicalPath(path.Dir(string(child.Final.Path)))}
	child.RequestedName = path.Base(relative)
	child.Part = childLocation(child.DestinationDirectory, "."+child.RequestedName+".part-"+string(plan.JobID))
	child.Discovery = nil
	freezeRouteEvidence(&child)
	return child
}

type directoryStreams struct {
	mu          sync.Mutex
	parent      Journal
	root        Checkpoint
	floor       uint64
	completed   uint64
	performance *TransferPerformance
	active      map[string]Checkpoint
	live        map[string]TransferProgress
}

func (s *directoryStreams) snapshot() Checkpoint {
	s.mu.Lock()
	defer s.mu.Unlock()
	return cloneCheckpoint(s.root)
}
func (s *directoryStreams) saveLocked(ctx context.Context) error {
	s.floor = max(s.floor, s.root.Offset)
	s.root.DirectoryPerformance = cloneTransferPerformance(s.performance)
	s.root.DirectoryChildren = nil
	s.root.Offset = s.completed
	s.root.Performance = cloneTransferPerformance(s.performance)
	names := make([]string, 0, len(s.active))
	for name := range s.active {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		child := s.active[name]
		s.root.DirectoryChildren = append(s.root.DirectoryChildren, DirectoryChildCheckpoint{RelativePath: name, Checkpoint: cloneCheckpoint(child)})
		s.root.Offset = saturatingAdd(s.root.Offset, child.Offset, ^uint64(0))
		s.root.Performance = mergeTransferPerformance(s.root.Performance, child.Performance)
	}
	s.root.Offset = max(s.root.Offset, s.floor)
	return s.parent.Save(ctx, cloneCheckpoint(s.root))
}
func (s *directoryStreams) setItems(items uint64) { s.mu.Lock(); s.root.Items = items; s.mu.Unlock() }

func (s *directoryStreams) complete(ctx context.Context, name string, bytes uint64) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.performance = mergeTransferPerformance(s.performance, s.active[name].Performance)
	delete(s.active, name)
	delete(s.live, name)
	s.completed = saturatingAdd(s.completed, bytes, ^uint64(0))
	return s.saveLocked(ctx)
}

type directoryStreamJournal struct {
	streams *directoryStreams
	name    string
}

func (j *directoryStreamJournal) ObserveBuffer(bytes int) {
	j.streams.mu.Lock()
	defer j.streams.mu.Unlock()
	if observer, ok := j.streams.parent.(bufferObserver); ok {
		observer.ObserveBuffer(bytes)
	}
}
func (j *directoryStreamJournal) Load(context.Context, domain.JobID) (*Checkpoint, error) {
	j.streams.mu.Lock()
	defer j.streams.mu.Unlock()
	child, ok := j.streams.active[j.name]
	if !ok {
		return nil, nil
	}
	copy := cloneCheckpoint(child)
	return &copy, nil
}
func (j *directoryStreamJournal) Save(ctx context.Context, child Checkpoint) error {
	s := j.streams
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(child.DirectoryChildren) != 0 {
		return errors.New("directory stream: nested child checkpoint")
	}
	if _, exists := s.active[j.name]; !exists && len(s.active) >= 2 {
		return errors.New("directory stream: active checkpoint budget exhausted")
	}
	s.active[j.name] = cloneCheckpoint(child)
	s.root.CurrentPath = j.name
	return s.saveLocked(ctx)
}
func (j *directoryStreamJournal) ReportProgress(ctx context.Context, progress TransferProgress) error {
	s := j.streams
	s.mu.Lock()
	defer s.mu.Unlock()
	s.live[j.name] = progress
	aggregate := TransferProgress{Phase: PhaseStreaming, Bytes: s.completed, DurableBytes: s.root.Offset}
	if len(s.active) > 0 {
		aggregate.Phase = PhaseTransferred
	}
	for name, child := range s.active {
		if child.Phase == PhasePrepared || child.Phase == PhaseStreaming {
			aggregate.Phase = PhaseStreaming
		}
		aggregate.Bytes = saturatingAdd(aggregate.Bytes, max(child.Offset, s.live[name].Bytes), ^uint64(0))
		aggregate.VerifiedBytes = saturatingAdd(aggregate.VerifiedBytes, s.live[name].VerifiedBytes, ^uint64(0))
	}
	aggregate.Bytes = max(aggregate.Bytes, s.floor)
	return reportProgress(ctx, s.parent, aggregate)
}

type directoryStreamResult struct {
	item   DiscoveredItem
	result Result
	err    error
}

func (worker *Worker) executeDirectoryStreams(ctx context.Context, plan Plan, control Control, root Checkpoint, resuming bool, source, destinationProvider providerapi.Provider, destination providerapi.MutableProvider) (Result, error) {
	if len(root.DirectoryChildren) > 2 {
		return Result{}, errors.New("directory stream: invalid active checkpoint budget")
	}
	streams := &directoryStreams{parent: worker.journal, root: root, floor: root.Offset, performance: cloneTransferPerformance(root.DirectoryPerformance), active: make(map[string]Checkpoint), live: make(map[string]TransferProgress)}
	// Reopen the bounded interrupted transactions first. The ordinary walk below
	// then validates completed files and reconstructs aggregate byte/item counts.
	for _, saved := range root.DirectoryChildren {
		child := saved.Checkpoint
		if saved.RelativePath == "" || path.Clean(saved.RelativePath) != saved.RelativePath || path.IsAbs(saved.RelativePath) || saved.RelativePath == ".." || len(saved.RelativePath) >= 3 && saved.RelativePath[:3] == "../" || len(child.DirectoryChildren) != 0 || child.JobID != plan.JobID || child.SourceFingerprint.Size == nil {
			return Result{}, errors.New("directory stream: invalid child identity")
		}
		entry := domain.Entry{Location: childLocation(plan.Source.Location, saved.RelativePath), Kind: domain.EntryFile, Fingerprint: child.SourceFingerprint}
		childPlan := directoryFilePlan(plan, entry, saved.RelativePath, root.Final)
		if !checkpointMatchesPlan(child, childPlan) {
			return Result{}, errors.New("directory stream: child checkpoint does not match plan")
		}
		streams.active[saved.RelativePath] = child
	}
	// Do not count active performance twice when projecting child updates.
	if root.DirectoryPerformance == nil && len(root.DirectoryChildren) == 0 {
		streams.performance = cloneTransferPerformance(root.Performance)
	}
	result := Result{Outcome: OutcomeCompleted, Final: root.Final}
	streamCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	var controlMu sync.Mutex
	var childControl Control
	if control != nil {
		childControl = ControlFunc(func(Checkpoint) ControlAction {
			controlMu.Lock()
			defer controlMu.Unlock()
			return control.Action(streams.snapshot())
		})
	}
	for _, saved := range root.DirectoryChildren {
		entry := domain.Entry{Location: childLocation(plan.Source.Location, saved.RelativePath), Kind: domain.EntryFile, Fingerprint: saved.Checkpoint.SourceFingerprint}
		childPlan := directoryFilePlan(plan, entry, saved.RelativePath, root.Final)
		childResult, err := worker.withJournal(&directoryStreamJournal{streams: streams, name: saved.RelativePath}).Execute(streamCtx, childPlan, childControl)
		if err != nil {
			return result, err
		}
		if childResult.Outcome == OutcomeWaitingConflict {
			return childResult, nil
		}
		if err := streams.complete(ctx, saved.RelativePath, 0); err != nil {
			return result, err
		}
	}
	items, failures, err := DiscoverDirectory(streamCtx, source, plan.Source.Location, *plan.Discovery)
	if err != nil {
		return result, err
	}
	validationBuffer := make([]byte, streamPacketBytes)
	slots := directoryStreamWorkers(plan)
	completed := make(chan directoryStreamResult, slots)
	active := 0
	var stopErr error
	var conflict *Result
	accept := func(done directoryStreamResult) {
		record := ItemResult{RelativePath: done.item.RelativePath, Source: done.item.Entry.Location, Destination: childLocation(root.Final, done.item.RelativePath)}
		if done.err != nil {
			if code, ok := continuableDirectoryItemError(done.err); ok {
				record.Status = ItemFailed
				record.ErrorCode = code
				result.Failed++
				if stopErr == nil {
					stopErr = &PartialItemsError{Failed: result.Failed}
				}
			} else if stopErr == nil {
				stopErr = done.err
				cancel()
			}
		} else if done.result.Outcome == OutcomeWaitingConflict {
			copy := done.result
			conflict = &copy
		} else {
			record.Status = ItemSucceeded
			record.Bytes = done.result.Bytes
			result.Succeeded++
			result.Bytes += done.result.Bytes
			streams.setItems(result.Items + 1)
			if err := streams.complete(ctx, done.item.RelativePath, done.result.Bytes); err != nil && stopErr == nil {
				stopErr = err
				cancel()
			}
		}
		if record.Status != "" {
			result.Items++
			appendItemResult(&result, record)
		}
	}
	drain := func() {
		for active > 0 {
			accept(<-completed)
			active--
		}
	}
	for item := range items {
		if stopErr != nil || conflict != nil {
			break
		}
		// A recovery readback occupies a file slot too. Reserve that slot before
		// validating an existing item, so validation cannot add a third window.
		if active == slots {
			accept(<-completed)
			active--
			if stopErr != nil || conflict != nil {
				break
			}
		}
		location := childLocation(root.Final, item.RelativePath)
		if item.Entry.Kind != domain.EntryFile {
			drain()
			if stopErr != nil || conflict != nil {
				break
			}
		}
		if resuming && root.DirectoryRootOwned {
			// Validation uses one extra bounded buffer, accounted in admission.
			matched, bytes, err := worker.validateOwnedDirectoryStreamItem(streamCtx, plan, source, destinationProvider, item, location, validationBuffer)
			if err != nil {
				stopErr = err
				cancel()
				break
			}
			if matched {
				result.Items++
				result.Succeeded++
				result.Bytes += bytes
				appendItemResult(&result, ItemResult{RelativePath: item.RelativePath, Source: item.Entry.Location, Destination: location, Status: ItemSucceeded, Bytes: bytes})
				streams.setItems(result.Items)
				if err := streams.complete(ctx, item.RelativePath, bytes); err != nil {
					stopErr = err
					cancel()
					break
				}
				continue
			}
		}
		if childControl != nil {
			action := childControl.Action(Checkpoint{})
			if action != ControlContinue {
				if action == ControlPause {
					stopErr = ErrPaused
				} else {
					stopErr = ErrCanceled
				}
				cancel()
				break
			}
		}
		if item.Entry.Kind == domain.EntryFile {
			active++
			go func(item DiscoveredItem) {
				childPlan := directoryFilePlan(plan, item.Entry, item.RelativePath, root.Final)
				childResult, err := worker.withJournal(&directoryStreamJournal{streams: streams, name: item.RelativePath}).Execute(streamCtx, childPlan, childControl)
				completed <- directoryStreamResult{item: item, result: childResult, err: err}
			}(item)
			continue
		}
		record := ItemResult{RelativePath: item.RelativePath, Source: item.Entry.Location, Destination: location, Status: ItemSkipped}
		if item.Entry.Kind == domain.EntryDirectory {
			if err := ensureDirectory(streamCtx, destinationProvider, destination, location); err != nil {
				stopErr = err
				cancel()
				break
			}
			record.Status = ItemSucceeded
			result.Succeeded++
		} else {
			result.Skipped++
		}
		result.Items++
		appendItemResult(&result, record)
		streams.setItems(result.Items)
		if err := streams.complete(ctx, item.RelativePath, 0); err != nil {
			stopErr = err
			cancel()
			break
		}
	}
	drain()
	if stopErr == nil && conflict == nil {
		stopErr = <-failures
	}
	streams.mu.Lock()
	defer streams.mu.Unlock()
	streams.root.Items = result.Items
	streams.root.CurrentPath = ""
	if conflict != nil {
		streams.root.Phase = PhaseWaitingConflict
		streams.root.Outcome = OutcomeWaitingConflict
		result.Outcome = OutcomeWaitingConflict
		result.Final = conflict.Final
		result.PartRetained = true
	} else if stopErr == nil {
		streams.root.Phase = PhaseCommitted
		streams.root.Outcome = OutcomeCompleted
	} else if result.Failed > 0 {
		result.Outcome = OutcomeCompletedPartial
		streams.root.Outcome = OutcomeCompletedPartial
		var partial *PartialItemsError
		if errors.As(stopErr, &partial) {
			stopErr = &PartialItemsError{Failed: result.Failed}
		}
	}
	if err := streams.saveLocked(ctx); err != nil {
		return result, err
	}
	if errors.Is(stopErr, ErrPaused) || errors.Is(stopErr, ErrCanceled) {
		result.Bytes = streams.root.Offset
		result.PartRetained = len(streams.active) > 0
	}
	return result, stopErr
}

func (worker *Worker) validateOwnedDirectoryStreamItem(ctx context.Context, plan Plan, source, destination providerapi.Provider, item DiscoveredItem, location domain.Location, buffer []byte) (bool, uint64, error) {
	if item.Entry.Kind != domain.EntryFile {
		return worker.validateOwnedDirectoryItem(ctx, plan, source, destination, item, location, buffer)
	}
	entry, err := destination.Stat(ctx, providerapi.StatRequest{Location: location})
	if domain.IsCode(err, domain.CodeNotFound) {
		return false, 0, nil
	}
	if err != nil {
		return false, 0, err
	}
	sourceHash, err := worker.verifyFile(ctx, plan, nil, source, item.Entry.Location, item.Entry.Fingerprint, buffer)
	if err != nil {
		return false, 0, err
	}
	targetHash, err := worker.verifyFile(ctx, plan, nil, destination, location, entry.Fingerprint, buffer)
	if err != nil {
		return false, 0, err
	}
	if sourceHash != targetHash {
		return false, 0, planError(domain.CodeConflict, "resume_directory", location, "completed directory item content changed", domain.RetryAfterConflict)
	}
	if entry.Metadata.Size == nil {
		return false, 0, errors.New("directory stream: completed file has no size")
	}
	return true, *entry.Metadata.Size, nil
}
