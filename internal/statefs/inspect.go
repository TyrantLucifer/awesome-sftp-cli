package statefs

import (
	"context"
	"errors"
	"fmt"
)

var ErrDatabaseActive = errors.New("database has live sidecars")

// InspectDatabase validates a stopped project database through SQLite's
// immutable read-only mode. A live WAL/SHM state is reported without opening
// the database so inspection cannot create or modify sidecars.
func InspectDatabase(ctx context.Context, path string) (uint64, error) {
	identity, err := PreflightIdentity(path)
	if err != nil {
		return 0, fmt.Errorf("inspect database identity: %w", err)
	}
	if identity.Kind != IdentityProject {
		return 0, fmt.Errorf("inspect database identity: project database is absent")
	}
	if identity.HasSidecars {
		return 0, ErrDatabaseActive
	}
	migrations, contracts, err := resolveCompiledState(nil, nil)
	if err != nil {
		return 0, fmt.Errorf("inspect database compiled state: %w", err)
	}
	head, err := validateImmutableState(ctx, path, migrations, contracts, true)
	if err != nil {
		return 0, fmt.Errorf("inspect immutable database: %w", err)
	}
	return head, nil
}

// AvailableFilesystemBytes reports bytes available to the current user without
// creating a filesystem probe or changing the inspected directory.
func AvailableFilesystemBytes(path string) (uint64, error) {
	return availableFilesystemBytes(path)
}
