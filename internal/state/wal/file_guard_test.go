package wal

import (
	"context"
	"database/sql"
	"net/url"
	"os"
	"path/filepath"
	"testing"

	"github.com/TyrantLucifer/awesome-sftp-cli/internal/testkit"
	_ "modernc.org/sqlite"
)

func TestFileGuardObservesNativeStatementAndCommitGrowth(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	root := testkit.PersistentTempDir(t)
	if err := os.Chmod(root, 0o700); err != nil { //nolint:gosec // directory requires owner traversal
		t.Fatalf("set private root: %v", err)
	}
	path := filepath.Join(root, "guard.sqlite3")
	uri := &url.URL{Scheme: "file", Path: path, RawQuery: "_pragma=" + url.QueryEscape("wal_autocheckpoint(1000)")}
	database, err := sql.Open("sqlite", uri.String())
	if err != nil {
		t.Fatalf("open database: %v", err)
	}
	t.Cleanup(func() {
		if err := database.Close(); err != nil {
			t.Errorf("close database: %v", err)
		}
	})
	connection, err := database.Conn(ctx)
	if err != nil {
		t.Fatalf("reserve connection: %v", err)
	}
	t.Cleanup(func() {
		if err := connection.Close(); err != nil {
			t.Errorf("close connection: %v", err)
		}
	})
	if _, err := connection.ExecContext(ctx, "CREATE TABLE guarded(id INTEGER PRIMARY KEY, value TEXT NOT NULL) STRICT"); err != nil {
		t.Fatalf("create table: %v", err)
	}
	if err := os.Chmod(path, 0o600); err != nil {
		t.Fatalf("set private database mode: %v", err)
	}
	var journalMode string
	if err := connection.QueryRowContext(ctx, "PRAGMA journal_mode=WAL").Scan(&journalMode); err != nil || journalMode != "wal" {
		t.Fatalf("enable WAL: mode=%q error=%v", journalMode, err)
	}
	var busy, logFrames, checkpointed int64
	if err := connection.QueryRowContext(ctx, "PRAGMA wal_checkpoint(TRUNCATE)").Scan(&busy, &logFrames, &checkpointed); err != nil || busy != 0 || logFrames != 0 || checkpointed != 0 {
		t.Fatalf("truncate setup WAL: result=(%d,%d,%d) error=%v", busy, logFrames, checkpointed, err)
	}

	guard, err := OpenFileGuard(ctx, connection)
	if err != nil {
		t.Fatalf("OpenFileGuard(): %v", err)
	}
	transaction, err := guard.Begin([]uint64{1 << 20})
	if err != nil {
		t.Fatalf("Begin(): %v", err)
	}
	if _, err := connection.ExecContext(ctx, "BEGIN IMMEDIATE"); err != nil {
		t.Fatalf("begin SQL transaction: %v", err)
	}
	if _, err := connection.ExecContext(ctx, "INSERT INTO guarded(value) VALUES('bounded')"); err != nil {
		t.Fatalf("insert guarded row: %v", err)
	}
	if err := transaction.AfterStatement(0); err != nil {
		t.Fatalf("AfterStatement(): %v", err)
	}
	if err := transaction.BeforeCommit(); err != nil {
		t.Fatalf("BeforeCommit(): %v", err)
	}
	if _, err := connection.ExecContext(ctx, "COMMIT"); err != nil {
		t.Fatalf("commit SQL transaction: %v", err)
	}
	if err := transaction.AfterCommit(); err != nil {
		t.Fatalf("AfterCommit(): %v", err)
	}
	if snapshot := guard.Snapshot(); snapshot.WALBytes == 0 || snapshot.WALBytes > 1<<20 {
		t.Fatalf("guard snapshot = %#v", snapshot)
	}
	passive, err := guard.PassiveCheckpoint(ctx, connection)
	if err != nil {
		t.Fatalf("PassiveCheckpoint(): %v", err)
	}
	if passive.Busy != 0 || passive.LogFrames < passive.CheckpointedFrames {
		t.Fatalf("passive checkpoint = %#v", passive)
	}
	if err := guard.TruncateIdle(ctx, connection); err != nil {
		t.Fatalf("TruncateIdle(): %v", err)
	}
	if snapshot := guard.Snapshot(); snapshot.WALBytes != 0 {
		t.Fatalf("post-truncate snapshot = %#v", snapshot)
	}
}
