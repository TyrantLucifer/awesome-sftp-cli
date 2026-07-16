package migration

import (
	"crypto/sha256"
	"encoding/binary"
	"fmt"
	"io"
	"regexp"
	"unicode/utf8"
)

const (
	checksumMagic        = "amsftp-schema-migration-v1\x00"
	maxNameBytes         = 64
	maxStatements        = 1024
	maxStatementBytes    = 1024 * 1024
	maxChecksumInputSize = 8 * 1024 * 1024
)

var migrationNamePattern = regexp.MustCompile(`^[a-z][a-z0-9_]{0,63}$`)

// Checksum returns the checksum-v1 SHA-256 digest.
func Checksum(migration Migration) ([sha256.Size]byte, error) {
	digest := sha256.New()
	if err := WriteChecksumInput(digest, migration); err != nil {
		return [sha256.Size]byte{}, err
	}
	var result [sha256.Size]byte
	copy(result[:], digest.Sum(nil))
	return result, nil
}

// WriteChecksumInput streams the exact checksum-v1 binary representation.
func WriteChecksumInput(writer io.Writer, migration Migration) error {
	if writer == nil {
		return fmt.Errorf("encode migration checksum: nil writer")
	}
	if err := validateChecksumFields(migration); err != nil {
		return err
	}
	counting := &boundedWriter{writer: writer, remaining: maxChecksumInputSize}
	if _, err := io.WriteString(counting, checksumMagic); err != nil {
		return fmt.Errorf("encode migration checksum magic: %w", err)
	}
	if err := binary.Write(counting, binary.BigEndian, migration.Version); err != nil {
		return fmt.Errorf("encode migration version: %w", err)
	}
	// validateChecksumFields bounds the name well below uint32.
	nameLength := uint32(len(migration.Name)) //nolint:gosec
	if err := binary.Write(counting, binary.BigEndian, nameLength); err != nil {
		return fmt.Errorf("encode migration name length: %w", err)
	}
	if _, err := io.WriteString(counting, migration.Name); err != nil {
		return fmt.Errorf("encode migration name: %w", err)
	}
	// validateChecksumFields bounds the count well below uint32.
	statementCount := uint32(len(migration.Statements)) //nolint:gosec
	if err := binary.Write(counting, binary.BigEndian, statementCount); err != nil {
		return fmt.Errorf("encode migration statement count: %w", err)
	}
	for index, statement := range migration.Statements {
		if err := binary.Write(counting, binary.BigEndian, uint64(len(statement))); err != nil {
			return fmt.Errorf("encode migration statement %d length: %w", index, err)
		}
		if _, err := io.WriteString(counting, statement); err != nil {
			return fmt.Errorf("encode migration statement %d: %w", index, err)
		}
	}
	return nil
}

func validateChecksumFields(migration Migration) error {
	if migration.Version == 0 || migration.Version > maxMigrationVersion {
		return fmt.Errorf("encode migration checksum: version %d is outside 1..%d", migration.Version, maxMigrationVersion)
	}
	if len(migration.Name) == 0 || len(migration.Name) > maxNameBytes || !migrationNamePattern.MatchString(migration.Name) {
		return fmt.Errorf("encode migration checksum: invalid name %q", migration.Name)
	}
	if len(migration.Statements) == 0 || len(migration.Statements) > maxStatements {
		return fmt.Errorf("encode migration checksum: statement count %d is outside 1..%d", len(migration.Statements), maxStatements)
	}
	for index, statement := range migration.Statements {
		if len(statement) == 0 || len(statement) > maxStatementBytes {
			return fmt.Errorf("encode migration checksum: statement %d length %d is outside 1..%d", index, len(statement), maxStatementBytes)
		}
		if !utf8.ValidString(statement) {
			return fmt.Errorf("encode migration checksum: statement %d is not valid UTF-8", index)
		}
		if containsNUL(statement) {
			return fmt.Errorf("encode migration checksum: statement %d contains NUL", index)
		}
		if isASCIIWhitespaceOnly(statement) {
			return fmt.Errorf("encode migration checksum: statement %d is blank", index)
		}
	}
	return nil
}

type boundedWriter struct {
	writer    io.Writer
	remaining uint64
}

func (writer *boundedWriter) Write(value []byte) (int, error) {
	if uint64(len(value)) > writer.remaining {
		return 0, fmt.Errorf("checksum input exceeds %d bytes", maxChecksumInputSize)
	}
	written, err := writer.writer.Write(value)
	if written < 0 || written > len(value) {
		return 0, fmt.Errorf("invalid writer count %d", written)
	}
	writer.remaining -= uint64(written)
	return written, err
}

func containsNUL(value string) bool {
	for index := 0; index < len(value); index++ {
		if value[index] == 0 {
			return true
		}
	}
	return false
}

func isASCIIWhitespaceOnly(value string) bool {
	for index := 0; index < len(value); index++ {
		switch value[index] {
		case ' ', '\t', '\r', '\n':
		default:
			return false
		}
	}
	return true
}
