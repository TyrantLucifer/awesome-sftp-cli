package foundation

import (
	"sort"
	"sync"
	"time"
)

type ManualClock struct {
	mu           sync.Mutex
	now          time.Time
	nextSequence uint64
	timers       []*manualTimer
}

type manualTimerState uint8

const (
	manualTimerActive manualTimerState = iota
	manualTimerFired
	manualTimerStopped
)

type manualTimer struct {
	clock    *ManualClock
	channel  chan time.Time
	deadline time.Time
	sequence uint64
	state    manualTimerState
}

func NewManualClock(start time.Time) *ManualClock {
	return &ManualClock{now: start}
}

func (c *ManualClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.now
}

func (c *ManualClock) NewTimer(duration time.Duration) Timer {
	c.mu.Lock()
	deadline := c.now.Add(duration)
	timer := &manualTimer{
		clock:    c,
		channel:  make(chan time.Time, 1),
		deadline: deadline,
		sequence: c.nextSequence,
		state:    manualTimerActive,
	}
	c.nextSequence++

	if duration <= 0 {
		timer.deadline = c.now
		timer.state = manualTimerFired
		c.mu.Unlock()
		timer.channel <- timer.deadline
		return timer
	}

	insertAt := sort.Search(len(c.timers), func(index int) bool {
		candidate := c.timers[index]
		if candidate.deadline.After(timer.deadline) {
			return true
		}
		return candidate.deadline.Equal(timer.deadline) && candidate.sequence > timer.sequence
	})
	c.timers = append(c.timers, nil)
	copy(c.timers[insertAt+1:], c.timers[insertAt:])
	c.timers[insertAt] = timer
	c.mu.Unlock()
	return timer
}

// NextTimerDeadline exposes the earliest registered timer so tests can avoid
// advancing the manual clock before a concurrent waiter has armed its timer.
func (c *ManualClock) NextTimerDeadline() (time.Time, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if len(c.timers) == 0 {
		return time.Time{}, false
	}
	return c.timers[0].deadline, true
}

func (c *ManualClock) Advance(duration time.Duration) {
	if duration < 0 {
		panic("foundation.ManualClock.Advance: negative duration")
	}

	c.mu.Lock()
	c.now = c.now.Add(duration)
	dueCount := sort.Search(len(c.timers), func(index int) bool {
		return c.timers[index].deadline.After(c.now)
	})
	due := append([]*manualTimer(nil), c.timers[:dueCount]...)
	remaining := append([]*manualTimer(nil), c.timers[dueCount:]...)
	c.timers = remaining
	for _, timer := range due {
		timer.state = manualTimerFired
	}
	c.mu.Unlock()

	for _, timer := range due {
		timer.channel <- timer.deadline
	}
}

func (t *manualTimer) C() <-chan time.Time {
	return t.channel
}

func (t *manualTimer) Stop() bool {
	t.clock.mu.Lock()
	defer t.clock.mu.Unlock()

	if t.state != manualTimerActive {
		return false
	}
	t.state = manualTimerStopped
	for index, timer := range t.clock.timers {
		if timer != t {
			continue
		}
		copy(t.clock.timers[index:], t.clock.timers[index+1:])
		last := len(t.clock.timers) - 1
		t.clock.timers[last] = nil
		t.clock.timers = t.clock.timers[:last]
		break
	}
	return true
}
