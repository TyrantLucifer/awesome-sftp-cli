package fake

import (
	"context"
	"errors"
	"fmt"
	"io"
	"reflect"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/TyrantLucifer/awesome-mac-sftp/internal/domain"
	"github.com/TyrantLucifer/awesome-mac-sftp/internal/foundation"
	providerapi "github.com/TyrantLucifer/awesome-mac-sftp/internal/provider"
)

const maximumPageLimit uint32 = 4096

var nextCursorNamespace atomic.Uint64

type cursorBinding struct {
	location domain.Location
	sort     *providerapi.SortHint
	snapshot *listingSnapshot
	offset   int
}

type listingSnapshot struct {
	directoryID          uint64
	generation           uint64
	entries              []domain.Entry
	directoryFingerprint domain.Fingerprint
	requestedSortApplied bool
}

type nodeRevisionKey struct {
	nodeID   uint64
	revision uint64
}

type historicalStat struct {
	kind        domain.EntryKind
	metadata    domain.Metadata
	fingerprint domain.Fingerprint
	symlink     *domain.SymlinkInfo
}

type readHandle struct {
	mu sync.Mutex

	provider     *Provider
	node         *treeNode
	sessionEpoch uint64
	info         providerapi.ReadInfo
	data         []byte
	offset       int
	closed       bool
}

var _ providerapi.ReadHandle = (*readHandle)(nil)

type writeHandle struct {
	mu sync.Mutex

	provider     *Provider
	node         *treeNode
	sessionEpoch uint64
	location     domain.Location
	offset       int64
	closed       bool
}

var _ providerapi.WriteHandle = (*writeHandle)(nil)

// Scenario is the caller-owned input used to construct one isolated fake.
type Scenario struct {
	Endpoint     domain.Endpoint
	Snapshot     domain.EndpointSnapshot
	Root         Node
	DefaultLimit uint32
	Clock        foundation.Clock
	Script       []FaultStep
}

// Provider is an isolated, concurrency-safe in-memory provider.
type Provider struct {
	mu sync.RWMutex

	endpoint        domain.Endpoint
	snapshot        domain.EndpointSnapshot
	root            *treeNode
	defaultLimit    uint32
	clock           foundation.Clock
	cursorNamespace uint64
	nextCursor      uint64
	cursors         map[providerapi.PageCursor]cursorBinding
	nextNodeID      uint64
	history         map[nodeRevisionKey]historicalStat
	sessionEpoch    uint64
	capabilitySeen  map[domain.CapabilityName]struct{}
	capabilityLost  map[domain.CapabilityName]struct{}
	script          *scriptState
}

var _ providerapi.Provider = (*Provider)(nil)
var _ providerapi.MutableProvider = (*Provider)(nil)

// New validates and takes ownership of a deep copy of scenario.
func New(scenario Scenario) (*Provider, *Controller, error) {
	if scenario.Endpoint.ID == "" {
		return nil, nil, fmt.Errorf("create fake provider: endpoint ID is empty")
	}
	if scenario.Endpoint.Kind != domain.EndpointLocal && scenario.Endpoint.Kind != domain.EndpointSSH {
		return nil, nil, fmt.Errorf("create fake provider: endpoint kind %q is invalid", scenario.Endpoint.Kind)
	}
	if scenario.Snapshot.EndpointID != scenario.Endpoint.ID {
		return nil, nil, fmt.Errorf("create fake provider: endpoint snapshot does not match endpoint")
	}
	if scenario.Snapshot.SessionID == "" {
		return nil, nil, fmt.Errorf("create fake provider: snapshot session ID is empty")
	}
	if scenario.Snapshot.Capabilities.Revision.SessionID != scenario.Snapshot.SessionID {
		return nil, nil, fmt.Errorf("create fake provider: capability session does not match snapshot")
	}
	if !isKnownConnectionState(scenario.Snapshot.State) {
		return nil, nil, fmt.Errorf(
			"create fake provider: snapshot connection state %q is invalid",
			scenario.Snapshot.State,
		)
	}
	capabilities, err := domain.NewCapabilitySnapshot(
		scenario.Snapshot.Capabilities.Revision,
		scenario.Snapshot.Capabilities.Complete,
		scenario.Snapshot.Capabilities.Items,
	)
	if err != nil {
		return nil, nil, fmt.Errorf("create fake provider: capabilities: %w", err)
	}
	root, err := buildTree(scenario.Root)
	if err != nil {
		return nil, nil, err
	}
	if scenario.Clock == nil {
		return nil, nil, fmt.Errorf("create fake provider: clock is nil")
	}
	defaultLimit := scenario.DefaultLimit
	if defaultLimit == 0 || defaultLimit > maximumPageLimit {
		return nil, nil, fmt.Errorf(
			"create fake provider: default page limit must be between 1 and %d",
			maximumPageLimit,
		)
	}
	script, err := newScriptState(scenario.Script)
	if err != nil {
		return nil, nil, err
	}

	snapshot := scenario.Snapshot
	snapshot.Capabilities = capabilities
	capabilitySeen, capabilityLost := capabilityHistoryForNewSession(capabilities)
	implementation := &Provider{
		endpoint:        scenario.Endpoint,
		snapshot:        snapshot,
		root:            root,
		defaultLimit:    defaultLimit,
		clock:           scenario.Clock,
		cursorNamespace: nextCursorNamespace.Add(1),
		cursors:         make(map[providerapi.PageCursor]cursorBinding),
		nextNodeID:      maxNodeID(root) + 1,
		history:         make(map[nodeRevisionKey]historicalStat),
		sessionEpoch:    1,
		capabilitySeen:  capabilitySeen,
		capabilityLost:  capabilityLost,
		script:          script,
	}
	implementation.seedHistoryLocked(root, "/")
	return implementation, &Controller{provider: implementation}, nil
}

func (p *Provider) Descriptor() domain.Endpoint {
	return p.endpoint
}

func (p *Provider) Snapshot(ctx context.Context) (domain.EndpointSnapshot, error) {
	if err := p.checkContext(ctx, "snapshot", nil); err != nil {
		return domain.EndpointSnapshot{}, err
	}
	if _, err := p.recordOperation(ctx, OperationSnapshot, nil); err != nil {
		return domain.EndpointSnapshot{}, err
	}
	p.mu.RLock()
	defer p.mu.RUnlock()
	if err := p.checkContext(ctx, "snapshot", nil); err != nil {
		return domain.EndpointSnapshot{}, err
	}
	return cloneSnapshot(p.snapshot), nil
}

func (p *Provider) Normalize(
	ctx context.Context,
	request domain.NormalizeRequest,
) (domain.Location, error) {
	if err := p.checkContext(ctx, "normalize", nil); err != nil {
		return domain.Location{}, err
	}
	if request.EndpointID != p.endpoint.ID {
		return domain.Location{}, p.error(
			domain.CodeInvalidArgument,
			"normalize",
			nil,
			"request endpoint does not match provider",
			nil,
		)
	}
	if strings.IndexByte(request.Input, 0) >= 0 {
		return domain.Location{}, p.error(
			domain.CodeInvalidArgument,
			"normalize",
			nil,
			"path contains NUL",
			nil,
		)
	}

	var components []string
	if request.Base != nil {
		if request.Base.EndpointID != p.endpoint.ID {
			return domain.Location{}, p.error(
				domain.CodeInvalidArgument,
				"normalize",
				nil,
				"base endpoint does not match provider",
				nil,
			)
		}
		base, err := canonicalizeAbsolute(string(request.Base.Path))
		if err != nil || base != string(request.Base.Path) {
			return domain.Location{}, p.error(
				domain.CodeInvalidArgument,
				"normalize",
				nil,
				"base path is not canonical",
				err,
			)
		}
		components = splitCanonical(base)
	}

	absolute := strings.HasPrefix(request.Input, "/")
	if absolute {
		components = nil
	} else if request.Base == nil {
		return domain.Location{}, p.error(
			domain.CodeInvalidArgument,
			"normalize",
			nil,
			"relative path requires a base",
			nil,
		)
	}

	normalized, err := applyPath(components, request.Input)
	if err != nil {
		return domain.Location{}, p.error(
			domain.CodeInvalidArgument,
			"normalize",
			nil,
			"path escapes endpoint root",
			err,
		)
	}
	canonical := "/"
	if len(normalized) != 0 {
		canonical += strings.Join(normalized, "/")
	}
	location := domain.Location{EndpointID: p.endpoint.ID, Path: domain.CanonicalPath(canonical)}
	if _, err := p.recordOperation(ctx, OperationNormalize, &location); err != nil {
		return domain.Location{}, err
	}
	return location, nil
}

func (p *Provider) List(
	ctx context.Context,
	request providerapi.ListRequest,
) (providerapi.ListPage, error) {
	if err := p.checkContext(ctx, "list", &request.Location); err != nil {
		return providerapi.ListPage{}, err
	}
	if err := providerapi.ValidateListRequest(p.endpoint.ID, request); err != nil {
		return providerapi.ListPage{}, err
	}
	canonical, err := canonicalizeAbsolute(string(request.Location.Path))
	if err != nil || canonical != string(request.Location.Path) {
		return providerapi.ListPage{}, p.error(
			domain.CodeInvalidArgument,
			"list",
			&request.Location,
			"location path is not canonical",
			err,
		)
	}
	effect, err := p.recordOperation(ctx, OperationList, &request.Location)
	if err != nil {
		return providerapi.ListPage{}, err
	}
	disconnectedAt, err := p.disconnectTime(ctx, "list", &request.Location, effect)
	if err != nil {
		return providerapi.ListPage{}, err
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	if err := p.checkContext(ctx, "list", &request.Location); err != nil {
		return providerapi.ListPage{}, err
	}
	if effect.Disconnect {
		p.applyDisconnectLocked(disconnectedAt)
	}
	if err := p.checkPersistentStateLocked("list", &request.Location, "read"); err != nil {
		return providerapi.ListPage{}, err
	}
	if err := p.checkPathPermissionLocked(
		"list",
		&request.Location,
		request.Location.Path,
		true,
	); err != nil {
		return providerapi.ListPage{}, err
	}
	var snapshot *listingSnapshot
	offset := 0
	if request.Cursor != "" {
		binding, issued := p.cursors[request.Cursor]
		if !issued {
			return providerapi.ListPage{}, p.error(
				domain.CodeInvalidArgument,
				"list",
				&request.Location,
				"cursor was not issued by this provider",
				nil,
			)
		}
		if binding.location != request.Location || !equalSortHint(binding.sort, request.Sort) {
			return providerapi.ListPage{}, p.error(
				domain.CodeInvalidArgument,
				"list",
				&request.Location,
				"cursor binding does not match request",
				nil,
			)
		}
		if binding.snapshot == nil {
			return providerapi.ListPage{}, p.error(
				domain.CodeInvalidArgument,
				"list",
				&request.Location,
				"cursor has no listing snapshot",
				nil,
			)
		}
		directory, resolveErr := resolveNode(p.root, request.Location.Path, true)
		if resolveErr != nil || directory.kind != domain.EntryDirectory {
			return providerapi.ListPage{}, p.opError(
				domain.CodeConflict,
				"list",
				&request.Location,
				"listed directory is no longer available",
				domain.RetryAfterConflict,
				resolveErr,
			)
		}
		if binding.snapshot.directoryID != directory.id ||
			binding.snapshot.generation != directory.listingGeneration {
			return providerapi.ListPage{}, p.opError(
				domain.CodeConflict,
				"list",
				&request.Location,
				"listing directory identity or generation changed",
				domain.RetryAfterConflict,
				nil,
			)
		}
		snapshot = binding.snapshot
		offset = binding.offset
	} else {
		directory, err := p.listDirectoryLocked(request.Location)
		if err != nil {
			return providerapi.ListPage{}, err
		}
		snapshot = p.newListingSnapshotLocked(directory, request.Location, request.Sort)
	}

	if offset < 0 || offset > len(snapshot.entries) {
		return providerapi.ListPage{}, p.error(
			domain.CodeInvalidArgument,
			"list",
			&request.Location,
			"cursor offset is invalid",
			nil,
		)
	}
	pageLimit := int(request.Limit)
	if int(p.defaultLimit) < pageLimit {
		pageLimit = int(p.defaultLimit)
	}
	end := offset + pageLimit
	if end > len(snapshot.entries) {
		end = len(snapshot.entries)
	}
	entries := make([]domain.Entry, 0, end-offset)
	for _, entry := range snapshot.entries[offset:end] {
		owned := cloneEntry(entry)
		p.scrubResolvedKindLocked(&owned)
		entries = append(entries, owned)
	}
	done := end == len(snapshot.entries)
	var next providerapi.PageCursor
	if !done {
		p.nextCursor++
		next = providerapi.PageCursor(fmt.Sprintf(
			"fake:%d:%d",
			p.cursorNamespace,
			p.nextCursor,
		))
		p.cursors[next] = cursorBinding{
			location: request.Location,
			sort:     cloneSortHint(request.Sort),
			snapshot: snapshot,
			offset:   end,
		}
	}
	return providerapi.ListPage{
		Entries:              entries,
		NextCursor:           next,
		Done:                 done,
		RequestedSortApplied: snapshot.requestedSortApplied,
		Consistency:          providerapi.ConsistencySnapshot,
		DirectoryFingerprint: cloneFingerprint(snapshot.directoryFingerprint),
	}, nil
}

func (p *Provider) listDirectoryLocked(location domain.Location) (*treeNode, error) {
	directory, err := resolveNode(p.root, location.Path, true)
	if err != nil {
		return nil, p.lookupError("list", &location, err)
	}
	if directory.kind != domain.EntryDirectory {
		return nil, p.error(
			domain.CodeInvalidArgument,
			"list",
			&location,
			"location is not a directory",
			nil,
		)
	}
	return directory, nil
}

func (p *Provider) newListingSnapshotLocked(
	directory *treeNode,
	location domain.Location,
	sortHint *providerapi.SortHint,
) *listingSnapshot {
	children := make([]*treeNode, 0, len(directory.children))
	for _, child := range directory.children {
		children = append(children, child)
	}
	requestedSortApplied := sortHint != nil && sortHint.Key == "name"
	sort.Slice(children, func(left int, right int) bool {
		if requestedSortApplied && sortHint.DirectoriesFirst {
			leftDirectory := children[left].kind == domain.EntryDirectory
			rightDirectory := children[right].kind == domain.EntryDirectory
			if leftDirectory != rightDirectory {
				return leftDirectory
			}
		}
		if requestedSortApplied && sortHint.Direction == providerapi.SortDescending {
			return children[left].name > children[right].name
		}
		return children[left].name < children[right].name
	})
	entries := make([]domain.Entry, 0, len(children))
	for _, child := range children {
		childPath := joinCanonical(location.Path, child.name)
		entries = append(entries, p.entryLocked(
			child,
			domain.Location{EndpointID: p.endpoint.ID, Path: childPath},
			child.name,
		))
	}
	return &listingSnapshot{
		directoryID:          directory.id,
		generation:           directory.listingGeneration,
		entries:              entries,
		directoryFingerprint: p.fingerprintLocked(directory),
		requestedSortApplied: requestedSortApplied,
	}
}

func (p *Provider) Stat(
	ctx context.Context,
	request providerapi.StatRequest,
) (domain.Entry, error) {
	if err := p.checkContext(ctx, "stat", &request.Location); err != nil {
		return domain.Entry{}, err
	}
	if err := providerapi.ValidateStatRequest(p.endpoint.ID, request); err != nil {
		return domain.Entry{}, err
	}
	canonical, err := canonicalizeAbsolute(string(request.Location.Path))
	if err != nil || canonical != string(request.Location.Path) {
		return domain.Entry{}, p.error(
			domain.CodeInvalidArgument,
			"stat",
			&request.Location,
			"location path is not canonical",
			err,
		)
	}
	effect, err := p.recordOperation(ctx, OperationStat, &request.Location)
	if err != nil {
		return domain.Entry{}, err
	}
	disconnectedAt, err := p.disconnectTime(ctx, "stat", &request.Location, effect)
	if err != nil {
		return domain.Entry{}, err
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	if err := p.checkContext(ctx, "stat", &request.Location); err != nil {
		return domain.Entry{}, err
	}
	if effect.Disconnect {
		p.applyDisconnectLocked(disconnectedAt)
	}
	if err := p.checkPersistentStateLocked("stat", &request.Location, "read"); err != nil {
		return domain.Entry{}, err
	}
	if err := p.checkPathPermissionLocked(
		"stat",
		&request.Location,
		request.Location.Path,
		request.FollowSymlinks,
	); err != nil {
		return domain.Entry{}, err
	}
	node, err := resolveNode(p.root, request.Location.Path, request.FollowSymlinks)
	if err != nil {
		return domain.Entry{}, p.lookupError("stat", &request.Location, err)
	}
	if effect.StaleNodeRevision != nil {
		entry, exists := p.historicalEntryLocked(
			node,
			*effect.StaleNodeRevision,
			request.Location,
			baseName(request.Location.Path),
		)
		if !exists {
			return domain.Entry{}, p.opError(
				domain.CodeInvalidArgument,
				"stat",
				&request.Location,
				"requested stale node revision is unavailable",
				domain.RetryNever,
				nil,
			)
		}
		if !request.FollowSymlinks {
			p.scrubResolvedKindLocked(&entry)
		}
		return entry, nil
	}
	entry := p.entryLocked(node, request.Location, baseName(request.Location.Path))
	if !request.FollowSymlinks {
		p.scrubResolvedKindLocked(&entry)
	}
	return entry, nil
}

func (p *Provider) OpenRead(
	ctx context.Context,
	request providerapi.OpenReadRequest,
) (providerapi.ReadHandle, error) {
	if err := p.checkContext(ctx, "open_read", &request.Location); err != nil {
		return nil, err
	}
	if err := providerapi.ValidateOpenReadRequest(p.endpoint.ID, request); err != nil {
		return nil, err
	}
	canonical, err := canonicalizeAbsolute(string(request.Location.Path))
	if err != nil || canonical != string(request.Location.Path) {
		return nil, p.error(
			domain.CodeInvalidArgument,
			"open_read",
			&request.Location,
			"location path is not canonical",
			err,
		)
	}
	effect, err := p.recordOperation(ctx, OperationOpenRead, &request.Location)
	if err != nil {
		return nil, err
	}
	disconnectedAt, err := p.disconnectTime(ctx, "open_read", &request.Location, effect)
	if err != nil {
		return nil, err
	}
	p.mu.Lock()
	if err := p.checkContext(ctx, "open_read", &request.Location); err != nil {
		p.mu.Unlock()
		return nil, err
	}
	if effect.Disconnect {
		p.applyDisconnectLocked(disconnectedAt)
	}
	if err := p.checkPersistentStateLocked("open_read", &request.Location, "read"); err != nil {
		p.mu.Unlock()
		return nil, err
	}
	if err := p.checkPathPermissionLocked(
		"open_read",
		&request.Location,
		request.Location.Path,
		true,
	); err != nil {
		p.mu.Unlock()
		return nil, err
	}
	node, err := resolveNode(p.root, request.Location.Path, true)
	if err != nil {
		p.mu.Unlock()
		return nil, p.lookupError("open_read", &request.Location, err)
	}
	if node.kind != domain.EntryFile {
		p.mu.Unlock()
		return nil, p.error(
			domain.CodeInvalidArgument,
			"open_read",
			&request.Location,
			"location is not a regular file",
			nil,
		)
	}
	entry := p.entryLocked(node, request.Location, baseName(request.Location.Path))
	if request.ExpectedFingerprint != nil &&
		!reflect.DeepEqual(*request.ExpectedFingerprint, entry.Fingerprint) {
		p.mu.Unlock()
		return nil, p.opError(
			domain.CodeConflict,
			"open_read",
			&request.Location,
			"fingerprint does not match",
			domain.RetryAfterConflict,
			nil,
		)
	}
	start := request.Offset
	if start > int64(len(node.data)) {
		start = int64(len(node.data))
	}
	end := int64(len(node.data))
	if request.Limit != nil && *request.Limit < end-start {
		end = start + *request.Limit
	}
	data := append([]byte(nil), node.data[start:end]...)
	info := providerapi.ReadInfo{
		Entry:       cloneEntry(entry),
		Fingerprint: cloneFingerprint(entry.Fingerprint),
	}
	sessionEpoch := p.sessionEpoch
	p.mu.Unlock()
	return &readHandle{
		provider:     p,
		node:         node,
		sessionEpoch: sessionEpoch,
		info:         info,
		data:         data,
	}, nil
}

func (h *readHandle) Info() providerapi.ReadInfo {
	h.mu.Lock()
	defer h.mu.Unlock()
	return cloneReadInfo(h.info)
}

func (h *readHandle) Read(ctx context.Context, destination []byte) (int, error) {
	location := h.info.Entry.Location
	if err := h.provider.checkContext(ctx, "read", &location); err != nil {
		return 0, err
	}
	effect, err := h.provider.recordOperation(ctx, OperationRead, &location)
	if err != nil {
		return 0, err
	}
	disconnectedAt, err := h.provider.disconnectTime(ctx, "read", &location, effect)
	if err != nil {
		return 0, err
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	if err := h.provider.checkContext(ctx, "read", &location); err != nil {
		return 0, err
	}
	if h.closed {
		return 0, h.provider.error(
			domain.CodeInvalidArgument,
			"read",
			&location,
			"read handle is closed",
			io.ErrClosedPipe,
		)
	}
	h.provider.mu.Lock()
	defer h.provider.mu.Unlock()
	if err := h.provider.checkContext(ctx, "read", &location); err != nil {
		return 0, err
	}
	if err := h.provider.checkHandleSessionLocked(
		"read",
		&location,
		h.sessionEpoch,
	); err != nil {
		return 0, err
	}
	if effect.Disconnect {
		h.provider.applyDisconnectLocked(disconnectedAt)
	}
	if err := h.provider.checkPersistentStateLocked("read", &location, "read"); err != nil {
		return 0, err
	}
	if err := h.provider.checkNodePermissionLocked("read", &location, h.node); err != nil {
		return 0, err
	}

	remaining := len(h.data) - h.offset
	maximum := len(destination)
	if effect.MaxReadBytes > 0 && effect.MaxReadBytes < maximum {
		maximum = effect.MaxReadBytes
	}
	if remaining < maximum {
		maximum = remaining
	}
	n := copy(destination[:maximum], h.data[h.offset:h.offset+maximum])
	h.offset += n
	if effect.Error != nil {
		injected := cloneOpError(effect.Error)
		injected.Effect = domain.EffectNone
		return n, injected
	}
	if remaining == 0 {
		return 0, io.EOF
	}
	return n, nil
}

func (h *readHandle) Close(ctx context.Context) error {
	location := h.info.Entry.Location
	if err := h.provider.checkContext(ctx, "close_read", &location); err != nil {
		return err
	}
	if _, err := h.provider.recordOperation(ctx, OperationCloseRead, &location); err != nil {
		return err
	}
	h.mu.Lock()
	if err := h.provider.checkContext(ctx, "close_read", &location); err != nil {
		h.mu.Unlock()
		return err
	}
	h.closed = true
	h.mu.Unlock()
	return nil
}

func (p *Provider) OpenWrite(
	ctx context.Context,
	request providerapi.OpenWriteRequest,
) (providerapi.WriteHandle, error) {
	if err := p.checkContext(ctx, "open_write", &request.Location); err != nil {
		return nil, err
	}
	if err := providerapi.ValidateOpenWriteRequest(p.endpoint.ID, request); err != nil {
		return nil, err
	}
	canonical, err := canonicalizeAbsolute(string(request.Location.Path))
	if err != nil || canonical != string(request.Location.Path) || request.Location.Path == "/" {
		return nil, p.error(
			domain.CodeInvalidArgument,
			"open_write",
			&request.Location,
			"write location must be a canonical non-root path",
			err,
		)
	}
	if request.Disposition == providerapi.WriteCreateNew && request.Offset != 0 {
		return nil, p.error(
			domain.CodeInvalidArgument,
			"open_write",
			&request.Location,
			"create-new offset must be zero",
			nil,
		)
	}
	if request.Disposition == providerapi.WriteTruncate && request.Offset != 0 {
		return nil, p.error(
			domain.CodeInvalidArgument,
			"open_write",
			&request.Location,
			"truncate offset must be zero",
			nil,
		)
	}
	effect, err := p.recordOperation(ctx, OperationOpenWrite, &request.Location)
	if err != nil {
		return nil, err
	}
	parentLocation, name, _ := parentPath(request.Location.Path)
	disconnectedAt, err := p.disconnectTime(ctx, "open_write", &request.Location, effect)
	if err != nil {
		return nil, err
	}
	var modifiedAt time.Time
	if request.Disposition == providerapi.WriteTruncate && !effect.Disconnect {
		modifiedAt = p.clock.Now()
		if err := p.checkContext(ctx, "open_write", &request.Location); err != nil {
			return nil, err
		}
	}

	p.mu.Lock()
	defer p.mu.Unlock()
	if err := p.checkContext(ctx, "open_write", &request.Location); err != nil {
		return nil, err
	}
	if effect.Disconnect {
		p.applyDisconnectLocked(disconnectedAt)
	}
	if err := p.checkPersistentStateLocked("open_write", &request.Location, "write"); err != nil {
		return nil, err
	}
	if err := p.checkPathPermissionLocked(
		"open_write",
		&request.Location,
		parentLocation,
		true,
	); err != nil {
		return nil, err
	}
	if request.Disposition != providerapi.WriteCreateNew {
		if err := p.checkPathPermissionLocked(
			"open_write",
			&request.Location,
			request.Location.Path,
			true,
		); err != nil {
			return nil, err
		}
	}
	parent, err := resolveNode(p.root, parentLocation, true)
	if err != nil {
		return nil, p.lookupError("open_write", &request.Location, err)
	}
	if parent.kind != domain.EntryDirectory {
		return nil, p.error(
			domain.CodeNotFound,
			"open_write",
			&request.Location,
			"parent directory was not found",
			nil,
		)
	}

	var node *treeNode
	switch request.Disposition {
	case providerapi.WriteCreateNew:
		if _, exists := parent.children[name]; exists {
			return nil, p.error(
				domain.CodeAlreadyExists,
				"open_write",
				&request.Location,
				"path already exists",
				nil,
			)
		}
		if request.ExpectedFingerprint != nil {
			return nil, p.opError(
				domain.CodeConflict,
				"open_write",
				&request.Location,
				"fingerprint does not match an absent path",
				domain.RetryAfterConflict,
				nil,
			)
		}
		size := uint64(0)
		node = &treeNode{
			id:       p.nextNodeID,
			version:  1,
			name:     name,
			kind:     domain.EntryFile,
			metadata: domain.Metadata{Size: &size},
		}
		p.nextNodeID++
		parent.children[name] = node
		p.seedHistoryLocked(node, request.Location.Path)
		p.bumpDirectoryLocked(parent)
	case providerapi.WriteResumeExisting, providerapi.WriteTruncate:
		node, err = resolveNode(p.root, request.Location.Path, true)
		if err != nil {
			return nil, p.lookupError("open_write", &request.Location, err)
		}
		if node.kind != domain.EntryFile {
			return nil, p.error(
				domain.CodeInvalidArgument,
				"open_write",
				&request.Location,
				"location is not a regular file",
				nil,
			)
		}
		fingerprint := p.fingerprintLocked(node)
		if request.ExpectedFingerprint != nil &&
			!reflect.DeepEqual(*request.ExpectedFingerprint, fingerprint) {
			return nil, p.opError(
				domain.CodeConflict,
				"open_write",
				&request.Location,
				"fingerprint does not match",
				domain.RetryAfterConflict,
				nil,
			)
		}
		if request.Disposition == providerapi.WriteResumeExisting {
			if request.Offset > int64(len(node.data)) {
				return nil, p.error(
					domain.CodeInvalidArgument,
					"open_write",
					&request.Location,
					"resume offset exceeds file size",
					nil,
				)
			}
		} else {
			node.data = nil
			p.touchNodeLocked(node, modifiedAt)
			p.bumpParentOfNodeLocked(node)
		}
	}
	return &writeHandle{
		provider:     p,
		node:         node,
		sessionEpoch: p.sessionEpoch,
		location:     request.Location,
		offset:       request.Offset,
	}, nil
}

func (h *writeHandle) Write(ctx context.Context, source []byte) (int, error) {
	location := h.location
	if err := h.provider.checkContext(ctx, "write", &location); err != nil {
		return 0, err
	}
	effect, err := h.provider.recordOperation(ctx, OperationWrite, &location)
	if err != nil {
		return 0, err
	}
	var disconnectedAt time.Time
	var modifiedAt time.Time
	if effect.Disconnect {
		disconnectedAt = h.provider.clock.Now()
	} else {
		modifiedAt = h.provider.clock.Now()
	}
	if err := h.provider.checkContext(ctx, "write", &location); err != nil {
		return 0, err
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	if err := h.provider.checkContext(ctx, "write", &location); err != nil {
		return 0, err
	}
	if h.closed {
		return 0, h.provider.error(
			domain.CodeInvalidArgument,
			"write",
			&location,
			"write handle is closed",
			io.ErrClosedPipe,
		)
	}
	h.provider.mu.Lock()
	defer h.provider.mu.Unlock()
	if err := h.provider.checkContext(ctx, "write", &location); err != nil {
		return 0, err
	}
	if err := h.provider.checkHandleSessionLocked(
		"write",
		&location,
		h.sessionEpoch,
	); err != nil {
		return 0, err
	}
	if effect.Disconnect {
		h.provider.applyDisconnectLocked(disconnectedAt)
	}
	if err := h.provider.checkPersistentStateLocked("write", &location, "write"); err != nil {
		return 0, err
	}
	if err := h.provider.checkNodePermissionLocked("write", &location, h.node); err != nil {
		return 0, err
	}
	maximum := len(source)
	if effect.MaxWriteBytes > 0 && effect.MaxWriteBytes < maximum {
		maximum = effect.MaxWriteBytes
	}
	if maximum == 0 {
		if effect.Error != nil {
			injected := cloneOpError(effect.Error)
			injected.Effect = domain.EffectNone
			return 0, injected
		}
		return 0, nil
	}
	end := h.offset + int64(maximum)
	if end < h.offset || end > int64(int(^uint(0)>>1)) {
		return 0, h.provider.error(
			domain.CodeResourceExhausted,
			"write",
			&location,
			"write range is too large",
			nil,
		)
	}
	if end > int64(len(h.node.data)) {
		h.node.data = append(h.node.data, make([]byte, int(end)-len(h.node.data))...)
	}
	n := copy(h.node.data[int(h.offset):int(end)], source[:maximum])
	h.offset += int64(n)
	h.provider.touchNodeLocked(h.node, modifiedAt)
	h.provider.bumpParentOfNodeLocked(h.node)
	if effect.Error != nil {
		injected := cloneOpError(effect.Error)
		injected.Effect = domain.EffectApplied
		return n, injected
	}
	return n, nil
}

func (h *writeHandle) Sync(ctx context.Context) error {
	location := h.location
	if err := h.provider.checkContext(ctx, "sync_write", &location); err != nil {
		return err
	}
	effect, err := h.provider.recordOperation(ctx, OperationSyncWrite, &location)
	if err != nil {
		return err
	}
	disconnectedAt, err := h.provider.disconnectTime(ctx, "sync_write", &location, effect)
	if err != nil {
		return err
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	if err := h.provider.checkContext(ctx, "sync_write", &location); err != nil {
		return err
	}
	if h.closed {
		return h.provider.error(
			domain.CodeInvalidArgument,
			"sync_write",
			&location,
			"write handle is closed",
			io.ErrClosedPipe,
		)
	}
	h.provider.mu.Lock()
	defer h.provider.mu.Unlock()
	if err := h.provider.checkContext(ctx, "sync_write", &location); err != nil {
		return err
	}
	if err := h.provider.checkHandleSessionLocked(
		"sync_write",
		&location,
		h.sessionEpoch,
	); err != nil {
		return err
	}
	if effect.Disconnect {
		h.provider.applyDisconnectLocked(disconnectedAt)
	}
	if err := h.provider.checkPersistentStateLocked("sync_write", &location, "write"); err != nil {
		return err
	}
	if err := h.provider.checkNodePermissionLocked("sync_write", &location, h.node); err != nil {
		return err
	}
	return nil
}

func (h *writeHandle) Close(ctx context.Context) error {
	location := h.location
	if err := h.provider.checkContext(ctx, "close_write", &location); err != nil {
		return err
	}
	if _, err := h.provider.recordOperation(ctx, OperationCloseWrite, &location); err != nil {
		return err
	}
	h.mu.Lock()
	if err := h.provider.checkContext(ctx, "close_write", &location); err != nil {
		h.mu.Unlock()
		return err
	}
	h.closed = true
	h.mu.Unlock()
	return nil
}

func (p *Provider) Mkdir(
	ctx context.Context,
	request providerapi.MkdirRequest,
) (domain.Entry, error) {
	if err := p.checkContext(ctx, "mkdir", &request.Location); err != nil {
		return domain.Entry{}, err
	}
	if err := providerapi.ValidateMkdirRequest(p.endpoint.ID, request); err != nil {
		return domain.Entry{}, err
	}
	canonical, err := canonicalizeAbsolute(string(request.Location.Path))
	if err != nil || canonical != string(request.Location.Path) || request.Location.Path == "/" {
		return domain.Entry{}, p.error(
			domain.CodeInvalidArgument,
			"mkdir",
			&request.Location,
			"directory location must be a canonical non-root path",
			err,
		)
	}
	effect, err := p.recordOperation(ctx, OperationMkdir, &request.Location)
	if err != nil {
		return domain.Entry{}, err
	}
	disconnectedAt, err := p.disconnectTime(ctx, "mkdir", &request.Location, effect)
	if err != nil {
		return domain.Entry{}, err
	}
	parentLocation, name, _ := parentPath(request.Location.Path)
	p.mu.Lock()
	defer p.mu.Unlock()
	if err := p.checkContext(ctx, "mkdir", &request.Location); err != nil {
		return domain.Entry{}, err
	}
	if effect.Disconnect {
		p.applyDisconnectLocked(disconnectedAt)
	}
	if err := p.checkPersistentStateLocked("mkdir", &request.Location, "write"); err != nil {
		return domain.Entry{}, err
	}
	if err := p.checkPathPermissionLocked(
		"mkdir",
		&request.Location,
		parentLocation,
		true,
	); err != nil {
		return domain.Entry{}, err
	}
	parent, err := resolveNode(p.root, parentLocation, true)
	if err != nil {
		return domain.Entry{}, p.lookupError("mkdir", &request.Location, err)
	}
	if parent.kind != domain.EntryDirectory {
		return domain.Entry{}, p.error(
			domain.CodeNotFound,
			"mkdir",
			&request.Location,
			"parent directory was not found",
			nil,
		)
	}
	if existing, exists := parent.children[name]; exists {
		if !request.Exclusive && existing.kind == domain.EntryDirectory {
			return p.entryLocked(existing, request.Location, name), nil
		}
		return domain.Entry{}, p.error(
			domain.CodeAlreadyExists,
			"mkdir",
			&request.Location,
			"path already exists",
			nil,
		)
	}
	node := &treeNode{
		id:                p.nextNodeID,
		version:           1,
		listingGeneration: 1,
		name:              name,
		kind:              domain.EntryDirectory,
		children:          make(map[string]*treeNode),
	}
	p.nextNodeID++
	parent.children[name] = node
	p.seedHistoryLocked(node, request.Location.Path)
	p.bumpDirectoryLocked(parent)
	return p.entryLocked(node, request.Location, name), nil
}

func (p *Provider) Rename(
	ctx context.Context,
	request providerapi.RenameRequest,
) (providerapi.RenameResult, error) {
	if err := p.checkContext(ctx, "rename", &request.Source); err != nil {
		return providerapi.RenameResult{}, err
	}
	if err := providerapi.ValidateRenameRequest(p.endpoint.ID, request); err != nil {
		return providerapi.RenameResult{}, err
	}
	sourceCanonical, sourceErr := canonicalizeAbsolute(string(request.Source.Path))
	destinationCanonical, destinationErr := canonicalizeAbsolute(string(request.Destination.Path))
	if sourceErr != nil || destinationErr != nil ||
		sourceCanonical != string(request.Source.Path) ||
		destinationCanonical != string(request.Destination.Path) ||
		request.Source.Path == "/" || request.Destination.Path == "/" {
		return providerapi.RenameResult{}, p.error(
			domain.CodeInvalidArgument,
			"rename",
			&request.Source,
			"rename locations must be canonical non-root paths",
			errors.Join(sourceErr, destinationErr),
		)
	}
	effect, err := p.recordOperation(ctx, OperationRename, &request.Source)
	if err != nil {
		return providerapi.RenameResult{}, err
	}
	disconnectedAt, err := p.disconnectTime(ctx, "rename", &request.Source, effect)
	if err != nil {
		return providerapi.RenameResult{}, err
	}
	sourceParentPath, sourceName, _ := parentPath(request.Source.Path)
	destinationParentPath, destinationName, _ := parentPath(request.Destination.Path)

	p.mu.Lock()
	defer p.mu.Unlock()
	if err := p.checkContext(ctx, "rename", &request.Source); err != nil {
		return providerapi.RenameResult{}, err
	}
	if effect.Disconnect {
		p.applyDisconnectLocked(disconnectedAt)
	}
	if err := p.checkPersistentStateLocked("rename", &request.Source, "write"); err != nil {
		return providerapi.RenameResult{}, err
	}
	if err := p.checkPathPermissionLocked(
		"rename",
		&request.Source,
		request.Source.Path,
		false,
	); err != nil {
		return providerapi.RenameResult{}, err
	}
	if err := p.checkPathPermissionLocked(
		"rename",
		&request.Destination,
		destinationParentPath,
		true,
	); err != nil {
		return providerapi.RenameResult{}, err
	}
	if request.Replace {
		if err := p.checkPathPermissionLocked(
			"rename",
			&request.Destination,
			request.Destination.Path,
			false,
		); err != nil {
			return providerapi.RenameResult{}, err
		}
	}
	sourceParent, err := resolveNode(p.root, sourceParentPath, true)
	if err != nil || sourceParent.kind != domain.EntryDirectory {
		if err == nil {
			err = errTreeNotDirectory
		}
		return providerapi.RenameResult{}, p.lookupError("rename", &request.Source, err)
	}
	sourceNode, exists := sourceParent.children[sourceName]
	if !exists {
		return providerapi.RenameResult{}, p.lookupError(
			"rename",
			&request.Source,
			errTreeNotFound,
		)
	}
	if request.ExpectedSource != nil &&
		!reflect.DeepEqual(*request.ExpectedSource, p.fingerprintLocked(sourceNode)) {
		return providerapi.RenameResult{}, p.opError(
			domain.CodeConflict,
			"rename",
			&request.Source,
			"source fingerprint does not match",
			domain.RetryAfterConflict,
			nil,
		)
	}
	if sourceNode.kind == domain.EntryDirectory && strings.HasPrefix(
		string(request.Destination.Path),
		string(request.Source.Path)+"/",
	) {
		return providerapi.RenameResult{}, p.error(
			domain.CodeInvalidArgument,
			"rename",
			&request.Source,
			"directory cannot be moved into its descendant",
			nil,
		)
	}

	destinationParent, err := resolveNode(p.root, destinationParentPath, true)
	if err != nil || destinationParent.kind != domain.EntryDirectory {
		if err == nil {
			err = errTreeNotDirectory
		}
		return providerapi.RenameResult{}, p.lookupError("rename", &request.Destination, err)
	}
	if sourceNode.kind == domain.EntryDirectory && containsNode(sourceNode, destinationParent) {
		return providerapi.RenameResult{}, p.error(
			domain.CodeInvalidArgument,
			"rename",
			&request.Source,
			"directory cannot be moved into its descendant",
			nil,
		)
	}
	destinationNode, destinationExists := destinationParent.children[destinationName]
	if request.ExpectedDestination != nil {
		if !destinationExists ||
			!reflect.DeepEqual(*request.ExpectedDestination, p.fingerprintLocked(destinationNode)) {
			return providerapi.RenameResult{}, p.opError(
				domain.CodeConflict,
				"rename",
				&request.Destination,
				"destination fingerprint does not match",
				domain.RetryAfterConflict,
				nil,
			)
		}
	}
	if destinationExists && destinationNode == sourceNode {
		return providerapi.RenameResult{Atomic: true}, nil
	}
	if request.Source == request.Destination {
		return providerapi.RenameResult{Atomic: true}, nil
	}
	if destinationExists {
		if !request.Replace {
			return providerapi.RenameResult{}, p.error(
				domain.CodeAlreadyExists,
				"rename",
				&request.Destination,
				"destination already exists",
				nil,
			)
		}
		if (sourceNode.kind == domain.EntryDirectory) !=
			(destinationNode.kind == domain.EntryDirectory) {
			return providerapi.RenameResult{}, p.opError(
				domain.CodeConflict,
				"rename",
				&request.Destination,
				"source and destination kinds are incompatible",
				domain.RetryAfterConflict,
				nil,
			)
		}
		if destinationNode.kind == domain.EntryDirectory && len(destinationNode.children) != 0 {
			return providerapi.RenameResult{}, p.opError(
				domain.CodeConflict,
				"rename",
				&request.Destination,
				"destination directory is not empty",
				domain.RetryAfterConflict,
				nil,
			)
		}
	}

	delete(sourceParent.children, sourceName)
	if destinationExists {
		delete(destinationParent.children, destinationName)
	}
	sourceNode.name = destinationName
	destinationParent.children[destinationName] = sourceNode
	p.bumpDirectoryLocked(sourceParent)
	if sourceParent != destinationParent {
		p.bumpDirectoryLocked(destinationParent)
	}
	return providerapi.RenameResult{
		Atomic:   !effect.NonAtomicRename,
		Replaced: destinationExists,
	}, nil
}

func (p *Provider) Remove(ctx context.Context, request providerapi.RemoveRequest) error {
	if err := p.checkContext(ctx, "remove", &request.Location); err != nil {
		return err
	}
	if err := providerapi.ValidateRemoveRequest(p.endpoint.ID, request); err != nil {
		return err
	}
	canonical, err := canonicalizeAbsolute(string(request.Location.Path))
	if err != nil || canonical != string(request.Location.Path) || request.Location.Path == "/" {
		return p.error(
			domain.CodeInvalidArgument,
			"remove",
			&request.Location,
			"remove location must be a canonical non-root path",
			err,
		)
	}
	effect, err := p.recordOperation(ctx, OperationRemove, &request.Location)
	if err != nil {
		return err
	}
	disconnectedAt, err := p.disconnectTime(ctx, "remove", &request.Location, effect)
	if err != nil {
		return err
	}
	parentLocation, name, _ := parentPath(request.Location.Path)
	p.mu.Lock()
	defer p.mu.Unlock()
	if err := p.checkContext(ctx, "remove", &request.Location); err != nil {
		return err
	}
	if effect.Disconnect {
		p.applyDisconnectLocked(disconnectedAt)
	}
	if err := p.checkPersistentStateLocked("remove", &request.Location, "write"); err != nil {
		return err
	}
	if err := p.checkPathPermissionLocked(
		"remove",
		&request.Location,
		request.Location.Path,
		false,
	); err != nil {
		return err
	}
	parent, err := resolveNode(p.root, parentLocation, true)
	if err != nil || parent.kind != domain.EntryDirectory {
		if err == nil {
			err = errTreeNotDirectory
		}
		return p.lookupError("remove", &request.Location, err)
	}
	node, exists := parent.children[name]
	if !exists {
		return p.lookupError("remove", &request.Location, errTreeNotFound)
	}
	if request.Expected != nil &&
		!reflect.DeepEqual(*request.Expected, p.fingerprintLocked(node)) {
		return p.opError(
			domain.CodeConflict,
			"remove",
			&request.Location,
			"fingerprint does not match",
			domain.RetryAfterConflict,
			nil,
		)
	}
	if node.kind == domain.EntryDirectory && len(node.children) != 0 {
		return p.opError(
			domain.CodeConflict,
			"remove",
			&request.Location,
			"directory is not empty",
			domain.RetryAfterConflict,
			nil,
		)
	}
	delete(parent.children, name)
	p.bumpDirectoryLocked(parent)
	return nil
}

func (p *Provider) disconnectTime(
	ctx context.Context,
	operation string,
	location *domain.Location,
	effect FaultEffect,
) (time.Time, error) {
	if !effect.Disconnect {
		return time.Time{}, nil
	}
	observedAt := p.clock.Now()
	if err := p.checkContext(ctx, operation, location); err != nil {
		return time.Time{}, err
	}
	return observedAt, nil
}

func (p *Provider) applyDisconnectLocked(observedAt time.Time) {
	p.snapshot.State = domain.StateDisconnected
	p.snapshot.ObservedAt = observedAt
	clear(p.cursors)
}

func (p *Provider) checkHandleSessionLocked(
	operation string,
	location *domain.Location,
	sessionEpoch uint64,
) error {
	if sessionEpoch == p.sessionEpoch {
		return nil
	}
	return p.opError(
		domain.CodeConflict,
		operation,
		location,
		"handle belongs to an earlier provider session",
		domain.RetryAfterReplan,
		nil,
	)
}

func (p *Provider) checkPersistentStateLocked(
	operation string,
	location *domain.Location,
	capability domain.CapabilityName,
) error {
	switch p.snapshot.State {
	case domain.StateReady, domain.StateDegraded:
	case domain.StateAuthRequired:
		return p.opError(
			domain.CodeAuthRequired,
			operation,
			location,
			"endpoint authentication is required",
			domain.RetryAfterAuth,
			nil,
		)
	case domain.StateConnecting, domain.StateDisconnected, domain.StateFailed:
		return p.opError(
			domain.CodeTransportInterrupted,
			operation,
			location,
			"endpoint connection is not operational",
			domain.RetryAfterReconnect,
			nil,
		)
	default:
		return p.opError(
			domain.CodeInternal,
			operation,
			location,
			"endpoint connection state is invalid",
			domain.RetryNever,
			nil,
		)
	}
	if _, lost := p.capabilityLost[capability]; !lost {
		return nil
	}
	return p.opError(
		domain.CodeCapabilityLost,
		operation,
		location,
		fmt.Sprintf("required capability %q was withdrawn", capability),
		domain.RetryAfterReplan,
		nil,
	)
}

func (p *Provider) checkPathPermissionLocked(
	operation string,
	location *domain.Location,
	path domain.CanonicalPath,
	followFinal bool,
) error {
	_, err := resolveNodeWithPermission(p.root, path, followFinal)
	if !errors.Is(err, errTreePermission) {
		return nil
	}
	return p.permissionError(operation, location)
}

func (p *Provider) checkNodePermissionLocked(
	operation string,
	location *domain.Location,
	node *treeNode,
) error {
	if node == nil || !node.permissionDenied {
		return nil
	}
	return p.permissionError(operation, location)
}

func (p *Provider) permissionError(operation string, location *domain.Location) error {
	return p.opError(
		domain.CodePermissionDenied,
		operation,
		location,
		"permission denied",
		domain.RetryNever,
		nil,
	)
}

// scrubResolvedKindLocked applies authorization at return to an owned Entry.
// It follows the Entry copy's frozen RawTarget, not the current lexical link,
// and clears only for an actual permission denial. Structural drift deliberately
// leaves the frozen ResolvedKind untouched.
func (p *Provider) scrubResolvedKindLocked(entry *domain.Entry) {
	if entry == nil || entry.Symlink == nil || entry.Symlink.ResolvedKind == nil {
		return
	}
	parent, _, err := parentPath(entry.Location.Path)
	if err != nil {
		return
	}
	target, err := resolveLinkTarget(parent, entry.Symlink.RawTarget)
	if err != nil {
		return
	}
	_, err = resolveNodeWithPermission(p.root, target, true)
	if errors.Is(err, errTreePermission) {
		entry.Symlink.ResolvedKind = nil
	}
}

func (p *Provider) lookupError(
	operation string,
	location *domain.Location,
	err error,
) error {
	switch {
	case errors.Is(err, errTreeNotFound), errors.Is(err, errTreeNotDirectory):
		return p.error(domain.CodeNotFound, operation, location, "path was not found", err)
	case errors.Is(err, errSymlinkLoop):
		return p.opError(
			domain.CodeConflict,
			operation,
			location,
			"symlink resolution loop",
			domain.RetryAfterConflict,
			err,
		)
	case errors.Is(err, errSymlinkEscapes):
		return p.error(
			domain.CodeInvalidArgument,
			operation,
			location,
			"symlink target escapes endpoint root",
			err,
		)
	default:
		return p.error(domain.CodeInternal, operation, location, "tree lookup failed", err)
	}
}

func (p *Provider) entryLocked(
	node *treeNode,
	location domain.Location,
	name string,
) domain.Entry {
	entry := domain.Entry{
		Location:    location,
		Name:        name,
		Kind:        node.kind,
		Metadata:    cloneMetadata(node.metadata),
		Fingerprint: p.fingerprintLocked(node),
	}
	if node.kind == domain.EntrySymlink {
		entry.Symlink = &domain.SymlinkInfo{RawTarget: node.linkTarget}
		if target, err := resolveNode(p.root, location.Path, true); err == nil {
			resolvedKind := target.kind
			entry.Symlink.ResolvedKind = &resolvedKind
		}
	}
	return entry
}

func (p *Provider) fingerprintLocked(node *treeNode) domain.Fingerprint {
	versionID := fmt.Sprintf("fake:%d:%d:%d", p.cursorNamespace, node.id, node.version)
	return domain.Fingerprint{
		Size:              clonePointer(node.metadata.Size),
		ModifiedAt:        clonePointer(node.metadata.ModifiedAt),
		ModifiedPrecision: clonePointer(node.metadata.ModifiedPrecision),
		FileID:            clonePointer(node.metadata.FileID),
		VersionID:         &versionID,
	}
}

func cloneEntry(entry domain.Entry) domain.Entry {
	cloned := entry
	cloned.Metadata = cloneMetadata(entry.Metadata)
	cloned.Fingerprint = cloneFingerprint(entry.Fingerprint)
	if entry.Symlink != nil {
		symlink := *entry.Symlink
		symlink.ResolvedKind = clonePointer(entry.Symlink.ResolvedKind)
		cloned.Symlink = &symlink
	}
	return cloned
}

func cloneReadInfo(info providerapi.ReadInfo) providerapi.ReadInfo {
	return providerapi.ReadInfo{
		Entry:       cloneEntry(info.Entry),
		Fingerprint: cloneFingerprint(info.Fingerprint),
	}
}

func (p *Provider) seedHistoryLocked(node *treeNode, path domain.CanonicalPath) {
	p.recordHistoryLocked(node, &path)
	for name, child := range node.children {
		p.seedHistoryLocked(child, joinCanonical(path, name))
	}
}

func (p *Provider) recordHistoryLocked(node *treeNode, knownPath *domain.CanonicalPath) {
	key := nodeRevisionKey{nodeID: node.id, revision: node.version}
	if _, exists := p.history[key]; exists {
		return
	}
	path := knownPath
	if path == nil {
		if attachedPath, attached := findNodePath(p.root, node, "/"); attached {
			path = &attachedPath
		}
	}
	snapshot := historicalStat{
		kind:        node.kind,
		metadata:    cloneMetadata(node.metadata),
		fingerprint: p.fingerprintLocked(node),
	}
	if node.kind == domain.EntrySymlink {
		snapshot.symlink = &domain.SymlinkInfo{RawTarget: node.linkTarget}
		if path != nil {
			if target, err := resolveNode(p.root, *path, true); err == nil {
				resolvedKind := target.kind
				snapshot.symlink.ResolvedKind = &resolvedKind
			}
		}
	}
	p.history[key] = snapshot
}

func (p *Provider) historicalEntryLocked(
	node *treeNode,
	revision uint64,
	location domain.Location,
	name string,
) (domain.Entry, bool) {
	snapshot, exists := p.history[nodeRevisionKey{nodeID: node.id, revision: revision}]
	if !exists {
		return domain.Entry{}, false
	}
	// Permission-aware ResolvedKind scrubbing happens after this owned rebase;
	// the immutable stored snapshot must never be scrubbed in place.
	return domain.Entry{
		Location:    location,
		Name:        name,
		Kind:        snapshot.kind,
		Metadata:    cloneMetadata(snapshot.metadata),
		Fingerprint: cloneFingerprint(snapshot.fingerprint),
		Symlink:     cloneSymlinkInfo(snapshot.symlink),
	}, true
}

func cloneSymlinkInfo(info *domain.SymlinkInfo) *domain.SymlinkInfo {
	if info == nil {
		return nil
	}
	cloned := *info
	cloned.ResolvedKind = clonePointer(info.ResolvedKind)
	return &cloned
}

func (p *Provider) touchNodeLocked(node *treeNode, modifiedAt time.Time) {
	node.version++
	if node.kind == domain.EntryFile {
		size := uint64(len(node.data))
		node.metadata.Size = &size
	}
	precision := domain.TimePrecision("nanosecond")
	node.metadata.ModifiedAt = &modifiedAt
	node.metadata.ModifiedPrecision = &precision
	p.recordHistoryLocked(node, nil)
}

func (p *Provider) bumpDirectoryLocked(directory *treeNode) {
	directory.version++
	directory.listingGeneration++
	p.recordHistoryLocked(directory, nil)
}

func (p *Provider) bumpParentOfNodeLocked(node *treeNode) {
	path, attached := findNodePath(p.root, node, "/")
	if !attached || path == "/" {
		return
	}
	parent, _, err := parentPath(path)
	if err != nil {
		return
	}
	directory, err := resolveNode(p.root, parent, false)
	if err != nil || directory.kind != domain.EntryDirectory {
		return
	}
	p.bumpDirectoryLocked(directory)
}

func equalSortHint(left *providerapi.SortHint, right *providerapi.SortHint) bool {
	if left == nil || right == nil {
		return left == nil && right == nil
	}
	return *left == *right
}

func cloneSortHint(sortHint *providerapi.SortHint) *providerapi.SortHint {
	if sortHint == nil {
		return nil
	}
	cloned := *sortHint
	return &cloned
}

func (p *Provider) checkContext(
	ctx context.Context,
	operation string,
	location *domain.Location,
) error {
	if ctx == nil {
		return p.error(
			domain.CodeInvalidArgument,
			operation,
			location,
			"context is nil",
			nil,
		)
	}
	if err := ctx.Err(); err != nil {
		code := domain.CodeCanceled
		retry := domain.RetryNever
		message := "operation canceled"
		if err == context.DeadlineExceeded {
			code = domain.CodeTimeout
			retry = domain.RetryBackoff
			message = "operation timed out"
		}
		return p.opError(code, operation, location, message, retry, err)
	}
	return nil
}

func (p *Provider) error(
	code domain.Code,
	operation string,
	location *domain.Location,
	message string,
	cause error,
) error {
	return p.opError(
		code,
		operation,
		location,
		message,
		domain.RetryNever,
		cause,
	)
}

func (p *Provider) opError(
	code domain.Code,
	operation string,
	location *domain.Location,
	message string,
	retry domain.RetryKind,
	cause error,
) error {
	return &domain.OpError{
		Code:       code,
		Message:    message,
		Operation:  operation,
		EndpointID: p.endpoint.ID,
		Location:   cloneLocation(location),
		Retry:      domain.RetryAdvice{Kind: retry},
		Effect:     domain.EffectNone,
		Cause:      cause,
	}
}

func canonicalizeAbsolute(value string) (string, error) {
	if value == "" || !strings.HasPrefix(value, "/") || strings.IndexByte(value, 0) >= 0 {
		return "", fmt.Errorf("invalid absolute path")
	}
	components, err := applyPath(nil, value)
	if err != nil {
		return "", err
	}
	if len(components) == 0 {
		return "/", nil
	}
	return "/" + strings.Join(components, "/"), nil
}

func applyPath(base []string, value string) ([]string, error) {
	components := append([]string(nil), base...)
	for _, component := range strings.Split(value, "/") {
		switch component {
		case "", ".":
			continue
		case "..":
			if len(components) == 0 {
				return nil, fmt.Errorf("parent traversal above root")
			}
			components = components[:len(components)-1]
		default:
			components = append(components, component)
		}
	}
	return components, nil
}

func splitCanonical(value string) []string {
	if value == "/" {
		return nil
	}
	return strings.Split(strings.TrimPrefix(value, "/"), "/")
}

func cloneSnapshot(snapshot domain.EndpointSnapshot) domain.EndpointSnapshot {
	cloned := snapshot
	cloned.Capabilities = cloneCapabilitySnapshot(snapshot.Capabilities)
	return cloned
}

func cloneCapabilitySnapshot(snapshot domain.CapabilitySnapshot) domain.CapabilitySnapshot {
	cloned := snapshot
	if snapshot.Items == nil {
		return cloned
	}
	cloned.Items = make([]domain.Capability, len(snapshot.Items))
	for index, capability := range snapshot.Items {
		cloned.Items[index] = capability
		if capability.Constraints != nil {
			cloned.Items[index].Constraints = append(
				[]domain.CapabilityConstraint(nil),
				capability.Constraints...,
			)
		}
	}
	return cloned
}

func cloneLocation(location *domain.Location) *domain.Location {
	if location == nil {
		return nil
	}
	cloned := *location
	return &cloned
}
