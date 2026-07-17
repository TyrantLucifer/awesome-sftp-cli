package transfer

import (
	"context"
	"errors"
	"fmt"
	"sync"

	"github.com/TyrantLucifer/awesome-mac-sftp/internal/domain"
)

var ErrResourceExhausted = errors.New("resource budget exhausted")

type ResourceUsage struct {
	ActiveJobs      uint32
	QueuedJobs      uint32
	Connections     uint32
	SSHProcesses    uint32
	HelperProcesses uint32
	FileDescriptors uint32
	Goroutines      uint32
	MemoryBytes     uint64
	EventBytes      uint64
	LogBytes        uint64
}

type ResourceRequest struct {
	JobID       domain.JobID
	EndpointIDs []domain.EndpointID
	Usage       ResourceUsage
}

type ResourceSnapshot struct {
	Limits      ResourceLimits
	Total       ResourceUsage
	PerEndpoint map[domain.EndpointID]ResourceUsage
	PerJob      map[domain.JobID]ResourceUsage
	Waiters     int
}

type ResourceLedger struct {
	mu sync.Mutex

	limits      ResourceLimits
	total       ResourceUsage
	perEndpoint map[domain.EndpointID]ResourceUsage
	perJob      map[domain.JobID]ResourceUsage
	waiters     int
	wake        chan struct{}
}

type ResourceLease struct {
	ledger   *ResourceLedger
	request  ResourceRequest
	released sync.Once
}

func NewResourceLedger(limits ResourceLimits) (*ResourceLedger, error) {
	if limits == (ResourceLimits{}) {
		limits = HardResourceCeilings()
	}
	validated, err := TightenResourceLimits(limits)
	if err != nil {
		return nil, err
	}
	return &ResourceLedger{
		limits: validated, perEndpoint: make(map[domain.EndpointID]ResourceUsage),
		perJob: make(map[domain.JobID]ResourceUsage), wake: make(chan struct{}),
	}, nil
}

func (ledger *ResourceLedger) TryAcquire(request ResourceRequest) (*ResourceLease, error) {
	if ledger == nil {
		return nil, errors.New("resource ledger is required")
	}
	request, err := normalizeResourceRequest(request)
	if err != nil {
		return nil, err
	}
	ledger.mu.Lock()
	defer ledger.mu.Unlock()
	if err := ledger.canAcquireLocked(request); err != nil {
		return nil, err
	}
	ledger.acquireLocked(request)
	return &ResourceLease{ledger: ledger, request: request}, nil
}

func (ledger *ResourceLedger) Acquire(ctx context.Context, request ResourceRequest) (*ResourceLease, error) {
	if ledger == nil {
		return nil, errors.New("resource ledger is required")
	}
	if ctx == nil {
		ctx = context.Background()
	}
	request, err := normalizeResourceRequest(request)
	if err != nil {
		return nil, err
	}

	ledger.mu.Lock()
	if err := ledger.canEverAcquireLocked(request); err != nil {
		ledger.mu.Unlock()
		return nil, err
	}
	ledger.waiters++
	for {
		if err := ctx.Err(); err != nil {
			ledger.waiters--
			ledger.mu.Unlock()
			return nil, err
		}
		if err := ledger.canAcquireLocked(request); err == nil {
			ledger.acquireLocked(request)
			ledger.waiters--
			ledger.mu.Unlock()
			return &ResourceLease{ledger: ledger, request: request}, nil
		}
		wake := ledger.wake
		ledger.mu.Unlock()
		select {
		case <-ctx.Done():
			ledger.mu.Lock()
			ledger.waiters--
			ledger.mu.Unlock()
			return nil, ctx.Err()
		case <-wake:
			ledger.mu.Lock()
		}
	}
}

func (ledger *ResourceLedger) canEverAcquireLocked(request ResourceRequest) error {
	if uint32(len(request.EndpointIDs)) > ledger.limits.Endpoints { //nolint:gosec // the slice is bounded by the 64-endpoint hard ceiling.
		return fmt.Errorf("%w: endpoint ceiling %d", ErrResourceExhausted, ledger.limits.Endpoints)
	}
	if err := usageFits(ResourceUsage{}, request.Usage, ledger.limits); err != nil {
		return err
	}
	if request.Usage.ActiveJobs > ledger.limits.ActiveJobsPerEndpoint && len(request.EndpointIDs) > 0 {
		return fmt.Errorf("%w: per-endpoint active Job ceiling %d", ErrResourceExhausted, ledger.limits.ActiveJobsPerEndpoint)
	}
	if request.Usage.Connections > ledger.limits.ConnectionsPerEndpoint && len(request.EndpointIDs) > 0 {
		return fmt.Errorf("%w: per-endpoint connection ceiling %d", ErrResourceExhausted, ledger.limits.ConnectionsPerEndpoint)
	}
	return nil
}

func (ledger *ResourceLedger) Snapshot() ResourceSnapshot {
	if ledger == nil {
		return ResourceSnapshot{}
	}
	ledger.mu.Lock()
	defer ledger.mu.Unlock()
	perEndpoint := make(map[domain.EndpointID]ResourceUsage, len(ledger.perEndpoint))
	for endpointID, usage := range ledger.perEndpoint {
		perEndpoint[endpointID] = usage
	}
	perJob := make(map[domain.JobID]ResourceUsage, len(ledger.perJob))
	for jobID, usage := range ledger.perJob {
		perJob[jobID] = usage
	}
	return ResourceSnapshot{
		Limits: ledger.limits, Total: ledger.total, PerEndpoint: perEndpoint,
		PerJob: perJob, Waiters: ledger.waiters,
	}
}

func (lease *ResourceLease) Release() {
	if lease == nil || lease.ledger == nil {
		return
	}
	lease.released.Do(func() {
		lease.ledger.release(lease.request)
	})
}

func normalizeResourceRequest(request ResourceRequest) (ResourceRequest, error) {
	if request.JobID == "" {
		return ResourceRequest{}, errors.New("resource request JobID is required")
	}
	seen := make(map[domain.EndpointID]struct{}, len(request.EndpointIDs))
	endpoints := make([]domain.EndpointID, 0, len(request.EndpointIDs))
	for _, endpointID := range request.EndpointIDs {
		if endpointID == "" {
			return ResourceRequest{}, errors.New("resource request endpoint ID is empty")
		}
		if _, duplicate := seen[endpointID]; duplicate {
			continue
		}
		seen[endpointID] = struct{}{}
		endpoints = append(endpoints, endpointID)
	}
	request.EndpointIDs = endpoints
	return request, nil
}

func (ledger *ResourceLedger) canAcquireLocked(request ResourceRequest) error {
	limits := ledger.limits
	if uint32(len(ledger.perEndpoint))+ledger.newEndpointCountLocked(request.EndpointIDs) > limits.Endpoints { //nolint:gosec // the map cannot exceed the 64-endpoint hard ceiling.
		return fmt.Errorf("%w: endpoint ceiling %d", ErrResourceExhausted, limits.Endpoints)
	}
	if err := usageFits(ledger.total, request.Usage, limits); err != nil {
		return err
	}
	for _, endpointID := range request.EndpointIDs {
		current := ledger.perEndpoint[endpointID]
		if exceeds32(current.ActiveJobs, request.Usage.ActiveJobs, limits.ActiveJobsPerEndpoint) {
			return fmt.Errorf("%w: endpoint %s active Job ceiling %d", ErrResourceExhausted, endpointID, limits.ActiveJobsPerEndpoint)
		}
		if exceeds32(current.Connections, request.Usage.Connections, limits.ConnectionsPerEndpoint) {
			return fmt.Errorf("%w: endpoint %s connection ceiling %d", ErrResourceExhausted, endpointID, limits.ConnectionsPerEndpoint)
		}
	}
	return nil
}

func usageFits(current, requested ResourceUsage, limits ResourceLimits) error {
	checks32 := []struct {
		name             string
		current, request uint32
		limit            uint32
	}{
		{"active Jobs", current.ActiveJobs, requested.ActiveJobs, limits.ActiveJobs},
		{"queued Jobs", current.QueuedJobs, requested.QueuedJobs, limits.QueuedJobs},
		{"connections", current.Connections, requested.Connections, limits.Connections},
		{"SSH processes", current.SSHProcesses, requested.SSHProcesses, limits.SSHProcesses},
		{"Helper processes", current.HelperProcesses, requested.HelperProcesses, limits.HelperProcesses},
		{"file descriptors", current.FileDescriptors, requested.FileDescriptors, limits.FileDescriptors},
		{"goroutines", current.Goroutines, requested.Goroutines, limits.Goroutines},
	}
	for _, check := range checks32 {
		if exceeds32(check.current, check.request, check.limit) {
			return fmt.Errorf("%w: %s ceiling %d", ErrResourceExhausted, check.name, check.limit)
		}
	}
	checks64 := []struct {
		name             string
		current, request uint64
		limit            uint64
	}{
		{"memory bytes", current.MemoryBytes, requested.MemoryBytes, limits.MemoryBytes},
		{"event bytes", current.EventBytes, requested.EventBytes, limits.EventBytes},
		{"log bytes", current.LogBytes, requested.LogBytes, limits.LogBytes},
	}
	for _, check := range checks64 {
		if exceeds64(check.current, check.request, check.limit) {
			return fmt.Errorf("%w: %s ceiling %d", ErrResourceExhausted, check.name, check.limit)
		}
	}
	return nil
}

func (ledger *ResourceLedger) acquireLocked(request ResourceRequest) {
	ledger.total = addUsage(ledger.total, request.Usage)
	ledger.perJob[request.JobID] = addUsage(ledger.perJob[request.JobID], request.Usage)
	for _, endpointID := range request.EndpointIDs {
		ledger.perEndpoint[endpointID] = addUsage(ledger.perEndpoint[endpointID], request.Usage)
	}
}

func (ledger *ResourceLedger) release(request ResourceRequest) {
	ledger.mu.Lock()
	ledger.total = subtractUsage(ledger.total, request.Usage)
	jobUsage := subtractUsage(ledger.perJob[request.JobID], request.Usage)
	if jobUsage == (ResourceUsage{}) {
		delete(ledger.perJob, request.JobID)
	} else {
		ledger.perJob[request.JobID] = jobUsage
	}
	for _, endpointID := range request.EndpointIDs {
		endpointUsage := subtractUsage(ledger.perEndpoint[endpointID], request.Usage)
		if endpointUsage == (ResourceUsage{}) {
			delete(ledger.perEndpoint, endpointID)
		} else {
			ledger.perEndpoint[endpointID] = endpointUsage
		}
	}
	close(ledger.wake)
	ledger.wake = make(chan struct{})
	ledger.mu.Unlock()
}

func (ledger *ResourceLedger) newEndpointCountLocked(endpointIDs []domain.EndpointID) uint32 {
	var count uint32
	for _, endpointID := range endpointIDs {
		if _, exists := ledger.perEndpoint[endpointID]; !exists {
			count++
		}
	}
	return count
}

func exceeds32(current, request, limit uint32) bool {
	return request > limit || current > limit-request
}
func exceeds64(current, request, limit uint64) bool {
	return request > limit || current > limit-request
}

func addUsage(left, right ResourceUsage) ResourceUsage {
	return ResourceUsage{
		ActiveJobs: left.ActiveJobs + right.ActiveJobs, QueuedJobs: left.QueuedJobs + right.QueuedJobs,
		Connections: left.Connections + right.Connections, SSHProcesses: left.SSHProcesses + right.SSHProcesses,
		HelperProcesses: left.HelperProcesses + right.HelperProcesses,
		FileDescriptors: left.FileDescriptors + right.FileDescriptors, Goroutines: left.Goroutines + right.Goroutines,
		MemoryBytes: left.MemoryBytes + right.MemoryBytes, EventBytes: left.EventBytes + right.EventBytes,
		LogBytes: left.LogBytes + right.LogBytes,
	}
}

func subtractUsage(left, right ResourceUsage) ResourceUsage {
	return ResourceUsage{
		ActiveJobs: left.ActiveJobs - right.ActiveJobs, QueuedJobs: left.QueuedJobs - right.QueuedJobs,
		Connections: left.Connections - right.Connections, SSHProcesses: left.SSHProcesses - right.SSHProcesses,
		HelperProcesses: left.HelperProcesses - right.HelperProcesses,
		FileDescriptors: left.FileDescriptors - right.FileDescriptors, Goroutines: left.Goroutines - right.Goroutines,
		MemoryBytes: left.MemoryBytes - right.MemoryBytes, EventBytes: left.EventBytes - right.EventBytes,
		LogBytes: left.LogBytes - right.LogBytes,
	}
}
