package app

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"strings"
	"testing"

	"github.com/TyrantLucifer/awesome-sftp-cli/internal/daemon"
	"github.com/TyrantLucifer/awesome-sftp-cli/internal/domain"
	"github.com/TyrantLucifer/awesome-sftp-cli/internal/ipc"
)

const testHelperEndpointID = "ep_aaaaaaaaaaaaaaaaaaaaaaaaaa"

type helperRPCCall struct {
	name    string
	request any
}

type fakeHelperRPC struct {
	t       *testing.T
	calls   []helperRPCCall
	respond func(string, any, any) error
}

func (fake *fakeHelperRPC) Call(_ context.Context, name string, request, response any) error {
	fake.t.Helper()
	fake.calls = append(fake.calls, helperRPCCall{name: name, request: request})
	if fake.respond == nil {
		return nil
	}
	return fake.respond(name, request, response)
}

func TestHelperStatusReportsNegotiatedLevelOneWithVersionedJSONAndReleases(t *testing.T) {
	fake := &fakeHelperRPC{t: t}
	fake.respond = func(name string, request, response any) error {
		switch name {
		case daemon.ProviderConnectSSH:
			if got := request.(ipc.ProviderConnectSSHRequest); got.HostAlias != "work" {
				t.Fatalf("connect request = %#v", got)
			}
			response.(*ipc.ProviderConnectSSHResponse).Endpoint = ipc.WireEndpoint{ID: testHelperEndpointID, Kind: domain.EndpointSSH, SSHHostAlias: "work"}
		case daemon.ProviderSnapshot:
			*response.(*ipc.ProviderSnapshotResponse) = helperStatusSnapshot("1", "1.2.3", "disk_stats,hash", "none")
		case daemon.ProviderRelease:
			if got := request.(ipc.ProviderReleaseRequest); got.EndpointID != testHelperEndpointID {
				t.Fatalf("release request = %#v", got)
			}
		default:
			t.Fatalf("unexpected RPC %q", name)
		}
		return nil
	}

	var stdout bytes.Buffer
	if err := runHelperCommand(t.Context(), []string{"status", "work", "--format", "json"}, &stdout, fake); err != nil {
		t.Fatal(err)
	}
	var output struct {
		OutputVersion int                    `json:"output_version"`
		Command       string                 `json:"command"`
		Host          string                 `json:"host"`
		EndpointID    string                 `json:"endpoint_id"`
		State         domain.ConnectionState `json:"state"`
		Helper        struct {
			Level                      uint16   `json:"level"`
			Version                    string   `json:"version"`
			Capabilities               []string `json:"capabilities"`
			Reason                     string   `json:"reason"`
			Recovery                   string   `json:"recovery"`
			ProductionDistributionOpen bool     `json:"production_distribution_open"`
		} `json:"helper"`
	}
	if err := json.Unmarshal(stdout.Bytes(), &output); err != nil {
		t.Fatalf("decode %q: %v", stdout.String(), err)
	}
	if output.OutputVersion != PublicCLIContractVersion || output.Command != "helper status" || output.Host != "work" || output.EndpointID != testHelperEndpointID || output.State != domain.StateReady {
		t.Fatalf("output = %#v", output)
	}
	if output.Helper.Level != 1 || output.Helper.Version != "1.2.3" || strings.Join(output.Helper.Capabilities, ",") != "disk_stats,hash" || output.Helper.Reason != "none" || output.Helper.Recovery == "" || output.Helper.ProductionDistributionOpen {
		t.Fatalf("helper output = %#v", output.Helper)
	}
	wantCalls := []string{daemon.ProviderConnectSSH, daemon.ProviderSnapshot, daemon.ProviderRelease}
	if len(fake.calls) != len(wantCalls) {
		t.Fatalf("RPC calls = %#v", fake.calls)
	}
	for index, want := range wantCalls {
		if fake.calls[index].name != want {
			t.Fatalf("RPC %d = %q, want %q", index, fake.calls[index].name, want)
		}
	}
}

func TestHelperStatusHumanLevelZeroExplainsSafeFallbackAndClosedDistribution(t *testing.T) {
	fake := &fakeHelperRPC{t: t}
	fake.respond = func(name string, _ any, response any) error {
		switch name {
		case daemon.ProviderConnectSSH:
			response.(*ipc.ProviderConnectSSHResponse).Endpoint = ipc.WireEndpoint{ID: testHelperEndpointID, Kind: domain.EndpointSSH, SSHHostAlias: "legacy"}
		case daemon.ProviderSnapshot:
			*response.(*ipc.ProviderSnapshotResponse) = helperStatusSnapshot("0", "", "", "not_available")
		case daemon.ProviderRelease:
		default:
			t.Fatalf("unexpected RPC %q", name)
		}
		return nil
	}
	var stdout bytes.Buffer
	if err := runHelperCommand(t.Context(), []string{"status", "legacy"}, &stdout, fake); err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"legacy", "L0", "not_available", "Level 0", "production distribution: closed"} {
		if !strings.Contains(stdout.String(), want) {
			t.Fatalf("human output %q does not contain %q", stdout.String(), want)
		}
	}
}

func TestHelperInstallAndUpgradeFailClosedBeforeRPCOrRuntime(t *testing.T) {
	for _, command := range []string{"install", "upgrade"} {
		for _, format := range []string{"human", "json"} {
			t.Run(command+"/"+format, func(t *testing.T) {
				connections := 0
				args := []string{command, "work", "--accept-shared-session-stable-home", "--format", format}
				var stdout bytes.Buffer
				err := runHelperWithConnector(t.Context(), args, &stdout, func(context.Context) (helperRPC, io.Closer, error) {
					connections++
					return &fakeHelperRPC{t: t}, nopCloser{}, nil
				})
				if err == nil || exitCode(err) != ExitConfig || !strings.Contains(err.Error(), "production Helper distribution is closed") {
					t.Fatalf("error = %v, exit = %d", err, exitCode(err))
				}
				if connections != 0 || stdout.Len() != 0 {
					t.Fatalf("connections = %d, stdout = %q", connections, stdout.String())
				}
				if format == "json" {
					var rendered bytes.Buffer
					renderer, ok := err.(interface{ RenderCLIError(io.Writer) error })
					if !ok {
						t.Fatalf("error %T has no machine renderer", err)
					}
					if renderErr := renderer.RenderCLIError(&rendered); renderErr != nil {
						t.Fatal(renderErr)
					}
					if !strings.Contains(rendered.String(), `"output_version":1`) || !strings.Contains(rendered.String(), `"class":"configuration"`) || !strings.Contains(rendered.String(), "production Helper distribution is closed") {
						t.Fatalf("machine error = %q", rendered.String())
					}
				}
			})
		}
	}
}

func TestHelperInstallAndUpgradeRejectUntrustedInputsOrMissingConsentBeforeRuntime(t *testing.T) {
	for _, args := range [][]string{
		{"install", "work"},
		{"upgrade", "work"},
		{"install", "-oProxyCommand=bad", "--accept-shared-session-stable-home"},
		{"upgrade", "work", "--accept-shared-session-stable-home", "--accept-shared-session-stable-home"},
		{"install", "work", "--accept-shared-session-stable-home", "--manifest", "/tmp/manifest"},
		{"upgrade", "work", "--accept-shared-session-stable-home", "--artifact", "/tmp/amsftp"},
		{"serve"},
	} {
		connections := 0
		err := runHelperWithConnector(t.Context(), args, &bytes.Buffer{}, func(context.Context) (helperRPC, io.Closer, error) {
			connections++
			return &fakeHelperRPC{t: t}, nopCloser{}, nil
		})
		if err == nil || exitCode(err) != ExitUsage {
			t.Fatalf("args %q error = %v, exit = %d", args, err, exitCode(err))
		}
		if connections != 0 {
			t.Fatalf("args %q made %d connections", args, connections)
		}
	}
}

func TestHelperDisableAndRemoveFailClosedBeforeRPCOrRuntime(t *testing.T) {
	commands := map[string][]string{
		"disable": {"disable", "work"},
		"remove":  {"remove", "work", "--accept-shared-session-stable-home"},
	}
	for command, baseArgs := range commands {
		for _, format := range []string{"human", "json"} {
			t.Run(command+"/"+format, func(t *testing.T) {
				connections := 0
				args := append(append([]string(nil), baseArgs...), "--format", format)
				var stdout bytes.Buffer
				err := runHelperWithConnector(t.Context(), args, &stdout, func(context.Context) (helperRPC, io.Closer, error) {
					connections++
					return &fakeHelperRPC{t: t}, nopCloser{}, nil
				})
				if err == nil || exitCode(err) != ExitConfig || !strings.Contains(err.Error(), "production Helper lifecycle is closed") {
					t.Fatalf("error = %v, exit = %d", err, exitCode(err))
				}
				if connections != 0 || stdout.Len() != 0 {
					t.Fatalf("connections = %d, stdout = %q", connections, stdout.String())
				}
				if format == "json" {
					var rendered bytes.Buffer
					renderer, ok := err.(interface{ RenderCLIError(io.Writer) error })
					if !ok {
						t.Fatalf("error %T has no machine renderer", err)
					}
					if renderErr := renderer.RenderCLIError(&rendered); renderErr != nil {
						t.Fatal(renderErr)
					}
					if !strings.Contains(rendered.String(), `"output_version":1`) || !strings.Contains(rendered.String(), `"class":"configuration"`) || !strings.Contains(rendered.String(), "production Helper lifecycle is closed") {
						t.Fatalf("machine error = %q", rendered.String())
					}
				}
			})
		}
	}
}

func TestHelperDisableAndRemoveRejectUntrustedInputsOrWrongConsentBeforeRuntime(t *testing.T) {
	for _, args := range [][]string{
		{"disable", "work", "--accept-shared-session-stable-home"},
		{"disable", "work", "--artifact", "sha256:bad"},
		{"remove", "work"},
		{"remove", "work", "--accept-shared-session-stable-home", "--accept-shared-session-stable-home"},
		{"remove", "work", "--accept-shared-session-stable-home", "--path", "/tmp/helper"},
	} {
		connections := 0
		err := runHelperWithConnector(t.Context(), args, &bytes.Buffer{}, func(context.Context) (helperRPC, io.Closer, error) {
			connections++
			return &fakeHelperRPC{t: t}, nopCloser{}, nil
		})
		if err == nil || exitCode(err) != ExitUsage {
			t.Fatalf("args %q error = %v, exit = %d", args, err, exitCode(err))
		}
		if connections != 0 {
			t.Fatalf("args %q made %d connections", args, connections)
		}
	}
}

func TestHelperStatusValidatesBeforeConnecting(t *testing.T) {
	for _, args := range [][]string{
		nil,
		{"status", "-oProxyCommand=bad"},
		{"status", "work", "--format", "yaml"},
		{"status", "work", "extra"},
	} {
		connections := 0
		err := runHelperWithConnector(t.Context(), args, &bytes.Buffer{}, func(context.Context) (helperRPC, io.Closer, error) {
			connections++
			return &fakeHelperRPC{t: t}, nopCloser{}, nil
		})
		if err == nil || exitCode(err) != ExitUsage {
			t.Fatalf("args %q error = %v, exit = %d", args, err, exitCode(err))
		}
		if connections != 0 {
			t.Fatalf("args %q made %d connections", args, connections)
		}
	}
}

func TestHelperStatusRejectsMalformedCapabilityAndStillReleases(t *testing.T) {
	fake := &fakeHelperRPC{t: t}
	fake.respond = func(name string, _ any, response any) error {
		switch name {
		case daemon.ProviderConnectSSH:
			response.(*ipc.ProviderConnectSSHResponse).Endpoint = ipc.WireEndpoint{ID: testHelperEndpointID, Kind: domain.EndpointSSH}
		case daemon.ProviderSnapshot:
			snapshot := helperStatusSnapshot("1", "1.2.3", "hash", "none")
			snapshot.Items[0].Constraints = append(snapshot.Items[0].Constraints, domain.CapabilityConstraint{Name: "level", Value: "0"})
			*response.(*ipc.ProviderSnapshotResponse) = snapshot
		case daemon.ProviderRelease:
		default:
			t.Fatalf("unexpected RPC %q", name)
		}
		return nil
	}
	err := runHelperCommand(t.Context(), []string{"status", "work"}, &bytes.Buffer{}, fake)
	if err == nil || exitCode(err) != ExitInternal || !strings.Contains(err.Error(), "duplicate helper status constraint") {
		t.Fatalf("error = %v, exit = %d", err, exitCode(err))
	}
	if len(fake.calls) != 3 || fake.calls[2].name != daemon.ProviderRelease {
		t.Fatalf("RPC calls = %#v", fake.calls)
	}
}

func TestHelperStatusSnapshotFailureStillReleasesAndJSONErrorIsVersioned(t *testing.T) {
	fake := &fakeHelperRPC{t: t}
	fake.respond = func(name string, _ any, response any) error {
		switch name {
		case daemon.ProviderConnectSSH:
			response.(*ipc.ProviderConnectSSHResponse).Endpoint = ipc.WireEndpoint{ID: testHelperEndpointID, Kind: domain.EndpointSSH}
		case daemon.ProviderSnapshot:
			return errors.New("snapshot failed")
		case daemon.ProviderRelease:
		default:
			t.Fatalf("unexpected RPC %q", name)
		}
		return nil
	}
	var stderr bytes.Buffer
	err := runHelperWithConnector(t.Context(), []string{"status", "work", "--format", "json"}, &bytes.Buffer{}, func(context.Context) (helperRPC, io.Closer, error) {
		return fake, nopCloser{}, nil
	})
	if err == nil || exitCode(err) != ExitInternal {
		t.Fatalf("error = %v, exit = %d", err, exitCode(err))
	}
	renderer, ok := err.(interface{ RenderCLIError(io.Writer) error })
	if !ok {
		t.Fatalf("error %T has no machine renderer", err)
	}
	if renderErr := renderer.RenderCLIError(&stderr); renderErr != nil {
		t.Fatal(renderErr)
	}
	if !strings.Contains(stderr.String(), `"output_version":1`) || !strings.Contains(stderr.String(), `"class":"internal"`) {
		t.Fatalf("machine error = %q", stderr.String())
	}
	if len(fake.calls) != 3 || fake.calls[2].name != daemon.ProviderRelease {
		t.Fatalf("RPC calls = %#v", fake.calls)
	}
}

func TestHelperStatusDoesNotPublishSuccessBeforeReleaseAndCloseSucceed(t *testing.T) {
	tests := []struct {
		name       string
		releaseErr error
		closer     io.Closer
	}{
		{name: "release", releaseErr: errors.New("release failed"), closer: nopCloser{}},
		{name: "close", closer: errorCloser{err: errors.New("close failed")}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			fake := &fakeHelperRPC{t: t}
			fake.respond = func(name string, _ any, response any) error {
				switch name {
				case daemon.ProviderConnectSSH:
					response.(*ipc.ProviderConnectSSHResponse).Endpoint = ipc.WireEndpoint{ID: testHelperEndpointID, Kind: domain.EndpointSSH}
				case daemon.ProviderSnapshot:
					*response.(*ipc.ProviderSnapshotResponse) = helperStatusSnapshot("0", "", "", "not_available")
				case daemon.ProviderRelease:
					return tt.releaseErr
				default:
					t.Fatalf("unexpected RPC %q", name)
				}
				return nil
			}
			var stdout bytes.Buffer
			err := runHelperWithConnector(t.Context(), []string{"status", "work", "--format", "json"}, &stdout, func(context.Context) (helperRPC, io.Closer, error) {
				return fake, tt.closer, nil
			})
			if err == nil || exitCode(err) != ExitInternal {
				t.Fatalf("error = %v, exit = %d", err, exitCode(err))
			}
			if stdout.Len() != 0 {
				t.Fatalf("published success before cleanup completed: %q", stdout.String())
			}
		})
	}
}

type errorCloser struct{ err error }

func (closer errorCloser) Close() error { return closer.err }

func helperStatusSnapshot(level, version, capabilities, reason string) ipc.ProviderSnapshotResponse {
	constraints := []domain.CapabilityConstraint{
		{Name: "level", Value: level},
		{Name: "reason", Value: reason},
		{Name: "recovery", Value: "continue with Level 0"},
	}
	if version != "" {
		constraints = append(constraints, domain.CapabilityConstraint{Name: "version", Value: version})
	}
	if capabilities != "" {
		constraints = append(constraints, domain.CapabilityConstraint{Name: "capabilities", Value: capabilities})
	}
	return ipc.ProviderSnapshotResponse{
		EndpointID: testHelperEndpointID,
		SessionID:  "sess_aaaaaaaaaaaaaaaaaaaaaaaaaa",
		State:      domain.StateReady,
		Generation: 1,
		Complete:   true,
		Items:      []ipc.WireCapability{{Name: "helper_status", Version: 1, Constraints: constraints}},
	}
}
