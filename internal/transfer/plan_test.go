package transfer

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"testing"
	"time"

	"github.com/TyrantLucifer/awesome-mac-sftp/internal/domain"
	"github.com/TyrantLucifer/awesome-mac-sftp/internal/job"
	providerapi "github.com/TyrantLucifer/awesome-mac-sftp/internal/provider"
	"github.com/TyrantLucifer/awesome-mac-sftp/internal/provider/localfs"
)

const (
	planTestRequestID domain.RequestID = "req_aaaaaaaaaaaaaaaaaaaaaaaaaa"
	planTestJobID     domain.JobID     = "job_aaaaaaaaaaaaaaaaaaaaaaaaaa"
	planTestEventID   domain.EventID   = "evt_aaaaaaaaaaaaaaaaaaaaaaaaaa"
	planTestPlanID                     = "plan_aaaaaaaaaaaaaaaaaaaaaaaaaa"
)

func TestCaptureAndFreezeCopyOwnImmutableFileReferenceAndPolicy(t *testing.T) {
	sourceRoot := t.TempDir()
	destinationRoot := t.TempDir()
	if err := os.WriteFile(filepath.Join(sourceRoot, "source.txt"), []byte("payload"), 0o600); err != nil {
		t.Fatal(err)
	}
	source := newPlanTestProvider(t, "ep_aaaaaaaaaaaaaaaaaaaaaaaaaa", sourceRoot, domain.EndpointLocal)
	destination := newPlanTestProvider(t, "ep_bbbbbbbbbbbbbbbbbbbbbbbbbb", destinationRoot, domain.EndpointLocal)
	planner := NewPlanner(MapResolver{
		source.Descriptor().ID:      source,
		destination.Descriptor().ID: destination,
	})

	sourceLocation := normalizePlanTest(t, source, "/source.txt")
	reference, err := planner.Capture(context.Background(), sourceLocation)
	if err != nil {
		t.Fatalf("Capture(): %v", err)
	}
	if reference.Kind != domain.EntryFile || reference.Fingerprint.Strength() == domain.FingerprintWeak {
		t.Fatalf("Capture() = %#v, want a fingerprinted file", reference)
	}
	intent := Intent{
		Clipboard:            ClipboardCopy,
		Source:               reference,
		DestinationDirectory: normalizePlanTest(t, destination, "/"),
		Name:                 "copied.txt",
		ConflictPolicy:       ConflictAsk,
	}
	plan, create, err := planner.FreezeCopy(context.Background(), FreezeRequest{
		Intent:    intent,
		RequestID: planTestRequestID,
		PlanID:    planTestPlanID,
		JobID:     planTestJobID,
		EventID:   planTestEventID,
		Now:       time.Unix(1_800_000_000, 0),
	})
	if err != nil {
		t.Fatalf("FreezeCopy(): %v", err)
	}
	if plan.Kind != OperationCopy || plan.Route != RouteLocal || plan.Verification != VerifySHA256 || plan.BufferBytes != DefaultBufferBytes {
		t.Fatalf("Plan = %#v", plan)
	}
	if plan.Part.EndpointID != plan.Final.EndpointID || filepath.Dir(string(plan.Part.Path)) != filepath.Dir(string(plan.Final.Path)) {
		t.Fatalf("part/final are not in one directory: part=%#v final=%#v", plan.Part, plan.Final)
	}
	if plan.Part.Path == plan.Final.Path || plan.Final.Path != "/copied.txt" {
		t.Fatalf("part/final = %q/%q", plan.Part.Path, plan.Final.Path)
	}
	if create.InitialState != job.StateQueued || len(create.Steps) != 3 || create.SourceJSON == "" || create.DestinationJSON == nil {
		t.Fatalf("CreateRequest = %#v", create)
	}

	// Caller-owned pointer fields and later UI navigation cannot rewrite a
	// frozen plan or its canonical persistence payload.
	before := plan
	beforeSourceJSON := create.SourceJSON
	*intent.Source.Fingerprint.Size = 999
	intent.Source.Location.Path = "/different"
	intent.DestinationDirectory.Path = "/different"
	intent.Name = "different"
	if !reflect.DeepEqual(plan, before) || create.SourceJSON != beforeSourceJSON {
		t.Fatalf("frozen plan changed after caller mutation: before=%#v after=%#v", before, plan)
	}
}

func TestFreezeCopyRejectsStaleSourceAndUnsafeDestinationCapability(t *testing.T) {
	sourceRoot := t.TempDir()
	destinationRoot := t.TempDir()
	if err := os.WriteFile(filepath.Join(sourceRoot, "source"), []byte("first"), 0o600); err != nil {
		t.Fatal(err)
	}
	source := newPlanTestProvider(t, "ep_aaaaaaaaaaaaaaaaaaaaaaaaaa", sourceRoot, domain.EndpointLocal)
	destination := newPlanTestProvider(t, "ep_bbbbbbbbbbbbbbbbbbbbbbbbbb", destinationRoot, domain.EndpointLocal)
	planner := NewPlanner(MapResolver{source.Descriptor().ID: source, destination.Descriptor().ID: destination})
	reference, err := planner.Capture(context.Background(), normalizePlanTest(t, source, "/source"))
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(sourceRoot, "source"), []byte("changed-size"), 0o600); err != nil {
		t.Fatal(err)
	}
	_, _, err = planner.FreezeCopy(context.Background(), validFreezeRequest(reference, normalizePlanTest(t, destination, "/")))
	if !domain.IsCode(err, domain.CodeConflict) {
		t.Fatalf("stale source error = %v, want conflict", err)
	}

	readOnly := readOnlyProvider{Provider: destination}
	planner = NewPlanner(MapResolver{source.Descriptor().ID: source, destination.Descriptor().ID: readOnly})
	reference, err = planner.Capture(context.Background(), normalizePlanTest(t, source, "/source"))
	if err != nil {
		t.Fatal(err)
	}
	_, _, err = planner.FreezeCopy(context.Background(), validFreezeRequest(reference, normalizePlanTest(t, destination, "/")))
	if !domain.IsCode(err, domain.CodeUnsupported) {
		t.Fatalf("read-only destination error = %v, want unsupported", err)
	}
}

func TestFreezeCopyRoutesAndConflictStates(t *testing.T) {
	tests := []struct {
		name       string
		sourceKind domain.EndpointKind
		destKind   domain.EndpointKind
		policy     ConflictPolicy
		confirmed  bool
		final      bool
		route      Route
		state      job.State
	}{
		{name: "local", sourceKind: domain.EndpointLocal, destKind: domain.EndpointLocal, policy: ConflictAsk, route: RouteLocal, state: job.StateQueued},
		{name: "upload", sourceKind: domain.EndpointLocal, destKind: domain.EndpointSSH, policy: ConflictAsk, route: RouteSFTPRelay, state: job.StateQueued},
		{name: "download", sourceKind: domain.EndpointSSH, destKind: domain.EndpointLocal, policy: ConflictAsk, route: RouteSFTPRelay, state: job.StateQueued},
		{name: "remote relay", sourceKind: domain.EndpointSSH, destKind: domain.EndpointSSH, policy: ConflictAsk, route: RouteSFTPRelay, state: job.StateQueued},
		{name: "ask conflict", sourceKind: domain.EndpointLocal, destKind: domain.EndpointLocal, policy: ConflictAsk, final: true, route: RouteLocal, state: job.StateAwaitingConfirmation},
		{name: "confirmed overwrite", sourceKind: domain.EndpointLocal, destKind: domain.EndpointLocal, policy: ConflictOverwrite, confirmed: true, final: true, route: RouteLocal, state: job.StateQueued},
		{name: "auto rename", sourceKind: domain.EndpointLocal, destKind: domain.EndpointLocal, policy: ConflictAutoRename, final: true, route: RouteLocal, state: job.StateQueued},
		{name: "skip", sourceKind: domain.EndpointLocal, destKind: domain.EndpointLocal, policy: ConflictSkip, final: true, route: RouteLocal, state: job.StateQueued},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			sourceRoot := t.TempDir()
			destinationRoot := t.TempDir()
			if err := os.WriteFile(filepath.Join(sourceRoot, "source"), []byte("data"), 0o600); err != nil {
				t.Fatal(err)
			}
			if test.final {
				if err := os.WriteFile(filepath.Join(destinationRoot, "final"), []byte("existing"), 0o600); err != nil {
					t.Fatal(err)
				}
			}
			source := newPlanTestProvider(t, "ep_aaaaaaaaaaaaaaaaaaaaaaaaaa", sourceRoot, test.sourceKind)
			destination := newPlanTestProvider(t, "ep_bbbbbbbbbbbbbbbbbbbbbbbbbb", destinationRoot, test.destKind)
			planner := NewPlanner(MapResolver{source.Descriptor().ID: source, destination.Descriptor().ID: destination})
			reference, err := planner.Capture(context.Background(), normalizePlanTest(t, source, "/source"))
			if err != nil {
				t.Fatal(err)
			}
			request := validFreezeRequest(reference, normalizePlanTest(t, destination, "/"))
			request.Intent.Name = "final"
			request.Intent.ConflictPolicy = test.policy
			request.Intent.ConflictConfirmed = test.confirmed
			plan, create, err := planner.FreezeCopy(context.Background(), request)
			if err != nil {
				t.Fatalf("FreezeCopy(): %v", err)
			}
			if plan.Route != test.route || create.InitialState != test.state {
				t.Fatalf("route/state = %s/%s, want %s/%s", plan.Route, create.InitialState, test.route, test.state)
			}
		})
	}
}

func validFreezeRequest(source FileRef, destination domain.Location) FreezeRequest {
	return FreezeRequest{
		Intent: Intent{
			Clipboard:            ClipboardCopy,
			Source:               source,
			DestinationDirectory: destination,
			Name:                 "final",
			ConflictPolicy:       ConflictAsk,
		},
		RequestID: planTestRequestID,
		PlanID:    planTestPlanID,
		JobID:     planTestJobID,
		EventID:   planTestEventID,
		Now:       time.Unix(1_800_000_000, 0),
	}
}

type endpointKindProvider struct {
	*localfs.Provider
	descriptor domain.Endpoint
}

func (p *endpointKindProvider) Descriptor() domain.Endpoint { return p.descriptor }

func newPlanTestProvider(t *testing.T, endpointID domain.EndpointID, root string, kind domain.EndpointKind) *endpointKindProvider {
	t.Helper()
	base, err := localfs.New(localfs.Config{
		Endpoint:  domain.Endpoint{ID: endpointID, Kind: domain.EndpointLocal, DisplayName: string(endpointID)},
		SessionID: "sess_aaaaaaaaaaaaaaaaaaaaaaaaaa",
		Root:      root,
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = base.Close() })
	descriptor := base.Descriptor()
	descriptor.Kind = kind
	if kind == domain.EndpointSSH {
		descriptor.SSHHostAlias = "fixture"
	}
	return &endpointKindProvider{Provider: base, descriptor: descriptor}
}

func normalizePlanTest(t *testing.T, implementation providerapi.Provider, input string) domain.Location {
	t.Helper()
	location, err := implementation.Normalize(context.Background(), domain.NormalizeRequest{EndpointID: implementation.Descriptor().ID, Input: input})
	if err != nil {
		t.Fatal(err)
	}
	return location
}

type readOnlyProvider struct{ providerapi.Provider }

func (p readOnlyProvider) Snapshot(ctx context.Context) (domain.EndpointSnapshot, error) {
	snapshot, err := p.Provider.Snapshot(ctx)
	if err != nil {
		return domain.EndpointSnapshot{}, err
	}
	snapshot.Capabilities.Items = []domain.Capability{{Name: "read", Version: 1}}
	return snapshot, nil
}

func requirePlanError(t *testing.T, err error, code domain.Code) *domain.OpError {
	t.Helper()
	var operationError *domain.OpError
	if !errors.As(err, &operationError) || operationError.Code != code {
		t.Fatalf("error = %v, want %s", err, code)
	}
	return operationError
}
