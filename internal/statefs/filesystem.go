package statefs

import (
	"fmt"
	"path/filepath"

	"github.com/TyrantLucifer/awesome-mac-sftp/internal/platform"
)

type Filesystem string

const (
	FilesystemAPFS Filesystem = "apfs"
	FilesystemExt4 Filesystem = "ext4"
	FilesystemXFS  Filesystem = "xfs"
)

func ValidateRoot(root string) (Filesystem, error) {
	if !filepath.IsAbs(root) || filepath.Clean(root) != root {
		return "", fmt.Errorf("validate state root: path must be canonical and absolute")
	}
	if err := platform.ValidatePrivateDirectory(root, platform.ValidatePersistent); err != nil {
		return "", fmt.Errorf("validate state root trust: %w", err)
	}
	filesystem, err := detectApprovedFilesystem(root)
	if err != nil {
		return "", fmt.Errorf("validate state root filesystem: %w", err)
	}
	return filesystem, nil
}
