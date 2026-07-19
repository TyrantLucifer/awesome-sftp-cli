package testkit

import (
	"math"
	"strings"
	"sync"
	"testing"

	"github.com/TyrantLucifer/awesome-sftp-cli/internal/domain"
)

var _ domain.Generator = (*SequenceGenerator)(nil)

func TestSequenceGeneratorEmitsZeroPaddedLowercaseBase32(t *testing.T) {
	generator := &SequenceGenerator{}

	first, err := generator.New("ep_")
	if err != nil {
		t.Fatalf("first New(): %v", err)
	}
	second, err := generator.New("sess_")
	if err != nil {
		t.Fatalf("second New(): %v", err)
	}

	if want := "ep_" + strings.Repeat("a", 26); first != want {
		t.Fatalf("first ID = %q, want %q", first, want)
	}
	if want := "sess_" + strings.Repeat("a", 25) + "b"; second != want {
		t.Fatalf("second ID = %q, want %q", second, want)
	}
}

func TestSequenceGeneratorIsConcurrentAndUnique(t *testing.T) {
	const count = 256
	generator := &SequenceGenerator{}
	results := make(chan string, count)
	errors := make(chan error, count)

	var waitGroup sync.WaitGroup
	waitGroup.Add(count)
	for range count {
		go func() {
			defer waitGroup.Done()
			value, err := generator.New("req_")
			if err != nil {
				errors <- err
				return
			}
			results <- value
		}()
	}
	waitGroup.Wait()
	close(results)
	close(errors)

	for err := range errors {
		t.Errorf("New(): %v", err)
	}
	seen := make(map[string]struct{}, count)
	for value := range results {
		if !strings.HasPrefix(value, "req_") || len(value) != len("req_")+26 {
			t.Errorf("New() = %q, want req_ plus 26 characters", value)
		}
		if _, duplicate := seen[value]; duplicate {
			t.Errorf("duplicate sequence value %q", value)
		}
		seen[value] = struct{}{}
	}
	if len(seen) != count {
		t.Fatalf("unique values = %d, want %d", len(seen), count)
	}
}

func TestSequenceGeneratorProducesValidTypedIDs(t *testing.T) {
	generator := &SequenceGenerator{}

	for range 64 {
		value, err := domain.NewRequestID(generator)
		if err != nil {
			t.Fatalf("NewRequestID(): %v", err)
		}
		if _, err := domain.ParseRequestID(string(value)); err != nil {
			t.Fatalf("ParseRequestID(%q): %v", value, err)
		}
	}
}

func TestSequenceGeneratorExhaustionDoesNotWrap(t *testing.T) {
	fresh := &SequenceGenerator{}
	first, err := fresh.New("req_")
	if err != nil {
		t.Fatalf("fresh New(): %v", err)
	}

	generator := &SequenceGenerator{next: math.MaxUint64}
	last, err := generator.New("req_")
	if err != nil {
		t.Fatalf("maximum New(): %v", err)
	}
	if last == first {
		t.Fatalf("maximum ID = first ID %q, want distinct values", last)
	}
	if generator.next != math.MaxUint64 {
		t.Errorf("next after maximum emission = %d, want %d", generator.next, uint64(math.MaxUint64))
	}

	got, err := generator.New("req_")
	if err == nil {
		t.Error("New() after maximum error = nil, want exhaustion error")
	} else if err.Error() == "" {
		t.Error("New() after maximum returned an empty exhaustion error")
	}
	if got != "" {
		t.Errorf("New() after maximum = %q, want no repeated ID", got)
	}
	if generator.next != math.MaxUint64 {
		t.Errorf("next after exhaustion = %d, want %d", generator.next, uint64(math.MaxUint64))
	}
}
