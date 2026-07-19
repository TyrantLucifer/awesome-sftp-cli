package statefs

import (
	"context"
	"math"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/TyrantLucifer/awesome-sftp-cli/internal/state/migration"
)

func TestEvaluateMigrationSpaceUsesLargestPendingBudgetAndExactBoundary(t *testing.T) {
	t.Parallel()

	want := 10*4096 + 2*1024*1024 + migrationSpaceReserveBytes
	report, err := EvaluateMigrationSpace(MigrationSpaceInput{
		PageCount: 10, PageSize: 4096, AvailableBytes: want,
		PendingWalBudgets: []uint64{1024 * 1024, 2 * 1024 * 1024, 512 * 1024},
	})
	if err != nil {
		t.Fatalf("EvaluateMigrationSpace(exact boundary): %v", err)
	}
	if report.ExpectedBackupBytes != 10*4096 || report.MaxPendingWalBytes != 2*1024*1024 || report.RequiredBytes != want || report.AvailableBytes != want {
		t.Fatalf("space report = %#v", report)
	}
	if _, err := EvaluateMigrationSpace(MigrationSpaceInput{
		PageCount: 10, PageSize: 4096, AvailableBytes: want - 1,
		PendingWalBudgets: []uint64{2 * 1024 * 1024},
	}); err == nil {
		t.Fatal("EvaluateMigrationSpace(one byte short) error = nil")
	}
}

func TestEvaluateMigrationSpaceRejectsOverflowAndInvalidInputs(t *testing.T) {
	t.Parallel()

	for name, input := range map[string]MigrationSpaceInput{
		"zero page size":        {PageCount: 1, AvailableBytes: math.MaxUint64, PendingWalBudgets: []uint64{1}},
		"no pending budget":     {PageCount: 1, PageSize: 4096, AvailableBytes: math.MaxUint64},
		"zero pending budget":   {PageCount: 1, PageSize: 4096, AvailableBytes: math.MaxUint64, PendingWalBudgets: []uint64{0}},
		"backup multiplication": {PageCount: math.MaxUint64, PageSize: 2, AvailableBytes: math.MaxUint64, PendingWalBudgets: []uint64{1}},
		"required addition":     {PageCount: math.MaxUint64 - migrationSpaceReserveBytes, PageSize: 1, AvailableBytes: math.MaxUint64, PendingWalBudgets: []uint64{1}},
	} {
		input := input
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			if _, err := EvaluateMigrationSpace(input); err == nil {
				t.Fatal("EvaluateMigrationSpace() error = nil")
			}
		})
	}
}

func TestCheckMigrationSpaceReadsLiveDatabaseWithoutMutation(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	root := privateTempDir(t)
	path := filepath.Join(root, "amsftp.db")
	database, _, err := Initialize(ctx, InitializeConfig{
		Root: root, DatabasePath: path,
		Random: strings.NewReader(strings.Repeat("h", probeRandomBytes+16)), Now: time.Unix(800, 0),
	})
	if err != nil {
		t.Fatalf("Initialize(): %v", err)
	}
	defer func() {
		if err := database.Close(); err != nil {
			t.Errorf("close database: %v", err)
		}
	}()
	connection, err := database.Conn(ctx)
	if err != nil {
		t.Fatalf("reserve connection: %v", err)
	}
	defer func() {
		if err := connection.Close(); err != nil {
			t.Errorf("close connection: %v", err)
		}
	}()
	report, err := CheckMigrationSpace(ctx, connection, root, []migration.Migration{{
		Version: 2, Name: "space", Statements: []string{"CREATE TABLE space_gate(id INTEGER PRIMARY KEY) STRICT"}, MaxMigrationWalBytes: 4096,
	}})
	if err != nil {
		t.Fatalf("CheckMigrationSpace(): %v", err)
	}
	if report.ExpectedBackupBytes == 0 || report.MaxPendingWalBytes != 4096 || report.RequiredBytes <= migrationSpaceReserveBytes {
		t.Fatalf("live space report = %#v", report)
	}
	var attempts int
	if err := connection.QueryRowContext(ctx, "SELECT count(*) FROM migration_attempts").Scan(&attempts); err != nil || attempts != 0 {
		t.Fatalf("attempt rows after read-only gate = %d, error=%v", attempts, err)
	}
}
