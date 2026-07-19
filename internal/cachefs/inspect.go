package cachefs

import (
	"crypto/sha256"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"syscall"

	"github.com/TyrantLucifer/awesome-sftp-cli/internal/cache"
	"github.com/TyrantLucifer/awesome-sftp-cli/internal/platform"
)

type MaterializationInfo struct {
	ID     cache.MaterializationID
	SHA256 cache.BlobID
	Size   int64
	Path   string
}

func (store *Store) InspectBlob(id cache.BlobID) (BlobInfo, error) {
	file, info, err := store.openBlob(id)
	if err != nil {
		return BlobInfo{}, err
	}
	if err := file.Close(); err != nil {
		return BlobInfo{}, fmt.Errorf("inspect cache blob close: %w", err)
	}
	return info, nil
}

func (store *Store) OpenBlob(id cache.BlobID) (*os.File, BlobInfo, error) {
	return store.openBlob(id)
}

func (store *Store) openBlob(id cache.BlobID) (*os.File, BlobInfo, error) {
	path, err := store.BlobPath(id)
	if err != nil {
		return nil, BlobInfo{}, err
	}
	file, before, err := openPrivateRegular(path)
	if err != nil {
		return nil, BlobInfo{}, fmt.Errorf("inspect cache blob %q: %w", id, err)
	}
	digest := sha256.New()
	size, err := io.Copy(digest, file)
	if err != nil {
		_ = file.Close()
		return nil, BlobInfo{}, fmt.Errorf("inspect cache blob %q content: %w", id, err)
	}
	var sum [sha256.Size]byte
	copy(sum[:], digest.Sum(nil))
	computed := cache.BlobIDFromDigest(sum)
	if computed != id || size != before.Size() {
		_ = file.Close()
		return nil, BlobInfo{}, fmt.Errorf("%w: blob basename=%s computed=%s size=%d stat-size=%d", ErrIdentityMismatch, id, computed, size, before.Size())
	}
	if err := validateOpenAndPathStable(path, file, before); err != nil {
		_ = file.Close()
		return nil, BlobInfo{}, fmt.Errorf("inspect cache blob %q stability: %w", id, err)
	}
	if _, err := file.Seek(0, io.SeekStart); err != nil {
		_ = file.Close()
		return nil, BlobInfo{}, fmt.Errorf("inspect cache blob %q rewind: %w", id, err)
	}
	return file, BlobInfo{Identity: BlobIdentity{ID: id, Size: size}, Path: path}, nil
}

func (store *Store) InspectMaterialization(id cache.MaterializationID) (MaterializationInfo, error) {
	file, info, err := store.openMaterialization(id)
	if err != nil {
		return MaterializationInfo{}, err
	}
	if err := file.Close(); err != nil {
		return MaterializationInfo{}, fmt.Errorf("inspect cache materialization close: %w", err)
	}
	return info, nil
}

func (store *Store) OpenMaterialization(id cache.MaterializationID) (*os.File, MaterializationInfo, error) {
	return store.openMaterialization(id)
}

func (store *Store) openMaterialization(id cache.MaterializationID) (*os.File, MaterializationInfo, error) {
	path, err := store.MaterializationPath(id)
	if err != nil {
		return nil, MaterializationInfo{}, err
	}
	if err := validatePrivateDirectory(filepath.Dir(path)); err != nil {
		return nil, MaterializationInfo{}, fmt.Errorf("inspect cache materialization %q directory: %w", id, err)
	}
	file, before, err := openPrivateRegular(path)
	if err != nil {
		return nil, MaterializationInfo{}, fmt.Errorf("inspect cache materialization %q: %w", id, err)
	}
	digest := sha256.New()
	size, err := io.Copy(digest, file)
	if err != nil {
		_ = file.Close()
		return nil, MaterializationInfo{}, fmt.Errorf("inspect cache materialization %q content: %w", id, err)
	}
	if size != before.Size() {
		_ = file.Close()
		return nil, MaterializationInfo{}, fmt.Errorf("inspect cache materialization %q: size changed", id)
	}
	if err := validateOpenAndPathStable(path, file, before); err != nil {
		_ = file.Close()
		return nil, MaterializationInfo{}, fmt.Errorf("inspect cache materialization %q stability: %w", id, err)
	}
	var sum [sha256.Size]byte
	copy(sum[:], digest.Sum(nil))
	if _, err := file.Seek(0, io.SeekStart); err != nil {
		_ = file.Close()
		return nil, MaterializationInfo{}, fmt.Errorf("inspect cache materialization %q rewind: %w", id, err)
	}
	return file, MaterializationInfo{ID: id, SHA256: cache.BlobIDFromDigest(sum), Size: size, Path: path}, nil
}

func openPrivateRegular(path string) (*os.File, os.FileInfo, error) {
	before, err := os.Lstat(path) //nolint:gosec // exact ID-derived cache path
	if err != nil {
		return nil, nil, fmt.Errorf("lstat private cache file: %w", err)
	}
	if err := validatePrivateRegular(path, before); err != nil {
		return nil, nil, err
	}
	file, err := os.Open(path) //nolint:gosec // exact ID-derived path revalidated against the opened handle below
	if err != nil {
		return nil, nil, fmt.Errorf("open private cache file: %w", err)
	}
	opened, err := file.Stat()
	if err != nil {
		_ = file.Close()
		return nil, nil, fmt.Errorf("stat opened private cache file: %w", err)
	}
	if !os.SameFile(before, opened) {
		_ = file.Close()
		return nil, nil, fmt.Errorf("private cache file identity changed while opening")
	}
	if err := validateOpenPrivateRegular(path, opened); err != nil {
		_ = file.Close()
		return nil, nil, err
	}
	return file, before, nil
}

func validatePrivateRegular(path string, metadata os.FileInfo) error {
	if err := validateOpenPrivateRegular(path, metadata); err != nil {
		return err
	}
	if err := platform.ValidatePrivateFile(path, platform.ValidatePersistent); err != nil {
		return fmt.Errorf("validate owner-private cache file %q: %w", path, err)
	}
	return nil
}

func validateOpenPrivateRegular(path string, metadata os.FileInfo) error {
	if metadata == nil || !metadata.Mode().IsRegular() || metadata.Mode().Perm() != 0o600 {
		return fmt.Errorf("private cache file %q must be regular with mode 0600", path)
	}
	stat, ok := metadata.Sys().(*syscall.Stat_t)
	if !ok || stat.Nlink != 1 {
		return fmt.Errorf("private cache file %q must have exactly one hard link", path)
	}
	return nil
}

func validateOpenAndPathStable(path string, file *os.File, before os.FileInfo) error {
	afterOpen, err := file.Stat()
	if err != nil {
		return fmt.Errorf("restat opened cache file: %w", err)
	}
	afterPath, err := os.Lstat(path) //nolint:gosec // exact ID-derived cache path
	if err != nil {
		return fmt.Errorf("relstat cache file path: %w", err)
	}
	if !os.SameFile(before, afterOpen) || !os.SameFile(before, afterPath) || before.Size() != afterOpen.Size() || before.Size() != afterPath.Size() || before.Mode() != afterOpen.Mode() || before.Mode() != afterPath.Mode() || !before.ModTime().Equal(afterOpen.ModTime()) || !before.ModTime().Equal(afterPath.ModTime()) {
		return fmt.Errorf("cache file identity, size, mode, or modification time changed")
	}
	if err := validateOpenPrivateRegular(path, afterOpen); err != nil {
		return err
	}
	return validatePrivateRegular(path, afterPath)
}

func removeExactFile(path string, expected os.FileInfo) error {
	current, err := os.Lstat(path) //nolint:gosec // exact random staging path
	if err != nil {
		return err
	}
	if !os.SameFile(expected, current) {
		return fmt.Errorf("refuse to remove changed cache temp")
	}
	if err := validatePrivateRegular(path, current); err != nil {
		return err
	}
	return os.Remove(path)
}
