package commandrun

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"

	"github.com/TyrantLucifer/awesome-mac-sftp/internal/externalprocess"
	"github.com/TyrantLucifer/awesome-mac-sftp/internal/platform"
	"github.com/TyrantLucifer/awesome-mac-sftp/internal/transport/openssh"
)

type ShellKind string

const (
	ShellLocal  ShellKind = "local"
	ShellRemote ShellKind = "remote"
)

type RemoteShellMode string

const (
	RemoteShellHome             RemoteShellMode = "home"
	RemoteShellCurrentDirectory RemoteShellMode = "current_directory"
)

var fixedRemoteShellArguments = []string{
	"-tt",
	"-oEscapeChar=none",
	"-oForwardAgent=no",
	"-oForwardX11=no",
	"-oPermitLocalCommand=no",
	"-oClearAllForwardings=yes",
	"-oRemoteCommand=none",
	"-oStdinNull=no",
	"-oForkAfterAuthentication=no",
	"-oTunnel=no",
	"-oGSSAPIDelegateCredentials=no",
	"-oControlMaster=no",
	"-oControlPath=none",
	"-oControlPersist=no",
	"-oSessionType=default",
}

type ShellPlan struct {
	kind           ShellKind
	executable     string
	arguments      []string
	environment    []string
	directory      string
	localShell     externalprocess.ResolvedCommand
	directoryInfo  fs.FileInfo
	executableInfo fs.FileInfo
}

func (plan ShellPlan) Kind() ShellKind       { return plan.kind }
func (plan ShellPlan) Executable() string    { return plan.executable }
func (plan ShellPlan) Directory() string     { return plan.directory }
func (plan ShellPlan) Arguments() []string   { return append([]string(nil), plan.arguments...) }
func (plan ShellPlan) Environment() []string { return append([]string(nil), plan.environment...) }
func (plan ShellPlan) Argv() []string        { return append([]string{plan.executable}, plan.arguments...) }

func (plan ShellPlan) Revalidate() error {
	switch plan.kind {
	case ShellLocal:
		if err := plan.localShell.Revalidate(); err != nil {
			return fmt.Errorf("revalidate local shell: %w", err)
		}
		current, err := os.Lstat(plan.directory)
		if err != nil || plan.directoryInfo == nil || !os.SameFile(plan.directoryInfo, current) || !current.IsDir() || current.Mode()&os.ModeSymlink != 0 {
			return errors.New("revalidate local shell: cwd identity changed")
		}
	case ShellRemote:
		current, err := platform.ExecutableIdentity(plan.executable)
		if err != nil {
			return fmt.Errorf("revalidate remote shell OpenSSH executable: %w", err)
		}
		if !platform.SameExecutableIdentity(plan.executableInfo, current) {
			return errors.New("revalidate remote shell: OpenSSH executable identity changed")
		}
	default:
		return fmt.Errorf("revalidate shell: unknown kind %q", plan.kind)
	}
	return nil
}

func PlanLocalShell(shell externalprocess.ResolvedCommand, cwd string, environment []string) (ShellPlan, error) {
	if err := shell.Revalidate(); err != nil {
		return ShellPlan{}, fmt.Errorf("plan local shell: %w", err)
	}
	if !filepath.IsAbs(cwd) || filepath.Clean(cwd) != cwd {
		return ShellPlan{}, errors.New("plan local shell: cwd must be canonical and absolute")
	}
	info, err := os.Lstat(cwd)
	if err != nil {
		return ShellPlan{}, fmt.Errorf("plan local shell cwd: %w", err)
	}
	if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return ShellPlan{}, errors.New("plan local shell: cwd must be a real directory")
	}
	return ShellPlan{
		kind: ShellLocal, executable: shell.Executable, arguments: append([]string(nil), shell.Args...),
		environment: externalprocess.ScrubEnvironment(environment), directory: cwd, localShell: shell, directoryInfo: info,
	}, nil
}

type RemoteCWDProof struct {
	hostAlias      string
	cwd            string
	executable     string
	executableInfo fs.FileInfo
}

func ProbeRemoteShellCWD(ctx context.Context, config RemoteConfig, canonicalAbsoluteRawCWD string) (RemoteCWDProof, error) {
	plan, err := PlanRemoteCommand(ctx, config, canonicalAbsoluteRawCWD, ":")
	if err != nil {
		return RemoteCWDProof{}, fmt.Errorf("probe remote shell cwd: %w", err)
	}
	result, err := RunRemoteCommand(ctx, plan, DefaultStreamBytes)
	if err != nil {
		return RemoteCWDProof{}, fmt.Errorf("probe remote shell cwd: %w", err)
	}
	if result.Effect != EffectKnown || result.ExitCode != 0 {
		return RemoteCWDProof{}, fmt.Errorf("probe remote shell cwd: remote probe exited %d", result.ExitCode)
	}
	return RemoteCWDProof{
		hostAlias: config.OpenSSH.HostAlias, cwd: canonicalAbsoluteRawCWD,
		executable: plan.executable, executableInfo: plan.executableInfo,
	}, nil
}

func PlanRemoteShell(ctx context.Context, config RemoteConfig, mode RemoteShellMode, canonicalAbsoluteRawCWD string, proof *RemoteCWDProof) (ShellPlan, error) {
	if ctx == nil {
		return ShellPlan{}, errors.New("plan remote shell: nil context")
	}
	if err := openssh.ValidateHostAlias(config.OpenSSH.HostAlias); err != nil {
		return ShellPlan{}, fmt.Errorf("plan remote shell: %w", err)
	}
	binary := config.OpenSSH.Binary
	if binary == "" {
		binary = openssh.DefaultBinary
	}
	identity, err := platform.ExecutableIdentity(binary)
	if err != nil {
		return ShellPlan{}, fmt.Errorf("plan remote shell OpenSSH executable: %w", err)
	}
	arguments := append([]string(nil), fixedRemoteShellArguments...)
	arguments = append(arguments, config.OpenSSH.HostAlias)
	switch mode {
	case RemoteShellHome:
		if canonicalAbsoluteRawCWD != "" || proof != nil {
			return ShellPlan{}, errors.New("plan remote home shell: cwd and proof must be empty")
		}
	case RemoteShellCurrentDirectory:
		bootstrap, buildErr := buildRemoteShellBootstrap(canonicalAbsoluteRawCWD)
		if buildErr != nil {
			return ShellPlan{}, buildErr
		}
		if proof == nil || proof.hostAlias != config.OpenSSH.HostAlias || proof.cwd != canonicalAbsoluteRawCWD || proof.executable != binary || !platform.SameExecutableIdentity(proof.executableInfo, identity) {
			return ShellPlan{}, errors.New("plan remote current-directory shell: matching successful probe is required")
		}
		arguments = append(arguments, bootstrap)
	default:
		return ShellPlan{}, fmt.Errorf("plan remote shell: unknown mode %q", mode)
	}
	return ShellPlan{
		kind: ShellRemote, executable: binary, arguments: arguments,
		environment: append([]string(nil), config.OpenSSH.Environment...), executableInfo: identity,
	}, nil
}

func buildRemoteShellBootstrap(canonicalAbsoluteRawCWD string) (string, error) {
	if _, err := buildRemoteBootstrap(canonicalAbsoluteRawCWD); err != nil {
		return "", fmt.Errorf("plan remote shell: %w", err)
	}
	bootstrap := `case ${SHELL-} in /*) cd ` + QuotePOSIXBytes(canonicalAbsoluteRawCWD) + ` && exec "$SHELL" -l;; *) exit 126;; esac`
	if len(bootstrap) > MaxCommandBytes {
		return "", fmt.Errorf("plan remote shell: bootstrap exceeds %d bytes", MaxCommandBytes)
	}
	return bootstrap, nil
}
