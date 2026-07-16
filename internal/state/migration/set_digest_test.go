package migration

import (
	"crypto/sha256"
	"testing"
)

func TestMigrationSetDigestFreezesOriginalTargetChecksumsContractsAndBudgets(t *testing.T) {
	t.Parallel()

	v2 := Migration{Version: 2, Name: "second", Statements: []string{"CREATE TABLE second(id INTEGER PRIMARY KEY) STRICT"}, MaxMigrationWalBytes: 4096}
	v3 := Migration{Version: 3, Name: "third", Statements: []string{"ALTER TABLE second ADD COLUMN note TEXT"}, MaxMigrationWalBytes: 8192}
	migrations := []Migration{Version1(), v2, v3}
	contracts := map[uint64][sha256.Size]byte{2: sha256.Sum256([]byte("contract-2")), 3: sha256.Sum256([]byte("contract-3"))}
	digest, err := MigrationSetDigest(1, 3, migrations, contracts)
	if err != nil {
		t.Fatalf("MigrationSetDigest(): %v", err)
	}
	repeated, err := MigrationSetDigest(1, 3, migrations, contracts)
	if err != nil || repeated != digest {
		t.Fatalf("repeated digest = %x, %v; want %x", repeated, err, digest)
	}

	changedBudget := append([]Migration(nil), migrations...)
	changedBudget[1].MaxMigrationWalBytes++
	changed, err := MigrationSetDigest(1, 3, changedBudget, contracts)
	if err != nil {
		t.Fatalf("digest changed budget: %v", err)
	}
	if changed == digest {
		t.Fatal("migration WAL budget did not change set digest")
	}
	changedContracts := map[uint64][sha256.Size]byte{2: contracts[2], 3: sha256.Sum256([]byte("changed-contract-3"))}
	changed, err = MigrationSetDigest(1, 3, migrations, changedContracts)
	if err != nil {
		t.Fatalf("digest changed contract: %v", err)
	}
	if changed == digest {
		t.Fatal("schema contract did not change set digest")
	}
}

func TestMigrationSetDigestRejectsInvalidFrozenRange(t *testing.T) {
	t.Parallel()

	v2 := Migration{Version: 2, Name: "second", Statements: []string{"CREATE TABLE second(id INTEGER PRIMARY KEY) STRICT"}, MaxMigrationWalBytes: 4096}
	migrations := []Migration{Version1(), v2}
	contract := sha256.Sum256([]byte("contract-2"))
	for name, test := range map[string]struct {
		original  uint64
		target    uint64
		contracts map[uint64][sha256.Size]byte
	}{
		"zero original":    {original: 0, target: 2, contracts: map[uint64][sha256.Size]byte{2: contract}},
		"empty range":      {original: 1, target: 1, contracts: map[uint64][sha256.Size]byte{2: contract}},
		"newer target":     {original: 1, target: 3, contracts: map[uint64][sha256.Size]byte{2: contract}},
		"missing contract": {original: 1, target: 2, contracts: nil},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			if _, err := MigrationSetDigest(test.original, test.target, migrations, test.contracts); err == nil {
				t.Fatal("MigrationSetDigest() error = nil")
			}
		})
	}
}
