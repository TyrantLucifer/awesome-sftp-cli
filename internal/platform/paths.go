package platform

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
)

const (
	applicationID                    = "io.github.tyrantlucifer.amsftp"
	applicationSlug                  = "amsftp"
	controlSocketName                = "control-v1.sock"
	lockFileName                     = "daemon.lock"
	maxControlSocketBytes            = 100
	DiagnosticUnsafePreferredRuntime = "unsafe_preferred_runtime"
)

type Overrides struct {
	ConfigFile string
	StateDir   string
	CacheDir   string
	RuntimeDir string
}

type Paths struct {
	ConfigDir     string
	ConfigFile    string
	StateDir      string
	DatabaseFile  string
	LogDir        string
	LogFile       string
	CacheDir      string
	RuntimeDir    string
	ControlSocket string
	LockFile      string
}

type Diagnostic struct {
	Code    string
	Message string
}

type pathSources struct {
	goos          string
	euid          int
	homeDir       string
	userConfigDir string
	userCacheDir  string
	installRoot   string
	environment   map[string]string
}

func ResolvePaths(overrides Overrides) (Paths, []Diagnostic, error) {
	installRoot, err := DiscoverManagedInstallRoot()
	if err != nil {
		return Paths{}, nil, fmt.Errorf("resolve managed install root: %w", err)
	}
	homeDir, userConfigDir, userCacheDir := installRoot, installRoot, installRoot
	if installRoot == "" {
		homeDir, err = os.UserHomeDir()
		if err != nil {
			return Paths{}, nil, fmt.Errorf("resolve home directory: %w", err)
		}
		userConfigDir, err = currentUserConfigDir(runtime.GOOS, homeDir)
		if err != nil {
			return Paths{}, nil, err
		}
		userCacheDir, err = currentUserCacheDir(runtime.GOOS, homeDir)
		if err != nil {
			return Paths{}, nil, err
		}
	}

	environment := make(map[string]string, 5)
	for _, name := range []string{"TMPDIR", "XDG_CONFIG_HOME", "XDG_STATE_HOME", "XDG_CACHE_HOME", "XDG_RUNTIME_DIR"} {
		if value, ok := os.LookupEnv(name); ok {
			environment[name] = value
		}
	}

	return resolvePaths(pathSources{
		goos:          runtime.GOOS,
		euid:          os.Geteuid(),
		homeDir:       homeDir,
		userConfigDir: userConfigDir,
		userCacheDir:  userCacheDir,
		installRoot:   installRoot,
		environment:   environment,
	}, overrides)
}

func currentUserConfigDir(goos, homeDir string) (string, error) {
	if goos == "linux" {
		if value, ok := os.LookupEnv("XDG_CONFIG_HOME"); ok && value != "" && !filepath.IsAbs(value) {
			return filepath.Join(homeDir, ".config"), nil
		}
	}
	directory, err := os.UserConfigDir()
	if err != nil {
		return "", fmt.Errorf("resolve user config directory: %w", err)
	}
	return directory, nil
}

func currentUserCacheDir(goos, homeDir string) (string, error) {
	if goos == "linux" {
		if value, ok := os.LookupEnv("XDG_CACHE_HOME"); ok && value != "" && !filepath.IsAbs(value) {
			return filepath.Join(homeDir, ".cache"), nil
		}
	}
	directory, err := os.UserCacheDir()
	if err != nil {
		return "", fmt.Errorf("resolve user cache directory: %w", err)
	}
	return directory, nil
}

func resolvePaths(sources pathSources, overrides Overrides) (Paths, []Diagnostic, error) {
	if sources.goos != "darwin" && sources.goos != "linux" {
		return Paths{}, nil, fmt.Errorf("unsupported operating system %q", sources.goos)
	}
	if sources.euid < 0 {
		return Paths{}, nil, fmt.Errorf("effective uid must be non-negative")
	}
	if sources.installRoot == "" {
		for name, path := range map[string]string{
			"home directory":        sources.homeDir,
			"user config directory": sources.userConfigDir,
			"user cache directory":  sources.userCacheDir,
		} {
			if err := validateCanonicalAbsolutePath(path); err != nil {
				return Paths{}, nil, fmt.Errorf("%s: %w", name, err)
			}
		}
	} else {
		if err := validateCanonicalAbsolutePath(sources.installRoot); err != nil {
			return Paths{}, nil, fmt.Errorf("managed install root: %w", err)
		}
	}
	if value := sources.environment["XDG_STATE_HOME"]; sources.installRoot == "" && value != "" && filepath.IsAbs(value) {
		if err := validateCanonicalAbsolutePath(value); err != nil {
			return Paths{}, nil, fmt.Errorf("XDG_STATE_HOME: %w", err)
		}
	}
	for name, path := range map[string]string{
		"config file override": overrides.ConfigFile,
		"state override":       overrides.StateDir,
		"cache override":       overrides.CacheDir,
		"runtime override":     overrides.RuntimeDir,
	} {
		if path == "" {
			continue
		}
		if err := validateCanonicalAbsolutePath(path); err != nil {
			return Paths{}, nil, fmt.Errorf("%s: %w", name, err)
		}
	}

	var paths Paths
	var preferredRuntime string
	fallbackRuntime := filepath.Join("/tmp", applicationSlug+"-"+strconv.Itoa(sources.euid))
	var diagnostics []Diagnostic

	if sources.installRoot != "" {
		paths.ConfigDir = filepath.Join(sources.installRoot, "config")
		paths.StateDir = filepath.Join(sources.installRoot, "state")
		paths.LogDir = filepath.Join(paths.StateDir, "log")
		paths.CacheDir = filepath.Join(sources.installRoot, "cache")
		if sources.goos == "darwin" {
			preferredRuntime, diagnostics = preferredRuntimeDirectory(
				sources.environment["TMPDIR"],
				applicationSlug+"-"+strconv.Itoa(sources.euid),
				fallbackRuntime,
			)
		} else {
			preferredRuntime, diagnostics = preferredRuntimeDirectory(
				sources.environment["XDG_RUNTIME_DIR"],
				applicationSlug,
				fallbackRuntime,
			)
		}
	} else {
		switch sources.goos {
		case "darwin":
			paths.ConfigDir = filepath.Join(sources.userConfigDir, applicationID)
			paths.StateDir = filepath.Join(paths.ConfigDir, "state")
			paths.LogDir = filepath.Join(sources.homeDir, "Library", "Logs", applicationID)
			paths.CacheDir = filepath.Join(sources.userCacheDir, applicationID)
			preferredRuntime, diagnostics = preferredRuntimeDirectory(
				sources.environment["TMPDIR"],
				applicationSlug+"-"+strconv.Itoa(sources.euid),
				fallbackRuntime,
			)
		case "linux":
			stateBase := absoluteEnvironmentOrDefault(sources.environment["XDG_STATE_HOME"], filepath.Join(sources.homeDir, ".local", "state"))
			paths.ConfigDir = filepath.Join(sources.userConfigDir, applicationSlug)
			paths.StateDir = filepath.Join(stateBase, applicationSlug)
			paths.LogDir = filepath.Join(paths.StateDir, "log")
			paths.CacheDir = filepath.Join(sources.userCacheDir, applicationSlug)
			preferredRuntime, diagnostics = preferredRuntimeDirectory(
				sources.environment["XDG_RUNTIME_DIR"],
				applicationSlug,
				fallbackRuntime,
			)
		}
	}

	if overrides.ConfigFile != "" {
		paths.ConfigFile = overrides.ConfigFile
		paths.ConfigDir = filepath.Dir(overrides.ConfigFile)
	} else {
		paths.ConfigFile = filepath.Join(paths.ConfigDir, "config.json")
	}
	if overrides.StateDir != "" {
		paths.StateDir = overrides.StateDir
		if sources.goos == "linux" {
			paths.LogDir = filepath.Join(paths.StateDir, "log")
		}
	}
	if overrides.CacheDir != "" {
		paths.CacheDir = overrides.CacheDir
	}
	if overrides.RuntimeDir != "" {
		preferredRuntime = overrides.RuntimeDir
		diagnostics = nil
	}

	paths.DatabaseFile = filepath.Join(paths.StateDir, "amsftp.db")
	paths.LogFile = filepath.Join(paths.LogDir, "daemon.jsonl")
	paths.RuntimeDir = preferredRuntime
	paths.ControlSocket = filepath.Join(paths.RuntimeDir, controlSocketName)
	paths.LockFile = filepath.Join(paths.RuntimeDir, lockFileName)

	if err := validateControlSocketPath(paths.ControlSocket); err != nil {
		if overrides.RuntimeDir != "" || paths.RuntimeDir == fallbackRuntime {
			return Paths{}, nil, err
		}
		paths.RuntimeDir = fallbackRuntime
		paths.ControlSocket = filepath.Join(paths.RuntimeDir, controlSocketName)
		paths.LockFile = filepath.Join(paths.RuntimeDir, lockFileName)
		diagnostics = []Diagnostic{unsafePreferredRuntimeDiagnostic()}
		if err := validateControlSocketPath(paths.ControlSocket); err != nil {
			return Paths{}, nil, err
		}
	}

	return paths, diagnostics, nil
}

func absoluteEnvironmentOrDefault(value, fallback string) string {
	if value != "" && filepath.IsAbs(value) {
		return value
	}
	return fallback
}

func preferredRuntimeDirectory(value, suffix, fallback string) (string, []Diagnostic) {
	if value == "" {
		return fallback, nil
	}
	if !filepath.IsAbs(value) || filepath.Clean(value) != value || strings.IndexByte(value, 0) >= 0 {
		return fallback, []Diagnostic{unsafePreferredRuntimeDiagnostic()}
	}
	return filepath.Join(value, suffix), nil
}

func unsafePreferredRuntimeDiagnostic() Diagnostic {
	return Diagnostic{
		Code:    DiagnosticUnsafePreferredRuntime,
		Message: "preferred runtime directory is unsafe; using the validated short fallback",
	}
}

func validateCanonicalAbsolutePath(path string) error {
	if path == "" {
		return fmt.Errorf("path is empty")
	}
	if strings.IndexByte(path, 0) >= 0 {
		return fmt.Errorf("path contains NUL")
	}
	if !filepath.IsAbs(path) {
		return fmt.Errorf("path is not absolute")
	}
	if filepath.Clean(path) != path {
		return fmt.Errorf("path is not lexically clean")
	}
	return nil
}

func validateControlSocketPath(path string) error {
	if err := validateCanonicalAbsolutePath(path); err != nil {
		return fmt.Errorf("control socket path: %w", err)
	}
	if length := len([]byte(path)); length > maxControlSocketBytes {
		return fmt.Errorf("control socket path is %d bytes; maximum is %d", length, maxControlSocketBytes)
	}
	return nil
}
