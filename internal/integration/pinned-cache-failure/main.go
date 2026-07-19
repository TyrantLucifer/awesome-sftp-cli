package main

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"net/url"
	"os"
	"path/filepath"
	"time"

	"github.com/TyrantLucifer/awesome-mac-sftp/internal/state/migration"
	_ "github.com/TyrantLucifer/awesome-mac-sftp/internal/state/sqlite"
	"github.com/TyrantLucifer/awesome-mac-sftp/internal/statefs"
)

const failedAttemptID = "66666666666666666666666666666666"

type failureConfig struct {
	StateRoot    string
	DatabasePath string
	Now          time.Time
}

type failureReport struct {
	Schema         string `json:"schema"`
	AttemptID      string `json:"attempt_id"`
	OriginalHead   uint64 `json:"original_head"`
	TargetHead     uint64 `json:"target_head"`
	Status         string `json:"status"`
	BackupBasename string `json:"backup_basename"`
	BackupSHA256   string `json:"backup_sha256"`
}

func main() {
	if err := execute(context.Background(), os.Args[1:], os.Stdout); err != nil {
		_, _ = fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func execute(ctx context.Context, args []string, output io.Writer) error {
	if ctx == nil || output == nil || len(args) == 0 || args[0] != "prepare" {
		return fmt.Errorf("usage: pinned-cache-failure prepare --state-root PATH --database PATH")
	}
	flags := flag.NewFlagSet("prepare", flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	config := failureConfig{Now: time.Now().UTC()}
	flags.StringVar(&config.StateRoot, "state-root", "", "state root")
	flags.StringVar(&config.DatabasePath, "database", "", "database path")
	if err := flags.Parse(args[1:]); err != nil {
		return fmt.Errorf("parse failed upgrade arguments: %w", err)
	}
	if flags.NArg() != 0 {
		return fmt.Errorf("parse failed upgrade arguments: unexpected positional arguments")
	}
	report, err := prepareFailedUpgrade(ctx, config)
	if err != nil {
		return err
	}
	if err := json.NewEncoder(output).Encode(report); err != nil {
		return fmt.Errorf("encode failed upgrade report: %w", err)
	}
	return nil
}

func prepareFailedUpgrade(ctx context.Context, config failureConfig) (failureReport, error) {
	if ctx == nil || config.StateRoot == "" || !filepath.IsAbs(config.StateRoot) || filepath.Clean(config.StateRoot) != config.StateRoot ||
		config.DatabasePath != filepath.Join(config.StateRoot, filepath.Base(config.DatabasePath)) {
		return failureReport{}, fmt.Errorf("prepare failed upgrade: state root and direct-child database must be canonical and absolute")
	}
	migrations, contracts := migration.CompiledSet()
	if len(migrations) != 4 || migration.SchemaHead != 4 {
		return failureReport{}, fmt.Errorf("prepare failed upgrade: compiled target is not Version 4")
	}
	database, err := sql.Open("sqlite", failureSQLiteDSN(config.DatabasePath))
	if err != nil {
		return failureReport{}, fmt.Errorf("prepare failed upgrade: open database: %w", err)
	}
	database.SetMaxOpenConns(4)
	defer database.Close()
	connection, err := database.Conn(ctx)
	if err != nil {
		return failureReport{}, fmt.Errorf("prepare failed upgrade: reserve connection: %w", err)
	}
	defer connection.Close()
	if err := migration.ValidateHead(ctx, connection, migrations, contracts, 3); err != nil {
		return failureReport{}, fmt.Errorf("prepare failed upgrade: validate Version 3 source: %w", err)
	}
	digests, err := migration.SchemaContractDigests(migrations, contracts)
	if err != nil {
		return failureReport{}, fmt.Errorf("prepare failed upgrade: schema contract digests: %w", err)
	}
	setDigest, err := migration.MigrationSetDigest(3, 4, migrations, digests)
	if err != nil {
		return failureReport{}, fmt.Errorf("prepare failed upgrade: migration-set digest: %w", err)
	}
	request := migration.AttemptRequest{AttemptID: failedAttemptID, OriginalHead: 3, TargetHead: 4, MigrationSetDigest: setDigest}
	attempt, _, err := migration.PrepareAttempt(ctx, connection, request)
	if err != nil {
		return failureReport{}, fmt.Errorf("prepare failed upgrade: persist attempt: %w", err)
	}
	_, backupDigest, err := statefs.CreateMigrationBackup(ctx, statefs.BackupConfig{
		Root: config.StateRoot, Source: database, Attempt: attempt, CreatedAt: config.Now,
		Migrations: migrations, SchemaContracts: contracts,
	})
	if err != nil {
		return failureReport{}, fmt.Errorf("prepare failed upgrade: create verified backup: %w", err)
	}
	attempt, err = migration.MarkAttemptFailed(ctx, connection, failedAttemptID, "stage6_rehearsal")
	if err != nil {
		return failureReport{}, fmt.Errorf("prepare failed upgrade: persist failure boundary: %w", err)
	}
	if attempt.OriginalHead != 3 || attempt.TargetHead != 4 {
		return failureReport{}, fmt.Errorf("prepare failed upgrade: persisted attempt heads changed")
	}
	return failureReport{
		Schema: "amsftp-failed-upgrade-rehearsal-v1", AttemptID: attempt.AttemptID,
		OriginalHead: 3, TargetHead: 4, Status: string(attempt.Status),
		BackupBasename: attempt.ReservedBackupBasename, BackupSHA256: encodeDigest(backupDigest),
	}, nil
}

func failureSQLiteDSN(path string) string {
	location := (&url.URL{Scheme: "file", Path: path}).String()
	return location + "?_pragma=foreign_keys(1)&_pragma=journal_mode(WAL)&_pragma=synchronous(FULL)&_pragma=busy_timeout(5000)"
}

func encodeDigest(digest [sha256.Size]byte) string { return hex.EncodeToString(digest[:]) }
