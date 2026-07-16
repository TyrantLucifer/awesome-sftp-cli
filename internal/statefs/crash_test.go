package statefs

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/TyrantLucifer/awesome-mac-sftp/internal/state/migration"
)

const (
	crashHelperModeEnvironment  = "AMSFTP_STATEFS_CRASH_HELPER"
	crashHelperRootEnvironment  = "AMSFTP_STATEFS_CRASH_ROOT"
	crashHelperPointEnvironment = "AMSFTP_STATEFS_CRASH_POINT"
	crashHelperExitCode         = 91
)

func TestBootstrapRecoversFromProcessDeathAtDurabilityBoundaries(t *testing.T) {
	points := []string{
		"intent_persisted",
		"temp_created",
		"version_committed",
		"temp_synced",
		"final_published",
		"final_persisted",
		"intent_removed",
	}
	for _, point := range points {
		t.Run(point, func(t *testing.T) {
			root := privateTempDir(t)
			runStateFSCrashHelper(t, "bootstrap", root, point)
			if point == "intent_persisted" || point == "temp_created" || point == "version_committed" || point == "temp_synced" {
				runStateFSCrashHelper(t, "bootstrap", root, point)
				if entries := directoryEntries(t, root); len(entries) > 5 {
					t.Fatalf("consecutive crash artifacts are unbounded: %v", entries)
				}
			}

			path := filepath.Join(root, "amsftp.db")
			database, report, err := Initialize(context.Background(), InitializeConfig{
				Root: root, DatabasePath: path,
				Random: strings.NewReader(strings.Repeat("r", probeRandomBytes+16)), Now: time.Unix(2_000, 0),
			})
			if err != nil {
				t.Fatalf("Initialize(after %s crash): %v", point, err)
			}
			if err := database.Close(); err != nil {
				t.Fatalf("close recovered database: %v", err)
			}
			if point == "final_published" || point == "final_persisted" {
				if !report.Recovered || report.Bootstrapped {
					t.Fatalf("published recovery report = %#v", report)
				}
			}
			if report.SchemaHead != 1 {
				t.Fatalf("recovered schema head = %d, want 1", report.SchemaHead)
			}
			assertOnlyFinalStateFile(t, root)

			reopened, secondReport, err := Initialize(context.Background(), InitializeConfig{
				Root: root, DatabasePath: path,
				Random: strings.NewReader(strings.Repeat("s", probeRandomBytes)), Now: time.Unix(2_001, 0),
			})
			if err != nil {
				t.Fatalf("Initialize(second restart after %s): %v", point, err)
			}
			if err := reopened.Close(); err != nil {
				t.Fatalf("close second restart: %v", err)
			}
			if secondReport.Recovered || secondReport.Bootstrapped || secondReport.SchemaHead != 1 {
				t.Fatalf("second restart report = %#v", secondReport)
			}
			assertOnlyFinalStateFile(t, root)
		})
	}
}

func TestRuntimeRecoversCommittedWALAfterProcessDeath(t *testing.T) {
	root := privateTempDir(t)
	path := filepath.Join(root, "amsftp.db")
	database, _, err := Initialize(context.Background(), InitializeConfig{
		Root: root, DatabasePath: path,
		Random: strings.NewReader(strings.Repeat("w", probeRandomBytes+16)), Now: time.Unix(2_100, 0),
	})
	if err != nil {
		t.Fatalf("Initialize(fixture): %v", err)
	}
	if err := database.Close(); err != nil {
		t.Fatalf("close fixture: %v", err)
	}

	runStateFSCrashHelper(t, "wal", root, "wal_committed")
	if identity, err := PreflightIdentity(path); err != nil || !identity.HasSidecars {
		t.Fatalf("post-crash identity = %#v, error=%v, want sidecars", identity, err)
	}

	recovered, report, err := Initialize(context.Background(), InitializeConfig{
		Root: root, DatabasePath: path,
		Random: strings.NewReader(strings.Repeat("x", probeRandomBytes)), Now: time.Unix(2_101, 0),
	})
	if err != nil {
		t.Fatalf("Initialize(WAL recovery): %v", err)
	}
	if report.Bootstrapped || report.SchemaHead != 1 {
		t.Fatalf("WAL recovery report = %#v", report)
	}
	var count int
	if err := recovered.QueryRowContext(context.Background(), "SELECT count(*) FROM operation_plans WHERE plan_id='crash-plan'").Scan(&count); err != nil {
		t.Fatalf("read recovered WAL row: %v", err)
	}
	if count != 1 {
		t.Fatalf("recovered WAL row count = %d, want 1", count)
	}
	if err := recovered.Close(); err != nil {
		t.Fatalf("close WAL recovery: %v", err)
	}
	assertOnlyFinalStateFile(t, root)
}

func TestStateFSProcessDeathHelper(t *testing.T) {
	mode := os.Getenv(crashHelperModeEnvironment)
	if mode == "" {
		return
	}
	root := os.Getenv(crashHelperRootEnvironment)
	point := os.Getenv(crashHelperPointEnvironment)
	path := filepath.Join(root, "amsftp.db")
	switch mode {
	case "bootstrap":
		database, _, err := Initialize(context.Background(), InitializeConfig{
			Root: root, DatabasePath: path,
			Random: strings.NewReader(strings.Repeat("c", probeRandomBytes+16)), Now: time.Unix(1_999, 0),
			bootstrapFault: func(actual string) {
				if actual == point {
					os.Exit(crashHelperExitCode)
				}
			},
		})
		if err != nil {
			t.Fatalf("bootstrap helper: %v", err)
		}
		_ = database.Close()
	case "wal":
		database, err := openRuntimeDatabase(context.Background(), path, []migration.Migration{migration.Version1()}, map[uint64][]byte{1: migration.Version1SchemaContract()})
		if err != nil {
			t.Fatalf("open WAL helper database: %v", err)
		}
		if _, err := database.ExecContext(context.Background(), `INSERT INTO operation_plans(plan_id, request_id, kind, source_json, destination_json, route, verification, conflict_policy, risk_class, frozen_at_unix) VALUES('crash-plan', 'crash-request', 'copy', '{}', '{}', 'local', 'metadata', 'skip', 'normal', 2100)`); err != nil {
			t.Fatalf("commit WAL helper row: %v", err)
		}
		os.Exit(crashHelperExitCode)
	default:
		t.Fatalf("unknown crash helper mode %q", mode)
	}
	t.Fatalf("crash point %q was not reached", point)
}

func runStateFSCrashHelper(t *testing.T, mode, root, point string) {
	t.Helper()
	executable, err := os.Executable()
	if err != nil {
		t.Fatalf("resolve test executable: %v", err)
	}
	command := exec.Command(executable, "-test.run=^TestStateFSProcessDeathHelper$") //nolint:gosec // exact current test binary
	command.Env = []string{
		crashHelperModeEnvironment + "=" + mode,
		crashHelperRootEnvironment + "=" + root,
		crashHelperPointEnvironment + "=" + point,
	}
	output, err := command.CombinedOutput()
	var exitError *exec.ExitError
	if !errors.As(err, &exitError) || exitError.ExitCode() != crashHelperExitCode {
		t.Fatalf("crash helper mode=%s point=%s error=%v output=%s", mode, point, err, output)
	}
}

func assertOnlyFinalStateFile(t *testing.T, root string) {
	t.Helper()
	entries := directoryEntries(t, root)
	if len(entries) != 1 || entries[0] != "amsftp.db" {
		t.Fatalf("state root after recovery = %v, want only amsftp.db", entries)
	}
	for _, suffix := range []string{"-wal", "-shm", "-journal"} {
		if _, err := os.Lstat(filepath.Join(root, "amsftp.db") + suffix); !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("state sidecar %s remains: %v", suffix, err)
		}
	}
}
