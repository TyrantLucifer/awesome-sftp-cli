package migration

const version4MaxMigrationWalBytes = 16 * 1024 * 1024

var version4Statements = []string{
	"CREATE TABLE job_history_retention(singleton INTEGER PRIMARY KEY CHECK(singleton = 1), job_id TEXT NOT NULL UNIQUE REFERENCES jobs(job_id), policy_version INTEGER NOT NULL CHECK(policy_version = 1), claimed_at_unix INTEGER NOT NULL CHECK(claimed_at_unix > 0)) STRICT",
}

// Version4 adds one durable retention claim. The singleton makes a bounded
// cleanup restartable without exposing a general-purpose deletion queue.
func Version4() Migration {
	statements := make([]string, len(version4Statements))
	copy(statements, version4Statements)
	return Migration{
		Version:              4,
		Name:                 "job_history_retention",
		Statements:           statements,
		MaxMigrationWalBytes: version4MaxMigrationWalBytes,
	}
}
