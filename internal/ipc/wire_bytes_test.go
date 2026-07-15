package ipc

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"
)

func TestWireBytesRoundTripInvalidUTF8(t *testing.T) {
	input := []byte{0xff, '/', 0x00, 0x80}
	encoded := EncodeWireBytes(input)
	got, err := encoded.Decode()
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, input) {
		t.Fatalf("got %x want %x", got, input)
	}
}

func TestWireBytesJSONContainsOnlyBase64Text(t *testing.T) {
	input := []byte{0xff, 0x00, 0x80}
	data, err := json.Marshal(EncodeWireBytes(input))
	if err != nil {
		t.Fatal(err)
	}

	var object map[string]json.RawMessage
	if err := json.Unmarshal(data, &object); err != nil {
		t.Fatal(err)
	}
	if len(object) != 1 || object["base64"] == nil {
		t.Fatalf("wire JSON = %s, want exactly one base64 field", data)
	}
	if bytes.Contains(data, input) {
		t.Fatalf("wire JSON contains raw path bytes: %x", data)
	}
}

func TestWireBytesDecodeRejectsInvalidBase64WithoutEchoingValue(t *testing.T) {
	const invalidBase64Text = "not-base64-sensitive-marker"
	_, err := (WireBytes{Base64: invalidBase64Text}).Decode()
	if err == nil {
		t.Fatal("Decode() error = nil, want invalid-base64 error")
	}
	if strings.Contains(err.Error(), invalidBase64Text) {
		t.Fatalf("Decode() error leaked wire value: %q", err)
	}
}
