package cacheprocess

import (
	"os"
	"strings"
	"testing"

	"github.com/TyrantLucifer/awesome-mac-sftp/internal/cache"
)

func TestCurrentProcessIdentityMatches(t *testing.T) {
	identity, err := CurrentIdentity()
	if err != nil {
		t.Fatalf("CurrentIdentity() error = %v", err)
	}
	if identity.PID != os.Getpid() {
		t.Fatalf("CurrentIdentity().PID = %d, want %d", identity.PID, os.Getpid())
	}
	if identity.BirthID == "" {
		t.Fatal("CurrentIdentity().BirthID is empty")
	}

	if got := NewClassifier().Classify(identity); got != cache.ProcessMatches {
		t.Fatalf("Classify(current identity) = %q, want %q", got, cache.ProcessMatches)
	}
}

func TestClassifierDetectsBirthMismatchForLivePID(t *testing.T) {
	identity, err := CurrentIdentity()
	if err != nil {
		t.Fatalf("CurrentIdentity() error = %v", err)
	}
	identity.BirthID = differentValidBirthID(t, identity.BirthID)

	if got := NewClassifier().Classify(identity); got != cache.ProcessBirthMismatch {
		t.Fatalf("Classify(reused PID identity) = %q, want %q", got, cache.ProcessBirthMismatch)
	}
}

func TestClassifierReportsMissingPIDGone(t *testing.T) {
	identity, err := CurrentIdentity()
	if err != nil {
		t.Fatalf("CurrentIdentity() error = %v", err)
	}
	identity.PID = 1 << 30

	if got := NewClassifier().Classify(identity); got != cache.ProcessGone {
		t.Fatalf("Classify(missing PID) = %q, want %q", got, cache.ProcessGone)
	}
}

func TestClassifierFailsClosedForMalformedIdentity(t *testing.T) {
	tests := []cache.ProcessIdentity{
		{},
		{PID: -1, BirthID: "linux-start-ticks:1"},
		{PID: os.Getpid(), BirthID: ""},
		{PID: os.Getpid(), BirthID: "not-a-platform-birth-id"},
	}
	for _, identity := range tests {
		if got := NewClassifier().Classify(identity); got != cache.ProcessUncertain {
			t.Errorf("Classify(%+v) = %q, want %q", identity, got, cache.ProcessUncertain)
		}
	}
}

func TestClassifierMapsLookupOutcomesFailClosed(t *testing.T) {
	const expected = "test-birth:41"
	tests := []struct {
		name    string
		birthID string
		outcome lookupOutcome
		want    cache.ProcessStatus
	}{
		{name: "exact match", birthID: expected, outcome: lookupFound, want: cache.ProcessMatches},
		{name: "PID reused", birthID: "test-birth:42", outcome: lookupFound, want: cache.ProcessBirthMismatch},
		{name: "process gone", outcome: lookupGone, want: cache.ProcessGone},
		{name: "permission or unsupported", outcome: lookupUncertain, want: cache.ProcessUncertain},
		{name: "empty observed birth", outcome: lookupFound, want: cache.ProcessUncertain},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			classifier := newClassifier(
				func(int) (string, lookupOutcome) { return test.birthID, test.outcome },
				func(value string) bool { return strings.HasPrefix(value, "test-birth:") },
			)
			got := classifier.Classify(cache.ProcessIdentity{PID: 7, BirthID: expected})
			if got != test.want {
				t.Fatalf("Classify() = %q, want %q", got, test.want)
			}
		})
	}
}

func differentValidBirthID(t *testing.T, birthID string) string {
	t.Helper()
	switch {
	case strings.HasPrefix(birthID, "linux-start-ticks:"):
		return "linux-start-ticks:0"
	case strings.HasPrefix(birthID, "darwin-start:"):
		return "darwin-start:1:0"
	default:
		t.Fatalf("unrecognized current birth ID %q", birthID)
		return ""
	}
}
