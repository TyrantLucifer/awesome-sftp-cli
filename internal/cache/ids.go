// Package cache defines the daemon-owned, persistence-independent cache domain.
package cache

import (
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"fmt"
	"hash"
)

const (
	contentIDHexLength = sha256.Size * 2
	randomIDHexLength  = 16 * 2

	maxEndpointIDBytes      = 4 << 10
	maxCanonicalPathBytes   = 1 << 20
	maxCanonicalFingerprint = 64 << 10
)

type BlobID string
type EntryID string
type MaterializationID string
type ReferenceID string
type LeaseID string

func ParseBlobID(value string) (BlobID, error) {
	if err := validateLowerHexID("blob", value, contentIDHexLength); err != nil {
		return "", err
	}
	return BlobID(value), nil
}

func ParseEntryID(value string) (EntryID, error) {
	if err := validateLowerHexID("entry", value, contentIDHexLength); err != nil {
		return "", err
	}
	return EntryID(value), nil
}

func ParseMaterializationID(value string) (MaterializationID, error) {
	if err := validateLowerHexID("materialization", value, randomIDHexLength); err != nil {
		return "", err
	}
	return MaterializationID(value), nil
}

func ParseReferenceID(value string) (ReferenceID, error) {
	if err := validateLowerHexID("reference", value, randomIDHexLength); err != nil {
		return "", err
	}
	return ReferenceID(value), nil
}

func ParseLeaseID(value string) (LeaseID, error) {
	if err := validateLowerHexID("lease", value, randomIDHexLength); err != nil {
		return "", err
	}
	return LeaseID(value), nil
}

func BlobIDFromDigest(digest [sha256.Size]byte) BlobID {
	return BlobID(hex.EncodeToString(digest[:]))
}

// DeriveEntryID binds one canonical Location and one canonical fingerprint.
// Length prefixes make the encoding unambiguous without interpreting raw path bytes.
func DeriveEntryID(endpointID string, canonicalRawPath []byte, canonicalFingerprint []byte) (EntryID, error) {
	if endpointID == "" || len(endpointID) > maxEndpointIDBytes {
		return "", fmt.Errorf("derive cache entry ID: endpoint ID length must be in [1,%d]", maxEndpointIDBytes)
	}
	if len(canonicalRawPath) == 0 || len(canonicalRawPath) > maxCanonicalPathBytes {
		return "", fmt.Errorf("derive cache entry ID: canonical path length must be in [1,%d]", maxCanonicalPathBytes)
	}
	if len(canonicalFingerprint) == 0 || len(canonicalFingerprint) > maxCanonicalFingerprint {
		return "", fmt.Errorf("derive cache entry ID: canonical fingerprint length must be in [1,%d]", maxCanonicalFingerprint)
	}

	digest := sha256.New()
	writeHashBytes(digest, []byte("amsftp-cache-entry-v1\x00"))
	writeLengthValue(digest, []byte(endpointID))
	writeLengthValue(digest, canonicalRawPath)
	writeLengthValue(digest, canonicalFingerprint)
	return EntryID(hex.EncodeToString(digest.Sum(nil))), nil
}

func validateLowerHexID(kind string, value string, width int) error {
	if len(value) != width {
		return fmt.Errorf("parse cache %s ID: expected %d lowercase hexadecimal characters", kind, width)
	}
	for index := range value {
		character := value[index]
		if (character < '0' || character > '9') && (character < 'a' || character > 'f') {
			return fmt.Errorf("parse cache %s ID: expected %d lowercase hexadecimal characters", kind, width)
		}
	}
	return nil
}

func writeLengthValue(destination hash.Hash, value []byte) {
	var length [8]byte
	binary.BigEndian.PutUint64(length[:], uint64(len(value)))
	writeHashBytes(destination, length[:])
	writeHashBytes(destination, value)
}

func writeHashBytes(destination hash.Hash, value []byte) {
	_, _ = destination.Write(value)
}
