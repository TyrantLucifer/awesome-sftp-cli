//go:build darwin || linux

package app

import (
	"testing"

	"github.com/TyrantLucifer/awesome-sftp-cli/internal/config"
	"github.com/TyrantLucifer/awesome-sftp-cli/internal/domain"
	"github.com/TyrantLucifer/awesome-sftp-cli/internal/transfer"
	"github.com/TyrantLucifer/awesome-sftp-cli/internal/tui"
)

func TestRuntimeDirectPolicyAndConfiguredCopyIntentRemainProductionClosed(t *testing.T) {
	policy := runtimeDirectPolicy(
		config.IntegrityConfig{TransferPolicy: "require_strong"},
		config.DirectTransferConfig{Enabled: false},
	)
	if policy != (transfer.DirectPolicy{Integrity: domain.IntegrityRequireStrong}) {
		t.Fatalf("runtime direct policy = %#v", policy)
	}
	source := transfer.FileRef{Location: domain.Location{EndpointID: "ep_aaaaaaaaaaaaaaaaaaaaaaaaaa", Path: "/source"}}
	destination := domain.Location{EndpointID: "ep_bbbbbbbbbbbbbbbbbbbbbbbbbb", Path: "/destination"}
	input := tui.Intent{Clipboard: transfer.ClipboardCut, Source: source, Location: destination, Name: "renamed"}
	want := transfer.Intent{
		Clipboard: transfer.ClipboardCut, Source: source, DestinationDirectory: destination,
		Name: "renamed", ConflictPolicy: transfer.ConflictAsk, DirectPolicy: policy,
	}
	if intent := configuredCopyIntent(input, policy, config.TransferConfig{}); intent != want {
		t.Fatalf("configured copy intent = %#v, want %#v", intent, want)
	}
}

func TestConfiguredCopyIntentCarriesIndependentCompletionPolicies(t *testing.T) {
	intent := configuredCopyIntent(tui.Intent{Clipboard: transfer.ClipboardCopy}, transfer.DirectPolicy{}, config.TransferConfig{Verification: "sha256", Durability: "completion"})
	if intent.Verification != transfer.VerifySHA256 || intent.Durability != transfer.DurabilityCompletion {
		t.Fatalf("completion intent=%#v", intent)
	}
}
