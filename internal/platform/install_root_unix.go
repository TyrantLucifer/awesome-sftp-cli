//go:build darwin || linux

package platform

import (
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
)

const (
	ManagedInstallRootMarker        = ".amsftp-root"
	ManagedInstallRootMarkerContent = "amsftp-managed-root-v1\n"
	maximumInstallMarkerBytes       = 64
)

// DiscoverManagedInstallRoot returns the validated root recorded beside a
// standalone executable. Absence means the normal platform paths remain in use.
func DiscoverManagedInstallRoot() (string, error) {
	executable, err := os.Executable()
	if err != nil {
		return "", err
	}
	return discoverManagedInstallRoot(executable)
}

func discoverManagedInstallRoot(executable string) (string, error) {
	absolute, err := filepath.Abs(executable)
	if err != nil {
		return "", err
	}
	resolved, err := filepath.EvalSymlinks(absolute)
	if err != nil {
		return "", err
	}
	root := filepath.Dir(filepath.Dir(resolved))
	marker := filepath.Join(root, ManagedInstallRootMarker)
	if _, err := os.Lstat(marker); errors.Is(err, fs.ErrNotExist) {
		return "", nil
	} else if err != nil {
		return "", err
	}
	if err := ValidateExecutable(resolved); err != nil {
		return "", fmt.Errorf("validate managed executable: %w", err)
	}
	if err := ValidatePrivateDirectory(root, ValidatePersistent); err != nil {
		return "", fmt.Errorf("validate managed root: %w", err)
	}
	if err := ValidatePrivateFile(marker, ValidatePersistent); err != nil {
		return "", fmt.Errorf("validate managed root marker: %w", err)
	}
	file, err := os.Open(marker) // #nosec G304 -- the path and every ancestor passed owner-private validation.
	if err != nil {
		return "", err
	}
	defer file.Close()
	raw, err := io.ReadAll(io.LimitReader(file, maximumInstallMarkerBytes+1))
	if err != nil {
		return "", err
	}
	if string(raw) != ManagedInstallRootMarkerContent {
		return "", errors.New("managed root marker is invalid")
	}
	return root, nil
}

// PreflightInstallation validates the target executable and all persistent
// roots without creating or replacing any filesystem object.
func PreflightInstallation(prefix string, managedRoot bool) error {
	if err := validateCanonicalAbsolutePath(prefix); err != nil {
		return fmt.Errorf("install prefix: %w", err)
	}
	if prefix == string(filepath.Separator) {
		return errors.New("install prefix cannot be the filesystem root")
	}
	if managedRoot {
		if err := ValidatePrivateDirectory(prefix, ValidatePersistent); err != nil {
			return fmt.Errorf("managed install root: %w", err)
		}
		marker := filepath.Join(prefix, ManagedInstallRootMarker)
		if _, err := os.Lstat(marker); err == nil {
			if err := ValidatePrivateFile(marker, ValidatePersistent); err != nil {
				return fmt.Errorf("managed install marker: %w", err)
			}
		} else if !errors.Is(err, fs.ErrNotExist) {
			return fmt.Errorf("inspect managed install marker: %w", err)
		}
	}
	executable := filepath.Join(prefix, "bin", "amsftp")
	if _, err := os.Lstat(executable); err == nil {
		if err := ValidateExecutable(executable); err != nil {
			return fmt.Errorf("installed executable: %w", err)
		}
	} else if errors.Is(err, fs.ErrNotExist) {
		if err := ValidateIntegrityCreatePath(filepath.Dir(executable), ValidatePersistent); err != nil {
			return fmt.Errorf("executable target: %w", err)
		}
	} else {
		return fmt.Errorf("inspect installed executable: %w", err)
	}

	sources, err := currentPathSources(prefix, managedRoot)
	if err != nil {
		return err
	}
	paths, _, err := resolvePaths(sources, Overrides{})
	if err != nil {
		return err
	}
	for name, path := range map[string]string{
		"config": paths.ConfigDir,
		"state":  paths.StateDir,
		"cache":  paths.CacheDir,
	} {
		if err := ValidatePrivateCreatePath(path, ValidatePersistent); err != nil {
			return fmt.Errorf("%s root: %w", name, err)
		}
	}
	return nil
}

func currentPathSources(prefix string, managedRoot bool) (pathSources, error) {
	environment := make(map[string]string, 5)
	for _, name := range []string{"TMPDIR", "XDG_CONFIG_HOME", "XDG_STATE_HOME", "XDG_CACHE_HOME", "XDG_RUNTIME_DIR"} {
		if value, ok := os.LookupEnv(name); ok {
			environment[name] = value
		}
	}
	if managedRoot {
		return pathSources{
			goos: runtime.GOOS, euid: os.Geteuid(), homeDir: prefix,
			userConfigDir: prefix, userCacheDir: prefix,
			installRoot: prefix, environment: environment,
		}, nil
	}
	homeDir, err := os.UserHomeDir()
	if err != nil {
		return pathSources{}, fmt.Errorf("resolve home directory: %w", err)
	}
	userConfigDir, err := currentUserConfigDir(runtime.GOOS, homeDir)
	if err != nil {
		return pathSources{}, err
	}
	userCacheDir, err := currentUserCacheDir(runtime.GOOS, homeDir)
	if err != nil {
		return pathSources{}, err
	}
	return pathSources{
		goos: runtime.GOOS, euid: os.Geteuid(), homeDir: homeDir,
		userConfigDir: userConfigDir, userCacheDir: userCacheDir,
		environment: environment,
	}, nil
}
