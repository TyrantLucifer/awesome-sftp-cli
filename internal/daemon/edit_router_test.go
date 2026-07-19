package daemon

import (
	"context"
	"testing"
	"time"

	providerapi "github.com/TyrantLucifer/awesome-sftp-cli/internal/provider"
	"github.com/TyrantLucifer/awesome-sftp-cli/internal/state/editstore"
)

func TestProviderSessionExposesDurableEditLifecycleRoutes(t *testing.T) {
	factory, err := NewProviderSessions([]providerapi.Provider{testLocalProvider(t)}, 4)
	if err != nil {
		t.Fatal(err)
	}
	store := &recordingEditStore{}
	factory.SetEditSessionStore(store)
	session := factory.NewSession()
	t.Cleanup(func() { _ = session.Close() })

	create := editstore.CreateRequest{SessionID: "44444444444444444444444444444444", EventKind: "session_created"}
	created := handlePayload[EditSessionResponse](t, session, EditSessionCreate, EditSessionCreateRequest{Request: create})
	if created.Session.SessionID != create.SessionID || store.created.SessionID != create.SessionID {
		t.Fatalf("created = %#v, request = %#v", created, store.created)
	}
	transition := editstore.TransitionRequest{SessionID: create.SessionID, ExpectedVersion: 1, EventKind: "local_changed"}
	updated := handlePayload[EditSessionResponse](t, session, EditSessionTransition, EditSessionTransitionRequest{Request: transition})
	if updated.Session.StateVersion != 2 || store.transitioned.EventKind != "local_changed" {
		t.Fatalf("updated = %#v, request = %#v", updated, store.transitioned)
	}
	got := handlePayload[EditSessionResponse](t, session, EditSessionGet, EditSessionGetRequest{SessionID: create.SessionID})
	if got.Session.SessionID != create.SessionID {
		t.Fatalf("get = %#v", got)
	}
	events := handlePayload[EditSessionEventsResponse](t, session, EditSessionEvents, EditSessionEventsRequest{SessionID: create.SessionID, Limit: 20})
	if len(events.Events) != 1 || events.Events[0].Kind != "local_changed" {
		t.Fatalf("events = %#v", events)
	}
	recoverable := handlePayload[EditSessionRecoverableResponse](t, session, EditSessionRecoverable, EditSessionRecoverableRequest{Limit: 20})
	if len(recoverable.Sessions) != 1 || recoverable.Sessions[0].SessionID != create.SessionID {
		t.Fatalf("recoverable = %#v", recoverable)
	}
}

type recordingEditStore struct {
	created      editstore.CreateRequest
	transitioned editstore.TransitionRequest
}

func (store *recordingEditStore) Create(_ context.Context, request editstore.CreateRequest) (editstore.Record, error) {
	store.created = request
	return editstore.Record{SessionID: request.SessionID, StateVersion: 1}, nil
}

func (store *recordingEditStore) Transition(_ context.Context, request editstore.TransitionRequest) (editstore.Record, error) {
	store.transitioned = request
	return editstore.Record{SessionID: request.SessionID, StateVersion: 2}, nil
}

func (store *recordingEditStore) Get(_ context.Context, sessionID string) (editstore.Record, error) {
	return editstore.Record{SessionID: sessionID, StateVersion: 2}, nil
}

func (store *recordingEditStore) ListEvents(_ context.Context, sessionID string, _ int64, _ int) ([]editstore.EventRecord, error) {
	return []editstore.EventRecord{{SessionID: sessionID, Sequence: 2, Kind: "local_changed", CreatedAt: time.Unix(1, 0)}}, nil
}

func (store *recordingEditStore) ListRecoverable(_ context.Context, _ int) ([]editstore.RecoveryRecord, error) {
	return []editstore.RecoveryRecord{{Record: editstore.Record{SessionID: store.created.SessionID}}}, nil
}
