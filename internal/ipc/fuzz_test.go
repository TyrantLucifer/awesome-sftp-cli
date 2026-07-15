package ipc

import (
	"bytes"
	"encoding/binary"
	"testing"
)

const fuzzInputLimit = 64 * 1024

func FuzzFrameDecoder(f *testing.F) {
	f.Add([]byte{0, 0, 0, 1, 'x'}, uint16(16))
	f.Add([]byte{0, 0, 0, 0}, uint16(16))
	f.Add([]byte{0, 0}, uint16(16))
	f.Add([]byte{0, 0, 0, 4, 'x'}, uint16(16))
	f.Add([]byte{0, 0, 1, 0}, uint16(8))

	f.Fuzz(func(t *testing.T, wire []byte, rawMaximum uint16) {
		if len(wire) > fuzzInputLimit {
			t.Skip()
		}
		maximum := uint32(rawMaximum%4096) + 1
		reader, err := NewReader(bytes.NewReader(wire), maximum)
		if err != nil {
			t.Fatalf("NewReader(): %v", err)
		}

		payload, err := reader.ReadFrame()
		if err != nil {
			return
		}
		if len(payload) == 0 || uint64(len(payload)) > uint64(maximum) {
			t.Fatalf("accepted payload length %d with maximum %d", len(payload), maximum)
		}
		if len(wire) < 4 || uint64(binary.BigEndian.Uint32(wire[:4])) != uint64(len(payload)) {
			t.Fatalf("accepted frame has inconsistent header: %x", wire)
		}
		if !bytes.Equal(payload, wire[4:4+len(payload)]) {
			t.Fatal("accepted frame payload differs from wire bytes")
		}
	})
}

func FuzzEnvelopeDecode(f *testing.F) {
	f.Add([]byte(`{"protocol":{"major":1,"minor":0},"kind":"request","name":"stat","request_id":"req_aaaaaaaaaaaaaaaaaaaaaaaaaa"}`))
	f.Add([]byte(`{"protocol":{"major":1,"minor":9,"future":true},"kind":"event","name":"changed","cursor":{"epoch":"e","sequence":1},"future":{}}`))
	f.Add([]byte(`{"protocol":{"major":1,"minor":0},"kind":"event","name":"changed"}`))
	f.Add([]byte(`{"protocol":{"major":1,"minor":0},"kind":"response","name":"stat","request_id":"req_aaaaaaaaaaaaaaaaaaaaaaaaaa","payload":{},"error":{"code":"internal"}}`))
	f.Add([]byte(`[]`))
	f.Add([]byte(`{} {}`))

	f.Fuzz(func(t *testing.T, data []byte) {
		if len(data) > fuzzInputLimit {
			t.Skip()
		}
		envelope, err := DecodeEnvelope(data)
		if err != nil {
			return
		}
		if err := envelope.Validate(); err != nil {
			t.Fatalf("DecodeEnvelope accepted invalid envelope: %v", err)
		}
		encoded, err := EncodeEnvelope(envelope)
		if err != nil {
			t.Fatalf("EncodeEnvelope(decoded): %v", err)
		}
		if _, err := DecodeEnvelope(encoded); err != nil {
			t.Fatalf("DecodeEnvelope(encoded): %v", err)
		}
	})
}

func FuzzWireBytes(f *testing.F) {
	f.Add([]byte{})
	f.Add([]byte{0xff, '/', 0x00, 0x80})
	f.Add([]byte("ordinary UTF-8"))
	f.Add([]byte("%%%"))
	f.Add([]byte("Lw=="))

	f.Fuzz(func(t *testing.T, value []byte) {
		if len(value) > fuzzInputLimit {
			t.Skip()
		}
		wire := EncodeWireBytes(value)
		decoded, err := wire.Decode()
		if err != nil {
			t.Fatalf("Decode(Encode(value)): %v", err)
		}
		if !bytes.Equal(decoded, value) {
			t.Fatalf("round trip changed bytes: got %x want %x", decoded, value)
		}

		decoded, err = (WireBytes{Base64: string(value)}).Decode()
		if err != nil {
			return
		}
		roundTrip, err := EncodeWireBytes(decoded).Decode()
		if err != nil {
			t.Fatalf("Decode(re-encoded base64): %v", err)
		}
		if !bytes.Equal(roundTrip, decoded) {
			t.Fatal("valid base64 changed after canonical re-encoding")
		}
	})
}
