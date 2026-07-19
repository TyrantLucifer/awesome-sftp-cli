package supportbundle

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"syscall"

	"github.com/TyrantLucifer/awesome-sftp-cli/internal/platform"
)

const publishChunkBytes = 32 * 1024

// Publish durably creates one owner-private bundle without replacing any path.
func Publish(ctx context.Context, path string, content []byte) error {
	return publishWithValidation(ctx, path, content,
		func(path string) error { return platform.ValidatePrivateDirectory(path, platform.ValidatePersistent) },
		func(path string) error { return platform.ValidatePrivateFile(path, platform.ValidatePersistent) },
		syncPublishDirectory,
	)
}

func publishWithValidation(
	ctx context.Context,
	path string,
	content []byte,
	validateDirectory func(string) error,
	validateFile func(string) error,
	syncDirectory func(string) error,
) (returnErr error) {
	if ctx == nil {
		return errors.New("publish support bundle: nil context")
	}
	if err := ctx.Err(); err != nil {
		return fmt.Errorf("publish support bundle: %w", err)
	}
	if len(content) == 0 || len(content) > MaxBundleBytes {
		return errors.New("publish support bundle: content size is outside bounds")
	}
	if !filepath.IsAbs(path) || filepath.Clean(path) != path {
		return errors.New("publish support bundle: output path must be canonical and absolute")
	}
	parent := filepath.Dir(path)
	if validateDirectory == nil || validateFile == nil || syncDirectory == nil {
		return errors.New("publish support bundle: incomplete validation runtime")
	}
	if err := validateDirectory(parent); err != nil {
		return fmt.Errorf("publish support bundle: validate output directory: %w", err)
	}

	file, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL|syscall.O_NOFOLLOW, 0o600) //nolint:gosec // canonical output in a validated owner-private directory
	if err != nil {
		return fmt.Errorf("publish support bundle: create output: %w", err)
	}
	remove := true
	closed := false
	defer func() {
		var closeErr error
		if !closed {
			closeErr = file.Close()
		}
		if remove {
			removeErr := os.Remove(path)
			if errors.Is(removeErr, os.ErrNotExist) {
				removeErr = nil
			}
			returnErr = errors.Join(returnErr, closeErr, removeErr)
			return
		}
		returnErr = errors.Join(returnErr, closeErr)
	}()

	for offset := 0; offset < len(content); {
		if err := ctx.Err(); err != nil {
			return fmt.Errorf("publish support bundle: %w", err)
		}
		end := min(offset+publishChunkBytes, len(content))
		written, err := file.Write(content[offset:end])
		if err != nil {
			return fmt.Errorf("publish support bundle: write output: %w", err)
		}
		if written != end-offset {
			return errors.New("publish support bundle: short write")
		}
		offset = end
	}
	if err := ctx.Err(); err != nil {
		return fmt.Errorf("publish support bundle: %w", err)
	}
	if err := file.Sync(); err != nil {
		return fmt.Errorf("publish support bundle: sync output: %w", err)
	}
	if err := file.Close(); err != nil {
		return fmt.Errorf("publish support bundle: close output: %w", err)
	}
	closed = true
	if err := validateFile(path); err != nil {
		return fmt.Errorf("publish support bundle: validate output: %w", err)
	}
	if err := syncDirectory(parent); err != nil {
		return err
	}
	remove = false
	return nil
}

func syncPublishDirectory(path string) error {
	directory, err := os.Open(path) //nolint:gosec // caller supplies the validated exact parent
	if err != nil {
		return fmt.Errorf("publish support bundle: open output directory: %w", err)
	}
	if err := directory.Sync(); err != nil {
		_ = directory.Close()
		return fmt.Errorf("publish support bundle: sync output directory: %w", err)
	}
	if err := directory.Close(); err != nil {
		return fmt.Errorf("publish support bundle: close output directory: %w", err)
	}
	return nil
}
