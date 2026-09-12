package testkit

import (
	"sync"
	"testing"
	"time"

	"github.com/TyrantLucifer/awesome-sftp-cli/internal/foundation"
)

var (
	_ foundation.Clock = (*ManualClock)(nil)
)

func TestManualClockStartsAtProvidedTime(t *testing.T) {
	start := time.Date(2026, time.July, 14, 12, 0, 0, 123, time.UTC)
	clock := NewManualClock(start)

	if got := clock.Now(); !got.Equal(start) {
		t.Fatalf("Now() = %v, want %v", got, start)
	}
}

func TestManualClockFiresTimersAtTheirDeadlines(t *testing.T) {
	start := time.Date(2026, time.July, 14, 12, 0, 0, 0, time.UTC)
	clock := NewManualClock(start)
	later := clock.NewTimer(2 * time.Second)
	earlier := clock.NewTimer(time.Second)
	sameDeadline := clock.NewTimer(time.Second)

	clock.Advance(time.Second)
	assertTimerFiredAt(t, earlier, start.Add(time.Second))
	assertTimerFiredAt(t, sameDeadline, start.Add(time.Second))
	assertTimerNotFired(t, later)

	clock.Advance(time.Second)
	assertTimerFiredAt(t, later, start.Add(2*time.Second))
}

func TestManualClockNonPositiveTimersFireImmediately(t *testing.T) {
	tests := []struct {
		name     string
		duration time.Duration
	}{
		{name: "zero", duration: 0},
		{name: "negative", duration: -time.Nanosecond},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			start := time.Unix(123, 456)
			clock := NewManualClock(start)
			timer := clock.NewTimer(test.duration)

			assertTimerFiredAt(t, timer, start)
			if timer.Stop() {
				t.Fatal("Stop() = true for an already-fired timer")
			}
		})
	}
}

func TestManualClockTimerCanBeStopped(t *testing.T) {
	clock := NewManualClock(time.Unix(0, 0))
	timer := clock.NewTimer(time.Second)

	if !timer.Stop() {
		t.Fatal("first Stop() = false, want true")
	}
	if timer.Stop() {
		t.Fatal("second Stop() = true, want false")
	}

	clock.Advance(time.Second)
	assertTimerNotFired(t, timer)
}

func TestManualClockTimerFiresOnlyOnce(t *testing.T) {
	start := time.Unix(0, 0)
	clock := NewManualClock(start)
	timer := clock.NewTimer(time.Second)

	clock.Advance(time.Second)
	assertTimerFiredAt(t, timer, start.Add(time.Second))
	if timer.Stop() {
		t.Fatal("Stop() = true after timer fired")
	}

	clock.Advance(time.Hour)
	assertTimerNotFired(t, timer)
}

func TestManualClockAdvanceZeroKeepsCurrentTime(t *testing.T) {
	start := time.Unix(123, 456)
	clock := NewManualClock(start)

	clock.Advance(0)
	if got := clock.Now(); !got.Equal(start) {
		t.Fatalf("Now() = %v after Advance(0), want %v", got, start)
	}
}

func TestManualClockAdvanceRejectsNegativeDuration(t *testing.T) {
	clock := NewManualClock(time.Unix(0, 0))

	defer func() {
		if recover() == nil {
			t.Fatal("Advance() did not panic for a negative duration")
		}
	}()
	clock.Advance(-time.Nanosecond)
}

func TestManualClockConcurrentAccess(t *testing.T) {
	clock := NewManualClock(time.Unix(0, 0))
	start := make(chan struct{})
	var workers sync.WaitGroup

	for index := 0; index < 32; index++ {
		workers.Add(1)
		go func(index int) {
			defer workers.Done()
			<-start
			_ = clock.Now()
			timer := clock.NewTimer(time.Duration(index+1) * time.Second)
			if index%2 == 0 {
				timer.Stop()
			}
		}(index)
	}
	workers.Add(1)
	go func() {
		defer workers.Done()
		<-start
		clock.Advance(time.Second)
	}()

	close(start)
	workers.Wait()
}

func assertTimerFiredAt(t *testing.T, timer foundation.Timer, want time.Time) {
	t.Helper()

	select {
	case got := <-timer.C():
		if !got.Equal(want) {
			t.Fatalf("timer fired at %v, want %v", got, want)
		}
	default:
		t.Fatal("timer did not fire")
	}
}

func assertTimerNotFired(t *testing.T, timer foundation.Timer) {
	t.Helper()

	select {
	case got := <-timer.C():
		t.Fatalf("timer fired unexpectedly at %v", got)
	default:
	}
}
