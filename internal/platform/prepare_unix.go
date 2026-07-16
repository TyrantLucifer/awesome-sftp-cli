//go:build darwin || linux

package platform

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
)

// PreparePrivateDirectory creates a private root one component at a time and
// validates the complete trust chain after every creation.
func PreparePrivateDirectory(path string, purpose ValidationPurpose) error {
	if err := validatePurpose(purpose); err != nil {
		return err
	}
	components, err := absolutePathComponents(path)
	if err != nil {
		return err
	}
	created := make([]string, 0, len(components))
	rollback := func() {
		for index := len(created) - 1; index >= 0; index-- {
			_ = os.Remove(created[index])
		}
	}
	for index, component := range components {
		if index == 0 {
			continue
		}
		_, err := os.Lstat(component)
		if err == nil {
			continue
		}
		if !errors.Is(err, os.ErrNotExist) {
			rollback()
			return fmt.Errorf("inspect private directory component %q: %w", component, err)
		}
		parent := filepath.Dir(component)
		if err := validateTrustedAncestorForCreate(parent, purpose); err != nil {
			rollback()
			return err
		}
		if err := os.Mkdir(component, 0o700); err != nil {
			rollback()
			return fmt.Errorf("create private directory %q: %w", component, err)
		}
		created = append(created, component)
		// #nosec G302 -- this API creates owner-private directories with exact mode 0700.
		if err := os.Chmod(component, 0o700); err != nil {
			rollback()
			return fmt.Errorf("set private directory mode %q: %w", component, err)
		}
		if err := validateTrustedAncestorForCreate(parent, purpose); err != nil {
			rollback()
			return err
		}
	}
	if err := ValidatePrivateDirectory(path, purpose); err != nil {
		rollback()
		return fmt.Errorf("validate prepared private directory: %w", err)
	}
	return nil
}

func validateTrustedAncestorForCreate(path string, purpose ValidationPurpose) error {
	validator, err := currentTrustValidator()
	if err != nil {
		return err
	}
	resolved, err := validator.resolveTrustedSystemAlias(path, purpose)
	if err != nil {
		return err
	}
	components, err := absolutePathComponents(resolved)
	if err != nil {
		return err
	}
	for _, component := range components {
		metadata, err := validator.filesystem.lstat(component)
		if err != nil {
			return fmt.Errorf("inspect %q: %w", component, err)
		}
		if err := validator.validateAncestorMetadata(component, metadata, purpose); err != nil {
			return err
		}
		if err := validator.acls.validateACL(component, aclIntegrityOnly, purpose != ValidatePersistent); err != nil {
			return fmt.Errorf("validate integrity ACL for %q: %w", component, err)
		}
	}
	return nil
}

// PrepareRuntimeDirectory validates the preferred runtime root and, when
// allowed, safely falls back to the user-specific sticky-/tmp root.
func PrepareRuntimeDirectory(paths Paths, allowFallback bool) (Paths, []Diagnostic, error) {
	fallback := filepath.Join("/tmp", applicationSlug+"-"+strconv.Itoa(os.Geteuid()))
	purpose := ValidateRuntime
	if paths.RuntimeDir == fallback {
		purpose = ValidateRuntimeFallback
	}
	if err := PreparePrivateDirectory(paths.RuntimeDir, purpose); err == nil {
		return paths, nil, nil
	} else if !allowFallback || paths.RuntimeDir == fallback {
		return Paths{}, nil, fmt.Errorf("prepare runtime directory: %w", err)
	}
	paths.RuntimeDir = fallback
	paths.ControlSocket = filepath.Join(fallback, controlSocketName)
	paths.LockFile = filepath.Join(fallback, lockFileName)
	if err := PreparePrivateDirectory(fallback, ValidateRuntimeFallback); err != nil {
		return Paths{}, nil, fmt.Errorf("prepare fallback runtime directory: %w", err)
	}
	return paths, []Diagnostic{unsafePreferredRuntimeDiagnostic()}, nil
}

func RuntimeValidationPurpose(paths Paths) ValidationPurpose {
	fallback := filepath.Join("/tmp", applicationSlug+"-"+strconv.Itoa(os.Geteuid()))
	if paths.RuntimeDir == fallback {
		return ValidateRuntimeFallback
	}
	return ValidateRuntime
}
