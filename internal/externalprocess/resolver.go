package externalprocess

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// ResolvedCommand freezes a canonical absolute executable and its file
// identity. Call Revalidate immediately before execution.
type ResolvedCommand struct {
	Executable string
	Args       []string

	identity os.FileInfo
}

func ResolveCommand(command Command, pathEnvironment string) (ResolvedCommand, error) {
	if err := validateCommand(command); err != nil {
		return ResolvedCommand{}, err
	}
	if !filepath.IsAbs(command.Executable) && strings.ContainsRune(command.Executable, filepath.Separator) {
		return ResolvedCommand{}, errors.New("resolve external command: relative executable containing slash is forbidden")
	}

	candidate := command.Executable
	if !filepath.IsAbs(candidate) {
		var err error
		candidate, err = discoverExecutable(candidate, pathEnvironment)
		if err != nil {
			return ResolvedCommand{}, err
		}
	}
	canonical, info, err := canonicalExecutable(candidate)
	if err != nil {
		return ResolvedCommand{}, err
	}
	return ResolvedCommand{
		Executable: canonical,
		Args:       append([]string(nil), command.Args...),
		identity:   info,
	}, nil
}

func (command ResolvedCommand) Revalidate() error {
	if !filepath.IsAbs(command.Executable) || filepath.Clean(command.Executable) != command.Executable || command.identity == nil {
		return errors.New("revalidate external command: command is not a resolved executable")
	}
	info, err := validateCanonicalExecutable(command.Executable)
	if err != nil {
		return fmt.Errorf("revalidate external command: %w", err)
	}
	if !os.SameFile(command.identity, info) {
		return errors.New("revalidate external command: executable file identity changed")
	}
	return nil
}

// ResolveEditor applies the frozen environment priority. A non-empty invalid
// higher-priority value is an error and never falls through to a lower value.
func ResolveEditor(environment map[string]string, pathEnvironment string) (ResolvedCommand, error) {
	for _, key := range []string{"AMSFTP_EDITOR", "VISUAL", "EDITOR"} {
		value := environment[key]
		if value == "" {
			continue
		}
		command, err := ParseEnvironmentCommand(value)
		if err != nil {
			return ResolvedCommand{}, fmt.Errorf("resolve editor from %s: %w", key, err)
		}
		resolved, err := ResolveCommand(command, pathEnvironment)
		if err != nil {
			return ResolvedCommand{}, fmt.Errorf("resolve editor from %s: %w", key, err)
		}
		return resolved, nil
	}
	for _, executable := range []string{"nvim", "vim", "vi"} {
		resolved, err := ResolveCommand(Command{Executable: executable}, pathEnvironment)
		if err == nil {
			return resolved, nil
		}
	}
	return ResolvedCommand{}, errors.New("resolve editor: no configured editor or nvim/vim/vi executable found")
}

func DefaultOpenerPath(goos string) (string, bool) {
	switch goos {
	case "darwin":
		return "/usr/bin/open", true
	case "linux":
		return "/usr/bin/xdg-open", true
	default:
		return "", false
	}
}

// ResolveOpener uses explicit structured configuration when supplied.
// Otherwise it validates the exact platform path; default openers are never
// selected through PATH.
func ResolveOpener(explicit *Command, goos, pathEnvironment string) (ResolvedCommand, error) {
	if explicit != nil {
		resolved, err := ResolveCommand(*explicit, pathEnvironment)
		if err != nil {
			return ResolvedCommand{}, fmt.Errorf("resolve explicit opener: %w", err)
		}
		return resolved, nil
	}
	path, ok := DefaultOpenerPath(goos)
	if !ok {
		return ResolvedCommand{}, fmt.Errorf("resolve default opener: unsupported platform %q requires explicit configuration", goos)
	}
	resolved, err := ResolveCommand(Command{Executable: path}, "")
	if err != nil {
		return ResolvedCommand{}, fmt.Errorf("resolve default opener %q: %w", path, err)
	}
	return resolved, nil
}

func discoverExecutable(name, pathEnvironment string) (string, error) {
	if name == "" || strings.ContainsRune(name, filepath.Separator) {
		return "", errors.New("discover external command: executable must be a bare name")
	}
	for _, directory := range filepath.SplitList(pathEnvironment) {
		if directory == "" {
			directory = "."
		}
		candidate := filepath.Join(directory, name)
		absolute, err := filepath.Abs(candidate)
		if err != nil {
			continue
		}
		if _, _, err := canonicalExecutable(absolute); err == nil {
			return absolute, nil
		}
	}
	return "", fmt.Errorf("discover external command: executable %q not found in PATH", name)
}

func canonicalExecutable(path string) (string, os.FileInfo, error) {
	absolute, err := filepath.Abs(path)
	if err != nil {
		return "", nil, fmt.Errorf("resolve external command: absolute path: %w", err)
	}
	if err := validateCommand(Command{Executable: absolute}); err != nil {
		return "", nil, fmt.Errorf("resolve external command path: %w", err)
	}
	canonical, err := filepath.EvalSymlinks(absolute)
	if err != nil {
		return "", nil, fmt.Errorf("resolve external command: canonicalize %q: %w", absolute, err)
	}
	canonical = filepath.Clean(canonical)
	if err := validateCommand(Command{Executable: canonical}); err != nil {
		return "", nil, fmt.Errorf("resolve canonical external command path: %w", err)
	}
	info, err := validateCanonicalExecutable(canonical)
	if err != nil {
		return "", nil, fmt.Errorf("resolve external command %q: %w", canonical, err)
	}
	return canonical, info, nil
}

func validateCanonicalExecutable(path string) (os.FileInfo, error) {
	info, err := os.Lstat(path)
	if err != nil {
		return nil, err
	}
	if info.Mode()&os.ModeSymlink != 0 {
		return nil, errors.New("canonical executable is a symlink")
	}
	if !info.Mode().IsRegular() {
		return nil, errors.New("executable is not a regular file")
	}
	if info.Mode().Perm()&0o111 == 0 {
		return nil, errors.New("file has no executable permission bits")
	}
	return info, nil
}
