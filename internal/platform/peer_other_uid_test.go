//go:build darwin || linux

package platform

import (
	"errors"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"
)

func TestControlSocketRejectsOtherUIDPeer(t *testing.T) {
	if os.Geteuid() != 0 {
		t.Skip("requires root to launch a real peer under another uid")
	}
	hostileUID := requiredCredentialID(t, "AMSFTP_HOSTILE_UID")
	hostileGID := requiredCredentialID(t, "AMSFTP_HOSTILE_GID")
	if hostileUID == 0 {
		t.Fatalf("hostile uid %d matches server euid", hostileUID)
	}

	directory := shortPrivateTemporaryDirectory(t)
	lock, err := AcquireInstanceLock(filepath.Join(directory, lockFileName), ValidateRuntimeFallback)
	if err != nil {
		t.Fatalf("AcquireInstanceLock(): %v", err)
	}
	defer lock.Close()
	path := filepath.Join(directory, controlSocketName)
	listener, err := ListenControlSocket(path, ValidateRuntimeFallback, lock)
	if err != nil {
		t.Fatalf("ListenControlSocket(): %v", err)
	}
	defer listener.Close()

	// Deliberately remove the DAC defense so the credential check is the only
	// boundary under test. Production creates these objects as 0700/0600.
	// #nosec G302 -- widening is intentional and confined to this root-only adversarial fixture.
	if err := os.Chmod(directory, 0o777); err != nil {
		t.Fatalf("chmod runtime fixture: %v", err)
	}
	// #nosec G302 -- widening is intentional and confined to this root-only adversarial fixture.
	if err := os.Chmod(path, 0o777); err != nil {
		t.Fatalf("chmod socket fixture: %v", err)
	}

	accepted := make(chan error, 1)
	go func() {
		connection, acceptErr := listener.Accept()
		if connection != nil {
			_ = connection.Close()
		}
		accepted <- acceptErr
	}()
	// #nosec G204,G702 -- the executable is the current test binary and the sole argument is fixed.
	command := exec.Command(os.Args[0], "-test.run=^TestOtherUIDPeerHelperProcess$")
	command.Env = append(os.Environ(),
		"AMSFTP_OTHER_UID_PEER_HELPER=1",
		"AMSFTP_OTHER_UID_PEER_SOCKET="+path,
	)
	command.SysProcAttr = &syscall.SysProcAttr{Credential: &syscall.Credential{
		Uid: hostileUID,
		Gid: hostileGID,
	}}
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("other-uid peer helper: %v\n%s", err, output)
	}
	select {
	case err := <-accepted:
		if err == nil || !strings.Contains(err.Error(), "does not match effective uid") {
			t.Fatalf("Accept() error = %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("Accept() did not reject other-uid peer")
	}
}

func TestOtherUIDPeerHelperProcess(t *testing.T) {
	if os.Getenv("AMSFTP_OTHER_UID_PEER_HELPER") != "1" {
		t.Skip("subprocess helper")
	}
	path := os.Getenv("AMSFTP_OTHER_UID_PEER_SOCKET")
	if path == "" {
		t.Fatal("missing helper socket path")
	}
	connection, err := net.DialUnix("unix", nil, &net.UnixAddr{Name: path, Net: "unix"})
	if err != nil {
		t.Fatalf("dial fixture socket: %v", err)
	}
	if err := connection.Close(); err != nil && !errors.Is(err, net.ErrClosed) {
		t.Fatalf("close fixture connection: %v", err)
	}
}

func requiredCredentialID(t *testing.T, name string) uint32 {
	t.Helper()
	value := os.Getenv(name)
	parsed, err := strconv.ParseUint(value, 10, 32)
	if err != nil {
		t.Fatalf("%s=%q: %v", name, value, err)
	}
	return uint32(parsed)
}
