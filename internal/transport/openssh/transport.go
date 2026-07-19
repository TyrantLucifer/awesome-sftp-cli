package openssh

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os/exec"
	"strings"
	"sync"
	"syscall"
	"unicode/utf8"

	"github.com/TyrantLucifer/awesome-sftp-cli/internal/platform"
	pkgsftp "github.com/pkg/sftp"
)

const DefaultBinary = "/usr/bin/ssh"
const maxStderrBytes = 64 * 1024

var fixedSFTPArguments = []string{
	"-T", "-oEscapeChar=none", "-oForwardAgent=no", "-oForwardX11=no",
	"-oPermitLocalCommand=no", "-oClearAllForwardings=yes", "-oRemoteCommand=none",
	"-oStdinNull=no", "-oForkAfterAuthentication=no", "-oTunnel=no",
	"-oGSSAPIDelegateCredentials=no", "-s",
}

type Config struct {
	Binary, HostAlias string
	ConfigFile        string
	Environment       []string
	Redact            []string
	Fresh             bool
}

func Arguments(hostAlias string) ([]string, error) {
	if err := ValidateHostAlias(hostAlias); err != nil {
		return nil, err
	}
	arguments := append([]string(nil), fixedSFTPArguments...)
	return append(arguments, hostAlias, "sftp"), nil
}

func freshArguments(hostAlias string) ([]string, error) {
	arguments, err := Arguments(hostAlias)
	if err != nil {
		return nil, err
	}
	insert := len(arguments) - 2
	fresh := []string{"-oControlMaster=no", "-oControlPath=none", "-oControlPersist=no"}
	arguments = append(arguments, make([]string, len(fresh))...)
	copy(arguments[insert+len(fresh):], arguments[insert:])
	copy(arguments[insert:], fresh)
	return arguments, nil
}

func argumentsForConfig(config Config) ([]string, error) {
	if err := validateExplicitConfigFile(config.ConfigFile); err != nil {
		return nil, err
	}
	var arguments []string
	var err error
	if config.Fresh {
		arguments, err = freshArguments(config.HostAlias)
	} else {
		arguments, err = Arguments(config.HostAlias)
	}
	if err != nil {
		return nil, err
	}
	if config.ConfigFile != "" {
		arguments = append([]string{"-F", config.ConfigFile}, arguments...)
	}
	return arguments, nil
}

func validateExplicitConfigFile(path string) error {
	if path == "" {
		return nil
	}
	if err := platform.ValidatePrivateFile(path, platform.ValidateRuntime); err != nil {
		return fmt.Errorf("validate explicit OpenSSH config: %w", err)
	}
	return nil
}

func ValidateHostAlias(value string) error {
	if value == "" {
		return errors.New("SSH host alias is empty")
	}
	if strings.HasPrefix(value, "-") {
		return errors.New("SSH host alias starts with a dash")
	}
	if !utf8.ValidString(value) {
		return errors.New("SSH host alias is not valid UTF-8")
	}
	for _, value := range []byte(value) {
		if value == 0 || value < 0x20 || value == 0x7f {
			return errors.New("SSH host alias contains a control byte")
		}
	}
	return nil
}

type Session struct {
	client   *pkgsftp.Client
	command  *exec.Cmd
	cancel   context.CancelFunc
	stderr   *boundedBuffer
	waitOnce sync.Once
	waitErr  error
}

type dialLifecycle struct {
	mu               sync.Mutex
	established      bool
	cancel           context.CancelFunc
	stopParentCancel func() bool
}

func newDialLifecycle(parent context.Context) (context.Context, *dialLifecycle) {
	commandContext, cancel := context.WithCancel(context.Background())
	lifecycle := &dialLifecycle{cancel: cancel}
	lifecycle.stopParentCancel = context.AfterFunc(parent, lifecycle.cancelBeforeEstablished)
	return commandContext, lifecycle
}

func (l *dialLifecycle) cancelBeforeEstablished() {
	l.mu.Lock()
	defer l.mu.Unlock()
	if !l.established {
		l.cancel()
	}
}

func (l *dialLifecycle) establish(parent context.Context) error {
	l.mu.Lock()
	defer l.mu.Unlock()
	if err := parent.Err(); err != nil {
		l.cancel()
		return err
	}
	l.established = true
	return nil
}

func (l *dialLifecycle) stop() {
	l.stopParentCancel()
}

func Dial(ctx context.Context, config Config) (*Session, error) {
	binary := config.Binary
	if binary == "" {
		binary = DefaultBinary
	}
	before, err := platform.ExecutableIdentity(binary)
	if err != nil {
		return nil, fmt.Errorf("validate OpenSSH executable: %w", err)
	}
	arguments, err := argumentsForConfig(config)
	if err != nil {
		return nil, err
	}
	commandCtx, lifecycle := newDialLifecycle(ctx)
	cancel := lifecycle.cancel
	defer lifecycle.stop()
	// #nosec G204 -- binary has a validated absolute trust chain and arguments are fixed plus a validated host alias.
	command := exec.CommandContext(commandCtx, binary, arguments...)
	configureProcessGroup(command)
	if config.Environment != nil {
		command.Env = append([]string(nil), config.Environment...)
	}
	stdin, err := command.StdinPipe()
	if err != nil {
		cancel()
		return nil, err
	}
	stdout, err := command.StdoutPipe()
	if err != nil {
		cancel()
		return nil, err
	}
	stderrPipe, err := command.StderrPipe()
	if err != nil {
		cancel()
		return nil, err
	}
	after, err := platform.ExecutableIdentity(binary)
	if err != nil {
		cancel()
		return nil, fmt.Errorf("revalidate OpenSSH executable: %w", err)
	}
	if !platform.SameExecutableIdentity(before, after) {
		cancel()
		return nil, errors.New("OpenSSH executable changed before start")
	}
	if err := command.Start(); err != nil {
		cancel()
		return nil, fmt.Errorf("start OpenSSH SFTP subsystem: %w", err)
	}
	collector := &boundedBuffer{redactions: append([]string(nil), config.Redact...)}
	go func() { _, _ = io.Copy(collector, stderrPipe) }()
	client, err := pkgsftp.NewClientPipe(stdout, stdin)
	if err != nil {
		cancel()
		_ = command.Wait()
		return nil, fmt.Errorf("negotiate SFTP subsystem: %w: %s", err, collector.String())
	}
	if err := lifecycle.establish(ctx); err != nil {
		_ = client.Close()
		cancel()
		_ = command.Wait()
		return nil, fmt.Errorf("negotiate SFTP subsystem: %w", err)
	}
	return &Session{client: client, command: command, cancel: cancel, stderr: collector}, nil
}

func (s *Session) Client() *pkgsftp.Client {
	if s == nil {
		return nil
	}
	return s.client
}
func (s *Session) Diagnostic() string {
	if s == nil {
		return ""
	}
	return s.stderr.String()
}
func (s *Session) Close() error {
	if s == nil {
		return nil
	}
	clientErr := s.client.Close()
	s.cancel()
	s.waitOnce.Do(func() { s.waitErr = s.command.Wait() })
	if s.waitErr != nil && !isExpectedExit(s.waitErr) {
		return errors.Join(clientErr, fmt.Errorf("OpenSSH exited: %w", s.waitErr))
	}
	return clientErr
}

func isExpectedExit(err error) bool {
	if errors.Is(err, context.Canceled) {
		return true
	}
	var exit *exec.ExitError
	if !errors.As(err, &exit) {
		return false
	}
	status, ok := exit.Sys().(syscall.WaitStatus)
	return ok && status.Signaled() && status.Signal() == syscall.SIGKILL
}

type boundedBuffer struct {
	mu         sync.Mutex
	data       []byte
	discarded  int64
	redactions []string
}

func (b *boundedBuffer) Write(value []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	available := maxStderrBytes - len(b.data)
	if available > 0 {
		count := min(available, len(value))
		b.data = append(b.data, value[:count]...)
	}
	if len(value) > available {
		b.discarded += int64(len(value) - max(available, 0))
	}
	return len(value), nil
}
func (b *boundedBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	cleaned := bytes.Map(func(r rune) rune {
		if r == '\n' || r == '\t' || r >= 0x20 {
			return r
		}
		return -1
	}, b.data)
	text := strings.TrimSpace(string(cleaned))
	for _, sensitive := range b.redactions {
		if sensitive != "" {
			text = strings.ReplaceAll(text, sensitive, "[redacted]")
		}
	}
	if b.discarded > 0 {
		return fmt.Sprintf("%s [stderr truncated; %d bytes discarded]", text, b.discarded)
	}
	return text
}
