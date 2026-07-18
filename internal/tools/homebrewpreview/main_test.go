package main

import (
	"crypto/sha256"
	"encoding/hex"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestRunRendersFormulaFromExactBoundedArchiveSet(t *testing.T) {
	root := t.TempDir()
	for _, suffix := range []string{"darwin_arm64", "darwin_amd64", "linux_arm64", "linux_amd64"} {
		name := "amsftp_1.0.0_" + suffix + ".tar.gz"
		if err := os.WriteFile(filepath.Join(root, name), []byte("archive:"+suffix), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	var output strings.Builder
	if err := run([]string{"1.0.0", "BSD-3-Clause", root, "http://127.0.0.1:41731"}, &output); err != nil {
		t.Fatal(err)
	}
	formula := output.String()
	for _, suffix := range []string{"darwin_arm64", "darwin_amd64", "linux_arm64", "linux_amd64"} {
		name := "amsftp_1.0.0_" + suffix + ".tar.gz"
		body := []byte("archive:" + suffix)
		digest := sha256.Sum256(body)
		if strings.Count(formula, "http://127.0.0.1:41731/"+name) != 1 || strings.Count(formula, hex.EncodeToString(digest[:])) != 1 {
			t.Fatalf("formula does not bind %s exactly once:\n%s", name, formula)
		}
	}
}

func TestRunRejectsUnsafeArchiveInputsAndArguments(t *testing.T) {
	if err := run(nil, &strings.Builder{}); err == nil || !strings.Contains(err.Error(), "usage:") {
		t.Fatalf("missing argument error = %v", err)
	}
	root := t.TempDir()
	for _, suffix := range []string{"darwin_arm64", "darwin_amd64", "linux_arm64", "linux_amd64"} {
		name := "amsftp_1.0.0_" + suffix + ".tar.gz"
		if err := os.WriteFile(filepath.Join(root, name), []byte("archive:"+suffix), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	target := filepath.Join(root, "amsftp_1.0.0_linux_amd64.tar.gz")
	if err := os.Remove(target); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("amsftp_1.0.0_linux_arm64.tar.gz", target); err != nil {
		t.Fatal(err)
	}
	if err := run([]string{"1.0.0", "BSD-3-Clause", root, "http://127.0.0.1:41731"}, &strings.Builder{}); err == nil || !strings.Contains(err.Error(), "non-symlink regular file") {
		t.Fatalf("symlink archive error = %v", err)
	}
}

func TestPreviewAssetHandlerServesOnlyExactTwoVersionArchiveSet(t *testing.T) {
	root := t.TempDir()
	for _, version := range []string{"0.9.0", "1.0.0"} {
		for _, suffix := range []string{"darwin_arm64", "darwin_amd64", "linux_arm64", "linux_amd64"} {
			name := "amsftp_" + version + "_" + suffix + ".tar.gz"
			if err := os.WriteFile(filepath.Join(root, name), []byte(version+":"+suffix), 0o600); err != nil {
				t.Fatal(err)
			}
		}
	}
	handler, err := newPreviewAssetHandler(root)
	if err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequest(http.MethodGet, "http://127.0.0.1/amsftp_1.0.0_darwin_arm64.tar.gz", nil)
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusOK || response.Body.String() != "1.0.0:darwin_arm64" {
		t.Fatalf("allowed archive response = (%d, %q)", response.Code, response.Body.String())
	}
	for _, path := range []string{"/", "/checksums.txt", "/../manifest.json", "/amsftp_1.0.1_darwin_arm64.tar.gz"} {
		request := httptest.NewRequest(http.MethodGet, "http://127.0.0.1"+path, nil)
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, request)
		if response.Code != http.StatusNotFound {
			body, _ := io.ReadAll(response.Body)
			t.Fatalf("forbidden path %q response = (%d, %q)", path, response.Code, body)
		}
	}
}

func TestPreviewAssetHandlerRejectsIncompleteExtraAndSymlinkSets(t *testing.T) {
	for name, mutate := range map[string]func(*testing.T, string){
		"incomplete": func(t *testing.T, root string) {
			t.Helper()
			if err := os.Remove(filepath.Join(root, "amsftp_0.9.0_linux_amd64.tar.gz")); err != nil {
				t.Fatal(err)
			}
		},
		"extra": func(t *testing.T, root string) {
			t.Helper()
			if err := os.WriteFile(filepath.Join(root, "checksums.txt"), []byte("extra"), 0o600); err != nil {
				t.Fatal(err)
			}
		},
		"symlink": func(t *testing.T, root string) {
			t.Helper()
			path := filepath.Join(root, "amsftp_0.9.0_linux_amd64.tar.gz")
			if err := os.Remove(path); err != nil {
				t.Fatal(err)
			}
			if err := os.Symlink("amsftp_1.0.0_linux_amd64.tar.gz", path); err != nil {
				t.Fatal(err)
			}
		},
	} {
		t.Run(name, func(t *testing.T) {
			root := exactPreviewAssetFixture(t)
			mutate(t, root)
			if _, err := newPreviewAssetHandler(root); err == nil {
				t.Fatal("unsafe preview asset set was accepted")
			}
		})
	}
}

func exactPreviewAssetFixture(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	for _, version := range []string{"0.9.0", "1.0.0"} {
		for _, suffix := range []string{"darwin_arm64", "darwin_amd64", "linux_arm64", "linux_amd64"} {
			name := "amsftp_" + version + "_" + suffix + ".tar.gz"
			if err := os.WriteFile(filepath.Join(root, name), []byte(version+":"+suffix), 0o600); err != nil {
				t.Fatal(err)
			}
		}
	}
	return root
}
