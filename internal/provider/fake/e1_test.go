package fake

import (
	"context"
	"errors"
	"reflect"
	"testing"
	"time"

	"github.com/TyrantLucifer/awesome-sftp-cli/internal/domain"
	"github.com/TyrantLucifer/awesome-sftp-cli/internal/foundation"
	providerapi "github.com/TyrantLucifer/awesome-sftp-cli/internal/provider"
)

type e1OperationSpec struct {
	operation  Operation
	capability domain.CapabilityName
	location   domain.Location
}

type e1Invocation struct {
	invoke      func(context.Context) error
	cleanup     func()
	assertZero  func(*testing.T)
	readHandle  *readHandle
	writeHandle *writeHandle
}

func TestConnectionStateOperationMatrix(t *testing.T) {
	states := []struct {
		state domain.ConnectionState
		code  domain.Code
		retry domain.RetryKind
	}{
		{state: domain.StateReady},
		{state: domain.StateDegraded},
		{state: domain.StateAuthRequired, code: domain.CodeAuthRequired, retry: domain.RetryAfterAuth},
		{state: domain.StateConnecting, code: domain.CodeTransportInterrupted, retry: domain.RetryAfterReconnect},
		{state: domain.StateDisconnected, code: domain.CodeTransportInterrupted, retry: domain.RetryAfterReconnect},
		{state: domain.StateFailed, code: domain.CodeTransportInterrupted, retry: domain.RetryAfterReconnect},
	}

	for _, state := range states {
		for _, spec := range e1OperationSpecs() {
			t.Run(string(state.state)+"/"+string(spec.operation), func(t *testing.T) {
				implementation, controller, err := New(validScenario(t))
				if err != nil {
					t.Fatalf("New(): %v", err)
				}
				invocation := prepareE1Invocation(t, implementation, spec)
				defer invocation.cleanup()
				setE1State(t, implementation, controller, state.state)

				err = invocation.invoke(context.Background())
				if state.code == "" {
					if err != nil {
						t.Fatalf("%s in state %s: %v", spec.operation, state.state, err)
					}
					return
				}
				requireE1Error(t, err, state.code, state.retry, string(spec.operation), spec.location)
			})
		}
	}
}

func TestCapabilityLossOperationMatrix(t *testing.T) {
	t.Run("initial complete absence does not gate", func(t *testing.T) {
		for _, spec := range e1OperationSpecs() {
			t.Run(string(spec.operation), func(t *testing.T) {
				scenario := validScenario(t)
				scenario.Snapshot.Capabilities = e1CapabilitySnapshot(
					contractSessionID,
					1,
					true,
					nil,
				)
				implementation, _, err := New(scenario)
				if err != nil {
					t.Fatalf("New(): %v", err)
				}
				invocation := prepareE1Invocation(t, implementation, spec)
				defer invocation.cleanup()
				if err := invocation.invoke(context.Background()); err != nil {
					t.Fatalf("%s gated by initial absence: %v", spec.operation, err)
				}
			})
		}
	})

	t.Run("confirmed complete withdrawal gates exact operation map", func(t *testing.T) {
		for _, spec := range e1OperationSpecs() {
			t.Run(string(spec.operation), func(t *testing.T) {
				scenario := e1ReadWriteScenario(t)
				implementation, controller, err := New(scenario)
				if err != nil {
					t.Fatalf("New(): %v", err)
				}
				invocation := prepareE1Invocation(t, implementation, spec)
				defer invocation.cleanup()
				retained := domain.CapabilityName("read")
				if spec.capability == "read" {
					retained = "write"
				}
				if err := controller.SetCapabilities(e1CapabilitySnapshot(
					contractSessionID,
					2,
					true,
					[]domain.Capability{{Name: retained, Version: 1}},
				)); err != nil {
					t.Fatalf("SetCapabilities(withdraw %s): %v", spec.capability, err)
				}

				err = invocation.invoke(context.Background())
				requireE1Error(
					t,
					err,
					domain.CodeCapabilityLost,
					domain.RetryAfterReplan,
					string(spec.operation),
					spec.location,
				)
			})
		}
	})
}

func TestCapabilityLossLifecycle(t *testing.T) {
	for _, spec := range []e1OperationSpec{
		e1Spec(OperationOpenRead),
		e1Spec(OperationOpenWrite),
	} {
		t.Run(string(spec.capability), func(t *testing.T) {
			scenario := e1ReadWriteScenario(t)
			implementation, controller, err := New(scenario)
			if err != nil {
				t.Fatalf("New(): %v", err)
			}

			if err := controller.SetCapabilities(e1CapabilitySnapshot(
				contractSessionID,
				2,
				false,
				nil,
			)); err != nil {
				t.Fatalf("SetCapabilities(incomplete absence): %v", err)
			}
			invokeE1Once(t, implementation, spec)

			if err := controller.SetCapabilities(e1CapabilitySnapshot(
				contractSessionID,
				3,
				true,
				nil,
			)); err != nil {
				t.Fatalf("SetCapabilities(complete absence): %v", err)
			}
			requireE1InvocationError(
				t,
				implementation,
				spec,
			)
			setE1State(t, implementation, controller, domain.StateDisconnected)
			setE1State(t, implementation, controller, domain.StateReady)
			requireE1InvocationError(
				t,
				implementation,
				spec,
			)

			if err := controller.SetCapabilities(e1CapabilitySnapshot(
				contractSessionID,
				4,
				false,
				nil,
			)); err != nil {
				t.Fatalf("SetCapabilities(incomplete after loss): %v", err)
			}
			requireE1InvocationError(
				t,
				implementation,
				spec,
			)

			if err := controller.SetCapabilities(e1CapabilitySnapshot(
				contractSessionID,
				5,
				false,
				[]domain.Capability{{Name: spec.capability, Version: 2}},
			)); err != nil {
				t.Fatalf("SetCapabilities(explicit restore): %v", err)
			}
			invokeE1Once(t, implementation, spec)

			if err := controller.SetCapabilities(e1CapabilitySnapshot(
				contractSessionID,
				6,
				true,
				nil,
			)); err != nil {
				t.Fatalf("SetCapabilities(withdraw again): %v", err)
			}
			requireE1InvocationError(
				t,
				implementation,
				spec,
			)

			newSession := d3EndpointSnapshot(
				contractEndpointID,
				d3SessionB,
				domain.StateReady,
				1,
				nil,
				time.Date(2026, time.July, 14, 15, 0, 0, 0, time.UTC),
			)
			if err := controller.SetSnapshot(newSession); err != nil {
				t.Fatalf("SetSnapshot(new session): %v", err)
			}
			invokeE1Once(t, implementation, spec)
		})
	}
}

func TestCapabilityLossRetainsCursorAndStaysCapabilityAndSessionScoped(t *testing.T) {
	t.Run("loss and restore retain issued cursor", func(t *testing.T) {
		implementation, controller, err := New(e1ReadWriteScenario(t))
		if err != nil {
			t.Fatalf("New(): %v", err)
		}
		cursor := issueD3Cursor(t, implementation)
		if err := controller.SetCapabilities(e1CapabilitySnapshot(
			contractSessionID,
			2,
			true,
			[]domain.Capability{{Name: "write", Version: 1}},
		)); err != nil {
			t.Fatalf("SetCapabilities(withdraw read): %v", err)
		}
		implementation.mu.RLock()
		_, retained := implementation.cursors[cursor.Cursor]
		implementation.mu.RUnlock()
		if !retained {
			t.Fatalf("capability loss cleared cursor %q", cursor.Cursor)
		}
		if err := controller.SetCapabilities(e1CapabilitySnapshot(
			contractSessionID,
			3,
			false,
			[]domain.Capability{{Name: "read", Version: 2}},
		)); err != nil {
			t.Fatalf("SetCapabilities(restore read): %v", err)
		}
		requireD3CursorRetained(t, implementation, cursor)
	})

	t.Run("read and write losses are independent", func(t *testing.T) {
		implementation, controller, err := New(e1ReadWriteScenario(t))
		if err != nil {
			t.Fatalf("New(): %v", err)
		}
		if err := controller.SetCapabilities(e1CapabilitySnapshot(
			contractSessionID,
			2,
			true,
			[]domain.Capability{{Name: "write", Version: 1}},
		)); err != nil {
			t.Fatalf("SetCapabilities(withdraw read): %v", err)
		}
		invokeE1Once(t, implementation, e1Spec(OperationOpenWrite))
		requireE1InvocationError(
			t,
			implementation,
			e1Spec(OperationStat),
		)
	})

	t.Run("new session may confirm a fresh loss", func(t *testing.T) {
		implementation, controller, err := New(e1ReadWriteScenario(t))
		if err != nil {
			t.Fatalf("New(): %v", err)
		}
		if err := controller.SetCapabilities(e1CapabilitySnapshot(
			contractSessionID,
			2,
			true,
			[]domain.Capability{{Name: "write", Version: 1}},
		)); err != nil {
			t.Fatalf("SetCapabilities(withdraw A read): %v", err)
		}
		sessionB := d3EndpointSnapshot(
			contractEndpointID,
			d3SessionB,
			domain.StateReady,
			1,
			[]domain.Capability{{Name: "read", Version: 1}},
			time.Date(2026, time.July, 14, 15, 30, 0, 0, time.UTC),
		)
		if err := controller.SetSnapshot(sessionB); err != nil {
			t.Fatalf("SetSnapshot(B): %v", err)
		}
		invokeE1Once(t, implementation, e1Spec(OperationStat))
		if err := controller.SetCapabilities(e1CapabilitySnapshot(
			d3SessionB,
			2,
			true,
			nil,
		)); err != nil {
			t.Fatalf("SetCapabilities(withdraw B read): %v", err)
		}
		requireE1InvocationError(
			t,
			implementation,
			e1Spec(OperationStat),
		)
	})
}

func TestHandleSessionEpochAndSameSessionReconnect(t *testing.T) {
	scenario := e1ReadWriteScenario(t)
	implementation, controller, err := New(scenario)
	if err != nil {
		t.Fatalf("New(): %v", err)
	}
	location := domain.Location{EndpointID: contractEndpointID, Path: "/contract-file"}
	readInterface, err := implementation.OpenRead(context.Background(), providerapi.OpenReadRequest{
		Location: location,
	})
	if err != nil {
		t.Fatalf("OpenRead(A): %v", err)
	}
	read := readInterface.(*readHandle)
	writeInterface, err := implementation.OpenWrite(context.Background(), providerapi.OpenWriteRequest{
		Location:    location,
		Disposition: providerapi.WriteResumeExisting,
	})
	if err != nil {
		t.Fatalf("OpenWrite(A): %v", err)
	}
	write := writeInterface.(*writeHandle)
	infoBefore := read.Info()

	setE1State(t, implementation, controller, domain.StateDisconnected)
	_, err = read.Read(context.Background(), make([]byte, 1))
	requireE1Error(t, err, domain.CodeTransportInterrupted, domain.RetryAfterReconnect, "read", location)
	_, err = write.Write(context.Background(), []byte("x"))
	requireE1Error(t, err, domain.CodeTransportInterrupted, domain.RetryAfterReconnect, "write", location)

	setE1State(t, implementation, controller, domain.StateReady)
	if n, err := read.Read(context.Background(), make([]byte, 1)); err != nil || n != 1 {
		t.Fatalf("Read(after same-session reconnect) = (%d, %v), want (1, nil)", n, err)
	}
	if n, err := write.Write(context.Background(), []byte("x")); err != nil || n != 1 {
		t.Fatalf("Write(after same-session reconnect) = (%d, %v), want (1, nil)", n, err)
	}
	dataBeforeSessionChange := e1FileData(t, implementation, location.Path)

	sessionB := d3EndpointSnapshot(
		contractEndpointID,
		d3SessionB,
		domain.StateAuthRequired,
		1,
		[]domain.Capability{{Name: "read", Version: 1}, {Name: "write", Version: 1}},
		time.Date(2026, time.July, 14, 16, 0, 0, 0, time.UTC),
	)
	if err := controller.SetSnapshot(sessionB); err != nil {
		t.Fatalf("SetSnapshot(A -> B): %v", err)
	}
	_, err = read.Read(context.Background(), make([]byte, 1))
	requireE1Error(t, err, domain.CodeConflict, domain.RetryAfterReplan, "read", location)
	_, err = write.Write(context.Background(), []byte("y"))
	requireE1Error(t, err, domain.CodeConflict, domain.RetryAfterReplan, "write", location)
	if got := e1FileData(t, implementation, location.Path); !reflect.DeepEqual(got, dataBeforeSessionChange) {
		t.Fatalf("old-session handle changed file: got %q want %q", got, dataBeforeSessionChange)
	}
	sessionBReady := cloneSnapshot(sessionB)
	sessionBReady.State = domain.StateReady
	sessionBReady.ObservedAt = sessionBReady.ObservedAt.Add(time.Minute)
	if err := controller.SetSnapshot(sessionBReady); err != nil {
		t.Fatalf("SetSnapshot(B ready): %v", err)
	}
	bReadInterface, err := implementation.OpenRead(context.Background(), providerapi.OpenReadRequest{Location: location})
	if err != nil {
		t.Fatalf("OpenRead(B): %v", err)
	}
	bRead := bReadInterface.(*readHandle)
	bWriteInterface, err := implementation.OpenWrite(context.Background(), providerapi.OpenWriteRequest{
		Location:    location,
		Disposition: providerapi.WriteResumeExisting,
	})
	if err != nil {
		t.Fatalf("OpenWrite(B): %v", err)
	}
	bWrite := bWriteInterface.(*writeHandle)

	sessionAAgain := d3EndpointSnapshot(
		contractEndpointID,
		contractSessionID,
		domain.StateReady,
		1,
		[]domain.Capability{{Name: "read", Version: 1}, {Name: "write", Version: 1}},
		sessionBReady.ObservedAt.Add(time.Minute),
	)
	if err := controller.SetSnapshot(sessionAAgain); err != nil {
		t.Fatalf("SetSnapshot(B -> A): %v", err)
	}
	_, err = read.Read(context.Background(), make([]byte, 1))
	requireE1Error(t, err, domain.CodeConflict, domain.RetryAfterReplan, "read", location)
	_, err = write.Write(context.Background(), []byte("z"))
	requireE1Error(t, err, domain.CodeConflict, domain.RetryAfterReplan, "write", location)
	_, err = bRead.Read(context.Background(), make([]byte, 1))
	requireE1Error(t, err, domain.CodeConflict, domain.RetryAfterReplan, "read", location)
	_, err = bWrite.Write(context.Background(), []byte("q"))
	requireE1Error(t, err, domain.CodeConflict, domain.RetryAfterReplan, "write", location)
	if err := controller.SetCapabilities(e1CapabilitySnapshot(
		contractSessionID,
		2,
		true,
		nil,
	)); err != nil {
		t.Fatalf("SetCapabilities(A-again withdraw): %v", err)
	}
	setE1State(t, implementation, controller, domain.StateAuthRequired)
	if !reflect.DeepEqual(read.Info(), infoBefore) {
		t.Fatalf("Info changed across session transitions: before=%#v after=%#v", infoBefore, read.Info())
	}
	if err := read.Close(context.Background()); err != nil {
		t.Fatalf("Close(old read): %v", err)
	}
	if err := read.Close(context.Background()); err != nil {
		t.Fatalf("Close(old read second): %v", err)
	}
	if err := write.Close(context.Background()); err != nil {
		t.Fatalf("Close(old write): %v", err)
	}
	if err := write.Close(context.Background()); err != nil {
		t.Fatalf("Close(old write second): %v", err)
	}
	if err := bRead.Close(context.Background()); err != nil {
		t.Fatalf("Close(B read): %v", err)
	}
	if err := bWrite.Close(context.Background()); err != nil {
		t.Fatalf("Close(B write): %v", err)
	}
	if err := controller.SetCapabilities(e1CapabilitySnapshot(
		contractSessionID,
		3,
		false,
		[]domain.Capability{{Name: "read", Version: 2}, {Name: "write", Version: 2}},
	)); err != nil {
		t.Fatalf("SetCapabilities(A-again restore): %v", err)
	}
	setE1State(t, implementation, controller, domain.StateReady)

	freshRead, err := implementation.OpenRead(context.Background(), providerapi.OpenReadRequest{Location: location})
	if err != nil {
		t.Fatalf("OpenRead(fresh A): %v", err)
	}
	if n, err := freshRead.Read(context.Background(), make([]byte, 1)); err != nil || n != 1 {
		t.Fatalf("fresh Read() = (%d, %v), want (1, nil)", n, err)
	}
	if err := freshRead.Close(context.Background()); err != nil {
		t.Fatalf("Close(fresh read): %v", err)
	}
}

func TestCurrentHandlesResumeWithoutProgressAfterRestoreOrReconnect(t *testing.T) {
	t.Run("capability restore", func(t *testing.T) {
		implementation, controller, err := New(e1ReadWriteScenario(t))
		if err != nil {
			t.Fatalf("New(): %v", err)
		}
		location := e1Spec(OperationRead).location
		readInterface, err := implementation.OpenRead(context.Background(), providerapi.OpenReadRequest{Location: location})
		if err != nil {
			t.Fatalf("OpenRead(): %v", err)
		}
		read := readInterface.(*readHandle)
		writeInterface, err := implementation.OpenWrite(context.Background(), providerapi.OpenWriteRequest{
			Location:    location,
			Disposition: providerapi.WriteResumeExisting,
		})
		if err != nil {
			t.Fatalf("OpenWrite(): %v", err)
		}
		write := writeInterface.(*writeHandle)
		beforeData := e1FileData(t, implementation, location.Path)
		if err := controller.SetCapabilities(e1CapabilitySnapshot(
			contractSessionID,
			2,
			true,
			nil,
		)); err != nil {
			t.Fatalf("SetCapabilities(withdraw both): %v", err)
		}
		if n, err := read.Read(context.Background(), make([]byte, 1)); n != 0 {
			t.Fatalf("blocked Read progress = %d, want 0", n)
		} else {
			requireE1Error(t, err, domain.CodeCapabilityLost, domain.RetryAfterReplan, "read", location)
		}
		if n, err := write.Write(context.Background(), []byte("x")); n != 0 {
			t.Fatalf("blocked Write progress = %d, want 0", n)
		} else {
			requireE1Error(t, err, domain.CodeCapabilityLost, domain.RetryAfterReplan, "write", location)
		}
		requireE1Error(t, write.Sync(context.Background()), domain.CodeCapabilityLost, domain.RetryAfterReplan, "sync_write", location)
		if got := e1FileData(t, implementation, location.Path); !reflect.DeepEqual(got, beforeData) {
			t.Fatalf("capability-blocked handle changed data: got %q want %q", got, beforeData)
		}
		if state := captureE1HandleState(&e1Invocation{readHandle: read, writeHandle: write}); state.readOffset != 0 || state.writeOffset != 0 {
			t.Fatalf("capability-blocked handle offsets = %#v, want zero", state)
		}
		if err := controller.SetCapabilities(e1CapabilitySnapshot(
			contractSessionID,
			3,
			false,
			[]domain.Capability{{Name: "read", Version: 2}, {Name: "write", Version: 2}},
		)); err != nil {
			t.Fatalf("SetCapabilities(restore both): %v", err)
		}
		if n, err := read.Read(context.Background(), make([]byte, 1)); err != nil || n != 1 {
			t.Fatalf("restored Read = (%d, %v), want (1, nil)", n, err)
		}
		if n, err := write.Write(context.Background(), []byte("y")); err != nil || n != 1 {
			t.Fatalf("restored Write = (%d, %v), want (1, nil)", n, err)
		}
		if err := write.Sync(context.Background()); err != nil {
			t.Fatalf("restored Sync(): %v", err)
		}
		if err := read.Close(context.Background()); err != nil {
			t.Fatalf("Close(read): %v", err)
		}
		if err := write.Close(context.Background()); err != nil {
			t.Fatalf("Close(write): %v", err)
		}
	})

	t.Run("fault disconnect reconnect", func(t *testing.T) {
		scenario := e1ReadWriteScenario(t)
		scenario.Script = []FaultStep{{
			Match:  FaultMatch{Operation: OperationRead, Nth: 1},
			Effect: FaultEffect{Disconnect: true},
		}}
		implementation, controller, err := New(scenario)
		if err != nil {
			t.Fatalf("New(): %v", err)
		}
		location := e1Spec(OperationRead).location
		readInterface, err := implementation.OpenRead(context.Background(), providerapi.OpenReadRequest{Location: location})
		if err != nil {
			t.Fatalf("OpenRead(): %v", err)
		}
		read := readInterface.(*readHandle)
		writeInterface, err := implementation.OpenWrite(context.Background(), providerapi.OpenWriteRequest{
			Location:    location,
			Disposition: providerapi.WriteResumeExisting,
		})
		if err != nil {
			t.Fatalf("OpenWrite(): %v", err)
		}
		write := writeInterface.(*writeHandle)
		if n, err := read.Read(context.Background(), make([]byte, 1)); n != 0 {
			t.Fatalf("disconnect Read progress = %d, want 0", n)
		} else {
			requireE1Error(t, err, domain.CodeTransportInterrupted, domain.RetryAfterReconnect, "read", location)
		}
		if n, err := write.Write(context.Background(), []byte("x")); n != 0 {
			t.Fatalf("disconnected Write progress = %d, want 0", n)
		} else {
			requireE1Error(t, err, domain.CodeTransportInterrupted, domain.RetryAfterReconnect, "write", location)
		}
		requireE1Error(t, write.Sync(context.Background()), domain.CodeTransportInterrupted, domain.RetryAfterReconnect, "sync_write", location)
		setE1State(t, implementation, controller, domain.StateReady)
		if n, err := read.Read(context.Background(), make([]byte, 1)); err != nil || n != 1 {
			t.Fatalf("reconnected Read = (%d, %v), want (1, nil)", n, err)
		}
		if n, err := write.Write(context.Background(), []byte("y")); err != nil || n != 1 {
			t.Fatalf("reconnected Write = (%d, %v), want (1, nil)", n, err)
		}
		if err := write.Sync(context.Background()); err != nil {
			t.Fatalf("reconnected Sync(): %v", err)
		}
		if err := read.Close(context.Background()); err != nil {
			t.Fatalf("Close(read): %v", err)
		}
		if err := write.Close(context.Background()); err != nil {
			t.Fatalf("Close(write): %v", err)
		}
	})
}

func TestDisconnectFaultOperationMatrix(t *testing.T) {
	disconnectedAt := time.Date(2026, time.July, 14, 17, 0, 0, 0, time.UTC)
	for _, spec := range e1OperationSpecs() {
		t.Run(string(spec.operation), func(t *testing.T) {
			scenario := e1ReadWriteScenario(t)
			scenario.Clock = foundation.NewManualClock(disconnectedAt)
			scenario.Script = []FaultStep{{
				Match:  FaultMatch{Operation: spec.operation, Nth: 1},
				Effect: FaultEffect{Disconnect: true},
			}}
			implementation, _, err := New(scenario)
			if err != nil {
				t.Fatalf("New(): %v", err)
			}
			invocation := prepareE1Invocation(t, implementation, spec)
			defer invocation.cleanup()
			beforeState := captureD3State(implementation)
			before := beforeState.snapshot
			beforeTree := e1TreeState(implementation)
			beforeHandle := captureE1HandleState(invocation)

			err = invocation.invoke(context.Background())
			invocation.assertZero(t)
			requireE1Error(
				t,
				err,
				domain.CodeTransportInterrupted,
				domain.RetryAfterReconnect,
				string(spec.operation),
				spec.location,
			)
			afterState := captureD3State(implementation)
			after := afterState.snapshot
			if after.State != domain.StateDisconnected ||
				after.SessionID != before.SessionID ||
				!reflect.DeepEqual(after.Capabilities, before.Capabilities) ||
				!after.ObservedAt.Equal(disconnectedAt) {
				t.Fatalf("snapshot after %s disconnect = %#v, before=%#v", spec.operation, after, before)
			}
			if afterState.sessionEpoch != beforeState.sessionEpoch ||
				!reflect.DeepEqual(afterState.capabilitySeen, beforeState.capabilitySeen) ||
				!reflect.DeepEqual(afterState.capabilityLost, beforeState.capabilityLost) {
				t.Fatalf("%s disconnect changed epoch/capability history: before=%#v after=%#v", spec.operation, beforeState, afterState)
			}
			if afterTree := e1TreeState(implementation); !reflect.DeepEqual(afterTree, beforeTree) {
				t.Fatalf("%s disconnect mutated tree:\nbefore=%#v\nafter=%#v", spec.operation, beforeTree, afterTree)
			}
			if afterHandle := captureE1HandleState(invocation); !reflect.DeepEqual(afterHandle, beforeHandle) {
				t.Fatalf("%s disconnect changed handle progress: before=%#v after=%#v", spec.operation, beforeHandle, afterHandle)
			}
		})
	}
}

func TestDisconnectClearsCursorsAndPersistentStateGatesEveryOperation(t *testing.T) {
	scenario := e1ReadWriteScenario(t)
	scenario.Script = []FaultStep{{
		Match:  FaultMatch{Operation: OperationStat, Nth: 1},
		Effect: FaultEffect{Disconnect: true},
	}}
	implementation, controller, err := New(scenario)
	if err != nil {
		t.Fatalf("New(): %v", err)
	}
	cursor := issueD3Cursor(t, implementation)
	invocations := make([]struct {
		spec       e1OperationSpec
		invocation *e1Invocation
	}, 0, len(e1OperationSpecs()))
	for _, spec := range e1OperationSpecs() {
		invocations = append(invocations, struct {
			spec       e1OperationSpec
			invocation *e1Invocation
		}{spec: spec, invocation: prepareE1Invocation(t, implementation, spec)})
	}
	defer func() {
		for _, prepared := range invocations {
			prepared.invocation.cleanup()
		}
	}()

	stat := e1Spec(OperationStat)
	_, err = implementation.Stat(context.Background(), providerapi.StatRequest{Location: stat.location})
	requireE1Error(t, err, domain.CodeTransportInterrupted, domain.RetryAfterReconnect, "stat", stat.location)
	requireD3CursorAbsent(t, implementation, cursor)

	for _, prepared := range invocations {
		err := prepared.invocation.invoke(context.Background())
		requireE1Error(
			t,
			err,
			domain.CodeTransportInterrupted,
			domain.RetryAfterReconnect,
			string(prepared.spec.operation),
			prepared.spec.location,
		)
	}

	if _, err := implementation.Snapshot(context.Background()); err != nil {
		t.Fatalf("Snapshot(disconnected): %v", err)
	}
	location, err := implementation.Normalize(context.Background(), domain.NormalizeRequest{
		EndpointID: contractEndpointID,
		Input:      "/contract-file",
	})
	if err != nil || location.Path != "/contract-file" {
		t.Fatalf("Normalize(disconnected) = (%#v, %v)", location, err)
	}
	for _, prepared := range invocations {
		if prepared.invocation.readHandle != nil {
			_ = prepared.invocation.readHandle.Info()
		}
	}
	if calls := controller.Calls(); len(calls) == 0 {
		t.Fatal("disconnect fixture recorded no calls")
	}
}

func TestProviderDisconnectTargetsExecutionTimeSession(t *testing.T) {
	disconnectedAt := time.Date(2026, time.July, 14, 18, 0, 0, 0, time.UTC)
	scenario := e1ReadWriteScenario(t)
	scenario.Clock = foundation.NewManualClock(disconnectedAt.Add(-time.Hour))
	scenario.Script = []FaultStep{{
		Match: FaultMatch{Operation: OperationStat, Nth: 1},
		Effect: FaultEffect{
			WaitGate:   "provider-disconnect",
			Disconnect: true,
		},
	}}
	implementation, controller, err := New(scenario)
	if err != nil {
		t.Fatalf("New(): %v", err)
	}
	location := e1Spec(OperationStat).location
	ctx := newDoneObservedContext(context.Background(), 1)
	result := make(chan error, 1)
	go func() {
		_, err := implementation.Stat(ctx, providerapi.StatRequest{Location: location})
		result <- err
	}()
	<-ctx.observed

	sessionB := d3EndpointSnapshot(
		contractEndpointID,
		d3SessionB,
		domain.StateReady,
		7,
		[]domain.Capability{{Name: "read", Version: 7}},
		time.Date(2026, time.July, 14, 17, 30, 0, 0, time.UTC),
	)
	if err := controller.SetSnapshot(sessionB); err != nil {
		t.Fatalf("SetSnapshot(B while gated): %v", err)
	}
	controller.Advance(time.Hour)
	controller.ReleaseGate("provider-disconnect")
	requireE1Error(t, <-result, domain.CodeTransportInterrupted, domain.RetryAfterReconnect, "stat", location)

	after := captureD3State(implementation).snapshot
	if after.SessionID != d3SessionB || after.Capabilities.Revision.Generation != 7 ||
		after.State != domain.StateDisconnected || !after.ObservedAt.Equal(disconnectedAt) {
		t.Fatalf("provider-only disconnect targeted wrong session: %#v", after)
	}
}

func TestGatedOldAndClosedHandleDisconnectCannotAffectCurrentSession(t *testing.T) {
	t.Run("gated old session", func(t *testing.T) {
		scenario := e1ReadWriteScenario(t)
		scenario.Script = []FaultStep{{
			Match: FaultMatch{Operation: OperationRead, Nth: 1},
			Effect: FaultEffect{
				WaitGate:   "old-read",
				Disconnect: true,
			},
		}}
		implementation, controller, err := New(scenario)
		if err != nil {
			t.Fatalf("New(): %v", err)
		}
		location := e1Spec(OperationRead).location
		readInterface, err := implementation.OpenRead(context.Background(), providerapi.OpenReadRequest{Location: location})
		if err != nil {
			t.Fatalf("OpenRead(A): %v", err)
		}
		read := readInterface.(*readHandle)
		ctx := newDoneObservedContext(context.Background(), 1)
		result := make(chan error, 1)
		go func() {
			_, err := read.Read(ctx, make([]byte, 1))
			result <- err
		}()
		<-ctx.observed

		sessionB := d3EndpointSnapshot(
			contractEndpointID,
			d3SessionB,
			domain.StateReady,
			1,
			nil,
			time.Date(2026, time.July, 14, 19, 0, 0, 0, time.UTC),
		)
		if err := controller.SetSnapshot(sessionB); err != nil {
			t.Fatalf("SetSnapshot(B): %v", err)
		}
		controller.ReleaseGate("old-read")
		requireE1Error(t, <-result, domain.CodeConflict, domain.RetryAfterReplan, "read", location)
		after := captureD3State(implementation).snapshot
		if !reflect.DeepEqual(after, sessionB) {
			t.Fatalf("old handle disconnected B: got %#v want %#v", after, sessionB)
		}
		if err := read.Close(context.Background()); err != nil {
			t.Fatalf("Close(old read): %v", err)
		}
	})

	t.Run("gated closed current handle", func(t *testing.T) {
		scenario := e1ReadWriteScenario(t)
		scenario.Script = []FaultStep{{
			Match: FaultMatch{Operation: OperationRead, Nth: 1},
			Effect: FaultEffect{
				WaitGate:   "closed-read",
				Disconnect: true,
			},
		}}
		implementation, controller, err := New(scenario)
		if err != nil {
			t.Fatalf("New(): %v", err)
		}
		location := e1Spec(OperationRead).location
		readInterface, err := implementation.OpenRead(context.Background(), providerapi.OpenReadRequest{Location: location})
		if err != nil {
			t.Fatalf("OpenRead(): %v", err)
		}
		read := readInterface.(*readHandle)
		before := captureD3State(implementation).snapshot
		ctx := newDoneObservedContext(context.Background(), 1)
		result := make(chan error, 1)
		go func() {
			_, err := read.Read(ctx, make([]byte, 1))
			result <- err
		}()
		<-ctx.observed
		if err := read.Close(context.Background()); err != nil {
			t.Fatalf("Close(while gated): %v", err)
		}
		controller.ReleaseGate("closed-read")
		requireE1Error(t, <-result, domain.CodeInvalidArgument, domain.RetryNever, "read", location)
		if after := captureD3State(implementation).snapshot; !reflect.DeepEqual(after, before) {
			t.Fatalf("closed handle changed snapshot: before=%#v after=%#v", before, after)
		}
	})

	t.Run("closed wins over old session", func(t *testing.T) {
		scenario := e1ReadWriteScenario(t)
		scenario.Script = []FaultStep{{
			Match: FaultMatch{Operation: OperationRead, Nth: 1},
			Effect: FaultEffect{
				WaitGate:   "closed-old-read",
				Disconnect: true,
			},
		}}
		implementation, controller, err := New(scenario)
		if err != nil {
			t.Fatalf("New(): %v", err)
		}
		location := e1Spec(OperationRead).location
		readInterface, err := implementation.OpenRead(context.Background(), providerapi.OpenReadRequest{Location: location})
		if err != nil {
			t.Fatalf("OpenRead(): %v", err)
		}
		read := readInterface.(*readHandle)
		ctx := newDoneObservedContext(context.Background(), 1)
		result := make(chan error, 1)
		go func() {
			_, err := read.Read(ctx, make([]byte, 1))
			result <- err
		}()
		<-ctx.observed
		sessionB := d3EndpointSnapshot(
			contractEndpointID,
			d3SessionB,
			domain.StateReady,
			1,
			nil,
			time.Date(2026, time.July, 14, 19, 30, 0, 0, time.UTC),
		)
		if err := controller.SetSnapshot(sessionB); err != nil {
			t.Fatalf("SetSnapshot(B): %v", err)
		}
		if err := read.Close(context.Background()); err != nil {
			t.Fatalf("Close(old while gated): %v", err)
		}
		controller.ReleaseGate("closed-old-read")
		requireE1Error(t, <-result, domain.CodeInvalidArgument, domain.RetryNever, "read", location)
		if after := captureD3State(implementation).snapshot; !reflect.DeepEqual(after, sessionB) {
			t.Fatalf("closed old handle disconnected B: got %#v want %#v", after, sessionB)
		}
	})
}

func TestValidHandleDisconnectPrecedesConnectionAndCapabilityChecks(t *testing.T) {
	for _, spec := range []e1OperationSpec{e1Spec(OperationRead), e1Spec(OperationWrite)} {
		t.Run(string(spec.operation), func(t *testing.T) {
			scenario := e1ReadWriteScenario(t)
			scenario.Script = []FaultStep{{
				Match:  FaultMatch{Operation: spec.operation, Nth: 1},
				Effect: FaultEffect{Disconnect: true},
			}}
			implementation, controller, err := New(scenario)
			if err != nil {
				t.Fatalf("New(): %v", err)
			}
			invocation := prepareE1Invocation(t, implementation, spec)
			defer invocation.cleanup()
			retained := domain.CapabilityName("read")
			if spec.capability == "read" {
				retained = "write"
			}
			if err := controller.SetCapabilities(e1CapabilitySnapshot(
				contractSessionID,
				2,
				true,
				[]domain.Capability{{Name: retained, Version: 1}},
			)); err != nil {
				t.Fatalf("SetCapabilities(withdraw): %v", err)
			}
			setE1State(t, implementation, controller, domain.StateAuthRequired)
			err = invocation.invoke(context.Background())
			requireE1Error(
				t,
				err,
				domain.CodeTransportInterrupted,
				domain.RetryAfterReconnect,
				string(spec.operation),
				spec.location,
			)
			if got := captureD3State(implementation).snapshot.State; got != domain.StateDisconnected {
				t.Fatalf("disconnect state = %q, want disconnected", got)
			}
		})
	}
}

func TestProviderDisconnectPrecedesConnectionAndCapabilityChecks(t *testing.T) {
	scenario := e1ReadWriteScenario(t)
	scenario.Script = []FaultStep{{
		Match:  FaultMatch{Operation: OperationStat, Nth: 1},
		Effect: FaultEffect{Disconnect: true},
	}}
	implementation, controller, err := New(scenario)
	if err != nil {
		t.Fatalf("New(): %v", err)
	}
	if err := controller.SetCapabilities(e1CapabilitySnapshot(
		contractSessionID,
		2,
		true,
		[]domain.Capability{{Name: "write", Version: 1}},
	)); err != nil {
		t.Fatalf("SetCapabilities(withdraw read): %v", err)
	}
	setE1State(t, implementation, controller, domain.StateAuthRequired)
	location := e1Spec(OperationStat).location
	_, err = implementation.Stat(context.Background(), providerapi.StatRequest{Location: location})
	requireE1Error(t, err, domain.CodeTransportInterrupted, domain.RetryAfterReconnect, "stat", location)
	if got := captureD3State(implementation).snapshot.State; got != domain.StateDisconnected {
		t.Fatalf("disconnect state = %q, want disconnected", got)
	}
}

func TestPersistentCheckPrecedenceAndConsumedEffects(t *testing.T) {
	t.Run("state then capability then lookup", func(t *testing.T) {
		implementation, controller, err := New(e1ReadWriteScenario(t))
		if err != nil {
			t.Fatalf("New(): %v", err)
		}
		if err := controller.SetCapabilities(e1CapabilitySnapshot(
			contractSessionID,
			2,
			true,
			[]domain.Capability{{Name: "write", Version: 1}},
		)); err != nil {
			t.Fatalf("SetCapabilities(withdraw read): %v", err)
		}
		missing := domain.Location{EndpointID: contractEndpointID, Path: "/missing"}
		setE1State(t, implementation, controller, domain.StateAuthRequired)
		_, err = implementation.Stat(context.Background(), providerapi.StatRequest{Location: missing})
		requireE1Error(t, err, domain.CodeAuthRequired, domain.RetryAfterAuth, "stat", missing)
		setE1State(t, implementation, controller, domain.StateReady)
		_, err = implementation.Stat(context.Background(), providerapi.StatRequest{Location: missing})
		requireE1Error(t, err, domain.CodeCapabilityLost, domain.RetryAfterReplan, "stat", missing)
		if err := controller.SetCapabilities(e1CapabilitySnapshot(
			contractSessionID,
			3,
			false,
			[]domain.Capability{{Name: "read", Version: 2}},
		)); err != nil {
			t.Fatalf("SetCapabilities(restore read): %v", err)
		}
		_, err = implementation.Stat(context.Background(), providerapi.StatRequest{Location: missing})
		requireE1Error(t, err, domain.CodeNotFound, domain.RetryNever, "stat", missing)
	})

	t.Run("stale waits behind capability and remains consumed", func(t *testing.T) {
		revision := uint64(999)
		scenario := e1ReadWriteScenario(t)
		scenario.Script = []FaultStep{{
			Match:  FaultMatch{Operation: OperationStat, Nth: 1},
			Effect: FaultEffect{StaleNodeRevision: &revision},
		}}
		implementation, controller, err := New(scenario)
		if err != nil {
			t.Fatalf("New(): %v", err)
		}
		if err := controller.SetCapabilities(e1CapabilitySnapshot(
			contractSessionID,
			2,
			true,
			[]domain.Capability{{Name: "write", Version: 1}},
		)); err != nil {
			t.Fatalf("SetCapabilities(withdraw read): %v", err)
		}
		location := e1Spec(OperationStat).location
		_, err = implementation.Stat(context.Background(), providerapi.StatRequest{Location: location})
		requireE1Error(t, err, domain.CodeCapabilityLost, domain.RetryAfterReplan, "stat", location)
		if err := controller.SetCapabilities(e1CapabilitySnapshot(
			contractSessionID,
			3,
			false,
			[]domain.Capability{{Name: "read", Version: 2}},
		)); err != nil {
			t.Fatalf("SetCapabilities(restore read): %v", err)
		}
		if _, err := implementation.Stat(context.Background(), providerapi.StatRequest{Location: location}); err != nil {
			t.Fatalf("second Stat reached consumed stale effect: %v", err)
		}
	})

	t.Run("short read makes no progress behind capability", func(t *testing.T) {
		scenario := e1ReadWriteScenario(t)
		scenario.Script = []FaultStep{{
			Match:  FaultMatch{Operation: OperationRead, Nth: 1},
			Effect: FaultEffect{MaxReadBytes: 1},
		}}
		implementation, controller, err := New(scenario)
		if err != nil {
			t.Fatalf("New(): %v", err)
		}
		location := e1Spec(OperationRead).location
		readInterface, err := implementation.OpenRead(context.Background(), providerapi.OpenReadRequest{Location: location})
		if err != nil {
			t.Fatalf("OpenRead(): %v", err)
		}
		read := readInterface.(*readHandle)
		if err := controller.SetCapabilities(e1CapabilitySnapshot(
			contractSessionID,
			2,
			true,
			[]domain.Capability{{Name: "write", Version: 1}},
		)); err != nil {
			t.Fatalf("SetCapabilities(withdraw read): %v", err)
		}
		if n, err := read.Read(context.Background(), make([]byte, 2)); n != 0 {
			t.Fatalf("capability-blocked short Read progress = %d, want 0", n)
		} else {
			requireE1Error(t, err, domain.CodeCapabilityLost, domain.RetryAfterReplan, "read", location)
		}
		if state := captureE1HandleState(&e1Invocation{readHandle: read}); state.readOffset != 0 {
			t.Fatalf("capability-blocked read offset = %d, want 0", state.readOffset)
		}
		if err := controller.SetCapabilities(e1CapabilitySnapshot(
			contractSessionID,
			3,
			false,
			[]domain.Capability{{Name: "read", Version: 2}},
		)); err != nil {
			t.Fatalf("SetCapabilities(restore read): %v", err)
		}
		if n, err := read.Read(context.Background(), make([]byte, 2)); err != nil || n != 2 {
			t.Fatalf("Read after consumed short fault = (%d, %v), want (2, nil)", n, err)
		}
		if err := read.Close(context.Background()); err != nil {
			t.Fatalf("Close(): %v", err)
		}
	})

	t.Run("non-atomic rename makes no mutation behind state", func(t *testing.T) {
		scenario := e1ReadWriteScenario(t)
		scenario.Script = []FaultStep{{
			Match:  FaultMatch{Operation: OperationRename, Nth: 1},
			Effect: FaultEffect{NonAtomicRename: true},
		}}
		implementation, controller, err := New(scenario)
		if err != nil {
			t.Fatalf("New(): %v", err)
		}
		before := e1TreeState(implementation)
		setE1State(t, implementation, controller, domain.StateAuthRequired)
		spec := e1Spec(OperationRename)
		invocation := prepareE1Invocation(t, implementation, spec)
		err = invocation.invoke(context.Background())
		requireE1Error(t, err, domain.CodeAuthRequired, domain.RetryAfterAuth, "rename", spec.location)
		if after := e1TreeState(implementation); !reflect.DeepEqual(after, before) {
			t.Fatalf("state-blocked non-atomic rename mutated tree: before=%#v after=%#v", before, after)
		}
		setE1State(t, implementation, controller, domain.StateReady)
		result, err := implementation.Rename(context.Background(), providerapi.RenameRequest{
			Source:      spec.location,
			Destination: domain.Location{EndpointID: contractEndpointID, Path: "/e1-renamed-link"},
		})
		if err != nil || !result.Atomic {
			t.Fatalf("Rename after consumed non-atomic fault = (%#v, %v), want Atomic true", result, err)
		}
	})
}

func TestStandaloneInjectedErrorPrecedesPersistentState(t *testing.T) {
	location := e1Spec(OperationStat).location
	scenario := e1ReadWriteScenario(t)
	scenario.Script = []FaultStep{{
		Match: FaultMatch{Operation: OperationStat, Nth: 1},
		Effect: FaultEffect{Error: &domain.OpError{
			Code:    domain.CodeTimeout,
			Message: "injected before state",
			Retry:   domain.RetryAdvice{Kind: domain.RetryBackoff},
			Effect:  domain.EffectNone,
		}},
	}}
	implementation, controller, err := New(scenario)
	if err != nil {
		t.Fatalf("New(): %v", err)
	}
	setE1State(t, implementation, controller, domain.StateDisconnected)
	_, err = implementation.Stat(context.Background(), providerapi.StatRequest{Location: location})
	requireE1Error(t, err, domain.CodeTimeout, domain.RetryBackoff, "stat", location)
}

func TestDisconnectClockRunsOutsideLocksAndCancellationPreventsCommit(t *testing.T) {
	t.Run("provider operation", func(t *testing.T) {
		clock := &callbackClock{}
		scenario := e1ReadWriteScenario(t)
		scenario.Clock = clock
		scenario.Script = []FaultStep{{
			Match:  FaultMatch{Operation: OperationStat, Nth: 1},
			Effect: FaultEffect{Disconnect: true},
		}}
		implementation, _, err := New(scenario)
		if err != nil {
			t.Fatalf("New(): %v", err)
		}
		var calls int
		var providerLockHeld bool
		var snapshotErr error
		observedAt := time.Date(2026, time.July, 14, 20, 0, 0, 0, time.UTC)
		clock.now = func() time.Time {
			calls++
			if !implementation.mu.TryLock() {
				providerLockHeld = true
				return observedAt
			}
			implementation.mu.Unlock()
			_, snapshotErr = implementation.Snapshot(context.Background())
			return observedAt
		}
		location := e1Spec(OperationStat).location
		_, err = implementation.Stat(context.Background(), providerapi.StatRequest{Location: location})
		requireE1Error(t, err, domain.CodeTransportInterrupted, domain.RetryAfterReconnect, "stat", location)
		if calls != 1 || providerLockHeld || snapshotErr != nil {
			t.Fatalf("Clock.Now calls=%d providerLockHeld=%t snapshotErr=%v", calls, providerLockHeld, snapshotErr)
		}
	})

	t.Run("handle operation", func(t *testing.T) {
		clock := &callbackClock{}
		scenario := e1ReadWriteScenario(t)
		scenario.Clock = clock
		scenario.Script = []FaultStep{{
			Match:  FaultMatch{Operation: OperationRead, Nth: 1},
			Effect: FaultEffect{Disconnect: true},
		}}
		implementation, _, err := New(scenario)
		if err != nil {
			t.Fatalf("New(): %v", err)
		}
		location := e1Spec(OperationRead).location
		readInterface, err := implementation.OpenRead(context.Background(), providerapi.OpenReadRequest{Location: location})
		if err != nil {
			t.Fatalf("OpenRead(): %v", err)
		}
		read := readInterface.(*readHandle)
		var calls int
		var handleLockHeld bool
		var providerLockHeld bool
		var snapshotErr error
		clock.now = func() time.Time {
			calls++
			if !read.mu.TryLock() {
				handleLockHeld = true
			} else {
				read.mu.Unlock()
				_ = read.Info()
			}
			if !implementation.mu.TryLock() {
				providerLockHeld = true
			} else {
				implementation.mu.Unlock()
				_, snapshotErr = implementation.Snapshot(context.Background())
			}
			return time.Date(2026, time.July, 14, 20, 30, 0, 0, time.UTC)
		}
		_, err = read.Read(context.Background(), make([]byte, 1))
		requireE1Error(t, err, domain.CodeTransportInterrupted, domain.RetryAfterReconnect, "read", location)
		if calls != 1 || handleLockHeld || providerLockHeld || snapshotErr != nil {
			t.Fatalf("Clock.Now calls=%d handleLockHeld=%t providerLockHeld=%t snapshotErr=%v", calls, handleLockHeld, providerLockHeld, snapshotErr)
		}
		if err := read.Close(context.Background()); err != nil {
			t.Fatalf("Close(): %v", err)
		}
	})

	t.Run("cancellation after clock", func(t *testing.T) {
		clock := &callbackClock{}
		scenario := e1ReadWriteScenario(t)
		scenario.Clock = clock
		scenario.Script = []FaultStep{{
			Match:  FaultMatch{Operation: OperationStat, Nth: 1},
			Effect: FaultEffect{Disconnect: true},
		}}
		implementation, _, err := New(scenario)
		if err != nil {
			t.Fatalf("New(): %v", err)
		}
		ctx, cancel := context.WithCancel(context.Background())
		clock.now = func() time.Time {
			cancel()
			return time.Date(2026, time.July, 14, 21, 0, 0, 0, time.UTC)
		}
		before := captureD3State(implementation).snapshot
		location := e1Spec(OperationStat).location
		_, err = implementation.Stat(ctx, providerapi.StatRequest{Location: location})
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("Stat() error = %v, want context.Canceled", err)
		}
		requireE1Error(t, err, domain.CodeCanceled, domain.RetryNever, "stat", location)
		if after := captureD3State(implementation).snapshot; !reflect.DeepEqual(after, before) {
			t.Fatalf("canceled disconnect changed snapshot: before=%#v after=%#v", before, after)
		}
		if _, err := implementation.Stat(context.Background(), providerapi.StatRequest{Location: location}); err != nil {
			t.Fatalf("second Stat reached consumed disconnect: %v", err)
		}
	})

	t.Run("gate cancellation records and consumes without disconnect", func(t *testing.T) {
		scenario := e1ReadWriteScenario(t)
		scenario.Script = []FaultStep{{
			Match: FaultMatch{Operation: OperationStat, Nth: 1},
			Effect: FaultEffect{
				WaitGate:   "cancel-disconnect",
				Disconnect: true,
			},
		}}
		implementation, controller, err := New(scenario)
		if err != nil {
			t.Fatalf("New(): %v", err)
		}
		base, cancel := context.WithCancel(context.Background())
		ctx := newDoneObservedContext(base, 1)
		location := e1Spec(OperationStat).location
		before := captureD3State(implementation).snapshot
		result := make(chan error, 1)
		go func() {
			_, err := implementation.Stat(ctx, providerapi.StatRequest{Location: location})
			result <- err
		}()
		<-ctx.observed
		cancel()
		err = <-result
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("Stat() error = %v, want context.Canceled", err)
		}
		requireE1Error(t, err, domain.CodeCanceled, domain.RetryNever, "stat", location)
		if after := captureD3State(implementation).snapshot; !reflect.DeepEqual(after, before) {
			t.Fatalf("gate-canceled disconnect changed snapshot: before=%#v after=%#v", before, after)
		}
		controller.ReleaseGate("cancel-disconnect")
		if _, err := implementation.Stat(context.Background(), providerapi.StatRequest{Location: location}); err != nil {
			t.Fatalf("second Stat reached consumed gated disconnect: %v", err)
		}
		statCalls := 0
		for _, call := range controller.Calls() {
			if call.Operation == OperationStat {
				statCalls++
			}
		}
		if statCalls != 2 {
			t.Fatalf("recorded Stat calls = %d, want 2", statCalls)
		}
	})
}

func e1OperationSpecs() []e1OperationSpec {
	operations := []Operation{
		OperationList,
		OperationStat,
		OperationOpenRead,
		OperationRead,
		OperationOpenWrite,
		OperationWrite,
		OperationSyncWrite,
		OperationMkdir,
		OperationRename,
		OperationRemove,
	}
	result := make([]e1OperationSpec, 0, len(operations))
	for _, operation := range operations {
		result = append(result, e1Spec(operation))
	}
	return result
}

func e1Spec(operation Operation) e1OperationSpec {
	spec := e1OperationSpec{
		operation:  operation,
		capability: "read",
		location: domain.Location{
			EndpointID: contractEndpointID,
			Path:       "/contract-file",
		},
	}
	switch operation {
	case OperationList:
		spec.location.Path = "/"
	case OperationOpenWrite, OperationWrite, OperationSyncWrite:
		spec.capability = "write"
	case OperationMkdir:
		spec.capability = "write"
		spec.location.Path = "/e1-directory"
	case OperationRename:
		spec.capability = "write"
		spec.location.Path = "/contract-link"
	case OperationRemove:
		spec.capability = "write"
		spec.location.Path = "/contract-link"
	}
	return spec
}

func prepareE1Invocation(
	t *testing.T,
	implementation *Provider,
	spec e1OperationSpec,
) *e1Invocation {
	t.Helper()
	invocation := &e1Invocation{
		cleanup:    func() {},
		assertZero: func(*testing.T) {},
	}
	switch spec.operation {
	case OperationList:
		var page providerapi.ListPage
		invocation.invoke = func(ctx context.Context) error {
			var err error
			page, err = implementation.List(ctx, providerapi.ListRequest{Location: spec.location, Limit: 1})
			return err
		}
		invocation.assertZero = func(t *testing.T) {
			t.Helper()
			if !reflect.DeepEqual(page, providerapi.ListPage{}) {
				t.Fatalf("List disconnect result = %#v, want zero", page)
			}
		}
	case OperationStat:
		var entry domain.Entry
		invocation.invoke = func(ctx context.Context) error {
			var err error
			entry, err = implementation.Stat(ctx, providerapi.StatRequest{Location: spec.location})
			return err
		}
		invocation.assertZero = func(t *testing.T) {
			t.Helper()
			if !reflect.DeepEqual(entry, domain.Entry{}) {
				t.Fatalf("Stat disconnect result = %#v, want zero", entry)
			}
		}
	case OperationOpenRead:
		var opened providerapi.ReadHandle
		invocation.invoke = func(ctx context.Context) error {
			var err error
			opened, err = implementation.OpenRead(ctx, providerapi.OpenReadRequest{Location: spec.location})
			if err != nil {
				return err
			}
			return opened.Close(context.Background())
		}
		invocation.assertZero = func(t *testing.T) {
			t.Helper()
			if opened != nil {
				t.Fatalf("OpenRead disconnect handle = %#v, want nil", opened)
			}
		}
	case OperationRead:
		handle, err := implementation.OpenRead(context.Background(), providerapi.OpenReadRequest{Location: spec.location})
		if err != nil {
			t.Fatalf("OpenRead(setup): %v", err)
		}
		invocation.readHandle = handle.(*readHandle)
		var n int
		invocation.invoke = func(ctx context.Context) error {
			var err error
			n, err = invocation.readHandle.Read(ctx, make([]byte, 1))
			return err
		}
		invocation.assertZero = func(t *testing.T) {
			t.Helper()
			if n != 0 {
				t.Fatalf("Read disconnect progress = %d, want 0", n)
			}
		}
		invocation.cleanup = func() {
			if err := invocation.readHandle.Close(context.Background()); err != nil {
				t.Fatalf("Close(read setup handle): %v", err)
			}
		}
	case OperationOpenWrite:
		var opened providerapi.WriteHandle
		invocation.invoke = func(ctx context.Context) error {
			var err error
			opened, err = implementation.OpenWrite(ctx, providerapi.OpenWriteRequest{
				Location:    spec.location,
				Disposition: providerapi.WriteResumeExisting,
			})
			if err != nil {
				return err
			}
			return opened.Close(context.Background())
		}
		invocation.assertZero = func(t *testing.T) {
			t.Helper()
			if opened != nil {
				t.Fatalf("OpenWrite disconnect handle = %#v, want nil", opened)
			}
		}
	case OperationWrite, OperationSyncWrite:
		handle, err := implementation.OpenWrite(context.Background(), providerapi.OpenWriteRequest{
			Location:    spec.location,
			Disposition: providerapi.WriteResumeExisting,
		})
		if err != nil {
			t.Fatalf("OpenWrite(setup): %v", err)
		}
		invocation.writeHandle = handle.(*writeHandle)
		if spec.operation == OperationWrite {
			var n int
			invocation.invoke = func(ctx context.Context) error {
				var err error
				n, err = invocation.writeHandle.Write(ctx, []byte("x"))
				return err
			}
			invocation.assertZero = func(t *testing.T) {
				t.Helper()
				if n != 0 {
					t.Fatalf("Write disconnect progress = %d, want 0", n)
				}
			}
		} else {
			invocation.invoke = invocation.writeHandle.Sync
		}
		invocation.cleanup = func() {
			if err := invocation.writeHandle.Close(context.Background()); err != nil {
				t.Fatalf("Close(write setup handle): %v", err)
			}
		}
	case OperationMkdir:
		var entry domain.Entry
		invocation.invoke = func(ctx context.Context) error {
			var err error
			entry, err = implementation.Mkdir(ctx, providerapi.MkdirRequest{Location: spec.location, Exclusive: true})
			return err
		}
		invocation.assertZero = func(t *testing.T) {
			t.Helper()
			if !reflect.DeepEqual(entry, domain.Entry{}) {
				t.Fatalf("Mkdir disconnect result = %#v, want zero", entry)
			}
		}
	case OperationRename:
		var result providerapi.RenameResult
		invocation.invoke = func(ctx context.Context) error {
			var err error
			result, err = implementation.Rename(ctx, providerapi.RenameRequest{
				Source: spec.location,
				Destination: domain.Location{
					EndpointID: contractEndpointID,
					Path:       "/e1-renamed-link",
				},
			})
			return err
		}
		invocation.assertZero = func(t *testing.T) {
			t.Helper()
			if result != (providerapi.RenameResult{}) {
				t.Fatalf("Rename disconnect result = %#v, want zero", result)
			}
		}
	case OperationRemove:
		invocation.invoke = func(ctx context.Context) error {
			return implementation.Remove(ctx, providerapi.RemoveRequest{Location: spec.location})
		}
	default:
		t.Fatalf("unsupported E1 operation %q", spec.operation)
	}
	return invocation
}

func invokeE1Once(t *testing.T, implementation *Provider, spec e1OperationSpec) {
	t.Helper()
	invocation := prepareE1Invocation(t, implementation, spec)
	defer invocation.cleanup()
	if err := invocation.invoke(context.Background()); err != nil {
		t.Fatalf("%s: %v", spec.operation, err)
	}
}

func requireE1InvocationError(
	t *testing.T,
	implementation *Provider,
	spec e1OperationSpec,
) {
	t.Helper()
	invocation := prepareE1Invocation(t, implementation, spec)
	defer invocation.cleanup()
	err := invocation.invoke(context.Background())
	requireE1Error(
		t,
		err,
		domain.CodeCapabilityLost,
		domain.RetryAfterReplan,
		string(spec.operation),
		spec.location,
	)
}

func requireE1Error(
	t *testing.T,
	err error,
	code domain.Code,
	retry domain.RetryKind,
	operation string,
	location domain.Location,
) {
	t.Helper()
	opError := requireCode(t, err, code)
	if opError.Operation != operation || opError.EndpointID != contractEndpointID ||
		opError.Location == nil || *opError.Location != location ||
		opError.Retry.Kind != retry || opError.Retry.After != 0 ||
		opError.Effect != domain.EffectNone {
		t.Fatalf(
			"error = %#v, want %s/%s endpoint=%s location=%#v retry=%s effect=none",
			opError,
			code,
			operation,
			contractEndpointID,
			location,
			retry,
		)
	}
}

func setE1State(
	t *testing.T,
	implementation *Provider,
	controller *Controller,
	state domain.ConnectionState,
) {
	t.Helper()
	next := captureD3State(implementation).snapshot
	next.State = state
	next.ObservedAt = next.ObservedAt.Add(time.Minute)
	if err := controller.SetSnapshot(next); err != nil {
		t.Fatalf("SetSnapshot(%s): %v", state, err)
	}
}

func e1ReadWriteScenario(t *testing.T) Scenario {
	t.Helper()
	scenario := validScenario(t)
	scenario.Snapshot.Capabilities = e1CapabilitySnapshot(
		contractSessionID,
		1,
		true,
		[]domain.Capability{{Name: "read", Version: 1}, {Name: "write", Version: 1}},
	)
	return scenario
}

func e1CapabilitySnapshot(
	sessionID domain.SessionID,
	generation uint64,
	complete bool,
	items []domain.Capability,
) domain.CapabilitySnapshot {
	return domain.CapabilitySnapshot{
		Revision: domain.CapabilityRevision{SessionID: sessionID, Generation: generation},
		Complete: complete,
		Items:    items,
	}
}

func e1FileData(t *testing.T, implementation *Provider, path domain.CanonicalPath) []byte {
	t.Helper()
	implementation.mu.RLock()
	defer implementation.mu.RUnlock()
	node, err := resolveNode(implementation.root, path, true)
	if err != nil {
		t.Fatalf("resolveNode(%s): %v", path, err)
	}
	return append([]byte(nil), node.data...)
}

type e1HandleState struct {
	readOffset  int
	readClosed  bool
	writeOffset int64
	writeClosed bool
}

func captureE1HandleState(invocation *e1Invocation) e1HandleState {
	var state e1HandleState
	if invocation.readHandle != nil {
		invocation.readHandle.mu.Lock()
		state.readOffset = invocation.readHandle.offset
		state.readClosed = invocation.readHandle.closed
		invocation.readHandle.mu.Unlock()
	}
	if invocation.writeHandle != nil {
		invocation.writeHandle.mu.Lock()
		state.writeOffset = invocation.writeHandle.offset
		state.writeClosed = invocation.writeHandle.closed
		invocation.writeHandle.mu.Unlock()
	}
	return state
}

func e1TreeState(implementation *Provider) []replaceTreeState {
	implementation.mu.RLock()
	defer implementation.mu.RUnlock()
	var state []replaceTreeState
	captureReplaceTree(implementation.root, "/", &state)
	return state
}
