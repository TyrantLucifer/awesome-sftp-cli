package main

import (
	"context"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/TyrantLucifer/awesome-mac-sftp/internal/statefs"
	"github.com/TyrantLucifer/awesome-mac-sftp/internal/testkit"
)

func TestSeedAndVerifyPinnedCacheEvidenceThroughProductionOwners(t *testing.T) {
	t.Parallel()

	fixture := newLifecycleFixture(t)
	if err := execute(context.Background(), []string{
		"seed", "--database", fixture.database, "--cache-root", fixture.cacheRoot, "--record", fixture.record,
	}, io.Discard); err != nil {
		t.Fatalf("seed pinned cache: %v", err)
	}
	if err := execute(context.Background(), []string{
		"verify", "--database", fixture.database, "--cache-root", fixture.cacheRoot, "--record", fixture.record,
	}, io.Discard); err != nil {
		t.Fatalf("verify pinned cache: %v", err)
	}

	info, err := os.Lstat(fixture.record)
	if err != nil {
		t.Fatalf("stat evidence record: %v", err)
	}
	if !info.Mode().IsRegular() || info.Mode().Perm() != 0o600 {
		t.Fatalf("evidence mode = %v", info.Mode())
	}
	content, err := os.ReadFile(fixture.record)
	if err != nil {
		t.Fatalf("read evidence record: %v", err)
	}
	var record evidenceRecord
	if err := json.Unmarshal(content, &record); err != nil {
		t.Fatalf("decode evidence record: %v", err)
	}
	if record.Schema != evidenceSchema || record.EntryID == "" || record.BlobID == "" || record.ContentSHA256 == "" {
		t.Fatalf("incomplete evidence record = %#v", record)
	}
}

func TestVerifyRejectsTamperedPinnedCacheEvidence(t *testing.T) {
	t.Parallel()

	fixture := newLifecycleFixture(t)
	if err := execute(context.Background(), []string{
		"seed", "--database", fixture.database, "--cache-root", fixture.cacheRoot, "--record", fixture.record,
	}, io.Discard); err != nil {
		t.Fatalf("seed pinned cache: %v", err)
	}
	content, err := os.ReadFile(fixture.record)
	if err != nil {
		t.Fatal(err)
	}
	var record evidenceRecord
	if err := json.Unmarshal(content, &record); err != nil {
		t.Fatal(err)
	}
	record.BlobID = strings.Repeat("0", 64)
	tampered, err := json.Marshal(record)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(fixture.record, append(tampered, '\n'), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := execute(context.Background(), []string{
		"verify", "--database", fixture.database, "--cache-root", fixture.cacheRoot, "--record", fixture.record,
	}, io.Discard); err == nil || !strings.Contains(err.Error(), "blob identity") {
		t.Fatalf("verify tampered evidence error = %v", err)
	}
}

type lifecycleFixture struct {
	database  string
	cacheRoot string
	record    string
}

func newLifecycleFixture(t *testing.T) lifecycleFixture {
	t.Helper()
	root := testkit.PersistentTempDir(t)
	stateRoot := filepath.Join(root, "state")
	cacheRoot := filepath.Join(root, "cache")
	for _, directory := range []string{stateRoot, cacheRoot} {
		if err := os.Mkdir(directory, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	database := filepath.Join(stateRoot, "amsftp.db")
	db, _, err := statefs.Initialize(context.Background(), statefs.InitializeConfig{
		Root: stateRoot, DatabasePath: database, Now: time.Unix(1_800_000_000, 0).UTC(),
	})
	if err != nil {
		t.Fatalf("initialize state: %v", err)
	}
	if err := db.Close(); err != nil {
		t.Fatalf("close initialized state: %v", err)
	}
	return lifecycleFixture{database: database, cacheRoot: cacheRoot, record: filepath.Join(root, "pinned-cache-evidence.json")}
}
