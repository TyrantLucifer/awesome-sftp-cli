//go:build darwin || linux

package platform

import (
	"errors"
	"net"
	"os"
	"strings"
	"syscall"
	"testing"
)

func TestVerifyPeerUIDValue(t *testing.T) {
	if err := verifyPeerUIDValue(1000, 1000, nil); err != nil {
		t.Fatalf("verifyPeerUIDValue(): %v", err)
	}
	if err := verifyPeerUIDValue(2000, 1000, nil); err == nil {
		t.Fatal("mismatched peer uid accepted")
	}
	queryErr := errors.New("sentinel peer query failure")
	if err := verifyPeerUIDValue(0, 1000, queryErr); err == nil || !strings.Contains(err.Error(), queryErr.Error()) {
		t.Fatalf("query error = %v", err)
	}
}

func TestVerifyPeerUIDAcceptsCurrentNativeUnixPeer(t *testing.T) {
	left, right := unixSocketPair(t)
	defer left.Close()
	defer right.Close()

	if err := VerifyPeerUID(left); err != nil {
		t.Fatalf("VerifyPeerUID(left): %v", err)
	}
	if err := VerifyPeerUID(right); err != nil {
		t.Fatalf("VerifyPeerUID(right): %v", err)
	}
}

func unixSocketPair(t *testing.T) (*net.UnixConn, *net.UnixConn) {
	t.Helper()
	fds, err := syscall.Socketpair(syscall.AF_UNIX, syscall.SOCK_STREAM, 0)
	if err != nil {
		t.Fatalf("socketpair: %v", err)
	}
	files := []*os.File{
		os.NewFile(uintptr(fds[0]), "peer-left"),
		os.NewFile(uintptr(fds[1]), "peer-right"),
	}
	connections := make([]*net.UnixConn, 0, 2)
	for _, file := range files {
		connection, fileErr := net.FileConn(file)
		_ = file.Close()
		if fileErr != nil {
			t.Fatalf("net.FileConn: %v", fileErr)
		}
		unixConnection, ok := connection.(*net.UnixConn)
		if !ok {
			_ = connection.Close()
			t.Fatalf("connection type = %T", connection)
		}
		connections = append(connections, unixConnection)
	}
	return connections[0], connections[1]
}
