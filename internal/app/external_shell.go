//go:build darwin || linux

package app

import (
	"context"
	"fmt"
	"os"

	"github.com/TyrantLucifer/awesome-mac-sftp/internal/commandrun"
	"github.com/TyrantLucifer/awesome-mac-sftp/internal/domain"
	"github.com/TyrantLucifer/awesome-mac-sftp/internal/terminalhandoff"
	"github.com/TyrantLucifer/awesome-mac-sftp/internal/transport/openssh"
	"github.com/TyrantLucifer/awesome-mac-sftp/internal/tui"
)

type handoffTCellScreen interface {
	Size() (int, int)
	ShowCursor(int, int)
	Show()
	Suspend() error
	Resume() error
	Sync()
}

type tcellHandoffScreen struct {
	screen            handoffTCellScreen
	beforeInputResume func()
}

func newTCellHandoffScreen(screen handoffTCellScreen, beforeInputResume func()) *tcellHandoffScreen {
	return &tcellHandoffScreen{screen: screen, beforeInputResume: beforeInputResume}
}

func (screen *tcellHandoffScreen) Freeze() (terminalhandoff.Snapshot, error) {
	width, height := screen.screen.Size()
	return terminalhandoff.NewSnapshot(nil, terminalhandoff.Size{Columns: width, Rows: height}), nil
}

func (screen *tcellHandoffScreen) StopInput() error { return nil }
func (screen *tcellHandoffScreen) ShowCursor() error {
	screen.screen.ShowCursor(0, 0)
	screen.screen.Show()
	return nil
}
func (screen *tcellHandoffScreen) LeaveAlternate() error { return screen.screen.Suspend() }
func (screen *tcellHandoffScreen) LeaveRaw() error       { return nil }
func (screen *tcellHandoffScreen) EnterAlternate() error {
	if screen.beforeInputResume != nil {
		screen.beforeInputResume()
	}
	return screen.screen.Resume()
}
func (screen *tcellHandoffScreen) EnterRaw() error { return nil }
func (screen *tcellHandoffScreen) RestoreCursor(terminalhandoff.Snapshot) error {
	return nil
}
func (screen *tcellHandoffScreen) ReplayResize(terminalhandoff.Size) error {
	screen.screen.Sync()
	return nil
}
func (screen *tcellHandoffScreen) Resume(terminalhandoff.Snapshot) error {
	screen.screen.Sync()
	return nil
}

func runShellIntent(ctx context.Context, intent tui.Intent, environment []string, screen handoffTCellScreen, beforeInputResume func()) string {
	if ctx == nil || intent.Kind != tui.IntentShell || screen == nil {
		return "shell failed: invalid shell request"
	}
	var plan commandrun.ShellPlan
	var err error
	switch intent.Endpoint.Kind {
	case domain.EndpointLocal:
		shell, resolveErr := commandrun.ResolveLocalShell("", environmentValue(environment, "SHELL"))
		if resolveErr != nil {
			return "local shell failed: " + resolveErr.Error()
		}
		plan, err = commandrun.PlanLocalShell(shell, string(intent.Location.Path), environment)
	case domain.EndpointSSH:
		config := commandrun.RemoteConfig{OpenSSH: openssh.Config{
			HostAlias: intent.Endpoint.SSHHostAlias, Environment: append([]string(nil), environment...),
		}}
		if intent.ShellHome {
			plan, err = commandrun.PlanRemoteShell(ctx, config, commandrun.RemoteShellHome, "", nil)
		} else {
			var proof commandrun.RemoteCWDProof
			proof, err = commandrun.ProbeRemoteShellCWD(ctx, config, string(intent.Location.Path))
			if err != nil {
				return "remote cwd probe failed: " + err.Error() + "; press gS for an explicit home shell"
			}
			plan, err = commandrun.PlanRemoteShell(ctx, config, commandrun.RemoteShellCurrentDirectory, string(intent.Location.Path), &proof)
		}
	default:
		return fmt.Sprintf("shell failed: unsupported endpoint kind %q", intent.Endpoint.Kind)
	}
	if err != nil {
		return "shell plan failed: " + err.Error()
	}
	if err := plan.Revalidate(); err != nil {
		return "shell revalidation failed: " + err.Error()
	}
	tty, err := os.OpenFile("/dev/tty", os.O_RDWR, 0)
	if err != nil {
		return "shell failed: open controlling TTY: " + err.Error()
	}
	defer tty.Close()
	platform, err := terminalhandoff.NewUnixPlatform(tty)
	if err != nil {
		return "shell failed: " + err.Error()
	}
	controller, err := terminalhandoff.NewController(newTCellHandoffScreen(screen, beforeInputResume), platform)
	if err != nil {
		return "shell failed: " + err.Error()
	}
	launcher, err := terminalhandoff.NewExecLauncher(plan.Executable(), plan.Arguments(), plan.Environment(), plan.Directory())
	if err != nil {
		return "shell failed: " + err.Error()
	}
	result, err := controller.Run(ctx, launcher)
	if err != nil {
		return "shell failed after terminal recovery: " + err.Error()
	}
	return formatShellResult(result, intent.Endpoint.Kind == domain.EndpointSSH && !intent.ShellHome)
}

func formatShellResult(result terminalhandoff.Result, offerHomeRetry bool) string {
	var message string
	switch result.Kind {
	case terminalhandoff.ExitNormal:
		message = "shell exited; pane refreshed"
	case terminalhandoff.ExitNonZero:
		message = fmt.Sprintf("shell exited %d; pane refreshed", result.ExitCode)
	case terminalhandoff.ExitSignaled:
		message = "shell terminated by " + result.Signal + "; pane refreshed"
	default:
		message = "shell lost its terminal; pane refreshed"
	}
	if offerHomeRetry && result.Kind != terminalhandoff.ExitNormal {
		message += "; press gS for an explicit home shell"
	}
	return message
}
