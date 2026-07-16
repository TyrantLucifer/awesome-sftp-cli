package statefs

import (
	"encoding/binary"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

func TestPreflightIdentityRecognizesOnlyPristineOrExactProject(t *testing.T) {
	t.Parallel()

	t.Run("pristine", func(t *testing.T) {
		t.Parallel()
		path := filepath.Join(privateTempDir(t), "amsftp.db")
		identity, err := PreflightIdentity(path)
		if err != nil {
			t.Fatalf("PreflightIdentity(): %v", err)
		}
		if identity.Kind != IdentityPristine || identity.HasSidecars {
			t.Fatalf("identity = %#v, want pristine", identity)
		}
	})

	t.Run("project", func(t *testing.T) {
		t.Parallel()
		path := filepath.Join(privateTempDir(t), "amsftp.db")
		writeHeaderFile(t, path, statePageSize, 0, applicationID)
		identity, err := PreflightIdentity(path)
		if err != nil {
			t.Fatalf("PreflightIdentity(): %v", err)
		}
		if identity.Kind != IdentityProject || identity.HasSidecars {
			t.Fatalf("identity = %#v, want project", identity)
		}
	})

	for name, header := range map[string][]byte{
		"zero byte":        {},
		"truncated":        []byte("SQLite format 3\x00"),
		"wrong magic":      headerBytes(statePageSize, 0, applicationID, "Not SQLite!!!!!"),
		"wrong page size":  headerBytes(8192, 0, applicationID, "SQLite format 3\x00"),
		"user version":     headerBytes(statePageSize, 1, applicationID, "SQLite format 3\x00"),
		"application zero": headerBytes(statePageSize, 0, 0, "SQLite format 3\x00"),
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			path := filepath.Join(privateTempDir(t), "amsftp.db")
			if err := os.WriteFile(path, header, 0o600); err != nil {
				t.Fatalf("write candidate: %v", err)
			}
			if _, err := PreflightIdentity(path); !errors.Is(err, ErrForeignDatabase) {
				t.Fatalf("PreflightIdentity() error = %v, want ErrForeignDatabase", err)
			}
		})
	}
}

func TestPreflightIdentityRejectsForeignDatabaseWithoutDirectoryMutation(t *testing.T) {
	t.Parallel()

	directory := privateTempDir(t)
	path := filepath.Join(directory, "amsftp.db")
	content := []byte("foreign bytes that must remain unchanged")
	if err := os.WriteFile(path, content, 0o600); err != nil {
		t.Fatalf("write foreign database: %v", err)
	}
	beforeEntries := directoryEntries(t, directory)
	beforeInfo, err := os.Lstat(path)
	if err != nil {
		t.Fatalf("stat foreign database: %v", err)
	}

	if _, err := PreflightIdentity(path); !errors.Is(err, ErrForeignDatabase) {
		t.Fatalf("PreflightIdentity() error = %v, want ErrForeignDatabase", err)
	}
	after, err := os.ReadFile(path) //nolint:gosec // test-owned path
	if err != nil {
		t.Fatalf("read foreign database: %v", err)
	}
	if !reflect.DeepEqual(after, content) {
		t.Fatalf("foreign bytes changed: %q", after)
	}
	afterEntries := directoryEntries(t, directory)
	if !reflect.DeepEqual(afterEntries, beforeEntries) {
		t.Fatalf("directory entries changed: before=%v after=%v", beforeEntries, afterEntries)
	}
	afterInfo, err := os.Lstat(path)
	if err != nil {
		t.Fatalf("stat foreign database after rejection: %v", err)
	}
	if afterInfo.Mode() != beforeInfo.Mode() || afterInfo.Size() != beforeInfo.Size() || !afterInfo.ModTime().Equal(beforeInfo.ModTime()) {
		t.Fatalf("foreign attrs changed: before=%v after=%v", beforeInfo, afterInfo)
	}
}

func TestPreflightIdentityRequiresExactSidecars(t *testing.T) {
	t.Parallel()

	directory := privateTempDir(t)
	path := filepath.Join(directory, "amsftp.db")
	if err := os.WriteFile(path+"-wal", []byte("orphan"), 0o600); err != nil {
		t.Fatalf("write orphan WAL: %v", err)
	}
	if _, err := PreflightIdentity(path); !errors.Is(err, ErrAmbiguousState) {
		t.Fatalf("orphan sidecar error = %v, want ErrAmbiguousState", err)
	}

	writeHeaderFile(t, path, statePageSize, 0, applicationID)
	identity, err := PreflightIdentity(path)
	if err != nil {
		t.Fatalf("project with private WAL: %v", err)
	}
	if !identity.HasSidecars {
		t.Fatal("project WAL was not reported")
	}
	if err := os.Chmod(path+"-wal", 0o644); err != nil { //nolint:gosec // deliberately unsafe negative fixture
		t.Fatalf("change WAL mode: %v", err)
	}
	if _, err := PreflightIdentity(path); !errors.Is(err, ErrAmbiguousState) {
		t.Fatalf("public sidecar error = %v, want ErrAmbiguousState", err)
	}
}

func writeHeaderFile(t *testing.T, path string, pageSize uint16, userVersion, appID uint32) {
	t.Helper()
	if err := os.WriteFile(path, headerBytes(pageSize, userVersion, appID, "SQLite format 3\x00"), 0o600); err != nil {
		t.Fatalf("write header file: %v", err)
	}
}

func headerBytes(pageSize uint16, userVersion, appID uint32, magic string) []byte {
	header := make([]byte, sqliteHeaderBytes)
	copy(header, magic)
	binary.BigEndian.PutUint16(header[16:18], pageSize)
	binary.BigEndian.PutUint32(header[60:64], userVersion)
	binary.BigEndian.PutUint32(header[68:72], appID)
	return header
}

func directoryEntries(t *testing.T, directory string) []string {
	t.Helper()
	entries, err := os.ReadDir(directory)
	if err != nil {
		t.Fatalf("read directory: %v", err)
	}
	result := make([]string, len(entries))
	for index, entry := range entries {
		result[index] = entry.Name()
	}
	return result
}

func privateTempDir(t *testing.T) string {
	t.Helper()
	directory := t.TempDir()
	if err := os.Chmod(directory, 0o700); err != nil { //nolint:gosec // exact owner-private directory mode
		t.Fatalf("make temp directory private: %v", err)
	}
	return directory
}
