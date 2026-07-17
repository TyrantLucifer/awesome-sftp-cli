package helper

import (
	"context"
	"errors"
	"io"
	"net"
	"os"
	"path/filepath"
	"syscall"
	"testing"
	"time"

	"github.com/TyrantLucifer/awesome-mac-sftp/internal/transport/openssh"
	pkgsftp "github.com/pkg/sftp"
)

func TestSFTPInstallRemoteProvidesExactExclusiveNoReplacePrimitives(t *testing.T) {
	client, cleanup := newHelperSFTPClient(t)
	defer cleanup()
	probeCalls := 0
	remote, err := NewSFTPInstallRemote(SFTPInstallRemoteConfig{
		Client: client,
		BindingProbe: func(context.Context) ([]byte, error) {
			probeCalls++
			return []byte("amsftp-helper-bind-v1\x001001\x00/home/alice\x00Linux\x00x86_64\x00"), nil
		},
		LinkAttributes: localLinkAttributes,
		MkdirExact: func(_ context.Context, value string, mode uint32) error {
			return os.Mkdir(value, os.FileMode(mode)) // #nosec G115 -- production adapter and this test permit only 0700.
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	observation, err := remote.Probe(context.Background())
	if err != nil || observation.UID != 1001 || observation.Target != (Target{OS: "linux", Arch: "amd64"}) || probeCalls != 1 {
		t.Fatalf("probe = %#v, calls=%d, err=%v", observation, probeCalls, err)
	}

	root := filepath.Join(t.TempDir(), "install")
	if err := remote.Mkdir(context.Background(), root, 0o700); err != nil {
		t.Fatal(err)
	}
	attrs, err := remote.Lstat(context.Background(), root)
	if err != nil || attrs.Kind != RemoteDirectory || attrs.Mode != 0o700 || attrs.UID != uint32(os.Geteuid()) { // #nosec G115 -- supported test UIDs are non-negative and fit uint32.
		t.Fatalf("directory attrs = %#v, %v", attrs, err)
	}
	temp := filepath.Join(root, "temp")
	handle, err := remote.OpenExclusive(context.Background(), temp)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := remote.OpenExclusive(context.Background(), temp); !errors.Is(err, ErrRemoteAlreadyExists) {
		t.Fatalf("second exclusive open = %v", err)
	}
	if err := handle.Chmod(context.Background(), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := handle.Write(context.Background(), []byte("fixture")); err != nil {
		t.Fatal(err)
	}
	if err := handle.Close(context.Background()); err != nil {
		t.Fatal(err)
	}
	final := filepath.Join(root, "final")
	if err := remote.PublishNoReplace(context.Background(), temp, final); err != nil {
		t.Fatal(err)
	}
	reader, err := remote.OpenRead(context.Background(), final)
	if err != nil {
		t.Fatal(err)
	}
	data, err := io.ReadAll(reader)
	_ = reader.Close()
	if err != nil || string(data) != "fixture" {
		t.Fatalf("published data = %q, %v", data, err)
	}
	other := filepath.Join(root, "other")
	otherHandle, err := remote.OpenExclusive(context.Background(), other)
	if err != nil {
		t.Fatal(err)
	}
	_ = otherHandle.Close(context.Background())
	if err := remote.PublishNoReplace(context.Background(), other, final); !errors.Is(err, ErrRemoteAlreadyExists) {
		t.Fatalf("replace publication = %v", err)
	}
	if err := remote.RemoveExact(context.Background(), other); err != nil {
		t.Fatal(err)
	}
}

func TestSFTPInstallRemoteRejectsMissingRawUIDOrMode(t *testing.T) {
	client, cleanup := newHelperSFTPClient(t)
	defer cleanup()
	path := filepath.Join(t.TempDir(), "file")
	if err := os.WriteFile(path, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	remote, err := NewSFTPInstallRemote(SFTPInstallRemoteConfig{
		Client: client, BindingProbe: func(context.Context) ([]byte, error) { return nil, errors.New("unused") },
		LinkAttributes: func(context.Context, string) (openssh.SFTPAttributes, error) { return openssh.SFTPAttributes{}, nil },
		MkdirExact:     func(context.Context, string, uint32) error { return errors.New("unused") },
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := remote.Lstat(context.Background(), path); err == nil {
		t.Fatal("Lstat accepted attributes with absent UID/mode")
	}
}

func newHelperSFTPClient(t *testing.T) (*pkgsftp.Client, func()) {
	t.Helper()
	serverConnection, clientConnection := net.Pipe()
	server, err := pkgsftp.NewServer(serverConnection)
	if err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() { done <- server.Serve() }()
	client, err := pkgsftp.NewClientPipe(clientConnection, clientConnection)
	if err != nil {
		t.Fatal(err)
	}
	return client, func() {
		_ = client.Close()
		_ = server.Close()
		_ = clientConnection.Close()
		select {
		case <-done:
		case <-time.After(time.Second):
			t.Error("SFTP server did not stop")
		}
	}
}

func localLinkAttributes(_ context.Context, path string) (openssh.SFTPAttributes, error) {
	info, err := os.Lstat(path)
	if err != nil {
		return openssh.SFTPAttributes{}, err
	}
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !ok {
		return openssh.SFTPAttributes{}, errors.New("missing local stat")
	}
	mode := uint32(stat.Mode) //nolint:unconvert // syscall.Stat_t.Mode is uint16 on Darwin and uint32 on Linux.
	uid := stat.Uid
	return openssh.SFTPAttributes{Mode: &mode, UID: &uid}, nil
}
