package migration

import (
	"bytes"
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"testing"
	"time"
)

const version2TestAttemptID = "22222222222222222222222222222222"

func TestVersion2ChecksumAndBudgetAreFrozen(t *testing.T) {
	t.Parallel()

	v1 := Version1()
	v2 := Version2()
	if err := ValidateSet([]Migration{v1, v2}); err != nil {
		t.Fatalf("ValidateSet([Version1, Version2]) error: %v", err)
	}
	if v2.Version != 2 || v2.Name != "stage3_cache" || v2.MaxMigrationWalBytes != 64*1024*1024 {
		t.Fatalf("Version2() identity = version %d name %q budget %d", v2.Version, v2.Name, v2.MaxMigrationWalBytes)
	}
	digest, err := Checksum(v2)
	if err != nil {
		t.Fatalf("checksum Version 2: %v", err)
	}
	const want = "3e15e4350c117143015526452c9d5e517bed29940bbd8c17c7b5172e69c2d821"
	if got := hex.EncodeToString(digest[:]); got != want {
		t.Fatalf("Version 2 checksum = %s, want %s", got, want)
	}
}

func TestVersion2SchemaContractIsFrozenAndCoversStage3Ownership(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	connection := version1Connection(t, ctx)
	prepareVersion2Attempt(t, ctx, connection)
	if err := (Runner{AttemptID: version2TestAttemptID, WALMonitor: noopMigrationWALMonitor{}}).Apply(ctx, connection, Version2(), "2026-07-16T12:00:00Z"); err != nil {
		t.Fatalf("apply Version 2: %v", err)
	}

	actual, err := BuildSchemaContract(ctx, connection, 2)
	if err != nil {
		t.Fatalf("build Version 2 schema contract: %v", err)
	}
	if !bytes.Equal(actual, Version2SchemaContract()) {
		t.Fatalf("runtime Version 2 contract digest = %x, frozen = %x", sha256.Sum256(actual), sha256.Sum256(Version2SchemaContract()))
	}
	if err := ValidateVersion2SchemaContract(ctx, connection); err != nil {
		t.Fatalf("validate Version 2 schema contract: %v", err)
	}

	wantTables := []string{
		"cache_blobs",
		"cache_entries",
		"cache_leases",
		"cache_materializations",
		"cache_references",
		"edit_session_events",
		"edit_session_jobs",
		"edit_sessions",
	}
	for _, table := range wantTables {
		var count int
		if err := connection.QueryRowContext(ctx, "SELECT count(*) FROM sqlite_schema WHERE type='table' AND name=?", table).Scan(&count); err != nil {
			t.Fatalf("inspect table %s: %v", table, err)
		}
		if count != 1 {
			t.Fatalf("table %s count = %d, want 1", table, count)
		}
	}
}

func TestVersion2LeavesVersion1CompatibilityBytesUnchanged(t *testing.T) {
	t.Parallel()

	v1Digest, err := Checksum(Version1())
	if err != nil {
		t.Fatalf("checksum Version 1: %v", err)
	}
	if got := hex.EncodeToString(v1Digest[:]); got != "281a5d34c0ebdd06de26fd1098fbf3efd7c8a7e283f5328ea218d1ca8dfb19f9" {
		t.Fatalf("Version 1 checksum changed to %s", got)
	}
	if got := sha256.Sum256(Version1SchemaContract()); hex.EncodeToString(got[:]) != version1ContractDigest {
		t.Fatalf("Version 1 schema contract digest changed to %x", got)
	}
}

func prepareVersion2Attempt(t *testing.T, ctx context.Context, connection *sql.Conn) {
	t.Helper()

	digests, err := SchemaContractDigests(
		[]Migration{Version1(), Version2()},
		map[uint64][]byte{1: Version1SchemaContract(), 2: Version2SchemaContract()},
	)
	if err != nil {
		t.Fatalf("schema contract digests: %v", err)
	}
	setDigest, err := MigrationSetDigest(1, 2, []Migration{Version1(), Version2()}, digests)
	if err != nil {
		t.Fatalf("migration set digest: %v", err)
	}
	request := AttemptRequest{AttemptID: version2TestAttemptID, OriginalHead: 1, TargetHead: 2, MigrationSetDigest: setDigest}
	if _, _, err := PrepareAttempt(ctx, connection, request); err != nil {
		t.Fatalf("prepare Version 2 attempt: %v", err)
	}
	backupDigest := sha256.Sum256([]byte("stage3-version2-contract-backup"))
	if _, err := RecordVerifiedBackup(ctx, connection, request.AttemptID, backupDigest, time.Unix(200, 0)); err != nil {
		t.Fatalf("record Version 2 backup: %v", err)
	}
	if _, err := MarkAttemptRunning(ctx, connection, request.AttemptID); err != nil {
		t.Fatalf("mark Version 2 attempt running: %v", err)
	}
}
