package helper

import (
	"context"
	"errors"
	"fmt"

	"github.com/TyrantLucifer/awesome-sftp-cli/internal/domain"
)

type EnableRequest struct {
	EndpointID    domain.EndpointID
	ProtocolMajor uint16
	Target        Target
	Verifier      Verifier
	Policy        Policy
	State         *StateStore
	Remote        InstallRemote
}

type EnablePlan struct {
	EndpointID  domain.EndpointID
	Manifest    Manifest
	Observation Observation
	FinalPath   string
}

// PrepareEnable reloads exact persisted metadata and repeats current policy,
// binding, high-water, ancestor, final-attribute and full remote hash checks.
// No cached "verified" boolean can make this operation succeed.
func PrepareEnable(ctx context.Context, request EnableRequest) (EnablePlan, error) {
	if ctx == nil || request.EndpointID == "" || request.ProtocolMajor == 0 || request.Target.OS == "" || request.Target.Arch == "" || request.State == nil || request.Remote == nil {
		return EnablePlan{}, errors.New("prepare helper enable: request is incomplete")
	}
	record, err := request.State.LoadEnabled(request.EndpointID, request.ProtocolMajor, request.Target)
	if err != nil {
		return EnablePlan{}, fmt.Errorf("prepare helper enable: load exact metadata: %w", err)
	}
	manifest, err := verifyCurrentPolicy(request.Verifier, request.Policy, record.RawManifest, record.RawSignature)
	if err != nil {
		return EnablePlan{}, fmt.Errorf("prepare helper enable: %w", err)
	}
	if manifest.ProtocolMajor != request.ProtocolMajor || manifest.Target() != request.Target {
		return EnablePlan{}, errors.New("prepare helper enable: persisted selection does not match signed target")
	}
	if err := validateProbeUtilities(ctx, request.Remote, nil); err != nil {
		return EnablePlan{}, fmt.Errorf("prepare helper enable: pre-probe utility validation: %w", err)
	}
	observation, err := request.Remote.Probe(ctx)
	if err != nil {
		return EnablePlan{}, fmt.Errorf("prepare helper enable: binding probe: %w", err)
	}
	if observation.Target != manifest.Target() {
		return EnablePlan{}, errors.New("prepare helper enable: observed target changed")
	}
	decision, err := request.State.Check(request.EndpointID, manifest, false)
	if err != nil {
		return EnablePlan{}, err
	}
	if decision != HighWaterNoop {
		return EnablePlan{}, errors.New("prepare helper enable: installed artifact is not the persistent high-water")
	}
	snapshot, err := inspectInstallSnapshotFromObservation(ctx, request.Remote, manifest, observation)
	if err != nil {
		return EnablePlan{}, fmt.Errorf("prepare helper enable: fresh remote validation: %w", err)
	}
	if !snapshot.FinalExists || len(snapshot.CreateDirectories) != 0 || snapshot.Plan.FinalPath != record.FinalPath {
		return EnablePlan{}, errors.New("prepare helper enable: derived path or installed artifact changed")
	}
	return EnablePlan{EndpointID: request.EndpointID, Manifest: manifest, Observation: observation, FinalPath: record.FinalPath}, nil
}

func ValidateEnabledClient(plan EnablePlan, client *Client, required []CapabilityName) error {
	if plan.Manifest.Raw == nil || client == nil || client.Level() != 1 {
		return errors.New("validate enabled helper client: plan or live client is unavailable")
	}
	negotiated := client.Negotiated()
	version, err := parseReleaseVersion(negotiated.HelperVersion)
	if err != nil || negotiated.Protocol != plan.Manifest.ProtocolMajor || version != plan.Manifest.Version {
		return errors.New("validate enabled helper client: protocol or exact artifact version mismatch")
	}
	seen := make(map[CapabilityName]struct{}, len(required))
	for _, capability := range required {
		if !knownCapability(capability) {
			return errors.New("validate enabled helper client: required capability is unknown")
		}
		if _, duplicate := seen[capability]; duplicate {
			return errors.New("validate enabled helper client: duplicate required capability")
		}
		seen[capability] = struct{}{}
		if !client.HasCapability(capability) {
			return fmt.Errorf("validate enabled helper client: capability %q was not negotiated", capability)
		}
	}
	return nil
}
