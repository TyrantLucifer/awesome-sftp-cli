package app

import (
	"context"
	"errors"
	"fmt"
	"io"
)

func runCompletion(_ context.Context, args []string, stdout io.Writer, _ io.Writer) error {
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
