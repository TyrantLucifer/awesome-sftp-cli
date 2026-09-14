package transfer

// StreamPolicy freezes independent durability and directory scheduling budgets.
// A zero policy denotes a legacy plan whose checkpoint spans BufferBytes.
type StreamPolicy struct {
	CheckpointBytes      uint64 `json:"checkpoint_bytes,omitempty"`
	CheckpointIntervalMS uint32 `json:"checkpoint_interval_ms,omitempty"`
	DirectoryWorkers     uint32 `json:"directory_workers,omitempty"`
}

func DefaultStreamPolicy() StreamPolicy {
	return StreamPolicy{CheckpointBytes: 64 << 20, CheckpointIntervalMS: 1000, DirectoryWorkers: 2}
}

func (policy StreamPolicy) valid() bool {
	if policy == (StreamPolicy{}) {
		return true
	}
	return policy.CheckpointBytes > 0 && policy.CheckpointBytes <= 64<<20 &&
		policy.CheckpointIntervalMS >= 100 && policy.CheckpointIntervalMS <= 5000 &&
		policy.DirectoryWorkers >= 1 && policy.DirectoryWorkers <= 8
}
