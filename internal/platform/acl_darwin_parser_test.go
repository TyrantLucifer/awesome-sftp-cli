package platform

import (
	"encoding/binary"
	"strings"
	"testing"
)

func TestValidateDarwinACLAcceptsNoACLAndDenyDelete(t *testing.T) {
	for name, data := range map[string][]byte{
		"no ACL": darwinFilesecBytes(darwinACLNoEntries),
		"everyone deny delete": darwinFilesecBytes(1, darwinACLTestACE{
			flags:  darwinACEDeny,
			rights: darwinRightDelete,
		}),
	} {
		t.Run(name, func(t *testing.T) {
			for _, profile := range []aclProfile{aclIntegrityOnly, aclOwnerPrivate} {
				if err := validateDarwinACL(data, profile); err != nil {
					t.Fatalf("validateDarwinACL(profile %d): %v", profile, err)
				}
			}
		})
	}
}

func TestValidateDarwinACLAppliesProfilesToAllowEntries(t *testing.T) {
	allowRead := darwinFilesecBytes(1, darwinACLTestACE{
		flags:  darwinACEPermit,
		rights: darwinRightReadData | darwinRightReadAttributes,
	})
	if err := validateDarwinACL(allowRead, aclIntegrityOnly); err != nil {
		t.Fatalf("integrity allow-read: %v", err)
	}
	if err := validateDarwinACL(allowRead, aclOwnerPrivate); err == nil {
		t.Fatal("owner-private accepted allow-read")
	}

	for name, rights := range map[string]uint32{
		"write":          darwinRightWriteData,
		"append":         darwinRightAppendData,
		"delete":         darwinRightDelete,
		"delete child":   darwinRightDeleteChild,
		"write attrs":    darwinRightWriteAttributes,
		"write xattrs":   darwinRightWriteExtAttributes,
		"write security": darwinRightWriteSecurity,
		"chown":          darwinRightTakeOwnership,
		"generic write":  darwinRightGenericWrite,
	} {
		t.Run(name, func(t *testing.T) {
			data := darwinFilesecBytes(1, darwinACLTestACE{flags: darwinACEPermit, rights: rights})
			if err := validateDarwinACL(data, aclIntegrityOnly); err == nil {
				t.Fatal("integrity-only accepted mutating allow")
			}
		})
	}

	inheritedRead := darwinFilesecBytes(1, darwinACLTestACE{
		flags:  darwinACEPermit | darwinACEInherited,
		rights: darwinRightReadData,
	})
	if err := validateDarwinACL(inheritedRead, aclOwnerPrivate); err == nil {
		t.Fatal("owner-private accepted inherited allow-read")
	}
}

func TestValidateDarwinACLRejectsMalformedOrUnknownData(t *testing.T) {
	valid := darwinFilesecBytes(1, darwinACLTestACE{flags: darwinACEDeny, rights: darwinRightDelete})
	tests := map[string][]byte{
		"empty":            nil,
		"short":            valid[:darwinFilesecHeaderBytes-1],
		"wrong magic":      append([]byte{0, 0, 0, 0}, valid[4:]...),
		"truncated ACE":    valid[:len(valid)-1],
		"extra data":       append(append([]byte(nil), valid...), 0),
		"too many entries": darwinFilesecBytes(darwinACLMaxEntries + 1),
		"unknown ACL flags": func() []byte {
			data := append([]byte(nil), valid...)
			binary.LittleEndian.PutUint32(data[darwinACLFlagsOffset:], 1)
			return data
		}(),
		"unknown ACE kind": darwinFilesecBytes(1, darwinACLTestACE{flags: 3, rights: darwinRightReadData}),
		"unknown ACE flag": darwinFilesecBytes(1, darwinACLTestACE{flags: darwinACEDeny | 1<<15, rights: darwinRightDelete}),
		"unknown right":    darwinFilesecBytes(1, darwinACLTestACE{flags: darwinACEDeny, rights: 1}),
	}

	for name, data := range tests {
		t.Run(name, func(t *testing.T) {
			err := validateDarwinACL(data, aclIntegrityOnly)
			if err == nil {
				t.Fatal("validateDarwinACL() error = nil")
			}
			if strings.TrimSpace(err.Error()) == "" {
				t.Fatal("validateDarwinACL() returned empty error")
			}
		})
	}
}

func TestExtractDarwinAttributeReferenceChecksBounds(t *testing.T) {
	filesec := darwinFilesecBytes(darwinACLNoEntries)
	valid := make([]byte, 12+len(filesec))
	// #nosec G115 -- these fixed test buffers are far smaller than uint32.
	binary.LittleEndian.PutUint32(valid[0:4], uint32(len(valid)))
	binary.LittleEndian.PutUint32(valid[4:8], 8)
	// #nosec G115 -- this fixed test fixture is far smaller than uint32.
	binary.LittleEndian.PutUint32(valid[8:12], uint32(len(filesec)))
	copy(valid[12:], filesec)
	got, err := extractDarwinAttributeReference(valid)
	if err != nil {
		t.Fatalf("extractDarwinAttributeReference(): %v", err)
	}
	if string(got) != string(filesec) {
		t.Fatal("extracted attribute differs")
	}

	for name, mutate := range map[string]func([]byte){
		"reported length too short": func(data []byte) { binary.LittleEndian.PutUint32(data[0:4], 11) },
		// #nosec G115 -- mutated test buffers are far smaller than uint32.
		"reported length too long": func(data []byte) { binary.LittleEndian.PutUint32(data[0:4], uint32(len(data)+1)) },
		"negative offset":          func(data []byte) { binary.LittleEndian.PutUint32(data[4:8], ^uint32(0)) },
		"start past end":           func(data []byte) { binary.LittleEndian.PutUint32(data[4:8], uint32(len(data))) },  // #nosec G115 -- bounded fixture length.
		"length past end":          func(data []byte) { binary.LittleEndian.PutUint32(data[8:12], uint32(len(data))) }, // #nosec G115 -- bounded fixture length.
	} {
		t.Run(name, func(t *testing.T) {
			data := append([]byte(nil), valid...)
			mutate(data)
			if _, err := extractDarwinAttributeReference(data); err == nil {
				t.Fatal("extractDarwinAttributeReference() error = nil")
			}
		})
	}
}

type darwinACLTestACE struct {
	flags  uint32
	rights uint32
}

func darwinFilesecBytes(entryCount uint32, entries ...darwinACLTestACE) []byte {
	data := make([]byte, darwinFilesecHeaderBytes+len(entries)*darwinACEBytes)
	binary.LittleEndian.PutUint32(data[0:4], darwinFilesecMagic)
	binary.LittleEndian.PutUint32(data[darwinACLEntryCountOffset:], entryCount)
	for index, entry := range entries {
		offset := darwinFilesecHeaderBytes + index*darwinACEBytes
		binary.LittleEndian.PutUint32(data[offset+16:offset+20], entry.flags)
		binary.LittleEndian.PutUint32(data[offset+20:offset+24], entry.rights)
	}
	return data
}
