package app

import (
	"context"
	"errors"
	"fmt"
	"io"
	"path/filepath"

	"github.com/TyrantLucifer/awesome-sftp-cli/internal/platform"
	"github.com/TyrantLucifer/awesome-sftp-cli/internal/workspace"
)

func runCompletion(ctx context.Context, args []string, stdout io.Writer, _ io.Writer) error {
	return runCompletionWithWorkspaceRoot(ctx, args, stdout, completionWorkspaceRoot)
}

func runCompletionWithWorkspaceRoot(
	_ context.Context,
	args []string,
	stdout io.Writer,
	resolveWorkspaceRoot func() (string, error),
) error {
	if len(args) == 1 && args[0] == "__workspaces" {
		root, err := resolveWorkspaceRoot()
		if err != nil {
			return NewExitError(ExitConfig, fmt.Errorf("resolve workspace completion root: %w", err))
		}
		names, err := workspace.CompletionNames(root)
		if err != nil {
			return NewExitError(ExitConfig, err)
		}
		for _, name := range names {
			if _, err := fmt.Fprintln(stdout, name); err != nil {
				return NewExitError(ExitInternal, fmt.Errorf("write workspace completion: %w", err))
			}
		}
		return nil
	}
	if len(args) != 1 {
		return NewExitError(ExitUsage, errors.New("completion requires exactly one shell: bash, zsh, or fish"))
	}
	script, err := RenderCompletion(args[0])
	if err != nil {
		return NewExitError(ExitUsage, err)
	}
	if _, err := io.WriteString(stdout, script); err != nil {
		return NewExitError(ExitInternal, fmt.Errorf("write completion: %w", err))
	}
	return nil
}

func completionWorkspaceRoot() (string, error) {
	paths, _, err := platform.ResolvePaths(platform.Overrides{})
	if err != nil {
		return "", err
	}
	return filepath.Join(paths.StateDir, "workspaces"), nil
}
