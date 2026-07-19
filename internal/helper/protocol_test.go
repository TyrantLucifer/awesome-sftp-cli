package helper

import (
	"bytes"
	"encoding/binary"
	"encoding/json"
	"strings"
	"testing"

	"github.com/TyrantLucifer/awesome-sftp-cli/internal/ipc"
)

func TestHelperHandshakeStartsAtByteZeroAndNegotiatesIndependentCapabilities(t *testing.T) {
	client := ClientHello{
		MinimumProtocol:   1,
		MaximumProtocol:   1,
		MaximumFrame:      MaxHelperFrameBytes,
		MaximumConcurrent: 2,
		ClientVersion:     Version{Major: 4},
		Capabilities: []CapabilityRequest{
			{Name: CapabilityFilenameSearch, MaximumVersion: 1},
			{Name: CapabilityStrongHash, MaximumVersion: 1},
			{Name: CapabilityWatch, MaximumVersion: 1},
		},
	}
	server := ServerConfig{
		Protocol:          1,
		HelperVersion:     Version{Major: 4},
		MaximumFrame:      MaxHelperFrameBytes,
		MaximumConcurrent: 4,
		Capabilities: []Capability{
			{Name: CapabilityFilenameSearch, Version: 1},
			{Name: CapabilityStrongHash, Version: 1},
			{Name: CapabilityDiskStats, Version: 1},
		},
	}
	input := framedEnvelope(t, Envelope{Version: 1, Type: FrameClientHello}, client)
	var output bytes.Buffer
	negotiated, err := ServeHandshake(bytes.NewReader(input), &output, server)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.HasPrefix(output.Bytes(), []byte(HelperPreface)) || output.String()[:len(HelperPreface)] != HelperPreface {
		t.Fatalf("stdout does not start at byte zero with preface: %q", output.Bytes())
	}
	if negotiated.Protocol != 1 || negotiated.MaximumConcurrent != 2 || len(negotiated.Capabilities) != 2 || negotiated.Capabilities[0].Name != CapabilityFilenameSearch || negotiated.Capabilities[1].Name != CapabilityStrongHash {
		t.Fatalf("negotiated = %#v", negotiated)
	}
	responseFrame := output.Bytes()[len(HelperPreface):]
	reader, err := ipc.NewReader(bytes.NewReader(responseFrame), MaxHelperFrameBytes)
	if err != nil {
		t.Fatal(err)
	}
	payload, err := reader.ReadFrame()
	if err != nil {
		t.Fatal(err)
	}
	envelope, err := DecodeHelperEnvelope(payload)
	if err != nil || envelope.Type != FrameServerHello {
		t.Fatalf("server envelope = %#v, %v", envelope, err)
	}
}

func TestHelperEnvelopeAndPayloadRejectMalformedOversizeUnknownAndDeepInput(t *testing.T) {
	valid := []byte(`{"v":1,"type":"ping","payload":{"nonce":"0123456789abcdef"}}`)
	if _, err := DecodeHelperEnvelope(valid); err != nil {
		t.Fatalf("valid envelope: %v", err)
	}
	tests := [][]byte{
		[]byte(`{"v":1,"type":"ping","payload":{},"unknown":true}`),
		[]byte(`{"v":1,"type":"ping","payload":{}} trailing`),
		[]byte(`[]`),
		[]byte(`{"v":2,"type":"ping","payload":{}}`),
		[]byte(`{"v":1,"type":"unknown","payload":{}}`),
		[]byte(`{"v":1,"type":"ping","payload":{"x":[[[[[[[[[0]]]]]]]]]}}`),
	}
	for index, raw := range tests {
		if _, err := DecodeHelperEnvelope(raw); err == nil {
			t.Fatalf("mutation %d succeeded", index)
		}
	}
	oversizeHeader := make([]byte, 4)
	binary.BigEndian.PutUint32(oversizeHeader, MaxHelperFrameBytes+1)
	reader, err := ipc.NewReader(bytes.NewReader(oversizeHeader), MaxHelperFrameBytes)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := reader.ReadFrame(); err == nil {
		t.Fatal("oversize frame succeeded")
	}
	unknownPayload := Envelope{Version: 1, Type: FrameClientHello, Payload: json.RawMessage(`{"minimum_protocol":1,"maximum_protocol":1,"maximum_frame":1024,"maximum_concurrent":1,"client_version":"4.0.0","capabilities":[],"secret":"x"}`)}
	if _, err := ParseClientHello(unknownPayload); err == nil {
		t.Fatal("unknown handshake field succeeded")
	}
}

func TestHelperHandshakeRejectsBannerProtocolMismatchAndCapabilityViolation(t *testing.T) {
	if err := ValidateHelperPreface(strings.NewReader("banner" + HelperPreface)); err == nil {
		t.Fatal("banner before byte-zero preface succeeded")
	}
	client := ClientHello{MinimumProtocol: 2, MaximumProtocol: 2, MaximumFrame: 1024, MaximumConcurrent: 1, ClientVersion: Version{Major: 4}}
	input := framedEnvelope(t, Envelope{Version: 1, Type: FrameClientHello}, client)
	if _, err := ServeHandshake(bytes.NewReader(input), &bytes.Buffer{}, ServerConfig{Protocol: 1, HelperVersion: Version{Major: 4}, MaximumFrame: 1024, MaximumConcurrent: 1}); err == nil {
		t.Fatal("protocol mismatch succeeded")
	}
	duplicate := client
	duplicate.MinimumProtocol = 1
	duplicate.MaximumProtocol = 1
	duplicate.Capabilities = []CapabilityRequest{{Name: CapabilityStrongHash, MaximumVersion: 1}, {Name: CapabilityStrongHash, MaximumVersion: 1}}
	input = framedEnvelope(t, Envelope{Version: 1, Type: FrameClientHello}, duplicate)
	if _, err := ServeHandshake(bytes.NewReader(input), &bytes.Buffer{}, ServerConfig{Protocol: 1, HelperVersion: Version{Major: 4}, MaximumFrame: 1024, MaximumConcurrent: 1}); err == nil {
		t.Fatal("duplicate capability succeeded")
	}
}

func framedEnvelope(t *testing.T, envelope Envelope, payload any) []byte {
	t.Helper()
	rawPayload, err := json.Marshal(payload)
	if err != nil {
		t.Fatal(err)
	}
	envelope.Payload = rawPayload
	raw, err := EncodeHelperEnvelope(envelope)
	if err != nil {
		t.Fatal(err)
	}
	var wire bytes.Buffer
	writer, err := ipc.NewWriter(&wire, MaxHelperFrameBytes)
	if err != nil {
		t.Fatal(err)
	}
	if err := writer.WriteFrame(raw); err != nil {
		t.Fatal(err)
	}
	return wire.Bytes()
}
