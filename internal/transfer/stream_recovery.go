package transfer

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"reflect"
	"time"

	"github.com/TyrantLucifer/awesome-sftp-cli/internal/domain"
	providerapi "github.com/TyrantLucifer/awesome-sftp-cli/internal/provider"
)

// recoverStreamSuffix proves both the durable prefix and the uncheckpointed
// tail before rolling the exact opened part back. No unproved bytes are removed.
func (worker *Worker) recoverStreamSuffix(ctx context.Context, plan Plan, source providerapi.Provider, destinationProvider providerapi.Provider, destination providerapi.MutableProvider, current *Checkpoint, entry domain.Entry, expectedPrefix string, buffer []byte) (restoredEntry domain.Entry, recoveryErr error) {
	ctx, finishControl := controlledStreamContext(ctx, worker.control, cloneCheckpoint(*current))
	defer func() { recoveryErr = finishControl(recoveryErr) }()
	conflict := func() error {
		return planError(domain.CodeConflict, "resume_copy", plan.Part, "part suffix does not match the frozen source", domain.RetryAfterConflict)
	}
	if entry.Metadata.Size == nil || *entry.Metadata.Size < current.Offset || plan.StreamPolicy.CheckpointBytes == 0 {
		return domain.Entry{}, conflict()
	}
	size := *entry.Metadata.Size
	if size-current.Offset > plan.StreamPolicy.CheckpointBytes || plan.Source.Fingerprint.Size == nil || size > *plan.Source.Fingerprint.Size {
		return domain.Entry{}, conflict()
	}
	if len(buffer) < 2 {
		return domain.Entry{}, errors.New("resume stream: comparison buffer is too small")
	}
	part, err := destinationProvider.OpenRead(ctx, providerapi.OpenReadRequest{Location: plan.Part, ExpectedFingerprint: &entry.Fingerprint})
	if err != nil {
		return domain.Entry{}, err
	}
	defer part.Close(context.Background())
	partStream, partAdmission := worker.verificationReader(ctx, plan, destinationProvider, part)
	var sourceStream *sourceStream
	var sourceAdmission *streamAdmission
	started := time.Now()
	recorded := false
	recordReadback := func() {
		if recorded {
			return
		}
		recorded = true
		if current.Performance == nil {
			current.Performance = &TransferPerformance{}
		}
		addPerformanceDuration(&current.Performance.VerifyNanoseconds, time.Since(started))
		verified := partStream.readBytes
		scheduled := partAdmission.bytes.Load()
		waited := partAdmission.waited.Load()
		if sourceStream != nil {
			verified += sourceStream.readBytes
			scheduled += sourceAdmission.bytes.Load()
			waited += sourceAdmission.waited.Load()
		}
		current.Performance.VerifiedBytes = saturatingAdd(current.Performance.VerifiedBytes, verified, ^uint64(0))
		current.Performance.ScheduledBytes = saturatingAdd(current.Performance.ScheduledBytes, scheduled, ^uint64(0))
		current.Performance.SchedulerNanoseconds = saturatingAdd(current.Performance.SchedulerNanoseconds, waited, ^uint64(0))
	}
	defer recordReadback()
	prefix := sha256.New()
	for remaining := current.Offset; remaining > 0; {
		count := min(uint64(len(buffer)), remaining)
		n, err := partStream.Read(buffer[:count])
		if n < 0 || uint64(n) > count {
			return domain.Entry{}, conflict()
		}
		if n > 0 {
			_, _ = prefix.Write(buffer[:n])
			remaining -= uint64(n)
		}
		if err != nil && (!errors.Is(err, io.EOF) || remaining != 0) {
			return domain.Entry{}, err
		}
		if n == 0 {
			return domain.Entry{}, conflict()
		}
	}
	if hex.EncodeToString(prefix.Sum(nil)) != expectedPrefix {
		return domain.Entry{}, conflict()
	}
	offset, err := checkedProviderOffset(current.Offset)
	if err != nil {
		return domain.Entry{}, err
	}
	limit, err := checkedProviderOffset(size - current.Offset)
	if err != nil {
		return domain.Entry{}, err
	}
	original, err := source.OpenRead(ctx, providerapi.OpenReadRequest{Location: plan.Source.Location, Offset: offset, Limit: &limit, ExpectedFingerprint: &plan.Source.Fingerprint})
	if err != nil {
		return domain.Entry{}, err
	}
	defer original.Close(context.Background())
	sourceStream, sourceAdmission = worker.verificationReader(ctx, plan, source, original)
	left := buffer[:len(buffer)/2]
	right := buffer[len(buffer)/2 : 2*(len(buffer)/2)]
	for remaining := size - current.Offset; remaining > 0; {
		count := min(uint64(len(left)), remaining)
		if err := readExactStream(partStream, left[:count]); err != nil {
			return domain.Entry{}, err
		}
		if err := readExactStream(sourceStream, right[:count]); err != nil {
			return domain.Entry{}, err
		}
		if !bytes.Equal(left[:count], right[:count]) {
			return domain.Entry{}, conflict()
		}
		remaining -= count
	}
	if err := part.Close(ctx); err != nil {
		return domain.Entry{}, err
	}
	if err := original.Close(ctx); err != nil {
		return domain.Entry{}, err
	}
	sourceNow, err := source.Stat(ctx, providerapi.StatRequest{Location: plan.Source.Location})
	if err != nil {
		return domain.Entry{}, err
	}
	if !reflect.DeepEqual(sourceNow.Fingerprint, plan.Source.Fingerprint) {
		return domain.Entry{}, conflict()
	}
	recordReadback()
	handle, err := destination.OpenWrite(ctx, providerapi.OpenWriteRequest{Location: plan.Part, Offset: offset, Disposition: providerapi.WriteResumeExisting, ExpectedFingerprint: &entry.Fingerprint})
	if err != nil {
		return domain.Entry{}, err
	}
	defer handle.Close(context.Background())
	truncator, ok := handle.(providerapi.TruncatingWriteHandle)
	if !ok {
		return domain.Entry{}, planError(domain.CodeUnsupported, "resume_copy", plan.Part, "provider cannot restore a proven durable prefix", domain.RetryAfterReplan)
	}
	if err := truncator.Truncate(ctx, offset); err != nil {
		return domain.Entry{}, err
	}
	if err := handle.Sync(ctx); err != nil {
		return domain.Entry{}, err
	}
	if err := handle.Close(ctx); err != nil {
		return domain.Entry{}, err
	}
	restored, err := destinationProvider.Stat(ctx, providerapi.StatRequest{Location: plan.Part})
	if err != nil {
		return domain.Entry{}, err
	}
	if restored.Metadata.Size == nil || *restored.Metadata.Size != current.Offset {
		return domain.Entry{}, conflict()
	}
	current.PartFingerprint = cloneFingerprint(restored.Fingerprint)
	current.recordDurable()
	if err := worker.journal.Save(ctx, *current); err != nil {
		return domain.Entry{}, err
	}
	return restored, nil
}

func readExactStream(reader io.Reader, buffer []byte) error {
	for len(buffer) > 0 {
		n, err := reader.Read(buffer)
		if n < 0 || n > len(buffer) {
			return errors.New("read transfer: invalid byte count")
		}
		buffer = buffer[n:]
		if err != nil {
			if errors.Is(err, io.EOF) && len(buffer) == 0 {
				return nil
			}
			return err
		}
		if n == 0 {
			return io.ErrNoProgress
		}
	}
	return nil
}
