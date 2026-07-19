package statefs

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func TestInspectMigrationStateReadOnlyClassifiesPinnedStatesWithoutMutation(t *testing.T) {
	tests := []struct {
		name          string
		fixture       string
		disposition   MigrationDisposition
		head          uint64
		holdAttemptID string
	}{
		{
			name:          "verified restored backup remains held",
			fixture:       "sqlite-backup-v1-stage3.sqlite",
			disposition:   MigrationDispositionRestoredBackupHold,
			head:          1,
			holdAttemptID: strings.Repeat("2", 32),
		},
		{
			name:        "supported older database can upgrade",
			fixture:     "sqlite-v1-stage2.sqlite",
			disposition: MigrationDispositionUpgradeAvailable,
			head:        1,
		},
		{
			name:        "version 3 database can upgrade",
			fixture:     "sqlite-v3-stage3.sqlite",
			disposition: MigrationDispositionUpgradeAvailable,
			head:        3,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			root := privateTempDir(t)
			path := filepath.Join(root, "amsftp.db")
			copyHistoricalStateFixture(t, tt.fixture, root, filepath.Base(path))
			before := snapshotMigrationDiagnosisFiles(t, root)

			report, err := InspectMigrationStateReadOnly(context.Background(), MigrationInspectionConfig{
				Root: root, DatabasePath: path,
			})
			if err != nil {
				t.Fatal(err)
			}
			if report.Disposition != tt.disposition || report.SchemaHead != tt.head || report.BinaryTargetHead != 4 {
				t.Fatalf("report = %#v", report)
			}
			if report.HoldAttemptID != tt.holdAttemptID {
				t.Fatalf("hold attempt ID = %q, want %q", report.HoldAttemptID, tt.holdAttemptID)
			}
			if report.ActiveAttempt != nil {
				t.Fatalf("active attempt = %#v, want nil", report.ActiveAttempt)
			}
			after := snapshotMigrationDiagnosisFiles(t, root)
			if !reflect.DeepEqual(after, before) {
				t.Fatalf("read-only inspection mutated state: before=%#v after=%#v", before, after)
			}
		})
	}
}

func TestInspectMigrationStateReadOnlyReportsNewerSchemaWithoutWriting(t *testing.T) {
	root := privateTempDir(t)
	path := filepath.Join(root, "amsftp.db")
	copyHistoricalStateFixture(t, "sqlite-v3-stage3.sqlite", root, filepath.Base(path))
	database, err := sql.Open("sqlite", durabilityURI(path, false))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := database.ExecContext(context.Background(), `INSERT INTO schema_migrations(version, name, sha256, applied_at) VALUES
		(4, 'future-4', ?, 'future'),
		(5, 'future-5', ?, 'future')`, strings.Repeat("a", 64), strings.Repeat("b", 64)); err != nil {
		_ = database.Close()
		t.Fatal(err)
	}
	if err := database.Close(); err != nil {
		t.Fatal(err)
	}
	if err := requireDatabaseSidecarsAbsent(path); err != nil {
		t.Fatal(err)
	}
	before := snapshotMigrationDiagnosisFiles(t, root)
	report, err := InspectMigrationStateReadOnly(context.Background(), MigrationInspectionConfig{Root: root, DatabasePath: path})
	if err != nil {
		t.Fatal(err)
	}
	if report.Disposition != MigrationDispositionNewerSchema || report.SchemaHead != 5 || report.BinaryTargetHead != 4 {
		t.Fatalf("report = %#v", report)
	}
	after := snapshotMigrationDiagnosisFiles(t, root)
	if !reflect.DeepEqual(after, before) {
		t.Fatalf("newer-schema inspection mutated state: before=%#v after=%#v", before, after)
	}
}

func TestInspectMigrationStateReadOnlyDefersSidecarRecovery(t *testing.T) {
	root := privateTempDir(t)
	path := filepath.Join(root, "amsftp.db")
	copyHistoricalStateFixture(t, "sqlite-v3-stage3.sqlite", root, filepath.Base(path))
	if err := os.WriteFile(path+"-wal", []byte("opaque recovery state"), 0o600); err != nil {
		t.Fatal(err)
	}
	before := snapshotMigrationDiagnosisFiles(t, root)
	report, err := InspectMigrationStateReadOnly(context.Background(), MigrationInspectionConfig{Root: root, DatabasePath: path})
	if err != nil {
		t.Fatal(err)
	}
	if report.Disposition != MigrationDispositionSidecarRecoveryRequired || report.SchemaHead != 0 || !report.HasSidecars {
		t.Fatalf("report = %#v", report)
	}
	after := snapshotMigrationDiagnosisFiles(t, root)
	if !reflect.DeepEqual(after, before) {
		t.Fatalf("sidecar inspection mutated state: before=%#v after=%#v", before, after)
	}
}

type migrationDiagnosisFileSnapshot struct {
	Name   string
	Mode   os.FileMode
	Size   int64
	ModNS  int64
	SHA256 string
}

func snapshotMigrationDiagnosisFiles(t *testing.T, root string) []migrationDiagnosisFileSnapshot {
	t.Helper()
	entries, err := os.ReadDir(root)
	if err != nil {
		t.Fatal(err)
	}
	result := make([]migrationDiagnosisFileSnapshot, 0, len(entries))
	for _, entry := range entries {
		info, err := entry.Info()
		if err != nil {
			t.Fatal(err)
		}
		raw, err := os.ReadFile(filepath.Join(root, entry.Name())) //nolint:gosec // test-owned private root
		if err != nil {
			t.Fatal(err)
		}
		result = append(result, migrationDiagnosisFileSnapshot{
			Name: entry.Name(), Mode: info.Mode(), Size: info.Size(), ModNS: info.ModTime().UnixNano(),
			SHA256: fmt.Sprintf("%x", sha256.Sum256(raw)),
		})
	}
	return result
}
