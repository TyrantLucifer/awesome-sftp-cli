//go:build linux

package platform

import "syscall"

func platformPeerUID(fd uintptr) (int, error) {
	credentials, err := syscall.GetsockoptUcred(int(fd), syscall.SOL_SOCKET, syscall.SO_PEERCRED)
	if err != nil {
		return 0, err
	}
	return int(credentials.Uid), nil
}
