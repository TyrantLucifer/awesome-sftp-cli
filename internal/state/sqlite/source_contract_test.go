package sqlite

import (
	"bytes"
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

func TestModerncBackupAppliesURIPragmasBeforeBackupInit(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	goBinary, err := exec.LookPath("go")
	if err != nil {
		t.Fatalf("locate active Go toolchain: %v", err)
	}
	// The executable is resolved once to an absolute path; arguments are fixed.
	command := exec.CommandContext(ctx, goBinary, "env", "GOMODCACHE") //nolint:gosec
	output, err := command.Output()
	if err != nil {
		t.Fatalf("locate Go module cache: %v", err)
	}
	moduleDir := filepath.Join(string(bytes.TrimSpace(output)), "modernc.org", "sqlite@v1.53.0")
	// moduleDir is the trusted module cache reported by the active Go toolchain.
	connSource, err := os.ReadFile(filepath.Join(moduleDir, "conn.go")) //nolint:gosec
	if err != nil {
		t.Fatalf("read pinned modernc conn.go: %v", err)
	}
	sqliteSource, err := os.ReadFile(filepath.Join(moduleDir, "sqlite.go")) //nolint:gosec
	if err != nil {
		t.Fatalf("read pinned modernc sqlite.go: %v", err)
	}
	statementSource, err := os.ReadFile(filepath.Join(moduleDir, "stmt.go")) //nolint:gosec
	if err != nil {
		t.Fatalf("read pinned modernc stmt.go: %v", err)
	}
	moduleSource, err := os.ReadFile(filepath.Join(moduleDir, "go.mod")) //nolint:gosec
	if err != nil {
		t.Fatalf("read pinned modernc go.mod: %v", err)
	}

	assertSourceOrder(
		t,
		connSource,
		"func (c *conn) NewBackup(dstUri string) (*Backup, error)",
		"dstConn, err := newConn(dstUri)",
		"backup, err := c.backup(dstConn, false)",
	)
	assertSourceOrder(
		t,
		connSource,
		"func newConn(dsn string) (*conn, error)",
		"if err = applyQueryParams(c, query); err != nil",
		"return c, nil",
	)
	assertSourceOrder(
		t,
		connSource,
		"func (c *conn) backup(remoteConn *conn, restore bool)",
		"pBackup = sqlite3.Xsqlite3_backup_init",
		"return &Backup{srcConn: c, dstConn: remoteConn, pBackup: pBackup}, nil",
	)
	assertSourceOrder(
		t,
		sqliteSource,
		"func applyQueryParams(c *conn, query string) error",
		"for _, v := range q[\"_pragma\"]",
		"_, err := c.exec(context.Background(), cmd, nil)",
	)
	assertSourceOrder(
		t,
		statementSource,
		"// FALLBACK PATH: Multi-statement script",
		"for psql := s.psql; *(*byte)(unsafe.Pointer(psql)) != 0",
		"pstmt, err = s.c.prepareV2(&psql)",
	)
	if !bytes.Contains(moduleSource, []byte("modernc.org/libc v1.73.4")) {
		t.Fatal("pinned modernc sqlite module no longer selects modernc.org/libc v1.73.4")
	}
}

func assertSourceOrder(t *testing.T, source []byte, snippets ...string) {
	t.Helper()

	remaining := source
	for _, snippet := range snippets {
		index := bytes.Index(remaining, []byte(snippet))
		if index < 0 {
			t.Fatalf("pinned modernc source is missing contract snippet %q", snippet)
		}
		remaining = remaining[index+len(snippet):]
	}
}
