//go:build darwin || linux

// Package statefs owns the filesystem boundary for the persistent state DB.
package statefs

import (
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"

	"github.com/TyrantLucifer/awesome-mac-sftp/internal/platform"
	"golang.org/x/sys/unix"
)

const (
	sqliteHeaderBytes = 100
	statePageSize     = uint16(4096)
	applicationID     = uint32(0x414d5346)
	stateMaxMainBytes = int64(8 * 1024 * 1024 * 1024)
)

var (
	ErrForeignDatabase = errors.New("foreign database")
	ErrAmbiguousState  = errors.New("ambiguous persistent state")
)

type IdentityKind uint8

const (
	IdentityPristine IdentityKind = iota + 1
	IdentityProject
)

type Identity struct {
	Kind        IdentityKind
	HasSidecars bool
	Size        int64
}

// PreflightIdentity performs no directory or SQLite writes. A foreign final is
// rejected before sidecars or capability probes are considered.
func PreflightIdentity(databasePath string) (Identity, error) {
	if !filepath.IsAbs(databasePath) || filepath.Clean(databasePath) != databasePath {
		return Identity{}, fmt.Errorf("preflight state identity: database path must be canonical and absolute")
	}
	metadata, err := os.Lstat(databasePath)
	if errors.Is(err, os.ErrNotExist) {
		for _, suffix := range []string{"-wal", "-shm", "-journal"} {
			if _, sidecarErr := os.Lstat(databasePath + suffix); sidecarErr == nil {
				return Identity{}, fmt.Errorf("%w: final is absent but %s exists", ErrAmbiguousState, suffix)
			} else if !errors.Is(sidecarErr, os.ErrNotExist) {
				return Identity{}, fmt.Errorf("preflight state identity: inspect orphan %s: %w", suffix, sidecarErr)
			}
		}
		return Identity{Kind: IdentityPristine}, nil
	}
	if err != nil {
		return Identity{}, fmt.Errorf("preflight state identity: inspect final: %w", err)
	}
	if metadata.Mode()&fs.ModeType != 0 || metadata.Mode().Perm() != 0o600 {
		return Identity{}, fmt.Errorf("%w: final must be a regular 0600 file", ErrForeignDatabase)
	}
	if metadata.Size() < sqliteHeaderBytes || metadata.Size() > stateMaxMainBytes {
		return Identity{}, fmt.Errorf("%w: final size %d is outside the project bounds", ErrForeignDatabase, metadata.Size())
	}
	fileDescriptor, err := unix.Open(databasePath, unix.O_RDONLY|unix.O_CLOEXEC|unix.O_NOFOLLOW, 0)
	if err != nil {
		return Identity{}, fmt.Errorf("preflight state identity: open final without following links: %w", err)
	}
	file := os.NewFile(uintptr(fileDescriptor), databasePath)
	if file == nil {
		_ = unix.Close(fileDescriptor)
		return Identity{}, fmt.Errorf("preflight state identity: wrap final file descriptor")
	}
	header := make([]byte, sqliteHeaderBytes)
	_, readErr := io.ReadFull(file, header)
	closeErr := file.Close()
	if readErr != nil {
		return Identity{}, fmt.Errorf("%w: read final header: %w", ErrForeignDatabase, readErr)
	}
	if closeErr != nil {
		return Identity{}, fmt.Errorf("preflight state identity: close final: %w", closeErr)
	}
	if string(header[:16]) != "SQLite format 3\x00" {
		return Identity{}, fmt.Errorf("%w: SQLite header magic does not match", ErrForeignDatabase)
	}
	if pageSize := binary.BigEndian.Uint16(header[16:18]); pageSize != statePageSize {
		return Identity{}, fmt.Errorf("%w: page size %d, want %d", ErrForeignDatabase, pageSize, statePageSize)
	}
	if userVersion := binary.BigEndian.Uint32(header[60:64]); userVersion != 0 {
		return Identity{}, fmt.Errorf("%w: user_version %d, want 0", ErrForeignDatabase, userVersion)
	}
	if projectID := binary.BigEndian.Uint32(header[68:72]); projectID != applicationID {
		return Identity{}, fmt.Errorf("%w: application_id %#x, want %#x", ErrForeignDatabase, projectID, applicationID)
	}
	if err := platform.ValidatePrivateFile(databasePath, platform.ValidatePersistent); err != nil {
		return Identity{}, fmt.Errorf("%w: validate project final: %w", ErrAmbiguousState, err)
	}

	identity := Identity{Kind: IdentityProject, Size: metadata.Size()}
	for _, suffix := range []string{"-wal", "-shm", "-journal"} {
		sidecar := databasePath + suffix
		if _, err := os.Lstat(sidecar); errors.Is(err, os.ErrNotExist) {
			continue
		} else if err != nil {
			return Identity{}, fmt.Errorf("%w: inspect project %s: %w", ErrAmbiguousState, suffix, err)
		}
		if err := platform.ValidatePrivateFile(sidecar, platform.ValidatePersistent); err != nil {
			return Identity{}, fmt.Errorf("%w: validate project %s: %w", ErrAmbiguousState, suffix, err)
		}
		identity.HasSidecars = true
	}
	return identity, nil
}
