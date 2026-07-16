package migration

const version3MaxMigrationWalBytes = 16 * 1024 * 1024

var version3Statements = []string{
	"CREATE TABLE edit_session_details(session_id TEXT PRIMARY KEY REFERENCES edit_sessions(session_id), purpose TEXT NOT NULL CHECK(purpose IN ('editor','opener')), reference_id TEXT NOT NULL CHECK(length(reference_id)=32 AND reference_id NOT GLOB '*[^0-9a-f]*'), lease_id TEXT NOT NULL CHECK(length(lease_id)=32 AND lease_id NOT GLOB '*[^0-9a-f]*'), machine_json BLOB NOT NULL CHECK(length(machine_json) BETWEEN 2 AND 32768), decision_kind TEXT CHECK(decision_kind IS NULL OR decision_kind IN ('upload','overwrite','save_as','skip')), audit_reason TEXT CHECK(audit_reason IS NULL OR length(audit_reason) BETWEEN 1 AND 512), target_endpoint_id TEXT NOT NULL CHECK(length(target_endpoint_id) BETWEEN 1 AND 255), target_path_bytes BLOB NOT NULL CHECK(length(target_path_bytes) BETWEEN 1 AND 4096), baseline_local_sha256 TEXT NOT NULL CHECK(length(baseline_local_sha256)=64 AND baseline_local_sha256 NOT GLOB '*[^0-9a-f]*'), current_local_sha256 TEXT CHECK(current_local_sha256 IS NULL OR (length(current_local_sha256)=64 AND current_local_sha256 NOT GLOB '*[^0-9a-f]*')), original_presence TEXT NOT NULL CHECK(original_presence IN ('present','absent')), original_kind TEXT CHECK(original_kind IS NULL OR original_kind='file'), original_fingerprint_json BLOB CHECK(original_fingerprint_json IS NULL OR length(original_fingerprint_json) BETWEEN 2 AND 8192), expected_presence TEXT NOT NULL CHECK(expected_presence IN ('present','absent')), expected_kind TEXT CHECK(expected_kind IS NULL OR expected_kind='file'), expected_fingerprint_json BLOB CHECK(expected_fingerprint_json IS NULL OR length(expected_fingerprint_json) BETWEEN 2 AND 8192), created_at_unix INTEGER NOT NULL CHECK(created_at_unix>0), updated_at_unix INTEGER NOT NULL CHECK(updated_at_unix>=created_at_unix), CHECK((original_presence='present' AND original_kind='file' AND original_fingerprint_json IS NOT NULL) OR (original_presence='absent' AND original_kind IS NULL AND original_fingerprint_json IS NULL)), CHECK((expected_presence='present' AND expected_kind='file' AND expected_fingerprint_json IS NOT NULL) OR (expected_presence='absent' AND expected_kind IS NULL AND expected_fingerprint_json IS NULL))) STRICT",
	"CREATE INDEX edit_session_details_by_target ON edit_session_details(target_endpoint_id,target_path_bytes,session_id)",
}

// Version3 makes every edit decision input reconstructible without changing
// the already-frozen Version 2 schema.
func Version3() Migration {
	statements := make([]string, len(version3Statements))
	copy(statements, version3Statements)
	return Migration{Version: 3, Name: "edit_session_recovery", Statements: statements, MaxMigrationWalBytes: version3MaxMigrationWalBytes}
}
