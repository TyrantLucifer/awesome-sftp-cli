package main

import (
	"crypto/sha256"
	"encoding/hex"
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
