package integration

import (
	"os/exec"
	"testing"
)

func TestHostedStage1RecoveryNormalizesSplitTerminalWrites(t *testing.T) {
	command := exec.Command("python3", "hosted-stage1-recovery.py", "--self-test")
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("recovery harness self-test failed: %v\n%s", err, output)
	}
}
