package retrypolicy

import "time"

const (
	MaxReconnectDelay = 30 * time.Second
	DefaultJobDelay   = time.Minute
	MaxJobDelay       = 10 * time.Minute
)

// DefaultReconnectDelays returns a fresh copy of the bounded retry schedule
// used by client reconnects before Stage 6 made the schedule configurable.
func DefaultReconnectDelays() []time.Duration {
	return []time.Duration{100 * time.Millisecond, 250 * time.Millisecond, 500 * time.Millisecond}
}
