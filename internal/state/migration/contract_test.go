package migration

import (
	"bytes"
	"context"
	"crypto/sha256"
	"database/sql"
	"path/filepath"
	"testing"

	"github.com/TyrantLucifer/awesome-mac-sftp/internal/testkit"
)

func TestSchemaContractIsDeterministicAndCoversWholeMainSchema(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	database := openTestDatabase(t, filepath.Join(testkit.PersistentTempDir(t), "contract.sqlite3"))
	connection := reserveConnection(t, ctx, database)
	if err := (Runner{}).Apply(ctx, connection, Version1(), "2026-07-16T00:00:00Z"); err != nil {
		t.Fatalf("apply version 1: %v", err)
	}

	first, err := BuildSchemaContract(ctx, connection, 1)
	if err != nil {
		t.Fatalf("build first schema contract: %v", err)
	}
	second, err := BuildSchemaContract(ctx, connection, 1)
	if err != nil {
		t.Fatalf("build second schema contract: %v", err)
	}
	if !bytes.Equal(first, second) {
		t.Fatal("schema contract is not deterministic")
	}
	if !bytes.HasPrefix(first, []byte(schemaContractMagic)) {
		t.Fatalf("schema contract does not start with frozen magic")
	}
	if !bytes.Equal(first, Version1SchemaContract()) {
		t.Fatalf("runtime Version 1 contract does not match the committed canonical bytes")
	}
	if err := ValidateVersion1SchemaContract(ctx, connection); err != nil {
		t.Fatalf("validate exact Version 1 contract: %v", err)
	}

	before := sha256.Sum256(first)
	if _, err := connection.ExecContext(ctx, "CREATE TABLE unexpected_table(id INTEGER PRIMARY KEY) STRICT"); err != nil {
		t.Fatalf("create unexpected table: %v", err)
	}
	afterBytes, err := BuildSchemaContract(ctx, connection, 1)
	if err != nil {
		t.Fatalf("build tampered schema contract: %v", err)
	}
	after := sha256.Sum256(afterBytes)
	if before == after {
		t.Fatal("unexpected table did not change whole-schema digest")
	}
	if err := ValidateVersion1SchemaContract(ctx, connection); err == nil {
		t.Fatal("ValidateVersion1SchemaContract() accepted an unexpected table")
	}
}

func TestSchemaContractRejectsAttachedOrTemporarySchema(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	database := openTestDatabase(t, filepath.Join(testkit.PersistentTempDir(t), "contract-reject.sqlite3"))
	connection := reserveConnection(t, ctx, database)
	if err := (Runner{}).Apply(ctx, connection, Version1(), "2026-07-16T00:00:00Z"); err != nil {
		t.Fatalf("apply version 1: %v", err)
	}

	if _, err := connection.ExecContext(ctx, "CREATE TEMP TABLE shadow(id INTEGER)"); err != nil {
		t.Fatalf("create temp table: %v", err)
	}
	if _, err := BuildSchemaContract(ctx, connection, 1); err == nil {
		t.Fatal("BuildSchemaContract() accepted a non-empty temp schema")
	}
	if _, err := connection.ExecContext(ctx, "DROP TABLE temp.shadow"); err != nil {
		t.Fatalf("drop temp table: %v", err)
	}
	if _, err := connection.ExecContext(ctx, "ATTACH DATABASE ':memory:' AS other"); err != nil {
		t.Fatalf("attach database: %v", err)
	}
	if _, err := BuildSchemaContract(ctx, connection, 1); err == nil {
		t.Fatal("BuildSchemaContract() accepted an attached schema")
	}
}

func reserveConnection(t *testing.T, ctx context.Context, database *sql.DB) *sql.Conn {
	t.Helper()
	connection, err := database.Conn(ctx)
	if err != nil {
		t.Fatalf("reserve database connection: %v", err)
	}
	t.Cleanup(func() {
		if err := connection.Close(); err != nil {
			t.Errorf("close reserved database connection: %v", err)
		}
	})
	return connection
}
