package platform

import (
	"encoding/binary"
	"errors"
	"io/fs"
	"strings"
	"testing"
)

func TestValidatePOSIXAccessACLAcceptsNonWritableIntegrityEntries(t *testing.T) {
	acl := posixACLBytes(
		posixACLTestEntry{tag: posixACLUserObject, permissions: 0o7},
		posixACLTestEntry{tag: posixACLNamedUser, permissions: 0o4, id: 2000},
		posixACLTestEntry{tag: posixACLGroupObject, permissions: 0o4},
		posixACLTestEntry{tag: posixACLNamedGroup, permissions: 0o4, id: 3000},
		posixACLTestEntry{tag: posixACLMask, permissions: 0o4},
		posixACLTestEntry{tag: posixACLOther, permissions: 0},
	)

	if err := validatePOSIXAccessACL(acl, 0o4, aclIntegrityOnly); err != nil {
		t.Fatalf("validatePOSIXAccessACL(): %v", err)
	}
}

func TestValidatePOSIXAccessACLRejectsEffectiveNonOwnerPermissions(t *testing.T) {
	tests := map[string]struct {
		profile aclProfile
		entries []posixACLTestEntry
	}{
		"integrity named user write": {
			profile: aclIntegrityOnly,
			entries: posixExtendedACL(posixACLTestEntry{tag: posixACLNamedUser, permissions: 0o6, id: 2000}, 0o6),
		},
		"integrity group object write": {
			profile: aclIntegrityOnly,
			entries: posixExtendedACL(posixACLTestEntry{tag: posixACLNamedUser, permissions: 0o4, id: 2000}, 0o6, posixACLTestEntry{tag: posixACLGroupObject, permissions: 0o6}),
		},
		"owner private named user read": {
			profile: aclOwnerPrivate,
			entries: posixExtendedACL(posixACLTestEntry{tag: posixACLNamedUser, permissions: 0o4, id: 2000}, 0o4),
		},
		"owner private group read": {
			profile: aclOwnerPrivate,
			entries: posixExtendedACL(posixACLTestEntry{tag: posixACLNamedGroup, permissions: 0o4, id: 3000}, 0o4),
		},
		"owner private other read": {
			profile: aclOwnerPrivate,
			entries: posixExtendedACL(posixACLTestEntry{tag: posixACLNamedUser, permissions: 0, id: 2000}, 0, posixACLTestEntry{tag: posixACLOther, permissions: 0o4}),
		},
	}

	for name, test := range tests {
		t.Run(name, func(t *testing.T) {
			data := posixACLBytes(test.entries...)
			mask := uint16(0)
			for _, entry := range test.entries {
				if entry.tag == posixACLMask {
					mask = entry.permissions
				}
			}
			if err := validatePOSIXAccessACL(data, mask, test.profile); err == nil {
				t.Fatal("validatePOSIXAccessACL() error = nil")
			}
		})
	}
}

func TestValidatePOSIXAccessACLRequiresMaskToMatchMode(t *testing.T) {
	acl := posixACLBytes(posixExtendedACL(
		posixACLTestEntry{tag: posixACLNamedUser, permissions: 0o4, id: 2000},
		0o4,
	)...)

	err := validatePOSIXAccessACL(acl, 0o6, aclIntegrityOnly)
	if err == nil || !strings.Contains(err.Error(), "group mode") {
		t.Fatalf("validatePOSIXAccessACL() error = %v", err)
	}
}

func TestValidatePOSIXAccessACLRejectsMalformedInput(t *testing.T) {
	valid := posixACLBytes(posixExtendedACL(
		posixACLTestEntry{tag: posixACLNamedUser, permissions: 0o4, id: 2000},
		0o4,
	)...)
	tests := map[string][]byte{
		"empty":                 nil,
		"short header":          {2, 0, 0},
		"wrong version":         append([]byte{3, 0, 0, 0}, valid[4:]...),
		"truncated short entry": append([]byte(nil), valid[:len(valid)-1]...),
		"unknown tag":           posixACLBytes(posixACLTestEntry{tag: 0x40, permissions: 0}),
		"invalid permissions":   posixACLBytes(posixACLTestEntry{tag: posixACLUserObject, permissions: 0o10}),
		"duplicate named id": posixACLBytes(posixExtendedACL(
			posixACLTestEntry{tag: posixACLNamedUser, permissions: 0o4, id: 2000},
			0o4,
			posixACLTestEntry{tag: posixACLNamedUser, permissions: 0o4, id: 2000},
		)...),
		"missing required entry": posixACLBytes(
			posixACLTestEntry{tag: posixACLUserObject, permissions: 0o7},
			posixACLTestEntry{tag: posixACLGroupObject, permissions: 0o4},
		),
	}

	for name, data := range tests {
		t.Run(name, func(t *testing.T) {
			if err := validatePOSIXAccessACL(data, 0o4, aclIntegrityOnly); err == nil {
				t.Fatal("validatePOSIXAccessACL() error = nil")
			}
		})
	}
}

func TestPOSIXACLValidatorAllowsDACFallbackOnlyOnApprovedFilesystems(t *testing.T) {
	tests := map[string]struct {
		filesystemType int64
		runtime        bool
		wantError      bool
	}{
		"ext4":             {filesystemType: linuxExtFilesystemMagic},
		"xfs":              {filesystemType: linuxXFSFilesystemMagic},
		"tmpfs runtime":    {filesystemType: linuxTmpfsFilesystemMagic, runtime: true},
		"tmpfs persistent": {filesystemType: linuxTmpfsFilesystemMagic, wantError: true},
		"unknown":          {filesystemType: 0x12345678, wantError: true},
		"cifs":             {filesystemType: linuxCIFSFilesystemMagic, wantError: true},
	}

	for name, test := range tests {
		t.Run(name, func(t *testing.T) {
			system := fakePOSIXACLSystem{
				xattrs:          map[string]fakeXattrResult{},
				mode:            fs.ModeDir | 0o700,
				filesystemMagic: test.filesystemType,
			}
			validator := posixACLValidator{system: system}
			err := validator.validateACL("/safe/app", aclOwnerPrivate, test.runtime)
			if (err != nil) != test.wantError {
				t.Fatalf("validateACL() error = %v, wantError %v", err, test.wantError)
			}
		})
	}
}

func TestPOSIXACLValidatorRejectsDefaultACLAndQueryFailures(t *testing.T) {
	validDefault := posixACLBytes(
		posixACLTestEntry{tag: posixACLUserObject, permissions: 0o7},
		posixACLTestEntry{tag: posixACLGroupObject, permissions: 0},
		posixACLTestEntry{tag: posixACLOther, permissions: 0},
	)
	tests := map[string]map[string]fakeXattrResult{
		"default ACL": {
			posixACLAccessXattr:  {err: errACLNoData},
			posixACLDefaultXattr: {data: validDefault},
		},
		"access query failure": {
			posixACLAccessXattr:  {err: errors.New("sentinel access query failure")},
			posixACLDefaultXattr: {err: errACLNoData},
		},
		"default query failure": {
			posixACLAccessXattr:  {err: errACLNoData},
			posixACLDefaultXattr: {err: errors.New("sentinel default query failure")},
		},
	}

	for name, xattrs := range tests {
		t.Run(name, func(t *testing.T) {
			validator := posixACLValidator{system: fakePOSIXACLSystem{
				xattrs:          xattrs,
				mode:            fs.ModeDir | 0o700,
				filesystemMagic: linuxExtFilesystemMagic,
			}}
			if err := validator.validateACL("/safe/app", aclOwnerPrivate, false); err == nil {
				t.Fatal("validateACL() error = nil")
			}
		})
	}
}

func TestPOSIXACLValidatorUsesEffectiveMaskAndMode(t *testing.T) {
	access := posixACLBytes(posixExtendedACL(
		posixACLTestEntry{tag: posixACLNamedUser, permissions: 0o4, id: 2000},
		0o4,
	)...)
	validator := posixACLValidator{system: fakePOSIXACLSystem{
		xattrs: map[string]fakeXattrResult{
			posixACLAccessXattr:  {data: access},
			posixACLDefaultXattr: {err: errACLNoData},
		},
		mode:            fs.ModeDir | 0o740,
		filesystemMagic: linuxExtFilesystemMagic,
	}}

	if err := validator.validateACL("/safe", aclIntegrityOnly, false); err != nil {
		t.Fatalf("validateACL(): %v", err)
	}
}

type posixACLTestEntry struct {
	tag         uint16
	permissions uint16
	id          uint32
}

func posixExtendedACL(named posixACLTestEntry, mask uint16, replacements ...posixACLTestEntry) []posixACLTestEntry {
	entries := []posixACLTestEntry{
		{tag: posixACLUserObject, permissions: 0o7},
		named,
		{tag: posixACLGroupObject, permissions: 0o4},
		{tag: posixACLMask, permissions: mask},
		{tag: posixACLOther, permissions: 0},
	}
	for _, replacement := range replacements {
		replaced := false
		for index := range entries {
			if entries[index].tag == replacement.tag && replacement.tag != posixACLNamedUser && replacement.tag != posixACLNamedGroup {
				entries[index] = replacement
				replaced = true
				break
			}
		}
		if !replaced {
			entries = append(entries, replacement)
		}
	}
	return entries
}

func posixACLBytes(entries ...posixACLTestEntry) []byte {
	data := make([]byte, 4)
	binary.LittleEndian.PutUint32(data, posixACLVersion)
	for _, entry := range entries {
		encoded := make([]byte, 4)
		binary.LittleEndian.PutUint16(encoded[0:2], entry.tag)
		binary.LittleEndian.PutUint16(encoded[2:4], entry.permissions)
		data = append(data, encoded...)
		if entry.tag == posixACLNamedUser || entry.tag == posixACLNamedGroup {
			id := make([]byte, 4)
			binary.LittleEndian.PutUint32(id, entry.id)
			data = append(data, id...)
		}
	}
	return data
}

type fakeXattrResult struct {
	data []byte
	err  error
}

type fakePOSIXACLSystem struct {
	xattrs          map[string]fakeXattrResult
	mode            fs.FileMode
	filesystemMagic int64
	statError       error
}

func (f fakePOSIXACLSystem) getxattr(_ string, name string) ([]byte, error) {
	result, ok := f.xattrs[name]
	if !ok {
		return nil, errACLNoData
	}
	return append([]byte(nil), result.data...), result.err
}

func (f fakePOSIXACLSystem) lstatMode(string) (fs.FileMode, error) {
	return f.mode, f.statError
}

func (f fakePOSIXACLSystem) filesystemType(string) (int64, error) {
	return f.filesystemMagic, f.statError
}
