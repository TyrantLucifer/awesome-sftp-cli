package migration

import (
	"bytes"
	"encoding/hex"
	"strings"
	"testing"
)

func TestChecksumV1Golden(t *testing.T) {
	t.Parallel()

	migration := Migration{
		Version:    1,
		Name:       "init",
		Statements: []string{"CREATE TABLE jobs(id INTEGER PRIMARY KEY);"},
	}
	var encoded bytes.Buffer
	if err := WriteChecksumInput(&encoded, migration); err != nil {
		t.Fatalf("encode checksum input: %v", err)
	}
	const wantHex = "616d736674702d736368656d612d6d6967726174696f6e2d763100000000000000000100000004696e697400000001000000000000002a435245415445205441424c45206a6f627328696420494e5445474552205052494d415259204b4559293b"
	if got := hex.EncodeToString(encoded.Bytes()); got != wantHex {
		t.Fatalf("encoded checksum input = %s, want %s", got, wantHex)
	}
	digest, err := Checksum(migration)
	if err != nil {
		t.Fatalf("checksum migration: %v", err)
	}
	const wantDigest = "e5e82c0a4bc1d54a3a4091ce62177b04d3cce9d82925c1ef9a3902f3b99bd122"
	if got := hex.EncodeToString(digest[:]); got != wantDigest {
		t.Fatalf("checksum = %s, want %s", got, wantDigest)
	}
}

func TestChecksumRejectsInvalidMigration(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		migration Migration
	}{
		{name: "zero version", migration: Migration{Version: 0, Name: "init", Statements: []string{"CREATE TABLE x(id INTEGER)"}}},
		{name: "large version", migration: Migration{Version: 1 << 63, Name: "init", Statements: []string{"CREATE TABLE x(id INTEGER)"}}},
		{name: "invalid name", migration: Migration{Version: 1, Name: "Init", Statements: []string{"CREATE TABLE x(id INTEGER)"}}},
		{name: "empty statements", migration: Migration{Version: 1, Name: "init"}},
		{name: "blank statement", migration: Migration{Version: 1, Name: "init", Statements: []string{" \t\r\n"}}},
		{name: "nul statement", migration: Migration{Version: 1, Name: "init", Statements: []string{"CREATE TABLE x\x00(id INTEGER)"}}},
		{name: "oversized statement", migration: Migration{Version: 1, Name: "init", Statements: []string{strings.Repeat("x", maxStatementBytes+1)}}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			if _, err := Checksum(test.migration); err == nil {
				t.Fatal("Checksum() error = nil, want rejection")
			}
		})
	}
}

func TestValidateSetRequiresStrictVersionsAndBudgets(t *testing.T) {
	t.Parallel()

	v1 := Version1()
	v2 := Migration{Version: 2, Name: "next", Statements: []string{"ALTER TABLE jobs ADD COLUMN note TEXT"}, MaxMigrationWalBytes: 1}
	if err := ValidateSet([]Migration{v1, v2}); err != nil {
		t.Fatalf("ValidateSet(valid) error: %v", err)
	}

	tests := []struct {
		name       string
		migrations []Migration
	}{
		{name: "empty"},
		{name: "starts at two", migrations: []Migration{v2}},
		{name: "gap", migrations: []Migration{v1, {Version: 3, Name: "gap", Statements: []string{"CREATE TABLE gap(id INTEGER)"}, MaxMigrationWalBytes: 1}}},
		{name: "v1 budget", migrations: []Migration{withBudget(v1, 1)}},
		{name: "v2 zero budget", migrations: []Migration{v1, withBudget(v2, 0)}},
		{name: "v2 excessive budget", migrations: []Migration{v1, withBudget(v2, maxMigrationWalBytes+1)}},
		{name: "synthetic golden is not real v1", migrations: []Migration{{Version: 1, Name: "init", Statements: []string{"CREATE TABLE jobs(id INTEGER PRIMARY KEY);"}}}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			if err := ValidateSet(test.migrations); err == nil {
				t.Fatal("ValidateSet() error = nil, want rejection")
			}
		})
	}
}

func TestVersion1Checksum(t *testing.T) {
	t.Parallel()

	digest, err := Checksum(Version1())
	if err != nil {
		t.Fatalf("checksum Version 1: %v", err)
	}
	const want = "281a5d34c0ebdd06de26fd1098fbf3efd7c8a7e283f5328ea218d1ca8dfb19f9"
	if got := hex.EncodeToString(digest[:]); got != want {
		t.Fatalf("Version 1 checksum = %s, want %s", got, want)
	}
}

func withBudget(migration Migration, budget uint64) Migration {
	migration.MaxMigrationWalBytes = budget
	return migration
}
