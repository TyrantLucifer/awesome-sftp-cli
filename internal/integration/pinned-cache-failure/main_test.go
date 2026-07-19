package main

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/TyrantLucifer/awesome-sftp-cli/internal/state/migration"
	"github.com/TyrantLucifer/awesome-sftp-cli/internal/statefs"
	"github.com/TyrantLucifer/awesome-sftp-cli/internal/testkit"
)

func TestPrepareFailedUpgradeRequiresExplicitResumeAndReusesVerifiedBackup(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	root := testkit.PersistentTempDir(t)
	stateRoot := filepath.Join(root, "state")
	if err := os.Mkdir(stateRoot, 0o700); err != nil {
		t.Fatal(err)
	}
	database := filepath.Join(stateRoot, "amsftp.db")
	migrations := []migration.Migration{migration.Version1(), migration.Version2(), migration.Version3()}
	contracts := map[uint64][]byte{
		1: migration.Version1SchemaContract(),
		2: migration.Version2SchemaContract(),
		3: migration.Version3SchemaContract(),
	}
	db, report, err := statefs.Initialize(ctx, statefs.InitializeConfig{
		Root: stateRoot, DatabasePath: database, Now: time.Unix(1_800_000_100, 0).UTC(),
		Migrations: migrations, SchemaContracts: contracts,
	})
	if err != nil {
		t.Fatalf("initialize Version 3 source: %v", err)
	}
	if report.SchemaHead != 3 {
		t.Fatalf("source report = %#v", report)
	}
	if err := db.Close(); err != nil {
		t.Fatalf("close Version 3 source: %v", err)
	}

	failure, err := prepareFailedUpgrade(ctx, failureConfig{
		StateRoot: stateRoot, DatabasePath: database, Now: time.Unix(1_800_000_101, 0).UTC(),
	})
	if err != nil {
		t.Fatalf("prepare failed upgrade: %v", err)
	}
	if failure.OriginalHead != 3 || failure.TargetHead != 4 || failure.Status != "failed" || failure.BackupBasename == "" || failure.BackupSHA256 == "" {
		t.Fatalf("failure report = %#v", failure)
	}
	backupPath := filepath.Join(stateRoot, failure.BackupBasename)
	before, err := os.Lstat(backupPath)
	if err != nil {
		t.Fatalf("stat verified backup: %v", err)
	}

	_, _, err = statefs.Initialize(ctx, statefs.InitializeConfig{
		Root: stateRoot, DatabasePath: database, Now: time.Unix(1_800_000_102, 0).UTC(),
	})
	if !errors.Is(err, statefs.ErrExplicitMigrationResumeRequired) {
		t.Fatalf("implicit recovery error = %v", err)
	}

	resumed, resumedReport, err := statefs.Initialize(ctx, statefs.InitializeConfig{
		Root: stateRoot, DatabasePath: database, Now: time.Unix(1_800_000_103, 0).UTC(),
		ExplicitMigrationResume: true,
	})
	if err != nil {
		t.Fatalf("explicit recovery: %v", err)
	}
	if resumedReport.SchemaHead != 4 {
		t.Fatalf("resumed report = %#v", resumedReport)
	}
	if err := resumed.Close(); err != nil {
		t.Fatalf("close resumed state: %v", err)
	}
	after, err := os.Lstat(backupPath)
	if err != nil {
		t.Fatalf("restat verified backup: %v", err)
	}
	if !os.SameFile(before, after) {
		t.Fatal("explicit recovery replaced the verified backup")
	}
	if failure.AttemptID != strings.Repeat("6", 32) {
		t.Fatalf("attempt ID = %q", failure.AttemptID)
	}
}
