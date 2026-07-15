package domain

import (
	"bytes"
	"strings"
	"sync"
	"testing"
)

type recordingGenerator struct {
	prefix string
	value  string
	err    error
}

func (g *recordingGenerator) New(prefix string) (string, error) {
	g.prefix = prefix
	return prefix + g.value, g.err
}

func TestRandomIDUsesExpectedPrefixAndLength(t *testing.T) {
	prefixes := []string{"ep_", "sess_", "req_", "job_", "evt_"}
	for _, prefix := range prefixes {
		t.Run(prefix, func(t *testing.T) {
			generator := &RandomGenerator{Reader: bytes.NewReader(make([]byte, 16))}

			got, err := generator.New(prefix)
			if err != nil {
				t.Fatalf("New(%q): %v", prefix, err)
			}
			if !strings.HasPrefix(got, prefix) {
				t.Fatalf("New(%q) = %q, want prefix %q", prefix, got, prefix)
			}
			encoded := strings.TrimPrefix(got, prefix)
			if len(encoded) != 26 {
				t.Fatalf("encoded length = %d, want 26", len(encoded))
			}
			for _, character := range encoded {
				if !strings.ContainsRune("abcdefghijklmnopqrstuvwxyz234567", character) {
					t.Fatalf("encoded ID %q contains non-base32 character %q", encoded, character)
				}
			}
		})
	}
}

func TestRandomIDReturnsReaderError(t *testing.T) {
	generator := &RandomGenerator{Reader: bytes.NewReader(make([]byte, 15))}

	if _, err := generator.New("ep_"); err == nil {
		t.Fatal("New() error = nil, want random reader error")
	}
}

func TestRandomGeneratorSerializesConcurrentReaderAccess(t *testing.T) {
	const count = 64
	generator := &RandomGenerator{Reader: bytes.NewReader(make([]byte, count*idRandomBytes))}
	errors := make(chan error, count)

	var waitGroup sync.WaitGroup
	waitGroup.Add(count)
	for range count {
		go func() {
			defer waitGroup.Done()
			_, err := generator.New("ep_")
			errors <- err
		}()
	}
	waitGroup.Wait()
	close(errors)

	for err := range errors {
		if err != nil {
			t.Errorf("New(): %v", err)
		}
	}
}

func TestParseIDRejectsWrongPrefix(t *testing.T) {
	value := "sess_" + strings.Repeat("a", 26)

	if _, err := ParseEndpointID(value); err == nil {
		t.Fatal("ParseEndpointID() error = nil, want wrong-prefix error")
	}
}

func TestParseIDRejectsMalformedEncoding(t *testing.T) {
	tests := map[string]string{
		"too short":        "ep_" + strings.Repeat("a", 25),
		"too long":         "ep_" + strings.Repeat("a", 27),
		"uppercase":        "ep_" + strings.Repeat("A", 26),
		"invalid alphabet": "ep_" + strings.Repeat("0", 26),
	}
	for name, value := range tests {
		t.Run(name, func(t *testing.T) {
			if _, err := ParseEndpointID(value); err == nil {
				t.Fatalf("ParseEndpointID(%q) error = nil, want malformed-ID error", value)
			}
		})
	}
}

func TestParseIDAcceptsEveryLowercaseBase32Character(t *testing.T) {
	for _, character := range "abcdefghijklmnopqrstuvwxyz234567" {
		value := "ep_" + strings.Repeat(string(character), 26)
		if _, err := ParseEndpointID(value); err != nil {
			t.Errorf("ParseEndpointID(%q): %v", value, err)
		}
	}
}

func TestTypedIDConstructorsAndParsersUseTheirWirePrefixes(t *testing.T) {
	payload := strings.Repeat("a", 26)
	tests := []struct {
		name   string
		prefix string
		newID  func(Generator) (string, error)
		parse  func(string) (string, error)
	}{
		{
			name:   "endpoint",
			prefix: "ep_",
			newID: func(generator Generator) (string, error) {
				value, err := NewEndpointID(generator)
				return string(value), err
			},
			parse: func(value string) (string, error) {
				parsed, err := ParseEndpointID(value)
				return string(parsed), err
			},
		},
		{
			name:   "session",
			prefix: "sess_",
			newID: func(generator Generator) (string, error) {
				value, err := NewSessionID(generator)
				return string(value), err
			},
			parse: func(value string) (string, error) {
				parsed, err := ParseSessionID(value)
				return string(parsed), err
			},
		},
		{
			name:   "request",
			prefix: "req_",
			newID: func(generator Generator) (string, error) {
				value, err := NewRequestID(generator)
				return string(value), err
			},
			parse: func(value string) (string, error) {
				parsed, err := ParseRequestID(value)
				return string(parsed), err
			},
		},
		{
			name:   "job",
			prefix: "job_",
			newID: func(generator Generator) (string, error) {
				value, err := NewJobID(generator)
				return string(value), err
			},
			parse: func(value string) (string, error) {
				parsed, err := ParseJobID(value)
				return string(parsed), err
			},
		},
		{
			name:   "event",
			prefix: "evt_",
			newID: func(generator Generator) (string, error) {
				value, err := NewEventID(generator)
				return string(value), err
			},
			parse: func(value string) (string, error) {
				parsed, err := ParseEventID(value)
				return string(parsed), err
			},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			generator := &recordingGenerator{value: payload}
			got, err := test.newID(generator)
			if err != nil {
				t.Fatalf("new ID: %v", err)
			}
			want := test.prefix + payload
			if got != want {
				t.Fatalf("new ID = %q, want %q", got, want)
			}
			if generator.prefix != test.prefix {
				t.Fatalf("generator prefix = %q, want %q", generator.prefix, test.prefix)
			}

			parsed, err := test.parse(got)
			if err != nil {
				t.Fatalf("parse ID: %v", err)
			}
			if parsed != want {
				t.Fatalf("parsed ID = %q, want %q", parsed, want)
			}
		})
	}
}

func TestTypedIDConstructorRejectsMalformedGeneratorValue(t *testing.T) {
	generator := &recordingGenerator{value: "short"}

	if _, err := NewEndpointID(generator); err == nil {
		t.Fatal("NewEndpointID() error = nil, want malformed-generator-value error")
	}
}
