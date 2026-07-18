package migration

// CompiledSet returns isolated copies of the complete forward-only migration
// history and its per-head whole-schema contracts.
func CompiledSet() ([]Migration, map[uint64][]byte) {
	migrations := []Migration{Version1(), Version2(), Version3(), Version4()}
	contracts := map[uint64][]byte{
		1: Version1SchemaContract(),
		2: Version2SchemaContract(),
		3: Version3SchemaContract(),
		4: Version4SchemaContract(),
	}
	return migrations, contracts
}
