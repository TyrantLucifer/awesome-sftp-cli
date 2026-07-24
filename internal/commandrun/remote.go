package commandrun

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path"
	"strings"
	"sync"
	"syscall"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/TyrantLucifer/awesome-sftp-cli/internal/platform"
	"github.com/TyrantLucifer/awesome-sftp-cli/internal/transport/openssh"
)

const (
	remoteMarker       = "amsftp-shell-wire-v1\n"
	MaxDiagnosticBytes = 32 * 1024
)

var ErrRemoteMarker = errors.New("remote command stdout did not start with the protocol marker")

var fixedRemoteCommandArguments = []string{
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
}

// AttributeProber exposes the narrow SFTP attribute read required by the
// remote utility trust preflight. Pointer fields preserve protocol presence.
type AttributeProber interface {
	ProbeAttributes(context.Context, string) (openssh.SFTPAttributes, error)
}

// OpenSSHAttributeProber adapts the raw SFTP v3 attribute probe.
type OpenSSHAttributeProber struct {
	Config openssh.Config
}

func (probe OpenSSHAttributeProber) ProbeAttributes(ctx context.Context, rawPath string) (openssh.SFTPAttributes, error) {
	return openssh.ProbeLinkAttributes(ctx, probe.Config, rawPath)
}

type RemoteConfig struct {
	OpenSSH    openssh.Config
	Attributes AttributeProber
	Timeout    time.Duration
}

// RemotePlan keeps command text and process arguments private so callers
// cannot mutate the security-relevant plan after confirmation.
type RemotePlan struct {
	executable     string
	arguments      []string
	bootstrap      string
	cwd            string
	userText       string
	environment    []string
	redactions     []string
	timeout        time.Duration
	executableInfo os.FileInfo
}

func (plan RemotePlan) Argv() []string {
	return append([]string{plan.executable}, plan.arguments...)
}

func (plan RemotePlan) Arguments() []string {
	return append([]string(nil), plan.arguments...)
}

func (plan RemotePlan) Executable() string { return plan.executable }
func (plan RemotePlan) Bootstrap() string  { return plan.bootstrap }
func (plan RemotePlan) CWD() string        { return plan.cwd }

func PlanRemoteCommand(ctx context.Context, config RemoteConfig, canonicalAbsoluteRawCWD, userText string) (RemotePlan, error) {
	if ctx == nil {
		return RemotePlan{}, errors.New("plan remote command: nil context")
	}
	if config.Timeout < 0 {
		return RemotePlan{}, errors.New("plan remote command: timeout cannot be negative")
	}
	if err := validateUserCommand(userText); err != nil {
		return RemotePlan{}, err
	}
	bootstrap, err := buildRemoteBootstrap(canonicalAbsoluteRawCWD)
	if err != nil {
		return RemotePlan{}, err
	}
	binary := config.OpenSSH.Binary
	if binary == "" {
		binary = openssh.DefaultBinary
	}
	executableInfo, err := platform.ExecutableIdentity(binary)
	if err != nil {
		return RemotePlan{}, fmt.Errorf("plan remote command OpenSSH executable: %w", err)
	}
	if err := openssh.ValidateHostAlias(config.OpenSSH.HostAlias); err != nil {
		return RemotePlan{}, fmt.Errorf("plan remote command: %w", err)
	}
	prober := config.Attributes
	if prober == nil {
		probeConfig := config.OpenSSH
		probeConfig.Binary = binary
		prober = OpenSSHAttributeProber{Config: probeConfig}
	}
	if err := PreflightRemoteUtilities(ctx, prober); err != nil {
		return RemotePlan{}, err
	}
	arguments := append([]string(nil), fixedRemoteCommandArguments...)
	arguments = append(arguments, config.OpenSSH.HostAlias, bootstrap)
	return RemotePlan{
		executable:     binary,
		arguments:      arguments,
		bootstrap:      bootstrap,
		cwd:            canonicalAbsoluteRawCWD,
		userText:       userText,
		environment:    append([]string(nil), config.OpenSSH.Environment...),
		redactions:     append([]string(nil), config.OpenSSH.Redact...),
		timeout:        config.Timeout,
		executableInfo: executableInfo,
	}, nil
}

func validateUserCommand(value string) error {
	if value == "" || len(value) > MaxCommandBytes || !utf8.ValidString(value) {
		return fmt.Errorf("plan remote command: text must be valid UTF-8 with length in [1,%d]", MaxCommandBytes)
	}
	for index := 0; index < len(value); index++ {
		if value[index] == 0 || value[index] == '\r' || value[index] == '\n' {
			return errors.New("plan remote command: NUL, CR, and LF are forbidden")
		}
	}
	return nil
}

func buildRemoteBootstrap(rawCWD string) (string, error) {
	if rawCWD == "" || len(rawCWD) > MaxCommandBytes || !path.IsAbs(rawCWD) || path.Clean(rawCWD) != rawCWD || strings.IndexByte(rawCWD, 0) >= 0 {
		return "", errors.New("plan remote command: cwd must be a canonical absolute raw path without NUL")
	}
	bootstrap := `case ${SHELL-} in /*) cd ` + QuotePOSIXBytes(rawCWD) + ` && /usr/bin/printf '%s\n' amsftp-shell-wire-v1 && IFS= read -r AMSFTP_COMMAND && exec "$SHELL" -c "$AMSFTP_COMMAND";; *) exit 126;; esac`
	if len(bootstrap) > MaxCommandBytes {
		return "", fmt.Errorf("plan remote command: bootstrap exceeds %d bytes", MaxCommandBytes)
	}
	return bootstrap, nil
}

// QuotePOSIXBytes applies the reviewed POSIX byte single-quote encoder.
func QuotePOSIXBytes(value string) string {
	return "'" + strings.ReplaceAll(value, "'", "'\\''") + "'"
}

func PreflightRemoteUtilities(ctx context.Context, prober AttributeProber) error {
	if ctx == nil {
		return errors.New("remote utility preflight: nil context")
	}
	if prober == nil {
		return errors.New("remote utility preflight: nil attribute prober")
	}
	requirements := []struct {
		path       string
		wantType   uint32
		executable bool
	}{
		{path: "/usr", wantType: 0o040000, executable: true},
		{path: "/usr/bin", wantType: 0o040000, executable: true},
		{path: "/usr/bin/printf", wantType: 0o100000, executable: true},
	}
	for _, requirement := range requirements {
		attributes, err := prober.ProbeAttributes(ctx, requirement.path)
		if err != nil {
			return fmt.Errorf("remote utility preflight %s: %w", requirement.path, err)
		}
		if err := validateRemoteUtilityAttributes(requirement.path, attributes, requirement.wantType, requirement.executable); err != nil {
			return err
		}
	}
	return nil
}

func validateRemoteUtilityAttributes(rawPath string, attributes openssh.SFTPAttributes, wantType uint32, executable bool) error {
	if attributes.UID == nil || *attributes.UID != 0 {
		return fmt.Errorf("remote utility preflight %s: root UID is absent or not zero", rawPath)
	}
	if attributes.GID == nil {
		return fmt.Errorf("remote utility preflight %s: GID is absent", rawPath)
	}
	if attributes.Mode == nil {
		return fmt.Errorf("remote utility preflight %s: mode is absent", rawPath)
	}
	mode := *attributes.Mode
	if mode&0o170000 != wantType {
		return fmt.Errorf("remote utility preflight %s: unexpected file type", rawPath)
	}
	if mode&0o022 != 0 {
		return fmt.Errorf("remote utility preflight %s: group or other writable", rawPath)
	}
	if executable && mode&0o111 == 0 {
		return fmt.Errorf("remote utility preflight %s: not executable", rawPath)
	}
	return nil
}

type EffectStatus uint8

const (
	EffectNone EffectStatus = iota
	EffectKnown
	EffectUnknown
)

type RemoteResult struct {
	Stdout           StreamSnapshot
	Stderr           StreamSnapshot
	ExitCode         int
	Signaled         bool
	Signal           string
	Duration         time.Duration
	Effect           EffectStatus
	CommandBytesSent uint64
	Diagnostic       string
}

func RunRemoteCommand(ctx context.Context, plan RemotePlan, streamBytes int) (RemoteResult, error) {
	if ctx == nil {
		return RemoteResult{}, errors.New("run remote command: nil context")
	}
	if streamBytes <= 0 || streamBytes > DefaultStreamBytes {
		return RemoteResult{}, fmt.Errorf("run remote command: stream budget must be in [1,%d]", DefaultStreamBytes)
	}
	if err := validateFrozenRemotePlan(plan); err != nil {
		return RemoteResult{}, err
	}
	currentExecutable, err := platform.ExecutableIdentity(plan.executable)
	if err != nil {
		return RemoteResult{}, fmt.Errorf("run remote command OpenSSH executable: %w", err)
	}
	if !platform.SameExecutableIdentity(plan.executableInfo, currentExecutable) {
		return RemoteResult{}, errors.New("run remote command: OpenSSH executable identity changed")
	}

	runCtx := ctx
	cancel := func() {}
	if plan.timeout > 0 {
		runCtx, cancel = context.WithTimeout(ctx, plan.timeout)
	}
	defer cancel()
	// #nosec G204 -- the executable has a validated absolute trust chain and argv is a frozen remote-command plan.
	command := exec.CommandContext(runCtx, plan.executable, plan.arguments...)
	configureRemoteProcess(command)
	command.WaitDelay = outputDrainWait
	if plan.environment != nil {
		command.Env = append([]string(nil), plan.environment...)
	}
	stdin, err := command.StdinPipe()
	if err != nil {
		return RemoteResult{}, fmt.Errorf("run remote command stdin: %w", err)
	}
	stdoutRing := newByteRing(streamBytes)
	stderrRing := newByteRing(streamBytes)
	marker := newMarkerGate(stdoutRing)
	diagnostic := &diagnosticBuffer{}
	command.Stdout = marker
	command.Stderr = io.MultiWriter(stderrRing, diagnostic)

	started := time.Now()
	if err := command.Start(); err != nil {
		return RemoteResult{}, fmt.Errorf("run remote command start: %w", err)
	}
	waitDone := make(chan error, 1)
	go func() { waitDone <- command.Wait() }()

	var waitErr error
	waited := false
	var markerErr error
	select {
	case markerErr = <-marker.ready:
	case waitErr = <-waitDone:
		waited = true
		markerErr = marker.finishEOF()
	case <-runCtx.Done():
		_ = stdin.Close()
		killRemoteProcessGroup(command)
		waitErr = <-waitDone
		result := remoteResult(command, stdoutRing, stderrRing, diagnostic, started, waitErr, EffectUnknown, 0, plan.redactions)
		return result, runCtx.Err()
	}
	if markerErr != nil {
		_ = stdin.Close()
		killRemoteProcessGroup(command)
		if !waited {
			waitErr = <-waitDone
		}
		result := remoteResult(command, stdoutRing, stderrRing, diagnostic, started, waitErr, EffectNone, 0, plan.redactions)
		return result, fmt.Errorf("run remote command marker: %w", markerErr)
	}
	if err := runCtx.Err(); err != nil {
		_ = stdin.Close()
		killRemoteProcessGroup(command)
		if !waited {
			waitErr = <-waitDone
		}
		result := remoteResult(command, stdoutRing, stderrRing, diagnostic, started, waitErr, EffectUnknown, 0, plan.redactions)
		return result, err
	}

	commandLine := plan.userText + "\n"
	written, writeErr := writeCount(stdin, []byte(commandLine))
	_ = stdin.Close()
	commandBytesSent, conversionErr := nonNegativeUint64(written)
	if conversionErr != nil {
		killRemoteProcessGroup(command)
		if !waited {
			waitErr = <-waitDone
		}
		result := remoteResult(command, stdoutRing, stderrRing, diagnostic, started, waitErr, EffectUnknown, 0, plan.redactions)
		return result, fmt.Errorf("run remote command input byte count: %w", conversionErr)
	}
	if writeErr != nil {
		killRemoteProcessGroup(command)
		if !waited {
			waitErr = <-waitDone
		}
		effect := EffectNone
		if written > 0 {
			effect = EffectUnknown
		}
		result := remoteResult(command, stdoutRing, stderrRing, diagnostic, started, waitErr, effect, commandBytesSent, plan.redactions)
		return result, fmt.Errorf("run remote command input: %w", writeErr)
	}
	if !waited {
		waitErr = <-waitDone
	}
	if errors.Is(waitErr, exec.ErrWaitDelay) {
		killRemoteProcessGroup(command)
		result := remoteResult(command, stdoutRing, stderrRing, diagnostic, started, waitErr, EffectUnknown, commandBytesSent, plan.redactions)
		return result, fmt.Errorf("run remote command detached output: %w", waitErr)
	}
	if err := runCtx.Err(); err != nil {
		result := remoteResult(command, stdoutRing, stderrRing, diagnostic, started, waitErr, EffectUnknown, commandBytesSent, plan.redactions)
		return result, err
	}
	var exitError *exec.ExitError
	if waitErr != nil && !errors.As(waitErr, &exitError) {
		result := remoteResult(command, stdoutRing, stderrRing, diagnostic, started, waitErr, EffectUnknown, commandBytesSent, plan.redactions)
		return result, fmt.Errorf("run remote command wait: %w", waitErr)
	}
	result := remoteResult(command, stdoutRing, stderrRing, diagnostic, started, waitErr, EffectKnown, commandBytesSent, plan.redactions)
	return result, nil
}

func validateFrozenRemotePlan(plan RemotePlan) error {
	if plan.executable == "" || plan.executableInfo == nil {
		return errors.New("run remote command: incomplete plan")
	}
	if err := validateUserCommand(plan.userText); err != nil {
		return fmt.Errorf("run remote command: invalid frozen command: %w", err)
	}
	bootstrap, err := buildRemoteBootstrap(plan.cwd)
	if err != nil || bootstrap != plan.bootstrap {
		return errors.New("run remote command: invalid frozen bootstrap")
	}
	wantLength := len(fixedRemoteCommandArguments) + 2
	if len(plan.arguments) != wantLength {
		return errors.New("run remote command: invalid frozen argv length")
	}
	for index, value := range fixedRemoteCommandArguments {
		if plan.arguments[index] != value {
			return errors.New("run remote command: invalid frozen OpenSSH option")
		}
	}
	hostIndex := len(fixedRemoteCommandArguments)
	if err := openssh.ValidateHostAlias(plan.arguments[hostIndex]); err != nil {
		return fmt.Errorf("run remote command: invalid frozen host alias: %w", err)
	}
	if plan.arguments[hostIndex+1] != plan.bootstrap {
		return errors.New("run remote command: invalid frozen bootstrap argument")
	}
	return nil
}

func configureRemoteProcess(command *exec.Cmd) {
	command.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	command.Cancel = func() error {
		if command.Process == nil {
			return os.ErrProcessDone
		}
		err := syscall.Kill(-command.Process.Pid, syscall.SIGKILL)
		if errors.Is(err, syscall.ESRCH) {
			return os.ErrProcessDone
		}
		return err
	}
}

func killRemoteProcessGroup(command *exec.Cmd) {
	if command != nil && command.Process != nil {
		_ = syscall.Kill(-command.Process.Pid, syscall.SIGKILL)
	}
}

func writeCount(writer io.Writer, value []byte) (int, error) {
	total := 0
	for len(value) > 0 {
		written, err := writer.Write(value)
		if written > 0 {
			total += written
			value = value[written:]
		}
		if err != nil {
			return total, err
		}
		if written == 0 {
			return total, io.ErrShortWrite
		}
	}
	return total, nil
}

func nonNegativeUint64(value int) (uint64, error) {
	if value < 0 {
		return 0, fmt.Errorf("negative value %d", value)
	}
	// #nosec G115 -- every non-negative int value is representable as uint64.
	return uint64(value), nil
}

type markerGate struct {
	mu      sync.Mutex
	prefix  []byte
	decided bool
	ready   chan error
	ring    *byteRing
}

func newMarkerGate(ring *byteRing) *markerGate {
	return &markerGate{ready: make(chan error, 1), ring: ring}
}

func (gate *markerGate) Write(value []byte) (int, error) {
	gate.mu.Lock()
	defer gate.mu.Unlock()
	originalLength := len(value)
	if gate.decided {
		_, _ = gate.ring.Write(value)
		return originalLength, nil
	}
	needed := len(remoteMarker) - len(gate.prefix)
	count := min(needed, len(value))
	gate.prefix = append(gate.prefix, value[:count]...)
	value = value[count:]
	if len(gate.prefix) == len(remoteMarker) {
		gate.decided = true
		if bytes.Equal(gate.prefix, []byte(remoteMarker)) {
			gate.ready <- nil
		} else {
			_, _ = gate.ring.Write(gate.prefix)
			gate.ready <- ErrRemoteMarker
		}
		gate.prefix = nil
		if len(value) > 0 {
			_, _ = gate.ring.Write(value)
		}
	}
	return originalLength, nil
}

func (gate *markerGate) finishEOF() error {
	gate.mu.Lock()
	defer gate.mu.Unlock()
	if gate.decided {
		select {
		case outcome := <-gate.ready:
			return outcome
		default:
			return nil
		}
	}
	gate.decided = true
	_, _ = gate.ring.Write(gate.prefix)
	gate.prefix = nil
	return ErrRemoteMarker
}

type diagnosticBuffer struct {
	mu        sync.Mutex
	data      []byte
	discarded uint64
}

func (buffer *diagnosticBuffer) Write(value []byte) (int, error) {
	buffer.mu.Lock()
	defer buffer.mu.Unlock()
	available := MaxDiagnosticBytes - len(buffer.data)
	if available > 0 {
		count := min(available, len(value))
		buffer.data = append(buffer.data, value[:count]...)
	}
	if len(value) > max(available, 0) {
		discarded, err := nonNegativeUint64(len(value) - max(available, 0))
		if err != nil {
			return 0, err
		}
		buffer.discarded += discarded
	}
	return len(value), nil
}

func (buffer *diagnosticBuffer) String(redactions []string) string {
	buffer.mu.Lock()
	value := append([]byte(nil), buffer.data...)
	discarded := buffer.discarded
	buffer.mu.Unlock()
	cleaned := bytes.Map(func(value rune) rune {
		if unicode.IsControl(value) || value == '\u2028' || value == '\u2029' {
			return ' '
		}
		return value
	}, value)
	text := strings.TrimSpace(strings.ToValidUTF8(string(cleaned), "�"))
	for _, sensitive := range redactions {
		if sensitive != "" {
			text = strings.ReplaceAll(text, sensitive, "[redacted]")
		}
	}
	suffix := ""
	if discarded > 0 {
		suffix = fmt.Sprintf(" [stderr truncated; %d bytes discarded]", discarded)
	}
	budget := MaxDiagnosticBytes - len(suffix)
	if budget < 0 {
		budget = 0
	}
	text = truncateUTF8Bytes(text, budget)
	return text + suffix
}

func truncateUTF8Bytes(value string, limit int) string {
	if len(value) <= limit {
		return value
	}
	value = value[:limit]
	for len(value) > 0 && !utf8.ValidString(value) {
		value = value[:len(value)-1]
	}
	return value
}

func remoteResult(command *exec.Cmd, stdout, stderr *byteRing, diagnostic *diagnosticBuffer, started time.Time, waitErr error, effect EffectStatus, sent uint64, redactions []string) RemoteResult {
	result := RemoteResult{
		Stdout:           stdout.Snapshot(),
		Stderr:           stderr.Snapshot(),
		ExitCode:         -1,
		Duration:         time.Since(started),
		Effect:           effect,
		CommandBytesSent: sent,
		Diagnostic:       diagnostic.String(redactions),
	}
	if command.ProcessState != nil {
		result.ExitCode = command.ProcessState.ExitCode()
		if status, ok := command.ProcessState.Sys().(syscall.WaitStatus); ok && status.Signaled() {
			result.Signaled = true
			result.Signal = status.Signal().String()
		}
	}
	_ = waitErr
	return result
}
