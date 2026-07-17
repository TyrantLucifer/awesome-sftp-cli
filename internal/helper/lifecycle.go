package helper

import (
	"context"
	"errors"
	"fmt"

	"github.com/TyrantLucifer/awesome-mac-sftp/internal/domain"
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
	release, err := leaser.AcquireHelperRemoval(ctx, request.EndpointID, plan.Manifest.ArtifactID())
	if err != nil {
		return fmt.Errorf("remove enabled helper: acquire durable reference lease: %w", err)
	}
	if release == nil {
		return errors.New("remove enabled helper: reference lease returned no release function")
	}
	defer release()
	attrs, err := request.Remote.Lstat(ctx, plan.FinalPath)
	if err != nil || !exactOwned(attrs, plan.Observation.UID, RemoteRegular, 0o700, plan.Manifest.Size) {
		return errors.New("remove enabled helper: final artifact attributes changed after validation")
	}
	if err := request.Remote.RemoveExact(ctx, plan.FinalPath); err != nil {
		return fmt.Errorf("remove enabled helper: remove exact artifact: %w", err)
	}
	if err := request.State.Disable(request.EndpointID, request.ProtocolMajor, request.Target); err != nil {
		return fmt.Errorf("remove enabled helper: persist disabled state: %w", err)
	}
	return nil
}
