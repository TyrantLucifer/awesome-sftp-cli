package fake

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/TyrantLucifer/awesome-mac-sftp/internal/domain"
	"github.com/TyrantLucifer/awesome-mac-sftp/internal/foundation"
)

func FuzzNormalizePath(f *testing.F) {
	f.Add("/", false, false)
	f.Add("///directory//./file", false, false)
	f.Add("/directory/../../escape", false, false)
	f.Add(string([]byte{'/', 'a', 0, 'b'}), false, false)
	f.Add(string([]byte{'/', 0xff, '/', 0x80}), false, false)
	f.Add("child/../file", true, false)
	f.Add("relative-without-base", false, false)
	f.Add("/", false, true)

	f.Fuzz(func(t *testing.T, input string, useBase bool, endpointMismatch bool) {
		implementation, _, err := New(normalizeFuzzScenario())
		if err != nil {
			t.Fatalf("New(): %v", err)
		}
		endpointID := contractEndpointID
		if endpointMismatch {
			endpointID = "ep_bbbbbbbbbbbbbbbbbbbbbbbbbb"
		}
		var base *domain.Location
		if useBase {
			base = &domain.Location{EndpointID: contractEndpointID, Path: "/base"}
		}

		location, err := implementation.Normalize(context.Background(), domain.NormalizeRequest{
			EndpointID: endpointID,
			Base:       base,
			Input:      input,
		})
		if err != nil {
			if !domain.IsCode(err, domain.CodeInvalidArgument) {
				t.Fatalf("Normalize(%x) error = %v, want invalid_argument", []byte(input), err)
			}
			return
		}
		if endpointMismatch {
			t.Fatalf("Normalize(%x) accepted endpoint mismatch", []byte(input))
		}
		if location.EndpointID != contractEndpointID {
			t.Fatalf("Normalize(%x).EndpointID = %q", []byte(input), location.EndpointID)
		}
		path := string(location.Path)
		if !strings.HasPrefix(path, "/") || strings.IndexByte(path, 0) >= 0 {
			t.Fatalf("Normalize(%x).Path = %x, outside root or contains NUL", []byte(input), []byte(path))
		}
		for _, component := range strings.Split(strings.TrimPrefix(path, "/"), "/") {
			if component == "." || component == ".." {
				t.Fatalf("Normalize(%x).Path = %x, contains traversal", []byte(input), []byte(path))
			}
		}

		again, err := implementation.Normalize(context.Background(), domain.NormalizeRequest{
			EndpointID: contractEndpointID,
			Input:      path,
		})
		if err != nil {
			t.Fatalf("Normalize(canonical %x): %v", []byte(path), err)
		}
		if again != location {
			t.Fatalf("Normalize(canonical %x) = %#v, want %#v", []byte(path), again, location)
		}
	})
}

func normalizeFuzzScenario() Scenario {
	capabilities, err := domain.NewCapabilitySnapshot(
		domain.CapabilityRevision{SessionID: contractSessionID, Generation: 1},
		true,
		nil,
	)
	if err != nil {
		panic(err)
	}
	observedAt := time.Unix(0, 0).UTC()
	return Scenario{
		Endpoint: domain.Endpoint{
			ID:          contractEndpointID,
			Kind:        domain.EndpointLocal,
			DisplayName: "Normalize fuzz fake",
		},
		Snapshot: domain.EndpointSnapshot{
			EndpointID:   contractEndpointID,
			SessionID:    contractSessionID,
			State:        domain.StateReady,
			Capabilities: capabilities,
			ObservedAt:   observedAt,
		},
		Root: Node{
			Name: "/",
			Kind: domain.EntryDirectory,
		},
		DefaultLimit: 1,
		Clock:        foundation.NewManualClock(observedAt),
	}
}
