package app

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/TyrantLucifer/awesome-mac-sftp/internal/buildinfo"
	"github.com/TyrantLucifer/awesome-mac-sftp/internal/daemon"
	"github.com/TyrantLucifer/awesome-mac-sftp/internal/platform"
)

type daemonControlClient interface {
	Call(context.Context, string, any, any) error
	Info() daemon.ClientInfo
	Close() error
}

type daemonCommandRuntime struct {
	probe func(context.Context) (daemonControlClient, bool, error)
	start func(context.Context) (daemonControlClient, error)
}

type daemonCommandOptions struct {
	command string
	format  string
}

type daemonCommandOutput struct {
	OutputVersion int               `json:"output_version"`
	Daemon        daemonStateOutput `json:"daemon"`
}

type daemonStateOutput struct {
	Running       bool   `json:"running"`
	State         string `json:"state"`
	DaemonVersion string `json:"daemon_version"`
	Protocol      string `json:"protocol"`
}

func runDaemonCommand(ctx context.Context, args []string, stdout io.Writer) error {
	options, err := parseDaemonCommand(args)
	if err != nil {
		return machineCommandError(args, NewExitError(ExitUsage, err))
	}
	paths, purpose, err := runtimePaths()
	if err != nil {
		return machineCommandError(args, NewExitError(ExitConfig, fmt.Errorf("resolve runtime paths: %w", err)))
	}
	runtime := daemonCommandRuntime{
		probe: func(ctx context.Context) (daemonControlClient, bool, error) {
			return probeDaemon(ctx, paths, purpose)
		},
		start: func(ctx context.Context) (daemonControlClient, error) {
			return connectDaemon(ctx, paths, purpose)
		},
	}
	return executeDaemonCommand(ctx, options, args, stdout, runtime)
}

func runDaemonCommandWithRuntime(ctx context.Context, args []string, stdout io.Writer, runtime daemonCommandRuntime) error {
	options, err := parseDaemonCommand(args)
	if err != nil {
		return machineCommandError(args, NewExitError(ExitUsage, err))
	}
	return executeDaemonCommand(ctx, options, args, stdout, runtime)
}

func executeDaemonCommand(ctx context.Context, options daemonCommandOptions, args []string, stdout io.Writer, runtime daemonCommandRuntime) error {
	if runtime.probe == nil {
		return machineCommandError(args, NewExitError(ExitInternal, errors.New("daemon probe is not configured")))
	}
	client, found, probeErr := runtime.probe(ctx)
	if probeErr != nil {
		if client != nil {
			_ = client.Close()
		}
		return machineCommandError(args, classifyJobConnectionError(fmt.Errorf("probe daemon: %w", probeErr)))
	}
	if found && client == nil {
		return machineCommandError(args, NewExitError(ExitInternal, errors.New("daemon probe reported a daemon without a client")))
	}

	switch options.command {
	case "status":
		if !found {
			return writeDaemonResult(args, stdout, options, daemonStateOutput{State: "stopped"})
		}
		return finishDaemonResult(args, stdout, options, client, daemonState(client.Info(), "running"))
	case "start":
		if found {
			return finishDaemonResult(args, stdout, options, client, daemonState(client.Info(), "already_running"))
		}
		if client != nil {
			_ = client.Close()
		}
		if runtime.start == nil {
			return machineCommandError(args, NewExitError(ExitInternal, errors.New("daemon starter is not configured")))
		}
		started, err := runtime.start(ctx)
		if err != nil {
			return machineCommandError(args, classifyJobConnectionError(fmt.Errorf("start daemon: %w", err)))
		}
		if started == nil {
			return machineCommandError(args, NewExitError(ExitInternal, errors.New("daemon starter returned no client")))
		}
		return finishDaemonResult(args, stdout, options, started, daemonState(started.Info(), "started"))
	case "stop":
		if !found {
			return machineCommandError(args, NewExitError(ExitConflict, errors.New("daemon is not running")))
		}
		var response daemon.ShutdownResponse
		if err := client.Call(ctx, daemon.RequestShutdown, daemon.ShutdownRequest{}, &response); err != nil {
			_ = client.Close()
			return machineCommandError(args, classifyJobCLIError(err))
		}
		if !response.Accepted {
			_ = client.Close()
			return machineCommandError(args, NewExitError(ExitInternal, errors.New("daemon did not accept shutdown")))
		}
		state := daemonState(client.Info(), "stopped")
		state.Running = false
		return finishDaemonResult(args, stdout, options, client, state)
	default:
		return machineCommandError(args, NewExitError(ExitInternal, errors.New("unreachable daemon command")))
	}
}

func parseDaemonCommand(args []string) (daemonCommandOptions, error) {
	options := daemonCommandOptions{format: "human"}
	if len(args) == 0 {
		return options, errors.New("daemon requires one of: start, status, stop")
	}
	options.command = args[0]
	if options.command != "start" && options.command != "status" && options.command != "stop" {
		return options, fmt.Errorf("unknown daemon command %q", options.command)
	}
	seenFormat := false
	seenConfirm := false
	confirmation := ""
	for index := 1; index < len(args); index++ {
		argument := args[index]
		if argument != "--format" && argument != "--confirm" {
			return options, fmt.Errorf("unknown daemon option %q", argument)
		}
		if index+1 >= len(args) {
			return options, fmt.Errorf("daemon option %q requires a value", argument)
		}
		index++
		value := args[index]
		switch argument {
		case "--format":
			if seenFormat {
				return options, errors.New("daemon --format may be provided only once")
			}
			seenFormat = true
			if value != "human" && value != "json" {
				return options, errors.New("daemon --format must be human or json")
			}
			options.format = value
		case "--confirm":
			if seenConfirm {
				return options, errors.New("daemon --confirm may be provided only once")
			}
			seenConfirm = true
			confirmation = value
		}
	}
	if options.command == "stop" {
		if confirmation != "stop" {
			return options, errors.New("daemon stop requires --confirm stop")
		}
	} else if seenConfirm {
		return options, fmt.Errorf("daemon %s does not accept --confirm", options.command)
	}
	return options, nil
}

func probeDaemon(ctx context.Context, paths platform.Paths, purpose platform.ValidationPurpose) (daemonControlClient, bool, error) {
	if _, err := os.Lstat(paths.ControlSocket); errors.Is(err, os.ErrNotExist) {
		return nil, false, nil
	} else if err != nil {
		return nil, false, err
	}
	connection, err := platform.DialControlSocket(ctx, paths.ControlSocket, purpose)
	if err != nil {
		return nil, true, err
	}
	client, err := daemon.NewClient(ctx, connection, buildinfo.Current().String(), fmt.Sprintf("daemon-control-%d", os.Getpid()))
	if err != nil {
		_ = connection.Close()
		return nil, true, err
	}
	return client, true, nil
}

func daemonState(info daemon.ClientInfo, state string) daemonStateOutput {
	return daemonStateOutput{
		Running:       true,
		State:         state,
		DaemonVersion: info.DaemonVersion,
		Protocol:      fmt.Sprintf("%d.%d", info.Protocol.Major, info.Protocol.Minor),
	}
}

func finishDaemonResult(args []string, stdout io.Writer, options daemonCommandOptions, client daemonControlClient, state daemonStateOutput) error {
	if client == nil {
		return machineCommandError(args, NewExitError(ExitInternal, errors.New("daemon runtime returned no client")))
	}
	outputErr := writeDaemonResult(args, stdout, options, state)
	closeErr := client.Close()
	if closeErr != nil {
		closeErr = NewExitError(ExitInternal, fmt.Errorf("close daemon control client: %w", closeErr))
	}
	return machineCommandError(args, errors.Join(outputErr, closeErr))
}

func writeDaemonResult(args []string, stdout io.Writer, options daemonCommandOptions, state daemonStateOutput) error {
	if options.format == "json" {
		encoder := json.NewEncoder(stdout)
		encoder.SetEscapeHTML(false)
		if err := encoder.Encode(daemonCommandOutput{OutputVersion: PublicCLIContractVersion, Daemon: state}); err != nil {
			return machineCommandError(args, NewExitError(ExitInternal, fmt.Errorf("write daemon JSON: %w", err)))
		}
		return nil
	}
	var message string
	switch state.State {
	case "stopped":
		message = "Daemon stopped."
		if state.DaemonVersion == "" && !state.Running {
			message = "Daemon is not running."
		}
	case "running":
		message = fmt.Sprintf("Daemon is running (version %s, protocol %s).", state.DaemonVersion, state.Protocol)
	case "already_running":
		message = fmt.Sprintf("Daemon is already running (version %s, protocol %s).", state.DaemonVersion, state.Protocol)
	case "started":
		message = fmt.Sprintf("Daemon started (version %s, protocol %s).", state.DaemonVersion, state.Protocol)
	default:
		return machineCommandError(args, NewExitError(ExitInternal, fmt.Errorf("unknown daemon output state %q", state.State)))
	}
	if _, err := io.WriteString(stdout, strings.TrimSpace(message)+"\n"); err != nil {
		return machineCommandError(args, NewExitError(ExitInternal, fmt.Errorf("write daemon output: %w", err)))
	}
	return nil
}
