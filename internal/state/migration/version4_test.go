package migration

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"testing"
)

func TestVersion4ChecksumBudgetAndWholeSchemaContractAreFrozen(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	connection := version1Connection(t, ctx)
	for _, item := range []Migration{Version2(), Version3(), Version4()} {
		for _, statement := range item.Statements {
			if _, err := connection.ExecContext(ctx, statement); err != nil {
				t.Fatalf("apply Version %d statement: %v", item.Version, err)
			}
		}
		digest, err := Checksum(item)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := connection.ExecContext(ctx, "INSERT INTO schema_migrations(version,name,sha256,applied_at) VALUES(?,?,?,'2026-07-18T00:00:00Z')", item.Version, item.Name, hex.EncodeToString(digest[:])); err != nil {
			t.Fatal(err)
		}
	}

	v4 := Version4()
	if v4.Version != 4 || v4.Name != "job_history_retention" || v4.MaxMigrationWalBytes != 16*1024*1024 {
		t.Fatalf("Version4() = %#v", v4)
	}
	digest, err := Checksum(v4)
	if err != nil {
		t.Fatal(err)
	}
	if got := hex.EncodeToString(digest[:]); got != version4Checksum {
		t.Fatalf("Version 4 checksum = %s, want %s", got, version4Checksum)
	}
	actual, err := BuildSchemaContract(ctx, connection, 4)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(actual, Version4SchemaContract()) {
		t.Fatalf("Version 4 contract digest = %x, frozen = %x", sha256.Sum256(actual), sha256.Sum256(Version4SchemaContract()))
	}
	if err := ValidateVersion4SchemaContract(ctx, connection); err != nil {
		t.Fatal(err)
	}

	var foreignTable, onDelete string
	if err := connection.QueryRowContext(ctx, "SELECT \"table\", on_delete FROM pragma_foreign_key_list('job_history_retention') WHERE \"from\"='job_id'").Scan(&foreignTable, &onDelete); err != nil {
		t.Fatal(err)
	}
	if foreignTable != "jobs" || onDelete != "NO ACTION" {
		t.Fatalf("retention claim FK = table:%q on_delete:%q", foreignTable, onDelete)
	}
}

func TestVersion4LeavesVersion3CompatibilityBytesUnchanged(t *testing.T) {
	digest, err := Checksum(Version3())
	if err != nil {
		t.Fatal(err)
	}
	if got := hex.EncodeToString(digest[:]); got != "16ae664c033fb1fae7da937eae6c4b19c6b05430fa3499fa5f0da8daa58e1ab4" {
		t.Fatalf("Version 3 checksum changed to %s", got)
	}
	if got := sha256.Sum256(Version3SchemaContract()); hex.EncodeToString(got[:]) != version3ContractDigest {
		t.Fatalf("Version 3 schema contract changed to %x", got)
	}
}

func TestCompiledRegistryReturnsIsolatedVersion4Head(t *testing.T) {
	migrations, contracts := CompiledSet()
	if SchemaHead != 4 || len(migrations) != 4 || len(contracts) != 4 || migrations[3].Version != 4 {
		t.Fatalf("compiled set = head:%d migrations:%#v contracts:%d", SchemaHead, migrations, len(contracts))
	}
	migrations[3].Statements[0] = "mutated"
	contracts[4][0] ^= 0xff
	again, againContracts := CompiledSet()
	if again[3].Statements[0] == "mutated" || bytes.Equal(contracts[4], againContracts[4]) {
		t.Fatal("CompiledSet leaked caller mutation")
	}
}
