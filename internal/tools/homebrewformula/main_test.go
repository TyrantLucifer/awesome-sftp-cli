package main

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/TyrantLucifer/awesome-sftp-cli/internal/releasepack"
)

func TestRunRendersImmutableFourTargetFormula(t *testing.T) {
	root := t.TempDir()
	version := "1.2.3"
	for _, target := range releasepack.Targets {
		name := fmt.Sprintf("amsftp_%s_%s_%s.tar.gz", version, target.OS, target.Arch)
		if err := os.WriteFile(filepath.Join(root, name), []byte(target.OS+"/"+target.Arch), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	var output bytes.Buffer
	if err := run([]string{version, "BSD-3-Clause", root}, &output); err != nil {
		t.Fatal(err)
	}
	formula := output.String()
	for _, target := range releasepack.Targets {
		name := fmt.Sprintf("amsftp_%s_%s_%s.tar.gz", version, target.OS, target.Arch)
		url := "https://github.com/TyrantLucifer/awesome-sftp-cli/releases/download/v" + version + "/" + name
		if !strings.Contains(formula, url) {
			t.Errorf("formula missing %s", url)
		}
	}
	if !strings.Contains(formula, `license "BSD-3-Clause"`) || !strings.Contains(formula, `bin.install "amsftp"`) {
		t.Fatalf("formula is incomplete:\n%s", formula)
	}
}

func TestRunRejectsSymlinkArchive(t *testing.T) {
	root := t.TempDir()
	version := "1.2.3"
	outside := filepath.Join(t.TempDir(), "archive")
	if err := os.WriteFile(outside, []byte("archive"), 0o600); err != nil {
		t.Fatal(err)
	}
	for index, target := range releasepack.Targets {
		name := fmt.Sprintf("amsftp_%s_%s_%s.tar.gz", version, target.OS, target.Arch)
		path := filepath.Join(root, name)
		if index == 0 {
			if err := os.Symlink(outside, path); err != nil {
				t.Fatal(err)
			}
			continue
		}
		if err := os.WriteFile(path, []byte("archive"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	if err := run([]string{version, "BSD-3-Clause", root}, &bytes.Buffer{}); err == nil {
		t.Fatal("accepted a symlink archive")
	}
}
