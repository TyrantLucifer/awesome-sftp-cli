package openssh

import (
	"bytes"
	"context"
	"encoding/binary"
	"testing"
)

func TestExchangeSFTPMkdirCarriesExact0700InCreatePacket(t *testing.T) {
	responses := append(framedSFTPPacket([]byte{sftpTypeVersion, 0, 0, 0, sftpVersion3}), framedSFTPPacket(sftpStatusPacketForTest(attributeProbeRequestID, 0))...)
	var requests bytes.Buffer
	if err := exchangeSFTPMkdirExact(bytes.NewReader(responses), &requests, "/home/alice/.local/lib/amsftp", 0o700); err != nil {
		t.Fatal(err)
	}
	reader := bytes.NewReader(requests.Bytes())
	if _, err := readSFTPPacket(reader); err != nil {
		t.Fatal(err)
	}
	mkdir, err := readSFTPPacket(reader)
	if err != nil {
		t.Fatal(err)
	}
	decoder := sftpDecoder{value: mkdir}
	typeCode, _ := decoder.byte()
	requestID, _ := decoder.uint32()
	pathLength, _ := decoder.uint32()
	pathBytes := make([]byte, pathLength)
	copy(pathBytes, mkdir[decoder.offset:decoder.offset+int(pathLength)])
	decoder.offset += int(pathLength)
	flags, _ := decoder.uint32()
	mode, _ := decoder.uint32()
	if typeCode != sftpTypeMkdir || requestID != attributeProbeRequestID || string(pathBytes) != "/home/alice/.local/lib/amsftp" || flags != sftpAttrPermissions || mode != 0o700 || decoder.remaining() != 0 {
		t.Fatalf("mkdir packet = type %d id %d path %q flags %#x mode %#o trailing %d", typeCode, requestID, pathBytes, flags, mode, decoder.remaining())
	}
}

func TestSFTPMkdirExactRejectsUnsafeInputAndNonzeroStatus(t *testing.T) {
	for _, test := range []struct {
		path string
		mode uint32
	}{
		{"relative", 0o700},
		{"/unclean/../path", 0o700},
		{"/safe", 0o755},
	} {
		if err := MkdirExact(context.Background(), Config{HostAlias: "work"}, test.path, test.mode); err == nil {
			t.Fatalf("unsafe mkdir %#v was accepted", test)
		}
	}
	responses := append(framedSFTPPacket([]byte{sftpTypeVersion, 0, 0, 0, sftpVersion3}), framedSFTPPacket(sftpStatusPacketForTest(attributeProbeRequestID, 4))...)
	if err := exchangeSFTPMkdirExact(bytes.NewReader(responses), &bytes.Buffer{}, "/safe", 0o700); err == nil {
		t.Fatal("nonzero SFTP MKDIR status was accepted")
	}
}

func framedSFTPPacket(payload []byte) []byte {
	if len(payload) > maxSFTPPacketBytes {
		panic("test SFTP packet exceeds protocol limit")
	}
	result := make([]byte, 4+len(payload))
	// #nosec G115 -- the explicit protocol limit above is smaller than uint32.
	binary.BigEndian.PutUint32(result[:4], uint32(len(payload)))
	copy(result[4:], payload)
	return result
}

func sftpStatusPacketForTest(requestID, code uint32) []byte {
	result := make([]byte, 1+4+4+4+4)
	result[0] = sftpTypeStatus
	binary.BigEndian.PutUint32(result[1:5], requestID)
	binary.BigEndian.PutUint32(result[5:9], code)
	return result
}
