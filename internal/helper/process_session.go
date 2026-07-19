package helper

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os/exec"
	"strings"
	"sync"
	"time"

	"github.com/TyrantLucifer/awesome-sftp-cli/internal/platform"
)

const MaxHelperStderrBytes = 64 * 1024

const (
	DefaultHelperHeartbeatInterval = 30 * time.Second
	DefaultHelperHeartbeatTimeout  = 10 * time.Second
)

type OpenSSHSessionConfig struct {
	SSHPath           string
	HostAlias         string
	Plan              InstallPlan
	Environment       []string
	Redact            []string
	Hello             ClientHello
	HandshakeTimeout  time.Duration
	HeartbeatInterval time.Duration
	HeartbeatTimeout  time.Duration
}

type ProcessSession struct {
	client     *Client
	cancel     context.CancelFunc
	process    *processCompletion
	stderrDone <-chan struct{}
	stderr     *helperStderrBuffer

	closeOnce sync.Once
	closeErr  error
}

type processCompletion struct {
	done chan struct{}
	err  error
}

// StartOpenSSHSession launches one fresh, non-listening Helper over OpenSSH
// stdio. The remote command is produced solely by HelperSSHArguments; all
// operation paths and patterns are subsequently sent in framed stdin.
func StartOpenSSHSession(parent context.Context, config OpenSSHSessionConfig) (*ProcessSession, error) {
	if parent == nil {
		return nil, errors.New("start helper OpenSSH session: context is required")
	}
	if config.HandshakeTimeout < time.Millisecond || config.HandshakeTimeout > time.Minute {
		return nil, errors.New("start helper OpenSSH session: handshake timeout is outside hard limits")
	}
	if (config.HeartbeatInterval == 0) != (config.HeartbeatTimeout == 0) {
		return nil, errors.New("start helper OpenSSH session: heartbeat interval and timeout must be enabled together")
	}
	if config.HeartbeatInterval == 0 {
		config.HeartbeatInterval = DefaultHelperHeartbeatInterval
		config.HeartbeatTimeout = DefaultHelperHeartbeatTimeout
	}
	arguments, err := HelperSSHArguments(config.SSHPath, config.HostAlias, config.Plan)
	if err != nil {
		return nil, err
	}
	before, err := platform.ExecutableIdentity(config.SSHPath)
	if err != nil {
		return nil, fmt.Errorf("start helper OpenSSH session: validate executable: %w", err)
	}
	processContext, cancel := context.WithCancel(parent)
	command := exec.CommandContext(processContext, arguments[0], arguments[1:]...) // #nosec G204 -- executable identity and frozen argv are revalidated below.
	configureHelperProcess(command)
	if config.Environment != nil {
		command.Env = append([]string(nil), config.Environment...)
	}
	stdin, err := command.StdinPipe()
	if err != nil {
		cancel()
		return nil, fmt.Errorf("start helper OpenSSH session: stdin: %w", err)
	}
	stdout, err := command.StdoutPipe()
	if err != nil {
		cancel()
		return nil, fmt.Errorf("start helper OpenSSH session: stdout: %w", err)
	}
	stderrPipe, err := command.StderrPipe()
	if err != nil {
		cancel()
		return nil, fmt.Errorf("start helper OpenSSH session: stderr: %w", err)
	}
	after, err := platform.ExecutableIdentity(config.SSHPath)
	if err != nil {
		cancel()
		return nil, fmt.Errorf("start helper OpenSSH session: revalidate executable: %w", err)
	}
	if !platform.SameExecutableIdentity(before, after) {
		cancel()
		return nil, errors.New("start helper OpenSSH session: executable changed before start")
	}
	collector := &helperStderrBuffer{redactions: append([]string(nil), config.Redact...), cancel: cancel}
	if err := command.Start(); err != nil {
		cancel()
		return nil, fmt.Errorf("start helper OpenSSH session: start: %w", err)
	}
	stderrDone := make(chan struct{})
	go func() {
		_, _ = io.Copy(collector, stderrPipe)
		close(stderrDone)
	}()
	stopTermination := context.AfterFunc(processContext, func() { terminateHelperProcess(command) })
	process := &processCompletion{done: make(chan struct{})}
	go func() {
		process.err = command.Wait()
		close(process.done)
		stopTermination()
	}()
	handshakeContext, stopHandshake := context.WithTimeout(processContext, config.HandshakeTimeout)
	stopOnTimeout := context.AfterFunc(handshakeContext, cancel)
	client, err := newClient(processContext, stdout, stdin, config.Hello, func(error) { cancel() })
	stopOnTimeout()
	stopHandshake()
	if err != nil {
		cancel()
		_ = stdin.Close()
		_ = stdout.Close()
		<-process.done
		<-stderrDone
		if collector.Overflowed() {
			return nil, fmt.Errorf("start helper OpenSSH session: stderr exceeded %d bytes: %s", MaxHelperStderrBytes, collector.String())
		}
		return nil, fmt.Errorf("start helper OpenSSH session: handshake: %w: %s", err, collector.String())
	}
	if err := client.EnableHeartbeat(config.HeartbeatInterval, config.HeartbeatTimeout); err != nil {
		_ = client.Close()
		cancel()
		<-process.done
		<-stderrDone
		return nil, err
	}
	return &ProcessSession{client: client, cancel: cancel, process: process, stderrDone: stderrDone, stderr: collector}, nil
}

func (s *ProcessSession) Client() *Client {
	if s == nil {
		return nil
	}
	return s.client
}

func (s *ProcessSession) Diagnostic() string {
	if s == nil || s.stderr == nil {
		return ""
	}
	return s.stderr.String()
}

// Wait blocks until the client reader, OpenSSH process, and stderr reader have
// all reached terminal state. The returned error is the process wait result;
// protocol and heartbeat failures remain available through Client().Failure().
func (s *ProcessSession) Wait() error {
	if s == nil {
		return nil
	}
	<-s.client.done
	<-s.process.done
	<-s.stderrDone
	return s.process.err
}

func (s *ProcessSession) Close() error {
	if s == nil {
		return nil
	}
	s.closeOnce.Do(func() {
		s.cancel()
		s.closeErr = s.client.Close()
		_ = s.Wait()
	})
	return s.closeErr
}

type helperStderrBuffer struct {
	mu         sync.Mutex
	data       []byte
	discarded  uint64
	redactions []string
	cancel     context.CancelFunc
	overflow   sync.Once
}

func (b *helperStderrBuffer) Write(value []byte) (int, error) {
	b.mu.Lock()
	available := MaxHelperStderrBytes - len(b.data)
	if available > 0 {
		count := min(available, len(value))
		b.data = append(b.data, value[:count]...)
	}
	if len(value) > available {
		b.discarded += uint64(len(value) - max(available, 0)) // #nosec G115 -- subtraction is positive under the enclosing condition.
		b.overflow.Do(b.cancel)
	}
	b.mu.Unlock()
	return len(value), nil
}

func (b *helperStderrBuffer) Overflowed() bool {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.discarded != 0
}

func (b *helperStderrBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	cleaned := bytes.Map(func(value rune) rune {
		if value == '\n' || value == '\t' || value >= 0x20 {
			return value
		}
		return -1
	}, b.data)
	text := strings.TrimSpace(string(cleaned))
	for _, sensitive := range b.redactions {
		if sensitive != "" {
			text = strings.ReplaceAll(text, sensitive, "[redacted]")
		}
	}
	if b.discarded != 0 {
		return fmt.Sprintf("%s [stderr truncated; %d bytes discarded]", text, b.discarded)
	}
	return text
}
