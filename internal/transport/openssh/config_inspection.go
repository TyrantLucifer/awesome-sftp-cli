package openssh

import (
	"bufio"
	"context"
	"errors"
	"os/exec"
	"strconv"
	"strings"
	"sync"

	"github.com/TyrantLucifer/awesome-sftp-cli/internal/platform"
)

const maxConfigInspectionBytes = 64 * 1024

type ConfigInspection struct {
	Hostname              string
	Port                  string
	StrictHostKeyChecking string
	KnownHostsConfigured  bool
	ProxyConfigured       bool
}

// InspectConfig asks the validated system OpenSSH to expand one host with -G.
// -G parses configuration only: it does not connect, authenticate, or invoke a
// ProxyCommand. Subprocess output is bounded and is never included in errors.
func InspectConfig(ctx context.Context, binary, hostAlias string) (ConfigInspection, error) {
	if binary == "" {
		binary = DefaultBinary
	}
	if err := ValidateHostAlias(hostAlias); err != nil {
		return ConfigInspection{}, err
	}
	before, err := platform.ExecutableIdentity(binary)
	if err != nil {
		return ConfigInspection{}, errors.New("inspect OpenSSH configuration: executable is not trusted")
	}
	// #nosec G204 -- binary has a validated absolute trust chain and the sole
	// variable argument is a separately validated non-option host alias.
	command := exec.CommandContext(ctx, binary, "-G", "-T", hostAlias)
	configureProcessGroup(command)
	var stdout, stderr configInspectionBuffer
	stdout.limit = maxConfigInspectionBytes
	stderr.limit = maxConfigInspectionBytes
	command.Stdout = &stdout
	command.Stderr = &stderr
	after, err := platform.ExecutableIdentity(binary)
	if err != nil || !platform.SameExecutableIdentity(before, after) {
		return ConfigInspection{}, errors.New("inspect OpenSSH configuration: executable changed before start")
	}
	if err := command.Run(); err != nil {
		return ConfigInspection{}, errors.New("inspect OpenSSH configuration: expansion failed")
	}
	if stdout.overflowed() || stderr.overflowed() {
		return ConfigInspection{}, errors.New("inspect OpenSSH configuration: subprocess output exceeds limit")
	}
	inspection, err := parseConfigInspection(stdout.bytes())
	if err != nil {
		return ConfigInspection{}, errors.New("inspect OpenSSH configuration: expanded configuration is invalid")
	}
	return inspection, nil
}

func parseConfigInspection(content []byte) (ConfigInspection, error) {
	var result ConfigInspection
	configuredKnownHosts := false
	proxyJump := false
	proxyCommand := false
	seen := make(map[string]struct{}, 6)
	scanner := bufio.NewScanner(strings.NewReader(string(content)))
	scanner.Buffer(make([]byte, 4096), maxConfigInspectionBytes)
	for scanner.Scan() {
		key, value, ok := strings.Cut(scanner.Text(), " ")
		if !ok {
			continue
		}
		key = strings.ToLower(key)
		if !isConfigInspectionKey(key) {
			continue
		}
		if _, exists := seen[key]; exists {
			return ConfigInspection{}, errors.New("duplicate OpenSSH expansion field")
		}
		seen[key] = struct{}{}
		value = strings.TrimSpace(value)
		switch key {
		case "hostname":
			if value != "" && !strings.ContainsAny(value, "\x00\r\n") {
				result.Hostname = value
			}
		case "port":
			port, err := strconv.ParseUint(value, 10, 16)
			if err == nil && port > 0 {
				result.Port = strconv.FormatUint(port, 10)
			}
		case "stricthostkeychecking":
			policy := strings.ToLower(value)
			if !isKnownHostCheckingPolicy(policy) {
				return ConfigInspection{}, errors.New("unknown OpenSSH host key policy")
			}
			result.StrictHostKeyChecking = policy
		case "userknownhostsfile":
			for _, path := range strings.Fields(value) {
				if path != "none" && path != "/dev/null" {
					configuredKnownHosts = true
				}
			}
		case "proxyjump":
			proxyJump = value != "" && value != "none"
		case "proxycommand":
			proxyCommand = value != "" && value != "none"
		}
	}
	if err := scanner.Err(); err != nil {
		return ConfigInspection{}, err
	}
	if result.Hostname == "" || result.Port == "" || result.StrictHostKeyChecking == "" {
		return ConfigInspection{}, errors.New("required OpenSSH expansion fields are missing")
	}
	result.KnownHostsConfigured = configuredKnownHosts
	result.ProxyConfigured = proxyJump || proxyCommand
	return result, nil
}

func isConfigInspectionKey(key string) bool {
	switch key {
	case "hostname", "port", "stricthostkeychecking", "userknownhostsfile", "proxyjump", "proxycommand":
		return true
	default:
		return false
	}
}

func isKnownHostCheckingPolicy(value string) bool {
	switch value {
	case "yes", "ask", "accept-new", "no", "true", "on", "false", "off":
		return true
	default:
		return false
	}
}

type configInspectionBuffer struct {
	mu        sync.Mutex
	data      []byte
	discarded int64
	limit     int
}

func (b *configInspectionBuffer) Write(value []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	available := b.limit - len(b.data)
	if available > 0 {
		count := min(available, len(value))
		b.data = append(b.data, value[:count]...)
	}
	if len(value) > available {
		b.discarded += int64(len(value) - max(available, 0))
	}
	return len(value), nil
}

func (b *configInspectionBuffer) bytes() []byte {
	b.mu.Lock()
	defer b.mu.Unlock()
	return append([]byte(nil), b.data...)
}

func (b *configInspectionBuffer) overflowed() bool {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.discarded != 0
}
