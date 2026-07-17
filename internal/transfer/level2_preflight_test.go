package transfer

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/TyrantLucifer/awesome-mac-sftp/internal/directprotocol"
	"github.com/TyrantLucifer/awesome-mac-sftp/internal/domain"
	"github.com/TyrantLucifer/awesome-mac-sftp/internal/state/jobstore"
)

type level2PreflightFixture struct {
	requests []directprotocol.Request
	result   func(directprotocol.Request) (directprotocol.Result, error)
}

func (fixture *level2PreflightFixture) Preflight(_ context.Context, request directprotocol.Request) (directprotocol.Result, error) {
	fixture.requests = append(fixture.requests, request)
	return fixture.result(request)
}

// newLevel2FixturePlanner deliberately exists only in a same-package _test.go
// file. Ordinary runtime construction has no Level 2 backend injection surface.
func newLevel2FixturePlanner(resolver Resolver, backend level2PreflightBackend) *Planner {
	return &Planner{resolver: resolver, level2: backend}
}

func TestLevel2PreflightAllPassFreezesTrustedDirectRoute(t *testing.T) {
	fixture := &level2PreflightFixture{result: passingLevel2PreflightResult}
	planner, request, destination := newLevel2PreflightPlanFixture(t, fixture)

	plan, create, err := planner.FreezeCopy(context.Background(), request)
	if err != nil {
		t.Fatalf("FreezeCopy(): %v", err)
	}
	if plan.Route != RouteLevel2Direct || plan.Level2Preflight == nil || plan.Level2Preflight.Outcome != Level2PreflightPassed {
		t.Fatalf("Level 2 Plan = %#v", plan)
	}
	if len(fixture.requests) != 1 {
		t.Fatalf("preflight requests = %d, want 1", len(fixture.requests))
	}
	frozenRequest := fixture.requests[0]
	if frozenRequest.TargetHostAlias != destination.Descriptor().SSHHostAlias || frozenRequest.TargetHostAlias == destination.Descriptor().DisplayName {
		t.Fatalf("target alias = %q, trusted/display = %q/%q", frozenRequest.TargetHostAlias, destination.Descriptor().SSHHostAlias, destination.Descriptor().DisplayName)
	}
	if frozenRequest.SourceEndpointID != plan.SourceEndpoint.ID || frozenRequest.DestinationEndpointID != plan.DestinationEndpoint.ID ||
		frozenRequest.SourcePath != string(plan.Source.Location.Path) || frozenRequest.PartPath != string(plan.Part.Path) || frozenRequest.FinalPath != string(plan.Final.Path) {
		t.Fatalf("preflight request does not bind the frozen Plan: %#v", frozenRequest)
	}
	if plan.RouteEvidence == nil || plan.RouteEvidence.Selected != (RouteDecision{Route: RouteLevel2Direct, Reason: ReasonLevel2PreflightPassed, Eligible: true}) {
		t.Fatalf("route evidence = %#v", plan.RouteEvidence)
	}
	reloaded, err := DecodePlan(jobstore.PlanRecord{
		PlanID: create.PlanID, Kind: create.Kind, SourceJSON: create.SourceJSON, DestinationJSON: create.DestinationJSON,
		Route: create.Route, Verification: create.Verification, ConflictPolicy: create.ConflictPolicy, FrozenAt: create.Now,
	}, create.JobID)
	if err != nil || !reflect.DeepEqual(reloaded, plan) {
		t.Fatalf("DecodePlan(Level 2) = (%#v, %v), want %#v", reloaded, err, plan)
	}
}

func TestLevel2PreflightEveryRequiredFailureOrUnknownSelectsRelay(t *testing.T) {
	for index, checkName := range directprotocol.CheckOrder() {
		for _, status := range []directprotocol.Status{directprotocol.Fail, directprotocol.Unknown} {
			t.Run(string(checkName)+"/"+string(status), func(t *testing.T) {
				fixture := &level2PreflightFixture{result: func(request directprotocol.Request) (directprotocol.Result, error) {
					result, err := passingLevel2PreflightResult(request)
					result.Checks[index].Status = status
					result.Checks[index].Reason = string(checkName) + "_" + string(status)
					return result, err
				}}
				planner, request, _ := newLevel2PreflightPlanFixture(t, fixture)
				plan, _, err := planner.FreezeCopy(context.Background(), request)
				if err != nil {
					t.Fatalf("FreezeCopy(): %v", err)
				}
				wantReason := ReasonLevel2PreflightFailed
				wantOutcome := Level2PreflightFailed
				if status == directprotocol.Unknown {
					wantReason = ReasonLevel2PreflightUnknown
					wantOutcome = Level2PreflightUnknown
				}
				if plan.Route != RouteSFTPRelay || plan.Level2Preflight == nil || plan.Level2Preflight.Outcome != wantOutcome ||
					plan.Level2Preflight.FirstCheck != resultCheck(checkName, status) {
					t.Fatalf("relay Plan = %#v", plan)
				}
				if plan.RouteEvidence == nil || plan.RouteEvidence.Candidates[0] != (RouteDecision{Route: RouteLevel2Direct, Reason: wantReason, Eligible: false}) {
					t.Fatalf("route evidence = %#v", plan.RouteEvidence)
				}
			})
		}
	}
}

func TestLevel2PolicyAndProductionClosureNeverInvokeDirectFixture(t *testing.T) {
	t.Run("policy disabled", func(t *testing.T) {
		fixture := &level2PreflightFixture{result: passingLevel2PreflightResult}
		planner, request, _ := newLevel2PreflightPlanFixture(t, fixture)
		request.Intent.DirectPolicy.UserEnabled = false
		plan, _, err := planner.FreezeCopy(context.Background(), request)
		if err != nil {
			t.Fatal(err)
		}
		if len(fixture.requests) != 0 || plan.Route != RouteSFTPRelay || plan.Level2Preflight != nil ||
			plan.RouteEvidence.Candidates[0].Reason != ReasonLevel2PolicyDisabled {
			t.Fatalf("policy-disabled Plan/requests = %#v/%#v", plan, fixture.requests)
		}
	})

	t.Run("ordinary runtime is production closed", func(t *testing.T) {
		fixturePlanner, request, destination := newLevel2PreflightPlanFixture(t, &level2PreflightFixture{result: passingLevel2PreflightResult})
		planner := NewPlanner(fixturePlanner.resolver)
		plan, _, err := planner.FreezeCopy(context.Background(), request)
		if err != nil {
			t.Fatal(err)
		}
		if plan.Route != RouteSFTPRelay || plan.Level2Preflight != nil || plan.RouteEvidence.Candidates[0].Reason != ReasonProductionDistributionClosed {
			t.Fatalf("production-closed Plan for %q = %#v", destination.Descriptor().SSHHostAlias, plan)
		}
	})
}

func TestLevel2MalformedOrUnavailablePreflightSafelySelectsRelay(t *testing.T) {
	tests := []struct {
		name   string
		result func(directprotocol.Request) (directprotocol.Result, error)
	}{
		{name: "backend unavailable", result: func(directprotocol.Request) (directprotocol.Result, error) {
			return directprotocol.Result{}, errors.New("secret backend diagnostic")
		}},
		{name: "malformed result", result: func(request directprotocol.Request) (directprotocol.Result, error) {
			result, err := passingLevel2PreflightResult(request)
			result.RequestID = "req_bbbbbbbbbbbbbbbbbbbbbbbbbb"
			return result, err
		}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			fixture := &level2PreflightFixture{result: test.result}
			planner, request, _ := newLevel2PreflightPlanFixture(t, fixture)
			plan, create, err := planner.FreezeCopy(context.Background(), request)
			if err != nil {
				t.Fatalf("FreezeCopy(): %v", err)
			}
			if plan.Route != RouteSFTPRelay || plan.Level2Preflight == nil || plan.Level2Preflight.Outcome != Level2PreflightInvalid ||
				plan.Level2Preflight.Result != nil || plan.Level2Preflight.FirstCheck.Reason != "preflight_unavailable" {
				t.Fatalf("safe fallback Plan = %#v", plan)
			}
			encoded, marshalErr := json.Marshal(create)
			if marshalErr != nil {
				t.Fatal(marshalErr)
			}
			if strings.Contains(string(encoded), "secret backend diagnostic") {
				t.Fatal("raw backend diagnostic entered durable state")
			}
		})
	}
}

func TestLevel2FrozenControlPlaneContainsNoCredentialDelegationOrCommandSurface(t *testing.T) {
	fixture := &level2PreflightFixture{result: passingLevel2PreflightResult}
	planner, request, _ := newLevel2PreflightPlanFixture(t, fixture)
	plan, _, err := planner.FreezeCopy(context.Background(), request)
	if err != nil {
		t.Fatal(err)
	}
	raw, err := json.Marshal(plan.Level2Preflight)
	if err != nil {
		t.Fatal(err)
	}
	lower := strings.ToLower(string(raw))
	for _, forbidden := range []string{
		"private_key", "identity_file", "password", "askpass", "ticket", "credential", "agent_forward", "forwardagent",
		"gssapi", "kerberos", "known_hosts", "stricthostkeychecking", "proxycommand", "controlmaster", "controlpath",
		"remote_command", "command_string", "shell", "argv",
	} {
		if strings.Contains(lower, forbidden) {
			t.Fatalf("frozen direct control plane contains forbidden credential/command field %q: %s", forbidden, raw)
		}
	}
	if !strings.Contains(lower, `"target_host_alias":"trusted-target"`) ||
		!strings.Contains(lower, `"cancel_semantics":"request_context"`) ||
		!strings.Contains(lower, `"progress_semantics":"target_durable_bytes"`) {
		t.Fatalf("frozen direct control authority/semantics are incomplete: %s", raw)
	}
}

func TestValidateExecutionRejectsTamperedLevel2Binding(t *testing.T) {
	_, plan, _, _, _, _ := newLevel2DirectPlanFixture(t)
	tests := []struct {
		name   string
		mutate func(*Plan)
	}{
		{name: "target alias", mutate: func(plan *Plan) { plan.Level2Preflight.Request.TargetHostAlias = "other-target" }},
		{name: "source digest", mutate: func(plan *Plan) { plan.Level2Preflight.Result.SourceSHA256 = strings.Repeat("b", 64) }},
		{name: "control semantics", mutate: func(plan *Plan) { plan.Level2Preflight.Request.Control.CancelSemantics = "best_effort" }},
		{name: "preflight outcome", mutate: func(plan *Plan) { plan.Level2Preflight.Outcome = Level2PreflightFailed }},
		{name: "route evidence", mutate: func(plan *Plan) { plan.RouteEvidence.Selected.Reason = ReasonBoundedRelayDefault }},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			candidate := plan
			binding := *plan.Level2Preflight
			request := binding.Request
			request.SourceFingerprint = cloneFingerprint(binding.Request.SourceFingerprint)
			result := *binding.Result
			result.SourceFingerprint = cloneFingerprint(binding.Result.SourceFingerprint)
			result.Checks = append([]directprotocol.Check(nil), binding.Result.Checks...)
			binding.Request = request
			binding.Result = &result
			candidate.Level2Preflight = &binding
			evidence := *plan.RouteEvidence
			evidence.Candidates = append([]RouteDecision(nil), plan.RouteEvidence.Candidates...)
			candidate.RouteEvidence = &evidence
			test.mutate(&candidate)
			if err := validateExecution(candidate); err == nil {
				t.Fatal("tampered Level 2 Plan was accepted")
			}
		})
	}
}

func newLevel2PreflightPlanFixture(t *testing.T, fixture *level2PreflightFixture) (*Planner, FreezeRequest, *endpointKindProvider) {
	t.Helper()
	sourceRoot := t.TempDir()
	destinationRoot := t.TempDir()
	if err := os.WriteFile(filepath.Join(sourceRoot, "source.bin"), []byte("direct payload"), 0o600); err != nil {
		t.Fatal(err)
	}
	source := newPlanTestProvider(t, "ep_aaaaaaaaaaaaaaaaaaaaaaaaaa", sourceRoot, domain.EndpointSSH)
	destination := newPlanTestProvider(t, "ep_bbbbbbbbbbbbbbbbbbbbbbbbbb", destinationRoot, domain.EndpointSSH)
	destination.descriptor.DisplayName = "untrusted display value"
	destination.descriptor.SSHHostAlias = "trusted-target"
	resolver := MapResolver{source.Descriptor().ID: source, destination.Descriptor().ID: destination}
	planner := newLevel2FixturePlanner(resolver, fixture)
	sourceRef, err := planner.Capture(context.Background(), normalizePlanTest(t, source, "/source.bin"))
	if err != nil {
		t.Fatal(err)
	}
	request := validFreezeRequest(sourceRef, normalizePlanTest(t, destination, "/"))
	request.Intent.DirectPolicy = DirectPolicy{
		UserEnabled: true, WorkspaceEnabled: true, DataAllowed: true, Integrity: IntegrityRequireStrong,
	}
	return planner, request, destination
}

func passingLevel2PreflightResult(request directprotocol.Request) (directprotocol.Result, error) {
	checks := make([]directprotocol.Check, 0, len(directprotocol.CheckOrder()))
	for _, name := range directprotocol.CheckOrder() {
		checks = append(checks, directprotocol.Check{Name: name, Status: directprotocol.Pass, Reason: "passed"})
	}
	digest := sha256.Sum256([]byte("direct payload"))
	return directprotocol.Result{
		Version: directprotocol.Version, RequestID: request.RequestID, JobID: request.JobID,
		CheckedAtUnix: request.DeadlineUnix - 300, ExpiresAtUnix: request.DeadlineUnix - 60,
		Checks: checks, SourceFingerprint: cloneFingerprint(request.SourceFingerprint), SourceSize: request.ExpectedSize,
		SourceSHA256: hex.EncodeToString(digest[:]),
	}, nil
}

func resultCheck(name directprotocol.CheckName, status directprotocol.Status) directprotocol.Check {
	return directprotocol.Check{Name: name, Status: status, Reason: string(name) + "_" + string(status)}
}
