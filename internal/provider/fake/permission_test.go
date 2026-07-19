package fake

import (
	"context"
	"errors"
	"reflect"
	"sync"
	"testing"
	"time"

	"github.com/TyrantLucifer/awesome-sftp-cli/internal/domain"
	providerapi "github.com/TyrantLucifer/awesome-sftp-cli/internal/provider"
)

func TestSetPermissionValidationRestorationAndAliasIdentity(t *testing.T) {
	for _, controller := range []*Controller{nil, {}} {
		err := controller.SetPermission("/", false)
		opError := requireCode(t, err, domain.CodeInternal)
		if opError.Operation != "set_permission" || opError.EndpointID != "" ||
			opError.Location != nil || opError.Retry.Kind != domain.RetryNever ||
			opError.Effect != domain.EffectNone {
			t.Fatalf("detached SetPermission error = %#v", opError)
		}
	}

	implementation, controller, err := New(e2Scenario(t))
	if err != nil {
		t.Fatalf("New(): %v", err)
	}
	invalid := domain.CanonicalPath("/workspace/../target-file")
	err = controller.SetPermission(invalid, false)
	opError := requireCode(t, err, domain.CodeInvalidArgument)
	if opError.Operation != "set_permission" || opError.EndpointID != contractEndpointID ||
		opError.Location == nil || opError.Location.Path != invalid ||
		opError.Retry.Kind != domain.RetryNever || opError.Effect != domain.EffectNone {
		t.Fatalf("invalid SetPermission error = %#v", opError)
	}
	opError.Location.Path = "/mutated-by-caller"
	err = controller.SetPermission(invalid, false)
	opError = requireCode(t, err, domain.CodeInvalidArgument)
	if opError.Location == nil || opError.Location.Path != invalid {
		t.Fatalf("SetPermission error Location was not owned: %#v", opError.Location)
	}

	err = controller.SetPermission("/missing", false)
	requireE2Error(t, err, domain.CodeNotFound, "set_permission", e2Location("/missing"))
	err = controller.SetPermission("/loop-a", false)
	requireE1Error(
		t,
		err,
		domain.CodeConflict,
		domain.RetryAfterConflict,
		"set_permission",
		e2Location("/loop-a"),
	)
	err = controller.SetPermission("/escape", false)
	requireE2Error(t, err, domain.CodeInvalidArgument, "set_permission", e2Location("/escape"))
	if calls := controller.Calls(); len(calls) != 0 {
		t.Fatalf("Controller operations recorded Provider calls: %#v", calls)
	}

	if err := controller.SetPermission("/", false); err != nil {
		t.Fatalf("SetPermission(root deny): %v", err)
	}
	_, err = implementation.List(context.Background(), providerapi.ListRequest{
		Location: e2Location("/"), Limit: 1,
	})
	requireE2Permission(t, err, "list", e2Location("/"))
	if err := controller.SetPermission("/", true); err != nil {
		t.Fatalf("SetPermission(root restore): %v", err)
	}
	if _, err := implementation.List(context.Background(), providerapi.ListRequest{
		Location: e2Location("/"), Limit: 1,
	}); err != nil {
		t.Fatalf("List(after root restore): %v", err)
	}
	if _, err := implementation.List(context.Background(), providerapi.ListRequest{
		Location: e2Location("/listing"), Limit: 1,
	}); err != nil {
		t.Fatalf("List(cursor setup): %v", err)
	}
	beforePermissionOnly := captureE2NonPermissionState(implementation)
	if err := controller.SetPermission("/workspace", false); err != nil {
		t.Fatalf("SetPermission(ancestor deny): %v", err)
	}
	if err := controller.SetPermission("/workspace/file", false); err != nil {
		t.Fatalf("SetPermission(cross denied ancestor): %v", err)
	}
	if err := controller.SetPermission("/workspace/file", false); err != nil {
		t.Fatalf("SetPermission(idempotent deny): %v", err)
	}
	if err := controller.SetPermission("/workspace/file", true); err != nil {
		t.Fatalf("SetPermission(child restore across denied ancestor): %v", err)
	}
	if err := controller.SetPermission("/workspace", true); err != nil {
		t.Fatalf("SetPermission(ancestor restore): %v", err)
	}
	afterPermissionOnly := captureE2NonPermissionState(implementation)
	if !reflect.DeepEqual(afterPermissionOnly, beforePermissionOnly) {
		t.Fatalf("SetPermission changed non-permission state:\nbefore=%#v\nafter=%#v",
			beforePermissionOnly, afterPermissionOnly)
	}

	if err := controller.SetPermission("/file-link", false); err != nil {
		t.Fatalf("SetPermission(alias deny): %v", err)
	}
	_, err = implementation.Stat(context.Background(), providerapi.StatRequest{
		Location: e2Location("/target-file"), FollowSymlinks: true,
	})
	requireE2Permission(t, err, "stat", e2Location("/target-file"))
	link, err := implementation.Stat(context.Background(), providerapi.StatRequest{
		Location: e2Location("/file-link"), FollowSymlinks: false,
	})
	if err != nil {
		t.Fatalf("lstat(denied target): %v", err)
	}
	if link.Symlink == nil || link.Symlink.RawTarget != "/target-file" ||
		link.Symlink.ResolvedKind != nil {
		t.Fatalf("lstat(denied target) = %#v, want visible link with scrubbed kind", link)
	}
	if err := controller.SetPermission("/target-file", true); err != nil {
		t.Fatalf("SetPermission(physical restore): %v", err)
	}
	read, err := implementation.OpenRead(context.Background(), providerapi.OpenReadRequest{
		Location: e2Location("/file-link"),
	})
	if err != nil {
		t.Fatalf("OpenRead(alias after physical restore): %v", err)
	}
	if err := read.Close(context.Background()); err != nil {
		t.Fatalf("Close(alias read): %v", err)
	}

	implementation.mu.RLock()
	target, resolveErr := resolveNode(implementation.root, "/target-file", true)
	linkNode, linkErr := resolveNode(implementation.root, "/file-link", false)
	targetDenied := target != nil && target.permissionDenied
	linkDenied := linkNode != nil && linkNode.permissionDenied
	implementation.mu.RUnlock()
	if resolveErr != nil || linkErr != nil || targetDenied || linkDenied {
		t.Fatalf("alias restore state: targetErr=%v linkErr=%v targetDenied=%t linkDenied=%t",
			resolveErr, linkErr, targetDenied, linkDenied)
	}
}

func TestPermissionReadAndNamespaceOperationMatrix(t *testing.T) {
	t.Run("ancestor blocks list stat and open read", func(t *testing.T) {
		implementation, controller := newE2Fixture(t)
		if err := controller.SetPermission("/workspace", false); err != nil {
			t.Fatalf("SetPermission(): %v", err)
		}
		_, err := implementation.List(context.Background(), providerapi.ListRequest{
			Location: e2Location("/workspace"), Limit: 2,
		})
		requireE2Permission(t, err, "list", e2Location("/workspace"))
		for _, follow := range []bool{false, true} {
			_, err = implementation.Stat(context.Background(), providerapi.StatRequest{
				Location: e2Location("/workspace/file"), FollowSymlinks: follow,
			})
			requireE2Permission(t, err, "stat", e2Location("/workspace/file"))
		}
		opened, err := implementation.OpenRead(context.Background(), providerapi.OpenReadRequest{
			Location: e2Location("/workspace/file"),
		})
		if opened != nil {
			t.Fatalf("OpenRead() handle = %#v, want nil", opened)
		}
		requireE2Permission(t, err, "open_read", e2Location("/workspace/file"))
		aliasLocation := e2Location("/workspace-link/file")
		_, err = implementation.Stat(context.Background(), providerapi.StatRequest{
			Location: aliasLocation, FollowSymlinks: true,
		})
		requireE2Permission(t, err, "stat", aliasLocation)
	})

	t.Run("create and mkdir check resolved parent chain", func(t *testing.T) {
		implementation, controller := newE2Fixture(t)
		if err := controller.SetPermission("/workspace", false); err != nil {
			t.Fatalf("SetPermission(): %v", err)
		}
		created := e2Location("/workspace/created")
		handle, err := implementation.OpenWrite(context.Background(), providerapi.OpenWriteRequest{
			Location: created, Disposition: providerapi.WriteCreateNew,
		})
		if handle != nil {
			t.Fatalf("OpenWrite(create) handle = %#v, want nil", handle)
		}
		requireE2Permission(t, err, "open_write", created)
		directory := e2Location("/workspace/created-directory")
		entry, err := implementation.Mkdir(context.Background(), providerapi.MkdirRequest{
			Location: directory, Exclusive: true,
		})
		if !reflect.DeepEqual(entry, domain.Entry{}) {
			t.Fatalf("Mkdir() entry = %#v, want zero", entry)
		}
		requireE2Permission(t, err, "mkdir", directory)
		requireE2PathMissing(t, implementation, created.Path)
		requireE2PathMissing(t, implementation, directory.Path)
	})

	for _, disposition := range []providerapi.WriteDisposition{
		providerapi.WriteResumeExisting,
		providerapi.WriteTruncate,
	} {
		t.Run("existing write "+string(disposition)+" checks parent and target", func(t *testing.T) {
			implementation, controller := newE2Fixture(t)
			location := e2Location("/workspace/file")
			if err := controller.SetPermission(location.Path, false); err != nil {
				t.Fatalf("SetPermission(): %v", err)
			}
			before := e2TreeSnapshot(implementation)
			handle, err := implementation.OpenWrite(context.Background(), providerapi.OpenWriteRequest{
				Location: location, Disposition: disposition,
			})
			if handle != nil {
				t.Fatalf("OpenWrite() handle = %#v, want nil", handle)
			}
			requireE2Permission(t, err, "open_write", location)
			if after := e2TreeSnapshot(implementation); !reflect.DeepEqual(after, before) {
				t.Fatalf("denied OpenWrite changed tree:\nbefore=%#v\nafter=%#v", before, after)
			}
		})
	}

	t.Run("rename checks both parents source and replaced destination", func(t *testing.T) {
		tests := []struct {
			name         string
			deny         domain.CanonicalPath
			wantLocation domain.Location
		}{
			{name: "source parent", deny: "/source-parent", wantLocation: e2Location("/source-parent/source")},
			{name: "source dirent", deny: "/source-parent/source", wantLocation: e2Location("/source-parent/source")},
			{name: "destination parent", deny: "/destination-parent", wantLocation: e2Location("/destination-parent/destination")},
			{name: "replaced destination", deny: "/destination-parent/destination", wantLocation: e2Location("/destination-parent/destination")},
		}
		for _, test := range tests {
			t.Run(test.name, func(t *testing.T) {
				implementation, controller := newE2Fixture(t)
				if err := controller.SetPermission(test.deny, false); err != nil {
					t.Fatalf("SetPermission(%s): %v", test.deny, err)
				}
				before := e2TreeSnapshot(implementation)
				source := e2Location("/source-parent/source")
				result, err := implementation.Rename(context.Background(), providerapi.RenameRequest{
					Source:      source,
					Destination: e2Location("/destination-parent/destination"),
					Replace:     true,
				})
				if result != (providerapi.RenameResult{}) {
					t.Fatalf("Rename() result = %#v, want zero", result)
				}
				requireE2Permission(t, err, "rename", test.wantLocation)
				if after := e2TreeSnapshot(implementation); !reflect.DeepEqual(after, before) {
					t.Fatalf("denied Rename changed tree:\nbefore=%#v\nafter=%#v", before, after)
				}
			})
		}
	})

	t.Run("remove checks parent and lexical target", func(t *testing.T) {
		for _, deny := range []domain.CanonicalPath{"/remove-parent", "/remove-parent/target"} {
			t.Run(string(deny), func(t *testing.T) {
				implementation, controller := newE2Fixture(t)
				if err := controller.SetPermission(deny, false); err != nil {
					t.Fatalf("SetPermission(%s): %v", deny, err)
				}
				before := e2TreeSnapshot(implementation)
				location := e2Location("/remove-parent/target")
				err := implementation.Remove(context.Background(), providerapi.RemoveRequest{Location: location})
				requireE2Permission(t, err, "remove", location)
				if after := e2TreeSnapshot(implementation); !reflect.DeepEqual(after, before) {
					t.Fatalf("denied Remove changed tree:\nbefore=%#v\nafter=%#v", before, after)
				}
			})
		}
	})
}

func TestPermissionRootAndParentOnlyOperationBoundaries(t *testing.T) {
	t.Run("root blocks new traversals but not already opened handles", func(t *testing.T) {
		for _, spec := range e1OperationSpecs() {
			t.Run(string(spec.operation), func(t *testing.T) {
				implementation, controller, err := New(e1ReadWriteScenario(t))
				if err != nil {
					t.Fatalf("New(): %v", err)
				}
				invocation := prepareE1Invocation(t, implementation, spec)
				defer invocation.cleanup()
				if err := controller.SetPermission("/", false); err != nil {
					t.Fatalf("SetPermission(root deny): %v", err)
				}
				err = invocation.invoke(context.Background())
				switch spec.operation {
				case OperationRead, OperationWrite, OperationSyncWrite:
					if err != nil {
						t.Fatalf("opened %s was blocked by later root denial: %v", spec.operation, err)
					}
				default:
					requireE2Permission(t, err, string(spec.operation), spec.location)
				}
			})
		}
	})

	t.Run("create new ignores denied existing final dirent", func(t *testing.T) {
		implementation, controller := newE2Fixture(t)
		location := e2Location("/workspace/file")
		if err := controller.SetPermission(location.Path, false); err != nil {
			t.Fatalf("SetPermission(final deny): %v", err)
		}
		handle, err := implementation.OpenWrite(context.Background(), providerapi.OpenWriteRequest{
			Location: location, Disposition: providerapi.WriteCreateNew,
		})
		if handle != nil {
			t.Fatalf("OpenWrite(create existing) handle = %#v, want nil", handle)
		}
		requireE2Error(t, err, domain.CodeAlreadyExists, "open_write", location)
	})

	t.Run("nonexclusive mkdir ignores denied existing final directory", func(t *testing.T) {
		implementation, controller := newE2Fixture(t)
		location := e2Location("/existing-directory")
		if err := controller.SetPermission(location.Path, false); err != nil {
			t.Fatalf("SetPermission(final deny): %v", err)
		}
		entry, err := implementation.Mkdir(context.Background(), providerapi.MkdirRequest{
			Location: location, Exclusive: false,
		})
		if err != nil {
			t.Fatalf("Mkdir(nonexclusive existing denied): %v", err)
		}
		if entry.Kind != domain.EntryDirectory || entry.Location != location {
			t.Fatalf("Mkdir(nonexclusive existing denied) = %#v", entry)
		}
	})

	t.Run("replace false ignores denied existing destination", func(t *testing.T) {
		implementation, controller := newE2Fixture(t)
		destination := e2Location("/destination-parent/destination")
		if err := controller.SetPermission(destination.Path, false); err != nil {
			t.Fatalf("SetPermission(destination deny): %v", err)
		}
		result, err := implementation.Rename(context.Background(), providerapi.RenameRequest{
			Source: e2Location("/source-parent/source"), Destination: destination, Replace: false,
		})
		if result != (providerapi.RenameResult{}) {
			t.Fatalf("Rename(replace=false) result = %#v", result)
		}
		requireE2Error(t, err, domain.CodeAlreadyExists, "rename", destination)
	})
}

func TestPermissionStructuralErrorsRemainLookupResults(t *testing.T) {
	t.Run("followed stat and open read preserve not-directory", func(t *testing.T) {
		implementation, _ := newE2Fixture(t)
		location := e2Location("/target-file/child")
		_, err := implementation.Stat(context.Background(), providerapi.StatRequest{
			Location: location, FollowSymlinks: true,
		})
		requireE2Error(t, err, domain.CodeNotFound, "stat", location)
		handle, err := implementation.OpenRead(context.Background(), providerapi.OpenReadRequest{
			Location: location,
		})
		if handle != nil {
			t.Fatalf("OpenRead(not-directory) handle = %#v, want nil", handle)
		}
		requireE2Error(t, err, domain.CodeNotFound, "open_read", location)
	})

	t.Run("followed stat and open read preserve symlink escape", func(t *testing.T) {
		implementation, _ := newE2Fixture(t)
		location := e2Location("/escape")
		_, err := implementation.Stat(context.Background(), providerapi.StatRequest{
			Location: location, FollowSymlinks: true,
		})
		requireE2Error(t, err, domain.CodeInvalidArgument, "stat", location)
		handle, err := implementation.OpenRead(context.Background(), providerapi.OpenReadRequest{
			Location: location,
		})
		if handle != nil {
			t.Fatalf("OpenRead(escape) handle = %#v, want nil", handle)
		}
		requireE2Error(t, err, domain.CodeInvalidArgument, "open_read", location)
	})

	t.Run("parent-only operations preserve structural errors", func(t *testing.T) {
		implementation, _ := newE2Fixture(t)
		writeLocation := e2Location("/target-file/child/new")
		handle, err := implementation.OpenWrite(context.Background(), providerapi.OpenWriteRequest{
			Location: writeLocation, Disposition: providerapi.WriteCreateNew,
		})
		if handle != nil {
			t.Fatalf("OpenWrite(not-directory parent) handle = %#v, want nil", handle)
		}
		requireE2Error(t, err, domain.CodeNotFound, "open_write", writeLocation)

		mkdirLocation := e2Location("/escape/child")
		entry, err := implementation.Mkdir(context.Background(), providerapi.MkdirRequest{
			Location: mkdirLocation, Exclusive: true,
		})
		if entry != (domain.Entry{}) {
			t.Fatalf("Mkdir(escape parent) entry = %#v, want zero", entry)
		}
		requireE2Error(t, err, domain.CodeInvalidArgument, "mkdir", mkdirLocation)
	})
}

func TestPermissionFinalSymlinkTargetModes(t *testing.T) {
	implementation, controller := newE2Fixture(t)
	if err := controller.SetPermission("/target-file", false); err != nil {
		t.Fatalf("SetPermission(target deny): %v", err)
	}
	location := e2Location("/file-link")
	entry, err := implementation.Stat(context.Background(), providerapi.StatRequest{
		Location: location, FollowSymlinks: false,
	})
	if err != nil {
		t.Fatalf("lstat(denied target): %v", err)
	}
	if entry.Symlink == nil || entry.Symlink.RawTarget != "/target-file" ||
		entry.Symlink.ResolvedKind != nil {
		t.Fatalf("lstat(denied target) = %#v, want visible scrubbed symlink", entry)
	}
	_, err = implementation.Stat(context.Background(), providerapi.StatRequest{
		Location: location, FollowSymlinks: true,
	})
	requireE2Permission(t, err, "stat", location)
	handle, err := implementation.OpenRead(context.Background(), providerapi.OpenReadRequest{
		Location: location,
	})
	if handle != nil {
		t.Fatalf("OpenRead(denied target) handle = %#v, want nil", handle)
	}
	requireE2Permission(t, err, "open_read", location)
}

func TestPermissionValidHandleDisconnectPrecedesNodeDenial(t *testing.T) {
	scenario := e2Scenario(t)
	scenario.Script = []FaultStep{{
		Match:  FaultMatch{Operation: OperationRead, Nth: 1},
		Effect: FaultEffect{Disconnect: true},
	}}
	implementation, controller, err := New(scenario)
	if err != nil {
		t.Fatalf("New(): %v", err)
	}
	location := e2Location("/workspace/file")
	handleInterface, err := implementation.OpenRead(context.Background(), providerapi.OpenReadRequest{
		Location: location,
	})
	if err != nil {
		t.Fatalf("OpenRead(): %v", err)
	}
	handle := handleInterface.(*readHandle)
	defer handle.Close(context.Background())
	if err := controller.SetPermission(location.Path, false); err != nil {
		t.Fatalf("SetPermission(deny): %v", err)
	}
	buffer := []byte{0xaa}
	n, err := handle.Read(context.Background(), buffer)
	if n != 0 || !reflect.DeepEqual(buffer, []byte{0xaa}) {
		t.Fatalf("Disconnect Read = (%d, %x), want zero and untouched buffer", n, buffer)
	}
	requireE1Error(
		t,
		err,
		domain.CodeTransportInterrupted,
		domain.RetryAfterReconnect,
		"read",
		location,
	)
	snapshot, err := implementation.Snapshot(context.Background())
	if err != nil {
		t.Fatalf("Snapshot(): %v", err)
	}
	if snapshot.State != domain.StateDisconnected {
		t.Fatalf("snapshot state = %q, want disconnected", snapshot.State)
	}
	snapshot.State = domain.StateReady
	snapshot.ObservedAt = snapshot.ObservedAt.Add(time.Minute)
	if err := controller.SetSnapshot(snapshot); err != nil {
		t.Fatalf("SetSnapshot(reconnect): %v", err)
	}
	n, err = handle.Read(context.Background(), buffer)
	if n != 0 {
		t.Fatalf("denied Read after reconnect progress = %d, want zero", n)
	}
	requireE2Permission(t, err, "read", location)
}

func TestPermissionOngoingHandlesObserveDenyAndRestoreByNodeIdentity(t *testing.T) {
	implementation, controller := newE2Fixture(t)
	location := e2Location("/workspace/file")
	readInterface, err := implementation.OpenRead(context.Background(), providerapi.OpenReadRequest{
		Location: location,
	})
	if err != nil {
		t.Fatalf("OpenRead(): %v", err)
	}
	read := readInterface.(*readHandle)
	writeInterface, err := implementation.OpenWrite(context.Background(), providerapi.OpenWriteRequest{
		Location: location, Disposition: providerapi.WriteResumeExisting,
	})
	if err != nil {
		t.Fatalf("OpenWrite(): %v", err)
	}
	write := writeInterface.(*writeHandle)
	defer func() {
		if err := read.Close(context.Background()); err != nil {
			t.Fatalf("Close(read): %v", err)
		}
		if err := write.Close(context.Background()); err != nil {
			t.Fatalf("Close(write): %v", err)
		}
	}()
	if err := controller.SetPermission("/workspace", false); err != nil {
		t.Fatalf("SetPermission(ancestor deny): %v", err)
	}
	if n, err := read.Read(context.Background(), nil); err != nil || n != 0 {
		t.Fatalf("Read(opened handle under denied ancestor) = (%d, %v)", n, err)
	}
	if n, err := write.Write(context.Background(), nil); err != nil || n != 0 {
		t.Fatalf("Write(opened handle under denied ancestor) = (%d, %v)", n, err)
	}
	if err := write.Sync(context.Background()); err != nil {
		t.Fatalf("Sync(opened handle under denied ancestor): %v", err)
	}
	if err := controller.SetPermission("/workspace", true); err != nil {
		t.Fatalf("SetPermission(ancestor restore): %v", err)
	}

	if err := controller.SetPermission(location.Path, false); err != nil {
		t.Fatalf("SetPermission(deny): %v", err)
	}
	beforeTree := e2TreeSnapshot(implementation)
	beforeRead := captureE1HandleState(&e1Invocation{readHandle: read})
	beforeWrite := captureE1HandleState(&e1Invocation{writeHandle: write})
	buffer := []byte{0xaa, 0xbb}
	n, err := read.Read(context.Background(), buffer)
	if n != 0 || !reflect.DeepEqual(buffer, []byte{0xaa, 0xbb}) {
		t.Fatalf("denied Read = (%d, %v, %x), want zero and untouched buffer", n, err, buffer)
	}
	requireE2Permission(t, err, "read", location)
	n, err = write.Write(context.Background(), []byte("XX"))
	if n != 0 {
		t.Fatalf("denied Write progress = %d, want 0", n)
	}
	requireE2Permission(t, err, "write", location)
	err = write.Sync(context.Background())
	requireE2Permission(t, err, "sync_write", location)
	if info := read.Info(); info.Entry.Location != location {
		t.Fatalf("Info() while denied = %#v", info)
	}
	closeInterface, err := implementation.OpenRead(context.Background(), providerapi.OpenReadRequest{
		Location: e2Location("/target-file"),
	})
	if err != nil {
		t.Fatalf("OpenRead(close probe): %v", err)
	}
	if err := controller.SetPermission("/target-file", false); err != nil {
		t.Fatalf("SetPermission(close probe deny): %v", err)
	}
	if err := closeInterface.Close(context.Background()); err != nil {
		t.Fatalf("Close() while denied: %v", err)
	}
	if err := controller.SetPermission("/target-file", true); err != nil {
		t.Fatalf("SetPermission(close probe restore): %v", err)
	}
	if after := e2TreeSnapshot(implementation); !reflect.DeepEqual(after, beforeTree) {
		t.Fatalf("denied handles changed tree:\nbefore=%#v\nafter=%#v", beforeTree, after)
	}
	if after := captureE1HandleState(&e1Invocation{readHandle: read}); after != beforeRead {
		t.Fatalf("denied Read changed handle: before=%#v after=%#v", beforeRead, after)
	}
	if after := captureE1HandleState(&e1Invocation{writeHandle: write}); after != beforeWrite {
		t.Fatalf("denied Write/Sync changed handle: before=%#v after=%#v", beforeWrite, after)
	}

	if err := controller.SetPermission("/workspace-link/file", true); err != nil {
		t.Fatalf("SetPermission(alias restore): %v", err)
	}
	buffer = make([]byte, 2)
	if n, err := read.Read(context.Background(), buffer); err != nil || n != 2 || string(buffer) != "wo" {
		t.Fatalf("Read(after restore) = (%d, %v, %q), want (2, nil, %q)", n, err, buffer, "wo")
	}
	if n, err := write.Write(context.Background(), []byte("OK")); err != nil || n != 2 {
		t.Fatalf("Write(after restore) = (%d, %v), want (2, nil)", n, err)
	}
	if err := write.Sync(context.Background()); err != nil {
		t.Fatalf("Sync(after restore): %v", err)
	}
}

func TestPermissionHandleIdentityAndHigherPrecedenceGuards(t *testing.T) {
	t.Run("rename then deny new path blocks old handles by identity", func(t *testing.T) {
		implementation, controller := newE2Fixture(t)
		source := e2Location("/source-parent/source")
		readInterface, err := implementation.OpenRead(context.Background(), providerapi.OpenReadRequest{Location: source})
		if err != nil {
			t.Fatalf("OpenRead(): %v", err)
		}
		read := readInterface.(*readHandle)
		writeInterface, err := implementation.OpenWrite(context.Background(), providerapi.OpenWriteRequest{
			Location: source, Disposition: providerapi.WriteResumeExisting,
		})
		if err != nil {
			t.Fatalf("OpenWrite(): %v", err)
		}
		write := writeInterface.(*writeHandle)
		defer read.Close(context.Background())
		defer write.Close(context.Background())
		destination := e2Location("/source-parent/moved")
		if _, err := implementation.Rename(context.Background(), providerapi.RenameRequest{
			Source: source, Destination: destination,
		}); err != nil {
			t.Fatalf("Rename(): %v", err)
		}
		if err := controller.SetPermission(destination.Path, false); err != nil {
			t.Fatalf("SetPermission(new path deny): %v", err)
		}
		_, err = read.Read(context.Background(), make([]byte, 1))
		requireE2Permission(t, err, "read", source)
		_, err = write.Write(context.Background(), []byte("x"))
		requireE2Permission(t, err, "write", source)
		err = write.Sync(context.Background())
		requireE2Permission(t, err, "sync_write", source)
	})

	t.Run("closed and old epoch precede permission", func(t *testing.T) {
		implementation, controller := newE2Fixture(t)
		location := e2Location("/workspace/file")
		closedInterface, err := implementation.OpenRead(context.Background(), providerapi.OpenReadRequest{Location: location})
		if err != nil {
			t.Fatalf("OpenRead(closed): %v", err)
		}
		closed := closedInterface.(*readHandle)
		if err := closed.Close(context.Background()); err != nil {
			t.Fatalf("Close(): %v", err)
		}
		oldInterface, err := implementation.OpenRead(context.Background(), providerapi.OpenReadRequest{Location: location})
		if err != nil {
			t.Fatalf("OpenRead(old): %v", err)
		}
		old := oldInterface.(*readHandle)
		defer old.Close(context.Background())
		if err := controller.SetPermission(location.Path, false); err != nil {
			t.Fatalf("SetPermission(deny): %v", err)
		}
		_, err = closed.Read(context.Background(), make([]byte, 1))
		requireE2Error(t, err, domain.CodeInvalidArgument, "read", location)
		next := captureD3State(implementation).snapshot
		next.SessionID = d3SessionB
		next.Capabilities.Revision.SessionID = d3SessionB
		next.Capabilities.Revision.Generation = 1
		next.ObservedAt = next.ObservedAt.Add(time.Minute)
		if err := controller.SetSnapshot(next); err != nil {
			t.Fatalf("SetSnapshot(new session): %v", err)
		}
		_, err = old.Read(context.Background(), make([]byte, 1))
		requireE1Error(t, err, domain.CodeConflict, domain.RetryAfterReplan, "read", location)
	})
}

func TestPermissionFinalSymlinkNamespaceOperationsAreLexical(t *testing.T) {
	implementation, controller := newE2Fixture(t)
	if err := controller.SetPermission("/target-file", false); err != nil {
		t.Fatalf("SetPermission(target deny): %v", err)
	}
	source := e2Location("/rename-link")
	destination := e2Location("/renamed-link")
	result, err := implementation.Rename(context.Background(), providerapi.RenameRequest{
		Source: source, Destination: destination,
	})
	if err != nil || !result.Atomic || result.Replaced {
		t.Fatalf("Rename(lexical symlink) = (%#v, %v)", result, err)
	}
	entry, err := implementation.Stat(context.Background(), providerapi.StatRequest{
		Location: destination, FollowSymlinks: false,
	})
	if err != nil {
		t.Fatalf("lstat(renamed link): %v", err)
	}
	if entry.Kind != domain.EntrySymlink || entry.Symlink == nil ||
		entry.Symlink.ResolvedKind != nil {
		t.Fatalf("renamed denied-target link = %#v", entry)
	}
	remove := e2Location("/remove-link")
	if err := implementation.Remove(context.Background(), providerapi.RemoveRequest{Location: remove}); err != nil {
		t.Fatalf("Remove(lexical symlink): %v", err)
	}
	requireE2PathMissing(t, implementation, remove.Path)
	replacementSource := e2Location("/source-parent/source")
	replacementDestination := e2Location("/destination-link")
	result, err = implementation.Rename(context.Background(), providerapi.RenameRequest{
		Source: replacementSource, Destination: replacementDestination, Replace: true,
	})
	if err != nil || !result.Atomic || !result.Replaced {
		t.Fatalf("Rename(replace denied-target symlink) = (%#v, %v)", result, err)
	}
	if err := controller.SetPermission("/target-file", true); err != nil {
		t.Fatalf("SetPermission(target restore): %v", err)
	}
	if _, err := implementation.Stat(context.Background(), providerapi.StatRequest{
		Location: e2Location("/target-file"), FollowSymlinks: true,
	}); err != nil {
		t.Fatalf("target was removed with symlink: %v", err)
	}
}

func TestPermissionListAndLstatAuthorizationAtReturn(t *testing.T) {
	t.Run("cursor pages scrub current denial without mutating snapshot", func(t *testing.T) {
		implementation, controller := newE2Fixture(t)
		request := providerapi.ListRequest{Location: e2Location("/listing"), Limit: 1}
		first, err := implementation.List(context.Background(), request)
		if err != nil {
			t.Fatalf("List(first): %v", err)
		}
		requireE2ResolvedKind(t, first.Entries, "a-link", domain.EntryFile)
		first.Entries[0].Symlink.RawTarget = "/caller-mutated"
		*first.Entries[0].Symlink.ResolvedKind = domain.EntryOther
		if err := controller.SetPermission("/target-file", false); err != nil {
			t.Fatalf("SetPermission(deny): %v", err)
		}
		request.Cursor = first.NextCursor
		second, err := implementation.List(context.Background(), request)
		if err != nil {
			t.Fatalf("List(second): %v", err)
		}
		requireE2ResolvedKind(t, second.Entries, "b-link", "")
		if second.Entries[0].Symlink.RawTarget != "/target-file" {
			t.Fatalf("snapshot RawTarget = %q, want original", second.Entries[0].Symlink.RawTarget)
		}
		if err := controller.SetPermission("/target-file", true); err != nil {
			t.Fatalf("SetPermission(restore): %v", err)
		}
		restoredSecond, err := implementation.List(context.Background(), request)
		if err != nil {
			t.Fatalf("List(replayed second after restore): %v", err)
		}
		requireE2ResolvedKind(t, restoredSecond.Entries, "b-link", domain.EntryFile)
		request.Cursor = restoredSecond.NextCursor
		third, err := implementation.List(context.Background(), request)
		if err != nil {
			t.Fatalf("List(third): %v", err)
		}
		requireE2ResolvedKind(t, third.Entries, "c-link", domain.EntryFile)

		implementation.mu.RLock()
		for _, binding := range implementation.cursors {
			if binding.snapshot == nil {
				continue
			}
			for _, frozen := range binding.snapshot.entries {
				if frozen.Symlink == nil || frozen.Symlink.ResolvedKind == nil ||
					*frozen.Symlink.ResolvedKind != domain.EntryFile ||
					frozen.Symlink.RawTarget != "/target-file" {
					implementation.mu.RUnlock()
					t.Fatalf("frozen listing snapshot was scrubbed/mutated: %#v", frozen)
				}
			}
		}
		implementation.mu.RUnlock()
	})

	t.Run("requested directory denial wins over bogus cursor and restore resumes", func(t *testing.T) {
		implementation, controller := newE2Fixture(t)
		request := providerapi.ListRequest{Location: e2Location("/listing"), Limit: 1}
		first, err := implementation.List(context.Background(), request)
		if err != nil {
			t.Fatalf("List(first): %v", err)
		}
		if first.NextCursor == "" {
			t.Fatal("List(first) did not issue cursor")
		}
		if err := controller.SetPermission("/listing", false); err != nil {
			t.Fatalf("SetPermission(directory deny): %v", err)
		}
		bogus := request
		bogus.Cursor = "fake:bogus"
		_, err = implementation.List(context.Background(), bogus)
		requireE2Permission(t, err, "list", request.Location)
		if err := controller.SetPermission("/listing", true); err != nil {
			t.Fatalf("SetPermission(directory restore): %v", err)
		}
		request.Cursor = first.NextCursor
		second, err := implementation.List(context.Background(), request)
		if err != nil {
			t.Fatalf("List(original cursor after restore): %v", err)
		}
		requireE2ResolvedKind(t, second.Entries, "b-link", domain.EntryFile)
	})

	t.Run("structural target drift preserves frozen kind", func(t *testing.T) {
		implementation, _ := newE2Fixture(t)
		request := providerapi.ListRequest{Location: e2Location("/drift-listing"), Limit: 1}
		first, err := implementation.List(context.Background(), request)
		if err != nil {
			t.Fatalf("List(first): %v", err)
		}
		if len(first.Entries) != 1 || first.Entries[0].Name != "a-padding" {
			t.Fatalf("first page = %#v", first.Entries)
		}
		if err := implementation.Remove(context.Background(), providerapi.RemoveRequest{
			Location: e2Location("/drift-target"),
		}); err != nil {
			t.Fatalf("Remove(drift target): %v", err)
		}
		if _, err := implementation.Mkdir(context.Background(), providerapi.MkdirRequest{
			Location: e2Location("/drift-target"), Exclusive: true,
		}); err != nil {
			t.Fatalf("Mkdir(replacement drift target): %v", err)
		}
		request.Cursor = first.NextCursor
		second, err := implementation.List(context.Background(), request)
		if err != nil {
			t.Fatalf("List(frozen continuation): %v", err)
		}
		requireE2ResolvedKind(t, second.Entries, "z-link", domain.EntryFile)
	})

	for _, test := range []struct {
		name         string
		deny         domain.CanonicalPath
		wantScrubbed bool
	}{
		{name: "denied frozen target scrubs after current raw target changes", deny: "/switch-target-a", wantScrubbed: true},
		{name: "denied current-only target does not scrub frozen target", deny: "/switch-target-b", wantScrubbed: false},
	} {
		t.Run(test.name, func(t *testing.T) {
			implementation, controller := newE2Fixture(t)
			request := providerapi.ListRequest{Location: e2Location("/switch-listing"), Limit: 1}
			first, err := implementation.List(context.Background(), request)
			if err != nil {
				t.Fatalf("List(first): %v", err)
			}
			implementation.mu.Lock()
			link, resolveErr := resolveNode(implementation.root, "/switch-listing/z-link", false)
			if resolveErr == nil {
				link.linkTarget = "/switch-target-b"
			}
			implementation.mu.Unlock()
			if resolveErr != nil {
				t.Fatalf("resolve switch link: %v", resolveErr)
			}
			if err := controller.SetPermission(test.deny, false); err != nil {
				t.Fatalf("SetPermission(%s): %v", test.deny, err)
			}
			request.Cursor = first.NextCursor
			second, err := implementation.List(context.Background(), request)
			if err != nil {
				t.Fatalf("List(continuation): %v", err)
			}
			want := domain.EntryFile
			if test.wantScrubbed {
				want = ""
			}
			requireE2ResolvedKind(t, second.Entries, "z-link", want)
			if second.Entries[0].Symlink.RawTarget != "/switch-target-a" {
				t.Fatalf("frozen RawTarget = %q, want /switch-target-a", second.Entries[0].Symlink.RawTarget)
			}
		})
	}

	for _, test := range []struct {
		name   string
		mutate func(*testing.T, *Provider, *Controller)
	}{
		{
			name: "missing current target",
			mutate: func(t *testing.T, implementation *Provider, _ *Controller) {
				t.Helper()
				if err := implementation.Remove(context.Background(), providerapi.RemoveRequest{
					Location: e2Location("/drift-target"),
				}); err != nil {
					t.Fatalf("Remove(drift target): %v", err)
				}
			},
		},
		{
			name: "looping current target chain",
			mutate: func(t *testing.T, _ *Provider, controller *Controller) {
				t.Helper()
				if err := controller.ReplaceNode("/chain-a", Node{
					Name: "chain-a", Kind: domain.EntrySymlink, LinkTarget: "/chain-b",
				}); err != nil {
					t.Fatalf("ReplaceNode(chain-a loop): %v", err)
				}
			},
		},
	} {
		t.Run(test.name+" preserves frozen kind", func(t *testing.T) {
			implementation, controller := newE2Fixture(t)
			location := "/drift-listing"
			if test.name == "looping current target chain" {
				location = "/chain-listing"
			}
			request := providerapi.ListRequest{Location: e2Location(domain.CanonicalPath(location)), Limit: 1}
			first, err := implementation.List(context.Background(), request)
			if err != nil {
				t.Fatalf("List(first): %v", err)
			}
			test.mutate(t, implementation, controller)
			request.Cursor = first.NextCursor
			second, err := implementation.List(context.Background(), request)
			if err != nil {
				t.Fatalf("List(continuation): %v", err)
			}
			requireE2ResolvedKind(t, second.Entries, "z-link", domain.EntryFile)
		})
	}

	t.Run("denied ordinary child remains listed", func(t *testing.T) {
		implementation, controller := newE2Fixture(t)
		if err := controller.SetPermission("/workspace/file", false); err != nil {
			t.Fatalf("SetPermission(child deny): %v", err)
		}
		page, err := implementation.List(context.Background(), providerapi.ListRequest{
			Location: e2Location("/workspace"), Limit: 1,
		})
		if err != nil {
			t.Fatalf("List(parent of denied child): %v", err)
		}
		if len(page.Entries) != 1 || page.Entries[0].Name != "file" || page.Entries[0].Kind != domain.EntryFile {
			t.Fatalf("List(parent of denied child) = %#v", page.Entries)
		}
	})

	t.Run("live lstat deny and restore", func(t *testing.T) {
		implementation, controller := newE2Fixture(t)
		if err := controller.SetPermission("/target-file", false); err != nil {
			t.Fatalf("SetPermission(deny): %v", err)
		}
		entry, err := implementation.Stat(context.Background(), providerapi.StatRequest{
			Location: e2Location("/file-link"), FollowSymlinks: false,
		})
		if err != nil {
			t.Fatalf("lstat(denied): %v", err)
		}
		requireE2ResolvedKind(t, []domain.Entry{entry}, "file-link", "")
		if err := controller.SetPermission("/target-file", true); err != nil {
			t.Fatalf("SetPermission(restore): %v", err)
		}
		entry, err = implementation.Stat(context.Background(), providerapi.StatRequest{
			Location: e2Location("/file-link"), FollowSymlinks: false,
		})
		if err != nil {
			t.Fatalf("lstat(restored): %v", err)
		}
		requireE2ResolvedKind(t, []domain.Entry{entry}, "file-link", domain.EntryFile)
	})
}

func TestPermissionStaleLstatUsesFrozenTargetAndCurrentAuthorization(t *testing.T) {
	scenario := e2Scenario(t)
	scenario.Script = []FaultStep{
		staleStatExactStep("/stale-link", 1, 1),
		staleStatExactStep("/stale-link", 2, 1),
		staleStatExactStep("/stale-link", 3, 1),
		staleStatExactStep("/stale-link", 4, 1),
	}
	implementation, controller, err := New(scenario)
	if err != nil {
		t.Fatalf("New(): %v", err)
	}
	if err := implementation.Remove(context.Background(), providerapi.RemoveRequest{
		Location: e2Location("/stale-target"),
	}); err != nil {
		t.Fatalf("Remove(stale target): %v", err)
	}
	if _, err := implementation.Mkdir(context.Background(), providerapi.MkdirRequest{
		Location: e2Location("/stale-target"), Exclusive: true,
	}); err != nil {
		t.Fatalf("Mkdir(stale target replacement): %v", err)
	}
	if err := controller.ReplaceNode("/stale-link", Node{
		Name: "stale-link", Kind: domain.EntrySymlink, LinkTarget: "/stale-target-b",
	}); err != nil {
		t.Fatalf("ReplaceNode(current stale link target B): %v", err)
	}
	request := providerapi.StatRequest{Location: e2Location("/stale-link"), FollowSymlinks: false}
	entry, err := implementation.Stat(context.Background(), request)
	if err != nil {
		t.Fatalf("stale lstat(structural drift): %v", err)
	}
	if entry.Symlink == nil || entry.Symlink.RawTarget != "/stale-target" {
		t.Fatalf("stale lstat symlink = %#v", entry.Symlink)
	}
	requireE2ResolvedKind(t, []domain.Entry{entry}, "stale-link", domain.EntryFile)
	entry.Symlink.RawTarget = "/caller-mutated"
	*entry.Symlink.ResolvedKind = domain.EntryOther

	if err := controller.SetPermission("/stale-target-b", false); err != nil {
		t.Fatalf("SetPermission(current-only target B deny): %v", err)
	}
	entry, err = implementation.Stat(context.Background(), request)
	if err != nil {
		t.Fatalf("stale lstat(current-only B denied): %v", err)
	}
	if entry.Symlink == nil || entry.Symlink.RawTarget != "/stale-target" {
		t.Fatalf("stale lstat owned RawTarget = %#v", entry.Symlink)
	}
	requireE2ResolvedKind(t, []domain.Entry{entry}, "stale-link", domain.EntryFile)
	if err := controller.SetPermission("/stale-target-b", true); err != nil {
		t.Fatalf("SetPermission(current-only target B restore): %v", err)
	}
	if err := controller.SetPermission("/stale-target", false); err != nil {
		t.Fatalf("SetPermission(frozen target A deny): %v", err)
	}
	entry, err = implementation.Stat(context.Background(), request)
	if err != nil {
		t.Fatalf("stale lstat(frozen A denied): %v", err)
	}
	requireE2ResolvedKind(t, []domain.Entry{entry}, "stale-link", "")
	if err := controller.SetPermission("/stale-target", true); err != nil {
		t.Fatalf("SetPermission(frozen target A restore): %v", err)
	}
	entry, err = implementation.Stat(context.Background(), request)
	if err != nil {
		t.Fatalf("stale lstat(restored): %v", err)
	}
	requireE2ResolvedKind(t, []domain.Entry{entry}, "stale-link", domain.EntryFile)
}

func TestPermissionPersistentPrecedenceAndSelectedEffectsAreAtomic(t *testing.T) {
	t.Run("state then capability then permission then lookup", func(t *testing.T) {
		implementation, controller := newE2Fixture(t)
		location := e2Location("/workspace/file")
		if err := controller.SetPermission(location.Path, false); err != nil {
			t.Fatalf("SetPermission(): %v", err)
		}
		setE1State(t, implementation, controller, domain.StateAuthRequired)
		_, err := implementation.Stat(context.Background(), providerapi.StatRequest{Location: location})
		requireE1Error(t, err, domain.CodeAuthRequired, domain.RetryAfterAuth, "stat", location)
		setE1State(t, implementation, controller, domain.StateReady)
		if err := controller.SetCapabilities(e1CapabilitySnapshot(
			contractSessionID, 2, true, []domain.Capability{{Name: "write", Version: 1}},
		)); err != nil {
			t.Fatalf("SetCapabilities(withdraw read): %v", err)
		}
		_, err = implementation.Stat(context.Background(), providerapi.StatRequest{Location: location})
		requireE1Error(t, err, domain.CodeCapabilityLost, domain.RetryAfterReplan, "stat", location)
		if err := controller.SetCapabilities(e1CapabilitySnapshot(
			contractSessionID, 3, false, []domain.Capability{{Name: "read", Version: 2}},
		)); err != nil {
			t.Fatalf("SetCapabilities(restore read): %v", err)
		}
		_, err = implementation.Stat(context.Background(), providerapi.StatRequest{Location: location})
		requireE2Permission(t, err, "stat", location)
		missingBelowDenied := e2Location("/workspace/missing")
		if err := controller.SetPermission("/workspace", false); err != nil {
			t.Fatalf("SetPermission(denied missing ancestor): %v", err)
		}
		_, err = implementation.Stat(context.Background(), providerapi.StatRequest{Location: missingBelowDenied})
		requireE2Permission(t, err, "stat", missingBelowDenied)
		if err := controller.SetPermission("/workspace", true); err != nil {
			t.Fatalf("SetPermission(missing ancestor restore): %v", err)
		}
		_, err = implementation.Stat(context.Background(), providerapi.StatRequest{Location: missingBelowDenied})
		requireE2Error(t, err, domain.CodeNotFound, "stat", missingBelowDenied)
	})

	t.Run("permission precedes fingerprint and other mutation preconditions", func(t *testing.T) {
		bogusVersion := "bogus"
		bogus := domain.Fingerprint{VersionID: &bogusVersion}

		t.Run("truncate fingerprint", func(t *testing.T) {
			implementation, controller := newE2Fixture(t)
			location := e2Location("/workspace/file")
			if err := controller.SetPermission(location.Path, false); err != nil {
				t.Fatalf("SetPermission(): %v", err)
			}
			before := e2TreeSnapshot(implementation)
			handle, err := implementation.OpenWrite(context.Background(), providerapi.OpenWriteRequest{
				Location: location, Disposition: providerapi.WriteTruncate, ExpectedFingerprint: &bogus,
			})
			if handle != nil {
				t.Fatalf("OpenWrite(truncate) handle = %#v", handle)
			}
			requireE2Permission(t, err, "open_write", location)
			if after := e2TreeSnapshot(implementation); !reflect.DeepEqual(after, before) {
				t.Fatalf("denied truncate mutated tree")
			}
		})

		t.Run("rename destination permission before bad source fingerprint", func(t *testing.T) {
			implementation, controller := newE2Fixture(t)
			source := e2Location("/source-parent/source")
			destination := e2Location("/destination-parent/destination")
			if err := controller.SetPermission(destination.Path, false); err != nil {
				t.Fatalf("SetPermission(destination): %v", err)
			}
			result, err := implementation.Rename(context.Background(), providerapi.RenameRequest{
				Source: source, Destination: destination, Replace: true, ExpectedSource: &bogus,
			})
			if result != (providerapi.RenameResult{}) {
				t.Fatalf("Rename() result = %#v", result)
			}
			requireE2Permission(t, err, "rename", destination)
		})

		t.Run("remove permission before fingerprint", func(t *testing.T) {
			implementation, controller := newE2Fixture(t)
			location := e2Location("/remove-parent/target")
			if err := controller.SetPermission(location.Path, false); err != nil {
				t.Fatalf("SetPermission(): %v", err)
			}
			before := e2TreeSnapshot(implementation)
			err := implementation.Remove(context.Background(), providerapi.RemoveRequest{
				Location: location, Expected: &bogus,
			})
			requireE2Permission(t, err, "remove", location)
			if after := e2TreeSnapshot(implementation); !reflect.DeepEqual(after, before) {
				t.Fatalf("denied Remove mutated tree")
			}
		})
	})

	t.Run("standalone injected error precedes permission", func(t *testing.T) {
		scenario := e2Scenario(t)
		scenario.Script = []FaultStep{{
			Match: FaultMatch{Operation: OperationStat, Nth: 1},
			Effect: FaultEffect{Error: &domain.OpError{
				Code: domain.CodeTimeout, Message: "injected before permission",
				Retry: domain.RetryAdvice{Kind: domain.RetryBackoff}, Effect: domain.EffectNone,
			}},
		}}
		implementation, controller, err := New(scenario)
		if err != nil {
			t.Fatalf("New(): %v", err)
		}
		location := e2Location("/workspace/file")
		if err := controller.SetPermission(location.Path, false); err != nil {
			t.Fatalf("SetPermission(): %v", err)
		}
		_, err = implementation.Stat(context.Background(), providerapi.StatRequest{Location: location})
		requireE1Error(t, err, domain.CodeTimeout, domain.RetryBackoff, "stat", location)
	})

	t.Run("disconnect precedes permission and changes connection state", func(t *testing.T) {
		scenario := e2Scenario(t)
		scenario.Script = []FaultStep{{
			Match:  FaultMatch{Operation: OperationStat, Nth: 1},
			Effect: FaultEffect{Disconnect: true},
		}}
		implementation, controller, err := New(scenario)
		if err != nil {
			t.Fatalf("New(): %v", err)
		}
		location := e2Location("/workspace/file")
		if err := controller.SetPermission(location.Path, false); err != nil {
			t.Fatalf("SetPermission(): %v", err)
		}
		_, err = implementation.Stat(context.Background(), providerapi.StatRequest{Location: location})
		requireE1Error(
			t, err, domain.CodeTransportInterrupted, domain.RetryAfterReconnect, "stat", location,
		)
		snapshot, err := implementation.Snapshot(context.Background())
		if err != nil {
			t.Fatalf("Snapshot(): %v", err)
		}
		if snapshot.State != domain.StateDisconnected {
			t.Fatalf("disconnect state = %q, want disconnected", snapshot.State)
		}
	})

	t.Run("stale stat is consumed before permission without leaking stale data", func(t *testing.T) {
		scenario := e2Scenario(t)
		scenario.Script = []FaultStep{staleStatExactStep("/workspace/file", 1, 1)}
		implementation, controller, err := New(scenario)
		if err != nil {
			t.Fatalf("New(): %v", err)
		}
		location := e2Location("/workspace/file")
		write, err := implementation.OpenWrite(context.Background(), providerapi.OpenWriteRequest{
			Location: location, Disposition: providerapi.WriteResumeExisting,
		})
		if err != nil {
			t.Fatalf("OpenWrite(): %v", err)
		}
		if _, err := write.Write(context.Background(), []byte("new")); err != nil {
			t.Fatalf("Write(): %v", err)
		}
		if err := write.Close(context.Background()); err != nil {
			t.Fatalf("Close(): %v", err)
		}
		if err := controller.SetPermission(location.Path, false); err != nil {
			t.Fatalf("SetPermission(deny): %v", err)
		}
		_, err = implementation.Stat(context.Background(), providerapi.StatRequest{Location: location})
		requireE2Permission(t, err, "stat", location)
		if err := controller.SetPermission(location.Path, true); err != nil {
			t.Fatalf("SetPermission(restore): %v", err)
		}
		entry, err := implementation.Stat(context.Background(), providerapi.StatRequest{Location: location})
		if err != nil {
			t.Fatalf("Stat(after consumed stale): %v", err)
		}
		if entry.Metadata.Size == nil || *entry.Metadata.Size != uint64(len("newkspace-data")) {
			t.Fatalf("Stat(after consumed stale) size = %#v", entry.Metadata.Size)
		}
	})

	t.Run("short read and write have zero progress while denied", func(t *testing.T) {
		scenario := e2Scenario(t)
		scenario.Script = []FaultStep{
			{
				Match: FaultMatch{Operation: OperationRead, Nth: 1},
				Effect: FaultEffect{
					MaxReadBytes: 1,
					Error: &domain.OpError{
						Code: domain.CodeTimeout, Message: "combined denied read",
						Retry: domain.RetryAdvice{Kind: domain.RetryBackoff}, Effect: domain.EffectNone,
					},
				},
			},
			{
				Match: FaultMatch{Operation: OperationWrite, Nth: 1},
				Effect: FaultEffect{
					MaxWriteBytes: 1,
					Error: &domain.OpError{
						Code: domain.CodeResourceExhausted, Message: "combined denied write",
						Retry: domain.RetryAdvice{Kind: domain.RetryNever}, Effect: domain.EffectNone,
					},
				},
			},
		}
		implementation, controller, err := New(scenario)
		if err != nil {
			t.Fatalf("New(): %v", err)
		}
		location := e2Location("/workspace/file")
		readInterface, err := implementation.OpenRead(context.Background(), providerapi.OpenReadRequest{Location: location})
		if err != nil {
			t.Fatalf("OpenRead(): %v", err)
		}
		read := readInterface.(*readHandle)
		writeInterface, err := implementation.OpenWrite(context.Background(), providerapi.OpenWriteRequest{
			Location: location, Disposition: providerapi.WriteResumeExisting,
		})
		if err != nil {
			t.Fatalf("OpenWrite(): %v", err)
		}
		write := writeInterface.(*writeHandle)
		defer read.Close(context.Background())
		defer write.Close(context.Background())
		if err := controller.SetPermission(location.Path, false); err != nil {
			t.Fatalf("SetPermission(deny): %v", err)
		}
		before := e2TreeSnapshot(implementation)
		buffer := []byte{0xcc, 0xdd}
		n, err := read.Read(context.Background(), buffer)
		if n != 0 || !reflect.DeepEqual(buffer, []byte{0xcc, 0xdd}) {
			t.Fatalf("denied short Read = (%d, %x)", n, buffer)
		}
		requireE2Permission(t, err, "read", location)
		n, err = write.Write(context.Background(), []byte("YZ"))
		if n != 0 {
			t.Fatalf("denied short Write progress = %d", n)
		}
		requireE2Permission(t, err, "write", location)
		if after := e2TreeSnapshot(implementation); !reflect.DeepEqual(after, before) {
			t.Fatalf("denied short I/O mutated tree:\nbefore=%#v\nafter=%#v", before, after)
		}
		if err := controller.SetPermission(location.Path, true); err != nil {
			t.Fatalf("SetPermission(restore): %v", err)
		}
		buffer = make([]byte, 2)
		if n, err := read.Read(context.Background(), buffer); err != nil || n != 2 || string(buffer) != "wo" {
			t.Fatalf("Read(after consumed short) = (%d, %v, %q)", n, err, buffer)
		}
		if n, err := write.Write(context.Background(), []byte("YZ")); err != nil || n != 2 {
			t.Fatalf("Write(after consumed short) = (%d, %v)", n, err)
		}
	})

	t.Run("non atomic rename has zero mutation while denied and is consumed", func(t *testing.T) {
		scenario := e2Scenario(t)
		scenario.Script = []FaultStep{{
			Match:  FaultMatch{Operation: OperationRename, Nth: 1},
			Effect: FaultEffect{NonAtomicRename: true},
		}}
		implementation, controller, err := New(scenario)
		if err != nil {
			t.Fatalf("New(): %v", err)
		}
		source := e2Location("/source-parent/source")
		destination := e2Location("/source-parent/moved")
		if err := controller.SetPermission(source.Path, false); err != nil {
			t.Fatalf("SetPermission(deny): %v", err)
		}
		before := e2TreeSnapshot(implementation)
		result, err := implementation.Rename(context.Background(), providerapi.RenameRequest{
			Source: source, Destination: destination,
		})
		if result != (providerapi.RenameResult{}) {
			t.Fatalf("denied non-atomic Rename result = %#v", result)
		}
		requireE2Permission(t, err, "rename", source)
		if after := e2TreeSnapshot(implementation); !reflect.DeepEqual(after, before) {
			t.Fatalf("denied non-atomic Rename mutated tree")
		}
		if err := controller.SetPermission(source.Path, true); err != nil {
			t.Fatalf("SetPermission(restore): %v", err)
		}
		result, err = implementation.Rename(context.Background(), providerapi.RenameRequest{
			Source: source, Destination: destination,
		})
		if err != nil || !result.Atomic {
			t.Fatalf("Rename(after consumed non-atomic) = (%#v, %v)", result, err)
		}
	})
}

func TestPermissionReplaceNodePreservesRootAndAllowsFreshDescendants(t *testing.T) {
	implementation, controller := newE2Fixture(t)
	if err := controller.SetPermission("/workspace", false); err != nil {
		t.Fatalf("SetPermission(workspace deny): %v", err)
	}
	if err := controller.SetPermission("/workspace/file", false); err != nil {
		t.Fatalf("SetPermission(old child deny): %v", err)
	}
	err := controller.ReplaceNode("/workspace", Node{
		Name: "workspace", Kind: domain.EntryDirectory,
		Children: []Node{{Name: "file", Kind: domain.EntryFile, Data: []byte("fresh")}},
	})
	if err != nil {
		t.Fatalf("ReplaceNode(): %v", err)
	}
	_, err = implementation.Stat(context.Background(), providerapi.StatRequest{
		Location: e2Location("/workspace/file"), FollowSymlinks: true,
	})
	requireE2Permission(t, err, "stat", e2Location("/workspace/file"))
	if err := controller.SetPermission("/workspace", true); err != nil {
		t.Fatalf("SetPermission(workspace restore): %v", err)
	}
	entry, err := implementation.Stat(context.Background(), providerapi.StatRequest{
		Location: e2Location("/workspace/file"), FollowSymlinks: true,
	})
	if err != nil {
		t.Fatalf("Stat(fresh descendant): %v", err)
	}
	if entry.Metadata.Size == nil || *entry.Metadata.Size != 5 {
		t.Fatalf("fresh descendant = %#v", entry)
	}
}

func TestPermissionGatedOperationObservesExecutionTimeDenial(t *testing.T) {
	scenario := e2Scenario(t)
	scenario.Script = []FaultStep{{
		Match:  FaultMatch{Operation: OperationStat, Nth: 1},
		Effect: FaultEffect{WaitGate: "permission-gate"},
	}}
	implementation, controller, err := New(scenario)
	if err != nil {
		t.Fatalf("New(): %v", err)
	}
	location := e2Location("/workspace/file")
	ctx := newDoneObservedContext(context.Background(), 1)
	result := make(chan error, 1)
	go func() {
		_, err := implementation.Stat(ctx, providerapi.StatRequest{Location: location})
		result <- err
	}()
	<-ctx.observed
	if err := controller.SetPermission(location.Path, false); err != nil {
		t.Fatalf("SetPermission(while Stat gated): %v", err)
	}
	controller.ReleaseGate("permission-gate")
	requireE2Permission(t, <-result, "stat", location)
}

func TestPermissionConcurrentToggleAndOperationsAreRaceSafe(t *testing.T) {
	implementation, controller := newE2Fixture(t)
	location := e2Location("/workspace/file")
	readInterface, err := implementation.OpenRead(context.Background(), providerapi.OpenReadRequest{Location: location})
	if err != nil {
		t.Fatalf("OpenRead(): %v", err)
	}
	read := readInterface.(*readHandle)
	writeInterface, err := implementation.OpenWrite(context.Background(), providerapi.OpenWriteRequest{
		Location: location, Disposition: providerapi.WriteResumeExisting,
	})
	if err != nil {
		t.Fatalf("OpenWrite(): %v", err)
	}
	write := writeInterface.(*writeHandle)
	defer read.Close(context.Background())
	defer write.Close(context.Background())

	start := make(chan struct{})
	errorsChannel := make(chan error, 2048)
	var group sync.WaitGroup
	group.Add(5)
	go func() {
		defer group.Done()
		<-start
		for index := 0; index < 200; index++ {
			if err := controller.SetPermission(location.Path, index%2 == 0); err != nil {
				errorsChannel <- err
				return
			}
		}
	}()
	go func() {
		defer group.Done()
		<-start
		for index := 0; index < 200; index++ {
			_, err := implementation.Stat(context.Background(), providerapi.StatRequest{Location: location})
			e2AcceptPermissionOrNil(errorsChannel, err)
		}
	}()
	go func() {
		defer group.Done()
		<-start
		for index := 0; index < 200; index++ {
			_, err := read.Read(context.Background(), nil)
			e2AcceptPermissionOrNil(errorsChannel, err)
		}
	}()
	go func() {
		defer group.Done()
		<-start
		for index := 0; index < 200; index++ {
			_, err := write.Write(context.Background(), nil)
			e2AcceptPermissionOrNil(errorsChannel, err)
			if err == nil {
				err = write.Sync(context.Background())
				e2AcceptPermissionOrNil(errorsChannel, err)
			}
		}
	}()
	go func() {
		defer group.Done()
		<-start
		request := providerapi.ListRequest{Location: e2Location("/listing"), Limit: 1}
		for index := 0; index < 200; index++ {
			page, err := implementation.List(context.Background(), request)
			if err != nil {
				errorsChannel <- err
				return
			}
			if len(page.Entries) != 1 || page.Entries[0].Symlink == nil {
				errorsChannel <- errors.New("concurrent List returned malformed entry")
				return
			}
		}
	}()
	close(start)
	group.Wait()
	close(errorsChannel)
	for err := range errorsChannel {
		if err != nil {
			t.Fatalf("concurrent permission operation: %v", err)
		}
	}
	if err := controller.SetPermission(location.Path, true); err != nil {
		t.Fatalf("SetPermission(final restore): %v", err)
	}
}

func e2Scenario(t *testing.T) Scenario {
	t.Helper()
	scenario := e1ReadWriteScenario(t)
	scenario.DefaultLimit = 1
	scenario.Root = Node{
		Name: "/", Kind: domain.EntryDirectory,
		Children: []Node{
			{
				Name: "workspace", Kind: domain.EntryDirectory,
				Children: []Node{
					{Name: "file", Kind: domain.EntryFile, Data: []byte("workspace-data")},
					{Name: "sibling", Kind: domain.EntryFile, Data: []byte("sibling")},
				},
			},
			{Name: "workspace-link", Kind: domain.EntrySymlink, LinkTarget: "/workspace"},
			{Name: "existing-directory", Kind: domain.EntryDirectory},
			{Name: "target-file", Kind: domain.EntryFile, Data: []byte("target")},
			{Name: "file-link", Kind: domain.EntrySymlink, LinkTarget: "/target-file"},
			{Name: "rename-link", Kind: domain.EntrySymlink, LinkTarget: "/target-file"},
			{Name: "remove-link", Kind: domain.EntrySymlink, LinkTarget: "/target-file"},
			{Name: "destination-link", Kind: domain.EntrySymlink, LinkTarget: "/target-file"},
			{
				Name: "source-parent", Kind: domain.EntryDirectory,
				Children: []Node{{Name: "source", Kind: domain.EntryFile, Data: []byte("source")}},
			},
			{
				Name: "destination-parent", Kind: domain.EntryDirectory,
				Children: []Node{{Name: "destination", Kind: domain.EntryFile, Data: []byte("destination")}},
			},
			{
				Name: "remove-parent", Kind: domain.EntryDirectory,
				Children: []Node{{Name: "target", Kind: domain.EntryFile, Data: []byte("remove")}},
			},
			{
				Name: "listing", Kind: domain.EntryDirectory,
				Children: []Node{
					{Name: "a-link", Kind: domain.EntrySymlink, LinkTarget: "/target-file"},
					{Name: "b-link", Kind: domain.EntrySymlink, LinkTarget: "/target-file"},
					{Name: "c-link", Kind: domain.EntrySymlink, LinkTarget: "/target-file"},
				},
			},
			{Name: "drift-target", Kind: domain.EntryFile, Data: []byte("drift")},
			{
				Name: "drift-listing", Kind: domain.EntryDirectory,
				Children: []Node{
					{Name: "a-padding", Kind: domain.EntryFile},
					{Name: "z-link", Kind: domain.EntrySymlink, LinkTarget: "/drift-target"},
				},
			},
			{Name: "stale-target", Kind: domain.EntryFile, Data: []byte("stale")},
			{Name: "stale-target-b", Kind: domain.EntryFile, Data: []byte("stale-b")},
			{Name: "stale-link", Kind: domain.EntrySymlink, LinkTarget: "/stale-target"},
			{Name: "switch-target-a", Kind: domain.EntryFile, Data: []byte("A")},
			{Name: "switch-target-b", Kind: domain.EntryFile, Data: []byte("B")},
			{
				Name: "switch-listing", Kind: domain.EntryDirectory,
				Children: []Node{
					{Name: "a-padding", Kind: domain.EntryFile},
					{Name: "z-link", Kind: domain.EntrySymlink, LinkTarget: "/switch-target-a"},
				},
			},
			{Name: "chain-target", Kind: domain.EntryFile, Data: []byte("chain")},
			{Name: "chain-a", Kind: domain.EntrySymlink, LinkTarget: "/chain-target"},
			{Name: "chain-b", Kind: domain.EntrySymlink, LinkTarget: "/chain-a"},
			{
				Name: "chain-listing", Kind: domain.EntryDirectory,
				Children: []Node{
					{Name: "a-padding", Kind: domain.EntryFile},
					{Name: "z-link", Kind: domain.EntrySymlink, LinkTarget: "/chain-a"},
				},
			},
			{Name: "loop-a", Kind: domain.EntrySymlink, LinkTarget: "/loop-b"},
			{Name: "loop-b", Kind: domain.EntrySymlink, LinkTarget: "/loop-a"},
			{Name: "escape", Kind: domain.EntrySymlink, LinkTarget: "../../outside"},
		},
	}
	return scenario
}

func newE2Fixture(t *testing.T) (*Provider, *Controller) {
	t.Helper()
	implementation, controller, err := New(e2Scenario(t))
	if err != nil {
		t.Fatalf("New(): %v", err)
	}
	return implementation, controller
}

func e2Location(path domain.CanonicalPath) domain.Location {
	return domain.Location{EndpointID: contractEndpointID, Path: path}
}

func requireE2Error(
	t *testing.T,
	err error,
	code domain.Code,
	operation string,
	location domain.Location,
) {
	t.Helper()
	opError := requireCode(t, err, code)
	if opError.Operation != operation || opError.EndpointID != contractEndpointID ||
		opError.Location == nil || *opError.Location != location ||
		opError.Retry.Kind != domain.RetryNever || opError.Retry.After != 0 ||
		opError.Effect != domain.EffectNone {
		t.Fatalf("error = %#v, want %s/%s endpoint=%s location=%#v never/none",
			opError, code, operation, contractEndpointID, location)
	}
}

func requireE2Permission(t *testing.T, err error, operation string, location domain.Location) {
	t.Helper()
	requireE2Error(t, err, domain.CodePermissionDenied, operation, location)
}

func requireE2PathMissing(t *testing.T, implementation *Provider, path domain.CanonicalPath) {
	t.Helper()
	implementation.mu.RLock()
	_, err := resolveNode(implementation.root, path, false)
	implementation.mu.RUnlock()
	if !errors.Is(err, errTreeNotFound) {
		t.Fatalf("resolveNode(%s) error = %v, want not found", path, err)
	}
}

func e2TreeSnapshot(implementation *Provider) []replaceTreeState {
	implementation.mu.RLock()
	defer implementation.mu.RUnlock()
	var state []replaceTreeState
	captureReplaceTree(implementation.root, "/", &state)
	return state
}

func requireE2ResolvedKind(
	t *testing.T,
	entries []domain.Entry,
	name string,
	want domain.EntryKind,
) {
	t.Helper()
	if len(entries) != 1 || entries[0].Name != name || entries[0].Symlink == nil {
		t.Fatalf("entries = %#v, want one symlink %q", entries, name)
	}
	if want == "" {
		if entries[0].Symlink.ResolvedKind != nil {
			t.Fatalf("ResolvedKind = %#v, want nil", entries[0].Symlink.ResolvedKind)
		}
		return
	}
	if entries[0].Symlink.ResolvedKind == nil || *entries[0].Symlink.ResolvedKind != want {
		t.Fatalf("ResolvedKind = %#v, want %q", entries[0].Symlink.ResolvedKind, want)
	}
}

func e2AcceptPermissionOrNil(destination chan<- error, err error) {
	if err == nil {
		return
	}
	var opError *domain.OpError
	if !errors.As(err, &opError) || opError.Code != domain.CodePermissionDenied {
		destination <- err
	}
}

type e2NonPermissionState struct {
	tree       []replaceTreeState
	d3         d3State
	nextNodeID uint64
	history    map[nodeRevisionKey]historicalStat
	calls      []Call
}

func captureE2NonPermissionState(implementation *Provider) e2NonPermissionState {
	state := e2NonPermissionState{d3: captureD3State(implementation)}
	implementation.mu.RLock()
	captureReplaceTree(implementation.root, "/", &state.tree)
	for index := range state.tree {
		state.tree[index].permissionDenied = false
	}
	state.nextNodeID = implementation.nextNodeID
	state.history = make(map[nodeRevisionKey]historicalStat, len(implementation.history))
	for key, historical := range implementation.history {
		state.history[key] = historicalStat{
			kind:        historical.kind,
			metadata:    cloneMetadata(historical.metadata),
			fingerprint: cloneFingerprint(historical.fingerprint),
			symlink:     cloneSymlinkInfo(historical.symlink),
		}
	}
	implementation.mu.RUnlock()
	state.calls = implementation.script.callsCopy()
	return state
}
