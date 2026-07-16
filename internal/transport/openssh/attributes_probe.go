package openssh

import (
	"bytes"
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"os/exec"
	"path"
	"time"

	"github.com/TyrantLucifer/awesome-mac-sftp/internal/platform"
)

const (
	defaultAttributeProbeTimeout = 10 * time.Second
	maxAttributeProbePathBytes   = 32 * 1024
	maxSFTPPacketBytes           = 256 * 1024
	attributeProbeRequestID      = 1

	sftpVersion3        = 3
	sftpTypeInit        = 1
	sftpTypeVersion     = 2
	sftpTypeStat        = 17
	sftpTypeStatus      = 101
	sftpTypeAttrs       = 105
	sftpAttrSize        = 0x00000001
	sftpAttrUIDGID      = 0x00000002
	sftpAttrPermissions = 0x00000004
	sftpAttrACModTime   = 0x00000008
	sftpAttrExtended    = 0x80000000
	sftpKnownAttrFlags  = sftpAttrSize | sftpAttrUIDGID | sftpAttrPermissions | sftpAttrACModTime | sftpAttrExtended
)

// SFTPAttributes preserves whether each security-relevant field was present in
// the SFTP v3 ATTRS packet. A nil pointer means the server omitted that field;
// it must never be interpreted as numeric zero.
type SFTPAttributes struct {
	Mode *uint32
	UID  *uint32
	GID  *uint32
}

// ProbeAttributes starts a fresh, isolated OpenSSH SFTP subprocess and performs
// exactly one raw SFTP v3 STAT. It intentionally does not reuse a pkg/sftp
// client because pkg/sftp's os.FileInfo projection loses UID/GID presence.
func ProbeAttributes(ctx context.Context, config Config, canonicalAbsoluteRawPath string) (SFTPAttributes, error) {
	if err := validateAttributeProbePath(canonicalAbsoluteRawPath); err != nil {
		return SFTPAttributes{}, err
	}
	binaryPath := config.Binary
	if binaryPath == "" {
		binaryPath = DefaultBinary
	}
	before, err := platform.ExecutableIdentity(binaryPath)
	if err != nil {
		return SFTPAttributes{}, fmt.Errorf("validate OpenSSH executable for attribute probe: %w", err)
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
		return SFTPAttributes{}, fmt.Errorf("open attribute probe stdin: %w", err)
	}
	stdout, err := command.StdoutPipe()
	if err != nil {
		return SFTPAttributes{}, fmt.Errorf("open attribute probe stdout: %w", err)
	}
	stderr, err := command.StderrPipe()
	if err != nil {
		return SFTPAttributes{}, fmt.Errorf("open attribute probe stderr: %w", err)
	}
	after, err := platform.ExecutableIdentity(binaryPath)
	if err != nil {
		return SFTPAttributes{}, fmt.Errorf("revalidate OpenSSH executable for attribute probe: %w", err)
	}
	if !platform.SameExecutableIdentity(before, after) {
		return SFTPAttributes{}, errors.New("OpenSSH executable changed before attribute probe start")
	}
	if err := command.Start(); err != nil {
		return SFTPAttributes{}, fmt.Errorf("start OpenSSH SFTP attribute probe: %w", err)
	}

	collector := &boundedBuffer{redactions: append([]string(nil), config.Redact...)}
	collectorDone := make(chan struct{})
	go func() {
		_, _ = io.Copy(collector, stderr)
		close(collectorDone)
	}()

	attributes, exchangeErr := exchangeSFTPAttributes(stdout, stdin, canonicalAbsoluteRawPath)
	_ = stdin.Close()
	if exchangeErr != nil {
		cancel()
	}
	waitErr := command.Wait()
	<-collectorDone
	if err := probeContext.Err(); err != nil {
		return SFTPAttributes{}, attributeProbeError(err, collector.String())
	}
	if exchangeErr != nil {
		return SFTPAttributes{}, attributeProbeError(exchangeErr, collector.String())
	}
	if waitErr != nil {
		return SFTPAttributes{}, attributeProbeError(fmt.Errorf("OpenSSH attribute probe exited: %w", waitErr), collector.String())
	}
	return attributes, nil
}

func validateAttributeProbePath(value string) error {
	if value == "" || len(value) > maxAttributeProbePathBytes || !path.IsAbs(value) || path.Clean(value) != value || bytes.IndexByte([]byte(value), 0) >= 0 {
		return errors.New("SFTP attribute probe path is not a canonical absolute raw path")
	}
	return nil
}

func exchangeSFTPAttributes(reader io.Reader, writer io.Writer, rawPath string) (SFTPAttributes, error) {
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
	if err := writeSFTPPacket(writer, sftpStatPacket(attributeProbeRequestID, rawPath)); err != nil {
		return SFTPAttributes{}, fmt.Errorf("send SFTP STAT: %w", err)
	}
	response, err := readSFTPPacket(reader)
	if err != nil {
		return SFTPAttributes{}, fmt.Errorf("read SFTP STAT response: %w", err)
	}
	return parseSFTPAttributes(response)
}

func sftpInitPacket() []byte {
	return []byte{sftpTypeInit, 0, 0, 0, sftpVersion3}
}

func sftpStatPacket(requestID uint32, rawPath string) []byte {
	if len(rawPath) > maxAttributeProbePathBytes {
		panic("SFTP attribute probe path exceeds protocol limit")
	}
	packet := make([]byte, 1+4+4+len(rawPath))
	packet[0] = sftpTypeStat
	binary.BigEndian.PutUint32(packet[1:5], requestID)
	// #nosec G115 -- the explicit path limit above is smaller than uint32.
	binary.BigEndian.PutUint32(packet[5:9], uint32(len(rawPath)))
	copy(packet[9:], rawPath)
	return packet
}

func writeSFTPPacket(writer io.Writer, payload []byte) error {
	if len(payload) == 0 || len(payload) > maxSFTPPacketBytes {
		return errors.New("invalid SFTP packet length")
	}
	var header [4]byte
	// #nosec G115 -- maxSFTPPacketBytes is smaller than uint32.
	binary.BigEndian.PutUint32(header[:], uint32(len(payload)))
	if err := writeAll(writer, header[:]); err != nil {
		return err
	}
	return writeAll(writer, payload)
}

func writeAll(writer io.Writer, value []byte) error {
	for len(value) > 0 {
		written, err := writer.Write(value)
		if err != nil {
			return err
		}
		if written <= 0 || written > len(value) {
			return io.ErrShortWrite
		}
		value = value[written:]
	}
	return nil
}

func readSFTPPacket(reader io.Reader) ([]byte, error) {
	var header [4]byte
	if _, err := io.ReadFull(reader, header[:]); err != nil {
		return nil, err
	}
	length := binary.BigEndian.Uint32(header[:])
	if length == 0 || length > maxSFTPPacketBytes {
		return nil, fmt.Errorf("invalid SFTP packet length %d", length)
	}
	payload := make([]byte, int(length))
	if _, err := io.ReadFull(reader, payload); err != nil {
		return nil, err
	}
	return payload, nil
}

func parseSFTPVersion(payload []byte) error {
	decoder := sftpDecoder{value: payload}
	typeCode, err := decoder.byte()
	if err != nil || typeCode != sftpTypeVersion {
		return errors.New("SFTP attribute probe received an invalid VERSION packet")
	}
	version, err := decoder.uint32()
	if err != nil || version != sftpVersion3 {
		return errors.New("SFTP attribute probe requires protocol version 3")
	}
	for decoder.remaining() > 0 {
		if err := decoder.skipString(); err != nil {
			return errors.New("SFTP attribute probe received malformed VERSION extensions")
		}
		if err := decoder.skipString(); err != nil {
			return errors.New("SFTP attribute probe received malformed VERSION extensions")
		}
	}
	return nil
}

func parseSFTPAttributes(payload []byte) (SFTPAttributes, error) {
	decoder := sftpDecoder{value: payload}
	typeCode, err := decoder.byte()
	if err != nil {
		return SFTPAttributes{}, errors.New("SFTP STAT response is empty")
	}
	if typeCode == sftpTypeStatus {
		return SFTPAttributes{}, parseSFTPStatus(&decoder, attributeProbeRequestID)
	}
	if typeCode != sftpTypeAttrs {
		return SFTPAttributes{}, fmt.Errorf("unexpected SFTP STAT response type %d", typeCode)
	}
	responseID, err := decoder.uint32()
	if err != nil || responseID != attributeProbeRequestID {
		return SFTPAttributes{}, errors.New("SFTP ATTRS response request id mismatch")
	}
	flags, err := decoder.uint32()
	if err != nil {
		return SFTPAttributes{}, errors.New("SFTP ATTRS response is truncated before flags")
	}
	if flags & ^uint32(sftpKnownAttrFlags) != 0 {
		return SFTPAttributes{}, fmt.Errorf("SFTP ATTRS response contains unsupported flags 0x%08x", flags)
	}
	var attributes SFTPAttributes
	if flags&sftpAttrSize != 0 {
		if _, err := decoder.uint64(); err != nil {
			return SFTPAttributes{}, errors.New("SFTP ATTRS size is truncated")
		}
	}
	if flags&sftpAttrUIDGID != 0 {
		uid, uidErr := decoder.uint32()
		gid, gidErr := decoder.uint32()
		if uidErr != nil || gidErr != nil {
			return SFTPAttributes{}, errors.New("SFTP ATTRS UID/GID is truncated")
		}
		attributes.UID = uint32Pointer(uid)
		attributes.GID = uint32Pointer(gid)
	}
	if flags&sftpAttrPermissions != 0 {
		mode, err := decoder.uint32()
		if err != nil {
			return SFTPAttributes{}, errors.New("SFTP ATTRS permissions are truncated")
		}
		attributes.Mode = uint32Pointer(mode)
	}
	if flags&sftpAttrACModTime != 0 {
		if _, err := decoder.uint32(); err != nil {
			return SFTPAttributes{}, errors.New("SFTP ATTRS access time is truncated")
		}
		if _, err := decoder.uint32(); err != nil {
			return SFTPAttributes{}, errors.New("SFTP ATTRS modification time is truncated")
		}
	}
	if flags&sftpAttrExtended != 0 {
		count, err := decoder.uint32()
		remaining := decoder.remaining()
		// #nosec G115 -- remaining is checked non-negative before conversion.
		if err != nil || remaining < 0 || uint64(count)*8 > uint64(remaining) {
			return SFTPAttributes{}, errors.New("SFTP ATTRS extensions are malformed")
		}
		for index := uint32(0); index < count; index++ {
			if err := decoder.skipString(); err != nil {
				return SFTPAttributes{}, errors.New("SFTP ATTRS extension type is malformed")
			}
			if err := decoder.skipString(); err != nil {
				return SFTPAttributes{}, errors.New("SFTP ATTRS extension data is malformed")
			}
		}
	}
	if decoder.remaining() != 0 {
		return SFTPAttributes{}, errors.New("SFTP ATTRS response has trailing bytes")
	}
	return attributes, nil
}

func parseSFTPStatus(decoder *sftpDecoder, requestID uint32) error {
	responseID, err := decoder.uint32()
	if err != nil || responseID != requestID {
		return errors.New("SFTP STATUS response request id mismatch")
	}
	code, err := decoder.uint32()
	if err != nil {
		return errors.New("SFTP STATUS response is truncated")
	}
	if err := decoder.skipString(); err != nil {
		return errors.New("SFTP STATUS message is malformed")
	}
	if err := decoder.skipString(); err != nil {
		return errors.New("SFTP STATUS language is malformed")
	}
	if decoder.remaining() != 0 {
		return errors.New("SFTP STATUS response has trailing bytes")
	}
	return fmt.Errorf("SFTP STAT failed with status code %d", code)
}

func uint32Pointer(value uint32) *uint32 {
	copy := value
	return &copy
}

func attributeProbeError(err error, diagnostic string) error {
	if diagnostic == "" {
		return fmt.Errorf("SFTP attribute probe: %w", err)
	}
	return fmt.Errorf("SFTP attribute probe: %w: %s", err, diagnostic)
}

type sftpDecoder struct {
	value  []byte
	offset int
}

func (d *sftpDecoder) remaining() int {
	return len(d.value) - d.offset
}

func (d *sftpDecoder) byte() (byte, error) {
	if d.remaining() < 1 {
		return 0, io.ErrUnexpectedEOF
	}
	value := d.value[d.offset]
	d.offset++
	return value, nil
}

func (d *sftpDecoder) uint32() (uint32, error) {
	if d.remaining() < 4 {
		return 0, io.ErrUnexpectedEOF
	}
	value := binary.BigEndian.Uint32(d.value[d.offset : d.offset+4])
	d.offset += 4
	return value, nil
}

func (d *sftpDecoder) uint64() (uint64, error) {
	if d.remaining() < 8 {
		return 0, io.ErrUnexpectedEOF
	}
	value := binary.BigEndian.Uint64(d.value[d.offset : d.offset+8])
	d.offset += 8
	return value, nil
}

func (d *sftpDecoder) skipString() error {
	length, err := d.uint32()
	remaining := d.remaining()
	// #nosec G115 -- remaining is checked non-negative before conversion.
	if err != nil || remaining < 0 || uint64(length) > uint64(remaining) {
		return io.ErrUnexpectedEOF
	}
	// #nosec G115 -- length is bounded by the remaining in-memory slice.
	d.offset += int(length)
	return nil
}
