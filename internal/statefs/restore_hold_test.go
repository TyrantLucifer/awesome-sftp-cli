package statefs

import (
	"context"
	"crypto/sha256"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/TyrantLucifer/awesome-mac-sftp/internal/state/migration"
)

func TestInitializeLeavesRestoredBackupHeldAndByteExactForReadOnlyDiagnosis(t *testing.T) {
	root := privateTempDir(t)
	path := filepath.Join(root, "amsftp.db")
	copyHistoricalStateFixture(t, "sqlite-backup-v1-stage3.sqlite", root, filepath.Base(path))
	before := snapshotMigrationDiagnosisFiles(t, root)
	database, _, err := Initialize(context.Background(), InitializeConfig{
		Root: root, DatabasePath: path, Random: strings.NewReader(strings.Repeat("p", probeRandomBytes)),
		Now: time.Unix(1_999, 0),
	})
	if database != nil {
		_ = database.Close()
		t.Fatal("Initialize(restored backup) returned a writable database")
	}
	if err == nil || !strings.Contains(err.Error(), "restored-backup hold") {
		t.Fatalf("Initialize(restored backup) error = %v", err)
	}
	after := snapshotMigrationDiagnosisFiles(t, root)
	if !reflect.DeepEqual(after, before) {
		t.Fatalf("ordinary startup mutated held restore: before=%#v after=%#v", before, after)
	}
	report, inspectErr := InspectMigrationStateReadOnly(context.Background(), MigrationInspectionConfig{Root: root, DatabasePath: path})
	if inspectErr != nil {
		t.Fatal(inspectErr)
	}
	if report.Disposition != MigrationDispositionRestoredBackupHold {
		t.Fatalf("report = %#v", report)
	}
}

func TestApproveRestoredBackupForUpgradeBindsPreimageAndStartsExplicitAttempt(t *testing.T) {
	root := privateTempDir(t)
	path := filepath.Join(root, "amsftp.db")
	copyHistoricalStateFixture(t, "sqlite-backup-v1-stage3.sqlite", root, filepath.Base(path))
	raw, err := os.ReadFile(path) //nolint:gosec // exact test-owned path
	if err != nil {
		t.Fatal(err)
	}
	preimage := sha256.Sum256(raw)
	heldAttemptID := strings.Repeat("2", 32)
	newAttemptID := strings.Repeat("4", 32)

	attempt, err := ApproveRestoredBackupForUpgrade(context.Background(), RestoreUpgradeApprovalConfig{
		Root: root, DatabasePath: path, ExpectedSHA256: preimage,
		HeldAttemptID: heldAttemptID, NewAttemptID: newAttemptID,
		Random: strings.NewReader(strings.Repeat("p", probeRandomBytes)),
	})
	if err != nil {
		t.Fatal(err)
	}
	if attempt.AttemptID != newAttemptID || attempt.Status != migration.AttemptPreparing || attempt.OriginalHead != 1 || attempt.TargetHead != 3 {
		t.Fatalf("attempt = %#v", attempt)
	}
	report, err := InspectMigrationStateReadOnly(context.Background(), MigrationInspectionConfig{Root: root, DatabasePath: path})
	if err != nil {
		t.Fatal(err)
	}
	if report.Disposition != MigrationDispositionExplicitResumeRequired || report.HoldAttemptID != "" || report.ActiveAttempt == nil || report.ActiveAttempt.AttemptID != newAttemptID {
		t.Fatalf("post-approval report = %#v", report)
	}

	database, initializeReport, err := Initialize(context.Background(), InitializeConfig{
		Root: root, DatabasePath: path, ExplicitMigrationResume: true,
		Random: strings.NewReader(strings.Repeat("q", probeRandomBytes)), Now: time.Unix(2_000, 0),
	})
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	if initializeReport.SchemaHead != 3 {
		t.Fatalf("initialize report = %#v", initializeReport)
	}
	var attempts, holdRows int
	if err := database.QueryRowContext(context.Background(), "SELECT count(*) FROM migration_attempts").Scan(&attempts); err != nil {
		t.Fatal(err)
	}
	if err := database.QueryRowContext(context.Background(), "SELECT count(*) FROM migration_control WHERE singleton=1 AND upgrade_hold=0 AND hold_reason IS NULL AND hold_attempt_id IS NULL").Scan(&holdRows); err != nil {
		t.Fatal(err)
	}
	if attempts != 0 || holdRows != 1 {
		t.Fatalf("attempts/clear hold rows = %d/%d, want 0/1", attempts, holdRows)
	}
}

func TestApproveRestoredBackupForUpgradeRejectsWrongIdentityWithoutMutation(t *testing.T) {
	tests := []struct {
		name          string
		heldAttemptID string
		digest        [sha256.Size]byte
	}{
		{name: "wrong held attempt", heldAttemptID: strings.Repeat("3", 32)},
		{name: "wrong digest", heldAttemptID: strings.Repeat("2", 32), digest: sha256.Sum256([]byte("wrong"))},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			root := privateTempDir(t)
			path := filepath.Join(root, "amsftp.db")
			copyHistoricalStateFixture(t, "sqlite-backup-v1-stage3.sqlite", root, filepath.Base(path))
			if tt.digest == [sha256.Size]byte{} {
				raw, err := os.ReadFile(path) //nolint:gosec // exact test-owned path
				if err != nil {
					t.Fatal(err)
				}
				tt.digest = sha256.Sum256(raw)
			}
			before := snapshotMigrationDiagnosisFiles(t, root)
			if _, err := ApproveRestoredBackupForUpgrade(context.Background(), RestoreUpgradeApprovalConfig{
				Root: root, DatabasePath: path, ExpectedSHA256: tt.digest,
				HeldAttemptID: tt.heldAttemptID, NewAttemptID: strings.Repeat("4", 32),
				Random: strings.NewReader(strings.Repeat("p", probeRandomBytes)),
			}); err == nil {
				t.Fatal("ApproveRestoredBackupForUpgrade() error = nil")
			}
			after := snapshotMigrationDiagnosisFiles(t, root)
			if !reflect.DeepEqual(after, before) {
				t.Fatalf("rejected approval mutated state: before=%#v after=%#v", before, after)
			}
		})
	}
}
