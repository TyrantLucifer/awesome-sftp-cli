package transfer

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"io"
	"reflect"
	"time"

	"github.com/TyrantLucifer/awesome-sftp-cli/internal/domain"
	providerapi "github.com/TyrantLucifer/awesome-sftp-cli/internal/provider"
)

// verifyFile shares packet admission and the bounded read window with transfer
// data. A full digest is checked against a stable observation of the opened file.
func (worker *Worker) verifyFile(ctx context.Context, plan Plan, current *Checkpoint, implementation providerapi.Provider, location domain.Location, expected domain.Fingerprint, buffer []byte) (checksum string, verifyErr error) {
	checkpoint := Checkpoint{JobID: plan.JobID}
	if current != nil {
		checkpoint = cloneCheckpoint(*current)
	}
	ctx, finishControl := controlledStreamContext(ctx, worker.control, checkpoint)
	defer func() { verifyErr = finishControl(verifyErr) }()
	request := providerapi.OpenReadRequest{Location: location}
	if expected.Strength() != domain.FingerprintWeak {
		request.ExpectedFingerprint = &expected
	}
	handle, err := implementation.OpenRead(ctx, request)
	if err != nil {
		return "", err
	}
	defer handle.Close(context.Background())
	info := handle.Info()
	reader, admission := worker.verificationReader(ctx, plan, implementation, handle)
	started := time.Now()
	if current != nil {
		if current.Performance == nil {
			current.Performance = &TransferPerformance{}
		}
		prior := current.Performance.VerifiedBytes
		reader.onRead = func(n uint64) error {
			return reportProgress(ctx, worker.journal, TransferProgress{Phase: current.Phase, Bytes: current.Offset, DurableBytes: current.durableBytes(), VerifiedBytes: saturatingAdd(prior, n, ^uint64(0))})
		}
		defer func() {
			addPerformanceDuration(&current.Performance.VerifyNanoseconds, time.Since(started))
			current.Performance.VerifiedBytes = saturatingAdd(current.Performance.VerifiedBytes, reader.readBytes, ^uint64(0))
			current.Performance.SchedulerNanoseconds = saturatingAdd(current.Performance.SchedulerNanoseconds, admission.waited.Load(), ^uint64(0))
			current.Performance.ScheduledBytes = saturatingAdd(current.Performance.ScheduledBytes, admission.bytes.Load(), ^uint64(0))
		}()
	}
	var input io.Reader = reader
	if info.Fingerprint.Size != nil {
		size, err := checkedProviderOffset(*info.Fingerprint.Size)
		if err != nil {
			return "", err
		}
		input = io.LimitReader(reader, size)
	}
	digest := sha256.New()
	n, err := io.CopyBuffer(digest, input, buffer[:min(len(buffer), streamPacketBytes)])
	if err != nil {
		return "", err
	}
	if n < 0 {
		return "", io.ErrUnexpectedEOF
	}
	if info.Fingerprint.Size != nil && uint64(n) != *info.Fingerprint.Size {
		return "", planError(domain.CodeConflict, "verify_part", location, "verified file size changed", domain.RetryAfterConflict)
	}
	if err := handle.Close(ctx); err != nil {
		return "", err
	}
	latest, err := implementation.Stat(ctx, providerapi.StatRequest{Location: location})
	if err != nil {
		return "", err
	}
	if !reflect.DeepEqual(latest.Fingerprint, info.Fingerprint) {
		return "", planError(domain.CodeConflict, "verify_part", location, "file changed during verification", domain.RetryAfterConflict)
	}
	return hex.EncodeToString(digest.Sum(nil)), nil
}

func (worker *Worker) verificationReader(ctx context.Context, plan Plan, implementation providerapi.Provider, handle providerapi.ReadHandle) (*sourceStream, *streamAdmission) {
	admission := &streamAdmission{worker: worker, plan: plan}
	reader := &sourceStream{ctx: ctx, handle: handle, options: worker.streamReadOptions(plan, handle.Info().Fingerprint.Size)}
	if worker.scheduler != nil {
		reader.options.MaxRequestBytes = worker.scheduler.QuantumBytes()
	}
	if implementation.Descriptor().Kind == domain.EndpointSSH {
		reader.beforeRead = admission.gate(implementation.Descriptor().ID)
		reader.options.BeforeRead = reader.beforeRead
	}
	reader.readAhead = worker.scheduler == nil
	if policy, ok := worker.scheduler.(bandwidthReadAheadPolicy); ok {
		reader.readAhead = policy.AllowsReadAhead(BandwidthRequest{JobID: plan.JobID, EndpointID: implementation.Descriptor().ID, JobBytesPerSecond: plan.Bandwidth.JobBytesPerSecond})
	}
	return reader, admission
}
