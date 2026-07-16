// Package cachefs owns the private, crash-conservative filesystem portion of
// the Stage 3 content cache. It publishes only verified immutable blobs. It
// never deletes findings discovered by Scan, and quarantine is reserved only
// as an owner-private preserve/report location.
package cachefs

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"

	"github.com/TyrantLucifer/awesome-mac-sftp/internal/cache"
	"github.com/TyrantLucifer/awesome-mac-sftp/internal/platform"
)

const ContentRootName = "content-v1"

var (
	ErrIdentityMismatch = errors.New("cache content identity mismatch")
	ErrLimitExceeded    = errors.New("cache stream byte limit exceeded")
)

type Store struct {
	root      string
	publishMu sync.Mutex
}

func Initialize(cacheRoot string) (*Store, error) {
	if !filepath.IsAbs(cacheRoot) || filepath.Clean(cacheRoot) != cacheRoot {
		return nil, fmt.Errorf("initialize cache filesystem: cache root must be canonical and absolute")
	}
	if err := validatePrivateDirectory(cacheRoot); err != nil {
		return nil, fmt.Errorf("initialize cache filesystem root: %w", err)
	}

	root := filepath.Join(cacheRoot, ContentRootName)
	directories := []string{
		root,
		filepath.Join(root, "blobs"),
		filepath.Join(root, "blobs", "sha256"),
		filepath.Join(root, "entries"),
		filepath.Join(root, "materializations"),
		filepath.Join(root, "staging"),
		filepath.Join(root, "quarantine"),
	}
	for _, directory := range directories {
		if err := ensurePrivateDirectory(directory); err != nil {
			return nil, fmt.Errorf("initialize cache filesystem layout: %w", err)
		}
	}
	return &Store{root: root}, nil
}

func (store *Store) Root() string {
	if store == nil {
		return ""
	}
	return store.root
}

func (store *Store) BlobPath(id cache.BlobID) (string, error) {
	if store == nil {
		return "", fmt.Errorf("derive cache blob path: nil store")
	}
	parsed, err := cache.ParseBlobID(string(id))
	if err != nil {
		return "", fmt.Errorf("derive cache blob path: %w", err)
	}
	value := string(parsed)
	return filepath.Join(store.root, "blobs", "sha256", value[:2], value+".blob"), nil
}

func (store *Store) MaterializationPath(id cache.MaterializationID) (string, error) {
	if store == nil {
		return "", fmt.Errorf("derive cache materialization path: nil store")
	}
	parsed, err := cache.ParseMaterializationID(string(id))
	if err != nil {
		return "", fmt.Errorf("derive cache materialization path: %w", err)
	}
	return filepath.Join(store.root, "materializations", string(parsed), "content"), nil
}

func ensurePrivateDirectory(path string) error {
	metadata, err := os.Lstat(path) //nolint:gosec // exact path derived beneath a validated private root
	if err == nil {
		if err := validatePrivateDirectoryMetadata(path, metadata); err != nil {
			return err
		}
		return validatePrivateDirectory(path)
	}
	if !os.IsNotExist(err) {
		return fmt.Errorf("inspect private cache directory %q: %w", path, err)
	}
	if err := os.Mkdir(path, 0o700); err != nil {
		if !os.IsExist(err) {
			return fmt.Errorf("create private cache directory %q: %w", path, err)
		}
	}
	metadata, err = os.Lstat(path) //nolint:gosec // exact path derived beneath a validated private root
	if err != nil {
		return fmt.Errorf("reinspect private cache directory %q: %w", path, err)
	}
	if err := validatePrivateDirectoryMetadata(path, metadata); err != nil {
		return err
	}
	if err := validatePrivateDirectory(path); err != nil {
		return err
	}
	if err := syncDirectory(filepath.Dir(path)); err != nil {
		return fmt.Errorf("persist private cache directory %q: %w", path, err)
	}
	return nil
}

func validatePrivateDirectory(path string) error {
	if err := platform.ValidatePrivateDirectory(path, platform.ValidatePersistent); err != nil {
		return fmt.Errorf("validate owner-private cache directory %q: %w", path, err)
	}
	return nil
}

func validatePrivateDirectoryMetadata(path string, metadata os.FileInfo) error {
	if metadata == nil || !metadata.IsDir() || metadata.Mode()&os.ModeSymlink != 0 || metadata.Mode().Perm() != 0o700 {
		return fmt.Errorf("private cache directory %q must be a real directory with mode 0700", path)
	}
	return nil
}

func syncDirectory(path string) error {
	directory, err := os.Open(path) //nolint:gosec // exact validated cache directory
	if err != nil {
		return fmt.Errorf("open directory for sync %q: %w", path, err)
	}
	defer directory.Close()
	if err := directory.Sync(); err != nil {
		return fmt.Errorf("sync directory %q: %w", path, err)
	}
	return nil
}
