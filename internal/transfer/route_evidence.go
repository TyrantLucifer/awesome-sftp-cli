package transfer

import (
	"reflect"

	"github.com/TyrantLucifer/awesome-mac-sftp/internal/domain"
)

const RouteEvidenceVersion uint16 = 1

const (
	RouteAtomicRename   Route = "atomic_rename"
	RouteSFTPServerCopy Route = "sftp_server_copy"
	RouteLevel2Direct   Route = "level2_direct"
)

type RouteReason string

const (
	ReasonSameEndpointAtomicRename     RouteReason = "same_endpoint_atomic_rename"
	ReasonServerCopySelected           RouteReason = "server_copy_capability_selected"
	ReasonServerCopyUnavailable        RouteReason = "server_copy_unavailable"
	ReasonHelperSameHostSelected       RouteReason = "helper_same_host_selected"
	ReasonHelperSameHostUnavailable    RouteReason = "helper_same_host_unavailable"
	ReasonProductionDistributionClosed RouteReason = "production_distribution_closed"
	ReasonBoundedRelayDefault          RouteReason = "bounded_relay_default"
)

type IntegrityPolicy string

const (
	IntegrityBaseline      IntegrityPolicy = "baseline"
	IntegrityStrong        IntegrityPolicy = "strong"
	IntegrityRequireStrong IntegrityPolicy = "require_strong"
)

type RouteDecision struct {
	Route    Route       `json:"route"`
	Reason   RouteReason `json:"reason"`
	Eligible bool        `json:"eligible"`
}

type RouteIntegrityEvidence struct {
	Policy       IntegrityPolicy `json:"policy"`
	Verification Verification    `json:"verification"`
	Algorithm    string          `json:"algorithm"`
}

type RouteEvidence struct {
	Version           uint16                 `json:"version"`
	Selected          RouteDecision          `json:"selected"`
	Candidates        []RouteDecision        `json:"candidates"`
	Integrity         RouteIntegrityEvidence `json:"integrity"`
	DowngradeBoundary string                 `json:"downgrade_boundary"`
	Risk              string                 `json:"risk"`
	ProgressSemantics string                 `json:"progress_semantics"`
	Part              domain.Location        `json:"part"`
	Final             domain.Location        `json:"final"`
}

func freezeRouteEvidence(plan *Plan) {
	if plan == nil {
		return
	}
	evidence := RouteEvidence{
		Version: RouteEvidenceVersion,
		Integrity: RouteIntegrityEvidence{
			Policy:       IntegrityStrong,
			Verification: plan.Verification,
			Algorithm:    "sha256",
		},
		DowngradeBoundary: "before_target_write",
		Risk:              "low",
		ProgressSemantics: "durable_bytes",
		Part:              plan.Part,
		Final:             plan.Final,
	}

	if plan.Kind == OperationMove && plan.MoveStrategy == MoveAtomicRename {
		evidence.Selected = RouteDecision{Route: RouteAtomicRename, Reason: ReasonSameEndpointAtomicRename, Eligible: true}
		evidence.Candidates = append(evidence.Candidates, evidence.Selected)
		evidence.DowngradeBoundary = "postcondition_check_only"
		evidence.ProgressSemantics = "phase_only"
		plan.RouteEvidence = &evidence

		return
	}

	sameSSH := plan.SourceEndpoint.ID == plan.DestinationEndpoint.ID &&
		plan.SourceEndpoint.Kind == domain.EndpointSSH && plan.DestinationEndpoint.Kind == domain.EndpointSSH
	if sameSSH && plan.Kind == OperationCopy && plan.Source.Kind == domain.EntryFile {
		if plan.Route == RouteSFTPServerCopy && plan.ServerCopy != nil {
			serverCopy := RouteDecision{Route: RouteSFTPServerCopy, Reason: ReasonServerCopySelected, Eligible: true}
			evidence.Candidates = append(evidence.Candidates, serverCopy)
			evidence.Selected = serverCopy
			evidence.DowngradeBoundary = "frozen_route_no_silent_downgrade"
			evidence.ProgressSemantics = "phase_only"
		} else {
			evidence.Candidates = append(evidence.Candidates, RouteDecision{
				Route: RouteSFTPServerCopy, Reason: ReasonServerCopyUnavailable, Eligible: false,
			})
		}
		if plan.Route == RouteHelperSameHost {
			helper := RouteDecision{Route: RouteHelperSameHost, Reason: ReasonHelperSameHostSelected, Eligible: true}
			evidence.Candidates = append(evidence.Candidates, helper)
			evidence.Selected = helper
			evidence.DowngradeBoundary = "frozen_route_no_silent_downgrade"
			evidence.ProgressSemantics = "phase_only"
		} else {
			evidence.Candidates = append(evidence.Candidates, RouteDecision{
				Route: RouteHelperSameHost, Reason: ReasonHelperSameHostUnavailable, Eligible: false,
			})
		}
	}

	if plan.SourceEndpoint.ID != plan.DestinationEndpoint.ID &&
		plan.SourceEndpoint.Kind == domain.EndpointSSH && plan.DestinationEndpoint.Kind == domain.EndpointSSH {
		evidence.Candidates = append(evidence.Candidates, RouteDecision{
			Route: RouteLevel2Direct, Reason: ReasonProductionDistributionClosed, Eligible: false,
		})
	}
	if evidence.Selected.Route == "" {
		evidence.Selected = RouteDecision{Route: plan.Route, Reason: ReasonBoundedRelayDefault, Eligible: true}
	}
	fallbackRoute := plan.Route
	if plan.Route == RouteSFTPServerCopy || plan.Route == RouteHelperSameHost || plan.Route == RouteLevel2Direct {
		fallbackRoute = RouteSFTPRelay
	}
	evidence.Candidates = append(evidence.Candidates, RouteDecision{
		Route: fallbackRoute, Reason: ReasonBoundedRelayDefault, Eligible: true,
	})
	plan.RouteEvidence = &evidence
}

func validRouteEvidence(plan Plan) bool {
	if plan.RouteEvidence == nil {
		return true
	}
	expected := plan
	expected.RouteEvidence = nil
	freezeRouteEvidence(&expected)
	return reflect.DeepEqual(plan.RouteEvidence, expected.RouteEvidence)
}
