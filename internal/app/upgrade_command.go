package app

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"runtime"
	"strings"

	"github.com/TyrantLucifer/awesome-sftp-cli/internal/buildinfo"
	"github.com/TyrantLucifer/awesome-sftp-cli/internal/daemon"
	"github.com/TyrantLucifer/awesome-sftp-cli/internal/platform"
	"github.com/TyrantLucifer/awesome-sftp-cli/internal/selfupdate"
)

type upgradeChannel = selfupdate.Channel

const (
	upgradeChannelHomebrew   = selfupdate.ChannelHomebrew
	upgradeChannelStandalone = selfupdate.ChannelStandalone
)

type upgradePlan struct {
	Channel        upgradeChannel
	CurrentVersion string
	TargetVersion  string
	Available      bool
	raw            selfupdate.Plan
}

type upgradeCommandRuntime struct {
	plan        func(context.Context) (upgradePlan, error)
	probe       func(context.Context) (daemonControlClient, bool, error)
	waitStopped func(context.Context) error
	apply       func(context.Context, upgradePlan) error
	start       func(context.Context) (daemonControlClient, error)
	verify      func(context.Context, upgradePlan, daemonControlClient) error
}

type upgradeCommandOptions struct {
	format string
}

type upgradeCommandOutput struct {
	OutputVersion   int            `json:"output_version"`
	Command         string         `json:"command"`
	Status          string         `json:"status"`
	Channel         upgradeChannel `json:"channel"`
	PreviousVersion string         `json:"previous_version"`
	Version         string         `json:"version"`
	DaemonRestarted bool           `json:"daemon_restarted"`
}

func runUpgrade(ctx context.Context, args []string, stdout io.Writer, _ io.Writer) error {
	options, err := parseUpgradeCommand(args)
	if err != nil {
		return machineCommandError(args, NewExitError(ExitUsage, err))
	}
	executable, err := os.Executable()
	if err != nil {
		return machineCommandError(args, NewExitError(ExitInternal, errors.New("cannot identify the installed AMSFTP executable")))
	}
	executable, err = platform.ResolveTrustedExecutable(executable)
	if err != nil {
		return machineCommandError(args, NewExitError(ExitConfig, errors.New("the installed AMSFTP executable did not pass integrity checks")))
	}
	updater, err := selfupdate.New(selfupdate.Config{
		GOOS: runtime.GOOS, GOARCH: runtime.GOARCH, Executable: executable, CurrentVersion: buildinfo.Current().Version,
	})
	if err != nil {
		return machineCommandError(args, NewExitError(ExitConfig, errors.New("this AMSFTP installation cannot be upgraded automatically")))
	}
	paths, purpose, err := runtimePaths()
	if err != nil {
		return machineCommandError(args, NewExitError(ExitConfig, errors.New("cannot resolve daemon runtime state for upgrade")))
	}
	launchExecutable := ""
	runtime := upgradeCommandRuntime{
		plan: func(ctx context.Context) (upgradePlan, error) {
			plan, planErr := updater.Plan(ctx)
			if planErr != nil {
				return upgradePlan{}, planErr
			}
			launchExecutable = plan.LaunchExecutable
			return upgradePlan{
				Channel: plan.Channel, CurrentVersion: plan.CurrentVersion,
				TargetVersion: plan.TargetVersion, Available: plan.Available, raw: plan,
			}, nil
		},
		probe: func(ctx context.Context) (daemonControlClient, bool, error) {
			return probeDaemon(ctx, paths, purpose)
		},
		waitStopped: func(ctx context.Context) error {
			return waitForDaemonShutdown(ctx, paths, purpose)
		},
		apply: func(ctx context.Context, plan upgradePlan) error {
			return updater.Apply(ctx, plan.raw)
		},
		start: func(ctx context.Context) (daemonControlClient, error) {
			return connectDaemonWithExecutable(ctx, paths, purpose, launchExecutable)
		},
		verify: func(ctx context.Context, plan upgradePlan, client daemonControlClient) error {
			if err := updater.Verify(ctx, plan.raw); err != nil {
				return err
			}
			if client != nil && !strings.HasPrefix(client.Info().DaemonVersion, plan.TargetVersion+" ") {
				return errors.New("restarted daemon reports an unexpected version")
			}
			return nil
		},
	}
	return executeUpgrade(ctx, options, args, stdout, runtime)
}

func runUpgradeWithRuntime(ctx context.Context, args []string, stdout io.Writer, runtime upgradeCommandRuntime) error {
	options, err := parseUpgradeCommand(args)
	if err != nil {
		return machineCommandError(args, NewExitError(ExitUsage, err))
	}
	return executeUpgrade(ctx, options, args, stdout, runtime)
}

func executeUpgrade(ctx context.Context, options upgradeCommandOptions, args []string, stdout io.Writer, runtime upgradeCommandRuntime) error {
	if runtime.plan == nil {
		return machineCommandError(args, NewExitError(ExitInternal, errors.New("upgrade planner is not configured")))
	}
	plan, err := runtime.plan(ctx)
	if err != nil {
		if errors.Is(ctx.Err(), context.Canceled) {
			return machineCommandError(args, NewExitError(ExitCanceled, errors.New("upgrade canceled while checking for updates")))
		}
		if errors.Is(ctx.Err(), context.DeadlineExceeded) {
			return machineCommandError(args, NewExitError(ExitNetwork, errors.New("upgrade check timed out")))
		}
		return machineCommandError(args, NewExitError(ExitInternal, errors.New("unable to check for an AMSFTP update")))
	}
	if plan.CurrentVersion == "" || plan.TargetVersion == "" || plan.Channel == "" || plan.Available != (plan.CurrentVersion != plan.TargetVersion) {
		return machineCommandError(args, NewExitError(ExitInternal, errors.New("upgrade planner returned an invalid plan")))
	}
	if !plan.Available || plan.CurrentVersion == plan.TargetVersion {
		return writeUpgradeResult(stdout, options, upgradeCommandOutput{
			OutputVersion: PublicCLIContractVersion, Command: "upgrade", Status: "already_current",
			Channel: plan.Channel, PreviousVersion: plan.CurrentVersion, Version: plan.TargetVersion,
		})
	}
	if runtime.probe == nil || runtime.apply == nil {
		return machineCommandError(args, NewExitError(ExitInternal, errors.New("upgrade runtime is incomplete")))
	}
	client, daemonWasRunning, probeErr := runtime.probe(ctx)
	if probeErr != nil {
		if client != nil {
			_ = client.Close()
		}
		if errors.Is(probeErr, context.Canceled) {
			return machineCommandError(args, NewExitError(ExitCanceled, errors.New("upgrade canceled while checking daemon state")))
		}
		return machineCommandError(args, NewExitError(ExitNetwork, errors.New("cannot safely determine daemon state for upgrade")))
	}
	if daemonWasRunning && client == nil {
		return machineCommandError(args, NewExitError(ExitInternal, errors.New("daemon probe returned an invalid running state")))
	}
	if daemonWasRunning {
		var response daemon.ShutdownResponse
		if err := client.Call(ctx, daemon.RequestShutdown, daemon.ShutdownRequest{ForUpgrade: true}, &response); err != nil {
			_ = client.Close()
			return machineCommandError(args, classifyUpgradeShutdownError(err))
		}
		if !response.Accepted {
			_ = client.Close()
			return machineCommandError(args, NewExitError(ExitInternal, errors.New("daemon did not accept upgrade shutdown")))
		}
		if err := client.Close(); err != nil {
			return machineCommandError(args, NewExitError(ExitInternal, errors.New("cannot close daemon control connection for upgrade")))
		}
		if runtime.waitStopped == nil {
			return machineCommandError(args, NewExitError(ExitInternal, errors.New("daemon upgrade shutdown waiter is not configured")))
		}
		if err := runtime.waitStopped(ctx); err != nil {
			return machineCommandError(args, NewExitError(ExitInternal, errors.New("daemon shutdown could not be proven before upgrade")))
		}
	} else if client != nil {
		_ = client.Close()
	}

	if err := runtime.apply(ctx, plan); err != nil {
		recovered := recoverDaemonAfterUpgradeFailure(ctx, daemonWasRunning, runtime)
		failureCode := ExitInternal
		if selfupdate.ApplyEffectUnknown(err) {
			failureCode = ExitPartial
		}
		if daemonWasRunning && !recovered {
			return machineCommandError(args, NewExitError(ExitPartial, errors.New("upgrade failed and the previous daemon could not be restarted")))
		}
		if recovered {
			return machineCommandError(args, NewExitError(failureCode, errors.New("upgrade failed; daemon availability was restored")))
		}
		return machineCommandError(args, NewExitError(failureCode, errors.New("upgrade failed")))
	}

	var restarted daemonControlClient
	if daemonWasRunning {
		if runtime.start == nil {
			return machineCommandError(args, NewExitError(ExitPartial, errors.New("AMSFTP was upgraded but daemon restart is not configured")))
		}
		restarted, err = runtime.start(ctx)
		if err != nil || restarted == nil {
			if restarted != nil {
				_ = restarted.Close()
			}
			return machineCommandError(args, NewExitError(ExitPartial, errors.New("AMSFTP was upgraded but the daemon did not restart")))
		}
		defer restarted.Close()
	}
	if runtime.verify == nil {
		return machineCommandError(args, NewExitError(ExitPartial, errors.New("AMSFTP was upgraded but post-upgrade verification is not configured")))
	}
	if err := runtime.verify(ctx, plan, restarted); err != nil {
		return machineCommandError(args, NewExitError(ExitPartial, errors.New("AMSFTP was upgraded but post-upgrade verification failed")))
	}
	return writeUpgradeResult(stdout, options, upgradeCommandOutput{
		OutputVersion: PublicCLIContractVersion, Command: "upgrade", Status: "upgraded",
		Channel: plan.Channel, PreviousVersion: plan.CurrentVersion, Version: plan.TargetVersion,
		DaemonRestarted: daemonWasRunning,
	})
}

func recoverDaemonAfterUpgradeFailure(ctx context.Context, daemonWasRunning bool, runtime upgradeCommandRuntime) bool {
	if !daemonWasRunning || runtime.start == nil {
		return false
	}
	recoveryCtx := ctx
	cancel := func() {}
	if ctx.Err() != nil {
		recoveryCtx, cancel = context.WithTimeout(context.Background(), 2*daemonReadyTimeout)
	}
	defer cancel()
	client, err := runtime.start(recoveryCtx)
	if err != nil || client == nil {
		if client != nil {
			_ = client.Close()
		}
		return false
	}
	_ = client.Close()
	return true
}

func classifyUpgradeShutdownError(err error) error {
	if errors.Is(err, context.Canceled) {
		return NewExitError(ExitCanceled, errors.New("upgrade canceled during daemon shutdown"))
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return NewExitError(ExitNetwork, errors.New("daemon upgrade shutdown timed out"))
	}
	var remote *daemon.RemoteError
	if errors.As(err, &remote) {
		return classifyJobCLIError(remote)
	}
	return NewExitError(ExitNetwork, errors.New("daemon upgrade shutdown RPC failed"))
}

func parseUpgradeCommand(args []string) (upgradeCommandOptions, error) {
	options := upgradeCommandOptions{format: "human"}
	if len(args) == 0 {
		return options, nil
	}
	if len(args) != 2 || args[0] != "--format" {
		return options, errors.New("upgrade accepts only the optional --format human|json flag")
	}
	if args[1] != "human" && args[1] != "json" {
		return options, fmt.Errorf("unsupported upgrade output format %q", args[1])
	}
	options.format = args[1]
	return options, nil
}

func writeUpgradeResult(stdout io.Writer, options upgradeCommandOptions, output upgradeCommandOutput) error {
	if options.format == "json" {
		encoder := json.NewEncoder(stdout)
		encoder.SetEscapeHTML(false)
		return encoder.Encode(output)
	}
	if output.Status == "already_current" {
		_, err := fmt.Fprintf(stdout, "AMSFTP %s is already current (%s).\n", output.Version, output.Channel)
		return err
	}
	restart := "daemon was not running"
	if output.DaemonRestarted {
		restart = "daemon restarted"
	}
	_, err := fmt.Fprintf(stdout, "Upgraded amsftp from %s to %s (%s); %s.\n", output.PreviousVersion, output.Version, output.Channel, restart)
	return err
}
