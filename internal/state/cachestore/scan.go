package cachestore

import (
	"database/sql"
	"encoding/hex"
	"fmt"
	"time"

	"github.com/TyrantLucifer/awesome-sftp-cli/internal/cache"
)

const (
	blobSelect            = "SELECT sha256,size_bytes,basename,state,created_at_unix,last_access_unix FROM cache_blobs"
	entrySelect           = "SELECT entry_id,endpoint_id,path_bytes,fingerprint_strength,fingerprint_size,modified_unix_nano,modified_precision,file_id,version_id,hash_algorithm,hash_hex,freshness,policy,workspace_id,pinned,blob_sha256,complete,created_at_unix,last_access_unix FROM cache_entries"
	materializationSelect = "SELECT materialization_id,entry_id,baseline_blob_sha256,basename,size_bytes,current_sha256,state,pinned,created_at_unix,updated_at_unix,last_access_unix FROM cache_materializations"
	referenceSelect       = "SELECT reference_id,owner_kind,owner_id,blob_sha256,materialization_id,created_at_unix FROM cache_references"
	leaseSelect           = "SELECT lease_id,blob_sha256,materialization_id,owner_kind,owner_id,daemon_instance_id,owner_pid,process_birth_identity,heartbeat_at_unix,expires_at_unix,grace_until_unix,state FROM cache_leases"
)

type scanner interface{ Scan(...any) error }

func scanBlob(row scanner) (cache.Blob, error) {
	var id, basename, state string
	var size, created, access int64
	if err := row.Scan(&id, &size, &basename, &state, &created, &access); err != nil {
		return cache.Blob{}, fmt.Errorf("scan cache blob: %w", err)
	}
	item := cache.Blob{ID: cache.BlobID(id), Size: size, State: cache.BlobState(state), CreatedAt: at(created), LastAccessAt: at(access)}
	if basename != blobBasename(item.ID) {
		return cache.Blob{}, fmt.Errorf("scan cache blob %q: invalid derived basename", item.ID)
	}
	if err := validateBlob(item); err != nil {
		return cache.Blob{}, fmt.Errorf("scan cache blob: %w", err)
	}
	return item, nil
}

func scanEntry(row scanner) (cache.Entry, error) {
	var id, endpoint, strength, freshness, policy string
	var path []byte
	var fingerprintSize, modified sql.NullInt64
	var precision, fileID, versionID, algorithm, encoded, workspace, blob sql.NullString
	var pinned, complete, created, access int64
	if err := row.Scan(&id, &endpoint, &path, &strength, &fingerprintSize, &modified, &precision, &fileID, &versionID, &algorithm, &encoded, &freshness, &policy, &workspace, &pinned, &blob, &complete, &created, &access); err != nil {
		return cache.Entry{}, fmt.Errorf("scan cache entry: %w", err)
	}
	if fingerprintSize.Valid || modified.Valid || precision.Valid || fileID.Valid || versionID.Valid || !algorithm.Valid || algorithm.String != fingerprintCodec || !encoded.Valid {
		return cache.Entry{}, fmt.Errorf("scan cache entry %q: unsupported Version 2 fingerprint encoding", id)
	}
	canonical, err := hex.DecodeString(encoded.String)
	if err != nil || len(canonical) == 0 {
		return cache.Entry{}, fmt.Errorf("scan cache entry %q: invalid canonical fingerprint encoding", id)
	}
	if !workspace.Valid || !blob.Valid || complete != 1 {
		return cache.Entry{}, fmt.Errorf("scan cache entry %q: incomplete entries are unsupported", id)
	}
	item := cache.Entry{ID: cache.EntryID(id), EndpointID: endpoint, CanonicalPath: append([]byte(nil), path...), Fingerprint: cache.Fingerprint{Strength: cache.FingerprintStrength(strength), Canonical: canonical}, Freshness: cache.EntryFreshness(freshness), Policy: cache.Policy(policy), WorkspaceID: cache.WorkspaceID(workspace.String), Pinned: pinned == 1, BlobID: cache.BlobID(blob.String), CreatedAt: at(created), LastAccessAt: at(access)}
	if err := validateEntry(item); err != nil {
		return cache.Entry{}, fmt.Errorf("scan cache entry: %w", err)
	}
	return item, nil
}

func scanMaterialization(row scanner) (cache.Materialization, error) {
	var id, entry, basename, state string
	var baseline, current sql.NullString
	var size, pinned, created, updated, access int64
	if err := row.Scan(&id, &entry, &baseline, &basename, &size, &current, &state, &pinned, &created, &updated, &access); err != nil {
		return cache.Materialization{}, fmt.Errorf("scan cache materialization: %w", err)
	}
	if state == "preparing" {
		return cache.Materialization{}, fmt.Errorf("scan cache materialization %q: unsupported recovery state %q", id, state)
	}
	if !baseline.Valid || !current.Valid {
		return cache.Materialization{}, fmt.Errorf("scan cache materialization %q: unsupported absent digest", id)
	}
	item := cache.Materialization{ID: cache.MaterializationID(id), EntryID: cache.EntryID(entry), BaselineBlobID: cache.BlobID(baseline.String), CurrentBlobID: cache.BlobID(current.String), Size: size, State: cache.MaterializationState(state), Pinned: pinned == 1, CreatedAt: at(created), LastAccessAt: at(access)}
	if basename != materializationBasename(item.ID) {
		return cache.Materialization{}, fmt.Errorf("scan cache materialization %q: invalid derived basename", id)
	}
	if updated < created {
		return cache.Materialization{}, fmt.Errorf("scan cache materialization %q: invalid update time", id)
	}
	if err := validateMaterialization(item); err != nil {
		return cache.Materialization{}, fmt.Errorf("scan cache materialization: %w", err)
	}
	return item, nil
}

func scanReference(row scanner) (cache.Reference, error) {
	var id, ownerKind, ownerID string
	var blob, materialization sql.NullString
	var created int64
	if err := row.Scan(&id, &ownerKind, &ownerID, &blob, &materialization, &created); err != nil {
		return cache.Reference{}, fmt.Errorf("scan cache reference: %w", err)
	}
	target, err := decodeTarget(blob, materialization)
	if err != nil {
		return cache.Reference{}, fmt.Errorf("scan cache reference %q: %w", id, err)
	}
	item := cache.Reference{ID: cache.ReferenceID(id), OwnerKind: cache.ReferenceOwnerKind(ownerKind), OwnerID: ownerID, Target: target, CreatedAt: at(created)}
	if err := validateReference(item); err != nil {
		return cache.Reference{}, fmt.Errorf("scan cache reference: %w", err)
	}
	return item, nil
}

func scanLease(row scanner) (cache.Lease, error) {
	var id, ownerKind, ownerID, daemon, state string
	var blob, materialization, birth sql.NullString
	var pid sql.NullInt64
	var heartbeat, expiry, grace int64
	if err := row.Scan(&id, &blob, &materialization, &ownerKind, &ownerID, &daemon, &pid, &birth, &heartbeat, &expiry, &grace, &state); err != nil {
		return cache.Lease{}, fmt.Errorf("scan cache lease: %w", err)
	}
	if state == "uncertain" {
		return cache.Lease{}, fmt.Errorf("scan cache lease %q: unsupported recovery state %q", id, state)
	}
	target, err := decodeTarget(blob, materialization)
	if err != nil {
		return cache.Lease{}, fmt.Errorf("scan cache lease %q: %w", id, err)
	}
	owner, err := decodeLeaseOwner(ownerKind)
	if err != nil {
		return cache.Lease{}, fmt.Errorf("scan cache lease %q: %w", id, err)
	}
	item := cache.Lease{ID: cache.LeaseID(id), OwnerKind: owner, OwnerID: ownerID, DaemonInstanceID: daemon, Target: target, State: cache.LeaseState(state), HeartbeatAt: at(heartbeat), ExpiresAt: at(expiry), GraceUntil: at(grace)}
	if pid.Valid != birth.Valid {
		return cache.Lease{}, fmt.Errorf("scan cache lease %q: partial process identity", id)
	}
	if pid.Valid {
		item.Process = &cache.ProcessIdentity{PID: int(pid.Int64), BirthID: birth.String}
	}
	if item.State == cache.LeaseReleased {
		item.ReleasedAt = item.HeartbeatAt
	}
	if err := validateLease(item, item.State == cache.LeaseReleased); err != nil {
		return cache.Lease{}, fmt.Errorf("scan cache lease: %w", err)
	}
	return item, nil
}

func decodeTarget(blob, materialization sql.NullString) (cache.Target, error) {
	if blob.Valid == materialization.Valid {
		return cache.Target{}, fmt.Errorf("exactly one target is required")
	}
	if blob.Valid {
		return cache.BlobTarget(cache.BlobID(blob.String)), nil
	}
	return cache.MaterializationTarget(cache.MaterializationID(materialization.String)), nil
}
func decodeLeaseOwner(value string) (cache.LeaseOwnerKind, error) {
	switch value {
	case "preview":
		return cache.LeaseOwnerPreview, nil
	case "edit":
		return cache.LeaseOwnerEditor, nil
	case "open":
		return cache.LeaseOwnerOpener, nil
	case "upload":
		return cache.LeaseOwnerUpload, nil
	default:
		return "", fmt.Errorf("unsupported owner kind %q", value)
	}
}
func at(value int64) time.Time { return time.Unix(value, 0).UTC() }
