package helper

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"

	"github.com/TyrantLucifer/awesome-sftp-cli/internal/transport/openssh"
	pkgsftp "github.com/pkg/sftp"
)

type SFTPInstallRemoteConfig struct {
	Client         *pkgsftp.Client
	BindingProbe   func(context.Context) ([]byte, error)
	Probe          func(context.Context) (Observation, error)
	LinkAttributes func(context.Context, string) (openssh.SFTPAttributes, error)
	MkdirExact     func(context.Context, string, uint32) error
}

type SFTPInstallRemote struct {
	client         *pkgsftp.Client
	bindingProbe   func(context.Context) ([]byte, error)
	probe          func(context.Context) (Observation, error)
	linkAttributes func(context.Context, string) (openssh.SFTPAttributes, error)
	mkdirExact     func(context.Context, string, uint32) error
}

func NewSFTPInstallRemote(config SFTPInstallRemoteConfig) (*SFTPInstallRemote, error) {
	if config.Client == nil || (config.BindingProbe == nil) == (config.Probe == nil) || config.LinkAttributes == nil || config.MkdirExact == nil {
		return nil, errors.New("create helper SFTP install remote: client and fresh probes are required")
	}
	return &SFTPInstallRemote{client: config.Client, bindingProbe: config.BindingProbe, probe: config.Probe, linkAttributes: config.LinkAttributes, mkdirExact: config.MkdirExact}, nil
}

func (r *SFTPInstallRemote) Probe(ctx context.Context) (Observation, error) {
	if err := requireRemoteContext(ctx); err != nil {
		return Observation{}, err
	}
	if r.probe != nil {
		return r.probe(ctx)
	}
	raw, err := r.bindingProbe(ctx)
	if err != nil {
		return Observation{}, err
	}
	return ParseBindingProbe(raw)
}

func (r *SFTPInstallRemote) RealPath(ctx context.Context, value string) (string, error) {
	if err := requireRemoteContext(ctx); err != nil {
		return "", err
	}
	return r.client.RealPath(value)
}

func (r *SFTPInstallRemote) Lstat(ctx context.Context, value string) (RemoteAttrs, error) {
	if err := requireRemoteContext(ctx); err != nil {
		return RemoteAttrs{}, err
	}
	info, err := r.client.Lstat(value)
	if err != nil {
		return RemoteAttrs{}, mapSFTPInstallError(err)
	}
	raw, err := r.linkAttributes(ctx, value)
	if err != nil {
		return RemoteAttrs{}, fmt.Errorf("helper SFTP lstat raw attributes: %w", err)
	}
	if raw.Mode == nil || raw.UID == nil {
		return RemoteAttrs{}, errors.New("helper SFTP lstat omitted security-relevant mode or UID")
	}
	if uint32(info.Mode().Perm()) != *raw.Mode&0777 || !rawTypeMatches(info.Mode(), *raw.Mode) {
		return RemoteAttrs{}, errors.New("helper SFTP lstat projections disagree")
	}
	size := info.Size()
	if size < 0 {
		return RemoteAttrs{}, errors.New("helper SFTP lstat returned a negative size")
	}
	return RemoteAttrs{Kind: remoteKind(info.Mode()), UID: *raw.UID, Mode: *raw.Mode & 0777, Size: uint64(size)}, nil
}

func (r *SFTPInstallRemote) Mkdir(ctx context.Context, value string, mode uint32) error {
	if err := requireRemoteContext(ctx); err != nil {
		return err
	}
	if mode != 0700 {
		return errors.New("helper SFTP mkdir: only exact 0700 is allowed")
	}
	// pkg/sftp's Mkdir packet omits attributes, which would create a
	// world/group-accessible window before chmod under common umasks. Require a
	// raw SFTP MKDIR implementation that carries exact 0700 in the create packet.
	if err := r.mkdirExact(ctx, value, mode); err != nil {
		return mapSFTPInstallError(err)
	}
	attrs, err := r.Lstat(ctx, value)
	if err != nil || attrs.Kind != RemoteDirectory || attrs.Mode != mode {
		return errors.New("helper SFTP mkdir: created directory does not have exact attributes")
	}
	return nil
}

func (r *SFTPInstallRemote) OpenExclusive(ctx context.Context, value string) (RemoteWriteHandle, error) {
	if err := requireRemoteContext(ctx); err != nil {
		return nil, err
	}
	file, err := r.client.OpenFile(value, os.O_WRONLY|os.O_CREATE|os.O_EXCL)
	if err != nil {
		if _, statErr := r.client.Lstat(value); statErr == nil {
			return nil, fmt.Errorf("%w: %w", ErrRemoteAlreadyExists, err)
		}
		return nil, mapSFTPInstallError(err)
	}
	return &sftpInstallWriteHandle{file: file}, nil
}

func (r *SFTPInstallRemote) OpenRead(ctx context.Context, value string) (io.ReadCloser, error) {
	if err := requireRemoteContext(ctx); err != nil {
		return nil, err
	}
	file, err := r.client.Open(value)
	if err != nil {
		return nil, mapSFTPInstallError(err)
	}
	return file, nil
}

func (r *SFTPInstallRemote) PublishNoReplace(ctx context.Context, source, destination string) error {
	if err := requireRemoteContext(ctx); err != nil {
		return err
	}
	// Some nominal SFTP v3 servers incorrectly implement SSH_FXP_RENAME with
	// replacement semantics. The OpenSSH hardlink extension gives this regular
	// immutable artifact a target-exists-fails publication primitive; the
	// replacement posix-rename extension is deliberately never used.
	if _, supported := r.client.HasExtension("hardlink@openssh.com"); !supported {
		return errors.New("helper SFTP publish: no safe no-replace primitive is available")
	}
	if err := r.client.Link(source, destination); err != nil {
		if _, statErr := r.client.Lstat(destination); statErr == nil {
			return fmt.Errorf("%w: %w", ErrRemoteAlreadyExists, err)
		}
		return mapSFTPInstallError(err)
	}
	if err := r.client.Remove(source); err != nil {
		return fmt.Errorf("helper SFTP publish: final linked but temporary removal failed: %w", err)
	}
	return nil
}

func (r *SFTPInstallRemote) RemoveExact(ctx context.Context, value string) error {
	if err := requireRemoteContext(ctx); err != nil {
		return err
	}
	if err := r.client.Remove(value); err != nil {
		return mapSFTPInstallError(err)
	}
	return nil
}

type sftpInstallWriteHandle struct{ file *pkgsftp.File }

func (h *sftpInstallWriteHandle) Chmod(ctx context.Context, mode uint32) error {
	if err := requireRemoteContext(ctx); err != nil {
		return err
	}
	if mode != 0600 && mode != 0700 {
		return errors.New("helper SFTP handle chmod: mode is not allowed")
	}
	return h.file.Chmod(os.FileMode(mode)) // #nosec G115 -- mode is constrained above.
}

func (h *sftpInstallWriteHandle) Stat(ctx context.Context) (RemoteAttrs, error) {
	if err := requireRemoteContext(ctx); err != nil {
		return RemoteAttrs{}, err
	}
	info, err := h.file.Stat()
	if err != nil {
		return RemoteAttrs{}, err
	}
	stat, ok := info.Sys().(*pkgsftp.FileStat)
	if !ok || stat == nil {
		return RemoteAttrs{}, errors.New("helper SFTP handle stat omitted raw attributes")
	}
	size := info.Size()
	if size < 0 {
		return RemoteAttrs{}, errors.New("helper SFTP handle stat returned a negative size")
	}
	return RemoteAttrs{Kind: remoteKind(info.Mode()), UID: stat.UID, Mode: uint32(info.Mode().Perm()), Size: uint64(size)}, nil
}

func (h *sftpInstallWriteHandle) Write(ctx context.Context, value []byte) (int, error) {
	if err := requireRemoteContext(ctx); err != nil {
		return 0, err
	}
	return h.file.Write(value)
}

func (h *sftpInstallWriteHandle) Close(context.Context) error { return h.file.Close() }

func requireRemoteContext(ctx context.Context) error {
	if ctx == nil {
		return errors.New("helper SFTP operation: context is required")
	}
	return ctx.Err()
}

func mapSFTPInstallError(err error) error {
	if errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("%w: %w", ErrRemoteNotExist, err)
	}
	if errors.Is(err, os.ErrExist) {
		return fmt.Errorf("%w: %w", ErrRemoteAlreadyExists, err)
	}
	var status *pkgsftp.StatusError
	if errors.As(err, &status) && status.Code == uint32(pkgsftp.ErrSSHFxNoSuchFile) {
		return fmt.Errorf("%w: %w", ErrRemoteNotExist, err)
	}
	return err
}

func remoteKind(mode os.FileMode) RemoteKind {
	switch {
	case mode.IsRegular():
		return RemoteRegular
	case mode.IsDir():
		return RemoteDirectory
	case mode&os.ModeSymlink != 0:
		return RemoteSymlink
	default:
		return RemoteOther
	}
}

func rawTypeMatches(mode os.FileMode, raw uint32) bool {
	typeBits := raw & 0170000
	switch {
	case mode.IsRegular():
		return typeBits == 0100000
	case mode.IsDir():
		return typeBits == 0040000
	case mode&os.ModeSymlink != 0:
		return typeBits == 0120000
	default:
		return typeBits != 0100000 && typeBits != 0040000 && typeBits != 0120000
	}
}
