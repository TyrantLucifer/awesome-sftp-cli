package sftp

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"io"
	"os"
	"path"
	"strings"
	"sync"

	"github.com/TyrantLucifer/awesome-sftp-cli/internal/domain"
	providerapi "github.com/TyrantLucifer/awesome-sftp-cli/internal/provider"
	pkgsftp "github.com/pkg/sftp"
)

var _ providerapi.MutableProvider = (*Provider)(nil)
var _ providerapi.DestinationPreserver = (*Provider)(nil)

func (p *Provider) OpenWrite(ctx context.Context, request providerapi.OpenWriteRequest) (providerapi.WriteHandle, error) {
	if err := p.check(ctx, "open_write", &request.Location); err != nil {
		return nil, err
	}
	if err := providerapi.ValidateOpenWriteRequest(p.endpoint.ID, request); err != nil {
		return nil, err
	}
	if err := p.validateMutableLocation(request.Location, "open_write"); err != nil {
		return nil, err
	}
	remotePath := p.remotePath(request.Location)
	if request.Disposition == providerapi.WriteCreateNew {
		if _, err := p.client.Lstat(remotePath); err == nil {
			return nil, p.opError(domain.CodeAlreadyExists, "open_write", &request.Location, "path already exists", errRetryConflict, os.ErrExist)
		} else if !isSFTPNotExist(err) {
			return nil, p.mapMutationError("open_write", &request.Location, err, domain.EffectNone)
		}
	}

	flags := os.O_WRONLY
	if request.Disposition == providerapi.WriteCreateNew {
		flags |= os.O_CREATE | os.O_EXCL
	}
	file, err := p.client.OpenFile(remotePath, flags)
	if err != nil {
		if request.Disposition == providerapi.WriteCreateNew {
			if _, statErr := p.client.Lstat(remotePath); statErr == nil {
				return nil, p.opError(domain.CodeAlreadyExists, "open_write", &request.Location, "path already exists", errRetryConflict, err)
			}
		}
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
	if request.ExpectedFingerprint != nil && !equalFingerprint(*request.ExpectedFingerprint, fingerprint(info)) {
		return nil, p.opError(domain.CodeConflict, "open_write", &request.Location, "fingerprint does not match", errRetryConflict, nil)
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
	if err := p.check(ctx, "mkdir", &request.Location); err != nil {
		return domain.Entry{}, err
	}
	if err := providerapi.ValidateMkdirRequest(p.endpoint.ID, request); err != nil {
		return domain.Entry{}, err
	}
	if err := p.validateMutableLocation(request.Location, "mkdir"); err != nil {
		return domain.Entry{}, err
	}
	remotePath := p.remotePath(request.Location)
	if info, err := p.client.Lstat(remotePath); err == nil {
		if !request.Exclusive && info.IsDir() && info.Mode()&os.ModeSymlink == 0 {
			return p.entryFromInfo(request.Location, path.Base(string(request.Location.Path)), info), nil
		}
		return domain.Entry{}, p.opError(domain.CodeAlreadyExists, "mkdir", &request.Location, "path already exists", errRetryConflict, os.ErrExist)
	} else if !isSFTPNotExist(err) {
		return domain.Entry{}, p.mapMutationError("mkdir", &request.Location, err, domain.EffectNone)
	}
	if err := p.client.Mkdir(remotePath); err != nil {
		if _, statErr := p.client.Lstat(remotePath); statErr == nil {
			return domain.Entry{}, p.opError(domain.CodeAlreadyExists, "mkdir", &request.Location, "path already exists", errRetryConflict, err)
		}
		return domain.Entry{}, p.mapMutationError("mkdir", &request.Location, err, domain.EffectNone)
	}
	info, err := p.client.Lstat(remotePath)
	if err != nil {
		return domain.Entry{}, p.mapMutationError("mkdir", &request.Location, err, domain.EffectApplied)
	}
	return p.entryFromInfo(request.Location, path.Base(string(request.Location.Path)), info), nil
}

func (p *Provider) Rename(ctx context.Context, request providerapi.RenameRequest) (providerapi.RenameResult, error) {
	if err := p.check(ctx, "rename", &request.Source); err != nil {
		return providerapi.RenameResult{}, err
	}
	if err := providerapi.ValidateRenameRequest(p.endpoint.ID, request); err != nil {
		return providerapi.RenameResult{}, err
	}
	if err := p.validateMutableLocation(request.Source, "rename"); err != nil {
		return providerapi.RenameResult{}, err
	}
	if err := p.validateMutableLocation(request.Destination, "rename"); err != nil {
		return providerapi.RenameResult{}, err
	}
	if strings.HasPrefix(string(request.Destination.Path)+"/", string(request.Source.Path)+"/") {
		return providerapi.RenameResult{}, p.invalid("rename", &request.Source, "rename paths are unsafe")
	}
	sourcePath := p.remotePath(request.Source)
	destinationPath := p.remotePath(request.Destination)
	sourceInfo, err := p.client.Lstat(sourcePath)
	if err != nil {
		return providerapi.RenameResult{}, p.mapMutationError("rename", &request.Source, err, domain.EffectNone)
	}
	if request.ExpectedSource != nil && !equalFingerprint(*request.ExpectedSource, fingerprint(sourceInfo)) {
		return providerapi.RenameResult{}, p.opError(domain.CodeConflict, "rename", &request.Source, "source fingerprint does not match", errRetryConflict, nil)
	}
	destinationInfo, destinationErr := p.client.Lstat(destinationPath)
	destinationExists := destinationErr == nil
	if destinationErr != nil && !isSFTPNotExist(destinationErr) {
		return providerapi.RenameResult{}, p.mapMutationError("rename", &request.Destination, destinationErr, domain.EffectNone)
	}
	if request.ExpectedDestination != nil && (!destinationExists || !equalFingerprint(*request.ExpectedDestination, fingerprint(destinationInfo))) {
		return providerapi.RenameResult{}, p.opError(domain.CodeConflict, "rename", &request.Destination, "destination fingerprint does not match", errRetryConflict, nil)
	}
	if !request.Replace && destinationExists {
		return providerapi.RenameResult{}, p.opError(domain.CodeAlreadyExists, "rename", &request.Destination, "destination already exists", errRetryConflict, os.ErrExist)
	}
	if request.Replace {
		if _, supported := p.client.HasExtension("posix-rename@openssh.com"); !supported {
			return providerapi.RenameResult{}, p.opError(domain.CodeUnsupported, "rename", &request.Destination, "server does not support atomic replacement rename", errRetryNever, nil)
		}
		if err := p.client.PosixRename(sourcePath, destinationPath); err != nil {
			return providerapi.RenameResult{}, p.mapMutationError("rename", &request.Destination, err, domain.EffectUnknown)
		}
		return providerapi.RenameResult{Atomic: true, Replaced: destinationExists}, nil
	}
	if !sourceInfo.Mode().IsRegular() {
		return providerapi.RenameResult{}, p.opError(domain.CodeUnsupported, "rename", &request.Source, "no-replace rename is supported only for regular files", errRetryNever, nil)
	}
	if _, supported := p.client.HasExtension("hardlink@openssh.com"); !supported {
		return providerapi.RenameResult{}, p.opError(domain.CodeUnsupported, "rename", &request.Destination, "server does not support no-replace publication", errRetryNever, nil)
	}
	if err := p.client.Link(sourcePath, destinationPath); err != nil {
		if _, statErr := p.client.Lstat(destinationPath); statErr == nil {
			return providerapi.RenameResult{}, p.opError(domain.CodeAlreadyExists, "rename", &request.Destination, "destination appeared concurrently", errRetryConflict, err)
		}
		return providerapi.RenameResult{}, p.mapMutationError("rename", &request.Destination, err, domain.EffectNone)
	}
	if err := p.client.Remove(sourcePath); err != nil {
		return providerapi.RenameResult{}, p.mapMutationError("rename", &request.Source, err, domain.EffectApplied)
	}
	return providerapi.RenameResult{Atomic: false, Replaced: false}, nil
}

func (p *Provider) PreserveDestination(ctx context.Context, request providerapi.PreserveDestinationRequest) (providerapi.PreserveDestinationResult, error) {
	if err := p.check(ctx, "preserve_destination", &request.Source); err != nil {
		return providerapi.PreserveDestinationResult{}, err
	}
	if err := providerapi.ValidatePreserveDestinationRequest(p.endpoint.ID, request); err != nil {
		return providerapi.PreserveDestinationResult{}, err
	}
	if err := p.validateMutableLocation(request.Source, "preserve_destination"); err != nil {
		return providerapi.PreserveDestinationResult{}, err
	}
	if err := p.validateMutableLocation(request.Backup, "preserve_destination"); err != nil {
		return providerapi.PreserveDestinationResult{}, err
	}
	if p.preserveSlot == nil {
		return providerapi.PreserveDestinationResult{}, p.opError(domain.CodeInternal, "preserve_destination", &request.Source, "preservation slot is unavailable", errRetryNever, nil)
	}
	select {
	case p.preserveSlot <- struct{}{}:
	case <-ctx.Done():
		return providerapi.PreserveDestinationResult{}, p.mapMutationError("preserve_destination", &request.Source, ctx.Err(), domain.EffectNone)
	}
	type preserveResult struct {
		result providerapi.PreserveDestinationResult
		err    error
	}
	completed := make(chan preserveResult, 1)
	go func() {
		result, err := p.preserveDestinationBlocking(ctx, request)
		<-p.preserveSlot
		completed <- preserveResult{result: result, err: err}
	}()
	select {
	case completedResult := <-completed:
		return completedResult.result, completedResult.err
	case <-ctx.Done():
		// Closing the transport unblocks every pkg/sftp request in this single
		// preservation transaction. The slot remains occupied until the worker
		// actually exits, so repeated timeouts cannot accumulate goroutines.
		_ = p.client.Close()
		return providerapi.PreserveDestinationResult{EffectUnknown: true}, p.mapMutationError("preserve_destination", &request.Backup, ctx.Err(), domain.EffectUnknown)
	}
}

func (p *Provider) preserveDestinationBlocking(ctx context.Context, request providerapi.PreserveDestinationRequest) (providerapi.PreserveDestinationResult, error) {
	sourcePath := p.remotePath(request.Source)
	backupPath := p.remotePath(request.Backup)
	if backupInfo, backupErr := p.client.Lstat(backupPath); backupErr == nil {
		if _, sourceErr := p.client.Lstat(sourcePath); sourceErr == nil {
			return providerapi.PreserveDestinationResult{BackupPresent: true}, p.opError(domain.CodeConflict, "preserve_destination", &request.Backup, "source and preservation path both exist", errRetryConflict, nil)
		} else if !isSFTPNotExist(sourceErr) {
			return providerapi.PreserveDestinationResult{BackupPresent: true}, p.mapMutationError("preserve_destination", &request.Source, sourceErr, domain.EffectNone)
		}
		if !equalFingerprint(request.ExpectedFingerprint, fingerprint(backupInfo)) {
			return providerapi.PreserveDestinationResult{BackupPresent: true}, p.opError(domain.CodeConflict, "preserve_destination", &request.Backup, "preserved fingerprint does not match", errRetryConflict, nil)
		}
		contentSHA, hashErr := p.hashMutableFileBlocking(ctx, backupPath, request.Backup, request.ExpectedSize, request.MaxBytes)
		if hashErr != nil || contentSHA != request.ExpectedSHA256 {
			return providerapi.PreserveDestinationResult{BackupPresent: true}, errors.Join(p.opError(domain.CodeConflict, "preserve_destination", &request.Backup, "preserved content does not match", errRetryConflict, nil), hashErr)
		}
		return providerapi.PreserveDestinationResult{BackupPresent: true}, nil
	} else if !isSFTPNotExist(backupErr) {
		return providerapi.PreserveDestinationResult{}, p.mapMutationError("preserve_destination", &request.Backup, backupErr, domain.EffectNone)
	}
	sourceInfo, err := p.client.Lstat(sourcePath)
	if err != nil {
		return providerapi.PreserveDestinationResult{}, p.mapMutationError("preserve_destination", &request.Source, err, domain.EffectNone)
	}
	if !sourceInfo.Mode().IsRegular() || !equalFingerprint(request.ExpectedFingerprint, fingerprint(sourceInfo)) {
		return providerapi.PreserveDestinationResult{}, p.opError(domain.CodeConflict, "preserve_destination", &request.Source, "source fingerprint does not match", errRetryConflict, nil)
	}
	if renameErr := p.client.Rename(sourcePath, backupPath); renameErr != nil {
		_, backupErr := p.client.Lstat(backupPath)
		_, sourceErr := p.client.Lstat(sourcePath)
		if backupErr != nil || sourceErr == nil {
			return providerapi.PreserveDestinationResult{EffectUnknown: true}, p.mapMutationError("preserve_destination", &request.Backup, renameErr, domain.EffectUnknown)
		}
	}
	contentSHA, hashErr := p.hashMutableFileBlocking(ctx, backupPath, request.Backup, request.ExpectedSize, request.MaxBytes)
	if hashErr == nil && contentSHA == request.ExpectedSHA256 {
		return providerapi.PreserveDestinationResult{BackupPresent: true}, nil
	}
	var restoreErr error
	_, sourceErr := p.client.Lstat(sourcePath)
	if isSFTPNotExist(sourceErr) {
		restoreErr = p.client.Rename(backupPath, sourcePath)
		if restoreErr == nil {
			return providerapi.PreserveDestinationResult{SourceRestored: true}, errors.Join(
				p.opError(domain.CodeConflict, "preserve_destination", &request.Backup, "content changed while being preserved", errRetryConflict, nil),
				hashErr,
			)
		}
	} else if sourceErr != nil {
		return providerapi.PreserveDestinationResult{EffectUnknown: true}, errors.Join(
			p.opError(domain.CodeConflict, "preserve_destination", &request.Backup, "content changed and restore state is unknown", errRetryConflict, sourceErr),
			hashErr,
		)
	}
	_, backupProbeErr := p.client.Lstat(backupPath)
	result := providerapi.PreserveDestinationResult{BackupPresent: backupProbeErr == nil, EffectUnknown: backupProbeErr != nil && !isSFTPNotExist(backupProbeErr)}
	return result, errors.Join(
		p.opError(domain.CodeConflict, "preserve_destination", &request.Backup, "content changed while being preserved", errRetryConflict, nil),
		hashErr, restoreErr, backupProbeErr,
	)
}

func (p *Provider) hashMutableFileBlocking(ctx context.Context, remotePath string, location domain.Location, expectedSize, maxBytes int64) (string, error) {
	file, err := p.client.Open(remotePath)
	if err != nil {
		return "", p.mapMutationError("rename", &location, err, domain.EffectNone)
	}
	defer func() { _ = file.Close() }()
	digest := sha256.New()
	buffer := make([]byte, 256*1024)
	var total int64
	zeroReads := 0
	for {
		if err := ctx.Err(); err != nil {
			return "", err
		}
		remaining := maxBytes - total + 1
		if remaining <= 0 {
			return "", p.opError(domain.CodeResourceExhausted, "preserve_destination", &location, "preserved content exceeds byte limit", errRetryNever, nil)
		}
		chunk := buffer
		if int64(len(chunk)) > remaining {
			chunk = chunk[:remaining]
		}
		count, readErr := file.Read(chunk)
		if count != 0 {
			zeroReads = 0
			total += int64(count)
			_, _ = digest.Write(chunk[:count])
		} else if readErr == nil {
			zeroReads++
			if zeroReads >= 100 {
				return "", io.ErrNoProgress
			}
		}
		if errors.Is(readErr, io.EOF) {
			if total != expectedSize {
				return "", p.opError(domain.CodeConflict, "preserve_destination", &location, "preserved content size does not match", errRetryConflict, nil)
			}
			return fmt.Sprintf("%x", digest.Sum(nil)), nil
		}
		if readErr != nil {
			return "", p.mapMutationError("rename", &location, readErr, domain.EffectNone)
		}
	}
}

func (p *Provider) Remove(ctx context.Context, request providerapi.RemoveRequest) error {
	if err := p.check(ctx, "remove", &request.Location); err != nil {
		return err
	}
	if err := providerapi.ValidateRemoveRequest(p.endpoint.ID, request); err != nil {
		return err
	}
	if err := p.validateMutableLocation(request.Location, "remove"); err != nil {
		return err
	}
	remotePath := p.remotePath(request.Location)
	info, err := p.client.Lstat(remotePath)
	if err != nil {
		return p.mapMutationError("remove", &request.Location, err, domain.EffectNone)
	}
	if request.Expected != nil && !equalFingerprint(*request.Expected, fingerprint(info)) {
		return p.opError(domain.CodeConflict, "remove", &request.Location, "fingerprint does not match", errRetryConflict, nil)
	}
	if info.IsDir() && info.Mode()&os.ModeSymlink == 0 {
		err = p.client.RemoveDirectory(remotePath)
	} else {
		err = p.client.Remove(remotePath)
	}
	if err != nil {
		return p.mapMutationError("remove", &request.Location, err, domain.EffectUnknown)
	}
	return nil
}

func (p *Provider) validateMutableLocation(location domain.Location, operation string) error {
	if location.EndpointID != p.endpoint.ID || !path.IsAbs(string(location.Path)) ||
		path.Clean(string(location.Path)) != string(location.Path) || containsNUL(string(location.Path)) {
		return p.invalid(operation, &location, "location is not canonical for this provider")
	}
	if location.Path == "/" {
		return p.invalid(operation, &location, "mutation location must not be root")
	}
	return nil
}

func (p *Provider) mapMutationError(operation string, location *domain.Location, err error, effect domain.EffectStatus) error {
	mapped := p.mapError(operation, location, err)
	var operationError *domain.OpError
	if errors.As(mapped, &operationError) {
		operationError.Effect = effect
	}
	return mapped
}

func isSFTPNotExist(err error) bool {
	if errors.Is(err, os.ErrNotExist) || errors.Is(err, pkgsftp.ErrSSHFxNoSuchFile) {
		return true
	}
	code, ok := sftpStatusCode(err)
	return ok && code == uint32(pkgsftp.ErrSSHFxNoSuchFile)
}

type writeHandle struct {
	mu sync.Mutex

	provider *Provider
	file     *pkgsftp.File
	location domain.Location
	closed   bool
}

var _ providerapi.WriteHandle = (*writeHandle)(nil)

func (h *writeHandle) Write(ctx context.Context, source []byte) (int, error) {
	if err := h.provider.check(ctx, "write", &h.location); err != nil {
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
	if err := h.provider.check(ctx, "sync_write", &h.location); err != nil {
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
	if err := h.provider.check(ctx, "close_write", &h.location); err != nil {
		return err
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.closed {
		return nil
	}
	h.closed = true
	if err := h.file.Close(); err != nil {
		return h.provider.mapMutationError("close_write", &h.location, err, domain.EffectUnknown)
	}
	return nil
}
