package migration

import (
	"context"
	"testing"
)

func TestValidateHeadMatchesExactHistoryAndSchemaContract(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	connection := version1Connection(t, ctx)
	contracts := map[uint64][]byte{1: Version1SchemaContract()}
	if err := ValidateHead(ctx, connection, []Migration{Version1()}, contracts, 1); err != nil {
		t.Fatalf("ValidateHead(): %v", err)
	}
	if _, err := connection.ExecContext(ctx, "UPDATE schema_migrations SET name='rewritten' WHERE version=1"); err != nil {
		t.Fatalf("rewrite history fixture: %v", err)
	}
	if err := ValidateHead(ctx, connection, []Migration{Version1()}, contracts, 1); err == nil {
		t.Fatal("ValidateHead(rewritten history) error = nil")
	}
}

func TestValidateHeadRejectsMissingOrChangedContract(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	connection := version1Connection(t, ctx)
	for name, contracts := range map[string]map[uint64][]byte{
		"missing": nil,
		"changed": {1: []byte("not the frozen contract")},
	} {
		t.Run(name, func(t *testing.T) {
			if err := ValidateHead(ctx, connection, []Migration{Version1()}, contracts, 1); err == nil {
				t.Fatal("ValidateHead() error = nil")
			}
		})
	}
}
