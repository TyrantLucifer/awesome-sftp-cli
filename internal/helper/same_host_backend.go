package helper

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/TyrantLucifer/awesome-mac-sftp/internal/domain"
	"github.com/TyrantLucifer/awesome-mac-sftp/internal/transfer"
)

// SameHostCopyBackend adapts the framed Helper capabilities to the transfer
// Planner/Worker contract. It never commits Final; the durable Worker owns all
// conflict, verification, and rename semantics.
type SameHostCopyBackend struct {
	endpointID domain.EndpointID
	plan       EnablePlan
	client     *Client
	generator  domain.Generator
}

func NewSameHostCopyBackend(plan EnablePlan, client *Client, generator domain.Generator) (*SameHostCopyBackend, error) {
	if plan.EndpointID == "" || plan.Manifest.Raw == nil || client == nil || generator == nil {
		return nil, errors.New("create Helper same-host backend: enabled plan, client, and generator are required")
	}
	return &SameHostCopyBackend{endpointID: plan.EndpointID, plan: plan, client: client, generator: generator}, nil
}

func (backend *SameHostCopyBackend) PrepareCopy(ctx context.Context, request transfer.SameHostCopyPrepareRequest) (transfer.SameHostCopyBinding, error) {
	if backend == nil || ctx == nil || request.Source.EndpointID == "" || request.Source.EndpointID != backend.endpointID || request.Source.EndpointID != request.Part.EndpointID ||
		request.Source.EndpointID != request.Final.EndpointID || request.MaxBytes == 0 || request.MaxBytes > transfer.MaxSameHostCopyBytes {
		return transfer.SameHostCopyBinding{}, errors.New("prepare Helper same-host copy: request is invalid")
	}
	if err := ValidateEnabledClient(backend.plan, backend.client, []CapabilityName{CapabilityStrongHash, CapabilitySameHostCopy}); err != nil {
		return transfer.SameHostCopyBinding{}, err
	}
	body, err := json.Marshal(StrongHashRequest{Path: string(request.Source.Path), Algorithm: "sha256", MaxBytes: request.MaxBytes})
	if err != nil {
		return transfer.SameHostCopyBinding{}, err
	}
	raw, completion, err := backend.requestOne(ctx, CapabilityStrongHash, body)
	if err != nil {
		return transfer.SameHostCopyBinding{}, err
	}
	var result StrongHashResult
	if err := DecodePayload(raw, &result); err != nil {
		return transfer.SameHostCopyBinding{}, fmt.Errorf("prepare Helper same-host copy: decode strong hash: %w", err)
	}
	if completion.Status != "complete" || completion.Reason != "none" || !result.Valid || result.Algorithm != "sha256" ||
		len(result.Digest) != 64 || !isLowerHex(result.Digest) || result.Fingerprint.Size > request.MaxBytes {
		return transfer.SameHostCopyBinding{}, errors.New("prepare Helper same-host copy: strong hash was incomplete or invalid")
	}
	if !helperFingerprintMatchesProvider(result, request.ExpectedFingerprint) {
		return transfer.SameHostCopyBinding{}, errors.New("prepare Helper same-host copy: source identity differs from frozen Provider fingerprint")
	}
	negotiated := backend.client.Negotiated()
	capabilityVersion, ok := negotiatedCapabilityVersion(negotiated, CapabilitySameHostCopy)
	if !ok {
		return transfer.SameHostCopyBinding{}, errors.New("prepare Helper same-host copy: capability was removed")
	}
	return transfer.SameHostCopyBinding{
		EndpointID: backend.endpointID, ArtifactID: backend.plan.Manifest.ArtifactID(),
		Protocol: negotiated.Protocol, HelperVersion: negotiated.HelperVersion, CapabilityVersion: capabilityVersion,
		SourceSHA256: result.Digest, SourceSize: result.Fingerprint.Size,
		SourceIdentity: transfer.SameHostSourceIdentity{
			Size: result.Fingerprint.Size, Mode: result.Fingerprint.Mode, ModifiedUnixNS: result.Fingerprint.ModifiedUnixNS, FileID: result.Fingerprint.FileID,
		},
	}, nil
}

func (backend *SameHostCopyBackend) StageCopy(ctx context.Context, request transfer.SameHostCopyStageRequest) (transfer.SameHostCopyStageResult, error) {
	if backend == nil || ctx == nil || request.Source.EndpointID == "" || request.Source.EndpointID != backend.endpointID || request.Source.EndpointID != request.Part.EndpointID ||
		request.Source.EndpointID != request.Final.EndpointID || request.MaxBytes == 0 || request.MaxBytes > transfer.MaxSameHostCopyBytes ||
		request.Binding.SourceSize > request.MaxBytes || request.Binding.SourceIdentity.Size != request.Binding.SourceSize ||
		len(request.Binding.SourceSHA256) != 64 || !isLowerHex(request.Binding.SourceSHA256) {
		return transfer.SameHostCopyStageResult{}, errors.New("stage Helper same-host copy: request is invalid")
	}
	if err := ValidateEnabledClient(backend.plan, backend.client, []CapabilityName{CapabilitySameHostCopy}); err != nil {
		return transfer.SameHostCopyStageResult{}, err
	}
	negotiated := backend.client.Negotiated()
	capabilityVersion, ok := negotiatedCapabilityVersion(negotiated, CapabilitySameHostCopy)
	if !ok || request.Binding.Protocol != negotiated.Protocol || request.Binding.HelperVersion != negotiated.HelperVersion ||
		request.Binding.CapabilityVersion != capabilityVersion || request.Binding.EndpointID != backend.endpointID ||
		request.Binding.ArtifactID != backend.plan.Manifest.ArtifactID() {
		return transfer.SameHostCopyStageResult{}, errors.New("stage Helper same-host copy: frozen capability binding changed")
	}
	body, err := json.Marshal(SameHostCopyRequest{
		Source: string(request.Source.Path), Part: string(request.Part.Path), Final: string(request.Final.Path), JobID: request.JobID,
		ExpectedSource: FileFingerprint{
			Size: request.Binding.SourceIdentity.Size, Mode: request.Binding.SourceIdentity.Mode,
			ModifiedUnixNS: request.Binding.SourceIdentity.ModifiedUnixNS, FileID: request.Binding.SourceIdentity.FileID,
		},
		ExpectedSHA256: request.Binding.SourceSHA256, ExpectedSize: request.Binding.SourceSize, MaxBytes: request.MaxBytes,
	})
	if err != nil {
		return transfer.SameHostCopyStageResult{}, err
	}
	raw, completion, err := backend.requestOne(ctx, CapabilitySameHostCopy, body)
	if err != nil {
		return transfer.SameHostCopyStageResult{}, err
	}
	var result SameHostCopyResult
	if err := DecodePayload(raw, &result); err != nil {
		return transfer.SameHostCopyStageResult{}, fmt.Errorf("stage Helper same-host copy: decode result: %w", err)
	}
	if completion.Status != "complete" || completion.Reason != "staged_not_committed" || result.Part != string(request.Part.Path) ||
		result.Size != request.Binding.SourceSize || result.SHA256 != request.Binding.SourceSHA256 || result.Committed {
		return transfer.SameHostCopyStageResult{}, errors.New("stage Helper same-host copy: result violates frozen plan")
	}
	return transfer.SameHostCopyStageResult{Part: request.Part, Size: result.Size, SHA256: result.SHA256, Committed: false}, nil
}

func helperFingerprintMatchesProvider(result StrongHashResult, expected domain.Fingerprint) bool {
	if expected.Size == nil || expected.ModifiedAt == nil || expected.ModifiedPrecision == nil || *expected.Size != result.Fingerprint.Size {
		return false
	}
	observed := time.Unix(0, result.Fingerprint.ModifiedUnixNS).UTC()
	want := expected.ModifiedAt.UTC()
	switch *expected.ModifiedPrecision {
	case "second":
		if !observed.Truncate(time.Second).Equal(want.Truncate(time.Second)) {
			return false
		}
	case "nanosecond":
		if !observed.Equal(want) {
			return false
		}
	default:
		return false
	}
	if expected.FileID != nil && *expected.FileID != result.Fingerprint.FileID || expected.VersionID != nil && *expected.VersionID != "" {
		return false
	}
	if expected.HashAlgorithm != nil || expected.HashHex != nil {
		return expected.HashAlgorithm != nil && *expected.HashAlgorithm == "sha256" && expected.HashHex != nil && *expected.HashHex == result.Digest
	}
	return true
}

func (backend *SameHostCopyBackend) requestOne(ctx context.Context, operation CapabilityName, body json.RawMessage) (json.RawMessage, Completion, error) {
	requestID, err := domain.NewRequestID(backend.generator)
	if err != nil {
		return nil, Completion{}, err
	}
	events, err := backend.client.Start(ctx, requestID, operation, body)
	if err != nil {
		return nil, Completion{}, err
	}
	var result json.RawMessage
	var operationErr error
	for event := range events {
		if event.Err != nil {
			return nil, Completion{}, event.Err
		}
		switch event.Type {
		case FrameResult:
			if result != nil {
				return nil, Completion{}, errors.New("helper same-host request returned multiple results")
			}
			result = append(json.RawMessage(nil), event.Payload...)
		case FrameProgress:
			return nil, Completion{}, errors.New("helper same-host request returned unexpected progress")
		case FrameError:
			var structured StructuredError
			if err := DecodePayload(event.Payload, &structured); err != nil {
				return nil, Completion{}, err
			}
			operationErr = fmt.Errorf("helper %s failed: %s", operation, structured.Message)
		case FrameComplete:
			var completion Completion
			if err := DecodePayload(event.Payload, &completion); err != nil {
				return nil, Completion{}, err
			}
			if operationErr != nil {
				return nil, completion, operationErr
			}
			if result == nil {
				return nil, completion, errors.New("helper same-host request completed without one result")
			}
			return result, completion, nil
		default:
			return nil, Completion{}, errors.New("helper same-host request returned an invalid frame")
		}
	}
	return nil, Completion{}, errors.New("helper same-host request ended without completion")
}

func negotiatedCapabilityVersion(negotiated Negotiated, name CapabilityName) (uint16, bool) {
	for _, capability := range negotiated.Capabilities {
		if capability.Name == name {
			return capability.Version, true
		}
	}
	return 0, false
}
