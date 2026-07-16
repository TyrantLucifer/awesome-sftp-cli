package externalprocess

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
)

// Plan is a direct-exec invocation. Args excludes argv[0], includes the
// canonical materialization as its final item, and is never shell text.
type Plan struct {
	Executable string
	Args       []string
	Env        []string

	resolvedIdentity os.FileInfo
	fileIdentity     os.FileInfo
	materialization  string
}

func NewPlan(command ResolvedCommand, materialization string, baseEnvironment []string) (Plan, error) {
	if err := command.Revalidate(); err != nil {
		return Plan{}, fmt.Errorf("plan external command: %w", err)
	}
	canonicalFile, fileInfo, err := canonicalRegularFile(materialization)
	if err != nil {
		return Plan{}, fmt.Errorf("plan external command materialization: %w", err)
	}
	args := append(append([]string(nil), command.Args...), canonicalFile)
	if len(args) > MaxArguments {
		return Plan{}, fmt.Errorf("plan external command: runtime argument count exceeds %d", MaxArguments)
	}
	if err := validateCommand(Command{Executable: command.Executable, Args: args}); err != nil {
		return Plan{}, fmt.Errorf("plan external command: %w", err)
	}
	return Plan{
		Executable:       command.Executable,
		Args:             args,
		Env:              ScrubEnvironment(baseEnvironment),
		resolvedIdentity: command.identity,
		fileIdentity:     fileInfo,
		materialization:  canonicalFile,
	}, nil
}

// CommandContext revalidates both frozen file identities and constructs an
// os/exec direct invocation. It performs no shell evaluation.
func (plan Plan) CommandContext(ctx context.Context) (*exec.Cmd, error) {
	if ctx == nil {
		return nil, errors.New("build external command: context is nil")
	}
	configuredArgs := 0
	if len(plan.Args) > 0 {
		configuredArgs = len(plan.Args) - 1
	}
	resolved := ResolvedCommand{
		Executable: plan.Executable,
		Args:       plan.Args[:configuredArgs],
		identity:   plan.resolvedIdentity,
	}
	if err := resolved.Revalidate(); err != nil {
		return nil, fmt.Errorf("build external command: %w", err)
	}
	if len(plan.Args) == 0 || plan.Args[len(plan.Args)-1] != plan.materialization {
		return nil, errors.New("build external command: materialization is not the final argument")
	}
	info, err := validateCanonicalRegularFile(plan.materialization)
	if err != nil {
		return nil, fmt.Errorf("build external command: materialization: %w", err)
	}
	if !os.SameFile(plan.fileIdentity, info) {
		return nil, errors.New("build external command: materialization file identity changed")
	}
	if err := validateCommand(Command{Executable: plan.Executable, Args: plan.Args}); err != nil {
		return nil, fmt.Errorf("build external command: %w", err)
	}

	cmd := exec.CommandContext(ctx, plan.Executable, plan.Args...)
	cmd.Env = ScrubEnvironment(plan.Env)
	return cmd, nil
}

func canonicalRegularFile(path string) (string, os.FileInfo, error) {
	if path == "" {
		return "", nil, errors.New("path is empty")
	}
	absolute, err := filepath.Abs(path)
	if err != nil {
		return "", nil, err
	}
	canonical, err := filepath.EvalSymlinks(absolute)
	if err != nil {
		return "", nil, err
	}
	canonical = filepath.Clean(canonical)
	info, err := validateCanonicalRegularFile(canonical)
	if err != nil {
		return "", nil, err
	}
	return canonical, info, nil
}

func validateCanonicalRegularFile(path string) (os.FileInfo, error) {
	info, err := os.Lstat(path)
	if err != nil {
		return nil, err
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() {
		return nil, errors.New("path is not a canonical regular file")
	}
	return info, nil
}
