//go:build darwin || linux

package integration

import (
	"os/exec"
	"path/filepath"
	"testing"
)

func TestStage2LocalPTYCopyAndDurableJobsReattachMVP(t *testing.T) {
	binary := filepath.Join(t.TempDir(), "amsftp")
	// #nosec G204 -- the output is confined to the test-owned temporary directory.
	build := exec.Command("go", "build", "-trimpath", "-o", binary, "../../cmd/amsftp")
	if output, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build amsftp: %v\n%s", err, output)
	}
	// #nosec G204 -- the script is fixed and binary is a test-owned executable built above.
	command := exec.Command("python3", "hosted-stage2-mvp.py", binary)
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("Stage 2 local PTY MVP failed: %v\n%s", err, output)
	}
}
