// Package migration owns AMSFTP's compiled, forward-only SQLite migrations.
package migration

import (
	"fmt"
	"math"
)

const (
	SchemaHead           uint64 = 4
	maxMigrationVersion         = uint64(math.MaxInt64)
	maxMigrationWalBytes        = uint64(8*1024*1024*1024 + 64*1024*1024)
)

// Migration is immutable compatibility data compiled into the daemon.
type Migration struct {
	Version              uint64
	Name                 string
	Statements           []string
	MaxMigrationWalBytes uint64
}

// ValidateSet enforces a continuous forward-only history and the per-version
// WAL declaration required by ADR-0008.
func ValidateSet(migrations []Migration) error {
	if len(migrations) == 0 {
		return fmt.Errorf("validate migration set: empty set")
	}
	for index, migration := range migrations {
		wantVersion := uint64(index + 1)
		if migration.Version != wantVersion {
			return fmt.Errorf("validate migration set: migration index %d has version %d, want %d", index, migration.Version, wantVersion)
		}
		if _, err := Checksum(migration); err != nil {
			return fmt.Errorf("validate migration set: version %d: %w", migration.Version, err)
		}
		if migration.Version == 1 {
			if migration.MaxMigrationWalBytes != 0 {
				return fmt.Errorf("validate migration set: version 1 WAL budget = %d, want 0", migration.MaxMigrationWalBytes)
			}
			if err := validateVersion1(migration); err != nil {
				return err
			}
		} else if migration.MaxMigrationWalBytes == 0 || migration.MaxMigrationWalBytes > maxMigrationWalBytes {
			return fmt.Errorf("validate migration set: version %d WAL budget %d is outside 1..%d", migration.Version, migration.MaxMigrationWalBytes, maxMigrationWalBytes)
		}
		for statementIndex, statement := range migration.Statements {
			if _, err := AdmitStatement(migration.Version, statementIndex, statement); err != nil {
				return fmt.Errorf("validate migration set: version %d statement %d: %w", migration.Version, statementIndex, err)
			}
		}
	}
	return nil
}
