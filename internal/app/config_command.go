package app

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"

	"github.com/TyrantLucifer/awesome-sftp-cli/internal/config"
	"github.com/TyrantLucifer/awesome-sftp-cli/internal/keymap"
)

func runConfig(_ context.Context, args []string, stdout io.Writer, _ io.Writer) error {
	if !configCommandUsesDefaultPath(args) {
		return runConfigCommand(args, "", stdout)
	}
	paths, _, err := runtimePaths()
	if err != nil {
		return NewExitError(ExitConfig, fmt.Errorf("resolve config path: %w", err))
	}
	return runConfigCommand(args, paths.ConfigFile, stdout)
}

func configCommandUsesDefaultPath(args []string) bool {
	if len(args) == 1 {
		return args[0] == "validate" || args[0] == "print-effective" || args[0] == "print-effective-keymap"
	}
	return len(args) == 2 && args[0] == "reset-keymap" && args[1] == "--yes"
}

func runConfigCommand(args []string, defaultPath string, stdout io.Writer) error {
	if len(args) < 1 {
		return NewExitError(ExitUsage, errors.New("config requires a subcommand"))
	}
	command := args[0]
	if command != "validate" && command != "print-effective" && command != "print-effective-keymap" && command != "reset-keymap" {
		return NewExitError(ExitUsage, fmt.Errorf("unknown config command %q", command))
	}

	path := defaultPath
	explicit := false
	if command == "reset-keymap" {
		if len(args) < 2 || len(args) > 3 || args[1] != "--yes" {
			return NewExitError(ExitUsage, errors.New("config reset-keymap requires --yes and an optional path"))
		}
		explicit = len(args) == 3
		if explicit {
			path = args[2]
		}
	} else {
		if len(args) > 2 {
			return NewExitError(ExitUsage, errors.New("config subcommand accepts at most one optional path"))
		}
		explicit = len(args) == 2
		if explicit {
			path = args[1]
		}
	}
	if explicit {
		if path == "" {
			return NewExitError(ExitUsage, errors.New("config path is empty"))
		}
		if _, err := os.Lstat(path); err != nil {
			return NewExitError(ExitConfig, fmt.Errorf("inspect config %q: %w", path, err))
		}
	}

	loaded, err := loadApplicationConfig(path)
	if err != nil {
		return NewExitError(ExitConfig, fmt.Errorf("load config %q: %w", path, err))
	}
	switch command {
	case "validate":
		_, err = fmt.Fprintf(stdout, "config valid (schema %d)\n", loaded.SchemaVersion)
	case "print-effective":
		err = config.WriteRedactedEffective(stdout, loaded)
	case "print-effective-keymap":
		err = keymap.WriteEffective(stdout, loaded.Keymap.Bindings)
	case "reset-keymap":
		loaded.Keymap.Bindings = nil
		if err := replaceApplicationConfig(path, loaded); err != nil {
			return NewExitError(ExitConfig, err)
		}
		_, err = fmt.Fprintln(stdout, "keymap reset to defaults")
	}
	if err != nil {
		return NewExitError(ExitInternal, err)
	}
	return nil
}
