package fake

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"reflect"
	"runtime/debug"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/TyrantLucifer/awesome-sftp-cli/internal/domain"
	"github.com/TyrantLucifer/awesome-sftp-cli/internal/foundation"
	providerapi "github.com/TyrantLucifer/awesome-sftp-cli/internal/provider"
	"github.com/TyrantLucifer/awesome-sftp-cli/internal/provider/contracttest"
)

const (
	contractEndpointID domain.EndpointID = "ep_aaaaaaaaaaaaaaaaaaaaaaaaaa"
	contractSessionID  domain.SessionID  = "sess_aaaaaaaaaaaaaaaaaaaaaaaaaa"
)

type contractFactory struct{}

func TestProviderContract(t *testing.T) {
	contracttest.Run(t, contractFactory{})
	contracttest.RunMutable(t, contractFactory{})
}

func (contractFactory) New(t *testing.T) contracttest.Fixture {
	t.Helper()

	implementation, controller, err := New(validScenario(t))
	if err != nil {
		t.Fatalf("New(): %v", err)
	}

	return contracttest.Fixture{
		Provider:          implementation,
		InvalidateListing: controller.InvalidateListing,
		ChangeCapabilities: func(ctx context.Context) error {
			return controller.ChangeCapabilities(ctx, false, []domain.Capability{{
				Name:    "contract-changed",
				Version: 2,
			}})
		},
	}
}

func TestNewRejectsInvalidScenario(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*Scenario)
	}{
		{
			name: "endpoint snapshot mismatch",
			mutate: func(scenario *Scenario) {
				scenario.Snapshot.EndpointID = "ep_bbbbbbbbbbbbbbbbbbbbbbbbbb"
			},
		},
		{
			name: "snapshot capability session mismatch",
			mutate: func(scenario *Scenario) {
				scenario.Snapshot.Capabilities.Revision.SessionID =
					"sess_bbbbbbbbbbbbbbbbbbbbbbbbbb"
			},
		},
		{
			name: "root is not a directory",
			mutate: func(scenario *Scenario) {
				scenario.Root.Kind = domain.EntryFile
			},
		},
		{
			name: "duplicate children",
			mutate: func(scenario *Scenario) {
				scenario.Root.Children[1].Name = scenario.Root.Children[0].Name
			},
		},
		{
			name: "duplicate nested children",
			mutate: func(scenario *Scenario) {
				scenario.Root.Children[0].Children = append(
					scenario.Root.Children[0].Children,
					scenario.Root.Children[0].Children[0],
				)
			},
		},
		{
			name: "NUL in child name",
			mutate: func(scenario *Scenario) {
				scenario.Root.Children[0].Name = "nested\x00bad"
			},
		},
		{
			name: "empty child name",
			mutate: func(scenario *Scenario) {
				scenario.Root.Children[0].Name = ""
			},
		},
		{
			name: "separator in child name",
			mutate: func(scenario *Scenario) {
				scenario.Root.Children[0].Name = "nested/bad"
			},
		},
		{
			name: "dot child name",
			mutate: func(scenario *Scenario) {
				scenario.Root.Children[0].Name = "."
			},
		},
		{
			name: "dot-dot child name",
			mutate: func(scenario *Scenario) {
				scenario.Root.Children[0].Name = ".."
			},
		},
		{
			name: "NUL in symlink target",
			mutate: func(scenario *Scenario) {
				scenario.Root.Children[2].LinkTarget = "/contract-file\x00bad"
			},
		},
		{
			name: "file metadata size disagrees with data",
			mutate: func(scenario *Scenario) {
				wrong := uint64(999)
				scenario.Root.Children[1].Metadata.Size = &wrong
			},
		},
		{
			name: "zero default page limit",
			mutate: func(scenario *Scenario) {
				scenario.DefaultLimit = 0
			},
		},
		{
			name: "default page limit above contract maximum",
			mutate: func(scenario *Scenario) {
				scenario.DefaultLimit = 4097
			},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			scenario := validScenario(t)
			test.mutate(&scenario)

			if _, _, err := New(scenario); err == nil {
				t.Fatal("New() error = nil, want invalid-scenario error")
			}
		})
	}
}

func TestNewAcceptsInvalidUTF8ChildName(t *testing.T) {
	if _, _, err := New(validScenario(t)); err != nil {
		t.Fatalf("New(): %v", err)
	}
}

func TestNewRejectsCyclicChildren(t *testing.T) {
	const childEnvironment = "AMSFTP_FAKE_TEST_CYCLIC_CHILDREN"
	if os.Getenv(childEnvironment) == "1" {
		debug.SetMaxStack(1 << 20)
		children := make([]Node, 1)
		children[0] = Node{Name: "cycle", Kind: domain.EntryDirectory}
		children[0].Children = children
		scenario := validScenario(t)
		scenario.Root.Children = children
		_, _, err := New(scenario)
		if err == nil {
			t.Fatal("New(cyclic children) error = nil")
		}
		if !strings.Contains(err.Error(), "cyclic Node.Children") {
			t.Fatalf("New(cyclic children) error = %q, want descriptive cycle error", err)
		}
		return
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	//nolint:gosec // os.Args[0] is the current Go test binary, not external input.
	command := exec.CommandContext(
		ctx,
		os.Args[0],
		"-test.run=^TestNewRejectsCyclicChildren$",
		"-test.count=1",
	)
	command.Env = append(os.Environ(), childEnvironment+"=1")
	output, err := command.CombinedOutput()
	if ctx.Err() != nil {
		t.Fatalf("cyclic-children child process timed out: %v", ctx.Err())
	}
	if err != nil {
		const outputLimit = 4096
		if len(output) > outputLimit {
			output = output[:outputLimit]
		}
		t.Fatalf("cyclic-children child process failed: %v\n%s", err, output)
	}
}

func TestNewCopiesSharedChildrenInIndependentBranches(t *testing.T) {
	shared := []Node{{Name: "file", Kind: domain.EntryFile, Data: []byte("shared")}}
	scenario := validScenario(t)
	scenario.Root.Children = []Node{
		{Name: "left", Kind: domain.EntryDirectory, Children: shared},
		{Name: "right", Kind: domain.EntryDirectory, Children: shared},
	}
	implementation, _, err := New(scenario)
	if err != nil {
		t.Fatalf("New(shared acyclic children): %v", err)
	}
	mutable := requireMutable(t, implementation)
	left := domain.Location{EndpointID: contractEndpointID, Path: "/left/file"}
	leftEntry := statEntry(t, implementation, left)
	handle, err := mutable.OpenWrite(context.Background(), providerapi.OpenWriteRequest{
		Location:            left,
		Disposition:         providerapi.WriteTruncate,
		ExpectedFingerprint: &leftEntry.Fingerprint,
	})
	if err != nil {
		t.Fatalf("OpenWrite(left): %v", err)
	}
	writeAll(t, handle, []byte("left-only"))
	if err := handle.Close(context.Background()); err != nil {
		t.Fatalf("Close(left): %v", err)
	}
	right := domain.Location{EndpointID: contractEndpointID, Path: "/right/file"}
	if got := readFile(t, implementation, right); !bytes.Equal(got, []byte("shared")) {
		t.Fatalf("right shared-slice copy = %q, want shared", got)
	}
}

func TestNormalizeRejectsRootEscapeAndRelativeWithoutBase(t *testing.T) {
	implementation, _, err := New(validScenario(t))
	if err != nil {
		t.Fatalf("New(): %v", err)
	}

	tests := []struct {
		name    string
		request domain.NormalizeRequest
	}{
		{
			name: "relative path without base",
			request: domain.NormalizeRequest{
				EndpointID: contractEndpointID,
				Input:      "contract-file",
			},
		},
		{
			name: "absolute parent traversal escapes root",
			request: domain.NormalizeRequest{
				EndpointID: contractEndpointID,
				Input:      "/contract-directory/../../escape",
			},
		},
		{
			name: "base endpoint mismatch",
			request: domain.NormalizeRequest{
				EndpointID: contractEndpointID,
				Base: &domain.Location{
					EndpointID: "ep_bbbbbbbbbbbbbbbbbbbbbbbbbb",
					Path:       "/",
				},
				Input: "contract-file",
			},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			_, err := implementation.Normalize(context.Background(), test.request)
			if !domain.IsCode(err, domain.CodeInvalidArgument) {
				t.Fatalf("Normalize() error = %v, want invalid_argument", err)
			}
		})
	}
}

func TestNormalizeCleansWithinRootAndPreservesInvalidUTF8(t *testing.T) {
	implementation, _, err := New(validScenario(t))
	if err != nil {
		t.Fatalf("New(): %v", err)
	}

	input := string([]byte{'/', 0xff, '/', '/', '.', '/', 'x', '/', '.', '.', '/', 'y'})
	first, err := implementation.Normalize(context.Background(), domain.NormalizeRequest{
		EndpointID: contractEndpointID,
		Input:      input,
	})
	if err != nil {
		t.Fatalf("Normalize(): %v", err)
	}
	want := []byte{'/', 0xff, '/', 'y'}
	if got := []byte(first.Path); !bytes.Equal(got, want) {
		t.Fatalf("Normalize() path bytes = %x, want %x", got, want)
	}

	second, err := implementation.Normalize(context.Background(), domain.NormalizeRequest{
		EndpointID: contractEndpointID,
		Input:      string(first.Path),
	})
	if err != nil {
		t.Fatalf("Normalize(canonical): %v", err)
	}
	if second != first {
		t.Fatalf("Normalize(canonical) = %#v, want %#v", second, first)
	}
}

func TestSnapshotDoesNotShareScenarioCapabilitySlices(t *testing.T) {
	scenario := validScenario(t)
	scenario.Snapshot.Capabilities.Items[0].Constraints = []domain.CapabilityConstraint{{
		Name:  "mode",
		Value: "before",
	}}
	implementation, _, err := New(scenario)
	if err != nil {
		t.Fatalf("New(): %v", err)
	}

	scenario.Snapshot.Capabilities.Items[0].Name = "changed"
	scenario.Snapshot.Capabilities.Items[0].Constraints[0].Value = "changed"
	first, err := implementation.Snapshot(context.Background())
	if err != nil {
		t.Fatalf("Snapshot(): %v", err)
	}
	if first.Capabilities.Items[0].Name != "read" ||
		first.Capabilities.Items[0].Constraints[0].Value != "before" {
		t.Fatalf("Snapshot() observed caller mutation: %#v", first.Capabilities.Items)
	}

	first.Capabilities.Items[0].Constraints[0].Value = "returned mutation"
	second, err := implementation.Snapshot(context.Background())
	if err != nil {
		t.Fatalf("second Snapshot(): %v", err)
	}
	if got := second.Capabilities.Items[0].Constraints[0].Value; got != "before" {
		t.Fatalf("second Snapshot() constraint = %q, want before", got)
	}
}

func TestListCursorIsProviderIssuedAndDirectoryGenerationScoped(t *testing.T) {
	first, controller, err := New(validScenario(t))
	if err != nil {
		t.Fatalf("New(first): %v", err)
	}
	second, _, err := New(validScenario(t))
	if err != nil {
		t.Fatalf("New(second): %v", err)
	}
	root := domain.Location{EndpointID: contractEndpointID, Path: "/"}
	request := providerListRequest(root, 1, nil)
	page, err := first.List(context.Background(), request)
	if err != nil {
		t.Fatalf("first List(): %v", err)
	}
	if page.Done || page.NextCursor == "" {
		t.Fatal("first List() did not issue a continuation cursor")
	}

	crossFixture := request
	crossFixture.Cursor = page.NextCursor
	_, err = second.List(context.Background(), crossFixture)
	if got := requireCode(t, err, domain.CodeInvalidArgument).Effect; got != domain.EffectNone {
		t.Fatalf("cross-fixture cursor Effect = %q, want %q", got, domain.EffectNone)
	}

	forged := request
	forged.Cursor = page.NextCursor + "x"
	_, err = first.List(context.Background(), forged)
	requireCode(t, err, domain.CodeInvalidArgument)

	nested := domain.Location{EndpointID: contractEndpointID, Path: "/contract-directory"}
	if err := controller.InvalidateListing(context.Background(), nested); err != nil {
		t.Fatalf("InvalidateListing(nested): %v", err)
	}
	continuation := request
	continuation.Cursor = page.NextCursor
	if _, err := first.List(context.Background(), continuation); err != nil {
		t.Fatalf("root continuation after nested invalidation: %v", err)
	}

	fresh, err := first.List(context.Background(), request)
	if err != nil {
		t.Fatalf("fresh root List(): %v", err)
	}
	if err := controller.InvalidateListing(context.Background(), root); err != nil {
		t.Fatalf("InvalidateListing(root): %v", err)
	}
	stale := request
	stale.Cursor = fresh.NextCursor
	_, err = first.List(context.Background(), stale)
	if got := requireCode(t, err, domain.CodeConflict).Effect; got != domain.EffectNone {
		t.Fatalf("stale cursor Effect = %q, want %q", got, domain.EffectNone)
	}
}

func TestCursorRejectsReplacementDirectoryWithSameGeneration(t *testing.T) {
	scenario := validScenario(t)
	scenario.Root.Children = []Node{
		{
			Name: "a",
			Kind: domain.EntryDirectory,
			Children: []Node{
				{Name: "one", Kind: domain.EntryFile},
				{Name: "two", Kind: domain.EntryFile},
			},
		},
		{
			Name: "b",
			Kind: domain.EntryDirectory,
			Children: []Node{
				{Name: "other-one", Kind: domain.EntryFile},
				{Name: "other-two", Kind: domain.EntryFile},
			},
		},
	}
	implementation, _, err := New(scenario)
	if err != nil {
		t.Fatalf("New(): %v", err)
	}
	mutable := requireMutable(t, implementation)
	a := domain.Location{EndpointID: contractEndpointID, Path: "/a"}
	request := providerListRequest(a, 1, nil)
	first, err := implementation.List(context.Background(), request)
	if err != nil {
		t.Fatalf("List(/a): %v", err)
	}
	if first.Done {
		t.Fatal("List(/a) did not issue a cursor")
	}

	if _, err := mutable.Rename(context.Background(), providerapi.RenameRequest{
		Source:      a,
		Destination: domain.Location{EndpointID: contractEndpointID, Path: "/old-a"},
	}); err != nil {
		t.Fatalf("Rename(/a -> /old-a): %v", err)
	}
	if _, err := mutable.Rename(context.Background(), providerapi.RenameRequest{
		Source:      domain.Location{EndpointID: contractEndpointID, Path: "/b"},
		Destination: a,
	}); err != nil {
		t.Fatalf("Rename(/b -> /a): %v", err)
	}

	request.Cursor = first.NextCursor
	_, err = implementation.List(context.Background(), request)
	requireCode(t, err, domain.CodeConflict)
}

func TestCursorConflictsWhenListedDirectoryDisappears(t *testing.T) {
	for _, test := range []struct {
		name      string
		replace   bool
		freshCode domain.Code
	}{
		{name: "missing", freshCode: domain.CodeNotFound},
		{name: "replaced by file", replace: true, freshCode: domain.CodeInvalidArgument},
	} {
		t.Run(test.name, func(t *testing.T) {
			scenario := validScenario(t)
			scenario.Root.Children = []Node{
				{
					Name: "listed",
					Kind: domain.EntryDirectory,
					Children: []Node{
						{Name: "one", Kind: domain.EntryFile},
						{Name: "two", Kind: domain.EntryFile},
					},
				},
				{Name: "replacement", Kind: domain.EntryFile},
			}
			implementation, _, err := New(scenario)
			if err != nil {
				t.Fatalf("New(): %v", err)
			}
			mutable := requireMutable(t, implementation)
			listed := domain.Location{EndpointID: contractEndpointID, Path: "/listed"}
			request := providerListRequest(listed, 1, nil)
			first, err := implementation.List(context.Background(), request)
			if err != nil {
				t.Fatalf("List(first): %v", err)
			}
			if first.Done {
				t.Fatal("List(first) did not issue a cursor")
			}

			if _, err := mutable.Rename(context.Background(), providerapi.RenameRequest{
				Source:      listed,
				Destination: domain.Location{EndpointID: contractEndpointID, Path: "/moved"},
			}); err != nil {
				t.Fatalf("Rename(listed away): %v", err)
			}
			if test.replace {
				if _, err := mutable.Rename(context.Background(), providerapi.RenameRequest{
					Source: domain.Location{
						EndpointID: contractEndpointID,
						Path:       "/replacement",
					},
					Destination: listed,
				}); err != nil {
					t.Fatalf("Rename(file into listed path): %v", err)
				}
			}

			continuation := request
			continuation.Cursor = first.NextCursor
			_, err = implementation.List(context.Background(), continuation)
			requireCode(t, err, domain.CodeConflict)

			_, err = implementation.List(context.Background(), request)
			requireCode(t, err, test.freshCode)
		})
	}
}

func TestListSnapshotFreezesFutureDirectoryFingerprint(t *testing.T) {
	scenario := validScenario(t)
	scenario.Root.Children = []Node{
		{Name: "a-first", Kind: domain.EntryFile},
		{
			Name: "b-future",
			Kind: domain.EntryDirectory,
			Children: []Node{{
				Name: "existing",
				Kind: domain.EntryFile,
			}},
		},
	}
	implementation, _, err := New(scenario)
	if err != nil {
		t.Fatalf("New(): %v", err)
	}
	mutable := requireMutable(t, implementation)
	future := domain.Location{EndpointID: contractEndpointID, Path: "/b-future"}
	before := statEntry(t, implementation, future).Fingerprint
	root := domain.Location{EndpointID: contractEndpointID, Path: "/"}
	request := providerListRequest(root, 1, nil)
	first, err := implementation.List(context.Background(), request)
	if err != nil {
		t.Fatalf("List(first): %v", err)
	}
	if first.Done || len(first.Entries) != 1 || first.Entries[0].Name != "a-first" {
		t.Fatalf("List(first) = %#v, want a cursor before b-future", first)
	}

	createFile(t, mutable, domain.Location{
		EndpointID: contractEndpointID,
		Path:       "/b-future/new",
	}, []byte("new"))
	after := statEntry(t, implementation, future).Fingerprint
	if reflect.DeepEqual(after, before) {
		t.Fatal("nested mutation did not change the live directory fingerprint")
	}

	request.Cursor = first.NextCursor
	second, err := implementation.List(context.Background(), request)
	if err != nil {
		t.Fatalf("List(continuation): %v", err)
	}
	if len(second.Entries) != 1 || second.Entries[0].Name != "b-future" {
		t.Fatalf("List(continuation) entries = %#v", second.Entries)
	}
	if !reflect.DeepEqual(second.Entries[0].Fingerprint, before) {
		t.Fatalf(
			"continuation fingerprint = %#v, want signing-time %#v",
			second.Entries[0].Fingerprint,
			before,
		)
	}

	second.Entries[0].Fingerprint.VersionID = cloneString("caller-mutated")
	repeated, err := implementation.List(context.Background(), request)
	if err != nil {
		t.Fatalf("List(repeated continuation): %v", err)
	}
	if !reflect.DeepEqual(repeated.Entries[0].Fingerprint, before) {
		t.Fatalf("repeated continuation observed caller mutation: %#v", repeated.Entries[0])
	}
}

func TestListSnapshotFreezesFutureSymlinkResolution(t *testing.T) {
	scenario := validScenario(t)
	scenario.Root.Children = []Node{
		{Name: "a-first", Kind: domain.EntryFile},
		{Name: "b-link", Kind: domain.EntrySymlink, LinkTarget: "/targets/file"},
		{
			Name: "targets",
			Kind: domain.EntryDirectory,
			Children: []Node{{
				Name: "file",
				Kind: domain.EntryFile,
				Data: []byte("target"),
			}},
		},
	}
	implementation, _, err := New(scenario)
	if err != nil {
		t.Fatalf("New(): %v", err)
	}
	mutable := requireMutable(t, implementation)
	root := domain.Location{EndpointID: contractEndpointID, Path: "/"}
	request := providerListRequest(root, 1, nil)
	first, err := implementation.List(context.Background(), request)
	if err != nil {
		t.Fatalf("List(first): %v", err)
	}
	if first.Done || len(first.Entries) != 1 || first.Entries[0].Name != "a-first" {
		t.Fatalf("List(first) = %#v, want a cursor before b-link", first)
	}

	if err := mutable.Remove(context.Background(), providerapi.RemoveRequest{
		Location: domain.Location{EndpointID: contractEndpointID, Path: "/targets/file"},
	}); err != nil {
		t.Fatalf("Remove(external symlink target): %v", err)
	}

	request.Cursor = first.NextCursor
	second, err := implementation.List(context.Background(), request)
	if err != nil {
		t.Fatalf("List(continuation): %v", err)
	}
	if len(second.Entries) != 1 || second.Entries[0].Name != "b-link" {
		t.Fatalf("List(continuation) entries = %#v", second.Entries)
	}
	link := second.Entries[0].Symlink
	if link == nil || link.ResolvedKind == nil || *link.ResolvedKind != domain.EntryFile {
		t.Fatalf("continuation symlink = %#v, want signing-time resolved file", link)
	}
}

func TestListCursorBindsEverySortFieldAndCarriesOffset(t *testing.T) {
	implementation, _, err := New(validScenario(t))
	if err != nil {
		t.Fatalf("New(): %v", err)
	}
	root := domain.Location{EndpointID: contractEndpointID, Path: "/"}
	sortHint := &providerapi.SortHint{
		Key:              "name",
		Direction:        providerapi.SortAscending,
		DirectoriesFirst: true,
	}
	request := providerListRequest(root, 1, sortHint)
	first, err := implementation.List(context.Background(), request)
	if err != nil {
		t.Fatalf("List(first): %v", err)
	}
	if first.Done || first.NextCursor == "" {
		t.Fatal("List(first) did not issue a continuation cursor")
	}

	changes := []providerapi.SortHint{
		{Key: "other", Direction: providerapi.SortAscending, DirectoriesFirst: true},
		{Key: "name", Direction: providerapi.SortDescending, DirectoriesFirst: true},
		{Key: "name", Direction: providerapi.SortAscending, DirectoriesFirst: false},
	}
	for _, changed := range changes {
		continuation := request
		continuation.Cursor = first.NextCursor
		continuation.Sort = &changed
		_, err := implementation.List(context.Background(), continuation)
		requireCode(t, err, domain.CodeInvalidArgument)
	}

	seen := make(map[string]struct{})
	request.Cursor = ""
	for pageNumber := 0; pageNumber < 10; pageNumber++ {
		page, err := implementation.List(context.Background(), request)
		if err != nil {
			t.Fatalf("List(page %d): %v", pageNumber, err)
		}
		for _, entry := range page.Entries {
			if _, duplicate := seen[entry.Name]; duplicate {
				t.Fatalf("List(page %d) repeated entry %q", pageNumber, entry.Name)
			}
			seen[entry.Name] = struct{}{}
		}
		if page.Done {
			if len(seen) != len(validScenario(t).Root.Children) {
				t.Fatalf("listed entry count = %d, want %d", len(seen), len(validScenario(t).Root.Children))
			}
			return
		}
		request.Cursor = page.NextCursor
	}
	t.Fatal("List() did not terminate")
}

func TestStatSymlinkResolutionFailuresTerminate(t *testing.T) {
	tests := []struct {
		name     string
		children []Node
		path     domain.CanonicalPath
		wantCode domain.Code
	}{
		{
			name: "self loop",
			children: []Node{{
				Name:       "self",
				Kind:       domain.EntrySymlink,
				LinkTarget: "/self",
			}},
			path:     "/self",
			wantCode: domain.CodeConflict,
		},
		{
			name: "two-node loop",
			children: []Node{
				{Name: "a", Kind: domain.EntrySymlink, LinkTarget: "/b"},
				{Name: "b", Kind: domain.EntrySymlink, LinkTarget: "/a"},
			},
			path:     "/a",
			wantCode: domain.CodeConflict,
		},
		{
			name: "dangling",
			children: []Node{{
				Name:       "dangling",
				Kind:       domain.EntrySymlink,
				LinkTarget: "/missing",
			}},
			path:     "/dangling",
			wantCode: domain.CodeNotFound,
		},
		{
			name: "target escapes root",
			children: []Node{{
				Name:       "escape",
				Kind:       domain.EntrySymlink,
				LinkTarget: "../../outside",
			}},
			path:     "/escape",
			wantCode: domain.CodeInvalidArgument,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			scenario := validScenario(t)
			scenario.Root.Children = test.children
			implementation, _, err := New(scenario)
			if err != nil {
				t.Fatalf("New(): %v", err)
			}
			location := domain.Location{EndpointID: contractEndpointID, Path: test.path}
			entry, err := implementation.Stat(context.Background(), providerapi.StatRequest{
				Location: location,
			})
			if err != nil {
				t.Fatalf("Stat(lstat): %v", err)
			}
			if entry.Kind != domain.EntrySymlink {
				t.Fatalf("Stat(lstat).Kind = %q, want symlink", entry.Kind)
			}

			_, err = implementation.Stat(context.Background(), providerapi.StatRequest{
				Location:       location,
				FollowSymlinks: true,
			})
			if got := requireCode(t, err, test.wantCode).Effect; got != domain.EffectNone {
				t.Fatalf("Stat(follow) Effect = %q, want %q", got, domain.EffectNone)
			}
		})
	}
}

func TestOpenReadOwnsScenarioBytesAndChecksFingerprintFirst(t *testing.T) {
	scenario := validScenario(t)
	fileID := "contract-file-id"
	scenario.Root.Children[1].Metadata.FileID = &fileID
	implementation, _, err := New(scenario)
	if err != nil {
		t.Fatalf("New(): %v", err)
	}
	scenario.Root.Children[1].Name = "changed"
	scenario.Root.Children[1].Data[0] = 'X'
	*scenario.Root.Children[1].Metadata.FileID = "changed"

	location := domain.Location{EndpointID: contractEndpointID, Path: "/contract-file"}
	entry, err := implementation.Stat(context.Background(), providerapi.StatRequest{Location: location})
	if err != nil {
		t.Fatalf("Stat(): %v", err)
	}
	if entry.Metadata.FileID == nil || *entry.Metadata.FileID != "contract-file-id" {
		t.Fatalf("Stat().Metadata.FileID = %#v, want contract-file-id", entry.Metadata.FileID)
	}

	mismatch := entry.Fingerprint
	wrongSize := uint64(999)
	mismatch.Size = &wrongSize
	_, err = implementation.OpenRead(context.Background(), providerapi.OpenReadRequest{
		Location:            location,
		ExpectedFingerprint: &mismatch,
	})
	if got := requireCode(t, err, domain.CodeConflict).Effect; got != domain.EffectNone {
		t.Fatalf("fingerprint mismatch Effect = %q, want %q", got, domain.EffectNone)
	}

	handle, err := implementation.OpenRead(context.Background(), providerapi.OpenReadRequest{
		Location:            location,
		ExpectedFingerprint: &entry.Fingerprint,
	})
	if err != nil {
		t.Fatalf("OpenRead(): %v", err)
	}
	firstInfo := handle.Info()
	*firstInfo.Entry.Metadata.Size = 0
	secondInfo := handle.Info()
	if secondInfo.Entry.Metadata.Size == nil || *secondInfo.Entry.Metadata.Size != uint64(len("contract data")) {
		t.Fatalf("second Info().Metadata.Size = %#v", secondInfo.Entry.Metadata.Size)
	}
	if got := readAll(t, handle); !bytes.Equal(got, []byte("contract data")) {
		t.Fatalf("read bytes = %q, want contract data", got)
	}
	if err := handle.Close(context.Background()); err != nil {
		t.Fatalf("Close(): %v", err)
	}
}

func TestOpenWriteDispositionsAndFingerprintPrecondition(t *testing.T) {
	implementation, _, err := New(validScenario(t))
	if err != nil {
		t.Fatalf("New(): %v", err)
	}
	mutable := requireMutable(t, implementation)
	created := domain.Location{EndpointID: contractEndpointID, Path: "/created"}

	create, err := mutable.OpenWrite(context.Background(), providerapi.OpenWriteRequest{
		Location:    created,
		Disposition: providerapi.WriteCreateNew,
	})
	if err != nil {
		t.Fatalf("OpenWrite(create): %v", err)
	}
	input := []byte("created")
	writeAll(t, create, input)
	input[0] = 'X'
	if err := create.Sync(context.Background()); err != nil {
		t.Fatalf("Sync(create): %v", err)
	}
	if err := create.Close(context.Background()); err != nil {
		t.Fatalf("Close(create): %v", err)
	}
	if got := readFile(t, implementation, created); !bytes.Equal(got, []byte("created")) {
		t.Fatalf("created bytes = %q, want created", got)
	}

	_, err = mutable.OpenWrite(context.Background(), providerapi.OpenWriteRequest{
		Location:    created,
		Disposition: providerapi.WriteCreateNew,
	})
	if got := requireCode(t, err, domain.CodeAlreadyExists).Effect; got != domain.EffectNone {
		t.Fatalf("create-existing Effect = %q, want %q", got, domain.EffectNone)
	}

	createdEntry, err := implementation.Stat(
		context.Background(),
		providerapi.StatRequest{Location: created},
	)
	if err != nil {
		t.Fatalf("Stat(created): %v", err)
	}
	resume, err := mutable.OpenWrite(context.Background(), providerapi.OpenWriteRequest{
		Location:            created,
		Offset:              int64(len("created")),
		Disposition:         providerapi.WriteResumeExisting,
		ExpectedFingerprint: &createdEntry.Fingerprint,
	})
	if err != nil {
		t.Fatalf("OpenWrite(resume): %v", err)
	}
	writeAll(t, resume, []byte("-resumed"))
	if err := resume.Close(context.Background()); err != nil {
		t.Fatalf("Close(resume): %v", err)
	}
	if got := readFile(t, implementation, created); !bytes.Equal(got, []byte("created-resumed")) {
		t.Fatalf("resumed bytes = %q, want created-resumed", got)
	}

	beforeTruncate, err := implementation.Stat(
		context.Background(),
		providerapi.StatRequest{Location: created},
	)
	if err != nil {
		t.Fatalf("Stat(before truncate): %v", err)
	}
	mismatch := beforeTruncate.Fingerprint
	wrongVersion := "wrong-version"
	mismatch.VersionID = &wrongVersion
	_, err = mutable.OpenWrite(context.Background(), providerapi.OpenWriteRequest{
		Location:            created,
		Disposition:         providerapi.WriteTruncate,
		ExpectedFingerprint: &mismatch,
	})
	if got := requireCode(t, err, domain.CodeConflict).Effect; got != domain.EffectNone {
		t.Fatalf("truncate mismatch Effect = %q, want %q", got, domain.EffectNone)
	}
	if got := readFile(t, implementation, created); !bytes.Equal(got, []byte("created-resumed")) {
		t.Fatalf("bytes after rejected truncate = %q", got)
	}

	truncate, err := mutable.OpenWrite(context.Background(), providerapi.OpenWriteRequest{
		Location:            created,
		Disposition:         providerapi.WriteTruncate,
		ExpectedFingerprint: &beforeTruncate.Fingerprint,
	})
	if err != nil {
		t.Fatalf("OpenWrite(truncate): %v", err)
	}
	writeAll(t, truncate, []byte("reset"))
	if err := truncate.Close(context.Background()); err != nil {
		t.Fatalf("Close(truncate): %v", err)
	}
	if got := readFile(t, implementation, created); !bytes.Equal(got, []byte("reset")) {
		t.Fatalf("truncated bytes = %q, want reset", got)
	}

	missing := domain.Location{EndpointID: contractEndpointID, Path: "/missing-write"}
	for _, disposition := range []providerapi.WriteDisposition{
		providerapi.WriteResumeExisting,
		providerapi.WriteTruncate,
	} {
		_, err := mutable.OpenWrite(context.Background(), providerapi.OpenWriteRequest{
			Location:    missing,
			Disposition: disposition,
		})
		requireCode(t, err, domain.CodeNotFound)
	}
}

func TestMutableTreeOperationsEnforcePreconditions(t *testing.T) {
	implementation, _, err := New(validScenario(t))
	if err != nil {
		t.Fatalf("New(): %v", err)
	}
	mutable := requireMutable(t, implementation)
	directory := domain.Location{EndpointID: contractEndpointID, Path: "/created-directory"}
	created, err := mutable.Mkdir(context.Background(), providerapi.MkdirRequest{
		Location:  directory,
		Exclusive: true,
	})
	if err != nil {
		t.Fatalf("Mkdir(): %v", err)
	}
	if created.Kind != domain.EntryDirectory || created.Location != directory {
		t.Fatalf("Mkdir() = %#v", created)
	}
	if _, err := mutable.Mkdir(context.Background(), providerapi.MkdirRequest{Location: directory}); err != nil {
		t.Fatalf("Mkdir(non-exclusive existing): %v", err)
	}
	_, err = mutable.Mkdir(context.Background(), providerapi.MkdirRequest{
		Location:  directory,
		Exclusive: true,
	})
	if got := requireCode(t, err, domain.CodeAlreadyExists).Effect; got != domain.EffectNone {
		t.Fatalf("exclusive mkdir Effect = %q, want %q", got, domain.EffectNone)
	}

	child := domain.Location{EndpointID: contractEndpointID, Path: "/created-directory/child"}
	createFile(t, mutable, child, []byte("child"))
	err = mutable.Remove(context.Background(), providerapi.RemoveRequest{Location: directory})
	if got := requireCode(t, err, domain.CodeConflict).Effect; got != domain.EffectNone {
		t.Fatalf("remove non-empty directory Effect = %q, want %q", got, domain.EffectNone)
	}
	if err := mutable.Remove(context.Background(), providerapi.RemoveRequest{Location: child}); err != nil {
		t.Fatalf("Remove(child): %v", err)
	}
	if err := mutable.Remove(context.Background(), providerapi.RemoveRequest{Location: directory}); err != nil {
		t.Fatalf("Remove(empty directory): %v", err)
	}

	source := domain.Location{EndpointID: contractEndpointID, Path: "/rename-source"}
	destination := domain.Location{EndpointID: contractEndpointID, Path: "/rename-destination"}
	createFile(t, mutable, source, []byte("source"))
	createFile(t, mutable, destination, []byte("destination"))
	_, err = mutable.Rename(context.Background(), providerapi.RenameRequest{
		Source:      source,
		Destination: destination,
	})
	if got := requireCode(t, err, domain.CodeAlreadyExists).Effect; got != domain.EffectNone {
		t.Fatalf("rename without replace Effect = %q, want %q", got, domain.EffectNone)
	}

	sourceEntry := statEntry(t, implementation, source)
	destinationEntry := statEntry(t, implementation, destination)
	wrongSource := sourceEntry.Fingerprint
	wrongVersion := "wrong"
	wrongSource.VersionID = &wrongVersion
	_, err = mutable.Rename(context.Background(), providerapi.RenameRequest{
		Source:         source,
		Destination:    destination,
		Replace:        true,
		ExpectedSource: &wrongSource,
	})
	if got := requireCode(t, err, domain.CodeConflict).Effect; got != domain.EffectNone {
		t.Fatalf("rename source mismatch Effect = %q, want %q", got, domain.EffectNone)
	}
	wrongDestination := destinationEntry.Fingerprint
	wrongDestination.VersionID = &wrongVersion
	_, err = mutable.Rename(context.Background(), providerapi.RenameRequest{
		Source:              source,
		Destination:         destination,
		Replace:             true,
		ExpectedSource:      &sourceEntry.Fingerprint,
		ExpectedDestination: &wrongDestination,
	})
	if got := requireCode(t, err, domain.CodeConflict).Effect; got != domain.EffectNone {
		t.Fatalf("rename destination mismatch Effect = %q, want %q", got, domain.EffectNone)
	}
	result, err := mutable.Rename(context.Background(), providerapi.RenameRequest{
		Source:              source,
		Destination:         destination,
		Replace:             true,
		ExpectedSource:      &sourceEntry.Fingerprint,
		ExpectedDestination: &destinationEntry.Fingerprint,
	})
	if err != nil {
		t.Fatalf("Rename(replace): %v", err)
	}
	if !result.Atomic || !result.Replaced {
		t.Fatalf("Rename(replace) = %#v, want atomic replacement", result)
	}
	if got := readFile(t, implementation, destination); !bytes.Equal(got, []byte("source")) {
		t.Fatalf("replacement bytes = %q, want source", got)
	}
	requireStatCode(t, implementation, source, domain.CodeNotFound)

	file := domain.Location{EndpointID: contractEndpointID, Path: "/contract-file"}
	fileEntry := statEntry(t, implementation, file)
	wrongFile := fileEntry.Fingerprint
	wrongFile.VersionID = &wrongVersion
	err = mutable.Remove(context.Background(), providerapi.RemoveRequest{
		Location: file,
		Expected: &wrongFile,
	})
	if got := requireCode(t, err, domain.CodeConflict).Effect; got != domain.EffectNone {
		t.Fatalf("remove mismatch Effect = %q, want %q", got, domain.EffectNone)
	}
	if _, err := implementation.Stat(context.Background(), providerapi.StatRequest{Location: file}); err != nil {
		t.Fatalf("Stat(file after rejected remove): %v", err)
	}

	link := domain.Location{EndpointID: contractEndpointID, Path: "/contract-link"}
	if err := mutable.Remove(context.Background(), providerapi.RemoveRequest{Location: link}); err != nil {
		t.Fatalf("Remove(symlink): %v", err)
	}
	if _, err := implementation.Stat(context.Background(), providerapi.StatRequest{Location: file}); err != nil {
		t.Fatalf("symlink removal affected target: %v", err)
	}

	subdirectory := domain.Location{
		EndpointID: contractEndpointID,
		Path:       "/contract-directory/subdirectory",
	}
	if _, err := mutable.Mkdir(context.Background(), providerapi.MkdirRequest{
		Location:  subdirectory,
		Exclusive: true,
	}); err != nil {
		t.Fatalf("Mkdir(subdirectory): %v", err)
	}
	_, err = mutable.Rename(context.Background(), providerapi.RenameRequest{
		Source: domain.Location{
			EndpointID: contractEndpointID,
			Path:       "/contract-directory",
		},
		Destination: domain.Location{
			EndpointID: contractEndpointID,
			Path:       "/contract-directory/subdirectory/moved",
		},
	})
	requireCode(t, err, domain.CodeInvalidArgument)

	root := domain.Location{EndpointID: contractEndpointID, Path: "/"}
	if _, err := mutable.Mkdir(context.Background(), providerapi.MkdirRequest{Location: root}); !domain.IsCode(err, domain.CodeInvalidArgument) {
		t.Fatalf("Mkdir(root) error = %v, want invalid_argument", err)
	}
	if err := mutable.Remove(context.Background(), providerapi.RemoveRequest{Location: root}); !domain.IsCode(err, domain.CodeInvalidArgument) {
		t.Fatalf("Remove(root) error = %v, want invalid_argument", err)
	}
}

func TestWriteHandleStaysBoundAcrossRenameAndABA(t *testing.T) {
	implementation, _, err := New(validScenario(t))
	if err != nil {
		t.Fatalf("New(): %v", err)
	}
	mutable := requireMutable(t, implementation)
	original := domain.Location{EndpointID: contractEndpointID, Path: "/contract-file"}
	originalEntry := statEntry(t, implementation, original)
	handle, err := mutable.OpenWrite(context.Background(), providerapi.OpenWriteRequest{
		Location:            original,
		Offset:              int64(len("contract data")),
		Disposition:         providerapi.WriteResumeExisting,
		ExpectedFingerprint: &originalEntry.Fingerprint,
	})
	if err != nil {
		t.Fatalf("OpenWrite(original): %v", err)
	}
	renamed := domain.Location{EndpointID: contractEndpointID, Path: "/renamed"}
	if _, err := mutable.Rename(context.Background(), providerapi.RenameRequest{
		Source:      original,
		Destination: renamed,
	}); err != nil {
		t.Fatalf("Rename(): %v", err)
	}
	createFile(t, mutable, original, []byte("replacement"))
	writeAll(t, handle, []byte("-renamed"))
	if err := handle.Close(context.Background()); err != nil {
		t.Fatalf("Close(renamed handle): %v", err)
	}
	if got := readFile(t, implementation, renamed); !bytes.Equal(got, []byte("contract data-renamed")) {
		t.Fatalf("renamed bytes = %q", got)
	}
	if got := readFile(t, implementation, original); !bytes.Equal(got, []byte("replacement")) {
		t.Fatalf("replacement bytes = %q", got)
	}

	renamedEntry := statEntry(t, implementation, renamed)
	detached, err := mutable.OpenWrite(context.Background(), providerapi.OpenWriteRequest{
		Location:            renamed,
		Offset:              int64(len("contract data-renamed")),
		Disposition:         providerapi.WriteResumeExisting,
		ExpectedFingerprint: &renamedEntry.Fingerprint,
	})
	if err != nil {
		t.Fatalf("OpenWrite(detached): %v", err)
	}
	if err := mutable.Remove(context.Background(), providerapi.RemoveRequest{
		Location: renamed,
		Expected: &renamedEntry.Fingerprint,
	}); err != nil {
		t.Fatalf("Remove(renamed): %v", err)
	}
	createFile(t, mutable, renamed, []byte("new-node"))
	writeAll(t, detached, []byte("-detached"))
	if err := detached.Close(context.Background()); err != nil {
		t.Fatalf("Close(detached): %v", err)
	}
	if got := readFile(t, implementation, renamed); !bytes.Equal(got, []byte("new-node")) {
		t.Fatalf("ABA replacement bytes = %q, want new-node", got)
	}
}

func TestRenameRejectsDescendantThroughSymlinkAlias(t *testing.T) {
	scenario := validScenario(t)
	scenario.Root.Children = append(scenario.Root.Children, Node{
		Name:       "directory-alias",
		Kind:       domain.EntrySymlink,
		LinkTarget: "/contract-directory",
	})
	implementation, _, err := New(scenario)
	if err != nil {
		t.Fatalf("New(): %v", err)
	}
	mutable := requireMutable(t, implementation)
	_, err = mutable.Rename(context.Background(), providerapi.RenameRequest{
		Source: domain.Location{
			EndpointID: contractEndpointID,
			Path:       "/contract-directory",
		},
		Destination: domain.Location{
			EndpointID: contractEndpointID,
			Path:       "/directory-alias/moved",
		},
	})
	if got := requireCode(t, err, domain.CodeInvalidArgument).Effect; got != domain.EffectNone {
		t.Fatalf("alias-descendant rename Effect = %q, want %q", got, domain.EffectNone)
	}
}

func TestRenameSameDirentThroughSymlinkParentIsNoOp(t *testing.T) {
	for _, replace := range []bool{false, true} {
		t.Run(fmt.Sprintf("replace=%t", replace), func(t *testing.T) {
			scenario := validScenario(t)
			scenario.Root.Children = []Node{
				{
					Name: "real",
					Kind: domain.EntryDirectory,
					Children: []Node{
						{Name: "file", Kind: domain.EntryFile, Data: []byte("original")},
						{Name: "second", Kind: domain.EntryFile},
					},
				},
				{Name: "alias", Kind: domain.EntrySymlink, LinkTarget: "/real"},
			}
			implementation, _, err := New(scenario)
			if err != nil {
				t.Fatalf("New(): %v", err)
			}
			mutable := requireMutable(t, implementation)
			source := domain.Location{EndpointID: contractEndpointID, Path: "/real/file"}
			destination := domain.Location{EndpointID: contractEndpointID, Path: "/alias/file"}
			sourceEntry := statEntry(t, implementation, source)
			destinationEntry := statEntry(t, implementation, destination)
			wrongSource := sourceEntry.Fingerprint
			wrongSource.VersionID = cloneString("wrong-source")
			_, err = mutable.Rename(context.Background(), providerapi.RenameRequest{
				Source:              source,
				Destination:         destination,
				Replace:             replace,
				ExpectedSource:      &wrongSource,
				ExpectedDestination: &destinationEntry.Fingerprint,
			})
			requireCode(t, err, domain.CodeConflict)

			wrongDestination := destinationEntry.Fingerprint
			wrongDestination.VersionID = cloneString("wrong-destination")
			_, err = mutable.Rename(context.Background(), providerapi.RenameRequest{
				Source:              source,
				Destination:         destination,
				Replace:             replace,
				ExpectedSource:      &sourceEntry.Fingerprint,
				ExpectedDestination: &wrongDestination,
			})
			requireCode(t, err, domain.CodeConflict)

			directory := domain.Location{EndpointID: contractEndpointID, Path: "/real"}
			request := providerListRequest(directory, 1, nil)
			first, err := implementation.List(context.Background(), request)
			if err != nil {
				t.Fatalf("List(first): %v", err)
			}
			if first.Done {
				t.Fatal("List(first) did not issue a cursor")
			}

			result, err := mutable.Rename(context.Background(), providerapi.RenameRequest{
				Source:              source,
				Destination:         destination,
				Replace:             replace,
				ExpectedSource:      &sourceEntry.Fingerprint,
				ExpectedDestination: &destinationEntry.Fingerprint,
			})
			if err != nil {
				t.Fatalf("Rename(same dirent): %v", err)
			}
			if !result.Atomic || result.Replaced {
				t.Fatalf("Rename(same dirent) = %#v, want atomic non-replacing no-op", result)
			}

			request.Cursor = first.NextCursor
			if _, err := implementation.List(context.Background(), request); err != nil {
				t.Fatalf("listing cursor after same-dirent rename: %v", err)
			}
			if got := readFile(t, implementation, source); !bytes.Equal(got, []byte("original")) {
				t.Fatalf("source bytes after no-op rename = %q", got)
			}
			if got := readFile(t, implementation, destination); !bytes.Equal(got, []byte("original")) {
				t.Fatalf("destination bytes after no-op rename = %q", got)
			}
		})
	}
}

func TestMutationsInvalidateOnlyParentDirectoryListing(t *testing.T) {
	implementation, _, err := New(validScenario(t))
	if err != nil {
		t.Fatalf("New(): %v", err)
	}
	mutable := requireMutable(t, implementation)
	root := domain.Location{EndpointID: contractEndpointID, Path: "/"}
	rootRequest := providerListRequest(root, 1, nil)
	rootFirst, err := implementation.List(context.Background(), rootRequest)
	if err != nil {
		t.Fatalf("List(root): %v", err)
	}

	nested := domain.Location{EndpointID: contractEndpointID, Path: "/contract-directory"}
	second := domain.Location{EndpointID: contractEndpointID, Path: "/contract-directory/second"}
	createFile(t, mutable, second, []byte("second"))
	rootRequest.Cursor = rootFirst.NextCursor
	if _, err := implementation.List(context.Background(), rootRequest); err != nil {
		t.Fatalf("root cursor after nested mutation: %v", err)
	}

	nestedRequest := providerListRequest(nested, 1, nil)
	nestedFirst, err := implementation.List(context.Background(), nestedRequest)
	if err != nil {
		t.Fatalf("List(nested): %v", err)
	}
	if nestedFirst.Done {
		t.Fatal("nested fixture did not issue cursor")
	}
	nestedFile := domain.Location{EndpointID: contractEndpointID, Path: "/contract-directory/nested-file"}
	nestedEntry := statEntry(t, implementation, nestedFile)
	handle, err := mutable.OpenWrite(context.Background(), providerapi.OpenWriteRequest{
		Location:            nestedFile,
		Offset:              int64(len("nested data")),
		Disposition:         providerapi.WriteResumeExisting,
		ExpectedFingerprint: &nestedEntry.Fingerprint,
	})
	if err != nil {
		t.Fatalf("OpenWrite(nested): %v", err)
	}
	writeAll(t, handle, []byte("!"))
	if err := handle.Close(context.Background()); err != nil {
		t.Fatalf("Close(nested): %v", err)
	}
	nestedRequest.Cursor = nestedFirst.NextCursor
	_, err = implementation.List(context.Background(), nestedRequest)
	requireCode(t, err, domain.CodeConflict)
}

func TestMutationThroughSymlinkInvalidatesUnderlyingDirectoryCursor(t *testing.T) {
	scenario := validScenario(t)
	scenario.Root.Children[0].Children = append(scenario.Root.Children[0].Children, Node{
		Name: "second",
		Kind: domain.EntryFile,
		Data: []byte("second"),
	})
	scenario.Root.Children = append(scenario.Root.Children, Node{
		Name:       "directory-alias",
		Kind:       domain.EntrySymlink,
		LinkTarget: "/contract-directory",
	})
	implementation, _, err := New(scenario)
	if err != nil {
		t.Fatalf("New(): %v", err)
	}
	mutable := requireMutable(t, implementation)
	directory := domain.Location{EndpointID: contractEndpointID, Path: "/contract-directory"}
	alias := domain.Location{EndpointID: contractEndpointID, Path: "/directory-alias"}
	issueCursor := func(location domain.Location) providerapi.PageCursor {
		t.Helper()
		page, err := implementation.List(context.Background(), providerListRequest(location, 1, nil))
		if err != nil {
			t.Fatalf("List(%s): %v", location.Path, err)
		}
		if page.Done {
			t.Fatalf("List(%s) did not issue cursor", location.Path)
		}
		return page.NextCursor
	}
	assertStale := func(location domain.Location, cursor providerapi.PageCursor) {
		t.Helper()
		request := providerListRequest(location, 1, nil)
		request.Cursor = cursor
		_, err := implementation.List(context.Background(), request)
		requireCode(t, err, domain.CodeConflict)
	}

	realCursor := issueCursor(directory)
	aliasCursor := issueCursor(alias)
	created, err := mutable.OpenWrite(context.Background(), providerapi.OpenWriteRequest{
		Location: domain.Location{
			EndpointID: contractEndpointID,
			Path:       "/directory-alias/third",
		},
		Disposition: providerapi.WriteCreateNew,
	})
	if err != nil {
		t.Fatalf("OpenWrite(create through alias): %v", err)
	}
	if err := created.Close(context.Background()); err != nil {
		t.Fatalf("Close(create through alias): %v", err)
	}
	assertStale(directory, realCursor)
	assertStale(alias, aliasCursor)

	realCursor = issueCursor(directory)
	aliasCursor = issueCursor(alias)
	created, err = mutable.OpenWrite(context.Background(), providerapi.OpenWriteRequest{
		Location: domain.Location{
			EndpointID: contractEndpointID,
			Path:       "/contract-directory/fourth",
		},
		Disposition: providerapi.WriteCreateNew,
	})
	if err != nil {
		t.Fatalf("OpenWrite(create through real path): %v", err)
	}
	if err := created.Close(context.Background()); err != nil {
		t.Fatalf("Close(create through real path): %v", err)
	}
	assertStale(directory, realCursor)
	assertStale(alias, aliasCursor)
}

func TestControllerKeepsListingAndCapabilityGenerationsIndependent(t *testing.T) {
	implementation, controller, err := New(validScenario(t))
	if err != nil {
		t.Fatalf("New(): %v", err)
	}
	root := domain.Location{EndpointID: contractEndpointID, Path: "/"}
	request := providerListRequest(root, 1, nil)
	firstPage, err := implementation.List(context.Background(), request)
	if err != nil {
		t.Fatalf("List(first): %v", err)
	}
	firstSnapshot, err := implementation.Snapshot(context.Background())
	if err != nil {
		t.Fatalf("Snapshot(first): %v", err)
	}
	constraints := []domain.CapabilityConstraint{{Name: "mode", Value: "before"}}
	items := []domain.Capability{{
		Name:        "changed",
		Version:     2,
		Constraints: constraints,
	}}
	if err := controller.ChangeCapabilities(context.Background(), false, items); err != nil {
		t.Fatalf("ChangeCapabilities(): %v", err)
	}
	items[0].Name = "caller-mutated"
	constraints[0].Value = "caller-mutated"
	secondSnapshot, err := implementation.Snapshot(context.Background())
	if err != nil {
		t.Fatalf("Snapshot(second): %v", err)
	}
	if secondSnapshot.SessionID != firstSnapshot.SessionID ||
		secondSnapshot.Capabilities.Revision.Generation !=
			firstSnapshot.Capabilities.Revision.Generation+1 {
		t.Fatalf("capability revision transition = %#v -> %#v", firstSnapshot, secondSnapshot)
	}
	if got := secondSnapshot.Capabilities.Items[0]; got.Name != "changed" || got.Constraints[0].Value != "before" {
		t.Fatalf("capability payload observed caller mutation: %#v", got)
	}
	request.Cursor = firstPage.NextCursor
	if _, err := implementation.List(context.Background(), request); err != nil {
		t.Fatalf("listing cursor after capability change: %v", err)
	}

	request.Cursor = ""
	freshPage, err := implementation.List(context.Background(), request)
	if err != nil {
		t.Fatalf("List(fresh): %v", err)
	}
	file := domain.Location{EndpointID: contractEndpointID, Path: "/contract-file"}
	if err := controller.InvalidateListing(context.Background(), file); !domain.IsCode(err, domain.CodeInvalidArgument) {
		t.Fatalf("InvalidateListing(file) error = %v, want invalid_argument", err)
	}
	missing := domain.Location{EndpointID: contractEndpointID, Path: "/missing"}
	if err := controller.InvalidateListing(context.Background(), missing); !domain.IsCode(err, domain.CodeNotFound) {
		t.Fatalf("InvalidateListing(missing) error = %v, want not_found", err)
	}
	if err := controller.InvalidateListing(context.Background(), root); err != nil {
		t.Fatalf("InvalidateListing(root): %v", err)
	}
	request.Cursor = freshPage.NextCursor
	_, err = implementation.List(context.Background(), request)
	requireCode(t, err, domain.CodeConflict)
	afterInvalidation, err := implementation.Snapshot(context.Background())
	if err != nil {
		t.Fatalf("Snapshot(after invalidation): %v", err)
	}
	if !reflect.DeepEqual(afterInvalidation, secondSnapshot) {
		t.Fatalf("listing invalidation changed snapshot: before=%#v after=%#v", secondSnapshot, afterInvalidation)
	}
}

func TestReturnedEntriesAndOpenReadAreIndependentSnapshots(t *testing.T) {
	implementation, _, err := New(validScenario(t))
	if err != nil {
		t.Fatalf("New(): %v", err)
	}
	mutable := requireMutable(t, implementation)
	root := domain.Location{EndpointID: contractEndpointID, Path: "/"}
	firstPage, err := implementation.List(
		context.Background(),
		providerListRequest(root, 4096, nil),
	)
	if err != nil {
		t.Fatalf("List(first): %v", err)
	}
	if len(firstPage.Entries) == 0 || firstPage.DirectoryFingerprint.VersionID == nil {
		t.Fatalf("List(first) did not return owned entries/fingerprint: %#v", firstPage)
	}
	firstPage.Entries[0].Name = "caller-mutated"
	*firstPage.DirectoryFingerprint.VersionID = "caller-mutated"
	secondPage, err := implementation.List(
		context.Background(),
		providerListRequest(root, 4096, nil),
	)
	if err != nil {
		t.Fatalf("List(second): %v", err)
	}
	if secondPage.Entries[0].Name == "caller-mutated" ||
		*secondPage.DirectoryFingerprint.VersionID == "caller-mutated" {
		t.Fatalf("List(second) observed returned-value mutation: %#v", secondPage)
	}

	file := domain.Location{EndpointID: contractEndpointID, Path: "/contract-file"}
	before := statEntry(t, implementation, file)
	handle, err := implementation.OpenRead(context.Background(), providerapi.OpenReadRequest{
		Location:            file,
		ExpectedFingerprint: &before.Fingerprint,
	})
	if err != nil {
		t.Fatalf("OpenRead(snapshot): %v", err)
	}
	before.Metadata.Size = cloneUint64(999)
	before.Fingerprint.VersionID = cloneString("caller-mutated")
	afterReturnedMutation := statEntry(t, implementation, file)
	if afterReturnedMutation.Metadata.Size == nil ||
		*afterReturnedMutation.Metadata.Size != uint64(len("contract data")) ||
		*afterReturnedMutation.Fingerprint.VersionID == "caller-mutated" {
		t.Fatalf("Stat() observed returned-value mutation: %#v", afterReturnedMutation)
	}

	truncate, err := mutable.OpenWrite(context.Background(), providerapi.OpenWriteRequest{
		Location:            file,
		Disposition:         providerapi.WriteTruncate,
		ExpectedFingerprint: &afterReturnedMutation.Fingerprint,
	})
	if err != nil {
		t.Fatalf("OpenWrite(truncate): %v", err)
	}
	writeAll(t, truncate, []byte("new data"))
	if err := truncate.Close(context.Background()); err != nil {
		t.Fatalf("Close(truncate): %v", err)
	}
	if got := readAll(t, handle); !bytes.Equal(got, []byte("contract data")) {
		t.Fatalf("already-open read bytes = %q, want contract data", got)
	}
	info := handle.Info()
	if info.Entry.Metadata.Size == nil || *info.Entry.Metadata.Size != uint64(len("contract data")) {
		t.Fatalf("already-open Info() changed after write: %#v", info)
	}
	if err := handle.Close(context.Background()); err != nil {
		t.Fatalf("Close(snapshot read): %v", err)
	}
	if got := readFile(t, implementation, file); !bytes.Equal(got, []byte("new data")) {
		t.Fatalf("new OpenRead bytes = %q, want new data", got)
	}

	entries := listAllEntries(t, implementation, root)
	var link domain.Entry
	for _, entry := range entries {
		if entry.Kind == domain.EntrySymlink {
			link = entry
			break
		}
	}
	if link.Symlink == nil || link.Symlink.ResolvedKind == nil {
		t.Fatalf("symlink entry = %#v", link)
	}
	link.Symlink.RawTarget = "caller-mutated"
	*link.Symlink.ResolvedKind = domain.EntryOther
	entries = listAllEntries(t, implementation, root)
	for _, entry := range entries {
		if entry.Kind == domain.EntrySymlink {
			if entry.Symlink == nil || entry.Symlink.RawTarget != "/contract-file" ||
				entry.Symlink.ResolvedKind == nil || *entry.Symlink.ResolvedKind != domain.EntryFile {
				t.Fatalf("fresh symlink entry observed caller mutation: %#v", entry)
			}
			return
		}
	}
	t.Fatal("fresh listing has no symlink")
}

func TestMutableOperationsRejectMissingAndNonDirectoryParents(t *testing.T) {
	implementation, _, err := New(validScenario(t))
	if err != nil {
		t.Fatalf("New(): %v", err)
	}
	mutable := requireMutable(t, implementation)
	locations := []domain.Location{
		{EndpointID: contractEndpointID, Path: "/missing/child"},
		{EndpointID: contractEndpointID, Path: "/contract-file/child"},
	}
	for _, location := range locations {
		if _, err := mutable.Mkdir(context.Background(), providerapi.MkdirRequest{
			Location: location,
		}); !domain.IsCode(err, domain.CodeNotFound) {
			t.Fatalf("Mkdir(%q) error = %v, want not_found", location.Path, err)
		}
		if _, err := mutable.OpenWrite(context.Background(), providerapi.OpenWriteRequest{
			Location:    location,
			Disposition: providerapi.WriteCreateNew,
		}); !domain.IsCode(err, domain.CodeNotFound) {
			t.Fatalf("OpenWrite(%q) error = %v, want not_found", location.Path, err)
		}
	}
	source := domain.Location{EndpointID: contractEndpointID, Path: "/contract-file"}
	for _, destination := range locations {
		if _, err := mutable.Rename(context.Background(), providerapi.RenameRequest{
			Source:      source,
			Destination: destination,
		}); !domain.IsCode(err, domain.CodeNotFound) {
			t.Fatalf("Rename(destination %q) error = %v, want not_found", destination.Path, err)
		}
	}
}

func TestMutableOperationsHonorCancellationAndWriteCloseIsIdempotent(t *testing.T) {
	implementation, _, err := New(validScenario(t))
	if err != nil {
		t.Fatalf("New(): %v", err)
	}
	mutable := requireMutable(t, implementation)
	file := domain.Location{EndpointID: contractEndpointID, Path: "/contract-file"}
	entry := statEntry(t, implementation, file)
	handle, err := mutable.OpenWrite(context.Background(), providerapi.OpenWriteRequest{
		Location:            file,
		Disposition:         providerapi.WriteResumeExisting,
		ExpectedFingerprint: &entry.Fingerprint,
	})
	if err != nil {
		t.Fatalf("OpenWrite(): %v", err)
	}
	canceled, cancel := context.WithCancel(context.Background())
	cancel()
	n, err := handle.Write(canceled, []byte("changed"))
	if n != 0 || !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled Write() = (%d, %v), want (0, context.Canceled)", n, err)
	}
	if got := requireCode(t, err, domain.CodeCanceled).Effect; got != domain.EffectNone {
		t.Fatalf("canceled Write Effect = %q, want %q", got, domain.EffectNone)
	}
	if err := handle.Sync(canceled); !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled Sync() error = %v, want context.Canceled", err)
	}
	if err := handle.Close(canceled); !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled Close() error = %v, want context.Canceled", err)
	}
	if got := readFile(t, implementation, file); !bytes.Equal(got, []byte("contract data")) {
		t.Fatalf("bytes after canceled write = %q", got)
	}
	if err := handle.Close(context.Background()); err != nil {
		t.Fatalf("first Close(): %v", err)
	}
	if err := handle.Close(context.Background()); err != nil {
		t.Fatalf("second Close(): %v", err)
	}

	newLocation := domain.Location{EndpointID: contractEndpointID, Path: "/canceled"}
	_, err = mutable.OpenWrite(canceled, providerapi.OpenWriteRequest{
		Location:    newLocation,
		Disposition: providerapi.WriteCreateNew,
	})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled OpenWrite() error = %v", err)
	}
	_, err = mutable.Mkdir(canceled, providerapi.MkdirRequest{Location: newLocation})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled Mkdir() error = %v", err)
	}
	_, err = mutable.Rename(canceled, providerapi.RenameRequest{
		Source:      file,
		Destination: newLocation,
	})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled Rename() error = %v", err)
	}
	if err := mutable.Remove(canceled, providerapi.RemoveRequest{Location: file}); !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled Remove() error = %v", err)
	}
	if _, err := implementation.Stat(context.Background(), providerapi.StatRequest{Location: newLocation}); !domain.IsCode(err, domain.CodeNotFound) {
		t.Fatalf("canceled mutations created path: %v", err)
	}
}

func TestCancellationAfterWaitingForMutexHasNoEffect(t *testing.T) {
	implementation, controller, err := New(validScenario(t))
	if err != nil {
		t.Fatalf("New(): %v", err)
	}
	mutable := requireMutable(t, implementation)

	t.Run("provider state mutex", func(t *testing.T) {
		location := domain.Location{EndpointID: contractEndpointID, Path: "/canceled-after-wait"}
		ctx, cancel, observed := newObservedCancelContext()
		result := make(chan error, 1)
		implementation.mu.Lock()
		go func() {
			_, err := mutable.Mkdir(ctx, providerapi.MkdirRequest{Location: location})
			result <- err
		}()
		waitObserved(t, observed)
		cancel()
		implementation.mu.Unlock()
		err := <-result
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("Mkdir() error = %v, want context.Canceled", err)
		}
		if got := requireCode(t, err, domain.CodeCanceled).Effect; got != domain.EffectNone {
			t.Fatalf("Mkdir() Effect = %q, want %q", got, domain.EffectNone)
		}
		requireStatCode(t, implementation, location, domain.CodeNotFound)
	})

	t.Run("write handle mutex", func(t *testing.T) {
		file := domain.Location{EndpointID: contractEndpointID, Path: "/contract-file"}
		entry := statEntry(t, implementation, file)
		handle, err := mutable.OpenWrite(context.Background(), providerapi.OpenWriteRequest{
			Location:            file,
			Offset:              int64(len("contract data")),
			Disposition:         providerapi.WriteResumeExisting,
			ExpectedFingerprint: &entry.Fingerprint,
		})
		if err != nil {
			t.Fatalf("OpenWrite(): %v", err)
		}
		concrete := handle.(*writeHandle)
		ctx, cancel, observed := newObservedCancelContext()
		type writeResult struct {
			n   int
			err error
		}
		result := make(chan writeResult, 1)
		concrete.mu.Lock()
		go func() {
			n, err := handle.Write(ctx, []byte("-must-not-land"))
			result <- writeResult{n: n, err: err}
		}()
		waitObserved(t, observed)
		cancel()
		concrete.mu.Unlock()
		got := <-result
		if got.n != 0 || !errors.Is(got.err, context.Canceled) {
			t.Fatalf("Write() = (%d, %v), want (0, context.Canceled)", got.n, got.err)
		}
		if bytes := readFile(t, implementation, file); !reflect.DeepEqual(bytes, []byte("contract data")) {
			t.Fatalf("bytes after canceled waiting Write = %q", bytes)
		}
		if err := handle.Close(context.Background()); err != nil {
			t.Fatalf("Close(): %v", err)
		}
	})

	t.Run("controller state mutex", func(t *testing.T) {
		root := domain.Location{EndpointID: contractEndpointID, Path: "/"}
		request := providerListRequest(root, 1, nil)
		page, err := implementation.List(context.Background(), request)
		if err != nil {
			t.Fatalf("List(): %v", err)
		}
		ctx, cancel, observed := newObservedCancelContext()
		result := make(chan error, 1)
		implementation.mu.Lock()
		go func() {
			result <- controller.InvalidateListing(ctx, root)
		}()
		waitObserved(t, observed)
		cancel()
		implementation.mu.Unlock()
		err = <-result
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("InvalidateListing() error = %v, want context.Canceled", err)
		}
		request.Cursor = page.NextCursor
		if _, err := implementation.List(context.Background(), request); err != nil {
			t.Fatalf("cursor after canceled invalidation: %v", err)
		}
	})
}

func TestInjectedClockNowRunsOutsideProviderAndHandleMutexes(t *testing.T) {
	t.Run("blocking write clock", func(t *testing.T) {
		entered := make(chan struct{})
		release := make(chan struct{})
		clock := &callbackClock{now: func() time.Time {
			close(entered)
			<-release
			return time.Unix(123, 0).UTC()
		}}
		scenario := validScenario(t)
		scenario.Clock = clock
		implementation, _, err := New(scenario)
		if err != nil {
			t.Fatalf("New(): %v", err)
		}
		mutable := requireMutable(t, implementation)
		file := domain.Location{EndpointID: contractEndpointID, Path: "/contract-file"}
		entry := statEntry(t, implementation, file)
		handle, err := mutable.OpenWrite(context.Background(), providerapi.OpenWriteRequest{
			Location:            file,
			Offset:              int64(len("contract data")),
			Disposition:         providerapi.WriteResumeExisting,
			ExpectedFingerprint: &entry.Fingerprint,
		})
		if err != nil {
			t.Fatalf("OpenWrite(): %v", err)
		}
		ctx, cancel := context.WithCancel(context.Background())
		writeDone := make(chan error, 1)
		go func() {
			_, err := handle.Write(ctx, []byte("-must-not-land"))
			writeDone <- err
		}()
		waitObserved(t, entered)
		snapshotDone := make(chan error, 1)
		go func() {
			_, err := implementation.Snapshot(context.Background())
			snapshotDone <- err
		}()
		select {
		case err := <-snapshotDone:
			if err != nil {
				t.Fatalf("Snapshot(): %v", err)
			}
		case <-time.After(time.Second):
			t.Error("Snapshot() blocked while Clock.Now was blocked")
		}
		cancel()
		close(release)
		if err := <-writeDone; !errors.Is(err, context.Canceled) {
			t.Fatalf("Write() error = %v, want context.Canceled", err)
		}
		if got := readFile(t, implementation, file); !bytes.Equal(got, []byte("contract data")) {
			t.Fatalf("bytes after canceled clock wait = %q", got)
		}
	})

	t.Run("reentrant capability clock", func(t *testing.T) {
		var implementation *Provider
		var reentrantBlocked bool
		clock := &callbackClock{now: func() time.Time {
			done := make(chan error, 1)
			go func() {
				_, err := implementation.Snapshot(context.Background())
				done <- err
			}()
			select {
			case err := <-done:
				if err != nil {
					panic(err)
				}
			case <-time.After(100 * time.Millisecond):
				reentrantBlocked = true
			}
			return time.Unix(456, 0).UTC()
		}}
		scenario := validScenario(t)
		scenario.Clock = clock
		var controller *Controller
		var err error
		implementation, controller, err = New(scenario)
		if err != nil {
			t.Fatalf("New(): %v", err)
		}
		if err := controller.ChangeCapabilities(context.Background(), true, nil); err != nil {
			t.Fatalf("ChangeCapabilities(): %v", err)
		}
		if reentrantBlocked {
			t.Fatal("Clock.Now reentrant Snapshot blocked on Provider mutex")
		}
	})
}

func providerListRequest(
	location domain.Location,
	limit uint32,
	sortHint *providerapi.SortHint,
) providerapi.ListRequest {
	return providerapi.ListRequest{Location: location, Limit: limit, Sort: sortHint}
}

func readAll(t *testing.T, handle providerapi.ReadHandle) []byte {
	t.Helper()
	var result []byte
	buffer := make([]byte, 3)
	for {
		n, err := handle.Read(context.Background(), buffer)
		result = append(result, buffer[:n]...)
		if errors.Is(err, io.EOF) {
			return result
		}
		if err != nil {
			t.Fatalf("Read(): %v", err)
		}
		if n == 0 {
			t.Fatal("Read() made no progress without EOF")
		}
	}
}

func requireMutable(t *testing.T, implementation *Provider) providerapi.MutableProvider {
	t.Helper()
	mutable, ok := any(implementation).(providerapi.MutableProvider)
	if !ok {
		t.Fatal("*fake.Provider does not implement provider.MutableProvider")
	}
	return mutable
}

func writeAll(t *testing.T, handle providerapi.WriteHandle, data []byte) {
	t.Helper()
	for len(data) != 0 {
		n, err := handle.Write(context.Background(), data)
		if err != nil {
			t.Fatalf("Write(): %v", err)
		}
		if n <= 0 || n > len(data) {
			t.Fatalf("Write() n = %d for %d bytes", n, len(data))
		}
		data = data[n:]
	}
}

func readFile(t *testing.T, implementation *Provider, location domain.Location) []byte {
	t.Helper()
	handle, err := implementation.OpenRead(
		context.Background(),
		providerapi.OpenReadRequest{Location: location},
	)
	if err != nil {
		t.Fatalf("OpenRead(%q): %v", location.Path, err)
	}
	data := readAll(t, handle)
	if err := handle.Close(context.Background()); err != nil {
		t.Fatalf("Close(%q): %v", location.Path, err)
	}
	return data
}

func createFile(
	t *testing.T,
	mutable providerapi.MutableProvider,
	location domain.Location,
	data []byte,
) {
	t.Helper()
	handle, err := mutable.OpenWrite(context.Background(), providerapi.OpenWriteRequest{
		Location:    location,
		Disposition: providerapi.WriteCreateNew,
	})
	if err != nil {
		t.Fatalf("OpenWrite(create %q): %v", location.Path, err)
	}
	writeAll(t, handle, data)
	if err := handle.Close(context.Background()); err != nil {
		t.Fatalf("Close(create %q): %v", location.Path, err)
	}
}

func statEntry(t *testing.T, implementation *Provider, location domain.Location) domain.Entry {
	t.Helper()
	entry, err := implementation.Stat(
		context.Background(),
		providerapi.StatRequest{Location: location},
	)
	if err != nil {
		t.Fatalf("Stat(%q): %v", location.Path, err)
	}
	return entry
}

func requireStatCode(
	t *testing.T,
	implementation *Provider,
	location domain.Location,
	code domain.Code,
) {
	t.Helper()
	_, err := implementation.Stat(
		context.Background(),
		providerapi.StatRequest{Location: location},
	)
	requireCode(t, err, code)
}

func listAllEntries(
	t *testing.T,
	implementation *Provider,
	location domain.Location,
) []domain.Entry {
	t.Helper()
	request := providerListRequest(location, 4096, nil)
	var entries []domain.Entry
	for pageNumber := 0; pageNumber < 100; pageNumber++ {
		page, err := implementation.List(context.Background(), request)
		if err != nil {
			t.Fatalf("List(%q, page %d): %v", location.Path, pageNumber, err)
		}
		entries = append(entries, page.Entries...)
		if page.Done {
			return entries
		}
		request.Cursor = page.NextCursor
	}
	t.Fatalf("List(%q) did not terminate", location.Path)
	return nil
}

func cloneUint64(value uint64) *uint64 {
	return &value
}

func cloneString(value string) *string {
	return &value
}

type observedContext struct {
	context.Context
	once     sync.Once
	observed chan struct{}
}

func (c *observedContext) Err() error {
	c.once.Do(func() { close(c.observed) })
	return c.Context.Err()
}

func newObservedCancelContext() (*observedContext, context.CancelFunc, <-chan struct{}) {
	ctx, cancel := context.WithCancel(context.Background())
	observed := make(chan struct{})
	return &observedContext{Context: ctx, observed: observed}, cancel, observed
}

func waitObserved(t *testing.T, observed <-chan struct{}) {
	t.Helper()
	select {
	case <-observed:
	case <-time.After(time.Second):
		t.Fatal("operation did not reach the expected wait point")
	}
}

type callbackClock struct {
	now func() time.Time
}

func (c *callbackClock) Now() time.Time {
	return c.now()
}

func (*callbackClock) NewTimer(duration time.Duration) foundation.Timer {
	return foundation.RealClock{}.NewTimer(duration)
}

func requireCode(t *testing.T, err error, code domain.Code) *domain.OpError {
	t.Helper()
	if err == nil {
		t.Fatalf("error = nil, want code %q", code)
	}
	var opError *domain.OpError
	if !errors.As(err, &opError) {
		t.Fatalf("error type = %T, want *domain.OpError", err)
	}
	if opError.Code != code {
		t.Fatalf("error code = %q, want %q", opError.Code, code)
	}
	return opError
}

func validScenario(t *testing.T) Scenario {
	t.Helper()

	capabilities, err := domain.NewCapabilitySnapshot(
		domain.CapabilityRevision{SessionID: contractSessionID, Generation: 1},
		true,
		[]domain.Capability{{Name: "read", Version: 1}},
	)
	if err != nil {
		t.Fatalf("NewCapabilitySnapshot(): %v", err)
	}

	observedAt := time.Date(2026, time.July, 14, 12, 0, 0, 0, time.UTC)
	return Scenario{
		Endpoint: domain.Endpoint{
			ID:          contractEndpointID,
			Kind:        domain.EndpointLocal,
			DisplayName: "Contract fake",
		},
		Snapshot: domain.EndpointSnapshot{
			EndpointID:   contractEndpointID,
			SessionID:    contractSessionID,
			State:        domain.StateReady,
			Capabilities: capabilities,
			ObservedAt:   observedAt,
		},
		Root: Node{
			Name: "/",
			Kind: domain.EntryDirectory,
			Children: []Node{
				{
					Name: "contract-directory",
					Kind: domain.EntryDirectory,
					Children: []Node{{
						Name: "nested-file",
						Kind: domain.EntryFile,
						Data: []byte("nested data"),
					}},
				},
				{
					Name: "contract-file",
					Kind: domain.EntryFile,
					Data: []byte("contract data"),
				},
				{
					Name:       "contract-link",
					Kind:       domain.EntrySymlink,
					LinkTarget: "/contract-file",
				},
				{
					Name: string([]byte{0xff, 'x'}),
					Kind: domain.EntryFile,
					Data: []byte("invalid UTF-8 name"),
				},
			},
		},
		DefaultLimit: 2,
		Clock:        foundation.NewManualClock(observedAt),
	}
}
