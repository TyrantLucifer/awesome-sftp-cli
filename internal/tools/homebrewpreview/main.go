package main

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"github.com/TyrantLucifer/awesome-mac-sftp/internal/releasepack"
)

const maxPreviewArchiveBytes = 256 << 20

func main() {
	if err := run(os.Args[1:], os.Stdout); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func run(args []string, stdout io.Writer) error {
	if len(args) != 4 {
		return errors.New("usage: homebrewpreview VERSION LICENSE ASSET_DIRECTORY LOOPBACK_BASE_URL")
	}
	archives := make([]releasepack.Archive, 0, len(releasepack.Targets))
	for _, target := range releasepack.Targets {
		name := fmt.Sprintf("amsftp_%s_%s_%s.tar.gz", args[0], target.OS, target.Arch)
		body, err := readPreviewArchive(filepath.Join(args[2], name))
		if err != nil {
			return fmt.Errorf("read Homebrew preview archive %s: %w", name, err)
		}
		archives = append(archives, releasepack.Archive{Name: name, Target: target, Bytes: body})
	}
	formula, err := releasepack.BuildHomebrewPreviewFormula(releasepack.HomebrewPreviewFormulaRequest{
		HomebrewFormulaRequest: releasepack.HomebrewFormulaRequest{Version: args[0], License: args[1], Archives: archives},
		AssetBaseURL:           args[3],
	})
	if err != nil {
		return err
	}
	_, err = stdout.Write(formula)
	return err
}

func readPreviewArchive(path string) ([]byte, error) {
	before, err := os.Lstat(path) //nolint:gosec // path is the caller-selected directory plus an internally generated canonical archive basename.
	if err != nil {
		return nil, err
	}
	if before.Mode()&os.ModeSymlink != 0 || !before.Mode().IsRegular() || before.Size() <= 0 || before.Size() > maxPreviewArchiveBytes {
		return nil, errors.New("archive must be a bounded non-symlink regular file")
	}
	file, err := os.Open(path) //nolint:gosec // path is an exact canonical archive under the caller-selected asset directory and is revalidated below.
	if err != nil {
		return nil, err
	}
	defer file.Close()
	after, err := file.Stat()
	if err != nil || !after.Mode().IsRegular() || !os.SameFile(before, after) || after.Size() != before.Size() {
		return nil, errors.New("archive identity changed while opening")
	}
	body, err := io.ReadAll(io.LimitReader(file, maxPreviewArchiveBytes+1))
	if err != nil {
		return nil, err
	}
	if int64(len(body)) != before.Size() {
		return nil, errors.New("archive size changed while reading")
	}
	return body, nil
}
