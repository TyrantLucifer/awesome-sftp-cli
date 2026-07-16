package app

import (
	"context"
	"fmt"
	"strings"

	"github.com/TyrantLucifer/awesome-mac-sftp/internal/commandrun"
	"github.com/TyrantLucifer/awesome-mac-sftp/internal/domain"
	"github.com/TyrantLucifer/awesome-mac-sftp/internal/transport/openssh"
	"github.com/TyrantLucifer/awesome-mac-sftp/internal/tui"
)

func runCommandIntent(ctx context.Context, intent tui.Intent, environment []string) tui.CommandCompleted {
	action := tui.CommandCompleted{Pane: intent.Pane, Location: intent.Location, ExitCode: -1}
	if ctx == nil || intent.Kind != tui.IntentRunCommand {
		action.Message = "command failed: invalid command request"
		return action
	}
	switch intent.Endpoint.Kind {
	case domain.EndpointLocal:
		shell, err := commandrun.ResolveLocalShell("", environmentValue(environment, "SHELL"))
		if err != nil {
			action.Message = "command shell failed: " + err.Error()
			return action
		}
		plan, err := commandrun.PlanLocalCommand(shell, string(intent.Location.Path), intent.CommandText)
		if err != nil {
			action.Message = "command plan failed: " + err.Error()
			return action
		}
		result, err := commandrun.RunLocalCommand(ctx, plan, commandrun.DefaultStreamBytes)
		action.ExitCode = result.ExitCode
		action.Stdout = result.Stdout.Data
		action.Stderr = result.Stderr.Data
		action.StdoutDiscarded = result.Stdout.Discarded
		action.StderrDiscarded = result.Stderr.Discarded
		if err != nil {
			action.Message = "command failed: " + err.Error()
		}
		return action
	case domain.EndpointSSH:
		if intent.Endpoint.SSHHostAlias == "" {
			action.Message = "remote command failed: endpoint has no SSH host alias"
			return action
		}
		plan, err := commandrun.PlanRemoteCommand(ctx, commandrun.RemoteConfig{OpenSSH: openssh.Config{
			HostAlias: intent.Endpoint.SSHHostAlias, Environment: append([]string(nil), environment...),
		}}, string(intent.Location.Path), intent.CommandText)
		if err != nil {
			action.Message = "remote command plan failed: " + err.Error()
			return action
		}
		result, err := commandrun.RunRemoteCommand(ctx, plan, commandrun.DefaultStreamBytes)
		action.ExitCode = result.ExitCode
		action.Stdout = result.Stdout.Data
		action.Stderr = result.Stderr.Data
		action.StdoutDiscarded = result.Stdout.Discarded
		action.StderrDiscarded = result.Stderr.Discarded
		action.EffectUnknown = result.Effect == commandrun.EffectUnknown
		if err != nil {
			action.Message = "remote command failed: " + err.Error()
			if result.Diagnostic != "" {
				action.Message += ": " + result.Diagnostic
			}
		}
		return action
	default:
		action.Message = fmt.Sprintf("command failed: unsupported endpoint kind %q", intent.Endpoint.Kind)
		return action
	}
}

func environmentValue(environment []string, key string) string {
	prefix := key + "="
	value := ""
	for _, item := range environment {
		if strings.HasPrefix(item, prefix) {
			value = strings.TrimPrefix(item, prefix)
		}
	}
	return value
}
