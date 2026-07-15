//go:build darwin

package platform

import (
	"fmt"
	"syscall"
	"unsafe"
)

const (
	darwinSOLLocal      = 0
	darwinLocalPeerCred = 1
	darwinXucredSize    = 76
	darwinXucredVersion = 0
)

type darwinXucred struct {
	Version uint32
	UID     uint32
	NGroups int16
	_       [2]byte
	Groups  [16]uint32
}

func platformPeerUID(fd uintptr) (int, error) {
	credentials := darwinXucred{}
	length := uint32(unsafe.Sizeof(credentials))
	_, _, errno := syscall.Syscall6(
		syscall.SYS_GETSOCKOPT,
		fd,
		darwinSOLLocal,
		darwinLocalPeerCred,
		// #nosec G103 -- getsockopt requires an audited fixed-layout credential buffer.
		uintptr(unsafe.Pointer(&credentials)),
		// #nosec G103 -- getsockopt requires the address of the bounded buffer length.
		uintptr(unsafe.Pointer(&length)),
		0,
	)
	if errno != 0 {
		return 0, errno
	}
	if length != darwinXucredSize || unsafe.Sizeof(credentials) != darwinXucredSize {
		return 0, fmt.Errorf("unexpected LOCAL_PEERCRED size %d", length)
	}
	if credentials.Version != darwinXucredVersion {
		return 0, fmt.Errorf("unexpected LOCAL_PEERCRED version %d", credentials.Version)
	}
	return int(credentials.UID), nil
}
