package transfer

import (
	"context"
	"errors"
	"hash"
	"io"
	"reflect"
	"sync/atomic"
	"time"

	"github.com/TyrantLucifer/awesome-sftp-cli/internal/domain"
	providerapi "github.com/TyrantLucifer/awesome-sftp-cli/internal/provider"
)

const streamPacketBytes = 32 << 10

// streamAdmission accounts actual network requests; local copies are admitted
// once at the source. Callbacks can run concurrently in a Provider read window.
type streamAdmission struct {
	worker *Worker
	plan   Plan
	waited atomic.Uint64
	bytes  atomic.Uint64
}

func (a *streamAdmission) gate(endpoint domain.EndpointID) func(context.Context, uint32) error {
	return func(ctx context.Context, n uint32) error {
		if a.worker.scheduler == nil {
			return nil
		}
		start := time.Now()
		err := waitForScheduledBytes(ctx, a.worker.scheduler, BandwidthRequest{
			JobID: a.plan.JobID, EndpointID: endpoint, JobBytesPerSecond: a.plan.Bandwidth.JobBytesPerSecond, Class: ScheduleBulk,
		}, n)
		if elapsed := time.Since(start); elapsed > 0 {
			a.waited.Add(uint64(elapsed))
		}
		if err == nil {
			a.bytes.Add(uint64(n))
		}
		return err
	}
}

type sourceStream struct {
	ctx         context.Context
	handle      providerapi.ReadHandle
	digest      hash.Hash
	options     providerapi.ReadStreamOptions
	beforeRead  func(context.Context, uint32) error
	beforeWrite func(context.Context, uint32) error
	readAhead   bool
	readTime    time.Duration
	readBytes   uint64
	onRead      func(uint64) error
}

func (r *sourceStream) Read(buffer []byte) (int, error) {
	if err := r.ctx.Err(); err != nil {
		return 0, err
	}
	if len(buffer) == 0 {
		return 0, nil
	}
	packetBytes := uint32(streamPacketBytes)
	if r.options.MaxRequestBytes > 0 {
		packetBytes = min(packetBytes, r.options.MaxRequestBytes)
	}
	buffer = buffer[:min(len(buffer), int(packetBytes))]
	start := time.Now()
	var n int
	var err error
	if handle, ok := r.handle.(providerapi.StreamReadHandle); ok {
		n, err = handle.ReadStream(r.ctx, buffer, r.options)
	} else {
		if r.beforeRead != nil {
			if err = r.beforeRead(r.ctx, uint32(len(buffer))); err != nil { //nolint:gosec // buffer is capped to streamPacketBytes above.
				return 0, err
			}
		}
		if handle, ok := r.handle.(providerapi.ReadAheadHandle); ok && r.readAhead {
			n, err = handle.ReadAhead(r.ctx, buffer, r.options.MaxBytes)
		} else {
			n, err = r.handle.Read(r.ctx, buffer)
		}
	}
	r.readTime += time.Since(start)
	if n < 0 || n > len(buffer) {
		return 0, errors.New("stream transfer: invalid read count")
	}
	if n > 0 {
		if r.beforeWrite != nil {
			if err := r.beforeWrite(r.ctx, uint32(n)); err != nil { //nolint:gosec // n is positive and checked against the 32 KiB buffer.
				return 0, err
			}
		}
		r.readBytes += uint64(n)
		if r.digest != nil {
			if _, hashErr := r.digest.Write(buffer[:n]); hashErr != nil {
				return n, hashErr
			}
		}
		if r.onRead != nil {
			if reportErr := r.onRead(r.readBytes); reportErr != nil {
				return n, reportErr
			}
		}
	}
	if n == 0 && err == nil {
		return 0, io.ErrNoProgress
	}
	return n, err
}

type streamWriteAdapter struct {
	ctx    context.Context
	handle providerapi.WriteHandle
}

// checkpointReader ends an epoch without ending the underlying source stream.
// EOF here is a request to drain acknowledgements before taking a checkpoint.
type checkpointReader struct {
	reader    io.Reader
	remaining uint64
	deadline  time.Time
	started   bool
}

func (r *checkpointReader) Read(buffer []byte) (int, error) {
	if r.remaining == 0 || r.started && !r.deadline.IsZero() && !time.Now().Before(r.deadline) {
		return 0, io.EOF
	}
	if uint64(len(buffer)) > r.remaining {
		buffer = buffer[:r.remaining]
	}
	n, err := r.reader.Read(buffer)
	if n > 0 {
		r.remaining -= uint64(n)
		r.started = true
	}
	return n, err
}
func (w streamWriteAdapter) Write(buffer []byte) (int, error) {
	if err := writeAll(w.ctx, w.handle, buffer); err != nil {
		return 0, err
	}
	return len(buffer), nil
}

// copyStream separates packet admission from the durability boundary. The
// Provider owns wire concurrency; the coordinator alone advances checkpoints.
func (worker *Worker) copyStream(ctx context.Context, plan Plan, source providerapi.ReadHandle, destination providerapi.WriteHandle, destinationProvider providerapi.Provider, current *Checkpoint, digest hash.Hash, buffer []byte, control Control) error {
	ctx, cancelStream := context.WithCancel(ctx)
	defer cancelStream()
	if current.Performance == nil {
		current.Performance = &TransferPerformance{}
	}
	admission := &streamAdmission{worker: worker, plan: plan}
	streamStarted := time.Now()
	defer func() {
		addPerformanceDuration(&current.Performance.StreamingNanoseconds, time.Since(streamStarted))
		current.Performance.SchedulerNanoseconds = saturatingAdd(current.Performance.SchedulerNanoseconds, admission.waited.Load(), ^uint64(0))
		current.Performance.ScheduledBytes = saturatingAdd(current.Performance.ScheduledBytes, admission.bytes.Load(), ^uint64(0))
	}()
	reader := &sourceStream{ctx: ctx, handle: source, digest: digest, options: worker.streamReadOptions(plan, plan.Source.Fingerprint.Size)}
	if worker.scheduler != nil {
		reader.options.MaxRequestBytes = worker.scheduler.QuantumBytes()
	}
	baseOffset := current.Offset
	reader.onRead = func(consumed uint64) error {
		return reportProgress(ctx, worker.journal, TransferProgress{Phase: PhaseStreaming, Bytes: baseOffset + consumed, DurableBytes: current.durableBytes()})
	}
	if plan.SourceEndpoint.Kind == domain.EndpointSSH || plan.DestinationEndpoint.Kind != domain.EndpointSSH {
		reader.beforeRead = admission.gate(plan.SourceEndpoint.ID)
		reader.options.BeforeRead = reader.beforeRead
	}
	if plan.DestinationEndpoint.Kind == domain.EndpointSSH {
		reader.beforeWrite = admission.gate(plan.DestinationEndpoint.ID)
	}
	// Legacy read-ahead facets cannot admit individual requests, so use them only
	// without a rate limit. Scheduled facets keep their window in both cases.
	reader.readAhead = worker.scheduler == nil && !plan.Bandwidth.requiresControl()
	if policy, ok := worker.scheduler.(bandwidthReadAheadPolicy); ok {
		reader.readAhead = policy.AllowsReadAhead(BandwidthRequest{JobID: plan.JobID, EndpointID: plan.SourceEndpoint.ID, JobBytesPerSecond: plan.Bandwidth.JobBytesPerSecond})
	}
	var previousReadTime time.Duration
	for {
		if control != nil {
			switch control.Action(cloneCheckpoint(*current)) {
			case ControlPause:
				if err := worker.closeAndRefreshCheckpoint(ctx, destinationProvider, destination, current); err != nil {
					return err
				}
				return ErrPaused
			case ControlCancel:
				if err := worker.closeAndRefreshCheckpoint(ctx, destinationProvider, destination, current); err != nil {
					return err
				}
				return ErrCanceled
			}
		}
		chunk := uint64(plan.BufferBytes)
		var deadline time.Time
		if plan.StreamPolicy.CheckpointBytes != 0 {
			chunk = plan.StreamPolicy.CheckpointBytes
			deadline = time.Now().Add(time.Duration(plan.StreamPolicy.CheckpointIntervalMS) * time.Millisecond)
		}
		if size := plan.Source.Fingerprint.Size; size != nil {
			if current.Offset >= *size {
				break
			}
			chunk = min(chunk, *size-current.Offset)
		}
		limited := &checkpointReader{reader: reader, remaining: chunk, deadline: deadline}
		// Use one stream context across epochs so a Provider can retain its read
		// window. The observer sees only an immutable durable checkpoint and is
		// joined before the coordinator changes that checkpoint.
		stopControl := make(chan struct{})
		joinedControl := make(chan struct{})
		controlResult := make(chan ControlAction, 1)
		if control != nil && plan.StreamPolicy.CheckpointBytes != 0 {
			snapshot := cloneCheckpoint(*current)
			go func() {
				defer close(joinedControl)
				monitorStagedCopyControl(ctx, control, snapshot, cancelStream, controlResult, stopControl)
			}()
		} else {
			close(joinedControl)
		}
		consumedBefore := reader.readBytes
		started := time.Now()
		var n int64
		var err error
		if stream, ok := destination.(providerapi.WindowedStreamWriteHandle); ok && plan.Durability != "" {
			n, err = stream.WriteFromWindow(ctx, limited, streamWindowRequests(plan, plan.Source.Fingerprint.Size))
		} else if stream, ok := destination.(providerapi.StreamWriteHandle); ok && plan.Durability == "" {
			n, err = stream.WriteFrom(ctx, limited)
		} else {
			n, err = io.CopyBuffer(streamWriteAdapter{ctx: ctx, handle: destination}, limited, buffer[:min(len(buffer), streamPacketBytes)])
		}
		close(stopControl)
		<-joinedControl
		select {
		case action := <-controlResult:
			if action == ControlPause {
				return ErrPaused
			}
			return ErrCanceled
		default:
		}
		addPerformanceDuration(&current.Performance.WriteNanoseconds, time.Since(started))
		addPerformanceDuration(&current.Performance.ReadNanoseconds, reader.readTime-previousReadTime)
		previousReadTime = reader.readTime
		if err != nil {
			return err
		}
		if n < 0 || uint64(n) > chunk || uint64(n) != reader.readBytes-consumedBefore {
			return errors.New("stream transfer: invalid acknowledged count")
		}
		if n == 0 {
			break
		}
		if plan.checkpointSync() {
			started = time.Now()
			err = destination.Sync(ctx)
			addPerformanceDuration(&current.Performance.SyncNanoseconds, time.Since(started))
			if err != nil {
				return err
			}
		}
		next := current.Offset + uint64(n)
		if plan.checkpointSync() {
			started = time.Now()
			entry, err := destinationProvider.Stat(ctx, providerapi.StatRequest{Location: plan.Part})
			addPerformanceDuration(&current.Performance.StatNanoseconds, time.Since(started))
			if err != nil {
				return err
			}
			if entry.Metadata.Size == nil || *entry.Metadata.Size != next {
				return planError(domain.CodeConflict, "stream_copy", plan.Part, "part size does not match acknowledged offset", domain.RetryNever)
			}
			current.PartFingerprint = cloneFingerprint(entry.Fingerprint)
		}
		state, err := marshalChecksum(digest)
		if err != nil {
			return err
		}
		current.Offset = next
		if plan.checkpointSync() {
			current.recordDurable()
		}
		current.ChecksumState = state
		current.Performance.Chunks++
		started = time.Now()
		err = worker.journal.Save(ctx, *current)
		addPerformanceDuration(&current.Performance.CheckpointNanoseconds, time.Since(started))
		if err != nil {
			return err
		}
		if uint64(n) < chunk && (deadline.IsZero() || time.Now().Before(deadline)) {
			break
		}
	}
	if plan.Durability == DurabilityCompletion {
		started := time.Now()
		if err := destination.Sync(ctx); err != nil {
			return err
		}
		addPerformanceDuration(&current.Performance.SyncNanoseconds, time.Since(started))
		current.recordDurable()
		if err := worker.journal.Save(ctx, *current); err != nil {
			return err
		}
	}
	// Fixed-size streams never issue speculative reads past the frozen end. Check
	// the source again instead, so growth or replacement cannot pass unnoticed.
	if size := plan.Source.Fingerprint.Size; size != nil {
		provider, err := worker.resolver.Resolve(plan.Source.Location.EndpointID)
		if err != nil {
			return err
		}
		entry, err := provider.Stat(ctx, providerapi.StatRequest{Location: plan.Source.Location})
		if err != nil {
			return err
		}
		if !reflect.DeepEqual(entry.Fingerprint, plan.Source.Fingerprint) {
			return planError(domain.CodeConflict, "stream_copy", plan.Source.Location, "source changed during transfer", domain.RetryAfterConflict)
		}
	}
	return nil
}

// controlledStreamContext makes packet admission interruptible throughout a
// readback, including an otherwise arbitrarily long bandwidth wait.
func controlledStreamContext(ctx context.Context, control Control, checkpoint Checkpoint) (context.Context, func(error) error) {
	if control == nil {
		return ctx, func(err error) error { return err }
	}
	child, cancel := context.WithCancel(ctx)
	stop := make(chan struct{})
	joined := make(chan struct{})
	action := make(chan ControlAction, 1)
	go func() {
		defer close(joined)
		monitorStagedCopyControl(child, control, checkpoint, cancel, action, stop)
	}()
	return child, func(err error) error {
		close(stop)
		<-joined
		cancel()
		select {
		case requested := <-action:
			if requested == ControlPause {
				return ErrPaused
			}
			return ErrCanceled
		default:
			return err
		}
	}
}

// Once create succeeds, cancellation must not strand an unrecorded empty part.
// Finish the bounded initialization boundary before returning to cancellable
// streaming. Failed identity/durability checks still leave the part untouched.
func (worker *Worker) initializeCreatedPart(ctx context.Context, plan Plan, destination providerapi.Provider, handle providerapi.WriteHandle, checkpoint *Checkpoint, digest hash.Hash) error {
	initializationCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 30*time.Second)
	defer cancel()
	if err := syncInitialPart(initializationCtx, plan, handle); err != nil {
		return err
	}
	entry, err := destination.Stat(initializationCtx, providerapi.StatRequest{Location: checkpoint.Part})
	if err != nil {
		return err
	}
	if entry.Metadata.Size == nil || *entry.Metadata.Size != 0 {
		return planError(domain.CodeConflict, "initialize_part", checkpoint.Part, "created part is no longer empty", domain.RetryAfterConflict)
	}
	state, err := marshalChecksum(digest)
	if err != nil {
		return err
	}
	checkpoint.Phase = PhaseStreaming
	checkpoint.PartFingerprint = cloneFingerprint(entry.Fingerprint)
	checkpoint.ChecksumState = state
	return worker.journal.Save(initializationCtx, *checkpoint)
}
