package installscript

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"crypto/sha256"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

const releaseOrigin = "https://github.com/TyrantLucifer/awesome-sftp-cli"

func TestInstallerInstallsAndAtomicallyUpgradesVerifiedRelease(t *testing.T) {
	assets := t.TempDir()
	oldVersion := "9.8.7"
	newVersion := "9.8.8"
	writeRelease(t, assets, oldVersion, false)
	writeRelease(t, assets, newVersion, false)

	server := httptest.NewServer(http.FileServer(http.Dir(assets)))
	t.Cleanup(server.Close)
	script := patchedInstaller(t, server.URL)
	prefix := filepath.Join(t.TempDir(), "prefix")
	state := filepath.Join(t.TempDir(), "daemon-running")
	logPath := filepath.Join(t.TempDir(), "daemon.log")

	runInstaller(t, script, prefix, oldVersion, state, logPath, false)
	assertInstalledVersion(t, prefix, oldVersion)
	if _, err := os.Stat(state); err != nil {
		t.Fatalf("daemon state after install: %v", err)
	}

	runInstaller(t, script, prefix, newVersion, state, logPath, false)
	assertInstalledVersion(t, prefix, newVersion)
	logBytes, err := os.ReadFile(logPath) //nolint:gosec // logPath is a test-owned path below t.TempDir.
	if err != nil {
		t.Fatal(err)
	}
	if got := string(logBytes); got != "start "+oldVersion+"\nstop "+oldVersion+"\nstart "+newVersion+"\n" {
		t.Fatalf("daemon lifecycle = %q", got)
	}
	for _, relative := range []string{
		"share/man/man1/amsftp.1",
		"share/bash-completion/completions/amsftp",
		"share/zsh/site-functions/_amsftp",
		"share/fish/vendor_completions.d/amsftp.fish",
	} {
		if info, err := os.Stat(filepath.Join(prefix, relative)); err != nil || !info.Mode().IsRegular() {
			t.Fatalf("installed material %s: info=%v err=%v", relative, info, err)
		}
	}
	if _, err := os.Stat(filepath.Join(prefix, "bin", "amsftp.previous")); err != nil {
		t.Fatalf("previous binary: %v", err)
	}
}

func TestInstallerRejectsChecksumMismatchWithoutReplacingInstalledBinary(t *testing.T) {
	assets := t.TempDir()
	goodVersion := "9.8.7"
	badVersion := "9.8.8"
	writeRelease(t, assets, goodVersion, false)
	writeRelease(t, assets, badVersion, true)

	server := httptest.NewServer(http.FileServer(http.Dir(assets)))
	t.Cleanup(server.Close)
	script := patchedInstaller(t, server.URL)
	prefix := filepath.Join(t.TempDir(), "prefix")
	state := filepath.Join(t.TempDir(), "daemon-running")
	logPath := filepath.Join(t.TempDir(), "daemon.log")

	runInstaller(t, script, prefix, goodVersion, state, logPath, false)
	runInstaller(t, script, prefix, badVersion, state, logPath, true)
	assertInstalledVersion(t, prefix, goodVersion)
}

func TestInstallerResolvesLatestRelease(t *testing.T) {
	assets := t.TempDir()
	version := "9.8.7"
	writeRelease(t, assets, version, false)
	files := http.FileServer(http.Dir(assets))
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		switch request.URL.Path {
		case "/releases/latest":
			http.Redirect(writer, request, "/releases/tag/v"+version, http.StatusFound)
		case "/releases/tag/v" + version:
			writer.WriteHeader(http.StatusOK)
		default:
			files.ServeHTTP(writer, request)
		}
	}))
	t.Cleanup(server.Close)
	script := patchedInstaller(t, server.URL)
	prefix := filepath.Join(t.TempDir(), "prefix")
	state := filepath.Join(t.TempDir(), "daemon-running")
	logPath := filepath.Join(t.TempDir(), "daemon.log")
	command := exec.Command("/bin/sh", script, "--prefix", prefix) //nolint:gosec // script is generated inside this test's private temporary directory.
	command.Env = append(os.Environ(), "AMSFTP_FAKE_STATE="+state, "AMSFTP_FAKE_LOG="+logPath)
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("latest installer failed: %v\n%s", err, output)
	}
	assertInstalledVersion(t, prefix, version)
}

func patchedInstaller(t *testing.T, origin string) string {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join("..", "..", "install.sh"))
	if err != nil {
		t.Fatal(err)
	}
	old := `release_origin="` + releaseOrigin + `"`
	replacement := `release_origin="` + origin + `"`
	if !bytes.Contains(raw, []byte(old)) {
		t.Fatalf("installer does not contain canonical release origin %q", old)
	}
	patched := bytes.Replace(raw, []byte(old), []byte(replacement), 1)
	patched = bytes.ReplaceAll(patched, []byte("--proto '=https'"), []byte("--proto '=http,https'"))
	path := filepath.Join(t.TempDir(), "install.sh")
	if err := os.WriteFile(path, patched, 0o700); err != nil { //nolint:gosec // the test fixture must be executable and lives below t.TempDir.
		t.Fatal(err)
	}
	return path
}

func runInstaller(t *testing.T, script, prefix, version, state, logPath string, wantFailure bool) {
	t.Helper()
	command := exec.Command("/bin/sh", script, "--version", version, "--prefix", prefix) //nolint:gosec // script is generated inside this test's private temporary directory.
	command.Env = append(os.Environ(), "AMSFTP_FAKE_STATE="+state, "AMSFTP_FAKE_LOG="+logPath)
	output, err := command.CombinedOutput()
	if wantFailure && err == nil {
		t.Fatalf("installer unexpectedly succeeded: %s", output)
	}
	if !wantFailure && err != nil {
		t.Fatalf("installer failed: %v\n%s", err, output)
	}
}

func assertInstalledVersion(t *testing.T, prefix, version string) {
	t.Helper()
	binary := filepath.Join(prefix, "bin", "amsftp")
	info, err := os.Stat(binary)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o755 {
		t.Fatalf("binary mode = %o", info.Mode().Perm())
	}
	output, err := exec.Command(binary, "--version").CombinedOutput() //nolint:gosec // binary is installed from a test-owned archive into t.TempDir.
	if err != nil {
		t.Fatalf("installed --version: %v: %s", err, output)
	}
	if !strings.HasPrefix(string(output), version+" ") {
		t.Fatalf("installed version output = %q", output)
	}
}

func writeRelease(t *testing.T, root, version string, corruptChecksum bool) {
	t.Helper()
	osName, archName := target(t)
	name := fmt.Sprintf("amsftp_%s_%s_%s.tar.gz", version, osName, archName)
	directory := filepath.Join(root, "releases", "download", "v"+version)
	if err := os.MkdirAll(directory, 0o700); err != nil {
		t.Fatal(err)
	}
	archive := releaseArchive(t, version, osName, archName)
	if err := os.WriteFile(filepath.Join(directory, name), archive, 0o600); err != nil {
		t.Fatal(err)
	}
	digest := fmt.Sprintf("%x", sha256.Sum256(archive))
	if corruptChecksum {
		digest = strings.Repeat("0", 64)
	}
	if err := os.WriteFile(filepath.Join(directory, "checksums.txt"), []byte(digest+"  "+name+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
}

func target(t *testing.T) (string, string) {
	t.Helper()
	osName := runtime.GOOS
	if osName == "darwin" {
		osName = "darwin"
	} else if osName != "linux" {
		t.Skipf("installer contract is supported on Darwin and Linux, not %s", osName)
	}
	archName := runtime.GOARCH
	if archName != "amd64" && archName != "arm64" {
		t.Skipf("installer contract is supported on amd64 and arm64, not %s", archName)
	}
	return osName, archName
}

func releaseArchive(t *testing.T, version, osName, archName string) []byte {
	t.Helper()
	root := fmt.Sprintf("amsftp_%s_%s_%s", version, osName, archName)
	binary := fmt.Sprintf(`#!/bin/sh
version=%q
case "${1-}" in
  --version) printf '%%s commit=test dirty=false\n' "$version" ;;
  completion) printf '# completion %%s %%s\n' "${2-}" "$version" ;;
  doctor) printf '{"ok":true}\n' ;;
  daemon)
    case "${2-}" in
      status)
        if test -f "$AMSFTP_FAKE_STATE"; then printf '{"running":true}\n'; else printf '{"running":false}\n'; fi
        ;;
      stop)
        rm -f "$AMSFTP_FAKE_STATE"
        printf 'stop %%s\n' "$version" >>"$AMSFTP_FAKE_LOG"
        printf '{"running":false}\n'
        ;;
      start)
        : >"$AMSFTP_FAKE_STATE"
        printf 'start %%s\n' "$version" >>"$AMSFTP_FAKE_LOG"
        printf '{"running":true}\n'
        ;;
    esac
    ;;
esac
`, version)
	files := []struct {
		name string
		mode int64
		body string
	}{
		{name: root + "/amsftp", mode: 0o755, body: binary},
		{name: root + "/share/man/man1/amsftp.1", mode: 0o644, body: ".TH AMSFTP 1\n"},
	}
	var output bytes.Buffer
	gzipWriter := gzip.NewWriter(&output)
	tarWriter := tar.NewWriter(gzipWriter)
	for _, file := range files {
		if err := tarWriter.WriteHeader(&tar.Header{Name: file.name, Mode: file.mode, Size: int64(len(file.body)), Typeflag: tar.TypeReg}); err != nil {
			t.Fatal(err)
		}
		if _, err := tarWriter.Write([]byte(file.body)); err != nil {
			t.Fatal(err)
		}
	}
	if err := tarWriter.Close(); err != nil {
		t.Fatal(err)
	}
	if err := gzipWriter.Close(); err != nil {
		t.Fatal(err)
	}
	return output.Bytes()
}
