package job

import "testing"

func TestStateTransitions(t *testing.T) {
	t.Parallel()

	allowed := map[State][]State{
		StateDraft:                {StateAwaitingConfirmation, StateQueued, StateCanceled},
		StateAwaitingConfirmation: {StateQueued, StateCanceled},
		StateQueued:               {StateRunning, StatePaused, StateFailed, StateCanceled},
		StateRunning:              {StateVerifying, StatePaused, StateWaitingAuth, StateWaitingConflict, StateRetryWait, StateFailed, StateCanceled},
		StateVerifying:            {StateCompleted, StateCompletedWithSourceRetained, StatePaused, StateWaitingAuth, StateWaitingConflict, StateRetryWait, StateFailed, StateCanceled},
		StatePaused:               {StateQueued, StateCanceled},
		StateWaitingAuth:          {StateQueued, StateCanceled},
		StateWaitingConflict:      {StateQueued, StateCanceled},
		StateRetryWait:            {StateQueued, StateCanceled},
	}
	states := AllStates()
	for _, from := range states {
		allowedTargets := make(map[State]bool)
		for _, to := range allowed[from] {
			allowedTargets[to] = true
		}
		for _, to := range states {
			if got := CanTransition(from, to); got != allowedTargets[to] {
				t.Fatalf("CanTransition(%q, %q) = %t, want %t", from, to, got, allowedTargets[to])
			}
		}
	}
}

func TestTerminalAndRestartStates(t *testing.T) {
	t.Parallel()

	for _, state := range []State{StateCompleted, StateCompletedWithSourceRetained, StateFailed, StateCanceled} {
		if !state.Terminal() {
			t.Fatalf("%q is not terminal", state)
		}
		if _, changed := ConservativeRestartState(state); changed {
			t.Fatalf("terminal state %q changed on restart", state)
		}
	}
	for _, state := range []State{StateRunning, StateVerifying} {
		got, changed := ConservativeRestartState(state)
		if !changed || got != StatePaused {
			t.Fatalf("ConservativeRestartState(%q) = (%q, %t), want paused,true", state, got, changed)
		}
	}
	for _, state := range []State{StateDraft, StateAwaitingConfirmation, StateQueued, StatePaused, StateWaitingAuth, StateWaitingConflict, StateRetryWait} {
		got, changed := ConservativeRestartState(state)
		if changed || got != state {
			t.Fatalf("ConservativeRestartState(%q) = (%q, %t), want unchanged", state, got, changed)
		}
	}
}
