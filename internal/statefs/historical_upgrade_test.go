package statefs

import (
	"context"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestInitializeUpgradesEveryPinnedHistoricalSQLiteState(t *testing.T) {
	type historicalBackup struct {
		fixture     string
		destination string
	}
	tests := []struct {
		name       string
		fixture    string
		jobID      string
		attemptID  string
		backups    []historicalBackup
		detailRows int
	}{
		{name: "v1", fixture: "sqlite-v1-stage2.sqlite", jobID: "job-historical-v1", attemptID: strings.Repeat("7", 32)},
		{name: "v2", fixture: "sqlite-v2-stage3.sqlite", jobID: "job-historical-v2", attemptID: strings.Repeat("8", 32), backups: []historicalBackup{
			{fixture: "sqlite-backup-v1-stage3.sqlite", destination: ".amsftp-backup-v1-" + strings.Repeat("2", 32) + ".sqlite3"},
		}},
		{name: "v3", fixture: "sqlite-v3-stage3.sqlite", jobID: "job-historical-v2", attemptID: strings.Repeat("9", 32), detailRows: 1, backups: []historicalBackup{
			{fixture: "sqlite-backup-v1-stage3.sqlite", destination: ".amsftp-backup-v1-" + strings.Repeat("2", 32) + ".sqlite3"},
			{fixture: "sqlite-backup-v2-stage3.sqlite", destination: ".amsftp-backup-v1-" + strings.Repeat("3", 32) + ".sqlite3"},
		}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			root := privateTempDir(t)
			path := filepath.Join(root, "amsftp.db")
			copyHistoricalStateFixture(t, tt.fixture, root, filepath.Base(path))
			for _, backup := range tt.backups {
				copyHistoricalStateFixture(t, backup.fixture, root, backup.destination)
			}
			database, report, err := Initialize(context.Background(), InitializeConfig{
				Root: root, DatabasePath: path, Random: strings.NewReader(strings.Repeat("r", probeRandomBytes)),
				Now: time.Unix(1_900, 0), MigrationAttemptID: tt.attemptID,
			})
			if err != nil {
				t.Fatalf("Initialize(%s): %v", tt.fixture, err)
			}
			defer database.Close()
			if report.SchemaHead != 4 {
				t.Fatalf("Initialize(%s) report = %#v", tt.fixture, report)
			}
			var jobs int
			if err := database.QueryRowContext(context.Background(), "SELECT count(*) FROM jobs WHERE job_id=?", tt.jobID).Scan(&jobs); err != nil || jobs != 1 {
				t.Fatalf("Initialize(%s) historical job count = %d, error=%v", tt.fixture, jobs, err)
			}
			var details int
			if err := database.QueryRowContext(context.Background(), "SELECT count(*) FROM edit_session_details").Scan(&details); err != nil || details != tt.detailRows {
				t.Fatalf("Initialize(%s) historical detail count = %d, error=%v, want %d", tt.fixture, details, err, tt.detailRows)
			}
			connection, err := database.Conn(context.Background())
			if err != nil {
				t.Fatalf("Initialize(%s) reserve retention connection: %v", tt.fixture, err)
			}
			defer connection.Close()
			if _, err := ReconcileBackupRetentionAfterSchemaValidation(context.Background(), connection, root, 4); err != nil {
				t.Fatalf("Initialize(%s) retained backup closure: %v", tt.fixture, err)
			}
		})
	}
}

func copyHistoricalStateFixture(t *testing.T, source, destinationRoot, destination string) {
	t.Helper()
	raw, err := fs.ReadFile(os.DirFS(filepath.Join("..", "compatibility", "testdata", "historical")), source)
	if err != nil {
		t.Fatal(err)
	}
	root, err := os.OpenRoot(destinationRoot)
	if err != nil {
		t.Fatal(err)
	}
	defer root.Close()
	if err := root.WriteFile(destination, raw, 0o600); err != nil {
		t.Fatal(err)
	}
}
