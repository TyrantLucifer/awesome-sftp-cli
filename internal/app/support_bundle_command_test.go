package app

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
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
	"github.com/TyrantLucifer/awesome-mac-sftp/internal/platform"
	"github.com/TyrantLucifer/awesome-mac-sftp/internal/redaction"
	"github.com/TyrantLucifer/awesome-mac-sftp/internal/state/jobstore"
	"github.com/TyrantLucifer/awesome-mac-sftp/internal/supportbundle"
	"github.com/TyrantLucifer/awesome-mac-sftp/internal/testkit"
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

func TestSupportBundleCompositionDropsSafeShapedSecretCorpus(t *testing.T) {
	t.Parallel()

	secrets := []string{
		"username_canary_7f3a",
		"hostname_canary_7f3a",
		"private_key_canary_7f3a",
		"askpass_answer_canary_7f3a",
		"kerberos_ticket_canary_7f3a",
		"environment_secret_canary_7f3a",
		"command_argument_canary_7f3a",
		"file_content_canary_7f3a",
	}
	for _, secret := range secrets {
		snapshot := supportBundleSnapshot{
			Build:  buildinfo.Info{Version: secret, Commit: secret, GoVersion: secret, GOOS: secret, GOARCH: secret},
			Config: config.Default(), ConfigStatus: "valid",
			Doctor: doctor.Report{OutputVersion: doctor.OutputVersion, Results: []doctor.Result{{
				Code: doctor.Code(secret), Status: doctor.Status(secret), Severity: doctor.Severity(secret),
				DetailCode: secret, Remediation: secret,
			}}},
			Diagnostics: []diagnostic.Record{{
				Sequence: 1, Time: time.Unix(1, 0).UTC(), Level: secret,
				Component: secret, Event: secret, ErrorCode: domain.Code(secret),
			}},
			Jobs: []transfer.JobView{{
				Snapshot: jobstore.Snapshot{State: job.State(secret)},
				Kind:     transfer.OperationKind(secret), Route: transfer.Route(secret),
				PlannedRoute: transfer.Route(secret), DowngradedFrom: transfer.Route(secret),
				RouteReason: transfer.RouteReason(secret), Phase: transfer.Phase(secret),
			}},
			DaemonInfo: daemon.ClientInfo{
				DaemonVersion: secret,
				Features:      []ipc.ProtocolFeature{{Name: secret, Version: 1}},
				EventTypes:    []string{secret},
			},
			DaemonStatus: secret, DatabaseStatus: secret, DatabaseDetail: secret,
		}
		sources, err := composeSupportBundleSources(snapshot)
		if err != nil {
			t.Fatalf("composeSupportBundleSources(%q): %v", secret, err)
		}
		for _, source := range sources {
			if bytes.Contains(source.Bytes, []byte(secret)) {
				t.Fatalf("%s retained safe-shaped secret %q: %s", source.Name, secret, source.Bytes)
			}
		}
		plan, err := supportbundle.Preview(sources)
		if err != nil {
			t.Fatalf("Preview(%q): %v", secret, err)
		}
		bundle, err := supportbundle.Build(sources, plan.ConsentDigest)
		if err != nil {
			t.Fatalf("Build(%q): %v", secret, err)
		}
		if expanded := expandSupportBundle(t, bundle); bytes.Contains(expanded, []byte(secret)) {
			t.Fatalf("published archive retained safe-shaped secret %q: %s", secret, expanded)
		}
	}
}

func TestSupportBundleGatheringPreservesCorruptStateAndExportsOnlyStableCodes(t *testing.T) {
	root := testkit.PersistentTempDir(t)
	paths := platform.Paths{
		ConfigDir: filepath.Join(root, "config"), ConfigFile: filepath.Join(root, "config", "config.json"),
		StateDir: filepath.Join(root, "state"), DatabaseFile: filepath.Join(root, "state", "state.db"),
		CacheDir: filepath.Join(root, "cache"), RuntimeDir: filepath.Join(root, "runtime"),
		ControlSocket: filepath.Join(root, "runtime", "control-v1.sock"),
	}
	for _, directory := range []string{paths.ConfigDir, paths.StateDir, paths.CacheDir} {
		if err := os.MkdirAll(directory, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	fixtures := map[string][]byte{
		paths.ConfigFile:                         []byte("corrupt-config-secret"),
		paths.DatabaseFile:                       []byte("corrupt-database-secret"),
		filepath.Join(paths.CacheDir, "unknown"): []byte("corrupt-cache-secret"),
	}
	for path, content := range fixtures {
		if err := os.WriteFile(path, content, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	const rawCause = "username_canary_7f3a/private/key?token=askpass"
	runtime := supportBundleGatherRuntime{
		paths:        paths,
		configExists: func() (bool, error) { return true, nil },
		loadConfig: func(string) (config.Config, error) {
			return config.Config{}, errors.New(rawCause)
		},
		runDoctor: func(ctx context.Context) doctor.Report {
			return doctor.Run(ctx, map[doctor.Code]doctor.Probe{
				doctor.CheckDatabase: func(context.Context) (doctor.Observation, error) { return doctor.Observation{}, errors.New(rawCause) },
				doctor.CheckCache:    func(context.Context) (doctor.Observation, error) { return doctor.Observation{}, errors.New(rawCause) },
			}, false)
		},
		probeDaemon: func(context.Context) (daemonControlClient, bool, error) {
			return nil, true, errors.New(rawCause)
		},
	}
	sources, err := gatherSupportBundleSourcesWithRuntime(t.Context(), runtime)
	if err != nil {
		t.Fatalf("gatherSupportBundleSourcesWithRuntime(): %v", err)
	}
	if _, err := supportbundle.Preview(sources); err != nil {
		t.Fatalf("Preview(corrupt state): %v", err)
	}
	for _, source := range sources {
		if bytes.Contains(source.Bytes, []byte(rawCause)) || bytes.Contains(source.Bytes, []byte("corrupt-")) {
			t.Fatalf("%s leaked corrupt state: %s", source.Name, source.Bytes)
		}
	}
	assertSupportBundleJSONField(t, sources, "config-shape.json", "status", "invalid")
	assertSupportBundleJSONField(t, sources, "database-health.json", "status", "fail")
	assertSupportBundleJSONField(t, sources, "database-health.json", "detail", "probe_failed")
	assertSupportBundleJSONField(t, sources, "capabilities.json", "status", "unhealthy")
	for path, want := range fixtures {
		// #nosec G304 -- every path is a fixed test-owned fixture below PersistentTempDir.
		got, readErr := os.ReadFile(path)
		if readErr != nil {
			t.Fatal(readErr)
		}
		if !bytes.Equal(got, want) {
			t.Fatalf("corrupt fixture %s changed: got %q want %q", path, got, want)
		}
	}
}

func assertSupportBundleJSONField(t *testing.T, sources []supportbundle.Source, name, field string, want any) {
	t.Helper()
	for _, source := range sources {
		if source.Name != name {
			continue
		}
		var document map[string]any
		if err := json.Unmarshal(source.Bytes, &document); err != nil {
			t.Fatal(err)
		}
		if document[field] != want {
			t.Fatalf("%s[%s] = %#v, want %#v", name, field, document[field], want)
		}
		return
	}
	t.Fatalf("support-bundle source %q not found", name)
}

func expandSupportBundle(t *testing.T, bundle []byte) []byte {
	t.Helper()
	compressed, err := gzip.NewReader(bytes.NewReader(bundle))
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := compressed.Close(); err != nil {
			t.Error(err)
		}
	}()
	reader := tar.NewReader(compressed)
	var expanded bytes.Buffer
	for {
		_, err := reader.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatal(err)
		}
		if _, err := io.Copy(&expanded, io.LimitReader(reader, supportbundle.MaxFileBytes+1)); err != nil {
			t.Fatal(err)
		}
	}
	return expanded.Bytes()
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
