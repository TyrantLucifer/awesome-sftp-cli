package integration

import (
	"bytes"
	"os"
	"os/exec"
	"strconv"
	"strings"
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

func TestHostedStage1RecoveryExercisesNoArgumentPicker(t *testing.T) {
	script, err := os.ReadFile("hosted-stage1-recovery.py")
	if err != nil {
		t.Fatal(err)
	}
	for _, required := range []string{
		`fresh_picker = PtyApp([])`,
		`fresh_picker.wait_for_screen("Open workspace or SSH host", "recovery-a", "recovery-b", "recovery-picker", "Type an SSH alias;`,
		`fresh_picker.send("\x1b[B\x1b[B")`,
		`fresh_picker.wait_for_screen("> host       recovery-picker")`,
		`fresh_picker.send("\r")`,
		`fresh_picker.wait_for_screen(os.path.basename(PICKER_MARKER))`,
		`workspace_picker = PtyApp([])`,
		`workspace_picker.send("recovery-stage1")`,
		`workspace_picker.wait_for_screen("Host: recovery-stage1")`,
		`workspace_picker.send("\x1b[B")`,
		`workspace_picker.wait_for_screen("> workspace  recovery-stage1")`,
		`workspace_picker.wait_for_screen("a-parent-marker.txt", "b-stage1-marker.txt")`,
	} {
		if !strings.Contains(string(script), required) {
			t.Fatalf("Stage 1 recovery harness does not require %q", required)
		}
	}
}

func TestHostedKerberosFailureKeepsTUIResponsive(t *testing.T) {
	script, err := os.ReadFile("hosted-kerberos.sh")
	if err != nil {
		t.Fatal(err)
	}
	for _, required := range []string{
		`"event":"rpc_request_failed"`,
		`"error_code":"auth_required"`,
		`(failed)`,
		`-exact "failed" {}`,
		`set stty_init "rows 30 columns 200"`,
		`log_file -a -noappend $env(AMSFTP_OUTPUT)`,
		`vt-observer`,
	} {
		if !strings.Contains(string(script), required) {
			t.Fatalf("Kerberos harness does not require %q", required)
		}
	}
}
