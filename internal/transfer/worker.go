package transfer

import (
	"context"
	"crypto/sha256"
	"encoding"
	"encoding/hex"
	"errors"
	"fmt"
	"hash"
	"io"
	"math"
	"path"
	"reflect"
	"time"

	"github.com/TyrantLucifer/awesome-mac-sftp/internal/domain"
	providerapi "github.com/TyrantLucifer/awesome-mac-sftp/internal/provider"
)

var (
	ErrPaused   = errors.New("transfer paused")
	ErrCanceled = errors.New("transfer canceled")
)

const preservationTimeout = 15 * time.Minute

type PartialItemsError struct{ Failed uint64 }

func (err *PartialItemsError) Error() string {
	return fmt.Sprintf("directory transfer has %d retryable failed items", err.Failed)
}

type Phase string

const (
	PhasePrepared        Phase = "prepared"
	PhaseStreaming       Phase = "streaming"
	PhaseTransferred     Phase = "transferred"
	PhaseVerified        Phase = "verified"
	PhaseWaitingConflict Phase = "waiting_conflict"
	PhaseCommitting      Phase = "committing"
	PhaseCommitted       Phase = "committed"
)

type Outcome string

const (
	OutcomeCompleted        Outcome = "completed"
	OutcomeCompletedPartial Outcome = "completed_partial"
	OutcomeSkipped          Outcome = "skipped"
	OutcomeWaitingConflict  Outcome = "waiting_conflict"
)

type Checkpoint struct {
	JobID               domain.JobID       `json:"job_id"`
	Phase               Phase              `json:"phase"`
	Offset              uint64             `json:"offset"`
	SourceFingerprint   domain.Fingerprint `json:"source_fingerprint"`
	Part                domain.Location    `json:"part"`
	PartFingerprint     domain.Fingerprint `json:"part_fingerprint"`
	ChecksumState       []byte             `json:"checksum_state,omitempty"`
	ChecksumHex         string             `json:"checksum_hex,omitempty"`
	Final               domain.Location    `json:"final"`
	Outcome             Outcome            `json:"outcome,omitempty"`
	Items               uint64             `json:"items,omitempty"`
	CurrentPath         string             `json:"current_path,omitempty"`
	DirectoryRootOwned  bool               `json:"directory_root_owned,omitempty"`
	ActualRoute         Route              `json:"actual_route,omitempty"`
	DowngradedFrom      Route              `json:"downgraded_from,omitempty"`
	RouteReason         RouteReason        `json:"route_reason,omitempty"`
	DirectFormatVersion uint16             `json:"direct_format_version,omitempty"`
	DirectNonce         string             `json:"direct_nonce,omitempty"`
}

type Journal interface {
	Load(context.Context, domain.JobID) (*Checkpoint, error)
	Save(context.Context, Checkpoint) error
}

type bufferObserver interface{ ObserveBuffer(int) }

type ControlAction uint8

const (
	ControlContinue ControlAction = iota
	ControlPause
	ControlCancel
)

type Control interface {
	Action(Checkpoint) ControlAction
}

type ControlFunc func(Checkpoint) ControlAction

func (function ControlFunc) Action(checkpoint Checkpoint) ControlAction {
	return function(checkpoint)
}

type Result struct {
	Outcome              Outcome
	Final                domain.Location
	Bytes                uint64
	SHA256               string
	PartRetained         bool
	PreservedDestination domain.Location
	PreservationUnknown  bool
	Items                uint64
	Succeeded            uint64
	Skipped              uint64
	Failed               uint64
	Manifest             []ItemResult
	ManifestTruncated    uint64
}

type ItemStatus string

const (
	ItemSucceeded ItemStatus = "succeeded"
	ItemSkipped   ItemStatus = "skipped"
	ItemFailed    ItemStatus = "failed"
)

type ItemResult struct {
	RelativePath string          `json:"relative_path"`
	Source       domain.Location `json:"source"`
	Destination  domain.Location `json:"destination"`
	Status       ItemStatus      `json:"status"`
	Bytes        uint64          `json:"bytes,omitempty"`
	ErrorCode    domain.Code     `json:"error_code,omitempty"`
}

type Worker struct {
	resolver  Resolver
	journal   Journal
	sameHost  SameHostCopyBackend
	level2    level2DataBackend
	scheduler bandwidthScheduler
}

type bandwidthScheduler interface {
	Wait(context.Context, BandwidthRequest) error
	QuantumBytes() uint32
}

func NewWorker(resolver Resolver, journal Journal) *Worker {
	return &Worker{resolver: resolver, journal: journal}
}

func NewWorkerWithSameHost(resolver Resolver, journal Journal, backend SameHostCopyBackend) *Worker {
	return &Worker{resolver: resolver, journal: journal, sameHost: backend}
}

func (worker *Worker) Execute(ctx context.Context, plan Plan, control Control) (Result, error) {
	if err := validateExecution(plan); err != nil {
		return Result{}, err
	}
	if worker == nil || worker.resolver == nil || worker.journal == nil {
		return Result{}, errors.New("execute transfer: resolver and durable journal are required")
	}
	if plan.Route == RouteLevel2Direct && worker.level2 == nil {
		return Result{}, planError(domain.CodeUnsupported, "execute_direct", plan.Part, "Level 2 data-plane fixture is not attached", domain.RetryAfterReplan)
	}
	directRevalidationFallback := false
	if plan.Source.Kind == domain.EntryDirectory {
		return worker.executeDirectory(ctx, plan, control)
	}
	source, err := worker.resolver.Resolve(plan.Source.Location.EndpointID)
	if err != nil {
		return Result{}, err
	}
	destinationProvider, err := worker.resolver.Resolve(plan.Part.EndpointID)
	if err != nil {
		return Result{}, err
	}
	destination, ok := destinationProvider.(providerapi.MutableProvider)
	if !ok {
		return Result{}, planError(domain.CodeUnsupported, "execute_copy", plan.Part, "destination mutation facet disappeared", domain.RetryAfterReplan)
	}
	if err := requireFrozenCapability(ctx, source, plan.Source.Location, plan.SourceCapability); err != nil {
		return Result{}, err
	}
	if err := requireFrozenCapability(ctx, destinationProvider, plan.Part, plan.DestinationCapability); err != nil {
		return Result{}, err
	}

	checkpoint, err := worker.journal.Load(ctx, plan.JobID)
	if err != nil {
		return Result{}, fmt.Errorf("execute transfer: load checkpoint: %w", err)
	}
	if checkpoint != nil && checkpoint.JobID != plan.JobID {
		return Result{}, errors.New("execute transfer: checkpoint belongs to another Job")
	}
	if checkpoint != nil && !checkpointMatchesPlan(*checkpoint, plan) {
		return Result{}, errors.New("execute transfer: checkpoint does not match frozen route identity")
	}
	if checkpoint != nil && checkpoint.Phase == PhaseCommitted {
		result := Result{Outcome: checkpoint.Outcome, Final: checkpoint.Final, Bytes: checkpoint.Offset, SHA256: checkpoint.ChecksumHex}
		if plan.PreservedDestination.Path != "" {
			result.PreservedDestination = plan.PreservedDestination
		}
		return result, nil
	}
	if plan.Route == RouteLevel2Direct && (checkpoint == nil || checkpoint.ActualRoute != RouteSFTPRelay) {
		refreshed, fallback, refreshErr := worker.refreshExpiredLevel2Preflight(ctx, plan, time.Now())
		if refreshErr != nil {
			return Result{}, refreshErr
		}
		plan = refreshed
		directRevalidationFallback = fallback
	}

	buffer := make([]byte, int(plan.BufferBytes))
	if observer, ok := worker.journal.(bufferObserver); ok {
		observer.ObserveBuffer(len(buffer))
	}
	hasher := sha256.New()
	current := Checkpoint{
		JobID:             plan.JobID,
		Phase:             PhasePrepared,
		SourceFingerprint: cloneFingerprint(plan.Source.Fingerprint),
		Part:              plan.Part,
		Final:             plan.Final,
		ActualRoute:       plan.Route,
		RouteReason:       plannedRouteReason(plan),
	}
	if checkpoint != nil {
		current = cloneCheckpoint(*checkpoint)
		if current.ActualRoute == "" {
			current.ActualRoute = plan.Route
			current.RouteReason = plannedRouteReason(plan)
		}
	}
	if directRevalidationFallback {
		reason := ReasonLevel2RevalidationFailed
		if checkpoint != nil {
			cleaned, cleanupErr := cleanupExactDirectPartForRelay(ctx, plan, destinationProvider, destination, current)
			if cleanupErr != nil {
				return Result{}, cleanupErr
			}
			if cleaned {
				reason = ReasonLevel2PartCleanedForRelay
			} else if current.Offset == 0 {
				if _, statErr := destinationProvider.Stat(ctx, providerapi.StatRequest{Location: plan.Part}); !domain.IsCode(statErr, domain.CodeNotFound) {
					if statErr != nil {
						return Result{}, statErr
					}
					return Result{}, planError(domain.CodeConflict, "preflight_direct", plan.Part, "unacknowledged direct part exists after failed revalidation", domain.RetryAfterReplan)
				}
			} else {
				return Result{}, planError(domain.CodeConflict, "preflight_direct", plan.Part, "expired direct evidence failed with an unprovable checkpoint", domain.RetryAfterReplan)
			}
		}
		current.Phase = PhasePrepared
		current.Offset = 0
		current.PartFingerprint = domain.Fingerprint{}
		current.ChecksumState = nil
		current.ChecksumHex = ""
		current.ActualRoute = RouteSFTPRelay
		current.DowngradedFrom = RouteLevel2Direct
		current.RouteReason = reason
		current.DirectFormatVersion = Level2DirectFormatVersion
		current.DirectNonce = plan.Level2Preflight.Request.Nonce
		if err := worker.journal.Save(ctx, current); err != nil {
			return Result{}, fmt.Errorf("execute transfer: persist expired direct route downgrade: %w", err)
		}
	}
	if plan.Route == RouteLevel2Direct {
		if checkpoint == nil {
			current.DirectFormatVersion = Level2DirectFormatVersion
			current.DirectNonce = plan.Level2Preflight.Request.Nonce
		}
		if current.ActualRoute != RouteSFTPRelay && (current.Phase == PhasePrepared || current.Phase == PhaseStreaming) {
			result, directErr := worker.executeLevel2Direct(ctx, plan, source, destinationProvider, destination, &current, checkpoint != nil, control, buffer)
			if !errors.Is(directErr, errLevel2SafeRelayFallback) && !errors.Is(directErr, errLevel2CleanedPartRelayFallback) {
				return result, directErr
			}
			current.Phase = PhasePrepared
			current.Offset = 0
			current.PartFingerprint = domain.Fingerprint{}
			current.ChecksumState = nil
			current.ChecksumHex = ""
			current.ActualRoute = RouteSFTPRelay
			current.DowngradedFrom = RouteLevel2Direct
			current.RouteReason = ReasonLevel2FailedBeforeWrite
			if errors.Is(directErr, errLevel2CleanedPartRelayFallback) {
				current.RouteReason = ReasonLevel2PartCleanedForRelay
			}
			if err := worker.journal.Save(ctx, current); err != nil {
				return Result{}, fmt.Errorf("execute transfer: persist safe direct route downgrade: %w", err)
			}
		}
	}
	if plan.Route == RouteHelperSameHost && current.Phase == PhasePrepared {
		return worker.executeSameHostCopy(ctx, plan, destinationProvider, destination, &current, checkpoint != nil, control, buffer)
	}
	if plan.Route == RouteSFTPServerCopy && current.ActualRoute != RouteSFTPRelay && current.Phase == PhasePrepared {
		result, serverCopyErr := worker.executeServerCopy(ctx, plan, source, destinationProvider, destination, &current, checkpoint != nil, control, buffer)
		if !errors.Is(serverCopyErr, errServerCopySafeFallback) {
			return result, serverCopyErr
		}
		current.ActualRoute = RouteSFTPRelay
		current.DowngradedFrom = RouteSFTPServerCopy
		current.RouteReason = ReasonServerCopyFailedBeforeWrite
		if err := worker.journal.Save(ctx, current); err != nil {
			return Result{}, fmt.Errorf("execute transfer: persist safe route downgrade: %w", err)
		}
	}

	if current.Phase == PhaseVerified || current.Phase == PhaseWaitingConflict || current.Phase == PhaseCommitting {
		return worker.commit(ctx, plan, destinationProvider, destination, current, buffer)
	}
	if current.Phase == PhaseTransferred {
		partEntry, statErr := destinationProvider.Stat(ctx, providerapi.StatRequest{Location: plan.Part})
		if statErr != nil {
			return Result{}, statErr
		}
		checksum, verifyErr := verifyFile(ctx, destinationProvider, plan.Part, partEntry.Fingerprint, buffer)
		if verifyErr != nil {
			return Result{}, verifyErr
		}
		if checksum != current.ChecksumHex {
			return Result{}, planError(domain.CodeConflict, "verify_part", plan.Part, "part checksum does not match streamed source", domain.RetryNever)
		}
		current.Phase = PhaseVerified
		current.PartFingerprint = cloneFingerprint(partEntry.Fingerprint)
		if err := worker.journal.Save(ctx, current); err != nil {
			return Result{}, err
		}
		return worker.commit(ctx, plan, destinationProvider, destination, current, buffer)
	}

	var writeHandle providerapi.WriteHandle
	if checkpoint == nil || current.Phase == PhasePrepared {
		if checkpoint == nil {
			if err := worker.journal.Save(ctx, current); err != nil {
				return Result{}, fmt.Errorf("execute transfer: persist part intent: %w", err)
			}
		}
		writeHandle, err = destination.OpenWrite(ctx, providerapi.OpenWriteRequest{
			Location:    plan.Part,
			Disposition: providerapi.WriteCreateNew,
		})
		if err != nil {
			return Result{}, err
		}
		if err := writeHandle.Sync(ctx); err != nil {
			_ = writeHandle.Close(context.Background())
			return Result{}, err
		}
		partEntry, statErr := destinationProvider.Stat(ctx, providerapi.StatRequest{Location: plan.Part})
		if statErr != nil {
			_ = writeHandle.Close(context.Background())
			return Result{}, statErr
		}
		current.Phase = PhaseStreaming
		current.PartFingerprint = cloneFingerprint(partEntry.Fingerprint)
		current.ChecksumState, err = marshalChecksum(hasher)
		if err != nil {
			_ = writeHandle.Close(context.Background())
			return Result{}, err
		}
		if err := worker.journal.Save(ctx, current); err != nil {
			_ = writeHandle.Close(context.Background())
			return Result{}, err
		}
	} else if current.Phase == PhaseStreaming {
		providerOffset, offsetErr := checkedProviderOffset(current.Offset)
		if offsetErr != nil {
			return Result{}, offsetErr
		}
		if err := unmarshalChecksum(hasher, current.ChecksumState); err != nil {
			return Result{}, err
		}
		partEntry, statErr := destinationProvider.Stat(ctx, providerapi.StatRequest{Location: plan.Part})
		if statErr != nil {
			return Result{}, statErr
		}
		if partEntry.Metadata.Size == nil || *partEntry.Metadata.Size != current.Offset {
			return Result{}, planError(domain.CodeConflict, "resume_copy", plan.Part, "part no longer matches durable checkpoint", domain.RetryAfterConflict)
		}
		if !reflect.DeepEqual(partEntry.Fingerprint, current.PartFingerprint) {
			checksum, verifyErr := verifyFile(ctx, destinationProvider, plan.Part, partEntry.Fingerprint, buffer)
			if verifyErr != nil {
				return Result{}, verifyErr
			}
			if checksum != hex.EncodeToString(hasher.Sum(nil)) {
				return Result{}, planError(domain.CodeConflict, "resume_copy", plan.Part, "part content no longer matches durable checkpoint", domain.RetryAfterConflict)
			}
			current.PartFingerprint = cloneFingerprint(partEntry.Fingerprint)
			if err := worker.journal.Save(ctx, current); err != nil {
				return Result{}, fmt.Errorf("execute transfer: refresh revalidated part checkpoint: %w", err)
			}
		}
		writeHandle, err = destination.OpenWrite(ctx, providerapi.OpenWriteRequest{
			Location:            plan.Part,
			Offset:              providerOffset,
			Disposition:         providerapi.WriteResumeExisting,
			ExpectedFingerprint: &current.PartFingerprint,
		})
		if err != nil {
			return Result{}, err
		}
	} else {
		return Result{}, fmt.Errorf("execute transfer: unsupported checkpoint phase %q", current.Phase)
	}
	defer func() { _ = writeHandle.Close(context.Background()) }()

	providerOffset, err := checkedProviderOffset(current.Offset)
	if err != nil {
		return Result{}, err
	}
	readHandle, err := source.OpenRead(ctx, providerapi.OpenReadRequest{
		Location:            plan.Source.Location,
		Offset:              providerOffset,
		ExpectedFingerprint: &plan.Source.Fingerprint,
	})
	if err != nil {
		return Result{}, err
	}
	defer func() { _ = readHandle.Close(context.Background()) }()

	for {
		if control != nil {
			switch control.Action(cloneCheckpoint(current)) {
			case ControlPause:
				if err := worker.closeAndRefreshCheckpoint(ctx, destinationProvider, writeHandle, &current); err != nil {
					return Result{}, err
				}
				return Result{Final: plan.Final, Bytes: current.Offset, PartRetained: true}, ErrPaused
			case ControlCancel:
				if err := worker.closeAndRefreshCheckpoint(ctx, destinationProvider, writeHandle, &current); err != nil {
					return Result{}, err
				}
				return Result{Final: plan.Final, Bytes: current.Offset, PartRetained: true}, ErrCanceled
			}
		}
		readBuffer := buffer
		if worker.scheduler != nil {
			quantum := worker.scheduler.QuantumBytes()
			if quantum > 0 && int(quantum) < len(readBuffer) {
				readBuffer = readBuffer[:quantum]
			}
		}
		n, readErr := readHandle.Read(ctx, readBuffer)
		if n < 0 || n > len(readBuffer) {
			return Result{}, errors.New("execute transfer: provider returned invalid read count")
		}
		if n > 0 {
			if worker.scheduler != nil {
				if err := worker.scheduler.Wait(ctx, BandwidthRequest{
					JobID: plan.JobID, EndpointID: plan.SourceEndpoint.ID, PeerEndpointID: plan.DestinationEndpoint.ID,
					JobBytesPerSecond: plan.Bandwidth.JobBytesPerSecond, Class: ScheduleBulk,
					Bytes: uint32(n), //nolint:gosec // n is bounded by the scheduler quantum, whose hard ceiling fits uint32.
				}); err != nil {
					return Result{}, err
				}
			}
			if err := writeAll(ctx, writeHandle, buffer[:n]); err != nil {
				return Result{}, err
			}
			if _, err := hasher.Write(buffer[:n]); err != nil {
				return Result{}, fmt.Errorf("execute transfer: hash source: %w", err)
			}
			if err := writeHandle.Sync(ctx); err != nil {
				return Result{}, err
			}
			current.Offset += uint64(n)
			partEntry, statErr := destinationProvider.Stat(ctx, providerapi.StatRequest{Location: plan.Part})
			if statErr != nil {
				return Result{}, statErr
			}
			if partEntry.Metadata.Size == nil || *partEntry.Metadata.Size != current.Offset {
				return Result{}, planError(domain.CodeConflict, "stream_copy", plan.Part, "part size does not match streamed offset", domain.RetryNever)
			}
			current.PartFingerprint = cloneFingerprint(partEntry.Fingerprint)
			current.ChecksumState, err = marshalChecksum(hasher)
			if err != nil {
				return Result{}, err
			}
			if err := worker.journal.Save(ctx, current); err != nil {
				return Result{}, fmt.Errorf("execute transfer: save streaming checkpoint: %w", err)
			}
		}
		if readErr != nil {
			if !errors.Is(readErr, io.EOF) {
				return Result{}, readErr
			}
			break
		}
		if n == 0 {
			return Result{}, errors.New("execute transfer: source read made no progress")
		}
	}
	if plan.Source.Fingerprint.Size != nil && current.Offset != *plan.Source.Fingerprint.Size {
		return Result{}, planError(domain.CodeConflict, "stream_copy", plan.Source.Location, "source size changed during transfer", domain.RetryAfterConflict)
	}
	if err := writeHandle.Close(ctx); err != nil {
		return Result{}, err
	}
	if err := readHandle.Close(ctx); err != nil {
		return Result{}, err
	}
	partEntry, err := destinationProvider.Stat(ctx, providerapi.StatRequest{Location: plan.Part})
	if err != nil {
		return Result{}, err
	}
	current.PartFingerprint = cloneFingerprint(partEntry.Fingerprint)
	current.ChecksumHex = hex.EncodeToString(hasher.Sum(nil))
	if plan.Version == 2 && current.ChecksumHex != plan.ExpectedSourceSHA256 {
		return Result{Final: plan.Final, Bytes: current.Offset, SHA256: current.ChecksumHex, PartRetained: true}, planError(domain.CodeConflict, "verify_sync_back_source", plan.Source.Location, "materialization content differs from frozen edit digest", domain.RetryAfterConflict)
	}
	current.Phase = PhaseTransferred
	if err := worker.journal.Save(ctx, current); err != nil {
		return Result{}, err
	}
	destinationChecksum, err := verifyFile(ctx, destinationProvider, plan.Part, partEntry.Fingerprint, buffer)
	if err != nil {
		return Result{}, err
	}
	if destinationChecksum != current.ChecksumHex {
		return Result{}, planError(domain.CodeConflict, "verify_part", plan.Part, "part checksum does not match streamed source", domain.RetryNever)
	}
	current.Phase = PhaseVerified
	if err := worker.journal.Save(ctx, current); err != nil {
		return Result{}, err
	}
	return worker.commit(ctx, plan, destinationProvider, destination, current, buffer)
}

func checkpointMatchesPlan(checkpoint Checkpoint, plan Plan) bool {
	if checkpoint.Part != plan.Part || !reflect.DeepEqual(checkpoint.SourceFingerprint, plan.Source.Fingerprint) {
		return false
	}
	if plan.Route == RouteLevel2Direct {
		if plan.Level2Preflight == nil || checkpoint.DirectFormatVersion != Level2DirectFormatVersion ||
			validateLowerHexIdentity(checkpoint.DirectNonce, 32) != nil {
			return false
		}
	} else if checkpoint.DirectFormatVersion != 0 || checkpoint.DirectNonce != "" {
		return false
	}
	if checkpoint.Phase != PhaseCommitting && checkpoint.Phase != PhaseCommitted {
		return checkpoint.Final == plan.Final && validCheckpointRoute(checkpoint, plan)
	}
	return checkpoint.Final.EndpointID == plan.DestinationDirectory.EndpointID &&
		path.Dir(string(checkpoint.Final.Path)) == string(plan.DestinationDirectory.Path) && validCheckpointRoute(checkpoint, plan)
}

func validCheckpointRoute(checkpoint Checkpoint, plan Plan) bool {
	if checkpoint.ActualRoute == "" {
		return checkpoint.DowngradedFrom == "" && checkpoint.RouteReason == ""
	}
	if checkpoint.ActualRoute == plan.Route {
		return checkpoint.DowngradedFrom == "" && checkpoint.RouteReason == plannedRouteReason(plan)
	}
	return checkpoint.ActualRoute == RouteSFTPRelay && checkpoint.DowngradedFrom == plan.Route &&
		(plan.Route == RouteSFTPServerCopy && checkpoint.RouteReason == ReasonServerCopyFailedBeforeWrite ||
			plan.Route == RouteLevel2Direct && (checkpoint.RouteReason == ReasonLevel2FailedBeforeWrite || checkpoint.RouteReason == ReasonLevel2RevalidationFailed || checkpoint.RouteReason == ReasonLevel2PartCleanedForRelay))
}

func plannedRouteReason(plan Plan) RouteReason {
	if plan.RouteEvidence != nil && plan.RouteEvidence.Selected.Route == plan.Route {
		return plan.RouteEvidence.Selected.Reason
	}
	return ""
}

func (worker *Worker) closeAndRefreshCheckpoint(ctx context.Context, destination providerapi.Provider, handle providerapi.WriteHandle, checkpoint *Checkpoint) error {
	if err := handle.Sync(ctx); err != nil {
		return err
	}
	if err := handle.Close(ctx); err != nil {
		return err
	}
	entry, err := destination.Stat(ctx, providerapi.StatRequest{Location: checkpoint.Part})
	if err != nil {
		return err
	}
	if entry.Metadata.Size == nil || *entry.Metadata.Size != checkpoint.Offset {
		return planError(domain.CodeConflict, "checkpoint_copy", checkpoint.Part, "part size changed while closing write handle", domain.RetryAfterConflict)
	}
	checkpoint.PartFingerprint = cloneFingerprint(entry.Fingerprint)
	if err := worker.journal.Save(ctx, *checkpoint); err != nil {
		return fmt.Errorf("execute transfer: refresh closed part checkpoint: %w", err)
	}
	return nil
}

func (worker *Worker) commit(ctx context.Context, plan Plan, destinationProvider providerapi.Provider, destination providerapi.MutableProvider, checkpoint Checkpoint, buffer []byte) (result Result, returnErr error) {
	preserved := false
	preservationUnknown := false
	defer func() {
		if returnErr != nil && result.PreservedDestination.Path == "" && (preserved || preservationUnknown) {
			result.PreservedDestination = plan.PreservedDestination
			result.PreservationUnknown = preservationUnknown
		}
	}()
	partEntry, err := destinationProvider.Stat(ctx, providerapi.StatRequest{Location: plan.Part})
	if err != nil {
		if checkpoint.Phase == PhaseCommitting && domain.IsCode(err, domain.CodeNotFound) {
			proved, proofErr := worker.proveCommitted(ctx, plan, destinationProvider, plan.Final, checkpoint.ChecksumHex, buffer)
			if proofErr == nil && proved {
				checkpoint.Phase = PhaseCommitted
				checkpoint.Outcome = OutcomeCompleted
				checkpoint.Final = plan.Final
				if saveErr := worker.journal.Save(ctx, checkpoint); saveErr != nil {
					return Result{}, saveErr
				}
				result := Result{Outcome: OutcomeCompleted, Final: plan.Final, Bytes: checkpoint.Offset, SHA256: checkpoint.ChecksumHex}
				if plan.PreservedDestination.Path != "" {
					result.PreservedDestination = plan.PreservedDestination
				}
				return result, nil
			}
		}
		return Result{}, err
	}
	if !reflect.DeepEqual(partEntry.Fingerprint, checkpoint.PartFingerprint) {
		return Result{}, planError(domain.CodeConflict, "commit_copy", plan.Part, "verified part changed before commit", domain.RetryAfterConflict)
	}
	final := plan.Final
	finalEntry, finalErr := destinationProvider.Stat(ctx, providerapi.StatRequest{Location: final})
	finalExists := finalErr == nil
	if finalErr != nil && !domain.IsCode(finalErr, domain.CodeNotFound) {
		return Result{}, finalErr
	}
	if plan.Version == 2 && plan.ExpectedDestination.Presence == DestinationPresent {
		_, backupErr := destinationProvider.Stat(ctx, providerapi.StatRequest{Location: plan.PreservedDestination})
		backupExists := backupErr == nil
		if backupErr != nil && !domain.IsCode(backupErr, domain.CodeNotFound) {
			return Result{}, backupErr
		}
		if !backupExists && !destinationPreconditionMatches(plan.ExpectedDestination, finalExists, finalEntry) {
			checkpoint.Phase = PhaseWaitingConflict
			checkpoint.Outcome = OutcomeWaitingConflict
			if err := worker.journal.Save(ctx, checkpoint); err != nil {
				return Result{}, err
			}
			return Result{Outcome: OutcomeWaitingConflict, Final: final, Bytes: checkpoint.Offset, SHA256: checkpoint.ChecksumHex, PartRetained: true}, nil
		}
		if !backupExists {
			contentSHA, hashErr := verifyFile(ctx, destinationProvider, final, plan.ExpectedDestination.Fingerprint, buffer)
			if hashErr != nil {
				return Result{}, hashErr
			}
			if contentSHA != string(plan.ExpectedDestination.ContentSHA256) {
				checkpoint.Phase = PhaseWaitingConflict
				checkpoint.Outcome = OutcomeWaitingConflict
				if err := worker.journal.Save(ctx, checkpoint); err != nil {
					return Result{}, err
				}
				return Result{Outcome: OutcomeWaitingConflict, Final: final, Bytes: checkpoint.Offset, SHA256: checkpoint.ChecksumHex, PartRetained: true}, nil
			}
		}
		preserver, ok := destinationProvider.(providerapi.DestinationPreserver)
		if !ok {
			return Result{}, planError(domain.CodeUnsupported, "commit_copy", final, "destination cannot preserve the replaced remote version", domain.RetryAfterReplan)
		}
		expectedSize := int64(*plan.ExpectedDestination.Fingerprint.Size) //nolint:gosec // validated below the 2 GiB preservation ceiling
		preserveCtx, cancelPreserve := context.WithTimeout(ctx, preservationTimeout)
		preserveResult, preserveErr := preserver.PreserveDestination(preserveCtx, providerapi.PreserveDestinationRequest{
			Source: final, Backup: plan.PreservedDestination, ExpectedFingerprint: plan.ExpectedDestination.Fingerprint,
			ExpectedSHA256: string(plan.ExpectedDestination.ContentSHA256), ExpectedSize: expectedSize, MaxBytes: 2 * 1024 * 1024 * 1024,
		})
		cancelPreserve()
		preserved = preserveResult.BackupPresent
		preservationUnknown = preserveResult.EffectUnknown
		if preserveErr != nil {
			if domain.IsCode(preserveErr, domain.CodeConflict) {
				checkpoint.Phase = PhaseWaitingConflict
				checkpoint.Outcome = OutcomeWaitingConflict
				if saveErr := worker.journal.Save(ctx, checkpoint); saveErr != nil {
					return Result{}, saveErr
				}
				conflictResult := Result{Outcome: OutcomeWaitingConflict, Final: final, Bytes: checkpoint.Offset, SHA256: checkpoint.ChecksumHex, PartRetained: true}
				if preserved || preservationUnknown {
					conflictResult.PreservedDestination = plan.PreservedDestination
					conflictResult.PreservationUnknown = preservationUnknown
				}
				return conflictResult, nil
			}
			return Result{}, preserveErr
		}
		if !preserved {
			return Result{}, planError(domain.CodeIntegrityFailed, "commit_copy", plan.PreservedDestination, "provider did not confirm preserved destination", domain.RetryNever)
		}
		finalEntry, finalErr = destinationProvider.Stat(ctx, providerapi.StatRequest{Location: final})
		finalExists = finalErr == nil
		if finalErr != nil && !domain.IsCode(finalErr, domain.CodeNotFound) {
			return Result{}, finalErr
		}
		if finalExists {
			proved, proofErr := proveCommitted(ctx, destinationProvider, final, checkpoint.ChecksumHex, buffer)
			if proofErr != nil || !proved {
				checkpoint.Phase = PhaseWaitingConflict
				checkpoint.Outcome = OutcomeWaitingConflict
				if err := worker.journal.Save(ctx, checkpoint); err != nil {
					return Result{}, err
				}
				return Result{Outcome: OutcomeWaitingConflict, Final: final, Bytes: checkpoint.Offset, SHA256: checkpoint.ChecksumHex, PartRetained: true, PreservedDestination: plan.PreservedDestination}, nil
			}
		}
	}
	if plan.Version == 2 && plan.ExpectedDestination.Presence == DestinationAbsent && !destinationPreconditionMatches(plan.ExpectedDestination, finalExists, finalEntry) {
		checkpoint.Phase = PhaseWaitingConflict
		checkpoint.Outcome = OutcomeWaitingConflict
		if err := worker.journal.Save(ctx, checkpoint); err != nil {
			return Result{}, err
		}
		return Result{Outcome: OutcomeWaitingConflict, Final: final, Bytes: checkpoint.Offset, SHA256: checkpoint.ChecksumHex, PartRetained: true}, nil
	}
	if finalExists {
		switch plan.ConflictPolicy {
		case ConflictAsk:
			checkpoint.Phase = PhaseWaitingConflict
			checkpoint.Outcome = OutcomeWaitingConflict
			if err := worker.journal.Save(ctx, checkpoint); err != nil {
				return Result{}, err
			}
			return Result{Outcome: OutcomeWaitingConflict, Final: final, Bytes: checkpoint.Offset, SHA256: checkpoint.ChecksumHex, PartRetained: true}, nil
		case ConflictSkip:
			if err := destination.Remove(ctx, providerapi.RemoveRequest{Location: plan.Part, Expected: &partEntry.Fingerprint}); err != nil {
				return Result{}, err
			}
			checkpoint.Phase = PhaseCommitted
			checkpoint.Outcome = OutcomeSkipped
			checkpoint.Final = final
			if err := worker.journal.Save(ctx, checkpoint); err != nil {
				return Result{}, err
			}
			return Result{Outcome: OutcomeSkipped, Final: final, Bytes: checkpoint.Offset, SHA256: checkpoint.ChecksumHex}, nil
		case ConflictAutoRename:
			final, err = chooseAutoRename(ctx, destinationProvider, plan.DestinationDirectory, plan.RequestedName)
			if err != nil {
				return Result{}, err
			}
		}
	}
	checkpoint.Phase = PhaseCommitting
	checkpoint.Final = final
	if err := worker.journal.Save(ctx, checkpoint); err != nil {
		return Result{}, err
	}
	renameRequest := providerapi.RenameRequest{
		Source:         plan.Part,
		Destination:    final,
		Replace:        finalExists && plan.ConflictPolicy == ConflictOverwrite,
		ExpectedSource: &partEntry.Fingerprint,
	}
	if renameRequest.Replace {
		if plan.Version == 2 {
			expected := cloneFingerprint(plan.ExpectedDestination.Fingerprint)
			renameRequest.ExpectedDestination = &expected
		} else {
			renameRequest.ExpectedDestination = &finalEntry.Fingerprint
		}
	}
	_, renameErr := destination.Rename(ctx, renameRequest)
	if renameErr != nil {
		if plan.Version == 2 && plan.Origin == OriginSyncBack && (domain.IsCode(renameErr, domain.CodeConflict) || domain.IsCode(renameErr, domain.CodeAlreadyExists)) {
			checkpoint.Phase = PhaseWaitingConflict
			checkpoint.Outcome = OutcomeWaitingConflict
			if err := worker.journal.Save(ctx, checkpoint); err != nil {
				return Result{}, err
			}
			return Result{Outcome: OutcomeWaitingConflict, Final: final, Bytes: checkpoint.Offset, SHA256: checkpoint.ChecksumHex, PartRetained: true}, nil
		}
		proved, proofErr := worker.proveCommitted(ctx, plan, destinationProvider, final, checkpoint.ChecksumHex, buffer)
		if proofErr != nil || !proved {
			return Result{}, renameErr
		}
	}
	checksum, err := worker.verifyCommittedFile(ctx, plan, destinationProvider, final, checkpoint.ChecksumHex, buffer)
	if err != nil {
		return Result{}, err
	}
	if checksum != checkpoint.ChecksumHex {
		return Result{}, planError(domain.CodeConflict, "commit_copy", final, "committed final checksum differs from verified part", domain.RetryNever)
	}
	checkpoint.Phase = PhaseCommitted
	checkpoint.Outcome = OutcomeCompleted
	checkpoint.Final = final
	if err := worker.journal.Save(ctx, checkpoint); err != nil {
		return Result{}, err
	}
	_, partErr := destinationProvider.Stat(ctx, providerapi.StatRequest{Location: plan.Part})
	partRetained := partErr == nil
	result = Result{Outcome: OutcomeCompleted, Final: final, Bytes: checkpoint.Offset, SHA256: checkpoint.ChecksumHex, PartRetained: partRetained}
	if preserved {
		result.PreservedDestination = plan.PreservedDestination
	}
	return result, nil
}

func validateExecution(plan Plan) error {
	validSourceKind := plan.Source.Kind == domain.EntryFile || plan.Source.Kind == domain.EntryDirectory ||
		(plan.Kind == OperationDelete && plan.Source.Kind == domain.EntrySymlink)
	if (plan.Version != 1 && plan.Version != 2) || !validSourceKind ||
		plan.SourceEndpoint.ID != plan.Source.Location.EndpointID || plan.DestinationEndpoint.ID != plan.Part.EndpointID ||
		plan.Part.EndpointID == "" || plan.Final.EndpointID != plan.Part.EndpointID || plan.Part.Path == plan.Final.Path {
		return errors.New("execute transfer: invalid frozen plan")
	}
	if _, err := domain.ParseJobID(string(plan.JobID)); err != nil {
		return errors.New("execute transfer: invalid frozen Job ID")
	}
	if plan.Version == 1 {
		if plan.Origin != "" || plan.EditSessionID != "" || plan.ExpectedDestination != nil || plan.OriginalDestinationPrecondition != nil || plan.ExpectedSourceSHA256 != "" {
			return errors.New("execute transfer: Plan Version 1 contains sync-back fields")
		}
	} else if plan.Origin != OriginSyncBack || validateLowerHexIdentity(plan.EditSessionID, 32) != nil ||
		plan.Kind != OperationCopy || plan.Source.Kind != domain.EntryFile || plan.ConflictPolicy != ConflictOverwrite ||
		validateLowerHexIdentity(plan.ExpectedSourceSHA256, 64) != nil || !validDestinationPrecondition(plan.ExpectedDestination) || !validDestinationPrecondition(plan.OriginalDestinationPrecondition) ||
		plan.DestinationDirectory != (domain.Location{EndpointID: plan.Final.EndpointID, Path: domain.CanonicalPath(path.Dir(string(plan.Final.Path)))}) ||
		plan.Part != childLocation(plan.DestinationDirectory, "."+path.Base(string(plan.Final.Path))+".part-"+string(plan.JobID)) ||
		(plan.ExpectedDestination.Presence == DestinationPresent && plan.PreservedDestination != childLocation(plan.DestinationDirectory, "."+path.Base(string(plan.Final.Path))+".amsftp-original-"+string(plan.JobID))) ||
		(plan.ExpectedDestination.Presence == DestinationAbsent && plan.PreservedDestination.Path != "") {
		return errors.New("execute transfer: invalid sync-back Plan Version 2")
	}
	if plan.BufferBytes == 0 || plan.BufferBytes > 4*1024*1024 {
		return errors.New("execute transfer: buffer budget is outside 1..4MiB")
	}
	if plan.Verification != VerifySHA256 {
		return errors.New("execute transfer: unsupported verification")
	}
	if plan.Route != RouteLocal && plan.Route != RouteSFTPRelay && plan.Route != RouteHelperSameHost && plan.Route != RouteSFTPServerCopy && plan.Route != RouteLevel2Direct {
		return errors.New("execute transfer: unsupported route")
	}
	if !validRouteEvidence(plan) {
		return errors.New("execute transfer: invalid frozen route evidence")
	}
	if plan.Route == RouteHelperSameHost {
		if plan.Version != 1 || plan.Kind != OperationCopy || plan.Source.Kind != domain.EntryFile ||
			plan.SourceEndpoint.ID != plan.DestinationEndpoint.ID || plan.SourceEndpoint.Kind != domain.EndpointSSH ||
			plan.DestinationEndpoint.Kind != domain.EndpointSSH || plan.SameHostCopy == nil ||
			!validSameHostCopyBinding(*plan.SameHostCopy, plan.SourceEndpoint.ID, plan.Source.Fingerprint) {
			return errors.New("execute transfer: invalid Helper same-host route")
		}
	} else if plan.SameHostCopy != nil {
		return errors.New("execute transfer: unexpected Helper same-host binding")
	}
	if plan.Route == RouteSFTPServerCopy {
		if plan.Version != 1 || plan.Kind != OperationCopy || plan.Source.Kind != domain.EntryFile ||
			plan.SourceEndpoint.ID != plan.DestinationEndpoint.ID || plan.SourceEndpoint.Kind != domain.EndpointSSH ||
			plan.DestinationEndpoint.Kind != domain.EndpointSSH || plan.ServerCopy == nil ||
			!validServerCopyBinding(*plan.ServerCopy, plan) {
			return errors.New("execute transfer: invalid server-copy route")
		}
	} else if plan.ServerCopy != nil {
		return errors.New("execute transfer: unexpected server-copy binding")
	}
	if plan.Level2Preflight != nil {
		if !plan.DirectPolicy.enabled() || plan.Version != 1 || plan.Source.Kind != domain.EntryFile ||
			plan.SourceEndpoint.ID == plan.DestinationEndpoint.ID || plan.SourceEndpoint.Kind != domain.EndpointSSH ||
			plan.DestinationEndpoint.Kind != domain.EndpointSSH || !validLevel2PreflightBinding(*plan.Level2Preflight, plan) {
			return errors.New("execute transfer: invalid Level 2 preflight binding")
		}
	} else if plan.Route == RouteLevel2Direct {
		return errors.New("execute transfer: Level 2 route lacks preflight binding")
	}
	if plan.Route == RouteLevel2Direct && plan.Level2Preflight.Outcome != Level2PreflightPassed {
		return errors.New("execute transfer: Level 2 route lacks passing preflight evidence")
	}
	if plan.Source.Kind == domain.EntryFile && plan.Discovery != nil {
		return errors.New("execute transfer: file plan has a directory discovery budget")
	}
	if plan.Source.Kind == domain.EntryDirectory {
		if plan.Discovery == nil || plan.Discovery.QueueItems < 1 || plan.Discovery.QueueItems > 4096 ||
			plan.Discovery.PageItems < 1 || plan.Discovery.PageItems > 4096 ||
			plan.Discovery.MaxDepth < 1 || plan.Discovery.MaxDepth > 256 {
			return errors.New("execute transfer: directory discovery budget is invalid")
		}
	}
	if plan.Kind == OperationMove {
		if plan.MoveStrategy != "" && plan.MoveStrategy != MoveCopyDelete && plan.MoveStrategy != MoveAtomicRename {
			return errors.New("execute transfer: move strategy is invalid")
		}
		if plan.MoveStrategy == MoveAtomicRename && (plan.MoveCapability == nil || plan.SourceEndpoint.ID != plan.DestinationEndpoint.ID) {
			return errors.New("execute transfer: atomic move capability is invalid")
		}
		if plan.MoveStrategy == MoveCopyDelete && plan.SourceDeleteCapability == nil {
			return errors.New("execute transfer: move source deletion capability is missing")
		}
	}
	if plan.Kind == OperationDelete {
		if plan.DeleteIrreversible == plan.DeleteTrash || plan.DeleteTrash && plan.DeleteCapability == nil ||
			plan.Source.Location.Path == "/" || plan.Final != plan.Source.Location ||
			plan.SourceEndpoint.ID != plan.DestinationEndpoint.ID {
			return errors.New("execute transfer: delete plan is invalid")
		}
	} else if plan.Kind != OperationCopy && plan.Kind != OperationMove {
		return errors.New("execute transfer: operation kind is invalid")
	}
	return nil
}

func destinationPreconditionMatches(expected *DestinationPrecondition, exists bool, entry domain.Entry) bool {
	if expected == nil {
		return false
	}
	if expected.Presence == DestinationAbsent {
		return !exists
	}
	return exists && expected.Presence == DestinationPresent && entry.Kind == expected.Kind && reflect.DeepEqual(entry.Fingerprint, expected.Fingerprint)
}

func validDestinationPrecondition(expected *DestinationPrecondition) bool {
	if expected == nil {
		return false
	}
	switch expected.Presence {
	case DestinationAbsent:
		return expected.Kind == "" && expected.Fingerprint.Strength() == domain.FingerprintWeak && expected.ContentSHA256 == ""
	case DestinationPresent:
		return expected.Kind == domain.EntryFile && expected.Fingerprint.Strength() != domain.FingerprintWeak &&
			expected.Fingerprint.Size != nil && *expected.Fingerprint.Size <= 2*1024*1024*1024 &&
			validateLowerHexIdentity(string(expected.ContentSHA256), 64) == nil
	default:
		return false
	}
}

func validateLowerHexIdentity(value string, length int) error {
	if len(value) != length {
		return errors.New("invalid lowercase hex identity")
	}
	for _, character := range value {
		if character < '0' || character > '9' && character < 'a' || character > 'f' {
			return errors.New("invalid lowercase hex identity")
		}
	}
	return nil
}

func requireFrozenCapability(ctx context.Context, implementation providerapi.Provider, location domain.Location, binding CapabilityBinding) error {
	snapshot, err := implementation.Snapshot(ctx)
	if err != nil {
		return err
	}
	if !snapshot.Capabilities.Complete || snapshot.Capabilities.Revision != binding.Revision {
		return planError(domain.CodeCapabilityLost, "execute_copy", location, "capability revision changed after planning", domain.RetryAfterReplan)
	}
	current, ok := snapshot.Capabilities.Lookup(binding.Capability.Name)
	if !ok || !reflect.DeepEqual(current, binding.Capability) {
		return planError(domain.CodeCapabilityLost, "execute_copy", location, "frozen capability changed after planning", domain.RetryAfterReplan)
	}
	return nil
}

func writeAll(ctx context.Context, handle providerapi.WriteHandle, data []byte) error {
	for len(data) != 0 {
		n, err := handle.Write(ctx, data)
		if n < 0 || n > len(data) {
			return errors.New("execute transfer: provider returned invalid write count")
		}
		data = data[n:]
		if err != nil {
			return err
		}
		if n == 0 {
			return errors.New("execute transfer: destination write made no progress")
		}
	}
	return nil
}

func verifyFile(ctx context.Context, implementation providerapi.Provider, location domain.Location, expected domain.Fingerprint, buffer []byte) (string, error) {
	request := providerapi.OpenReadRequest{Location: location}
	if expected.Strength() != domain.FingerprintWeak {
		request.ExpectedFingerprint = &expected
	}
	handle, err := implementation.OpenRead(ctx, request)
	if err != nil {
		return "", err
	}
	defer func() { _ = handle.Close(context.Background()) }()
	hasher := sha256.New()
	for {
		n, readErr := handle.Read(ctx, buffer)
		if n < 0 || n > len(buffer) {
			return "", errors.New("verify transfer: provider returned invalid read count")
		}
		if n > 0 {
			if _, err := hasher.Write(buffer[:n]); err != nil {
				return "", err
			}
		}
		if readErr != nil {
			if !errors.Is(readErr, io.EOF) {
				return "", readErr
			}
			break
		}
		if n == 0 {
			return "", errors.New("verify transfer: read made no progress")
		}
	}
	if err := handle.Close(ctx); err != nil {
		return "", err
	}
	return hex.EncodeToString(hasher.Sum(nil)), nil
}

func proveCommitted(ctx context.Context, implementation providerapi.Provider, final domain.Location, checksum string, buffer []byte) (bool, error) {
	actual, err := verifyFile(ctx, implementation, final, domain.Fingerprint{}, buffer)
	if err != nil {
		if domain.IsCode(err, domain.CodeNotFound) {
			return false, nil
		}
		return false, err
	}
	return actual == checksum, nil
}

func (worker *Worker) verifyCommittedFile(ctx context.Context, plan Plan, implementation providerapi.Provider, location domain.Location, checksum string, buffer []byte) (string, error) {
	if plan.Route == RouteLevel2Direct {
		if plan.Level2Preflight == nil || plan.Level2Preflight.Result == nil {
			return "", errors.New("verify committed direct: preflight evidence is absent")
		}
		return worker.verifyLevel2(ctx, plan, location, plan.Level2Preflight.Result.SourceSize, checksum)
	}
	return verifyFile(ctx, implementation, location, domain.Fingerprint{}, buffer)
}

func (worker *Worker) proveCommitted(ctx context.Context, plan Plan, implementation providerapi.Provider, final domain.Location, checksum string, buffer []byte) (bool, error) {
	actual, err := worker.verifyCommittedFile(ctx, plan, implementation, final, checksum, buffer)
	if err != nil {
		if domain.IsCode(err, domain.CodeNotFound) {
			return false, nil
		}
		return false, err
	}
	return actual == checksum, nil
}

func marshalChecksum(hasher hash.Hash) ([]byte, error) {
	marshaler, ok := hasher.(encoding.BinaryMarshaler)
	if !ok {
		return nil, errors.New("execute transfer: SHA-256 state is not serializable")
	}
	state, err := marshaler.MarshalBinary()
	if err != nil {
		return nil, fmt.Errorf("execute transfer: marshal SHA-256 checkpoint: %w", err)
	}
	return state, nil
}

func unmarshalChecksum(hasher hash.Hash, state []byte) error {
	unmarshaler, ok := hasher.(encoding.BinaryUnmarshaler)
	if !ok {
		return errors.New("execute transfer: SHA-256 state is not restorable")
	}
	if err := unmarshaler.UnmarshalBinary(state); err != nil {
		return fmt.Errorf("execute transfer: restore SHA-256 checkpoint: %w", err)
	}
	return nil
}

func cloneCheckpoint(checkpoint Checkpoint) Checkpoint {
	checkpoint.SourceFingerprint = cloneFingerprint(checkpoint.SourceFingerprint)
	checkpoint.PartFingerprint = cloneFingerprint(checkpoint.PartFingerprint)
	checkpoint.ChecksumState = append([]byte(nil), checkpoint.ChecksumState...)
	return checkpoint
}

func checkedProviderOffset(offset uint64) (int64, error) {
	if offset > math.MaxInt64 {
		return 0, errors.New("execute transfer: checkpoint offset exceeds provider range")
	}
	return int64(offset), nil // #nosec G115 -- the MaxInt64 bound is checked immediately above.
}
