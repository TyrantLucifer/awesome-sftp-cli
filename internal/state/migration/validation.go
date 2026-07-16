package migration

import (
	"bytes"
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
)

// ValidateHead verifies the exact compiled history prefix and whole-main
// schema contract for head on one consistent connection view.
func ValidateHead(ctx context.Context, connection *sql.Conn, migrations []Migration, contracts map[uint64][]byte, head uint64) error {
	if connection == nil || head == 0 || head > uint64(len(migrations)) { //nolint:gosec // len is non-negative
		return fmt.Errorf("validate migration head: invalid connection or head %d", head)
	}
	if err := ValidateSet(migrations); err != nil {
		return fmt.Errorf("validate migration head: %w", err)
	}
	if err := checkMigrationConnection(ctx, connection); err != nil {
		return fmt.Errorf("validate migration head: %w", err)
	}
	rows, err := connection.QueryContext(ctx, "SELECT version, name, sha256 FROM schema_migrations ORDER BY version")
	if err != nil {
		return fmt.Errorf("validate migration head: query history: %w", err)
	}
	seen := uint64(0)
	for rows.Next() {
		seen++
		if seen > head {
			_ = rows.Close()
			return fmt.Errorf("validate migration head: history is newer than head %d", head)
		}
		var version int64
		var name, encodedChecksum string
		if err := rows.Scan(&version, &name, &encodedChecksum); err != nil {
			_ = rows.Close()
			return fmt.Errorf("validate migration head: scan history version %d: %w", seen, err)
		}
		item := migrations[seen-1]
		checksum, err := Checksum(item)
		if err != nil {
			_ = rows.Close()
			return fmt.Errorf("validate migration head: checksum version %d: %w", seen, err)
		}
		if version != int64(seen) || name != item.Name || encodedChecksum != hex.EncodeToString(checksum[:]) { //nolint:gosec // seen is bounded by maxMigrationVersion through head
			_ = rows.Close()
			return fmt.Errorf("validate migration head: history version %d does not match compiled migration", seen)
		}
	}
	if err := errors.Join(rows.Err(), rows.Close()); err != nil {
		return fmt.Errorf("validate migration head: finish history: %w", err)
	}
	if seen != head {
		return fmt.Errorf("validate migration head: history rows %d, want %d", seen, head)
	}
	wantContract, ok := contracts[head]
	if !ok || len(wantContract) == 0 {
		return fmt.Errorf("validate migration head: contract for head %d is missing", head)
	}
	actualContract, err := BuildSchemaContract(ctx, connection, head)
	if err != nil {
		return fmt.Errorf("validate migration head: build head %d contract: %w", head, err)
	}
	if !bytes.Equal(actualContract, wantContract) {
		actualDigest := sha256.Sum256(actualContract)
		wantDigest := sha256.Sum256(wantContract)
		return fmt.Errorf("validate migration head: contract digest %x, want %x", actualDigest, wantDigest)
	}
	return nil
}

// SchemaContractDigests returns the exact digests used by a frozen migration
// attempt, rejecting a missing contract for any compiled head.
func SchemaContractDigests(migrations []Migration, contracts map[uint64][]byte) (map[uint64][sha256.Size]byte, error) {
	if err := ValidateSet(migrations); err != nil {
		return nil, fmt.Errorf("schema contract digests: %w", err)
	}
	result := make(map[uint64][sha256.Size]byte, len(migrations))
	for index := range migrations {
		head := uint64(index + 1)
		contract, ok := contracts[head]
		if !ok || len(contract) == 0 {
			return nil, fmt.Errorf("schema contract digests: contract for head %d is missing", head)
		}
		result[head] = sha256.Sum256(contract)
	}
	return result, nil
}
