//go:build darwin || linux

package platform

import (
	"fmt"
	"net"
	"os"
)

func VerifyPeerUID(connection *net.UnixConn) error {
	if connection == nil {
		return fmt.Errorf("peer connection is nil")
	}
	raw, err := connection.SyscallConn()
	if err != nil {
		return fmt.Errorf("access peer socket: %w", err)
	}
	var uid int
	var queryErr error
	if err := raw.Control(func(fd uintptr) {
		uid, queryErr = platformPeerUID(fd)
	}); err != nil {
		return fmt.Errorf("inspect peer socket: %w", err)
	}
	return verifyPeerUIDValue(uid, os.Geteuid(), queryErr)
}

func verifyPeerUIDValue(actual, expected int, queryErr error) error {
	if queryErr != nil {
		return fmt.Errorf("query peer uid: %w", queryErr)
	}
	if actual != expected {
		return fmt.Errorf("peer uid %d does not match effective uid %d", actual, expected)
	}
	return nil
}
