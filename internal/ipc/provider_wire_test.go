package ipc

import (
	"reflect"
	"testing"
	"time"

	"github.com/TyrantLucifer/awesome-sftp-cli/internal/domain"
)

func TestProviderWireRoundTripPreservesRawPathNameAndLinkTarget(t *testing.T) {
	size := uint64(42)
	mode := uint32(0o100600)
	modified := time.Unix(123, 456).UTC()
	precision := domain.TimePrecision("nanosecond")
	fileID := "1:2"
	targetKind := domain.EntryFile
	entry := domain.Entry{
		Location: domain.Location{
			EndpointID: "ep_aaaaaaaaaaaaaaaaaaaaaaaaaa",
			Path:       domain.CanonicalPath([]byte{'/', 'b', 0xff}),
		},
		Name: string([]byte{'b', 0xff}),
		Kind: domain.EntrySymlink,
		Metadata: domain.Metadata{
			Size:              &size,
			Mode:              &mode,
			ModifiedAt:        &modified,
			ModifiedPrecision: &precision,
			FileID:            &fileID,
		},
		Fingerprint: domain.Fingerprint{
			Size:              &size,
			ModifiedAt:        &modified,
			ModifiedPrecision: &precision,
			FileID:            &fileID,
		},
		Symlink: &domain.SymlinkInfo{
			RawTarget:    string([]byte{'.', '/', 0xfe}),
			ResolvedKind: &targetKind,
		},
	}

	wire := EncodeEntry(entry)
	decoded, err := DecodeEntry(wire)
	if err != nil {
		t.Fatalf("DecodeEntry(): %v", err)
	}
	if !reflect.DeepEqual(decoded, entry) {
		t.Fatalf("round trip = %#v, want %#v", decoded, entry)
	}
}

func TestDecodeProviderWireRejectsInvalidIdentityBytes(t *testing.T) {
	tests := []struct {
		name string
		wire WireEntry
	}{
		{
			name: "invalid path base64",
			wire: WireEntry{
				Location: WireLocation{EndpointID: "ep_aaaaaaaaaaaaaaaaaaaaaaaaaa", Path: WireBytes{Base64: "!"}},
				Name:     EncodeWireBytes([]byte("name")),
				Kind:     domain.EntryFile,
			},
		},
		{
			name: "path contains NUL",
			wire: WireEntry{
				Location: WireLocation{EndpointID: "ep_aaaaaaaaaaaaaaaaaaaaaaaaaa", Path: EncodeWireBytes([]byte{'/', 0, 'x'})},
				Name:     EncodeWireBytes([]byte("name")),
				Kind:     domain.EntryFile,
			},
		},
		{
			name: "name contains separator",
			wire: WireEntry{
				Location: WireLocation{EndpointID: "ep_aaaaaaaaaaaaaaaaaaaaaaaaaa", Path: EncodeWireBytes([]byte("/name"))},
				Name:     EncodeWireBytes([]byte("bad/name")),
				Kind:     domain.EntryFile,
			},
		},
		{
			name: "invalid endpoint ID",
			wire: WireEntry{
				Location: WireLocation{EndpointID: "local", Path: EncodeWireBytes([]byte("/name"))},
				Name:     EncodeWireBytes([]byte("name")),
				Kind:     domain.EntryFile,
			},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if _, err := DecodeEntry(test.wire); err == nil {
				t.Fatal("DecodeEntry() error = nil")
			}
		})
	}
}

func TestLocationWireRoundTrip(t *testing.T) {
	location := domain.Location{
		EndpointID: "ep_aaaaaaaaaaaaaaaaaaaaaaaaaa",
		Path:       domain.CanonicalPath([]byte{'/', 0xff}),
	}
	wire := EncodeLocation(location)
	decoded, err := DecodeLocation(wire)
	if err != nil {
		t.Fatal(err)
	}
	if decoded != location {
		t.Fatalf("decoded = %#v, want %#v", decoded, location)
	}
}
