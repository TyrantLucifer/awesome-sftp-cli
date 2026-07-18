package app

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"strings"
	"testing"

	"github.com/TyrantLucifer/awesome-mac-sftp/internal/config"
	"github.com/TyrantLucifer/awesome-mac-sftp/internal/doctor"
	"github.com/TyrantLucifer/awesome-mac-sftp/internal/platform"
	"github.com/TyrantLucifer/awesome-mac-sftp/internal/statefs"
	"github.com/TyrantLucifer/awesome-mac-sftp/internal/transport/openssh"
)

func TestDoctorCommandRunsFixedReadOnlyChecksAndRendersJSON(t *testing.T) {
	t.Parallel()

	runtime := passingDoctorRuntime()
	var stdout bytes.Buffer
	if err := runDoctorWithRuntime(t.Context(), []string{"--format", "json", "--endpoint", "work-alias"}, &stdout, runtime); err != nil {
		t.Fatalf("runDoctorWithRuntime(): %v", err)
	}
	var report doctor.Report
	if err := json.Unmarshal(stdout.Bytes(), &report); err != nil {
		t.Fatalf("decode doctor JSON: %v\n%s", err, stdout.String())
	}
	wantCodes := append(doctor.RequiredCodes(), doctor.CheckEndpoint)
	if len(report.Results) != len(wantCodes) {
		t.Fatalf("result count = %d, want %d", len(report.Results), len(wantCodes))
	}
	wantDetails := []string{
		"config_valid", "runtime_private", "socket_private", "daemon_reachable", "openssh_validated",
		"known_hosts_policy_valid", "database_healthy", "cache_private", "helper_disabled", "disk_space_available", "endpoint_reachable",
	}
	for index, result := range report.Results {
		if result.Code != wantCodes[index] || result.Status != doctor.Pass || result.DetailCode != wantDetails[index] {
			t.Fatalf("result[%d] = %#v", index, result)
		}
	}
}

func TestDoctorCommandReportsAbsenceAndLiveDatabaseWithoutRepair(t *testing.T) {
	t.Parallel()

	runtime := passingDoctorRuntime()
	runtime.pathExists = func(path string) (bool, error) {
		return path == runtime.paths.ConfigFile || path == runtime.paths.DatabaseFile, nil
	}
	runtime.inspectDatabase = func(context.Context, string) (uint64, error) {
		return 0, statefs.ErrDatabaseActive
	}
	var stdout bytes.Buffer
	if err := runDoctorWithRuntime(t.Context(), []string{"--format", "json"}, &stdout, runtime); err != nil {
		t.Fatalf("runDoctorWithRuntime(): %v", err)
	}
	var report doctor.Report
	if err := json.Unmarshal(stdout.Bytes(), &report); err != nil {
		t.Fatalf("decode doctor JSON: %v", err)
	}
	assertDoctorResult(t, report, doctor.CheckRuntimeDirectory, doctor.Warn, "runtime_not_created")
	assertDoctorResult(t, report, doctor.CheckSocket, doctor.Warn, "socket_not_created")
	assertDoctorResult(t, report, doctor.CheckDaemon, doctor.Warn, "daemon_not_running")
	assertDoctorResult(t, report, doctor.CheckDatabase, doctor.Warn, "database_active")
	assertDoctorResult(t, report, doctor.CheckCache, doctor.Warn, "cache_not_created")
	assertDoctorResult(t, report, doctor.CheckKnownHosts, doctor.Skipped, "endpoint_not_requested")
	if len(report.Results) != len(doctor.RequiredCodes()) {
		t.Fatalf("unexpected endpoint result: %#v", report.Results)
	}
}

func TestDoctorCommandNeverExportsRawProbeCausesOrEndpointMetadata(t *testing.T) {
	t.Parallel()

	const secret = "stage6-doctor-raw-secret"
	runtime := passingDoctorRuntime()
	runtime.validateExecutable = func(string) error { return errors.New(secret) }
	runtime.inspectOpenSSH = func(context.Context, string) (openssh.ConfigInspection, error) {
		return openssh.ConfigInspection{}, errors.New(secret)
	}
	var stdout bytes.Buffer
	if err := runDoctorWithRuntime(t.Context(), []string{"--format", "human", "--endpoint", "secret-host.example"}, &stdout, runtime); err != nil {
		t.Fatalf("runDoctorWithRuntime(): %v", err)
	}
	for _, forbidden := range []string{secret, "secret-host.example", runtime.paths.ConfigFile, "127.0.0.1"} {
		if strings.Contains(stdout.String(), forbidden) {
			t.Fatalf("doctor output exposed %q: %s", forbidden, stdout.String())
		}
	}
	if !strings.Contains(stdout.String(), "openssh") || !strings.Contains(stdout.String(), "probe_failed") {
		t.Fatalf("human output lacks stable failure: %s", stdout.String())
	}
}

func TestDoctorCommandRejectsUnsafeArgumentsBeforeProbing(t *testing.T) {
	t.Parallel()

	for _, args := range [][]string{
		{"--format", "yaml"},
		{"--endpoint", "-oProxyCommand=bad"},
		{"unexpected"},
	} {
		called := false
		runtime := passingDoctorRuntime()
		runtime.pathExists = func(string) (bool, error) { called = true; return true, nil }
		err := runDoctorWithRuntime(t.Context(), args, &bytes.Buffer{}, runtime)
		if err == nil || exitCode(err) != ExitUsage {
			t.Fatalf("args %#v error = %v, exit = %d", args, err, exitCode(err))
		}
		if called {
			t.Fatalf("args %#v reached a probe", args)
		}
	}
}

func TestDoctorCommandRendersMachineErrorForJSONArgumentFailure(t *testing.T) {
	t.Parallel()

	err := runDoctorWithRuntime(t.Context(), []string{"--format", "json", "unexpected"}, &bytes.Buffer{}, passingDoctorRuntime())
	if err == nil || exitCode(err) != ExitUsage {
		t.Fatalf("runDoctorWithRuntime() error = %v, exit = %d", err, exitCode(err))
	}
	renderer, ok := err.(interface{ RenderCLIError(io.Writer) error })
	if !ok {
		t.Fatalf("JSON argument error lacks machine renderer: %T", err)
	}
	var rendered bytes.Buffer
	if err := renderer.RenderCLIError(&rendered); err != nil {
		t.Fatalf("RenderCLIError(): %v", err)
	}
	var envelope cliErrorEnvelope
	if err := json.Unmarshal(rendered.Bytes(), &envelope); err != nil {
		t.Fatalf("decode machine error: %v", err)
	}
	if envelope.OutputVersion != PublicCLIContractVersion || envelope.Error.ExitCode != int(ExitUsage) || envelope.Error.Class != "usage" {
		t.Fatalf("machine error = %#v", envelope)
	}
}

func passingDoctorRuntime() doctorRuntime {
	paths := platform.Paths{
		ConfigFile: "/safe/config.json", StateDir: "/safe/state", DatabaseFile: "/safe/state/amsftp.db",
		CacheDir: "/safe/cache", RuntimeDir: "/safe/runtime", ControlSocket: "/safe/runtime/control-v1.sock",
	}
	return doctorRuntime{
		paths: paths, purpose: platform.ValidateRuntime,
		pathExists:         func(string) (bool, error) { return true, nil },
		loadConfig:         func(string) (config.Config, error) { return config.Default(), nil },
		validateDirectory:  func(string, platform.ValidationPurpose) error { return nil },
		validateSocket:     func(string, platform.ValidationPurpose) error { return nil },
		dialDaemon:         func(context.Context, string, platform.ValidationPurpose) error { return nil },
		validateExecutable: func(string) error { return nil },
		inspectDatabase:    func(context.Context, string) (uint64, error) { return 4, nil },
		availableBytes:     func(string) (uint64, error) { return 1024 * 1024 * 1024, nil },
		inspectOpenSSH: func(context.Context, string) (openssh.ConfigInspection, error) {
			return openssh.ConfigInspection{Hostname: "127.0.0.1", Port: "22", StrictHostKeyChecking: "ask", KnownHostsConfigured: true}, nil
		},
		dialEndpoint: func(context.Context, string, string) error { return nil },
	}
}

func assertDoctorResult(t *testing.T, report doctor.Report, code doctor.Code, status doctor.Status, detail string) {
	t.Helper()
	for _, result := range report.Results {
		if result.Code == code {
			if result.Status != status || result.DetailCode != detail {
				t.Fatalf("%s result = %#v", code, result)
			}
			return
		}
	}
	t.Fatalf("missing doctor result %s", code)
}
