package helper

import (
	"encoding/base64"
	"testing"
)

func FuzzDecodeHelperEnvelopeNeverPanicsOrAcceptsInvalidRoundTrip(f *testing.F) {
	seeds := [][]byte{
		[]byte(`{"v":1,"type":"ping","payload":{"nonce":"0000000000000000"}}`),
		[]byte(`{"v":1,"type":"result","request_id":"req_aaaaaaaaaaaaaaaaaaaaaaaaaa","payload":{}}`),
		[]byte(`{"v":1,"type":"unknown","payload":{}}`),
		{0xff, 0x00, '{'},
	}
	for _, seed := range seeds {
		f.Add(seed)
	}
	f.Fuzz(func(t *testing.T, raw []byte) {
		envelope, err := DecodeHelperEnvelope(raw)
		if err != nil {
			return
		}
		encoded, err := EncodeHelperEnvelope(envelope)
		if err != nil {
			t.Fatalf("accepted envelope did not encode: %v", err)
		}
		if _, err := DecodeHelperEnvelope(encoded); err != nil {
			t.Fatalf("encoded accepted envelope did not decode: %v", err)
		}
	})
}

func FuzzManifestAndDetachedSignatureParsersNeverPanic(f *testing.F) {
	f.Add(append([]byte(nil), fixtureManifest...), fixtureSignatureBytes(fixtureManifest))
	f.Add([]byte("amsftp-helper-manifest-v1\n"), []byte("invalid"))
	f.Fuzz(func(_ *testing.T, manifest, signature []byte) {
		_, _ = ParseManifestV1(manifest)
		_, _ = ParseDetachedSignature(signature)
	})
}

func fixtureSignatureBytes(raw []byte) []byte {
	_ = raw
	encoded := base64.StdEncoding.EncodeToString(make([]byte, 64))
	return append([]byte(encoded), '\n')
}
