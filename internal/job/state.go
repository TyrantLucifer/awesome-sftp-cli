// Package job owns durable operation state and transition rules.
package job

type State string

const (
	StateDraft                       State = "draft"
	StateAwaitingConfirmation        State = "awaiting_confirmation"
	StateQueued                      State = "queued"
	StateRunning                     State = "running"
	StateVerifying                   State = "verifying"
	StatePaused                      State = "paused"
	StateWaitingAuth                 State = "waiting_auth"
	StateWaitingConflict             State = "waiting_conflict"
	StateRetryWait                   State = "retry_wait"
	StateCompleted                   State = "completed"
	StateCompletedWithSourceRetained State = "completed_with_source_retained"
	StateFailed                      State = "failed"
	StateCanceled                    State = "canceled"
)

var states = []State{
	StateDraft,
	StateAwaitingConfirmation,
	StateQueued,
	StateRunning,
	StateVerifying,
	StatePaused,
	StateWaitingAuth,
	StateWaitingConflict,
	StateRetryWait,
	StateCompleted,
	StateCompletedWithSourceRetained,
	StateFailed,
	StateCanceled,
}

var transitions = map[State]map[State]struct{}{
	StateDraft:                set(StateAwaitingConfirmation, StateQueued, StateCanceled),
	StateAwaitingConfirmation: set(StateQueued, StateCanceled),
	StateQueued:               set(StateRunning, StatePaused, StateCanceled),
	StateRunning:              set(StateVerifying, StatePaused, StateWaitingAuth, StateWaitingConflict, StateRetryWait, StateFailed, StateCanceled),
	StateVerifying:            set(StateCompleted, StateCompletedWithSourceRetained, StatePaused, StateWaitingAuth, StateWaitingConflict, StateRetryWait, StateFailed, StateCanceled),
	StatePaused:               set(StateQueued, StateCanceled),
	StateWaitingAuth:          set(StateQueued, StateCanceled),
	StateWaitingConflict:      set(StateQueued, StateCanceled),
	StateRetryWait:            set(StateQueued, StateCanceled),
}

func AllStates() []State {
	result := make([]State, len(states))
	copy(result, states)
	return result
}

func CanTransition(from, to State) bool {
	_, allowed := transitions[from][to]
	return allowed
}

func (state State) Valid() bool {
	for _, candidate := range states {
		if candidate == state {
			return true
		}
	}
	return false
}

func (state State) Terminal() bool {
	return state == StateCompleted || state == StateCompletedWithSourceRetained || state == StateFailed || state == StateCanceled
}

// ConservativeRestartState pauses phases that may have had an in-flight
// external effect. Stable queued/wait/control states remain unchanged.
func ConservativeRestartState(state State) (State, bool) {
	if state == StateRunning || state == StateVerifying {
		return StatePaused, true
	}
	return state, false
}

func set(states ...State) map[State]struct{} {
	result := make(map[State]struct{}, len(states))
	for _, state := range states {
		result[state] = struct{}{}
	}
	return result
}
