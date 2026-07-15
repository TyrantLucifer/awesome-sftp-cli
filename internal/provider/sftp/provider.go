package sftp

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"path"
	"sync"
	"time"

	"github.com/TyrantLucifer/awesome-mac-sftp/internal/domain"
	providerapi "github.com/TyrantLucifer/awesome-mac-sftp/internal/provider"
	pkgsftp "github.com/pkg/sftp"
)

type Config struct {
	Endpoint   domain.Endpoint
	SessionID  domain.SessionID
	Client     *pkgsftp.Client
	MaxCursors int
	Root       string
	Close      func() error
}
type cursorState struct {
	location    domain.Location
	sort        *providerapi.SortHint
	entries     []os.FileInfo
	index       int
	fingerprint domain.Fingerprint
}
type Provider struct {
	mu         sync.Mutex
	endpoint   domain.Endpoint
	snapshot   domain.EndpointSnapshot
	client     *pkgsftp.Client
	root       string
	cursors    map[providerapi.PageCursor]*cursorState
	next       uint64
	maxCursors int
	closed     bool
	close      func() error
}

var _ providerapi.Provider = (*Provider)(nil)

func New(config Config) (*Provider, error) {
	if config.Endpoint.ID == "" || config.Endpoint.Kind != domain.EndpointSSH || config.Endpoint.SSHHostAlias == "" {
		return nil, errors.New("create SFTP provider: invalid SSH endpoint")
	}
	if config.SessionID == "" || config.Client == nil {
		return nil, errors.New("create SFTP provider: session ID and client are required")
	}
	root := config.Root
	if root == "" {
		root = "/"
	}
	if !path.IsAbs(root) || path.Clean(root) != root || containsNUL(root) {
		return nil, errors.New("create SFTP provider: root must be canonical absolute")
	}
	maximum := config.MaxCursors
	if maximum == 0 {
		maximum = 64
	}
	if maximum < 1 {
		return nil, errors.New("create SFTP provider: maximum cursors must be positive")
	}
	capabilities, err := domain.NewCapabilitySnapshot(domain.CapabilityRevision{SessionID: config.SessionID, Generation: 1}, true, []domain.Capability{{Name: "read", Version: 1}})
	if err != nil {
		return nil, err
	}
	return &Provider{endpoint: config.Endpoint, snapshot: domain.EndpointSnapshot{EndpointID: config.Endpoint.ID, SessionID: config.SessionID, State: domain.StateReady, Capabilities: capabilities, ObservedAt: time.Now()}, client: config.Client, root: root, close: config.Close, cursors: make(map[providerapi.PageCursor]*cursorState), maxCursors: maximum}, nil
}
func (p *Provider) Descriptor() domain.Endpoint { return p.endpoint }
func (p *Provider) Snapshot(ctx context.Context) (domain.EndpointSnapshot, error) {
	if err := p.check(ctx, "snapshot", nil); err != nil {
		return domain.EndpointSnapshot{}, err
	}
	return p.snapshot, nil
}
func (p *Provider) Normalize(ctx context.Context, request domain.NormalizeRequest) (domain.Location, error) {
	if err := p.check(ctx, "normalize", nil); err != nil {
		return domain.Location{}, err
	}
	if request.EndpointID != p.endpoint.ID {
		return domain.Location{}, p.invalid("normalize", nil, "endpoint mismatch")
	}
	value := request.Input
	if request.Base != nil {
		if request.Base.EndpointID != p.endpoint.ID {
			return domain.Location{}, p.invalid("normalize", request.Base, "base endpoint mismatch")
		}
		if !path.IsAbs(value) {
			value = path.Join(string(request.Base.Path), value)
		}
	}
	value = path.Clean(value)
	if !path.IsAbs(value) || containsNUL(value) {
		return domain.Location{}, p.invalid("normalize", nil, "path is not canonical absolute")
	}
	return domain.NewLocation(p.endpoint.ID, domain.CanonicalPath(value))
}
func (p *Provider) List(ctx context.Context, request providerapi.ListRequest) (providerapi.ListPage, error) {
	if err := p.check(ctx, "list", &request.Location); err != nil {
		return providerapi.ListPage{}, err
	}
	if err := providerapi.ValidateListRequest(p.endpoint.ID, request); err != nil {
		return providerapi.ListPage{}, p.invalid("list", &request.Location, err.Error())
	}
	var state *cursorState
	if request.Cursor == "" {
		info, err := p.client.Stat(p.remotePath(request.Location))
		if err != nil {
			return providerapi.ListPage{}, p.mapError("list", &request.Location, err)
		}
		entries, err := p.client.ReadDirContext(ctx, p.remotePath(request.Location))
		if err != nil {
			return providerapi.ListPage{}, p.mapError("list", &request.Location, err)
		}
		state = &cursorState{location: request.Location, sort: cloneSort(request.Sort), entries: entries, fingerprint: fingerprint(info)}
	} else {
		p.mu.Lock()
		state = p.cursors[request.Cursor]
		p.mu.Unlock()
		if state == nil {
			return providerapi.ListPage{}, p.invalid("list", &request.Location, "unknown cursor")
		}
		if state.location != request.Location || !sameSort(state.sort, request.Sort) {
			return providerapi.ListPage{}, p.invalid("list", &request.Location, "cursor binding mismatch")
		}
		p.mu.Lock()
		delete(p.cursors, request.Cursor)
		p.mu.Unlock()
		info, err := p.client.Stat(p.remotePath(request.Location))
		if err != nil {
			return providerapi.ListPage{}, p.mapError("list", &request.Location, err)
		}
		if !equalFingerprint(state.fingerprint, fingerprint(info)) {
			return providerapi.ListPage{}, p.opError(domain.CodeConflict, "list", &request.Location, "directory changed", errRetryConflict, nil)
		}
	}
	end := min(state.index+int(request.Limit), len(state.entries))
	result := make([]domain.Entry, 0, end-state.index)
	for _, info := range state.entries[state.index:end] {
		if err := ctx.Err(); err != nil {
			return providerapi.ListPage{}, p.mapError("list", &request.Location, err)
		}
		entry, err := p.entry(request.Location, info)
		if err != nil {
			return providerapi.ListPage{}, p.mapError("list", &request.Location, err)
		}
		result = append(result, entry)
	}
	state.index = end
	done := end == len(state.entries)
	var next providerapi.PageCursor
	if !done {
		p.mu.Lock()
		if len(p.cursors) >= p.maxCursors {
			p.mu.Unlock()
			return providerapi.ListPage{}, p.opError(domain.CodeResourceExhausted, "list", &request.Location, "too many open directory cursors", errRetryNever, nil)
		}
		p.next++
		next = providerapi.PageCursor(fmt.Sprintf("sftp-%d", p.next))
		p.cursors[next] = state
		p.mu.Unlock()
	}
	return providerapi.ListPage{Entries: result, NextCursor: next, Done: done, RequestedSortApplied: false, Consistency: providerapi.ConsistencyBestEffort, DirectoryFingerprint: state.fingerprint}, nil
}
func (p *Provider) Stat(ctx context.Context, request providerapi.StatRequest) (domain.Entry, error) {
	if err := p.check(ctx, "stat", &request.Location); err != nil {
		return domain.Entry{}, err
	}
	if request.Location.EndpointID != p.endpoint.ID {
		return domain.Entry{}, p.invalid("stat", &request.Location, "endpoint mismatch")
	}
	var info os.FileInfo
	var err error
	if request.FollowSymlinks {
		info, err = p.client.Stat(p.remotePath(request.Location))
	} else {
		info, err = p.client.Lstat(p.remotePath(request.Location))
	}
	if err != nil {
		return domain.Entry{}, p.mapError("stat", &request.Location, err)
	}
	return p.entryFromInfo(request.Location, path.Base(string(request.Location.Path)), info), nil
}
func (p *Provider) OpenRead(ctx context.Context, request providerapi.OpenReadRequest) (providerapi.ReadHandle, error) {
	if err := p.check(ctx, "open_read", &request.Location); err != nil {
		return nil, err
	}
	if request.Location.EndpointID != p.endpoint.ID || request.Offset < 0 {
		return nil, p.invalid("open_read", &request.Location, "invalid read request")
	}
	info, err := p.client.Stat(p.remotePath(request.Location))
	if err != nil {
		return nil, p.mapError("open_read", &request.Location, err)
	}
	entry := p.entryFromInfo(request.Location, path.Base(string(request.Location.Path)), info)
	if entry.Kind != domain.EntryFile {
		return nil, p.invalid("open_read", &request.Location, "location is not a regular file")
	}
	if request.ExpectedFingerprint != nil && !equalFingerprint(*request.ExpectedFingerprint, entry.Fingerprint) {
		return nil, p.opError(domain.CodeConflict, "open_read", &request.Location, "fingerprint mismatch", errRetryConflict, nil)
	}
	file, err := p.client.Open(p.remotePath(request.Location))
	if err != nil {
		return nil, p.mapError("open_read", &request.Location, err)
	}
	return &readHandle{provider: p, file: file, location: request.Location, info: providerapi.ReadInfo{Entry: entry, Fingerprint: entry.Fingerprint}, offset: request.Offset, limit: request.Limit}, nil
}
func (p *Provider) DiscardCursor(cursor providerapi.PageCursor) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	delete(p.cursors, cursor)
	return nil
}
func (p *Provider) Close() error {
	p.mu.Lock()
	if p.closed {
		p.mu.Unlock()
		return nil
	}
	p.closed = true
	p.cursors = make(map[providerapi.PageCursor]*cursorState)
	p.mu.Unlock()
	if p.close != nil {
		return p.close()
	}
	return p.client.Close()
}
func (p *Provider) entry(parent domain.Location, info os.FileInfo) (domain.Entry, error) {
	location, _ := domain.NewLocation(p.endpoint.ID, domain.CanonicalPath(path.Join(string(parent.Path), info.Name())))
	entry := p.entryFromInfo(location, info.Name(), info)
	if info.Mode()&os.ModeSymlink != 0 {
		target, err := p.client.ReadLink(p.remotePath(location))
		if err != nil {
			return domain.Entry{}, err
		}
		entry.Symlink = &domain.SymlinkInfo{RawTarget: target}
	}
	return entry, nil
}
func (p *Provider) remotePath(location domain.Location) string {
	if p.root == "/" {
		return string(location.Path)
	}
	return path.Join(p.root, string(location.Path))
}
func (p *Provider) entryFromInfo(location domain.Location, name string, info os.FileInfo) domain.Entry {
	kind := domain.EntryOther
	switch {
	case info.Mode()&os.ModeSymlink != 0:
		kind = domain.EntrySymlink
	case info.IsDir():
		kind = domain.EntryDirectory
	case info.Mode().IsRegular():
		kind = domain.EntryFile
	}
	size := uint64(max(info.Size(), 0))
	mode := uint32(info.Mode())
	modified := info.ModTime()
	precision := domain.TimePrecision("nanosecond")
	return domain.Entry{Location: location, Name: name, Kind: kind, Metadata: domain.Metadata{Size: &size, Mode: &mode, ModifiedAt: &modified, ModifiedPrecision: &precision}, Fingerprint: fingerprint(info)}
}
func fingerprint(info os.FileInfo) domain.Fingerprint {
	size := uint64(max(info.Size(), 0))
	modified := info.ModTime()
	precision := domain.TimePrecision("nanosecond")
	return domain.Fingerprint{Size: &size, ModifiedAt: &modified, ModifiedPrecision: &precision}
}
func equalFingerprint(a, b domain.Fingerprint) bool {
	return a.Size != nil && b.Size != nil && *a.Size == *b.Size && a.ModifiedAt != nil && b.ModifiedAt != nil && a.ModifiedAt.Equal(*b.ModifiedAt)
}
func cloneSort(value *providerapi.SortHint) *providerapi.SortHint {
	if value == nil {
		return nil
	}
	copy := *value
	return &copy
}
func sameSort(a, b *providerapi.SortHint) bool {
	if a == nil || b == nil {
		return a == nil && b == nil
	}
	return *a == *b
}
func containsNUL(value string) bool {
	for i := range value {
		if value[i] == 0 {
			return true
		}
	}
	return false
}

var errRetryNever = domain.RetryAdvice{Kind: domain.RetryNever}
var errRetryConflict = domain.RetryAdvice{Kind: domain.RetryAfterConflict}

func (p *Provider) check(ctx context.Context, operation string, location *domain.Location) error {
	if err := ctx.Err(); err != nil {
		return p.mapError(operation, location, err)
	}
	p.mu.Lock()
	closed := p.closed
	p.mu.Unlock()
	if closed {
		return p.opError(domain.CodeInternal, operation, location, "provider is closed", errRetryNever, os.ErrClosed)
	}
	return nil
}
func (p *Provider) invalid(operation string, location *domain.Location, message string) error {
	return p.opError(domain.CodeInvalidArgument, operation, location, message, errRetryNever, nil)
}
func (p *Provider) mapError(operation string, location *domain.Location, err error) error {
	code := domain.CodeInternal
	retry := errRetryNever
	statusCode, hasStatus := sftpStatusCode(err)
	switch {
	case errors.Is(err, context.Canceled):
		code = domain.CodeCanceled
	case errors.Is(err, context.DeadlineExceeded):
		code = domain.CodeTimeout
	case errors.Is(err, os.ErrNotExist), errors.Is(err, pkgsftp.ErrSSHFxNoSuchFile), hasStatus && statusCode == uint32(pkgsftp.ErrSSHFxNoSuchFile):
		code = domain.CodeNotFound
	case errors.Is(err, os.ErrPermission), errors.Is(err, pkgsftp.ErrSSHFxPermissionDenied), hasStatus && statusCode == uint32(pkgsftp.ErrSSHFxPermissionDenied):
		code = domain.CodePermissionDenied
	case errors.Is(err, pkgsftp.ErrSSHFxOpUnsupported), hasStatus && statusCode == uint32(pkgsftp.ErrSSHFxOpUnsupported):
		code = domain.CodeUnsupported
	case errors.Is(err, io.EOF), errors.Is(err, io.ErrUnexpectedEOF), errors.Is(err, net.ErrClosed), errors.Is(err, pkgsftp.ErrSSHFxNoConnection), errors.Is(err, pkgsftp.ErrSSHFxConnectionLost), hasStatus && (statusCode == uint32(pkgsftp.ErrSSHFxNoConnection) || statusCode == uint32(pkgsftp.ErrSSHFxConnectionLost)):
		code = domain.CodeTransportInterrupted
		retry = domain.RetryAdvice{Kind: domain.RetryAfterReconnect}
	}
	return p.opError(code, operation, location, "SFTP operation failed", retry, err)
}
func sftpStatusCode(err error) (uint32, bool) {
	var status *pkgsftp.StatusError
	if !errors.As(err, &status) {
		return 0, false
	}
	return status.Code, true
}
func (p *Provider) opError(code domain.Code, operation string, location *domain.Location, message string, retry domain.RetryAdvice, cause error) error {
	return &domain.OpError{Code: code, Operation: operation, EndpointID: p.endpoint.ID, Location: location, Message: message, Retry: retry, Effect: domain.EffectNone, Cause: cause}
}

type readHandle struct {
	mu       sync.Mutex
	provider *Provider
	file     *pkgsftp.File
	location domain.Location
	info     providerapi.ReadInfo
	offset   int64
	limit    *int64
	read     int64
	closed   bool
}

func (h *readHandle) Info() providerapi.ReadInfo { return h.info }
func (h *readHandle) Read(ctx context.Context, buffer []byte) (int, error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.closed {
		return 0, io.ErrClosedPipe
	}
	if err := ctx.Err(); err != nil {
		return 0, h.provider.mapError("read", &h.location, err)
	}
	if h.limit != nil {
		remaining := *h.limit - h.read
		if remaining <= 0 {
			return 0, io.EOF
		}
		if int64(len(buffer)) > remaining {
			buffer = buffer[:remaining]
		}
	}
	n, err := h.file.ReadAt(buffer, h.offset+h.read)
	h.read += int64(n)
	if err != nil && errors.Is(err, io.EOF) {
		return n, io.EOF
	}
	if err != nil {
		return n, h.provider.mapError("read", &h.location, err)
	}
	return n, nil
}
func (h *readHandle) Close(ctx context.Context) error {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.closed {
		return nil
	}
	h.closed = true
	if err := ctx.Err(); err != nil {
		return h.provider.mapError("close_read", &h.location, err)
	}
	if err := h.file.Close(); err != nil && !errors.Is(err, os.ErrClosed) {
		return h.provider.mapError("close_read", &h.location, err)
	}
	return nil
}
