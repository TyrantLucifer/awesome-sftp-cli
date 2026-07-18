package app

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"strings"
	"testing"
	"time"

	"github.com/TyrantLucifer/awesome-mac-sftp/internal/buildinfo"
	"github.com/TyrantLucifer/awesome-mac-sftp/internal/config"
	"github.com/TyrantLucifer/awesome-mac-sftp/internal/daemon"
	"github.com/TyrantLucifer/awesome-mac-sftp/internal/diagnostic"
	"github.com/TyrantLucifer/awesome-mac-sftp/internal/doctor"
	"github.com/TyrantLucifer/awesome-mac-sftp/internal/domain"
	"github.com/TyrantLucifer/awesome-mac-sftp/internal/ipc"
	"github.com/TyrantLucifer/awesome-mac-sftp/internal/job"
	"github.com/TyrantLucifer/awesome-mac-sftp/internal/redaction"
	"github.com/TyrantLucifer/awesome-mac-sftp/internal/state/jobstore"
	"github.com/TyrantLucifer/awesome-mac-sftp/internal/supportbundle"
	"github.com/TyrantLucifer/awesome-mac-sftp/internal/transfer"
)

func TestSupportBundlePreviewAndCreateRequireExactRecomposedConsent(t *testing.T) {
	t.Parallel()

	sources := reviewedSupportBundleSources()
	composeCalls := 0
	publishCalls := 0
	var published []byte
	runtime := supportBundleRuntime{
		compose: func(context.Context) ([]supportbundle.Source, error) {
			composeCalls++
			return append([]supportbundle.Source(nil), sources...), nil
		},
		publish: func(_ context.Context, path string, content []byte) error {
			publishCalls++
			if path != "/private/support.tar.gz" {
				t.Fatalf("publication path = %q", path)
			}
			published = append([]byte(nil), content...)
			return nil
		},
	}

	var previewOutput bytes.Buffer
	if err := runSupportBundleWithRuntime(t.Context(), []string{"preview", "--format", "json"}, &previewOutput, runtime); err != nil {
		t.Fatalf("preview: %v", err)
	}
	var plan supportbundle.Plan
	if err := json.Unmarshal(previewOutput.Bytes(), &plan); err != nil {
		t.Fatalf("decode preview: %v\n%s", err, previewOutput.String())
	}
	if composeCalls != 1 || publishCalls != 0 || plan.ConsentDigest == "" || len(plan.Files) != 9 {
		t.Fatalf("preview calls=(%d,%d) plan=%#v", composeCalls, publishCalls, plan)
	}

	var createOutput bytes.Buffer
	args := []string{"create", "--consent", plan.ConsentDigest, "--output", "/private/support.tar.gz", "--format", "json"}
	if err := runSupportBundleWithRuntime(t.Context(), args, &createOutput, runtime); err != nil {
		t.Fatalf("create: %v", err)
	}
	if composeCalls != 2 || publishCalls != 1 || len(published) == 0 || len(published) > supportbundle.MaxBundleBytes {
		t.Fatalf("create calls=(%d,%d) bundle=%d", composeCalls, publishCalls, len(published))
	}
	if strings.Contains(createOutput.String(), "/private/support.tar.gz") {
		t.Fatalf("create output exposed destination: %s", createOutput.String())
	}
	var result map[string]any
	if err := json.Unmarshal(createOutput.Bytes(), &result); err != nil {
		t.Fatalf("decode create result: %v", err)
	}
	if result["status"] != "published" || result["sha256"] == "" || result["size"] == nil {
		t.Fatalf("create result = %#v", result)
	}

	sources[0].Bytes = []byte(`{"version":"changed"}`)
	if err := runSupportBundleWithRuntime(t.Context(), args, io.Discard, runtime); err == nil || publishCalls != 1 {
		t.Fatalf("changed source create error=%v publish calls=%d", err, publishCalls)
	}
}

func TestSupportBundleRejectsArgumentsBeforeCompositionOrPublication(t *testing.T) {
	t.Parallel()

	for _, args := range [][]string{
		{},
		{"preview", "--format", "yaml"},
		{"create", "--consent", "bad", "--output", "/private/support.tar.gz"},
		{"create", "--consent", strings.Repeat("a", 64), "--output", "relative.tar.gz"},
		{"create", "--consent", strings.Repeat("a", 64)},
		{"unknown"},
	} {
		composeCalls := 0
		publishCalls := 0
		runtime := supportBundleRuntime{
			compose: func(context.Context) ([]supportbundle.Source, error) {
				composeCalls++
				return nil, errors.New("must not compose")
			},
			publish: func(context.Context, string, []byte) error { publishCalls++; return errors.New("must not publish") },
		}
		err := runSupportBundleWithRuntime(t.Context(), args, io.Discard, runtime)
		if err == nil || exitCode(err) != ExitUsage {
			t.Fatalf("args %#v error=%v exit=%d", args, err, exitCode(err))
		}
		if composeCalls != 0 || publishCalls != 0 {
			t.Fatalf("args %#v reached runtime: compose=%d publish=%d", args, composeCalls, publishCalls)
		}
	}
}

func TestSupportBundleCompositionIncludesReviewedLayoutWithoutRawIdentityContentOrPaths(t *testing.T) {
	t.Parallel()

	const raw = "stage6-secret-user@example.com/private/key?token=askpass-answer"
	applicationConfig := config.Default()
	applicationConfig.External.Editor = &config.CommandConfig{Executable: "/" + raw, Args: []string{raw}}
	snapshot := supportBundleSnapshot{
		Build:        buildinfo.Info{Version: "1.0.0", Commit: raw, GoVersion: "go1.26.5", GOOS: "linux", GOARCH: "amd64"},
		Config:       applicationConfig,
		ConfigStatus: "valid",
		Doctor: doctor.Report{OutputVersion: 1, Results: []doctor.Result{{
			Code: doctor.CheckConfig, Status: doctor.Pass, Severity: doctor.Info,
			DetailCode: "config_valid", Remediation: "troubleshooting/config",
		}}},
		Diagnostics: []diagnostic.Record{{
			Sequence: 1, Time: time.Unix(1, 0).UTC(), Level: "INFO", Message: raw,
			Component: raw, Event: raw, EndpointID: domain.EndpointID(raw), JobID: domain.JobID(raw),
			RequestID: domain.RequestID(raw), ErrorCode: domain.Code(raw),
		}},
		Jobs: []transfer.JobView{{
			Snapshot: jobstore.Snapshot{JobID: domain.JobID(raw), PlanID: raw, State: job.StateRunning},
			Kind:     transfer.OperationCopy, Route: transfer.RouteSFTPRelay, PlannedRoute: transfer.RouteSFTPRelay,
			Source: domain.Location{EndpointID: domain.EndpointID(raw), Path: domain.CanonicalPath("/" + raw)},
			Final:  domain.Location{EndpointID: domain.EndpointID(raw), Path: domain.CanonicalPath("/" + raw)},
			Phase:  transfer.PhaseStreaming, Bytes: 4, Items: 1, WaitingReason: raw, RecentError: raw,
		}},
		DaemonInfo: daemon.ClientInfo{
			DaemonVersion: raw, Protocol: ipc.ProtocolVersion{Major: 1, Minor: 0},
			Features: []ipc.ProtocolFeature{{Name: raw, Version: 1}}, EventTypes: []string{raw},
		},
		DaemonStatus:   "reachable",
		DatabaseStatus: "healthy",
		DatabaseDetail: raw,
	}
	sources, err := composeSupportBundleSources(snapshot)
	if err != nil {
		t.Fatalf("composeSupportBundleSources(): %v", err)
	}
	if len(sources) != 8 {
		t.Fatalf("source count = %d, want 8", len(sources))
	}
	if _, err := supportbundle.Preview(sources); err != nil {
		t.Fatalf("Preview(composed): %v", err)
	}
	for _, source := range sources {
		if bytes.Contains(source.Bytes, []byte(raw)) || bytes.Contains(source.Bytes, []byte("/"+raw)) {
			t.Fatalf("%s exposed seeded raw value: %s", source.Name, source.Bytes)
		}
		if source.Sensitivity == redaction.Secret || source.Sensitivity == redaction.Content {
			t.Fatalf("%s has forbidden sensitivity %q", source.Name, source.Sensitivity)
		}
	}
}

func reviewedSupportBundleSources() []supportbundle.Source {
	return []supportbundle.Source{
		{Name: "version.json", Sensitivity: redaction.SystemMetadata, Bytes: []byte(`{"version":"1.0.0"}`)},
		{Name: "platform.json", Sensitivity: redaction.SystemMetadata, Bytes: []byte(`{"goos":"linux"}`)},
		{Name: "config-shape.json", Sensitivity: redaction.Pseudonymous, Bytes: []byte(`{"status":"valid"}`)},
		{Name: "doctor.json", Sensitivity: redaction.SystemMetadata, Bytes: []byte(`{"output_version":1}`)},
		{Name: "diagnostics.json", Sensitivity: redaction.Pseudonymous, Bytes: []byte(`{"records":[]}`)},
		{Name: "jobs.json", Sensitivity: redaction.Pseudonymous, Bytes: []byte(`{"jobs":[]}`)},
		{Name: "database-health.json", Sensitivity: redaction.SystemMetadata, Bytes: []byte(`{"status":"healthy"}`)},
		{Name: "capabilities.json", Sensitivity: redaction.SystemMetadata, Bytes: []byte(`{"status":"reachable"}`)},
	}
}
