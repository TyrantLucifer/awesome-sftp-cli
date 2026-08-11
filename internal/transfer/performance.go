package transfer

import (
	"math"
	"time"
)

// TransferPerformance is a bounded, path-free summary of relay stage time.
// Durations are cumulative monotonic wall-clock nanoseconds. Stages may overlap,
// so their sum is diagnostic evidence rather than total transfer elapsed time.
type TransferPerformance struct {
	Chunks                uint64 `json:"chunks"`
	ReadNanoseconds       uint64 `json:"read_nanoseconds"`
	WriteNanoseconds      uint64 `json:"write_nanoseconds"`
	SyncNanoseconds       uint64 `json:"sync_nanoseconds"`
	StatNanoseconds       uint64 `json:"stat_nanoseconds"`
	CheckpointNanoseconds uint64 `json:"checkpoint_nanoseconds"`
}

func addPerformanceDuration(target *uint64, elapsed time.Duration) {
	if target == nil || elapsed <= 0 {
		return
	}
	value := uint64(elapsed)
	if math.MaxUint64-*target < value {
		*target = math.MaxUint64
		return
	}
	*target += value
}

func cloneTransferPerformance(performance *TransferPerformance) *TransferPerformance {
	if performance == nil {
		return nil
	}
	cloned := *performance
	return &cloned
}

func mergeTransferPerformance(base, current *TransferPerformance) *TransferPerformance {
	if base == nil {
		return cloneTransferPerformance(current)
	}
	if current == nil {
		return cloneTransferPerformance(base)
	}
	return &TransferPerformance{
		Chunks:                saturatingAdd(base.Chunks, current.Chunks, ^uint64(0)),
		ReadNanoseconds:       saturatingAdd(base.ReadNanoseconds, current.ReadNanoseconds, ^uint64(0)),
		WriteNanoseconds:      saturatingAdd(base.WriteNanoseconds, current.WriteNanoseconds, ^uint64(0)),
		SyncNanoseconds:       saturatingAdd(base.SyncNanoseconds, current.SyncNanoseconds, ^uint64(0)),
		StatNanoseconds:       saturatingAdd(base.StatNanoseconds, current.StatNanoseconds, ^uint64(0)),
		CheckpointNanoseconds: saturatingAdd(base.CheckpointNanoseconds, current.CheckpointNanoseconds, ^uint64(0)),
	}
}
