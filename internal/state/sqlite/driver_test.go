package sqlite

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"errors"
	"fmt"
	"io"
	"net/url"
	"os"
	"path/filepath"
	"testing"

	moderncsqlite "modernc.org/sqlite"
)

type backupConnection interface {
	NewBackup(string) (*moderncsqlite.Backup, error)
}

func TestNativeOpenTransactionWALAndOnlineBackup(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	dir := t.TempDir()
	sourcePath := filepath.Join(dir, "source.sqlite3")
	backupPath := filepath.Join(dir, "backup.sqlite3")
	sourceDB, err := sql.Open(driverName, fileURI(sourcePath))
	if err != nil {
		t.Fatalf("open source database: %v", err)
	}
	t.Cleanup(func() {
		if closeErr := sourceDB.Close(); closeErr != nil {
			t.Errorf("close source database: %v", closeErr)
		}
	})
	sourceDB.SetMaxOpenConns(1)
	var sqliteVersion string
	if err := sourceDB.QueryRowContext(ctx, "SELECT sqlite_version()").Scan(&sqliteVersion); err != nil {
		t.Fatalf("read SQLite version: %v", err)
	}
	if sqliteVersion != "3.53.2" {
		t.Fatalf("SQLite version = %q, want 3.53.2", sqliteVersion)
	}

	var journalMode string
	if err := sourceDB.QueryRowContext(ctx, "PRAGMA journal_mode=WAL").Scan(&journalMode); err != nil {
		t.Fatalf("enable WAL: %v", err)
	}
	if journalMode != "wal" {
		t.Fatalf("journal mode = %q, want wal", journalMode)
	}
	if _, err := sourceDB.ExecContext(ctx, "PRAGMA wal_autocheckpoint=0"); err != nil {
		t.Fatalf("disable automatic checkpoint for smoke: %v", err)
	}
	if _, err := sourceDB.ExecContext(ctx, "CREATE TABLE intake_marker(value TEXT NOT NULL)"); err != nil {
		t.Fatalf("create intake marker table: %v", err)
	}

	rollbackTx, err := sourceDB.BeginTx(ctx, nil)
	if err != nil {
		t.Fatalf("begin rollback transaction: %v", err)
	}
	if _, err := rollbackTx.ExecContext(ctx, "INSERT INTO intake_marker(value) VALUES(?)", "rolled-back"); err != nil {
		_ = rollbackTx.Rollback()
		t.Fatalf("insert rollback marker: %v", err)
	}
	if err := rollbackTx.Rollback(); err != nil {
		t.Fatalf("rollback source transaction: %v", err)
	}
	var rowCount int
	if err := sourceDB.QueryRowContext(ctx, "SELECT count(*) FROM intake_marker").Scan(&rowCount); err != nil {
		t.Fatalf("count rows after rollback: %v", err)
	}
	if rowCount != 0 {
		t.Fatalf("rows after rollback = %d, want 0", rowCount)
	}

	tx, err := sourceDB.BeginTx(ctx, nil)
	if err != nil {
		t.Fatalf("begin source transaction: %v", err)
	}
	if _, err := tx.ExecContext(ctx, "INSERT INTO intake_marker(value) VALUES(?)", "durable-intake"); err != nil {
		_ = tx.Rollback()
		t.Fatalf("insert source marker: %v", err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatalf("commit source transaction: %v", err)
	}

	walInfo, err := os.Stat(sourcePath + "-wal")
	if err != nil {
		t.Fatalf("stat source WAL: %v", err)
	}
	if walInfo.Size() <= 32 {
		t.Fatalf("source WAL size = %d, want header plus at least one frame", walInfo.Size())
	}

	sourceConn, err := sourceDB.Conn(ctx)
	if err != nil {
		t.Fatalf("reserve source connection: %v", err)
	}
	defer func() {
		if closeErr := sourceConn.Close(); closeErr != nil {
			t.Errorf("close reserved source connection: %v", closeErr)
		}
	}()

	backupURI := fileURI(
		backupPath,
		"checkpoint_fullfsync(1)",
		"fullfsync(1)",
		"synchronous(FULL)",
	)
	var backup *moderncsqlite.Backup
	if err := sourceConn.Raw(func(raw any) error {
		conn, ok := raw.(backupConnection)
		if !ok {
			return fmt.Errorf("modernc connection does not implement NewBackup")
		}
		var backupErr error
		backup, backupErr = conn.NewBackup(backupURI)
		return backupErr
	}); err != nil {
		t.Fatalf("create online backup: %v", err)
	}

	more, err := backup.Step(-1)
	if err != nil {
		_ = backup.Finish()
		t.Fatalf("copy online backup: %v", err)
	}
	if more {
		_ = backup.Finish()
		t.Fatal("backup reports pages remaining after Step(-1)")
	}
	destinationConn, err := backup.Commit()
	if err != nil {
		t.Fatalf("commit online backup: %v", err)
	}
	defer func() {
		if closeErr := destinationConn.Close(); closeErr != nil {
			t.Errorf("close backup destination connection: %v", closeErr)
		}
	}()

	for _, pragma := range []string{"checkpoint_fullfsync", "fullfsync"} {
		if got := queryDriverInt(t, ctx, destinationConn, "PRAGMA "+pragma); got != 1 {
			t.Fatalf("backup destination %s = %d, want 1", pragma, got)
		}
	}
	if got := queryDriverInt(t, ctx, destinationConn, "PRAGMA synchronous"); got != 2 {
		t.Fatalf("backup destination synchronous = %d, want 2 (FULL)", got)
	}

	backupDB, err := sql.Open(driverName, fileURI(backupPath))
	if err != nil {
		t.Fatalf("open completed backup: %v", err)
	}
	defer func() {
		if closeErr := backupDB.Close(); closeErr != nil {
			t.Errorf("close completed backup: %v", closeErr)
		}
	}()
	var marker string
	if err := backupDB.QueryRowContext(ctx, "SELECT value FROM intake_marker").Scan(&marker); err != nil {
		t.Fatalf("read marker from backup: %v", err)
	}
	if marker != "durable-intake" {
		t.Fatalf("backup marker = %q, want durable-intake", marker)
	}
}

func fileURI(path string, pragmas ...string) string {
	uri := &url.URL{Scheme: "file", Path: path}
	query := uri.Query()
	for _, pragma := range pragmas {
		query.Add("_pragma", pragma)
	}
	uri.RawQuery = query.Encode()
	return uri.String()
}

func queryDriverInt(t *testing.T, ctx context.Context, conn driver.Conn, query string) int64 {
	t.Helper()

	queryer, ok := conn.(driver.QueryerContext)
	if !ok {
		t.Fatalf("backup destination connection does not implement driver.QueryerContext")
	}
	rows, err := queryer.QueryContext(ctx, query, nil)
	if err != nil {
		t.Fatalf("query backup destination %q: %v", query, err)
	}
	defer func() {
		if closeErr := rows.Close(); closeErr != nil {
			t.Errorf("close rows for %q: %v", query, closeErr)
		}
	}()

	values := make([]driver.Value, 1)
	if err := rows.Next(values); err != nil {
		t.Fatalf("read row for %q: %v", query, err)
	}
	if err := rows.Next(values); !errors.Is(err, io.EOF) {
		t.Fatalf("second row for %q = %v, want EOF", query, err)
	}
	value, ok := values[0].(int64)
	if !ok {
		t.Fatalf("value for %q has type %T, want int64", query, values[0])
	}
	return value
}
