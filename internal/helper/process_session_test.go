package helper

import (
	"context"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/TyrantLucifer/awesome-mac-sftp/internal/testkit"
)

func TestOpenSSHProcessSessionUsesFrozenArgvHeartbeatAndBoundedStderr(t *testing.T) {
	sshPath, capture := fakeOpenSSHExecutable(t)
	session, err := StartOpenSSHSession(context.Background(), OpenSSHSessionConfig{
		SSHPath: sshPath, HostAlias: "fixture-host",
		Plan: fixtureProcessInstallPlan(),
		Environment: append(os.Environ(),
			"AMSFTP_HELPER_CHILD=serve",
			"AMSFTP_HELPER_TEST_BINARY="+mustExecutable(t),
			"AMSFTP_HELPER_ARG_CAPTURE="+capture,
		),
		Hello: ClientHello{
			MinimumProtocol: 1, MaximumProtocol: 1, MaximumFrame: MaxHelperFrameBytes, MaximumConcurrent: 1,
			ClientVersion: Version{Major: 4}, Capabilities: []CapabilityRequest{{Name: CapabilityDiskStats, MaximumVersion: 1}},
		},
		HandshakeTimeout: 5 * time.Second, HeartbeatInterval: 10 * time.Millisecond, HeartbeatTimeout: 100 * time.Millisecond,
	})
	if err != nil {
		t.Fatal(err)
	}
	if session.Client().Level() != 1 || !session.Client().HasCapability(CapabilityDiskStats) {
		t.Fatalf("session negotiation = %#v", session.Client().Negotiated())
	}
	events, err := session.Client().Start(context.Background(), "req_kkkkkkkkkkkkkkkkkkkkkkkkkk", CapabilityDiskStats, json.RawMessage(`{"path":"/"}`))
	if err != nil {
		t.Fatal(err)
	}
	if event := <-events; event.Type != FrameResult {
		t.Fatalf("disk result = %#v", event)
	}
	if event := <-events; event.Type != FrameComplete {
		t.Fatalf("disk completion = %#v", event)
	}
	if err := session.Close(); err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(capture) // #nosec G304 -- capture is inside the test-owned persistent directory.
	if err != nil {
		t.Fatal(err)
	}
	want, err := HelperSSHArguments(sshPath, "fixture-host", fixtureProcessInstallPlan())
	if err != nil {
		t.Fatal(err)
	}
	if got := strings.Split(strings.TrimSuffix(string(raw), "\n"), "\n"); strings.Join(got, "\x00") != strings.Join(want[1:], "\x00") {
		t.Fatalf("captured argv = %#v, want %#v", got, want[1:])
	}
	if session.Diagnostic() != "" {
		t.Fatalf("unexpected diagnostic %q", session.Diagnostic())
	}
}

func TestNativeHelperVersionMatrix(t *testing.T) {
	for _, mode := range []string{"future-minimum-client", "future-protocol"} {
		t.Run(mode, func(t *testing.T) {
			session, err := startNativeHelperMatrixSession(t, mode)
			if err == nil {
				_ = session.Close()
				t.Fatalf("current client accepted incompatible Helper mode %q", mode)
			}
			recovered, err := startNativeHelperMatrixSession(t, "serve")
			if err != nil {
				t.Fatalf("current Helper did not recover after %s rejection: %v", mode, err)
			}
			if recovered.Client().Level() != 1 || !recovered.Client().HasCapability(CapabilityDiskStats) {
				t.Fatalf("recovered current Helper negotiation = %#v", recovered.Client().Negotiated())
			}
			if err := recovered.Close(); err != nil {
				t.Fatal(err)
			}
		})
	}

	t.Run("future artifact exact binding", func(t *testing.T) {
		session, err := startNativeHelperMatrixSession(t, "future-compatible-client")
		if err != nil {
			t.Fatalf("future Helper with compatible protocol/minimum client did not negotiate: %v", err)
		}
		manifest, err := ParseManifestV1(fixtureManifest)
		if err != nil {
			_ = session.Close()
			t.Fatal(err)
		}
		if err := ValidateEnabledClient(EnablePlan{Manifest: manifest}, session.Client(), []CapabilityName{CapabilityDiskStats}); err == nil || !strings.Contains(err.Error(), "exact artifact version mismatch") {
			_ = session.Close()
			t.Fatalf("future Helper artifact binding error = %v", err)
		}
		if err := session.Close(); err != nil {
			t.Fatal(err)
		}
		recovered, err := startNativeHelperMatrixSession(t, "serve")
		if err != nil {
			t.Fatalf("current Helper did not recover after future artifact rejection: %v", err)
		}
		if err := ValidateEnabledClient(EnablePlan{Manifest: manifest}, recovered.Client(), []CapabilityName{CapabilityDiskStats}); err != nil {
			_ = recovered.Close()
			t.Fatalf("current Helper exact binding after rejection: %v", err)
		}
		if err := recovered.Close(); err != nil {
			t.Fatal(err)
		}
	})
}

func startNativeHelperMatrixSession(t *testing.T, mode string) (*ProcessSession, error) {
	t.Helper()
	sshPath, capture := fakeOpenSSHExecutable(t)
	return StartOpenSSHSession(context.Background(), OpenSSHSessionConfig{
		SSHPath: sshPath, HostAlias: "fixture-host", Plan: fixtureProcessInstallPlan(),
		Environment: append(os.Environ(),
			"AMSFTP_HELPER_CHILD="+mode,
			"AMSFTP_HELPER_TEST_BINARY="+mustExecutable(t),
			"AMSFTP_HELPER_ARG_CAPTURE="+capture,
		),
		Hello: ClientHello{
			MinimumProtocol: 1, MaximumProtocol: 1, MaximumFrame: MaxHelperFrameBytes,
			MaximumConcurrent: 1, ClientVersion: Version{Major: 4},
			Capabilities: []CapabilityRequest{{Name: CapabilityDiskStats, MaximumVersion: 1}},
		},
		HandshakeTimeout: 5 * time.Second,
	})
}

func TestOpenSSHProcessSessionRejectsStderrBeyondHardCap(t *testing.T) {
	sshPath, capture := fakeOpenSSHExecutable(t)
	_, err := StartOpenSSHSession(context.Background(), OpenSSHSessionConfig{
		SSHPath: sshPath, HostAlias: "fixture-host", Plan: fixtureProcessInstallPlan(),
		Environment: append(os.Environ(),
			"AMSFTP_HELPER_CHILD=stderr-overflow",
			"AMSFTP_HELPER_TEST_BINARY="+mustExecutable(t),
			"AMSFTP_HELPER_ARG_CAPTURE="+capture,
		),
		Hello:            ClientHello{MinimumProtocol: 1, MaximumProtocol: 1, MaximumFrame: MaxHelperFrameBytes, MaximumConcurrent: 1, ClientVersion: Version{Major: 4}},
		HandshakeTimeout: 5 * time.Second,
	})
	if err == nil || !strings.Contains(err.Error(), "stderr") {
		t.Fatalf("stderr overflow error = %v", err)
	}
}

func TestOpenSSHProcessSessionAcceptsExactStderrHardCap(t *testing.T) {
	sshPath, capture := fakeOpenSSHExecutable(t)
	session, err := StartOpenSSHSession(context.Background(), OpenSSHSessionConfig{
		SSHPath: sshPath, HostAlias: "fixture-host", Plan: fixtureProcessInstallPlan(),
		Environment: append(os.Environ(),
			"AMSFTP_HELPER_CHILD=stderr-exact",
			"AMSFTP_HELPER_TEST_BINARY="+mustExecutable(t),
			"AMSFTP_HELPER_ARG_CAPTURE="+capture,
		),
		Hello: ClientHello{
			MinimumProtocol: 1, MaximumProtocol: 1, MaximumFrame: MaxHelperFrameBytes,
			MaximumConcurrent: 1, ClientVersion: Version{Major: 4},
		},
		HandshakeTimeout: 5 * time.Second,
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := session.Close(); err != nil {
		t.Fatal(err)
	}
	if got := len(session.Diagnostic()); got != MaxHelperStderrBytes {
		t.Fatalf("diagnostic bytes = %d, want %d", got, MaxHelperStderrBytes)
	}
}

func TestOpenSSHProcessSessionHeartbeatFailureTerminatesProcessWithoutExplicitClose(t *testing.T) {
	sshPath, capture := fakeOpenSSHExecutable(t)
	session, err := StartOpenSSHSession(context.Background(), OpenSSHSessionConfig{
		SSHPath: sshPath, HostAlias: "fixture-host", Plan: fixtureProcessInstallPlan(),
		Environment: append(os.Environ(),
			"AMSFTP_HELPER_CHILD=no-pong",
			"AMSFTP_HELPER_TEST_BINARY="+mustExecutable(t),
			"AMSFTP_HELPER_ARG_CAPTURE="+capture,
		),
		Hello: ClientHello{
			MinimumProtocol: 1, MaximumProtocol: 1, MaximumFrame: MaxHelperFrameBytes,
			MaximumConcurrent: 1, ClientVersion: Version{Major: 4},
		},
		HandshakeTimeout: 5 * time.Second, HeartbeatInterval: 10 * time.Millisecond, HeartbeatTimeout: 20 * time.Millisecond,
	})
	if err != nil {
		t.Fatal(err)
	}
	wait := make(chan error, 1)
	go func() { wait <- session.Wait() }()
	select {
	case <-wait:
	case <-time.After(time.Second):
		t.Fatal("heartbeat failure did not terminate the OpenSSH process group")
	}
	<-session.stderrDone
	if session.Client().Failure() == nil {
		t.Fatal("terminated heartbeat session has no client failure")
	}
	if err := session.Client().Close(); err != nil {
		t.Fatal(err)
	}
}

func TestOpenSSHHelperChild(t *testing.T) {
	mode := os.Getenv("AMSFTP_HELPER_CHILD")
	if mode == "" {
		return
	}
	if mode == "stderr-overflow" {
		if _, err := io.CopyN(os.Stderr, strings.NewReader(strings.Repeat("x", MaxHelperStderrBytes+1)), MaxHelperStderrBytes+1); err != nil {
			os.Exit(12)
		}
		select {}
	}
	if mode == "stderr-exact" {
		if _, err := io.CopyN(os.Stderr, strings.NewReader(strings.Repeat("x", MaxHelperStderrBytes)), MaxHelperStderrBytes); err != nil {
			os.Exit(12)
		}
	}
	if mode == "no-pong" {
		_, _ = ServeHandshake(os.Stdin, os.Stdout, ServerConfig{
			Protocol: 1, HelperVersion: Version{Major: 4}, MaximumFrame: MaxHelperFrameBytes, MaximumConcurrent: 1,
		})
		select {}
	}
	config := NewLocalServiceConfig(Version{Major: 4})
	switch mode {
	case "future-minimum-client":
		config = NewLocalServiceConfig(Version{Major: 5})
		config.Server.MinimumClient = Version{Major: 5}
	case "future-protocol":
		config = NewLocalServiceConfig(Version{Major: 5})
		config.Server.Protocol = 2
	case "future-compatible-client":
		config = NewLocalServiceConfig(Version{Major: 5})
	}
	err := Serve(context.Background(), os.Stdin, os.Stdout, config)
	if err != nil {
		_, _ = os.Stderr.WriteString("child serve: " + err.Error() + "\n")
		os.Exit(11)
	}
	os.Exit(0)
}

func fakeOpenSSHExecutable(t *testing.T) (string, string) {
	t.Helper()
	directory := testkit.PersistentTempDir(t)
	path := filepath.Join(directory, "ssh-fixture")
	capture := filepath.Join(directory, "argv")
	script := "#!/bin/sh\n: > \"$AMSFTP_HELPER_ARG_CAPTURE\"\nfor arg in \"$@\"; do printf '%s\\n' \"$arg\" >> \"$AMSFTP_HELPER_ARG_CAPTURE\"; done\nexec \"$AMSFTP_HELPER_TEST_BINARY\" -test.run=TestOpenSSHHelperChild\n"
	if err := os.WriteFile(path, []byte(script), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(path, 0o700); err != nil { // #nosec G302 -- this temporary fixture must be executable.
		t.Fatal(err)
	}
	return path, capture
}

func fixtureProcessInstallPlan() InstallPlan {
	return InstallPlan{FinalPath: "/home/alice/.local/lib/amsftp/helpers/p1/4.0.0/linux-amd64-" + strings.Repeat("a", 64) + "/amsftp"}
}

func mustExecutable(t *testing.T) string {
	t.Helper()
	value, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	return value
}
