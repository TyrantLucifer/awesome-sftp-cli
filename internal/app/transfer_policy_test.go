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
	if policy != (transfer.DirectPolicy{Integrity: transfer.IntegrityRequireStrong}) {
		t.Fatalf("runtime direct policy = %#v", policy)
	}
	source := transfer.FileRef{Location: domain.Location{EndpointID: "ep_aaaaaaaaaaaaaaaaaaaaaaaaaa", Path: "/source"}}
	destination := domain.Location{EndpointID: "ep_bbbbbbbbbbbbbbbbbbbbbbbbbb", Path: "/destination"}
	input := tui.Intent{Clipboard: transfer.ClipboardCut, Source: source, Location: destination, Name: "renamed"}
	want := transfer.Intent{
		Clipboard: transfer.ClipboardCut, Source: source, DestinationDirectory: destination,
		Name: "renamed", ConflictPolicy: transfer.ConflictAsk, DirectPolicy: policy,
	}
	if intent := configuredCopyIntent(input, policy); intent != want {
		t.Fatalf("configured copy intent = %#v, want %#v", intent, want)
	}
}
