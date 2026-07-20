package main

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"github.com/TyrantLucifer/awesome-sftp-cli/internal/releasepack"
)

const maxArchiveBytes = 256 << 20

func main() {
	if err := run(os.Args[1:], os.Stdout); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func run(args []string, stdout io.Writer) error {
	if len(args) != 3 {
		return errors.New("usage: homebrewformula VERSION SPDX_LICENSE RELEASE_DIRECTORY")
	}
	root, err := safeReleaseDirectory(args[2])
	if err != nil {
		return err
	}
	archives := make([]releasepack.Archive, 0, len(releasepack.Targets))
	for _, target := range releasepack.Targets {
		name := fmt.Sprintf("amsftp_%s_%s_%s.tar.gz", args[0], target.OS, target.Arch)
		body, err := readArchive(filepath.Join(root, name))
		if err != nil {
			return fmt.Errorf("read Homebrew archive %s: %w", name, err)
		}
		archives = append(archives, releasepack.Archive{Name: name, Target: target, Bytes: body})
	}
	formula, err := releasepack.BuildHomebrewFormula(releasepack.HomebrewFormulaRequest{
		Version: args[0], License: args[1], Archives: archives,
	})
	if err != nil {
		return err
	}
	_, err = stdout.Write(formula)
	return err
}

func safeReleaseDirectory(path string) (string, error) {
	absolute, err := filepath.Abs(path)
	if err != nil {
		return "", fmt.Errorf("resolve release directory: %w", err)
	}
	before, err := os.Lstat(absolute)
	if err != nil || before.Mode()&os.ModeSymlink != 0 || !before.IsDir() {
		return "", errors.New("release directory must be a non-symlink directory")
	}
	resolved, err := filepath.EvalSymlinks(absolute)
	if err != nil {
		return "", fmt.Errorf("resolve release directory symlinks: %w", err)
	}
	after, err := os.Lstat(resolved)
	if err != nil || after.Mode()&os.ModeSymlink != 0 || !after.IsDir() || !os.SameFile(before, after) {
		return "", errors.New("release directory identity changed while resolving")
	}
	return resolved, nil
}

func readArchive(path string) ([]byte, error) {
	before, err := os.Lstat(path) //nolint:gosec // path is a canonical generated archive basename below a validated non-symlink release directory.
	if err != nil {
		return nil, err
	}
	if before.Mode()&os.ModeSymlink != 0 || !before.Mode().IsRegular() || before.Size() <= 0 || before.Size() > maxArchiveBytes {
		return nil, errors.New("archive must be a bounded non-symlink regular file")
	}
	file, err := os.Open(path) //nolint:gosec // path is an internally generated basename below the validated release directory.
	if err != nil {
		return nil, err
	}
	defer file.Close()
	after, err := file.Stat()
	if err != nil || !after.Mode().IsRegular() || !os.SameFile(before, after) || after.Size() != before.Size() {
		return nil, errors.New("archive identity changed while opening")
	}
	body, err := io.ReadAll(io.LimitReader(file, maxArchiveBytes+1))
	if err != nil {
		return nil, err
	}
	if int64(len(body)) != before.Size() {
		return nil, errors.New("archive size changed while reading")
	}
	return body, nil
}
