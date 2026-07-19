package transfer

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"reflect"

	"github.com/TyrantLucifer/awesome-mac-sftp/internal/directprotocol"
	"github.com/TyrantLucifer/awesome-mac-sftp/internal/domain"
)

const level2DirectCapability = "direct_transfer"

// ProductionDirectTransferOpen is the frozen production distribution boundary.
// Level 2 backends remain injectable only for isolated verification fixtures.
const ProductionDirectTransferOpen = false

// DefaultIntegrityPolicy returns the current production transfer-integrity floor.
func DefaultIntegrityPolicy() IntegrityPolicy { return IntegrityStrong }

type DirectPolicy struct {
	UserEnabled      bool            `json:"user_enabled"`
	WorkspaceEnabled bool            `json:"workspace_enabled"`
	DataAllowed      bool            `json:"data_allowed"`
	Integrity        IntegrityPolicy `json:"integrity"`
}

func (policy DirectPolicy) enabled() bool {
	return policy.UserEnabled && policy.WorkspaceEnabled && policy.DataAllowed &&
		(policy.Integrity == IntegrityStrong || policy.Integrity == IntegrityRequireStrong)
}

func (policy DirectPolicy) zero() bool {
	return !policy.UserEnabled && !policy.WorkspaceEnabled && !policy.DataAllowed
}

type Level2PreflightOutcome string

const (
	Level2PreflightPassed  Level2PreflightOutcome = "passed"
	Level2PreflightFailed  Level2PreflightOutcome = "failed"
	Level2PreflightUnknown Level2PreflightOutcome = "unknown"
	Level2PreflightInvalid Level2PreflightOutcome = "invalid"
)

type Level2PreflightBinding struct {
	ProtocolVersion uint16                 `json:"protocol_version"`
	Capability      string                 `json:"capability"`
	MaxBytes        uint64                 `json:"max_bytes"`
	Request         directprotocol.Request `json:"request"`
	Result          *directprotocol.Result `json:"result,omitempty"`
	SourceSize      uint64                 `json:"source_size,omitempty"`
	SourceSHA256    string                 `json:"source_sha256,omitempty"`
	Outcome         Level2PreflightOutcome `json:"outcome"`
	FirstCheck      directprotocol.Check   `json:"first_check,omitempty"`
}

type level2PreflightBackend interface {
	Preflight(context.Context, directprotocol.Request) (directprotocol.Result, error)
}

func (planner *Planner) tryLevel2Preflight(ctx context.Context, request FreezeRequest, plan *Plan) {
	if planner == nil || plan == nil || !plan.DirectPolicy.enabled() || planner.level2 == nil ||
		plan.Bandwidth.requiresControl() ||
		plan.Version != 1 || plan.Source.Kind != domain.EntryFile ||
		plan.SourceEndpoint.ID == plan.DestinationEndpoint.ID ||
		plan.SourceEndpoint.Kind != domain.EndpointSSH || plan.DestinationEndpoint.Kind != domain.EndpointSSH ||
		plan.Source.Fingerprint.Size == nil || *plan.Source.Fingerprint.Size > directprotocol.MaxTransferBytes {
		return
	}
	nonceBytes := make([]byte, 16)
	if _, err := rand.Read(nonceBytes); err != nil {
		return
	}
	preflightRequest := directprotocol.Request{
		Version: directprotocol.Version, RequestID: request.RequestID, JobID: request.JobID,
		SourceEndpointID: plan.SourceEndpoint.ID, SourcePath: string(plan.Source.Location.Path),
		DestinationEndpointID: plan.DestinationEndpoint.ID, PartPath: string(plan.Part.Path), FinalPath: string(plan.Final.Path),
		TargetHostAlias: plan.DestinationEndpoint.SSHHostAlias, ExpectedSize: *plan.Source.Fingerprint.Size,
		SourceFingerprint: cloneFingerprint(plan.Source.Fingerprint), IntegrityPolicy: string(plan.DirectPolicy.Integrity),
		DeadlineUnix: request.Now.Add(directprotocol.MaxRequestDuration / 2).Unix(), Nonce: hex.EncodeToString(nonceBytes),
		Control: directprotocol.FrozenControlLimits(),
	}
	binding := &Level2PreflightBinding{
		ProtocolVersion: directprotocol.Version, Capability: level2DirectCapability, MaxBytes: directprotocol.MaxTransferBytes,
		Request: preflightRequest, Outcome: Level2PreflightInvalid,
		FirstCheck: directprotocol.Check{Name: directprotocol.CheckProtocol, Status: directprotocol.Fail, Reason: "preflight_unavailable"},
	}
	plan.Level2Preflight = binding
	result, err := planner.level2.Preflight(ctx, preflightRequest)
	if err != nil {
		return
	}
	passed, first, err := directprotocol.Evaluate(preflightRequest, result, request.Now)
	if err != nil {
		return
	}
	binding.Result = &result
	binding.SourceSize = result.SourceSize
	binding.SourceSHA256 = result.SourceSHA256
	if passed {
		binding.Outcome = Level2PreflightPassed
		binding.FirstCheck = directprotocol.Check{}
		plan.Route = RouteLevel2Direct
		return
	}
	binding.FirstCheck = first
	if first.Status == directprotocol.Unknown {
		binding.Outcome = Level2PreflightUnknown
	} else {
		binding.Outcome = Level2PreflightFailed
	}
}

func validLevel2PreflightBinding(binding Level2PreflightBinding, plan Plan) bool {
	if binding.ProtocolVersion != directprotocol.Version || binding.Capability != level2DirectCapability ||
		binding.MaxBytes != directprotocol.MaxTransferBytes || binding.Request.Version != directprotocol.Version ||
		binding.Request.RequestID == "" || binding.Request.JobID != plan.JobID ||
		binding.Request.SourceEndpointID != plan.SourceEndpoint.ID || binding.Request.SourcePath != string(plan.Source.Location.Path) ||
		binding.Request.DestinationEndpointID != plan.DestinationEndpoint.ID || binding.Request.PartPath != string(plan.Part.Path) ||
		binding.Request.FinalPath != string(plan.Final.Path) || binding.Request.TargetHostAlias != plan.DestinationEndpoint.SSHHostAlias ||
		binding.Request.SourceFingerprint.Strength() == domain.FingerprintWeak ||
		!reflect.DeepEqual(binding.Request.SourceFingerprint, plan.Source.Fingerprint) ||
		plan.Source.Fingerprint.Size == nil || binding.Request.ExpectedSize != *plan.Source.Fingerprint.Size ||
		binding.Request.IntegrityPolicy != string(plan.DirectPolicy.Integrity) {
		return false
	}
	if binding.Outcome == Level2PreflightInvalid {
		return binding.Result == nil && binding.SourceSize == 0 && binding.SourceSHA256 == "" && binding.FirstCheck == (directprotocol.Check{
			Name: directprotocol.CheckProtocol, Status: directprotocol.Fail, Reason: "preflight_unavailable",
		}) && directprotocol.ValidateRequest(binding.Request, plan.FrozenAt) == nil
	}
	if binding.Result == nil {
		return false
	}
	if binding.SourceSize != binding.Result.SourceSize || binding.SourceSHA256 != binding.Result.SourceSHA256 {
		return false
	}
	passed, first, err := directprotocol.Evaluate(binding.Request, *binding.Result, plan.FrozenAt)
	if err != nil {
		return false
	}
	if passed {
		return binding.Outcome == Level2PreflightPassed && binding.FirstCheck == (directprotocol.Check{})
	}
	wantOutcome := Level2PreflightFailed
	if first.Status == directprotocol.Unknown {
		wantOutcome = Level2PreflightUnknown
	}
	return binding.Outcome == wantOutcome && binding.FirstCheck == first
}
