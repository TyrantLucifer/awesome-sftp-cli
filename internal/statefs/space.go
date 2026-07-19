package statefs

import (
	"context"
	"database/sql"
	"fmt"
	"math"

	"github.com/TyrantLucifer/awesome-sftp-cli/internal/state/migration"
)

const migrationSpaceReserveBytes = uint64(64 * 1024 * 1024)

type MigrationSpaceInput struct {
	PageCount         uint64
	PageSize          uint64
	AvailableBytes    uint64
	PendingWalBudgets []uint64
}

type MigrationSpaceReport struct {
	ExpectedBackupBytes uint64
	MaxPendingWalBytes  uint64
	ReserveBytes        uint64
	RequiredBytes       uint64
	AvailableBytes      uint64
}

func EvaluateMigrationSpace(input MigrationSpaceInput) (MigrationSpaceReport, error) {
	var report MigrationSpaceReport
	if input.PageCount == 0 || input.PageSize == 0 || len(input.PendingWalBudgets) == 0 {
		return report, fmt.Errorf("evaluate migration space: invalid page or pending-migration input")
	}
	backupBytes, ok := checkedMultiply(input.PageCount, input.PageSize)
	if !ok {
		return report, fmt.Errorf("evaluate migration space: backup size overflows uint64")
	}
	var maxPending uint64
	for _, budget := range input.PendingWalBudgets {
		if budget == 0 {
			return report, fmt.Errorf("evaluate migration space: zero pending WAL budget")
		}
		if budget > maxPending {
			maxPending = budget
		}
	}
	required, ok := checkedAdd(backupBytes, maxPending)
	if !ok {
		return report, fmt.Errorf("evaluate migration space: backup plus WAL budget overflows uint64")
	}
	required, ok = checkedAdd(required, migrationSpaceReserveBytes)
	if !ok {
		return report, fmt.Errorf("evaluate migration space: reserve addition overflows uint64")
	}
	report = MigrationSpaceReport{
		ExpectedBackupBytes: backupBytes,
		MaxPendingWalBytes:  maxPending,
		ReserveBytes:        migrationSpaceReserveBytes,
		RequiredBytes:       required,
		AvailableBytes:      input.AvailableBytes,
	}
	if input.AvailableBytes < required {
		return report, fmt.Errorf("evaluate migration space: available bytes %d are below required bytes %d", input.AvailableBytes, required)
	}
	return report, nil
}

func CheckMigrationSpace(ctx context.Context, connection *sql.Conn, root string, pending []migration.Migration) (MigrationSpaceReport, error) {
	if connection == nil || root == "" || len(pending) == 0 {
		return MigrationSpaceReport{}, fmt.Errorf("check migration space: invalid input")
	}
	budgets := make([]uint64, len(pending))
	for index, item := range pending {
		if item.Version <= 1 || item.MaxMigrationWalBytes == 0 {
			return MigrationSpaceReport{}, fmt.Errorf("check migration space: pending migration %d has invalid version or WAL budget", index)
		}
		if _, err := migration.Checksum(item); err != nil {
			return MigrationSpaceReport{}, fmt.Errorf("check migration space: pending migration %d: %w", index, err)
		}
		budgets[index] = item.MaxMigrationWalBytes
	}
	var pageCount, pageSize int64
	if err := connection.QueryRowContext(ctx, "PRAGMA page_count").Scan(&pageCount); err != nil {
		return MigrationSpaceReport{}, fmt.Errorf("check migration space: read page_count: %w", err)
	}
	if err := connection.QueryRowContext(ctx, "PRAGMA page_size").Scan(&pageSize); err != nil {
		return MigrationSpaceReport{}, fmt.Errorf("check migration space: read page_size: %w", err)
	}
	if pageCount <= 0 || pageSize <= 0 {
		return MigrationSpaceReport{}, fmt.Errorf("check migration space: invalid page_count/page_size %d/%d", pageCount, pageSize)
	}
	available, err := availableFilesystemBytes(root)
	if err != nil {
		return MigrationSpaceReport{}, fmt.Errorf("check migration space: %w", err)
	}
	return EvaluateMigrationSpace(MigrationSpaceInput{
		PageCount:         uint64(pageCount), //nolint:gosec // positivity checked above
		PageSize:          uint64(pageSize),  //nolint:gosec // positivity checked above
		AvailableBytes:    available,
		PendingWalBudgets: budgets,
	})
}

func checkedMultiply(left, right uint64) (uint64, bool) {
	if left != 0 && right > math.MaxUint64/left {
		return 0, false
	}
	return left * right, true
}

func checkedAdd(left, right uint64) (uint64, bool) {
	if left > math.MaxUint64-right {
		return 0, false
	}
	return left + right, true
}
