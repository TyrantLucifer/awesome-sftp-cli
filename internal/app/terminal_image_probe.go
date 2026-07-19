//go:build darwin || linux

package app

import (
	"errors"
	"fmt"
	"math"
	"os"
	"strings"
	"sync"
	"time"

	builtinpreview "github.com/TyrantLucifer/awesome-sftp-cli/internal/preview"
	"golang.org/x/sys/unix"
)

const terminalImageProbeTimeout = 200 * time.Millisecond

type terminalImageCapabilityState struct {
	mu    sync.RWMutex
	proof builtinpreview.ImageCapabilityProof
}

func newTerminalImageCapabilityState(proof builtinpreview.ImageCapabilityProof) *terminalImageCapabilityState {
	return &terminalImageCapabilityState{proof: proof}
}

func (state *terminalImageCapabilityState) Current() builtinpreview.ImageCapabilityProof {
	state.mu.RLock()
	defer state.mu.RUnlock()
	return state.proof
}

func (state *terminalImageCapabilityState) Reprobe(environment []string) {
	proof := probeTerminalImageCapability(environment)
	state.mu.Lock()
	state.proof = proof
	state.mu.Unlock()
}

func probeTerminalImageCapability(environment []string) builtinpreview.ImageCapabilityProof {
	proof, _ := probeTerminalImageCapabilityResult(environment)
	return proof
}

func probeTerminalImageCapabilityResult(environment []string) (builtinpreview.ImageCapabilityProof, error) {
	protocol := imageProbeCandidate(environmentValues(environment))
	if protocol == builtinpreview.ImageProtocolNone {
		return builtinpreview.ImageCapabilityProof{}, errors.New("terminal image probe: no protocol candidate")
	}
	query, err := builtinpreview.ImageCapabilityProbe(protocol)
	if err != nil {
		return builtinpreview.ImageCapabilityProof{}, err
	}
	descriptor, err := unix.Open("/dev/tty", unix.O_RDWR|unix.O_CLOEXEC|unix.O_NOCTTY|unix.O_NONBLOCK, 0)
	if err != nil {
		return builtinpreview.ImageCapabilityProof{}, fmt.Errorf("terminal image probe: open controlling terminal: %w", err)
	}
	defer unix.Close(descriptor)
	foregroundProcessGroup, err := unix.IoctlGetInt(descriptor, unix.TIOCGPGRP)
	if err != nil {
		return builtinpreview.ImageCapabilityProof{}, fmt.Errorf("terminal image probe: inspect foreground process group: %w", err)
	}
	if foregroundProcessGroup != unix.Getpgrp() {
		return builtinpreview.ImageCapabilityProof{}, errors.New("terminal image probe: process does not own the foreground terminal")
	}
	original, err := getProbeTermios(descriptor)
	if err != nil {
		return builtinpreview.ImageCapabilityProof{}, fmt.Errorf("terminal image probe: read termios: %w", err)
	}
	raw := *original
	raw.Iflag &^= unix.BRKINT | unix.ICRNL | unix.INPCK | unix.ISTRIP | unix.IXON
	raw.Oflag &^= unix.OPOST
	raw.Cflag |= unix.CS8
	raw.Lflag &^= unix.ECHO | unix.ICANON | unix.IEXTEN | unix.ISIG
	raw.Cc[unix.VMIN] = 0
	raw.Cc[unix.VTIME] = 1
	if err := setProbeTermios(descriptor, &raw); err != nil {
		return builtinpreview.ImageCapabilityProof{}, fmt.Errorf("terminal image probe: enter bounded raw mode: %w", err)
	}
	defer func() { _ = setProbeTermios(descriptor, original) }()
	if err := writeProbe(descriptor, query); err != nil {
		return builtinpreview.ImageCapabilityProof{}, fmt.Errorf("terminal image probe: write query: %w", err)
	}
	response, err := readProbeResponse(descriptor, terminalImageProbeTimeout)
	if err != nil {
		return builtinpreview.ImageCapabilityProof{}, fmt.Errorf("terminal image probe: read response: %w", err)
	}
	proof, err := builtinpreview.ConfirmImageCapability(protocol, response)
	if err != nil {
		return builtinpreview.ImageCapabilityProof{}, err
	}
	return proof, nil
}

func imageProbeCandidate(environment map[string]string) builtinpreview.ImageProtocol {
	term := strings.ToLower(environment["TERM"])
	switch {
	case environment["KITTY_WINDOW_ID"] != "" || strings.Contains(term, "kitty"):
		return builtinpreview.ImageProtocolKitty
	case environment["TERM_PROGRAM"] == "iTerm.app":
		return builtinpreview.ImageProtocolITerm2
	case strings.Contains(term, "sixel"):
		return builtinpreview.ImageProtocolSixel
	default:
		return builtinpreview.ImageProtocolNone
	}
}

func writeProbe(descriptor int, query []byte) error {
	for len(query) != 0 {
		written, err := unix.Write(descriptor, query)
		if errors.Is(err, unix.EINTR) {
			continue
		}
		if err != nil {
			return err
		}
		if written <= 0 {
			return errors.New("terminal image probe made no write progress")
		}
		query = query[written:]
	}
	return nil
}

func readProbeResponse(descriptor int, timeout time.Duration) ([]byte, error) {
	if descriptor < 0 || uint64(descriptor) > math.MaxInt32 {
		return nil, errors.New("terminal image probe descriptor is out of range")
	}
	deadline := time.Now().Add(timeout)
	response := make([]byte, 0, 64)
	buffer := make([]byte, 257)
	for len(response) <= 256 {
		remaining := time.Until(deadline)
		if remaining <= 0 {
			return nil, os.ErrDeadlineExceeded
		}
		milliseconds := int(remaining / time.Millisecond)
		if milliseconds < 1 {
			milliseconds = 1
		}
		events := []unix.PollFd{{Fd: int32(descriptor), Events: unix.POLLIN}} // #nosec G115 -- descriptor bounds are checked above.
		ready, err := unix.Poll(events, milliseconds)
		if errors.Is(err, unix.EINTR) {
			continue
		}
		if err != nil {
			return nil, err
		}
		if ready == 0 {
			return nil, os.ErrDeadlineExceeded
		}
		read, err := unix.Read(descriptor, buffer)
		if errors.Is(err, unix.EINTR) || errors.Is(err, unix.EAGAIN) {
			continue
		}
		if err != nil {
			return nil, err
		}
		if read == 0 {
			// VMIN=0 permits a zero-byte TTY read before the terminal reply is
			// available (observed on native Darwin). The absolute deadline still
			// bounds this loop; zero bytes are not proof that the TTY was closed.
			continue
		}
		if read < 0 {
			return nil, errors.New("terminal image probe returned an invalid read count")
		}
		response = append(response, buffer[:read]...)
		if len(response) > 256 {
			return nil, errors.New("terminal image probe response exceeded 256 bytes")
		}
		if _, err := builtinpreview.ConfirmImageCapability(imageResponseProtocol(response), response); err == nil {
			return response, nil
		}
	}
	return nil, errors.New("terminal image probe response exceeded 256 bytes")
}

func imageResponseProtocol(response []byte) builtinpreview.ImageProtocol {
	text := string(response)
	switch {
	case strings.HasPrefix(text, "\x1b_G"):
		return builtinpreview.ImageProtocolKitty
	case strings.HasPrefix(text, "\x1bP>|iTerm2 "):
		return builtinpreview.ImageProtocolITerm2
	case strings.HasPrefix(text, "\x1b[?"):
		return builtinpreview.ImageProtocolSixel
	default:
		return builtinpreview.ImageProtocolNone
	}
}
