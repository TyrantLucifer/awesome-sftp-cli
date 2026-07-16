package migration

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"strings"
	"testing"
)

func TestVersion3WALViolationRollsBackDetailsTableAndPreservesVersion2Head(t *testing.T) {
	ctx := context.Background()
	connection := version1Connection(t, ctx)
	v2 := Version2()
	for _, statement := range v2.Statements {
		if _, err := connection.ExecContext(ctx, statement); err != nil {
			t.Fatal(err)
		}
	}
	digest, _ := Checksum(v2)
	if _, err := connection.ExecContext(ctx, "INSERT INTO schema_migrations(version,name,sha256,applied_at) VALUES(2,?,?, '2026-07-16T12:00:00Z')", v2.Name, hex.EncodeToString(digest[:])); err != nil {
		t.Fatal(err)
	}
	attemptID := strings.Repeat("7", 32)
	if _, err := connection.ExecContext(ctx, "INSERT INTO migration_attempts(singleton,attempt_id,original_head,current_head,target_head,migration_set_sha256,reserved_backup_basename,backup_sha256,status,error_kind) VALUES(1,?,2,2,3,?,?,?,'running',NULL)", attemptID, strings.Repeat("a", 64), ".amsftp-backup-v1-"+attemptID+".sqlite3", strings.Repeat("b", 64)); err != nil {
		t.Fatal(err)
	}
	monitor := &faultMigrationWALMonitor{afterStatementError: errors.New("Version 3 WAL budget exceeded")}
	if err := (Runner{AttemptID: attemptID, WALMonitor: monitor}).Apply(ctx, connection, Version3(), "2026-07-16T12:00:01Z"); err == nil {
		t.Fatal("Version 3 migration succeeded after WAL violation")
	}
	var tables, history, currentHead int
	if err := connection.QueryRowContext(ctx, "SELECT count(*) FROM sqlite_schema WHERE name='edit_session_details'").Scan(&tables); err != nil {
		t.Fatal(err)
	}
	if err := connection.QueryRowContext(ctx, "SELECT max(version) FROM schema_migrations").Scan(&history); err != nil {
		t.Fatal(err)
	}
	if err := connection.QueryRowContext(ctx, "SELECT current_head FROM migration_attempts WHERE singleton=1").Scan(&currentHead); err != nil {
		t.Fatal(err)
	}
	if tables != 0 || history != 2 || currentHead != 2 {
		t.Fatalf("rollback state = tables:%d history:%d attempt:%d", tables, history, currentHead)
	}
}

func TestVersion3ChecksumBudgetAndWholeSchemaContractAreFrozen(t *testing.T) {
	ctx := context.Background()
	connection := version1Connection(t, ctx)
	for _, item := range []Migration{Version2(), Version3()} {
		for _, statement := range item.Statements {
			if _, err := connection.ExecContext(ctx, statement); err != nil {
				t.Fatalf("apply Version %d statement: %v", item.Version, err)
			}
		}
		digest, err := Checksum(item)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := connection.ExecContext(ctx, "INSERT INTO schema_migrations(version,name,sha256,applied_at) VALUES(?,?,?,'2026-07-16T12:00:00Z')", item.Version, item.Name, hex.EncodeToString(digest[:])); err != nil {
			t.Fatal(err)
		}
	}
	v3 := Version3()
	if v3.Version != 3 || v3.Name != "edit_session_recovery" || v3.MaxMigrationWalBytes != 16*1024*1024 {
		t.Fatalf("Version3() = %#v", v3)
	}
	digest, err := Checksum(v3)
	if err != nil {
		t.Fatal(err)
	}
	const wantChecksum = "16ae664c033fb1fae7da937eae6c4b19c6b05430fa3499fa5f0da8daa58e1ab4"
	if got := hex.EncodeToString(digest[:]); got != wantChecksum {
		t.Fatalf("Version 3 checksum = %s, want %s", got, wantChecksum)
	}
	actual, err := BuildSchemaContract(ctx, connection, 3)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(actual, Version3SchemaContract()) {
		t.Fatalf("Version 3 contract digest = %x, frozen = %x", sha256.Sum256(actual), sha256.Sum256(Version3SchemaContract()))
	}
	if err := ValidateVersion3SchemaContract(ctx, connection); err != nil {
		t.Fatal(err)
	}
}

func TestVersion3LeavesVersion2ContractAndChecksumUnchanged(t *testing.T) {
	digest, err := Checksum(Version2())
	if err != nil {
		t.Fatal(err)
	}
	if got := hex.EncodeToString(digest[:]); got != "3e15e4350c117143015526452c9d5e517bed29940bbd8c17c7b5172e69c2d821" {
		t.Fatalf("Version 2 checksum changed to %s", got)
	}
	if got := sha256.Sum256(Version2SchemaContract()); hex.EncodeToString(got[:]) != version2ContractDigest {
		t.Fatalf("Version 2 schema contract changed to %x", got)
	}
}
