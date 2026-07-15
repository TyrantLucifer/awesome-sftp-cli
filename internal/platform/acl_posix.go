package platform

import (
	"encoding/binary"
	"errors"
	"fmt"
	"io/fs"
)

const (
	posixACLVersion      uint32 = 0x0002
	posixACLUserObject   uint16 = 0x0001
	posixACLNamedUser    uint16 = 0x0002
	posixACLGroupObject  uint16 = 0x0004
	posixACLNamedGroup   uint16 = 0x0008
	posixACLMask         uint16 = 0x0010
	posixACLOther        uint16 = 0x0020
	posixACLAccessXattr         = "system.posix_acl_access"
	posixACLDefaultXattr        = "system.posix_acl_default"

	linuxExtFilesystemMagic   int64 = 0x0000ef53
	linuxXFSFilesystemMagic   int64 = 0x58465342
	linuxTmpfsFilesystemMagic int64 = 0x01021994
	linuxCIFSFilesystemMagic  int64 = 0xff534d42
)

var (
	errACLNoData      = errors.New("ACL xattr has no data")
	errACLUnsupported = errors.New("ACL xattr is unsupported")
)

type posixACLEntry struct {
	tag         uint16
	permissions uint16
	id          uint32
}

type posixACLSystem interface {
	getxattr(path string, name string) ([]byte, error)
	lstatMode(path string) (fs.FileMode, error)
	filesystemType(path string) (int64, error)
}

type posixACLValidator struct {
	system posixACLSystem
}

func (v posixACLValidator) validateACL(path string, profile aclProfile, runtimeFilesystemAllowed bool) error {
	access, accessErr := v.system.getxattr(path, posixACLAccessXattr)
	accessAbsent := isAbsentACLError(accessErr)
	if accessErr != nil && !accessAbsent {
		return fmt.Errorf("query POSIX access ACL: %w", accessErr)
	}
	defaults, defaultErr := v.system.getxattr(path, posixACLDefaultXattr)
	if defaultErr != nil && !isAbsentACLError(defaultErr) {
		return fmt.Errorf("query POSIX default ACL: %w", defaultErr)
	}
	if defaultErr == nil {
		if _, err := parsePOSIXACL(defaults); err != nil {
			return fmt.Errorf("parse POSIX default ACL: %w", err)
		}
		return fmt.Errorf("POSIX default ACL is not permitted")
	}

	filesystemType, err := v.system.filesystemType(path)
	if err != nil {
		return fmt.Errorf("identify ACL filesystem: %w", err)
	}
	if !approvedPOSIXACLFilesystem(filesystemType, runtimeFilesystemAllowed) {
		return fmt.Errorf("filesystem type %#x is not approved for POSIX ACL validation", filesystemType)
	}
	if accessAbsent {
		return nil
	}

	mode, err := v.system.lstatMode(path)
	if err != nil {
		return fmt.Errorf("inspect mode for POSIX access ACL: %w", err)
	}
	groupMode := uint16(mode.Perm()&0o070) >> 3
	if err := validatePOSIXAccessACL(access, groupMode, profile); err != nil {
		return fmt.Errorf("validate POSIX access ACL: %w", err)
	}
	return nil
}

func isAbsentACLError(err error) bool {
	return errors.Is(err, errACLNoData) || errors.Is(err, errACLUnsupported)
}

func approvedPOSIXACLFilesystem(filesystemType int64, runtimeFilesystemAllowed bool) bool {
	switch filesystemType {
	case linuxExtFilesystemMagic, linuxXFSFilesystemMagic:
		return true
	case linuxTmpfsFilesystemMagic:
		return runtimeFilesystemAllowed
	default:
		return false
	}
}

func validatePOSIXAccessACL(data []byte, groupMode uint16, profile aclProfile) error {
	entries, err := parsePOSIXACL(data)
	if err != nil {
		return err
	}

	var userObjects int
	var groupObjects int
	var others int
	var masks int
	var groupObjectPermissions uint16
	var maskPermissions uint16
	namedUsers := make(map[uint32]struct{})
	namedGroups := make(map[uint32]struct{})

	for _, entry := range entries {
		switch entry.tag {
		case posixACLUserObject:
			userObjects++
		case posixACLNamedUser:
			if _, exists := namedUsers[entry.id]; exists {
				return fmt.Errorf("duplicate named-user ACL entry")
			}
			namedUsers[entry.id] = struct{}{}
		case posixACLGroupObject:
			groupObjects++
			groupObjectPermissions = entry.permissions
		case posixACLNamedGroup:
			if _, exists := namedGroups[entry.id]; exists {
				return fmt.Errorf("duplicate named-group ACL entry")
			}
			namedGroups[entry.id] = struct{}{}
		case posixACLMask:
			masks++
			maskPermissions = entry.permissions
		case posixACLOther:
			others++
		}
	}

	if userObjects != 1 || groupObjects != 1 || others != 1 || masks > 1 {
		return fmt.Errorf("POSIX ACL does not contain exactly one owner, group, and other entry")
	}
	hasNamedEntries := len(namedUsers) != 0 || len(namedGroups) != 0
	if hasNamedEntries && masks != 1 {
		return fmt.Errorf("extended POSIX ACL does not contain exactly one mask")
	}
	effectiveGroupMode := groupObjectPermissions
	if masks == 1 {
		effectiveGroupMode = maskPermissions
	}
	if effectiveGroupMode != groupMode {
		return fmt.Errorf("ACL mask permissions %03o do not match group mode %03o", effectiveGroupMode, groupMode)
	}

	for _, entry := range entries {
		if entry.tag == posixACLUserObject || entry.tag == posixACLMask {
			continue
		}
		effective := entry.permissions
		if masks == 1 && (entry.tag == posixACLNamedUser || entry.tag == posixACLGroupObject || entry.tag == posixACLNamedGroup) {
			effective &= maskPermissions
		}
		switch profile {
		case aclIntegrityOnly:
			if effective&0o2 != 0 {
				return fmt.Errorf("ACL grants effective write permission outside the owner")
			}
		case aclOwnerPrivate:
			if effective != 0 {
				return fmt.Errorf("ACL grants effective permission outside the owner")
			}
		default:
			return fmt.Errorf("unknown ACL validation profile %d", profile)
		}
	}
	return nil
}

func parsePOSIXACL(data []byte) ([]posixACLEntry, error) {
	if len(data) < 4 {
		return nil, fmt.Errorf("POSIX ACL header is truncated")
	}
	if version := binary.LittleEndian.Uint32(data[:4]); version != posixACLVersion {
		return nil, fmt.Errorf("unsupported POSIX ACL version %d", version)
	}

	var entries []posixACLEntry
	for offset := 4; offset < len(data); {
		if len(data)-offset < 4 {
			return nil, fmt.Errorf("POSIX ACL entry is truncated")
		}
		entry := posixACLEntry{
			tag:         binary.LittleEndian.Uint16(data[offset : offset+2]),
			permissions: binary.LittleEndian.Uint16(data[offset+2 : offset+4]),
		}
		offset += 4
		if entry.permissions&^uint16(0o7) != 0 {
			return nil, fmt.Errorf("POSIX ACL entry has invalid permissions")
		}

		switch entry.tag {
		case posixACLNamedUser, posixACLNamedGroup:
			if len(data)-offset < 4 {
				return nil, fmt.Errorf("POSIX ACL named entry is truncated")
			}
			entry.id = binary.LittleEndian.Uint32(data[offset : offset+4])
			offset += 4
		case posixACLUserObject, posixACLGroupObject, posixACLMask, posixACLOther:
		default:
			return nil, fmt.Errorf("unknown POSIX ACL tag %#x", entry.tag)
		}
		entries = append(entries, entry)
	}
	if len(entries) == 0 {
		return nil, fmt.Errorf("POSIX ACL contains no entries")
	}
	return entries, nil
}
