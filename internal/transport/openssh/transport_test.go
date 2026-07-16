package openssh

import (
	"context"
	"errors"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/TyrantLucifer/awesome-mac-sftp/internal/platform"
	pkgsftp "github.com/pkg/sftp"
)

func TestArgumentsMatchADR0001Exactly(t *testing.T) {
	got, err := Arguments("work-alias")
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"-T", "-oEscapeChar=none", "-oForwardAgent=no", "-oForwardX11=no", "-oPermitLocalCommand=no", "-oClearAllForwardings=yes", "-oRemoteCommand=none", "-oStdinNull=no", "-oForkAfterAuthentication=no", "-oTunnel=no", "-oGSSAPIDelegateCredentials=no", "-s", "work-alias", "sftp"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("arguments = %#v", got)
	}
}

func TestValidateHostAliasRejectsOptionAndControlInjection(t *testing.T) {
	for _, value := range []string{"", "-oProxyCommand=bad", "host\x00bad", "host\nbad", "host\x7fbad"} {
		if err := ValidateHostAlias(value); err == nil {
			t.Fatalf("ValidateHostAlias(%q) error = nil", value)
		}
	}
	if err := ValidateHostAlias("work-prod.example"); err != nil {
		t.Fatal(err)
	}
}

func TestExpectedExitOnlyAcceptsCancellationSignal(t *testing.T) {
	nonzero := exec.Command("/bin/sh", "-c", "exit 7")
	if err := nonzero.Run(); err == nil || isExpectedExit(err) {
		t.Fatalf("ordinary non-zero exit classified as expected: %v", err)
	}

	killed := exec.Command("/bin/sh", "-c", "exec sleep 30")
	if err := killed.Start(); err != nil {
		t.Fatal(err)
	}
	if err := killed.Process.Kill(); err != nil {
		t.Fatal(err)
	}
	if err := killed.Wait(); err == nil || !isExpectedExit(err) {
		t.Fatalf("cancellation signal classified as unexpected: %v", err)
	}
}

func TestBoundedBufferRedactsSensitiveValues(t *testing.T) {
	buffer := boundedBuffer{redactions: []string{"stage1-secret-canary"}}
	if _, err := buffer.Write([]byte("proxy failed with stage1-secret-canary\n")); err != nil {
		t.Fatal(err)
	}
	diagnostic := buffer.String()
	if strings.Contains(diagnostic, "stage1-secret-canary") || !strings.Contains(diagnostic, "[redacted]") {
		t.Fatalf("diagnostic = %q", diagnostic)
	}
}

func TestDialLifecycleCancelsOnlyBeforeEstablishment(t *testing.T) {
	t.Run("before establishment", func(t *testing.T) {
		parent := context.Background()
		commandContext, lifecycle := newDialLifecycle(parent)
		defer lifecycle.stop()

		lifecycle.cancelBeforeEstablished()
		select {
		case <-commandContext.Done():
		default:
			t.Fatal("command context was not canceled")
		}
	})

	t.Run("after establishment", func(t *testing.T) {
		parent := context.Background()
		commandContext, lifecycle := newDialLifecycle(parent)
		defer lifecycle.stop()
		if err := lifecycle.establish(parent); err != nil {
			t.Fatal(err)
		}

		lifecycle.cancelBeforeEstablished()
		select {
		case <-commandContext.Done():
			t.Fatal("established command context was canceled")
		default:
		}
	})
}

func TestDialLifecycleRejectsEstablishmentAfterParentCancellation(t *testing.T) {
	parent, cancelParent := context.WithCancel(context.Background())
	commandContext, lifecycle := newDialLifecycle(parent)
	defer lifecycle.stop()
	cancelParent()

	if err := lifecycle.establish(parent); !errors.Is(err, context.Canceled) {
		t.Fatalf("establish() error = %v, want context.Canceled", err)
	}
	select {
	case <-commandContext.Done():
	case <-time.After(time.Second):
		t.Fatal("command context was not canceled")
	}
}

func TestCloseTreatsItsOwnedCommandCancellationAsExpected(t *testing.T) {
	if !isExpectedExit(context.Canceled) {
		t.Fatal("Close-owned command cancellation would be reported as an OpenSSH failure")
	}
}

func TestDialParentCancellationAfterNegotiationKeepsSessionAlive(t *testing.T) {
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	workingDirectory, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	directory, err := os.MkdirTemp(workingDirectory, ".amsftp-ssh-helper-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(directory) })
	// #nosec G302 -- the private executable-fixture directory must be owner-only and searchable.
	if err := os.Chmod(directory, 0o700); err != nil {
		t.Fatal(err)
	}
	binary := filepath.Join(directory, "ssh")
	// #nosec G306 -- executable fixture intentionally requires owner execute permission.
	if err := os.WriteFile(binary, []byte("#!/bin/sh\nexec \"$AMSFTP_TEST_BINARY\" -test.run=^TestSFTPServerHelperProcess$ --\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := platform.ValidateExecutable(binary); err != nil {
		t.Skipf("trusted executable fixture is unavailable; lifecycle state is covered without a subprocess: %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	session, err := Dial(ctx, Config{Binary: binary, HostAlias: "test-host", Environment: append(os.Environ(), "AMSFTP_TEST_SFTP_SERVER=1", "AMSFTP_TEST_BINARY="+executable)})
	if err != nil {
		t.Fatal(err)
	}
	defer session.Close()
	cancel()
	time.Sleep(50 * time.Millisecond)
	if _, err := session.Client().Stat("."); err != nil {
		t.Fatalf("SFTP session closed with completed dial context: %v", err)
	}
}

func TestSFTPServerHelperProcess(t *testing.T) {
	if os.Getenv("AMSFTP_TEST_SFTP_SERVER") != "1" {
		return
	}
	server, err := pkgsftp.NewServer(stdioReadWriteCloser{})
	if err != nil {
		os.Exit(2)
	}
	if err := server.Serve(); err != nil && !errors.Is(err, io.EOF) {
		os.Exit(3)
	}
	os.Exit(0)
}

type stdioReadWriteCloser struct{}

func (stdioReadWriteCloser) Read(value []byte) (int, error)  { return os.Stdin.Read(value) }
func (stdioReadWriteCloser) Write(value []byte) (int, error) { return os.Stdout.Write(value) }
func (stdioReadWriteCloser) Close() error                    { return nil }
