package cache

import (
	"testing"
	"time"
)

func TestLeaseHeartbeatUsesInjectedClock(t *testing.T) {
	t.Parallel()

	start := time.Date(2026, 7, 16, 9, 0, 0, 0, time.UTC)
	clock := &manualClock{now: start}
	manager, err := NewLeaseManager(clock, nil, 2*time.Minute, 15*time.Minute)
	if err != nil {
		t.Fatalf("new lease manager: %v", err)
	}
	lease := validLease(start)
	clock.Advance(30 * time.Second)

	heartbeated, err := manager.Heartbeat(lease)
	if err != nil {
		t.Fatalf("heartbeat: %v", err)
	}
	if !heartbeated.HeartbeatAt.Equal(clock.Now()) {
		t.Fatalf("heartbeat time = %v, want %v", heartbeated.HeartbeatAt, clock.Now())
	}
	if !heartbeated.ExpiresAt.Equal(clock.Now().Add(2 * time.Minute)) {
		t.Fatalf("expiry = %v", heartbeated.ExpiresAt)
	}
	if !heartbeated.GraceUntil.Equal(clock.Now().Add(17 * time.Minute)) {
		t.Fatalf("grace deadline = %v", heartbeated.GraceUntil)
	}
}

func TestLeaseHeartbeatPreservesOwnerSpecificGrace(t *testing.T) {
	t.Parallel()

	start := time.Date(2026, 7, 16, 9, 0, 0, 0, time.UTC)
	clock := &manualClock{now: start.Add(30 * time.Second)}
	manager, err := NewLeaseManager(clock, nil, 2*time.Minute, 15*time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	editor := validLease(start)
	editor.OwnerKind = LeaseOwnerEditor
	editor.GraceUntil = editor.ExpiresAt
	opener := validLease(start)

	editor, err = manager.Heartbeat(editor)
	if err != nil {
		t.Fatal(err)
	}
	opener, err = manager.Heartbeat(opener)
	if err != nil {
		t.Fatal(err)
	}
	if !editor.GraceUntil.Equal(editor.ExpiresAt) {
		t.Fatalf("editor heartbeat acquired opener grace: expiry=%v grace=%v", editor.ExpiresAt, editor.GraceUntil)
	}
	if got := opener.GraceUntil.Sub(opener.ExpiresAt); got != 15*time.Minute {
		t.Fatalf("opener grace = %v", got)
	}
}

func TestLeaseClassificationRequiresPIDAndBirthIdentity(t *testing.T) {
	t.Parallel()

	start := time.Date(2026, 7, 16, 9, 0, 0, 0, time.UTC)
	identity := ProcessIdentity{PID: 4321, BirthID: "linux-start-ticks:91"}
	tests := []struct {
		name       string
		advance    time.Duration
		status     ProcessStatus
		protection LeaseProtection
	}{
		{name: "heartbeat window", advance: time.Minute, status: ProcessGone, protection: LeaseProtectedActive},
		{name: "grace window", advance: 2*time.Minute + time.Second, status: ProcessGone, protection: LeaseProtectedGrace},
		{name: "matching process after grace", advance: 18 * time.Minute, status: ProcessMatches, protection: LeaseProtectedLiveProcess},
		{name: "dead process after grace", advance: 18 * time.Minute, status: ProcessGone, protection: LeaseReclaimable},
		{name: "reused pid after grace", advance: 18 * time.Minute, status: ProcessBirthMismatch, protection: LeaseReclaimable},
		{name: "unqueryable identity after grace", advance: 18 * time.Minute, status: ProcessUncertain, protection: LeaseProtectedUncertain},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			clock := &manualClock{now: start}
			classifier := &fixedProcessClassifier{status: test.status}
			manager, err := NewLeaseManager(clock, classifier, 2*time.Minute, 15*time.Minute)
			if err != nil {
				t.Fatalf("new lease manager: %v", err)
			}
			lease := validLease(start)
			lease.Process = &identity
			clock.Advance(test.advance)

			if got := manager.Classify(lease); got != test.protection {
				t.Fatalf("classification = %q, want %q", got, test.protection)
			}
			if test.advance < 2*time.Minute && classifier.calls != 0 {
				t.Fatalf("process was queried during valid heartbeat window: %d calls", classifier.calls)
			}
		})
	}
}

func TestExpiredLeaseWithoutReliableProcessIdentityStaysUncertain(t *testing.T) {
	t.Parallel()

	start := time.Date(2026, 7, 16, 9, 0, 0, 0, time.UTC)
	clock := &manualClock{now: start.Add(20 * time.Minute)}
	manager, err := NewLeaseManager(clock, nil, 2*time.Minute, 15*time.Minute)
	if err != nil {
		t.Fatalf("new lease manager: %v", err)
	}
	lease := validLease(start)
	lease.Process = nil
	if got := manager.Classify(lease); got != LeaseProtectedUncertain {
		t.Fatalf("classification = %q, want uncertain", got)
	}
}

func TestReleasedLeaseIsImmediatelyReclaimable(t *testing.T) {
	t.Parallel()

	start := time.Date(2026, 7, 16, 9, 0, 0, 0, time.UTC)
	clock := &manualClock{now: start.Add(time.Second)}
	manager, err := NewLeaseManager(clock, nil, 2*time.Minute, 15*time.Minute)
	if err != nil {
		t.Fatalf("new lease manager: %v", err)
	}
	released, err := manager.Release(validLease(start))
	if err != nil {
		t.Fatalf("release: %v", err)
	}
	if got := manager.Classify(released); got != LeaseReclaimable {
		t.Fatalf("classification = %q, want reclaimable", got)
	}
}

func validLease(start time.Time) Lease {
	return Lease{
		ID: testLeaseID('a'), OwnerKind: LeaseOwnerOpener, OwnerID: "opener-1",
		DaemonInstanceID: "daemon-1", Target: BlobTarget(testBlobID('b')), State: LeaseActive,
		HeartbeatAt: start, ExpiresAt: start.Add(2 * time.Minute), GraceUntil: start.Add(17 * time.Minute),
	}
}

type manualClock struct {
	now time.Time
}

func (c *manualClock) Now() time.Time {
	return c.now
}

func (c *manualClock) Advance(duration time.Duration) {
	c.now = c.now.Add(duration)
}

type fixedProcessClassifier struct {
	status ProcessStatus
	calls  int
}

func (c *fixedProcessClassifier) Classify(ProcessIdentity) ProcessStatus {
	c.calls++
	return c.status
}
