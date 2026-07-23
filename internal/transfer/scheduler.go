package transfer

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/TyrantLucifer/awesome-sftp-cli/internal/domain"
	"github.com/TyrantLucifer/awesome-sftp-cli/internal/foundation"
)

const (
	HardTransferBufferBytes    = 256 << 10
	HardRecoveryBufferBytes    = 512 << 10
	TransferScheduleQuantum    = HardTransferBufferBytes
	MaxBandwidthBytesPerSecond = 1 << 40

	DefaultInteractiveWeight = 4
	DefaultBulkWeight        = 1
)

var (
	ErrResourceLimitExpansion       = errors.New("resource limit exceeds hard ceiling")
	ErrSchedulerUpdateStrandsWaiter = errors.New("scheduler update would strand queued bandwidth request")
)

type ResourceLimits struct {
	ActiveJobs             uint32
	ActiveJobsPerEndpoint  uint32
	QueuedJobs             uint32
	Endpoints              uint32
	Connections            uint32
	ConnectionsPerEndpoint uint32
	SSHProcesses           uint32
	HelperProcesses        uint32
	FileDescriptors        uint32
	Goroutines             uint32
	MemoryBytes            uint64
	EventBytes             uint64
	LogBytes               uint64
}

func HardResourceCeilings() ResourceLimits {
	return ResourceLimits{
		ActiveJobs:             16,
		ActiveJobsPerEndpoint:  8,
		QueuedJobs:             512,
		Endpoints:              64,
		Connections:            32,
		ConnectionsPerEndpoint: 4,
		SSHProcesses:           32,
		HelperProcesses:        4,
		FileDescriptors:        512,
		Goroutines:             256,
		MemoryBytes:            64 << 20,
		EventBytes:             32 << 10,
		LogBytes:               32 << 10,
	}
}

func TightenResourceLimits(limits ResourceLimits) (ResourceLimits, error) {
	hard := HardResourceCeilings()
	if limits.ActiveJobs > hard.ActiveJobs ||
		limits.ActiveJobsPerEndpoint > hard.ActiveJobsPerEndpoint ||
		limits.QueuedJobs > hard.QueuedJobs ||
		limits.Endpoints > hard.Endpoints ||
		limits.Connections > hard.Connections ||
		limits.ConnectionsPerEndpoint > hard.ConnectionsPerEndpoint ||
		limits.SSHProcesses > hard.SSHProcesses ||
		limits.HelperProcesses > hard.HelperProcesses ||
		limits.FileDescriptors > hard.FileDescriptors ||
		limits.Goroutines > hard.Goroutines ||
		limits.MemoryBytes > hard.MemoryBytes ||
		limits.EventBytes > hard.EventBytes ||
		limits.LogBytes > hard.LogBytes {
		return ResourceLimits{}, ErrResourceLimitExpansion
	}
	return limits, nil
}

type ScheduleClass string

const (
	ScheduleInteractive ScheduleClass = "interactive"
	ScheduleBulk        ScheduleClass = "bulk"
)

type SchedulerPolicy struct {
	GlobalBytesPerSecond   uint64
	EndpointBytesPerSecond uint64
	JobBytesPerSecond      uint64
	BurstBytes             uint64
	QuantumBytes           uint32
	InteractiveWeight      uint8
	BulkWeight             uint8
}

type BandwidthRequest struct {
	JobID             domain.JobID
	EndpointID        domain.EndpointID
	PeerEndpointID    domain.EndpointID
	JobBytesPerSecond uint64
	Class             ScheduleClass
	Bytes             uint32
}

type SchedulerSnapshot struct {
	Waiters      int
	GrantedBytes uint64
	Policy       SchedulerPolicy
	Resources    ResourceSnapshot
}

type TransferScheduler struct {
	mu sync.Mutex

	clock        foundation.Clock
	policy       SchedulerPolicy
	global       integerTokenBucket
	endpoints    map[domain.EndpointID]*integerTokenBucket
	jobs         map[domain.JobID]*integerTokenBucket
	jobRates     map[domain.JobID]uint64
	jobEndpoints map[domain.JobID][]domain.EndpointID
	resources    *ResourceLedger

	interactive  []*bandwidthWaiter
	bulk         []*bandwidthWaiter
	cycle        []ScheduleClass
	cycleIndex   int
	wake         chan struct{}
	grantedBytes uint64
}

type bandwidthWaiter struct {
	request BandwidthRequest
}

type integerTokenBucket struct {
	rate      uint64
	capacity  uint64
	tokens    uint64
	remainder uint64
	last      time.Time
}

func NewTransferScheduler(clock foundation.Clock, policy SchedulerPolicy) (*TransferScheduler, error) {
	return NewTransferSchedulerWithLimits(clock, policy, HardResourceCeilings())
}

func NewTransferSchedulerWithLimits(clock foundation.Clock, policy SchedulerPolicy, limits ResourceLimits) (*TransferScheduler, error) {
	if clock == nil {
		return nil, errors.New("transfer scheduler clock is required")
	}
	normalized, err := normalizeSchedulerPolicy(policy)
	if err != nil {
		return nil, err
	}
	resources, err := NewResourceLedger(limits)
	if err != nil {
		return nil, err
	}
	now := clock.Now()
	scheduler := &TransferScheduler{
		clock:        clock,
		policy:       normalized,
		global:       newIntegerTokenBucket(normalized.GlobalBytesPerSecond, normalized.BurstBytes, normalized.BurstBytes, now),
		endpoints:    make(map[domain.EndpointID]*integerTokenBucket),
		jobs:         make(map[domain.JobID]*integerTokenBucket),
		jobRates:     make(map[domain.JobID]uint64),
		jobEndpoints: make(map[domain.JobID][]domain.EndpointID),
		resources:    resources,
		wake:         make(chan struct{}),
	}
	scheduler.rebuildCycle()
	return scheduler, nil
}

func (scheduler *TransferScheduler) Wait(ctx context.Context, request BandwidthRequest) error {
	if scheduler == nil {
		return errors.New("transfer scheduler is required")
	}
	if ctx == nil {
		ctx = context.Background()
	}
	if err := scheduler.validateRequest(request); err != nil {
		return err
	}
	if err := ctx.Err(); err != nil {
		return err
	}

	waiter := &bandwidthWaiter{request: request}
	scheduler.mu.Lock()
	scheduler.enqueue(waiter)
	scheduler.broadcastLocked()
	scheduler.mu.Unlock()

	for {
		if err := ctx.Err(); err != nil {
			scheduler.remove(waiter)
			return err
		}

		scheduler.mu.Lock()
		now := scheduler.clock.Now()
		scheduler.refillLocked(now)
		selected := scheduler.selectedLocked()
		if selected == waiter {
			scheduler.consumeLocked(request)
			scheduler.dequeueSelectedLocked()
			scheduler.grantedBytes += uint64(request.Bytes)
			scheduler.broadcastLocked()
			scheduler.mu.Unlock()
			return nil
		}

		wake := scheduler.wake
		waitFor := time.Hour
		if selected == nil && scheduler.isClassHeadLocked(waiter) {
			waitFor = scheduler.waitDurationLocked(request)
		}
		timer := scheduler.clock.NewTimer(waitFor)
		scheduler.mu.Unlock()

		select {
		case <-ctx.Done():
			timer.Stop()
			scheduler.remove(waiter)
			return ctx.Err()
		case <-wake:
			timer.Stop()
		case <-timer.C():
		}
	}
}

func (scheduler *TransferScheduler) Update(policy SchedulerPolicy) error {
	if scheduler == nil {
		return errors.New("transfer scheduler is required")
	}
	normalized, err := normalizeSchedulerPolicy(policy)
	if err != nil {
		return err
	}

	scheduler.mu.Lock()
	defer scheduler.mu.Unlock()
	for _, queue := range [][]*bandwidthWaiter{scheduler.interactive, scheduler.bulk} {
		for _, waiter := range queue {
			request := waiter.request
			if uint64(request.Bytes) <= normalized.BurstBytes {
				continue
			}
			jobRate := request.JobBytesPerSecond
			if jobRate == 0 {
				jobRate = normalized.JobBytesPerSecond
			}
			if normalized.GlobalBytesPerSecond != 0 ||
				normalized.EndpointBytesPerSecond != 0 && len(uniqueEndpointIDs(request)) != 0 ||
				jobRate != 0 {
				return fmt.Errorf("%w: Job %q has %d queued bytes above new burst %d", ErrSchedulerUpdateStrandsWaiter, request.JobID, request.Bytes, normalized.BurstBytes)
			}
		}
	}
	now := scheduler.clock.Now()
	scheduler.refillLocked(now)
	scheduler.global.update(normalized.GlobalBytesPerSecond, normalized.BurstBytes, now)
	for _, bucket := range scheduler.endpoints {
		bucket.update(normalized.EndpointBytesPerSecond, bucketCapacity(normalized.EndpointBytesPerSecond, normalized.BurstBytes), now)
	}
	for jobID, bucket := range scheduler.jobs {
		rate := scheduler.jobRates[jobID]
		if rate == 0 {
			rate = normalized.JobBytesPerSecond
		}
		bucket.update(rate, bucketCapacity(rate, normalized.BurstBytes), now)
	}
	scheduler.policy = normalized
	scheduler.rebuildCycle()
	scheduler.broadcastLocked()
	return nil
}

// ReleaseJob ends the bandwidth identity lifetime for a completed execution
// attempt. Shared endpoint buckets remain until the last referencing Job exits.
func (scheduler *TransferScheduler) ReleaseJob(jobID domain.JobID) {
	if scheduler == nil || jobID == "" {
		return
	}
	scheduler.mu.Lock()
	defer scheduler.mu.Unlock()
	if scheduler.hasWaiterForJobLocked(jobID) {
		return
	}
	endpoints := scheduler.jobEndpoints[jobID]
	delete(scheduler.jobs, jobID)
	delete(scheduler.jobRates, jobID)
	delete(scheduler.jobEndpoints, jobID)
	for _, endpointID := range endpoints {
		if !scheduler.endpointReferencedLocked(endpointID) {
			delete(scheduler.endpoints, endpointID)
		}
	}
}

func (scheduler *TransferScheduler) Snapshot() SchedulerSnapshot {
	if scheduler == nil {
		return SchedulerSnapshot{}
	}
	scheduler.mu.Lock()
	defer scheduler.mu.Unlock()
	return SchedulerSnapshot{
		Waiters:      len(scheduler.interactive) + len(scheduler.bulk),
		GrantedBytes: scheduler.grantedBytes,
		Policy:       scheduler.policy,
		Resources:    scheduler.resources.Snapshot(),
	}
}

func (scheduler *TransferScheduler) AcquireResources(ctx context.Context, request ResourceRequest) (*ResourceLease, error) {
	if scheduler == nil || scheduler.resources == nil {
		return nil, errors.New("transfer scheduler resource ledger is required")
	}
	return scheduler.resources.Acquire(ctx, request)
}

func (scheduler *TransferScheduler) TryAcquireResources(request ResourceRequest) (*ResourceLease, error) {
	if scheduler == nil || scheduler.resources == nil {
		return nil, errors.New("transfer scheduler resource ledger is required")
	}
	return scheduler.resources.TryAcquire(request)
}

func (scheduler *TransferScheduler) QuantumBytes() uint32 {
	if scheduler == nil {
		return TransferScheduleQuantum
	}
	scheduler.mu.Lock()
	defer scheduler.mu.Unlock()
	return scheduler.policy.QuantumBytes
}

// AllowsReadAhead reports whether fetching a bounded source window can bypass
// no active token bucket. It is checked before every source request so a hot
// policy update disables subsequent read-ahead without changing durable
// checkpoint or scheduler quantum semantics.
func (scheduler *TransferScheduler) AllowsReadAhead(request BandwidthRequest) bool {
	if scheduler == nil {
		return false
	}
	scheduler.mu.Lock()
	defer scheduler.mu.Unlock()
	jobRate := request.JobBytesPerSecond
	if jobRate == 0 {
		jobRate = scheduler.policy.JobBytesPerSecond
	}
	return scheduler.policy.GlobalBytesPerSecond == 0 &&
		scheduler.policy.EndpointBytesPerSecond == 0 &&
		jobRate == 0
}

func normalizeSchedulerPolicy(policy SchedulerPolicy) (SchedulerPolicy, error) {
	if policy.QuantumBytes == 0 {
		policy.QuantumBytes = TransferScheduleQuantum
	}
	if policy.QuantumBytes > TransferScheduleQuantum {
		return SchedulerPolicy{}, fmt.Errorf("scheduler quantum %d exceeds hard ceiling %d", policy.QuantumBytes, TransferScheduleQuantum)
	}
	if policy.BurstBytes == 0 {
		policy.BurstBytes = uint64(policy.QuantumBytes)
	}
	if policy.BurstBytes > HardRecoveryBufferBytes {
		return SchedulerPolicy{}, fmt.Errorf("scheduler burst %d exceeds recovery buffer ceiling %d", policy.BurstBytes, HardRecoveryBufferBytes)
	}
	if policy.BurstBytes < uint64(policy.QuantumBytes) {
		return SchedulerPolicy{}, fmt.Errorf("scheduler burst %d is below quantum %d", policy.BurstBytes, policy.QuantumBytes)
	}
	if policy.GlobalBytesPerSecond > MaxBandwidthBytesPerSecond ||
		policy.EndpointBytesPerSecond > MaxBandwidthBytesPerSecond ||
		policy.JobBytesPerSecond > MaxBandwidthBytesPerSecond {
		return SchedulerPolicy{}, fmt.Errorf("scheduler rate exceeds hard ceiling %d", MaxBandwidthBytesPerSecond)
	}
	if policy.InteractiveWeight == 0 {
		policy.InteractiveWeight = DefaultInteractiveWeight
	}
	if policy.BulkWeight == 0 {
		policy.BulkWeight = DefaultBulkWeight
	}
	if policy.InteractiveWeight > 16 || policy.BulkWeight > 16 {
		return SchedulerPolicy{}, errors.New("scheduler weights exceed hard ceiling 16")
	}
	return policy, nil
}

func (scheduler *TransferScheduler) validateRequest(request BandwidthRequest) error {
	if request.Bytes == 0 {
		return errors.New("bandwidth request bytes must be positive")
	}
	if request.Bytes > scheduler.policy.QuantumBytes {
		return fmt.Errorf("bandwidth request %d exceeds scheduler quantum %d", request.Bytes, scheduler.policy.QuantumBytes)
	}
	if request.JobID == "" {
		return errors.New("bandwidth request JobID is required")
	}
	if request.JobBytesPerSecond > MaxBandwidthBytesPerSecond {
		return fmt.Errorf("job bandwidth rate %d exceeds hard ceiling %d", request.JobBytesPerSecond, MaxBandwidthBytesPerSecond)
	}
	if request.Class != ScheduleInteractive && request.Class != ScheduleBulk {
		return fmt.Errorf("unsupported schedule class %q", request.Class)
	}
	return nil
}

func (scheduler *TransferScheduler) enqueue(waiter *bandwidthWaiter) {
	if waiter.request.Class == ScheduleInteractive {
		scheduler.interactive = append(scheduler.interactive, waiter)
		return
	}
	scheduler.bulk = append(scheduler.bulk, waiter)
}

func (scheduler *TransferScheduler) selectedLocked() *bandwidthWaiter {
	for attempts := 0; attempts < len(scheduler.cycle); attempts++ {
		index := (scheduler.cycleIndex + attempts) % len(scheduler.cycle)
		class := scheduler.cycle[index]
		queue := scheduler.queue(class)
		if len(queue) > 0 && scheduler.canGrantLocked(queue[0].request) {
			scheduler.cycleIndex = index
			return queue[0]
		}
	}
	return nil
}

func (scheduler *TransferScheduler) isClassHeadLocked(waiter *bandwidthWaiter) bool {
	queue := scheduler.queue(waiter.request.Class)
	return len(queue) > 0 && queue[0] == waiter
}

func (scheduler *TransferScheduler) dequeueSelectedLocked() {
	class := scheduler.cycle[scheduler.cycleIndex]
	if class == ScheduleInteractive {
		scheduler.interactive = scheduler.interactive[1:]
	} else {
		scheduler.bulk = scheduler.bulk[1:]
	}
	scheduler.cycleIndex = (scheduler.cycleIndex + 1) % len(scheduler.cycle)
}

func (scheduler *TransferScheduler) queue(class ScheduleClass) []*bandwidthWaiter {
	if class == ScheduleInteractive {
		return scheduler.interactive
	}
	return scheduler.bulk
}

func (scheduler *TransferScheduler) remove(waiter *bandwidthWaiter) {
	scheduler.mu.Lock()
	defer scheduler.mu.Unlock()
	if removeWaiter(&scheduler.interactive, waiter) || removeWaiter(&scheduler.bulk, waiter) {
		scheduler.broadcastLocked()
	}
}

func removeWaiter(queue *[]*bandwidthWaiter, target *bandwidthWaiter) bool {
	for index, waiter := range *queue {
		if waiter != target {
			continue
		}
		copy((*queue)[index:], (*queue)[index+1:])
		*queue = (*queue)[:len(*queue)-1]
		return true
	}
	return false
}

func (scheduler *TransferScheduler) rebuildCycle() {
	scheduler.cycle = scheduler.cycle[:0]
	for count := uint8(0); count < scheduler.policy.InteractiveWeight; count++ {
		scheduler.cycle = append(scheduler.cycle, ScheduleInteractive)
	}
	for count := uint8(0); count < scheduler.policy.BulkWeight; count++ {
		scheduler.cycle = append(scheduler.cycle, ScheduleBulk)
	}
	if scheduler.cycleIndex >= len(scheduler.cycle) {
		scheduler.cycleIndex = 0
	}
}

func (scheduler *TransferScheduler) refillLocked(now time.Time) {
	scheduler.global.refill(now)
	for _, bucket := range scheduler.endpoints {
		bucket.refill(now)
	}
	for _, bucket := range scheduler.jobs {
		bucket.refill(now)
	}
}

func (scheduler *TransferScheduler) canGrantLocked(request BandwidthRequest) bool {
	bytes := uint64(request.Bytes)
	if !scheduler.global.available(bytes) || !scheduler.jobBucketLocked(request).available(bytes) {
		return false
	}
	for _, endpointID := range uniqueEndpointIDs(request) {
		if !scheduler.endpointBucketLocked(endpointID).available(bytes) {
			return false
		}
	}
	return true
}

func (scheduler *TransferScheduler) consumeLocked(request BandwidthRequest) {
	bytes := uint64(request.Bytes)
	scheduler.global.consume(bytes)
	for _, endpointID := range uniqueEndpointIDs(request) {
		scheduler.endpointBucketLocked(endpointID).consume(bytes)
	}
	scheduler.jobBucketLocked(request).consume(bytes)
}

func (scheduler *TransferScheduler) waitDurationLocked(request BandwidthRequest) time.Duration {
	bytes := uint64(request.Bytes)
	waitFor := scheduler.global.waitDuration(bytes)
	for _, endpointID := range uniqueEndpointIDs(request) {
		if endpointWait := scheduler.endpointBucketLocked(endpointID).waitDuration(bytes); endpointWait > waitFor {
			waitFor = endpointWait
		}
	}
	if jobWait := scheduler.jobBucketLocked(request).waitDuration(bytes); jobWait > waitFor {
		waitFor = jobWait
	}
	if waitFor <= 0 {
		return time.Nanosecond
	}
	return waitFor
}

func uniqueEndpointIDs(request BandwidthRequest) []domain.EndpointID {
	endpointIDs := make([]domain.EndpointID, 0, 2)
	if request.EndpointID != "" {
		endpointIDs = append(endpointIDs, request.EndpointID)
	}
	if request.PeerEndpointID != "" && request.PeerEndpointID != request.EndpointID {
		endpointIDs = append(endpointIDs, request.PeerEndpointID)
	}
	return endpointIDs
}

func (scheduler *TransferScheduler) endpointBucketLocked(endpointID domain.EndpointID) *integerTokenBucket {
	if bucket, ok := scheduler.endpoints[endpointID]; ok {
		return bucket
	}
	now := scheduler.clock.Now()
	capacity := bucketCapacity(scheduler.policy.EndpointBytesPerSecond, scheduler.policy.BurstBytes)
	initial := initialBucketTokens(scheduler.policy.EndpointBytesPerSecond, capacity)
	bucket := newIntegerTokenBucket(scheduler.policy.EndpointBytesPerSecond, capacity, initial, now)
	scheduler.endpoints[endpointID] = &bucket
	return &bucket
}

func (scheduler *TransferScheduler) jobBucketLocked(request BandwidthRequest) *integerTokenBucket {
	scheduler.rememberJobEndpointsLocked(request)
	if bucket, ok := scheduler.jobs[request.JobID]; ok {
		return bucket
	}
	now := scheduler.clock.Now()
	rate := request.JobBytesPerSecond
	if rate == 0 {
		rate = scheduler.policy.JobBytesPerSecond
	}
	capacity := bucketCapacity(rate, scheduler.policy.BurstBytes)
	bucket := newIntegerTokenBucket(rate, capacity, initialBucketTokens(rate, capacity), now)
	scheduler.jobs[request.JobID] = &bucket
	scheduler.jobRates[request.JobID] = request.JobBytesPerSecond
	return &bucket
}

func (scheduler *TransferScheduler) rememberJobEndpointsLocked(request BandwidthRequest) {
	known := scheduler.jobEndpoints[request.JobID]
	for _, endpointID := range uniqueEndpointIDs(request) {
		found := false
		for _, existing := range known {
			if existing == endpointID {
				found = true
				break
			}
		}
		if !found {
			known = append(known, endpointID)
		}
	}
	scheduler.jobEndpoints[request.JobID] = known
}

func (scheduler *TransferScheduler) hasWaiterForJobLocked(jobID domain.JobID) bool {
	for _, queue := range [][]*bandwidthWaiter{scheduler.interactive, scheduler.bulk} {
		for _, waiter := range queue {
			if waiter.request.JobID == jobID {
				return true
			}
		}
	}
	return false
}

func (scheduler *TransferScheduler) endpointReferencedLocked(endpointID domain.EndpointID) bool {
	for _, endpoints := range scheduler.jobEndpoints {
		for _, existing := range endpoints {
			if existing == endpointID {
				return true
			}
		}
	}
	return false
}

func (scheduler *TransferScheduler) broadcastLocked() {
	close(scheduler.wake)
	scheduler.wake = make(chan struct{})
}

func bucketCapacity(_ uint64, burst uint64) uint64 {
	return burst
}

func initialBucketTokens(rate uint64, capacity uint64) uint64 {
	if rate > 0 && rate < capacity {
		return rate
	}
	return capacity
}

func newIntegerTokenBucket(rate uint64, capacity uint64, initial uint64, now time.Time) integerTokenBucket {
	if initial > capacity {
		initial = capacity
	}
	return integerTokenBucket{rate: rate, capacity: capacity, tokens: initial, last: now}
}

func (bucket *integerTokenBucket) update(rate uint64, capacity uint64, now time.Time) {
	bucket.rate = rate
	bucket.capacity = capacity
	if bucket.tokens > capacity {
		bucket.tokens = capacity
	}
	bucket.last = now
}

func (bucket *integerTokenBucket) refill(now time.Time) {
	if !now.After(bucket.last) {
		return
	}
	elapsed := now.Sub(bucket.last)
	bucket.last = now
	if bucket.rate == 0 || bucket.tokens >= bucket.capacity {
		bucket.remainder = 0
		return
	}

	seconds := uint64(elapsed / time.Second)     //nolint:gosec // elapsed is positive because now.After(last) was proven above.
	nanoseconds := uint64(elapsed % time.Second) //nolint:gosec // positive duration remainder is in [0, 1 second).
	added := saturatingMultiply(seconds, bucket.rate, bucket.capacity-bucket.tokens)
	fractional := (bucket.rate%uint64(time.Second))*nanoseconds + bucket.remainder
	added = saturatingAdd(added, (bucket.rate/uint64(time.Second))*nanoseconds, bucket.capacity-bucket.tokens)
	added = saturatingAdd(added, fractional/uint64(time.Second), bucket.capacity-bucket.tokens)
	bucket.remainder = fractional % uint64(time.Second)
	bucket.tokens = saturatingAdd(bucket.tokens, added, bucket.capacity)
	if bucket.tokens == bucket.capacity {
		bucket.remainder = 0
	}
}

func (bucket *integerTokenBucket) available(bytes uint64) bool {
	return bucket.rate == 0 || bucket.tokens >= bytes
}

func (bucket *integerTokenBucket) consume(bytes uint64) {
	if bucket.rate != 0 {
		bucket.tokens -= bytes
	}
}

func (bucket *integerTokenBucket) waitDuration(bytes uint64) time.Duration {
	if bucket.rate == 0 || bucket.tokens >= bytes {
		return 0
	}
	missing := bytes - bucket.tokens
	numerator := missing * uint64(time.Second)
	if numerator > bucket.remainder {
		numerator -= bucket.remainder
	} else {
		numerator = 1
	}
	nanoseconds := numerator / bucket.rate
	if numerator%bucket.rate != 0 {
		nanoseconds++
	}
	if nanoseconds > uint64(time.Duration(1<<63-1)) {
		return time.Duration(1<<63 - 1)
	}
	return time.Duration(nanoseconds)
}

func saturatingMultiply(left uint64, right uint64, ceiling uint64) uint64 {
	if left == 0 || right == 0 {
		return 0
	}
	if left > ceiling/right {
		return ceiling
	}
	product := left * right
	if product > ceiling {
		return ceiling
	}
	return product
}

func saturatingAdd(left uint64, right uint64, ceiling uint64) uint64 {
	if left >= ceiling || right >= ceiling-left {
		return ceiling
	}
	return left + right
}
