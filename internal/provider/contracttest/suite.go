package contracttest

import (
	"bytes"
	"context"
	"errors"
	"io"
	"reflect"
	"strings"
	"testing"

	"github.com/TyrantLucifer/awesome-sftp-cli/internal/domain"
	"github.com/TyrantLucifer/awesome-sftp-cli/internal/provider"
)

// Fixture exposes an isolated provider and deterministic state transitions used
// by the contract suite.
type Fixture struct {
	Provider           provider.Provider
	InvalidateListing  func(context.Context, domain.Location) error
	ChangeCapabilities func(context.Context) error
}

// Factory creates an isolated provider fixture for each contract subtest.
type Factory interface {
	New(t *testing.T) Fixture
}

// Run applies the reusable read-only Provider contract. A fixture must expose a
// root with at least two entries, including a non-empty regular file, a nested
// directory, and a resolvable symlink. Its hooks must invalidate listing
// cursors and change capabilities without replacing the provider session.
func Run(t *testing.T, factory Factory) {
	t.Helper()

	t.Run("descriptor stability", func(t *testing.T) {
		implementation := newFixture(t, factory).Provider
		first := implementation.Descriptor()
		second := implementation.Descriptor()
		if first.ID == "" {
			t.Fatal("Descriptor().ID is empty")
		}
		if first.Kind != domain.EndpointLocal && first.Kind != domain.EndpointSSH {
			t.Fatalf("Descriptor().Kind = %q, want a canonical endpoint kind", first.Kind)
		}
		if !reflect.DeepEqual(first, second) {
			t.Fatalf("Descriptor() changed: first=%#v second=%#v", first, second)
		}
	})

	t.Run("normalization boundaries", func(t *testing.T) {
		implementation := newFixture(t, factory).Provider
		descriptor := implementation.Descriptor()
		root := normalizeRoot(t, implementation)

		again, err := implementation.Normalize(context.Background(), domain.NormalizeRequest{
			EndpointID: descriptor.ID,
			Input:      string(root.Path),
		})
		if err != nil {
			t.Fatalf("Normalize(canonical root): %v", err)
		}
		if again != root {
			t.Fatalf("Normalize(canonical root) = %#v, want %#v", again, root)
		}

		relative, err := implementation.Normalize(context.Background(), domain.NormalizeRequest{
			EndpointID: descriptor.ID,
			Base:       &root,
			Input:      "contract-relative",
		})
		if err != nil {
			t.Fatalf("Normalize(relative): %v", err)
		}
		if relative.EndpointID != descriptor.ID || relative.Path == "" {
			t.Fatalf("Normalize(relative) = %#v, want non-empty location on %q", relative, descriptor.ID)
		}

		invalidUTF8 := string([]byte{'/', 0xff, 'x'})
		invalidUTF8Location, err := implementation.Normalize(
			context.Background(),
			domain.NormalizeRequest{EndpointID: descriptor.ID, Input: invalidUTF8},
		)
		if err != nil {
			t.Fatalf("Normalize(invalid UTF-8): %v", err)
		}
		if !bytes.Contains([]byte(invalidUTF8Location.Path), []byte{0xff}) {
			t.Fatalf("Normalize(invalid UTF-8) lost path byte: %x", []byte(invalidUTF8Location.Path))
		}

		_, err = implementation.Normalize(context.Background(), domain.NormalizeRequest{
			EndpointID: alternateEndpointID(descriptor.ID),
			Input:      "/",
		})
		requireOpError(t, err, domain.CodeInvalidArgument, "normalize", descriptor.ID, nil)

		_, err = implementation.Normalize(context.Background(), domain.NormalizeRequest{
			EndpointID: descriptor.ID,
			Input:      string([]byte{'/', 0, 'x'}),
		})
		requireOpError(t, err, domain.CodeInvalidArgument, "normalize", descriptor.ID, nil)
	})

	t.Run("bounded pages", func(t *testing.T) {
		implementation := newFixture(t, factory).Provider
		root := normalizeRoot(t, implementation)
		entries := listAll(t, implementation, root, 1)
		if len(entries) < 2 {
			t.Fatalf("fixture root entries = %d, want at least 2", len(entries))
		}
	})

	t.Run("cursor bindings and invalidation", func(t *testing.T) {
		fixture := newFixture(t, factory)
		implementation := fixture.Provider
		descriptor := implementation.Descriptor()
		root := normalizeRoot(t, implementation)
		request := provider.ListRequest{Location: root, Limit: 1}
		first, err := implementation.List(context.Background(), request)
		if err != nil {
			t.Fatalf("List(first page): %v", err)
		}
		if err := provider.ValidateListPage(request, first); err != nil {
			t.Fatalf("List(first page) contract: %v", err)
		}
		if first.Done || first.NextCursor == "" {
			t.Fatal("fixture must produce a non-terminal first page for cursor checks")
		}

		tampered := request
		tampered.Cursor = first.NextCursor + "x"
		_, err = implementation.List(context.Background(), tampered)
		requireOpError(t, err, domain.CodeInvalidArgument, "list", descriptor.ID, &root)

		wrongEndpoint := request
		wrongEndpoint.Cursor = first.NextCursor
		wrongEndpoint.Location.EndpointID = alternateEndpointID(descriptor.ID)
		_, err = implementation.List(context.Background(), wrongEndpoint)
		requireOpError(t, err, domain.CodeInvalidArgument, "list", descriptor.ID, &wrongEndpoint.Location)

		wrongSort := request
		wrongSort.Cursor = first.NextCursor
		wrongSort.Sort = &provider.SortHint{Key: "name", Direction: provider.SortDescending}
		_, err = implementation.List(context.Background(), wrongSort)
		requireOpError(t, err, domain.CodeInvalidArgument, "list", descriptor.ID, &root)

		entries := walkEntries(t, implementation, root)
		directory, ok := firstKind(entries, domain.EntryDirectory)
		if !ok {
			t.Fatal("fixture has no nested directory")
		}
		wrongPath := request
		wrongPath.Cursor = first.NextCursor
		wrongPath.Location = directory.Location
		_, err = implementation.List(context.Background(), wrongPath)
		requireOpError(t, err, domain.CodeInvalidArgument, "list", descriptor.ID, &directory.Location)

		secondFixture := newFixture(t, factory)
		secondImplementation := secondFixture.Provider
		secondRoot := normalizeRoot(t, secondImplementation)
		secondRequest := provider.ListRequest{Location: secondRoot, Limit: 1}
		secondFirst, err := secondImplementation.List(context.Background(), secondRequest)
		if err != nil {
			t.Fatalf("second fixture List(first page): %v", err)
		}
		if err := provider.ValidateListPage(secondRequest, secondFirst); err != nil {
			t.Fatalf("second fixture List(first page) contract: %v", err)
		}
		if secondFirst.Done || secondFirst.NextCursor == "" {
			t.Fatal("second fixture must produce a non-terminal first page for isolation checks")
		}

		if err := fixture.InvalidateListing(context.Background(), root); err != nil {
			t.Fatalf("InvalidateListing(): %v", err)
		}

		invalidated := request
		invalidated.Cursor = first.NextCursor
		_, err = implementation.List(context.Background(), invalidated)
		requireOpError(t, err, domain.CodeConflict, "list", descriptor.ID, &root)

		secondRequest.Cursor = secondFirst.NextCursor
		secondPage, err := secondImplementation.List(context.Background(), secondRequest)
		if err != nil {
			t.Fatalf("second fixture List(continuation): %v", err)
		}
		if err := provider.ValidateListPage(secondRequest, secondPage); err != nil {
			t.Fatalf("second fixture List(continuation) contract: %v", err)
		}
	})

	t.Run("short reads EOF and idempotent close", func(t *testing.T) {
		implementation := newFixture(t, factory).Provider
		root := normalizeRoot(t, implementation)
		file := requireKind(t, walkEntries(t, implementation, root), domain.EntryFile)
		limit := int64(1)
		handle, err := implementation.OpenRead(context.Background(), provider.OpenReadRequest{
			Location: file.Location,
			Limit:    &limit,
		})
		if err != nil {
			t.Fatalf("OpenRead(): %v", err)
		}
		if isNil(handle) {
			t.Fatal("OpenRead() returned a nil handle")
		}
		if info := handle.Info(); info.Entry.Location != file.Location {
			t.Fatalf("ReadHandle.Info().Entry.Location = %#v, want %#v", info.Entry.Location, file.Location)
		}

		buffer := make([]byte, 8)
		n, readErr := handle.Read(context.Background(), buffer)
		if n != 1 {
			t.Fatalf("bounded short Read() n = %d, want 1", n)
		}
		if readErr != nil && !errors.Is(readErr, io.EOF) {
			t.Fatalf("bounded short Read() error = %v, want nil or EOF", readErr)
		}

		n, readErr = handle.Read(context.Background(), buffer)
		if n != 0 || !errors.Is(readErr, io.EOF) {
			t.Fatalf("Read() after range end = (%d, %v), want (0, EOF)", n, readErr)
		}
		if err := handle.Close(context.Background()); err != nil {
			t.Fatalf("first Close(): %v", err)
		}
		if err := handle.Close(context.Background()); err != nil {
			t.Fatalf("second Close(): %v", err)
		}
	})

	t.Run("context cancellation", func(t *testing.T) {
		implementation := newFixture(t, factory).Provider
		descriptor := implementation.Descriptor()
		root := normalizeRoot(t, implementation)
		ctx, cancel := context.WithCancel(context.Background())
		cancel()

		_, err := implementation.List(ctx, provider.ListRequest{Location: root, Limit: 1})
		requireCanceled(t, err, "list", descriptor.ID, &root)

		file := requireKind(t, walkEntries(t, implementation, root), domain.EntryFile)
		handle, err := implementation.OpenRead(
			context.Background(),
			provider.OpenReadRequest{Location: file.Location},
		)
		if err != nil {
			t.Fatalf("OpenRead(): %v", err)
		}
		n, err := handle.Read(ctx, make([]byte, 1))
		if n != 0 {
			t.Fatalf("canceled Read() n = %d, want 0", n)
		}
		requireCanceled(t, err, "read", descriptor.ID, &file.Location)
		if err := handle.Close(context.Background()); err != nil {
			t.Fatalf("Close(): %v", err)
		}
	})

	t.Run("symlink stat behavior", func(t *testing.T) {
		implementation := newFixture(t, factory).Provider
		root := normalizeRoot(t, implementation)
		symlink := requireKind(t, walkEntries(t, implementation, root), domain.EntrySymlink)

		linkEntry, err := implementation.Stat(context.Background(), provider.StatRequest{
			Location:       symlink.Location,
			FollowSymlinks: false,
		})
		if err != nil {
			t.Fatalf("Stat(no follow): %v", err)
		}
		if linkEntry.Kind != domain.EntrySymlink {
			t.Fatalf("Stat(no follow).Kind = %q, want %q", linkEntry.Kind, domain.EntrySymlink)
		}

		targetEntry, err := implementation.Stat(context.Background(), provider.StatRequest{
			Location:       symlink.Location,
			FollowSymlinks: true,
		})
		if err != nil {
			t.Fatalf("Stat(follow): %v", err)
		}
		if targetEntry.Kind == domain.EntrySymlink {
			t.Fatal("Stat(follow) returned the unresolved symlink")
		}
	})

	t.Run("capability revisions", func(t *testing.T) {
		fixture := newFixture(t, factory)
		implementation := fixture.Provider
		descriptor := implementation.Descriptor()
		first, err := implementation.Snapshot(context.Background())
		if err != nil {
			t.Fatalf("first Snapshot(): %v", err)
		}
		validateSnapshot(t, descriptor, first)

		if err := fixture.ChangeCapabilities(context.Background()); err != nil {
			t.Fatalf("ChangeCapabilities(): %v", err)
		}

		second, err := implementation.Snapshot(context.Background())
		if err != nil {
			t.Fatalf("second Snapshot(): %v", err)
		}
		validateSnapshot(t, descriptor, second)

		if first.Capabilities.Complete == second.Capabilities.Complete &&
			reflect.DeepEqual(first.Capabilities.Items, second.Capabilities.Items) {
			t.Fatal("ChangeCapabilities() did not change capability payload or completeness")
		}
		if first.SessionID != second.SessionID {
			t.Fatalf("Snapshot().SessionID changed from %q to %q", first.SessionID, second.SessionID)
		}
		if second.Capabilities.Revision.Generation <= first.Capabilities.Revision.Generation {
			t.Fatalf(
				"capability generation did not increase: first=%d second=%d",
				first.Capabilities.Revision.Generation,
				second.Capabilities.Revision.Generation,
			)
		}
		if first.Capabilities.Revision == second.Capabilities.Revision {
			t.Fatal("capability revision did not change")
		}
	})

	t.Run("typed error context", func(t *testing.T) {
		implementation := newFixture(t, factory).Provider
		descriptor := implementation.Descriptor()
		root := normalizeRoot(t, implementation)
		missing, err := implementation.Normalize(context.Background(), domain.NormalizeRequest{
			EndpointID: descriptor.ID,
			Base:       &root,
			Input:      ".amsftp-contract-missing",
		})
		if err != nil {
			t.Fatalf("Normalize(missing): %v", err)
		}

		_, err = implementation.Stat(context.Background(), provider.StatRequest{Location: missing})
		opError := requireOpError(t, err, domain.CodeNotFound, "stat", descriptor.ID, &missing)
		if opError.Retry.Kind == "" {
			t.Fatal("typed error has empty retry advice")
		}
		if opError.Effect == "" {
			t.Fatal("typed error has empty effect status")
		}
	})
}

func newFixture(t *testing.T, factory Factory) Fixture {
	t.Helper()
	fixture := factory.New(t)
	if isNil(fixture.Provider) {
		t.Fatal("Factory.New().Provider is nil")
	}
	if fixture.InvalidateListing == nil {
		t.Fatal("Factory.New().InvalidateListing is nil")
	}
	if fixture.ChangeCapabilities == nil {
		t.Fatal("Factory.New().ChangeCapabilities is nil")
	}
	return fixture
}

func normalizeRoot(t *testing.T, implementation provider.Provider) domain.Location {
	t.Helper()
	descriptor := implementation.Descriptor()
	root, err := implementation.Normalize(context.Background(), domain.NormalizeRequest{
		EndpointID: descriptor.ID,
		Input:      "/",
	})
	if err != nil {
		t.Fatalf("Normalize(root): %v", err)
	}
	if root.EndpointID != descriptor.ID || root.Path == "" {
		t.Fatalf("Normalize(root) = %#v, want non-empty location on %q", root, descriptor.ID)
	}
	if strings.IndexByte(string(root.Path), 0) >= 0 {
		t.Fatalf("Normalize(root).Path contains NUL: %x", []byte(root.Path))
	}
	return root
}

func listAll(
	t *testing.T,
	implementation provider.Provider,
	location domain.Location,
	limit uint32,
) []domain.Entry {
	t.Helper()
	var entries []domain.Entry
	seenCursors := make(map[provider.PageCursor]struct{})
	request := provider.ListRequest{Location: location, Limit: limit}
	for pageNumber := 0; pageNumber < 10_000; pageNumber++ {
		page, err := implementation.List(context.Background(), request)
		if err != nil {
			t.Fatalf("List(page %d): %v", pageNumber, err)
		}
		if err := provider.ValidateListPage(request, page); err != nil {
			t.Fatalf("List(page %d) contract: %v", pageNumber, err)
		}
		entries = append(entries, page.Entries...)
		if page.Done {
			return entries
		}
		if _, duplicate := seenCursors[page.NextCursor]; duplicate {
			t.Fatalf("List(page %d) repeated cursor %q", pageNumber, page.NextCursor)
		}
		seenCursors[page.NextCursor] = struct{}{}
		request.Cursor = page.NextCursor
	}
	t.Fatal("List did not terminate within 10000 pages")
	return nil
}

func walkEntries(
	t *testing.T,
	implementation provider.Provider,
	root domain.Location,
) []domain.Entry {
	t.Helper()
	queue := []domain.Location{root}
	seenDirectories := make(map[string]struct{})
	var all []domain.Entry
	for len(queue) > 0 {
		location := queue[0]
		queue = queue[1:]
		key := string(location.EndpointID) + "\x00" + string(location.Path)
		if _, seen := seenDirectories[key]; seen {
			continue
		}
		seenDirectories[key] = struct{}{}
		if len(seenDirectories) > 10_000 {
			t.Fatal("directory walk exceeded 10000 directories")
		}

		entries := listAll(t, implementation, location, 4096)
		all = append(all, entries...)
		for _, entry := range entries {
			if entry.Kind == domain.EntryDirectory {
				queue = append(queue, entry.Location)
			}
		}
	}
	return all
}

func firstKind(entries []domain.Entry, kind domain.EntryKind) (domain.Entry, bool) {
	for _, entry := range entries {
		if entry.Kind == kind {
			return entry, true
		}
	}
	return domain.Entry{}, false
}

func requireKind(t *testing.T, entries []domain.Entry, kind domain.EntryKind) domain.Entry {
	t.Helper()
	entry, ok := firstKind(entries, kind)
	if !ok {
		t.Fatalf("fixture has no %q entry", kind)
	}
	return entry
}

func validateSnapshot(
	t *testing.T,
	descriptor domain.Endpoint,
	snapshot domain.EndpointSnapshot,
) {
	t.Helper()
	if snapshot.EndpointID != descriptor.ID {
		t.Fatalf("Snapshot().EndpointID = %q, want %q", snapshot.EndpointID, descriptor.ID)
	}
	if snapshot.SessionID == "" {
		t.Fatal("Snapshot().SessionID is empty")
	}
	if snapshot.Capabilities.Revision.SessionID != snapshot.SessionID {
		t.Fatalf(
			"capability session = %q, want snapshot session %q",
			snapshot.Capabilities.Revision.SessionID,
			snapshot.SessionID,
		)
	}
	if snapshot.Capabilities.Revision.Generation == 0 {
		t.Fatal("capability generation is zero")
	}
}

func requireCanceled(
	t *testing.T,
	err error,
	operation string,
	endpointID domain.EndpointID,
	location *domain.Location,
) {
	t.Helper()
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("%s error = %v, want context.Canceled", operation, err)
	}
	requireOpError(t, err, domain.CodeCanceled, operation, endpointID, location)
}

func requireOpError(
	t *testing.T,
	err error,
	code domain.Code,
	operation string,
	endpointID domain.EndpointID,
	location *domain.Location,
) *domain.OpError {
	t.Helper()
	if err == nil {
		t.Fatalf("%s error = nil, want code %q", operation, code)
	}
	var opError *domain.OpError
	if !errors.As(err, &opError) {
		t.Fatalf("%s error type = %T, want *domain.OpError", operation, err)
	}
	if opError.Code != code {
		t.Errorf("%s error code = %q, want %q", operation, opError.Code, code)
	}
	if opError.Operation != operation {
		t.Errorf("Operation = %q, want %q", opError.Operation, operation)
	}
	if opError.EndpointID != endpointID {
		t.Errorf("EndpointID = %q, want %q", opError.EndpointID, endpointID)
	}
	if location != nil {
		if opError.Location == nil || *opError.Location != *location {
			t.Errorf("Location = %#v, want %#v", opError.Location, location)
		}
	}
	return opError
}

func alternateEndpointID(endpointID domain.EndpointID) domain.EndpointID {
	first := domain.EndpointID("ep_aaaaaaaaaaaaaaaaaaaaaaaaaa")
	if endpointID != first {
		return first
	}
	return "ep_bbbbbbbbbbbbbbbbbbbbbbbbbb"
}

func isNil(value any) bool {
	if value == nil {
		return true
	}
	reflected := reflect.ValueOf(value)
	switch reflected.Kind() {
	case reflect.Chan, reflect.Func, reflect.Interface, reflect.Map, reflect.Pointer, reflect.Slice:
		return reflected.IsNil()
	default:
		return false
	}
}
