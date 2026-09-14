package transfer

import (
	"context"
	"errors"

	"github.com/TyrantLucifer/awesome-sftp-cli/internal/domain"
	providerapi "github.com/TyrantLucifer/awesome-sftp-cli/internal/provider"
)

// Durability is independent of content verification. The empty value retains
// the checkpoint-by-checkpoint Sync contract of already frozen plans.
type Durability string

const (
	DurabilityNone       Durability = "none"
	DurabilityCompletion Durability = "completion"
	DurabilityCheckpoint Durability = "checkpoint"
)

// CompletionEvidence keeps server-acknowledged progress (Checkpoint.Offset)
// separate from successful Sync requests and destination content readback.
// Sync evidence concerns file data, not parent-directory durability or a
// guarantee that a remote server's storage hardware honors its acknowledgments.
type CompletionEvidence struct {
	Version         uint16 `json:"version"`
	DurableBytes    uint64 `json:"durable_bytes"`
	ContentVerified bool   `json:"content_verified"`
}

func newCompletionEvidence(plan Plan) CompletionEvidence {
	if plan.Durability != "" {
		return CompletionEvidence{Version: 1}
	}
	return CompletionEvidence{}
}

func (c Checkpoint) durableBytes() uint64 {
	if c.Completion.Version == 0 {
		return c.Offset
	}
	return c.Completion.DurableBytes
}

func (p Plan) checkpointSync() bool {
	return p.Durability == "" || p.Durability == DurabilityCheckpoint
}

func freezeCompletionPolicy(plan *Plan, intent Intent) error {
	verification := intent.Verification
	if verification == "" {
		verification = VerifyProtocol
	}
	durability := intent.Durability
	if durability == "" {
		durability = DurabilityNone
	}
	if verification != VerifyProtocol && verification != VerifySHA256 ||
		durability != DurabilityNone && durability != DurabilityCompletion && durability != DurabilityCheckpoint {
		return errors.New("freeze transfer: unsupported completion policy")
	}
	if intent.DirectPolicy.Integrity == domain.IntegrityRequireStrong {
		verification = VerifySHA256
	}
	plan.Verification = verification
	plan.Durability = durability
	if plan.Kind == OperationMove {
		plan.Verification = VerifySHA256
		plan.Durability = ""
	}
	// This spelling explicitly opts into the legacy restart contract.
	if plan.Verification == VerifySHA256 && plan.Durability == DurabilityCheckpoint {
		plan.Durability = ""
	}
	if plan.Durability != "" && plan.StreamPolicy.CheckpointBytes != 0 {
		plan.StreamPolicy.CheckpointIntervalMS = 5000
	}
	if plan.Durability != "" && plan.Source.Kind == domain.EntryDirectory {
		plan.StreamPolicy.DirectoryWorkers = 8
	}
	return nil
}

func validCompletionPolicy(p Plan) bool {
	if p.Durability != "" && (p.Version != 1 || p.Kind != OperationCopy || p.Route != RouteLocal && p.Route != RouteSFTPRelay) {
		return false
	}
	if p.DirectPolicy.Integrity == domain.IntegrityRequireStrong && p.Verification != VerifySHA256 {
		return false
	}
	if p.Verification != VerifySHA256 && p.Verification != VerifyProtocol {
		return false
	}
	if p.Durability != "" && p.Durability != DurabilityNone && p.Durability != DurabilityCompletion && p.Durability != DurabilityCheckpoint {
		return false
	}
	if p.Verification == VerifyProtocol && (p.Version != 1 || p.Kind != OperationCopy || p.Durability == "" || p.Route != RouteLocal && p.Route != RouteSFTPRelay) {
		return false
	}
	if (p.Kind == OperationMove || p.Version == 2) && (p.Verification != VerifySHA256 || !p.checkpointSync()) {
		return false
	}
	return true
}

func syncInitialPart(ctx context.Context, plan Plan, handle providerapi.WriteHandle) error {
	if plan.checkpointSync() {
		return handle.Sync(ctx)
	}
	return nil
}

func (c *Checkpoint) recordDurable() {
	if c.Completion.Version != 0 {
		c.Completion.DurableBytes = c.Offset
	}
}
func (c *Checkpoint) recordContentVerified() {
	if c.Completion.Version != 0 {
		c.Completion.ContentVerified = true
	}
}
