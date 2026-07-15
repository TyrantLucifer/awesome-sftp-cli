package integration

import (
	"bytes"
	"os/exec"
	"strconv"
	"testing"
)

func TestVTObserverAccumulatesPatternsAcrossSynchronizedFrames(t *testing.T) {
	prelude := []byte("\x1b[?2026h\x1b[30;13Hloading | caps:1@1 | sort:name\x1b[?2026l")
	stream := append(append([]byte{}, prelude...), []byte(
		"\x1b[?2026h"+
			"\x1b[30;13H\x1b[7mrecon"+
			"\x1b[30;19Hected at nearest accessible"+
			"\x1b[30;47Hparent"+
			"\x1b[?2026l"+
			"\x1b[?2026h"+
			"\x1b[30;13Hold endpoint cleanup failed"+
			"\x1b[3;3Ha-recovered-marker.txt"+
			"\x1b[?2026l",
	)...)
	// #nosec G204 -- the command, checkpoint, and patterns are fixed test inputs.
	command := exec.Command(
		"go", "run", "./vt-observer",
		"-checkpoint", strconv.Itoa(len(prelude)),
		"reconnected at nearest accessible parent",
		"a-recovered-marker.txt",
	)
	command.Stdin = bytes.NewReader(stream)
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("VT observer did not reconstruct synchronized frames: %v\n%s", err, output)
	}
}

func TestHostedStage1RecoveryNormalizesSplitTerminalWrites(t *testing.T) {
	observer := t.TempDir() + "/vt-observer"
	// #nosec G204 -- the output is confined to the test-owned temporary directory.
	build := exec.Command("go", "build", "-o", observer, "./vt-observer")
	if output, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build VT observer: %v\n%s", err, output)
	}
	// #nosec G204 -- the script is fixed and observer is a test-owned binary built above.
	command := exec.Command("python3", "hosted-stage1-recovery.py", "--self-test", observer)
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("recovery harness self-test failed: %v\n%s", err, output)
	}
}
