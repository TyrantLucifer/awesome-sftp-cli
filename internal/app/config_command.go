package app

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"

	"github.com/TyrantLucifer/awesome-mac-sftp/internal/config"
)

func runConfig(_ context.Context, args []string, stdout io.Writer, _ io.Writer) error {
	if len(args) != 1 || args[0] != "validate" && args[0] != "print-effective" {
		return runConfigCommand(args, "", stdout)
	}
	paths, _, err := runtimePaths()
	if err != nil {
		return NewExitError(ExitConfig, fmt.Errorf("resolve config path: %w", err))
	}
	return runConfigCommand(args, paths.ConfigFile, stdout)
}

func runConfigCommand(args []string, defaultPath string, stdout io.Writer) error {
	if len(args) < 1 || len(args) > 2 {
		return NewExitError(ExitUsage, errors.New("config requires validate or print-effective and an optional path"))
	}
	command := args[0]
	if command != "validate" && command != "print-effective" {
		return NewExitError(ExitUsage, fmt.Errorf("unknown config command %q", command))
	}

	path := defaultPath
	explicit := len(args) == 2
	if explicit {
		path = args[1]
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
	if command == "validate" {
		_, err = fmt.Fprintf(stdout, "config valid (schema %d)\n", loaded.SchemaVersion)
	} else {
		err = config.WriteRedactedEffective(stdout, loaded)
	}
	if err != nil {
		return NewExitError(ExitInternal, err)
	}
	return nil
}
