package commandrun

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/TyrantLucifer/awesome-mac-sftp/internal/transport/openssh"
)

type fakeAttributeProber struct {
	attributes map[string]openssh.SFTPAttributes
	err        error
	paths      []string
}

func (probe *fakeAttributeProber) ProbeAttributes(_ context.Context, rawPath string) (openssh.SFTPAttributes, error) {
	probe.paths = append(probe.paths, rawPath)
	if probe.err != nil {
		return openssh.SFTPAttributes{}, probe.err
	}
	return probe.attributes[rawPath], nil
}

func TestPlanRemoteCommandFreezesExactFreshOpenSSHArgv(t *testing.T) {
	binary := writeRemoteSSH(t)
	probe := trustedUtilityProber()
	plan, err := PlanRemoteCommand(context.Background(), RemoteConfig{
		OpenSSH:    openssh.Config{Binary: binary, HostAlias: "conflicting-config"},
		Attributes: probe,
	}, "/srv/work", "printf hello")
	if err != nil {
		t.Fatal(err)
	}
	bootstrap := `case ${SHELL-} in /*) cd '/srv/work' && /usr/bin/printf '%s\n' amsftp-shell-wire-v1 && IFS= read -r AMSFTP_COMMAND && exec "$SHELL" -c "$AMSFTP_COMMAND";; *) exit 126;; esac`
	want := []string{
		binary,
		"-T",
		"-oEscapeChar=none",
		"-oForwardAgent=no",
		"-oForwardX11=no",
		"-oPermitLocalCommand=no",
		"-oClearAllForwardings=yes",
		"-oRemoteCommand=none",
		"-oStdinNull=no",
		"-oForkAfterAuthentication=no",
		"-oTunnel=no",
		"-oGSSAPIDelegateCredentials=no",
		"-oControlMaster=no",
		"-oControlPath=none",
		"-oControlPersist=no",
		"-oSessionType=default",
		"conflicting-config",
		bootstrap,
	}
	if got := plan.Argv(); !reflect.DeepEqual(got, want) {
		t.Fatalf("argv = %#v, want %#v", got, want)
	}
	got := plan.Argv()
	got[0] = "mutated"
	if reflect.DeepEqual(plan.Argv(), got) {
		t.Fatal("plan argv aliases caller-owned storage")
	}
	if plan.Bootstrap() != bootstrap {
		t.Fatalf("bootstrap = %q, want %q", plan.Bootstrap(), bootstrap)
	}
	if !reflect.DeepEqual(probe.paths, []string{"/usr", "/usr/bin", "/usr/bin/printf"}) {
		t.Fatalf("probe paths = %#v", probe.paths)
	}
}

func TestPOSIXQuoteAndBootstrapPreserveRawPathBytes(t *testing.T) {
	raw := "/work/'$;\n" + string([]byte{0xff})
	wantQuote := "'/work/'\\''$;\n" + string([]byte{0xff}) + "'"
	if got := QuotePOSIXBytes(raw); got != wantQuote {
		t.Fatalf("quote bytes = %x, want %x", []byte(got), []byte(wantQuote))
	}
	plan, err := PlanRemoteCommand(context.Background(), RemoteConfig{
		OpenSSH:    openssh.Config{Binary: writeRemoteSSH(t), HostAlias: "host"},
		Attributes: trustedUtilityProber(),
	}, raw, "true")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(plan.Bootstrap(), "cd "+wantQuote+" &&") {
		t.Fatalf("bootstrap does not contain byte-preserving cwd: %x", []byte(plan.Bootstrap()))
	}
}

func TestPlanRemoteCommandRejectsInvalidCommandCWDAndBootstrapBounds(t *testing.T) {
	config := RemoteConfig{
		OpenSSH:    openssh.Config{Binary: writeRemoteSSH(t), HostAlias: "host"},
		Attributes: trustedUtilityProber(),
	}
	for _, command := range []string{"", "a\nb", "a\rb", "a\x00b", string([]byte{0xff}), strings.Repeat("x", MaxCommandBytes+1)} {
		if _, err := PlanRemoteCommand(context.Background(), config, "/work", command); err == nil {
			t.Fatalf("unsafe command %q succeeded", command)
		}
	}
	for _, cwd := range []string{"", "relative", "/work/../escape", "/work\x00suffix", strings.Repeat("/'", MaxCommandBytes/2)} {
		if _, err := PlanRemoteCommand(context.Background(), config, cwd, "true"); err == nil {
			t.Fatalf("unsafe cwd %q succeeded", cwd)
		}
	}
	if _, err := PlanRemoteCommand(context.Background(), config, "/"+strings.Repeat("x", MaxCommandBytes), "true"); err == nil {
		t.Fatal("oversized cwd succeeded")
	}
}

func TestRemoteUtilityPreflightFailsClosedOnMissingOrUnsafeAttributes(t *testing.T) {
	zero, gid := uint32(0), uint32(0)
	dirMode, fileMode := uint32(0o040755), uint32(0o100755)
	base := map[string]openssh.SFTPAttributes{
		"/usr":            {UID: &zero, GID: &gid, Mode: &dirMode},
		"/usr/bin":        {UID: &zero, GID: &gid, Mode: &dirMode},
		"/usr/bin/printf": {UID: &zero, GID: &gid, Mode: &fileMode},
	}
	tests := map[string]func(map[string]openssh.SFTPAttributes){
		"missing uid": func(attrs map[string]openssh.SFTPAttributes) {
			attrs["/usr/bin/printf"] = openssh.SFTPAttributes{GID: &gid, Mode: &fileMode}
		},
		"nonroot uid": func(attrs map[string]openssh.SFTPAttributes) {
			value, item := uint32(501), attrs["/usr"]
			item.UID = &value
			attrs["/usr"] = item
		},
		"missing gid": func(attrs map[string]openssh.SFTPAttributes) {
			attrs["/usr/bin"] = openssh.SFTPAttributes{UID: &zero, Mode: &dirMode}
		},
		"missing mode": func(attrs map[string]openssh.SFTPAttributes) {
			attrs["/usr"] = openssh.SFTPAttributes{UID: &zero, GID: &gid}
		},
		"directory symlink": func(attrs map[string]openssh.SFTPAttributes) {
			value, item := uint32(0o120777), attrs["/usr/bin"]
			item.Mode = &value
			attrs["/usr/bin"] = item
		},
		"directory not searchable": func(attrs map[string]openssh.SFTPAttributes) {
			value, item := uint32(0o040644), attrs["/usr/bin"]
			item.Mode = &value
			attrs["/usr/bin"] = item
		},
		"file directory": func(attrs map[string]openssh.SFTPAttributes) {
			item := attrs["/usr/bin/printf"]
			item.Mode = &dirMode
			attrs["/usr/bin/printf"] = item
		},
		"group writable": func(attrs map[string]openssh.SFTPAttributes) {
			value, item := uint32(0o040775), attrs["/usr"]
			item.Mode = &value
			attrs["/usr"] = item
		},
		"other writable": func(attrs map[string]openssh.SFTPAttributes) {
			value, item := uint32(0o100757), attrs["/usr/bin/printf"]
			item.Mode = &value
			attrs["/usr/bin/printf"] = item
		},
		"not executable": func(attrs map[string]openssh.SFTPAttributes) {
			value, item := uint32(0o100644), attrs["/usr/bin/printf"]
			item.Mode = &value
			attrs["/usr/bin/printf"] = item
		},
	}
	for name, mutate := range tests {
		t.Run(name, func(t *testing.T) {
			attrs := cloneAttributes(base)
			mutate(attrs)
			if err := PreflightRemoteUtilities(context.Background(), &fakeAttributeProber{attributes: attrs}); err == nil {
				t.Fatal("unsafe utility attributes succeeded")
			}
		})
	}
	if err := PreflightRemoteUtilities(context.Background(), &fakeAttributeProber{attributes: base, err: errors.New("probe failed")}); err == nil {
		t.Fatal("probe error succeeded")
	}
}

func TestRunRemoteCommandSendsUserTextOnlyAfterByteZeroMarker(t *testing.T) {
	capture := filepath.Join(t.TempDir(), "stdin")
	plan := remoteHelperPlan(t, "success", capture, nil)
	result, err := RunRemoteCommand(context.Background(), plan, DefaultStreamBytes)
	if err != nil {
		t.Fatal(err)
	}
	input, err := os.ReadFile(capture)
	if err != nil {
		t.Fatal(err)
	}
	if string(input) != "printf hello\n" {
		t.Fatalf("stdin = %q", input)
	}
	if string(result.Stdout.Data) != "remote stdout" || string(result.Stderr.Data) != "remote stderr\n" {
		t.Fatalf("streams = %#v/%#v", result.Stdout, result.Stderr)
	}
	if result.Effect != EffectKnown || result.ExitCode != 0 || result.CommandBytesSent != uint64(len(input)) {
		t.Fatalf("result = %#v", result)
	}
}

func TestRunRemoteCommandRejectsSameInodeExecutableRewrite(t *testing.T) {
	plan := remoteHelperPlan(t, "success", filepath.Join(t.TempDir(), "stdin"), nil)
	info, err := os.Lstat(plan.Executable())
	if err != nil {
		t.Fatal(err)
	}
	original, err := os.ReadFile(plan.Executable())
	if err != nil {
		t.Fatal(err)
	}
	replacement := append([]byte(nil), original...)
	replacement[len(replacement)-1] ^= 1
	time.Sleep(time.Millisecond)
	if err := os.WriteFile(plan.Executable(), replacement, info.Mode().Perm()); err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(plan.Executable(), info.ModTime(), info.ModTime()); err != nil {
		t.Fatal(err)
	}
	result, err := RunRemoteCommand(context.Background(), plan, DefaultStreamBytes)
	if err == nil || !strings.Contains(err.Error(), "identity changed") {
		t.Fatalf("result=%#v err=%v", result, err)
	}
}

func TestRunRemoteCommandBannerAndMarkerEOFSendsZeroUserBytes(t *testing.T) {
	for _, mode := range []string{"banner", "marker-eof"} {
		t.Run(mode, func(t *testing.T) {
			capture := filepath.Join(t.TempDir(), "stdin")
			result, err := RunRemoteCommand(context.Background(), remoteHelperPlan(t, mode, capture, nil), DefaultStreamBytes)
			if err == nil {
				t.Fatal("marker failure error = nil")
			}
			input, readErr := os.ReadFile(capture)
			if readErr != nil && !errors.Is(readErr, os.ErrNotExist) {
				t.Fatal(readErr)
			}
			if len(input) != 0 || result.CommandBytesSent != 0 || result.Effect != EffectNone {
				t.Fatalf("input=%q result=%#v", input, result)
			}
		})
	}
}

func TestRunRemoteCommandContinuouslyDrainsDualOneMiBRings(t *testing.T) {
	result, err := RunRemoteCommand(context.Background(), remoteHelperPlan(t, "flood", filepath.Join(t.TempDir(), "stdin"), nil), DefaultStreamBytes)
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Stdout.Data) != DefaultStreamBytes || len(result.Stderr.Data) != DefaultStreamBytes {
		t.Fatalf("retained lengths = %d/%d", len(result.Stdout.Data), len(result.Stderr.Data))
	}
	if result.Stdout.Discarded != 4096 || result.Stderr.Discarded != 4096 {
		t.Fatalf("discarded = %d/%d", result.Stdout.Discarded, result.Stderr.Discarded)
	}
	if !bytes.Equal(result.Stdout.Data, bytes.Repeat([]byte{'o'}, DefaultStreamBytes)) || !bytes.Equal(result.Stderr.Data, bytes.Repeat([]byte{'e'}, DefaultStreamBytes)) {
		t.Fatal("rings did not retain stream tails")
	}
}

func TestRunRemoteCommandCancellationAndDetachedWriterAreEffectUnknown(t *testing.T) {
	t.Run("cancel", func(t *testing.T) {
		ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
		defer cancel()
		result, err := RunRemoteCommand(ctx, remoteHelperPlan(t, "hang", filepath.Join(t.TempDir(), "stdin"), nil), DefaultStreamBytes)
		if !errors.Is(err, context.DeadlineExceeded) || result.Effect != EffectUnknown || !result.Signaled {
			t.Fatalf("result=%#v err=%v", result, err)
		}
	})
	t.Run("detached writer", func(t *testing.T) {
		result, err := RunRemoteCommand(context.Background(), remoteHelperPlan(t, "detach", filepath.Join(t.TempDir(), "stdin"), nil), DefaultStreamBytes)
		if !errors.Is(err, exec.ErrWaitDelay) || result.Effect != EffectUnknown {
			t.Fatalf("result=%#v err=%v", result, err)
		}
	})
}

func TestRunRemoteCommandCancellationKillsTheOpenSSHProcessGroup(t *testing.T) {
	capture := filepath.Join(t.TempDir(), "stdin")
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	type outcome struct {
		result RemoteResult
		err    error
	}
	completed := make(chan outcome, 1)
	plan := remoteHelperPlan(t, "hang-child", capture, nil)
	go func() {
		result, err := RunRemoteCommand(ctx, plan, DefaultStreamBytes)
		completed <- outcome{result: result, err: err}
	}()
	var pidBytes []byte
	deadline := time.Now().Add(3 * time.Second)
	for {
		pidBytes, _ = os.ReadFile(capture + ".pid")
		if len(pidBytes) > 0 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("helper did not publish its process-group child")
		}
		time.Sleep(10 * time.Millisecond)
	}
	cancel()
	finished := <-completed
	if !errors.Is(finished.err, context.Canceled) || finished.result.Effect != EffectUnknown {
		result, err := finished.result, finished.err
		t.Fatalf("result=%#v err=%v", result, err)
	}
	pid, err := strconv.Atoi(string(pidBytes))
	if err != nil {
		t.Fatal(err)
	}
	childAlive := true
	t.Cleanup(func() {
		if childAlive {
			_ = syscall.Kill(pid, syscall.SIGKILL)
		}
	})
	killDeadline := time.Now().Add(time.Second)
	for {
		err = syscall.Kill(pid, 0)
		if errors.Is(err, syscall.ESRCH) {
			childAlive = false
			break
		}
		if time.Now().After(killDeadline) {
			t.Fatalf("process-group child %d survived cancellation: %v", pid, err)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func TestRunRemoteCommandDiagnosticIsBoundedSingleLineAndRedacted(t *testing.T) {
	secret := "remote-diagnostic-secret"
	result, err := RunRemoteCommand(context.Background(), remoteHelperPlan(t, "diagnostic", filepath.Join(t.TempDir(), "stdin"), []string{secret}), DefaultStreamBytes)
	if err == nil {
		t.Fatal("diagnostic marker failure error = nil")
	}
	if len(result.Diagnostic) > MaxDiagnosticBytes || strings.ContainsAny(result.Diagnostic, "\r\n") || strings.Contains(result.Diagnostic, "\u2028") {
		t.Fatalf("diagnostic is not bounded single-line: len=%d %q", len(result.Diagnostic), result.Diagnostic)
	}
	if strings.Contains(result.Diagnostic, secret) || !strings.Contains(result.Diagnostic, "[redacted]") {
		t.Fatalf("diagnostic was not redacted: %q", result.Diagnostic)
	}
}

func TestRemoteSSHHelperProcess(t *testing.T) {
	mode := os.Getenv("AMSFTP_REMOTE_HELPER")
	if mode == "" {
		return
	}
	reader := bufio.NewReader(os.Stdin)
	writeCapture := func() []byte {
		value, _ := reader.ReadString('\n')
		_ = os.WriteFile(os.Getenv("AMSFTP_REMOTE_CAPTURE"), []byte(value), 0o600)
		return []byte(value)
	}
	switch mode {
	case "success":
		_, _ = io.WriteString(os.Stdout, remoteMarker)
		writeCapture()
		_, _ = io.WriteString(os.Stdout, "remote stdout")
		_, _ = io.WriteString(os.Stderr, "remote stderr\n")
	case "banner":
		_, _ = io.WriteString(os.Stdout, "WARNING banner before marker\n")
		writeCapture()
	case "marker-eof":
		_, _ = io.WriteString(os.Stdout, "short")
	case "flood":
		_, _ = io.WriteString(os.Stdout, remoteMarker)
		writeCapture()
		_, _ = os.Stdout.Write(bytes.Repeat([]byte{'o'}, DefaultStreamBytes+4096))
		_, _ = os.Stderr.Write(bytes.Repeat([]byte{'e'}, DefaultStreamBytes+4096))
	case "hang":
		_, _ = io.WriteString(os.Stdout, remoteMarker)
		writeCapture()
		for {
			time.Sleep(time.Second)
		}
	case "hang-child":
		child := exec.Command("/bin/sh", "-c", "exec sleep 10")
		if err := child.Start(); err != nil {
			os.Exit(92)
		}
		_ = os.WriteFile(os.Getenv("AMSFTP_REMOTE_CAPTURE")+".pid", []byte(strconv.Itoa(child.Process.Pid)), 0o600)
		_, _ = io.WriteString(os.Stdout, remoteMarker)
		writeCapture()
		for {
			time.Sleep(time.Second)
		}
	case "detach":
		_, _ = io.WriteString(os.Stdout, remoteMarker)
		writeCapture()
		child := exec.Command("/bin/sh", "-c", "sleep 10")
		child.Stdout = os.Stdout
		child.Stderr = os.Stderr
		if err := child.Start(); err != nil {
			os.Exit(91)
		}
	case "diagnostic":
		_, _ = io.WriteString(os.Stdout, "WARNING banner before marker\n")
		_, _ = io.WriteString(os.Stderr, strings.Repeat("diagnostic "+os.Getenv("AMSFTP_REMOTE_SECRET")+"\r\n\u2028", 2048))
		writeCapture()
	default:
		os.Exit(90)
	}
	os.Exit(0)
}

func remoteHelperPlan(t *testing.T, mode, capture string, redactions []string) RemotePlan {
	t.Helper()
	binary, environment := writeRemoteSSHHelper(t, mode, capture)
	plan, err := PlanRemoteCommand(context.Background(), RemoteConfig{
		OpenSSH: openssh.Config{
			Binary:      binary,
			HostAlias:   "remote-test-host",
			Environment: environment,
			Redact:      redactions,
		},
		Attributes: trustedUtilityProber(),
	}, "/srv/work", "printf hello")
	if err != nil {
		t.Fatal(err)
	}
	return plan
}

func writeRemoteSSHHelper(t *testing.T, mode, capture string) (string, []string) {
	t.Helper()
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	directory := t.TempDir()
	binary := filepath.Join(directory, "ssh")
	script := "#!/bin/sh\nexec \"$AMSFTP_REMOTE_TEST_BINARY\" -test.run=^TestRemoteSSHHelperProcess$ -- \"$@\"\n"
	if err := os.WriteFile(binary, []byte(script), 0o700); err != nil {
		t.Fatal(err)
	}
	environment := append([]string(nil), os.Environ()...)
	environment = append(environment,
		"AMSFTP_REMOTE_TEST_BINARY="+executable,
		"AMSFTP_REMOTE_HELPER="+mode,
		"AMSFTP_REMOTE_CAPTURE="+capture,
		"AMSFTP_REMOTE_SECRET=remote-diagnostic-secret",
	)
	return binary, environment
}

func trustedUtilityProber() *fakeAttributeProber {
	uid, gid := uint32(0), uint32(0)
	dirMode, fileMode := uint32(0o040755), uint32(0o100755)
	return &fakeAttributeProber{attributes: map[string]openssh.SFTPAttributes{
		"/usr":            {UID: &uid, GID: &gid, Mode: &dirMode},
		"/usr/bin":        {UID: &uid, GID: &gid, Mode: &dirMode},
		"/usr/bin/printf": {UID: &uid, GID: &gid, Mode: &fileMode},
	}}
}

func cloneAttributes(input map[string]openssh.SFTPAttributes) map[string]openssh.SFTPAttributes {
	result := make(map[string]openssh.SFTPAttributes, len(input))
	for key, value := range input {
		copy := value
		result[key] = copy
	}
	return result
}

func writeRemoteSSH(t *testing.T) string {
	t.Helper()
	directory := t.TempDir()
	path := filepath.Join(directory, "ssh")
	if err := os.WriteFile(path, []byte("#!/bin/sh\nexit 0\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	return path
}
