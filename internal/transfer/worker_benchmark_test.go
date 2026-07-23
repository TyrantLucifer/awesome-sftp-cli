package transfer

import (
	"context"
	"testing"
	"time"

	providerapi "github.com/TyrantLucifer/awesome-sftp-cli/internal/provider"
)

const (
	benchmarkRelayChunkBytes = DefaultBufferBytes
	benchmarkRelayChunks     = 16
	benchmarkRelayLatency    = 2 * time.Millisecond
)

func BenchmarkWorkerDurableRelayPipeline(b *testing.B) {
	data := make([]byte, benchmarkRelayChunkBytes*benchmarkRelayChunks)
	for index := range data {
		data[index] = byte(index)
	}

	b.ReportAllocs()
	b.SetBytes(int64(len(data)))
	b.ReportMetric(benchmarkRelayChunks, "checkpoints/op")
	for range b.N {
		b.StopTimer()
		fixture := newWorkerFixture(b, data, ConflictAsk)
		fixture.plan.BufferBytes = benchmarkRelayChunkBytes
		fixture.resolver[fixture.source.Descriptor().ID] = &latencyReadProvider{
			Provider: fixture.source,
			delay:    benchmarkRelayLatency,
		}
		journal := &latencyJournal{
			memoryJournal: newMemoryJournal(),
			delay:         benchmarkRelayLatency,
		}
		b.StartTimer()

		result, err := NewWorker(fixture.resolver, journal).Execute(context.Background(), fixture.plan, nil)
		if err != nil {
			b.Fatalf("Execute(): %v", err)
		}
		if result.Outcome != OutcomeCompleted || result.Bytes != uint64(len(data)) {
			b.Fatalf("result = %#v", result)
		}
	}
}

type latencyReadProvider struct {
	providerapi.Provider
	delay time.Duration
}

func (provider *latencyReadProvider) OpenRead(ctx context.Context, request providerapi.OpenReadRequest) (providerapi.ReadHandle, error) {
	handle, err := provider.Provider.OpenRead(ctx, request)
	if err != nil {
		return nil, err
	}
	return &latencyReadHandle{ReadHandle: handle, delay: provider.delay}, nil
}

type latencyReadHandle struct {
	providerapi.ReadHandle
	delay time.Duration
}

func (handle *latencyReadHandle) Read(ctx context.Context, buffer []byte) (int, error) {
	timer := time.NewTimer(handle.delay)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return 0, ctx.Err()
	case <-timer.C:
		return handle.ReadHandle.Read(ctx, buffer)
	}
}

type latencyJournal struct {
	*memoryJournal
	delay time.Duration
}

func (journal *latencyJournal) Save(ctx context.Context, checkpoint Checkpoint) error {
	if checkpoint.Phase == PhaseStreaming && checkpoint.Offset > 0 {
		timer := time.NewTimer(journal.delay)
		defer timer.Stop()
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-timer.C:
		}
	}
	return journal.memoryJournal.Save(ctx, checkpoint)
}
