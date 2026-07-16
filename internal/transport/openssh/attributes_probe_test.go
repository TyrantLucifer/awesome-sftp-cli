package openssh

import (
	"bytes"
	"context"
	"encoding/binary"
	"errors"
	"io"
	"math"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/TyrantLucifer/awesome-mac-sftp/internal/platform"
)

func TestAttributeProbePacketsAreExactSFTPVersion3(t *testing.T) {
	path := "/tmp/name with 'quotes'\nand $metachar"
	var output bytes.Buffer
	if err := writeSFTPPacket(&output, sftpInitPacket()); err != nil {
		t.Fatal(err)
	}
	if err := writeSFTPPacket(&output, sftpStatPacket(attributeProbeRequestID, path)); err != nil {
		t.Fatal(err)
	}

	packetLength := 1 + 4 + 4 + len(path)
	if packetLength > math.MaxUint8 || len(path) > math.MaxUint8 {
		t.Fatal("test path no longer fits the single-byte packet fixture")
	}
	// #nosec G115 -- both fixture lengths are bounded by MaxUint8 above.
	want := append(
		[]byte{0, 0, 0, 5, sftpTypeInit, 0, 0, 0, sftpVersion3},
		append([]byte{0, 0, 0, byte(packetLength), sftpTypeStat, 0, 0, 0, attributeProbeRequestID, 0, 0, 0, byte(len(path))}, []byte(path)...)...,
	)
	if !bytes.Equal(output.Bytes(), want) {
		t.Fatalf("wire packets = %x, want %x", output.Bytes(), want)
	}
}

func TestParseSFTPAttributesPreservesProtocolPresence(t *testing.T) {
	t.Run("uid gid and mode present even when numeric values are zero", func(t *testing.T) {
		payload := attrsPayload(attributeProbeRequestID, sftpAttrUIDGID|sftpAttrPermissions, 0, 0, 0)
		attributes, err := parseSFTPAttributes(payload)
		if err != nil {
			t.Fatal(err)
		}
		if attributes.UID == nil || *attributes.UID != 0 || attributes.GID == nil || *attributes.GID != 0 || attributes.Mode == nil || *attributes.Mode != 0 {
			t.Fatalf("attributes = %#v", attributes)
		}
	})

	t.Run("missing uid gid remains nil", func(t *testing.T) {
		payload := attrsPayload(attributeProbeRequestID, sftpAttrPermissions, 0, 0, 0o100755)
		attributes, err := parseSFTPAttributes(payload)
		if err != nil {
			t.Fatal(err)
		}
		if attributes.UID != nil || attributes.GID != nil || attributes.Mode == nil || *attributes.Mode != 0o100755 {
			t.Fatalf("attributes = %#v", attributes)
		}
	})

	t.Run("all version 3 fields parse without changing presence", func(t *testing.T) {
		payload := attrsPayload(attributeProbeRequestID, sftpAttrSize|sftpAttrUIDGID|sftpAttrPermissions|sftpAttrACModTime|sftpAttrExtended, 501, 20, 0o40750)
		attributes, err := parseSFTPAttributes(payload)
		if err != nil {
			t.Fatal(err)
		}
		if attributes.UID == nil || *attributes.UID != 501 || attributes.GID == nil || *attributes.GID != 20 || attributes.Mode == nil || *attributes.Mode != 0o40750 {
			t.Fatalf("attributes = %#v", attributes)
		}
	})
}

func TestParseSFTPVersionRequiresExactVersion3Packet(t *testing.T) {
	validWithExtension := append([]byte{sftpTypeVersion, 0, 0, 0, sftpVersion3}, []byte{0, 0, 0, 3, 'p', 'o', 's', 0, 0, 0, 1, '1'}...)
	if err := parseSFTPVersion(validWithExtension); err != nil {
		t.Fatalf("valid version packet: %v", err)
	}
	for name, payload := range map[string][]byte{
		"empty":               nil,
		"wrong type":          {sftpTypeInit, 0, 0, 0, sftpVersion3},
		"wrong version":       {sftpTypeVersion, 0, 0, 0, 4},
		"truncated extension": append([]byte(nil), validWithExtension[:len(validWithExtension)-1]...),
	} {
		t.Run(name, func(t *testing.T) {
			if err := parseSFTPVersion(payload); err == nil {
				t.Fatalf("parseSFTPVersion(%x) error = nil", payload)
			}
		})
	}
}

func TestParseSFTPAttributesFailsClosed(t *testing.T) {
	valid := attrsPayload(attributeProbeRequestID, sftpAttrUIDGID|sftpAttrPermissions, 0, 0, 0o100755)
	status := sftpStatusPayload(attributeProbeRequestID, 2, "not found")
	tests := map[string][]byte{
		"empty":               nil,
		"wrong response type": append([]byte(nil), valid[1:]...),
		"wrong request id":    attrsPayload(attributeProbeRequestID+1, sftpAttrPermissions, 0, 0, 0o100755),
		"unknown flags":       attrsPayload(attributeProbeRequestID, 0x10, 0, 0, 0),
		"truncated":           valid[:len(valid)-1],
		"trailing":            append(append([]byte(nil), valid...), 0),
		"status":              status,
	}
	for name, payload := range tests {
		t.Run(name, func(t *testing.T) {
			if _, err := parseSFTPAttributes(payload); err == nil {
				t.Fatalf("parseSFTPAttributes(%x) error = nil", payload)
			}
		})
	}
}

func TestReadSFTPPacketRejectsBannerAndOversize(t *testing.T) {
	for name, input := range map[string][]byte{
		"banner":   []byte("SSH banner before protocol\n"),
		"oversize": {0, 4, 0, 1},
		"empty":    {0, 0, 0, 0},
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := readSFTPPacket(bytes.NewReader(input)); err == nil {
				t.Fatalf("readSFTPPacket(%x) error = nil", input)
			}
		})
	}
}

func TestProbeAttributesUsesFreshValidatedSFTPSubprocess(t *testing.T) {
	for _, test := range []struct {
		name       string
		flags      uint32
		wantUIDGID bool
	}{
		{name: "present", flags: sftpAttrUIDGID | sftpAttrPermissions, wantUIDGID: true},
		{name: "absent", flags: sftpAttrPermissions, wantUIDGID: false},
	} {
		t.Run(test.name, func(t *testing.T) {
			config := attributeProbeHelperConfig(t, map[string]string{
				"AMSFTP_TEST_ATTR_FLAGS":  decimalUint32(test.flags),
				"AMSFTP_TEST_ATTR_UID":    "0",
				"AMSFTP_TEST_ATTR_GID":    "0",
				"AMSFTP_TEST_ATTR_MODE":   decimalUint32(0o100755),
				"AMSFTP_TEST_ATTR_PATH":   "/usr/bin/printf",
				"AMSFTP_TEST_ATTR_HELPER": "respond",
			})

			attributes, err := ProbeAttributes(context.Background(), config, "/usr/bin/printf")
			if err != nil {
				t.Fatal(err)
			}
			if attributes.Mode == nil || *attributes.Mode != 0o100755 {
				t.Fatalf("mode = %#v", attributes.Mode)
			}
			if test.wantUIDGID {
				if attributes.UID == nil || *attributes.UID != 0 || attributes.GID == nil || *attributes.GID != 0 {
					t.Fatalf("UID/GID = %#v/%#v", attributes.UID, attributes.GID)
				}
			} else if attributes.UID != nil || attributes.GID != nil {
				t.Fatalf("absent UID/GID decoded as %#v/%#v", attributes.UID, attributes.GID)
			}
		})
	}
}

func TestProbeAttributesFailsClosedOnMalformedStatusTimeoutAndCancel(t *testing.T) {
	tests := []struct {
		name    string
		mode    string
		timeout bool
		cancel  bool
	}{
		{name: "malformed", mode: "malformed"},
		{name: "status", mode: "status"},
		{name: "banner", mode: "banner"},
		{name: "oversize", mode: "oversize"},
		{name: "timeout", mode: "hang", timeout: true},
		{name: "cancel", mode: "hang", cancel: true},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			config := attributeProbeHelperConfig(t, map[string]string{
				"AMSFTP_TEST_ATTR_HELPER": test.mode,
				"AMSFTP_TEST_ATTR_PATH":   "/usr/bin/printf",
			})
			ctx := context.Background()
			if test.timeout {
				var cancel context.CancelFunc
				ctx, cancel = context.WithTimeout(ctx, 200*time.Millisecond)
				defer cancel()
			} else if test.cancel {
				var cancel context.CancelFunc
				ctx, cancel = context.WithCancel(ctx)
				cancel()
			}
			if _, err := ProbeAttributes(ctx, config, "/usr/bin/printf"); err == nil {
				t.Fatal("ProbeAttributes error = nil")
			}
		})
	}
}

func TestProbeAttributesRejectsNonCanonicalPathBeforeStartingProcess(t *testing.T) {
	marker := filepath.Join(t.TempDir(), "started")
	config := attributeProbeHelperConfig(t, map[string]string{
		"AMSFTP_TEST_ATTR_HELPER": "respond",
		"AMSFTP_TEST_STARTED":     marker,
	})
	for _, invalid := range []string{"", "relative", "/usr/../bin/printf", "/usr/bin/printf\x00suffix"} {
		if _, err := ProbeAttributes(context.Background(), config, invalid); err == nil {
			t.Fatalf("ProbeAttributes(%q) error = nil", invalid)
		}
	}
	if _, err := os.Stat(marker); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("helper marker stat error = %v", err)
	}
}

func TestAttributeProbeHelperProcess(t *testing.T) {
	mode := os.Getenv("AMSFTP_TEST_ATTR_HELPER")
	if mode == "" {
		return
	}
	if marker := os.Getenv("AMSFTP_TEST_STARTED"); marker != "" {
		// #nosec G703 -- the parent test supplies a path in its private temporary directory.
		_ = os.WriteFile(marker, []byte("started"), 0o600)
	}
	if diagnostic := os.Getenv("AMSFTP_TEST_ATTR_STDERR"); diagnostic != "" {
		_, _ = io.WriteString(os.Stderr, diagnostic+"\n")
	}
	arguments := argumentsAfterDoubleDash(os.Args)
	wantArguments, err := Arguments("test-host")
	if err != nil || !reflect.DeepEqual(arguments, wantArguments) {
		os.Exit(80)
	}
	if mode == "hang" {
		select {}
	}
	initPacket, err := readSFTPPacket(os.Stdin)
	if err != nil || !bytes.Equal(initPacket, sftpInitPacket()) {
		os.Exit(81)
	}
	if mode == "banner" {
		_, _ = os.Stdout.Write([]byte("banner\n"))
		os.Exit(0)
	}
	if mode == "oversize" {
		_, _ = os.Stdout.Write([]byte{0, 4, 0, 1})
		os.Exit(0)
	}
	if err := writeSFTPPacket(os.Stdout, []byte{sftpTypeVersion, 0, 0, 0, sftpVersion3}); err != nil {
		os.Exit(82)
	}
	statPacket, err := readSFTPPacket(os.Stdin)
	if err != nil || !bytes.Equal(statPacket, sftpStatPacket(attributeProbeRequestID, os.Getenv("AMSFTP_TEST_ATTR_PATH"))) {
		os.Exit(83)
	}
	switch mode {
	case "respond":
		flags := parseDecimalUint32(os.Getenv("AMSFTP_TEST_ATTR_FLAGS"))
		uid := parseDecimalUint32(os.Getenv("AMSFTP_TEST_ATTR_UID"))
		gid := parseDecimalUint32(os.Getenv("AMSFTP_TEST_ATTR_GID"))
		permissions := parseDecimalUint32(os.Getenv("AMSFTP_TEST_ATTR_MODE"))
		_ = writeSFTPPacket(os.Stdout, attrsPayload(attributeProbeRequestID, flags, uid, gid, permissions))
	case "status":
		_ = writeSFTPPacket(os.Stdout, sftpStatusPayload(attributeProbeRequestID, 3, "permission denied"))
	case "malformed":
		_ = writeSFTPPacket(os.Stdout, []byte{sftpTypeAttrs})
	default:
		os.Exit(84)
	}
	os.Exit(0)
}

func attributeProbeHelperConfig(t *testing.T, values map[string]string) Config {
	t.Helper()
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	workingDirectory, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	directory, err := os.MkdirTemp(workingDirectory, ".amsftp-attrs-helper-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(directory) })
	// #nosec G302 -- a traversable owner-only directory requires execute permission.
	if err := os.Chmod(directory, 0o700); err != nil {
		t.Fatal(err)
	}
	binary := filepath.Join(directory, "ssh")
	script := "#!/bin/sh\nexec \"$AMSFTP_TEST_BINARY\" -test.run=^TestAttributeProbeHelperProcess$ -- \"$@\"\n"
	// #nosec G306 -- this owner-only fixture must be executable.
	if err := os.WriteFile(binary, []byte(script), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := platform.ValidateExecutable(binary); err != nil {
		t.Skipf("trusted executable fixture unavailable: %v", err)
	}
	environment := append([]string(nil), os.Environ()...)
	environment = append(environment, "AMSFTP_TEST_BINARY="+executable)
	for key, value := range values {
		environment = append(environment, key+"="+value)
	}
	return Config{Binary: binary, HostAlias: "test-host", Environment: environment, Redact: []string{"attribute-probe-secret"}}
}

func attrsPayload(requestID, flags, uid, gid, permissions uint32) []byte {
	var payload bytes.Buffer
	payload.WriteByte(sftpTypeAttrs)
	writeUint32(&payload, requestID)
	writeUint32(&payload, flags)
	if flags&sftpAttrSize != 0 {
		_ = binary.Write(&payload, binary.BigEndian, uint64(123))
	}
	if flags&sftpAttrUIDGID != 0 {
		writeUint32(&payload, uid)
		writeUint32(&payload, gid)
	}
	if flags&sftpAttrPermissions != 0 {
		writeUint32(&payload, permissions)
	}
	if flags&sftpAttrACModTime != 0 {
		writeUint32(&payload, 1)
		writeUint32(&payload, 2)
	}
	if flags&sftpAttrExtended != 0 {
		writeUint32(&payload, 0)
	}
	return payload.Bytes()
}

func sftpStatusPayload(requestID, code uint32, message string) []byte {
	var payload bytes.Buffer
	payload.WriteByte(sftpTypeStatus)
	writeUint32(&payload, requestID)
	writeUint32(&payload, code)
	writeString(&payload, message)
	writeString(&payload, "en")
	return payload.Bytes()
}

func argumentsAfterDoubleDash(arguments []string) []string {
	for index, argument := range arguments {
		if argument == "--" {
			return append([]string(nil), arguments[index+1:]...)
		}
	}
	return nil
}

func decimalUint32(value uint32) string {
	const digits = "0123456789"
	if value == 0 {
		return "0"
	}
	var buffer [10]byte
	index := len(buffer)
	for value > 0 {
		index--
		buffer[index] = digits[value%10]
		value /= 10
	}
	return string(buffer[index:])
}

func parseDecimalUint32(value string) uint32 {
	var result uint32
	for _, digit := range []byte(value) {
		if digit < '0' || digit > '9' {
			return 0
		}
		result = result*10 + uint32(digit-'0')
	}
	return result
}

func writeUint32(destination io.Writer, value uint32) {
	var encoded [4]byte
	binary.BigEndian.PutUint32(encoded[:], value)
	_, _ = destination.Write(encoded[:])
}

func writeString(destination io.Writer, value string) {
	if len(value) > math.MaxUint32 {
		panic("test SFTP string exceeds uint32")
	}
	// #nosec G115 -- the explicit bound above guarantees uint32 representation.
	writeUint32(destination, uint32(len(value)))
	_, _ = io.WriteString(destination, value)
}

func TestProbeAttributesRedactsSubprocessDiagnostics(t *testing.T) {
	config := attributeProbeHelperConfig(t, map[string]string{
		"AMSFTP_TEST_ATTR_HELPER": "malformed",
		"AMSFTP_TEST_ATTR_PATH":   "/usr/bin/printf",
	})
	config.Environment = append(config.Environment, "AMSFTP_TEST_ATTR_STDERR=attribute-probe-secret")
	_, err := ProbeAttributes(context.Background(), config, "/usr/bin/printf")
	if err == nil {
		t.Fatal("ProbeAttributes error = nil")
	}
	if strings.Contains(err.Error(), "attribute-probe-secret") {
		t.Fatalf("error leaked redacted diagnostic: %v", err)
	}
	if !strings.Contains(err.Error(), "[redacted]") {
		t.Fatalf("error omitted redacted diagnostic: %v", err)
	}
}
