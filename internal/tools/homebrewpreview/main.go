package main

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"time"

	"github.com/TyrantLucifer/awesome-mac-sftp/internal/releasepack"
)

const maxPreviewArchiveBytes = 256 << 20

func main() {
	if len(os.Args) == 4 && os.Args[1] == "--serve" {
		if err := servePreviewAssets(os.Args[2], os.Args[3]); err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		return
	}
	if err := run(os.Args[1:], os.Stdout); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func newPreviewAssetHandler(root string) (http.Handler, error) {
	absolute, err := filepath.Abs(root)
	if err != nil {
		return nil, fmt.Errorf("resolve Homebrew preview asset root: %w", err)
	}
	resolved, err := filepath.EvalSymlinks(absolute)
	if err != nil || resolved != absolute {
		return nil, errors.New("homebrew preview asset root must not traverse symlinks")
	}
	info, err := os.Lstat(resolved)
	if err != nil || info.Mode()&os.ModeSymlink != 0 || !info.IsDir() {
		return nil, errors.New("homebrew preview asset root must be a non-symlink directory")
	}
	allowed := make(map[string]struct{}, 2*len(releasepack.Targets))
	for _, version := range []string{"0.9.0", "1.0.0"} {
		for _, target := range releasepack.Targets {
			allowed[fmt.Sprintf("amsftp_%s_%s_%s.tar.gz", version, target.OS, target.Arch)] = struct{}{}
		}
	}
	entries, err := os.ReadDir(resolved)
	if err != nil || len(entries) != len(allowed) {
		return nil, errors.New("homebrew preview asset root must contain the exact two-version archive set")
	}
	for _, entry := range entries {
		if _, exists := allowed[entry.Name()]; !exists || entry.Type()&os.ModeSymlink != 0 || !entry.Type().IsRegular() {
			return nil, errors.New("homebrew preview asset root contains an unsafe entry")
		}
	}
	return http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		if request.Method != http.MethodGet && request.Method != http.MethodHead {
			http.NotFound(response, request)
			return
		}
		name := request.URL.Path
		if len(name) <= 1 || name[0] != '/' {
			http.NotFound(response, request)
			return
		}
		name = name[1:]
		if _, exists := allowed[name]; !exists {
			http.NotFound(response, request)
			return
		}
		body, err := readPreviewArchive(filepath.Join(resolved, name))
		if err != nil {
			http.Error(response, "preview archive unavailable", http.StatusInternalServerError)
			return
		}
		http.ServeContent(response, request, name, time.Unix(0, 0), bytes.NewReader(body))
	}), nil
}

func servePreviewAssets(root, portPath string) error {
	handler, err := newPreviewAssetHandler(root)
	if err != nil {
		return err
	}
	listener, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		return fmt.Errorf("listen for Homebrew preview assets: %w", err)
	}
	defer listener.Close()
	portFile, err := os.OpenFile(portPath, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600) //nolint:gosec // O_EXCL rejects a pre-existing file or symlink at the explicit runner-temp path.
	if err != nil {
		return fmt.Errorf("create Homebrew preview port file: %w", err)
	}
	port := listener.Addr().(*net.TCPAddr).Port
	if _, err := fmt.Fprintf(portFile, "%d", port); err != nil {
		_ = portFile.Close()
		return fmt.Errorf("write Homebrew preview port file: %w", err)
	}
	if err := portFile.Close(); err != nil {
		return fmt.Errorf("close Homebrew preview port file: %w", err)
	}
	server := &http.Server{
		Handler:           handler,
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       5 * time.Minute,
		WriteTimeout:      5 * time.Minute,
		IdleTimeout:       30 * time.Second,
	}
	if err := server.Serve(listener); !errors.Is(err, http.ErrServerClosed) {
		return fmt.Errorf("serve Homebrew preview assets: %w", err)
	}
	return nil
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
