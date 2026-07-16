package migration

import "testing"

func TestAdmitStatementAcceptsSingleMainStatement(t *testing.T) {
	t.Parallel()

	tests := []struct {
		statement string
		kind      StatementKind
		target    string
	}{
		{statement: "CREATE TABLE jobs(id INTEGER PRIMARY KEY)", kind: StatementCreateTable, target: "jobs"},
		{statement: "/* lead */ CREATE UNIQUE INDEX main.jobs_by_state ON jobs(state); -- tail", kind: StatementCreateIndex, target: "jobs_by_state"},
		{statement: "CREATE VIEW \"job_view\" AS SELECT id FROM jobs", kind: StatementCreateView, target: "job_view"},
		{statement: "ALTER TABLE main.jobs ADD COLUMN note TEXT", kind: StatementAlterTable, target: "jobs"},
		{statement: "DROP INDEX IF EXISTS jobs_by_state", kind: StatementDropIndex, target: "jobs_by_state"},
		{statement: "INSERT INTO migration_control(singleton) VALUES(1)", kind: StatementInsert, target: "migration_control"},
		{statement: "UPDATE jobs SET state='paused' WHERE id='job_abc;def'", kind: StatementUpdate, target: "jobs"},
		{statement: "DELETE FROM jobs WHERE id = 'job_abc'", kind: StatementDelete, target: "jobs"},
	}
	for _, test := range tests {
		t.Run(test.statement, func(t *testing.T) {
			t.Parallel()
			admitted, err := AdmitStatement(2, 0, test.statement)
			if err != nil {
				t.Fatalf("AdmitStatement() error: %v", err)
			}
			if admitted.Kind != test.kind || admitted.Target != test.target {
				t.Fatalf("AdmitStatement() = %#v, want kind %q target %q", admitted, test.kind, test.target)
			}
		})
	}
}

func TestAdmitStatementRejectsUnsafeOrAmbiguousSQL(t *testing.T) {
	t.Parallel()

	statements := []string{
		"",
		"/* comment only */",
		"CREATE TABLE x(id INTEGER); DELETE FROM jobs",
		"CREATE TABLE x(id TEXT); /* tail */ ;",
		"CREATE TABLE x(id TEXT /* unclosed",
		"CREATE TABLE \"x(id TEXT)",
		"BEGIN",
		"COMMIT",
		"ROLLBACK",
		"SAVEPOINT escape",
		"ATTACH 'other.db' AS other",
		"DETACH other",
		"VACUUM",
		"PRAGMA application_id=1095586630",
		"CREATE TRIGGER x AFTER INSERT ON jobs BEGIN DELETE FROM jobs; END",
		"CREATE TEMP TABLE x(id INTEGER)",
		"CREATE VIRTUAL TABLE x USING fts5(body)",
		"SELECT * FROM jobs",
		"WITH x AS (SELECT 1) DELETE FROM jobs",
		"REPLACE INTO jobs(id) VALUES(1)",
		"CREATE TABLE temp.x(id INTEGER)",
		"CREATE TABLE other.x(id INTEGER)",
		"CREATE TABLE \"main\".x(id INTEGER)",
		"CREATE TABLE main.\"x\".extra(id INTEGER)",
	}
	for _, statement := range statements {
		t.Run(statement, func(t *testing.T) {
			t.Parallel()
			if _, err := AdmitStatement(2, 0, statement); err == nil {
				t.Fatal("AdmitStatement() error = nil, want rejection")
			}
		})
	}
}

func TestAdmitStatementAllowsOnlyExactVersion1ApplicationIDOperation(t *testing.T) {
	t.Parallel()

	admitted, err := AdmitStatement(1, 1, applicationIDStatement)
	if err != nil {
		t.Fatalf("AdmitStatement(exact app ID) error: %v", err)
	}
	if admitted.Kind != StatementApplicationID {
		t.Fatalf("kind = %q, want %q", admitted.Kind, StatementApplicationID)
	}

	for _, statement := range []string{
		"pragma application_id = 1095586630",
		"PRAGMA application_id=1095586630",
		"PRAGMA application_id = 1095586630;",
		"PRAGMA application_id = 1",
	} {
		if _, err := AdmitStatement(1, 1, statement); err == nil {
			t.Fatalf("AdmitStatement(%q) error = nil, want rejection", statement)
		}
	}
	if _, err := AdmitStatement(1, 0, applicationIDStatement); err == nil {
		t.Fatal("application ID at wrong index was accepted")
	}
}
