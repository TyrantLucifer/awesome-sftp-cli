package app

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"sort"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/TyrantLucifer/awesome-mac-sftp/internal/daemon"
	"github.com/TyrantLucifer/awesome-mac-sftp/internal/domain"
	helperruntime "github.com/TyrantLucifer/awesome-mac-sftp/internal/helper"
	"github.com/TyrantLucifer/awesome-mac-sftp/internal/ipc"
	"github.com/TyrantLucifer/awesome-mac-sftp/internal/transport/openssh"
	"github.com/TyrantLucifer/awesome-mac-sftp/internal/tui"
)

const (
	helperCLIOutputVersion      = PublicCLIContractVersion
	helperStatusCapabilityName  = domain.CapabilityName("helper_status")
	helperStatusCapabilityLimit = 64
	helperStatusTextLimit       = 1024
)

type helperRPC interface {
	Call(context.Context, string, any, any) error
}

type helperRPCConnector func(context.Context) (helperRPC, io.Closer, error)

type helperCommandOptions struct {
	command string
	host    string
	format  string
}

type helperStatusOutput struct {
	Level                      uint16   `json:"level"`
	Version                    string   `json:"version"`
	Capabilities               []string `json:"capabilities"`
	Reason                     string   `json:"reason"`
	Recovery                   string   `json:"recovery"`
	ProductionDistributionOpen bool     `json:"production_distribution_open"`
}

type helperStatusEnvelope struct {
	OutputVersion int                    `json:"output_version"`
	Command       string                 `json:"command"`
	Host          string                 `json:"host"`
	EndpointID    string                 `json:"endpoint_id"`
	State         domain.ConnectionState `json:"state"`
	Helper        helperStatusOutput     `json:"helper"`
}

func runHelperManagement(ctx context.Context, args []string, stdout io.Writer) error {
	return runHelperWithConnector(ctx, args, stdout, func(ctx context.Context) (helperRPC, io.Closer, error) {
		paths, purpose, err := runtimePaths()
		if err != nil {
			return nil, nil, NewExitError(ExitConfig, fmt.Errorf("resolve runtime paths: %w", err))
		}
		client, err := connectDaemon(ctx, paths, purpose)
		if err != nil {
			return nil, nil, classifyJobConnectionError(fmt.Errorf("connect to daemon: %w", err))
		}
		return client, client, nil
	})
}

func runHelperWithConnector(ctx context.Context, args []string, stdout io.Writer, connector helperRPCConnector) error {
	options, err := parseHelperCommand(args)
	if err != nil {
		return machineCommandError(args, NewExitError(ExitUsage, err))
	}
	if connector == nil {
		return machineCommandError(args, NewExitError(ExitInternal, errors.New("helper RPC connector is not configured")))
	}
	rpc, closer, err := connector(ctx)
	if err != nil {
		return machineCommandError(args, err)
	}
	if closer == nil {
		return machineCommandError(args, NewExitError(ExitInternal, errors.New("helper RPC connector returned no closer")))
	}
	output, commandErr := executeHelperCommand(ctx, options, rpc)
	closeErr := closer.Close()
	if closeErr != nil {
		closeErr = NewExitError(ExitInternal, fmt.Errorf("close Helper RPC client: %w", closeErr))
	}
	if err := errors.Join(commandErr, closeErr); err != nil {
		return machineCommandError(args, err)
	}
	return machineCommandError(args, writeHelperStatus(options, output, stdout))
}

func runHelperCommand(ctx context.Context, args []string, stdout io.Writer, rpc helperRPC) error {
	options, err := parseHelperCommand(args)
	if err != nil {
		return NewExitError(ExitUsage, err)
	}
	output, err := executeHelperCommand(ctx, options, rpc)
	if err != nil {
		return err
	}
	return writeHelperStatus(options, output, stdout)
}

func executeHelperCommand(ctx context.Context, options helperCommandOptions, rpc helperRPC) (helperStatusEnvelope, error) {
	if rpc == nil {
		return helperStatusEnvelope{}, NewExitError(ExitInternal, errors.New("helper RPC client is not configured"))
	}
	var connected ipc.ProviderConnectSSHResponse
	if err := rpc.Call(ctx, daemon.ProviderConnectSSH, ipc.ProviderConnectSSHRequest{HostAlias: options.host}, &connected); err != nil {
		return helperStatusEnvelope{}, classifyJobCLIError(err)
	}

	var snapshot ipc.ProviderSnapshotResponse
	snapshotErr := rpc.Call(ctx, daemon.ProviderSnapshot, ipc.ProviderSnapshotRequest{EndpointID: connected.Endpoint.ID}, &snapshot)
	var output helperStatusEnvelope
	var commandErr error
	if snapshotErr != nil {
		commandErr = classifyJobCLIError(snapshotErr)
	} else {
		output, commandErr = decodeHelperStatusEnvelope(options, connected.Endpoint, snapshot)
	}
	var released ipc.ProviderReleaseResponse
	releaseErr := rpc.Call(ctx, daemon.ProviderRelease, ipc.ProviderReleaseRequest{EndpointID: connected.Endpoint.ID}, &released)
	if releaseErr != nil {
		releaseErr = classifyJobCLIError(releaseErr)
	}
	if err := errors.Join(commandErr, releaseErr); err != nil {
		return helperStatusEnvelope{}, err
	}
	return output, nil
}

func decodeHelperStatusEnvelope(options helperCommandOptions, endpoint ipc.WireEndpoint, response ipc.ProviderSnapshotResponse) (helperStatusEnvelope, error) {
	endpointID, err := domain.ParseEndpointID(endpoint.ID)
	if err != nil {
		return helperStatusEnvelope{}, NewExitError(ExitInternal, fmt.Errorf("decode Helper status endpoint: %w", err))
	}
	if endpoint.Kind != domain.EndpointSSH || response.EndpointID != endpoint.ID {
		return helperStatusEnvelope{}, NewExitError(ExitInternal, errors.New("decode Helper status: daemon returned a mismatched SSH endpoint"))
	}
	if !validHelperConnectionState(response.State) {
		return helperStatusEnvelope{}, NewExitError(ExitInternal, fmt.Errorf("decode Helper status: unknown connection state %q", response.State))
	}
	snapshot, err := capabilitySnapshotFromWire(response)
	if err != nil {
		return helperStatusEnvelope{}, NewExitError(ExitInternal, err)
	}
	status, err := decodeHelperStatus(snapshot)
	if err != nil {
		return helperStatusEnvelope{}, NewExitError(ExitInternal, err)
	}
	return helperStatusEnvelope{
		OutputVersion: helperCLIOutputVersion,
		Command:       "helper status",
		Host:          options.host,
		EndpointID:    string(endpointID),
		State:         response.State,
		Helper:        status,
	}, nil
}

func writeHelperStatus(options helperCommandOptions, envelope helperStatusEnvelope, stdout io.Writer) error {
	if options.format == "json" {
		encoder := json.NewEncoder(stdout)
		encoder.SetEscapeHTML(false)
		if err := encoder.Encode(envelope); err != nil {
			return NewExitError(ExitInternal, fmt.Errorf("write Helper JSON: %w", err))
		}
		return nil
	}
	status := envelope.Helper
	version := status.Version
	if version == "" {
		version = "-"
	}
	capabilities := strings.Join(status.Capabilities, ",")
	if capabilities == "" {
		capabilities = "-"
	}
	distribution := "closed"
	if status.ProductionDistributionOpen {
		distribution = "open"
	}
	_, err := fmt.Fprintf(stdout, "host: %s\nendpoint: %s\nstate: %s\nhelper: L%d\nversion: %s\ncapabilities: %s\nreason: %s\nrecovery: %s\nproduction distribution: %s\n",
		tui.SanitizeTerminalText(envelope.Host), envelope.EndpointID, envelope.State, status.Level,
		tui.SanitizeTerminalText(version), tui.SanitizeTerminalText(capabilities),
		tui.SanitizeTerminalText(status.Reason), tui.SanitizeTerminalText(status.Recovery), distribution)
	if err != nil {
		return NewExitError(ExitInternal, fmt.Errorf("write Helper status: %w", err))
	}
	return nil
}

func decodeHelperStatus(snapshot domain.CapabilitySnapshot) (helperStatusOutput, error) {
	capability, ok := snapshot.Lookup(helperStatusCapabilityName)
	if !ok || capability.Version != 1 {
		return helperStatusOutput{}, errors.New("decode Helper status: required helper_status v1 capability is missing")
	}
	values := make(map[string]string, len(capability.Constraints))
	for _, constraint := range capability.Constraints {
		if _, exists := values[constraint.Name]; exists {
			return helperStatusOutput{}, fmt.Errorf("decode Helper status: duplicate helper status constraint %q", constraint.Name)
		}
		switch constraint.Name {
		case "level", "version", "capabilities", "reason", "recovery":
		default:
			return helperStatusOutput{}, fmt.Errorf("decode Helper status: unknown helper status constraint %q", constraint.Name)
		}
		if err := validateHelperStatusText(constraint.Name, constraint.Value); err != nil {
			return helperStatusOutput{}, err
		}
		values[constraint.Name] = constraint.Value
	}
	level, err := strconv.ParseUint(values["level"], 10, 16)
	if err != nil || (level != 0 && level != 1) {
		return helperStatusOutput{}, fmt.Errorf("decode Helper status: invalid level %q", values["level"])
	}
	if values["reason"] == "" || values["recovery"] == "" {
		return helperStatusOutput{}, errors.New("decode Helper status: reason and recovery are required")
	}
	capabilities, err := decodeHelperCapabilityNames(values["capabilities"])
	if err != nil {
		return helperStatusOutput{}, err
	}
	if level == 0 && (values["version"] != "" || len(capabilities) != 0) {
		return helperStatusOutput{}, errors.New("decode Helper status: Level 0 cannot report a version or capabilities")
	}
	if level == 1 && values["version"] == "" {
		return helperStatusOutput{}, errors.New("decode Helper status: Level 1 requires a version")
	}
	return helperStatusOutput{
		Level:                      uint16(level),
		Version:                    values["version"],
		Capabilities:               capabilities,
		Reason:                     values["reason"],
		Recovery:                   values["recovery"],
		ProductionDistributionOpen: helperruntime.ProductionDistributionOpen,
	}, nil
}

func decodeHelperCapabilityNames(value string) ([]string, error) {
	if value == "" {
		return []string{}, nil
	}
	items := strings.Split(value, ",")
	if len(items) > helperStatusCapabilityLimit {
		return nil, fmt.Errorf("decode Helper status: more than %d capabilities", helperStatusCapabilityLimit)
	}
	seen := make(map[string]bool, len(items))
	for _, item := range items {
		if item == "" || seen[item] {
			return nil, fmt.Errorf("decode Helper status: invalid or duplicate capability %q", item)
		}
		if err := validateHelperStatusText("capability", item); err != nil {
			return nil, err
		}
		seen[item] = true
	}
	sort.Strings(items)
	return items, nil
}

func validateHelperStatusText(name, value string) error {
	if len(value) > helperStatusTextLimit {
		return fmt.Errorf("decode Helper status: %s exceeds %d bytes", name, helperStatusTextLimit)
	}
	if !utf8.ValidString(value) {
		return fmt.Errorf("decode Helper status: %s is not valid UTF-8", name)
	}
	for _, character := range value {
		if character < 0x20 || character == 0x7f {
			return fmt.Errorf("decode Helper status: %s contains a control character", name)
		}
	}
	return nil
}

func validHelperConnectionState(state domain.ConnectionState) bool {
	switch state {
	case domain.StateDisconnected, domain.StateConnecting, domain.StateReady, domain.StateDegraded, domain.StateAuthRequired, domain.StateFailed:
		return true
	default:
		return false
	}
}

func parseHelperCommand(args []string) (helperCommandOptions, error) {
	options := helperCommandOptions{format: "human"}
	if len(args) == 0 {
		return options, errors.New("helper requires: status <SSH-host> [--format human|json]")
	}
	if args[0] != "status" {
		return options, fmt.Errorf("unknown public helper command %q", args[0])
	}
	options.command = args[0]
	if len(args) < 2 {
		return options, errors.New("helper status requires exactly one SSH host alias")
	}
	options.host = args[1]
	if err := openssh.ValidateHostAlias(options.host); err != nil {
		return options, fmt.Errorf("helper status SSH host: %w", err)
	}
	seenFormat := false
	for index := 2; index < len(args); index++ {
		if args[index] != "--format" {
			return options, fmt.Errorf("unknown helper status option %q", args[index])
		}
		if seenFormat {
			return options, errors.New("helper status --format may be provided only once")
		}
		seenFormat = true
		if index+1 >= len(args) {
			return options, errors.New("helper status --format requires a value")
		}
		index++
		if args[index] != "human" && args[index] != "json" {
			return options, errors.New("helper status --format must be human or json")
		}
		options.format = args[index]
	}
	return options, nil
}
