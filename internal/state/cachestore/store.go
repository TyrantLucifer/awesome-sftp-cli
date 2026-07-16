// Package cachestore persists the typed Stage 3 cache catalog in the frozen
// SQLite Version 2 schema. File publication and deletion remain cachefs-owned.
package cachestore

import (
	"context"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/TyrantLucifer/awesome-mac-sftp/internal/cache"
	"github.com/TyrantLucifer/awesome-mac-sftp/internal/state/migration"
	"github.com/TyrantLucifer/awesome-mac-sftp/internal/state/wal"
)

const (
	statementWALBudget = uint64(1024 * 1024)
	fingerprintCodec   = "amsftp-canonical-v1"
)

type Store struct {
	database *sql.DB
	walGuard *wal.FileGuard
}

func New(ctx context.Context, database *sql.DB) (*Store, error) {
	if database == nil {
		return nil, fmt.Errorf("new cache store: nil database")
	}
	connection, err := database.Conn(ctx)
	if err != nil {
		return nil, fmt.Errorf("new cache store: reserve validation connection: %w", err)
	}
	defer connection.Close()
	compiled := []migration.Migration{migration.Version1(), migration.Version2()}
	contracts := map[uint64][]byte{1: migration.Version1SchemaContract(), 2: migration.Version2SchemaContract()}
	if err := migration.ValidateHead(ctx, connection, compiled, contracts, 2); err != nil {
		return nil, fmt.Errorf("new cache store: %w", err)
	}
	guard, err := wal.OpenFileGuard(ctx, connection)
	if err != nil {
		return nil, fmt.Errorf("new cache store: %w", err)
	}
	return &Store{database: database, walGuard: guard}, nil
}

func (store *Store) Publish(ctx context.Context, blob cache.Blob, entry cache.Entry) error {
	if err := validateBlob(blob); err != nil {
		return fmt.Errorf("publish cache catalog: %w", err)
	}
	if err := validateEntry(entry); err != nil {
		return fmt.Errorf("publish cache catalog: %w", err)
	}
	if entry.BlobID != blob.ID {
		return fmt.Errorf("publish cache catalog: entry blob %q does not match published blob %q", entry.BlobID, blob.ID)
	}
	if blob.State != cache.BlobPublished {
		return fmt.Errorf("publish cache catalog: blob %q is not published", blob.ID)
	}
	return store.write(ctx, 2, func(connection *sql.Conn, writer *transactionWriter) error {
		result, err := writer.ExecContext(ctx, "INSERT INTO cache_blobs(sha256,size_bytes,basename,state,created_at_unix,last_access_unix) VALUES(?,?,?,?,?,?) ON CONFLICT(sha256) DO UPDATE SET last_access_unix=max(cache_blobs.last_access_unix,excluded.last_access_unix) WHERE cache_blobs.size_bytes=excluded.size_bytes AND cache_blobs.basename=excluded.basename AND cache_blobs.state='published' AND excluded.state='published'", blob.ID, blob.Size, blobBasename(blob.ID), blob.State, unix(blob.CreatedAt), unix(blob.LastAccessAt))
		if err := requireOne("publish cache blob", result, err); err != nil {
			return fmt.Errorf("publish cache blob %q: %w", blob.ID, err)
		}
		strength, algorithm, encoded := encodeFingerprint(entry.Fingerprint)
		if _, err := writer.ExecContext(ctx, "INSERT INTO cache_entries(entry_id,endpoint_id,path_bytes,fingerprint_strength,fingerprint_size,modified_unix_nano,modified_precision,file_id,version_id,hash_algorithm,hash_hex,freshness,policy,workspace_id,pinned,blob_sha256,complete,created_at_unix,last_access_unix) VALUES(?,?,?,?,NULL,NULL,NULL,NULL,NULL,?,?,?,?,?,?,?,?,?,?)", entry.ID, entry.EndpointID, entry.CanonicalPath, strength, algorithm, encoded, entry.Freshness, entry.Policy, string(entry.WorkspaceID), boolInt(entry.Pinned), entry.BlobID, 1, unix(entry.CreatedAt), unix(entry.LastAccessAt)); err != nil {
			return fmt.Errorf("publish cache entry %q: %w", entry.ID, err)
		}
		return nil
	})
}

func (store *Store) GetBlob(ctx context.Context, id cache.BlobID) (cache.Blob, error) {
	if _, err := cache.ParseBlobID(string(id)); err != nil {
		return cache.Blob{}, fmt.Errorf("get cache blob: %w", err)
	}
	return scanBlob(store.database.QueryRowContext(ctx, blobSelect+" WHERE sha256=?", id))
}

func (store *Store) ListBlobs(ctx context.Context) ([]cache.Blob, error) {
	return listBlobs(ctx, store.database)
}

func listBlobs(ctx context.Context, source queryer) ([]cache.Blob, error) {
	rows, err := source.QueryContext(ctx, blobSelect+" ORDER BY sha256")
	if err != nil {
		return nil, fmt.Errorf("list cache blobs: %w", err)
	}
	defer rows.Close()
	var result []cache.Blob
	for rows.Next() {
		item, err := scanBlob(rows)
		if err != nil {
			return nil, err
		}
		result = append(result, item)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("list cache blobs: %w", err)
	}
	return result, nil
}

func (store *Store) TouchBlob(ctx context.Context, id cache.BlobID, at time.Time) error {
	if _, err := cache.ParseBlobID(string(id)); err != nil {
		return fmt.Errorf("touch cache blob: %w", err)
	}
	if err := validateTime("blob access", at); err != nil {
		return err
	}
	return store.updateOne(ctx, "touch cache blob", "UPDATE cache_blobs SET last_access_unix=? WHERE sha256=? AND last_access_unix<=?", unix(at), id, unix(at))
}

func (store *Store) GetEntry(ctx context.Context, id cache.EntryID) (cache.Entry, error) {
	if _, err := cache.ParseEntryID(string(id)); err != nil {
		return cache.Entry{}, fmt.Errorf("get cache entry: %w", err)
	}
	return scanEntry(store.database.QueryRowContext(ctx, entrySelect+" WHERE entry_id=?", id))
}

func (store *Store) ListEntries(ctx context.Context) ([]cache.Entry, error) {
	return listEntries(ctx, store.database)
}

func listEntries(ctx context.Context, source queryer) ([]cache.Entry, error) {
	rows, err := source.QueryContext(ctx, entrySelect+" ORDER BY entry_id")
	if err != nil {
		return nil, fmt.Errorf("list cache entries: %w", err)
	}
	defer rows.Close()
	var result []cache.Entry
	for rows.Next() {
		item, err := scanEntry(rows)
		if err != nil {
			return nil, err
		}
		result = append(result, item)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("list cache entries: %w", err)
	}
	return result, nil
}

func (store *Store) TouchEntry(ctx context.Context, id cache.EntryID, at time.Time) error {
	if _, err := cache.ParseEntryID(string(id)); err != nil {
		return fmt.Errorf("touch cache entry: %w", err)
	}
	if err := validateTime("entry access", at); err != nil {
		return err
	}
	return store.updateOne(ctx, "touch cache entry", "UPDATE cache_entries SET last_access_unix=? WHERE entry_id=? AND last_access_unix<=?", unix(at), id, unix(at))
}

func (store *Store) CreateMaterialization(ctx context.Context, item cache.Materialization) error {
	if err := validateMaterialization(item); err != nil {
		return fmt.Errorf("create cache materialization: %w", err)
	}
	return store.write(ctx, 1, func(_ *sql.Conn, w *transactionWriter) error {
		_, err := w.ExecContext(ctx, "INSERT INTO cache_materializations(materialization_id,entry_id,baseline_blob_sha256,basename,size_bytes,current_sha256,state,pinned,created_at_unix,updated_at_unix,last_access_unix) VALUES(?,?,?,?,?,?,?,?,?,?,?)", item.ID, item.EntryID, item.BaselineBlobID, materializationBasename(item.ID), item.Size, item.CurrentBlobID, item.State, boolInt(item.Pinned), unix(item.CreatedAt), unix(item.LastAccessAt), unix(item.LastAccessAt))
		if err != nil {
			return fmt.Errorf("create cache materialization %q: %w", item.ID, err)
		}
		return nil
	})
}

func (store *Store) UpdateMaterialization(ctx context.Context, item cache.Materialization) error {
	if err := validateMaterialization(item); err != nil {
		return fmt.Errorf("update cache materialization: %w", err)
	}
	return store.write(ctx, 1, func(_ *sql.Conn, w *transactionWriter) error {
		result, err := w.ExecContext(ctx, "UPDATE cache_materializations SET entry_id=?,baseline_blob_sha256=?,size_bytes=?,current_sha256=?,state=?,pinned=?,updated_at_unix=?,last_access_unix=? WHERE materialization_id=? AND basename=? AND created_at_unix=? AND updated_at_unix<=? AND last_access_unix<=?", item.EntryID, item.BaselineBlobID, item.Size, item.CurrentBlobID, item.State, boolInt(item.Pinned), unix(item.LastAccessAt), unix(item.LastAccessAt), item.ID, materializationBasename(item.ID), unix(item.CreatedAt), unix(item.LastAccessAt), unix(item.LastAccessAt))
		return requireOne("update cache materialization", result, err)
	})
}

func (store *Store) ListMaterializations(ctx context.Context) ([]cache.Materialization, error) {
	return listMaterializations(ctx, store.database)
}

func listMaterializations(ctx context.Context, source queryer) ([]cache.Materialization, error) {
	rows, err := source.QueryContext(ctx, materializationSelect+" ORDER BY materialization_id")
	if err != nil {
		return nil, fmt.Errorf("list cache materializations: %w", err)
	}
	defer rows.Close()
	var result []cache.Materialization
	for rows.Next() {
		item, err := scanMaterialization(rows)
		if err != nil {
			return nil, err
		}
		result = append(result, item)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("list cache materializations: %w", err)
	}
	return result, nil
}

func (store *Store) AddReference(ctx context.Context, item cache.Reference) error {
	if err := validateReference(item); err != nil {
		return fmt.Errorf("add cache reference: %w", err)
	}
	blob, materialization := targetValues(item.Target)
	return store.write(ctx, 1, func(_ *sql.Conn, w *transactionWriter) error {
		_, err := w.ExecContext(ctx, "INSERT INTO cache_references(reference_id,owner_kind,owner_id,blob_sha256,materialization_id,created_at_unix) VALUES(?,?,?,?,?,?)", item.ID, item.OwnerKind, item.OwnerID, blob, materialization, unix(item.CreatedAt))
		if err != nil {
			return fmt.Errorf("add cache reference %q: %w", item.ID, err)
		}
		return nil
	})
}

func (store *Store) RemoveReference(ctx context.Context, id cache.ReferenceID) error {
	if _, err := cache.ParseReferenceID(string(id)); err != nil {
		return fmt.Errorf("remove cache reference: %w", err)
	}
	return store.updateOne(ctx, "remove cache reference", "DELETE FROM cache_references WHERE reference_id=?", id)
}

func (store *Store) ListReferences(ctx context.Context) ([]cache.Reference, error) {
	return listReferences(ctx, store.database)
}

func listReferences(ctx context.Context, source queryer) ([]cache.Reference, error) {
	rows, err := source.QueryContext(ctx, referenceSelect+" ORDER BY reference_id")
	if err != nil {
		return nil, fmt.Errorf("list cache references: %w", err)
	}
	defer rows.Close()
	var result []cache.Reference
	for rows.Next() {
		item, err := scanReference(rows)
		if err != nil {
			return nil, err
		}
		result = append(result, item)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("list cache references: %w", err)
	}
	return result, nil
}

func (store *Store) AcquireLease(ctx context.Context, item cache.Lease) error {
	if err := validateLease(item, false); err != nil {
		return fmt.Errorf("acquire cache lease: %w", err)
	}
	blob, materialization := targetValues(item.Target)
	owner, err := encodeLeaseOwner(item.OwnerKind)
	if err != nil {
		return err
	}
	pid, birth := processValues(item.Process)
	return store.write(ctx, 1, func(_ *sql.Conn, w *transactionWriter) error {
		_, err := w.ExecContext(ctx, "INSERT INTO cache_leases(lease_id,blob_sha256,materialization_id,owner_kind,owner_id,daemon_instance_id,owner_pid,process_birth_identity,heartbeat_at_unix,expires_at_unix,grace_until_unix,state) VALUES(?,?,?,?,?,?,?,?,?,?,?,'active')", item.ID, blob, materialization, owner, item.OwnerID, item.DaemonInstanceID, pid, birth, unix(item.HeartbeatAt), unix(item.ExpiresAt), unix(item.GraceUntil))
		if err != nil {
			return fmt.Errorf("acquire cache lease %q: %w", item.ID, err)
		}
		return nil
	})
}

func (store *Store) HeartbeatLease(ctx context.Context, item cache.Lease) error {
	if err := validateLease(item, false); err != nil {
		return fmt.Errorf("heartbeat cache lease: %w", err)
	}
	owner, err := encodeLeaseOwner(item.OwnerKind)
	if err != nil {
		return err
	}
	blob, materialization := targetValues(item.Target)
	pid, birth := processValues(item.Process)
	return store.write(ctx, 1, func(_ *sql.Conn, w *transactionWriter) error {
		result, err := w.ExecContext(ctx, "UPDATE cache_leases SET heartbeat_at_unix=?,expires_at_unix=?,grace_until_unix=? WHERE lease_id=? AND state='active' AND daemon_instance_id=? AND owner_kind=? AND owner_id=? AND blob_sha256 IS ? AND materialization_id IS ? AND owner_pid IS ? AND process_birth_identity IS ? AND heartbeat_at_unix<=?", unix(item.HeartbeatAt), unix(item.ExpiresAt), unix(item.GraceUntil), item.ID, item.DaemonInstanceID, owner, item.OwnerID, blob, materialization, pid, birth, unix(item.HeartbeatAt))
		return requireOne("heartbeat cache lease", result, err)
	})
}

func (store *Store) ReleaseLease(ctx context.Context, item cache.Lease) error {
	if err := validateLease(item, true); err != nil {
		return fmt.Errorf("release cache lease: %w", err)
	}
	owner, err := encodeLeaseOwner(item.OwnerKind)
	if err != nil {
		return err
	}
	blob, materialization := targetValues(item.Target)
	pid, birth := processValues(item.Process)
	return store.write(ctx, 1, func(_ *sql.Conn, w *transactionWriter) error {
		result, err := w.ExecContext(ctx, "UPDATE cache_leases SET heartbeat_at_unix=?,expires_at_unix=?,grace_until_unix=?,state='released' WHERE lease_id=? AND state='active' AND daemon_instance_id=? AND owner_kind=? AND owner_id=? AND blob_sha256 IS ? AND materialization_id IS ? AND owner_pid IS ? AND process_birth_identity IS ? AND heartbeat_at_unix<=?", unix(item.ReleasedAt), unix(maxTime(item.ExpiresAt, item.ReleasedAt)), unix(maxTime(item.GraceUntil, maxTime(item.ExpiresAt, item.ReleasedAt))), item.ID, item.DaemonInstanceID, owner, item.OwnerID, blob, materialization, pid, birth, unix(item.ReleasedAt))
		return requireOne("release cache lease", result, err)
	})
}

func (store *Store) ListLeases(ctx context.Context) ([]cache.Lease, error) {
	return listLeases(ctx, store.database)
}

func listLeases(ctx context.Context, source queryer) ([]cache.Lease, error) {
	rows, err := source.QueryContext(ctx, leaseSelect+" ORDER BY lease_id")
	if err != nil {
		return nil, fmt.Errorf("list cache leases: %w", err)
	}
	defer rows.Close()
	var result []cache.Lease
	for rows.Next() {
		item, err := scanLease(rows)
		if err != nil {
			return nil, err
		}
		result = append(result, item)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("list cache leases: %w", err)
	}
	return result, nil
}

func (store *Store) LoadSnapshot(ctx context.Context) (result cache.Snapshot, returnErr error) {
	if store == nil || store.database == nil {
		return cache.Snapshot{}, fmt.Errorf("load cache snapshot: nil database")
	}
	connection, err := store.database.Conn(ctx)
	if err != nil {
		return cache.Snapshot{}, fmt.Errorf("load cache snapshot: reserve connection: %w", err)
	}
	defer func() { returnErr = errors.Join(returnErr, connection.Close()) }()
	if _, err := connection.ExecContext(ctx, "BEGIN"); err != nil {
		return cache.Snapshot{}, fmt.Errorf("load cache snapshot: begin read transaction: %w", err)
	}
	committed := false
	defer func() {
		if !committed {
			_, rollbackErr := connection.ExecContext(context.Background(), "ROLLBACK")
			returnErr = errors.Join(returnErr, rollbackErr)
		}
	}()
	blobs, err := listBlobs(ctx, connection)
	if err != nil {
		return cache.Snapshot{}, err
	}
	entries, err := listEntries(ctx, connection)
	if err != nil {
		return cache.Snapshot{}, err
	}
	materializations, err := listMaterializations(ctx, connection)
	if err != nil {
		return cache.Snapshot{}, err
	}
	references, err := listReferences(ctx, connection)
	if err != nil {
		return cache.Snapshot{}, err
	}
	leases, err := listLeases(ctx, connection)
	if err != nil {
		return cache.Snapshot{}, err
	}
	result = cache.Snapshot{Blobs: blobs, Entries: entries, Materializations: materializations, References: references, Leases: leases}
	if err := result.Validate(); err != nil {
		return cache.Snapshot{}, fmt.Errorf("load cache snapshot: %w", err)
	}
	if _, err := connection.ExecContext(ctx, "COMMIT"); err != nil {
		return cache.Snapshot{}, fmt.Errorf("load cache snapshot: commit read transaction: %w", err)
	}
	committed = true
	return result, nil
}

type queryer interface {
	QueryContext(context.Context, string, ...any) (*sql.Rows, error)
}

func (store *Store) updateOne(ctx context.Context, label, query string, args ...any) error {
	return store.write(ctx, 1, func(_ *sql.Conn, w *transactionWriter) error {
		result, err := w.ExecContext(ctx, query, args...)
		return requireOne(label, result, err)
	})
}

func requireOne(label string, result sql.Result, err error) error {
	if err != nil {
		return fmt.Errorf("%s: %w", label, err)
	}
	count, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("%s: count rows: %w", label, err)
	}
	if count != 1 {
		return fmt.Errorf("%s: row not found or state changed", label)
	}
	return nil
}

type transactionWriter struct {
	connection  *sql.Conn
	transaction *wal.FileTransaction
	next        int
}

func (w *transactionWriter) ExecContext(ctx context.Context, query string, args ...any) (sql.Result, error) {
	result, execErr := w.connection.ExecContext(ctx, query, args...)
	observeErr := w.transaction.AfterStatement(w.next)
	w.next++
	return result, errors.Join(execErr, observeErr)
}

func (store *Store) write(ctx context.Context, statements int, operation func(*sql.Conn, *transactionWriter) error) (returnErr error) {
	if store == nil || store.database == nil || store.walGuard == nil {
		return fmt.Errorf("cache store: nil database")
	}
	connection, err := store.database.Conn(ctx)
	if err != nil {
		return fmt.Errorf("cache store: reserve connection: %w", err)
	}
	defer func() { returnErr = errors.Join(returnErr, connection.Close()) }()
	budgets := make([]uint64, statements)
	for i := range budgets {
		budgets[i] = statementWALBudget
	}
	transaction, err := store.walGuard.Begin(budgets)
	if err != nil {
		return fmt.Errorf("cache store: WAL preflight: %w", err)
	}
	if _, err := connection.ExecContext(ctx, "BEGIN IMMEDIATE"); err != nil {
		return fmt.Errorf("cache store: begin immediate: %w", errors.Join(err, transaction.AfterRollback()))
	}
	writer := &transactionWriter{connection: connection, transaction: transaction}
	if err := operation(connection, writer); err != nil {
		_, rollbackErr := connection.ExecContext(context.Background(), "ROLLBACK")
		return errors.Join(err, rollbackErr, transaction.AfterRollback())
	}
	if writer.next != statements {
		_, rollbackErr := connection.ExecContext(context.Background(), "ROLLBACK")
		return errors.Join(fmt.Errorf("cache store: observed %d statements, want %d", writer.next, statements), rollbackErr, transaction.AfterRollback())
	}
	if err := transaction.BeforeCommit(); err != nil {
		_, rollbackErr := connection.ExecContext(context.Background(), "ROLLBACK")
		return errors.Join(err, rollbackErr, transaction.AfterRollback())
	}
	if _, err := connection.ExecContext(ctx, "COMMIT"); err != nil {
		_, rollbackErr := connection.ExecContext(context.Background(), "ROLLBACK")
		return fmt.Errorf("cache store: commit: %w", errors.Join(err, rollbackErr, transaction.AfterRollback()))
	}
	if err := transaction.AfterCommit(); err != nil {
		return fmt.Errorf("cache store: committed WAL observation: %w", err)
	}
	if _, err := store.walGuard.PassiveCheckpoint(ctx, connection); err != nil {
		return fmt.Errorf("cache store: committed passive checkpoint: %w", err)
	}
	return nil
}

func validateBlob(item cache.Blob) error {
	if err := item.Validate(); err != nil {
		return err
	}
	if err := validateTime("blob creation", item.CreatedAt); err != nil {
		return err
	}
	return validateTime("blob access", item.LastAccessAt)
}
func validateEntry(item cache.Entry) error {
	if err := item.Validate(); err != nil {
		return err
	}
	if len(item.EndpointID) > 255 {
		return fmt.Errorf("cache entry endpoint ID exceeds Version 2 bound")
	}
	if len(item.CanonicalPath) > 4096 {
		return fmt.Errorf("cache entry path exceeds Version 2 bound")
	}
	if len(item.WorkspaceID) > 128 {
		return fmt.Errorf("cache entry workspace ID exceeds Version 2 bound")
	}
	if err := validateTime("entry creation", item.CreatedAt); err != nil {
		return err
	}
	return validateTime("entry access", item.LastAccessAt)
}
func validateMaterialization(item cache.Materialization) error {
	if err := item.Validate(); err != nil {
		return err
	}
	if err := validateTime("materialization creation", item.CreatedAt); err != nil {
		return err
	}
	return validateTime("materialization access", item.LastAccessAt)
}
func validateReference(item cache.Reference) error {
	if item.OwnerKind == cache.ReferenceOwnerStage2Part {
		return fmt.Errorf("cache reference owner kind %q is not persistable in Version 2", item.OwnerKind)
	}
	if err := item.Validate(); err != nil {
		return err
	}
	if len(item.OwnerID) > 128 {
		return fmt.Errorf("cache reference owner ID exceeds Version 2 bound")
	}
	return validateTime("reference creation", item.CreatedAt)
}
func validateLease(item cache.Lease, released bool) error {
	if err := item.Validate(); err != nil {
		return err
	}
	if released != (item.State == cache.LeaseReleased) {
		return fmt.Errorf("cache lease state does not match operation")
	}
	if len(item.OwnerID) > 128 {
		return fmt.Errorf("cache lease owner ID exceeds Version 2 bound")
	}
	if !lowerHex(item.DaemonInstanceID, 32) {
		return fmt.Errorf("cache lease daemon instance ID must be 32 lowercase hex characters")
	}
	if item.Process != nil && len(item.Process.BirthID) > 128 {
		return fmt.Errorf("cache lease process birth identity exceeds Version 2 bound")
	}
	for label, value := range map[string]time.Time{"heartbeat": item.HeartbeatAt, "expiry": item.ExpiresAt, "grace": item.GraceUntil} {
		if err := validateTime("lease "+label, value); err != nil {
			return err
		}
	}
	if released {
		return validateTime("lease release", item.ReleasedAt)
	}
	return nil
}
func validateTime(label string, value time.Time) error {
	if value.IsZero() || value.Unix() <= 0 {
		return fmt.Errorf("validate %s: time must be after Unix epoch", label)
	}
	if value.Nanosecond() != 0 {
		return fmt.Errorf("validate %s: Version 2 stores whole seconds", label)
	}
	return nil
}
func encodeFingerprint(value cache.Fingerprint) (string, string, string) {
	return string(value.Strength), fingerprintCodec, hex.EncodeToString(value.Canonical)
}
func blobBasename(id cache.BlobID) string {
	return "blobs/sha256/" + string(id[:2]) + "/" + string(id) + ".blob"
}
func materializationBasename(id cache.MaterializationID) string {
	return "materializations/" + string(id) + "/content"
}
func boolInt(value bool) int {
	if value {
		return 1
	}
	return 0
}
func unix(value time.Time) int64 { return value.Unix() }
func targetValues(value cache.Target) (any, any) {
	if value.BlobID != "" {
		return value.BlobID, nil
	}
	return nil, value.MaterializationID
}
func processValues(value *cache.ProcessIdentity) (any, any) {
	if value == nil {
		return nil, nil
	}
	return value.PID, value.BirthID
}
func maxTime(a, b time.Time) time.Time {
	if a.Before(b) {
		return b
	}
	return a
}
func lowerHex(value string, width int) bool {
	if len(value) != width {
		return false
	}
	for _, c := range value {
		if !strings.ContainsRune("0123456789abcdef", c) {
			return false
		}
	}
	return true
}

func encodeLeaseOwner(value cache.LeaseOwnerKind) (string, error) {
	switch value {
	case cache.LeaseOwnerPreview:
		return "preview", nil
	case cache.LeaseOwnerEditor:
		return "edit", nil
	case cache.LeaseOwnerOpener:
		return "open", nil
	case cache.LeaseOwnerUpload:
		return "upload", nil
	default:
		return "", fmt.Errorf("cache lease owner kind %q is not persistable in Version 2", value)
	}
}
