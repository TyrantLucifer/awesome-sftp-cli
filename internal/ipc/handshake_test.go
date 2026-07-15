package ipc

import (
	"reflect"
	"testing"

	"github.com/TyrantLucifer/awesome-mac-sftp/internal/domain"
)

func TestNegotiateSelectsHighestSharedVersion(t *testing.T) {
	client := []VersionRange{
		{Major: 1, MinMinor: 0, MaxMinor: 7},
		{Major: 2, MinMinor: 1, MaxMinor: 4},
	}
	server := []VersionRange{
		{Major: 1, MinMinor: 3, MaxMinor: 9},
		{Major: 2, MinMinor: 0, MaxMinor: 2},
	}

	got, _, err := Negotiate(client, server, nil, nil)
	if err != nil {
		t.Fatalf("Negotiate(): %v", err)
	}
	want := ProtocolVersion{Major: 2, Minor: 2}
	if got != want {
		t.Fatalf("version = %#v, want %#v", got, want)
	}
}

func TestNegotiateIncludesMinorRangeBoundaries(t *testing.T) {
	got, _, err := Negotiate(
		[]VersionRange{{Major: 1, MinMinor: 3, MaxMinor: 3}},
		[]VersionRange{{Major: 1, MinMinor: 1, MaxMinor: 3}},
		nil,
		nil,
	)
	if err != nil {
		t.Fatalf("Negotiate(): %v", err)
	}
	if want := (ProtocolVersion{Major: 1, Minor: 3}); got != want {
		t.Fatalf("version = %#v, want %#v", got, want)
	}
}

func TestNegotiateIntersectsFeaturesAtLowerVersion(t *testing.T) {
	client := []ProtocolFeature{
		{Name: "watch", Version: 2},
		{Name: "hash", Version: 5},
		{Name: "client_only", Version: 1},
		{Name: "hash", Version: 3},
	}
	server := []ProtocolFeature{
		{Name: "server_only", Version: 1},
		{Name: "hash", Version: 4},
		{Name: "watch", Version: 7},
	}

	_, got, err := Negotiate(
		[]VersionRange{{Major: 1, MinMinor: 0, MaxMinor: 0}},
		[]VersionRange{{Major: 1, MinMinor: 0, MaxMinor: 0}},
		client,
		server,
	)
	if err != nil {
		t.Fatalf("Negotiate(): %v", err)
	}
	want := []ProtocolFeature{
		{Name: "hash", Version: 4},
		{Name: "watch", Version: 2},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("features = %#v, want %#v", got, want)
	}
}

func TestNegotiateReturnsProtocolIncompatible(t *testing.T) {
	tests := map[string]struct {
		client []VersionRange
		server []VersionRange
	}{
		"different major": {
			client: []VersionRange{{Major: 1, MinMinor: 0, MaxMinor: 4}},
			server: []VersionRange{{Major: 2, MinMinor: 0, MaxMinor: 4}},
		},
		"disjoint minor": {
			client: []VersionRange{{Major: 1, MinMinor: 0, MaxMinor: 2}},
			server: []VersionRange{{Major: 1, MinMinor: 3, MaxMinor: 4}},
		},
		"empty ranges": {},
		"invalid ranges": {
			client: []VersionRange{{Major: 1, MinMinor: 4, MaxMinor: 3}},
			server: []VersionRange{{Major: 1, MinMinor: 0, MaxMinor: 4}},
		},
	}

	for name, test := range tests {
		t.Run(name, func(t *testing.T) {
			version, features, err := Negotiate(test.client, test.server, nil, nil)
			if err == nil {
				t.Fatal("Negotiate() error = nil")
			}
			if !domain.IsCode(err, domain.CodeProtocolIncompatible) {
				t.Fatalf("Negotiate() error = %v, want protocol_incompatible", err)
			}
			if version != (ProtocolVersion{}) || features != nil {
				t.Fatalf("failure result = (%#v, %#v), want zero values", version, features)
			}
		})
	}
}
