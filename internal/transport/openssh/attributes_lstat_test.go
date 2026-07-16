package openssh

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

func TestProbeLinkAttributesUsesRawSFTPLstatAndPreservesPresence(t *testing.T) {
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	binary := filepath.Join(t.TempDir(), "ssh")
	script := "#!/bin/sh\nexec \"$AMSFTP_LSTAT_TEST_BINARY\" -test.run=^TestLinkAttributeProbeHelperProcess$ -- \"$@\"\n"
	if err := os.WriteFile(binary, []byte(script), 0o700); err != nil {
		t.Fatal(err)
	}
	config := Config{
		Binary:      binary,
		HostAlias:   "test-host",
		Environment: append(os.Environ(), "AMSFTP_LSTAT_TEST_BINARY="+executable),
	}
	attributes, err := ProbeLinkAttributes(context.Background(), config, "/usr/bin/printf")
	if err != nil {
		t.Fatal(err)
	}
	if attributes.UID == nil || *attributes.UID != 0 || attributes.GID == nil || *attributes.GID != 0 || attributes.Mode == nil || *attributes.Mode != 0o120777 {
		t.Fatalf("attributes = %#v", attributes)
	}
}

func TestLinkAttributeProbeHelperProcess(t *testing.T) {
	if os.Getenv("AMSFTP_LSTAT_TEST_BINARY") == "" {
		return
	}
	wantArguments, err := Arguments("test-host")
	if err != nil || !reflect.DeepEqual(argumentsAfterDoubleDash(os.Args), wantArguments) {
		os.Exit(80)
	}
	initPacket, err := readSFTPPacket(os.Stdin)
	if err != nil || !bytes.Equal(initPacket, sftpInitPacket()) {
		os.Exit(81)
	}
	if err := writeSFTPPacket(os.Stdout, []byte{sftpTypeVersion, 0, 0, 0, sftpVersion3}); err != nil {
		os.Exit(82)
	}
	request, err := readSFTPPacket(os.Stdin)
	if err != nil || !bytes.Equal(request, sftpLstatPacket(attributeProbeRequestID, "/usr/bin/printf")) {
		os.Exit(83)
	}
	if err := writeSFTPPacket(os.Stdout, attrsPayload(attributeProbeRequestID, sftpAttrUIDGID|sftpAttrPermissions, 0, 0, 0o120777)); err != nil {
		os.Exit(84)
	}
	os.Exit(0)
}
