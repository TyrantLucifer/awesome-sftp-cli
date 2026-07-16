package cache

import (
	"fmt"
	"time"
)

const (
	DefaultLeaseHeartbeat = 30 * time.Second
	DefaultLeaseExpiry    = 2 * time.Minute
	DefaultOpenerGrace    = 15 * time.Minute
)

type Clock interface {
	Now() time.Time
}

type ProcessStatus string

const (
	ProcessMatches       ProcessStatus = "matches"
	ProcessGone          ProcessStatus = "gone"
	ProcessBirthMismatch ProcessStatus = "birth_mismatch"
	ProcessUncertain     ProcessStatus = "uncertain"
)

type ProcessClassifier interface {
	Classify(ProcessIdentity) ProcessStatus
}

type LeaseProtection string

const (
	LeaseProtectedActive      LeaseProtection = "active"
	LeaseProtectedGrace       LeaseProtection = "grace"
	LeaseProtectedLiveProcess LeaseProtection = "live_process"
	LeaseProtectedUncertain   LeaseProtection = "uncertain"
	LeaseReclaimable          LeaseProtection = "reclaimable"
)

type LeaseManager struct {
	clock     Clock
	processes ProcessClassifier
	expiry    time.Duration
	grace     time.Duration
}

func NewLeaseManager(clock Clock, processes ProcessClassifier, expiry time.Duration, grace time.Duration) (*LeaseManager, error) {
	if clock == nil {
		return nil, fmt.Errorf("create cache lease manager: nil clock")
	}
	if expiry <= 0 || grace < 0 {
		return nil, fmt.Errorf("create cache lease manager: expiry must be positive and grace non-negative")
	}
	return &LeaseManager{clock: clock, processes: processes, expiry: expiry, grace: grace}, nil
}

func (manager *LeaseManager) Heartbeat(lease Lease) (Lease, error) {
	if manager == nil || manager.clock == nil {
		return Lease{}, fmt.Errorf("heartbeat cache lease: nil lease manager")
	}
	if err := lease.Validate(); err != nil {
		return Lease{}, fmt.Errorf("heartbeat cache lease: %w", err)
	}
	if lease.State != LeaseActive {
		return Lease{}, fmt.Errorf("heartbeat cache lease %q: lease is not active", lease.ID)
	}
	grace := lease.GraceUntil.Sub(lease.ExpiresAt)
	if grace < 0 {
		grace = 0
	}
	if grace > manager.grace {
		grace = manager.grace
	}
	now := manager.clock.Now()
	lease.HeartbeatAt = now
	lease.ExpiresAt = now.Add(manager.expiry)
	lease.GraceUntil = lease.ExpiresAt.Add(grace)
	return lease, nil
}

func (manager *LeaseManager) Release(lease Lease) (Lease, error) {
	if manager == nil || manager.clock == nil {
		return Lease{}, fmt.Errorf("release cache lease: nil lease manager")
	}
	if err := lease.Validate(); err != nil {
		return Lease{}, fmt.Errorf("release cache lease: %w", err)
	}
	if lease.State != LeaseActive {
		return Lease{}, fmt.Errorf("release cache lease %q: lease is not active", lease.ID)
	}
	lease.State = LeaseReleased
	lease.ReleasedAt = manager.clock.Now()
	return lease, nil
}

// Classify is fail-closed: malformed or unqueryable identities remain protected.
func (manager *LeaseManager) Classify(lease Lease) LeaseProtection {
	if lease.State == LeaseReleased {
		return LeaseReclaimable
	}
	if manager == nil || manager.clock == nil || lease.Validate() != nil {
		return LeaseProtectedUncertain
	}
	now := manager.clock.Now()
	if now.Before(lease.ExpiresAt) {
		return LeaseProtectedActive
	}
	if now.Before(lease.GraceUntil) {
		return LeaseProtectedGrace
	}
	if lease.Process == nil || manager.processes == nil {
		return LeaseProtectedUncertain
	}
	switch manager.processes.Classify(*lease.Process) {
	case ProcessMatches:
		return LeaseProtectedLiveProcess
	case ProcessGone, ProcessBirthMismatch:
		return LeaseReclaimable
	case ProcessUncertain:
		return LeaseProtectedUncertain
	default:
		return LeaseProtectedUncertain
	}
}
