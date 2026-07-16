package platform

import (
	"encoding/binary"
	"fmt"
	"math"
)

const (
	darwinFilesecMagic        uint32 = 0x012cc16d
	darwinACLNoEntries        uint32 = math.MaxUint32
	darwinACLMaxEntries       uint32 = 128
	darwinFilesecHeaderBytes         = 44
	darwinACEBytes                   = 24
	darwinACLEntryCountOffset        = 36
	darwinACLFlagsOffset             = 40

	darwinACEKindMask         uint32 = 0x0f
	darwinACEPermit           uint32 = 1
	darwinACEDeny             uint32 = 2
	darwinACEInherited        uint32 = 1 << 4
	darwinACEFileInherit      uint32 = 1 << 5
	darwinACEDirectoryInherit uint32 = 1 << 6
	darwinACELimitInherit     uint32 = 1 << 7
	darwinACEOnlyInherit      uint32 = 1 << 8

	darwinACLDeferInherit uint32 = 1 << 16
	darwinACLNoInherit    uint32 = 1 << 17

	darwinRightReadData           uint32 = 1 << 1
	darwinRightWriteData          uint32 = 1 << 2
	darwinRightExecute            uint32 = 1 << 3
	darwinRightDelete             uint32 = 1 << 4
	darwinRightAppendData         uint32 = 1 << 5
	darwinRightDeleteChild        uint32 = 1 << 6
	darwinRightReadAttributes     uint32 = 1 << 7
	darwinRightWriteAttributes    uint32 = 1 << 8
	darwinRightReadExtAttributes  uint32 = 1 << 9
	darwinRightWriteExtAttributes uint32 = 1 << 10
	darwinRightReadSecurity       uint32 = 1 << 11
	darwinRightWriteSecurity      uint32 = 1 << 12
	darwinRightTakeOwnership      uint32 = 1 << 13
	darwinRightSynchronize        uint32 = 1 << 20
	darwinRightGenericAll         uint32 = 1 << 21
	darwinRightGenericExecute     uint32 = 1 << 22
	darwinRightGenericWrite       uint32 = 1 << 23
	darwinRightGenericRead        uint32 = 1 << 24
	darwinRightLinkTarget         uint32 = 1 << 25
	darwinRightCheckImmutable     uint32 = 1 << 26
)

const (
	darwinKnownACEFlags = darwinACEKindMask | darwinACEInherited | darwinACEFileInherit |
		darwinACEDirectoryInherit | darwinACELimitInherit | darwinACEOnlyInherit
	darwinKnownACLFlags = darwinACLDeferInherit | darwinACLNoInherit
	darwinKnownRights   = darwinRightReadData | darwinRightWriteData | darwinRightExecute |
		darwinRightDelete | darwinRightAppendData | darwinRightDeleteChild |
		darwinRightReadAttributes | darwinRightWriteAttributes |
		darwinRightReadExtAttributes | darwinRightWriteExtAttributes |
		darwinRightReadSecurity | darwinRightWriteSecurity | darwinRightTakeOwnership |
		darwinRightSynchronize | darwinRightGenericAll | darwinRightGenericExecute |
		darwinRightGenericWrite | darwinRightGenericRead | darwinRightLinkTarget |
		darwinRightCheckImmutable
	darwinMutatingRights = darwinRightWriteData | darwinRightDelete | darwinRightAppendData |
		darwinRightDeleteChild | darwinRightWriteAttributes | darwinRightWriteExtAttributes |
		darwinRightWriteSecurity | darwinRightTakeOwnership | darwinRightGenericAll |
		darwinRightGenericWrite
)

func validateDarwinACL(data []byte, profile aclProfile) error {
	if profile != aclIntegrityOnly && profile != aclOwnerPrivate {
		return fmt.Errorf("unknown ACL validation profile %d", profile)
	}
	if len(data) < darwinFilesecHeaderBytes {
		return fmt.Errorf("darwin filesec ACL is truncated")
	}
	if magic := binary.LittleEndian.Uint32(data[:4]); magic != darwinFilesecMagic {
		return fmt.Errorf("unexpected Darwin filesec magic %#x", magic)
	}
	entryCount := binary.LittleEndian.Uint32(data[darwinACLEntryCountOffset:])
	flags := binary.LittleEndian.Uint32(data[darwinACLFlagsOffset:])
	if flags&^darwinKnownACLFlags != 0 {
		return fmt.Errorf("darwin ACL contains unknown flags %#x", flags)
	}

	if entryCount == darwinACLNoEntries {
		if len(data) != darwinFilesecHeaderBytes {
			return fmt.Errorf("darwin no-ACL filesec has trailing data")
		}
		return nil
	}
	if entryCount > darwinACLMaxEntries {
		return fmt.Errorf("darwin ACL has %d entries; maximum is %d", entryCount, darwinACLMaxEntries)
	}
	expected := uint64(darwinFilesecHeaderBytes) + uint64(entryCount)*darwinACEBytes
	if expected != uint64(len(data)) {
		return fmt.Errorf("darwin ACL length %d does not match entry count %d", len(data), entryCount)
	}

	for index := uint32(0); index < entryCount; index++ {
		offset := darwinFilesecHeaderBytes + int(index)*darwinACEBytes
		aceFlags := binary.LittleEndian.Uint32(data[offset+16 : offset+20])
		rights := binary.LittleEndian.Uint32(data[offset+20 : offset+24])
		if aceFlags&^darwinKnownACEFlags != 0 {
			return fmt.Errorf("darwin ACL entry %d contains unknown flags %#x", index, aceFlags)
		}
		kind := aceFlags & darwinACEKindMask
		if kind != darwinACEPermit && kind != darwinACEDeny {
			return fmt.Errorf("darwin ACL entry %d has unsupported kind %d", index, kind)
		}
		if rights&^darwinKnownRights != 0 {
			return fmt.Errorf("darwin ACL entry %d contains unknown rights %#x", index, rights)
		}
		if kind == darwinACEDeny {
			continue
		}
		if profile == aclOwnerPrivate {
			return fmt.Errorf("owner-private profile rejects Darwin allow ACL entries")
		}
		if rights&darwinMutatingRights != 0 {
			return fmt.Errorf("darwin ACL grants mutating rights")
		}
	}
	return nil
}

func extractDarwinAttributeReference(buffer []byte) ([]byte, error) {
	if len(buffer) < 12 {
		return nil, fmt.Errorf("darwin attribute buffer is truncated")
	}
	reportedLength := binary.LittleEndian.Uint32(buffer[0:4])
	if reportedLength < 12 || uint64(reportedLength) > uint64(len(buffer)) {
		return nil, fmt.Errorf("darwin attribute buffer reports invalid length %d", reportedLength)
	}
	// #nosec G115 -- attrreference offsets are signed 32-bit values encoded in the native field.
	offset := int32(binary.LittleEndian.Uint32(buffer[4:8]))
	length := binary.LittleEndian.Uint32(buffer[8:12])
	if offset < 8 {
		return nil, fmt.Errorf("darwin attribute reference has invalid offset %d", offset)
	}
	start := uint64(4) + uint64(offset)
	end := start + uint64(length)
	if start > uint64(reportedLength) || end < start || end > uint64(reportedLength) {
		return nil, fmt.Errorf("darwin attribute reference is outside the returned buffer")
	}
	return buffer[start:end], nil
}
