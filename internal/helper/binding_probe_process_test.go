package helper

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestOpenSSHBindingProbeUsesOnlyFrozenAbsoluteUtilitiesAndParsesByteZeroOutput(t *testing.T) {
	directory := t.TempDir()
	sshPath := filepath.Join(directory, "ssh-probe-fixture")
	capture := filepath.Join(directory, "argv")
	script := "#!/bin/sh\n: > \"$AMSFTP_PROBE_ARG_CAPTURE\"\nfor arg in \"$@\"; do printf '%s\\n' \"$arg\" >> \"$AMSFTP_PROBE_ARG_CAPTURE\"; done\nprintf 'amsftp-helper-bind-v1\\0001001\\000/home/alice\\000Linux\\000x86_64\\000'\n"
	if err := os.WriteFile(sshPath, []byte(script), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(sshPath, 0o700); err != nil { // #nosec G302 -- this temporary fixture must be executable.
		t.Fatal(err)
	}
	observation, err := RunOpenSSHBindingProbe(context.Background(), BindingProbeConfig{
		SSHPath: sshPath, HostAlias: "fixture-host", Environment: append(os.Environ(), "AMSFTP_PROBE_ARG_CAPTURE="+capture), Timeout: 5 * time.Second,
	})
	if err != nil {
		t.Fatal(err)
	}
	if observation != (Observation{UID: 1001, Home: "/home/alice", Target: Target{OS: "linux", Arch: "amd64"}}) {
		t.Fatalf("observation = %#v", observation)
	}
	raw, err := os.ReadFile(capture) // #nosec G304 -- capture is inside t.TempDir.
	if err != nil {
		t.Fatal(err)
	}
	want, err := BindingProbeSSHArguments(sshPath, "fixture-host")
	if err != nil {
		t.Fatal(err)
	}
	got := strings.Split(strings.TrimSuffix(string(raw), "\n"), "\n")
	if strings.Join(got, "\x00") != strings.Join(want[1:], "\x00") {
		t.Fatalf("binding argv = %#v, want %#v", got, want[1:])
	}
	command := want[len(want)-1]
	for _, required := range []string{"/usr/bin/printf", "/usr/bin/id", "command -p pwd -P", "/usr/bin/uname"} {
		if !strings.Contains(command, required) {
			t.Fatalf("binding command missing %q: %q", required, command)
		}
	}
}

func TestOpenSSHBindingProbeRejectsBannerAndStderrOverflow(t *testing.T) {
	for _, test := range []struct {
		name   string
		script string
	}{
		{name: "banner", script: "printf 'banner\\namsftp-helper-bind-v1\\0001001\\000/home/alice\\000Linux\\000x86_64\\000'"},
		{name: "stderr overflow", script: "head -c 65537 /dev/zero >&2; printf 'amsftp-helper-bind-v1\\0001001\\000/home/alice\\000Linux\\000x86_64\\000'"},
	} {
		t.Run(test.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "ssh-probe-fixture")
			if err := os.WriteFile(path, []byte("#!/bin/sh\n"+test.script+"\n"), 0o600); err != nil {
				t.Fatal(err)
			}
			if err := os.Chmod(path, 0o700); err != nil { // #nosec G302 -- this temporary fixture must be executable.
				t.Fatal(err)
			}
			if _, err := RunOpenSSHBindingProbe(context.Background(), BindingProbeConfig{SSHPath: path, HostAlias: "fixture-host", Environment: os.Environ(), Timeout: 5 * time.Second}); err == nil {
				t.Fatal("unsafe binding probe succeeded")
			}
		})
	}
}
