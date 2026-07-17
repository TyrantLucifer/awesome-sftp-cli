package transfer

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/TyrantLucifer/awesome-mac-sftp/internal/domain"
	providerapi "github.com/TyrantLucifer/awesome-mac-sftp/internal/provider"
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
