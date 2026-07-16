package cache

import (
	"fmt"
	"time"
)

type WorkspaceID string

type Policy string

const (
	PolicyLRU           Policy = "lru"
	PolicyEphemeral     Policy = "ephemeral"
	PolicyPinnedOffline Policy = "pinned_offline"
)

type BlobState string

const (
	BlobPublished BlobState = "published"
	BlobDeleting  BlobState = "deleting"
)

type FingerprintStrength string

const (
	FingerprintWeak   FingerprintStrength = "weak"
	FingerprintStrong FingerprintStrength = "strong"
)

type EntryFreshness string

const (
	EntryFresh   EntryFreshness = "fresh"
	EntryStale   EntryFreshness = "stale"
	EntryUnknown EntryFreshness = "unknown"
)

type MaterializationState string

const (
	MaterializationClean    MaterializationState = "clean"
	MaterializationDirty    MaterializationState = "dirty"
	MaterializationOrphaned MaterializationState = "orphaned"
	MaterializationDeleting MaterializationState = "deleting"
)

type ReferenceOwnerKind string

const (
	ReferenceOwnerWorkspace  ReferenceOwnerKind = "workspace"
	ReferenceOwnerPreview    ReferenceOwnerKind = "preview"
	ReferenceOwnerEdit       ReferenceOwnerKind = "edit"
	ReferenceOwnerOpen       ReferenceOwnerKind = "open"
	ReferenceOwnerUpload     ReferenceOwnerKind = "upload"
	ReferenceOwnerStage2Part ReferenceOwnerKind = "stage2_part"
)

type LeaseOwnerKind string

const (
	LeaseOwnerPreview LeaseOwnerKind = "preview"
	LeaseOwnerEditor  LeaseOwnerKind = "editor"
	LeaseOwnerOpener  LeaseOwnerKind = "opener"
	LeaseOwnerUpload  LeaseOwnerKind = "upload"
)

type LeaseState string

const (
	LeaseActive   LeaseState = "active"
	LeaseReleased LeaseState = "released"
)

type Fingerprint struct {
	Strength  FingerprintStrength
	Canonical []byte
}

type Blob struct {
	ID           BlobID
	Size         int64
	State        BlobState
	CreatedAt    time.Time
	LastAccessAt time.Time
}

type Entry struct {
	ID            EntryID
	EndpointID    string
	CanonicalPath []byte
	Fingerprint   Fingerprint
	Freshness     EntryFreshness
	Policy        Policy
	WorkspaceID   WorkspaceID
	Pinned        bool
	BlobID        BlobID
	CreatedAt     time.Time
	LastAccessAt  time.Time
}

type Materialization struct {
	ID             MaterializationID
	EntryID        EntryID
	BaselineBlobID BlobID
	CurrentBlobID  BlobID
	Size           int64
	State          MaterializationState
	Pinned         bool
	CreatedAt      time.Time
	LastAccessAt   time.Time
}

type Target struct {
	BlobID            BlobID
	MaterializationID MaterializationID
}

func BlobTarget(id BlobID) Target {
	return Target{BlobID: id}
}

func MaterializationTarget(id MaterializationID) Target {
	return Target{MaterializationID: id}
}

type Reference struct {
	ID        ReferenceID
	OwnerKind ReferenceOwnerKind
	OwnerID   string
	Target    Target
	CreatedAt time.Time
}

type ProcessIdentity struct {
	PID     int
	BirthID string
}

type Lease struct {
	ID               LeaseID
	OwnerKind        LeaseOwnerKind
	OwnerID          string
	DaemonInstanceID string
	Target           Target
	State            LeaseState
	HeartbeatAt      time.Time
	ExpiresAt        time.Time
	GraceUntil       time.Time
	ReleasedAt       time.Time
	Process          *ProcessIdentity
}

type Snapshot struct {
	Blobs            []Blob
	Entries          []Entry
	Materializations []Materialization
	References       []Reference
	Leases           []Lease
}

func (fingerprint Fingerprint) Validate() error {
	switch fingerprint.Strength {
	case FingerprintWeak, FingerprintStrong:
	default:
		return fmt.Errorf("validate cache fingerprint: unknown strength %q", fingerprint.Strength)
	}
	if len(fingerprint.Canonical) == 0 || len(fingerprint.Canonical) > maxCanonicalFingerprint {
		return fmt.Errorf("validate cache fingerprint: canonical bytes length must be in [1,%d]", maxCanonicalFingerprint)
	}
	return nil
}

func (blob Blob) Validate() error {
	if _, err := ParseBlobID(string(blob.ID)); err != nil {
		return err
	}
	if blob.Size < 0 {
		return fmt.Errorf("validate cache blob %q: negative size", blob.ID)
	}
	switch blob.State {
	case BlobPublished, BlobDeleting:
	default:
		return fmt.Errorf("validate cache blob %q: unknown state %q", blob.ID, blob.State)
	}
	return validateAccessTimes("blob", string(blob.ID), blob.CreatedAt, blob.LastAccessAt)
}

func (entry Entry) Validate() error {
	if _, err := ParseEntryID(string(entry.ID)); err != nil {
		return err
	}
	if entry.EndpointID == "" || len(entry.EndpointID) > maxEndpointIDBytes {
		return fmt.Errorf("validate cache entry %q: invalid endpoint ID length", entry.ID)
	}
	if len(entry.CanonicalPath) == 0 || len(entry.CanonicalPath) > maxCanonicalPathBytes {
		return fmt.Errorf("validate cache entry %q: invalid canonical path length", entry.ID)
	}
	if err := entry.Fingerprint.Validate(); err != nil {
		return fmt.Errorf("validate cache entry %q: %w", entry.ID, err)
	}
	switch entry.Freshness {
	case EntryFresh, EntryStale, EntryUnknown:
	default:
		return fmt.Errorf("validate cache entry %q: unknown freshness %q", entry.ID, entry.Freshness)
	}
	switch entry.Policy {
	case PolicyLRU, PolicyEphemeral, PolicyPinnedOffline:
	default:
		return fmt.Errorf("validate cache entry %q: unknown policy %q", entry.ID, entry.Policy)
	}
	if entry.Policy == PolicyPinnedOffline && !entry.Pinned {
		return fmt.Errorf("validate cache entry %q: pinned_offline policy requires pinned state", entry.ID)
	}
	if entry.WorkspaceID == "" {
		return fmt.Errorf("validate cache entry %q: empty workspace ID", entry.ID)
	}
	if _, err := ParseBlobID(string(entry.BlobID)); err != nil {
		return fmt.Errorf("validate cache entry %q blob: %w", entry.ID, err)
	}
	return validateAccessTimes("entry", string(entry.ID), entry.CreatedAt, entry.LastAccessAt)
}

func (materialization Materialization) Validate() error {
	if _, err := ParseMaterializationID(string(materialization.ID)); err != nil {
		return err
	}
	if _, err := ParseEntryID(string(materialization.EntryID)); err != nil {
		return fmt.Errorf("validate cache materialization %q entry: %w", materialization.ID, err)
	}
	if _, err := ParseBlobID(string(materialization.BaselineBlobID)); err != nil {
		return fmt.Errorf("validate cache materialization %q baseline: %w", materialization.ID, err)
	}
	if _, err := ParseBlobID(string(materialization.CurrentBlobID)); err != nil {
		return fmt.Errorf("validate cache materialization %q current digest: %w", materialization.ID, err)
	}
	if materialization.Size < 0 {
		return fmt.Errorf("validate cache materialization %q: negative size", materialization.ID)
	}
	switch materialization.State {
	case MaterializationClean, MaterializationDirty, MaterializationOrphaned, MaterializationDeleting:
	default:
		return fmt.Errorf("validate cache materialization %q: unknown state %q", materialization.ID, materialization.State)
	}
	return validateAccessTimes("materialization", string(materialization.ID), materialization.CreatedAt, materialization.LastAccessAt)
}

func (target Target) Validate() error {
	hasBlob := target.BlobID != ""
	hasMaterialization := target.MaterializationID != ""
	if hasBlob == hasMaterialization {
		return fmt.Errorf("validate cache target: exactly one blob or materialization ID is required")
	}
	if hasBlob {
		_, err := ParseBlobID(string(target.BlobID))
		return err
	}
	_, err := ParseMaterializationID(string(target.MaterializationID))
	return err
}

func (reference Reference) Validate() error {
	if _, err := ParseReferenceID(string(reference.ID)); err != nil {
		return err
	}
	switch reference.OwnerKind {
	case ReferenceOwnerWorkspace, ReferenceOwnerPreview, ReferenceOwnerEdit, ReferenceOwnerOpen,
		ReferenceOwnerUpload, ReferenceOwnerStage2Part:
	default:
		return fmt.Errorf("validate cache reference %q: unknown owner kind %q", reference.ID, reference.OwnerKind)
	}
	if reference.OwnerID == "" {
		return fmt.Errorf("validate cache reference %q: empty owner ID", reference.ID)
	}
	if err := reference.Target.Validate(); err != nil {
		return fmt.Errorf("validate cache reference %q: %w", reference.ID, err)
	}
	if reference.CreatedAt.IsZero() {
		return fmt.Errorf("validate cache reference %q: zero creation time", reference.ID)
	}
	return nil
}

func (identity ProcessIdentity) Validate() error {
	if identity.PID <= 0 {
		return fmt.Errorf("validate cache process identity: PID must be positive")
	}
	if identity.BirthID == "" {
		return fmt.Errorf("validate cache process identity: empty birth identity")
	}
	return nil
}

func (lease Lease) Validate() error {
	if _, err := ParseLeaseID(string(lease.ID)); err != nil {
		return err
	}
	switch lease.OwnerKind {
	case LeaseOwnerPreview, LeaseOwnerEditor, LeaseOwnerOpener, LeaseOwnerUpload:
	default:
		return fmt.Errorf("validate cache lease %q: unknown owner kind %q", lease.ID, lease.OwnerKind)
	}
	if lease.OwnerID == "" || lease.DaemonInstanceID == "" {
		return fmt.Errorf("validate cache lease %q: owner and daemon instance IDs are required", lease.ID)
	}
	if err := lease.Target.Validate(); err != nil {
		return fmt.Errorf("validate cache lease %q: %w", lease.ID, err)
	}
	switch lease.State {
	case LeaseActive:
		if !lease.ReleasedAt.IsZero() {
			return fmt.Errorf("validate cache lease %q: active lease has release time", lease.ID)
		}
	case LeaseReleased:
		if lease.ReleasedAt.IsZero() {
			return fmt.Errorf("validate cache lease %q: released lease has no release time", lease.ID)
		}
	default:
		return fmt.Errorf("validate cache lease %q: unknown state %q", lease.ID, lease.State)
	}
	if lease.HeartbeatAt.IsZero() || lease.ExpiresAt.Before(lease.HeartbeatAt) || lease.GraceUntil.Before(lease.ExpiresAt) {
		return fmt.Errorf("validate cache lease %q: invalid heartbeat, expiry, or grace ordering", lease.ID)
	}
	if lease.Process != nil {
		if err := lease.Process.Validate(); err != nil {
			return fmt.Errorf("validate cache lease %q: %w", lease.ID, err)
		}
	}
	return nil
}

func (snapshot Snapshot) Validate() error {
	blobs := make(map[BlobID]struct{}, len(snapshot.Blobs))
	entries := make(map[EntryID]struct{}, len(snapshot.Entries))
	materializations := make(map[MaterializationID]struct{}, len(snapshot.Materializations))
	references := make(map[ReferenceID]struct{}, len(snapshot.References))
	leases := make(map[LeaseID]struct{}, len(snapshot.Leases))

	for _, blob := range snapshot.Blobs {
		if err := blob.Validate(); err != nil {
			return err
		}
		if _, exists := blobs[blob.ID]; exists {
			return fmt.Errorf("validate cache snapshot: duplicate blob %q", blob.ID)
		}
		blobs[blob.ID] = struct{}{}
	}
	for _, entry := range snapshot.Entries {
		if err := entry.Validate(); err != nil {
			return err
		}
		if _, exists := entries[entry.ID]; exists {
			return fmt.Errorf("validate cache snapshot: duplicate entry %q", entry.ID)
		}
		if _, exists := blobs[entry.BlobID]; !exists {
			return fmt.Errorf("validate cache snapshot: entry %q references missing blob %q", entry.ID, entry.BlobID)
		}
		entries[entry.ID] = struct{}{}
	}
	for _, materialization := range snapshot.Materializations {
		if err := materialization.Validate(); err != nil {
			return err
		}
		if _, exists := materializations[materialization.ID]; exists {
			return fmt.Errorf("validate cache snapshot: duplicate materialization %q", materialization.ID)
		}
		if _, exists := entries[materialization.EntryID]; !exists {
			return fmt.Errorf("validate cache snapshot: materialization %q references missing entry %q", materialization.ID, materialization.EntryID)
		}
		if _, exists := blobs[materialization.BaselineBlobID]; !exists {
			return fmt.Errorf("validate cache snapshot: materialization %q references missing baseline blob %q", materialization.ID, materialization.BaselineBlobID)
		}
		materializations[materialization.ID] = struct{}{}
	}
	for _, reference := range snapshot.References {
		if err := reference.Validate(); err != nil {
			return err
		}
		if _, exists := references[reference.ID]; exists {
			return fmt.Errorf("validate cache snapshot: duplicate reference %q", reference.ID)
		}
		if !targetExists(reference.Target, blobs, materializations) {
			return fmt.Errorf("validate cache snapshot: reference %q has missing target", reference.ID)
		}
		references[reference.ID] = struct{}{}
	}
	for _, lease := range snapshot.Leases {
		if err := lease.Validate(); err != nil {
			return err
		}
		if _, exists := leases[lease.ID]; exists {
			return fmt.Errorf("validate cache snapshot: duplicate lease %q", lease.ID)
		}
		if !targetExists(lease.Target, blobs, materializations) {
			return fmt.Errorf("validate cache snapshot: lease %q has missing target", lease.ID)
		}
		leases[lease.ID] = struct{}{}
	}
	return nil
}

func validateAccessTimes(kind string, id string, createdAt time.Time, lastAccessAt time.Time) error {
	if createdAt.IsZero() || lastAccessAt.IsZero() || lastAccessAt.Before(createdAt) {
		return fmt.Errorf("validate cache %s %q: invalid creation or access time", kind, id)
	}
	return nil
}

func targetExists(target Target, blobs map[BlobID]struct{}, materializations map[MaterializationID]struct{}) bool {
	if target.BlobID != "" {
		_, exists := blobs[target.BlobID]
		return exists
	}
	_, exists := materializations[target.MaterializationID]
	return exists
}
