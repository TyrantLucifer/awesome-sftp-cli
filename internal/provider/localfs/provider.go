package localfs

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"time"

	"github.com/TyrantLucifer/awesome-sftp-cli/internal/domain"
	providerapi "github.com/TyrantLucifer/awesome-sftp-cli/internal/provider"
)

const defaultMaxCursors = 64

type Config struct {
	Endpoint   domain.Endpoint
	SessionID  domain.SessionID
	Root       string
	Now        func() time.Time
	MaxCursors int
}

type cursorState struct {
	sequence    uint64
	location    domain.Location
	sort        *providerapi.SortHint
	directory   *os.File
	fingerprint domain.Fingerprint
}

type Provider struct {
	mu sync.Mutex

	endpoint   domain.Endpoint
	snapshot   domain.EndpointSnapshot
	root       string
	rootHandle *os.Root
	maxCursors int
	nextCursor uint64
	cursors    map[providerapi.PageCursor]*cursorState
	closed     bool
}

var _ providerapi.Provider = (*Provider)(nil)

func New(config Config) (*Provider, error) {
	if config.Endpoint.ID == "" {
		return nil, errors.New("create local provider: endpoint ID is empty")
	}
	if config.Endpoint.Kind != domain.EndpointLocal {
		return nil, errors.New("create local provider: endpoint kind is not local")
	}
	if config.SessionID == "" {
		return nil, errors.New("create local provider: session ID is empty")
	}
	if !filepath.IsAbs(config.Root) || filepath.Clean(config.Root) != config.Root {
		return nil, errors.New("create local provider: root must be a clean absolute path")
	}
	info, err := os.Lstat(config.Root)
	if err != nil {
		return nil, fmt.Errorf("create local provider: inspect root: %w", err)
	}
	if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return nil, errors.New("create local provider: root is not a real directory")
	}
	capabilities, err := domain.NewCapabilitySnapshot(domain.CapabilityRevision{
		SessionID:  config.SessionID,
		Generation: 1,
	}, true, []domain.Capability{
		{Name: "read", Version: 1},
		{Name: "write", Version: 1},
	})
	if err != nil {
		return nil, fmt.Errorf("create local provider: capabilities: %w", err)
	}
	now := config.Now
	if now == nil {
		now = time.Now
	}
	maxCursors := config.MaxCursors
	if maxCursors == 0 {
		maxCursors = defaultMaxCursors
	}
	if maxCursors < 1 {
		return nil, errors.New("create local provider: maximum cursors must be positive")
	}
	rootHandle, err := os.OpenRoot(config.Root)
	if err != nil {
		return nil, fmt.Errorf("create local provider: open rooted filesystem handle: %w", err)
	}
	return &Provider{
		endpoint: config.Endpoint,
		snapshot: domain.EndpointSnapshot{
			EndpointID:   config.Endpoint.ID,
			SessionID:    config.SessionID,
			State:        domain.StateReady,
			Capabilities: capabilities,
			ObservedAt:   now(),
		},
		root:       config.Root,
		rootHandle: rootHandle,
		maxCursors: maxCursors,
		cursors:    make(map[providerapi.PageCursor]*cursorState),
	}, nil
}

func (p *Provider) Descriptor() domain.Endpoint {
	return p.endpoint
}

func (p *Provider) Snapshot(ctx context.Context) (domain.EndpointSnapshot, error) {
	if err := p.checkContext(ctx, "snapshot", nil); err != nil {
		return domain.EndpointSnapshot{}, err
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.closed {
		return domain.EndpointSnapshot{}, p.closedError("snapshot", nil)
	}
	return p.snapshot, nil
}

func (p *Provider) Normalize(ctx context.Context, request domain.NormalizeRequest) (domain.Location, error) {
	if err := p.checkContext(ctx, "normalize", nil); err != nil {
		return domain.Location{}, err
	}
	if request.EndpointID != p.endpoint.ID {
		return domain.Location{}, p.invalid("normalize", nil, "endpoint does not match provider")
	}
	if request.Input == "" || strings.IndexByte(request.Input, 0) >= 0 {
		return domain.Location{}, p.invalid("normalize", nil, "path is empty or contains NUL")
	}
	var canonical string
	if path.IsAbs(request.Input) {
		canonical = path.Clean(request.Input)
	} else {
		if request.Base == nil || request.Base.EndpointID != p.endpoint.ID ||
			!path.IsAbs(string(request.Base.Path)) {
			return domain.Location{}, p.invalid("normalize", request.Base, "relative path requires a canonical base")
		}
		canonical = path.Clean(path.Join(string(request.Base.Path), request.Input))
	}
	if !path.IsAbs(canonical) || strings.IndexByte(canonical, 0) >= 0 {
		return domain.Location{}, p.invalid("normalize", nil, "path is not canonical absolute")
	}
	return domain.NewLocation(p.endpoint.ID, domain.CanonicalPath(canonical))
}

func (p *Provider) List(ctx context.Context, request providerapi.ListRequest) (providerapi.ListPage, error) {
	if err := p.checkContext(ctx, "list", &request.Location); err != nil {
		return providerapi.ListPage{}, err
	}
	if err := providerapi.ValidateListRequest(p.endpoint.ID, request); err != nil {
		return providerapi.ListPage{}, err
	}
	if err := p.validateCanonical(request.Location, "list"); err != nil {
		return providerapi.ListPage{}, err
	}

	state, err := p.listingState(request)
	if err != nil {
		return providerapi.ListPage{}, err
	}
	keep := false
	defer func() {
		if !keep {
			_ = state.directory.Close()
		}
	}()

	currentInfo, err := os.Stat(p.hostPath(request.Location))
	if err != nil {
		return providerapi.ListPage{}, p.mapError("list", &request.Location, err)
	}
	if !currentInfo.IsDir() || !reflect.DeepEqual(state.fingerprint, fingerprint(currentInfo)) {
		return providerapi.ListPage{}, p.opError(
			domain.CodeConflict,
			"list",
			&request.Location,
			"listed directory changed",
			domain.RetryAfterConflict,
			nil,
		)
	}

	directoryEntries, readErr := state.directory.ReadDir(int(request.Limit))
	if readErr != nil && !errors.Is(readErr, io.EOF) {
		return providerapi.ListPage{}, p.mapError("list", &request.Location, readErr)
	}
	entries := make([]domain.Entry, 0, len(directoryEntries))
	for _, directoryEntry := range directoryEntries {
		if err := p.checkContext(ctx, "list", &request.Location); err != nil {
			return providerapi.ListPage{}, err
		}
		entry, err := p.entryFromDirEntry(request.Location, directoryEntry)
		if err != nil {
			return providerapi.ListPage{}, p.mapError("list", &request.Location, err)
		}
		entries = append(entries, entry)
	}
	done := errors.Is(readErr, io.EOF)
	var next providerapi.PageCursor
	if !done {
		next, err = p.storeCursor(state)
		if err != nil {
			return providerapi.ListPage{}, err
		}
		keep = true
	}
	return providerapi.ListPage{
		Entries:              entries,
		NextCursor:           next,
		Done:                 done,
		RequestedSortApplied: false,
		Consistency:          providerapi.ConsistencyBestEffort,
		DirectoryFingerprint: state.fingerprint,
	}, nil
}

func (p *Provider) Stat(ctx context.Context, request providerapi.StatRequest) (domain.Entry, error) {
	if err := p.checkContext(ctx, "stat", &request.Location); err != nil {
		return domain.Entry{}, err
	}
	if err := providerapi.ValidateStatRequest(p.endpoint.ID, request); err != nil {
		return domain.Entry{}, err
	}
	if err := p.validateCanonical(request.Location, "stat"); err != nil {
		return domain.Entry{}, err
	}
	hostPath := p.hostPath(request.Location)
	var info os.FileInfo
	var err error
	if request.FollowSymlinks {
		info, err = os.Stat(hostPath)
	} else {
		info, err = os.Lstat(hostPath)
	}
	if err != nil {
		return domain.Entry{}, p.mapError("stat", &request.Location, err)
	}
	return p.entryFromInfo(request.Location, path.Base(string(request.Location.Path)), hostPath, info), nil
}

func (p *Provider) OpenRead(ctx context.Context, request providerapi.OpenReadRequest) (providerapi.ReadHandle, error) {
	if err := p.checkContext(ctx, "open_read", &request.Location); err != nil {
		return nil, err
	}
	if err := providerapi.ValidateOpenReadRequest(p.endpoint.ID, request); err != nil {
		return nil, err
	}
	if err := p.validateCanonical(request.Location, "open_read"); err != nil {
		return nil, err
	}
	file, err := os.Open(p.hostPath(request.Location))
	if err != nil {
		return nil, p.mapError("open_read", &request.Location, err)
	}
	info, err := file.Stat()
	if err != nil {
		_ = file.Close()
		return nil, p.mapError("open_read", &request.Location, err)
	}
	if !info.Mode().IsRegular() {
		_ = file.Close()
		return nil, p.invalid("open_read", &request.Location, "location is not a regular file")
	}
	entry := p.entryFromInfo(request.Location, path.Base(string(request.Location.Path)), p.hostPath(request.Location), info)
	if request.ExpectedFingerprint != nil && !reflect.DeepEqual(*request.ExpectedFingerprint, entry.Fingerprint) {
		_ = file.Close()
		return nil, p.opError(
			domain.CodeConflict,
			"open_read",
			&request.Location,
			"fingerprint does not match",
			domain.RetryAfterConflict,
			nil,
		)
	}
	if _, err := file.Seek(request.Offset, io.SeekStart); err != nil {
		_ = file.Close()
		return nil, p.mapError("open_read", &request.Location, err)
	}
	var remaining *int64
	if request.Limit != nil {
		ownedLimit := *request.Limit
		remaining = &ownedLimit
	}
	return &readHandle{
		provider:  p,
		file:      file,
		info:      providerapi.ReadInfo{Entry: entry, Fingerprint: entry.Fingerprint},
		location:  request.Location,
		remaining: remaining,
	}, nil
}

func (p *Provider) DiscardCursor(cursor providerapi.PageCursor) error {
	if cursor == "" {
		return nil
	}
	p.mu.Lock()
	state, ok := p.cursors[cursor]
	if ok {
		delete(p.cursors, cursor)
	}
	p.mu.Unlock()
	if !ok {
		return p.invalid("discard_cursor", nil, "cursor is unknown")
	}
	return state.directory.Close()
}

func (p *Provider) Close() error {
	p.mu.Lock()
	if p.closed {
		p.mu.Unlock()
		return nil
	}
	p.closed = true
	states := make([]*cursorState, 0, len(p.cursors))
	for _, state := range p.cursors {
		states = append(states, state)
	}
	p.cursors = make(map[providerapi.PageCursor]*cursorState)
	p.mu.Unlock()
	var result error
	for _, state := range states {
		result = errors.Join(result, state.directory.Close())
	}
	if p.rootHandle != nil {
		result = errors.Join(result, p.rootHandle.Close())
	}
	return result
}

func (p *Provider) listingState(request providerapi.ListRequest) (*cursorState, error) {
	if request.Cursor == "" {
		file, err := os.Open(p.hostPath(request.Location))
		if err != nil {
			return nil, p.mapError("list", &request.Location, err)
		}
		info, err := file.Stat()
		if err != nil {
			_ = file.Close()
			return nil, p.mapError("list", &request.Location, err)
		}
		if !info.IsDir() {
			_ = file.Close()
			return nil, p.invalid("list", &request.Location, "location is not a directory")
		}
		return &cursorState{
			location:    request.Location,
			sort:        cloneSort(request.Sort),
			directory:   file,
			fingerprint: fingerprint(info),
		}, nil
	}

	p.mu.Lock()
	defer p.mu.Unlock()
	if p.closed {
		return nil, p.closedError("list", &request.Location)
	}
	state, ok := p.cursors[request.Cursor]
	if !ok {
		return nil, p.invalid("list", &request.Location, "cursor was not issued by this provider")
	}
	if state.location != request.Location || !reflect.DeepEqual(state.sort, request.Sort) {
		return nil, p.invalid("list", &request.Location, "cursor binding does not match request")
	}
	delete(p.cursors, request.Cursor)
	return state, nil
}

func (p *Provider) storeCursor(state *cursorState) (providerapi.PageCursor, error) {
	p.mu.Lock()
	if p.closed {
		p.mu.Unlock()
		return "", p.closedError("list", &state.location)
	}
	p.nextCursor++
	state.sequence = p.nextCursor
	cursor := providerapi.PageCursor(fmt.Sprintf("local:%d", p.nextCursor))
	p.cursors[cursor] = state
	var evicted *cursorState
	if len(p.cursors) > p.maxCursors {
		var oldest providerapi.PageCursor
		for candidate, candidateState := range p.cursors {
			if candidate == cursor {
				continue
			}
			if evicted == nil || candidateState.sequence < evicted.sequence {
				oldest, evicted = candidate, candidateState
			}
		}
		if evicted != nil {
			delete(p.cursors, oldest)
		}
	}
	p.mu.Unlock()
	if evicted != nil {
		_ = evicted.directory.Close()
	}
	return cursor, nil
}

func (p *Provider) entryFromDirEntry(parent domain.Location, directoryEntry os.DirEntry) (domain.Entry, error) {
	name := directoryEntry.Name()
	location, err := domain.NewLocation(p.endpoint.ID, domain.CanonicalPath(path.Join(string(parent.Path), name)))
	if err != nil {
		return domain.Entry{}, err
	}
	hostPath := p.hostPath(location)
	info, err := os.Lstat(hostPath)
	if err != nil {
		return domain.Entry{}, err
	}
	return p.entryFromInfo(location, name, hostPath, info), nil
}

func (p *Provider) entryFromInfo(location domain.Location, name, hostPath string, info os.FileInfo) domain.Entry {
	entry := domain.Entry{
		Location:    location,
		Name:        name,
		Kind:        entryKind(info.Mode()),
		Metadata:    metadata(info),
		Fingerprint: fingerprint(info),
	}
	if info.Mode()&os.ModeSymlink != 0 {
		if target, err := os.Readlink(hostPath); err == nil {
			entry.Symlink = &domain.SymlinkInfo{RawTarget: target}
			if targetInfo, statErr := os.Stat(hostPath); statErr == nil {
				kind := entryKind(targetInfo.Mode())
				entry.Symlink.ResolvedKind = &kind
			}
		}
	}
	return entry
}

func (p *Provider) validateCanonical(location domain.Location, operation string) error {
	if location.EndpointID != p.endpoint.ID || !path.IsAbs(string(location.Path)) ||
		path.Clean(string(location.Path)) != string(location.Path) ||
		strings.IndexByte(string(location.Path), 0) >= 0 {
		return p.invalid(operation, &location, "location is not canonical for this provider")
	}
	return nil
}

func (p *Provider) hostPath(location domain.Location) string {
	if location.Path == "/" {
		return p.root
	}
	return filepath.Join(p.root, filepath.FromSlash(strings.TrimPrefix(string(location.Path), "/")))
}

func (p *Provider) checkContext(ctx context.Context, operation string, location *domain.Location) error {
	if err := ctx.Err(); err != nil {
		return domain.FromContext(operation, p.endpoint.ID, location, err)
	}
	return nil
}

func (p *Provider) invalid(operation string, location *domain.Location, message string) error {
	return p.opError(domain.CodeInvalidArgument, operation, location, message, domain.RetryNever, nil)
}

func (p *Provider) closedError(operation string, location *domain.Location) error {
	return p.opError(domain.CodeInternal, operation, location, "provider is closed", domain.RetryNever, os.ErrClosed)
}

func (p *Provider) mapError(operation string, location *domain.Location, err error) error {
	if err == nil {
		return nil
	}
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return domain.FromContext(operation, p.endpoint.ID, location, err)
	}
	code := domain.CodeInternal
	switch {
	case errors.Is(err, os.ErrNotExist):
		code = domain.CodeNotFound
	case errors.Is(err, os.ErrPermission):
		code = domain.CodePermissionDenied
	case errors.Is(err, os.ErrInvalid):
		code = domain.CodeInvalidArgument
	}
	return p.opError(code, operation, location, "local filesystem operation failed", domain.RetryNever, err)
}

func (p *Provider) opError(
	code domain.Code,
	operation string,
	location *domain.Location,
	message string,
	retry domain.RetryKind,
	cause error,
) error {
	var owned *domain.Location
	if location != nil {
		copy := *location
		owned = &copy
	}
	return &domain.OpError{
		Code:       code,
		Message:    message,
		Operation:  operation,
		EndpointID: p.endpoint.ID,
		Location:   owned,
		Retry:      domain.RetryAdvice{Kind: retry},
		Effect:     domain.EffectNone,
		Cause:      cause,
	}
}

func cloneSort(value *providerapi.SortHint) *providerapi.SortHint {
	if value == nil {
		return nil
	}
	copy := *value
	return &copy
}

func entryKind(mode os.FileMode) domain.EntryKind {
	switch {
	case mode.IsRegular():
		return domain.EntryFile
	case mode.IsDir():
		return domain.EntryDirectory
	case mode&os.ModeSymlink != 0:
		return domain.EntrySymlink
	default:
		return domain.EntryOther
	}
}

func metadata(info os.FileInfo) domain.Metadata {
	size := uint64(max(info.Size(), 0))
	mode := uint32(info.Mode())
	modified := info.ModTime().UTC()
	precision := domain.TimePrecision("nanosecond")
	uid, gid, id := platformMetadata(info)
	return domain.Metadata{
		Size:              &size,
		Mode:              &mode,
		UID:               uid,
		GID:               gid,
		ModifiedAt:        &modified,
		ModifiedPrecision: &precision,
		FileID:            id,
	}
}

func fingerprint(info os.FileInfo) domain.Fingerprint {
	metadata := metadata(info)
	return domain.Fingerprint{
		Size:              metadata.Size,
		ModifiedAt:        metadata.ModifiedAt,
		ModifiedPrecision: metadata.ModifiedPrecision,
		FileID:            metadata.FileID,
	}
}

type readHandle struct {
	mu sync.Mutex

	provider  *Provider
	file      *os.File
	info      providerapi.ReadInfo
	location  domain.Location
	remaining *int64
	closed    bool
}

var _ providerapi.ReadHandle = (*readHandle)(nil)

func (h *readHandle) Info() providerapi.ReadInfo {
	return h.info
}

func (h *readHandle) Read(ctx context.Context, destination []byte) (int, error) {
	if err := h.provider.checkContext(ctx, "read", &h.location); err != nil {
		return 0, err
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.closed {
		return 0, h.provider.invalid("read", &h.location, "read handle is closed")
	}
	if h.remaining != nil {
		if *h.remaining == 0 {
			return 0, io.EOF
		}
		if int64(len(destination)) > *h.remaining {
			destination = destination[:*h.remaining]
		}
	}
	n, err := h.file.Read(destination)
	if h.remaining != nil {
		*h.remaining -= int64(n)
	}
	if n > 0 && errors.Is(err, io.EOF) {
		err = nil
	}
	return n, err
}

func (h *readHandle) Close(ctx context.Context) error {
	if err := h.provider.checkContext(ctx, "close_read", &h.location); err != nil {
		return err
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.closed {
		return nil
	}
	h.closed = true
	if err := h.file.Close(); err != nil && !errors.Is(err, os.ErrClosed) {
		return h.provider.mapError("close_read", &h.location, err)
	}
	return nil
}
