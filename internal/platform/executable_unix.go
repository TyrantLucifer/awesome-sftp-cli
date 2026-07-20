//go:build darwin || linux

package platform

import (
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"syscall"
)

// ResolveTrustedExecutable freezes a symlinked launch path to its canonical
// target, then applies the regular-file, owner, mode, ancestor, and ACL checks
// to the exact path that the caller will execute. This keeps package-manager
// entry points usable without executing through a mutable symlink afterward.
func ResolveTrustedExecutable(path string) (string, error) {
	absolute, err := filepath.Abs(path)
	if err != nil {
		return "", fmt.Errorf("resolve executable absolute path: %w", err)
	}
	resolved, err := filepath.EvalSymlinks(absolute)
	if err != nil {
		return "", fmt.Errorf("resolve executable symlinks: %w", err)
	}
	resolved = filepath.Clean(resolved)
	if err := ValidateExecutable(resolved); err != nil {
		return "", err
	}
	return resolved, nil
}

func ValidateExecutable(path string) error {
	validator, err := currentTrustValidator()
	if err != nil {
		return err
	}
	resolved, err := validator.resolveTrustedSystemAlias(path, ValidatePersistent)
	if err != nil {
		return fmt.Errorf("validate executable path: %w", err)
	}
	components, err := absolutePathComponents(resolved)
	if err != nil {
		return err
	}
	for index, component := range components {
		metadata, err := validator.filesystem.lstat(component)
		if err != nil {
			return fmt.Errorf("inspect executable path %q: %w", component, err)
		}
		if index != len(components)-1 {
			if err := validator.validateAncestorMetadata(component, metadata, ValidatePersistent); err != nil {
				return err
			}
		} else {
			if metadata.mode&fs.ModeType != 0 {
				return fmt.Errorf("executable %q is not a regular file", component)
			}
			if metadata.uid != 0 && metadata.uid != validator.euid {
				return fmt.Errorf("executable %q is owned by uid %d", component, metadata.uid)
			}
			if metadata.mode.Perm()&0o022 != 0 {
				return fmt.Errorf("executable %q is writable by group or other", component)
			}
			if metadata.mode&(fs.ModeSetuid|fs.ModeSetgid|fs.ModeSticky) != 0 {
				return fmt.Errorf("executable %q has special permission bits", component)
			}
			if err := syscall.Access(component, 1); err != nil {
				return fmt.Errorf("executable %q is not executable: %w", component, err)
			}
		}
		if err := validator.acls.validateACL(component, aclIntegrityOnly, false); err != nil {
			return fmt.Errorf("validate executable ACL for %q: %w", component, err)
		}
	}
	return nil
}

func ExecutableIdentity(path string) (fs.FileInfo, error) {
	if err := ValidateExecutable(path); err != nil {
		return nil, err
	}
	info, err := os.Lstat(path)
	if err != nil {
		return nil, fmt.Errorf("inspect validated executable: %w", err)
	}
	return info, nil
}
