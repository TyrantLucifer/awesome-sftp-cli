// Package editstore persists the Version 3 edit-session lifecycle and its
// bounded audit events. Content bytes remain owned by cachefs.
package editstore

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"time"

	"github.com/TyrantLucifer/awesome-mac-sftp/internal/cache"
	"github.com/TyrantLucifer/awesome-mac-sftp/internal/domain"
	"github.com/TyrantLucifer/awesome-mac-sftp/internal/edit"
	"github.com/TyrantLucifer/awesome-mac-sftp/internal/ipc"
	"github.com/TyrantLucifer/awesome-mac-sftp/internal/state/migration"
	"github.com/TyrantLucifer/awesome-mac-sftp/internal/state/wal"
)

const (
	editStatementWALBudget = uint64(512 * 1024)
	maxEditEventsPage      = 256
)

var eventKindPattern = regexp.MustCompile(`^[a-z][a-z0-9_]{0,63}$`)

type LocalState string

const (
	LocalUnknown LocalState = "unknown"
	LocalClean   LocalState = "clean"
	LocalDirty   LocalState = "dirty"
	LocalDeleted LocalState = "deleted"
)

type RemoteState string

const (
	RemoteUnknown     RemoteState = "unknown"
	RemoteUnchanged   RemoteState = "unchanged"
	RemoteModified    RemoteState = "modified"
	RemoteDeleted     RemoteState = "deleted"
	RemoteUnavailable RemoteState = "unavailable"
)

type State string

const (
	StatePreparing        State = "preparing"
	StateEditing          State = "editing"
	StateObserving        State = "observing"
	StateAwaitingDecision State = "awaiting_decision"
	StateConflict         State = "conflict"
	StateUploading        State = "uploading"
	StateCompleted        State = "completed"
	StateAbandoned        State = "abandoned"
	StateRecovery         State = "recovery"
)

type Store struct {
	database *sql.DB
	walGuard *wal.FileGuard
}

type Record struct {
	SessionID         string                  `json:"session_id"`
	SourceEntryID     cache.EntryID           `json:"source_entry_id"`
	MaterializationID cache.MaterializationID `json:"materialization_id"`
	LocalState        LocalState              `json:"local_state"`
	RemoteState       RemoteState             `json:"remote_state"`
	State             State                   `json:"state"`
	StateVersion      int64                   `json:"state_version"`
	CreatedAt         time.Time               `json:"created_at"`
	UpdatedAt         time.Time               `json:"updated_at"`
}

type EventRecord struct {
	SessionID string    `json:"session_id"`
	Sequence  int64     `json:"sequence"`
	EventID   string    `json:"event_id"`
	Kind      string    `json:"kind"`
	ErrorCode string    `json:"error_code,omitempty"`
	CreatedAt time.Time `json:"created_at"`
}

type CreateRequest struct {
	SessionID         string                  `json:"session_id"`
	SourceEntryID     cache.EntryID           `json:"source_entry_id"`
	MaterializationID cache.MaterializationID `json:"materialization_id"`
	LocalState        LocalState              `json:"local_state"`
	RemoteState       RemoteState             `json:"remote_state"`
	State             State                   `json:"state"`
	EventID           string                  `json:"event_id"`
	EventKind         string                  `json:"event_kind"`
	ErrorCode         string                  `json:"error_code,omitempty"`
	Details           Details                 `json:"details"`
	Now               time.Time               `json:"now"`
}

type TransitionRequest struct {
	SessionID       string                  `json:"session_id"`
	ExpectedVersion int64                   `json:"expected_version"`
	LocalState      LocalState              `json:"local_state"`
	RemoteState     RemoteState             `json:"remote_state"`
	State           State                   `json:"state"`
	EventID         string                  `json:"event_id"`
	EventKind       string                  `json:"event_kind"`
	ErrorCode       string                  `json:"error_code,omitempty"`
	Persistent      edit.PersistentSession  `json:"persistent"`
	Target          domain.Location         `json:"target,omitempty"`
	ExpectedRemote  edit.RemotePrecondition `json:"expected_remote,omitempty"`
	DecisionKind    edit.DecisionKind       `json:"decision_kind,omitempty"`
	AuditReason     string                  `json:"audit_reason,omitempty"`
	Now             time.Time               `json:"now"`
}

type Details struct {
	ReferenceID    cache.ReferenceID       `json:"reference_id"`
	LeaseID        cache.LeaseID           `json:"lease_id"`
	Persistent     edit.PersistentSession  `json:"persistent"`
	Target         domain.Location         `json:"target,omitempty"`
	ExpectedRemote edit.RemotePrecondition `json:"expected_remote,omitempty"`
	DecisionKind   edit.DecisionKind       `json:"decision_kind,omitempty"`
	AuditReason    string                  `json:"audit_reason,omitempty"`
}

type RecoveryRecord struct {
	Record
	Details
}

func New(ctx context.Context, database *sql.DB) (*Store, error) {
	if database == nil {
		return nil, errors.New("new edit store: nil database")
	}
	connection, err := database.Conn(ctx)
	if err != nil {
		return nil, fmt.Errorf("new edit store: reserve connection: %w", err)
	}
	defer connection.Close()
	compiled := []migration.Migration{migration.Version1(), migration.Version2(), migration.Version3()}
	contracts := map[uint64][]byte{1: migration.Version1SchemaContract(), 2: migration.Version2SchemaContract(), 3: migration.Version3SchemaContract()}
	if err := migration.ValidateHead(ctx, connection, compiled, contracts, 3); err != nil {
		return nil, fmt.Errorf("new edit store: %w", err)
	}
	guard, err := wal.OpenFileGuard(ctx, connection)
	if err != nil {
		return nil, fmt.Errorf("new edit store: %w", err)
	}
	return &Store{database: database, walGuard: guard}, nil
}

func (store *Store) Create(ctx context.Context, request CreateRequest) (Record, error) {
	if err := validateCreate(request); err != nil {
		return Record{}, err
	}
	var record Record
	err := store.write(ctx, []uint64{editStatementWALBudget, editStatementWALBudget, editStatementWALBudget}, func(connection *sql.Conn, writer *transactionWriter) error {
		now := request.Now.UTC().Truncate(time.Second).Unix()
		if _, err := writer.ExecContext(ctx, "INSERT INTO edit_sessions(session_id,source_entry_id,materialization_id,local_state,remote_state,state,state_version,created_at_unix,updated_at_unix) VALUES(?,?,?,?,?,?,1,?,?)", request.SessionID, request.SourceEntryID, request.MaterializationID, request.LocalState, request.RemoteState, request.State, now, now); err != nil {
			return fmt.Errorf("create edit session: %w", err)
		}
		if _, err := writer.ExecContext(ctx, "INSERT INTO edit_session_events(session_id,sequence,event_id,kind,error_code,created_at_unix) VALUES(?,1,?,?,?,?)", request.SessionID, request.EventID, request.EventKind, nullableString(request.ErrorCode), now); err != nil {
			return fmt.Errorf("create edit session event: %w", err)
		}
		if err := insertDetails(ctx, writer, request.SessionID, request.Details, now); err != nil {
			return err
		}
		var err error
		record, err = get(ctx, connection, request.SessionID)
		return err
	})
	return record, err
}

func (store *Store) Transition(ctx context.Context, request TransitionRequest) (Record, error) {
	if err := validateTransition(request); err != nil {
		return Record{}, err
	}
	var record Record
	err := store.write(ctx, []uint64{editStatementWALBudget, editStatementWALBudget, editStatementWALBudget}, func(connection *sql.Conn, writer *transactionWriter) error {
		var nextSequence int64
		if err := connection.QueryRowContext(ctx, "SELECT COALESCE(MAX(sequence),0)+1 FROM edit_session_events WHERE session_id=?", request.SessionID).Scan(&nextSequence); err != nil {
			return fmt.Errorf("transition edit session sequence: %w", err)
		}
		now := request.Now.UTC().Truncate(time.Second).Unix()
		result, err := writer.ExecContext(ctx, "UPDATE edit_sessions SET local_state=?,remote_state=?,state=?,state_version=state_version+1,updated_at_unix=? WHERE session_id=? AND state_version=?", request.LocalState, request.RemoteState, request.State, now, request.SessionID, request.ExpectedVersion)
		if err := requireOne("transition edit session", result, err); err != nil {
			return err
		}
		if _, err := writer.ExecContext(ctx, "INSERT INTO edit_session_events(session_id,sequence,event_id,kind,error_code,created_at_unix) VALUES(?,?,?,?,?,?)", request.SessionID, nextSequence, request.EventID, request.EventKind, nullableString(request.ErrorCode), now); err != nil {
			return fmt.Errorf("transition edit session event: %w", err)
		}
		if err := updateDetails(ctx, connection, writer, request, now); err != nil {
			return err
		}
		record, err = get(ctx, connection, request.SessionID)
		return err
	})
	return record, err
}

func (store *Store) ListRecoverable(ctx context.Context, limit int) ([]RecoveryRecord, error) {
	if limit < 1 || limit > 256 {
		return nil, errors.New("list recoverable edit sessions: limit outside 1..256")
	}
	var missingDetails int
	if err := store.database.QueryRowContext(ctx, `SELECT count(*) FROM edit_sessions s LEFT JOIN edit_session_details d ON d.session_id=s.session_id
		WHERE s.state NOT IN ('completed','abandoned') AND d.session_id IS NULL`).Scan(&missingDetails); err != nil {
		return nil, fmt.Errorf("list recoverable edit sessions: inspect recovery details: %w", err)
	}
	if missingDetails != 0 {
		return nil, fmt.Errorf("list recoverable edit sessions: %d session(s) lack Version 3 recovery details", missingDetails)
	}
	rows, err := store.database.QueryContext(ctx, `SELECT s.session_id,s.source_entry_id,s.materialization_id,s.local_state,s.remote_state,s.state,s.state_version,s.created_at_unix,s.updated_at_unix,
		d.reference_id,d.lease_id,d.machine_json,d.decision_kind,d.audit_reason,d.target_endpoint_id,d.target_path_bytes,d.expected_presence,d.expected_kind,d.expected_fingerprint_json
		FROM edit_sessions s JOIN edit_session_details d ON d.session_id=s.session_id
		WHERE s.state NOT IN ('completed','abandoned') ORDER BY s.updated_at_unix DESC,s.session_id LIMIT ?`, limit)
	if err != nil {
		return nil, fmt.Errorf("list recoverable edit sessions: %w", err)
	}
	defer rows.Close()
	result := make([]RecoveryRecord, 0, limit)
	for rows.Next() {
		item, err := scanRecovery(rows)
		if err != nil {
			return nil, err
		}
		result = append(result, item)
	}
	return result, rows.Err()
}

func (store *Store) Get(ctx context.Context, sessionID string) (Record, error) {
	if err := validateSessionID(sessionID); err != nil {
		return Record{}, err
	}
	return get(ctx, store.database, sessionID)
}

func get(ctx context.Context, source interface {
	QueryRowContext(context.Context, string, ...any) *sql.Row
}, sessionID string) (Record, error) {
	var record Record
	var created, updated int64
	err := source.QueryRowContext(ctx, "SELECT session_id,source_entry_id,materialization_id,local_state,remote_state,state,state_version,created_at_unix,updated_at_unix FROM edit_sessions WHERE session_id=?", sessionID).Scan(&record.SessionID, &record.SourceEntryID, &record.MaterializationID, &record.LocalState, &record.RemoteState, &record.State, &record.StateVersion, &created, &updated)
	if err != nil {
		return Record{}, err
	}
	record.CreatedAt = time.Unix(created, 0).UTC()
	record.UpdatedAt = time.Unix(updated, 0).UTC()
	return record, nil
}

func (store *Store) ListEvents(ctx context.Context, sessionID string, afterSequence int64, limit int) ([]EventRecord, error) {
	if err := validateSessionID(sessionID); err != nil {
		return nil, err
	}
	if afterSequence < 0 || limit < 1 || limit > maxEditEventsPage {
		return nil, errors.New("list edit session events: invalid sequence or limit")
	}
	rows, err := store.database.QueryContext(ctx, "SELECT session_id,sequence,event_id,kind,error_code,created_at_unix FROM edit_session_events WHERE session_id=? AND sequence>? ORDER BY sequence LIMIT ?", sessionID, afterSequence, limit)
	if err != nil {
		return nil, fmt.Errorf("list edit session events: %w", err)
	}
	defer rows.Close()
	result := make([]EventRecord, 0, limit)
	for rows.Next() {
		var item EventRecord
		var errorCode sql.NullString
		var created int64
		if err := rows.Scan(&item.SessionID, &item.Sequence, &item.EventID, &item.Kind, &errorCode, &created); err != nil {
			return nil, err
		}
		item.ErrorCode = errorCode.String
		item.CreatedAt = time.Unix(created, 0).UTC()
		result = append(result, item)
	}
	return result, rows.Err()
}

func insertDetails(ctx context.Context, writer *transactionWriter, sessionID string, details Details, now int64) error {
	details, machineJSON, originalJSON, expectedJSON, err := prepareDetails(sessionID, details)
	if err != nil {
		return err
	}
	originalKind, originalFingerprint := preconditionColumns(details.Persistent.Baseline.ExpectedRemote, originalJSON)
	expectedKind, expectedFingerprint := preconditionColumns(details.ExpectedRemote, expectedJSON)
	_, err = writer.ExecContext(ctx, `INSERT INTO edit_session_details(session_id,purpose,reference_id,lease_id,machine_json,decision_kind,audit_reason,target_endpoint_id,target_path_bytes,
		baseline_local_sha256,current_local_sha256,original_presence,original_kind,original_fingerprint_json,expected_presence,expected_kind,expected_fingerprint_json,created_at_unix,updated_at_unix)
		VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`, sessionID, details.Persistent.Purpose, details.ReferenceID, details.LeaseID, machineJSON,
		nullableString(string(details.DecisionKind)), nullableString(details.AuditReason), details.Target.EndpointID, []byte(details.Target.Path),
		details.Persistent.Baseline.LocalSHA256, nullableString(string(details.Persistent.CurrentLocal)), details.Persistent.Baseline.ExpectedRemote.Presence,
		originalKind, originalFingerprint, details.ExpectedRemote.Presence, expectedKind, expectedFingerprint, now, now)
	if err != nil {
		return fmt.Errorf("create edit session recovery details: %w", err)
	}
	return nil
}

func updateDetails(ctx context.Context, connection *sql.Conn, writer *transactionWriter, request TransitionRequest, now int64) error {
	var referenceID cache.ReferenceID
	var leaseID cache.LeaseID
	var targetEndpoint string
	var targetPath []byte
	var expectedPresence edit.ExpectedPresence
	var expectedKind sql.NullString
	var expectedJSON []byte
	var decision sql.NullString
	var audit sql.NullString
	if err := connection.QueryRowContext(ctx, "SELECT reference_id,lease_id,target_endpoint_id,target_path_bytes,expected_presence,expected_kind,expected_fingerprint_json,decision_kind,audit_reason FROM edit_session_details WHERE session_id=?", request.SessionID).Scan(&referenceID, &leaseID, &targetEndpoint, &targetPath, &expectedPresence, &expectedKind, &expectedJSON, &decision, &audit); err != nil {
		return fmt.Errorf("load edit session recovery details: %w", err)
	}
	target := domain.Location{EndpointID: domain.EndpointID(targetEndpoint), Path: domain.CanonicalPath(targetPath)}
	expected, err := decodePrecondition(expectedPresence, expectedKind.String, expectedJSON)
	if err != nil {
		return err
	}
	if request.Target.EndpointID != "" || request.Target.Path != "" {
		target = request.Target
	}
	if request.ExpectedRemote.Presence != "" {
		expected = request.ExpectedRemote
	}
	details := Details{ReferenceID: referenceID, LeaseID: leaseID, Persistent: request.Persistent, Target: target, ExpectedRemote: expected, DecisionKind: edit.DecisionKind(decision.String), AuditReason: audit.String}
	if request.DecisionKind != "" {
		details.DecisionKind = request.DecisionKind
		details.AuditReason = request.AuditReason
	}
	details, machineJSON, _, expectedEncoded, err := prepareDetails(request.SessionID, details)
	if err != nil {
		return err
	}
	expectedKindValue, expectedFingerprint := preconditionColumns(details.ExpectedRemote, expectedEncoded)
	result, err := writer.ExecContext(ctx, `UPDATE edit_session_details SET machine_json=?,decision_kind=?,audit_reason=?,target_endpoint_id=?,target_path_bytes=?,current_local_sha256=?,
		expected_presence=?,expected_kind=?,expected_fingerprint_json=?,updated_at_unix=? WHERE session_id=?`, machineJSON, nullableString(string(details.DecisionKind)), nullableString(details.AuditReason),
		details.Target.EndpointID, []byte(details.Target.Path), nullableString(string(details.Persistent.CurrentLocal)), details.ExpectedRemote.Presence, expectedKindValue, expectedFingerprint, now, request.SessionID)
	return requireOne("update edit session recovery details", result, err)
}

func prepareDetails(sessionID string, details Details) (Details, []byte, []byte, []byte, error) {
	if _, err := cache.ParseReferenceID(string(details.ReferenceID)); err != nil {
		return Details{}, nil, nil, nil, fmt.Errorf("edit session recovery details: %w", err)
	}
	if _, err := cache.ParseLeaseID(string(details.LeaseID)); err != nil {
		return Details{}, nil, nil, nil, fmt.Errorf("edit session recovery details: %w", err)
	}
	if _, err := edit.RestorePersistent(details.Persistent); err != nil || string(details.Persistent.ID) != sessionID {
		return Details{}, nil, nil, nil, errors.New("edit session recovery details: invalid persistent state")
	}
	if details.Target.EndpointID == "" && details.Target.Path == "" {
		details.Target = details.Persistent.Baseline.Target
	}
	if _, err := domain.NewLocation(details.Target.EndpointID, details.Target.Path); err != nil {
		return Details{}, nil, nil, nil, fmt.Errorf("edit session recovery target: %w", err)
	}
	if details.ExpectedRemote.Presence == "" {
		details.ExpectedRemote = details.Persistent.Baseline.ExpectedRemote
	}
	originalJSON, err := encodePrecondition(details.Persistent.Baseline.ExpectedRemote)
	if err != nil {
		return Details{}, nil, nil, nil, err
	}
	expectedJSON, err := encodePrecondition(details.ExpectedRemote)
	if err != nil {
		return Details{}, nil, nil, nil, err
	}
	if details.DecisionKind != "" && details.DecisionKind != edit.DecisionUpload && details.DecisionKind != edit.DecisionOverwrite && details.DecisionKind != edit.DecisionSaveAs && details.DecisionKind != edit.DecisionSkip {
		return Details{}, nil, nil, nil, errors.New("edit session recovery details: invalid decision kind")
	}
	if len(details.AuditReason) > 512 || details.AuditReason != "" && details.DecisionKind == "" {
		return Details{}, nil, nil, nil, errors.New("edit session recovery details: invalid audit reason")
	}
	machineJSON, err := json.Marshal(details.Persistent)
	if err != nil || len(machineJSON) < 2 || len(machineJSON) > 32768 {
		return Details{}, nil, nil, nil, errors.New("edit session recovery details: invalid machine state")
	}
	return details, machineJSON, originalJSON, expectedJSON, nil
}

func encodePrecondition(value edit.RemotePrecondition) ([]byte, error) {
	if value.Presence == edit.ExpectedAbsent {
		if value.Kind != "" || value.Fingerprint.Strength() != domain.FingerprintWeak {
			return nil, errors.New("edit session recovery details: invalid expected-absent precondition")
		}
		return nil, nil
	}
	if value.Presence != edit.ExpectedPresent || value.Kind != domain.EntryFile || value.Fingerprint.Strength() == domain.FingerprintWeak {
		return nil, errors.New("edit session recovery details: invalid expected-present precondition")
	}
	encoded, err := json.Marshal(ipc.EncodeFingerprint(value.Fingerprint))
	if err != nil || len(encoded) < 2 || len(encoded) > 8192 {
		return nil, errors.New("edit session recovery details: invalid fingerprint encoding")
	}
	return encoded, nil
}

func preconditionColumns(value edit.RemotePrecondition, encoded []byte) (any, any) {
	if value.Presence == edit.ExpectedAbsent {
		return nil, nil
	}
	return value.Kind, encoded
}

type recoveryScanner interface{ Scan(...any) error }

func scanRecovery(row recoveryScanner) (RecoveryRecord, error) {
	var result RecoveryRecord
	var created, updated int64
	var machineJSON []byte
	var decision, audit, expectedKind sql.NullString
	var targetEndpoint string
	var targetPath, expectedJSON []byte
	var expectedPresence edit.ExpectedPresence
	if err := row.Scan(&result.SessionID, &result.SourceEntryID, &result.MaterializationID, &result.LocalState, &result.RemoteState, &result.State, &result.StateVersion, &created, &updated,
		&result.ReferenceID, &result.LeaseID, &machineJSON, &decision, &audit, &targetEndpoint, &targetPath, &expectedPresence, &expectedKind, &expectedJSON); err != nil {
		return RecoveryRecord{}, err
	}
	decoder := json.NewDecoder(bytes.NewReader(machineJSON))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&result.Persistent); err != nil || decoder.Decode(&struct{}{}) == nil {
		return RecoveryRecord{}, errors.New("decode edit session recovery machine state")
	}
	if _, err := edit.RestorePersistent(result.Persistent); err != nil {
		return RecoveryRecord{}, err
	}
	result.Target = domain.Location{EndpointID: domain.EndpointID(targetEndpoint), Path: domain.CanonicalPath(targetPath)}
	var err error
	result.ExpectedRemote, err = decodePrecondition(expectedPresence, expectedKind.String, expectedJSON)
	if err != nil {
		return RecoveryRecord{}, err
	}
	result.DecisionKind = edit.DecisionKind(decision.String)
	result.AuditReason = audit.String
	result.CreatedAt = time.Unix(created, 0).UTC()
	result.UpdatedAt = time.Unix(updated, 0).UTC()
	return result, nil
}

func decodePrecondition(presence edit.ExpectedPresence, kind string, encoded []byte) (edit.RemotePrecondition, error) {
	if presence == edit.ExpectedAbsent {
		return edit.RemotePrecondition{Presence: edit.ExpectedAbsent}, nil
	}
	if presence != edit.ExpectedPresent || kind != string(domain.EntryFile) {
		return edit.RemotePrecondition{}, errors.New("decode edit session recovery precondition")
	}
	var wire ipc.WireFingerprint
	decoder := json.NewDecoder(bytes.NewReader(encoded))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&wire); err != nil {
		return edit.RemotePrecondition{}, err
	}
	fingerprint := ipc.DecodeFingerprint(wire)
	if fingerprint.Strength() == domain.FingerprintWeak {
		return edit.RemotePrecondition{}, errors.New("decode edit session recovery weak fingerprint")
	}
	return edit.RemotePrecondition{Presence: edit.ExpectedPresent, Kind: domain.EntryFile, Fingerprint: fingerprint}, nil
}

type transactionWriter struct {
	connection  *sql.Conn
	transaction *wal.FileTransaction
	next        int
}

func (writer *transactionWriter) ExecContext(ctx context.Context, query string, args ...any) (sql.Result, error) {
	result, execErr := writer.connection.ExecContext(ctx, query, args...)
	observeErr := writer.transaction.AfterStatement(writer.next)
	writer.next++
	return result, errors.Join(execErr, observeErr)
}

func (store *Store) write(ctx context.Context, budgets []uint64, operation func(*sql.Conn, *transactionWriter) error) (returnErr error) {
	if store == nil || store.database == nil || store.walGuard == nil {
		return errors.New("edit store: nil database")
	}
	connection, err := store.database.Conn(ctx)
	if err != nil {
		return err
	}
	defer func() { returnErr = errors.Join(returnErr, connection.Close()) }()
	transaction, err := store.walGuard.Begin(budgets)
	if err != nil {
		return err
	}
	if _, err := connection.ExecContext(ctx, "BEGIN IMMEDIATE"); err != nil {
		return errors.Join(err, transaction.AfterRollback())
	}
	writer := &transactionWriter{connection: connection, transaction: transaction}
	if err := operation(connection, writer); err != nil {
		_, rollbackErr := connection.ExecContext(context.Background(), "ROLLBACK")
		return errors.Join(err, rollbackErr, transaction.AfterRollback())
	}
	if err := transaction.BeforeCommit(); err != nil {
		_, rollbackErr := connection.ExecContext(context.Background(), "ROLLBACK")
		return errors.Join(err, rollbackErr, transaction.AfterRollback())
	}
	if _, err := connection.ExecContext(ctx, "COMMIT"); err != nil {
		return errors.Join(err, transaction.AfterRollback())
	}
	if err := transaction.AfterCommit(); err != nil {
		return err
	}
	_, err = store.walGuard.PassiveCheckpoint(ctx, connection)
	return err
}

func validateCreate(request CreateRequest) error {
	if err := validateSessionID(request.SessionID); err != nil {
		return fmt.Errorf("create edit session: %w", err)
	}
	if _, err := cache.ParseEntryID(string(request.SourceEntryID)); err != nil {
		return fmt.Errorf("create edit session: %w", err)
	}
	if _, err := cache.ParseMaterializationID(string(request.MaterializationID)); err != nil {
		return fmt.Errorf("create edit session: %w", err)
	}
	if request.Details.Persistent.Version != 1 || string(request.Details.Persistent.ID) != request.SessionID {
		return errors.New("create edit session: persistent machine must start at version 1")
	}
	return validateLifecycle(request.LocalState, request.RemoteState, request.State, request.EventID, request.EventKind, request.ErrorCode, request.Now)
}

func validateTransition(request TransitionRequest) error {
	if err := validateSessionID(request.SessionID); err != nil {
		return fmt.Errorf("transition edit session: %w", err)
	}
	if request.ExpectedVersion < 1 {
		return errors.New("transition edit session: invalid expected version")
	}
	if request.State == StateUploading {
		return errors.New("transition edit session: uploading is reserved for atomic Job binding")
	}
	if request.Persistent.Version != edit.Version(request.ExpectedVersion+1) || string(request.Persistent.ID) != request.SessionID {
		return errors.New("transition edit session: persistent machine version does not advance exactly once")
	}
	return validateLifecycle(request.LocalState, request.RemoteState, request.State, request.EventID, request.EventKind, request.ErrorCode, request.Now)
}

func validateLifecycle(local LocalState, remote RemoteState, state State, eventID, eventKind, errorCode string, now time.Time) error {
	if !local.valid() || !remote.valid() || !state.valid() || len(eventID) < 1 || len(eventID) > 128 || !eventKindPattern.MatchString(eventKind) || len(errorCode) > 128 || now.Unix() <= 0 {
		return errors.New("edit session: invalid lifecycle state, event, or time")
	}
	return nil
}

func validateSessionID(value string) error {
	if len(value) != 32 {
		return errors.New("edit session ID must be 32 lowercase hex characters")
	}
	for _, character := range value {
		if character < '0' || character > '9' && character < 'a' || character > 'f' {
			return errors.New("edit session ID must be 32 lowercase hex characters")
		}
	}
	return nil
}

func (value LocalState) valid() bool {
	return value == LocalUnknown || value == LocalClean || value == LocalDirty || value == LocalDeleted
}
func (value RemoteState) valid() bool {
	return value == RemoteUnknown || value == RemoteUnchanged || value == RemoteModified || value == RemoteDeleted || value == RemoteUnavailable
}
func (value State) valid() bool {
	return value == StatePreparing || value == StateEditing || value == StateObserving || value == StateAwaitingDecision || value == StateConflict || value == StateUploading || value == StateCompleted || value == StateAbandoned || value == StateRecovery
}

func requireOne(label string, result sql.Result, err error) error {
	if err != nil {
		return fmt.Errorf("%s: %w", label, err)
	}
	count, err := result.RowsAffected()
	if err != nil || count != 1 {
		return fmt.Errorf("%s: row not found or version changed", label)
	}
	return nil
}

func nullableString(value string) any {
	if value == "" {
		return nil
	}
	return value
}
