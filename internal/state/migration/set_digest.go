package migration

import (
	"crypto/sha256"
	"encoding/binary"
	"fmt"
)

const migrationSetMagic = "amsftp-migration-set-v1\x00"

// MigrationSetDigest freezes the complete original+1..target upgrade set. It
// deliberately includes each runtime WAL budget even though checksum-v1 does
// not, so a resumed attempt cannot silently inherit changed resource bounds.
func MigrationSetDigest(originalHead, targetHead uint64, migrations []Migration, contractDigests map[uint64][sha256.Size]byte) ([sha256.Size]byte, error) {
	if originalHead == 0 || originalHead >= targetHead || targetHead > uint64(len(migrations)) { //nolint:gosec // len is non-negative
		return [sha256.Size]byte{}, fmt.Errorf("migration set digest: invalid range %d..%d for %d migrations", originalHead, targetHead, len(migrations))
	}
	if err := ValidateSet(migrations); err != nil {
		return [sha256.Size]byte{}, fmt.Errorf("migration set digest: %w", err)
	}
	hasher := sha256.New()
	_, _ = hasher.Write([]byte(migrationSetMagic))
	writeSetUint64(hasher, originalHead)
	writeSetUint64(hasher, targetHead)
	for version := originalHead + 1; version <= targetHead; version++ {
		migration := migrations[version-1]
		checksum, err := Checksum(migration)
		if err != nil {
			return [sha256.Size]byte{}, fmt.Errorf("migration set digest: version %d checksum: %w", version, err)
		}
		contractDigest, ok := contractDigests[version]
		if !ok || contractDigest == [sha256.Size]byte{} {
			return [sha256.Size]byte{}, fmt.Errorf("migration set digest: version %d contract digest is missing", version)
		}
		writeSetUint64(hasher, version)
		_, _ = hasher.Write(checksum[:])
		_, _ = hasher.Write(contractDigest[:])
		writeSetUint64(hasher, migration.MaxMigrationWalBytes)
	}
	var result [sha256.Size]byte
	copy(result[:], hasher.Sum(nil))
	return result, nil
}

type byteWriter interface {
	Write([]byte) (int, error)
}

func writeSetUint64(writer byteWriter, value uint64) {
	var encoded [8]byte
	binary.BigEndian.PutUint64(encoded[:], value)
	_, _ = writer.Write(encoded[:])
}
