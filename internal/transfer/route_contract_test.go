package transfer

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/TyrantLucifer/awesome-mac-sftp/internal/domain"
	"github.com/TyrantLucifer/awesome-mac-sftp/internal/job"
	providerapi "github.com/TyrantLucifer/awesome-mac-sftp/internal/provider"
	"github.com/TyrantLucifer/awesome-mac-sftp/internal/testkit"
)

func TestPlannerFreezesUnifiedRouteEvidenceBeforeDurableJobCreation(t *testing.T) {
	tests := []struct {
		name            string
		freeze          func(*testing.T) Plan
		selectedRoute   string
		selectedReason  string
		candidateRoute  string
		candidateReason string
	}{
		{
			name:           "same endpoint atomic rename",
			freeze:         freezeSameEndpointRouteContractPlan(ClipboardCut, false),
			selectedRoute:  "atomic_rename",
			selectedReason: "same_endpoint_atomic_rename",
		},
		{
			name:            "same endpoint bounded relay records unavailable server copy",
			freeze:          freezeSameEndpointRouteContractPlan(ClipboardCopy, false),
			selectedRoute:   "sftp_relay",
			selectedReason:  "bounded_relay_default",
			candidateRoute:  "sftp_server_copy",
			candidateReason: "server_copy_unavailable",
		},
		{
			name:           "stage four helper same host",
			freeze:         freezeSameEndpointRouteContractPlan(ClipboardCopy, true),
			selectedRoute:  "helper_same_host",
			selectedReason: "helper_same_host_selected",
		},
		{
			name:           "cross endpoint bounded local relay",
			freeze:         freezeCrossEndpointRouteContractPlan,
			selectedRoute:  "sftp_relay",
			selectedReason: "bounded_relay_default",
		},
		{
			name:            "production level two direct remains closed",
			freeze:          freezeCrossEndpointRouteContractPlan,
			selectedRoute:   "sftp_relay",
			selectedReason:  "bounded_relay_default",
			candidateRoute:  "level2_direct",
			candidateReason: "production_distribution_closed",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			plan := test.freeze(t)
			evidence := routeEvidenceJSON(t, plan)
			if version := jsonUint16(t, evidence, "version"); version != 1 {
				t.Fatalf("route evidence version = %d, want 1", version)
			}
			selected := jsonObject(t, evidence, "selected")
			if route := jsonString(t, selected, "route"); route != test.selectedRoute {
				t.Fatalf("selected route = %q, want %q", route, test.selectedRoute)
			}
			if reason := jsonString(t, selected, "reason"); reason != test.selectedReason {
				t.Fatalf("selected reason = %q, want %q", reason, test.selectedReason)
			}
			integrity := jsonObject(t, evidence, "integrity")
			if policy := jsonString(t, integrity, "policy"); policy != "strong" {
				t.Fatalf("integrity policy = %q, want strong", policy)
			}
			if test.candidateRoute != "" {
				assertRouteCandidateJSON(t, evidence, test.candidateRoute, test.candidateReason)
			}
		})
	}
}

func TestValidateExecutionRejectsTamperedRouteEvidence(t *testing.T) {
	original := freezeCrossEndpointRouteContractPlan(t)
	tests := []struct {
		name   string
		tamper func(*RouteEvidence)
	}{
		{name: "version", tamper: func(evidence *RouteEvidence) { evidence.Version++ }},
		{name: "selected route", tamper: func(evidence *RouteEvidence) { evidence.Selected.Route = RouteLevel2Direct }},
		{name: "selected reason", tamper: func(evidence *RouteEvidence) { evidence.Selected.Reason = ReasonProductionDistributionClosed }},
		{name: "integrity", tamper: func(evidence *RouteEvidence) { evidence.Integrity.Policy = IntegrityBaseline }},
		{name: "part", tamper: func(evidence *RouteEvidence) { evidence.Part.Path = "/foreign.part" }},
		{name: "candidate eligibility", tamper: func(evidence *RouteEvidence) { evidence.Candidates[0].Eligible = true }},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			plan := original
			evidence := cloneRouteEvidence(*original.RouteEvidence)
			plan.RouteEvidence = &evidence
			test.tamper(plan.RouteEvidence)
			if err := validateExecution(plan); err == nil {
				t.Fatal("validateExecution accepted tampered route evidence")
			}
		})
	}
}

func TestPlannerSelectsDeclaredServerCopyOnlyWithCapabilityAndFacet(t *testing.T) {
	root := t.TempDir()
	if err := os.Mkdir(filepath.Join(root, "destination"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "source"), []byte("declared server copy"), 0o600); err != nil {
		t.Fatal(err)
	}
	base := newPlanTestProvider(t, "ep_aaaaaaaaaaaaaaaaaaaaaaaaaa", root, domain.EndpointSSH)
	implementation := &routeServerCopyProvider{endpointKindProvider: base, root: root, advertise: true}
	planner := NewPlanner(MapResolver{implementation.Descriptor().ID: implementation})
	reference, err := planner.Capture(context.Background(), normalizePlanTest(t, implementation, "/source"))
	if err != nil {
		t.Fatal(err)
	}
	plan, _, err := planner.FreezeCopy(context.Background(), validFreezeRequest(reference, normalizePlanTest(t, implementation, "/destination")))
	if err != nil {
		t.Fatal(err)
	}
	evidence := routeEvidenceJSON(t, plan)
	selected := jsonObject(t, evidence, "selected")
	if route := jsonString(t, selected, "route"); route != "sftp_server_copy" {
		t.Fatalf("selected route = %q, want sftp_server_copy", route)
	}
	if reason := jsonString(t, selected, "reason"); reason != "server_copy_capability_selected" {
		t.Fatalf("selected reason = %q, want server_copy_capability_selected", reason)
	}
	if boundary := jsonString(t, evidence, "downgrade_boundary"); boundary != "before_target_write_part_absent" {
		t.Fatalf("downgrade boundary = %q", boundary)
	}
	assertRouteCandidateJSON(t, evidence, "sftp_relay", "bounded_relay_default")
	if implementation.copyCalls != 0 {
		t.Fatalf("Planner executed server copy %d time(s)", implementation.copyCalls)
	}
}

func TestWorkerStagesServerCopyPartThenVerifiesAndCommits(t *testing.T) {
	root := t.TempDir()
	if err := os.Mkdir(filepath.Join(root, "destination"), 0o700); err != nil {
		t.Fatal(err)
	}
	payload := []byte("server-side staged payload")
	if err := os.WriteFile(filepath.Join(root, "source"), payload, 0o600); err != nil {
		t.Fatal(err)
	}
	base := newPlanTestProvider(t, "ep_aaaaaaaaaaaaaaaaaaaaaaaaaa", root, domain.EndpointSSH)
	implementation := &routeServerCopyProvider{endpointKindProvider: base, root: root, advertise: true}
	resolver := MapResolver{implementation.Descriptor().ID: implementation}
	planner := NewPlanner(resolver)
	reference, err := planner.Capture(context.Background(), normalizePlanTest(t, implementation, "/source"))
	if err != nil {
		t.Fatal(err)
	}
	plan, _, err := planner.FreezeCopy(context.Background(), validFreezeRequest(reference, normalizePlanTest(t, implementation, "/destination")))
	if err != nil {
		t.Fatal(err)
	}
	result, err := NewWorker(resolver, &volatileJournal{}).Execute(context.Background(), plan, nil)
	if err != nil {
		t.Fatal(err)
	}
	if result.Outcome != OutcomeCompleted || result.Final != plan.Final || result.Bytes != uint64(len(payload)) {
		t.Fatalf("result = %+v", result)
	}
	if implementation.copyCalls != 1 {
		t.Fatalf("server copy calls = %d, want 1", implementation.copyCalls)
	}
	actual, err := readRouteTestFile(context.Background(), implementation, plan.Final)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(actual, payload) {
		t.Fatalf("final payload = %q, want %q", actual, payload)
	}
	if _, err := implementation.Stat(context.Background(), providerapi.StatRequest{Location: plan.Part}); !domain.IsCode(err, domain.CodeNotFound) {
		t.Fatalf("part still exists after commit: %v", err)
	}
}

func TestPlannerRequiresBothServerCopyCapabilityAndFacet(t *testing.T) {
	tests := []struct {
		name     string
		provider func(*endpointKindProvider, string) providerapi.Provider
	}{
		{
			name: "capability_without_facet",
			provider: func(base *endpointKindProvider, _ string) providerapi.Provider {
				return &routeServerCopyCapabilityOnlyProvider{endpointKindProvider: base}
			},
		},
		{
			name: "facet_without_capability",
			provider: func(base *endpointKindProvider, root string) providerapi.Provider {
				return &routeServerCopyProvider{endpointKindProvider: base, root: root}
			},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			root := t.TempDir()
			if err := os.Mkdir(filepath.Join(root, "destination"), 0o700); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(root, "source"), []byte("strict gate"), 0o600); err != nil {
				t.Fatal(err)
			}
			implementation := test.provider(newPlanTestProvider(t, "ep_aaaaaaaaaaaaaaaaaaaaaaaaaa", root, domain.EndpointSSH), root)
			planner := NewPlanner(MapResolver{implementation.Descriptor().ID: implementation})
			reference, err := planner.Capture(context.Background(), normalizePlanTest(t, implementation, "/source"))
			if err != nil {
				t.Fatal(err)
			}
			plan, _, err := planner.FreezeCopy(context.Background(), validFreezeRequest(reference, normalizePlanTest(t, implementation, "/destination")))
			if err != nil {
				t.Fatal(err)
			}
			if plan.Route != RouteSFTPRelay || plan.ServerCopy != nil {
				t.Fatalf("route = %q, binding = %+v", plan.Route, plan.ServerCopy)
			}
			if plan.RouteEvidence == nil || plan.RouteEvidence.Candidates[0].Reason != ReasonServerCopyUnavailable {
				t.Fatalf("route evidence = %+v", plan.RouteEvidence)
			}
		})
	}
}

func TestPlannerRejectsUnknownOrDriftedServerCopyDeclaration(t *testing.T) {
	tests := []struct {
		name      string
		configure func(*routeServerCopyProvider)
	}{
		{name: "unknown_capability_version", configure: func(provider *routeServerCopyProvider) { provider.capabilityVersion = 2 }},
		{name: "revision_drift_during_freeze", configure: func(provider *routeServerCopyProvider) { provider.driftDestination = true }},
		{name: "same_revision_destination_omits_capability", configure: func(provider *routeServerCopyProvider) { provider.omitDestinationCapability = true }},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			root := t.TempDir()
			if err := os.Mkdir(filepath.Join(root, "destination"), 0o700); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(root, "source"), []byte("versioned gate"), 0o600); err != nil {
				t.Fatal(err)
			}
			base := newPlanTestProvider(t, "ep_aaaaaaaaaaaaaaaaaaaaaaaaaa", root, domain.EndpointSSH)
			implementation := &routeServerCopyProvider{endpointKindProvider: base, root: root, advertise: true}
			test.configure(implementation)
			planner := NewPlanner(MapResolver{implementation.Descriptor().ID: implementation})
			reference, err := planner.Capture(context.Background(), normalizePlanTest(t, implementation, "/source"))
			if err != nil {
				t.Fatal(err)
			}
			plan, _, err := planner.FreezeCopy(context.Background(), validFreezeRequest(reference, normalizePlanTest(t, implementation, "/destination")))
			if err != nil {
				t.Fatal(err)
			}
			if plan.Route != RouteSFTPRelay || plan.ServerCopy != nil {
				t.Fatalf("route = %q, binding = %+v", plan.Route, plan.ServerCopy)
			}
		})
	}
}

func TestValidateExecutionRejectsTamperedServerCopyBinding(t *testing.T) {
	plan, _ := newRouteServerCopyPlan(t, nil)
	tests := []struct {
		name   string
		mutate func(*Plan)
	}{
		{name: "max_bytes", mutate: func(plan *Plan) { plan.ServerCopy.MaxBytes-- }},
		{name: "capability_name", mutate: func(plan *Plan) { plan.ServerCopy.Capability.Name = "read" }},
		{name: "capability_version", mutate: func(plan *Plan) { plan.ServerCopy.Capability.Version = 0 }},
		{name: "missing_binding", mutate: func(plan *Plan) { plan.ServerCopy = nil }},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			tampered := plan
			binding := *plan.ServerCopy
			tampered.ServerCopy = &binding
			test.mutate(&tampered)
			freezeRouteEvidence(&tampered)
			if err := validateExecution(tampered); err == nil {
				t.Fatal("tampered server-copy binding was accepted")
			}
		})
	}
}

func TestWorkerServerCopyResponseLossAdoptsOnlyVerifiedPart(t *testing.T) {
	plan, implementation := newRouteServerCopyPlan(t, errors.New("copy response lost"))
	resolver := MapResolver{implementation.Descriptor().ID: implementation}
	result, err := NewWorker(resolver, &volatileJournal{}).Execute(context.Background(), plan, nil)
	if err != nil {
		t.Fatal(err)
	}
	if result.Outcome != OutcomeCompleted || implementation.copyCalls != 1 {
		t.Fatalf("result = %+v, calls = %d", result, implementation.copyCalls)
	}
}

func TestWorkerFallsBackToRelayOnlyAfterServerCopyProvesPartAbsent(t *testing.T) {
	plan, implementation := newRouteServerCopyPlan(t, nil)
	implementation.failBeforeWrite = true
	journal := &volatileJournal{}
	resolver := MapResolver{implementation.Descriptor().ID: implementation}
	result, err := NewWorker(resolver, journal).Execute(context.Background(), plan, nil)
	if err != nil {
		t.Fatal(err)
	}
	if result.Outcome != OutcomeCompleted || implementation.copyCalls != 1 {
		t.Fatalf("result = %+v, server copy calls = %d", result, implementation.copyCalls)
	}
	checkpoint, err := journal.Load(context.Background(), plan.JobID)
	if err != nil {
		t.Fatal(err)
	}
	if checkpoint == nil || checkpoint.ActualRoute != RouteSFTPRelay || checkpoint.DowngradedFrom != RouteSFTPServerCopy ||
		checkpoint.RouteReason != ReasonServerCopyFailedBeforeWrite {
		t.Fatalf("downgrade checkpoint = %+v", checkpoint)
	}
	if _, err := readRouteTestFile(context.Background(), implementation, plan.Final); err != nil {
		t.Fatal(err)
	}
}

func TestWorkerRestartResumesPersistedRelayDowngradeWithoutRetryingServerCopy(t *testing.T) {
	plan, implementation := newRouteServerCopyPlan(t, nil)
	checkpoint := Checkpoint{
		JobID:             plan.JobID,
		Phase:             PhasePrepared,
		SourceFingerprint: cloneFingerprint(plan.Source.Fingerprint),
		Part:              plan.Part,
		Final:             plan.Final,
		ActualRoute:       RouteSFTPRelay,
		DowngradedFrom:    RouteSFTPServerCopy,
		RouteReason:       ReasonServerCopyFailedBeforeWrite,
	}
	journal := &volatileJournal{checkpoint: &checkpoint}
	resolver := MapResolver{implementation.Descriptor().ID: implementation}
	result, err := NewWorker(resolver, journal).Execute(context.Background(), plan, nil)
	if err != nil {
		t.Fatal(err)
	}
	if result.Outcome != OutcomeCompleted || implementation.copyCalls != 0 {
		t.Fatalf("result = %+v, server copy calls = %d", result, implementation.copyCalls)
	}
}

func TestCheckpointRouteIdentityRejectsTamperedStableReason(t *testing.T) {
	plan, _ := newRouteServerCopyPlan(t, nil)
	checkpoint := Checkpoint{
		JobID:             plan.JobID,
		Phase:             PhasePrepared,
		SourceFingerprint: cloneFingerprint(plan.Source.Fingerprint),
		Part:              plan.Part,
		Final:             plan.Final,
		ActualRoute:       plan.Route,
		RouteReason:       ReasonServerCopyFailedBeforeWrite,
	}
	if checkpointMatchesPlan(checkpoint, plan) {
		t.Fatal("checkpoint with a forged selected-route reason matched the frozen plan")
	}
}

func TestWorkerDoesNotFallbackWhenServerCopyPartStateIsUnknown(t *testing.T) {
	plan, implementation := newRouteServerCopyPlan(t, nil)
	implementation.failBeforeWrite = true
	implementation.unknownPartAfterFailure = true
	journal := &volatileJournal{}
	resolver := MapResolver{implementation.Descriptor().ID: implementation}
	if _, err := NewWorker(resolver, journal).Execute(context.Background(), plan, nil); !domain.IsCode(err, domain.CodePermissionDenied) {
		t.Fatalf("error = %v, want permission_denied", err)
	}
	checkpoint, err := journal.Load(context.Background(), plan.JobID)
	if err != nil {
		t.Fatal(err)
	}
	if checkpoint == nil || checkpoint.ActualRoute != RouteSFTPServerCopy || checkpoint.DowngradedFrom != "" {
		t.Fatalf("unknown part state silently downgraded: %+v", checkpoint)
	}
	if _, statErr := implementation.endpointKindProvider.Stat(context.Background(), providerapi.StatRequest{Location: plan.Final}); !domain.IsCode(statErr, domain.CodeNotFound) {
		t.Fatalf("final was published after unknown part state: %v", statErr)
	}
}

func TestServerCopyAndRelayShareCommitConflictContract(t *testing.T) {
	for _, policy := range []ConflictPolicy{ConflictAsk, ConflictOverwrite, ConflictSkip, ConflictAutoRename} {
		t.Run(string(policy), func(t *testing.T) {
			relay := executeRouteConflictContract(t, false, policy)
			serverCopy := executeRouteConflictContract(t, true, policy)
			if !reflect.DeepEqual(serverCopy, relay) {
				t.Fatalf("server-copy result = %+v, relay result = %+v", serverCopy, relay)
			}
		})
	}
}

type routeConflictObservation struct {
	Outcome      Outcome
	Bytes        uint64
	SHA256       string
	FinalPath    domain.CanonicalPath
	PartRetained bool
	FinalPayload string
}

func executeRouteConflictContract(t *testing.T, serverCopy bool, policy ConflictPolicy) routeConflictObservation {
	t.Helper()
	root := t.TempDir()
	if err := os.Mkdir(filepath.Join(root, "destination"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "source"), []byte("shared source bytes"), 0o600); err != nil {
		t.Fatal(err)
	}
	base := newPlanTestProvider(t, "ep_aaaaaaaaaaaaaaaaaaaaaaaaaa", root, domain.EndpointSSH)
	var implementation providerapi.Provider = base
	if serverCopy {
		implementation = &routeServerCopyProvider{endpointKindProvider: base, root: root, advertise: true}
	}
	resolver := MapResolver{implementation.Descriptor().ID: implementation}
	planner := NewPlanner(resolver)
	reference, err := planner.Capture(context.Background(), normalizePlanTest(t, implementation, "/source"))
	if err != nil {
		t.Fatal(err)
	}
	request := validFreezeRequest(reference, normalizePlanTest(t, implementation, "/destination"))
	request.Intent.Name = "copied"
	request.Intent.ConflictPolicy = policy
	request.Intent.ConflictConfirmed = policy == ConflictOverwrite
	plan, _, err := planner.FreezeCopy(context.Background(), request)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "destination", "copied"), []byte("commit race"), 0o600); err != nil {
		t.Fatal(err)
	}
	result, err := NewWorker(resolver, &volatileJournal{}).Execute(context.Background(), plan, nil)
	if err != nil {
		t.Fatal(err)
	}
	payload, err := readRouteTestFile(context.Background(), implementation, result.Final)
	if err != nil {
		t.Fatal(err)
	}
	return routeConflictObservation{
		Outcome: result.Outcome, Bytes: result.Bytes, SHA256: result.SHA256, FinalPath: result.Final.Path,
		PartRetained: result.PartRetained, FinalPayload: string(payload),
	}
}

func TestManagerJobViewPersistsPlannedAndActualRouteAfterSafeDowngrade(t *testing.T) {
	root := t.TempDir()
	if err := os.Mkdir(filepath.Join(root, "destination"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "source"), []byte("durable downgrade"), 0o600); err != nil {
		t.Fatal(err)
	}
	base := newPlanTestProvider(t, "ep_aaaaaaaaaaaaaaaaaaaaaaaaaa", root, domain.EndpointSSH)
	implementation := &routeServerCopyProvider{endpointKindProvider: base, root: root, advertise: true, failBeforeWrite: true}
	resolver := MapResolver{implementation.Descriptor().ID: implementation}
	store, database := openTransferStore(t, context.Background(), testDatabasePath(t), true)
	t.Cleanup(func() { _ = database.Close() })
	manager, err := NewManager(ManagerConfig{
		Store: store, Resolver: resolver, Generator: &testkit.SequenceGenerator{},
		Now: func() time.Time { return time.Unix(1_800_000_000, 0) }, MaxConcurrent: 1,
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(manager.Close)
	if err := manager.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	reference, err := manager.Capture(context.Background(), normalizePlanTest(t, implementation, "/source"))
	if err != nil {
		t.Fatal(err)
	}
	created, err := manager.CreateCopy(context.Background(), Intent{
		Clipboard: ClipboardCopy, Source: reference, DestinationDirectory: normalizePlanTest(t, implementation, "/destination"),
		Name: "copied", ConflictPolicy: ConflictAsk,
	})
	if err != nil {
		t.Fatal(err)
	}
	completed := waitForTerminal(t, manager, created.JobID)
	if completed.State != job.StateCompleted {
		t.Fatalf("state = %q", completed.State)
	}
	views, err := manager.JobViews(context.Background(), 10)
	if err != nil || len(views) != 1 {
		t.Fatalf("views = %+v, error = %v", views, err)
	}
	view := views[0]
	if view.PlannedRoute != RouteSFTPServerCopy || view.Route != RouteSFTPRelay ||
		view.DowngradedFrom != RouteSFTPServerCopy || view.RouteReason != ReasonServerCopyFailedBeforeWrite {
		t.Fatalf("durable route view = %+v", view)
	}
	events, err := store.ListEvents(context.Background(), created.JobID, 0, 32)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	createdEvidenceFound := false
	for _, event := range events {
		if event.Kind == "job_created" && strings.Contains(event.PayloadJSON, `"selected_route":"sftp_server_copy"`) &&
			strings.Contains(event.PayloadJSON, `"route_reason":"server_copy_capability_selected"`) &&
			strings.Contains(event.PayloadJSON, `"integrity_policy":"strong"`) &&
			strings.Contains(event.PayloadJSON, `"downgrade_boundary":"before_target_write_part_absent"`) &&
			strings.Contains(event.PayloadJSON, `"progress_semantics":"phase_only"`) {
			createdEvidenceFound = true
		}
		if event.Kind == "job_verifying" && strings.Contains(event.PayloadJSON, `"planned_route":"sftp_server_copy"`) &&
			strings.Contains(event.PayloadJSON, `"actual_route":"sftp_relay"`) &&
			strings.Contains(event.PayloadJSON, `"route_reason":"server_copy_failed_part_absent"`) {
			found = true
		}
	}
	if !found {
		t.Fatalf("durable Log events do not disclose route downgrade: %+v", events)
	}
	if !createdEvidenceFound {
		t.Fatalf("durable Log creation event does not disclose selected route evidence: %+v", events)
	}
}

func TestWorkerServerCopyRejectsCorruptPartWithoutPublishingFinal(t *testing.T) {
	plan, implementation := newRouteServerCopyPlan(t, nil)
	implementation.corrupt = true
	resolver := MapResolver{implementation.Descriptor().ID: implementation}
	_, err := NewWorker(resolver, &volatileJournal{}).Execute(context.Background(), plan, nil)
	if !domain.IsCode(err, domain.CodeIntegrityFailed) {
		t.Fatalf("error = %v, want integrity_failed", err)
	}
	if _, statErr := implementation.Stat(context.Background(), providerapi.StatRequest{Location: plan.Final}); !domain.IsCode(statErr, domain.CodeNotFound) {
		t.Fatalf("final was published after corrupt stage: %v", statErr)
	}
	if _, statErr := implementation.Stat(context.Background(), providerapi.StatRequest{Location: plan.Part}); statErr != nil {
		t.Fatalf("auditable part was not retained: %v", statErr)
	}
}

func TestWorkerServerCopyCancelBeforeStageDoesNotWrite(t *testing.T) {
	plan, implementation := newRouteServerCopyPlan(t, nil)
	resolver := MapResolver{implementation.Descriptor().ID: implementation}
	_, err := NewWorker(resolver, &volatileJournal{}).Execute(context.Background(), plan, ControlFunc(func(Checkpoint) ControlAction {
		return ControlCancel
	}))
	if !errors.Is(err, ErrCanceled) {
		t.Fatalf("error = %v, want canceled", err)
	}
	if implementation.copyCalls != 0 {
		t.Fatalf("server copy calls = %d, want 0", implementation.copyCalls)
	}
	if _, statErr := implementation.Stat(context.Background(), providerapi.StatRequest{Location: plan.Part}); !domain.IsCode(statErr, domain.CodeNotFound) {
		t.Fatalf("part was written after pre-stage cancel: %v", statErr)
	}
}

func TestServerCopyAndRelayShareCancellationContract(t *testing.T) {
	relay := observeRouteCancellationContract(t, false)
	serverCopy := observeRouteCancellationContract(t, true)
	if !reflect.DeepEqual(serverCopy, relay) {
		t.Fatalf("server-copy cancellation = %+v, relay cancellation = %+v", serverCopy, relay)
	}
}

func TestWorkerServerCopyPropagatesDurableCancelDuringStage(t *testing.T) {
	plan, implementation := newRouteServerCopyPlan(t, nil)
	implementation.stageStarted = make(chan struct{})
	implementation.blockStageUntilCanceled = true
	resolver := MapResolver{implementation.Descriptor().ID: implementation}
	var cancelRequested atomic.Bool
	done := make(chan error, 1)
	go func() {
		_, err := NewWorker(resolver, &volatileJournal{}).Execute(context.Background(), plan, ControlFunc(func(Checkpoint) ControlAction {
			if cancelRequested.Load() {
				return ControlCancel
			}
			return ControlContinue
		}))
		done <- err
	}()
	select {
	case <-implementation.stageStarted:
	case <-time.After(time.Second):
		t.Fatal("server copy did not begin")
	}
	cancelRequested.Store(true)
	select {
	case err := <-done:
		if !errors.Is(err, ErrCanceled) {
			t.Fatalf("error = %v, want canceled", err)
		}
	case <-time.After(500 * time.Millisecond):
		t.Fatal("durable cancel did not interrupt server copy")
	}
	if _, statErr := implementation.Stat(context.Background(), providerapi.StatRequest{Location: plan.Final}); !domain.IsCode(statErr, domain.CodeNotFound) {
		t.Fatalf("final was exposed after cancel: %v", statErr)
	}
	if payload, err := readRouteTestFile(context.Background(), implementation, plan.Source.Location); err != nil || string(payload) != "frozen server copy payload" {
		t.Fatalf("source after cancel = %q, error = %v", payload, err)
	}
}

func TestWorkerServerCopyContextCancellationNeverBecomesRelayDowngrade(t *testing.T) {
	plan, implementation := newRouteServerCopyPlan(t, nil)
	implementation.stageStarted = make(chan struct{})
	implementation.blockStageUntilCanceled = true
	journal := &volatileJournal{}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		_, err := NewWorker(MapResolver{implementation.Descriptor().ID: implementation}, journal).Execute(ctx, plan, nil)
		done <- err
	}()
	select {
	case <-implementation.stageStarted:
	case <-time.After(time.Second):
		t.Fatal("server copy did not begin")
	}
	cancel()
	if err := <-done; !errors.Is(err, context.Canceled) {
		t.Fatalf("error = %v, want context canceled", err)
	}
	checkpoint, err := journal.Load(context.Background(), plan.JobID)
	if err != nil {
		t.Fatal(err)
	}
	if checkpoint == nil || checkpoint.ActualRoute != RouteSFTPServerCopy || checkpoint.DowngradedFrom != "" {
		t.Fatalf("context cancellation silently downgraded route: %+v", checkpoint)
	}
}

func TestWorkerRejectsCheckpointThatDoesNotMatchFrozenRouteIdentity(t *testing.T) {
	plan, implementation := newRouteServerCopyPlan(t, nil)
	tampered := Checkpoint{
		JobID:             plan.JobID,
		Phase:             PhasePrepared,
		SourceFingerprint: cloneFingerprint(plan.Source.Fingerprint),
		Part:              childLocation(plan.DestinationDirectory, ".foreign.part"),
		Final:             plan.Final,
	}
	journal := &volatileJournal{checkpoint: &tampered}
	resolver := MapResolver{implementation.Descriptor().ID: implementation}
	if _, err := NewWorker(resolver, journal).Execute(context.Background(), plan, nil); err == nil {
		t.Fatal("checkpoint with a foreign part identity was accepted")
	}
	if implementation.copyCalls != 0 {
		t.Fatalf("server copy calls = %d, want 0", implementation.copyCalls)
	}
}

func cloneRouteEvidence(evidence RouteEvidence) RouteEvidence {
	evidence.Candidates = append([]RouteDecision(nil), evidence.Candidates...)
	return evidence
}

func freezeSameEndpointRouteContractPlan(clipboard ClipboardKind, helper bool) func(*testing.T) Plan {
	return func(t *testing.T) Plan {
		t.Helper()
		root := t.TempDir()
		if err := os.Mkdir(filepath.Join(root, "destination"), 0o700); err != nil {
			t.Fatal(err)
		}
		payload := []byte("unified route contract")
		if err := os.WriteFile(filepath.Join(root, "source"), payload, 0o600); err != nil {
			t.Fatal(err)
		}
		implementation := newPlanTestProvider(t, "ep_aaaaaaaaaaaaaaaaaaaaaaaaaa", root, domain.EndpointSSH)
		var routeProvider providerapi.Provider = implementation
		if clipboard == ClipboardCut {
			routeProvider = &atomicRenameProvider{endpointKindProvider: implementation, root: root}
		}
		resolver := MapResolver{routeProvider.Descriptor().ID: routeProvider}
		planner := NewPlanner(resolver)
		if helper {
			planner = NewPlannerWithSameHost(resolver, &recordingSameHostBackend{root: root, payload: payload})
		}
		reference, err := planner.Capture(context.Background(), normalizePlanTest(t, routeProvider, "/source"))
		if err != nil {
			t.Fatal(err)
		}
		request := validFreezeRequest(reference, normalizePlanTest(t, routeProvider, "/destination"))
		request.Intent.Clipboard = clipboard
		plan, _, err := planner.FreezeCopy(context.Background(), request)
		if err != nil {
			t.Fatal(err)
		}
		return plan
	}
}

func freezeCrossEndpointRouteContractPlan(t *testing.T) Plan {
	t.Helper()
	sourceRoot := t.TempDir()
	destinationRoot := t.TempDir()
	if err := os.WriteFile(filepath.Join(sourceRoot, "source"), []byte("bounded relay"), 0o600); err != nil {
		t.Fatal(err)
	}
	source := newPlanTestProvider(t, "ep_aaaaaaaaaaaaaaaaaaaaaaaaaa", sourceRoot, domain.EndpointSSH)
	destination := newPlanTestProvider(t, "ep_bbbbbbbbbbbbbbbbbbbbbbbbbb", destinationRoot, domain.EndpointSSH)
	planner := NewPlanner(MapResolver{
		source.Descriptor().ID:      source,
		destination.Descriptor().ID: destination,
	})
	reference, err := planner.Capture(context.Background(), normalizePlanTest(t, source, "/source"))
	if err != nil {
		t.Fatal(err)
	}
	plan, _, err := planner.FreezeCopy(context.Background(), validFreezeRequest(reference, normalizePlanTest(t, destination, "/")))
	if err != nil {
		t.Fatal(err)
	}
	return plan
}

func routeEvidenceJSON(t *testing.T, plan Plan) map[string]any {
	t.Helper()
	encoded, err := json.Marshal(plan)
	if err != nil {
		t.Fatal(err)
	}
	var document map[string]any
	if err := json.Unmarshal(encoded, &document); err != nil {
		t.Fatal(err)
	}
	raw, ok := document["route_evidence"]
	if !ok {
		t.Fatal("frozen plan has no route_evidence")
	}
	evidence, ok := raw.(map[string]any)
	if !ok {
		t.Fatalf("route_evidence = %#v, want object", raw)
	}
	return evidence
}

func assertRouteCandidateJSON(t *testing.T, evidence map[string]any, route, reason string) {
	t.Helper()
	raw, ok := evidence["candidates"].([]any)
	if !ok {
		t.Fatalf("route candidates = %#v, want array", evidence["candidates"])
	}
	for _, item := range raw {
		candidate, candidateOK := item.(map[string]any)
		if !candidateOK || candidate["route"] != route {
			continue
		}
		if got := jsonString(t, candidate, "reason"); got != reason {
			t.Fatalf("candidate %q reason = %q, want %q", route, got, reason)
		}
		return
	}
	t.Fatalf("route candidate %q not found in %#v", route, raw)
}

func jsonObject(t *testing.T, parent map[string]any, key string) map[string]any {
	t.Helper()
	value, ok := parent[key].(map[string]any)
	if !ok {
		t.Fatalf("%s = %#v, want object", key, parent[key])
	}
	return value
}

func jsonString(t *testing.T, parent map[string]any, key string) string {
	t.Helper()
	value, ok := parent[key].(string)
	if !ok {
		t.Fatalf("%s = %#v, want string", key, parent[key])
	}
	return value
}

func jsonUint16(t *testing.T, parent map[string]any, key string) uint16 {
	t.Helper()
	value, ok := parent[key].(float64)
	if !ok || value < 0 || value > 65535 || value != float64(uint16(value)) {
		t.Fatalf("%s = %#v, want uint16", key, parent[key])
	}
	return uint16(value)
}

type routeServerCopyProvider struct {
	*endpointKindProvider
	root                      string
	advertise                 bool
	responseErr               error
	corrupt                   bool
	failBeforeWrite           bool
	unknownPartAfterFailure   bool
	serverCopyFailed          bool
	copyCalls                 int
	stageStarted              chan struct{}
	blockStageUntilCanceled   bool
	capabilityVersion         uint16
	driftDestination          bool
	omitDestinationCapability bool
	snapshotCalls             int
}

func (provider *routeServerCopyProvider) Stat(ctx context.Context, request providerapi.StatRequest) (domain.Entry, error) {
	if provider.unknownPartAfterFailure && provider.serverCopyFailed && strings.Contains(string(request.Location.Path), ".part-") {
		return domain.Entry{}, &domain.OpError{
			Code: domain.CodePermissionDenied, Message: "part state unknown", Operation: "stat", EndpointID: request.Location.EndpointID,
		}
	}
	return provider.endpointKindProvider.Stat(ctx, request)
}

func (provider *routeServerCopyProvider) Snapshot(ctx context.Context) (domain.EndpointSnapshot, error) {
	provider.snapshotCalls++
	snapshot, err := provider.endpointKindProvider.Snapshot(ctx)
	if err != nil {
		return domain.EndpointSnapshot{}, err
	}
	if !provider.advertise {
		return snapshot, nil
	}
	if provider.omitDestinationCapability && provider.snapshotCalls == 3 {
		return snapshot, nil
	}
	version := provider.capabilityVersion
	if version == 0 {
		version = 1
	}
	revision := snapshot.Capabilities.Revision
	if provider.driftDestination && provider.snapshotCalls == 3 {
		revision.Generation++
	}
	items := append(snapshot.Capabilities.Items, domain.Capability{Name: "server_copy", Version: version})
	snapshot.Capabilities, err = domain.NewCapabilitySnapshot(revision, true, items)
	return snapshot, err
}

func (provider *routeServerCopyProvider) ServerCopy(
	ctx context.Context,
	source domain.Location,
	part domain.Location,
	expected domain.Fingerprint,
	maxBytes uint64,
) (domain.Entry, error) {
	provider.copyCalls++
	entry, err := provider.Stat(ctx, providerapi.StatRequest{Location: source})
	if err != nil {
		return domain.Entry{}, err
	}
	if !reflect.DeepEqual(entry.Fingerprint, expected) || entry.Metadata.Size == nil || *entry.Metadata.Size > maxBytes {
		return domain.Entry{}, &domain.OpError{Code: domain.CodeConflict, Message: "source changed", Operation: "server_copy", EndpointID: source.EndpointID}
	}
	if provider.stageStarted != nil {
		close(provider.stageStarted)
	}
	if provider.blockStageUntilCanceled {
		<-ctx.Done()
		return domain.Entry{}, ctx.Err()
	}
	if provider.failBeforeWrite {
		provider.serverCopyFailed = true
		return domain.Entry{}, errors.New("server copy failed before write")
	}
	payload, err := readRouteTestFile(ctx, provider, source)
	if err != nil {
		return domain.Entry{}, err
	}
	if provider.corrupt && len(payload) != 0 {
		payload[0] ^= 0xff
	}
	writeHandle, err := provider.OpenWrite(ctx, providerapi.OpenWriteRequest{Location: part, Disposition: providerapi.WriteCreateNew})
	if err != nil {
		return domain.Entry{}, err
	}
	if err := writeAll(ctx, writeHandle, payload); err != nil {
		_ = writeHandle.Close(context.Background())
		return domain.Entry{}, err
	}
	if err := writeHandle.Sync(ctx); err != nil {
		_ = writeHandle.Close(context.Background())
		return domain.Entry{}, err
	}
	if err := writeHandle.Close(ctx); err != nil {
		return domain.Entry{}, err
	}
	partEntry, err := provider.Stat(ctx, providerapi.StatRequest{Location: part})
	if err != nil {
		return domain.Entry{}, err
	}
	return partEntry, provider.responseErr
}

type routeServerCopyCapabilityOnlyProvider struct {
	*endpointKindProvider
}

func (provider *routeServerCopyCapabilityOnlyProvider) Snapshot(ctx context.Context) (domain.EndpointSnapshot, error) {
	snapshot, err := provider.endpointKindProvider.Snapshot(ctx)
	if err != nil {
		return domain.EndpointSnapshot{}, err
	}
	items := append(snapshot.Capabilities.Items, domain.Capability{Name: "server_copy", Version: 1})
	snapshot.Capabilities, err = domain.NewCapabilitySnapshot(snapshot.Capabilities.Revision, true, items)
	return snapshot, err
}

func newRouteServerCopyPlan(t *testing.T, responseErr error) (Plan, *routeServerCopyProvider) {
	return newRouteCopyPlan(t, true, responseErr)
}

func newRouteCopyPlan(t *testing.T, advertise bool, responseErr error) (Plan, *routeServerCopyProvider) {
	t.Helper()
	root := t.TempDir()
	if err := os.Mkdir(filepath.Join(root, "destination"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "source"), []byte("frozen server copy payload"), 0o600); err != nil {
		t.Fatal(err)
	}
	base := newPlanTestProvider(t, "ep_aaaaaaaaaaaaaaaaaaaaaaaaaa", root, domain.EndpointSSH)
	implementation := &routeServerCopyProvider{endpointKindProvider: base, root: root, advertise: advertise, responseErr: responseErr}
	planner := NewPlanner(MapResolver{implementation.Descriptor().ID: implementation})
	reference, err := planner.Capture(context.Background(), normalizePlanTest(t, implementation, "/source"))
	if err != nil {
		t.Fatal(err)
	}
	plan, _, err := planner.FreezeCopy(context.Background(), validFreezeRequest(reference, normalizePlanTest(t, implementation, "/destination")))
	if err != nil {
		t.Fatal(err)
	}
	return plan, implementation
}

type routeCancellationObservation struct {
	Canceled    bool
	Bytes       uint64
	FinalAbsent bool
	Source      string
}

func observeRouteCancellationContract(t *testing.T, serverCopy bool) routeCancellationObservation {
	t.Helper()
	plan, implementation := newRouteCopyPlan(t, serverCopy, nil)
	result, executeErr := NewWorker(MapResolver{implementation.Descriptor().ID: implementation}, &volatileJournal{}).Execute(
		context.Background(), plan, ControlFunc(func(Checkpoint) ControlAction { return ControlCancel }),
	)
	_, finalErr := implementation.Stat(context.Background(), providerapi.StatRequest{Location: plan.Final})
	source, sourceErr := readRouteTestFile(context.Background(), implementation, plan.Source.Location)
	if sourceErr != nil {
		t.Fatal(sourceErr)
	}
	return routeCancellationObservation{
		Canceled: errors.Is(executeErr, ErrCanceled), Bytes: result.Bytes,
		FinalAbsent: domain.IsCode(finalErr, domain.CodeNotFound), Source: string(source),
	}
}

func readRouteTestFile(ctx context.Context, provider providerapi.Provider, location domain.Location) ([]byte, error) {
	handle, err := provider.OpenRead(ctx, providerapi.OpenReadRequest{Location: location})
	if err != nil {
		return nil, err
	}
	defer func() { _ = handle.Close(context.Background()) }()
	buffer := make([]byte, 32)
	var payload []byte
	for {
		n, readErr := handle.Read(ctx, buffer)
		if n > 0 {
			payload = append(payload, buffer[:n]...)
		}
		if readErr != nil {
			if errors.Is(readErr, io.EOF) {
				return payload, nil
			}
			return nil, readErr
		}
		if n == 0 {
			return nil, errors.New("test provider read made no progress")
		}
	}
}
