package statefs

import (
	"context"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

const internalProbeChildArgument = "--amsftp-internal-state-probe-v1"

const (
	probeCommandReadMarker byte = 'R'
	probeCommandBusyWrite  byte = 'B'
	probeCommandWrite      byte = 'W'
	probeCommandQuit       byte = 'Q'
	probeResponseOK        byte = 'O'
	probeResponseBusy      byte = 'B'
	probeResponseError     byte = 'E'
	maxProbePathBytes           = 4096
	probeChildLifetime          = 5 * time.Second
)

type probeChildProcess struct {
	command   *exec.Cmd
	toChild   *os.File
	fromChild *os.File
	pid       int
	finished  bool
	cancel    context.CancelFunc
}

func init() { //nolint:gochecknoinits // every containing binary must intercept the private same-binary child before role/test parsing
	if len(os.Args) != 2 || os.Args[1] != internalProbeChildArgument {
		return
	}
	input := os.NewFile(3, "amsftp-state-probe-input")
	output := os.NewFile(4, "amsftp-state-probe-output")
	if input == nil || output == nil {
		os.Exit(1)
	}
	err := runInternalProbeChild(context.Background(), input, output)
	closeErr := errors.Join(input.Close(), output.Close())
	if errors.Join(err, closeErr) != nil {
		os.Exit(1)
	}
	os.Exit(0)
}

func launchProbeChild(ctx context.Context, path string) (*probeChildProcess, error) {
	executable, err := os.Executable()
	if err != nil {
		return nil, fmt.Errorf("state capability probe: resolve current executable: %w", err)
	}
	parentToChildReader, parentToChildWriter, err := os.Pipe()
	if err != nil {
		return nil, fmt.Errorf("state capability probe: create child input pipe: %w", err)
	}
	childToParentReader, childToParentWriter, err := os.Pipe()
	if err != nil {
		_ = parentToChildReader.Close()
		_ = parentToChildWriter.Close()
		return nil, fmt.Errorf("state capability probe: create child output pipe: %w", err)
	}
	childContext, cancel := context.WithTimeout(ctx, probeChildLifetime)
	command := exec.CommandContext(childContext, executable, internalProbeChildArgument) //nolint:gosec // exact current executable and constant private argument
	command.Env = []string{}
	command.ExtraFiles = []*os.File{parentToChildReader, childToParentWriter}
	if err := command.Start(); err != nil {
		cancel()
		_ = parentToChildReader.Close()
		_ = parentToChildWriter.Close()
		_ = childToParentReader.Close()
		_ = childToParentWriter.Close()
		return nil, fmt.Errorf("state capability probe: start same-binary child: %w", err)
	}
	_ = parentToChildReader.Close()
	_ = childToParentWriter.Close()
	child := &probeChildProcess{
		command: command, toChild: parentToChildWriter, fromChild: childToParentReader, pid: command.Process.Pid, cancel: cancel,
	}
	if child.pid <= 0 || child.pid == os.Getpid() {
		child.abort()
		return nil, fmt.Errorf("state capability probe: child PID is not a distinct process")
	}
	if err := writeProbePath(child.toChild, path); err != nil {
		child.abort()
		return nil, fmt.Errorf("state capability probe: send database path to child: %w", err)
	}
	if err := child.readResponse(probeResponseOK); err != nil {
		child.abort()
		return nil, fmt.Errorf("state capability probe: initialize same-binary child: %w", err)
	}
	return child, nil
}

func (child *probeChildProcess) roundTrip(command, want byte) error {
	if child == nil || child.finished || child.toChild == nil || child.fromChild == nil {
		return fmt.Errorf("invalid child process")
	}
	if _, err := child.toChild.Write([]byte{command}); err != nil {
		return fmt.Errorf("write child command: %w", err)
	}
	return child.readResponse(want)
}

func (child *probeChildProcess) readResponse(want byte) error {
	var response [9]byte
	if _, err := io.ReadFull(child.fromChild, response[:]); err != nil {
		return fmt.Errorf("read child response: %w", err)
	}
	pid := binary.BigEndian.Uint64(response[1:])
	if pid != uint64(child.pid) { //nolint:gosec // child.pid is positive and checked at launch
		return fmt.Errorf("child response PID %d, want %d", pid, child.pid)
	}
	if response[0] != want {
		return fmt.Errorf("child response %q, want %q", response[0], want)
	}
	return nil
}

func (child *probeChildProcess) finish() error {
	if child == nil || child.finished {
		return fmt.Errorf("state capability probe: child already finished")
	}
	if err := child.roundTrip(probeCommandQuit, probeResponseOK); err != nil {
		child.abort()
		return fmt.Errorf("state capability probe: stop same-binary child: %w", err)
	}
	child.finished = true
	closeErr := errors.Join(child.toChild.Close(), child.fromChild.Close())
	waitErr := child.command.Wait()
	child.cancel()
	if err := errors.Join(closeErr, waitErr); err != nil {
		return fmt.Errorf("state capability probe: reap same-binary child: %w", err)
	}
	return nil
}

func (child *probeChildProcess) abort() {
	if child == nil || child.finished {
		return
	}
	child.finished = true
	_ = child.toChild.Close()
	_ = child.fromChild.Close()
	if child.command != nil && child.command.Process != nil {
		_ = child.command.Process.Kill()
		_ = child.command.Wait()
	}
	if child.cancel != nil {
		child.cancel()
	}
}

func writeProbePath(writer io.Writer, path string) error {
	encoded := []byte(path)
	if len(encoded) == 0 || len(encoded) > maxProbePathBytes {
		return fmt.Errorf("invalid path length %d", len(encoded))
	}
	var length [4]byte
	binary.BigEndian.PutUint32(length[:], uint32(len(encoded))) //nolint:gosec // length is bounded above
	if _, err := writer.Write(length[:]); err != nil {
		return err
	}
	_, err := writer.Write(encoded)
	return err
}

func readProbePath(reader io.Reader) (string, error) {
	var length [4]byte
	if _, err := io.ReadFull(reader, length[:]); err != nil {
		return "", err
	}
	size := binary.BigEndian.Uint32(length[:])
	if size == 0 || size > maxProbePathBytes {
		return "", fmt.Errorf("invalid path length %d", size)
	}
	encoded := make([]byte, int(size))
	if _, err := io.ReadFull(reader, encoded); err != nil {
		return "", err
	}
	path := string(encoded)
	base := filepath.Base(path)
	wantLength := len(probePrefix) + 32 + len(probeSuffix)
	if !filepath.IsAbs(path) || len(base) != wantLength || !strings.HasPrefix(base, probePrefix) || !strings.HasSuffix(base, probeSuffix) {
		return "", fmt.Errorf("invalid probe path")
	}
	encodedID := strings.TrimSuffix(strings.TrimPrefix(base, probePrefix), probeSuffix)
	decodedID, err := hex.DecodeString(encodedID)
	if err != nil || len(decodedID) != probeRandomBytes || hex.EncodeToString(decodedID) != encodedID {
		return "", fmt.Errorf("invalid probe path ID")
	}
	if err := validatePrivateRegular(path); err != nil {
		return "", err
	}
	return path, nil
}

func runInternalProbeChild(ctx context.Context, input io.Reader, output io.Writer) error {
	path, err := readProbePath(input)
	if err != nil {
		return err
	}
	database, err := openProbeDatabase(path)
	if err != nil {
		return err
	}
	defer database.Close()
	var journalMode string
	if err := database.QueryRowContext(ctx, "PRAGMA journal_mode").Scan(&journalMode); err != nil || journalMode != "wal" {
		return fmt.Errorf("child journal mode %q: %w", journalMode, err)
	}
	if _, err := database.ExecContext(ctx, "PRAGMA wal_autocheckpoint=0"); err != nil {
		return err
	}
	var autoCheckpoint int64
	if err := database.QueryRowContext(ctx, "PRAGMA wal_autocheckpoint").Scan(&autoCheckpoint); err != nil || autoCheckpoint != 0 {
		return fmt.Errorf("child WAL autocheckpoint = %d: %w", autoCheckpoint, err)
	}
	if err := writeProbeResponse(output, probeResponseOK); err != nil {
		return err
	}
	for {
		var command [1]byte
		if _, err := io.ReadFull(input, command[:]); err != nil {
			return err
		}
		switch command[0] {
		case probeCommandReadMarker:
			var marker string
			if err := database.QueryRowContext(ctx, "SELECT value FROM probe_marker WHERE value='parent-marker'").Scan(&marker); err != nil || marker != "parent-marker" {
				_ = writeProbeResponse(output, probeResponseError)
				return fmt.Errorf("child marker %q: %w", marker, err)
			}
			if err := writeProbeResponse(output, probeResponseOK); err != nil {
				return err
			}
		case probeCommandBusyWrite:
			_, err := database.ExecContext(ctx, "INSERT INTO probe_marker(value) VALUES('blocked-child')")
			if err == nil || !strings.Contains(strings.ToLower(err.Error()), "locked") {
				_ = writeProbeResponse(output, probeResponseError)
				return fmt.Errorf("child writer was not bounded by the parent lock: %w", err)
			}
			if err := writeProbeResponse(output, probeResponseBusy); err != nil {
				return err
			}
		case probeCommandWrite:
			if _, err := database.ExecContext(ctx, "INSERT INTO probe_marker(value) VALUES('child-marker')"); err != nil {
				_ = writeProbeResponse(output, probeResponseError)
				return err
			}
			if err := writeProbeResponse(output, probeResponseOK); err != nil {
				return err
			}
		case probeCommandQuit:
			return writeProbeResponse(output, probeResponseOK)
		default:
			_ = writeProbeResponse(output, probeResponseError)
			return fmt.Errorf("unknown child command")
		}
	}
}

func writeProbeResponse(writer io.Writer, status byte) error {
	var response [9]byte
	response[0] = status
	binary.BigEndian.PutUint64(response[1:], uint64(os.Getpid())) //nolint:gosec // process IDs are non-negative on supported platforms
	_, err := writer.Write(response[:])
	return err
}
