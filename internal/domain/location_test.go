package domain

import (
	"bytes"
	"testing"
)

func TestLocationRejectsEmptyEndpoint(t *testing.T) {
	if _, err := NewLocation("", "/tmp"); err == nil {
		t.Fatal("NewLocation() error = nil, want empty-endpoint error")
	}
}

func TestLocationRejectsEmptyOrNULPath(t *testing.T) {
	tests := map[string]CanonicalPath{
		"empty": "",
		"NUL":   CanonicalPath([]byte{'/', 'a', 0, 'b'}),
	}
	for name, path := range tests {
		t.Run(name, func(t *testing.T) {
			if _, err := NewLocation("ep_aaaaaaaaaaaaaaaaaaaaaaaaaa", path); err == nil {
				t.Fatal("NewLocation() error = nil, want invalid-path error")
			}
		})
	}
}

func TestLocationPreservesInvalidUTF8Bytes(t *testing.T) {
	want := []byte{'/', 0xff, 'x', 0x80}

	location, err := NewLocation(
		"ep_aaaaaaaaaaaaaaaaaaaaaaaaaa",
		CanonicalPath(want),
	)
	if err != nil {
		t.Fatalf("NewLocation(): %v", err)
	}
	if got := []byte(location.Path); !bytes.Equal(got, want) {
		t.Fatalf("path bytes = %x, want %x", got, want)
	}
}
