package helper

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os/exec"
	"time"

	"github.com/TyrantLucifer/awesome-sftp-cli/internal/platform"
	"github.com/TyrantLucifer/awesome-sftp-cli/internal/transport/openssh"
)

const bindingProbeRemoteCommand = "exec /usr/bin/printf 'amsftp-helper-bind-v1\\000%s\\000%s\\000%s\\000%s\\000' \"$(/usr/bin/id -u)\" \"$(command -p pwd -P)\" \"$(/usr/bin/uname -s)\" \"$(/usr/bin/uname -m)\""

type BindingProbeConfig struct {
	SSHPath     string
	HostAlias   string
	Environment []string
	Redact      []string
	Timeout     time.Duration
}

func BindingProbeSSHArguments(sshPath, hostAlias string) ([]string, error) {
	if err := validateAbsoluteExecutable(sshPath); err != nil {
		return nil, err
	}
	if err := openssh.ValidateHostAlias(hostAlias); err != nil {
		return nil, err
	}
	if len(bindingProbeRemoteCommand) > maxHelperCommandBytes || !isPrintableASCII(bindingProbeRemoteCommand) {
		return nil, errors.New("build helper binding probe arguments: fixed command is invalid")
	}
	return []string{
		sshPath,
		"-T",
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
		hostAlias,
		bindingProbeRemoteCommand,
	}, nil
}

func RunOpenSSHBindingProbe(parent context.Context, config BindingProbeConfig) (Observation, error) {
	if parent == nil || config.Timeout < time.Millisecond || config.Timeout > time.Minute {
		return Observation{}, errors.New("run helper binding probe: context or timeout is invalid")
	}
	arguments, err := BindingProbeSSHArguments(config.SSHPath, config.HostAlias)
	if err != nil {
		return Observation{}, err
	}
	before, err := platform.ExecutableIdentity(config.SSHPath)
	if err != nil {
		return Observation{}, fmt.Errorf("run helper binding probe: validate OpenSSH: %w", err)
	}
	probeContext, cancel := context.WithTimeout(parent, config.Timeout)
	defer cancel()
	command := exec.CommandContext(probeContext, arguments[0], arguments[1:]...) // #nosec G204 -- executable identity and frozen argv are revalidated below.
	configureHelperProcess(command)
	if config.Environment != nil {
		command.Env = append([]string(nil), config.Environment...)
	}
	stdout, err := command.StdoutPipe()
	if err != nil {
		return Observation{}, err
	}
	stderr, err := command.StderrPipe()
	if err != nil {
		return Observation{}, err
	}
	after, err := platform.ExecutableIdentity(config.SSHPath)
	if err != nil {
		return Observation{}, fmt.Errorf("run helper binding probe: revalidate OpenSSH: %w", err)
	}
	if !platform.SameExecutableIdentity(before, after) {
		return Observation{}, errors.New("run helper binding probe: OpenSSH executable changed")
	}
	if err := command.Start(); err != nil {
		return Observation{}, fmt.Errorf("run helper binding probe: start: %w", err)
	}
	stopTermination := context.AfterFunc(probeContext, func() { terminateHelperProcess(command) })
	defer stopTermination()
	collector := &helperStderrBuffer{redactions: append([]string(nil), config.Redact...), cancel: cancel}
	stderrDone := make(chan struct{})
	go func() {
		_, _ = io.Copy(collector, stderr)
		close(stderrDone)
	}()
	raw, readErr := io.ReadAll(io.LimitReader(stdout, MaxProbeStdoutBytes+1))
	waitErr := command.Wait()
	<-stderrDone
	if collector.Overflowed() {
		return Observation{}, fmt.Errorf("run helper binding probe: stderr exceeded %d bytes: %s", MaxHelperStderrBytes, collector.String())
	}
	if err := probeContext.Err(); err != nil {
		return Observation{}, fmt.Errorf("run helper binding probe: %w: %s", err, collector.String())
	}
	if readErr != nil {
		return Observation{}, fmt.Errorf("run helper binding probe: stdout: %w", readErr)
	}
	if len(raw) > MaxProbeStdoutBytes {
		return Observation{}, errors.New("run helper binding probe: stdout exceeds hard limit")
	}
	if waitErr != nil {
		return Observation{}, fmt.Errorf("run helper binding probe: OpenSSH exit: %w: %s", waitErr, collector.String())
	}
	observation, err := ParseBindingProbe(raw)
	if err != nil {
		return Observation{}, err
	}
	return observation, nil
}
