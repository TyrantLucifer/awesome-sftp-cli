package statefs

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"
)

func TestInspectDatabaseIsImmutableAndRequiresTheCompiledHead(t *testing.T) {
	t.Parallel()

	root := privateTempDir(t)
	path := filepath.Join(root, "amsftp.db")
	database, report, err := Initialize(context.Background(), InitializeConfig{
		Root: root, DatabasePath: path,
		Random: strings.NewReader(strings.Repeat("i", 512)), Now: time.Unix(1_700, 0),
	})
	if err != nil {
		t.Fatalf("Initialize(): %v", err)
	}
	if err := database.Close(); err != nil {
		t.Fatalf("close initialized database: %v", err)
	}
	beforeEntries := directoryEntries(t, root)
	beforeBytes, err := os.ReadFile(path) //nolint:gosec // test-owned trusted path
	if err != nil {
		t.Fatalf("read database before inspection: %v", err)
	}
	beforeInfo, err := os.Lstat(path)
	if err != nil {
		t.Fatalf("stat database before inspection: %v", err)
	}

	head, err := InspectDatabase(context.Background(), path)
	if err != nil {
		t.Fatalf("InspectDatabase(): %v", err)
	}
	if head != report.SchemaHead {
		t.Fatalf("head = %d, want initialized head %d", head, report.SchemaHead)
	}
	afterBytes, err := os.ReadFile(path) //nolint:gosec // test-owned trusted path
	if err != nil {
		t.Fatalf("read database after inspection: %v", err)
	}
	afterInfo, err := os.Lstat(path)
	if err != nil {
		t.Fatalf("stat database after inspection: %v", err)
	}
	if !reflect.DeepEqual(afterBytes, beforeBytes) || !reflect.DeepEqual(directoryEntries(t, root), beforeEntries) {
		t.Fatal("immutable inspection changed database bytes or directory entries")
	}
	if afterInfo.Mode() != beforeInfo.Mode() || afterInfo.Size() != beforeInfo.Size() || !afterInfo.ModTime().Equal(beforeInfo.ModTime()) {
		t.Fatalf("immutable inspection changed metadata: before=%v after=%v", beforeInfo, afterInfo)
	}
}

func TestInspectDatabaseRefusesLiveSidecarsWithoutMutation(t *testing.T) {
	t.Parallel()

	root := privateTempDir(t)
	path := filepath.Join(root, "amsftp.db")
	writeHeaderFile(t, path, statePageSize, 0, applicationID)
	wal := path + "-wal"
	content := []byte("live-wal-canary")
	if err := os.WriteFile(wal, content, 0o600); err != nil {
		t.Fatalf("write live sidecar: %v", err)
	}
	beforeEntries := directoryEntries(t, root)

	if _, err := InspectDatabase(context.Background(), path); !errors.Is(err, ErrDatabaseActive) {
		t.Fatalf("InspectDatabase() error = %v, want ErrDatabaseActive", err)
	}
	after, err := os.ReadFile(wal) //nolint:gosec // test-owned trusted path
	if err != nil {
		t.Fatalf("read sidecar after refusal: %v", err)
	}
	if !reflect.DeepEqual(after, content) || !reflect.DeepEqual(directoryEntries(t, root), beforeEntries) {
		t.Fatal("live-sidecar refusal changed state")
	}
}

func TestAvailableFilesystemBytesIsReadOnlyAndNonzero(t *testing.T) {
	t.Parallel()

	root := privateTempDir(t)
	before := directoryEntries(t, root)
	available, err := AvailableFilesystemBytes(root)
	if err != nil {
		t.Fatalf("AvailableFilesystemBytes(): %v", err)
	}
	if available == 0 {
		t.Fatal("AvailableFilesystemBytes() = 0")
	}
	if !reflect.DeepEqual(directoryEntries(t, root), before) {
		t.Fatal("filesystem-space inspection changed directory entries")
	}
}
