// Package cachemanager coordinates the cache filesystem and Version 2 catalog.
// It preserves the crash-safe cache ordering: durable bytes first,
// then one catalog transaction that makes an external handoff reachable and
// leased. Reconciliation is report-only and never deletes uncertain bytes.
package cachemanager

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/TyrantLucifer/awesome-sftp-cli/internal/cache"
	"github.com/TyrantLucifer/awesome-sftp-cli/internal/cachefs"
	"github.com/TyrantLucifer/awesome-sftp-cli/internal/cacheprocess"
	"github.com/TyrantLucifer/awesome-sftp-cli/internal/domain"
	"github.com/TyrantLucifer/awesome-sftp-cli/internal/ipc"
	"github.com/TyrantLucifer/awesome-sftp-cli/internal/state/cachestore"
)

type Manager struct {
	admissionMu sync.Mutex
	files       *cachefs.Store
	catalog     *cachestore.Store
	clock       cache.Clock
	daemonID    string
	limits      cache.Limits
	leaseState  *cache.LeaseManager
	processes   cache.ProcessClassifier
}

func New(files *cachefs.Store, catalog *cachestore.Store, clock cache.Clock, daemonID string, limits cache.Limits) (*Manager, error) {
	if files == nil || files.Root() == "" {
		return nil, fmt.Errorf("create cache manager: nil filesystem store")
	}
	if catalog == nil {
		return nil, fmt.Errorf("create cache manager: nil catalog store")
	}
	if clock == nil {
		return nil, fmt.Errorf("create cache manager: nil clock")
	}
	if !lowerHex(daemonID, 32) {
		return nil, fmt.Errorf("create cache manager: daemon ID must be 32 lowercase hex characters")
	}
	if _, err := cache.PlanEvictions(cache.Snapshot{}, limits, nil); err != nil {
		return nil, fmt.Errorf("create cache manager: %w", err)
	}
	processes := cacheprocess.NewClassifier()
	leases, err := cache.NewLeaseManager(clock, processes, cache.DefaultLeaseExpiry, cache.DefaultOpenerGrace)
	if err != nil {
		return nil, err
	}
	return &Manager{files: files, catalog: catalog, clock: clock, daemonID: daemonID, limits: limits, leaseState: leases, processes: processes}, nil
}

type PublishRequest struct {
	Location          domain.Location
	SourceFingerprint domain.Fingerprint
	WorkspaceID       cache.WorkspaceID
	Policy            cache.Policy
	Pinned            bool
	Source            io.Reader
	MaxBytes          int64
	ExpectedSize      *int64
}

type PublishResult struct {
	Blob         cache.Blob
	Entry        cache.Entry
	Path         string
	Deduplicated bool
}

func (manager *Manager) PublishComplete(ctx context.Context, request PublishRequest) (PublishResult, error) {
	if manager == nil || manager.files == nil || manager.catalog == nil || manager.clock == nil {
		return PublishResult{}, fmt.Errorf("publish complete cache entry: nil manager")
	}
	if ctx == nil {
		return PublishResult{}, fmt.Errorf("publish complete cache entry: nil context")
	}
	if _, err := domain.NewLocation(request.Location.EndpointID, request.Location.Path); err != nil {
		return PublishResult{}, fmt.Errorf("publish complete cache entry: %w", err)
	}
	if request.SourceFingerprint.Strength() == domain.FingerprintWeak {
		return PublishResult{}, fmt.Errorf("publish complete cache entry: weak source fingerprint")
	}
	if request.WorkspaceID == "" {
		return PublishResult{}, fmt.Errorf("publish complete cache entry: empty workspace ID")
	}
	if request.MaxBytes < 0 || request.Source == nil {
		return PublishResult{}, fmt.Errorf("publish complete cache entry: invalid source or byte limit")
	}
	if request.ExpectedSize != nil && (*request.ExpectedSize < 0 || *request.ExpectedSize > request.MaxBytes) {
		return PublishResult{}, fmt.Errorf("publish complete cache entry: invalid expected size")
	}
	var sourceSize *int64
	if request.SourceFingerprint.Size != nil {
		if *request.SourceFingerprint.Size > math.MaxInt64 || *request.SourceFingerprint.Size > uint64(request.MaxBytes) {
			return PublishResult{}, fmt.Errorf("publish complete cache entry: source fingerprint size exceeds byte limit")
		}
		value := int64(*request.SourceFingerprint.Size) //nolint:gosec // the explicit MaxInt64 bound above makes this conversion exact
		sourceSize = &value
		if request.ExpectedSize != nil && *request.ExpectedSize != value {
			return PublishResult{}, fmt.Errorf("publish complete cache entry: expected size conflicts with source fingerprint")
		}
	}
	manager.admissionMu.Lock()
	defer manager.admissionMu.Unlock()

	var expected *cachefs.BlobIdentity
	if request.ExpectedSize != nil {
		expected = &cachefs.BlobIdentity{Size: *request.ExpectedSize}
	} else if sourceSize != nil {
		expected = &cachefs.BlobIdentity{Size: *sourceSize}
	}
	publication, err := manager.files.PublishBlob(ctx, request.Source, request.MaxBytes, expected)
	if err != nil {
		return PublishResult{}, fmt.Errorf("publish complete cache entry bytes: %w", err)
	}
	if request.ExpectedSize != nil && publication.Identity.Size != *request.ExpectedSize {
		return PublishResult{}, fmt.Errorf("publish complete cache entry: expected size %d, read %d", *request.ExpectedSize, publication.Identity.Size)
	}
	if request.SourceFingerprint.Size != nil && (publication.Identity.Size < 0 || uint64(publication.Identity.Size) != *request.SourceFingerprint.Size) {
		return PublishResult{}, fmt.Errorf("publish complete cache entry: source fingerprint size %d, read %d", *request.SourceFingerprint.Size, publication.Identity.Size)
	}
	fingerprint, err := canonicalFingerprint(request.SourceFingerprint, publication.Identity)
	if err != nil {
		return PublishResult{}, err
	}
	entryID, err := cache.DeriveEntryID(string(request.Location.EndpointID), []byte(request.Location.Path), fingerprint.Canonical)
	if err != nil {
		return PublishResult{}, err
	}
	now := manager.now()
	blob := cache.Blob{ID: publication.Identity.ID, Size: publication.Identity.Size, State: cache.BlobPublished, CreatedAt: now, LastAccessAt: now}
	entry := cache.Entry{
		ID: entryID, EndpointID: string(request.Location.EndpointID), CanonicalPath: append([]byte(nil), []byte(request.Location.Path)...),
		Fingerprint: fingerprint, Freshness: cache.EntryFresh, Policy: request.Policy, WorkspaceID: request.WorkspaceID,
		Pinned: request.Pinned, BlobID: blob.ID, CreatedAt: now, LastAccessAt: now,
	}
	if err := manager.admitLocked(ctx, cache.Admission{Blob: &blob, Entry: &entry}); err != nil {
		if !publication.Deduplicated {
			err = errors.Join(err, manager.files.DeleteBlob(publication.Identity))
		}
		return PublishResult{}, fmt.Errorf("publish complete cache entry admission: %w", err)
	}
	manifest, err := manager.files.PublishEntryManifest(entryID, string(request.Location.EndpointID), []byte(request.Location.Path), fingerprint, publication.Identity)
	if err != nil {
		return PublishResult{}, fmt.Errorf("publish complete cache entry manifest: %w", err)
	}
	if err := manager.catalog.Publish(ctx, blob, entry); err != nil {
		return PublishResult{}, fmt.Errorf("publish complete cache entry catalog: %w", err)
	}
	return PublishResult{Blob: blob, Entry: entry, Path: manifest.Blob.Path, Deduplicated: publication.Deduplicated}, nil
}

type HandoffRequest struct {
	EntryID                    cache.EntryID
	MaterializationID          cache.MaterializationID
	ReferenceID                cache.ReferenceID
	LeaseID                    cache.LeaseID
	OwnerKind                  cache.LeaseOwnerKind
	OwnerID                    string
	Pinned                     bool
	Process                    *cache.ProcessIdentity
	RequireUniquePinnedOffline bool
}

type HandoffResult struct {
	Path            string
	Materialization cache.Materialization
	Reference       cache.Reference
	Lease           cache.Lease
}

type ReleaseHandoffRequest struct {
	MaterializationID cache.MaterializationID
	ReferenceID       cache.ReferenceID
	LeaseID           cache.LeaseID
	OwnerKind         cache.LeaseOwnerKind
	OwnerID           string
}

type HeartbeatHandoffRequest struct {
	MaterializationID cache.MaterializationID
	LeaseID           cache.LeaseID
	OwnerKind         cache.LeaseOwnerKind
	OwnerID           string
	Process           cache.ProcessIdentity
}

// HeartbeatHandoff renews only the exact active lease created for a live,
// classifiable process. The catalog update repeats every identity predicate so
// a concurrent release or replacement cannot be renewed accidentally.
func (manager *Manager) HeartbeatHandoff(ctx context.Context, request HeartbeatHandoffRequest) (cache.Lease, error) {
	if manager == nil || manager.catalog == nil || manager.clock == nil || manager.leaseState == nil || manager.processes == nil {
		return cache.Lease{}, fmt.Errorf("heartbeat cache handoff: nil manager")
	}
	if ctx == nil {
		return cache.Lease{}, fmt.Errorf("heartbeat cache handoff: nil context")
	}
	if _, err := cache.ParseMaterializationID(string(request.MaterializationID)); err != nil {
		return cache.Lease{}, fmt.Errorf("heartbeat cache handoff: %w", err)
	}
	if request.OwnerID == "" || len(request.OwnerID) > 128 {
		return cache.Lease{}, fmt.Errorf("heartbeat cache handoff: invalid owner ID")
	}
	if err := request.Process.Validate(); err != nil {
		return cache.Lease{}, fmt.Errorf("heartbeat cache handoff: invalid process identity: %w", err)
	}
	if manager.processes.Classify(request.Process) != cache.ProcessMatches {
		return cache.Lease{}, fmt.Errorf("heartbeat cache handoff: process identity is not live and classifiable")
	}
	lease, err := manager.catalog.GetLease(ctx, request.LeaseID)
	if err != nil {
		return cache.Lease{}, fmt.Errorf("heartbeat cache handoff lease: %w", err)
	}
	if lease.State != cache.LeaseActive || lease.OwnerKind != request.OwnerKind || lease.OwnerID != request.OwnerID || lease.Target != cache.MaterializationTarget(request.MaterializationID) || lease.Process == nil || *lease.Process != request.Process {
		return cache.Lease{}, fmt.Errorf("heartbeat cache handoff: lease identity does not match")
	}
	renewed, err := manager.leaseState.Heartbeat(lease)
	if err != nil {
		return cache.Lease{}, err
	}
	// Version 2 persists whole seconds; native clocks commonly include a
	// monotonic/sub-second component that must not enter the frozen schema.
	renewed.HeartbeatAt = renewed.HeartbeatAt.UTC().Truncate(time.Second)
	renewed.ExpiresAt = renewed.ExpiresAt.UTC().Truncate(time.Second)
	renewed.GraceUntil = renewed.GraceUntil.UTC().Truncate(time.Second)
	previousDaemonID := lease.DaemonInstanceID
	renewed.DaemonInstanceID = manager.daemonID
	if previousDaemonID != manager.daemonID {
		err = manager.catalog.AdoptAndHeartbeatLease(ctx, lease, renewed)
	} else {
		err = manager.catalog.HeartbeatLease(ctx, renewed)
	}
	if err != nil {
		return cache.Lease{}, fmt.Errorf("heartbeat cache handoff catalog: %w", err)
	}
	return renewed, nil
}

func (manager *Manager) PrepareHandoff(ctx context.Context, request HandoffRequest) (HandoffResult, error) {
	if manager == nil || manager.files == nil || manager.catalog == nil || manager.clock == nil {
		return HandoffResult{}, fmt.Errorf("prepare cache handoff: nil manager")
	}
	if ctx == nil {
		return HandoffResult{}, fmt.Errorf("prepare cache handoff: nil context")
	}
	if request.Process != nil {
		if err := request.Process.Validate(); err != nil || manager.processes == nil || manager.processes.Classify(*request.Process) != cache.ProcessMatches {
			return HandoffResult{}, fmt.Errorf("prepare cache handoff: supplied process identity is not live and classifiable")
		}
	}
	manager.admissionMu.Lock()
	defer manager.admissionMu.Unlock()
	entry, err := manager.catalog.GetEntry(ctx, request.EntryID)
	if err != nil {
		return HandoffResult{}, fmt.Errorf("prepare cache handoff entry: %w", err)
	}
	if request.RequireUniquePinnedOffline {
		if entry.Policy != cache.PolicyPinnedOffline || !entry.Pinned || entry.Freshness != cache.EntryUnknown {
			return HandoffResult{}, fmt.Errorf("prepare pinned offline cache handoff: selected entry is not pinned and unknown")
		}
		if err := manager.requireUniquePinnedOfflineEntryLocked(ctx, entry); err != nil {
			return HandoffResult{}, err
		}
	}
	blob, err := manager.catalog.GetBlob(ctx, entry.BlobID)
	if err != nil {
		return HandoffResult{}, fmt.Errorf("prepare cache handoff blob: %w", err)
	}
	now := manager.now()
	materialization := cache.Materialization{
		ID: request.MaterializationID, EntryID: entry.ID, BaselineBlobID: blob.ID, CurrentBlobID: blob.ID,
		Size: blob.Size, State: cache.MaterializationClean, Pinned: request.Pinned, CreatedAt: now, LastAccessAt: now,
	}
	if err := manager.admitLocked(ctx, cache.Admission{Materialization: &materialization}); err != nil {
		return HandoffResult{}, fmt.Errorf("prepare cache handoff admission: %w", err)
	}
	filesystemResult, err := manager.files.CreateMaterialization(ctx, request.MaterializationID, entry.ID, cachefs.BlobIdentity{ID: blob.ID, Size: blob.Size})
	if err != nil {
		return HandoffResult{}, fmt.Errorf("prepare cache handoff bytes: %w", err)
	}
	referenceOwner, err := referenceOwner(request.OwnerKind)
	if err != nil {
		return HandoffResult{}, err
	}
	reference := cache.Reference{ID: request.ReferenceID, OwnerKind: referenceOwner, OwnerID: request.OwnerID, Target: cache.MaterializationTarget(materialization.ID), CreatedAt: now}
	lease := cache.Lease{
		ID: request.LeaseID, OwnerKind: request.OwnerKind, OwnerID: request.OwnerID, DaemonInstanceID: manager.daemonID,
		Target: cache.MaterializationTarget(materialization.ID), State: cache.LeaseActive,
		HeartbeatAt: now, ExpiresAt: now.Add(cache.DefaultLeaseExpiry), GraceUntil: now.Add(cache.DefaultLeaseExpiry), Process: cloneProcess(request.Process),
	}
	if request.OwnerKind == cache.LeaseOwnerOpener {
		lease.GraceUntil = lease.ExpiresAt.Add(cache.DefaultOpenerGrace)
	}
	if err := manager.catalog.PrepareHandoff(ctx, materialization, reference, lease); err != nil {
		return HandoffResult{}, fmt.Errorf("prepare cache handoff catalog: %w", err)
	}
	return HandoffResult{Path: filesystemResult.Info.Path, Materialization: materialization, Reference: reference, Lease: lease}, nil
}

func (manager *Manager) requireUniquePinnedOfflineEntryLocked(ctx context.Context, selected cache.Entry) error {
	entries, err := manager.catalog.ListEntries(ctx)
	if err != nil {
		return fmt.Errorf("prepare pinned offline cache handoff catalog: %w", err)
	}
	matches := 0
	for _, entry := range entries {
		if entry.EndpointID == selected.EndpointID && bytes.Equal(entry.CanonicalPath, selected.CanonicalPath) &&
			entry.WorkspaceID == selected.WorkspaceID && entry.Policy == cache.PolicyPinnedOffline && entry.Pinned {
			matches++
		}
	}
	if matches != 1 {
		return fmt.Errorf("%w: %d fingerprints match the selected location", ErrPinnedOfflineAmbiguous, matches)
	}
	return nil
}

func (manager *Manager) ReleaseHandoff(ctx context.Context, request ReleaseHandoffRequest) error {
	if manager == nil || manager.catalog == nil || manager.clock == nil {
		return fmt.Errorf("release cache handoff: nil manager")
	}
	if ctx == nil {
		return fmt.Errorf("release cache handoff: nil context")
	}
	return manager.catalog.ReleaseHandoff(ctx, cachestore.ReleaseHandoffRequest{
		MaterializationID: request.MaterializationID,
		ReferenceID:       request.ReferenceID,
		LeaseID:           request.LeaseID,
		OwnerKind:         request.OwnerKind,
		OwnerID:           request.OwnerID,
		ReleasedAt:        manager.now(),
	})
}

type ReconcileReport struct {
	Filesystem                  cachefs.ScanReport
	Snapshot                    cache.Snapshot
	UncatalogedBlobs            []cache.BlobID
	UncatalogedEntries          []cache.EntryID
	UncatalogedMaterializations []cache.MaterializationID
	MissingBlobs                []cache.BlobID
	MissingEntries              []cache.EntryID
	MissingMaterializations     []cache.MaterializationID
	DirtyCandidates             []cache.MaterializationID
	NeedsAttention              bool
}

func (manager *Manager) Reconcile(ctx context.Context, maxVisited int) (ReconcileReport, error) {
	if manager == nil || manager.files == nil || manager.catalog == nil {
		return ReconcileReport{}, fmt.Errorf("reconcile cache: nil manager")
	}
	filesystem, err := manager.files.Scan(maxVisited)
	if err != nil {
		return ReconcileReport{}, fmt.Errorf("reconcile cache filesystem: %w", err)
	}
	snapshot, err := manager.catalog.LoadSnapshotBounded(ctx, maxVisited)
	if err != nil {
		return ReconcileReport{Filesystem: filesystem, NeedsAttention: true}, fmt.Errorf("reconcile cache catalog: %w", err)
	}
	report := ReconcileReport{Filesystem: filesystem, Snapshot: snapshot}
	fsBlobs := make(map[cache.BlobID]struct{}, len(filesystem.VerifiedBlobs))
	for _, item := range filesystem.VerifiedBlobs {
		fsBlobs[item.Identity.ID] = struct{}{}
	}
	dbBlobs := make(map[cache.BlobID]struct{}, len(snapshot.Blobs))
	for _, item := range snapshot.Blobs {
		dbBlobs[item.ID] = struct{}{}
	}
	fsEntries := make(map[cache.EntryID]struct{}, len(filesystem.VerifiedEntries))
	for _, item := range filesystem.VerifiedEntries {
		fsEntries[item.Manifest.EntryID] = struct{}{}
	}
	dbEntries := make(map[cache.EntryID]struct{}, len(snapshot.Entries))
	for _, item := range snapshot.Entries {
		dbEntries[item.ID] = struct{}{}
	}
	fsMaterializations := make(map[cache.MaterializationID]cachefs.MaterializationInfo, len(filesystem.VerifiedMaterializations))
	for _, item := range filesystem.VerifiedMaterializations {
		fsMaterializations[item.Manifest.MaterializationID] = item.Info
	}
	dbMaterializations := make(map[cache.MaterializationID]struct{}, len(snapshot.Materializations))
	for _, item := range snapshot.Materializations {
		dbMaterializations[item.ID] = struct{}{}
	}
	for id := range fsBlobs {
		if _, ok := dbBlobs[id]; !ok {
			report.UncatalogedBlobs = append(report.UncatalogedBlobs, id)
		}
	}
	for id := range dbBlobs {
		if _, ok := fsBlobs[id]; !ok {
			report.MissingBlobs = append(report.MissingBlobs, id)
		}
	}
	for id := range fsEntries {
		if _, ok := dbEntries[id]; !ok {
			report.UncatalogedEntries = append(report.UncatalogedEntries, id)
		}
	}
	for id := range dbEntries {
		if _, ok := fsEntries[id]; !ok {
			report.MissingEntries = append(report.MissingEntries, id)
		}
	}
	for id := range fsMaterializations {
		if _, ok := dbMaterializations[id]; !ok {
			report.UncatalogedMaterializations = append(report.UncatalogedMaterializations, id)
		}
	}
	for _, item := range snapshot.Materializations {
		info, ok := fsMaterializations[item.ID]
		if !ok {
			id := item.ID
			report.MissingMaterializations = append(report.MissingMaterializations, id)
			continue
		}
		if info.SHA256 != item.CurrentBlobID || info.Size != item.Size {
			report.DirtyCandidates = append(report.DirtyCandidates, item.ID)
		}
	}
	sortIDs(report.UncatalogedBlobs)
	sortIDs(report.UncatalogedEntries)
	sortIDs(report.UncatalogedMaterializations)
	sortIDs(report.MissingBlobs)
	sortIDs(report.MissingEntries)
	sortIDs(report.MissingMaterializations)
	sortIDs(report.DirtyCandidates)
	report.NeedsAttention = filesystem.Truncated || len(filesystem.Orphans)+len(filesystem.Unknown)+len(filesystem.Symlinks) != 0 ||
		len(report.UncatalogedBlobs)+len(report.UncatalogedEntries)+len(report.UncatalogedMaterializations)+
			len(report.MissingBlobs)+len(report.MissingEntries)+len(report.MissingMaterializations)+len(report.DirtyCandidates) != 0
	return report, nil
}

func (manager *Manager) PlanQuota(ctx context.Context) (cache.EvictionPlan, error) {
	snapshot, err := manager.catalog.LoadSnapshotBounded(ctx, maxLifecycleVisited)
	if err != nil {
		return cache.EvictionPlan{}, err
	}
	return cache.PlanEvictions(snapshot, manager.limits, manager.leaseState)
}

func (manager *Manager) now() time.Time { return manager.clock.Now().UTC().Truncate(time.Second) }

func canonicalFingerprint(source domain.Fingerprint, blob cachefs.BlobIdentity) (cache.Fingerprint, error) {
	value := struct {
		Format        int                 `json:"format"`
		Source        ipc.WireFingerprint `json:"source"`
		ContentSHA256 cache.BlobID        `json:"content_sha256"`
		ContentSize   int64               `json:"content_size"`
	}{Format: 1, Source: ipc.EncodeFingerprint(source), ContentSHA256: blob.ID, ContentSize: blob.Size}
	encoded, err := json.Marshal(value)
	if err != nil {
		return cache.Fingerprint{}, fmt.Errorf("encode cache source fingerprint: %w", err)
	}
	fingerprint := cache.Fingerprint{Strength: cache.FingerprintStrong, Canonical: encoded}
	if err := fingerprint.Validate(); err != nil {
		return cache.Fingerprint{}, err
	}
	return fingerprint, nil
}

func referenceOwner(value cache.LeaseOwnerKind) (cache.ReferenceOwnerKind, error) {
	switch value {
	case cache.LeaseOwnerPreview:
		return cache.ReferenceOwnerPreview, nil
	case cache.LeaseOwnerEditor:
		return cache.ReferenceOwnerEdit, nil
	case cache.LeaseOwnerOpener:
		return cache.ReferenceOwnerOpen, nil
	case cache.LeaseOwnerUpload:
		return cache.ReferenceOwnerUpload, nil
	default:
		return "", fmt.Errorf("prepare cache handoff: unsupported owner kind %q", value)
	}
}

func cloneProcess(value *cache.ProcessIdentity) *cache.ProcessIdentity {
	if value == nil {
		return nil
	}
	copy := *value
	return &copy
}

func lowerHex(value string, width int) bool {
	if len(value) != width {
		return false
	}
	for _, character := range value {
		if !strings.ContainsRune("0123456789abcdef", character) {
			return false
		}
	}
	return true
}

func sortIDs[T ~string](values []T) {
	sort.Slice(values, func(left, right int) bool { return values[left] < values[right] })
}
