package localfs

import (
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/TyrantLucifer/awesome-mac-sftp/internal/domain"
	providerapi "github.com/TyrantLucifer/awesome-mac-sftp/internal/provider"
	"github.com/TyrantLucifer/awesome-mac-sftp/internal/provider/contracttest"
)

const (
	testEndpointID domain.EndpointID = "ep_aaaaaaaaaaaaaaaaaaaaaaaaaa"
	testSessionID  domain.SessionID  = "sess_aaaaaaaaaaaaaaaaaaaaaaaaaa"
)

type contractFactory struct{}

type contractProvider struct {
	*Provider
	snapshot domain.EndpointSnapshot
}

func (p *contractProvider) Snapshot(context.Context) (domain.EndpointSnapshot, error) {
	return p.snapshot, nil
}

func TestProviderContract(t *testing.T) {
	contracttest.Run(t, contractFactory{})
}

func (contractFactory) New(t *testing.T) contracttest.Fixture {
	t.Helper()
	root := t.TempDir()
	writeFixture(t, root)
	implementation := newTestProvider(t, root, 16)
	wrapped := &contractProvider{Provider: implementation, snapshot: implementation.snapshot}

	return contracttest.Fixture{
		Provider: wrapped,
		InvalidateListing: func(context.Context, domain.Location) error {
			oldRoot := root + ".old"
			if err := os.Rename(root, oldRoot); err != nil {
				return err
			}
			if err := os.Mkdir(root, 0o700); err != nil {
				return err
			}
			writeFixture(t, root)
			return nil
		},
		ChangeCapabilities: func(context.Context) error {
			wrapped.snapshot.Capabilities = mustCapabilities(t, 2, []domain.Capability{{
				Name:    "contract-changed",
				Version: 2,
			}})
			return nil
		},
	}
}

func TestListPreservesRawFilenameAndSymlinkTargetBytes(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("the native Darwin filesystem rejects invalid UTF-8 names")
	}
	root := t.TempDir()
	rawName := string([]byte{'b', 'a', 'd', '-', 0xff})
	if err := os.WriteFile(filepath.Join(root, rawName), []byte("payload"), 0o600); err != nil {
		t.Fatal(err)
	}
	rawTarget := string([]byte{'b', 'a', 'd', '-', 0xff})
	if err := os.Symlink(rawTarget, filepath.Join(root, "link")); err != nil {
		t.Fatal(err)
	}

	implementation := newTestProvider(t, root, 16)
	rootLocation := normalize(t, implementation, "/")
	page, err := implementation.List(context.Background(), providerapi.ListRequest{
		Location: rootLocation,
		Limit:    16,
	})
	if err != nil {
		t.Fatalf("List(): %v", err)
	}

	var foundFile, foundLink bool
	for _, entry := range page.Entries {
		switch entry.Name {
		case rawName:
			foundFile = true
			if !strings.Contains(string(entry.Location.Path), rawName) {
				t.Fatalf("file path %x lost raw name %x", []byte(entry.Location.Path), []byte(rawName))
			}
		case "link":
			foundLink = true
			if entry.Symlink == nil || entry.Symlink.RawTarget != rawTarget {
				t.Fatalf("link target = %#v, want raw bytes %x", entry.Symlink, []byte(rawTarget))
			}
		}
	}
	if !foundFile || !foundLink {
		t.Fatalf("found file=%v link=%v in %#v", foundFile, foundLink, page.Entries)
	}
}

func TestListBoundsAndReleasesCursorResources(t *testing.T) {
	root := t.TempDir()
	for index := 0; index < 8; index++ {
		name := filepath.Join(root, string(rune('a'+index)))
		if err := os.WriteFile(name, []byte("x"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	implementation := newTestProvider(t, root, 2)
	location := normalize(t, implementation, "/")

	var first providerapi.PageCursor
	for index := 0; index < 3; index++ {
		page, err := implementation.List(context.Background(), providerapi.ListRequest{
			Location: location,
			Limit:    1,
		})
		if err != nil {
			t.Fatalf("List(%d): %v", index, err)
		}
		if index == 0 {
			first = page.NextCursor
		}
	}
	if got := activeCursorCount(implementation); got != 2 {
		t.Fatalf("active cursors = %d, want bounded at 2", got)
	}
	_, err := implementation.List(context.Background(), providerapi.ListRequest{
		Location: location,
		Cursor:   first,
		Limit:    1,
	})
	if !domain.IsCode(err, domain.CodeInvalidArgument) {
		t.Fatalf("evicted cursor error = %v, want invalid_argument", err)
	}

	page, err := implementation.List(context.Background(), providerapi.ListRequest{
		Location: location,
		Limit:    1,
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := implementation.DiscardCursor(page.NextCursor); err != nil {
		t.Fatalf("DiscardCursor(): %v", err)
	}
	if got := activeCursorCount(implementation); got != 1 {
		t.Fatalf("active cursors after discard = %d, want 1", got)
	}
	if err := implementation.Close(); err != nil {
		t.Fatalf("Close(): %v", err)
	}
	if got := activeCursorCount(implementation); got != 0 {
		t.Fatalf("active cursors after close = %d, want 0", got)
	}
}

func TestReadHonorsRangeFingerprintAndCancellation(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "file"), []byte("0123456789"), 0o600); err != nil {
		t.Fatal(err)
	}
	implementation := newTestProvider(t, root, 16)
	location := normalize(t, implementation, "/file")
	entry, err := implementation.Stat(context.Background(), providerapi.StatRequest{Location: location})
	if err != nil {
		t.Fatal(err)
	}
	limit := int64(3)
	handle, err := implementation.OpenRead(context.Background(), providerapi.OpenReadRequest{
		Location:            location,
		Offset:              2,
		Limit:               &limit,
		ExpectedFingerprint: &entry.Fingerprint,
	})
	if err != nil {
		t.Fatal(err)
	}
	buffer := make([]byte, 8)
	n, err := handle.Read(context.Background(), buffer)
	if err != nil && !errors.Is(err, io.EOF) {
		t.Fatal(err)
	}
	if got := string(buffer[:n]); got != "234" {
		t.Fatalf("read = %q, want 234", got)
	}
	if limit != 3 {
		t.Fatalf("OpenRead mutated caller limit to %d", limit)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if n, err := handle.Read(ctx, buffer); n != 0 || !domain.IsCode(err, domain.CodeCanceled) {
		t.Fatalf("canceled read = (%d, %v), want canceled", n, err)
	}
	if err := handle.Close(context.Background()); err != nil {
		t.Fatal(err)
	}

	bad := entry.Fingerprint
	wrongSize := uint64(999)
	bad.Size = &wrongSize
	_, err = implementation.OpenRead(context.Background(), providerapi.OpenReadRequest{
		Location:            location,
		ExpectedFingerprint: &bad,
	})
	if !domain.IsCode(err, domain.CodeConflict) {
		t.Fatalf("fingerprint mismatch error = %v, want conflict", err)
	}
}

func newTestProvider(t *testing.T, root string, maxCursors int) *Provider {
	t.Helper()
	implementation, err := New(Config{
		Endpoint: domain.Endpoint{
			ID:          testEndpointID,
			Kind:        domain.EndpointLocal,
			DisplayName: "Local",
		},
		SessionID:  testSessionID,
		Root:       root,
		Now:        time.Now,
		MaxCursors: maxCursors,
	})
	if err != nil {
		t.Fatalf("New(): %v", err)
	}
	t.Cleanup(func() { _ = implementation.Close() })
	return implementation
}

func normalize(t *testing.T, implementation *Provider, input string) domain.Location {
	t.Helper()
	location, err := implementation.Normalize(context.Background(), domain.NormalizeRequest{
		EndpointID: testEndpointID,
		Input:      input,
	})
	if err != nil {
		t.Fatalf("Normalize(%q): %v", input, err)
	}
	return location
}

func writeFixture(t *testing.T, root string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Join(root, "nested"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "file"), []byte("content"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "nested", "child"), []byte("child"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("file", filepath.Join(root, "link")); err != nil && !errors.Is(err, os.ErrExist) {
		t.Fatal(err)
	}
}

func activeCursorCount(implementation *Provider) int {
	implementation.mu.Lock()
	defer implementation.mu.Unlock()
	return len(implementation.cursors)
}

func mustCapabilities(t *testing.T, generation uint64, items []domain.Capability) domain.CapabilitySnapshot {
	t.Helper()
	snapshot, err := domain.NewCapabilitySnapshot(domain.CapabilityRevision{
		SessionID:  testSessionID,
		Generation: generation,
	}, true, items)
	if err != nil {
		t.Fatal(err)
	}
	return snapshot
}
