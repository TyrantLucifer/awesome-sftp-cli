package cachestore

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"time"

	"github.com/TyrantLucifer/awesome-mac-sftp/internal/cache"
)

var ErrEvictionProtected = errors.New("cache eviction target is protected or changed")

type MarkDirtyRequest struct {
	MaterializationID cache.MaterializationID
	ReferenceID       cache.ReferenceID
	LeaseID           cache.LeaseID
	OwnerKind         cache.LeaseOwnerKind
	OwnerID           string
	CurrentBlobID     cache.BlobID
	Size              int64
	ObservedAt        time.Time
}

// MarkMaterializationDirty atomically verifies the exact active handoff before
// recording daemon-observed content identity. Client-provided hashes never
// enter this API.
func (store *Store) MarkMaterializationDirty(ctx context.Context, request MarkDirtyRequest) (cache.Materialization, error) {
	if _, err := cache.ParseMaterializationID(string(request.MaterializationID)); err != nil {
		return cache.Materialization{}, fmt.Errorf("mark cache materialization dirty: %w", err)
	}
	if _, err := cache.ParseReferenceID(string(request.ReferenceID)); err != nil {
		return cache.Materialization{}, fmt.Errorf("mark cache materialization dirty: %w", err)
	}
	if _, err := cache.ParseLeaseID(string(request.LeaseID)); err != nil {
		return cache.Materialization{}, fmt.Errorf("mark cache materialization dirty: %w", err)
	}
	if _, err := cache.ParseBlobID(string(request.CurrentBlobID)); err != nil || request.Size < 0 {
		return cache.Materialization{}, fmt.Errorf("mark cache materialization dirty: invalid observed content identity")
	}
	owner, err := encodeLeaseOwner(request.OwnerKind)
	if err != nil || request.OwnerID == "" || len(request.OwnerID) > 128 || validateTime("materialization observation", request.ObservedAt) != nil {
		return cache.Materialization{}, fmt.Errorf("mark cache materialization dirty: invalid owner or observation time")
	}
	var result cache.Materialization
	err = store.write(ctx, 1, func(connection *sql.Conn, writer *transactionWriter) error {
		row := connection.QueryRowContext(ctx, "SELECT m.materialization_id,m.entry_id,m.baseline_blob_sha256,m.basename,m.size_bytes,m.current_sha256,m.state,m.pinned,m.created_at_unix,m.updated_at_unix,m.last_access_unix FROM cache_materializations AS m JOIN cache_leases AS l ON l.materialization_id=m.materialization_id JOIN cache_references AS r ON r.materialization_id=m.materialization_id WHERE m.materialization_id=? AND m.state IN ('clean','dirty') AND l.lease_id=? AND l.owner_kind=? AND l.owner_id=? AND l.state='active' AND r.reference_id=? AND r.owner_kind=? AND r.owner_id=?", request.MaterializationID, request.LeaseID, owner, request.OwnerID, request.ReferenceID, owner, request.OwnerID)
		stored, scanErr := scanMaterialization(row)
		if scanErr != nil {
			return fmt.Errorf("mark cache materialization dirty handoff: %w", scanErr)
		}
		result = stored
		result.CurrentBlobID = request.CurrentBlobID
		result.Size = request.Size
		result.State = cache.MaterializationDirty
		result.LastAccessAt = maxTime(result.LastAccessAt, request.ObservedAt)
		updated := unix(result.LastAccessAt)
		statement, execErr := writer.ExecContext(ctx, "UPDATE cache_materializations SET size_bytes=?,current_sha256=?,state='dirty',updated_at_unix=max(updated_at_unix,?),last_access_unix=max(last_access_unix,?) WHERE materialization_id=? AND state IN ('clean','dirty')", result.Size, result.CurrentBlobID, updated, updated, result.ID)
		return requireOne("mark cache materialization dirty", statement, execErr)
	})
	if err != nil {
		return cache.Materialization{}, err
	}
	return result, nil
}

type EvictionClaim struct {
	Target                 cache.EvictionTarget
	EntryID                cache.EntryID
	BlobID                 cache.BlobID
	BlobSize               int64
	MaterializationBlobID  cache.BlobID
	MaterializationSize    int64
	SharedEntryReferenceID cache.ReferenceID
}

const evictionEntryOwnerPrefix = "__amsftp_evict_entry__:"

func (store *Store) BeginEviction(ctx context.Context, target cache.EvictionTarget, at time.Time) (EvictionClaim, error) {
	if err := validateTime("eviction claim", at); err != nil {
		return EvictionClaim{}, err
	}
	switch {
	case target.MaterializationID != "" && target.EntryID == "" && target.BlobID == "":
		if _, err := cache.ParseMaterializationID(string(target.MaterializationID)); err != nil {
			return EvictionClaim{}, err
		}
		claim := EvictionClaim{Target: target}
		err := store.write(ctx, 1, func(connection *sql.Conn, writer *transactionWriter) error {
			if err := connection.QueryRowContext(ctx, "SELECT current_sha256,size_bytes FROM cache_materializations WHERE materialization_id=? AND state='clean'", target.MaterializationID).Scan(&claim.MaterializationBlobID, &claim.MaterializationSize); err != nil {
				return errors.Join(ErrEvictionProtected, err)
			}
			result, execErr := writer.ExecContext(ctx, "UPDATE cache_materializations SET state='deleting',updated_at_unix=max(updated_at_unix,?) WHERE materialization_id=? AND state='clean' AND pinned=0 AND NOT EXISTS(SELECT 1 FROM cache_references WHERE materialization_id=?) AND NOT EXISTS(SELECT 1 FROM cache_leases WHERE materialization_id=? AND state<>'released') AND NOT EXISTS(SELECT 1 FROM edit_sessions WHERE materialization_id=?)", unix(at), target.MaterializationID, target.MaterializationID, target.MaterializationID, target.MaterializationID)
			if execErr != nil {
				return execErr
			}
			count, countErr := result.RowsAffected()
			if countErr != nil {
				return countErr
			}
			if count != 1 {
				return ErrEvictionProtected
			}
			return nil
		})
		return claim, err
	case target.EntryID != "" && target.MaterializationID == "" && target.BlobID == "":
		if _, err := cache.ParseEntryID(string(target.EntryID)); err != nil {
			return EvictionClaim{}, err
		}
		claim := EvictionClaim{Target: target, EntryID: target.EntryID}
		err := store.write(ctx, 1, func(connection *sql.Conn, writer *transactionWriter) error {
			var entries int
			if scanErr := connection.QueryRowContext(ctx, "SELECT e.blob_sha256,b.size_bytes,(SELECT count(*) FROM cache_entries WHERE blob_sha256=e.blob_sha256) FROM cache_entries AS e JOIN cache_blobs AS b ON b.sha256=e.blob_sha256 WHERE e.entry_id=? AND e.pinned=0 AND b.state='published' AND NOT EXISTS(SELECT 1 FROM cache_materializations WHERE entry_id=e.entry_id) AND NOT EXISTS(SELECT 1 FROM cache_references WHERE blob_sha256=e.blob_sha256) AND NOT EXISTS(SELECT 1 FROM cache_leases WHERE blob_sha256=e.blob_sha256 AND state<>'released')", target.EntryID).Scan(&claim.BlobID, &claim.BlobSize, &entries); scanErr != nil {
				return errors.Join(ErrEvictionProtected, scanErr)
			}
			if entries == 1 {
				result, execErr := writer.ExecContext(ctx, "UPDATE cache_blobs SET state='deleting' WHERE sha256=? AND state='published' AND (SELECT count(*) FROM cache_entries WHERE blob_sha256=?)=1 AND EXISTS(SELECT 1 FROM cache_entries WHERE entry_id=? AND blob_sha256=? AND pinned=0) AND NOT EXISTS(SELECT 1 FROM cache_materializations WHERE entry_id=? OR baseline_blob_sha256=?) AND NOT EXISTS(SELECT 1 FROM cache_references WHERE blob_sha256=?) AND NOT EXISTS(SELECT 1 FROM cache_leases WHERE blob_sha256=? AND state<>'released')", claim.BlobID, claim.BlobID, target.EntryID, claim.BlobID, target.EntryID, claim.BlobID, claim.BlobID, claim.BlobID)
				return requireClaim(result, execErr)
			}
			claim.SharedEntryReferenceID = evictionEntryReferenceID(target.EntryID)
			ownerID := evictionEntryOwnerPrefix + string(target.EntryID)
			result, execErr := writer.ExecContext(ctx, "INSERT INTO cache_references(reference_id,owner_kind,owner_id,blob_sha256,materialization_id,created_at_unix) SELECT ?,'workspace',?,?,NULL,? WHERE EXISTS(SELECT 1 FROM cache_entries WHERE entry_id=? AND blob_sha256=? AND pinned=0) AND (SELECT count(*) FROM cache_entries WHERE blob_sha256=?)>1 AND NOT EXISTS(SELECT 1 FROM cache_materializations WHERE entry_id=?) AND NOT EXISTS(SELECT 1 FROM cache_references WHERE blob_sha256=?) AND NOT EXISTS(SELECT 1 FROM cache_leases WHERE blob_sha256=? AND state<>'released')", claim.SharedEntryReferenceID, ownerID, claim.BlobID, unix(at), target.EntryID, claim.BlobID, claim.BlobID, target.EntryID, claim.BlobID, claim.BlobID)
			return requireClaim(result, execErr)
		})
		return claim, err
	case target.BlobID != "" && target.EntryID == "" && target.MaterializationID == "":
		if _, err := cache.ParseBlobID(string(target.BlobID)); err != nil {
			return EvictionClaim{}, err
		}
		claim := EvictionClaim{Target: target, BlobID: target.BlobID}
		err := store.write(ctx, 1, func(connection *sql.Conn, writer *transactionWriter) error {
			if err := connection.QueryRowContext(ctx, "SELECT size_bytes FROM cache_blobs WHERE sha256=? AND state='published'", target.BlobID).Scan(&claim.BlobSize); err != nil {
				return errors.Join(ErrEvictionProtected, err)
			}
			result, execErr := writer.ExecContext(ctx, "UPDATE cache_blobs SET state='deleting' WHERE sha256=? AND state='published' AND NOT EXISTS(SELECT 1 FROM cache_entries WHERE blob_sha256=?) AND NOT EXISTS(SELECT 1 FROM cache_materializations WHERE baseline_blob_sha256=?) AND NOT EXISTS(SELECT 1 FROM cache_references WHERE blob_sha256=?) AND NOT EXISTS(SELECT 1 FROM cache_leases WHERE blob_sha256=? AND state<>'released')", target.BlobID, target.BlobID, target.BlobID, target.BlobID, target.BlobID)
			return requireClaim(result, execErr)
		})
		return claim, err
	default:
		return EvictionClaim{}, fmt.Errorf("begin cache eviction: exactly one target is required")
	}
}

func requireClaim(result sql.Result, err error) error {
	if err != nil {
		return err
	}
	count, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if count != 1 {
		return ErrEvictionProtected
	}
	return nil
}

func evictionEntryReferenceID(entryID cache.EntryID) cache.ReferenceID {
	digest := sha256.Sum256([]byte("cache-entry-eviction-v1:" + string(entryID)))
	return cache.ReferenceID(hex.EncodeToString(digest[:16]))
}

func (store *Store) ListPendingEvictions(ctx context.Context, limit int) ([]EvictionClaim, error) {
	if limit <= 0 || limit > cache.DefaultMaxCandidates {
		return nil, fmt.Errorf("list pending cache evictions: limit must be in [1,%d]", cache.DefaultMaxCandidates)
	}
	result := make([]EvictionClaim, 0, limit)
	rows, err := store.database.QueryContext(ctx, "SELECT materialization_id,current_sha256,size_bytes FROM cache_materializations WHERE state='deleting' ORDER BY materialization_id LIMIT ?", limit)
	if err != nil {
		return nil, fmt.Errorf("list pending cache materializations: %w", err)
	}
	for rows.Next() {
		var id cache.MaterializationID
		var blobID cache.BlobID
		var size int64
		if err := rows.Scan(&id, &blobID, &size); err != nil {
			_ = rows.Close()
			return nil, err
		}
		result = append(result, EvictionClaim{Target: cache.MaterializationEviction(id), MaterializationBlobID: blobID, MaterializationSize: size})
	}
	if err := rows.Close(); err != nil {
		return nil, err
	}
	if len(result) == limit {
		return result, nil
	}
	rows, err = store.database.QueryContext(ctx, "SELECT b.sha256,b.size_bytes,e.entry_id FROM cache_blobs AS b LEFT JOIN cache_entries AS e ON e.blob_sha256=b.sha256 WHERE b.state='deleting' ORDER BY b.sha256 LIMIT ?", limit-len(result))
	if err != nil {
		return nil, fmt.Errorf("list pending cache blobs: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var blobID cache.BlobID
		var entryID sql.NullString
		var size int64
		if err := rows.Scan(&blobID, &size, &entryID); err != nil {
			return nil, err
		}
		claim := EvictionClaim{BlobID: blobID, BlobSize: size}
		if entryID.Valid {
			claim.EntryID = cache.EntryID(entryID.String)
			claim.Target = cache.EntryEviction(claim.EntryID)
		} else {
			claim.Target = cache.BlobEviction(blobID)
		}
		result = append(result, claim)
	}
	if err := rows.Close(); err != nil {
		return nil, err
	}
	if len(result) == limit {
		return result, nil
	}
	rows, err = store.database.QueryContext(ctx, "SELECT r.reference_id,r.owner_id,r.blob_sha256,b.size_bytes,e.entry_id FROM cache_references AS r JOIN cache_blobs AS b ON b.sha256=r.blob_sha256 JOIN cache_entries AS e ON e.blob_sha256=r.blob_sha256 AND r.owner_id=?||e.entry_id WHERE r.owner_kind='workspace' AND r.owner_id LIKE ? ORDER BY r.reference_id LIMIT ?", evictionEntryOwnerPrefix, evictionEntryOwnerPrefix+"%", limit-len(result))
	if err != nil {
		return nil, fmt.Errorf("list pending shared entry evictions: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var claim EvictionClaim
		var ownerID string
		if err := rows.Scan(&claim.SharedEntryReferenceID, &ownerID, &claim.BlobID, &claim.BlobSize, &claim.EntryID); err != nil {
			return nil, err
		}
		if ownerID != evictionEntryOwnerPrefix+string(claim.EntryID) || claim.SharedEntryReferenceID != evictionEntryReferenceID(claim.EntryID) {
			return nil, fmt.Errorf("list pending shared entry evictions: invalid durable claim")
		}
		claim.Target = cache.EntryEviction(claim.EntryID)
		result = append(result, claim)
	}
	return result, rows.Err()
}

func (store *Store) FinalizeEviction(ctx context.Context, claim EvictionClaim) error {
	switch {
	case claim.Target.MaterializationID != "" && claim.Target.EntryID == "" && claim.Target.BlobID == "" && claim.EntryID == "" && claim.BlobID == "" && claim.MaterializationBlobID != "" && claim.MaterializationSize >= 0:
		if _, err := cache.ParseMaterializationID(string(claim.Target.MaterializationID)); err != nil {
			return err
		}
		return store.write(ctx, 2, func(_ *sql.Conn, writer *transactionWriter) error {
			if _, err := writer.ExecContext(ctx, "DELETE FROM cache_leases WHERE materialization_id=? AND state='released'", claim.Target.MaterializationID); err != nil {
				return err
			}
			result, err := writer.ExecContext(ctx, "DELETE FROM cache_materializations WHERE materialization_id=? AND state='deleting' AND current_sha256=? AND size_bytes=? AND NOT EXISTS(SELECT 1 FROM cache_references WHERE materialization_id=?) AND NOT EXISTS(SELECT 1 FROM cache_leases WHERE materialization_id=?) AND NOT EXISTS(SELECT 1 FROM edit_sessions WHERE materialization_id=?)", claim.Target.MaterializationID, claim.MaterializationBlobID, claim.MaterializationSize, claim.Target.MaterializationID, claim.Target.MaterializationID, claim.Target.MaterializationID)
			return requireOne("finalize cache materialization eviction", result, err)
		})
	case claim.SharedEntryReferenceID != "" && claim.EntryID != "" && claim.BlobID != "" && claim.Target.EntryID == claim.EntryID:
		if claim.SharedEntryReferenceID != evictionEntryReferenceID(claim.EntryID) {
			return fmt.Errorf("finalize cache eviction: invalid shared entry claim")
		}
		return store.write(ctx, 2, func(_ *sql.Conn, writer *transactionWriter) error {
			entryResult, err := writer.ExecContext(ctx, "DELETE FROM cache_entries WHERE entry_id=? AND blob_sha256=? AND pinned=0 AND NOT EXISTS(SELECT 1 FROM cache_materializations WHERE entry_id=?) AND EXISTS(SELECT 1 FROM cache_references WHERE reference_id=? AND owner_kind='workspace' AND owner_id=? AND blob_sha256=?)", claim.EntryID, claim.BlobID, claim.EntryID, claim.SharedEntryReferenceID, evictionEntryOwnerPrefix+string(claim.EntryID), claim.BlobID)
			if err := requireOne("finalize shared cache entry eviction", entryResult, err); err != nil {
				return err
			}
			claimResult, err := writer.ExecContext(ctx, "DELETE FROM cache_references WHERE reference_id=? AND owner_kind='workspace' AND owner_id=? AND blob_sha256=?", claim.SharedEntryReferenceID, evictionEntryOwnerPrefix+string(claim.EntryID), claim.BlobID)
			return requireOne("finalize shared cache entry claim", claimResult, err)
		})
	case claim.BlobID != "" && claim.Target.MaterializationID == "":
		if _, err := cache.ParseBlobID(string(claim.BlobID)); err != nil {
			return err
		}
		if claim.EntryID != "" {
			if _, err := cache.ParseEntryID(string(claim.EntryID)); err != nil || claim.Target.EntryID != claim.EntryID {
				return fmt.Errorf("finalize cache eviction: invalid entry claim")
			}
		} else if claim.Target.BlobID != claim.BlobID {
			return fmt.Errorf("finalize cache eviction: invalid blob claim")
		}
		return store.write(ctx, 3, func(_ *sql.Conn, writer *transactionWriter) error {
			entryResult, err := writer.ExecContext(ctx, "DELETE FROM cache_entries WHERE entry_id=? AND blob_sha256=? AND pinned=0 AND EXISTS(SELECT 1 FROM cache_blobs WHERE sha256=? AND state='deleting') AND NOT EXISTS(SELECT 1 FROM cache_materializations WHERE entry_id=?)", claim.EntryID, claim.BlobID, claim.BlobID, claim.EntryID)
			if err != nil {
				return err
			}
			if claim.EntryID != "" {
				if err := requireOne("finalize cache entry eviction", entryResult, nil); err != nil {
					return err
				}
			}
			if _, err := writer.ExecContext(ctx, "DELETE FROM cache_leases WHERE blob_sha256=? AND state='released'", claim.BlobID); err != nil {
				return err
			}
			blobResult, err := writer.ExecContext(ctx, "DELETE FROM cache_blobs WHERE sha256=? AND size_bytes=? AND state='deleting' AND NOT EXISTS(SELECT 1 FROM cache_entries WHERE blob_sha256=?) AND NOT EXISTS(SELECT 1 FROM cache_materializations WHERE baseline_blob_sha256=?) AND NOT EXISTS(SELECT 1 FROM cache_references WHERE blob_sha256=?) AND NOT EXISTS(SELECT 1 FROM cache_leases WHERE blob_sha256=?)", claim.BlobID, claim.BlobSize, claim.BlobID, claim.BlobID, claim.BlobID, claim.BlobID)
			return requireOne("finalize cache blob eviction", blobResult, err)
		})
	default:
		return fmt.Errorf("finalize cache eviction: invalid claim")
	}
}
