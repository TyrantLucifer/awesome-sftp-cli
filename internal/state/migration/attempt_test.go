package migration

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"math"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestAttemptStoreCreatesOneFrozenAttemptAndReusesExactMatch(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	connection := version1Connection(t, ctx)
	digest := sha256.Sum256([]byte("set"))
	request := AttemptRequest{AttemptID: "0123456789abcdef0123456789abcdef", OriginalHead: 1, TargetHead: 2, MigrationSetDigest: digest}
	created, reused, err := PrepareAttempt(ctx, connection, request)
	if err != nil {
		t.Fatalf("PrepareAttempt(): %v", err)
	}
	if reused || created.Status != AttemptPreparing || created.CurrentHead != 1 || created.BackupSHA256 != nil {
		t.Fatalf("created attempt = %#v, reused=%t", created, reused)
	}
	repeated, reused, err := PrepareAttempt(ctx, connection, request)
	if err != nil {
		t.Fatalf("PrepareAttempt(repeat): %v", err)
	}
	if !reused || repeated.AttemptID != created.AttemptID || repeated.OriginalHead != created.OriginalHead || repeated.TargetHead != created.TargetHead || repeated.MigrationSetSHA256 != created.MigrationSetSHA256 || repeated.Status != created.Status {
		t.Fatalf("repeated attempt = %#v, reused=%t; want %#v", repeated, reused, created)
	}
	request.TargetHead = 3
	if _, _, err := PrepareAttempt(ctx, connection, request); err == nil {
		t.Fatal("PrepareAttempt(mismatch) error = nil")
	}
	var count int
	if err := connection.QueryRowContext(ctx, "SELECT count(*) FROM migration_attempts").Scan(&count); err != nil || count != 1 {
		t.Fatalf("attempt count = %d, error=%v", count, err)
	}
}

func TestAttemptBackupCatalogAndRestartDecisions(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	connection := version1Connection(t, ctx)
	digest := sha256.Sum256([]byte("set"))
	request := AttemptRequest{AttemptID: "abcdef0123456789abcdef0123456789", OriginalHead: 1, TargetHead: 2, MigrationSetDigest: digest}
	attempt, _, err := PrepareAttempt(ctx, connection, request)
	if err != nil {
		t.Fatalf("PrepareAttempt(): %v", err)
	}
	if decision := RestartDecision(attempt); decision != RestartHoldExplicitResume {
		t.Fatalf("preparing decision = %q", decision)
	}
	backupDigest := sha256.Sum256([]byte("backup"))
	attempt, err = RecordVerifiedBackup(ctx, connection, request.AttemptID, backupDigest, time.Unix(100, 0))
	if err != nil {
		t.Fatalf("RecordVerifiedBackup(): %v", err)
	}
	if attempt.Status != AttemptReady || attempt.BackupSHA256 == nil || *attempt.BackupSHA256 != hex.EncodeToString(backupDigest[:]) {
		t.Fatalf("ready attempt = %#v", attempt)
	}
	if decision := RestartDecision(attempt); decision != RestartAutoContinue {
		t.Fatalf("ready decision = %q", decision)
	}
	attempt, err = MarkAttemptRunning(ctx, connection, request.AttemptID)
	if err != nil {
		t.Fatalf("MarkAttemptRunning(): %v", err)
	}
	if RestartDecision(attempt) != RestartHoldExplicitResume {
		t.Fatalf("running attempt auto-continued")
	}
	var catalogRows int
	if err := connection.QueryRowContext(ctx, "SELECT count(*) FROM migration_backups WHERE attempt_id=? AND status='verified'", request.AttemptID).Scan(&catalogRows); err != nil || catalogRows != 1 {
		t.Fatalf("verified catalog rows = %d, error=%v", catalogRows, err)
	}
}

func TestRunnerAdvancesAttemptHeadWithMigrationHistory(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	connection := version1Connection(t, ctx)
	digest := sha256.Sum256([]byte("set"))
	request := AttemptRequest{AttemptID: "11111111111111111111111111111111", OriginalHead: 1, TargetHead: 2, MigrationSetDigest: digest}
	if _, _, err := PrepareAttempt(ctx, connection, request); err != nil {
		t.Fatalf("PrepareAttempt(): %v", err)
	}
	backupDigest := sha256.Sum256([]byte("backup"))
	if _, err := RecordVerifiedBackup(ctx, connection, request.AttemptID, backupDigest, time.Unix(100, 0)); err != nil {
		t.Fatalf("RecordVerifiedBackup(): %v", err)
	}
	if _, err := MarkAttemptRunning(ctx, connection, request.AttemptID); err != nil {
		t.Fatalf("MarkAttemptRunning(): %v", err)
	}
	v2 := Migration{Version: 2, Name: "second", Statements: []string{"CREATE TABLE second(id INTEGER PRIMARY KEY) STRICT"}, MaxMigrationWalBytes: 4096}
	if err := (Runner{AttemptID: request.AttemptID}).Apply(ctx, connection, v2, "2026-07-16T00:00:01Z"); err != nil {
		t.Fatalf("apply version 2: %v", err)
	}
	attempt, err := LoadAttempt(ctx, connection)
	if err != nil {
		t.Fatalf("LoadAttempt(): %v", err)
	}
	if attempt.CurrentHead != 2 || attempt.Status != AttemptRunning {
		t.Fatalf("advanced attempt = %#v", attempt)
	}
	var historyRows int
	if err := connection.QueryRowContext(ctx, "SELECT count(*) FROM schema_migrations WHERE version=2").Scan(&historyRows); err != nil || historyRows != 1 {
		t.Fatalf("version 2 history rows = %d, error=%v", historyRows, err)
	}
}

func TestMarkAttemptFailedPreservesPreAndPostBackupRecoveryEvidence(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	preBackup := version1Connection(t, ctx)
	preRequest := AttemptRequest{
		AttemptID: "77777777777777777777777777777777", OriginalHead: 1, TargetHead: 2,
		MigrationSetDigest: sha256.Sum256([]byte("pre-backup-failure")),
	}
	if _, _, err := PrepareAttempt(ctx, preBackup, preRequest); err != nil {
		t.Fatalf("PrepareAttempt(pre-backup): %v", err)
	}
	failed, err := MarkAttemptFailed(ctx, preBackup, preRequest.AttemptID, "backup_publish")
	if err != nil {
		t.Fatalf("MarkAttemptFailed(pre-backup): %v", err)
	}
	if failed.Status != AttemptFailed || failed.ErrorKind == nil || *failed.ErrorKind != "backup_publish" || failed.BackupSHA256 != nil || failed.CurrentHead != failed.OriginalHead {
		t.Fatalf("pre-backup failed attempt = %#v", failed)
	}
	if RestartDecision(failed) != RestartHoldExplicitResume {
		t.Fatal("pre-backup failed attempt auto-continued")
	}

	postBackup := version1Connection(t, ctx)
	postRequest := AttemptRequest{
		AttemptID: "88888888888888888888888888888888", OriginalHead: 1, TargetHead: 2,
		MigrationSetDigest: sha256.Sum256([]byte("post-backup-failure")),
	}
	if _, _, err := PrepareAttempt(ctx, postBackup, postRequest); err != nil {
		t.Fatalf("PrepareAttempt(post-backup): %v", err)
	}
	backupDigest := sha256.Sum256([]byte("verified backup"))
	if _, err := RecordVerifiedBackup(ctx, postBackup, postRequest.AttemptID, backupDigest, time.Unix(600, 0)); err != nil {
		t.Fatalf("RecordVerifiedBackup(post-backup): %v", err)
	}
	if _, err := MarkAttemptRunning(ctx, postBackup, postRequest.AttemptID); err != nil {
		t.Fatalf("MarkAttemptRunning(post-backup): %v", err)
	}
	failed, err = MarkAttemptFailed(ctx, postBackup, postRequest.AttemptID, "statement_error")
	if err != nil {
		t.Fatalf("MarkAttemptFailed(post-backup): %v", err)
	}
	if failed.Status != AttemptFailed || failed.ErrorKind == nil || *failed.ErrorKind != "statement_error" || failed.BackupSHA256 == nil || *failed.BackupSHA256 != hex.EncodeToString(backupDigest[:]) {
		t.Fatalf("post-backup failed attempt = %#v", failed)
	}
	for _, invalid := range []string{"", "UPPER", strings.Repeat("x", 65), "contains space"} {
		if _, err := MarkAttemptFailed(ctx, postBackup, postRequest.AttemptID, invalid); err == nil {
			t.Fatalf("MarkAttemptFailed(error kind %q) error = nil", invalid)
		}
	}
}

func TestAttemptInterruptionAndCompletionAreConservative(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	connection := version1Connection(t, ctx)
	request := AttemptRequest{
		AttemptID: "99999999999999999999999999999999", OriginalHead: 1, TargetHead: 2,
		MigrationSetDigest: sha256.Sum256([]byte("interrupt-complete")),
	}
	if _, _, err := PrepareAttempt(ctx, connection, request); err != nil {
		t.Fatalf("PrepareAttempt(): %v", err)
	}
	backupDigest := sha256.Sum256([]byte("verified backup"))
	if _, err := RecordVerifiedBackup(ctx, connection, request.AttemptID, backupDigest, time.Unix(700, 0)); err != nil {
		t.Fatalf("RecordVerifiedBackup(): %v", err)
	}
	if _, err := MarkAttemptInterrupted(ctx, connection, request.AttemptID, "daemon_restart"); err == nil {
		t.Fatal("MarkAttemptInterrupted(ready) error = nil")
	}
	if _, err := MarkAttemptRunning(ctx, connection, request.AttemptID); err != nil {
		t.Fatalf("MarkAttemptRunning(): %v", err)
	}
	interrupted, err := MarkAttemptInterrupted(ctx, connection, request.AttemptID, "daemon_restart")
	if err != nil {
		t.Fatalf("MarkAttemptInterrupted(): %v", err)
	}
	if interrupted.Status != AttemptInterrupted || interrupted.ErrorKind == nil || *interrupted.ErrorKind != "daemon_restart" || interrupted.BackupSHA256 == nil {
		t.Fatalf("interrupted attempt = %#v", interrupted)
	}
	if RestartDecision(interrupted) != RestartHoldExplicitResume {
		t.Fatal("interrupted attempt auto-continued")
	}
	if err := ClearCompletedAttempt(ctx, connection, request); err == nil {
		t.Fatal("ClearCompletedAttempt(interrupted prefix) error = nil")
	}

	if _, err := RearmAttemptAfterExplicitValidation(ctx, connection, request); err != nil {
		t.Fatalf("RearmAttemptAfterExplicitValidation(): %v", err)
	}
	if _, err := MarkAttemptRunning(ctx, connection, request.AttemptID); err != nil {
		t.Fatalf("MarkAttemptRunning(resumed): %v", err)
	}
	v2 := Migration{Version: 2, Name: "second", Statements: []string{"CREATE TABLE second_completion(id INTEGER PRIMARY KEY) STRICT"}, MaxMigrationWalBytes: 4096}
	if err := (Runner{AttemptID: request.AttemptID}).Apply(ctx, connection, v2, "2026-07-16T00:00:02Z"); err != nil {
		t.Fatalf("apply version 2: %v", err)
	}
	mismatch := request
	mismatch.MigrationSetDigest = sha256.Sum256([]byte("changed completion set"))
	if err := ClearCompletedAttempt(ctx, connection, mismatch); err == nil {
		t.Fatal("ClearCompletedAttempt(mismatched set) error = nil")
	}
	if err := ClearCompletedAttempt(ctx, connection, request); err != nil {
		t.Fatalf("ClearCompletedAttempt(): %v", err)
	}
	if _, err := LoadAttempt(ctx, connection); !errors.Is(err, ErrNoAttempt) {
		t.Fatalf("LoadAttempt(after completion) error = %v", err)
	}
	var catalogRows int
	if err := connection.QueryRowContext(ctx, "SELECT count(*) FROM migration_backups WHERE attempt_id=? AND status='verified'", request.AttemptID).Scan(&catalogRows); err != nil || catalogRows != 1 {
		t.Fatalf("retained catalog rows = %d, error=%v", catalogRows, err)
	}
}

func TestRecordVerifiedBackupUsesMonotonicCatalogTimeAndRejectsOverflow(t *testing.T) {
	t.Parallel()

	t.Run("clock rollback", func(t *testing.T) {
		t.Parallel()
		ctx := context.Background()
		connection := version1Connection(t, ctx)
		if _, err := connection.ExecContext(ctx, "INSERT INTO migration_backups(attempt_id, original_head, target_head, migration_set_sha256, backup_basename, backup_sha256, created_at_unix, status) VALUES('aa000000000000000000000000000000', 1, 2, ?, '.amsftp-backup-v1-aa000000000000000000000000000000.sqlite3', ?, 1000, 'verified')", strings.Repeat("a", 64), strings.Repeat("b", 64)); err != nil {
			t.Fatalf("insert prior catalog row: %v", err)
		}
		request := AttemptRequest{
			AttemptID: "bb000000000000000000000000000000", OriginalHead: 1, TargetHead: 2,
			MigrationSetDigest: sha256.Sum256([]byte("monotonic catalog")),
		}
		if _, _, err := PrepareAttempt(ctx, connection, request); err != nil {
			t.Fatalf("PrepareAttempt(): %v", err)
		}
		if _, err := RecordVerifiedBackup(ctx, connection, request.AttemptID, sha256.Sum256([]byte("backup")), time.Unix(500, 0)); err != nil {
			t.Fatalf("RecordVerifiedBackup(): %v", err)
		}
		var createdAt int64
		if err := connection.QueryRowContext(ctx, "SELECT created_at_unix FROM migration_backups WHERE attempt_id=?", request.AttemptID).Scan(&createdAt); err != nil {
			t.Fatalf("read monotonic catalog time: %v", err)
		}
		if createdAt != 1001 {
			t.Fatalf("created_at_unix = %d, want 1001", createdAt)
		}
	})

	t.Run("overflow", func(t *testing.T) {
		t.Parallel()
		ctx := context.Background()
		connection := version1Connection(t, ctx)
		if _, err := connection.ExecContext(ctx, "INSERT INTO migration_backups(attempt_id, original_head, target_head, migration_set_sha256, backup_basename, backup_sha256, created_at_unix, status) VALUES('cc000000000000000000000000000000', 1, 2, ?, '.amsftp-backup-v1-cc000000000000000000000000000000.sqlite3', ?, ?, 'verified')", strings.Repeat("c", 64), strings.Repeat("d", 64), int64(math.MaxInt64)); err != nil {
			t.Fatalf("insert maximum catalog time: %v", err)
		}
		request := AttemptRequest{
			AttemptID: "dd000000000000000000000000000000", OriginalHead: 1, TargetHead: 2,
			MigrationSetDigest: sha256.Sum256([]byte("catalog overflow")),
		}
		if _, _, err := PrepareAttempt(ctx, connection, request); err != nil {
			t.Fatalf("PrepareAttempt(): %v", err)
		}
		if _, err := RecordVerifiedBackup(ctx, connection, request.AttemptID, sha256.Sum256([]byte("backup")), time.Unix(500, 0)); err == nil {
			t.Fatal("RecordVerifiedBackup(overflow) error = nil")
		}
		attempt, err := LoadAttempt(ctx, connection)
		if err != nil {
			t.Fatalf("LoadAttempt(): %v", err)
		}
		if attempt.Status != AttemptPreparing || attempt.BackupSHA256 != nil {
			t.Fatalf("attempt changed on catalog overflow = %#v", attempt)
		}
	})
}

func version1Connection(t *testing.T, ctx context.Context) *sql.Conn {
	t.Helper()
	database := openTestDatabase(t, filepath.Join(t.TempDir(), "attempt.sqlite3"))
	connection := reserveConnection(t, ctx, database)
	if err := (Runner{}).Apply(ctx, connection, Version1(), "2026-07-16T00:00:00Z"); err != nil {
		t.Fatalf("apply version 1: %v", err)
	}
	return connection
}
