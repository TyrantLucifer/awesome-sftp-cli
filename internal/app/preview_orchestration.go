//go:build darwin || linux

package app

import (
	"context"
	"fmt"

	"github.com/TyrantLucifer/awesome-sftp-cli/internal/cache"
	"github.com/TyrantLucifer/awesome-sftp-cli/internal/cacheprocess"
	"github.com/TyrantLucifer/awesome-sftp-cli/internal/daemon"
	"github.com/TyrantLucifer/awesome-sftp-cli/internal/domain"
	"github.com/TyrantLucifer/awesome-sftp-cli/internal/externalpreviewer"
	"github.com/TyrantLucifer/awesome-sftp-cli/internal/ipc"
	builtinpreview "github.com/TyrantLucifer/awesome-sftp-cli/internal/preview"
)

type previewRPCCaller interface {
	Call(context.Context, string, any, any) error
}

func previewMaterializer(client previewRPCCaller, location domain.Location, source builtinpreview.FrozenSource, hasFileSize bool, fileSize uint64, workspaceID cache.WorkspaceID, policy cache.Policy, ownerID string) externalpreviewer.MaterializeFunc {
	return func(ctx context.Context, maximumBytes int64) (externalpreviewer.LeasedMaterialization, error) {
		if client == nil || ctx == nil || maximumBytes <= 0 || ownerID == "" || len(ownerID) > 128 {
			return externalpreviewer.LeasedMaterialization{}, fmt.Errorf("materialize preview fallback: invalid request")
		}
		if hasFileSize && (fileSize > uint64(maximumBytes) || fileSize > uint64(cache.DefaultGlobalBytes)) {
			return externalpreviewer.LeasedMaterialization{}, fmt.Errorf("materialize preview fallback: source exceeds bounded input")
		}
		process, err := cacheprocess.CurrentIdentity()
		if err != nil {
			return externalpreviewer.LeasedMaterialization{}, fmt.Errorf("materialize preview fallback process: %w", err)
		}
		var response daemon.CacheMaterializeResponse
		err = client.Call(ctx, daemon.CacheMaterialize, daemon.CacheMaterializeRequest{
			Location: ipc.EncodeLocation(location), WorkspaceID: workspaceID, Policy: policy,
			Pinned: policy == cache.PolicyPinnedOffline, OwnerKind: cache.LeaseOwnerPreview, OwnerID: ownerID, Process: &process,
		}, &response)
		if err != nil {
			return externalpreviewer.LeasedMaterialization{}, err
		}
		leased := externalpreviewer.LeasedMaterialization{
			Path: response.Path, Complete: true,
			Release: func(releaseCtx context.Context) error {
				var released daemon.CacheReleaseHandoffResponse
				return client.Call(releaseCtx, daemon.CacheReleaseHandoff, daemon.CacheReleaseHandoffRequest{
					MaterializationID: response.MaterializationID, ReferenceID: response.ReferenceID, LeaseID: response.LeaseID,
					OwnerKind: cache.LeaseOwnerPreview, OwnerID: ownerID,
				}, &released)
			},
		}
		leased.Verified = source.Matches(location, ipc.DecodeFingerprint(response.SourceFingerprint))
		if !leased.Verified {
			return leased, fmt.Errorf("materialize preview fallback: source fingerprint changed")
		}
		return leased, nil
	}
}
