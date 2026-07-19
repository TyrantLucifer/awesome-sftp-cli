//go:build darwin || linux

package app

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/TyrantLucifer/awesome-sftp-cli/internal/config"
	"github.com/TyrantLucifer/awesome-sftp-cli/internal/platform"
)

func replaceApplicationConfig(path string, input config.Config) (resultErr error) {
	absolute, err := filepath.Abs(path)
	if err != nil {
		return fmt.Errorf("replace config: resolve path: %w", err)
	}
	directory := filepath.Dir(absolute)
	if err := platform.ValidatePrivateDirectory(directory, platform.ValidatePersistent); err != nil {
		return fmt.Errorf("replace config: validate directory: %w", err)
	}
	temporary, err := os.CreateTemp(directory, ".config-*.tmp")
	if err != nil {
		return fmt.Errorf("replace config: create temporary file: %w", err)
	}
	temporaryPath := temporary.Name()
	defer func() {
		_ = temporary.Close()
		if resultErr != nil {
			_ = os.Remove(temporaryPath)
		}
	}()
	// #nosec G302 -- configuration is owner-private state with exact mode 0600.
	if err := temporary.Chmod(0o600); err != nil {
		return fmt.Errorf("replace config: set temporary mode: %w", err)
	}
	if err := config.Write(temporary, input); err != nil {
		return fmt.Errorf("replace config: %w", err)
	}
	if err := temporary.Sync(); err != nil {
		return fmt.Errorf("replace config: sync temporary file: %w", err)
	}
	if err := temporary.Close(); err != nil {
		return fmt.Errorf("replace config: close temporary file: %w", err)
	}
	if err := platform.ValidatePrivateFile(temporaryPath, platform.ValidatePersistent); err != nil {
		return fmt.Errorf("replace config: validate temporary file: %w", err)
	}
	if err := os.Rename(temporaryPath, absolute); err != nil {
		return fmt.Errorf("replace config: publish: %w", err)
	}
	if err := platform.ValidatePrivateFile(absolute, platform.ValidatePersistent); err != nil {
		return fmt.Errorf("replace config: validate published file: %w", err)
	}
	if err := syncApplicationConfigDirectory(directory); err != nil {
		return fmt.Errorf("replace config: sync directory: %w", err)
	}
	return nil
}

func syncApplicationConfigDirectory(path string) error {
	// #nosec G304 -- caller passes only the validated configuration parent directory.
	directory, err := os.Open(path)
	if err != nil {
		return err
	}
	defer directory.Close()
	return directory.Sync()
}
