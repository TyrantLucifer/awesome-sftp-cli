package fake

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"reflect"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/TyrantLucifer/awesome-sftp-cli/internal/domain"
	"github.com/TyrantLucifer/awesome-sftp-cli/internal/foundation"
	providerapi "github.com/TyrantLucifer/awesome-sftp-cli/internal/provider"
)

func TestNewRejectsInvalidFaultScripts(t *testing.T) {
	file := domain.CanonicalPath("/contract-file")
	other := domain.CanonicalPath("/contract-directory")
	revision := uint64(1)
	zeroRevision := uint64(0)
	injected := faultError(domain.CodeTimeout)

	tests := []struct {
		name   string
		script []FaultStep
	}{
		{
			name: "unknown operation",
			script: []FaultStep{{
				Match:  FaultMatch{Operation: Operation("unknown"), Nth: 1},
				Effect: FaultEffect{Error: injected},
			}},
		},
		{
			name: "zero nth",
			script: []FaultStep{{
				Match:  FaultMatch{Operation: OperationStat},
				Effect: FaultEffect{Error: injected},
			}},
		},
		{
			name: "empty effect",
			script: []FaultStep{{
				Match: FaultMatch{Operation: OperationStat, Nth: 1},
			}},
		},
		{
			name: "duplicate matcher",
			script: []FaultStep{
				{
					Match:  FaultMatch{Operation: OperationStat, Nth: 1, Path: &file},
					Effect: FaultEffect{Error: injected},
				},
				{
					Match:  FaultMatch{Operation: OperationStat, Nth: 1, Path: &file},
					Effect: FaultEffect{Error: injected},
				},
			},
		},
		{
			name: "wildcard and exact overlap",
			script: []FaultStep{
				{
					Match:  FaultMatch{Operation: OperationStat, Nth: 1},
					Effect: FaultEffect{Error: injected},
				},
				{
					Match:  FaultMatch{Operation: OperationStat, Nth: 2, Path: &other},
					Effect: FaultEffect{Error: injected},
				},
			},
		},
		{
			name: "noncanonical exact path",
			script: []FaultStep{{
				Match: FaultMatch{
					Operation: OperationStat,
					Nth:       1,
					Path:      canonicalPathPointer("/contract-directory/../contract-file"),
				},
				Effect: FaultEffect{Error: injected},
			}},
		},
		{
			name: "snapshot cannot use exact path",
			script: []FaultStep{{
				Match:  FaultMatch{Operation: OperationSnapshot, Nth: 1, Path: &file},
				Effect: FaultEffect{Error: injected},
			}},
		},
		{
			name: "negative delay",
			script: []FaultStep{{
				Match:  FaultMatch{Operation: OperationStat, Nth: 1},
				Effect: FaultEffect{Delay: -1},
			}},
		},
		{
			name: "negative short read",
			script: []FaultStep{{
				Match:  FaultMatch{Operation: OperationRead, Nth: 1},
				Effect: FaultEffect{MaxReadBytes: -1},
			}},
		},
		{
			name: "short read on stat",
			script: []FaultStep{{
				Match:  FaultMatch{Operation: OperationStat, Nth: 1},
				Effect: FaultEffect{MaxReadBytes: 1},
			}},
		},
		{
			name: "short write on read",
			script: []FaultStep{{
				Match:  FaultMatch{Operation: OperationRead, Nth: 1},
				Effect: FaultEffect{MaxWriteBytes: 1},
			}},
		},
		{
			name: "stale revision on list",
			script: []FaultStep{{
				Match:  FaultMatch{Operation: OperationList, Nth: 1},
				Effect: FaultEffect{StaleNodeRevision: &revision},
			}},
		},
		{
			name: "zero stale revision",
			script: []FaultStep{{
				Match:  FaultMatch{Operation: OperationStat, Nth: 1},
				Effect: FaultEffect{StaleNodeRevision: &zeroRevision},
			}},
		},
		{
			name: "error and stale are two primary effects",
			script: []FaultStep{{
				Match: FaultMatch{Operation: OperationStat, Nth: 1},
				Effect: FaultEffect{
					Error:             injected,
					StaleNodeRevision: &revision,
				},
			}},
		},
		{
			name: "disconnect and error cannot combine",
			script: []FaultStep{{
				Match: FaultMatch{Operation: OperationStat, Nth: 1},
				Effect: FaultEffect{
					Disconnect: true,
					Error:      injected,
				},
			}},
		},
		{
			name: "error may not decorate wrong short operation",
			script: []FaultStep{{
				Match: FaultMatch{Operation: OperationRead, Nth: 1},
				Effect: FaultEffect{
					Error:         injected,
					MaxWriteBytes: 1,
				},
			}},
		},
		{
			name: "non-atomic rename on stat",
			script: []FaultStep{{
				Match:  FaultMatch{Operation: OperationStat, Nth: 1},
				Effect: FaultEffect{NonAtomicRename: true},
			}},
		},
		{
			name: "non-atomic rename and error cannot combine",
			script: []FaultStep{{
				Match: FaultMatch{Operation: OperationRename, Nth: 1},
				Effect: FaultEffect{
					Error:           injected,
					NonAtomicRename: true,
				},
			}},
		},
		{
			name: "disconnect on snapshot",
			script: []FaultStep{{
				Match:  FaultMatch{Operation: OperationSnapshot, Nth: 1},
				Effect: FaultEffect{Disconnect: true},
			}},
		},
		{
			name: "unknown injected code",
			script: []FaultStep{{
				Match: FaultMatch{Operation: OperationStat, Nth: 1},
				Effect: FaultEffect{Error: &domain.OpError{
					Code:   domain.Code("unknown"),
					Retry:  domain.RetryAdvice{Kind: domain.RetryNever},
					Effect: domain.EffectNone,
				}},
			}},
		},
		{
			name: "negative injected retry after",
			script: []FaultStep{{
				Match: FaultMatch{Operation: OperationStat, Nth: 1},
				Effect: FaultEffect{Error: &domain.OpError{
					Code: domain.CodeTimeout,
					Retry: domain.RetryAdvice{
						Kind:  domain.RetryBackoff,
						After: -1,
					},
					Effect: domain.EffectNone,
				}},
			}},
		},
		{
			name: "standalone injected error cannot claim applied effect",
			script: []FaultStep{{
				Match: FaultMatch{Operation: OperationStat, Nth: 1},
				Effect: FaultEffect{Error: &domain.OpError{
					Code:   domain.CodeTimeout,
					Retry:  domain.RetryAdvice{Kind: domain.RetryNever},
					Effect: domain.EffectApplied,
				}},
			}},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			scenario := validScenario(t)
			scenario.Script = test.script
			if _, _, err := New(scenario); err == nil {
				t.Fatal("New() error = nil, want invalid fault script")
			}
		})
	}
}

func TestNewAcceptsValidGroupAFaultShapes(t *testing.T) {
	revision := uint64(1)
	tests := []FaultStep{
		{
			Match:  FaultMatch{Operation: OperationRead, Nth: 1},
			Effect: FaultEffect{MaxReadBytes: 1, Error: faultError(domain.CodeTimeout)},
		},
		{
			Match:  FaultMatch{Operation: OperationWrite, Nth: 1},
			Effect: FaultEffect{MaxWriteBytes: 1, Error: faultError(domain.CodeResourceExhausted)},
		},
		{
			Match:  FaultMatch{Operation: OperationStat, Nth: 1},
			Effect: FaultEffect{StaleNodeRevision: &revision},
		},
		{
			Match:  FaultMatch{Operation: OperationRename, Nth: 1},
			Effect: FaultEffect{NonAtomicRename: true},
		},
		{
			Match:  FaultMatch{Operation: OperationList, Nth: 1},
			Effect: FaultEffect{Disconnect: true},
		},
	}
	for _, step := range tests {
		scenario := validScenario(t)
		scenario.Script = []FaultStep{step}
		if _, _, err := New(scenario); err != nil {
			t.Fatalf("New(%#v): %v", step, err)
		}
	}
}

func TestDisconnectChangesSessionSnapshot(t *testing.T) {
	scenario := validScenario(t)
	scenario.Script = []FaultStep{{
		Match:  FaultMatch{Operation: OperationStat, Nth: 1},
		Effect: FaultEffect{Disconnect: true},
	}}
	implementation, controller, err := New(scenario)
	if err != nil {
		t.Fatalf("New(): %v", err)
	}
	location := domain.Location{EndpointID: contractEndpointID, Path: "/contract-file"}
	root := domain.Location{EndpointID: contractEndpointID, Path: "/"}
	beforeSnapshot, err := implementation.Snapshot(context.Background())
	if err != nil {
		t.Fatalf("Snapshot(before): %v", err)
	}
	beforePage, err := implementation.List(context.Background(), providerapi.ListRequest{
		Location: root,
		Limit:    1,
	})
	if err != nil {
		t.Fatalf("List(before): %v", err)
	}
	if beforePage.Done || beforePage.NextCursor == "" {
		t.Fatalf("List(before) = %#v, want continuation cursor", beforePage)
	}
	controller.Advance(time.Hour)
	disconnectedAt := beforeSnapshot.ObservedAt.Add(time.Hour)

	_, err = implementation.Stat(context.Background(), providerapi.StatRequest{
		Location: location,
	})
	opError := requireCode(t, err, domain.CodeTransportInterrupted)
	if opError.Operation != "stat" || opError.EndpointID != contractEndpointID ||
		opError.Location == nil || *opError.Location != location ||
		opError.Retry.Kind != domain.RetryAfterReconnect || opError.Effect != domain.EffectNone {
		t.Fatalf("selected disconnect error = %#v", opError)
	}

	afterSnapshot, err := implementation.Snapshot(context.Background())
	if err != nil {
		t.Fatalf("Snapshot(after): %v", err)
	}
	if afterSnapshot.State != domain.StateDisconnected ||
		afterSnapshot.SessionID != beforeSnapshot.SessionID ||
		!reflect.DeepEqual(afterSnapshot.Capabilities, beforeSnapshot.Capabilities) ||
		!afterSnapshot.ObservedAt.Equal(disconnectedAt) {
		t.Fatalf("snapshot after disconnect = %#v, before=%#v want observedAt=%v", afterSnapshot, beforeSnapshot, disconnectedAt)
	}
	reconnected := cloneSnapshot(afterSnapshot)
	reconnected.State = domain.StateReady
	reconnected.ObservedAt = reconnected.ObservedAt.Add(time.Minute)
	if err := controller.SetSnapshot(reconnected); err != nil {
		t.Fatalf("SetSnapshot(reconnect): %v", err)
	}
	_, err = implementation.List(context.Background(), providerapi.ListRequest{
		Location: root,
		Limit:    1,
		Cursor:   beforePage.NextCursor,
	})
	if opError := requireCode(t, err, domain.CodeInvalidArgument); opError.Operation != "list" {
		t.Fatalf("cleared cursor error = %#v", opError)
	}
	if _, err := implementation.Stat(context.Background(), providerapi.StatRequest{
		Location: location,
	}); err != nil {
		t.Fatalf("second Stat() after reconnect and consumed disconnect: %v", err)
	}
	statCalls := 0
	for _, call := range controller.Calls() {
		if call.Operation == OperationStat {
			statCalls++
		}
	}
	if statCalls != 2 {
		t.Fatalf("recorded Stat calls = %d, want 2", statCalls)
	}
}

func TestStaleStatUsesRequestedRevision(t *testing.T) {
	scenario := validScenario(t)
	scenario.Script = []FaultStep{
		staleStatStep(3, 1),
		staleStatStep(4, 2),
	}
	implementation, _, err := New(scenario)
	if err != nil {
		t.Fatalf("New(): %v", err)
	}
	location := domain.Location{EndpointID: contractEndpointID, Path: "/contract-file"}

	initialLive := statEntry(t, implementation, location)

	handle, err := implementation.OpenWrite(context.Background(), providerapi.OpenWriteRequest{
		Location:    location,
		Offset:      int64(len("contract data")),
		Disposition: providerapi.WriteResumeExisting,
	})
	if err != nil {
		t.Fatalf("OpenWrite(): %v", err)
	}
	if n, err := handle.Write(context.Background(), []byte("!")); n != 1 || err != nil {
		t.Fatalf("Write() = (%d, %v), want (1, nil)", n, err)
	}
	currentLive := statEntry(t, implementation, location)

	old := statEntry(t, implementation, location)
	current := statEntry(t, implementation, location)
	if !reflect.DeepEqual(old, initialLive) {
		t.Fatalf("revision 1 changed after write: initial=%#v old=%#v", initialLive, old)
	}
	if !reflect.DeepEqual(current, currentLive) {
		t.Fatalf("requested current revision != captured live entry: current=%#v live=%#v", current, currentLive)
	}
}

func TestInitialHistorySeedsEveryNodeRecursively(t *testing.T) {
	scenario := validScenario(t)
	// Put the symlink before its target to ensure initial history is seeded only
	// after the complete tree can be resolved.
	scenario.Root.Children[1], scenario.Root.Children[2] =
		scenario.Root.Children[2], scenario.Root.Children[1]
	scenario.Script = []FaultStep{
		staleStatStep(6, 1),
		staleStatStep(7, 1),
		staleStatStep(8, 1),
		staleStatStep(9, 1),
		staleStatStep(10, 1),
	}
	implementation, _, err := New(scenario)
	if err != nil {
		t.Fatalf("New(): %v", err)
	}
	locations := []domain.Location{
		{EndpointID: contractEndpointID, Path: "/"},
		{EndpointID: contractEndpointID, Path: "/contract-directory"},
		{EndpointID: contractEndpointID, Path: "/contract-directory/nested-file"},
		{EndpointID: contractEndpointID, Path: "/contract-file"},
		{EndpointID: contractEndpointID, Path: "/contract-link"},
	}
	initial := make([]domain.Entry, 0, len(locations))
	for _, location := range locations {
		initial = append(initial, statEntry(t, implementation, location))
	}

	nested := locations[2]
	handle, err := implementation.OpenWrite(context.Background(), providerapi.OpenWriteRequest{
		Location:    nested,
		Offset:      int64(len("nested data")),
		Disposition: providerapi.WriteResumeExisting,
	})
	if err != nil {
		t.Fatalf("OpenWrite(nested): %v", err)
	}
	if n, err := handle.Write(context.Background(), []byte("!")); n != 1 || err != nil {
		t.Fatalf("Write(nested) = (%d, %v), want (1, nil)", n, err)
	}
	if _, err := implementation.OpenWrite(context.Background(), providerapi.OpenWriteRequest{
		Location: domain.Location{
			EndpointID: contractEndpointID,
			Path:       "/initial-history-root-bump",
		},
		Disposition: providerapi.WriteCreateNew,
	}); err != nil {
		t.Fatalf("OpenWrite(root child): %v", err)
	}
	target := locations[3]
	targetHandle, err := implementation.OpenWrite(context.Background(), providerapi.OpenWriteRequest{
		Location:    target,
		Offset:      int64(len("contract data")),
		Disposition: providerapi.WriteResumeExisting,
	})
	if err != nil {
		t.Fatalf("OpenWrite(top-level file): %v", err)
	}
	if n, err := targetHandle.Write(context.Background(), []byte("!")); n != 1 || err != nil {
		t.Fatalf("Write(top-level file) = (%d, %v), want (1, nil)", n, err)
	}

	for index, location := range locations {
		historical := statEntry(t, implementation, location)
		if !reflect.DeepEqual(historical, initial[index]) {
			t.Fatalf("revision 1 for %q changed: got=%#v want=%#v", location.Path, historical, initial[index])
		}
	}
	if initial[4].Symlink == nil || initial[4].Symlink.ResolvedKind == nil ||
		*initial[4].Symlink.ResolvedKind != domain.EntryFile {
		t.Fatalf("initial symlink history = %#v, want resolved file", initial[4].Symlink)
	}
}

func TestStaleHistoryTracksWritesAndReturnsDeepCopies(t *testing.T) {
	scenario := validScenario(t)
	scenario.Root.Children = append(scenario.Root.Children, Node{
		Name: "history-write",
		Kind: domain.EntryFile,
	})
	scenario.Script = []FaultStep{
		staleStatStep(3, 2),
		staleStatStep(4, 3),
		staleStatStep(5, 2),
	}
	implementation, _, err := New(scenario)
	if err != nil {
		t.Fatalf("New(): %v", err)
	}
	location := domain.Location{EndpointID: contractEndpointID, Path: "/history-write"}
	handle, err := implementation.OpenWrite(context.Background(), providerapi.OpenWriteRequest{
		Location:    location,
		Disposition: providerapi.WriteResumeExisting,
	})
	if err != nil {
		t.Fatalf("OpenWrite(): %v", err)
	}
	if n, err := handle.Write(context.Background(), []byte("a")); n != 1 || err != nil {
		t.Fatalf("first Write() = (%d, %v), want (1, nil)", n, err)
	}
	revisionTwoLive := statEntry(t, implementation, location)
	if n, err := handle.Write(context.Background(), []byte("bc")); n != 2 || err != nil {
		t.Fatalf("second Write() = (%d, %v), want (2, nil)", n, err)
	}
	revisionThreeLive := statEntry(t, implementation, location)

	revisionTwo := statEntry(t, implementation, location)
	if !reflect.DeepEqual(revisionTwo, revisionTwoLive) {
		t.Fatalf("revision 2 = %#v, want captured %#v", revisionTwo, revisionTwoLive)
	}
	wantVersion := *revisionTwo.Fingerprint.VersionID
	*revisionTwo.Metadata.Size = 999
	*revisionTwo.Fingerprint.VersionID = "caller-mutated"
	revisionTwo.Location.Path = "/caller-mutated"
	revisionTwo.Name = "caller-mutated"
	if revisionTwo.Metadata.ModifiedAt != nil {
		*revisionTwo.Metadata.ModifiedAt = time.Time{}
	}

	revisionThree := statEntry(t, implementation, location)
	if !reflect.DeepEqual(revisionThree, revisionThreeLive) {
		t.Fatalf("revision 3 = %#v, want captured %#v", revisionThree, revisionThreeLive)
	}
	revisionTwoAgain := statEntry(t, implementation, location)
	if !reflect.DeepEqual(revisionTwoAgain, revisionTwoLive) {
		t.Fatalf("second revision 2 = %#v, want captured %#v", revisionTwoAgain, revisionTwoLive)
	}
	if revisionTwoAgain.Location != location || revisionTwoAgain.Name != "history-write" ||
		revisionTwoAgain.Fingerprint.VersionID == nil ||
		*revisionTwoAgain.Fingerprint.VersionID != wantVersion {
		t.Fatalf("history leaked caller mutation: %#v", revisionTwoAgain)
	}
}

func TestStaleHistoryTracksTruncate(t *testing.T) {
	scenario := validScenario(t)
	scenario.Script = []FaultStep{
		staleStatStep(5, 1),
		staleStatStep(6, 2),
		staleStatStep(7, 1),
		staleStatStep(8, 2),
	}
	implementation, _, err := New(scenario)
	if err != nil {
		t.Fatalf("New(): %v", err)
	}
	location := domain.Location{EndpointID: contractEndpointID, Path: "/contract-file"}
	root := domain.Location{EndpointID: contractEndpointID, Path: "/"}
	before := statEntry(t, implementation, location)
	beforeRoot := statEntry(t, implementation, root)
	if _, err := implementation.OpenWrite(context.Background(), providerapi.OpenWriteRequest{
		Location:    location,
		Disposition: providerapi.WriteTruncate,
	}); err != nil {
		t.Fatalf("OpenWrite(truncate): %v", err)
	}
	after := statEntry(t, implementation, location)
	afterRoot := statEntry(t, implementation, root)

	old := statEntry(t, implementation, location)
	current := statEntry(t, implementation, location)
	if !reflect.DeepEqual(old, before) {
		t.Fatalf("pre-truncate history = %#v, want %#v", old, before)
	}
	if !reflect.DeepEqual(current, after) {
		t.Fatalf("post-truncate history = %#v, want %#v", current, after)
	}
	if current.Metadata.ModifiedAt == nil || current.Metadata.ModifiedPrecision == nil {
		t.Fatalf("truncate revision metadata = %#v, want modification time", current.Metadata)
	}
	oldRoot := statEntry(t, implementation, root)
	currentRoot := statEntry(t, implementation, root)
	if !reflect.DeepEqual(oldRoot, beforeRoot) {
		t.Fatalf("pre-truncate parent history = %#v, want %#v", oldRoot, beforeRoot)
	}
	if !reflect.DeepEqual(currentRoot, afterRoot) {
		t.Fatalf("post-truncate parent history = %#v, want %#v", currentRoot, afterRoot)
	}
}

func TestCreatedFileAndDirectoryHistoryStartAtRevisionOne(t *testing.T) {
	t.Run("create file", func(t *testing.T) {
		scenario := validScenario(t)
		scenario.Script = []FaultStep{staleStatStep(2, 1)}
		implementation, _, err := New(scenario)
		if err != nil {
			t.Fatalf("New(): %v", err)
		}
		location := domain.Location{EndpointID: contractEndpointID, Path: "/created-history-file"}
		if _, err := implementation.OpenWrite(context.Background(), providerapi.OpenWriteRequest{
			Location:    location,
			Disposition: providerapi.WriteCreateNew,
		}); err != nil {
			t.Fatalf("OpenWrite(create): %v", err)
		}
		live := statEntry(t, implementation, location)
		stale := statEntry(t, implementation, location)
		if !reflect.DeepEqual(stale, live) {
			t.Fatalf("created file revision 1 != live: stale=%#v live=%#v", stale, live)
		}
	})

	t.Run("mkdir", func(t *testing.T) {
		scenario := validScenario(t)
		scenario.Script = []FaultStep{staleStatStep(2, 1)}
		implementation, _, err := New(scenario)
		if err != nil {
			t.Fatalf("New(): %v", err)
		}
		location := domain.Location{EndpointID: contractEndpointID, Path: "/created-history-directory"}
		created, err := implementation.Mkdir(context.Background(), providerapi.MkdirRequest{
			Location:  location,
			Exclusive: true,
		})
		if err != nil {
			t.Fatalf("Mkdir(): %v", err)
		}
		live := statEntry(t, implementation, location)
		stale := statEntry(t, implementation, location)
		if !reflect.DeepEqual(stale, created) {
			t.Fatalf("created directory revision 1 != Mkdir result: stale=%#v created=%#v", stale, created)
		}
		if !reflect.DeepEqual(stale, live) {
			t.Fatalf("created directory revision 1 != live: stale=%#v live=%#v", stale, live)
		}
	})
}

func TestParentDirectoryHistoryTracksEveryNamespaceAndWriteBump(t *testing.T) {
	scenario := validScenario(t)
	scenario.Script = []FaultStep{
		staleStatStep(6, 1),
		staleStatStep(7, 2),
		staleStatStep(8, 3),
		staleStatStep(9, 4),
		staleStatStep(10, 5),
	}
	implementation, _, err := New(scenario)
	if err != nil {
		t.Fatalf("New(): %v", err)
	}
	mutable := requireMutable(t, implementation)
	root := domain.Location{EndpointID: contractEndpointID, Path: "/"}
	created := domain.Location{EndpointID: contractEndpointID, Path: "/parent-history"}
	renamed := domain.Location{EndpointID: contractEndpointID, Path: "/parent-history-renamed"}
	want := []domain.Entry{statEntry(t, implementation, root)}
	handle, err := implementation.OpenWrite(context.Background(), providerapi.OpenWriteRequest{
		Location:    created,
		Disposition: providerapi.WriteCreateNew,
	})
	if err != nil {
		t.Fatalf("OpenWrite(create): %v", err)
	}
	want = append(want, statEntry(t, implementation, root))
	if n, err := handle.Write(context.Background(), []byte("x")); n != 1 || err != nil {
		t.Fatalf("Write() = (%d, %v), want (1, nil)", n, err)
	}
	want = append(want, statEntry(t, implementation, root))
	if _, err := mutable.Rename(context.Background(), providerapi.RenameRequest{
		Source:      created,
		Destination: renamed,
	}); err != nil {
		t.Fatalf("Rename(): %v", err)
	}
	want = append(want, statEntry(t, implementation, root))
	if err := mutable.Remove(context.Background(), providerapi.RemoveRequest{Location: renamed}); err != nil {
		t.Fatalf("Remove(): %v", err)
	}
	want = append(want, statEntry(t, implementation, root))

	for index, expected := range want {
		historical := statEntry(t, implementation, root)
		if !reflect.DeepEqual(historical, expected) {
			t.Fatalf("root revision %d = %#v, want captured %#v", index+1, historical, expected)
		}
	}
}

func TestCrossDirectoryRenameRecordsBothParentsOnce(t *testing.T) {
	scenario := validScenario(t)
	scenario.Root.Children = append(scenario.Root.Children, Node{
		Name: "unrelated-directory",
		Kind: domain.EntryDirectory,
	})
	scenario.Script = []FaultStep{
		staleStatStep(9, 1),
		staleStatStep(10, 2),
		staleStatStep(11, 1),
		staleStatStep(12, 2),
		staleStatStep(13, 1),
		staleStatStep(14, 2),
		staleStatStep(15, 1),
		staleStatStep(16, 2),
	}
	implementation, _, err := New(scenario)
	if err != nil {
		t.Fatalf("New(): %v", err)
	}
	mutable := requireMutable(t, implementation)
	sourceParent := domain.Location{EndpointID: contractEndpointID, Path: "/"}
	destinationParent := domain.Location{EndpointID: contractEndpointID, Path: "/contract-directory"}
	unrelated := domain.Location{EndpointID: contractEndpointID, Path: "/unrelated-directory"}
	source := domain.Location{EndpointID: contractEndpointID, Path: "/contract-file"}
	destination := domain.Location{
		EndpointID: contractEndpointID,
		Path:       "/contract-directory/moved-contract-file",
	}

	beforeSourceParent := statEntry(t, implementation, sourceParent)
	beforeDestinationParent := statEntry(t, implementation, destinationParent)
	beforeUnrelated := statEntry(t, implementation, unrelated)
	beforeNode := statEntry(t, implementation, source)
	if _, err := mutable.Rename(context.Background(), providerapi.RenameRequest{
		Source:      source,
		Destination: destination,
	}); err != nil {
		t.Fatalf("Rename(): %v", err)
	}
	afterSourceParent := statEntry(t, implementation, sourceParent)
	afterDestinationParent := statEntry(t, implementation, destinationParent)
	afterUnrelated := statEntry(t, implementation, unrelated)
	afterNode := statEntry(t, implementation, destination)

	checks := []struct {
		location domain.Location
		want     domain.Entry
	}{
		{location: sourceParent, want: beforeSourceParent},
		{location: sourceParent, want: afterSourceParent},
		{location: destinationParent, want: beforeDestinationParent},
		{location: destinationParent, want: afterDestinationParent},
		{location: unrelated, want: beforeUnrelated},
	}
	for index, check := range checks {
		got := statEntry(t, implementation, check.location)
		if !reflect.DeepEqual(got, check.want) {
			t.Fatalf("historical check %d for %q = %#v, want %#v", index, check.location.Path, got, check.want)
		}
	}
	_, err = implementation.Stat(context.Background(), providerapi.StatRequest{Location: unrelated})
	requireUnavailableStaleRevision(t, err)

	wantMoved := cloneEntry(beforeNode)
	wantMoved.Location = destination
	wantMoved.Name = "moved-contract-file"
	if historical := statEntry(t, implementation, destination); !reflect.DeepEqual(historical, wantMoved) {
		t.Fatalf("moved node revision 1 = %#v, want rebased %#v", historical, wantMoved)
	}
	_, err = implementation.Stat(context.Background(), providerapi.StatRequest{Location: destination})
	requireUnavailableStaleRevision(t, err)
	if !reflect.DeepEqual(afterUnrelated, beforeUnrelated) {
		t.Fatalf("unrelated directory changed: before=%#v after=%#v", beforeUnrelated, afterUnrelated)
	}
	if !reflect.DeepEqual(afterNode, wantMoved) {
		t.Fatalf("rename changed moved node fingerprint: after=%#v want=%#v", afterNode, wantMoved)
	}
}

func TestUnknownStaleRevisionIsConsumedWithoutStateChange(t *testing.T) {
	scenario := validScenario(t)
	scenario.Script = []FaultStep{
		staleStatStep(2, 1),
		staleStatStep(3, 999),
	}
	implementation, controller, err := New(scenario)
	if err != nil {
		t.Fatalf("New(): %v", err)
	}
	location := domain.Location{EndpointID: contractEndpointID, Path: "/contract-file"}
	root := domain.Location{EndpointID: contractEndpointID, Path: "/"}
	beforeSnapshot, err := implementation.Snapshot(context.Background())
	if err != nil {
		t.Fatalf("Snapshot(before): %v", err)
	}
	beforePage, err := implementation.List(context.Background(), providerapi.ListRequest{
		Location: root,
		Limit:    1,
	})
	if err != nil {
		t.Fatalf("List(before): %v", err)
	}
	if beforePage.Done || beforePage.NextCursor == "" {
		t.Fatalf("List(before) = %#v, want continuation cursor", beforePage)
	}

	liveBefore := statEntry(t, implementation, location)
	stale := statEntry(t, implementation, location)
	if !reflect.DeepEqual(stale, liveBefore) {
		t.Fatalf("stale revision 1 = %#v, want captured %#v", stale, liveBefore)
	}
	_, err = implementation.Stat(context.Background(), providerapi.StatRequest{Location: location})
	requireUnavailableStaleRevision(t, err)
	if live := statEntry(t, implementation, location); !reflect.DeepEqual(live, stale) {
		t.Fatalf("normal Stat after consumed unknown revision = %#v, want %#v", live, stale)
	}
	afterSnapshot, err := implementation.Snapshot(context.Background())
	if err != nil {
		t.Fatalf("Snapshot(after): %v", err)
	}
	if !reflect.DeepEqual(afterSnapshot, beforeSnapshot) {
		t.Fatalf("snapshot changed after stale reads: before=%#v after=%#v", beforeSnapshot, afterSnapshot)
	}
	afterPage, err := implementation.List(context.Background(), providerapi.ListRequest{
		Location: root,
		Limit:    1,
		Cursor:   beforePage.NextCursor,
	})
	if err != nil {
		t.Fatalf("List(after, prior cursor): %v", err)
	}
	if !reflect.DeepEqual(afterPage.DirectoryFingerprint, beforePage.DirectoryFingerprint) {
		t.Fatalf("directory fingerprint changed after stale reads: before=%#v after=%#v", beforePage.DirectoryFingerprint, afterPage.DirectoryFingerprint)
	}
	statCalls := 0
	for _, call := range controller.Calls() {
		if call.Operation == OperationStat {
			statCalls++
		}
	}
	if statCalls != 4 {
		t.Fatalf("recorded Stat calls = %d, want 4", statCalls)
	}
}

func TestStaleHistoryFollowsNodeIdentityAcrossRenameAndABA(t *testing.T) {
	scenario := validScenario(t)
	scenario.Script = []FaultStep{
		staleStatStep(3, 1),
		staleStatStep(6, 1),
		staleStatStep(7, 1),
		staleStatStep(8, 2),
		staleStatStep(9, 3),
	}
	implementation, _, err := New(scenario)
	if err != nil {
		t.Fatalf("New(): %v", err)
	}
	mutable := requireMutable(t, implementation)
	source := domain.Location{EndpointID: contractEndpointID, Path: "/contract-file"}
	destination := domain.Location{EndpointID: contractEndpointID, Path: "/renamed-history"}
	revisionOneAtSource := statEntry(t, implementation, source)
	handle, err := implementation.OpenWrite(context.Background(), providerapi.OpenWriteRequest{
		Location:    source,
		Offset:      int64(len("contract data")),
		Disposition: providerapi.WriteResumeExisting,
	})
	if err != nil {
		t.Fatalf("OpenWrite(): %v", err)
	}
	if n, err := handle.Write(context.Background(), []byte("!")); n != 1 || err != nil {
		t.Fatalf("Write() = (%d, %v), want (1, nil)", n, err)
	}
	revisionTwoAtSource := statEntry(t, implementation, source)
	if _, err := mutable.Rename(context.Background(), providerapi.RenameRequest{
		Source:      source,
		Destination: destination,
	}); err != nil {
		t.Fatalf("Rename(): %v", err)
	}
	_, err = implementation.Stat(context.Background(), providerapi.StatRequest{Location: source})
	requireCode(t, err, domain.CodeNotFound)
	if n, err := handle.Write(context.Background(), []byte("?")); n != 1 || err != nil {
		t.Fatalf("Write(after rename) = (%d, %v), want (1, nil)", n, err)
	}
	revisionThreeAtDestination := statEntry(t, implementation, destination)
	if _, err := implementation.OpenWrite(context.Background(), providerapi.OpenWriteRequest{
		Location:    source,
		Disposition: providerapi.WriteCreateNew,
	}); err != nil {
		t.Fatalf("OpenWrite(recreate source): %v", err)
	}
	recreatedAtSource := statEntry(t, implementation, source)
	if reflect.DeepEqual(recreatedAtSource.Fingerprint, revisionOneAtSource.Fingerprint) {
		t.Fatalf("recreated node reused original identity: recreated=%#v original=%#v", recreatedAtSource, revisionOneAtSource)
	}

	if historicalB := statEntry(t, implementation, source); !reflect.DeepEqual(historicalB, recreatedAtSource) {
		t.Fatalf("old path revision 1 leaked original node: got=%#v want recreated=%#v", historicalB, recreatedAtSource)
	}
	wantRevisionOne := cloneEntry(revisionOneAtSource)
	wantRevisionOne.Location = destination
	wantRevisionOne.Name = "renamed-history"
	wantRevisionTwo := cloneEntry(revisionTwoAtSource)
	wantRevisionTwo.Location = destination
	wantRevisionTwo.Name = "renamed-history"
	if historical := statEntry(t, implementation, destination); !reflect.DeepEqual(historical, wantRevisionOne) {
		t.Fatalf("destination revision 1 = %#v, want %#v", historical, wantRevisionOne)
	}
	if historical := statEntry(t, implementation, destination); !reflect.DeepEqual(historical, wantRevisionTwo) {
		t.Fatalf("destination revision 2 = %#v, want %#v", historical, wantRevisionTwo)
	}
	if historical := statEntry(t, implementation, destination); !reflect.DeepEqual(historical, revisionThreeAtDestination) {
		t.Fatalf("destination revision 3 = %#v, want %#v", historical, revisionThreeAtDestination)
	}
}

func TestStaleSymlinkHistoryFreezesRawTargetAndResolvedKind(t *testing.T) {
	scenario := validScenario(t)
	scenario.Script = []FaultStep{
		staleStatStep(3, 1),
		staleStatStep(4, 1),
	}
	implementation, _, err := New(scenario)
	if err != nil {
		t.Fatalf("New(): %v", err)
	}
	mutable := requireMutable(t, implementation)
	target := domain.Location{EndpointID: contractEndpointID, Path: "/contract-file"}
	link := domain.Location{EndpointID: contractEndpointID, Path: "/contract-link"}
	initial := statEntry(t, implementation, link)
	if err := mutable.Remove(context.Background(), providerapi.RemoveRequest{Location: target}); err != nil {
		t.Fatalf("Remove(target): %v", err)
	}
	if _, err := mutable.Mkdir(context.Background(), providerapi.MkdirRequest{
		Location:  target,
		Exclusive: true,
	}); err != nil {
		t.Fatalf("Mkdir(replacement target): %v", err)
	}
	live := statEntry(t, implementation, link)
	if live.Symlink == nil || live.Symlink.ResolvedKind == nil ||
		*live.Symlink.ResolvedKind != domain.EntryDirectory {
		t.Fatalf("live symlink = %#v, want replacement directory resolution", live.Symlink)
	}
	historical := statEntry(t, implementation, link)
	if !reflect.DeepEqual(historical, initial) {
		t.Fatalf("historical symlink = %#v, want captured %#v", historical, initial)
	}
	historical.Symlink.RawTarget = "caller-mutated"
	*historical.Symlink.ResolvedKind = domain.EntryOther
	historicalAgain := statEntry(t, implementation, link)
	if !reflect.DeepEqual(historicalAgain, initial) {
		t.Fatalf("historical symlink leaked live/caller changes: got=%#v want=%#v", historicalAgain, initial)
	}
}

func TestStaleStatRequiresCurrentLivePath(t *testing.T) {
	scenario := validScenario(t)
	scenario.Script = []FaultStep{
		staleStatStep(2, 1),
		staleStatStep(4, 1),
	}
	implementation, controller, err := New(scenario)
	if err != nil {
		t.Fatalf("New(): %v", err)
	}
	mutable := requireMutable(t, implementation)
	location := domain.Location{EndpointID: contractEndpointID, Path: "/contract-file"}
	original := statEntry(t, implementation, location)
	if err := mutable.Remove(context.Background(), providerapi.RemoveRequest{Location: location}); err != nil {
		t.Fatalf("Remove(): %v", err)
	}
	_, err = implementation.Stat(context.Background(), providerapi.StatRequest{Location: location})
	requireCode(t, err, domain.CodeNotFound)
	if _, err := implementation.OpenWrite(context.Background(), providerapi.OpenWriteRequest{
		Location:    location,
		Disposition: providerapi.WriteCreateNew,
	}); err != nil {
		t.Fatalf("OpenWrite(recreate): %v", err)
	}
	recreatedLive := statEntry(t, implementation, location)
	if reflect.DeepEqual(recreatedLive.Fingerprint, original.Fingerprint) {
		t.Fatalf("recreated path reused removed node identity: recreated=%#v original=%#v", recreatedLive, original)
	}
	if recreatedHistorical := statEntry(t, implementation, location); !reflect.DeepEqual(recreatedHistorical, recreatedLive) {
		t.Fatalf("recreated path revision 1 = %#v, want new live node %#v", recreatedHistorical, recreatedLive)
	}
	statCalls := 0
	for _, call := range controller.Calls() {
		if call.Operation == OperationStat {
			statCalls++
		}
	}
	if statCalls != 4 {
		t.Fatalf("recorded Stat calls = %d, want 4", statCalls)
	}
}

func TestConcurrentStaleReadsAndHistoryWritesAreRaceSafe(t *testing.T) {
	const readers = 24
	scenario := validScenario(t)
	scenario.Root.Children = append(scenario.Root.Children, Node{
		Name: "history-race",
		Kind: domain.EntryFile,
	})
	for nth := uint64(1); nth <= readers; nth++ {
		scenario.Script = append(scenario.Script, staleStatStep(nth, 1))
	}
	for index := uint64(0); index < readers; index++ {
		scenario.Script = append(scenario.Script, staleStatStep(readers+index+1, index+2))
	}
	implementation, _, err := New(scenario)
	if err != nil {
		t.Fatalf("New(): %v", err)
	}
	location := domain.Location{EndpointID: contractEndpointID, Path: "/history-race"}
	handle, err := implementation.OpenWrite(context.Background(), providerapi.OpenWriteRequest{
		Location:    location,
		Disposition: providerapi.WriteResumeExisting,
	})
	if err != nil {
		t.Fatalf("OpenWrite(): %v", err)
	}
	start := make(chan struct{})
	entries := make(chan domain.Entry, readers)
	errorsSeen := make(chan error, readers+1)
	var wait sync.WaitGroup
	for index := 0; index < readers; index++ {
		wait.Add(1)
		go func() {
			defer wait.Done()
			<-start
			entry, err := implementation.Stat(context.Background(), providerapi.StatRequest{
				Location: location,
			})
			if err != nil {
				errorsSeen <- err
				return
			}
			entries <- entry
		}()
	}
	wait.Add(1)
	go func() {
		defer wait.Done()
		<-start
		for index := 0; index < readers; index++ {
			n, err := handle.Write(context.Background(), []byte{'x'})
			if err != nil {
				errorsSeen <- fmt.Errorf("Write(%d) wrote %d bytes: %w", index, n, err)
				return
			}
			if n != 1 {
				errorsSeen <- fmt.Errorf("Write(%d) = (%d, nil), want (1, nil)", index, n)
				return
			}
		}
	}()
	close(start)
	wait.Wait()
	close(entries)
	close(errorsSeen)
	for err := range errorsSeen {
		t.Errorf("concurrent operation: %v", err)
	}
	count := 0
	for entry := range entries {
		count++
		requireEntrySize(t, entry, 0)
	}
	if count != readers {
		t.Fatalf("stale results = %d, want %d", count, readers)
	}
	for revision := uint64(2); revision <= readers+1; revision++ {
		historical := statEntry(t, implementation, location)
		requireEntrySize(t, historical, revision-1)
	}
	if err := handle.Close(context.Background()); err != nil {
		t.Fatalf("Close(): %v", err)
	}
}

func TestFailedMutationsDoNotCreateHistoryRevisions(t *testing.T) {
	root := domain.Location{EndpointID: contractEndpointID, Path: "/"}
	file := domain.Location{EndpointID: contractEndpointID, Path: "/contract-file"}
	directory := domain.Location{EndpointID: contractEndpointID, Path: "/contract-directory"}
	nested := domain.Location{
		EndpointID: contractEndpointID,
		Path:       "/contract-directory/nested-file",
	}
	tests := []struct {
		name      string
		observed  []domain.Location
		wantCode  domain.Code
		operation func(*testing.T, *Provider, map[domain.CanonicalPath]domain.Entry) error
	}{
		{
			name:     "truncate fingerprint mismatch",
			observed: []domain.Location{file, root},
			wantCode: domain.CodeConflict,
			operation: func(
				t *testing.T,
				implementation *Provider,
				before map[domain.CanonicalPath]domain.Entry,
			) error {
				t.Helper()
				mismatch := cloneFingerprint(before[file.Path].Fingerprint)
				wrongVersion := "wrong-version"
				mismatch.VersionID = &wrongVersion
				_, err := implementation.OpenWrite(context.Background(), providerapi.OpenWriteRequest{
					Location:            file,
					Disposition:         providerapi.WriteTruncate,
					ExpectedFingerprint: &mismatch,
				})
				return err
			},
		},
		{
			name:     "create existing",
			observed: []domain.Location{file, root},
			wantCode: domain.CodeAlreadyExists,
			operation: func(
				_ *testing.T,
				implementation *Provider,
				_ map[domain.CanonicalPath]domain.Entry,
			) error {
				_, err := implementation.OpenWrite(context.Background(), providerapi.OpenWriteRequest{
					Location:    file,
					Disposition: providerapi.WriteCreateNew,
				})
				return err
			},
		},
		{
			name:     "mkdir existing exclusive",
			observed: []domain.Location{directory, root},
			wantCode: domain.CodeAlreadyExists,
			operation: func(
				_ *testing.T,
				implementation *Provider,
				_ map[domain.CanonicalPath]domain.Entry,
			) error {
				_, err := implementation.Mkdir(context.Background(), providerapi.MkdirRequest{
					Location:  directory,
					Exclusive: true,
				})
				return err
			},
		},
		{
			name:     "remove nonempty directory",
			observed: []domain.Location{directory, root},
			wantCode: domain.CodeConflict,
			operation: func(
				_ *testing.T,
				implementation *Provider,
				_ map[domain.CanonicalPath]domain.Entry,
			) error {
				return implementation.Remove(context.Background(), providerapi.RemoveRequest{
					Location: directory,
				})
			},
		},
		{
			name:     "rename existing destination",
			observed: []domain.Location{file, root, directory},
			wantCode: domain.CodeAlreadyExists,
			operation: func(
				_ *testing.T,
				implementation *Provider,
				_ map[domain.CanonicalPath]domain.Entry,
			) error {
				_, err := implementation.Rename(context.Background(), providerapi.RenameRequest{
					Source:      file,
					Destination: nested,
				})
				return err
			},
		},
		{
			name:     "zero length write",
			observed: []domain.Location{file, root},
			operation: func(
				_ *testing.T,
				implementation *Provider,
				_ map[domain.CanonicalPath]domain.Entry,
			) error {
				handle, err := implementation.OpenWrite(
					context.Background(),
					providerapi.OpenWriteRequest{
						Location:    file,
						Disposition: providerapi.WriteResumeExisting,
					},
				)
				if err != nil {
					return err
				}
				n, err := handle.Write(context.Background(), nil)
				if err != nil {
					return err
				}
				if n != 0 {
					return fmt.Errorf("zero-length Write() n = %d, want 0", n)
				}
				return handle.Close(context.Background())
			},
		},
		{
			name:     "same dirent rename",
			observed: []domain.Location{file, root},
			operation: func(
				_ *testing.T,
				implementation *Provider,
				_ map[domain.CanonicalPath]domain.Entry,
			) error {
				_, err := implementation.Rename(context.Background(), providerapi.RenameRequest{
					Source:      file,
					Destination: file,
				})
				return err
			},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			scenario := validScenario(t)
			for index := range test.observed {
				scenario.Script = append(
					scenario.Script,
					staleStatStep(uint64(len(test.observed)+index+1), 2),
				)
			}
			implementation, _, err := New(scenario)
			if err != nil {
				t.Fatalf("New(): %v", err)
			}
			before := make(map[domain.CanonicalPath]domain.Entry, len(test.observed))
			for _, location := range test.observed {
				before[location.Path] = statEntry(t, implementation, location)
			}
			err = test.operation(t, implementation, before)
			if test.wantCode == "" {
				if err != nil {
					t.Fatalf("operation: %v", err)
				}
			} else {
				requireCode(t, err, test.wantCode)
			}

			for _, location := range test.observed {
				_, err := implementation.Stat(
					context.Background(),
					providerapi.StatRequest{Location: location},
				)
				requireUnavailableStaleRevision(t, err)
			}
			for _, location := range test.observed {
				after := statEntry(t, implementation, location)
				if !reflect.DeepEqual(after, before[location.Path]) {
					t.Fatalf("%q changed after failed/no-op mutation: before=%#v after=%#v", location.Path, before[location.Path], after)
				}
			}
		})
	}
}

func TestDelayUsesManualClock(t *testing.T) {
	clock := newObservedManualClock(time.Unix(100, 0).UTC())
	scenario := validScenario(t)
	scenario.Clock = clock
	scenario.Script = []FaultStep{{
		Match: FaultMatch{Operation: OperationStat, Nth: 1},
		Effect: FaultEffect{
			Delay: 5 * time.Second,
			Error: faultError(domain.CodeTimeout),
		},
	}}
	implementation, controller, err := New(scenario)
	if err != nil {
		t.Fatalf("New(): %v", err)
	}
	result := make(chan error, 1)
	go func() {
		_, err := implementation.Stat(context.Background(), providerapi.StatRequest{
			Location: domain.Location{EndpointID: contractEndpointID, Path: "/contract-file"},
		})
		result <- err
	}()

	select {
	case duration := <-clock.registered:
		if duration != 5*time.Second {
			t.Fatalf("registered delay = %v, want 5s", duration)
		}
	case err := <-result:
		t.Fatalf("Stat() returned before timer registration: %v", err)
	}
	select {
	case err := <-result:
		t.Fatalf("Stat() returned before Advance: %v", err)
	default:
	}
	controller.Advance(4 * time.Second)
	select {
	case err := <-result:
		t.Fatalf("Stat() returned before full delay: %v", err)
	default:
	}
	controller.Advance(time.Second)
	requireCode(t, <-result, domain.CodeTimeout)

	calls := controller.Calls()
	if len(calls) != 1 || calls[0].Operation != OperationStat {
		t.Fatalf("Calls() = %#v, want one Stat", calls)
	}
}

func TestDelayCancellationStopsTimer(t *testing.T) {
	clock := newObservedManualClock(time.Unix(200, 0).UTC())
	scenario := validScenario(t)
	scenario.Clock = clock
	scenario.Script = []FaultStep{{
		Match:  FaultMatch{Operation: OperationStat, Nth: 1},
		Effect: FaultEffect{Delay: time.Hour},
	}}
	implementation, controller, err := New(scenario)
	if err != nil {
		t.Fatalf("New(): %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	result := make(chan error, 1)
	go func() {
		_, err := implementation.Stat(ctx, providerapi.StatRequest{
			Location: domain.Location{EndpointID: contractEndpointID, Path: "/contract-file"},
		})
		result <- err
	}()

	select {
	case <-clock.registered:
	case err := <-result:
		t.Fatalf("Stat() returned before timer registration: %v", err)
	}
	cancel()
	if err := <-result; !errors.Is(err, context.Canceled) {
		t.Fatalf("Stat() error = %v, want context.Canceled", err)
	}
	if stopped := <-clock.stopped; !stopped {
		t.Fatal("delay timer Stop() = false, want true")
	}
	if calls := controller.Calls(); len(calls) != 1 {
		t.Fatalf("Calls() length = %d, want 1", len(calls))
	}

	if _, err := implementation.Stat(context.Background(), providerapi.StatRequest{
		Location: domain.Location{EndpointID: contractEndpointID, Path: "/contract-file"},
	}); err != nil {
		t.Fatalf("second Stat() after consumed canceled fault: %v", err)
	}
}

func TestDelayRunsBeforeGate(t *testing.T) {
	clock := newObservedManualClock(time.Unix(300, 0).UTC())
	scenario := validScenario(t)
	scenario.Clock = clock
	scenario.Script = []FaultStep{{
		Match: FaultMatch{Operation: OperationStat, Nth: 1},
		Effect: FaultEffect{
			Delay:    2 * time.Second,
			WaitGate: "after-delay",
		},
	}}
	implementation, controller, err := New(scenario)
	if err != nil {
		t.Fatalf("New(): %v", err)
	}
	ctx := newDoneObservedContext(context.Background(), 4)
	result := make(chan error, 1)
	go func() {
		_, err := implementation.Stat(ctx, providerapi.StatRequest{
			Location: domain.Location{EndpointID: contractEndpointID, Path: "/contract-file"},
		})
		result <- err
	}()

	select {
	case <-clock.registered:
	case err := <-result:
		t.Fatalf("Stat() returned before delay registration: %v", err)
	}
	select {
	case <-ctx.observed:
	case err := <-result:
		t.Fatalf("Stat() returned before delay select: %v", err)
	}
	select {
	case <-ctx.observed:
		t.Fatal("gate wait began before the delay elapsed")
	default:
	}
	controller.Advance(2 * time.Second)
	select {
	case <-ctx.observed:
	case err := <-result:
		t.Fatalf("Stat() returned before gate release: %v", err)
	}
	select {
	case err := <-result:
		t.Fatalf("Stat() returned before gate release: %v", err)
	default:
	}
	controller.ReleaseGate("after-delay")
	if err := <-result; err != nil {
		t.Fatalf("Stat(): %v", err)
	}
}

func TestGateHonorsContextCancellation(t *testing.T) {
	scenario := validScenario(t)
	scenario.Script = []FaultStep{{
		Match:  FaultMatch{Operation: OperationStat, Nth: 1},
		Effect: FaultEffect{WaitGate: "cancel-gate"},
	}}
	implementation, controller, err := New(scenario)
	if err != nil {
		t.Fatalf("New(): %v", err)
	}
	base, cancel := context.WithCancel(context.Background())
	ctx := newDoneObservedContext(base, 2)
	result := make(chan error, 1)
	go func() {
		_, err := implementation.Stat(ctx, providerapi.StatRequest{
			Location: domain.Location{EndpointID: contractEndpointID, Path: "/contract-file"},
		})
		result <- err
	}()

	select {
	case <-ctx.observed:
	case err := <-result:
		t.Fatalf("Stat() returned before gate wait: %v", err)
	}
	cancel()
	if err := <-result; !errors.Is(err, context.Canceled) {
		t.Fatalf("Stat() error = %v, want context.Canceled", err)
	}
	if calls := controller.Calls(); len(calls) != 1 {
		t.Fatalf("Calls() length = %d, want 1", len(calls))
	}
	if _, err := implementation.Stat(context.Background(), providerapi.StatRequest{
		Location: domain.Location{EndpointID: contractEndpointID, Path: "/contract-file"},
	}); err != nil {
		t.Fatalf("second Stat() after consumed canceled gate: %v", err)
	}
}

func TestGateReleaseIsStickyIdempotentAndBroadcasts(t *testing.T) {
	t.Run("release before wait is sticky", func(t *testing.T) {
		scenario := validScenario(t)
		scenario.Script = []FaultStep{{
			Match:  FaultMatch{Operation: OperationStat, Nth: 1},
			Effect: FaultEffect{WaitGate: "sticky"},
		}}
		implementation, controller, err := New(scenario)
		if err != nil {
			t.Fatalf("New(): %v", err)
		}
		controller.ReleaseGate("sticky")
		controller.ReleaseGate("sticky")
		if _, err := implementation.Stat(context.Background(), providerapi.StatRequest{
			Location: domain.Location{EndpointID: contractEndpointID, Path: "/contract-file"},
		}); err != nil {
			t.Fatalf("Stat(): %v", err)
		}
	})

	t.Run("one release broadcasts to every waiter", func(t *testing.T) {
		scenario := validScenario(t)
		scenario.Script = []FaultStep{
			{
				Match:  FaultMatch{Operation: OperationStat, Nth: 1},
				Effect: FaultEffect{WaitGate: "broadcast"},
			},
			{
				Match:  FaultMatch{Operation: OperationStat, Nth: 2},
				Effect: FaultEffect{WaitGate: "broadcast"},
			},
		}
		implementation, controller, err := New(scenario)
		if err != nil {
			t.Fatalf("New(): %v", err)
		}
		contexts := []*doneObservedContext{
			newDoneObservedContext(context.Background(), 1),
			newDoneObservedContext(context.Background(), 1),
		}
		results := []chan error{make(chan error, 1), make(chan error, 1)}
		for index := range contexts {
			go func(index int) {
				_, err := implementation.Stat(contexts[index], providerapi.StatRequest{
					Location: domain.Location{
						EndpointID: contractEndpointID,
						Path:       "/contract-file",
					},
				})
				results[index] <- err
			}(index)
		}
		for index := range contexts {
			select {
			case <-contexts[index].observed:
			case err := <-results[index]:
				t.Fatalf("Stat(%d) returned before gate wait: %v", index, err)
			}
		}
		controller.ReleaseGate("broadcast")
		controller.ReleaseGate("broadcast")
		for index := range results {
			if err := <-results[index]; err != nil {
				t.Fatalf("Stat(%d): %v", index, err)
			}
		}
	})
}

func TestReleaseGateRejectsUnknownAndEmptyNames(t *testing.T) {
	scenario := validScenario(t)
	scenario.Script = []FaultStep{{
		Match:  FaultMatch{Operation: OperationStat, Nth: 1},
		Effect: FaultEffect{WaitGate: "known"},
	}}
	_, controller, err := New(scenario)
	if err != nil {
		t.Fatalf("New(): %v", err)
	}
	requirePanicContaining(t, "empty", func() { controller.ReleaseGate("") })
	requirePanicContaining(t, "unknown", func() { controller.ReleaseGate("missing") })
}

func TestAdvanceRejectsNonAdvanceableClock(t *testing.T) {
	scenario := validScenario(t)
	scenario.Clock = foundation.RealClock{}
	_, controller, err := New(scenario)
	if err != nil {
		t.Fatalf("New(): %v", err)
	}
	requirePanicContaining(t, "does not support Advance", func() {
		controller.Advance(time.Second)
	})
}

func TestGateWaitDoesNotHoldScriptProviderOrHandleLocks(t *testing.T) {
	scenario := validScenario(t)
	scenario.Script = []FaultStep{{
		Match:  FaultMatch{Operation: OperationRead, Nth: 1},
		Effect: FaultEffect{WaitGate: "read-gate"},
	}}
	implementation, controller, err := New(scenario)
	if err != nil {
		t.Fatalf("New(): %v", err)
	}
	location := domain.Location{EndpointID: contractEndpointID, Path: "/contract-file"}
	first, err := implementation.OpenRead(context.Background(), providerapi.OpenReadRequest{
		Location: location,
	})
	if err != nil {
		t.Fatalf("OpenRead(first): %v", err)
	}
	second, err := implementation.OpenRead(context.Background(), providerapi.OpenReadRequest{
		Location: location,
	})
	if err != nil {
		t.Fatalf("OpenRead(second): %v", err)
	}
	ctx := newDoneObservedContext(context.Background(), 1)
	type readResult struct {
		n   int
		err error
	}
	result := make(chan readResult, 1)
	go func() {
		n, err := first.Read(ctx, make([]byte, 1))
		result <- readResult{n: n, err: err}
	}()
	select {
	case <-ctx.observed:
	case got := <-result:
		t.Fatalf("first Read() returned before gate wait: (%d, %v)", got.n, got.err)
	}

	if calls := controller.Calls(); len(calls) != 3 || calls[2].Operation != OperationRead {
		t.Fatalf("Calls() while gate waits = %#v", calls)
	}
	if _, err := implementation.Snapshot(context.Background()); err != nil {
		t.Fatalf("Snapshot() while gate waits: %v", err)
	}
	if info := first.Info(); info.Entry.Location != location {
		t.Fatalf("Info() while gate waits = %#v", info)
	}
	if n, err := second.Read(context.Background(), make([]byte, 1)); n != 1 || err != nil {
		t.Fatalf("second Read() while gate waits = (%d, %v)", n, err)
	}
	controller.ReleaseGate("read-gate")
	if got := <-result; got.n != 1 || got.err != nil {
		t.Fatalf("first Read() = (%d, %v), want (1, nil)", got.n, got.err)
	}
}

func TestClockNewTimerCanReenterSnapshot(t *testing.T) {
	clock := newObservedManualClock(time.Unix(400, 0).UTC())
	scenario := validScenario(t)
	scenario.Clock = clock
	scenario.Script = []FaultStep{{
		Match:  FaultMatch{Operation: OperationStat, Nth: 1},
		Effect: FaultEffect{Delay: time.Second},
	}}
	implementation, controller, err := New(scenario)
	if err != nil {
		t.Fatalf("New(): %v", err)
	}
	reentered := make(chan error, 1)
	clock.onNewTimer = func() {
		_, err := implementation.Snapshot(context.Background())
		reentered <- err
	}
	result := make(chan error, 1)
	go func() {
		_, err := implementation.Stat(context.Background(), providerapi.StatRequest{
			Location: domain.Location{EndpointID: contractEndpointID, Path: "/contract-file"},
		})
		result <- err
	}()

	select {
	case err := <-reentered:
		if err != nil {
			t.Fatalf("reentrant Snapshot(): %v", err)
		}
	case err := <-result:
		t.Fatalf("Stat() returned before reentrant Snapshot: %v", err)
	}
	select {
	case <-clock.registered:
	case err := <-result:
		t.Fatalf("Stat() returned before timer registration: %v", err)
	}
	controller.Advance(time.Second)
	if err := <-result; err != nil {
		t.Fatalf("Stat(): %v", err)
	}
}

func TestListDelayPrecedesSnapshotMaterialization(t *testing.T) {
	clock := newObservedManualClock(time.Unix(500, 0).UTC())
	scenario := validScenario(t)
	scenario.Clock = clock
	scenario.DefaultLimit = 4096
	scenario.Script = []FaultStep{{
		Match:  FaultMatch{Operation: OperationList, Nth: 1},
		Effect: FaultEffect{Delay: time.Second},
	}}
	implementation, controller, err := New(scenario)
	if err != nil {
		t.Fatalf("New(): %v", err)
	}
	type listResult struct {
		page providerapi.ListPage
		err  error
	}
	result := make(chan listResult, 1)
	root := domain.Location{EndpointID: contractEndpointID, Path: "/"}
	go func() {
		page, err := implementation.List(context.Background(), providerapi.ListRequest{
			Location: root,
			Limit:    4096,
		})
		result <- listResult{page: page, err: err}
	}()
	select {
	case <-clock.registered:
	case got := <-result:
		t.Fatalf("List() returned before timer registration: %v", got.err)
	}

	created := domain.Location{EndpointID: contractEndpointID, Path: "/created-during-delay"}
	handle, err := implementation.OpenWrite(context.Background(), providerapi.OpenWriteRequest{
		Location:    created,
		Disposition: providerapi.WriteCreateNew,
	})
	if err != nil {
		t.Fatalf("OpenWrite() while List waits: %v", err)
	}
	if err := handle.Close(context.Background()); err != nil {
		t.Fatalf("Close() while List waits: %v", err)
	}
	controller.Advance(time.Second)
	got := <-result
	if got.err != nil {
		t.Fatalf("List(): %v", got.err)
	}
	found := false
	for _, entry := range got.page.Entries {
		if entry.Location == created {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("List() snapshot was materialized before delay: %#v", got.page.Entries)
	}
}

func TestGateReleaseIsFixtureLocal(t *testing.T) {
	scenario := validScenario(t)
	scenario.Script = []FaultStep{{
		Match:  FaultMatch{Operation: OperationStat, Nth: 1},
		Effect: FaultEffect{WaitGate: "fixture-gate"},
	}}
	first, firstController, err := New(scenario)
	if err != nil {
		t.Fatalf("New(first): %v", err)
	}
	second, secondController, err := New(scenario)
	if err != nil {
		t.Fatalf("New(second): %v", err)
	}
	location := domain.Location{EndpointID: contractEndpointID, Path: "/contract-file"}
	firstContext := newDoneObservedContext(context.Background(), 1)
	secondContext := newDoneObservedContext(context.Background(), 1)
	firstResult := make(chan error, 1)
	secondResult := make(chan error, 1)
	go func() {
		_, err := first.Stat(firstContext, providerapi.StatRequest{Location: location})
		firstResult <- err
	}()
	go func() {
		_, err := second.Stat(secondContext, providerapi.StatRequest{Location: location})
		secondResult <- err
	}()
	select {
	case <-firstContext.observed:
	case err := <-firstResult:
		t.Fatalf("first Stat() returned before gate wait: %v", err)
	}
	select {
	case <-secondContext.observed:
	case err := <-secondResult:
		t.Fatalf("second Stat() returned before gate wait: %v", err)
	}

	firstController.ReleaseGate("fixture-gate")
	if err := <-firstResult; err != nil {
		t.Fatalf("first Stat(): %v", err)
	}
	select {
	case err := <-secondResult:
		t.Fatalf("second fixture gate was released by first: %v", err)
	default:
	}
	secondController.ReleaseGate("fixture-gate")
	if err := <-secondResult; err != nil {
		t.Fatalf("second Stat(): %v", err)
	}
}

func TestNthCallFaultIsConsumedOnce(t *testing.T) {
	scenario := validScenario(t)
	scenario.Script = []FaultStep{{
		Match:  FaultMatch{Operation: OperationStat, Nth: 2},
		Effect: FaultEffect{Error: faultError(domain.CodeTimeout)},
	}}
	implementation, controller, err := New(scenario)
	if err != nil {
		t.Fatalf("New(): %v", err)
	}
	location := domain.Location{EndpointID: contractEndpointID, Path: "/contract-file"}

	if _, err := implementation.Stat(context.Background(), providerapi.StatRequest{
		Location: location,
	}); err != nil {
		t.Fatalf("first Stat(): %v", err)
	}
	_, err = implementation.Stat(context.Background(), providerapi.StatRequest{Location: location})
	if got := requireCode(t, err, domain.CodeTimeout); got.Location == nil || *got.Location != location {
		t.Fatalf("second Stat() Location = %#v, want %#v", got.Location, location)
	}
	if _, err := implementation.Stat(context.Background(), providerapi.StatRequest{
		Location: location,
	}); err != nil {
		t.Fatalf("third Stat(): %v", err)
	}

	calls := controller.Calls()
	if len(calls) != 3 {
		t.Fatalf("Calls() length = %d, want 3", len(calls))
	}
	for index, call := range calls {
		if call.Operation != OperationStat || call.Sequence != uint64(index+1) {
			t.Fatalf("Calls()[%d] = %#v", index, call)
		}
	}
}

func TestExactPathNthCountersAreIndependent(t *testing.T) {
	file := domain.CanonicalPath("/contract-file")
	directory := domain.CanonicalPath("/contract-directory")
	scenario := validScenario(t)
	scenario.Script = []FaultStep{
		{
			Match:  FaultMatch{Operation: OperationStat, Nth: 2, Path: &file},
			Effect: FaultEffect{Error: faultError(domain.CodeTimeout)},
		},
		{
			Match:  FaultMatch{Operation: OperationStat, Nth: 1, Path: &directory},
			Effect: FaultEffect{Error: faultError(domain.CodePermissionDenied)},
		},
	}
	implementation, _, err := New(scenario)
	if err != nil {
		t.Fatalf("New(): %v", err)
	}
	fileLocation := domain.Location{EndpointID: contractEndpointID, Path: file}
	directoryLocation := domain.Location{EndpointID: contractEndpointID, Path: directory}

	if _, err := implementation.Stat(context.Background(), providerapi.StatRequest{
		Location: fileLocation,
	}); err != nil {
		t.Fatalf("first file Stat(): %v", err)
	}
	_, err = implementation.Stat(context.Background(), providerapi.StatRequest{
		Location: directoryLocation,
	})
	requireCode(t, err, domain.CodePermissionDenied)
	_, err = implementation.Stat(context.Background(), providerapi.StatRequest{
		Location: fileLocation,
	})
	requireCode(t, err, domain.CodeTimeout)
	if _, err := implementation.Stat(context.Background(), providerapi.StatRequest{
		Location: directoryLocation,
	}); err != nil {
		t.Fatalf("second directory Stat(): %v", err)
	}
}

func TestInvalidAndInitiallyCanceledCallsDoNotRecordOrConsume(t *testing.T) {
	file := domain.CanonicalPath("/contract-file")
	scenario := validScenario(t)
	scenario.Script = []FaultStep{{
		Match:  FaultMatch{Operation: OperationStat, Nth: 1, Path: &file},
		Effect: FaultEffect{Error: faultError(domain.CodeTimeout)},
	}}
	implementation, controller, err := New(scenario)
	if err != nil {
		t.Fatalf("New(): %v", err)
	}

	invalid := domain.Location{EndpointID: contractEndpointID, Path: "/directory/../contract-file"}
	if _, err := implementation.Stat(context.Background(), providerapi.StatRequest{
		Location: invalid,
	}); !domain.IsCode(err, domain.CodeInvalidArgument) {
		t.Fatalf("invalid Stat() error = %v, want invalid_argument", err)
	}
	canceled, cancel := context.WithCancel(context.Background())
	cancel()
	location := domain.Location{EndpointID: contractEndpointID, Path: file}
	if _, err := implementation.Stat(canceled, providerapi.StatRequest{
		Location: location,
	}); !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled Stat() error = %v, want context.Canceled", err)
	}
	if calls := controller.Calls(); len(calls) != 0 {
		t.Fatalf("Calls() after invalid/canceled requests = %#v, want empty", calls)
	}

	_, err = implementation.Stat(context.Background(), providerapi.StatRequest{Location: location})
	requireCode(t, err, domain.CodeTimeout)
	if calls := controller.Calls(); len(calls) != 1 || calls[0].Sequence != 1 {
		t.Fatalf("Calls() after first valid request = %#v", calls)
	}
}

func TestPureOpenWriteValidationDoesNotRecordOrConsume(t *testing.T) {
	path := domain.CanonicalPath("/new-file")
	scenario := validScenario(t)
	scenario.Script = []FaultStep{{
		Match:  FaultMatch{Operation: OperationOpenWrite, Nth: 1, Path: &path},
		Effect: FaultEffect{Error: faultError(domain.CodeTimeout)},
	}}
	implementation, controller, err := New(scenario)
	if err != nil {
		t.Fatalf("New(): %v", err)
	}
	location := domain.Location{EndpointID: contractEndpointID, Path: path}

	if _, err := implementation.OpenWrite(context.Background(), providerapi.OpenWriteRequest{
		Location:    location,
		Offset:      1,
		Disposition: providerapi.WriteCreateNew,
	}); !domain.IsCode(err, domain.CodeInvalidArgument) {
		t.Fatalf("invalid OpenWrite() error = %v, want invalid_argument", err)
	}
	if calls := controller.Calls(); len(calls) != 0 {
		t.Fatalf("Calls() after invalid OpenWrite = %#v, want empty", calls)
	}

	_, err = implementation.OpenWrite(context.Background(), providerapi.OpenWriteRequest{
		Location:    location,
		Disposition: providerapi.WriteCreateNew,
	})
	requireCode(t, err, domain.CodeTimeout)
	if calls := controller.Calls(); len(calls) != 1 || calls[0].Sequence != 1 {
		t.Fatalf("Calls() after valid OpenWrite = %#v", calls)
	}
}

func TestNewOwnsFaultScriptInputs(t *testing.T) {
	path := domain.CanonicalPath("/contract-file")
	errorLocation := domain.Location{EndpointID: contractEndpointID, Path: "/script-location"}
	injected := faultError(domain.CodeTimeout)
	injected.Location = &errorLocation
	script := []FaultStep{{
		Match:  FaultMatch{Operation: OperationStat, Nth: 1, Path: &path},
		Effect: FaultEffect{Error: injected},
	}}
	scenario := validScenario(t)
	scenario.Script = script
	implementation, _, err := New(scenario)
	if err != nil {
		t.Fatalf("New(): %v", err)
	}

	path = "/contract-directory"
	script[0].Match.Nth = 99
	injected.Code = domain.CodeInternal
	errorLocation.Path = "/mutated"
	location := domain.Location{EndpointID: contractEndpointID, Path: "/contract-file"}
	_, err = implementation.Stat(context.Background(), providerapi.StatRequest{Location: location})
	opError := requireCode(t, err, domain.CodeTimeout)
	wantErrorLocation := domain.Location{EndpointID: contractEndpointID, Path: "/script-location"}
	if opError.Location == nil || *opError.Location != wantErrorLocation {
		t.Fatalf("injected Location = %#v, want %#v", opError.Location, wantErrorLocation)
	}
}

func TestCallsReturnsDeepCopy(t *testing.T) {
	implementation, controller, err := New(validScenario(t))
	if err != nil {
		t.Fatalf("New(): %v", err)
	}
	location := domain.Location{EndpointID: contractEndpointID, Path: "/contract-file"}
	if _, err := implementation.Stat(context.Background(), providerapi.StatRequest{
		Location: location,
	}); err != nil {
		t.Fatalf("Stat(): %v", err)
	}

	first := controller.Calls()
	if len(first) != 1 || first[0].Location == nil {
		t.Fatalf("first Calls() = %#v", first)
	}
	first[0].Operation = OperationRemove
	first[0].Location.Path = "/mutated"
	first = append(first, Call{Operation: OperationSnapshot, Sequence: 999})
	if len(first) != 2 || first[1].Sequence != 999 {
		t.Fatalf("mutated first Calls() = %#v", first)
	}

	second := controller.Calls()
	if len(second) != 1 || second[0].Operation != OperationStat ||
		second[0].Location == nil || *second[0].Location != location {
		t.Fatalf("second Calls() observed caller mutation: %#v", second)
	}
}

func TestFaultCallSequencesAreLinearized(t *testing.T) {
	implementation, controller, err := New(validScenario(t))
	if err != nil {
		t.Fatalf("New(): %v", err)
	}

	const workers = 64
	start := make(chan struct{})
	errorsByWorker := make(chan error, workers)
	var group sync.WaitGroup
	for index := 0; index < workers; index++ {
		group.Add(1)
		go func() {
			defer group.Done()
			<-start
			_, err := implementation.Snapshot(context.Background())
			errorsByWorker <- err
		}()
	}
	close(start)
	group.Wait()
	close(errorsByWorker)
	for err := range errorsByWorker {
		if err != nil {
			t.Fatalf("Snapshot(): %v", err)
		}
	}

	calls := controller.Calls()
	if len(calls) != workers {
		t.Fatalf("Calls() length = %d, want %d", len(calls), workers)
	}
	for index, call := range calls {
		if call.Operation != OperationSnapshot || call.Location != nil ||
			call.Sequence != uint64(index+1) {
			t.Fatalf("Calls()[%d] = %#v", index, call)
		}
	}
}

func TestCallsRecordEveryPublicOperationAtItsContractLocation(t *testing.T) {
	implementation, controller, err := New(validScenario(t))
	if err != nil {
		t.Fatalf("New(): %v", err)
	}
	_ = implementation.Descriptor()
	if _, err := implementation.Snapshot(context.Background()); err != nil {
		t.Fatalf("Snapshot(): %v", err)
	}
	root, err := implementation.Normalize(context.Background(), domain.NormalizeRequest{
		EndpointID: contractEndpointID,
		Input:      "/",
	})
	if err != nil {
		t.Fatalf("Normalize(): %v", err)
	}
	if _, err := implementation.List(context.Background(), providerapi.ListRequest{
		Location: root,
		Limit:    1,
	}); err != nil {
		t.Fatalf("List(): %v", err)
	}
	file := domain.Location{EndpointID: contractEndpointID, Path: "/contract-file"}
	if _, err := implementation.Stat(context.Background(), providerapi.StatRequest{
		Location: file,
	}); err != nil {
		t.Fatalf("Stat(): %v", err)
	}
	reader, err := implementation.OpenRead(context.Background(), providerapi.OpenReadRequest{
		Location: file,
	})
	if err != nil {
		t.Fatalf("OpenRead(): %v", err)
	}
	_ = reader.Info()
	if n, err := reader.Read(context.Background(), make([]byte, 1)); n != 1 || err != nil {
		t.Fatalf("Read() = (%d, %v), want (1, nil)", n, err)
	}
	if err := reader.Close(context.Background()); err != nil {
		t.Fatalf("Close(read): %v", err)
	}

	created := domain.Location{EndpointID: contractEndpointID, Path: "/recorded-write"}
	writer, err := implementation.OpenWrite(context.Background(), providerapi.OpenWriteRequest{
		Location:    created,
		Disposition: providerapi.WriteCreateNew,
	})
	if err != nil {
		t.Fatalf("OpenWrite(): %v", err)
	}
	if n, err := writer.Write(context.Background(), []byte("x")); n != 1 || err != nil {
		t.Fatalf("Write() = (%d, %v), want (1, nil)", n, err)
	}
	if err := writer.Sync(context.Background()); err != nil {
		t.Fatalf("Sync(): %v", err)
	}
	if err := writer.Close(context.Background()); err != nil {
		t.Fatalf("Close(write): %v", err)
	}
	directory := domain.Location{EndpointID: contractEndpointID, Path: "/recorded-directory"}
	if _, err := implementation.Mkdir(context.Background(), providerapi.MkdirRequest{
		Location: directory,
	}); err != nil {
		t.Fatalf("Mkdir(): %v", err)
	}
	renamed := domain.Location{EndpointID: contractEndpointID, Path: "/recorded-renamed"}
	if _, err := implementation.Rename(context.Background(), providerapi.RenameRequest{
		Source:      created,
		Destination: renamed,
	}); err != nil {
		t.Fatalf("Rename(): %v", err)
	}
	if err := implementation.Remove(context.Background(), providerapi.RemoveRequest{
		Location: renamed,
	}); err != nil {
		t.Fatalf("Remove(): %v", err)
	}

	wantOperations := []Operation{
		OperationSnapshot,
		OperationNormalize,
		OperationList,
		OperationStat,
		OperationOpenRead,
		OperationRead,
		OperationCloseRead,
		OperationOpenWrite,
		OperationWrite,
		OperationSyncWrite,
		OperationCloseWrite,
		OperationMkdir,
		OperationRename,
		OperationRemove,
	}
	wantLocations := []*domain.Location{
		nil,
		&root,
		&root,
		&file,
		&file,
		&file,
		&file,
		&created,
		&created,
		&created,
		&created,
		&directory,
		&created,
		&renamed,
	}
	calls := controller.Calls()
	if len(calls) != len(wantOperations) {
		t.Fatalf("Calls() length = %d, want %d: %#v", len(calls), len(wantOperations), calls)
	}
	for index, call := range calls {
		if call.Operation != wantOperations[index] || call.Sequence != uint64(index+1) {
			t.Fatalf("Calls()[%d] = %#v, want operation %q", index, call, wantOperations[index])
		}
		wantLocation := wantLocations[index]
		if wantLocation == nil {
			if call.Location != nil {
				t.Fatalf("Calls()[%d].Location = %#v, want nil", index, call.Location)
			}
			continue
		}
		if call.Location == nil || *call.Location != *wantLocation {
			t.Fatalf("Calls()[%d].Location = %#v, want %#v", index, call.Location, wantLocation)
		}
	}
}

func TestShortReadAndShortWriteReportProgress(t *testing.T) {
	t.Run("short read advances by the returned prefix", func(t *testing.T) {
		scenario := validScenario(t)
		scenario.Script = []FaultStep{{
			Match:  FaultMatch{Operation: OperationRead, Nth: 1},
			Effect: FaultEffect{MaxReadBytes: 3},
		}}
		implementation, _, err := New(scenario)
		if err != nil {
			t.Fatalf("New(): %v", err)
		}
		location := domain.Location{EndpointID: contractEndpointID, Path: "/contract-file"}
		handle, err := implementation.OpenRead(context.Background(), providerapi.OpenReadRequest{
			Location: location,
		})
		if err != nil {
			t.Fatalf("OpenRead(): %v", err)
		}

		buffer := make([]byte, 8)
		n, err := handle.Read(context.Background(), buffer)
		if n != 3 || err != nil || string(buffer[:n]) != "con" {
			t.Fatalf("first Read() = (%d, %v, %q), want (3, nil, %q)", n, err, buffer[:n], "con")
		}
		n, err = handle.Read(context.Background(), buffer[:3])
		if n != 3 || err != nil || string(buffer[:n]) != "tra" {
			t.Fatalf("second Read() = (%d, %v, %q), want (3, nil, %q)", n, err, buffer[:n], "tra")
		}
	})

	t.Run("short write advances by the returned prefix", func(t *testing.T) {
		scenario := validScenario(t)
		scenario.Root.Children = append(scenario.Root.Children, Node{
			Name: "short-write",
			Kind: domain.EntryFile,
		})
		scenario.Script = []FaultStep{{
			Match:  FaultMatch{Operation: OperationWrite, Nth: 1},
			Effect: FaultEffect{MaxWriteBytes: 3},
		}}
		implementation, _, err := New(scenario)
		if err != nil {
			t.Fatalf("New(): %v", err)
		}
		location := domain.Location{EndpointID: contractEndpointID, Path: "/short-write"}
		handle, err := implementation.OpenWrite(context.Background(), providerapi.OpenWriteRequest{
			Location:    location,
			Disposition: providerapi.WriteResumeExisting,
		})
		if err != nil {
			t.Fatalf("OpenWrite(): %v", err)
		}

		source := []byte("abcdef")
		n, err := handle.Write(context.Background(), source)
		if n != 3 || err != nil {
			t.Fatalf("first Write() = (%d, %v), want (3, nil)", n, err)
		}
		n, err = handle.Write(context.Background(), source[n:])
		if n != 3 || err != nil {
			t.Fatalf("second Write() = (%d, %v), want (3, nil)", n, err)
		}
		if got := readFile(t, implementation, location); !bytes.Equal(got, source) {
			t.Fatalf("file data = %q, want %q", got, source)
		}
	})
}

func TestCombinedErrorAndShortIOReportsAppliedPrefix(t *testing.T) {
	readCause := errors.New("read interrupted")
	writeCause := errors.New("write interrupted")
	scenario := validScenario(t)
	scenario.Root.Children = append(scenario.Root.Children, Node{
		Name: "combined-write",
		Kind: domain.EntryFile,
	})
	scenario.Script = []FaultStep{
		{
			Match: FaultMatch{Operation: OperationRead, Nth: 1},
			Effect: FaultEffect{
				MaxReadBytes: 3,
				Error: &domain.OpError{
					Code:    domain.CodeTimeout,
					Message: "read interrupted",
					Retry:   domain.RetryAdvice{Kind: domain.RetryBackoff},
					Effect:  domain.EffectUnknown,
					Cause:   readCause,
				},
			},
		},
		{
			Match: FaultMatch{Operation: OperationWrite, Nth: 1},
			Effect: FaultEffect{
				MaxWriteBytes: 3,
				Error: &domain.OpError{
					Code:    domain.CodeResourceExhausted,
					Message: "write interrupted",
					Retry:   domain.RetryAdvice{Kind: domain.RetryBackoff},
					Effect:  domain.EffectNone,
					Cause:   writeCause,
				},
			},
		},
	}
	implementation, _, err := New(scenario)
	if err != nil {
		t.Fatalf("New(): %v", err)
	}

	readLocation := domain.Location{EndpointID: contractEndpointID, Path: "/contract-file"}
	readHandle, err := implementation.OpenRead(context.Background(), providerapi.OpenReadRequest{
		Location: readLocation,
	})
	if err != nil {
		t.Fatalf("OpenRead(): %v", err)
	}
	buffer := make([]byte, 8)
	n, err := readHandle.Read(context.Background(), buffer)
	readError := requireCode(t, err, domain.CodeTimeout)
	if n != 3 || string(buffer[:n]) != "con" {
		t.Fatalf("Read() = (%d, %q), want (3, %q)", n, buffer[:n], "con")
	}
	if readError.Effect != domain.EffectNone || readError.Operation != "read" ||
		readError.EndpointID != contractEndpointID || readError.Location == nil ||
		*readError.Location != readLocation || !errors.Is(readError, readCause) {
		t.Fatalf("Read() error = %#v, want enriched EffectNone preserving cause", readError)
	}
	n, err = readHandle.Read(context.Background(), buffer[:3])
	if n != 3 || err != nil || string(buffer[:n]) != "tra" {
		t.Fatalf("second Read() = (%d, %v, %q), want (3, nil, %q)", n, err, buffer[:n], "tra")
	}

	writeLocation := domain.Location{EndpointID: contractEndpointID, Path: "/combined-write"}
	writeHandle, err := implementation.OpenWrite(context.Background(), providerapi.OpenWriteRequest{
		Location:    writeLocation,
		Disposition: providerapi.WriteResumeExisting,
	})
	if err != nil {
		t.Fatalf("OpenWrite(): %v", err)
	}
	source := []byte("abcdef")
	n, err = writeHandle.Write(context.Background(), source)
	writeError := requireCode(t, err, domain.CodeResourceExhausted)
	if n != 3 {
		t.Fatalf("Write() n = %d, want 3", n)
	}
	if writeError.Effect != domain.EffectApplied || writeError.Operation != "write" ||
		writeError.EndpointID != contractEndpointID || writeError.Location == nil ||
		*writeError.Location != writeLocation || !errors.Is(writeError, writeCause) {
		t.Fatalf("Write() error = %#v, want enriched EffectApplied preserving cause", writeError)
	}
	if got := readFile(t, implementation, writeLocation); string(got) != "abc" {
		t.Fatalf("file data after combined error = %q, want %q", got, "abc")
	}
	n, err = writeHandle.Write(context.Background(), source[n:])
	if n != 3 || err != nil {
		t.Fatalf("second Write() = (%d, %v), want (3, nil)", n, err)
	}
	if got := readFile(t, implementation, writeLocation); !bytes.Equal(got, source) {
		t.Fatalf("final file data = %q, want %q", got, source)
	}
}

func TestShortIOReportsOnlyActualAvailableProgress(t *testing.T) {
	t.Run("short-only preserves zero-length and EOF semantics", func(t *testing.T) {
		scenario := validScenario(t)
		scenario.Script = []FaultStep{
			{
				Match:  FaultMatch{Operation: OperationRead, Nth: 1},
				Effect: FaultEffect{MaxReadBytes: 3},
			},
			{
				Match:  FaultMatch{Operation: OperationRead, Nth: 3},
				Effect: FaultEffect{MaxReadBytes: 3},
			},
		}
		implementation, _, err := New(scenario)
		if err != nil {
			t.Fatalf("New(): %v", err)
		}
		location := domain.Location{EndpointID: contractEndpointID, Path: "/contract-file"}
		handle, err := implementation.OpenRead(context.Background(), providerapi.OpenReadRequest{Location: location})
		if err != nil {
			t.Fatalf("OpenRead(): %v", err)
		}
		if n, err := handle.Read(context.Background(), nil); n != 0 || err != nil {
			t.Fatalf("short-only zero-length Read() = (%d, %v), want (0, nil)", n, err)
		}
		buffer := make([]byte, 64)
		if n, err := handle.Read(context.Background(), buffer); n != len("contract data") || err != nil {
			t.Fatalf("Read(after zero-length) = (%d, %v), want full file", n, err)
		}
		if n, err := handle.Read(context.Background(), buffer); n != 0 || !errors.Is(err, io.EOF) {
			t.Fatalf("short-only EOF Read() = (%d, %v), want (0, EOF)", n, err)
		}
	})

	t.Run("read limit larger than available bytes", func(t *testing.T) {
		scenario := validScenario(t)
		scenario.Script = []FaultStep{{
			Match:  FaultMatch{Operation: OperationRead, Nth: 1},
			Effect: FaultEffect{MaxReadBytes: 100},
		}}
		implementation, _, err := New(scenario)
		if err != nil {
			t.Fatalf("New(): %v", err)
		}
		location := domain.Location{EndpointID: contractEndpointID, Path: "/contract-file"}
		handle, err := implementation.OpenRead(context.Background(), providerapi.OpenReadRequest{Location: location})
		if err != nil {
			t.Fatalf("OpenRead(): %v", err)
		}
		buffer := make([]byte, 64)
		n, err := handle.Read(context.Background(), buffer)
		if n != len("contract data") || err != nil || string(buffer[:n]) != "contract data" {
			t.Fatalf("Read() = (%d, %v, %q), want actual available bytes", n, err, buffer[:n])
		}
	})

	t.Run("zero length read does not invent progress or advance offset", func(t *testing.T) {
		scenario := validScenario(t)
		scenario.Script = []FaultStep{{
			Match: FaultMatch{Operation: OperationRead, Nth: 1},
			Effect: FaultEffect{
				MaxReadBytes: 3,
				Error:        faultError(domain.CodeTimeout),
			},
		}}
		implementation, _, err := New(scenario)
		if err != nil {
			t.Fatalf("New(): %v", err)
		}
		location := domain.Location{EndpointID: contractEndpointID, Path: "/contract-file"}
		handle, err := implementation.OpenRead(context.Background(), providerapi.OpenReadRequest{Location: location})
		if err != nil {
			t.Fatalf("OpenRead(): %v", err)
		}
		n, err := handle.Read(context.Background(), nil)
		opError := requireCode(t, err, domain.CodeTimeout)
		if n != 0 || opError.Effect != domain.EffectNone {
			t.Fatalf("zero-length Read() = (%d, %#v), want zero progress EffectNone", n, opError)
		}
		buffer := make([]byte, 3)
		n, err = handle.Read(context.Background(), buffer)
		if n != 3 || err != nil || string(buffer[:n]) != "con" {
			t.Fatalf("next Read() = (%d, %v, %q), want unchanged offset", n, err, buffer[:n])
		}
	})

	t.Run("read at EOF does not invent progress", func(t *testing.T) {
		scenario := validScenario(t)
		scenario.Script = []FaultStep{{
			Match: FaultMatch{Operation: OperationRead, Nth: 2},
			Effect: FaultEffect{
				MaxReadBytes: 3,
				Error:        faultError(domain.CodeTimeout),
			},
		}}
		implementation, _, err := New(scenario)
		if err != nil {
			t.Fatalf("New(): %v", err)
		}
		location := domain.Location{EndpointID: contractEndpointID, Path: "/contract-file"}
		handle, err := implementation.OpenRead(context.Background(), providerapi.OpenReadRequest{Location: location})
		if err != nil {
			t.Fatalf("OpenRead(): %v", err)
		}
		buffer := make([]byte, 64)
		if n, err := handle.Read(context.Background(), buffer); n != len("contract data") || err != nil {
			t.Fatalf("initial Read() = (%d, %v), want full file", n, err)
		}
		n, err := handle.Read(context.Background(), buffer)
		opError := requireCode(t, err, domain.CodeTimeout)
		if n != 0 || opError.Effect != domain.EffectNone {
			t.Fatalf("EOF combined Read() = (%d, %#v), want zero progress EffectNone", n, opError)
		}
		if n, err := handle.Read(context.Background(), buffer); n != 0 || !errors.Is(err, io.EOF) {
			t.Fatalf("next Read() = (%d, %v), want (0, EOF)", n, err)
		}
	})

	t.Run("write limit larger than source and zero source", func(t *testing.T) {
		scenario := validScenario(t)
		scenario.Root.Children = append(scenario.Root.Children, Node{
			Name: "actual-write",
			Kind: domain.EntryFile,
		})
		scenario.Script = []FaultStep{
			{
				Match: FaultMatch{Operation: OperationWrite, Nth: 1},
				Effect: FaultEffect{
					MaxWriteBytes: 3,
					Error:         faultError(domain.CodeResourceExhausted),
				},
			},
			{
				Match:  FaultMatch{Operation: OperationWrite, Nth: 2},
				Effect: FaultEffect{MaxWriteBytes: 100},
			},
		}
		implementation, _, err := New(scenario)
		if err != nil {
			t.Fatalf("New(): %v", err)
		}
		location := domain.Location{EndpointID: contractEndpointID, Path: "/actual-write"}
		handle, err := implementation.OpenWrite(context.Background(), providerapi.OpenWriteRequest{
			Location:    location,
			Disposition: providerapi.WriteResumeExisting,
		})
		if err != nil {
			t.Fatalf("OpenWrite(): %v", err)
		}
		n, err := handle.Write(context.Background(), nil)
		opError := requireCode(t, err, domain.CodeResourceExhausted)
		if n != 0 || opError.Effect != domain.EffectNone {
			t.Fatalf("zero-length Write() = (%d, %#v), want zero progress EffectNone", n, opError)
		}
		n, err = handle.Write(context.Background(), []byte("xy"))
		if n != 2 || err != nil {
			t.Fatalf("short Write() = (%d, %v), want actual source length", n, err)
		}
		if got := readFile(t, implementation, location); string(got) != "xy" {
			t.Fatalf("file data = %q, want %q", got, "xy")
		}
	})
}

func TestDiskFullErrorIsTypedAndDoesNotApply(t *testing.T) {
	cause := errors.New("filesystem reports no free blocks")
	injected := &domain.OpError{
		Code:    domain.CodeResourceExhausted,
		Message: "disk full",
		Retry: domain.RetryAdvice{
			Kind:  domain.RetryBackoff,
			After: 3 * time.Second,
		},
		Effect: domain.EffectNone,
		Cause:  cause,
	}
	scenario := validScenario(t)
	scenario.Script = []FaultStep{{
		Match:  FaultMatch{Operation: OperationWrite, Nth: 1},
		Effect: FaultEffect{Error: injected},
	}}
	implementation, controller, err := New(scenario)
	if err != nil {
		t.Fatalf("New(): %v", err)
	}
	location := domain.Location{EndpointID: contractEndpointID, Path: "/contract-file"}
	root := domain.Location{EndpointID: contractEndpointID, Path: "/"}
	handle, err := implementation.OpenWrite(context.Background(), providerapi.OpenWriteRequest{
		Location:    location,
		Offset:      2,
		Disposition: providerapi.WriteResumeExisting,
	})
	if err != nil {
		t.Fatalf("OpenWrite(): %v", err)
	}
	beforeData := readFile(t, implementation, location)
	beforeEntry := statEntry(t, implementation, location)
	beforeSnapshot, err := implementation.Snapshot(context.Background())
	if err != nil {
		t.Fatalf("Snapshot(before): %v", err)
	}
	beforePage, err := implementation.List(context.Background(), providerapi.ListRequest{
		Location: root,
		Limit:    1,
	})
	if err != nil {
		t.Fatalf("List(before): %v", err)
	}
	if beforePage.Done || beforePage.NextCursor == "" {
		t.Fatalf("List(before) = %#v, want continuation cursor", beforePage)
	}

	n, err := handle.Write(context.Background(), []byte("ZZ"))
	opError := requireCode(t, err, domain.CodeResourceExhausted)
	if n != 0 {
		t.Fatalf("Write() n = %d, want 0", n)
	}
	if opError.Retry != injected.Retry || opError.Effect != domain.EffectNone ||
		opError.Operation != "write" || opError.EndpointID != contractEndpointID ||
		opError.Location == nil || *opError.Location != location || !errors.Is(opError, cause) {
		t.Fatalf("Write() error = %#v, want enriched injected disk-full error", opError)
	}
	if got := readFile(t, implementation, location); !bytes.Equal(got, beforeData) {
		t.Fatalf("data after disk-full error = %q, want %q", got, beforeData)
	}
	afterEntry := statEntry(t, implementation, location)
	if !reflect.DeepEqual(afterEntry.Fingerprint, beforeEntry.Fingerprint) {
		t.Fatalf("fingerprint changed after disk-full error: before=%#v after=%#v", beforeEntry.Fingerprint, afterEntry.Fingerprint)
	}
	afterSnapshot, err := implementation.Snapshot(context.Background())
	if err != nil {
		t.Fatalf("Snapshot(after): %v", err)
	}
	if afterSnapshot.Capabilities.Revision != beforeSnapshot.Capabilities.Revision {
		t.Fatalf("capability revision changed: before=%#v after=%#v", beforeSnapshot.Capabilities.Revision, afterSnapshot.Capabilities.Revision)
	}
	afterPage, err := implementation.List(context.Background(), providerapi.ListRequest{
		Location: root,
		Limit:    1,
		Cursor:   beforePage.NextCursor,
	})
	if err != nil {
		t.Fatalf("List(after, prior cursor): %v", err)
	}
	if !reflect.DeepEqual(afterPage.DirectoryFingerprint, beforePage.DirectoryFingerprint) {
		t.Fatalf("directory fingerprint changed: before=%#v after=%#v", beforePage.DirectoryFingerprint, afterPage.DirectoryFingerprint)
	}
	writeCalls := 0
	for _, call := range controller.Calls() {
		if call.Operation == OperationWrite {
			writeCalls++
		}
	}
	if writeCalls != 1 {
		t.Fatalf("recorded Write calls = %d, want 1", writeCalls)
	}

	if n, err := handle.Write(context.Background(), []byte("X")); n != 1 || err != nil {
		t.Fatalf("Write(after consumed fault) = (%d, %v), want (1, nil)", n, err)
	}
	wantData := append([]byte(nil), beforeData...)
	wantData[2] = 'X'
	if got := readFile(t, implementation, location); !bytes.Equal(got, wantData) {
		t.Fatalf("data after retry = %q, want %q (offset must remain unchanged)", got, wantData)
	}
}

func TestCancellationAfterFaultSelectionPreventsCommit(t *testing.T) {
	clock := newCommitBlockingClock(time.Date(2026, time.July, 14, 12, 0, 0, 0, time.UTC))
	scenario := validScenario(t)
	scenario.Clock = clock
	scenario.Root.Children = append(scenario.Root.Children, Node{
		Name: "cancel-write",
		Kind: domain.EntryFile,
	})
	scenario.Script = []FaultStep{{
		Match: FaultMatch{Operation: OperationWrite, Nth: 1},
		Effect: FaultEffect{
			WaitGate:      "before-commit",
			MaxWriteBytes: 3,
			Error:         faultError(domain.CodeResourceExhausted),
		},
	}}
	implementation, controller, err := New(scenario)
	if err != nil {
		t.Fatalf("New(): %v", err)
	}
	controller.ReleaseGate("before-commit")
	location := domain.Location{EndpointID: contractEndpointID, Path: "/cancel-write"}
	handle, err := implementation.OpenWrite(context.Background(), providerapi.OpenWriteRequest{
		Location:    location,
		Disposition: providerapi.WriteResumeExisting,
	})
	if err != nil {
		t.Fatalf("OpenWrite(): %v", err)
	}
	before := statEntry(t, implementation, location)
	ctx, cancel := context.WithCancel(context.Background())
	type writeResult struct {
		n   int
		err error
	}
	result := make(chan writeResult, 1)
	go func() {
		n, err := handle.Write(ctx, []byte("abcdef"))
		result <- writeResult{n: n, err: err}
	}()
	select {
	case <-clock.entered:
	case got := <-result:
		t.Fatalf("Write() returned before the commit boundary: (%d, %v)", got.n, got.err)
	}
	cancel()
	close(clock.release)
	got := <-result
	if got.n != 0 || !errors.Is(got.err, context.Canceled) {
		t.Fatalf("canceled Write() = (%d, %v), want (0, context.Canceled)", got.n, got.err)
	}
	if data := readFile(t, implementation, location); len(data) != 0 {
		t.Fatalf("data after canceled Write() = %q, want empty", data)
	}
	after := statEntry(t, implementation, location)
	if !reflect.DeepEqual(after.Fingerprint, before.Fingerprint) {
		t.Fatalf("fingerprint changed after canceled Write(): before=%#v after=%#v", before.Fingerprint, after.Fingerprint)
	}
	writeCalls := 0
	for _, call := range controller.Calls() {
		if call.Operation == OperationWrite {
			writeCalls++
		}
	}
	if writeCalls != 1 {
		t.Fatalf("recorded Write calls = %d, want 1", writeCalls)
	}
	if n, err := handle.Write(context.Background(), []byte("abcdef")); n != 6 || err != nil {
		t.Fatalf("Write(after consumed fault) = (%d, %v), want (6, nil)", n, err)
	}
	if data := readFile(t, implementation, location); string(data) != "abcdef" {
		t.Fatalf("data after retry = %q, want %q", data, "abcdef")
	}
}

func TestClosedHandlePrecedenceForCombinedAndStandaloneErrors(t *testing.T) {
	t.Run("read", func(t *testing.T) {
		scenario := validScenario(t)
		scenario.Script = []FaultStep{
			{
				Match: FaultMatch{Operation: OperationRead, Nth: 1},
				Effect: FaultEffect{
					MaxReadBytes: 1,
					Error:        faultError(domain.CodeResourceExhausted),
				},
			},
			{
				Match:  FaultMatch{Operation: OperationRead, Nth: 2},
				Effect: FaultEffect{Error: faultError(domain.CodeTimeout)},
			},
		}
		implementation, _, err := New(scenario)
		if err != nil {
			t.Fatalf("New(): %v", err)
		}
		location := domain.Location{EndpointID: contractEndpointID, Path: "/contract-file"}
		handle, err := implementation.OpenRead(context.Background(), providerapi.OpenReadRequest{Location: location})
		if err != nil {
			t.Fatalf("OpenRead(): %v", err)
		}
		if err := handle.Close(context.Background()); err != nil {
			t.Fatalf("Close(): %v", err)
		}
		if n, err := handle.Read(context.Background(), make([]byte, 4)); n != 0 || !domain.IsCode(err, domain.CodeInvalidArgument) {
			t.Fatalf("combined fault on closed Read() = (%d, %v), want closed invalid_argument", n, err)
		}
		if n, err := handle.Read(context.Background(), make([]byte, 4)); n != 0 || !domain.IsCode(err, domain.CodeTimeout) {
			t.Fatalf("standalone fault on closed Read() = (%d, %v), want injected timeout", n, err)
		}
	})

	t.Run("write", func(t *testing.T) {
		scenario := validScenario(t)
		scenario.Script = []FaultStep{
			{
				Match: FaultMatch{Operation: OperationWrite, Nth: 1},
				Effect: FaultEffect{
					MaxWriteBytes: 1,
					Error:         faultError(domain.CodeResourceExhausted),
				},
			},
			{
				Match:  FaultMatch{Operation: OperationWrite, Nth: 2},
				Effect: FaultEffect{Error: faultError(domain.CodeTimeout)},
			},
		}
		implementation, _, err := New(scenario)
		if err != nil {
			t.Fatalf("New(): %v", err)
		}
		location := domain.Location{EndpointID: contractEndpointID, Path: "/contract-file"}
		before := readFile(t, implementation, location)
		handle, err := implementation.OpenWrite(context.Background(), providerapi.OpenWriteRequest{
			Location:    location,
			Disposition: providerapi.WriteResumeExisting,
		})
		if err != nil {
			t.Fatalf("OpenWrite(): %v", err)
		}
		if err := handle.Close(context.Background()); err != nil {
			t.Fatalf("Close(): %v", err)
		}
		if n, err := handle.Write(context.Background(), []byte("changed")); n != 0 || !domain.IsCode(err, domain.CodeInvalidArgument) {
			t.Fatalf("combined fault on closed Write() = (%d, %v), want closed invalid_argument", n, err)
		}
		if n, err := handle.Write(context.Background(), []byte("changed")); n != 0 || !domain.IsCode(err, domain.CodeTimeout) {
			t.Fatalf("standalone fault on closed Write() = (%d, %v), want injected timeout", n, err)
		}
		if got := readFile(t, implementation, location); !bytes.Equal(got, before) {
			t.Fatalf("closed writes changed data: got %q want %q", got, before)
		}
	})
}

func TestRenameCanReportNonAtomic(t *testing.T) {
	t.Run("successful mutation reports non-atomic", func(t *testing.T) {
		scenario := validScenario(t)
		scenario.Script = []FaultStep{{
			Match:  FaultMatch{Operation: OperationRename, Nth: 1},
			Effect: FaultEffect{NonAtomicRename: true},
		}}
		implementation, _, err := New(scenario)
		if err != nil {
			t.Fatalf("New(): %v", err)
		}
		source := domain.Location{EndpointID: contractEndpointID, Path: "/contract-file"}
		destination := domain.Location{EndpointID: contractEndpointID, Path: "/moved-file"}
		result, err := implementation.Rename(context.Background(), providerapi.RenameRequest{
			Source:      source,
			Destination: destination,
		})
		if err != nil {
			t.Fatalf("Rename(): %v", err)
		}
		if result.Atomic || result.Replaced {
			t.Fatalf("Rename() result = %#v, want non-atomic non-replacement", result)
		}
		requireStatCode(t, implementation, source, domain.CodeNotFound)
		if got := readFile(t, implementation, destination); string(got) != "contract data" {
			t.Fatalf("destination data = %q, want %q", got, "contract data")
		}
	})

	t.Run("failed rename does not mutate", func(t *testing.T) {
		scenario := validScenario(t)
		scenario.Script = []FaultStep{{
			Match:  FaultMatch{Operation: OperationRename, Nth: 1},
			Effect: FaultEffect{NonAtomicRename: true},
		}}
		implementation, _, err := New(scenario)
		if err != nil {
			t.Fatalf("New(): %v", err)
		}
		source := domain.Location{EndpointID: contractEndpointID, Path: "/contract-file"}
		destination := domain.Location{EndpointID: contractEndpointID, Path: "/contract-directory/nested-file"}
		beforeSource := statEntry(t, implementation, source)
		beforeDestination := statEntry(t, implementation, destination)
		_, err = implementation.Rename(context.Background(), providerapi.RenameRequest{
			Source:      source,
			Destination: destination,
		})
		requireCode(t, err, domain.CodeAlreadyExists)
		afterSource := statEntry(t, implementation, source)
		afterDestination := statEntry(t, implementation, destination)
		if !reflect.DeepEqual(afterSource.Fingerprint, beforeSource.Fingerprint) ||
			!reflect.DeepEqual(afterDestination.Fingerprint, beforeDestination.Fingerprint) {
			t.Fatalf("failed Rename() changed fingerprints: source=%#v destination=%#v", afterSource, afterDestination)
		}
	})

	t.Run("same-path no-op preserves atomic result", func(t *testing.T) {
		scenario := validScenario(t)
		scenario.Script = []FaultStep{{
			Match:  FaultMatch{Operation: OperationRename, Nth: 1},
			Effect: FaultEffect{NonAtomicRename: true},
		}}
		implementation, _, err := New(scenario)
		if err != nil {
			t.Fatalf("New(): %v", err)
		}
		location := domain.Location{EndpointID: contractEndpointID, Path: "/contract-file"}
		before := statEntry(t, implementation, location)
		result, err := implementation.Rename(context.Background(), providerapi.RenameRequest{
			Source:      location,
			Destination: location,
		})
		if err != nil {
			t.Fatalf("Rename(same path): %v", err)
		}
		if !result.Atomic || result.Replaced {
			t.Fatalf("Rename(same path) result = %#v, want unchanged atomic no-op", result)
		}
		after := statEntry(t, implementation, location)
		if !reflect.DeepEqual(after.Fingerprint, before.Fingerprint) {
			t.Fatalf("same-path Rename() changed fingerprint: before=%#v after=%#v", before.Fingerprint, after.Fingerprint)
		}
	})
}

func TestOperationConstantsCoverFaultScriptSurface(t *testing.T) {
	operations := []Operation{
		OperationSnapshot,
		OperationNormalize,
		OperationList,
		OperationStat,
		OperationOpenRead,
		OperationRead,
		OperationCloseRead,
		OperationOpenWrite,
		OperationWrite,
		OperationSyncWrite,
		OperationCloseWrite,
		OperationMkdir,
		OperationRename,
		OperationRemove,
	}
	want := []Operation{
		"snapshot",
		"normalize",
		"list",
		"stat",
		"open_read",
		"read",
		"close_read",
		"open_write",
		"write",
		"sync_write",
		"close_write",
		"mkdir",
		"rename",
		"remove",
	}
	if !reflect.DeepEqual(operations, want) {
		t.Fatalf("operation constants = %#v, want %#v", operations, want)
	}
}

func TestReplaceNodeRejectsInvalidInputAtomically(t *testing.T) {
	replacement := Node{Name: "contract-file", Kind: domain.EntryFile}
	for name, controller := range map[string]*Controller{
		"nil":      nil,
		"detached": {},
	} {
		t.Run(name+" controller", func(t *testing.T) {
			opError := requireCode(
				t,
				controller.ReplaceNode("/contract-file", replacement),
				domain.CodeInternal,
			)
			if opError.Operation != "replace_node" || opError.EndpointID != "" ||
				opError.Location != nil || opError.Retry.Kind != domain.RetryNever ||
				opError.Effect != domain.EffectNone {
				t.Fatalf("detached ReplaceNode() error = %#v", opError)
			}
		})
	}

	cycle := make([]Node, 1)
	cycle[0] = Node{Name: "cycle", Kind: domain.EntryDirectory, Children: cycle}
	wrongRootSize := uint64(99)
	wrongDeepSize := uint64(2)

	tests := []struct {
		name        string
		path        domain.CanonicalPath
		replacement Node
		code        domain.Code
		prepare     func(*Provider)
	}{
		{name: "empty path", path: "", replacement: replacement, code: domain.CodeInvalidArgument},
		{name: "relative path", path: "contract-file", replacement: replacement, code: domain.CodeInvalidArgument},
		{
			name:        "noncanonical path",
			path:        "/contract-directory/../contract-file",
			replacement: replacement,
			code:        domain.CodeInvalidArgument,
		},
		{name: "NUL path", path: "/contract-file\x00", replacement: replacement, code: domain.CodeInvalidArgument},
		{
			name:        "root path",
			path:        "/",
			replacement: Node{Name: "/", Kind: domain.EntryDirectory},
			code:        domain.CodeInvalidArgument,
		},
		{
			name:        "root name mismatch",
			path:        "/contract-file",
			replacement: Node{Name: "other", Kind: domain.EntryFile},
			code:        domain.CodeInvalidArgument,
		},
		{
			name:        "lexical final kind mismatch",
			path:        "/contract-link",
			replacement: Node{Name: "contract-link", Kind: domain.EntryFile},
			code:        domain.CodeInvalidArgument,
		},
		{
			name: "missing target",
			path: "/missing",
			replacement: Node{
				Name: "missing", Kind: domain.EntryDirectory,
				Children: []Node{{Name: "valid-child", Kind: domain.EntryFile}},
			},
			code: domain.CodeNotFound,
		},
		{
			name:        "intermediate non-directory",
			path:        "/contract-file/child",
			replacement: Node{Name: "child", Kind: domain.EntryFile},
			code:        domain.CodeNotFound,
		},
		{
			name:        "intermediate symlink loop",
			path:        "/replace-loop-a/child",
			replacement: Node{Name: "child", Kind: domain.EntryFile},
			code:        domain.CodeConflict,
		},
		{
			name:        "intermediate symlink escape",
			path:        "/replace-escape/child",
			replacement: Node{Name: "child", Kind: domain.EntryFile},
			code:        domain.CodeInvalidArgument,
		},
		{
			name:        "cyclic replacement",
			path:        "/contract-directory",
			replacement: Node{Name: "contract-directory", Kind: domain.EntryDirectory, Children: cycle},
			code:        domain.CodeInvalidArgument,
		},
		{
			name: "root metadata size mismatch",
			path: "/contract-file",
			replacement: Node{
				Name: "contract-file", Kind: domain.EntryFile, Data: []byte("x"),
				Metadata: domain.Metadata{Size: &wrongRootSize},
			},
			code: domain.CodeInvalidArgument,
		},
		{
			name: "deep metadata size mismatch",
			path: "/contract-directory",
			replacement: Node{
				Name: "contract-directory", Kind: domain.EntryDirectory,
				Children: []Node{{
					Name: "child", Kind: domain.EntryFile, Data: []byte("x"),
					Metadata: domain.Metadata{Size: &wrongDeepSize},
				}},
			},
			code: domain.CodeInvalidArgument,
		},
		{
			name:        "invalid kind",
			path:        "/contract-file",
			replacement: Node{Name: "contract-file", Kind: domain.EntryKind("invalid")},
			code:        domain.CodeInvalidArgument,
		},
		{
			name: "non-directory children",
			path: "/contract-file",
			replacement: Node{
				Name: "contract-file", Kind: domain.EntryFile,
				Children: []Node{{Name: "child", Kind: domain.EntryFile}},
			},
			code: domain.CodeInvalidArgument,
		},
		{
			name:        "empty symlink target",
			path:        "/contract-link",
			replacement: Node{Name: "contract-link", Kind: domain.EntrySymlink},
			code:        domain.CodeInvalidArgument,
		},
		{
			name: "NUL symlink target",
			path: "/contract-link",
			replacement: Node{
				Name: "contract-link", Kind: domain.EntrySymlink, LinkTarget: "bad\x00target",
			},
			code: domain.CodeInvalidArgument,
		},
		{
			name: "duplicate child names",
			path: "/contract-directory",
			replacement: Node{
				Name: "contract-directory", Kind: domain.EntryDirectory,
				Children: []Node{
					{Name: "duplicate", Kind: domain.EntryFile},
					{Name: "duplicate", Kind: domain.EntryFile},
				},
			},
			code: domain.CodeInvalidArgument,
		},
		{
			name: "valid descendants do not consume IDs before kind check",
			path: "/contract-file",
			replacement: Node{
				Name: "contract-file", Kind: domain.EntryDirectory,
				Children: []Node{{Name: "valid-child", Kind: domain.EntryFile}},
			},
			code: domain.CodeInvalidArgument,
		},
		{
			name: "descendant ID exhaustion",
			path: "/contract-directory",
			replacement: Node{
				Name: "contract-directory", Kind: domain.EntryDirectory,
				Children: []Node{{Name: "valid-child", Kind: domain.EntryFile}},
			},
			code: domain.CodeResourceExhausted,
			prepare: func(implementation *Provider) {
				implementation.nextNodeID = ^uint64(0)
			},
		},
	}
	for _, invalidName := range []string{"", ".", "..", "slash/name", "nul\x00name"} {
		tests = append(tests, struct {
			name        string
			path        domain.CanonicalPath
			replacement Node
			code        domain.Code
			prepare     func(*Provider)
		}{
			name: "invalid child name " + fmt.Sprintf("%q", invalidName),
			path: "/contract-directory",
			replacement: Node{
				Name: "contract-directory", Kind: domain.EntryDirectory,
				Children: []Node{{Name: invalidName, Kind: domain.EntryFile}},
			},
			code: domain.CodeInvalidArgument,
		})
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			scenario := validScenario(t)
			scenario.Root.Children = append(scenario.Root.Children,
				Node{Name: "replace-loop-a", Kind: domain.EntrySymlink, LinkTarget: "/replace-loop-b"},
				Node{Name: "replace-loop-b", Kind: domain.EntrySymlink, LinkTarget: "/replace-loop-a"},
				Node{Name: "replace-escape", Kind: domain.EntrySymlink, LinkTarget: "../../escape"},
			)
			implementation, controller, err := New(scenario)
			if err != nil {
				t.Fatalf("New(): %v", err)
			}

			rootLocation := domain.Location{EndpointID: contractEndpointID, Path: "/"}
			first, err := implementation.List(context.Background(), providerapi.ListRequest{
				Location: rootLocation,
				Limit:    1,
			})
			if err != nil || first.Done || first.NextCursor == "" {
				t.Fatalf("List(first) = (%#v, %v), want continuation", first, err)
			}
			read, err := implementation.OpenRead(context.Background(), providerapi.OpenReadRequest{
				Location: domain.Location{EndpointID: contractEndpointID, Path: "/contract-file"},
			})
			if err != nil {
				t.Fatalf("OpenRead(): %v", err)
			}
			write, err := implementation.OpenWrite(context.Background(), providerapi.OpenWriteRequest{
				Location:    domain.Location{EndpointID: contractEndpointID, Path: "/contract-file"},
				Offset:      2,
				Disposition: providerapi.WriteResumeExisting,
			})
			if err != nil {
				t.Fatalf("OpenWrite(): %v", err)
			}
			readHandle := read.(*readHandle)
			writeHandle := write.(*writeHandle)
			implementation.mu.Lock()
			file, resolveErr := resolveNode(implementation.root, "/contract-file", false)
			if resolveErr != nil {
				implementation.mu.Unlock()
				t.Fatalf("resolve contract file: %v", resolveErr)
			}
			file.permissionDenied = true
			if test.prepare != nil {
				test.prepare(implementation)
			}
			implementation.mu.Unlock()

			before := captureReplaceFixtureState(implementation, controller, readHandle, writeHandle)
			err = controller.ReplaceNode(test.path, test.replacement)
			requireCode(t, err, test.code)
			after := captureReplaceFixtureState(implementation, controller, readHandle, writeHandle)
			if !reflect.DeepEqual(after, before) {
				t.Fatalf("failed ReplaceNode() changed fixture:\nbefore=%#v\nafter=%#v", before, after)
			}

			if _, err := implementation.List(context.Background(), providerapi.ListRequest{
				Location: rootLocation,
				Cursor:   first.NextCursor,
				Limit:    1,
			}); err != nil {
				t.Fatalf("prior cursor after failed ReplaceNode(): %v", err)
			}
		})
	}
}

func TestReplaceNodeAllocatesNearIDExhaustionWithoutWrap(t *testing.T) {
	scenario := validScenario(t)
	implementation, controller, err := New(scenario)
	if err != nil {
		t.Fatalf("New(): %v", err)
	}
	implementation.mu.Lock()
	implementation.nextNodeID = ^uint64(0) - 1
	target, _ := resolveNode(implementation.root, "/contract-directory", false)
	targetPointer, targetID := target, target.id
	implementation.mu.Unlock()

	if err := controller.ReplaceNode("/contract-directory", Node{
		Name: "contract-directory", Kind: domain.EntryDirectory,
		Children: []Node{{Name: "last-representable-child", Kind: domain.EntryFile}},
	}); err != nil {
		t.Fatalf("ReplaceNode(): %v", err)
	}
	implementation.mu.RLock()
	child, resolveErr := resolveNode(
		implementation.root,
		"/contract-directory/last-representable-child",
		false,
	)
	if resolveErr != nil || child.id != ^uint64(0)-1 || implementation.nextNodeID != ^uint64(0) ||
		target != targetPointer || target.id != targetID {
		implementation.mu.RUnlock()
		t.Fatalf("near-exhaustion allocation: child=%#v resolve=%v next=%d target=%#v",
			child, resolveErr, implementation.nextNodeID, target)
	}
	implementation.mu.RUnlock()
}

func TestReplaceNodePreservesFileIdentityOwnershipAndHandles(t *testing.T) {
	path := domain.CanonicalPath("/contract-directory/nested-file")
	scenario := validScenario(t)
	scenario.Root.Children[0].Children[0].Data = []byte("ABCDE")
	scenario.Script = []FaultStep{
		staleStatExactStep(path, 3, 1),
		staleStatExactStep(path, 4, 2),
	}
	implementation, controller, err := New(scenario)
	if err != nil {
		t.Fatalf("New(): %v", err)
	}
	location := domain.Location{EndpointID: contractEndpointID, Path: path}
	parentLocation := domain.Location{EndpointID: contractEndpointID, Path: "/contract-directory"}
	rootLocation := domain.Location{EndpointID: contractEndpointID, Path: "/"}

	oldEntry := statEntry(t, implementation, location)
	oldRead, err := implementation.OpenRead(context.Background(), providerapi.OpenReadRequest{
		Location: location,
	})
	if err != nil {
		t.Fatalf("OpenRead(): %v", err)
	}
	oldWrite, err := implementation.OpenWrite(context.Background(), providerapi.OpenWriteRequest{
		Location:    location,
		Offset:      2,
		Disposition: providerapi.WriteResumeExisting,
	})
	if err != nil {
		t.Fatalf("OpenWrite(): %v", err)
	}

	implementation.mu.Lock()
	target, _ := resolveNode(implementation.root, path, false)
	parent, _ := resolveNode(implementation.root, parentLocation.Path, false)
	root := implementation.root
	target.permissionDenied = true
	targetPointer := target
	targetID := target.id
	targetVersion := target.version
	parentVersion := parent.version
	parentGeneration := parent.listingGeneration
	rootVersion := root.version
	rootGeneration := root.listingGeneration
	implementation.mu.Unlock()
	if oldRead.(*readHandle).node != targetPointer || oldWrite.(*writeHandle).node != targetPointer ||
		oldWrite.(*writeHandle).offset != 2 {
		t.Fatalf("opened handle identities do not point at replacement root")
	}

	modifiedAt := time.Date(2025, time.December, 31, 23, 59, 58, 0, time.UTC)
	precision := domain.TimePrecision("second")
	fileID := "replacement-file-id"
	mode := uint32(0o640)
	data := []byte("xyz12")
	replacement := Node{
		Name: "nested-file",
		Kind: domain.EntryFile,
		Data: data,
		Metadata: domain.Metadata{
			Mode:              &mode,
			ModifiedAt:        &modifiedAt,
			ModifiedPrecision: &precision,
			FileID:            &fileID,
		},
	}
	beforeCalls := controller.Calls()
	if err := controller.ReplaceNode(path, replacement); err != nil {
		t.Fatalf("ReplaceNode(): %v", err)
	}
	if afterCalls := controller.Calls(); !reflect.DeepEqual(afterCalls, beforeCalls) {
		t.Fatalf("Controller ReplaceNode recorded Provider call: before=%#v after=%#v", beforeCalls, afterCalls)
	}

	data[0] = 'Q'
	mode = 0
	modifiedAt = time.Time{}
	precision = "caller-mutated"
	fileID = "caller-mutated"
	replacement.Metadata.Size = cloneUint64(999)

	implementation.mu.Lock()
	if target != targetPointer || target.id != targetID || target.version != targetVersion+1 ||
		!target.permissionDenied {
		implementation.mu.Unlock()
		t.Fatalf("preserved file root = %#v", target)
	}
	if parent.version != parentVersion+1 || parent.listingGeneration != parentGeneration+1 {
		implementation.mu.Unlock()
		t.Fatalf("parent revision/generation = (%d,%d), want (%d,%d)",
			parent.version, parent.listingGeneration, parentVersion+1, parentGeneration+1)
	}
	if root.version != rootVersion || root.listingGeneration != rootGeneration {
		implementation.mu.Unlock()
		t.Fatalf("higher ancestor changed = (%d,%d), want (%d,%d)",
			root.version, root.listingGeneration, rootVersion, rootGeneration)
	}
	// Prove that replacement preserved denial, then restore access directly so
	// these D2 behavior checks remain valid once E2 enforces permissions.
	target.permissionDenied = false
	implementation.mu.Unlock()

	newEntry := statEntry(t, implementation, location)
	if newEntry.Metadata.Mode == nil || *newEntry.Metadata.Mode != 0o640 ||
		newEntry.Metadata.ModifiedAt == nil || !newEntry.Metadata.ModifiedAt.Equal(time.Date(2025, time.December, 31, 23, 59, 58, 0, time.UTC)) ||
		newEntry.Metadata.ModifiedPrecision == nil || *newEntry.Metadata.ModifiedPrecision != "second" ||
		newEntry.Metadata.FileID == nil || *newEntry.Metadata.FileID != "replacement-file-id" {
		t.Fatalf("replacement metadata ownership = %#v", newEntry.Metadata)
	}
	requireEntrySize(t, newEntry, 5)
	if got := readFile(t, implementation, location); string(got) != "xyz12" {
		t.Fatalf("new read bytes = %q, want xyz12", got)
	}

	oldHistorical := statEntry(t, implementation, location)
	newHistorical := statEntry(t, implementation, location)
	if !reflect.DeepEqual(oldHistorical, oldEntry) {
		t.Fatalf("old revision = %#v, want %#v", oldHistorical, oldEntry)
	}
	if !reflect.DeepEqual(newHistorical, newEntry) {
		t.Fatalf("replacement revision = %#v, want %#v", newHistorical, newEntry)
	}
	if got := string(readAll(t, oldRead)); got != "ABCDE" {
		t.Fatalf("old ReadHandle bytes = %q, want ABCDE", got)
	}
	if oldRead.(*readHandle).node != targetPointer || oldWrite.(*writeHandle).node != targetPointer ||
		oldWrite.(*writeHandle).offset != 2 {
		t.Fatalf("replacement changed handle identity/offset before write")
	}
	if info := oldRead.Info(); !reflect.DeepEqual(info.Entry, oldEntry) {
		t.Fatalf("old ReadHandle info = %#v, want %#v", info.Entry, oldEntry)
	}

	if n, err := oldWrite.Write(context.Background(), []byte("!")); n != 1 || err != nil {
		t.Fatalf("old WriteHandle.Write() = (%d, %v), want (1, nil)", n, err)
	}
	if got := readFile(t, implementation, location); string(got) != "xy!12" {
		t.Fatalf("bytes after retained WriteHandle = %q, want xy!12", got)
	}
	implementation.mu.RLock()
	if target != targetPointer || target.id != targetID || target.version != targetVersion+2 {
		t.Fatalf("file identity after retained write = %#v", target)
	}
	if parent.version != parentVersion+2 || parent.listingGeneration != parentGeneration+2 {
		t.Fatalf("parent after retained write = (%d,%d), want (%d,%d)",
			parent.version, parent.listingGeneration, parentVersion+2, parentGeneration+2)
	}
	implementation.mu.RUnlock()
	if rootAfter := statEntry(t, implementation, rootLocation); rootAfter.Fingerprint.VersionID == nil {
		t.Fatal("root fingerprint missing after replacement")
	}
}

func TestReplaceNodeRebuildsDirectoryDescendantsAndHistory(t *testing.T) {
	targetPath := domain.CanonicalPath("/replace-container/replace-dir")
	filePath := domain.CanonicalPath("/replace-container/replace-dir/target-file")
	linkPath := domain.CanonicalPath("/replace-container/replace-dir/link")
	scenario := validScenario(t)
	scenario.Root.Children = append(scenario.Root.Children,
		Node{
			Name: "replace-container",
			Kind: domain.EntryDirectory,
			Children: []Node{
				{
					Name: "replace-dir",
					Kind: domain.EntryDirectory,
					Children: []Node{
						{Name: "old-file", Kind: domain.EntryFile, Data: []byte("old")},
						{
							Name: "old-dir", Kind: domain.EntryDirectory,
							Children: []Node{{Name: "old-nested", Kind: domain.EntryFile}},
						},
					},
				},
				{Name: "sibling", Kind: domain.EntryDirectory},
			},
		},
		Node{Name: "replace-unrelated", Kind: domain.EntryDirectory},
	)
	scenario.Script = []FaultStep{
		staleStatExactStep(targetPath, 3, 1),
		staleStatExactStep(targetPath, 4, 2),
		staleStatExactStep(filePath, 2, 1),
		staleStatExactStep(linkPath, 2, 1),
	}
	implementation, controller, err := New(scenario)
	if err != nil {
		t.Fatalf("New(): %v", err)
	}
	targetLocation := domain.Location{EndpointID: contractEndpointID, Path: targetPath}
	fileLocation := domain.Location{EndpointID: contractEndpointID, Path: filePath}
	linkLocation := domain.Location{EndpointID: contractEndpointID, Path: linkPath}
	oldTargetEntry := statEntry(t, implementation, targetLocation)

	implementation.mu.Lock()
	target, _ := resolveNode(implementation.root, targetPath, false)
	parent, _ := resolveNode(implementation.root, "/replace-container", false)
	unrelated, _ := resolveNode(implementation.root, "/replace-unrelated", false)
	root := implementation.root
	target.permissionDenied = true
	targetPointer := target
	targetID := target.id
	targetVersion := target.version
	targetGeneration := target.listingGeneration
	parentVersion := parent.version
	parentGeneration := parent.listingGeneration
	rootVersion := root.version
	rootGeneration := root.listingGeneration
	unrelatedVersion := unrelated.version
	unrelatedGeneration := unrelated.listingGeneration
	oldChildren := make(map[*treeNode]uint64)
	collectReplaceNodeIDs(target, oldChildren)
	delete(oldChildren, target)
	nextNodeID := implementation.nextNodeID
	implementation.mu.Unlock()

	fileData := []byte("replacement-data")
	fileMode := uint32(0o600)
	rootFileID := "replacement-directory"
	replacement := Node{
		Name: "replace-dir",
		Kind: domain.EntryDirectory,
		Metadata: domain.Metadata{
			FileID: &rootFileID,
		},
		Children: []Node{
			{Name: "target-file", Kind: domain.EntryFile, Data: fileData, Metadata: domain.Metadata{Mode: &fileMode}},
			{
				Name: "fresh-dir", Kind: domain.EntryDirectory,
				Children: []Node{{Name: "nested", Kind: domain.EntryFile, Data: []byte("nested")}},
			},
			{Name: "link", Kind: domain.EntrySymlink, LinkTarget: "target-file"},
		},
	}
	if err := controller.ReplaceNode(targetPath, replacement); err != nil {
		t.Fatalf("ReplaceNode(): %v", err)
	}
	fileData[0] = 'X'
	fileMode = 0
	rootFileID = "caller-mutated"
	replacement.Children[0].Name = "caller-mutated"
	replacement.Children[2].LinkTarget = "/missing"

	implementation.mu.Lock()
	if target != targetPointer || target.id != targetID || !target.permissionDenied ||
		target.version != targetVersion+1 || target.listingGeneration != targetGeneration+1 {
		implementation.mu.Unlock()
		t.Fatalf("replacement directory root = %#v", target)
	}
	// Prove preservation before restoring access for descendant Stat/OpenRead.
	target.permissionDenied = false
	implementation.mu.Unlock()

	newTargetEntry := statEntry(t, implementation, targetLocation)
	newFileEntry := statEntry(t, implementation, fileLocation)
	newLinkEntry := statEntry(t, implementation, linkLocation)
	if got := readFile(t, implementation, fileLocation); string(got) != "replacement-data" {
		t.Fatalf("owned replacement file bytes = %q", got)
	}
	if newFileEntry.Metadata.Mode == nil || *newFileEntry.Metadata.Mode != 0o600 {
		t.Fatalf("owned replacement file metadata = %#v", newFileEntry.Metadata)
	}
	if newTargetEntry.Metadata.FileID == nil || *newTargetEntry.Metadata.FileID != "replacement-directory" {
		t.Fatalf("owned replacement root metadata = %#v", newTargetEntry.Metadata)
	}
	if newLinkEntry.Symlink == nil || newLinkEntry.Symlink.RawTarget != "target-file" ||
		newLinkEntry.Symlink.ResolvedKind == nil || *newLinkEntry.Symlink.ResolvedKind != domain.EntryFile {
		t.Fatalf("replacement symlink = %#v, want fully mounted file resolution", newLinkEntry.Symlink)
	}

	implementation.mu.RLock()
	if target != targetPointer || target.id != targetID ||
		target.version != targetVersion+1 || target.listingGeneration != targetGeneration+1 {
		t.Fatalf("replacement directory root = %#v", target)
	}
	if parent.version != parentVersion+1 || parent.listingGeneration != parentGeneration+1 {
		t.Fatalf("lexical parent = (%d,%d), want (%d,%d)",
			parent.version, parent.listingGeneration, parentVersion+1, parentGeneration+1)
	}
	if root.version != rootVersion || root.listingGeneration != rootGeneration ||
		unrelated.version != unrelatedVersion || unrelated.listingGeneration != unrelatedGeneration {
		t.Fatalf("unrelated revisions changed: root=(%d,%d) unrelated=(%d,%d)",
			root.version, root.listingGeneration, unrelated.version, unrelated.listingGeneration)
	}
	for oldChild := range oldChildren {
		if _, attached := findNodePath(implementation.root, oldChild, "/"); attached {
			t.Fatalf("old child %p remains attached", oldChild)
		}
	}
	fresh := make(map[*treeNode]uint64)
	collectReplaceNodeIDs(target, fresh)
	delete(fresh, target)
	wantIDs := map[domain.CanonicalPath]uint64{
		filePath: nextNodeID,
		"/replace-container/replace-dir/fresh-dir":        nextNodeID + 1,
		"/replace-container/replace-dir/fresh-dir/nested": nextNodeID + 2,
		"/replace-container/replace-dir/link":             nextNodeID + 3,
	}
	for path, wantID := range wantIDs {
		freshNode, resolveErr := resolveNode(implementation.root, path, false)
		if resolveErr != nil || freshNode.id != wantID {
			t.Fatalf("fresh descendant %q = (%#v, %v), want ID %d", path, freshNode, resolveErr, wantID)
		}
	}
	if implementation.nextNodeID != nextNodeID+uint64(len(wantIDs)) {
		t.Fatalf("nextNodeID = %d, want %d", implementation.nextNodeID, nextNodeID+uint64(len(wantIDs)))
	}
	seenIDs := map[uint64]struct{}{targetID: {}}
	for freshNode, id := range fresh {
		if id < nextNodeID {
			t.Fatalf("fresh descendant ID = %d, want >= %d", id, nextNodeID)
		}
		if _, duplicate := seenIDs[id]; duplicate {
			t.Fatalf("duplicate replacement ID %d", id)
		}
		seenIDs[id] = struct{}{}
		if freshNode.version != 1 || freshNode.permissionDenied {
			t.Fatalf("fresh descendant defaults = %#v", freshNode)
		}
		if freshNode.kind == domain.EntryDirectory && freshNode.listingGeneration != 1 {
			t.Fatalf("fresh directory generation = %d, want 1", freshNode.listingGeneration)
		}
		if _, exists := implementation.history[nodeRevisionKey{nodeID: id, revision: 1}]; !exists {
			t.Fatalf("fresh descendant %d has no revision-1 history", id)
		}
	}
	for _, oldID := range oldChildren {
		if _, reused := seenIDs[oldID]; reused {
			t.Fatalf("old descendant ID %d was reused", oldID)
		}
	}
	implementation.mu.RUnlock()

	oldTargetHistory := statEntry(t, implementation, targetLocation)
	newTargetHistory := statEntry(t, implementation, targetLocation)
	if !reflect.DeepEqual(oldTargetHistory, oldTargetEntry) {
		t.Fatalf("old target history = %#v, want %#v", oldTargetHistory, oldTargetEntry)
	}
	if !reflect.DeepEqual(newTargetHistory, newTargetEntry) {
		t.Fatalf("new target history = %#v, want %#v", newTargetHistory, newTargetEntry)
	}
	if historical := statEntry(t, implementation, fileLocation); !reflect.DeepEqual(historical, newFileEntry) {
		t.Fatalf("fresh file revision 1 = %#v, want %#v", historical, newFileEntry)
	}
	if historical := statEntry(t, implementation, linkLocation); !reflect.DeepEqual(historical, newLinkEntry) {
		t.Fatalf("fresh symlink revision 1 = %#v, want %#v", historical, newLinkEntry)
	}
}

func TestReplaceNodeUsesLexicalFinalAndFollowsIntermediateSymlinks(t *testing.T) {
	t.Run("lexical final symlink", func(t *testing.T) {
		scenario := validScenario(t)
		scenario.Root.Children = append(scenario.Root.Children,
			Node{Name: "replace-physical", Kind: domain.EntryFile, Data: []byte("physical")},
			Node{Name: "replace-new-target", Kind: domain.EntryFile, Data: []byte("new-target")},
			Node{Name: "replace-alias", Kind: domain.EntrySymlink, LinkTarget: "/replace-physical"},
			Node{Name: "replace-broken", Kind: domain.EntrySymlink, LinkTarget: "/missing"},
			Node{Name: "replace-final-loop-a", Kind: domain.EntrySymlink, LinkTarget: "/replace-final-loop-b"},
			Node{Name: "replace-final-loop-b", Kind: domain.EntrySymlink, LinkTarget: "/replace-final-loop-a"},
		)
		scenario.Script = []FaultStep{
			staleStatExactStep("/replace-alias", 3, 1),
			staleStatExactStep("/replace-alias", 4, 2),
		}
		implementation, controller, err := New(scenario)
		if err != nil {
			t.Fatalf("New(): %v", err)
		}
		physical := domain.Location{EndpointID: contractEndpointID, Path: "/replace-physical"}
		aliasLocation := domain.Location{EndpointID: contractEndpointID, Path: "/replace-alias"}
		beforePhysical := statEntry(t, implementation, physical)
		beforeBytes := readFile(t, implementation, physical)
		beforeAlias := statEntry(t, implementation, aliasLocation)

		implementation.mu.Lock()
		alias, _ := resolveNode(implementation.root, "/replace-alias", false)
		parent := implementation.root
		alias.permissionDenied = true
		aliasPointer, aliasID := alias, alias.id
		aliasVersion := alias.version
		aliasGeneration := alias.listingGeneration
		parentVersion, parentGeneration := parent.version, parent.listingGeneration
		implementation.mu.Unlock()
		if err := controller.ReplaceNode("/replace-alias", Node{
			Name: "replace-alias", Kind: domain.EntrySymlink, LinkTarget: "/replace-new-target",
		}); err != nil {
			t.Fatalf("ReplaceNode(alias): %v", err)
		}
		implementation.mu.Lock()
		if alias != aliasPointer || alias.id != aliasID || alias.linkTarget != "/replace-new-target" ||
			alias.version != aliasVersion+1 || alias.listingGeneration != aliasGeneration ||
			!alias.permissionDenied || parent.version != parentVersion+1 ||
			parent.listingGeneration != parentGeneration+1 {
			implementation.mu.Unlock()
			t.Fatalf("lexical alias replacement = %#v", alias)
		}
		alias.permissionDenied = false
		implementation.mu.Unlock()
		afterAlias := statEntry(t, implementation, aliasLocation)
		if stale := statEntry(t, implementation, aliasLocation); !reflect.DeepEqual(stale, beforeAlias) {
			t.Fatalf("old symlink revision = %#v, want %#v", stale, beforeAlias)
		}
		if stale := statEntry(t, implementation, aliasLocation); !reflect.DeepEqual(stale, afterAlias) {
			t.Fatalf("new symlink revision = %#v, want %#v", stale, afterAlias)
		}
		if afterPhysical := statEntry(t, implementation, physical); !reflect.DeepEqual(afterPhysical, beforePhysical) {
			t.Fatalf("followed target changed: before=%#v after=%#v", beforePhysical, afterPhysical)
		}
		if got := readFile(t, implementation, physical); !bytes.Equal(got, beforeBytes) {
			t.Fatalf("followed target bytes changed: got=%q want=%q", got, beforeBytes)
		}

		for _, path := range []domain.CanonicalPath{"/replace-broken", "/replace-final-loop-a"} {
			if err := controller.ReplaceNode(path, Node{
				Name: baseName(path), Kind: domain.EntrySymlink, LinkTarget: "/replace-physical",
			}); err != nil {
				t.Fatalf("ReplaceNode(%q final symlink): %v", path, err)
			}
			entry, err := implementation.Stat(context.Background(), providerapi.StatRequest{
				Location:       domain.Location{EndpointID: contractEndpointID, Path: path},
				FollowSymlinks: true,
			})
			if err != nil || entry.Kind != domain.EntryFile {
				t.Fatalf("repaired final symlink %q = (%#v, %v)", path, entry, err)
			}
		}
	})

	t.Run("other root", func(t *testing.T) {
		path := domain.CanonicalPath("/replace-other")
		scenario := validScenario(t)
		scenario.Root.Children = append(scenario.Root.Children, Node{
			Name: "replace-other", Kind: domain.EntryOther, Data: []byte("old-other"),
		})
		scenario.Script = []FaultStep{
			staleStatExactStep(path, 3, 1),
			staleStatExactStep(path, 4, 2),
		}
		implementation, controller, err := New(scenario)
		if err != nil {
			t.Fatalf("New(): %v", err)
		}
		location := domain.Location{EndpointID: contractEndpointID, Path: path}
		before := statEntry(t, implementation, location)
		implementation.mu.Lock()
		target, _ := resolveNode(implementation.root, path, false)
		parent := implementation.root
		target.permissionDenied = true
		targetPointer, targetID := target, target.id
		targetVersion, targetGeneration := target.version, target.listingGeneration
		parentVersion, parentGeneration := parent.version, parent.listingGeneration
		implementation.mu.Unlock()

		data := []byte("new-other")
		mode := uint32(0o444)
		if err := controller.ReplaceNode(path, Node{
			Name: "replace-other", Kind: domain.EntryOther, Data: data,
			Metadata: domain.Metadata{Mode: &mode},
		}); err != nil {
			t.Fatalf("ReplaceNode(other): %v", err)
		}
		data[0] = 'X'
		mode = 0

		implementation.mu.Lock()
		if target != targetPointer || target.id != targetID || target.version != targetVersion+1 ||
			target.listingGeneration != targetGeneration || !target.permissionDenied ||
			parent.version != parentVersion+1 || parent.listingGeneration != parentGeneration+1 ||
			string(target.data) != "new-other" || target.metadata.Mode == nil || *target.metadata.Mode != 0o444 {
			implementation.mu.Unlock()
			t.Fatalf("other replacement state = %#v parent=(%d,%d)",
				target, parent.version, parent.listingGeneration)
		}
		target.permissionDenied = false
		implementation.mu.Unlock()

		after := statEntry(t, implementation, location)
		if stale := statEntry(t, implementation, location); !reflect.DeepEqual(stale, before) {
			t.Fatalf("old other revision = %#v, want %#v", stale, before)
		}
		if stale := statEntry(t, implementation, location); !reflect.DeepEqual(stale, after) {
			t.Fatalf("new other revision = %#v, want %#v", stale, after)
		}
	})

	t.Run("intermediate alias", func(t *testing.T) {
		scenario := validScenario(t)
		scenario.Root.Children = append(scenario.Root.Children,
			Node{
				Name: "replace-physical-dir", Kind: domain.EntryDirectory,
				Children: []Node{
					{Name: "file", Kind: domain.EntryFile, Data: []byte("before")},
					{Name: "other", Kind: domain.EntryFile},
				},
			},
			Node{Name: "replace-dir-alias", Kind: domain.EntrySymlink, LinkTarget: "/replace-physical-dir"},
		)
		implementation, controller, err := New(scenario)
		if err != nil {
			t.Fatalf("New(): %v", err)
		}
		physicalDirectory := domain.Location{EndpointID: contractEndpointID, Path: "/replace-physical-dir"}
		aliasDirectory := domain.Location{EndpointID: contractEndpointID, Path: "/replace-dir-alias"}
		physicalCursor := issueReplaceCursor(t, implementation, physicalDirectory)
		aliasCursor := issueReplaceCursor(t, implementation, aliasDirectory)

		implementation.mu.RLock()
		alias, _ := resolveNode(implementation.root, aliasDirectory.Path, false)
		file, _ := resolveNode(implementation.root, "/replace-physical-dir/file", false)
		parent, _ := resolveNode(implementation.root, physicalDirectory.Path, false)
		aliasVersion := alias.version
		filePointer, fileID := file, file.id
		parentGeneration := parent.listingGeneration
		implementation.mu.RUnlock()

		if err := controller.ReplaceNode("/replace-dir-alias/file", Node{
			Name: "file", Kind: domain.EntryFile, Data: []byte("after"),
		}); err != nil {
			t.Fatalf("ReplaceNode(intermediate alias): %v", err)
		}
		if got := readFile(t, implementation, domain.Location{
			EndpointID: contractEndpointID,
			Path:       "/replace-physical-dir/file",
		}); string(got) != "after" {
			t.Fatalf("physical file bytes = %q, want after", got)
		}
		implementation.mu.RLock()
		if alias.version != aliasVersion || file != filePointer || file.id != fileID ||
			parent.listingGeneration != parentGeneration+1 {
			t.Fatalf("intermediate alias state: alias=%#v file=%#v parentGen=%d",
				alias, file, parent.listingGeneration)
		}
		implementation.mu.RUnlock()
		requireReplaceCursorConflict(t, implementation, physicalCursor)
		requireReplaceCursorConflict(t, implementation, aliasCursor)
	})
}

func TestReplaceNodeInvalidatesExactlyAffectedCursors(t *testing.T) {
	t.Run("directory root parent and fresh descendant", func(t *testing.T) {
		scenario := replaceCursorScenario(t)
		implementation, controller, err := New(scenario)
		if err != nil {
			t.Fatalf("New(): %v", err)
		}
		target := issueReplaceCursor(t, implementation, domain.Location{
			EndpointID: contractEndpointID, Path: "/cursor-container/target",
		})
		parent := issueReplaceCursor(t, implementation, domain.Location{
			EndpointID: contractEndpointID, Path: "/cursor-container",
		})
		oldChild := issueReplaceCursor(t, implementation, domain.Location{
			EndpointID: contractEndpointID, Path: "/cursor-container/target/child-dir",
		})
		higher := issueReplaceCursor(t, implementation, domain.Location{
			EndpointID: contractEndpointID, Path: "/",
		})
		unrelated := issueReplaceCursor(t, implementation, domain.Location{
			EndpointID: contractEndpointID, Path: "/cursor-unrelated",
		})

		if err := controller.ReplaceNode("/cursor-container/target", Node{
			Name: "target", Kind: domain.EntryDirectory,
			Children: []Node{
				{
					Name: "child-dir", Kind: domain.EntryDirectory,
					Children: []Node{
						{Name: "fresh-a", Kind: domain.EntryFile},
						{Name: "fresh-b", Kind: domain.EntryFile},
					},
				},
				{Name: "fresh-file", Kind: domain.EntryFile},
			},
		}); err != nil {
			t.Fatalf("ReplaceNode(directory): %v", err)
		}

		requireReplaceCursorConflict(t, implementation, target)
		requireReplaceCursorConflict(t, implementation, parent)
		requireReplaceCursorConflict(t, implementation, oldChild)
		requireReplaceCursorContinues(t, implementation, higher)
		requireReplaceCursorContinues(t, implementation, unrelated)
	})

	for _, test := range []struct {
		name        string
		path        domain.CanonicalPath
		replacement Node
	}{
		{
			name: "file",
			path: "/cursor-container/file",
			replacement: Node{
				Name: "file", Kind: domain.EntryFile, Data: []byte("new-file"),
			},
		},
		{
			name: "symlink",
			path: "/cursor-container/link",
			replacement: Node{
				Name: "link", Kind: domain.EntrySymlink, LinkTarget: "/cursor-other-target",
			},
		},
	} {
		t.Run(test.name+" only lexical parent", func(t *testing.T) {
			scenario := replaceCursorScenario(t)
			implementation, controller, err := New(scenario)
			if err != nil {
				t.Fatalf("New(): %v", err)
			}
			parent := issueReplaceCursor(t, implementation, domain.Location{
				EndpointID: contractEndpointID, Path: "/cursor-container",
			})
			higher := issueReplaceCursor(t, implementation, domain.Location{
				EndpointID: contractEndpointID, Path: "/",
			})
			target := issueReplaceCursor(t, implementation, domain.Location{
				EndpointID: contractEndpointID, Path: "/cursor-target",
			})
			unrelated := issueReplaceCursor(t, implementation, domain.Location{
				EndpointID: contractEndpointID, Path: "/cursor-unrelated",
			})

			if err := controller.ReplaceNode(test.path, test.replacement); err != nil {
				t.Fatalf("ReplaceNode(%s): %v", test.name, err)
			}
			requireReplaceCursorConflict(t, implementation, parent)
			requireReplaceCursorContinues(t, implementation, higher)
			requireReplaceCursorContinues(t, implementation, target)
			requireReplaceCursorContinues(t, implementation, unrelated)
		})
	}
}

func TestReplaceNodeKeepsRootRevisionHistoryByIdentity(t *testing.T) {
	path := domain.CanonicalPath("/replace-history")
	scenario := validScenario(t)
	scenario.Root.Children = append(scenario.Root.Children, Node{
		Name: "replace-history", Kind: domain.EntryFile, Data: []byte("old"),
	})
	scenario.Script = []FaultStep{
		staleStatExactStep(path, 3, 1),
		staleStatExactStep(path, 4, 2),
		staleStatExactStep(path, 6, 1),
	}
	implementation, controller, err := New(scenario)
	if err != nil {
		t.Fatalf("New(): %v", err)
	}
	location := domain.Location{EndpointID: contractEndpointID, Path: path}
	oldEntry := statEntry(t, implementation, location)
	implementation.mu.RLock()
	oldNode, _ := resolveNode(implementation.root, path, false)
	oldPointer, oldID := oldNode, oldNode.id
	implementation.mu.RUnlock()

	if err := controller.ReplaceNode(path, Node{
		Name: "replace-history", Kind: domain.EntryFile, Data: []byte("new"),
	}); err != nil {
		t.Fatalf("ReplaceNode(): %v", err)
	}
	newEntry := statEntry(t, implementation, location)
	implementation.mu.RLock()
	if oldNode != oldPointer || oldNode.id != oldID || oldNode.version != 2 {
		t.Fatalf("replacement root identity = %#v", oldNode)
	}
	implementation.mu.RUnlock()
	if stale := statEntry(t, implementation, location); !reflect.DeepEqual(stale, oldEntry) {
		t.Fatalf("replacement stale revision 1 = %#v, want %#v", stale, oldEntry)
	}
	if stale := statEntry(t, implementation, location); !reflect.DeepEqual(stale, newEntry) {
		t.Fatalf("replacement stale revision 2 = %#v, want %#v", stale, newEntry)
	}

	if err := implementation.Remove(context.Background(), providerapi.RemoveRequest{Location: location}); err != nil {
		t.Fatalf("Remove(): %v", err)
	}
	created, err := implementation.OpenWrite(context.Background(), providerapi.OpenWriteRequest{
		Location:    location,
		Disposition: providerapi.WriteCreateNew,
	})
	if err != nil {
		t.Fatalf("OpenWrite(create): %v", err)
	}
	if err := created.Close(context.Background()); err != nil {
		t.Fatalf("Close(create): %v", err)
	}
	recreated := statEntry(t, implementation, location)
	implementation.mu.RLock()
	recreatedNode, _ := resolveNode(implementation.root, path, false)
	if recreatedNode.id == oldID {
		t.Fatalf("recreated node reused replacement identity %d", oldID)
	}
	implementation.mu.RUnlock()
	if stale := statEntry(t, implementation, location); !reflect.DeepEqual(stale, recreated) {
		t.Fatalf("recreated revision 1 leaked detached history: stale=%#v recreated=%#v", stale, recreated)
	}
}

func TestReplaceNodeSerializesWithOpenWriteHandle(t *testing.T) {
	t.Run("replace and write", func(t *testing.T) {
		scenario := validScenario(t)
		scenario.Root.Children = append(scenario.Root.Children, Node{
			Name: "replace-race-file", Kind: domain.EntryFile, Data: []byte("ABCDE"),
		})
		implementation, controller, err := New(scenario)
		if err != nil {
			t.Fatalf("New(): %v", err)
		}
		location := domain.Location{EndpointID: contractEndpointID, Path: "/replace-race-file"}
		handle, err := implementation.OpenWrite(context.Background(), providerapi.OpenWriteRequest{
			Location:    location,
			Offset:      2,
			Disposition: providerapi.WriteResumeExisting,
		})
		if err != nil {
			t.Fatalf("OpenWrite(): %v", err)
		}
		implementation.mu.RLock()
		target, _ := resolveNode(implementation.root, location.Path, false)
		parent := implementation.root
		targetPointer, targetID := target, target.id
		targetVersion := target.version
		parentVersion, parentGeneration := parent.version, parent.listingGeneration
		implementation.mu.RUnlock()

		start := make(chan struct{})
		ready := make(chan struct{}, 2)
		replaceResult := make(chan error, 1)
		writeResult := make(chan struct {
			n   int
			err error
		}, 1)
		go func() {
			ready <- struct{}{}
			<-start
			replaceResult <- controller.ReplaceNode(location.Path, Node{
				Name: "replace-race-file", Kind: domain.EntryFile, Data: []byte("xyz12"),
			})
		}()
		go func() {
			ready <- struct{}{}
			<-start
			n, writeErr := handle.Write(context.Background(), []byte("!"))
			writeResult <- struct {
				n   int
				err error
			}{n: n, err: writeErr}
		}()
		<-ready
		<-ready
		close(start)
		if err := <-replaceResult; err != nil {
			t.Fatalf("concurrent ReplaceNode(): %v", err)
		}
		if result := <-writeResult; result.n != 1 || result.err != nil {
			t.Fatalf("concurrent Write() = (%d, %v), want (1, nil)", result.n, result.err)
		}
		got := string(readFile(t, implementation, location))
		if got != "xyz12" && got != "xy!12" {
			t.Fatalf("concurrent final bytes = %q, want complete serialized result", got)
		}
		implementation.mu.RLock()
		if target != targetPointer || target.id != targetID || target.version != targetVersion+2 ||
			parent.version != parentVersion+2 || parent.listingGeneration != parentGeneration+2 {
			t.Fatalf("concurrent revisions: target=%#v parent=(%d,%d)",
				target, parent.version, parent.listingGeneration)
		}
		implementation.mu.RUnlock()
	})

	t.Run("two replacements", func(t *testing.T) {
		scenario := validScenario(t)
		scenario.Root.Children = append(scenario.Root.Children, Node{
			Name: "replace-race-directory", Kind: domain.EntryDirectory,
			Children: []Node{{Name: "old", Kind: domain.EntryFile}},
		})
		implementation, controller, err := New(scenario)
		if err != nil {
			t.Fatalf("New(): %v", err)
		}
		path := domain.CanonicalPath("/replace-race-directory")
		implementation.mu.RLock()
		target, _ := resolveNode(implementation.root, path, false)
		parent := implementation.root
		targetPointer, targetID := target, target.id
		targetVersion, targetGeneration := target.version, target.listingGeneration
		parentVersion, parentGeneration := parent.version, parent.listingGeneration
		implementation.mu.RUnlock()

		replacements := []Node{
			{
				Name: "replace-race-directory", Kind: domain.EntryDirectory,
				Children: []Node{
					{Name: "a-file", Kind: domain.EntryFile, Data: []byte("A")},
					{Name: "a-dir", Kind: domain.EntryDirectory, Children: []Node{{Name: "nested-a", Kind: domain.EntryFile}}},
				},
			},
			{
				Name: "replace-race-directory", Kind: domain.EntryDirectory,
				Children: []Node{
					{Name: "b-file", Kind: domain.EntryFile, Data: []byte("B")},
					{Name: "b-dir", Kind: domain.EntryDirectory, Children: []Node{{Name: "nested-b", Kind: domain.EntryFile}}},
				},
			},
		}
		start := make(chan struct{})
		ready := make(chan struct{}, len(replacements))
		results := make(chan error, len(replacements))
		for index := range replacements {
			replacement := replacements[index]
			go func() {
				ready <- struct{}{}
				<-start
				results <- controller.ReplaceNode(path, replacement)
			}()
		}
		for range replacements {
			<-ready
		}
		close(start)
		for range replacements {
			if err := <-results; err != nil {
				t.Fatalf("concurrent ReplaceNode(): %v", err)
			}
		}

		entries := listAllEntries(t, implementation, domain.Location{
			EndpointID: contractEndpointID, Path: path,
		})
		names := make([]string, 0, len(entries))
		for _, entry := range entries {
			names = append(names, entry.Name)
		}
		sort.Strings(names)
		if !reflect.DeepEqual(names, []string{"a-dir", "a-file"}) &&
			!reflect.DeepEqual(names, []string{"b-dir", "b-file"}) {
			t.Fatalf("concurrent replacement tree is mixed: %q", names)
		}
		if names[0] == "a-dir" {
			if got := readFile(t, implementation, domain.Location{EndpointID: contractEndpointID, Path: "/replace-race-directory/a-file"}); string(got) != "A" {
				t.Fatalf("A tree file = %q", got)
			}
		} else if got := readFile(t, implementation, domain.Location{EndpointID: contractEndpointID, Path: "/replace-race-directory/b-file"}); string(got) != "B" {
			t.Fatalf("B tree file = %q", got)
		}
		implementation.mu.RLock()
		if target != targetPointer || target.id != targetID || target.version != targetVersion+2 ||
			target.listingGeneration != targetGeneration+2 || parent.version != parentVersion+2 ||
			parent.listingGeneration != parentGeneration+2 {
			t.Fatalf("concurrent replacement revisions: target=%#v parent=(%d,%d)",
				target, parent.version, parent.listingGeneration)
		}
		implementation.mu.RUnlock()
	})
}

func staleStatStep(nth uint64, value uint64) FaultStep {
	revision := value
	return FaultStep{
		Match: FaultMatch{
			Operation: OperationStat,
			Nth:       nth,
		},
		Effect: FaultEffect{StaleNodeRevision: &revision},
	}
}

func requireUnavailableStaleRevision(t *testing.T, err error) {
	t.Helper()
	opError := requireCode(t, err, domain.CodeInvalidArgument)
	if opError.Retry.Kind != domain.RetryNever || opError.Effect != domain.EffectNone {
		t.Fatalf("unavailable stale revision error = %#v, want retry-never EffectNone", opError)
	}
}

func requireEntrySize(t *testing.T, entry domain.Entry, size uint64) {
	t.Helper()
	if entry.Metadata.Size == nil || *entry.Metadata.Size != size {
		t.Fatalf("entry size = %#v, want %d", entry.Metadata.Size, size)
	}
	if entry.Fingerprint.Size == nil || *entry.Fingerprint.Size != size {
		t.Fatalf("fingerprint size = %#v, want %d", entry.Fingerprint.Size, size)
	}
}

type replaceTreeState struct {
	path              domain.CanonicalPath
	node              *treeNode
	id                uint64
	version           uint64
	listingGeneration uint64
	name              string
	kind              domain.EntryKind
	data              []byte
	metadata          domain.Metadata
	linkTarget        string
	permissionDenied  bool
}

type replaceReadHandleState struct {
	node   *treeNode
	info   providerapi.ReadInfo
	data   []byte
	offset int
	closed bool
}

type replaceWriteHandleState struct {
	node     *treeNode
	location domain.Location
	offset   int64
	closed   bool
}

type replaceFixtureState struct {
	root       *treeNode
	tree       []replaceTreeState
	snapshot   domain.EndpointSnapshot
	nextCursor uint64
	cursors    map[providerapi.PageCursor]cursorBinding
	nextNodeID uint64
	history    map[nodeRevisionKey]historicalStat
	calls      []Call
	read       replaceReadHandleState
	write      replaceWriteHandleState
}

func captureReplaceFixtureState(
	implementation *Provider,
	controller *Controller,
	read *readHandle,
	write *writeHandle,
) replaceFixtureState {
	implementation.mu.RLock()
	state := replaceFixtureState{
		root:       implementation.root,
		snapshot:   cloneSnapshot(implementation.snapshot),
		nextCursor: implementation.nextCursor,
		cursors:    make(map[providerapi.PageCursor]cursorBinding, len(implementation.cursors)),
		nextNodeID: implementation.nextNodeID,
		history:    make(map[nodeRevisionKey]historicalStat, len(implementation.history)),
	}
	captureReplaceTree(implementation.root, "/", &state.tree)
	for cursor, binding := range implementation.cursors {
		clonedBinding := binding
		clonedBinding.sort = cloneSortHint(binding.sort)
		if binding.snapshot != nil {
			clonedSnapshot := *binding.snapshot
			clonedSnapshot.entries = make([]domain.Entry, len(binding.snapshot.entries))
			for index := range binding.snapshot.entries {
				clonedSnapshot.entries[index] = cloneEntry(binding.snapshot.entries[index])
			}
			clonedSnapshot.directoryFingerprint = cloneFingerprint(
				binding.snapshot.directoryFingerprint,
			)
			clonedBinding.snapshot = &clonedSnapshot
		}
		state.cursors[cursor] = clonedBinding
	}
	for key, historical := range implementation.history {
		state.history[key] = historicalStat{
			kind:        historical.kind,
			metadata:    cloneMetadata(historical.metadata),
			fingerprint: cloneFingerprint(historical.fingerprint),
			symlink:     cloneSymlinkInfo(historical.symlink),
		}
	}
	implementation.mu.RUnlock()

	read.mu.Lock()
	state.read = replaceReadHandleState{
		node:   read.node,
		info:   cloneReadInfo(read.info),
		data:   append([]byte(nil), read.data...),
		offset: read.offset,
		closed: read.closed,
	}
	read.mu.Unlock()

	write.mu.Lock()
	state.write = replaceWriteHandleState{
		node:     write.node,
		location: write.location,
		offset:   write.offset,
		closed:   write.closed,
	}
	write.mu.Unlock()
	state.calls = controller.Calls()
	return state
}

func captureReplaceTree(
	node *treeNode,
	path domain.CanonicalPath,
	destination *[]replaceTreeState,
) {
	*destination = append(*destination, replaceTreeState{
		path:              path,
		node:              node,
		id:                node.id,
		version:           node.version,
		listingGeneration: node.listingGeneration,
		name:              node.name,
		kind:              node.kind,
		data:              append([]byte(nil), node.data...),
		metadata:          cloneMetadata(node.metadata),
		linkTarget:        node.linkTarget,
		permissionDenied:  node.permissionDenied,
	})
	names := make([]string, 0, len(node.children))
	for name := range node.children {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		captureReplaceTree(node.children[name], joinCanonical(path, name), destination)
	}
}

func collectReplaceNodeIDs(node *treeNode, destination map[*treeNode]uint64) {
	destination[node] = node.id
	for _, child := range node.children {
		collectReplaceNodeIDs(child, destination)
	}
}

func staleStatExactStep(path domain.CanonicalPath, nth uint64, revision uint64) FaultStep {
	requested := revision
	matched := path
	return FaultStep{
		Match: FaultMatch{
			Operation: OperationStat,
			Nth:       nth,
			Path:      &matched,
		},
		Effect: FaultEffect{StaleNodeRevision: &requested},
	}
}

type replaceCursorProbe struct {
	request providerapi.ListRequest
	want    domain.Entry
}

func issueReplaceCursor(
	t *testing.T,
	implementation *Provider,
	location domain.Location,
) replaceCursorProbe {
	t.Helper()
	all := listAllEntries(t, implementation, location)
	if len(all) < 2 {
		t.Fatalf("List(%q) has %d entries, want at least 2", location.Path, len(all))
	}
	request := providerapi.ListRequest{Location: location, Limit: 1}
	first, err := implementation.List(context.Background(), request)
	if err != nil {
		t.Fatalf("List(%q first page): %v", location.Path, err)
	}
	if first.Done || first.NextCursor == "" || len(first.Entries) != 1 {
		t.Fatalf("List(%q first page) = %#v, want continuation", location.Path, first)
	}
	request.Cursor = first.NextCursor
	return replaceCursorProbe{request: request, want: cloneEntry(all[1])}
}

func requireReplaceCursorConflict(
	t *testing.T,
	implementation *Provider,
	probe replaceCursorProbe,
) {
	t.Helper()
	_, err := implementation.List(context.Background(), probe.request)
	opError := requireCode(t, err, domain.CodeConflict)
	if opError.Retry.Kind != domain.RetryAfterConflict || opError.Effect != domain.EffectNone {
		t.Fatalf("cursor conflict = %#v", opError)
	}
}

func requireReplaceCursorContinues(
	t *testing.T,
	implementation *Provider,
	probe replaceCursorProbe,
) {
	t.Helper()
	page, err := implementation.List(context.Background(), probe.request)
	if err != nil {
		t.Fatalf("List(%q continuation): %v", probe.request.Location.Path, err)
	}
	if len(page.Entries) != 1 || !reflect.DeepEqual(page.Entries[0], probe.want) {
		t.Fatalf("List(%q continuation) = %#v, want frozen %#v",
			probe.request.Location.Path, page.Entries, probe.want)
	}
}

func replaceCursorScenario(t *testing.T) Scenario {
	t.Helper()
	scenario := validScenario(t)
	scenario.Root.Children = append(scenario.Root.Children,
		Node{
			Name: "cursor-container", Kind: domain.EntryDirectory,
			Children: []Node{
				{
					Name: "target", Kind: domain.EntryDirectory,
					Children: []Node{
						{
							Name: "child-dir", Kind: domain.EntryDirectory,
							Children: []Node{
								{Name: "old-a", Kind: domain.EntryFile},
								{Name: "old-b", Kind: domain.EntryFile},
							},
						},
						{Name: "old-file", Kind: domain.EntryFile},
					},
				},
				{Name: "file", Kind: domain.EntryFile, Data: []byte("old-file")},
				{Name: "link", Kind: domain.EntrySymlink, LinkTarget: "/cursor-target"},
				{Name: "sibling", Kind: domain.EntryDirectory},
			},
		},
		Node{
			Name: "cursor-target", Kind: domain.EntryDirectory,
			Children: []Node{
				{Name: "target-a", Kind: domain.EntryFile},
				{Name: "target-b", Kind: domain.EntryFile},
			},
		},
		Node{
			Name: "cursor-other-target", Kind: domain.EntryDirectory,
			Children: []Node{
				{Name: "other-target-a", Kind: domain.EntryFile},
				{Name: "other-target-b", Kind: domain.EntryFile},
			},
		},
		Node{
			Name: "cursor-unrelated", Kind: domain.EntryDirectory,
			Children: []Node{
				{Name: "unrelated-a", Kind: domain.EntryFile},
				{Name: "unrelated-b", Kind: domain.EntryFile},
			},
		},
	)
	return scenario
}

func faultError(code domain.Code) *domain.OpError {
	return &domain.OpError{
		Code:    code,
		Message: "injected fault",
		Retry:   domain.RetryAdvice{Kind: domain.RetryNever},
		Effect:  domain.EffectNone,
	}
}

func canonicalPathPointer(path domain.CanonicalPath) *domain.CanonicalPath {
	return &path
}

type observedManualClock struct {
	manual     *foundation.ManualClock
	registered chan time.Duration
	stopped    chan bool
	onNewTimer func()
}

type commitBlockingClock struct {
	manual  *foundation.ManualClock
	once    sync.Once
	entered chan struct{}
	release chan struct{}
}

func newCommitBlockingClock(start time.Time) *commitBlockingClock {
	return &commitBlockingClock{
		manual:  foundation.NewManualClock(start),
		entered: make(chan struct{}),
		release: make(chan struct{}),
	}
}

func (c *commitBlockingClock) Now() time.Time {
	c.once.Do(func() {
		close(c.entered)
		<-c.release
	})
	return c.manual.Now()
}

func (c *commitBlockingClock) NewTimer(duration time.Duration) foundation.Timer {
	return c.manual.NewTimer(duration)
}

func newObservedManualClock(start time.Time) *observedManualClock {
	return &observedManualClock{
		manual:     foundation.NewManualClock(start),
		registered: make(chan time.Duration, 16),
		stopped:    make(chan bool, 16),
	}
}

func (c *observedManualClock) Now() time.Time {
	return c.manual.Now()
}

func (c *observedManualClock) NewTimer(duration time.Duration) foundation.Timer {
	timer := c.manual.NewTimer(duration)
	if c.onNewTimer != nil {
		c.onNewTimer()
	}
	c.registered <- duration
	return &observedTimer{Timer: timer, stopped: c.stopped}
}

func (c *observedManualClock) Advance(duration time.Duration) {
	c.manual.Advance(duration)
}

type observedTimer struct {
	foundation.Timer
	stopped chan<- bool
}

func (t *observedTimer) Stop() bool {
	stopped := t.Timer.Stop()
	t.stopped <- stopped
	return stopped
}

type doneObservedContext struct {
	context.Context
	observed chan struct{}
}

func newDoneObservedContext(ctx context.Context, capacity int) *doneObservedContext {
	return &doneObservedContext{
		Context:  ctx,
		observed: make(chan struct{}, capacity),
	}
}

func (c *doneObservedContext) Done() <-chan struct{} {
	c.observed <- struct{}{}
	return c.Context.Done()
}

func requirePanicContaining(t *testing.T, text string, operation func()) {
	t.Helper()
	defer func() {
		value := recover()
		if value == nil {
			t.Fatalf("operation did not panic; want text %q", text)
		}
		if !strings.Contains(value.(string), text) {
			t.Fatalf("panic = %q, want text %q", value, text)
		}
	}()
	operation()
}
