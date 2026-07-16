//go:build darwin || linux

package externalpreviewer

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/TyrantLucifer/awesome-mac-sftp/internal/preview"
)

func TestOrchestrateKeepsSupportedBuiltinFormatsOutOfExternalProcesses(t *testing.T) {
	t.Parallel()
	called := false
	outcome := Orchestrate(context.Background(), newTestRunner(t, []Rule{{
		Name: "text", Match: Match{Extensions: []string{".txt"}}, Command: helperCommand(t, "exit", "0"),
		Timeout: 5 * time.Second, MaxInputBytes: 1024,
	}}), OrchestrationRequest{
		Path: "notes.txt", MediaType: "text/plain", BuiltIn: preview.Result{Kind: preview.KindText, View: preview.ViewAuto},
		Materialize: func(context.Context, int64) (LeasedMaterialization, error) {
			called = true
			return LeasedMaterialization{}, nil
		},
	})
	if outcome.Kind != OutcomeBuiltIn || called {
		t.Fatalf("outcome=%+v materialized=%t", outcome, called)
	}
}

func TestOrchestrateCanceledRequestNeverMaterializes(t *testing.T) {
	t.Parallel()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	called := false
	outcome := Orchestrate(ctx, newTestRunner(t, []Rule{{Name: "all", Match: Match{Extensions: []string{".bin"}}, Command: helperCommand(t, "exit", "0"), Timeout: 5 * time.Second, MaxInputBytes: 10}}), OrchestrationRequest{
		Path: "asset.bin", BuiltIn: preview.Result{Kind: preview.KindBinary, View: preview.ViewAuto}, HasFileSize: true, FileSize: 1,
		Materialize: func(context.Context, int64) (LeasedMaterialization, error) {
			called = true
			return LeasedMaterialization{}, nil
		},
	})
	if called || outcome.Kind != OutcomeBuiltIn || outcome.Code != CodeOrchestrationCanceled {
		t.Fatalf("outcome=%+v called=%t", outcome, called)
	}
}

func TestOrchestrateReleasesPartialLeaseWhenMaterializationReportsFailure(t *testing.T) {
	t.Parallel()
	released := false
	outcome := Orchestrate(context.Background(), newTestRunner(t, []Rule{{Name: "all", Match: Match{Extensions: []string{".bin"}}, Command: helperCommand(t, "exit", "0"), Timeout: 5 * time.Second, MaxInputBytes: 10}}), OrchestrationRequest{
		Path: "asset.bin", BuiltIn: preview.Result{Kind: preview.KindBinary, View: preview.ViewAuto}, HasFileSize: true, FileSize: 1,
		Materialize: func(context.Context, int64) (LeasedMaterialization, error) {
			return LeasedMaterialization{Release: func(context.Context) error { released = true; return nil }}, errors.New("failed after lease")
		},
	})
	if !released || outcome.Kind != OutcomeBuiltIn || outcome.Code != CodeMaterializeFailed {
		t.Fatalf("outcome=%+v released=%t", outcome, released)
	}
}

func TestOrchestrateUsesConfirmedImageOutputAndReleasesLease(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	path := writeMaterialization(t, dir, "asset.png", orchestrationPNG(t))
	proof, err := preview.ConfirmImageCapability(preview.ImageProtocolKitty, []byte("\x1b_Gi=31;OK\x1b\\"))
	if err != nil {
		t.Fatal(err)
	}
	released := 0
	outcome := Orchestrate(context.Background(), nil, OrchestrationRequest{
		Path: "asset.png", MediaType: "image/png", BuiltIn: preview.Result{Kind: preview.KindImage, View: preview.ViewAuto, Image: &preview.ImageMetadata{MediaType: "image/png", Width: 1, Height: 1}},
		HasFileSize: true, FileSize: uint64(len(orchestrationPNG(t))), Capability: proof, ImageLimits: preview.DefaultImageOutputLimits(),
		Materialize: func(_ context.Context, maximum int64) (LeasedMaterialization, error) {
			if maximum != int64(preview.DefaultImageOutputLimits().MaxPayloadBytes) {
				t.Fatalf("maximum=%d", maximum)
			}
			return LeasedMaterialization{Path: path, Complete: true, Verified: true, Release: func(context.Context) error { released++; return nil }}, nil
		},
	})
	if outcome.Kind != OutcomeTerminalImage || outcome.ImageProtocol != preview.ImageProtocolKitty || len(outcome.TerminalBytes) == 0 || released != 1 || outcome.ReleaseFailed {
		t.Fatalf("outcome=%+v released=%d", outcome, released)
	}
}

func TestOrchestrateUnconfirmedImageEmitsNothingAndDoesNotMaterialize(t *testing.T) {
	t.Parallel()
	called := false
	outcome := Orchestrate(context.Background(), nil, OrchestrationRequest{
		Path: "asset.png", MediaType: "image/png", BuiltIn: preview.Result{Kind: preview.KindImage, View: preview.ViewAuto, Image: &preview.ImageMetadata{MediaType: "image/png", Width: 1, Height: 1}},
		HasFileSize: true, FileSize: 68, ImageLimits: preview.DefaultImageOutputLimits(),
		Materialize: func(context.Context, int64) (LeasedMaterialization, error) {
			called = true
			return LeasedMaterialization{}, nil
		},
	})
	if outcome.Kind != OutcomeBuiltIn || called || len(outcome.TerminalBytes) != 0 || outcome.ImageProtocol != preview.ImageProtocolNone {
		t.Fatalf("outcome=%+v called=%t", outcome, called)
	}
}

func TestOrchestrateFallsBackToOrderedExternalRuleUnderLease(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	capture := filepath.Join(dir, "capture")
	path := writeMaterialization(t, dir, "asset.bin", []byte("body"))
	runner := newTestRunner(t, []Rule{
		{Name: "first", Match: Match{Extensions: []string{".bin"}}, Command: helperCommand(t, "capture", capture, "first"), Timeout: 5 * time.Second, MaxInputBytes: 10},
		{Name: "second", Match: Match{Extensions: []string{".bin"}}, Command: helperCommand(t, "capture", capture, "second"), Timeout: 5 * time.Second, MaxInputBytes: 10},
	})
	released := false
	outcome := Orchestrate(context.Background(), runner, OrchestrationRequest{
		Path: "asset.bin", MediaType: "application/octet-stream", BuiltIn: preview.Result{Kind: preview.KindBinary, View: preview.ViewAuto},
		HasFileSize: true, FileSize: 4,
		Materialize: func(_ context.Context, maximum int64) (LeasedMaterialization, error) {
			if maximum != 10 {
				t.Fatalf("maximum=%d", maximum)
			}
			return LeasedMaterialization{Path: path, Complete: true, Verified: true, Release: func(context.Context) error { released = true; return nil }}, nil
		},
	})
	if outcome.Kind != OutcomeExternal || outcome.External.Rule != "first" || !released {
		t.Fatalf("outcome=%+v released=%t", outcome, released)
	}
	if got := string(mustReadFile(t, capture)); !strings.Contains(got, "first") || strings.Contains(got, "second") {
		t.Fatalf("capture=%q", got)
	}
}

func TestOrchestrateRejectsUnverifiedOrSymlinkMaterializationAndStillReleases(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	path := writeMaterialization(t, dir, "asset.bin", []byte("x"))
	alias := filepath.Join(dir, "alias.bin")
	if err := os.Symlink(path, alias); err != nil {
		t.Fatal(err)
	}
	for name, leased := range map[string]LeasedMaterialization{
		"unverified": {Path: path, Complete: true, Release: func(context.Context) error { return nil }},
		"symlink":    {Path: alias, Complete: true, Verified: true, Release: func(context.Context) error { return nil }},
		"no release": {Path: path, Complete: true, Verified: true},
	} {
		t.Run(name, func(t *testing.T) {
			released := false
			original := leased.Release
			if original != nil {
				leased.Release = func(ctx context.Context) error { released = true; return original(ctx) }
			}
			outcome := Orchestrate(context.Background(), newTestRunner(t, []Rule{{Name: "all", Match: Match{Extensions: []string{".bin"}}, Command: helperCommand(t, "exit", "0"), Timeout: 5 * time.Second, MaxInputBytes: 10}}), OrchestrationRequest{
				Path: "asset.bin", BuiltIn: preview.Result{Kind: preview.KindBinary, View: preview.ViewAuto}, HasFileSize: true, FileSize: 1,
				Materialize: func(context.Context, int64) (LeasedMaterialization, error) { return leased, nil },
			})
			if outcome.Kind != OutcomeBuiltIn || outcome.Code != CodeUnsafeMaterialization || original != nil && !released {
				t.Fatalf("outcome=%+v released=%t", outcome, released)
			}
		})
	}
}

func TestOrchestrateHundredGiBSparseFileStopsBeforeReadOrMaterialize(t *testing.T) {
	t.Parallel()
	const size = uint64(100) << 30
	dir := t.TempDir()
	path := filepath.Join(dir, "huge.bin")
	file, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := file.Truncate(int64(size)); err != nil {
		t.Fatal(err)
	}
	if err := file.Close(); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if uint64(info.Size()) != size {
		t.Fatalf("sparse size=%d", info.Size())
	}
	called := false
	started := time.Now()
	outcome := Orchestrate(context.Background(), newTestRunner(t, []Rule{{Name: "small", Match: Match{Extensions: []string{".bin"}}, Command: helperCommand(t, "exit", "0"), Timeout: 5 * time.Second, MaxInputBytes: 64 << 10}}), OrchestrationRequest{
		Path: "huge.bin", BuiltIn: preview.Result{Kind: preview.KindBinary, View: preview.ViewAuto}, HasFileSize: true, FileSize: uint64(info.Size()),
		Materialize: func(context.Context, int64) (LeasedMaterialization, error) {
			called = true
			return LeasedMaterialization{}, errors.New("must not run")
		},
	})
	if called || outcome.Kind != OutcomeBuiltIn || outcome.External.Code != CodeInputTooLarge || time.Since(started) > time.Second {
		t.Fatalf("outcome=%+v called=%t duration=%s", outcome, called, time.Since(started))
	}
}

func TestOrchestrateFailureAndMaliciousDiagnosticReturnSafeBuiltinFallback(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	path := writeMaterialization(t, dir, "asset.bin", []byte("x"))
	outcome := Orchestrate(context.Background(), newTestRunner(t, []Rule{{Name: "bad", Match: Match{Extensions: []string{".bin"}}, Command: helperCommand(t, "ansi-diagnostic", "secret"), Timeout: 5 * time.Second, MaxInputBytes: 10, Redact: []string{"secret"}}}), OrchestrationRequest{
		Path: "asset.bin", BuiltIn: preview.Result{Kind: preview.KindBinary, View: preview.ViewAuto}, HasFileSize: true, FileSize: 1,
		Materialize: func(context.Context, int64) (LeasedMaterialization, error) {
			return LeasedMaterialization{Path: path, Complete: true, Verified: true, Release: func(context.Context) error { return nil }}, nil
		},
	})
	if outcome.Kind != OutcomeBuiltIn || outcome.External.Status != StatusNonZero || strings.ContainsAny(outcome.External.Diagnostic, "\x1b\r\n") || strings.Contains(outcome.External.Diagnostic, "secret") || !strings.Contains(outcome.External.Diagnostic, "[redacted]") {
		t.Fatalf("outcome=%+v", outcome)
	}
}

func orchestrationPNG(t *testing.T) []byte {
	t.Helper()
	return testPNGBytes(t)
}

func testPNGBytes(t *testing.T) []byte {
	t.Helper()
	// Same small, valid fixture as preview protocol tests without exporting test helpers.
	return []byte{137, 80, 78, 71, 13, 10, 26, 10, 0, 0, 0, 13, 73, 72, 68, 82, 0, 0, 0, 1, 0, 0, 0, 1, 8, 4, 0, 0, 0, 181, 28, 12, 2, 0, 0, 0, 11, 73, 68, 65, 84, 120, 218, 99, 100, 248, 15, 0, 1, 5, 1, 1, 39, 24, 227, 102, 0, 0, 0, 0, 73, 69, 78, 68, 174, 66, 96, 130}
}

func TestOrchestrateReportsReleaseFailureWithoutReplacingSafeResult(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	path := writeMaterialization(t, dir, "asset.bin", []byte("x"))
	outcome := Orchestrate(context.Background(), newTestRunner(t, []Rule{{Name: "ok", Match: Match{Extensions: []string{".bin"}}, Command: helperCommand(t, "exit", "0"), Timeout: 5 * time.Second, MaxInputBytes: 10}}), OrchestrationRequest{
		Path: "asset.bin", BuiltIn: preview.Result{Kind: preview.KindBinary, View: preview.ViewAuto}, HasFileSize: true, FileSize: 1,
		Materialize: func(context.Context, int64) (LeasedMaterialization, error) {
			return LeasedMaterialization{Path: path, Complete: true, Verified: true, Release: func(context.Context) error { return errors.New("private path must not escape") }}, nil
		},
	})
	if outcome.Kind != OutcomeExternal || !outcome.ReleaseFailed || bytes.Contains(outcome.TerminalBytes, []byte("private")) {
		t.Fatalf("outcome=%+v", outcome)
	}
}
