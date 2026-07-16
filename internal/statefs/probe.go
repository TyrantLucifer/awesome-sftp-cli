package statefs

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net/url"
	"os"
	"path/filepath"
	"time"

	"github.com/TyrantLucifer/awesome-mac-sftp/internal/platform"
	_ "github.com/TyrantLucifer/awesome-mac-sftp/internal/state/sqlite"
)

const (
	probePrefix        = ".amsftp-probe-v1-"
	probeSuffix        = ".sqlite3"
	probeRandomBytes   = 16
	probeBusyTimeoutMS = 100
)

type ProbeConfig struct {
	Random io.Reader
}

// ProbeAfterIdentity proves WAL visibility, writer exclusion, full file sync,
// and parent-directory sync using one exact random temporary database.
func ProbeAfterIdentity(ctx context.Context, root string, config ProbeConfig) (returnErr error) {
	if _, err := ValidateRoot(root); err != nil {
		return err
	}
	random := config.Random
	if random == nil {
		random = rand.Reader
	}
	id := make([]byte, probeRandomBytes)
	if _, err := io.ReadFull(random, id); err != nil {
		return fmt.Errorf("state capability probe: generate ID: %w", err)
	}
	path := filepath.Join(root, probePrefix+hex.EncodeToString(id)+probeSuffix)
	file, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_RDWR, 0o600) //nolint:gosec // exact owner-private probe mode
	if err != nil {
		return fmt.Errorf("state capability probe: create database: %w", err)
	}
	if err := file.Chmod(0o600); err != nil {
		_ = file.Close()
		_ = os.Remove(path)
		return fmt.Errorf("state capability probe: set database mode: %w", err)
	}
	if err := file.Close(); err != nil {
		_ = os.Remove(path)
		return fmt.Errorf("state capability probe: close initial database: %w", err)
	}
	defer func() {
		cleanupErr := cleanupProbe(path)
		returnErr = errors.Join(returnErr, cleanupErr)
	}()

	parent, err := openProbeDatabase(path)
	if err != nil {
		return err
	}
	defer parent.Close()

	journalMode := ""
	if err := parent.QueryRowContext(ctx, "PRAGMA journal_mode=WAL").Scan(&journalMode); err != nil {
		return fmt.Errorf("state capability probe: enable WAL: %w", err)
	}
	if journalMode != "wal" {
		return fmt.Errorf("state capability probe: journal mode %q, want wal", journalMode)
	}
	if _, err := parent.ExecContext(ctx, "PRAGMA wal_autocheckpoint=0"); err != nil {
		return fmt.Errorf("state capability probe: disable parent automatic checkpoint: %w", err)
	}
	var parentAutoCheckpoint int64
	if err := parent.QueryRowContext(ctx, "PRAGMA wal_autocheckpoint").Scan(&parentAutoCheckpoint); err != nil {
		return fmt.Errorf("state capability probe: read parent automatic checkpoint: %w", err)
	}
	if parentAutoCheckpoint != 0 {
		return fmt.Errorf("state capability probe: parent automatic checkpoint = %d, want 0", parentAutoCheckpoint)
	}
	if _, err := parent.ExecContext(ctx, "CREATE TABLE probe_marker(value TEXT NOT NULL)"); err != nil {
		return fmt.Errorf("state capability probe: create marker table: %w", err)
	}
	if err := requireTruncatedCheckpoint(ctx, parent); err != nil {
		return err
	}
	child, err := launchProbeChild(ctx, path)
	if err != nil {
		return err
	}
	defer child.abort()
	if _, err := parent.ExecContext(ctx, "INSERT INTO probe_marker(value) VALUES('parent-marker')"); err != nil {
		return fmt.Errorf("state capability probe: commit parent marker: %w", err)
	}
	walMetadata, err := os.Lstat(path + "-wal")
	if err != nil {
		return fmt.Errorf("state capability probe: inspect WAL: %w", err)
	}
	minimumWALBytes := int64(32 + 24 + statePageSize)
	if walMetadata.Size() < minimumWALBytes {
		return fmt.Errorf("state capability probe: WAL size %d, want at least %d", walMetadata.Size(), minimumWALBytes)
	}
	if err := child.roundTrip(probeCommandReadMarker, probeResponseOK); err != nil {
		return fmt.Errorf("state capability probe: child read marker from WAL: %w", err)
	}

	if _, err := parent.ExecContext(ctx, "BEGIN IMMEDIATE"); err != nil {
		return fmt.Errorf("state capability probe: reserve writer: %w", err)
	}
	busyStart := time.Now()
	busyErr := child.roundTrip(probeCommandBusyWrite, probeResponseBusy)
	busyElapsed := time.Since(busyStart)
	if _, rollbackErr := parent.ExecContext(ctx, "ROLLBACK"); rollbackErr != nil {
		return fmt.Errorf("state capability probe: release parent writer: %w", rollbackErr)
	}
	if busyErr != nil {
		return fmt.Errorf("state capability probe: child writer did not return bounded busy/locked: %w", busyErr)
	}
	if busyElapsed > time.Second {
		return fmt.Errorf("state capability probe: child busy took %s, want at most 1s", busyElapsed)
	}
	if err := child.roundTrip(probeCommandWrite, probeResponseOK); err != nil {
		return fmt.Errorf("state capability probe: child commit after release: %w", err)
	}
	var childRows int
	if err := parent.QueryRowContext(ctx, "SELECT count(*) FROM probe_marker WHERE value='child-marker'").Scan(&childRows); err != nil {
		return fmt.Errorf("state capability probe: parent read child marker: %w", err)
	}
	if childRows != 1 {
		return fmt.Errorf("state capability probe: child marker rows %d, want 1", childRows)
	}
	if err := child.finish(); err != nil {
		return err
	}

	for _, syncPath := range []string{path, path + "-wal"} {
		// syncPath is one of the exact random probe main/WAL paths derived above.
		syncFile, err := os.OpenFile(syncPath, os.O_RDWR, 0) //nolint:gosec
		if err != nil {
			return fmt.Errorf("state capability probe: open %q for full sync: %w", syncPath, err)
		}
		if err := fullSyncFile(syncFile); err != nil {
			_ = syncFile.Close()
			return fmt.Errorf("state capability probe: %w", err)
		}
		if err := syncFile.Close(); err != nil {
			return fmt.Errorf("state capability probe: close synced file %q: %w", syncPath, err)
		}
	}
	// root was canonicalized and trust-validated by ValidateRoot.
	directory, err := os.Open(root) //nolint:gosec
	if err != nil {
		return fmt.Errorf("state capability probe: open root for sync: %w", err)
	}
	if err := directory.Sync(); err != nil {
		_ = directory.Close()
		return fmt.Errorf("state capability probe: sync root: %w", err)
	}
	if err := directory.Close(); err != nil {
		return fmt.Errorf("state capability probe: close root: %w", err)
	}
	return nil
}

func openProbeDatabase(path string) (*sql.DB, error) {
	uri := &url.URL{Scheme: "file", Path: path}
	query := uri.Query()
	for _, pragma := range []string{
		"busy_timeout(100)",
		"checkpoint_fullfsync(1)",
		"foreign_keys(1)",
		"fullfsync(1)",
		"synchronous(FULL)",
		"trusted_schema(0)",
	} {
		query.Add("_pragma", pragma)
	}
	uri.RawQuery = query.Encode()
	database, err := sql.Open("sqlite", uri.String())
	if err != nil {
		return nil, fmt.Errorf("state capability probe: open database: %w", err)
	}
	database.SetMaxOpenConns(1)
	if err := database.Ping(); err != nil {
		_ = database.Close()
		return nil, fmt.Errorf("state capability probe: initialize database: %w", err)
	}
	return database, nil
}

func requireTruncatedCheckpoint(ctx context.Context, database *sql.DB) error {
	var busy, logFrames, checkpointed int64
	if err := database.QueryRowContext(ctx, "PRAGMA wal_checkpoint(TRUNCATE)").Scan(&busy, &logFrames, &checkpointed); err != nil {
		return fmt.Errorf("state capability probe: truncate WAL: %w", err)
	}
	if busy != 0 || logFrames != 0 || checkpointed != 0 {
		return fmt.Errorf("state capability probe: truncated checkpoint = (%d,%d,%d), want (0,0,0)", busy, logFrames, checkpointed)
	}
	return nil
}

func cleanupProbe(path string) error {
	var result error
	for _, candidate := range []string{path + "-wal", path + "-shm", path + "-journal", path} {
		metadata, err := os.Lstat(candidate)
		if errors.Is(err, os.ErrNotExist) {
			continue
		}
		if err != nil {
			result = errors.Join(result, fmt.Errorf("state capability probe: inspect cleanup %q: %w", candidate, err))
			continue
		}
		if !metadata.Mode().IsRegular() || metadata.Mode().Perm() != 0o600 {
			result = errors.Join(result, fmt.Errorf("state capability probe: refuse cleanup of unsafe %q", candidate))
			continue
		}
		if err := platform.ValidatePrivateFile(candidate, platform.ValidatePersistent); err != nil {
			result = errors.Join(result, fmt.Errorf("state capability probe: refuse cleanup of untrusted %q: %w", candidate, err))
			continue
		}
		if err := os.Remove(candidate); err != nil {
			result = errors.Join(result, fmt.Errorf("state capability probe: remove %q: %w", candidate, err))
		}
	}
	return result
}
