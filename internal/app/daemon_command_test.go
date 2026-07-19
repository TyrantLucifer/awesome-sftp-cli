package app

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"strings"
	"testing"

	"github.com/TyrantLucifer/awesome-mac-sftp/internal/daemon"
	"github.com/TyrantLucifer/awesome-mac-sftp/internal/domain"
	"github.com/TyrantLucifer/awesome-mac-sftp/internal/ipc"
)

type fakeDaemonControlClient struct {
	info      daemon.ClientInfo
	calls     int
	callName  string
	callError error
	closed    int
}

func (fake *fakeDaemonControlClient) Call(_ context.Context, name string, _ any, response any) error {
	fake.calls++
	fake.callName = name
	if fake.callError != nil {
		return fake.callError
	}
	if result, ok := response.(*daemon.ShutdownResponse); ok {
		result.Accepted = true
	}
	return nil
}

func (fake *fakeDaemonControlClient) Info() daemon.ClientInfo { return fake.info }
func (fake *fakeDaemonControlClient) Close() error            { fake.closed++; return nil }

func testDaemonInfo() daemon.ClientInfo {
	return daemon.ClientInfo{DaemonVersion: "1.0.0-test", Protocol: ipc.ProtocolVersion{Major: 1, Minor: 0}}
}

func TestDaemonCommandValidatesBeforeRuntimeActivity(t *testing.T) {
	tests := [][]string{
		{},
		{"unknown"},
		{"status", "--format", "yaml"},
		{"stop"},
		{"stop", "--confirm", "yes"},
		{"start", "--confirm", "stop"},
	}
	for _, args := range tests {
		t.Run(strings.Join(args, "_"), func(t *testing.T) {
			probes := 0
			starts := 0
			runtime := daemonCommandRuntime{
				probe: func(context.Context) (daemonControlClient, bool, error) {
					probes++
					return nil, false, nil
				},
				start: func(context.Context) (daemonControlClient, error) {
					starts++
					return nil, errors.New("must not start")
				},
			}
			err := runDaemonCommandWithRuntime(t.Context(), args, &bytes.Buffer{}, runtime)
			if err == nil || exitCode(err) != ExitUsage {
				t.Fatalf("args %q error = %v, exit = %d", args, err, exitCode(err))
			}
			if probes != 0 || starts != 0 {
				t.Fatalf("args %q performed probe=%d start=%d before validation", args, probes, starts)
			}
		})
	}
}

func TestDaemonStatusDoesNotStartAndReportsRunningState(t *testing.T) {
	tests := []struct {
		name    string
		found   bool
		client  *fakeDaemonControlClient
		args    []string
		want    string
		running bool
	}{
		{name: "stopped human", args: []string{"status"}, want: "Daemon is not running."},
		{name: "running json", found: true, client: &fakeDaemonControlClient{info: testDaemonInfo()}, args: []string{"status", "--format", "json"}, running: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			starts := 0
			runtime := daemonCommandRuntime{
				probe: func(context.Context) (daemonControlClient, bool, error) { return tt.client, tt.found, nil },
				start: func(context.Context) (daemonControlClient, error) {
					starts++
					return nil, errors.New("unexpected start")
				},
			}
			var stdout bytes.Buffer
			if err := runDaemonCommandWithRuntime(t.Context(), tt.args, &stdout, runtime); err != nil {
				t.Fatal(err)
			}
			if starts != 0 {
				t.Fatalf("status started daemon %d times", starts)
			}
			if tt.want != "" && !strings.Contains(stdout.String(), tt.want) {
				t.Fatalf("output = %q, want %q", stdout.String(), tt.want)
			}
			if tt.args[len(tt.args)-1] == "json" {
				var output daemonCommandOutput
				if err := json.Unmarshal(stdout.Bytes(), &output); err != nil {
					t.Fatal(err)
				}
				if output.OutputVersion != PublicCLIContractVersion || output.Daemon.Running != tt.running || output.Daemon.DaemonVersion != "1.0.0-test" || output.Daemon.Protocol != "1.0" {
					t.Fatalf("output = %#v", output)
				}
			}
			if tt.client != nil && tt.client.closed != 1 {
				t.Fatalf("client close count = %d, want 1", tt.client.closed)
			}
		})
	}
}

func TestDaemonStartIsIdempotentAndStopUsesAuthenticatedRPC(t *testing.T) {
	t.Run("start existing", func(t *testing.T) {
		client := &fakeDaemonControlClient{info: testDaemonInfo()}
		starts := 0
		runtime := daemonCommandRuntime{
			probe: func(context.Context) (daemonControlClient, bool, error) { return client, true, nil },
			start: func(context.Context) (daemonControlClient, error) {
				starts++
				return nil, errors.New("unexpected start")
			},
		}
		var stdout bytes.Buffer
		if err := runDaemonCommandWithRuntime(t.Context(), []string{"start"}, &stdout, runtime); err != nil {
			t.Fatal(err)
		}
		if starts != 0 || !strings.Contains(stdout.String(), "already running") {
			t.Fatalf("starts = %d, output = %q", starts, stdout.String())
		}
	})

	t.Run("start absent", func(t *testing.T) {
		client := &fakeDaemonControlClient{info: testDaemonInfo()}
		starts := 0
		runtime := daemonCommandRuntime{
			probe: func(context.Context) (daemonControlClient, bool, error) { return nil, false, nil },
			start: func(context.Context) (daemonControlClient, error) { starts++; return client, nil },
		}
		var stdout bytes.Buffer
		if err := runDaemonCommandWithRuntime(t.Context(), []string{"start", "--format", "json"}, &stdout, runtime); err != nil {
			t.Fatal(err)
		}
		if starts != 1 || !strings.Contains(stdout.String(), `"state":"started"`) {
			t.Fatalf("starts = %d, output = %q", starts, stdout.String())
		}
	})

	t.Run("stop confirmed", func(t *testing.T) {
		client := &fakeDaemonControlClient{info: testDaemonInfo()}
		runtime := daemonCommandRuntime{
			probe: func(context.Context) (daemonControlClient, bool, error) { return client, true, nil },
		}
		var stdout bytes.Buffer
		if err := runDaemonCommandWithRuntime(t.Context(), []string{"stop", "--confirm", "stop"}, &stdout, runtime); err != nil {
			t.Fatal(err)
		}
		if client.calls != 1 || client.callName != daemon.RequestShutdown || !strings.Contains(stdout.String(), "stopped") {
			t.Fatalf("client = %#v, output = %q", client, stdout.String())
		}
	})
}

func TestDaemonStartRejectsProbeFailureBeforeStarter(t *testing.T) {
	tests := []struct {
		name       string
		probeErr   error
		found      bool
		withClient bool
		wantCode   domain.Code
		wantRetry  domain.RetryKind
		wantEffect domain.EffectStatus
	}{
		{
			name:       "incompatible daemon",
			found:      true,
			withClient: true,
			probeErr: &daemon.RemoteError{
				RPC: ipc.RPCError{
					Code:    domain.CodeProtocolIncompatible,
					Message: "no shared daemon protocol",
					Retry:   domain.RetryAdvice{Kind: domain.RetryNever},
					Effect:  domain.EffectNone,
				},
			},
			wantCode:   domain.CodeProtocolIncompatible,
			wantRetry:  domain.RetryNever,
			wantEffect: domain.EffectNone,
		},
		{name: "unhealthy socket", found: true, withClient: true, probeErr: errors.New("daemon socket is unhealthy")},
		{name: "socket absence uncertain", probeErr: errors.New("inspect daemon socket: permission denied")},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var client *fakeDaemonControlClient
			if tt.withClient {
				client = &fakeDaemonControlClient{}
			}
			var probeClient daemonControlClient
			if client != nil {
				probeClient = client
			}
			starts := 0
			runtime := daemonCommandRuntime{
				probe: func(context.Context) (daemonControlClient, bool, error) {
					return probeClient, tt.found, tt.probeErr
				},
				start: func(context.Context) (daemonControlClient, error) {
					starts++
					return &fakeDaemonControlClient{info: testDaemonInfo()}, nil
				},
			}
			handler := func(ctx context.Context, args []string, stdout, _ io.Writer) error {
				return runDaemonCommandWithRuntime(ctx, args, stdout, runtime)
			}
			var stdout bytes.Buffer
			var stderr bytes.Buffer
			code := Run(t.Context(), []string{"daemon", "start", "--format", "json"}, &stdout, &stderr, Handlers{Daemon: handler})
			if code != int(ExitNetwork) || stdout.Len() != 0 {
				t.Fatalf("exit = %d stdout = %q stderr = %q", code, stdout.String(), stderr.String())
			}
			if starts != 0 {
				t.Fatalf("starter calls = %d, want 0", starts)
			}
			if client != nil && client.closed != 1 {
				t.Fatalf("probe client close count = %d, want 1", client.closed)
			}
			var envelope cliErrorEnvelope
			if err := json.Unmarshal(stderr.Bytes(), &envelope); err != nil {
				t.Fatalf("stderr is not one JSON object: %q: %v", stderr.String(), err)
			}
			if envelope.OutputVersion != PublicCLIContractVersion || envelope.Error.ExitCode != int(ExitNetwork) || envelope.Error.Class != "network" {
				t.Fatalf("machine error = %#v", envelope)
			}
			if envelope.Error.ErrorCode != tt.wantCode || envelope.Error.Retry != tt.wantRetry || envelope.Error.Effect != tt.wantEffect {
				t.Fatalf("machine diagnostic = %#v, want code=%q retry=%q effect=%q", envelope.Error, tt.wantCode, tt.wantRetry, tt.wantEffect)
			}
		})
	}
}

func TestDaemonJSONFailureUsesStableMachineErrorChannel(t *testing.T) {
	runtime := daemonCommandRuntime{
		probe: func(context.Context) (daemonControlClient, bool, error) {
			return nil, false, errors.New("socket unavailable")
		},
	}
	handler := func(ctx context.Context, args []string, stdout, _ io.Writer) error {
		return runDaemonCommandWithRuntime(ctx, args, stdout, runtime)
	}
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	code := Run(t.Context(), []string{"daemon", "status", "--format", "json"}, &stdout, &stderr, Handlers{Daemon: handler})
	if code != int(ExitNetwork) || stdout.Len() != 0 {
		t.Fatalf("exit = %d stdout = %q stderr = %q", code, stdout.String(), stderr.String())
	}
	var envelope cliErrorEnvelope
	if err := json.Unmarshal(stderr.Bytes(), &envelope); err != nil {
		t.Fatalf("stderr is not one JSON object: %q: %v", stderr.String(), err)
	}
	if envelope.OutputVersion != PublicCLIContractVersion || envelope.Error.ExitCode != int(ExitNetwork) || envelope.Error.Class != "network" || strings.Contains(stderr.String(), "amsftp:") {
		t.Fatalf("machine error = %#v, stderr = %q", envelope, stderr.String())
	}
}

func TestDaemonCommandRejectsRuntimeWithoutClient(t *testing.T) {
	tests := []struct {
		name    string
		args    []string
		runtime daemonCommandRuntime
	}{
		{
			name: "probe reports found without client",
			args: []string{"status"},
			runtime: daemonCommandRuntime{
				probe: func(context.Context) (daemonControlClient, bool, error) { return nil, true, nil },
			},
		},
		{
			name: "starter returns no client",
			args: []string{"start"},
			runtime: daemonCommandRuntime{
				probe: func(context.Context) (daemonControlClient, bool, error) { return nil, false, nil },
				start: func(context.Context) (daemonControlClient, error) { return nil, nil },
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := runDaemonCommandWithRuntime(t.Context(), tt.args, &bytes.Buffer{}, tt.runtime)
			if err == nil || exitCode(err) != ExitInternal {
				t.Fatalf("error = %v, exit = %d", err, exitCode(err))
			}
		})
	}
}
