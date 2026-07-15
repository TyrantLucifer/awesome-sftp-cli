package fake

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/TyrantLucifer/awesome-mac-sftp/internal/domain"
)

// Operation identifies one recordable Fake Provider operation.
type Operation string

const (
	OperationSnapshot   Operation = "snapshot"
	OperationNormalize  Operation = "normalize"
	OperationList       Operation = "list"
	OperationStat       Operation = "stat"
	OperationOpenRead   Operation = "open_read"
	OperationRead       Operation = "read"
	OperationCloseRead  Operation = "close_read"
	OperationOpenWrite  Operation = "open_write"
	OperationWrite      Operation = "write"
	OperationSyncWrite  Operation = "sync_write"
	OperationCloseWrite Operation = "close_write"
	OperationMkdir      Operation = "mkdir"
	OperationRename     Operation = "rename"
	OperationRemove     Operation = "remove"
)

// FaultMatch selects the Nth call for one operation and optional exact path.
type FaultMatch struct {
	Operation Operation
	Nth       uint64
	Path      *domain.CanonicalPath
}

// FaultEffect describes the deterministic effect selected for a matched call.
type FaultEffect struct {
	Delay             time.Duration
	WaitGate          string
	Error             *domain.OpError
	MaxReadBytes      int
	MaxWriteBytes     int
	StaleNodeRevision *uint64
	Disconnect        bool
	NonAtomicRename   bool
}

// FaultStep associates one unique matcher with one effect.
type FaultStep struct {
	Match  FaultMatch
	Effect FaultEffect
}

// Call is an immutable record of a structurally valid public operation.
type Call struct {
	Operation Operation
	Location  *domain.Location
	Sequence  uint64
}

type matcherMode uint8

const (
	matcherWildcard matcherMode = iota + 1
	matcherExact
)

type matcherKey struct {
	operation Operation
	path      domain.CanonicalPath
	mode      matcherMode
}

type ownedFaultStep struct {
	match    FaultMatch
	effect   FaultEffect
	consumed bool
}

type scriptState struct {
	mu sync.Mutex

	modes        map[Operation]matcherMode
	counts       map[matcherKey]uint64
	steps        []ownedFaultStep
	gates        map[string]*faultGate
	calls        []Call
	nextSequence uint64
}

type faultGate struct {
	released chan struct{}
	once     sync.Once
}

var validOperations = map[Operation]struct{}{
	OperationSnapshot:   {},
	OperationNormalize:  {},
	OperationList:       {},
	OperationStat:       {},
	OperationOpenRead:   {},
	OperationRead:       {},
	OperationCloseRead:  {},
	OperationOpenWrite:  {},
	OperationWrite:      {},
	OperationSyncWrite:  {},
	OperationCloseWrite: {},
	OperationMkdir:      {},
	OperationRename:     {},
	OperationRemove:     {},
}

func newScriptState(script []FaultStep) (*scriptState, error) {
	state := &scriptState{
		modes:  make(map[Operation]matcherMode),
		counts: make(map[matcherKey]uint64),
		steps:  make([]ownedFaultStep, 0, len(script)),
		gates:  make(map[string]*faultGate),
	}
	seen := make(map[matcherKey]map[uint64]struct{})
	for index, step := range script {
		owned := cloneFaultStep(step)
		if err := validateFaultStep(owned); err != nil {
			return nil, fmt.Errorf("create fake provider: fault step %d: %w", index, err)
		}

		mode := matcherWildcard
		if owned.Match.Path != nil {
			mode = matcherExact
		}
		if previous, exists := state.modes[owned.Match.Operation]; exists && previous != mode {
			return nil, fmt.Errorf(
				"create fake provider: fault step %d: operation %q mixes wildcard and exact-path matchers",
				index,
				owned.Match.Operation,
			)
		}
		state.modes[owned.Match.Operation] = mode

		key := matcherKey{operation: owned.Match.Operation, mode: mode}
		if owned.Match.Path != nil {
			key.path = *owned.Match.Path
		}
		if seen[key] == nil {
			seen[key] = make(map[uint64]struct{})
		}
		if _, duplicate := seen[key][owned.Match.Nth]; duplicate {
			return nil, fmt.Errorf(
				"create fake provider: fault step %d: duplicate matcher for operation %q, path %q, Nth %d",
				index,
				owned.Match.Operation,
				key.path,
				owned.Match.Nth,
			)
		}
		seen[key][owned.Match.Nth] = struct{}{}
		if owned.Effect.WaitGate != "" {
			if _, exists := state.gates[owned.Effect.WaitGate]; !exists {
				state.gates[owned.Effect.WaitGate] = &faultGate{
					released: make(chan struct{}),
				}
			}
		}
		state.steps = append(state.steps, ownedFaultStep{
			match:  owned.Match,
			effect: owned.Effect,
		})
	}
	return state, nil
}

func validateFaultStep(step FaultStep) error {
	if _, ok := validOperations[step.Match.Operation]; !ok {
		return fmt.Errorf("operation %q is invalid", step.Match.Operation)
	}
	if step.Match.Nth == 0 {
		return fmt.Errorf("nth must be greater than zero")
	}
	if step.Match.Path != nil {
		if step.Match.Operation == OperationSnapshot {
			return fmt.Errorf("snapshot matcher cannot select a path")
		}
		canonical, err := canonicalizeAbsolute(string(*step.Match.Path))
		if err != nil || canonical != string(*step.Match.Path) {
			return fmt.Errorf("matcher path %q is not canonical", *step.Match.Path)
		}
	}
	return validateFaultEffect(step.Match.Operation, step.Effect)
}

func validateFaultEffect(operation Operation, effect FaultEffect) error {
	if effect.Delay < 0 {
		return fmt.Errorf("delay must not be negative")
	}
	if strings.IndexByte(effect.WaitGate, 0) >= 0 {
		return fmt.Errorf("gate name contains NUL")
	}
	if effect.MaxReadBytes < 0 {
		return fmt.Errorf("maximum read bytes must not be negative")
	}
	if effect.MaxWriteBytes < 0 {
		return fmt.Errorf("maximum write bytes must not be negative")
	}
	if effect.MaxReadBytes > 0 && operation != OperationRead {
		return fmt.Errorf("maximum read bytes require a read operation")
	}
	if effect.MaxWriteBytes > 0 && operation != OperationWrite {
		return fmt.Errorf("maximum write bytes require a write operation")
	}
	if effect.StaleNodeRevision != nil && operation != OperationStat {
		return fmt.Errorf("stale node revision requires a stat operation")
	}
	if effect.StaleNodeRevision != nil && *effect.StaleNodeRevision == 0 {
		return fmt.Errorf("stale node revision must be greater than zero")
	}
	if effect.NonAtomicRename && operation != OperationRename {
		return fmt.Errorf("non-atomic rename requires a rename operation")
	}
	if effect.Disconnect && !disconnectOperation(operation) {
		return fmt.Errorf("disconnect is not valid for operation %q", operation)
	}
	if effect.Error != nil {
		if err := validateInjectedError(effect.Error); err != nil {
			return err
		}
	}

	shortEffects := 0
	if effect.MaxReadBytes > 0 {
		shortEffects++
	}
	if effect.MaxWriteBytes > 0 {
		shortEffects++
	}
	if shortEffects > 1 {
		return fmt.Errorf("read and write limits cannot be combined")
	}
	exclusivePrimaries := 0
	if effect.Disconnect {
		exclusivePrimaries++
	}
	if effect.StaleNodeRevision != nil {
		exclusivePrimaries++
	}
	if effect.NonAtomicRename {
		exclusivePrimaries++
	}
	if exclusivePrimaries > 0 &&
		(exclusivePrimaries > 1 || effect.Error != nil || shortEffects != 0) {
		return fmt.Errorf("disconnect, stale revision, and non-atomic rename exclude other primary effects")
	}
	if effect.Error != nil && shortEffects != 0 {
		validProgressError := operation == OperationRead && effect.MaxReadBytes > 0 ||
			operation == OperationWrite && effect.MaxWriteBytes > 0
		if !validProgressError {
			return fmt.Errorf("injected error can combine only with matching short I/O")
		}
	}
	if effect.Error != nil && shortEffects == 0 &&
		effect.Error.Effect != domain.EffectNone && effect.Error.Effect != domain.EffectUnknown {
		return fmt.Errorf("standalone injected error cannot claim applied effect")
	}

	hasEffect := effect.Delay > 0 || effect.WaitGate != "" || effect.Error != nil ||
		effect.MaxReadBytes > 0 || effect.MaxWriteBytes > 0 ||
		effect.StaleNodeRevision != nil || effect.Disconnect || effect.NonAtomicRename
	if !hasEffect {
		return fmt.Errorf("effect is empty")
	}
	return nil
}

func validateInjectedError(opError *domain.OpError) error {
	switch opError.Code {
	case domain.CodeInvalidArgument,
		domain.CodeNotFound,
		domain.CodeAlreadyExists,
		domain.CodePermissionDenied,
		domain.CodeAuthRequired,
		domain.CodeTransportInterrupted,
		domain.CodeTimeout,
		domain.CodeUnsupported,
		domain.CodeCapabilityLost,
		domain.CodeConflict,
		domain.CodeResourceExhausted,
		domain.CodeIntegrityFailed,
		domain.CodeCanceled,
		domain.CodeProtocolIncompatible,
		domain.CodeInternal:
	default:
		return fmt.Errorf("injected error code %q is invalid", opError.Code)
	}
	switch opError.Retry.Kind {
	case domain.RetryNever,
		domain.RetryImmediate,
		domain.RetryBackoff,
		domain.RetryAfterReconnect,
		domain.RetryAfterAuth,
		domain.RetryAfterConflict,
		domain.RetryAfterReplan:
	default:
		return fmt.Errorf("injected error retry advice %q is invalid", opError.Retry.Kind)
	}
	if opError.Retry.After < 0 {
		return fmt.Errorf("injected error retry delay must not be negative")
	}
	switch opError.Effect {
	case domain.EffectNone, domain.EffectApplied, domain.EffectUnknown:
	default:
		return fmt.Errorf("injected error effect %q is invalid", opError.Effect)
	}
	return nil
}

func disconnectOperation(operation Operation) bool {
	switch operation {
	case OperationList,
		OperationStat,
		OperationOpenRead,
		OperationRead,
		OperationOpenWrite,
		OperationWrite,
		OperationSyncWrite,
		OperationMkdir,
		OperationRename,
		OperationRemove:
		return true
	default:
		return false
	}
}

func (s *scriptState) record(operation Operation, location *domain.Location) (Call, FaultEffect, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.nextSequence++
	call := Call{
		Operation: operation,
		Location:  cloneLocation(location),
		Sequence:  s.nextSequence,
	}
	s.calls = append(s.calls, cloneCall(call))

	mode, scripted := s.modes[operation]
	if !scripted {
		return call, FaultEffect{}, false
	}
	key := matcherKey{operation: operation, mode: mode}
	if mode == matcherExact {
		if location == nil {
			return call, FaultEffect{}, false
		}
		key.path = location.Path
	}
	s.counts[key]++
	count := s.counts[key]
	for index := range s.steps {
		step := &s.steps[index]
		if step.consumed || step.match.Operation != operation || step.match.Nth != count {
			continue
		}
		if mode == matcherExact && (step.match.Path == nil || *step.match.Path != key.path) {
			continue
		}
		step.consumed = true
		return call, cloneFaultEffect(step.effect), true
	}
	return call, FaultEffect{}, false
}

func (s *scriptState) callsCopy() []Call {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.calls == nil {
		return nil
	}
	result := make([]Call, len(s.calls))
	for index, call := range s.calls {
		result[index] = cloneCall(call)
	}
	return result
}

func (s *scriptState) releaseGate(name string) bool {
	gate, exists := s.gates[name]
	if !exists {
		return false
	}
	gate.once.Do(func() { close(gate.released) })
	return true
}

func (p *Provider) recordOperation(
	ctx context.Context,
	operation Operation,
	location *domain.Location,
) (FaultEffect, error) {
	call, effect, matched := p.script.record(operation, location)
	if !matched {
		return FaultEffect{}, nil
	}
	if err := p.waitForFault(ctx, operation, location, effect); err != nil {
		return effect, err
	}
	if effect.Error != nil {
		effect.Error = enrichInjectedError(effect.Error, call, p.endpoint.ID)
		if effect.MaxReadBytes == 0 && effect.MaxWriteBytes == 0 {
			return effect, effect.Error
		}
	}
	if effect.MaxReadBytes > 0 || effect.MaxWriteBytes > 0 ||
		effect.StaleNodeRevision != nil || effect.Disconnect || effect.NonAtomicRename {
		return effect, nil
	}
	if effect.Error == nil && effect.MaxReadBytes == 0 && effect.MaxWriteBytes == 0 &&
		effect.StaleNodeRevision == nil && !effect.Disconnect && !effect.NonAtomicRename {
		return effect, nil
	}
	return effect, p.opError(
		domain.CodeUnsupported,
		string(operation),
		location,
		"selected primary fault effect is not implemented in the current Task 8 checkpoint",
		domain.RetryNever,
		nil,
	)
}

func (p *Provider) waitForFault(
	ctx context.Context,
	operation Operation,
	location *domain.Location,
	effect FaultEffect,
) error {
	if effect.Delay > 0 {
		timer := p.clock.NewTimer(effect.Delay)
		select {
		case <-timer.C():
		case <-ctx.Done():
			timer.Stop()
			return p.checkContext(ctx, string(operation), location)
		}
	}
	if effect.WaitGate != "" {
		gate := p.script.gates[effect.WaitGate]
		if gate == nil {
			return p.opError(
				domain.CodeInternal,
				string(operation),
				location,
				"selected fault gate is not initialized",
				domain.RetryNever,
				nil,
			)
		}
		select {
		case <-gate.released:
		case <-ctx.Done():
			return p.checkContext(ctx, string(operation), location)
		}
	}
	return p.checkContext(ctx, string(operation), location)
}

func enrichInjectedError(
	opError *domain.OpError,
	call Call,
	endpointID domain.EndpointID,
) *domain.OpError {
	cloned := cloneOpError(opError)
	if cloned.Operation == "" {
		cloned.Operation = string(call.Operation)
	}
	if cloned.EndpointID == "" {
		cloned.EndpointID = endpointID
	}
	if cloned.Location == nil {
		cloned.Location = cloneLocation(call.Location)
	}
	return cloned
}

func cloneFaultStep(step FaultStep) FaultStep {
	return FaultStep{
		Match: FaultMatch{
			Operation: step.Match.Operation,
			Nth:       step.Match.Nth,
			Path:      clonePointer(step.Match.Path),
		},
		Effect: cloneFaultEffect(step.Effect),
	}
}

func cloneFaultEffect(effect FaultEffect) FaultEffect {
	cloned := effect
	cloned.Error = cloneOpError(effect.Error)
	cloned.StaleNodeRevision = clonePointer(effect.StaleNodeRevision)
	return cloned
}

func cloneOpError(opError *domain.OpError) *domain.OpError {
	if opError == nil {
		return nil
	}
	cloned := *opError
	cloned.Location = cloneLocation(opError.Location)
	return &cloned
}

func cloneCall(call Call) Call {
	cloned := call
	cloned.Location = cloneLocation(call.Location)
	return cloned
}
