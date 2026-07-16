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
	"reflect"

	"github.com/TyrantLucifer/awesome-mac-sftp/internal/domain"
	providerapi "github.com/TyrantLucifer/awesome-mac-sftp/internal/provider"
)

var (
	ErrPaused   = errors.New("transfer paused")
	ErrCanceled = errors.New("transfer canceled")
)

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
	OutcomeCompleted       Outcome = "completed"
	OutcomeSkipped         Outcome = "skipped"
	OutcomeWaitingConflict Outcome = "waiting_conflict"
)

type Checkpoint struct {
	JobID             domain.JobID       `json:"job_id"`
	Phase             Phase              `json:"phase"`
	Offset            uint64             `json:"offset"`
	SourceFingerprint domain.Fingerprint `json:"source_fingerprint"`
	Part              domain.Location    `json:"part"`
	PartFingerprint   domain.Fingerprint `json:"part_fingerprint"`
	ChecksumState     []byte             `json:"checksum_state,omitempty"`
	ChecksumHex       string             `json:"checksum_hex,omitempty"`
	Final             domain.Location    `json:"final"`
	Outcome           Outcome            `json:"outcome,omitempty"`
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
	Outcome      Outcome
	Final        domain.Location
	Bytes        uint64
	SHA256       string
	PartRetained bool
}

type Worker struct {
	resolver Resolver
	journal  Journal
}

func NewWorker(resolver Resolver, journal Journal) *Worker {
	return &Worker{resolver: resolver, journal: journal}
}

func (worker *Worker) Execute(ctx context.Context, plan Plan, control Control) (Result, error) {
	if err := validateExecution(plan); err != nil {
		return Result{}, err
	}
	if worker == nil || worker.resolver == nil || worker.journal == nil {
		return Result{}, errors.New("execute transfer: resolver and durable journal are required")
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
	if checkpoint != nil && checkpoint.Phase == PhaseCommitted {
		return Result{Outcome: checkpoint.Outcome, Final: checkpoint.Final, Bytes: checkpoint.Offset, SHA256: checkpoint.ChecksumHex}, nil
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
	}
	if checkpoint != nil {
		current = cloneCheckpoint(*checkpoint)
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
		if partEntry.Metadata.Size == nil || *partEntry.Metadata.Size != current.Offset ||
			!reflect.DeepEqual(partEntry.Fingerprint, current.PartFingerprint) {
			return Result{}, planError(domain.CodeConflict, "resume_copy", plan.Part, "part no longer matches durable checkpoint", domain.RetryAfterConflict)
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
		n, readErr := readHandle.Read(ctx, buffer)
		if n < 0 || n > len(buffer) {
			return Result{}, errors.New("execute transfer: provider returned invalid read count")
		}
		if n > 0 {
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

func (worker *Worker) commit(ctx context.Context, plan Plan, destinationProvider providerapi.Provider, destination providerapi.MutableProvider, checkpoint Checkpoint, buffer []byte) (Result, error) {
	partEntry, err := destinationProvider.Stat(ctx, providerapi.StatRequest{Location: plan.Part})
	if err != nil {
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
		renameRequest.ExpectedDestination = &finalEntry.Fingerprint
	}
	_, renameErr := destination.Rename(ctx, renameRequest)
	if renameErr != nil {
		proved, proofErr := proveCommitted(ctx, destinationProvider, final, checkpoint.ChecksumHex, buffer)
		if proofErr != nil || !proved {
			return Result{}, renameErr
		}
	}
	checksum, err := verifyFile(ctx, destinationProvider, final, domain.Fingerprint{}, buffer)
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
	return Result{Outcome: OutcomeCompleted, Final: final, Bytes: checkpoint.Offset, SHA256: checkpoint.ChecksumHex, PartRetained: partRetained}, nil
}

func validateExecution(plan Plan) error {
	if plan.Version != 1 || plan.JobID == "" || plan.Source.Kind != domain.EntryFile ||
		plan.SourceEndpoint.ID != plan.Source.Location.EndpointID || plan.DestinationEndpoint.ID != plan.Part.EndpointID ||
		plan.Part.EndpointID == "" || plan.Final.EndpointID != plan.Part.EndpointID || plan.Part.Path == plan.Final.Path {
		return errors.New("execute transfer: invalid frozen plan")
	}
	if plan.BufferBytes == 0 || plan.BufferBytes > 4*1024*1024 {
		return errors.New("execute transfer: buffer budget is outside 1..4MiB")
	}
	if plan.Verification != VerifySHA256 {
		return errors.New("execute transfer: unsupported verification")
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
