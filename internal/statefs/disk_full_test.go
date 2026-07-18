package statefs

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	moderncsqlite "modernc.org/sqlite"
	"modernc.org/sqlite/lib"
)

const nativeDiskFullRootEnvironment = "AMSFTP_NATIVE_DISK_FULL_ROOT"

func TestNativeXFSDiskFullRollsBackWithoutFalseCommit(t *testing.T) {
	base := os.Getenv(nativeDiskFullRootEnvironment)
	if base == "" {
		t.Skip("native disk-full fixture is not configured")
	}
	filesystem, err := ValidateRoot(base)
	if err != nil {
		t.Fatalf("validate native disk-full root: %v", err)
	}
	if filesystem != FilesystemXFS {
		t.Fatalf("native disk-full filesystem = %s, want xfs", filesystem)
	}
	root := filepath.Join(base, "disk-full-state")
	if err := os.Mkdir(root, 0o700); err != nil { //nolint:gosec // owner-private native fixture root
		t.Fatalf("create disk-full state root: %v", err)
	}
	t.Cleanup(func() {
		// #nosec G703 -- root is an exact direct child of the validated native XFS fixture.
		_ = os.RemoveAll(root)
	})
	path := filepath.Join(root, "amsftp.db")
	database, _, err := Initialize(context.Background(), InitializeConfig{
		Root: root, DatabasePath: path,
		Random: strings.NewReader(strings.Repeat("f", probeRandomBytes+16)), Now: time.Unix(2_400, 0),
	})
	if err != nil {
		t.Fatalf("initialize disk-full fixture: %v", err)
	}
	connection, err := database.Conn(context.Background())
	if err != nil {
		t.Fatalf("reserve disk-full connection: %v", err)
	}
	if _, err := connection.ExecContext(context.Background(), "PRAGMA wal_checkpoint(TRUNCATE)"); err != nil {
		t.Fatalf("truncate setup WAL: %v", err)
	}
	if _, err := connection.ExecContext(context.Background(), "BEGIN IMMEDIATE"); err != nil {
		t.Fatalf("begin disk-full transaction: %v", err)
	}

	fillerPath := filepath.Join(base, "disk-full.filler")
	filler, err := os.OpenFile(fillerPath, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600) //nolint:gosec // exact native fixture path
	if err != nil {
		t.Fatalf("create disk-full filler: %v", err)
	}
	t.Cleanup(func() {
		// #nosec G703 -- fillerPath is an exact direct child of the validated native XFS fixture.
		_ = os.Remove(fillerPath)
	})
	block := make([]byte, 1<<20)
	var fullErr error
	for range 1_024 {
		written, writeErr := filler.Write(block)
		if writeErr != nil {
			fullErr = writeErr
			break
		}
		if written != len(block) {
			continue
		}
	}
	if syncErr := filler.Sync(); fullErr == nil && syncErr != nil {
		fullErr = syncErr
	}
	if closeErr := filler.Close(); fullErr == nil && closeErr != nil {
		fullErr = closeErr
	}
	if !errors.Is(fullErr, syscall.ENOSPC) {
		t.Fatalf("fill native XFS error = %v, want ENOSPC", fullErr)
	}

	_, statementErr := connection.ExecContext(context.Background(), `INSERT INTO operation_plans(plan_id, request_id, kind, source_json, destination_json, route, verification, conflict_policy, risk_class, frozen_at_unix) VALUES('disk-full-plan', 'disk-full-request', 'copy', printf('%.*c', 4194304, 'x'), '{}', 'local', 'metadata', 'skip', 'normal', 2400)`)
	commitErr := error(nil)
	if statementErr == nil {
		_, commitErr = connection.ExecContext(context.Background(), "COMMIT")
	}
	diskErr := statementErr
	if diskErr == nil {
		diskErr = commitErr
	}
	var sqliteError *moderncsqlite.Error
	if !errors.As(diskErr, &sqliteError) || sqliteError.Code()&0xff != sqlite3.SQLITE_FULL {
		t.Fatalf("disk-full transaction error = %v, want SQLITE_FULL", diskErr)
	}
	_, _ = connection.ExecContext(context.Background(), "ROLLBACK")
	// #nosec G703 -- fillerPath is an exact direct child of the validated native XFS fixture.
	if err := os.Remove(fillerPath); err != nil {
		t.Fatalf("remove disk-full filler: %v", err)
	}
	if err := syncDirectory(base); err != nil {
		t.Fatalf("persist filler removal: %v", err)
	}
	if err := connection.Close(); err != nil {
		t.Fatalf("close disk-full connection: %v", err)
	}
	if err := database.Close(); err != nil {
		t.Fatalf("close disk-full database: %v", err)
	}

	recovered, report, err := Initialize(context.Background(), InitializeConfig{
		Root: root, DatabasePath: path,
		Random: strings.NewReader(strings.Repeat("g", probeRandomBytes)), Now: time.Unix(2_401, 0),
	})
	if err != nil {
		t.Fatalf("recover after native disk full: %v", err)
	}
	defer recovered.Close()
	if report.Bootstrapped || report.SchemaHead != 4 {
		t.Fatalf("disk-full recovery report = %#v", report)
	}
	var count int
	if err := recovered.QueryRowContext(context.Background(), "SELECT count(*) FROM operation_plans WHERE plan_id='disk-full-plan'").Scan(&count); err != nil {
		t.Fatalf("read disk-full result: %v", err)
	}
	if count != 0 {
		t.Fatalf("disk-full row count = %d, want 0", count)
	}
}
