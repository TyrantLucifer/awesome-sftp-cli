package platform

import (
	"errors"
	"fmt"
	"io/fs"
	"path/filepath"
	"strings"
)

type aclProfile uint8

const (
	aclIntegrityOnly aclProfile = iota + 1
	aclOwnerPrivate
)

type ValidationPurpose uint8

const (
	ValidatePersistent ValidationPurpose = iota + 1
	ValidateRuntime
	ValidateRuntimeFallback
)

type trustMetadata struct {
	mode fs.FileMode
	uid  int
}

type trustFilesystem interface {
	lstat(string) (trustMetadata, error)
	readlink(string) (string, error)
}

type aclValidator interface {
	validateACL(path string, profile aclProfile, runtimeFilesystemAllowed bool) error
}

type trustValidator struct {
	goos       string
	euid       int
	filesystem trustFilesystem
	acls       aclValidator
}

func (v trustValidator) validatePrivateCreatePath(path string, purpose ValidationPurpose) error {
	if err := validatePurpose(purpose); err != nil {
		return err
	}
	resolved, err := v.resolveTrustedSystemAlias(path, purpose)
	if err != nil {
		return err
	}
	components, err := absolutePathComponents(resolved)
	if err != nil {
		return err
	}
	for index, component := range components {
		metadata, err := v.filesystem.lstat(component)
		if errors.Is(err, fs.ErrNotExist) {
			return nil
		}
		if err != nil {
			return fmt.Errorf("inspect %q: %w", component, err)
		}
		if index == len(components)-1 {
			if err := v.validatePrivateDirectoryMetadata(component, metadata); err != nil {
				return err
			}
			if err := v.acls.validateACL(component, aclOwnerPrivate, purpose != ValidatePersistent); err != nil {
				return fmt.Errorf("validate owner-private ACL for %q: %w", component, err)
			}
			return nil
		}
		if err := v.validateAncestorMetadata(component, metadata, purpose); err != nil {
			return err
		}
		if err := v.acls.validateACL(component, aclIntegrityOnly, purpose != ValidatePersistent); err != nil {
			return fmt.Errorf("validate integrity ACL for %q: %w", component, err)
		}
	}
	return nil
}

func (v trustValidator) validateIntegrityCreatePath(path string, purpose ValidationPurpose) error {
	if err := validatePurpose(purpose); err != nil {
		return err
	}
	resolved, err := v.resolveTrustedSystemAlias(path, purpose)
	if err != nil {
		return err
	}
	components, err := absolutePathComponents(resolved)
	if err != nil {
		return err
	}
	for _, component := range components {
		metadata, err := v.filesystem.lstat(component)
		if errors.Is(err, fs.ErrNotExist) {
			return nil
		}
		if err != nil {
			return fmt.Errorf("inspect %q: %w", component, err)
		}
		if err := v.validateAncestorMetadata(component, metadata, purpose); err != nil {
			return err
		}
		if err := v.acls.validateACL(component, aclIntegrityOnly, purpose != ValidatePersistent); err != nil {
			return fmt.Errorf("validate integrity ACL for %q: %w", component, err)
		}
	}
	return nil
}

func (v trustValidator) validatePrivateDirectory(path string, purpose ValidationPurpose) error {
	if err := validatePurpose(purpose); err != nil {
		return err
	}
	resolved, err := v.resolveTrustedSystemAlias(path, purpose)
	if err != nil {
		return err
	}
	components, err := absolutePathComponents(resolved)
	if err != nil {
		return err
	}

	for index, component := range components {
		metadata, err := v.filesystem.lstat(component)
		if err != nil {
			return fmt.Errorf("inspect %q: %w", component, err)
		}
		if index == len(components)-1 {
			if err := v.validatePrivateDirectoryMetadata(component, metadata); err != nil {
				return err
			}
			if err := v.acls.validateACL(component, aclOwnerPrivate, purpose != ValidatePersistent); err != nil {
				return fmt.Errorf("validate owner-private ACL for %q: %w", component, err)
			}
			continue
		}

		if err := v.validateAncestorMetadata(component, metadata, purpose); err != nil {
			return err
		}
		if err := v.acls.validateACL(component, aclIntegrityOnly, purpose != ValidatePersistent); err != nil {
			return fmt.Errorf("validate integrity ACL for %q: %w", component, err)
		}
	}
	return nil
}

func (v trustValidator) validatePrivateFile(path string, purpose ValidationPurpose) error {
	if err := validatePurpose(purpose); err != nil {
		return err
	}
	if err := validateCanonicalAbsolutePath(path); err != nil {
		return err
	}
	resolved, err := v.resolveTrustedSystemAlias(path, purpose)
	if err != nil {
		return err
	}
	if err := v.validatePrivateDirectory(filepath.Dir(resolved), purpose); err != nil {
		return err
	}

	metadata, err := v.filesystem.lstat(resolved)
	if err != nil {
		return fmt.Errorf("inspect %q: %w", resolved, err)
	}
	if metadata.uid != v.euid {
		return fmt.Errorf("private file %q is owned by uid %d, want %d", resolved, metadata.uid, v.euid)
	}
	if metadata.mode&fs.ModeType != 0 || exactPermissionBits(metadata.mode) != 0o600 {
		return fmt.Errorf("private file %q has mode %v, want regular 0600", resolved, metadata.mode)
	}
	if err := v.acls.validateACL(resolved, aclOwnerPrivate, purpose != ValidatePersistent); err != nil {
		return fmt.Errorf("validate owner-private ACL for %q: %w", resolved, err)
	}
	return nil
}

func (v trustValidator) validatePrivateSocket(path string, purpose ValidationPurpose) error {
	if err := validatePurpose(purpose); err != nil {
		return err
	}
	resolved, err := v.resolveTrustedSystemAlias(path, purpose)
	if err != nil {
		return err
	}
	if err := v.validatePrivateDirectory(filepath.Dir(resolved), purpose); err != nil {
		return err
	}
	metadata, err := v.filesystem.lstat(resolved)
	if err != nil {
		return fmt.Errorf("inspect %q: %w", resolved, err)
	}
	if metadata.uid != v.euid {
		return fmt.Errorf("control socket %q is owned by uid %d, want %d", resolved, metadata.uid, v.euid)
	}
	if metadata.mode&fs.ModeType != fs.ModeSocket || exactPermissionBits(metadata.mode) != 0o600 {
		return fmt.Errorf("control socket %q has mode %v, want socket 0600", resolved, metadata.mode)
	}
	if err := v.acls.validateACL(resolved, aclOwnerPrivate, purpose != ValidatePersistent); err != nil {
		return fmt.Errorf("validate owner-private ACL for %q: %w", resolved, err)
	}
	return nil
}

func (v trustValidator) validateAncestorMetadata(path string, metadata trustMetadata, purpose ValidationPurpose) error {
	if !metadata.mode.IsDir() {
		return fmt.Errorf("ancestor %q is not a real directory", path)
	}
	if metadata.uid != 0 && metadata.uid != v.euid {
		return fmt.Errorf("ancestor %q is owned by uid %d", path, metadata.uid)
	}
	if purpose == ValidateRuntimeFallback && v.isStickyRuntimeAncestor(path, metadata) {
		return nil
	}
	if metadata.mode.Perm()&0o022 != 0 {
		return fmt.Errorf("ancestor %q is writable by group or other", path)
	}
	return nil
}

func (v trustValidator) validatePrivateDirectoryMetadata(path string, metadata trustMetadata) error {
	if !metadata.mode.IsDir() {
		return fmt.Errorf("private root %q is not a real directory", path)
	}
	if metadata.uid != v.euid {
		return fmt.Errorf("private root %q is owned by uid %d, want %d", path, metadata.uid, v.euid)
	}
	if exactPermissionBits(metadata.mode) != 0o700 {
		return fmt.Errorf("private root %q has mode %v, want 0700", path, metadata.mode)
	}
	return nil
}

func (v trustValidator) isStickyRuntimeAncestor(path string, metadata trustMetadata) bool {
	want := "/tmp"
	if v.goos == "darwin" {
		want = "/private/tmp"
	}
	return path == want && metadata.uid == 0 && metadata.mode.IsDir() &&
		metadata.mode.Perm() == 0o777 && metadata.mode&fs.ModeSticky != 0
}

func (v trustValidator) resolveTrustedSystemAlias(path string, purpose ValidationPurpose) (string, error) {
	if err := validateCanonicalAbsolutePath(path); err != nil {
		return "", err
	}
	if v.goos != "darwin" {
		return path, nil
	}

	type alias struct {
		raw      string
		resolved string
		allowed  bool
	}
	aliases := []alias{
		{raw: "/var", resolved: "/private/var", allowed: true},
		{raw: "/tmp", resolved: "/private/tmp", allowed: purpose == ValidateRuntimeFallback},
	}
	for _, candidate := range aliases {
		if path != candidate.raw && !strings.HasPrefix(path, candidate.raw+string(filepath.Separator)) {
			continue
		}
		metadata, err := v.filesystem.lstat(candidate.raw)
		if err != nil {
			return "", fmt.Errorf("inspect Darwin system alias %q: %w", candidate.raw, err)
		}
		if metadata.mode&fs.ModeSymlink == 0 {
			return path, nil
		}
		if !candidate.allowed {
			return "", fmt.Errorf("darwin system alias %q is not allowed for this path", candidate.raw)
		}
		if metadata.uid != 0 {
			return "", fmt.Errorf("darwin system alias %q is not root owned", candidate.raw)
		}
		target, err := v.filesystem.readlink(candidate.raw)
		if err != nil {
			return "", fmt.Errorf("read Darwin system alias %q: %w", candidate.raw, err)
		}
		if !filepath.IsAbs(target) {
			target = filepath.Join("/", target)
		}
		if filepath.Clean(target) != candidate.resolved {
			return "", fmt.Errorf("darwin system alias %q has unexpected target", candidate.raw)
		}
		return candidate.resolved + strings.TrimPrefix(path, candidate.raw), nil
	}
	return path, nil
}

func validatePurpose(purpose ValidationPurpose) error {
	switch purpose {
	case ValidatePersistent, ValidateRuntime, ValidateRuntimeFallback:
		return nil
	default:
		return fmt.Errorf("unknown validation purpose %d", purpose)
	}
}

func exactPermissionBits(mode fs.FileMode) fs.FileMode {
	return mode & (fs.ModePerm | fs.ModeSetuid | fs.ModeSetgid | fs.ModeSticky)
}

func absolutePathComponents(path string) ([]string, error) {
	if err := validateCanonicalAbsolutePath(path); err != nil {
		return nil, err
	}
	if path == string(filepath.Separator) {
		return []string{path}, nil
	}
	parts := strings.Split(strings.TrimPrefix(path, string(filepath.Separator)), string(filepath.Separator))
	components := make([]string, 0, len(parts)+1)
	components = append(components, string(filepath.Separator))
	current := string(filepath.Separator)
	for _, part := range parts {
		current = filepath.Join(current, part)
		components = append(components, current)
	}
	return components, nil
}
