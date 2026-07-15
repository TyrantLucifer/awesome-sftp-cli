//go:build darwin

package platform

import (
	"fmt"
	"syscall"
	"unsafe"
)

const (
	darwinAttributeBitmapCount     = 5
	darwinExtendedSecurityAttr     = 0x00400000
	darwinGetattrNoFollowAny       = 0x00000800
	darwinExtendedSecurityMaxBytes = 4096
)

type darwinAttributeList struct {
	BitmapCount uint16
	Reserved    uint16
	Common      uint32
	Volume      uint32
	Directory   uint32
	File        uint32
	Fork        uint32
}

func newPlatformACLValidator() aclValidator {
	return darwinACLValidator{}
}

type darwinACLValidator struct{}

func (darwinACLValidator) validateACL(path string, profile aclProfile, _ bool) error {
	filesystem, err := darwinFilesystemType(path)
	if err != nil {
		return fmt.Errorf("identify Darwin ACL filesystem: %w", err)
	}
	if filesystem != "apfs" {
		return fmt.Errorf("filesystem type %q is not approved for Darwin ACL validation", filesystem)
	}
	data, err := darwinExtendedSecurity(path)
	if err != nil {
		return fmt.Errorf("query Darwin extended security: %w", err)
	}
	if len(data) == 0 {
		return nil
	}
	return validateDarwinACL(data, profile)
}

func darwinExtendedSecurity(path string) ([]byte, error) {
	pathBytes, err := syscall.BytePtrFromString(path)
	if err != nil {
		return nil, err
	}
	attributes := darwinAttributeList{
		BitmapCount: darwinAttributeBitmapCount,
		Common:      darwinExtendedSecurityAttr,
	}
	buffer := make([]byte, darwinExtendedSecurityMaxBytes)
	_, _, errno := syscall.Syscall6(
		syscall.SYS_GETATTRLIST,
		// #nosec G103 -- syscall requires pointers to the NUL-terminated path and fixed native buffers.
		uintptr(unsafe.Pointer(pathBytes)),
		// #nosec G103 -- audited fixed-layout getattrlist input.
		uintptr(unsafe.Pointer(&attributes)),
		// #nosec G103 -- audited bounded output buffer.
		uintptr(unsafe.Pointer(&buffer[0])),
		uintptr(len(buffer)),
		darwinGetattrNoFollowAny,
		0,
	)
	if errno != 0 {
		return nil, errno
	}
	if len(buffer) < 4 {
		return nil, fmt.Errorf("darwin attribute result is truncated")
	}
	// #nosec G103 -- buffer has been checked to contain the native uint32 length field.
	reported := uint64(*(*uint32)(unsafe.Pointer(&buffer[0])))
	if reported > uint64(len(buffer)) || reported < 12 {
		return nil, fmt.Errorf("darwin attribute result reports invalid length %d", reported)
	}
	return extractDarwinAttributeReference(buffer[:reported])
}

func darwinFilesystemType(path string) (string, error) {
	var stat syscall.Statfs_t
	if err := syscall.Statfs(path, &stat); err != nil {
		return "", err
	}
	bytes := make([]byte, 0, len(stat.Fstypename))
	for _, value := range stat.Fstypename {
		if value == 0 {
			break
		}
		// #nosec G115 -- Fstypename is a signed-byte C string and preserves the low byte.
		bytes = append(bytes, byte(value))
	}
	if len(bytes) == 0 {
		return "", fmt.Errorf("filesystem type is empty")
	}
	return string(bytes), nil
}
