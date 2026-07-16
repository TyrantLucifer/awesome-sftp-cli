package editstore

import (
	"context"
	"database/sql"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/TyrantLucifer/awesome-mac-sftp/internal/cache"
	"github.com/TyrantLucifer/awesome-mac-sftp/internal/domain"
	"github.com/TyrantLucifer/awesome-mac-sftp/internal/edit"
	"github.com/TyrantLucifer/awesome-mac-sftp/internal/statefs"
	"github.com/TyrantLucifer/awesome-mac-sftp/internal/testkit"
)

func TestSessionLifecycleSurvivesStoreRestart(t *testing.T) {
	ctx := context.Background()
	database := newEditDatabase(t, ctx)
	seedEditCatalog(t, ctx, database)
	store, err := New(ctx, database)
	if err != nil {
		t.Fatal(err)
	}
	persistent := newPersistentSession(t)
	created, err := store.Create(ctx, CreateRequest{
		SessionID: strings.Repeat("4", 32), SourceEntryID: cache.EntryID(strings.Repeat("2", 64)),
		MaterializationID: cache.MaterializationID(strings.Repeat("3", 32)), LocalState: LocalClean,
		RemoteState: RemoteUnchanged, State: StatePreparing, EventID: "edit-created", EventKind: "session_created",
		Details: recoveryDetails(persistent),
		Now:     time.Unix(1_700_000_100, 0),
	})
	if err != nil {
		t.Fatalf("create session: %v", err)
	}
	session, err := edit.RestorePersistent(persistent)
	if err != nil {
		t.Fatal(err)
	}
	observing, err := session.BeginObservation(session.Version())
	if err != nil {
		t.Fatal(err)
	}
	updated, err := store.Transition(ctx, TransitionRequest{
		SessionID: created.SessionID, ExpectedVersion: created.StateVersion, LocalState: LocalClean,
		RemoteState: RemoteUnchanged, State: StateObserving, EventID: "edit-observing", EventKind: "observation_started",
		Persistent: observing.Persistent(), Now: time.Unix(1_700_000_101, 0),
		DecisionKind: edit.DecisionOverwrite, AuditReason: "user confirmed reviewed remote replacement",
	})
	if err != nil {
		t.Fatalf("transition session: %v", err)
	}
	if updated.StateVersion != 2 || updated.State != StateObserving || updated.LocalState != LocalClean {
		t.Fatalf("updated session = %#v", updated)
	}

	restarted, err := New(ctx, database)
	if err != nil {
		t.Fatalf("restart store: %v", err)
	}
	got, err := restarted.Get(ctx, created.SessionID)
	if err != nil || got != updated {
		t.Fatalf("reloaded session = (%#v, %v), want %#v", got, err, updated)
	}
	events, err := restarted.ListEvents(ctx, created.SessionID, 0, 10)
	if err != nil || len(events) != 2 || events[0].Sequence != 1 || events[1].Sequence != 2 || events[1].Kind != "observation_started" {
		t.Fatalf("reloaded events = (%#v, %v)", events, err)
	}
	recoverable, err := restarted.ListRecoverable(ctx, 10)
	if err != nil || len(recoverable) != 1 || recoverable[0].Persistent.Version != observing.Version() || recoverable[0].ReferenceID != cache.ReferenceID(strings.Repeat("5", 32)) || recoverable[0].DecisionKind != edit.DecisionOverwrite || recoverable[0].AuditReason == "" {
		t.Fatalf("recoverable sessions = (%#v, %v)", recoverable, err)
	}
}

func TestSessionTransitionRollsBackStateWhenEventCannotBeInserted(t *testing.T) {
	ctx := context.Background()
	database := newEditDatabase(t, ctx)
	seedEditCatalog(t, ctx, database)
	store, err := New(ctx, database)
	if err != nil {
		t.Fatal(err)
	}
	persistent := newPersistentSession(t)
	created, err := store.Create(ctx, CreateRequest{
		SessionID: strings.Repeat("4", 32), SourceEntryID: cache.EntryID(strings.Repeat("2", 64)),
		MaterializationID: cache.MaterializationID(strings.Repeat("3", 32)), LocalState: LocalClean,
		RemoteState: RemoteUnchanged, State: StatePreparing, EventID: "duplicate-event", EventKind: "session_created", Details: recoveryDetails(persistent),
		Now: time.Unix(1_700_000_100, 0),
	})
	if err != nil {
		t.Fatal(err)
	}
	session, _ := edit.RestorePersistent(persistent)
	observing, _ := session.BeginObservation(session.Version())
	_, err = store.Transition(ctx, TransitionRequest{
		SessionID: created.SessionID, ExpectedVersion: 1, LocalState: LocalDirty, RemoteState: RemoteUnchanged,
		State: StateObserving, EventID: "duplicate-event", EventKind: "local_changed", Persistent: observing.Persistent(), Now: time.Unix(1_700_000_101, 0),
	})
	if err == nil {
		t.Fatal("transition with duplicate event ID succeeded")
	}
	got, getErr := store.Get(ctx, created.SessionID)
	if getErr != nil || got != created {
		t.Fatalf("session after rollback = (%#v, %v), want %#v", got, getErr, created)
	}
	if _, err := store.Get(ctx, strings.Repeat("9", 32)); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("missing session error = %v", err)
	}
}

func TestUploadingStateCanOnlyBeEnteredByAtomicJobBinding(t *testing.T) {
	ctx := context.Background()
	database := newEditDatabase(t, ctx)
	seedEditCatalog(t, ctx, database)
	store, err := New(ctx, database)
	if err != nil {
		t.Fatal(err)
	}
	persistent := newPersistentSession(t)
	created, err := store.Create(ctx, CreateRequest{
		SessionID: strings.Repeat("4", 32), SourceEntryID: cache.EntryID(strings.Repeat("2", 64)),
		MaterializationID: cache.MaterializationID(strings.Repeat("3", 32)), LocalState: LocalDirty,
		RemoteState: RemoteUnchanged, State: StateAwaitingDecision, EventID: "edit-created", EventKind: "session_created", Details: recoveryDetails(persistent),
		Now: time.Unix(1_700_000_100, 0),
	})
	if err != nil {
		t.Fatal(err)
	}
	_, err = store.Transition(ctx, TransitionRequest{
		SessionID: created.SessionID, ExpectedVersion: created.StateVersion, LocalState: LocalDirty,
		RemoteState: RemoteUnchanged, State: StateUploading, EventID: "unsafe-upload", EventKind: "uploading",
		Persistent: persistent,
		Now:        time.Unix(1_700_000_101, 0),
	})
	if err == nil {
		t.Fatal("ordinary edit transition entered uploading state")
	}
}

func TestListRecoverableFailsClosedOnSessionWithoutVersion3Details(t *testing.T) {
	ctx := context.Background()
	database := newEditDatabase(t, ctx)
	seedEditCatalog(t, ctx, database)
	created := int64(1_700_000_100)
	if _, err := database.ExecContext(ctx, "INSERT INTO edit_sessions(session_id,source_entry_id,materialization_id,local_state,remote_state,state,state_version,created_at_unix,updated_at_unix) VALUES(?,?,?,'dirty','unknown','recovery',1,?,?)", strings.Repeat("4", 32), strings.Repeat("2", 64), strings.Repeat("3", 32), created, created); err != nil {
		t.Fatal(err)
	}
	store, err := New(ctx, database)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.ListRecoverable(ctx, 10); err == nil {
		t.Fatal("recoverable list silently omitted a session without Version 3 details")
	}
}

func newPersistentSession(t *testing.T) edit.PersistentSession {
	t.Helper()
	target, err := domain.NewLocation("remote", "/file")
	if err != nil {
		t.Fatal(err)
	}
	size := uint64(7)
	modified := time.Unix(1_699_999_999, 0).UTC()
	precision := domain.TimePrecision("second")
	fingerprint := domain.Fingerprint{Size: &size, ModifiedAt: &modified, ModifiedPrecision: &precision}
	session, err := edit.NewSession(edit.NewSessionRequest{
		ID: edit.SessionID(strings.Repeat("4", 32)), Purpose: edit.PurposeEditor,
		Baseline: edit.Baseline{SourceEntryID: cache.EntryID(strings.Repeat("2", 64)), MaterializationID: cache.MaterializationID(strings.Repeat("3", 32)),
			Target: target, ExpectedRemote: edit.RemotePrecondition{Presence: edit.ExpectedPresent, Kind: domain.EntryFile, Fingerprint: fingerprint}, LocalSHA256: edit.SHA256(strings.Repeat("1", 64))},
	})
	if err != nil {
		t.Fatal(err)
	}
	return session.Persistent()
}

func recoveryDetails(persistent edit.PersistentSession) Details {
	return Details{ReferenceID: cache.ReferenceID(strings.Repeat("5", 32)), LeaseID: cache.LeaseID(strings.Repeat("6", 32)), Persistent: persistent}
}

func newEditDatabase(t *testing.T, ctx context.Context) *sql.DB {
	t.Helper()
	root := testkit.PersistentTempDir(t)
	if err := os.Chmod(root, 0o700); err != nil {
		t.Fatal(err)
	}
	database, _, err := statefs.Initialize(ctx, statefs.InitializeConfig{Root: root, DatabasePath: filepath.Join(root, "state.db")})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = database.Close() })
	return database
}

func seedEditCatalog(t *testing.T, ctx context.Context, database *sql.DB) {
	t.Helper()
	created := int64(1_700_000_000)
	blobID := strings.Repeat("1", 64)
	entryID := strings.Repeat("2", 64)
	materializationID := strings.Repeat("3", 32)
	if _, err := database.ExecContext(ctx, "INSERT INTO cache_blobs(sha256,size_bytes,basename,state,created_at_unix,last_access_unix) VALUES(?,7,?,'published',?,?)", blobID, "blobs/sha256/11/"+blobID+".blob", created, created); err != nil {
		t.Fatal(err)
	}
	if _, err := database.ExecContext(ctx, "INSERT INTO cache_entries(entry_id,endpoint_id,path_bytes,fingerprint_strength,freshness,policy,pinned,blob_sha256,complete,created_at_unix,last_access_unix) VALUES(?, 'remote', X'2f66696c65', 'strong', 'fresh', 'lru', 0, ?, 1, ?, ?)", entryID, blobID, created, created); err != nil {
		t.Fatal(err)
	}
	if _, err := database.ExecContext(ctx, "INSERT INTO cache_materializations(materialization_id,entry_id,baseline_blob_sha256,basename,size_bytes,current_sha256,state,pinned,created_at_unix,updated_at_unix,last_access_unix) VALUES(?,?,?,?,7,?,'clean',0,?,?,?)", materializationID, entryID, blobID, "materializations/"+materializationID+"/content", blobID, created, created, created); err != nil {
		t.Fatal(err)
	}
}
