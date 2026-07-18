package statefs

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

const (
	restoreCrashRootEnvironment  = "AMSFTP_TEST_RESTORE_CRASH_ROOT"
	restoreCrashPointEnvironment = "AMSFTP_TEST_RESTORE_CRASH_POINT"
	restoreCrashExitCode         = 87
)

func TestRestoreMigrationBackupPublishesVerifiedHeldDatabaseNoReplace(t *testing.T) {
	root := privateTempDir(t)
	catalogPath := filepath.Join(root, "amsftp.db")
	destinationPath := filepath.Join(root, "restored.db")
	backupName := ".amsftp-backup-v1-" + strings.Repeat("2", 32) + ".sqlite3"
	copyHistoricalStateFixture(t, "sqlite-v2-stage3.sqlite", root, filepath.Base(catalogPath))
	copyHistoricalStateFixture(t, "sqlite-backup-v1-stage3.sqlite", root, backupName)
	sourceBefore := snapshotOneMigrationFile(t, catalogPath)
	backupBefore := snapshotOneMigrationFile(t, filepath.Join(root, backupName))

	report, err := RestoreMigrationBackup(context.Background(), RestoreMigrationBackupConfig{
		Root: root, CatalogDatabasePath: catalogPath, DestinationPath: destinationPath,
		BackupAttemptID: strings.Repeat("2", 32),
	})
	if err != nil {
		t.Fatal(err)
	}
	if report.Reused || report.OriginalHead != 1 || report.BackupAttemptID != strings.Repeat("2", 32) {
		t.Fatalf("restore report = %#v", report)
	}
	if got := snapshotOneMigrationFile(t, catalogPath); got != sourceBefore {
		t.Fatalf("catalog database changed: before=%#v after=%#v", sourceBefore, got)
	}
	if got := snapshotOneMigrationFile(t, filepath.Join(root, backupName)); got != backupBefore {
		t.Fatalf("backup changed: before=%#v after=%#v", backupBefore, got)
	}
	inspection, err := InspectMigrationStateReadOnly(context.Background(), MigrationInspectionConfig{Root: root, DatabasePath: destinationPath})
	if err != nil {
		t.Fatal(err)
	}
	if inspection.Disposition != MigrationDispositionRestoredBackupHold || inspection.SchemaHead != 1 || inspection.HoldAttemptID != strings.Repeat("2", 32) {
		t.Fatalf("restored inspection = %#v", inspection)
	}

	beforeRetry := snapshotMigrationDiagnosisFiles(t, root)
	repeated, err := RestoreMigrationBackup(context.Background(), RestoreMigrationBackupConfig{
		Root: root, CatalogDatabasePath: catalogPath, DestinationPath: destinationPath,
		BackupAttemptID: strings.Repeat("2", 32),
	})
	if err != nil {
		t.Fatal(err)
	}
	if !repeated.Reused || repeated.SHA256 != report.SHA256 {
		t.Fatalf("repeated report = %#v, want reused %#v", repeated, report)
	}
	afterRetry := snapshotMigrationDiagnosisFiles(t, root)
	if !reflect.DeepEqual(afterRetry, beforeRetry) {
		t.Fatalf("idempotent restore mutated state: before=%#v after=%#v", beforeRetry, afterRetry)
	}
}

func TestRestoreMigrationBackupSupportsPinnedVersion2RollbackPoint(t *testing.T) {
	root := privateTempDir(t)
	catalogPath := filepath.Join(root, "amsftp.db")
	destinationPath := filepath.Join(root, "restored.db")
	copyHistoricalStateFixture(t, "sqlite-v3-stage3.sqlite", root, filepath.Base(catalogPath))
	copyHistoricalStateFixture(t, "sqlite-backup-v1-stage3.sqlite", root, ".amsftp-backup-v1-"+strings.Repeat("2", 32)+".sqlite3")
	copyHistoricalStateFixture(t, "sqlite-backup-v2-stage3.sqlite", root, ".amsftp-backup-v1-"+strings.Repeat("3", 32)+".sqlite3")
	report, err := RestoreMigrationBackup(context.Background(), RestoreMigrationBackupConfig{
		Root: root, CatalogDatabasePath: catalogPath, DestinationPath: destinationPath,
		BackupAttemptID: strings.Repeat("3", 32),
	})
	if err != nil {
		t.Fatal(err)
	}
	if report.OriginalHead != 2 || report.Reused {
		t.Fatalf("restore report = %#v", report)
	}
	inspection, err := InspectMigrationStateReadOnly(context.Background(), MigrationInspectionConfig{Root: root, DatabasePath: destinationPath})
	if err != nil {
		t.Fatal(err)
	}
	if inspection.SchemaHead != 2 || inspection.HoldAttemptID != strings.Repeat("3", 32) || inspection.Disposition != MigrationDispositionRestoredBackupHold {
		t.Fatalf("restored inspection = %#v", inspection)
	}
}

func TestRestoreMigrationBackupRejectsCollisionAndCorruptionWithoutDestinationMutation(t *testing.T) {
	t.Run("destination collision", func(t *testing.T) {
		root := privateTempDir(t)
		catalogPath := filepath.Join(root, "amsftp.db")
		destinationPath := filepath.Join(root, "restored.db")
		backupName := ".amsftp-backup-v1-" + strings.Repeat("2", 32) + ".sqlite3"
		copyHistoricalStateFixture(t, "sqlite-v2-stage3.sqlite", root, filepath.Base(catalogPath))
		copyHistoricalStateFixture(t, "sqlite-backup-v1-stage3.sqlite", root, backupName)
		if err := os.WriteFile(destinationPath, []byte("do not replace"), 0o600); err != nil {
			t.Fatal(err)
		}
		before := snapshotMigrationDiagnosisFiles(t, root)
		if _, err := RestoreMigrationBackup(context.Background(), RestoreMigrationBackupConfig{
			Root: root, CatalogDatabasePath: catalogPath, DestinationPath: destinationPath,
			BackupAttemptID: strings.Repeat("2", 32),
		}); err == nil {
			t.Fatal("RestoreMigrationBackup(collision) error = nil")
		}
		after := snapshotMigrationDiagnosisFiles(t, root)
		if !reflect.DeepEqual(after, before) {
			t.Fatalf("collision changed directory: before=%#v after=%#v", before, after)
		}
	})

	t.Run("cataloged backup corruption", func(t *testing.T) {
		root := privateTempDir(t)
		catalogPath := filepath.Join(root, "amsftp.db")
		destinationPath := filepath.Join(root, "restored.db")
		backupName := ".amsftp-backup-v1-" + strings.Repeat("2", 32) + ".sqlite3"
		copyHistoricalStateFixture(t, "sqlite-v2-stage3.sqlite", root, filepath.Base(catalogPath))
		copyHistoricalStateFixture(t, "sqlite-backup-v1-stage3.sqlite", root, backupName)
		backupPath := filepath.Join(root, backupName)
		file, err := os.OpenFile(backupPath, os.O_WRONLY|os.O_APPEND, 0) //nolint:gosec // exact test-owned path
		if err != nil {
			t.Fatal(err)
		}
		if _, err := file.Write([]byte("corrupt")); err != nil {
			_ = file.Close()
			t.Fatal(err)
		}
		if err := file.Close(); err != nil {
			t.Fatal(err)
		}
		before := snapshotMigrationDiagnosisFiles(t, root)
		if _, err := RestoreMigrationBackup(context.Background(), RestoreMigrationBackupConfig{
			Root: root, CatalogDatabasePath: catalogPath, DestinationPath: destinationPath,
			BackupAttemptID: strings.Repeat("2", 32),
		}); err == nil {
			t.Fatal("RestoreMigrationBackup(corrupt) error = nil")
		}
		if _, err := os.Lstat(destinationPath); !os.IsNotExist(err) {
			t.Fatalf("destination exists after rejected corruption: %v", err)
		}
		after := snapshotMigrationDiagnosisFiles(t, root)
		if !reflect.DeepEqual(after, before) {
			t.Fatalf("corruption rejection changed directory: before=%#v after=%#v", before, after)
		}
	})
}

func TestRestoreMigrationBackupRecoversOnlyExactPrivateInterruptedTemp(t *testing.T) {
	setup := func(t *testing.T) (string, string, string) {
		t.Helper()
		root := privateTempDir(t)
		catalogPath := filepath.Join(root, "amsftp.db")
		destinationPath := filepath.Join(root, "restored.db")
		backupName := ".amsftp-backup-v1-" + strings.Repeat("2", 32) + ".sqlite3"
		copyHistoricalStateFixture(t, "sqlite-v2-stage3.sqlite", root, filepath.Base(catalogPath))
		copyHistoricalStateFixture(t, "sqlite-backup-v1-stage3.sqlite", root, backupName)
		return root, catalogPath, destinationPath
	}
	t.Run("private partial is rebuilt", func(t *testing.T) {
		root, catalogPath, destinationPath := setup(t)
		tempPath := filepath.Join(root, filepath.Base(destinationPath)+".restore-v1-"+strings.Repeat("2", 32)+".tmp")
		if err := os.WriteFile(tempPath, []byte("partial"), 0o600); err != nil {
			t.Fatal(err)
		}
		if _, err := RestoreMigrationBackup(context.Background(), RestoreMigrationBackupConfig{
			Root: root, CatalogDatabasePath: catalogPath, DestinationPath: destinationPath,
			BackupAttemptID: strings.Repeat("2", 32),
		}); err != nil {
			t.Fatal(err)
		}
		if _, err := os.Lstat(tempPath); !os.IsNotExist(err) {
			t.Fatalf("temporary destination remains: %v", err)
		}
	})
	t.Run("wrong-mode partial is preserved", func(t *testing.T) {
		root, catalogPath, destinationPath := setup(t)
		tempPath := filepath.Join(root, filepath.Base(destinationPath)+".restore-v1-"+strings.Repeat("2", 32)+".tmp")
		if err := os.WriteFile(tempPath, []byte("untrusted partial"), 0o600); err != nil {
			t.Fatal(err)
		}
		if err := os.Chmod(tempPath, 0o644); err != nil { //nolint:gosec // deliberate persistent-file wrong-mode rejection fixture
			t.Fatal(err)
		}
		before := snapshotOneMigrationFile(t, tempPath)
		if _, err := RestoreMigrationBackup(context.Background(), RestoreMigrationBackupConfig{
			Root: root, CatalogDatabasePath: catalogPath, DestinationPath: destinationPath,
			BackupAttemptID: strings.Repeat("2", 32),
		}); err == nil {
			t.Fatal("RestoreMigrationBackup(wrong-mode temp) error = nil")
		}
		if after := snapshotOneMigrationFile(t, tempPath); after != before {
			t.Fatalf("wrong-mode temporary changed: before=%#v after=%#v", before, after)
		}
		if _, err := os.Lstat(destinationPath); !os.IsNotExist(err) {
			t.Fatalf("destination exists after wrong-mode temp rejection: %v", err)
		}
	})
}

func TestRestoreMigrationBackupRecoversEveryProcessDeathBoundary(t *testing.T) {
	for _, point := range []string{
		restoreFaultTempReserved,
		restoreFaultCopyComplete,
		restoreFaultTempSynced,
		restoreFaultTempVerified,
		restoreFaultFinalPublished,
		restoreFaultDirectorySynced,
	} {
		t.Run(point, func(t *testing.T) {
			root := privateTempDir(t)
			catalogPath := filepath.Join(root, "amsftp.db")
			destinationPath := filepath.Join(root, "restored.db")
			backupName := ".amsftp-backup-v1-" + strings.Repeat("2", 32) + ".sqlite3"
			copyHistoricalStateFixture(t, "sqlite-v2-stage3.sqlite", root, filepath.Base(catalogPath))
			copyHistoricalStateFixture(t, "sqlite-backup-v1-stage3.sqlite", root, backupName)
			sourceBefore := snapshotOneMigrationFile(t, catalogPath)
			backupBefore := snapshotOneMigrationFile(t, filepath.Join(root, backupName))
			runRestoreCrashHelper(t, root, point)

			if _, err := RestoreMigrationBackup(context.Background(), RestoreMigrationBackupConfig{
				Root: root, CatalogDatabasePath: catalogPath, DestinationPath: destinationPath,
				BackupAttemptID: strings.Repeat("2", 32),
			}); err != nil {
				t.Fatalf("retry after %s: %v", point, err)
			}
			if got := snapshotOneMigrationFile(t, catalogPath); got != sourceBefore {
				t.Fatalf("catalog changed across %s: before=%#v after=%#v", point, sourceBefore, got)
			}
			if got := snapshotOneMigrationFile(t, filepath.Join(root, backupName)); got != backupBefore {
				t.Fatalf("backup changed across %s: before=%#v after=%#v", point, backupBefore, got)
			}
			tempPath := filepath.Join(root, filepath.Base(destinationPath)+".restore-v1-"+strings.Repeat("2", 32)+".tmp")
			if _, err := os.Lstat(tempPath); !os.IsNotExist(err) {
				t.Fatalf("temporary destination remains after %s recovery: %v", point, err)
			}
			inspection, err := InspectMigrationStateReadOnly(context.Background(), MigrationInspectionConfig{Root: root, DatabasePath: destinationPath})
			if err != nil || inspection.Disposition != MigrationDispositionRestoredBackupHold {
				t.Fatalf("inspection after %s = %#v, error=%v", point, inspection, err)
			}
		})
	}
}

func TestRestoreMigrationBackupProcessDeathHelper(t *testing.T) {
	root := os.Getenv(restoreCrashRootEnvironment)
	if root == "" {
		return
	}
	point := os.Getenv(restoreCrashPointEnvironment)
	_, err := RestoreMigrationBackup(context.Background(), RestoreMigrationBackupConfig{
		Root: root, CatalogDatabasePath: filepath.Join(root, "amsftp.db"), DestinationPath: filepath.Join(root, "restored.db"),
		BackupAttemptID: strings.Repeat("2", 32),
		restoreFault: func(actual string) {
			if actual == point {
				os.Exit(restoreCrashExitCode)
			}
		},
	})
	if err != nil {
		t.Fatalf("restore crash helper: %v", err)
	}
	t.Fatalf("restore crash point %q was not reached", point)
}

func runRestoreCrashHelper(t *testing.T, root, point string) {
	t.Helper()
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	command := exec.Command(executable, "-test.run=^TestRestoreMigrationBackupProcessDeathHelper$") //nolint:gosec // exact current test binary
	command.Env = []string{
		restoreCrashRootEnvironment + "=" + root,
		restoreCrashPointEnvironment + "=" + point,
	}
	output, err := command.CombinedOutput()
	var exitError *exec.ExitError
	if !errors.As(err, &exitError) || exitError.ExitCode() != restoreCrashExitCode {
		t.Fatalf("restore crash helper point=%s error=%v output=%s", point, err, output)
	}
}

func snapshotOneMigrationFile(t *testing.T, path string) migrationDiagnosisFileSnapshot {
	t.Helper()
	root := filepath.Dir(path)
	for _, item := range snapshotMigrationDiagnosisFiles(t, root) {
		if item.Name == filepath.Base(path) {
			return item
		}
	}
	t.Fatalf("snapshot path %q was not found", path)
	return migrationDiagnosisFileSnapshot{}
}
