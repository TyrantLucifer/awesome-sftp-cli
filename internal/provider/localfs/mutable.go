package localfs

import (
	"context"
	"errors"
	"io"
	"os"
	"path"
	"reflect"
	"strings"
	"sync"
	"syscall"

	"github.com/TyrantLucifer/awesome-mac-sftp/internal/domain"
	providerapi "github.com/TyrantLucifer/awesome-mac-sftp/internal/provider"
)

var _ providerapi.MutableProvider = (*Provider)(nil)

func (p *Provider) OpenWrite(ctx context.Context, request providerapi.OpenWriteRequest) (providerapi.WriteHandle, error) {
	if err := p.checkMutable(ctx, "open_write", &request.Location); err != nil {
		return nil, err
	}
	if err := providerapi.ValidateOpenWriteRequest(p.endpoint.ID, request); err != nil {
		return nil, err
	}
	if request.Location.Path == "/" {
		return nil, p.invalid("open_write", &request.Location, "file location must not be root")
	}
	rootedPath, err := p.mutablePath(request.Location, "open_write")
	if err != nil {
		return nil, err
	}

	var file *os.File
	switch request.Disposition {
	case providerapi.WriteCreateNew:
		file, err = p.rootHandle.OpenFile(rootedPath, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	case providerapi.WriteResumeExisting, providerapi.WriteTruncate:
		file, err = p.rootHandle.OpenFile(rootedPath, os.O_WRONLY, 0)
	}
	if err != nil {
		return nil, p.mapMutationError("open_write", &request.Location, err, domain.EffectNone)
	}
	closeOnError := true
	defer func() {
		if closeOnError {
			_ = file.Close()
		}
	}()

	info, err := file.Stat()
	if err != nil {
		return nil, p.mapMutationError("open_write", &request.Location, err, domain.EffectNone)
	}
	if !info.Mode().IsRegular() {
		return nil, p.invalid("open_write", &request.Location, "location is not a regular file")
	}
	if request.ExpectedFingerprint != nil && !reflect.DeepEqual(*request.ExpectedFingerprint, fingerprint(info)) {
		return nil, p.opError(
			domain.CodeConflict,
			"open_write",
			&request.Location,
			"fingerprint does not match",
			domain.RetryAfterConflict,
			nil,
		)
	}
	if request.Disposition == providerapi.WriteResumeExisting && request.Offset > info.Size() {
		return nil, p.invalid("open_write", &request.Location, "resume offset exceeds file size")
	}
	if request.Disposition == providerapi.WriteTruncate {
		if err := file.Truncate(0); err != nil {
			return nil, p.mapMutationError("open_write", &request.Location, err, domain.EffectUnknown)
		}
	}
	if _, err := file.Seek(request.Offset, io.SeekStart); err != nil {
		return nil, p.mapMutationError("open_write", &request.Location, err, domain.EffectNone)
	}

	closeOnError = false
	return &writeHandle{provider: p, file: file, location: request.Location}, nil
}

func (p *Provider) Mkdir(ctx context.Context, request providerapi.MkdirRequest) (domain.Entry, error) {
	if err := p.checkMutable(ctx, "mkdir", &request.Location); err != nil {
		return domain.Entry{}, err
	}
	if err := providerapi.ValidateMkdirRequest(p.endpoint.ID, request); err != nil {
		return domain.Entry{}, err
	}
	if request.Location.Path == "/" {
		return domain.Entry{}, p.invalid("mkdir", &request.Location, "directory location must not be root")
	}
	rootedPath, err := p.mutablePath(request.Location, "mkdir")
	if err != nil {
		return domain.Entry{}, err
	}
	if err := p.rootHandle.Mkdir(rootedPath, 0o700); err != nil {
		if errors.Is(err, os.ErrExist) && !request.Exclusive {
			info, statErr := p.rootHandle.Lstat(rootedPath)
			if statErr == nil && info.IsDir() && info.Mode()&os.ModeSymlink == 0 {
				return p.entryFromInfo(request.Location, path.Base(string(request.Location.Path)), p.hostPath(request.Location), info), nil
			}
		}
		return domain.Entry{}, p.mapMutationError("mkdir", &request.Location, err, domain.EffectNone)
	}
	info, err := p.rootHandle.Lstat(rootedPath)
	if err != nil {
		return domain.Entry{}, p.mapMutationError("mkdir", &request.Location, err, domain.EffectApplied)
	}
	return p.entryFromInfo(request.Location, path.Base(string(request.Location.Path)), p.hostPath(request.Location), info), nil
}

func (p *Provider) Rename(ctx context.Context, request providerapi.RenameRequest) (providerapi.RenameResult, error) {
	if err := p.checkMutable(ctx, "rename", &request.Source); err != nil {
		return providerapi.RenameResult{}, err
	}
	if err := providerapi.ValidateRenameRequest(p.endpoint.ID, request); err != nil {
		return providerapi.RenameResult{}, err
	}
	if request.Source.Path == "/" || request.Destination.Path == "/" ||
		strings.HasPrefix(string(request.Destination.Path)+"/", string(request.Source.Path)+"/") {
		return providerapi.RenameResult{}, p.invalid("rename", &request.Source, "rename paths are unsafe")
	}
	sourcePath, err := p.mutablePath(request.Source, "rename")
	if err != nil {
		return providerapi.RenameResult{}, err
	}
	destinationPath, err := p.mutablePath(request.Destination, "rename")
	if err != nil {
		return providerapi.RenameResult{}, err
	}
	sourceInfo, err := p.rootHandle.Lstat(sourcePath)
	if err != nil {
		return providerapi.RenameResult{}, p.mapMutationError("rename", &request.Source, err, domain.EffectNone)
	}
	if request.ExpectedSource != nil && !reflect.DeepEqual(*request.ExpectedSource, fingerprint(sourceInfo)) {
		return providerapi.RenameResult{}, p.opError(domain.CodeConflict, "rename", &request.Source, "source fingerprint does not match", domain.RetryAfterConflict, nil)
	}
	destinationInfo, destinationErr := p.rootHandle.Lstat(destinationPath)
	destinationExists := destinationErr == nil
	if destinationErr != nil && !errors.Is(destinationErr, os.ErrNotExist) {
		return providerapi.RenameResult{}, p.mapMutationError("rename", &request.Destination, destinationErr, domain.EffectNone)
	}
	if request.ExpectedDestination != nil {
		if !destinationExists || !reflect.DeepEqual(*request.ExpectedDestination, fingerprint(destinationInfo)) {
			return providerapi.RenameResult{}, p.opError(domain.CodeConflict, "rename", &request.Destination, "destination fingerprint does not match", domain.RetryAfterConflict, nil)
		}
	}
	if !request.Replace && destinationExists {
		return providerapi.RenameResult{}, p.opError(domain.CodeAlreadyExists, "rename", &request.Destination, "destination already exists", domain.RetryAfterConflict, os.ErrExist)
	}

	if request.Replace {
		if err := p.rootHandle.Rename(sourcePath, destinationPath); err != nil {
			return providerapi.RenameResult{}, p.mapMutationError("rename", &request.Destination, err, domain.EffectUnknown)
		}
		return providerapi.RenameResult{Atomic: true, Replaced: destinationExists}, nil
	}
	if !sourceInfo.Mode().IsRegular() {
		return providerapi.RenameResult{}, p.opError(domain.CodeUnsupported, "rename", &request.Source, "no-replace rename is supported only for regular files", domain.RetryNever, nil)
	}
	// Hard-link publication makes the final name appear atomically without ever
	// replacing a concurrent destination. Removing the fully-written part is a
	// separate cleanup effect and is therefore reported as non-atomic overall.
	if err := p.rootHandle.Link(sourcePath, destinationPath); err != nil {
		return providerapi.RenameResult{}, p.mapMutationError("rename", &request.Destination, err, domain.EffectNone)
	}
	if err := p.rootHandle.Remove(sourcePath); err != nil {
		return providerapi.RenameResult{}, p.mapMutationError("rename", &request.Source, err, domain.EffectApplied)
	}
	return providerapi.RenameResult{Atomic: false, Replaced: false}, nil
}

func (p *Provider) Remove(ctx context.Context, request providerapi.RemoveRequest) error {
	if err := p.checkMutable(ctx, "remove", &request.Location); err != nil {
		return err
	}
	if err := providerapi.ValidateRemoveRequest(p.endpoint.ID, request); err != nil {
		return err
	}
	if request.Location.Path == "/" {
		return p.invalid("remove", &request.Location, "remove location must not be root")
	}
	rootedPath, err := p.mutablePath(request.Location, "remove")
	if err != nil {
		return err
	}
	info, err := p.rootHandle.Lstat(rootedPath)
	if err != nil {
		return p.mapMutationError("remove", &request.Location, err, domain.EffectNone)
	}
	if request.Expected != nil && !reflect.DeepEqual(*request.Expected, fingerprint(info)) {
		return p.opError(domain.CodeConflict, "remove", &request.Location, "fingerprint does not match", domain.RetryAfterConflict, nil)
	}
	if err := p.rootHandle.Remove(rootedPath); err != nil {
		effect := domain.EffectUnknown
		if errors.Is(err, os.ErrPermission) || errors.Is(err, syscall.ENOTEMPTY) || errors.Is(err, syscall.EEXIST) {
			effect = domain.EffectNone
		}
		return p.mapMutationError("remove", &request.Location, err, effect)
	}
	return nil
}

func (p *Provider) checkMutable(ctx context.Context, operation string, location *domain.Location) error {
	if err := p.checkContext(ctx, operation, location); err != nil {
		return err
	}
	p.mu.Lock()
	closed := p.closed
	p.mu.Unlock()
	if closed {
		return p.closedError(operation, location)
	}
	return nil
}

func (p *Provider) mutablePath(location domain.Location, operation string) (string, error) {
	if err := p.validateCanonical(location, operation); err != nil {
		return "", err
	}
	if p.rootHandle == nil {
		return "", p.closedError(operation, &location)
	}
	return strings.TrimPrefix(string(location.Path), "/"), nil
}

func (p *Provider) mapMutationError(operation string, location *domain.Location, err error, effect domain.EffectStatus) error {
	if err == nil {
		return nil
	}
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return domain.FromContext(operation, p.endpoint.ID, location, err)
	}
	code := domain.CodeInternal
	retry := domain.RetryNever
	switch {
	case errors.Is(err, os.ErrNotExist):
		code = domain.CodeNotFound
	case errors.Is(err, os.ErrExist):
		code = domain.CodeAlreadyExists
		retry = domain.RetryAfterConflict
	case errors.Is(err, os.ErrPermission):
		code = domain.CodePermissionDenied
	case errors.Is(err, syscall.ENOSPC), errors.Is(err, syscall.EDQUOT):
		code = domain.CodeResourceExhausted
	case errors.Is(err, syscall.ENOTEMPTY):
		code = domain.CodeConflict
		retry = domain.RetryAfterConflict
	case errors.Is(err, syscall.EXDEV), errors.Is(err, syscall.ENOTSUP):
		code = domain.CodeUnsupported
	case errors.Is(err, os.ErrInvalid):
		code = domain.CodeInvalidArgument
	}
	operationError := p.opError(code, operation, location, "local filesystem mutation failed", retry, err)
	var typed *domain.OpError
	if errors.As(operationError, &typed) {
		typed.Effect = effect
	}
	return operationError
}

type writeHandle struct {
	mu sync.Mutex

	provider *Provider
	file     *os.File
	location domain.Location
	closed   bool
}

var _ providerapi.WriteHandle = (*writeHandle)(nil)

func (h *writeHandle) Write(ctx context.Context, source []byte) (int, error) {
	if err := h.provider.checkMutable(ctx, "write", &h.location); err != nil {
		return 0, err
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.closed {
		return 0, h.provider.invalid("write", &h.location, "write handle is closed")
	}
	n, err := h.file.Write(source)
	if err != nil {
		effect := domain.EffectNone
		if n > 0 {
			effect = domain.EffectApplied
		}
		return n, h.provider.mapMutationError("write", &h.location, err, effect)
	}
	return n, nil
}

func (h *writeHandle) Sync(ctx context.Context) error {
	if err := h.provider.checkMutable(ctx, "sync_write", &h.location); err != nil {
		return err
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.closed {
		return h.provider.invalid("sync_write", &h.location, "write handle is closed")
	}
	if err := h.file.Sync(); err != nil {
		return h.provider.mapMutationError("sync_write", &h.location, err, domain.EffectUnknown)
	}
	return nil
}

func (h *writeHandle) Close(ctx context.Context) error {
	if err := h.provider.checkContext(ctx, "close_write", &h.location); err != nil {
		return err
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.closed {
		return nil
	}
	h.closed = true
	if err := h.file.Close(); err != nil && !errors.Is(err, os.ErrClosed) {
		return h.provider.mapMutationError("close_write", &h.location, err, domain.EffectUnknown)
	}
	return nil
}
