package fake

import (
	"context"
	"errors"
	"reflect"
	"sort"
	"testing"
	"time"

	"github.com/TyrantLucifer/awesome-sftp-cli/internal/domain"
	providerapi "github.com/TyrantLucifer/awesome-sftp-cli/internal/provider"
)

const d3SessionB domain.SessionID = "sess_bbbbbbbbbbbbbbbbbbbbbbbbbb"

func TestNewRejectsUnknownConnectionState(t *testing.T) {
	for _, state := range []domain.ConnectionState{"", "unknown"} {
		scenario := validScenario(t)
		scenario.Snapshot.State = state
		if _, _, err := New(scenario); err == nil {
			t.Fatalf("New() accepted connection state %q", state)
		}
	}

	for _, state := range []domain.ConnectionState{
		domain.StateDisconnected,
		domain.StateConnecting,
		domain.StateReady,
		domain.StateDegraded,
		domain.StateAuthRequired,
		domain.StateFailed,
	} {
		scenario := validScenario(t)
		scenario.Snapshot.State = state
		if _, _, err := New(scenario); err != nil {
			t.Fatalf("New() rejected known connection state %q: %v", state, err)
		}
	}
}

func TestSetSnapshotRejectsInvalidTransitionsAtomically(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*domain.EndpointSnapshot)
	}{
		{
			name: "endpoint mismatch",
			mutate: func(snapshot *domain.EndpointSnapshot) {
				snapshot.EndpointID = "ep_bbbbbbbbbbbbbbbbbbbbbbbbbb"
			},
		},
		{
			name: "empty session",
			mutate: func(snapshot *domain.EndpointSnapshot) {
				snapshot.SessionID = ""
				snapshot.Capabilities.Revision.SessionID = ""
			},
		},
		{
			name: "capability session mismatch",
			mutate: func(snapshot *domain.EndpointSnapshot) {
				snapshot.Capabilities.Revision.SessionID = d3SessionB
			},
		},
		{
			name: "zero generation",
			mutate: func(snapshot *domain.EndpointSnapshot) {
				snapshot.Capabilities.Revision.Generation = 0
			},
		},
		{
			name: "duplicate capabilities",
			mutate: func(snapshot *domain.EndpointSnapshot) {
				snapshot.Capabilities.Revision.Generation++
				snapshot.Capabilities.Items = []domain.Capability{
					{Name: "duplicate", Version: 1},
					{Name: "duplicate", Version: 2},
				}
			},
		},
		{
			name: "empty state",
			mutate: func(snapshot *domain.EndpointSnapshot) {
				snapshot.State = ""
			},
		},
		{
			name: "unknown state",
			mutate: func(snapshot *domain.EndpointSnapshot) {
				snapshot.State = "unknown"
			},
		},
		{
			name: "lower same-session generation",
			mutate: func(snapshot *domain.EndpointSnapshot) {
				snapshot.Capabilities.Revision.Generation--
			},
		},
		{
			name: "equal generation changed completeness",
			mutate: func(snapshot *domain.EndpointSnapshot) {
				snapshot.Capabilities.Complete = !snapshot.Capabilities.Complete
			},
		},
		{
			name: "equal generation changed items",
			mutate: func(snapshot *domain.EndpointSnapshot) {
				snapshot.Capabilities.Items = []domain.Capability{{Name: "changed", Version: 1}}
			},
		},
		{
			name: "equal generation changed version",
			mutate: func(snapshot *domain.EndpointSnapshot) {
				snapshot.Capabilities.Items[0].Version++
			},
		},
		{
			name: "equal generation changed constraints",
			mutate: func(snapshot *domain.EndpointSnapshot) {
				snapshot.Capabilities.Items[0].Constraints = []domain.CapabilityConstraint{{
					Name: "mode", Value: "changed",
				}}
			},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			implementation, controller, cursor := prepareD3AtomicFixture(t)
			before := captureD3State(implementation)
			beforeCalls := controller.Calls()
			candidate := cloneSnapshot(before.snapshot)
			candidate.ObservedAt = candidate.ObservedAt.Add(time.Minute)
			test.mutate(&candidate)

			err := controller.SetSnapshot(candidate)
			requireD3ControllerError(t, err, domain.CodeInvalidArgument, "set_snapshot")
			after := captureD3State(implementation)
			if !reflect.DeepEqual(after, before) {
				t.Fatalf("rejected SetSnapshot() changed state:\nbefore=%#v\nafter=%#v", before, after)
			}
			if afterCalls := controller.Calls(); !reflect.DeepEqual(afterCalls, beforeCalls) {
				t.Fatalf("SetSnapshot recorded Provider Call: before=%#v after=%#v", beforeCalls, afterCalls)
			}
			requireD3CursorRetained(t, implementation, cursor)
		})
	}

	var detached *Controller
	before := validScenario(t).Snapshot
	requireD3ControllerError(
		t,
		detached.SetSnapshot(before),
		domain.CodeInternal,
		"set_snapshot",
	)
	requireD3ControllerError(
		t,
		(&Controller{}).SetSnapshot(before),
		domain.CodeInternal,
		"set_snapshot",
	)
}

func TestSetSnapshotEqualGenerationRequiresExactCanonicalPayload(t *testing.T) {
	t.Run("nil versus empty items", func(t *testing.T) {
		scenario := validScenario(t)
		scenario.Snapshot.Capabilities = domain.CapabilitySnapshot{
			Revision: domain.CapabilityRevision{SessionID: contractSessionID, Generation: 1},
			Complete: false,
			Items:    nil,
		}
		implementation, controller, err := New(scenario)
		if err != nil {
			t.Fatalf("New(): %v", err)
		}
		before := captureD3State(implementation)
		candidate := cloneSnapshot(before.snapshot)
		candidate.Capabilities.Items = []domain.Capability{}
		requireD3ControllerError(t, controller.SetSnapshot(candidate), domain.CodeInvalidArgument, "set_snapshot")
		if after := captureD3State(implementation); !reflect.DeepEqual(after, before) {
			t.Fatalf("nil/empty item rejection changed state: before=%#v after=%#v", before, after)
		}
	})

	t.Run("nil versus empty constraints", func(t *testing.T) {
		implementation, controller, err := New(validScenario(t))
		if err != nil {
			t.Fatalf("New(): %v", err)
		}
		before := captureD3State(implementation)
		candidate := cloneSnapshot(before.snapshot)
		candidate.Capabilities.Items[0].Constraints = []domain.CapabilityConstraint{}
		requireD3ControllerError(t, controller.SetSnapshot(candidate), domain.CodeInvalidArgument, "set_snapshot")
		if after := captureD3State(implementation); !reflect.DeepEqual(after, before) {
			t.Fatalf("nil/empty constraint rejection changed state: before=%#v after=%#v", before, after)
		}
	})

	t.Run("constraint order", func(t *testing.T) {
		scenario := validScenario(t)
		scenario.Snapshot.Capabilities = domain.CapabilitySnapshot{
			Revision: domain.CapabilityRevision{SessionID: contractSessionID, Generation: 1},
			Complete: true,
			Items: []domain.Capability{{
				Name: "read", Version: 1,
				Constraints: []domain.CapabilityConstraint{
					{Name: "first", Value: "1"},
					{Name: "second", Value: "2"},
				},
			}},
		}
		implementation, controller, err := New(scenario)
		if err != nil {
			t.Fatalf("New(): %v", err)
		}
		before := captureD3State(implementation)
		candidate := cloneSnapshot(before.snapshot)
		constraints := candidate.Capabilities.Items[0].Constraints
		constraints[0], constraints[1] = constraints[1], constraints[0]
		requireD3ControllerError(t, controller.SetSnapshot(candidate), domain.CodeInvalidArgument, "set_snapshot")
		if after := captureD3State(implementation); !reflect.DeepEqual(after, before) {
			t.Fatalf("constraint-order rejection changed state: before=%#v after=%#v", before, after)
		}
	})
}

func TestSetSnapshotUsesInputObservedAtWithoutSamplingClock(t *testing.T) {
	var clockCalls int
	clock := &callbackClock{now: func() time.Time {
		clockCalls++
		return time.Date(2030, time.January, 1, 0, 0, 0, 0, time.UTC)
	}}
	scenario := validScenario(t)
	scenario.Clock = clock
	implementation, controller, err := New(scenario)
	if err != nil {
		t.Fatalf("New(): %v", err)
	}
	next := captureD3State(implementation).snapshot
	next.ObservedAt = time.Date(2026, time.July, 14, 12, 34, 56, 0, time.UTC)
	if err := controller.SetSnapshot(next); err != nil {
		t.Fatalf("SetSnapshot(): %v", err)
	}
	if clockCalls != 0 {
		t.Fatalf("SetSnapshot sampled Clock.Now %d times", clockCalls)
	}
	if got := captureD3State(implementation).snapshot.ObservedAt; !got.Equal(next.ObservedAt) {
		t.Fatalf("SetSnapshot ObservedAt = %v, want input %v", got, next.ObservedAt)
	}
}

func TestSetSnapshotSameSessionGenerationAndOwnership(t *testing.T) {
	implementation, controller, err := New(validScenario(t))
	if err != nil {
		t.Fatalf("New(): %v", err)
	}

	initial := captureD3State(implementation)
	disconnected := cloneSnapshot(initial.snapshot)
	disconnected.State = domain.StateDisconnected
	disconnected.ObservedAt = disconnected.ObservedAt.Add(time.Minute)
	if err := controller.SetSnapshot(disconnected); err != nil {
		t.Fatalf("SetSnapshot(equal disconnect): %v", err)
	}
	reconnected := cloneSnapshot(disconnected)
	reconnected.State = domain.StateReady
	reconnected.ObservedAt = reconnected.ObservedAt.Add(time.Minute)
	if err := controller.SetSnapshot(reconnected); err != nil {
		t.Fatalf("SetSnapshot(equal reconnect): %v", err)
	}
	if got := captureD3State(implementation).sessionEpoch; got != initial.sessionEpoch {
		t.Fatalf("same-session epoch = %d, want %d", got, initial.sessionEpoch)
	}

	constraints := []domain.CapabilityConstraint{{Name: "algorithm", Value: "sha256"}}
	items := []domain.Capability{
		{Name: "write", Version: 2},
		{Name: "read", Version: 3, Constraints: constraints},
	}
	higher := domain.EndpointSnapshot{
		EndpointID: implementation.endpoint.ID,
		SessionID:  contractSessionID,
		State:      domain.StateDegraded,
		Capabilities: domain.CapabilitySnapshot{
			Revision: domain.CapabilityRevision{SessionID: contractSessionID, Generation: 2},
			Complete: true,
			Items:    items,
		},
		ObservedAt: reconnected.ObservedAt.Add(time.Minute),
	}
	if err := controller.SetSnapshot(higher); err != nil {
		t.Fatalf("SetSnapshot(higher): %v", err)
	}
	items[0].Name = "caller-mutated"
	constraints[0].Value = "caller-mutated"
	higher.Capabilities.Items[1].Constraints[0].Name = "input-mutated"

	stored, err := implementation.Snapshot(context.Background())
	if err != nil {
		t.Fatalf("Snapshot(): %v", err)
	}
	if stored.State != domain.StateDegraded || !stored.ObservedAt.Equal(higher.ObservedAt) {
		t.Fatalf("stored endpoint snapshot = %#v, want degraded at %v", stored, higher.ObservedAt)
	}
	if got := stored.Capabilities.Items; len(got) != 2 || got[0].Name != "read" ||
		got[0].Constraints[0] != (domain.CapabilityConstraint{Name: "algorithm", Value: "sha256"}) ||
		got[1].Name != "write" {
		t.Fatalf("stored capabilities observed caller mutation or were not canonicalized: %#v", got)
	}
	stored.Capabilities.Items[0].Constraints[0].Value = "returned-mutation"
	again, err := implementation.Snapshot(context.Background())
	if err != nil {
		t.Fatalf("Snapshot(second): %v", err)
	}
	if got := again.Capabilities.Items[0].Constraints[0].Value; got != "sha256" {
		t.Fatalf("Snapshot() returned shared constraints: %q", got)
	}

	equalReordered := cloneSnapshot(again)
	equalReordered.Capabilities.Items[0], equalReordered.Capabilities.Items[1] =
		equalReordered.Capabilities.Items[1], equalReordered.Capabilities.Items[0]
	equalReordered.State = domain.StateReady
	equalReordered.ObservedAt = equalReordered.ObservedAt.Add(time.Minute)
	if err := controller.SetSnapshot(equalReordered); err != nil {
		t.Fatalf("SetSnapshot(equal canonical payload): %v", err)
	}

	lower := cloneSnapshot(equalReordered)
	lower.Capabilities.Revision.Generation = 1
	rejectedBefore := captureD3State(implementation)
	requireD3ControllerError(
		t,
		controller.SetSnapshot(lower),
		domain.CodeInvalidArgument,
		"set_snapshot",
	)
	if rejectedAfter := captureD3State(implementation); !reflect.DeepEqual(rejectedAfter, rejectedBefore) {
		t.Fatalf("lower generation changed state: before=%#v after=%#v", rejectedBefore, rejectedAfter)
	}
}

func TestSetSnapshotNewSessionRestartsGenerationAndUsesMonotonicEpoch(t *testing.T) {
	implementation, controller, err := New(validScenario(t))
	if err != nil {
		t.Fatalf("New(): %v", err)
	}
	mutable := requireMutable(t, implementation)
	file := domain.Location{EndpointID: contractEndpointID, Path: "/contract-file"}
	oldReadInterface, err := implementation.OpenRead(context.Background(), providerapi.OpenReadRequest{
		Location: file,
	})
	if err != nil {
		t.Fatalf("OpenRead(old A): %v", err)
	}
	oldRead := oldReadInterface.(*readHandle)
	oldWriteInterface, err := mutable.OpenWrite(context.Background(), providerapi.OpenWriteRequest{
		Location:    file,
		Disposition: providerapi.WriteResumeExisting,
	})
	if err != nil {
		t.Fatalf("OpenWrite(old A): %v", err)
	}
	oldWrite := oldWriteInterface.(*writeHandle)
	if oldRead.sessionEpoch != 1 || oldWrite.sessionEpoch != 1 {
		t.Fatalf("initial handle epochs = (%d,%d), want (1,1)", oldRead.sessionEpoch, oldWrite.sessionEpoch)
	}
	cursor := issueD3Cursor(t, implementation)

	if err := controller.SetCapabilities(domain.CapabilitySnapshot{
		Revision: domain.CapabilityRevision{SessionID: contractSessionID, Generation: 2},
		Complete: true,
	}); err != nil {
		t.Fatalf("SetCapabilities(withdraw read): %v", err)
	}
	if got := d3CapabilitySet(captureD3State(implementation).capabilityLost); !reflect.DeepEqual(got, []domain.CapabilityName{"read"}) {
		t.Fatalf("confirmed loss before new session = %v, want [read]", got)
	}

	sessionB := d3EndpointSnapshot(
		implementation.endpoint.ID,
		d3SessionB,
		domain.StateReady,
		1,
		nil,
		time.Date(2026, time.July, 14, 13, 0, 0, 0, time.UTC),
	)
	if err := controller.SetSnapshot(sessionB); err != nil {
		t.Fatalf("SetSnapshot(A -> B generation restart): %v", err)
	}
	afterB := captureD3State(implementation)
	if afterB.sessionEpoch != 2 || len(afterB.capabilitySeen) != 0 || len(afterB.capabilityLost) != 0 {
		t.Fatalf("state after new session B = %#v, want epoch 2 and cleared history", afterB)
	}
	requireD3CursorCleared(t, implementation, cursor)
	if oldRead.sessionEpoch != 1 || oldWrite.sessionEpoch != 1 {
		t.Fatalf("old A handle epochs changed to (%d,%d)", oldRead.sessionEpoch, oldWrite.sessionEpoch)
	}

	freshReadInterface, err := implementation.OpenRead(context.Background(), providerapi.OpenReadRequest{
		Location: file,
	})
	if err != nil {
		t.Fatalf("OpenRead(B): %v", err)
	}
	freshRead := freshReadInterface.(*readHandle)
	freshWriteInterface, err := mutable.OpenWrite(context.Background(), providerapi.OpenWriteRequest{
		Location:    file,
		Disposition: providerapi.WriteResumeExisting,
	})
	if err != nil {
		t.Fatalf("OpenWrite(B): %v", err)
	}
	freshWrite := freshWriteInterface.(*writeHandle)
	if freshRead.sessionEpoch != 2 || freshWrite.sessionEpoch != 2 {
		t.Fatalf("B handle epochs = (%d,%d), want (2,2)", freshRead.sessionEpoch, freshWrite.sessionEpoch)
	}

	sessionAAgain := d3EndpointSnapshot(
		implementation.endpoint.ID,
		contractSessionID,
		domain.StateReady,
		1,
		[]domain.Capability{{Name: "write", Version: 1}},
		sessionB.ObservedAt.Add(time.Minute),
	)
	if err := controller.SetSnapshot(sessionAAgain); err != nil {
		t.Fatalf("SetSnapshot(B -> A generation restart): %v", err)
	}
	afterAAgain := captureD3State(implementation)
	if afterAAgain.sessionEpoch != 3 {
		t.Fatalf("A -> B -> A epoch = %d, want 3", afterAAgain.sessionEpoch)
	}
	if got := d3CapabilitySet(afterAAgain.capabilitySeen); !reflect.DeepEqual(got, []domain.CapabilityName{"write"}) {
		t.Fatalf("A-again seen capabilities = %v, want [write]", got)
	}
	if len(afterAAgain.capabilityLost) != 0 {
		t.Fatalf("A-again inherited capability loss: %v", afterAAgain.capabilityLost)
	}
	if oldRead.sessionEpoch != 1 || freshRead.sessionEpoch != 2 {
		t.Fatalf("A -> B -> A revived handle epoch bookkeeping: old=%d B=%d", oldRead.sessionEpoch, freshRead.sessionEpoch)
	}

	if err := controller.SetCapabilities(domain.CapabilitySnapshot{
		Revision: domain.CapabilityRevision{SessionID: contractSessionID, Generation: 2},
		Complete: true,
		Items:    []domain.Capability{{Name: "write", Version: 2}},
	}); err != nil {
		t.Fatalf("SetCapabilities(A again): %v", err)
	}
	if err := controller.ChangeCapabilities(context.Background(), true, []domain.Capability{{Name: "write", Version: 3}}); err != nil {
		t.Fatalf("ChangeCapabilities(A again): %v", err)
	}
	sameSession := captureD3State(implementation).snapshot
	sameSession.State = domain.StateDegraded
	if err := controller.SetSnapshot(sameSession); err != nil {
		t.Fatalf("SetSnapshot(equal A again): %v", err)
	}
	if got := captureD3State(implementation).sessionEpoch; got != 3 {
		t.Fatalf("same-session capability/state changes advanced epoch to %d", got)
	}

	for name, handle := range map[string]providerapi.ReadHandle{
		"old A": oldRead,
		"B":     freshRead,
	} {
		if err := handle.Close(context.Background()); err != nil {
			t.Fatalf("Close(%s read): %v", name, err)
		}
	}
	for name, handle := range map[string]providerapi.WriteHandle{
		"old A": oldWrite,
		"B":     freshWrite,
	} {
		if err := handle.Close(context.Background()); err != nil {
			t.Fatalf("Close(%s write): %v", name, err)
		}
	}
}

func TestSetSnapshotRejectsSessionEpochExhaustionAtomically(t *testing.T) {
	implementation, controller, err := New(validScenario(t))
	if err != nil {
		t.Fatalf("New(): %v", err)
	}
	cursor := issueD3Cursor(t, implementation)
	implementation.mu.Lock()
	implementation.sessionEpoch = ^uint64(0)
	implementation.mu.Unlock()
	before := captureD3State(implementation)
	candidate := d3EndpointSnapshot(
		implementation.endpoint.ID,
		d3SessionB,
		domain.StateReady,
		1,
		nil,
		before.snapshot.ObservedAt.Add(time.Minute),
	)
	err = controller.SetSnapshot(candidate)
	requireD3ControllerError(t, err, domain.CodeResourceExhausted, "set_snapshot")
	if after := captureD3State(implementation); !reflect.DeepEqual(after, before) {
		t.Fatalf("epoch exhaustion changed state: before=%#v after=%#v", before, after)
	}
	requireD3CursorRetained(t, implementation, cursor)
	sameSession := cloneSnapshot(before.snapshot)
	sameSession.State = domain.StateDegraded
	sameSession.ObservedAt = sameSession.ObservedAt.Add(time.Minute)
	if err := controller.SetSnapshot(sameSession); err != nil {
		t.Fatalf("SetSnapshot(same session at max epoch): %v", err)
	}
	afterSameSession := captureD3State(implementation)
	if afterSameSession.sessionEpoch != ^uint64(0) {
		t.Fatalf("same-session transition changed max epoch to %d", afterSameSession.sessionEpoch)
	}
	requireD3CursorRetained(t, implementation, cursor)
}

func TestSnapshotTransitionCursorClearingMatrix(t *testing.T) {
	states := []struct {
		state  domain.ConnectionState
		retain bool
	}{
		{state: domain.StateReady, retain: true},
		{state: domain.StateDegraded, retain: true},
		{state: domain.StateConnecting},
		{state: domain.StateDisconnected},
		{state: domain.StateAuthRequired},
		{state: domain.StateFailed},
	}

	for _, test := range states {
		t.Run("same_session_to_"+string(test.state), func(t *testing.T) {
			implementation, controller, err := New(validScenario(t))
			if err != nil {
				t.Fatalf("New(): %v", err)
			}
			cursor := issueD3Cursor(t, implementation)
			beforeCounter := captureD3State(implementation).nextCursor
			next := captureD3State(implementation).snapshot
			next.State = test.state
			next.ObservedAt = next.ObservedAt.Add(time.Minute)
			if err := controller.SetSnapshot(next); err != nil {
				t.Fatalf("SetSnapshot(%q): %v", test.state, err)
			}
			if test.retain {
				requireD3CursorRetained(t, implementation, cursor)
				return
			}
			requireD3CursorAbsent(t, implementation, cursor)
			if got := captureD3State(implementation).nextCursor; got != beforeCounter {
				t.Fatalf("cursor clear reset/advanced nextCursor: got %d want %d", got, beforeCounter)
			}
			reconnect := captureD3State(implementation).snapshot
			reconnect.State = domain.StateReady
			reconnect.ObservedAt = reconnect.ObservedAt.Add(time.Minute)
			if err := controller.SetSnapshot(reconnect); err != nil {
				t.Fatalf("SetSnapshot(reconnect): %v", err)
			}
			requireD3CursorCleared(t, implementation, cursor)
			fresh := issueD3Cursor(t, implementation)
			if fresh.Cursor == cursor.Cursor {
				t.Fatalf("cleared cursor token %q was reused", fresh.Cursor)
			}
		})
	}

	t.Run("degraded to ready retains", func(t *testing.T) {
		scenario := validScenario(t)
		scenario.Snapshot.State = domain.StateDegraded
		implementation, controller, err := New(scenario)
		if err != nil {
			t.Fatalf("New(): %v", err)
		}
		cursor := issueD3Cursor(t, implementation)
		next := captureD3State(implementation).snapshot
		next.State = domain.StateReady
		if err := controller.SetSnapshot(next); err != nil {
			t.Fatalf("SetSnapshot(ready): %v", err)
		}
		requireD3CursorRetained(t, implementation, cursor)
	})

	for _, test := range states {
		t.Run("new_session_to_"+string(test.state), func(t *testing.T) {
			implementation, controller, err := New(validScenario(t))
			if err != nil {
				t.Fatalf("New(): %v", err)
			}
			cursor := issueD3Cursor(t, implementation)
			beforeCounter := captureD3State(implementation).nextCursor
			next := d3EndpointSnapshot(
				implementation.endpoint.ID,
				d3SessionB,
				test.state,
				1,
				nil,
				time.Date(2026, time.July, 14, 14, 0, 0, 0, time.UTC),
			)
			if err := controller.SetSnapshot(next); err != nil {
				t.Fatalf("SetSnapshot(new session %q): %v", test.state, err)
			}
			requireD3CursorAbsent(t, implementation, cursor)
			if got := captureD3State(implementation).nextCursor; got != beforeCounter {
				t.Fatalf("new-session cursor clear changed nextCursor: got %d want %d", got, beforeCounter)
			}
			if !test.retain {
				reconnect := captureD3State(implementation).snapshot
				reconnect.State = domain.StateReady
				reconnect.ObservedAt = reconnect.ObservedAt.Add(time.Minute)
				if err := controller.SetSnapshot(reconnect); err != nil {
					t.Fatalf("SetSnapshot(new-session reconnect): %v", err)
				}
			}
			requireD3CursorCleared(t, implementation, cursor)
		})
	}

	t.Run("rejected transition retains", func(t *testing.T) {
		implementation, controller, err := New(validScenario(t))
		if err != nil {
			t.Fatalf("New(): %v", err)
		}
		if err := controller.SetCapabilities(domain.CapabilitySnapshot{
			Revision: domain.CapabilityRevision{SessionID: contractSessionID, Generation: 2},
			Complete: true,
			Items:    []domain.Capability{{Name: "read", Version: 1}},
		}); err != nil {
			t.Fatalf("SetCapabilities(): %v", err)
		}
		cursor := issueD3Cursor(t, implementation)
		next := captureD3State(implementation).snapshot
		next.Capabilities.Revision.Generation = 1
		requireD3ControllerError(t, controller.SetSnapshot(next), domain.CodeInvalidArgument, "set_snapshot")
		requireD3CursorRetained(t, implementation, cursor)
	})

	for _, operation := range []string{"set", "legacy"} {
		t.Run(operation+" capabilities retain", func(t *testing.T) {
			implementation, controller, err := New(validScenario(t))
			if err != nil {
				t.Fatalf("New(): %v", err)
			}
			cursor := issueD3Cursor(t, implementation)
			switch operation {
			case "set":
				err = controller.SetCapabilities(domain.CapabilitySnapshot{
					Revision: domain.CapabilityRevision{SessionID: contractSessionID, Generation: 2},
					Complete: false,
				})
			case "legacy":
				err = controller.ChangeCapabilities(context.Background(), false, nil)
			}
			if err != nil {
				t.Fatalf("capability transition: %v", err)
			}
			requireD3CursorRetained(t, implementation, cursor)
		})
	}
}

func TestSetCapabilitiesRequiresCurrentStrictlyNewerAndOwnsInput(t *testing.T) {
	tests := []struct {
		name       string
		capability domain.CapabilitySnapshot
	}{
		{
			name: "empty session",
			capability: domain.CapabilitySnapshot{
				Revision: domain.CapabilityRevision{Generation: 4},
			},
		},
		{
			name: "different session",
			capability: domain.CapabilitySnapshot{
				Revision: domain.CapabilityRevision{SessionID: d3SessionB, Generation: 4},
			},
		},
		{
			name: "zero generation",
			capability: domain.CapabilitySnapshot{
				Revision: domain.CapabilityRevision{SessionID: contractSessionID},
			},
		},
		{
			name: "lower generation",
			capability: domain.CapabilitySnapshot{
				Revision: domain.CapabilityRevision{SessionID: contractSessionID, Generation: 2},
			},
		},
		{
			name: "equal generation",
			capability: domain.CapabilitySnapshot{
				Revision: domain.CapabilityRevision{SessionID: contractSessionID, Generation: 3},
			},
		},
		{
			name: "duplicate capabilities",
			capability: domain.CapabilitySnapshot{
				Revision: domain.CapabilityRevision{SessionID: contractSessionID, Generation: 4},
				Items: []domain.Capability{
					{Name: "duplicate", Version: 1},
					{Name: "duplicate", Version: 2},
				},
			},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			implementation, controller, err := New(validScenario(t))
			if err != nil {
				t.Fatalf("New(): %v", err)
			}
			if err := controller.SetCapabilities(domain.CapabilitySnapshot{
				Revision: domain.CapabilityRevision{SessionID: contractSessionID, Generation: 3},
				Complete: true,
				Items:    []domain.Capability{{Name: "read", Version: 1}},
			}); err != nil {
				t.Fatalf("SetCapabilities(setup): %v", err)
			}
			cursor := issueD3Cursor(t, implementation)
			before := captureD3State(implementation)
			beforeCalls := controller.Calls()
			err = controller.SetCapabilities(test.capability)
			requireD3ControllerError(t, err, domain.CodeInvalidArgument, "set_capabilities")
			if after := captureD3State(implementation); !reflect.DeepEqual(after, before) {
				t.Fatalf("rejected SetCapabilities() changed state: before=%#v after=%#v", before, after)
			}
			if afterCalls := controller.Calls(); !reflect.DeepEqual(afterCalls, beforeCalls) {
				t.Fatalf("Controller SetCapabilities recorded Provider Call: before=%#v after=%#v", beforeCalls, afterCalls)
			}
			requireD3CursorRetained(t, implementation, cursor)
		})
	}

	var detached *Controller
	requireD3ControllerError(
		t,
		detached.SetCapabilities(domain.CapabilitySnapshot{}),
		domain.CodeInternal,
		"set_capabilities",
	)
	requireD3ControllerError(
		t,
		(&Controller{}).SetCapabilities(domain.CapabilitySnapshot{}),
		domain.CodeInternal,
		"set_capabilities",
	)

	t.Run("valid update owns nested payload", func(t *testing.T) {
		observedAt := time.Date(2026, time.July, 14, 15, 0, 0, 0, time.UTC)
		clock := &callbackClock{now: func() time.Time { return observedAt }}
		scenario := validScenario(t)
		scenario.Clock = clock
		implementation, controller, err := New(scenario)
		if err != nil {
			t.Fatalf("New(): %v", err)
		}
		cursor := issueD3Cursor(t, implementation)
		before := captureD3State(implementation)
		constraints := []domain.CapabilityConstraint{{Name: "algorithm", Value: "sha256"}}
		items := []domain.Capability{
			{Name: "write", Version: 2},
			{Name: "read", Version: 3, Constraints: constraints},
		}
		input := domain.CapabilitySnapshot{
			Revision: domain.CapabilityRevision{SessionID: contractSessionID, Generation: 2},
			Complete: true,
			Items:    items,
		}
		if err := controller.SetCapabilities(input); err != nil {
			t.Fatalf("SetCapabilities(): %v", err)
		}
		items[0].Name = "caller-mutated"
		constraints[0].Value = "caller-mutated"
		input.Items[1].Constraints[0].Name = "input-mutated"
		after := captureD3State(implementation)
		if after.snapshot.EndpointID != before.snapshot.EndpointID ||
			after.snapshot.SessionID != before.snapshot.SessionID ||
			after.snapshot.State != before.snapshot.State ||
			after.sessionEpoch != before.sessionEpoch {
			t.Fatalf("SetCapabilities changed endpoint/session/state/epoch: before=%#v after=%#v", before, after)
		}
		if !after.snapshot.ObservedAt.Equal(observedAt) {
			t.Fatalf("SetCapabilities ObservedAt = %v, want %v", after.snapshot.ObservedAt, observedAt)
		}
		if got := after.snapshot.Capabilities.Items; len(got) != 2 || got[0].Name != "read" ||
			got[0].Constraints[0] != (domain.CapabilityConstraint{Name: "algorithm", Value: "sha256"}) ||
			got[1].Name != "write" {
			t.Fatalf("SetCapabilities retained caller storage: %#v", got)
		}
		requireD3CursorRetained(t, implementation, cursor)
	})
}

func TestSetCapabilitiesRevalidatesExecutionTimeSessionAndGeneration(t *testing.T) {
	tests := []struct {
		name           string
		switchSnapshot func(*Provider) domain.EndpointSnapshot
	}{
		{
			name: "session changes during clock",
			switchSnapshot: func(implementation *Provider) domain.EndpointSnapshot {
				return d3EndpointSnapshot(
					implementation.endpoint.ID,
					d3SessionB,
					domain.StateReady,
					1,
					nil,
					time.Date(2026, time.July, 14, 15, 30, 0, 0, time.UTC),
				)
			},
		},
		{
			name: "generation advances during clock",
			switchSnapshot: func(implementation *Provider) domain.EndpointSnapshot {
				next := captureD3State(implementation).snapshot
				next.Capabilities.Revision.Generation = 3
				next.Capabilities.Items = []domain.Capability{{Name: "read", Version: 3}}
				next.ObservedAt = next.ObservedAt.Add(time.Minute)
				return next
			},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			clock := &callbackClock{}
			scenario := validScenario(t)
			scenario.Clock = clock
			implementation, controller, err := New(scenario)
			if err != nil {
				t.Fatalf("New(): %v", err)
			}
			var switched d3State
			var switchErr error
			clock.now = func() time.Time {
				switchErr = controller.SetSnapshot(test.switchSnapshot(implementation))
				if switchErr == nil {
					switched = captureD3State(implementation)
				}
				return time.Date(2026, time.July, 14, 15, 45, 0, 0, time.UTC)
			}
			input := domain.CapabilitySnapshot{
				Revision: domain.CapabilityRevision{SessionID: contractSessionID, Generation: 2},
				Complete: true,
				Items:    []domain.Capability{{Name: "read", Version: 2}},
			}
			err = controller.SetCapabilities(input)
			if switchErr != nil {
				t.Fatalf("SetSnapshot from Clock.Now: %v", switchErr)
			}
			requireD3ControllerError(t, err, domain.CodeInvalidArgument, "set_capabilities")
			if after := captureD3State(implementation); !reflect.DeepEqual(after, switched) {
				t.Fatalf("rejected stale SetCapabilities changed execution-time state: switched=%#v after=%#v", switched, after)
			}
		})
	}
}

func TestCapabilityLossBookkeepingIsSharedByAllCapabilityEntryPoints(t *testing.T) {
	for _, entrypoint := range []string{"snapshot", "capabilities", "legacy"} {
		t.Run(entrypoint, func(t *testing.T) {
			implementation, controller, err := New(validScenario(t))
			if err != nil {
				t.Fatalf("New(): %v", err)
			}
			assertD3CapabilityBookkeeping(t, implementation, []domain.CapabilityName{"read"}, nil)

			applyD3Capabilities(t, implementation, controller, entrypoint, false, nil)
			assertD3CapabilityBookkeeping(t, implementation, []domain.CapabilityName{"read"}, nil)

			applyD3Capabilities(t, implementation, controller, entrypoint, true, nil)
			assertD3CapabilityBookkeeping(
				t,
				implementation,
				[]domain.CapabilityName{"read"},
				[]domain.CapabilityName{"read"},
			)

			applyD3Capabilities(t, implementation, controller, entrypoint, false, nil)
			assertD3CapabilityBookkeeping(
				t,
				implementation,
				[]domain.CapabilityName{"read"},
				[]domain.CapabilityName{"read"},
			)

			applyD3Capabilities(t, implementation, controller, entrypoint, false, []domain.Capability{{
				Name: "read", Version: 2,
			}})
			assertD3CapabilityBookkeeping(t, implementation, []domain.CapabilityName{"read"}, nil)

			applyD3Capabilities(t, implementation, controller, entrypoint, true, nil)
			assertD3CapabilityBookkeeping(
				t,
				implementation,
				[]domain.CapabilityName{"read"},
				[]domain.CapabilityName{"read"},
			)

			applyD3Capabilities(t, implementation, controller, entrypoint, false, []domain.Capability{{
				Name: "write", Version: 1,
			}})
			assertD3CapabilityBookkeeping(
				t,
				implementation,
				[]domain.CapabilityName{"read", "write"},
				[]domain.CapabilityName{"read"},
			)

			applyD3Capabilities(t, implementation, controller, entrypoint, true, []domain.Capability{
				{Name: "read", Version: 3},
				{Name: "write", Version: 2},
			})
			assertD3CapabilityBookkeeping(
				t,
				implementation,
				[]domain.CapabilityName{"read", "write"},
				nil,
			)

			newSession := d3EndpointSnapshot(
				implementation.endpoint.ID,
				d3SessionB,
				domain.StateReady,
				1,
				nil,
				time.Date(2026, time.July, 14, 16, 0, 0, 0, time.UTC),
			)
			if err := controller.SetSnapshot(newSession); err != nil {
				t.Fatalf("SetSnapshot(new session): %v", err)
			}
			assertD3CapabilityBookkeeping(t, implementation, nil, nil)
		})
	}
}

func TestCapabilityControllerClockNowRunsOutsideProviderLock(t *testing.T) {
	for _, entrypoint := range []string{"capabilities", "legacy"} {
		t.Run(entrypoint, func(t *testing.T) {
			observedAt := time.Date(2026, time.July, 14, 17, 0, 0, 0, time.UTC)
			clock := &callbackClock{}
			scenario := validScenario(t)
			scenario.Clock = clock
			implementation, controller, err := New(scenario)
			if err != nil {
				t.Fatalf("New(): %v", err)
			}
			var calls int
			var providerLockHeld bool
			var reentrantErr error
			clock.now = func() time.Time {
				calls++
				if !implementation.mu.TryRLock() {
					providerLockHeld = true
					return observedAt
				}
				implementation.mu.RUnlock()
				_, reentrantErr = implementation.Snapshot(context.Background())
				return observedAt
			}

			switch entrypoint {
			case "capabilities":
				err = controller.SetCapabilities(domain.CapabilitySnapshot{
					Revision: domain.CapabilityRevision{SessionID: contractSessionID, Generation: 2},
					Complete: true,
					Items:    []domain.Capability{{Name: "read", Version: 2}},
				})
			case "legacy":
				err = controller.ChangeCapabilities(context.Background(), true, []domain.Capability{{
					Name: "read", Version: 2,
				}})
			}
			if err != nil {
				t.Fatalf("capability transition: %v", err)
			}
			if calls != 1 || providerLockHeld || reentrantErr != nil {
				t.Fatalf("Clock.Now calls=%d lockHeld=%t reentrantErr=%v", calls, providerLockHeld, reentrantErr)
			}
			if got := captureD3State(implementation).snapshot.ObservedAt; !got.Equal(observedAt) {
				t.Fatalf("ObservedAt = %v, want %v", got, observedAt)
			}
		})
	}
}

func TestLegacyChangeCapabilitiesValidationCancellationOverflowAndOwnership(t *testing.T) {
	t.Run("pre-canceled context is atomic and skips clock", func(t *testing.T) {
		var clockCalls int
		clock := &callbackClock{now: func() time.Time {
			clockCalls++
			return time.Date(2026, time.July, 14, 18, 0, 0, 0, time.UTC)
		}}
		scenario := validScenario(t)
		scenario.Clock = clock
		implementation, controller, err := New(scenario)
		if err != nil {
			t.Fatalf("New(): %v", err)
		}
		before := captureD3State(implementation)
		beforeCalls := controller.Calls()
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		err = controller.ChangeCapabilities(ctx, true, nil)
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("ChangeCapabilities() error = %v, want context.Canceled", err)
		}
		requireD3ControllerError(t, err, domain.CodeCanceled, "change_capabilities")
		if clockCalls != 0 {
			t.Fatalf("pre-canceled ChangeCapabilities sampled Clock.Now %d times", clockCalls)
		}
		if after := captureD3State(implementation); !reflect.DeepEqual(after, before) {
			t.Fatalf("pre-canceled ChangeCapabilities changed state: before=%#v after=%#v", before, after)
		}
		if afterCalls := controller.Calls(); !reflect.DeepEqual(afterCalls, beforeCalls) {
			t.Fatalf("pre-canceled Controller call changed Provider calls: before=%#v after=%#v", beforeCalls, afterCalls)
		}
	})

	t.Run("cancellation from clock callback is atomic", func(t *testing.T) {
		clock := &callbackClock{}
		scenario := validScenario(t)
		scenario.Clock = clock
		implementation, controller, err := New(scenario)
		if err != nil {
			t.Fatalf("New(): %v", err)
		}
		ctx, cancel := context.WithCancel(context.Background())
		clock.now = func() time.Time {
			cancel()
			return time.Date(2026, time.July, 14, 18, 30, 0, 0, time.UTC)
		}
		before := captureD3State(implementation)
		err = controller.ChangeCapabilities(ctx, true, nil)
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("ChangeCapabilities() error = %v, want context.Canceled", err)
		}
		requireD3ControllerError(t, err, domain.CodeCanceled, "change_capabilities")
		if after := captureD3State(implementation); !reflect.DeepEqual(after, before) {
			t.Fatalf("ChangeCapabilities canceled after clock changed state: before=%#v after=%#v", before, after)
		}
	})

	t.Run("generation overflow is atomic", func(t *testing.T) {
		implementation, controller, err := New(validScenario(t))
		if err != nil {
			t.Fatalf("New(): %v", err)
		}
		maximum := captureD3State(implementation).snapshot
		maximum.Capabilities.Revision.Generation = ^uint64(0)
		maximum.ObservedAt = maximum.ObservedAt.Add(time.Minute)
		if err := controller.SetSnapshot(maximum); err != nil {
			t.Fatalf("SetSnapshot(max generation): %v", err)
		}
		cursor := issueD3Cursor(t, implementation)
		before := captureD3State(implementation)
		err = controller.ChangeCapabilities(context.Background(), true, nil)
		requireD3ControllerError(t, err, domain.CodeResourceExhausted, "change_capabilities")
		if after := captureD3State(implementation); !reflect.DeepEqual(after, before) {
			t.Fatalf("generation overflow changed state: before=%#v after=%#v", before, after)
		}
		requireD3CursorRetained(t, implementation, cursor)
	})

	t.Run("duplicate payload is atomic", func(t *testing.T) {
		var clockCalls int
		clock := &callbackClock{now: func() time.Time {
			clockCalls++
			return time.Date(2026, time.July, 14, 18, 45, 0, 0, time.UTC)
		}}
		scenario := validScenario(t)
		scenario.Clock = clock
		implementation, controller, err := New(scenario)
		if err != nil {
			t.Fatalf("New(): %v", err)
		}
		before := captureD3State(implementation)
		err = controller.ChangeCapabilities(context.Background(), true, []domain.Capability{
			{Name: "duplicate", Version: 1},
			{Name: "duplicate", Version: 2},
		})
		requireD3ControllerError(t, err, domain.CodeInvalidArgument, "change_capabilities")
		if clockCalls != 0 {
			t.Fatalf("invalid ChangeCapabilities sampled Clock.Now %d times", clockCalls)
		}
		if after := captureD3State(implementation); !reflect.DeepEqual(after, before) {
			t.Fatalf("invalid legacy payload changed state: before=%#v after=%#v", before, after)
		}
	})

	t.Run("valid update owns payload and retains cursor", func(t *testing.T) {
		observedAt := time.Date(2026, time.July, 14, 19, 0, 0, 0, time.UTC)
		clock := &callbackClock{now: func() time.Time { return observedAt }}
		scenario := validScenario(t)
		scenario.Clock = clock
		implementation, controller, err := New(scenario)
		if err != nil {
			t.Fatalf("New(): %v", err)
		}
		cursor := issueD3Cursor(t, implementation)
		constraints := []domain.CapabilityConstraint{{Name: "mode", Value: "before"}}
		items := []domain.Capability{{Name: "read", Version: 2, Constraints: constraints}}
		if err := controller.ChangeCapabilities(context.Background(), true, items); err != nil {
			t.Fatalf("ChangeCapabilities(): %v", err)
		}
		items[0].Name = "caller-mutated"
		constraints[0].Value = "caller-mutated"
		after := captureD3State(implementation)
		if after.snapshot.Capabilities.Revision.Generation != 2 ||
			!after.snapshot.ObservedAt.Equal(observedAt) ||
			after.snapshot.Capabilities.Items[0].Name != "read" ||
			after.snapshot.Capabilities.Items[0].Constraints[0].Value != "before" {
			t.Fatalf("ChangeCapabilities result = %#v", after.snapshot)
		}
		requireD3CursorRetained(t, implementation, cursor)
	})
}

type d3State struct {
	snapshot       domain.EndpointSnapshot
	sessionEpoch   uint64
	capabilitySeen map[domain.CapabilityName]struct{}
	capabilityLost map[domain.CapabilityName]struct{}
	nextCursor     uint64
	cursors        map[providerapi.PageCursor]cursorBinding
}

func captureD3State(implementation *Provider) d3State {
	implementation.mu.RLock()
	defer implementation.mu.RUnlock()
	state := d3State{
		snapshot:       cloneSnapshot(implementation.snapshot),
		sessionEpoch:   implementation.sessionEpoch,
		capabilitySeen: cloneD3CapabilityMap(implementation.capabilitySeen),
		capabilityLost: cloneD3CapabilityMap(implementation.capabilityLost),
		nextCursor:     implementation.nextCursor,
		cursors:        make(map[providerapi.PageCursor]cursorBinding, len(implementation.cursors)),
	}
	for cursor, binding := range implementation.cursors {
		state.cursors[cursor] = cloneD3CursorBinding(binding)
	}
	return state
}

func cloneD3CursorBinding(binding cursorBinding) cursorBinding {
	cloned := binding
	cloned.sort = cloneSortHint(binding.sort)
	if binding.snapshot == nil {
		return cloned
	}
	snapshot := *binding.snapshot
	if binding.snapshot.entries != nil {
		snapshot.entries = make([]domain.Entry, len(binding.snapshot.entries))
		for index, entry := range binding.snapshot.entries {
			snapshot.entries[index] = cloneEntry(entry)
		}
	}
	snapshot.directoryFingerprint = cloneFingerprint(binding.snapshot.directoryFingerprint)
	cloned.snapshot = &snapshot
	return cloned
}

func cloneD3CapabilityMap(source map[domain.CapabilityName]struct{}) map[domain.CapabilityName]struct{} {
	if source == nil {
		return nil
	}
	cloned := make(map[domain.CapabilityName]struct{}, len(source))
	for name := range source {
		cloned[name] = struct{}{}
	}
	return cloned
}

func d3CapabilitySet(source map[domain.CapabilityName]struct{}) []domain.CapabilityName {
	if len(source) == 0 {
		return nil
	}
	names := make([]domain.CapabilityName, 0, len(source))
	for name := range source {
		names = append(names, name)
	}
	sort.Slice(names, func(left int, right int) bool { return names[left] < names[right] })
	return names
}

func assertD3CapabilityBookkeeping(
	t *testing.T,
	implementation *Provider,
	wantSeen []domain.CapabilityName,
	wantLost []domain.CapabilityName,
) {
	t.Helper()
	state := captureD3State(implementation)
	if got := d3CapabilitySet(state.capabilitySeen); !reflect.DeepEqual(got, wantSeen) {
		t.Fatalf("capability seen = %v, want %v", got, wantSeen)
	}
	if got := d3CapabilitySet(state.capabilityLost); !reflect.DeepEqual(got, wantLost) {
		t.Fatalf("capability lost = %v, want %v", got, wantLost)
	}
}

func applyD3Capabilities(
	t *testing.T,
	implementation *Provider,
	controller *Controller,
	entrypoint string,
	complete bool,
	items []domain.Capability,
) {
	t.Helper()
	state := captureD3State(implementation)
	beforeCalls := controller.Calls()
	var err error
	switch entrypoint {
	case "snapshot":
		next := cloneSnapshot(state.snapshot)
		next.Capabilities = domain.CapabilitySnapshot{
			Revision: domain.CapabilityRevision{
				SessionID:  state.snapshot.SessionID,
				Generation: state.snapshot.Capabilities.Revision.Generation + 1,
			},
			Complete: complete,
			Items:    items,
		}
		next.ObservedAt = next.ObservedAt.Add(time.Minute)
		err = controller.SetSnapshot(next)
	case "capabilities":
		err = controller.SetCapabilities(domain.CapabilitySnapshot{
			Revision: domain.CapabilityRevision{
				SessionID:  state.snapshot.SessionID,
				Generation: state.snapshot.Capabilities.Revision.Generation + 1,
			},
			Complete: complete,
			Items:    items,
		})
	case "legacy":
		err = controller.ChangeCapabilities(context.Background(), complete, items)
	default:
		t.Fatalf("unknown D3 capability entrypoint %q", entrypoint)
	}
	if err != nil {
		t.Fatalf("%s capability transition: %v", entrypoint, err)
	}
	if afterCalls := controller.Calls(); !reflect.DeepEqual(afterCalls, beforeCalls) {
		t.Fatalf("%s capability transition recorded Provider Call: before=%#v after=%#v", entrypoint, beforeCalls, afterCalls)
	}
}

func d3EndpointSnapshot(
	endpointID domain.EndpointID,
	sessionID domain.SessionID,
	state domain.ConnectionState,
	generation uint64,
	items []domain.Capability,
	observedAt time.Time,
) domain.EndpointSnapshot {
	return domain.EndpointSnapshot{
		EndpointID: endpointID,
		SessionID:  sessionID,
		State:      state,
		Capabilities: domain.CapabilitySnapshot{
			Revision: domain.CapabilityRevision{SessionID: sessionID, Generation: generation},
			Complete: true,
			Items:    items,
		},
		ObservedAt: observedAt,
	}
}

func prepareD3AtomicFixture(t *testing.T) (*Provider, *Controller, providerapi.ListRequest) {
	t.Helper()
	scenario := validScenario(t)
	scenario.Snapshot.Capabilities = domain.CapabilitySnapshot{
		Revision: domain.CapabilityRevision{SessionID: contractSessionID, Generation: 1},
		Complete: true,
		Items: []domain.Capability{
			{Name: "read", Version: 1},
			{Name: "write", Version: 1},
		},
	}
	implementation, controller, err := New(scenario)
	if err != nil {
		t.Fatalf("New(): %v", err)
	}
	cursor := issueD3Cursor(t, implementation)
	if err := controller.SetCapabilities(domain.CapabilitySnapshot{
		Revision: domain.CapabilityRevision{SessionID: contractSessionID, Generation: 2},
		Complete: true,
		Items:    []domain.Capability{{Name: "read", Version: 1}},
	}); err != nil {
		t.Fatalf("SetCapabilities(setup loss): %v", err)
	}
	assertD3CapabilityBookkeeping(
		t,
		implementation,
		[]domain.CapabilityName{"read", "write"},
		[]domain.CapabilityName{"write"},
	)
	return implementation, controller, cursor
}

func issueD3Cursor(t *testing.T, implementation *Provider) providerapi.ListRequest {
	t.Helper()
	request := providerapi.ListRequest{
		Location: domain.Location{EndpointID: implementation.endpoint.ID, Path: "/"},
		Limit:    1,
	}
	page, err := implementation.List(context.Background(), request)
	if err != nil {
		t.Fatalf("List(first page): %v", err)
	}
	if page.Done || page.NextCursor == "" {
		t.Fatalf("List(first page) = %#v, want continuation cursor", page)
	}
	request.Cursor = page.NextCursor
	return request
}

func requireD3CursorRetained(
	t *testing.T,
	implementation *Provider,
	request providerapi.ListRequest,
) {
	t.Helper()
	if _, err := implementation.List(context.Background(), request); err != nil {
		t.Fatalf("retained cursor %q failed: %v", request.Cursor, err)
	}
}

func requireD3CursorCleared(
	t *testing.T,
	implementation *Provider,
	request providerapi.ListRequest,
) {
	t.Helper()
	_, err := implementation.List(context.Background(), request)
	opError := requireCode(t, err, domain.CodeInvalidArgument)
	if opError.Operation != "list" || opError.Retry.Kind != domain.RetryNever ||
		opError.Effect != domain.EffectNone || opError.Location == nil ||
		*opError.Location != request.Location || opError.EndpointID != request.Location.EndpointID {
		t.Fatalf("cleared cursor error = %#v", opError)
	}
}

func requireD3CursorAbsent(
	t *testing.T,
	implementation *Provider,
	request providerapi.ListRequest,
) {
	t.Helper()
	implementation.mu.RLock()
	_, exists := implementation.cursors[request.Cursor]
	implementation.mu.RUnlock()
	if exists {
		t.Fatalf("cursor %q remains in Provider map", request.Cursor)
	}
}

func requireD3ControllerError(t *testing.T, err error, code domain.Code, operation string) {
	t.Helper()
	opError := requireCode(t, err, code)
	wantEndpoint := contractEndpointID
	if code == domain.CodeInternal {
		wantEndpoint = ""
	}
	if opError.Operation != operation || opError.Retry.Kind != domain.RetryNever ||
		opError.Effect != domain.EffectNone || opError.Location != nil ||
		opError.EndpointID != wantEndpoint {
		t.Fatalf("controller error = %#v, want %s/%s/never/none with nil location", opError, code, operation)
	}
}
