//go:build darwin || linux

package integration

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestStage2PTYActionsWaitForReadySelection(t *testing.T) {
	script, err := os.ReadFile("hosted-stage2-mvp.py")
	if err != nil {
		t.Fatal(err)
	}
	text := string(script)
	for _, check := range []struct {
		function string
		filename string
		action   string
	}{
		{function: "run_copy", filename: "filename", action: "y"},
		{function: "run_move", filename: "filename", action: "d"},
		{function: "run_rename", filename: "old_name", action: "r"},
		{function: "run_delete", filename: "filename", action: "D"},
	} {
		start := strings.Index(text, "def "+check.function+"(")
		if start < 0 {
			t.Fatalf("%s function is missing", check.function)
		}
		end := strings.Index(text[start:], "\ndef ")
		if end < 0 {
			t.Fatalf("%s function is malformed", check.function)
		}
		body := text[start : start+end]
		ready := strings.Index(body, "read_ready_selection(fd, observer, output, "+check.filename+")")
		action := strings.Index(body, "os.write(fd, b\""+check.action+"\")")
		if ready < 0 || action < 0 || ready > action {
			t.Fatalf("%s must observe a ready selected target before sending %s", check.function, check.action)
		}
	}
	if !strings.Contains(text, "read_until(fd, observer, output, b\"READ-ONLY | sort:name\")") {
		t.Fatal("ready-selection barrier must observe the loaded TUI state")
	}
}

func TestStage2LocalPTYCopyAndDurableJobsReattachMVP(t *testing.T) {
	binary, observer := buildStage2MVPFixtures(t)
	// #nosec G204 -- the script and freshly built fixture paths are test-owned.
	command := exec.Command("python3", "hosted-stage2-mvp.py", binary)
	command.Env = append(os.Environ(), "AMSFTP_STAGE2_VT_OBSERVER="+observer)
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("Stage 2 local PTY MVP failed: %v\n%s", err, output)
	}
}

func buildStage2MVPFixtures(t *testing.T) (string, string) {
	t.Helper()
	directory := t.TempDir()
	binary := filepath.Join(directory, "amsftp")
	// #nosec G204 -- the output is confined to the test-owned temporary directory.
	build := exec.Command("go", "build", "-trimpath", "-o", binary, "../../cmd/amsftp")
	if output, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build amsftp: %v\n%s", err, output)
	}
	observer := filepath.Join(directory, "vt-observer")
	// #nosec G204 -- the output is confined to the test-owned temporary directory.
	build = exec.Command("go", "build", "-trimpath", "-o", observer, "./vt-observer")
	if output, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build VT observer: %v\n%s", err, output)
	}
	return binary, observer
}
