package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/TyrantLucifer/awesome-mac-sftp/internal/cache"
	"github.com/TyrantLucifer/awesome-mac-sftp/internal/cachefs"
	"github.com/TyrantLucifer/awesome-mac-sftp/internal/cachemanager"
	"github.com/TyrantLucifer/awesome-mac-sftp/internal/domain"
	"github.com/TyrantLucifer/awesome-mac-sftp/internal/state/cachestore"
	_ "github.com/TyrantLucifer/awesome-mac-sftp/internal/state/sqlite"
)

const (
	evidenceSchema    = "amsftp-pinned-cache-lifecycle-v1"
	evidenceEndpoint  = "ep_aaaaaaaaaaaaaaaaaaaaaaaaaa"
	evidencePath      = "/stage6/pinned-cache-upgrade.txt"
	evidenceWorkspace = "stage6-upgrade-recovery"
	maxEvidenceBytes  = 32 * 1024
)

var evidenceContent = []byte("Stage 5 pinned offline bytes survive upgrade, failed recovery, and rollback.\n")

type evidenceRecord struct {
	Schema        string `json:"schema"`
	EndpointID    string `json:"endpoint_id"`
	CanonicalPath string `json:"canonical_path"`
	WorkspaceID   string `json:"workspace_id"`
	EntryID       string `json:"entry_id"`
	BlobID        string `json:"blob_id"`
	ContentSHA256 string `json:"content_sha256"`
	SizeBytes     int64  `json:"size_bytes"`
}

type commandConfig struct {
	database  string
	cacheRoot string
	record    string
}

type fixedClock struct{ now time.Time }

func (clock fixedClock) Now() time.Time { return clock.now }

type owners struct {
	database *sql.DB
	files    *cachefs.Store
	manager  *cachemanager.Manager
}

func main() {
	if err := execute(context.Background(), os.Args[1:], os.Stdout); err != nil {
		_, _ = fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func execute(ctx context.Context, args []string, output io.Writer) error {
	if ctx == nil || output == nil {
		return fmt.Errorf("pinned cache lifecycle: nil context or output")
	}
	if len(args) == 0 || (args[0] != "seed" && args[0] != "verify") {
		return fmt.Errorf("usage: pinned-cache-lifecycle (seed|verify) --database PATH --cache-root PATH --record PATH")
	}
	flags := flag.NewFlagSet(args[0], flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	config := commandConfig{}
	flags.StringVar(&config.database, "database", "", "database path")
	flags.StringVar(&config.cacheRoot, "cache-root", "", "cache root")
	flags.StringVar(&config.record, "record", "", "evidence record")
	if err := flags.Parse(args[1:]); err != nil {
		return fmt.Errorf("parse pinned cache lifecycle arguments: %w", err)
	}
	if flags.NArg() != 0 {
		return fmt.Errorf("parse pinned cache lifecycle arguments: unexpected positional arguments")
	}
	if err := validateConfig(config); err != nil {
		return err
	}
	var (
		record evidenceRecord
		err    error
	)
	if args[0] == "seed" {
		record, err = seed(ctx, config)
	} else {
		record, err = verify(ctx, config)
	}
	if err != nil {
		return err
	}
	return writeJSON(output, record)
}

func validateConfig(config commandConfig) error {
	for name, path := range map[string]string{
		"database": config.database, "cache root": config.cacheRoot, "record": config.record,
	} {
		if path == "" || !filepath.IsAbs(path) || filepath.Clean(path) != path {
			return fmt.Errorf("pinned cache lifecycle: %s must be canonical and absolute", name)
		}
	}
	return nil
}

func seed(ctx context.Context, config commandConfig) (evidenceRecord, error) {
	owned, err := openOwners(ctx, config)
	if err != nil {
		return evidenceRecord{}, err
	}
	defer owned.close()

	location, fingerprint, digest, err := evidenceIdentity()
	if err != nil {
		return evidenceRecord{}, err
	}
	expectedSize := int64(len(evidenceContent))
	publication, err := owned.manager.PublishComplete(ctx, cachemanager.PublishRequest{
		Location: location, SourceFingerprint: fingerprint,
		WorkspaceID: cache.WorkspaceID(evidenceWorkspace), Policy: cache.PolicyPinnedOffline, Pinned: true,
		Source: bytes.NewReader(evidenceContent), MaxBytes: expectedSize, ExpectedSize: &expectedSize,
	})
	if err != nil {
		return evidenceRecord{}, fmt.Errorf("seed pinned cache through production owners: %w", err)
	}
	if publication.Entry.Policy != cache.PolicyPinnedOffline || !publication.Entry.Pinned || publication.Entry.BlobID != publication.Blob.ID {
		return evidenceRecord{}, fmt.Errorf("seed pinned cache: publication lost pinned ownership")
	}
	record := evidenceRecord{
		Schema: evidenceSchema, EndpointID: evidenceEndpoint, CanonicalPath: evidencePath, WorkspaceID: evidenceWorkspace,
		EntryID: string(publication.Entry.ID), BlobID: string(publication.Blob.ID), ContentSHA256: digest, SizeBytes: expectedSize,
	}
	if err := publishEvidence(config.record, record); err != nil {
		return evidenceRecord{}, err
	}
	return record, nil
}

func verify(ctx context.Context, config commandConfig) (evidenceRecord, error) {
	record, err := readEvidence(config.record)
	if err != nil {
		return evidenceRecord{}, err
	}
	owned, err := openOwners(ctx, config)
	if err != nil {
		return evidenceRecord{}, err
	}
	defer owned.close()

	location, _, digest, err := evidenceIdentity()
	if err != nil {
		return evidenceRecord{}, err
	}
	resolved, err := owned.manager.ResolvePinnedOffline(ctx, cachemanager.PinnedOfflineRequest{
		Location: location, WorkspaceID: cache.WorkspaceID(evidenceWorkspace),
	})
	if err != nil {
		return evidenceRecord{}, fmt.Errorf("verify pinned cache through production owners: %w", err)
	}
	if string(resolved.Entry.ID) != record.EntryID {
		return evidenceRecord{}, fmt.Errorf("verify pinned cache: entry identity got %q want %q", resolved.Entry.ID, record.EntryID)
	}
	if string(resolved.Entry.BlobID) != record.BlobID {
		return evidenceRecord{}, fmt.Errorf("verify pinned cache: blob identity got %q want %q", resolved.Entry.BlobID, record.BlobID)
	}
	if resolved.Entry.Policy != cache.PolicyPinnedOffline || !resolved.Entry.Pinned || resolved.Entry.Freshness != cache.EntryUnknown {
		return evidenceRecord{}, fmt.Errorf("verify pinned cache: resolved ownership = %#v", resolved.Entry)
	}
	if resolved.SourceFingerprint.Strength() != domain.FingerprintStrong || resolved.SourceFingerprint.HashHex == nil ||
		*resolved.SourceFingerprint.HashHex != digest || resolved.SourceFingerprint.Size == nil || *resolved.SourceFingerprint.Size != uint64(len(evidenceContent)) {
		return evidenceRecord{}, fmt.Errorf("verify pinned cache: source fingerprint differs from frozen identity")
	}
	file, info, err := owned.files.OpenBlob(resolved.Entry.BlobID)
	if err != nil {
		return evidenceRecord{}, fmt.Errorf("verify pinned cache blob: %w", err)
	}
	content, readErr := io.ReadAll(io.LimitReader(file, int64(len(evidenceContent))+1))
	closeErr := file.Close()
	if readErr != nil || closeErr != nil {
		return evidenceRecord{}, fmt.Errorf("verify pinned cache blob read: %w", errors.Join(readErr, closeErr))
	}
	if info.Identity.ID != resolved.Entry.BlobID || info.Identity.Size != int64(len(evidenceContent)) || !bytes.Equal(content, evidenceContent) {
		return evidenceRecord{}, fmt.Errorf("verify pinned cache: exact blob bytes or identity differ")
	}
	return record, nil
}

func evidenceIdentity() (domain.Location, domain.Fingerprint, string, error) {
	location, err := domain.NewLocation(domain.EndpointID(evidenceEndpoint), domain.CanonicalPath(evidencePath))
	if err != nil {
		return domain.Location{}, domain.Fingerprint{}, "", fmt.Errorf("build pinned cache location: %w", err)
	}
	digestBytes := sha256.Sum256(evidenceContent)
	digest := hex.EncodeToString(digestBytes[:])
	algorithm := "sha256"
	size := uint64(len(evidenceContent))
	return location, domain.Fingerprint{Size: &size, HashAlgorithm: &algorithm, HashHex: &digest}, digest, nil
}

func openOwners(ctx context.Context, config commandConfig) (*owners, error) {
	files, err := cachefs.Initialize(config.cacheRoot)
	if err != nil {
		return nil, fmt.Errorf("open pinned cache filesystem owner: %w", err)
	}
	database, err := sql.Open("sqlite", sqliteDSN(config.database))
	if err != nil {
		return nil, fmt.Errorf("open pinned cache database: %w", err)
	}
	database.SetMaxOpenConns(4)
	catalog, err := cachestore.New(ctx, database)
	if err != nil {
		_ = database.Close()
		return nil, fmt.Errorf("open pinned cache catalog owner: %w", err)
	}
	manager, err := cachemanager.New(files, catalog, fixedClock{now: time.Unix(1_800_000_000, 0).UTC()}, strings.Repeat("d", 32), cache.DefaultLimits())
	if err != nil {
		_ = database.Close()
		return nil, fmt.Errorf("open pinned cache manager owner: %w", err)
	}
	return &owners{database: database, files: files, manager: manager}, nil
}

func (owned *owners) close() {
	if owned != nil && owned.database != nil {
		_ = owned.database.Close()
	}
}

func sqliteDSN(path string) string {
	location := (&url.URL{Scheme: "file", Path: path}).String()
	return location + "?_pragma=foreign_keys(1)&_pragma=journal_mode(WAL)&_pragma=synchronous(FULL)&_pragma=busy_timeout(5000)"
}

func publishEvidence(path string, record evidenceRecord) (returnErr error) {
	file, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600) // #nosec G304 -- canonical absolute harness path is supplied by the CI owner.
	if err != nil {
		return fmt.Errorf("publish pinned cache evidence no-replace: %w", err)
	}
	defer func() { returnErr = errors.Join(returnErr, file.Close()) }()
	if err := writeJSON(file, record); err != nil {
		return err
	}
	if err := file.Sync(); err != nil {
		return fmt.Errorf("sync pinned cache evidence: %w", err)
	}
	return nil
}

func readEvidence(path string) (evidenceRecord, error) {
	metadata, err := os.Lstat(path) // #nosec G304 -- canonical absolute harness path is supplied by the CI owner.
	if err != nil {
		return evidenceRecord{}, fmt.Errorf("stat pinned cache evidence: %w", err)
	}
	if !metadata.Mode().IsRegular() || metadata.Mode().Perm() != 0o600 {
		return evidenceRecord{}, fmt.Errorf("read pinned cache evidence: record must be a private regular file")
	}
	file, err := os.Open(path) // #nosec G304 -- lstat and same-file verification bind this exact harness path.
	if err != nil {
		return evidenceRecord{}, fmt.Errorf("open pinned cache evidence: %w", err)
	}
	defer file.Close()
	opened, err := file.Stat()
	if err != nil || !os.SameFile(metadata, opened) {
		return evidenceRecord{}, fmt.Errorf("read pinned cache evidence: path identity changed")
	}
	decoder := json.NewDecoder(io.LimitReader(file, maxEvidenceBytes+1))
	decoder.DisallowUnknownFields()
	var record evidenceRecord
	if err := decoder.Decode(&record); err != nil {
		return evidenceRecord{}, fmt.Errorf("decode pinned cache evidence: %w", err)
	}
	if err := requireJSONEOF(decoder); err != nil {
		return evidenceRecord{}, err
	}
	_, _, digest, err := evidenceIdentity()
	if err != nil {
		return evidenceRecord{}, err
	}
	if record.Schema != evidenceSchema || record.EndpointID != evidenceEndpoint || record.CanonicalPath != evidencePath ||
		record.WorkspaceID != evidenceWorkspace || record.ContentSHA256 != digest || record.SizeBytes != int64(len(evidenceContent)) ||
		record.EntryID == "" || record.BlobID == "" {
		return evidenceRecord{}, fmt.Errorf("read pinned cache evidence: frozen identity differs")
	}
	return record, nil
}

func requireJSONEOF(decoder *json.Decoder) error {
	var extra any
	if err := decoder.Decode(&extra); err != io.EOF {
		return fmt.Errorf("decode pinned cache evidence: trailing JSON content")
	}
	return nil
}

func writeJSON(output io.Writer, value any) error {
	encoder := json.NewEncoder(output)
	encoder.SetEscapeHTML(false)
	if err := encoder.Encode(value); err != nil {
		return fmt.Errorf("encode pinned cache lifecycle JSON: %w", err)
	}
	return nil
}
