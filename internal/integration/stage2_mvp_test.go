//go:build darwin || linux

package integration

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

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
