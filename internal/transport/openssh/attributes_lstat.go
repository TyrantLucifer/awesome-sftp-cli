package openssh

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os/exec"

	"github.com/TyrantLucifer/awesome-mac-sftp/internal/platform"
)

const sftpTypeLstat = 7

// ProbeLinkAttributes starts a fresh SFTP subprocess and performs one raw
// SFTP v3 LSTAT. Unlike STAT, this preserves a final symlink's own type so
// security preflights can require a real directory or regular file.
func ProbeLinkAttributes(ctx context.Context, config Config, canonicalAbsoluteRawPath string) (SFTPAttributes, error) {
	if err := validateAttributeProbePath(canonicalAbsoluteRawPath); err != nil {
		return SFTPAttributes{}, err
	}
	binaryPath := config.Binary
	if binaryPath == "" {
		binaryPath = DefaultBinary
	}
	before, err := platform.ExecutableIdentity(binaryPath)
	if err != nil {
		return SFTPAttributes{}, fmt.Errorf("validate OpenSSH executable for link attribute probe: %w", err)
	}
	arguments, err := Arguments(config.HostAlias)
	if err != nil {
		return SFTPAttributes{}, err
	}
	probeContext, cancel := context.WithTimeout(ctx, defaultAttributeProbeTimeout)
	defer cancel()

	// #nosec G204 -- binaryPath has a validated absolute trust chain and arguments are the frozen SFTP argv.
	command := exec.CommandContext(probeContext, binaryPath, arguments...)
	configureProcessGroup(command)
	if config.Environment != nil {
		command.Env = append([]string(nil), config.Environment...)
	}
	stdin, err := command.StdinPipe()
	if err != nil {
		return SFTPAttributes{}, fmt.Errorf("open link attribute probe stdin: %w", err)
	}
	stdout, err := command.StdoutPipe()
	if err != nil {
		return SFTPAttributes{}, fmt.Errorf("open link attribute probe stdout: %w", err)
	}
	stderr, err := command.StderrPipe()
	if err != nil {
		return SFTPAttributes{}, fmt.Errorf("open link attribute probe stderr: %w", err)
	}
	after, err := platform.ExecutableIdentity(binaryPath)
	if err != nil {
		return SFTPAttributes{}, fmt.Errorf("revalidate OpenSSH executable for link attribute probe: %w", err)
	}
	if !platform.SameExecutableIdentity(before, after) {
		return SFTPAttributes{}, errors.New("OpenSSH executable changed before link attribute probe start")
	}
	if err := command.Start(); err != nil {
		return SFTPAttributes{}, fmt.Errorf("start OpenSSH SFTP link attribute probe: %w", err)
	}

	collector := &boundedBuffer{redactions: append([]string(nil), config.Redact...)}
	collectorDone := make(chan struct{})
	go func() {
		_, _ = io.Copy(collector, stderr)
		close(collectorDone)
	}()

	attributes, exchangeErr := exchangeSFTPLinkAttributes(stdout, stdin, canonicalAbsoluteRawPath)
	_ = stdin.Close()
	if exchangeErr != nil {
		cancel()
	}
	waitErr := command.Wait()
	<-collectorDone
	if err := probeContext.Err(); err != nil {
		return SFTPAttributes{}, linkAttributeProbeError(err, collector.String())
	}
	if exchangeErr != nil {
		return SFTPAttributes{}, linkAttributeProbeError(exchangeErr, collector.String())
	}
	if waitErr != nil {
		return SFTPAttributes{}, linkAttributeProbeError(fmt.Errorf("OpenSSH link attribute probe exited: %w", waitErr), collector.String())
	}
	return attributes, nil
}

func exchangeSFTPLinkAttributes(reader io.Reader, writer io.Writer, rawPath string) (SFTPAttributes, error) {
	if err := writeSFTPPacket(writer, sftpInitPacket()); err != nil {
		return SFTPAttributes{}, fmt.Errorf("send SFTP INIT: %w", err)
	}
	versionPacket, err := readSFTPPacket(reader)
	if err != nil {
		return SFTPAttributes{}, fmt.Errorf("read SFTP VERSION: %w", err)
	}
	if err := parseSFTPVersion(versionPacket); err != nil {
		return SFTPAttributes{}, err
	}
	if err := writeSFTPPacket(writer, sftpLstatPacket(attributeProbeRequestID, rawPath)); err != nil {
		return SFTPAttributes{}, fmt.Errorf("send SFTP LSTAT: %w", err)
	}
	response, err := readSFTPPacket(reader)
	if err != nil {
		return SFTPAttributes{}, fmt.Errorf("read SFTP LSTAT response: %w", err)
	}
	return parseSFTPAttributes(response, attributeProbeRequestID)
}

func sftpLstatPacket(requestID uint32, rawPath string) []byte {
	packet := sftpStatPacket(requestID, rawPath)
	packet[0] = sftpTypeLstat
	return packet
}

func linkAttributeProbeError(err error, diagnostic string) error {
	if diagnostic == "" {
		return fmt.Errorf("SFTP link attribute probe: %w", err)
	}
	return fmt.Errorf("SFTP link attribute probe: %w: %s", err, diagnostic)
}
