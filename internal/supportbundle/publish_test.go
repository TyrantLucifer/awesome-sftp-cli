package supportbundle

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestPublishCreatesPrivateNoReplaceBundleAndSyncsContent(t *testing.T) {
	t.Parallel()

	parent := privatePublishTestDirectory(t)
	path := filepath.Join(parent, "support.tar.gz")
	want := []byte("deterministic-support-bundle")
	if err := testPublish(t.Context(), path, want); err != nil {
		t.Fatalf("Publish(): %v", err)
	}
	got, err := os.ReadFile(path) // #nosec G304 -- path is the exact test-owned publication under t.TempDir.
	if err != nil {
		t.Fatalf("read publication: %v", err)
	}
	if string(got) != string(want) {
		t.Fatalf("publication = %q, want %q", got, want)
	}
	info, err := os.Lstat(path)
	if err != nil {
		t.Fatalf("lstat publication: %v", err)
	}
	if info.Mode().Perm() != 0o600 || !info.Mode().IsRegular() {
		t.Fatalf("publication mode = %v", info.Mode())
	}
}

func TestPublishRefusesExistingSymlinkRelativeAndUnsafeParentTargets(t *testing.T) {
	t.Parallel()

	parent := privatePublishTestDirectory(t)
	existing := filepath.Join(parent, "existing.tar.gz")
	if err := os.WriteFile(existing, []byte("keep"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := testPublish(t.Context(), existing, []byte("replace")); err == nil {
		t.Fatal("Publish() replaced an existing file")
	}
	if got, err := os.ReadFile(existing); err != nil || string(got) != "keep" { // #nosec G304 -- existing is an exact test-owned path.
		t.Fatalf("existing file changed: bytes=%q err=%v", got, err)
	}

	symlink := filepath.Join(parent, "symlink.tar.gz")
	if err := os.Symlink(existing, symlink); err != nil {
		t.Fatal(err)
	}
	if err := testPublish(t.Context(), symlink, []byte("replace")); err == nil {
		t.Fatal("Publish() followed an output symlink")
	}
	if err := Publish(t.Context(), "relative.tar.gz", []byte("relative")); err == nil {
		t.Fatal("Publish() accepted a relative output path")
	}

	unsafeParent := filepath.Join(parent, "shared")
	if err := os.Mkdir(unsafeParent, 0o755); err != nil { // #nosec G301 -- deliberately unsafe fixture must be rejected.
		t.Fatal(err)
	}
	if err := os.Chmod(unsafeParent, 0o755); err != nil { // #nosec G302 -- deliberately unsafe fixture defeats restrictive umask.
		t.Fatal(err)
	}
	unsafePath := filepath.Join(unsafeParent, "support.tar.gz")
	if err := testPublish(t.Context(), unsafePath, []byte("unsafe")); err == nil {
		t.Fatal("Publish() accepted a non-private parent")
	}
	if _, err := os.Lstat(unsafePath); !os.IsNotExist(err) {
		t.Fatalf("unsafe publication exists: %v", err)
	}
}

func TestPublishCancellationAfterCreateRemovesPartialTarget(t *testing.T) {
	t.Parallel()

	path := filepath.Join(privatePublishTestDirectory(t), "support.tar.gz")
	ctx := &cancelAfterChecksContext{remaining: 1}
	if err := testPublish(ctx, path, make([]byte, 128*1024)); err == nil {
		t.Fatal("Publish() succeeded after cancellation")
	}
	if _, err := os.Lstat(path); !os.IsNotExist(err) {
		t.Fatalf("canceled publication left a target: %v", err)
	}
}

func TestPublishSyncFailureRemovesPartialTarget(t *testing.T) {
	t.Parallel()

	path := filepath.Join(privatePublishTestDirectory(t), "support.tar.gz")
	err := publishWithValidation(t.Context(), path, []byte("bundle"), testPrivateDirectory, testPrivateFile, func(string) error {
		return errors.New("injected directory sync failure")
	})
	if err == nil {
		t.Fatal("publishWithValidation() succeeded after sync failure")
	}
	if _, statErr := os.Lstat(path); !os.IsNotExist(statErr) {
		t.Fatalf("failed publication left a target: %v", statErr)
	}
}

type cancelAfterChecksContext struct {
	remaining int
}

func privatePublishTestDirectory(t *testing.T) string {
	t.Helper()
	directory := t.TempDir()
	absolute, err := filepath.Abs(directory)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(absolute, 0o700); err != nil { // #nosec G302 -- owner-private directories require exact 0700.
		t.Fatal(err)
	}
	return absolute
}

func testPublish(ctx context.Context, path string, content []byte) error {
	return publishWithValidation(ctx, path, content, testPrivateDirectory, testPrivateFile, syncPublishDirectory)
}

func testPrivateDirectory(path string) error {
	info, err := os.Lstat(path)
	if err != nil {
		return err
	}
	if !info.IsDir() || info.Mode().Perm() != 0o700 {
		return errors.New("directory is not owner-private")
	}
	return nil
}

func testPrivateFile(path string) error {
	info, err := os.Lstat(path)
	if err != nil {
		return err
	}
	if !info.Mode().IsRegular() || info.Mode().Perm() != 0o600 {
		return errors.New("file is not owner-private")
	}
	return nil
}

func (ctx *cancelAfterChecksContext) Deadline() (time.Time, bool) { return time.Time{}, false }
func (ctx *cancelAfterChecksContext) Done() <-chan struct{}       { return nil }
func (ctx *cancelAfterChecksContext) Value(any) any               { return nil }
func (ctx *cancelAfterChecksContext) Err() error {
	if ctx.remaining == 0 {
		return context.Canceled
	}
	ctx.remaining--
	return nil
}
