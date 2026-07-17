package helper

import (
	"crypto/ed25519"
	"encoding/base64"
	"encoding/hex"
	"strings"
	"testing"
)

const fixtureSeedHex = "9d61b19deffd5a60ba844af492ec2cc44449c5697b326919703bac031cae7f60"

var fixtureManifest = []byte("amsftp-helper-manifest-v1\nversion=4.0.0\nprotocol_major=1\nos=linux\narch=amd64\nsize=28\nsha256=0bd3084aa66fb81346ccf2b7ed3c301b5cbbef431ea26e61c959a4793262d243\nkey_id=fixture-rfc8032-nonrelease\nmin_client=4.0.0\n")

func TestManifestV1StrictParseAndFixtureOnlyVerification(t *testing.T) {
	signature := fixtureSignature(t, fixtureManifest)
	manifest, err := ParseManifestV1(fixtureManifest)
	if err != nil {
		t.Fatal(err)
	}
	if manifest.Version.String() != "4.0.0" || manifest.ProtocolMajor != 1 || manifest.Size != 28 || manifest.Target() != (Target{OS: "linux", Arch: "amd64"}) {
		t.Fatalf("manifest = %#v", manifest)
	}
	if err := NewProductionVerifier().Verify(fixtureManifest, signature); err == nil {
		t.Fatal("production verifier accepted the non-release RFC fixture key")
	}
	if err := newFixtureVerifier(t).Verify(fixtureManifest, signature); err != nil {
		t.Fatalf("fixture verifier: %v", err)
	}
}

func TestManifestV1RejectsEveryNonCanonicalShape(t *testing.T) {
	valid := string(fixtureManifest)
	tests := map[string][]byte{
		"empty":                nil,
		"bom":                  append([]byte{0xef, 0xbb, 0xbf}, fixtureManifest...),
		"crlf":                 []byte(strings.ReplaceAll(valid, "\n", "\r\n")),
		"missing final lf":     []byte(strings.TrimSuffix(valid, "\n")),
		"extra final lf":       []byte(valid + "\n"),
		"unknown field":        []byte(strings.Replace(valid, "min_client=4.0.0\n", "unknown=x\nmin_client=4.0.0\n", 1)),
		"reordered":            []byte(strings.Replace(valid, "os=linux\narch=amd64\n", "arch=amd64\nos=linux\n", 1)),
		"leading zero version": []byte(strings.Replace(valid, "version=4.0.0", "version=04.0.0", 1)),
		"prerelease":           []byte(strings.Replace(valid, "version=4.0.0", "version=4.0.0-rc1", 1)),
		"zero protocol":        []byte(strings.Replace(valid, "protocol_major=1", "protocol_major=0", 1)),
		"unsupported os":       []byte(strings.Replace(valid, "os=linux", "os=freebsd", 1)),
		"uppercase hash":       []byte(strings.Replace(valid, "sha256=0b", "sha256=0B", 1)),
		"bad key id":           []byte(strings.Replace(valid, "key_id=fixture", "key_id=Fixture", 1)),
		"size zero":            []byte(strings.Replace(valid, "size=28", "size=0", 1)),
	}
	for name, raw := range tests {
		t.Run(name, func(t *testing.T) {
			if _, err := ParseManifestV1(raw); err == nil {
				t.Fatal("ParseManifestV1 succeeded")
			}
		})
	}
	if _, err := ParseManifestV1(make([]byte, 513)); err == nil {
		t.Fatal("oversize manifest succeeded")
	}
}

func TestDetachedSignatureRejectsNonCanonicalBase64(t *testing.T) {
	valid := fixtureSignature(t, fixtureManifest)
	tests := [][]byte{
		valid[:len(valid)-1],
		append(append([]byte(nil), valid...), '\n'),
		[]byte(strings.ReplaceAll(string(valid), "\n", "\r\n")),
		[]byte(strings.TrimSuffix(string(valid), "==\n") + "\n"),
		append([]byte{'-'}, valid[1:]...),
	}
	for index, raw := range tests {
		if _, err := ParseDetachedSignature(raw); err == nil {
			t.Fatalf("signature mutation %d succeeded", index)
		}
	}
}

func newFixtureVerifier(t *testing.T) Verifier {
	t.Helper()
	seed, err := hex.DecodeString(fixtureSeedHex)
	if err != nil {
		t.Fatal(err)
	}
	private := ed25519.NewKeyFromSeed(seed)
	public := append(ed25519.PublicKey(nil), private.Public().(ed25519.PublicKey)...)
	return Verifier{keys: map[string]ed25519.PublicKey{"fixture-rfc8032-nonrelease": public}}
}

func fixtureSignature(t *testing.T, raw []byte) []byte {
	t.Helper()
	seed, err := hex.DecodeString(fixtureSeedHex)
	if err != nil {
		t.Fatal(err)
	}
	signature := ed25519.Sign(ed25519.NewKeyFromSeed(seed), raw)
	return []byte(base64.StdEncoding.EncodeToString(signature) + "\n")
}
