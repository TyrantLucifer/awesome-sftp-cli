// Package helper implements the optional, fail-closed Level 1 Helper contracts.
package helper

import (
	"crypto/ed25519"
	"encoding/base64"
	"errors"
	"fmt"
	"strconv"
	"strings"
)

const (
	ManifestFormatVersion  = 1
	ManifestHeader         = "amsftp-helper-manifest-v1"
	MaxManifestBytes       = 512
	MaxHelperArtifactBytes = 128 << 20
	detachedSignatureBytes = 89
)

type Version struct {
	Major uint32
	Minor uint32
	Patch uint32
}

func (v Version) String() string { return fmt.Sprintf("%d.%d.%d", v.Major, v.Minor, v.Patch) }

func (v Version) Compare(other Version) int {
	if v.Major != other.Major {
		return compareUint32(v.Major, other.Major)
	}
	if v.Minor != other.Minor {
		return compareUint32(v.Minor, other.Minor)
	}
	return compareUint32(v.Patch, other.Patch)
}

func compareUint32(left, right uint32) int {
	if left < right {
		return -1
	}
	if left > right {
		return 1
	}
	return 0
}

type Target struct {
	OS   string
	Arch string
}

type Manifest struct {
	Raw           []byte
	Version       Version
	ProtocolMajor uint16
	OS            string
	Arch          string
	Size          uint64
	SHA256        string
	KeyID         string
	MinClient     Version
}

func (m Manifest) Target() Target { return Target{OS: m.OS, Arch: m.Arch} }

func ParseManifestV1(raw []byte) (Manifest, error) {
	if len(raw) == 0 || len(raw) > MaxManifestBytes {
		return Manifest{}, errors.New("parse helper manifest: length is invalid")
	}
	for _, value := range raw {
		if value < 0x20 || value > 0x7e {
			if value != '\n' {
				return Manifest{}, errors.New("parse helper manifest: only printable ASCII and LF are allowed")
			}
		}
	}
	if raw[len(raw)-1] != '\n' || len(raw) > 1 && raw[len(raw)-2] == '\n' {
		return Manifest{}, errors.New("parse helper manifest: exactly one final LF is required")
	}
	lines := strings.Split(string(raw), "\n")
	if len(lines) != 10 || lines[9] != "" || lines[0] != ManifestHeader {
		return Manifest{}, errors.New("parse helper manifest: line count or header is invalid")
	}
	values := make([]string, 8)
	keys := []string{"version", "protocol_major", "os", "arch", "size", "sha256", "key_id", "min_client"}
	for index, key := range keys {
		prefix := key + "="
		if !strings.HasPrefix(lines[index+1], prefix) {
			return Manifest{}, errors.New("parse helper manifest: fields are missing, unknown, or out of order")
		}
		values[index] = strings.TrimPrefix(lines[index+1], prefix)
	}
	version, err := parseReleaseVersion(values[0])
	if err != nil {
		return Manifest{}, err
	}
	protocol, err := parseCanonicalUint(values[1], 5, 65535)
	if err != nil {
		return Manifest{}, errors.New("parse helper manifest: protocol_major is invalid")
	}
	if values[2] != "darwin" && values[2] != "linux" {
		return Manifest{}, errors.New("parse helper manifest: os is invalid")
	}
	if values[3] != "amd64" && values[3] != "arm64" {
		return Manifest{}, errors.New("parse helper manifest: arch is invalid")
	}
	size, err := parseCanonicalUint(values[4], 9, MaxHelperArtifactBytes)
	if err != nil {
		return Manifest{}, errors.New("parse helper manifest: size is invalid")
	}
	if len(values[5]) != 64 || !allBytes(values[5], func(value byte) bool { return value >= '0' && value <= '9' || value >= 'a' && value <= 'f' }) {
		return Manifest{}, errors.New("parse helper manifest: sha256 is invalid")
	}
	if len(values[6]) < 1 || len(values[6]) > 64 || !isLowerOrDigit(values[6][0]) || !allBytes(values[6], isKeyIDByte) {
		return Manifest{}, errors.New("parse helper manifest: key_id is invalid")
	}
	minimum, err := parseReleaseVersion(values[7])
	if err != nil {
		return Manifest{}, err
	}
	return Manifest{Raw: append([]byte(nil), raw...), Version: version, ProtocolMajor: uint16(protocol), OS: values[2], Arch: values[3], Size: size, SHA256: values[5], KeyID: values[6], MinClient: minimum}, nil // #nosec G115 -- parser caps protocol at MaxHelperProtocolMajor.
}

func parseReleaseVersion(value string) (Version, error) {
	if len(value) == 0 || len(value) > 32 {
		return Version{}, errors.New("parse helper manifest: release version is invalid")
	}
	parts := strings.Split(value, ".")
	if len(parts) != 3 {
		return Version{}, errors.New("parse helper manifest: release version is invalid")
	}
	parsed := [3]uint32{}
	for index, part := range parts {
		if len(part) == 0 || len(part) > 10 || len(part) > 1 && part[0] == '0' || !allBytes(part, func(value byte) bool { return value >= '0' && value <= '9' }) {
			return Version{}, errors.New("parse helper manifest: release version is invalid")
		}
		number, err := strconv.ParseUint(part, 10, 31)
		if err != nil || number > 2147483647 {
			return Version{}, errors.New("parse helper manifest: release version is invalid")
		}
		parsed[index] = uint32(number)
	}
	return Version{Major: parsed[0], Minor: parsed[1], Patch: parsed[2]}, nil
}

func parseCanonicalUint(value string, maximumDigits int, maximum uint64) (uint64, error) {
	if len(value) == 0 || len(value) > maximumDigits || value[0] == '0' || !allBytes(value, func(current byte) bool { return current >= '0' && current <= '9' }) {
		return 0, errors.New("invalid canonical integer")
	}
	parsed, err := strconv.ParseUint(value, 10, 64)
	if err != nil || parsed == 0 || parsed > maximum {
		return 0, errors.New("integer outside bounds")
	}
	return parsed, nil
}

func allBytes(value string, allowed func(byte) bool) bool {
	for index := range value {
		if !allowed(value[index]) {
			return false
		}
	}
	return true
}

func isKeyIDByte(value byte) bool {
	return value >= 'a' && value <= 'z' || value >= '0' && value <= '9' || value == '.' || value == '_' || value == '-'
}

func isLowerOrDigit(value byte) bool {
	return value >= 'a' && value <= 'z' || value >= '0' && value <= '9'
}

func ParseDetachedSignature(raw []byte) ([]byte, error) {
	if len(raw) != detachedSignatureBytes || raw[88] != '\n' || raw[86] != '=' || raw[87] != '=' {
		return nil, errors.New("parse helper signature: length or padding is invalid")
	}
	encoded := string(raw[:88])
	if !allBytes(encoded[:86], func(value byte) bool {
		return value >= 'A' && value <= 'Z' || value >= 'a' && value <= 'z' || value >= '0' && value <= '9' || value == '+' || value == '/'
	}) {
		return nil, errors.New("parse helper signature: alphabet is invalid")
	}
	decoded, err := base64.StdEncoding.DecodeString(encoded)
	if err != nil || len(decoded) != ed25519.SignatureSize || base64.StdEncoding.EncodeToString(decoded) != encoded {
		return nil, errors.New("parse helper signature: encoding is not canonical")
	}
	return decoded, nil
}

type Verifier struct{ keys map[string]ed25519.PublicKey }

// NewProductionVerifier is deliberately empty while production distribution
// is CLOSED. Test fixture trust is injected only by same-package test code.
func NewProductionVerifier() Verifier { return Verifier{} }

func (v Verifier) Verify(rawManifest, rawSignature []byte) error {
	manifest, err := ParseManifestV1(rawManifest)
	if err != nil {
		return err
	}
	signature, err := ParseDetachedSignature(rawSignature)
	if err != nil {
		return err
	}
	key := v.keys[manifest.KeyID]
	if len(key) != ed25519.PublicKeySize {
		return errors.New("verify helper manifest: key is not trusted")
	}
	if !ed25519.Verify(key, rawManifest, signature) {
		return errors.New("verify helper manifest: signature is invalid")
	}
	return nil
}
