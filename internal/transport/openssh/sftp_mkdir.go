package openssh

import (
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"os/exec"

	"github.com/TyrantLucifer/awesome-mac-sftp/internal/platform"
)

const sftpTypeMkdir = 14

// MkdirExact starts an isolated raw SFTP v3 session and sends one MKDIR whose
// create packet carries exact 0700 permissions. It never creates first and
// chmods later, avoiding a server-umask-dependent exposure window.
func MkdirExact(ctx context.Context, config Config, canonicalAbsoluteRawPath string, mode uint32) error {
	if ctx == nil {
		return errors.New("SFTP exact mkdir context is required")
	}
	if err := validateAttributeProbePath(canonicalAbsoluteRawPath); err != nil {
		return fmt.Errorf("SFTP exact mkdir: %w", err)
	}
	if mode != 0o700 {
		return errors.New("SFTP exact mkdir requires mode 0700")
	}
	binaryPath := config.Binary
	if binaryPath == "" {
		binaryPath = DefaultBinary
	}
	before, err := platform.ExecutableIdentity(binaryPath)
	if err != nil {
		return fmt.Errorf("validate OpenSSH executable for exact mkdir: %w", err)
	}
	config.Fresh = true
	arguments, err := argumentsForConfig(config)
	if err != nil {
		return err
	}
	mkdirContext, cancel := context.WithTimeout(ctx, defaultAttributeProbeTimeout)
	defer cancel()
	// #nosec G204 -- binaryPath has a validated absolute trust chain and arguments are frozen fresh-SFTP argv.
	command := exec.CommandContext(mkdirContext, binaryPath, arguments...)
	configureProcessGroup(command)
	if config.Environment != nil {
		command.Env = append([]string(nil), config.Environment...)
	}
	stdin, err := command.StdinPipe()
	if err != nil {
		return fmt.Errorf("open exact mkdir stdin: %w", err)
	}
	stdout, err := command.StdoutPipe()
	if err != nil {
		return fmt.Errorf("open exact mkdir stdout: %w", err)
	}
	stderr, err := command.StderrPipe()
	if err != nil {
		return fmt.Errorf("open exact mkdir stderr: %w", err)
	}
	after, err := platform.ExecutableIdentity(binaryPath)
	if err != nil {
		return fmt.Errorf("revalidate OpenSSH executable for exact mkdir: %w", err)
	}
	if !platform.SameExecutableIdentity(before, after) {
		return errors.New("OpenSSH executable changed before exact mkdir start")
	}
	if err := command.Start(); err != nil {
		return fmt.Errorf("start OpenSSH SFTP exact mkdir: %w", err)
	}
	collector := &boundedBuffer{redactions: append([]string(nil), config.Redact...)}
	collectorDone := make(chan struct{})
	go func() {
		_, _ = io.Copy(collector, stderr)
		close(collectorDone)
	}()
	exchangeErr := exchangeSFTPMkdirExact(stdout, stdin, canonicalAbsoluteRawPath, mode)
	_ = stdin.Close()
	if exchangeErr != nil {
		cancel()
	}
	waitErr := command.Wait()
	<-collectorDone
	if err := mkdirContext.Err(); err != nil {
		return sftpMkdirError(err, collector.String())
	}
	if exchangeErr != nil {
		return sftpMkdirError(exchangeErr, collector.String())
	}
	if waitErr != nil {
		return sftpMkdirError(fmt.Errorf("OpenSSH exact mkdir exited: %w", waitErr), collector.String())
	}
	return nil
}

func exchangeSFTPMkdirExact(reader io.Reader, writer io.Writer, rawPath string, mode uint32) error {
	if err := writeSFTPPacket(writer, sftpInitPacket()); err != nil {
		return fmt.Errorf("send SFTP INIT: %w", err)
	}
	versionPacket, err := readSFTPPacket(reader)
	if err != nil {
		return fmt.Errorf("read SFTP VERSION: %w", err)
	}
	if err := parseSFTPVersion(versionPacket); err != nil {
		return err
	}
	if err := writeSFTPPacket(writer, sftpMkdirPacket(attributeProbeRequestID, rawPath, mode)); err != nil {
		return fmt.Errorf("send SFTP MKDIR: %w", err)
	}
	response, err := readSFTPPacket(reader)
	if err != nil {
		return fmt.Errorf("read SFTP MKDIR response: %w", err)
	}
	return parseSFTPMkdirStatus(response, attributeProbeRequestID)
}

func sftpMkdirPacket(requestID uint32, rawPath string, mode uint32) []byte {
	packet := make([]byte, 1+4+4+len(rawPath)+4+4)
	packet[0] = sftpTypeMkdir
	binary.BigEndian.PutUint32(packet[1:5], requestID)
	// #nosec G115 -- validateAttributeProbePath bounds the path below uint32.
	binary.BigEndian.PutUint32(packet[5:9], uint32(len(rawPath)))
	copy(packet[9:], rawPath)
	offset := 9 + len(rawPath)
	binary.BigEndian.PutUint32(packet[offset:offset+4], sftpAttrPermissions)
	binary.BigEndian.PutUint32(packet[offset+4:offset+8], mode)
	return packet
}

func parseSFTPMkdirStatus(payload []byte, requestID uint32) error {
	decoder := sftpDecoder{value: payload}
	typeCode, err := decoder.byte()
	if err != nil || typeCode != sftpTypeStatus {
		return errors.New("SFTP MKDIR received an invalid STATUS packet")
	}
	responseID, err := decoder.uint32()
	if err != nil || responseID != requestID {
		return errors.New("SFTP MKDIR STATUS request id mismatch")
	}
	code, err := decoder.uint32()
	if err != nil {
		return errors.New("SFTP MKDIR STATUS is truncated")
	}
	if err := decoder.skipString(); err != nil {
		return errors.New("SFTP MKDIR STATUS message is malformed")
	}
	if err := decoder.skipString(); err != nil || decoder.remaining() != 0 {
		return errors.New("SFTP MKDIR STATUS language or trailing bytes are malformed")
	}
	if code != 0 {
		return fmt.Errorf("SFTP MKDIR failed with status code %d", code)
	}
	return nil
}

func sftpMkdirError(err error, diagnostic string) error {
	if diagnostic == "" {
		return fmt.Errorf("SFTP exact mkdir: %w", err)
	}
	return fmt.Errorf("SFTP exact mkdir: %w: %s", err, diagnostic)
}
