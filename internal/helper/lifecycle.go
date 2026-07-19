package helper

import (
	"context"
	"errors"
	"fmt"

	"github.com/TyrantLucifer/awesome-sftp-cli/internal/domain"
)

// RemovalLeaser atomically excludes new durable Job/session references while
// verifying that no existing reference pins the exact Helper artifact.
type RemovalLeaser interface {
	AcquireHelperRemoval(context.Context, domain.EndpointID, ArtifactID) (release func(), err error)
}

// RemoveEnabled repeats the full enable-time trust and remote artifact checks,
// removes only the exact immutable artifact path, then clears its enabled
// pointer while retaining signed metadata and high-water state.
func RemoveEnabled(ctx context.Context, request EnableRequest, leaser RemovalLeaser) error {
	if leaser == nil {
		return errors.New("remove enabled helper: durable reference lease is required")
	}
	plan, err := PrepareEnable(ctx, request)
	if err != nil {
		return fmt.Errorf("remove enabled helper: %w", err)
	}
	return removePreparedArtifact(ctx, request, plan, leaser)
}

// RemoveArtifact explicitly uninstalls one exact installed version. Durable
// Jobs are checked before the state store disables and claims the artifact.
func RemoveArtifact(ctx context.Context, request EnableRequest, artifact ArtifactID, leaser RemovalLeaser) error {
	plan, err := prepareInstalledArtifact(ctx, request, artifact)
	if err != nil {
		return fmt.Errorf("remove Helper artifact: %w", err)
	}
	return removePreparedArtifact(ctx, request, plan, leaser)
}

// ResumePendingRemoval completes at most the one exact durable removal claim.
// A missing remote path is the successful postcondition after response loss.
func ResumePendingRemoval(ctx context.Context, request EnableRequest, leaser RemovalLeaser) error {
	if request.State == nil || request.Remote == nil || leaser == nil {
		return errors.New("resume Helper removal: request is incomplete")
	}
	claim, exists, err := request.State.PendingRemoval()
	if err != nil || !exists {
		return err
	}
	if claim.EndpointID != request.EndpointID || claim.ArtifactID.ProtocolMajor != request.ProtocolMajor || claim.ArtifactID.OS != request.Target.OS || claim.ArtifactID.Arch != request.Target.Arch {
		return errors.New("resume Helper removal: request does not match durable claim")
	}
	release, err := leaser.AcquireHelperRemoval(ctx, claim.EndpointID, claim.ArtifactID)
	if err != nil {
		return fmt.Errorf("resume Helper removal: acquire durable reference lease: %w", err)
	}
	if release == nil {
		return errors.New("resume Helper removal: reference lease returned no release function")
	}
	defer release()
	if _, err := request.Remote.Lstat(ctx, claim.FinalPath); errors.Is(err, ErrRemoteNotExist) {
		return request.State.CompleteRemoval(claim.EndpointID, claim.ArtifactID)
	} else if err != nil {
		return fmt.Errorf("resume Helper removal: inspect claimed artifact: %w", err)
	}
	plan, err := prepareInstalledArtifact(ctx, request, claim.ArtifactID)
	if err != nil {
		return fmt.Errorf("resume Helper removal: %w", err)
	}
	return removePreparedArtifactWithLease(ctx, request, plan)
}

func prepareInstalledArtifact(ctx context.Context, request EnableRequest, artifact ArtifactID) (EnablePlan, error) {
	if ctx == nil || request.EndpointID == "" || request.ProtocolMajor == 0 || request.Target.OS == "" || request.Target.Arch == "" || request.State == nil || request.Remote == nil || artifact.ProtocolMajor != request.ProtocolMajor || artifact.OS != request.Target.OS || artifact.Arch != request.Target.Arch {
		return EnablePlan{}, errors.New("prepare installed Helper artifact: request is incomplete or mismatched")
	}
	record, err := request.State.LoadArtifact(request.EndpointID, artifact)
	if err != nil {
		return EnablePlan{}, err
	}
	manifest, err := verifyCurrentPolicy(request.Verifier, request.Policy, record.RawManifest, record.RawSignature)
	if err != nil {
		return EnablePlan{}, err
	}
	if manifest.ArtifactID() != artifact {
		return EnablePlan{}, errors.New("prepare installed Helper artifact: signed metadata does not match exact artifact")
	}
	if err := validateProbeUtilities(ctx, request.Remote, nil); err != nil {
		return EnablePlan{}, err
	}
	observation, err := request.Remote.Probe(ctx)
	if err != nil {
		return EnablePlan{}, fmt.Errorf("binding probe: %w", err)
	}
	snapshot, err := inspectInstallSnapshotFromObservation(ctx, request.Remote, manifest, observation)
	if err != nil {
		return EnablePlan{}, err
	}
	if !snapshot.FinalExists || len(snapshot.CreateDirectories) != 0 || snapshot.Plan.FinalPath != record.FinalPath {
		return EnablePlan{}, errors.New("prepare installed Helper artifact: derived path or remote artifact changed")
	}
	return EnablePlan{EndpointID: request.EndpointID, Manifest: manifest, Observation: observation, FinalPath: record.FinalPath}, nil
}

func removePreparedArtifact(ctx context.Context, request EnableRequest, plan EnablePlan, leaser RemovalLeaser) error {
	if leaser == nil {
		return errors.New("remove enabled helper: durable reference lease is required")
	}
	release, err := leaser.AcquireHelperRemoval(ctx, request.EndpointID, plan.Manifest.ArtifactID())
	if err != nil {
		return fmt.Errorf("remove enabled helper: acquire durable reference lease: %w", err)
	}
	if release == nil {
		return errors.New("remove enabled helper: reference lease returned no release function")
	}
	defer release()
	return removePreparedArtifactWithLease(ctx, request, plan)
}

func removePreparedArtifactWithLease(ctx context.Context, request EnableRequest, plan EnablePlan) error {
	claim, err := request.State.BeginRemoval(request.EndpointID, plan.Manifest.ArtifactID())
	if err != nil {
		return fmt.Errorf("remove enabled helper: persist removal claim: %w", err)
	}
	if claim.FinalPath != plan.FinalPath {
		return errors.New("remove enabled helper: durable claim path differs from verified artifact")
	}
	attrs, err := request.Remote.Lstat(ctx, plan.FinalPath)
	if errors.Is(err, ErrRemoteNotExist) {
		return request.State.CompleteRemoval(request.EndpointID, plan.Manifest.ArtifactID())
	}
	if err != nil || !exactOwned(attrs, plan.Observation.UID, RemoteRegular, 0o700, plan.Manifest.Size) {
		return errors.New("remove enabled helper: final artifact attributes changed after validation")
	}
	if err := request.Remote.RemoveExact(ctx, plan.FinalPath); err != nil {
		return fmt.Errorf("remove enabled helper: remove exact artifact: %w", err)
	}
	if err := request.State.CompleteRemoval(request.EndpointID, plan.Manifest.ArtifactID()); err != nil {
		return fmt.Errorf("remove enabled helper: persist completed removal: %w", err)
	}
	return nil
}
