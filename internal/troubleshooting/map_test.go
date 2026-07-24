package troubleshooting

import (
	"testing"

	"github.com/TyrantLucifer/awesome-sftp-cli/internal/doctor"
	"github.com/TyrantLucifer/awesome-sftp-cli/internal/domain"
)

func TestEntriesCoverEveryDoctorAndDomainCodeExactlyOnce(t *testing.T) {
	entries := Entries()
	seen := make(map[string]bool, len(entries))
	for _, entry := range entries {
		key := string(entry.Kind) + "/" + entry.Code
		if seen[key] {
			t.Errorf("duplicate troubleshooting entry %q", key)
		}
		seen[key] = true
		if entry.Meaning == "" || entry.Action == "" {
			t.Errorf("incomplete troubleshooting entry %#v", entry)
		}
	}
	doctorCodes := append(doctor.RequiredCodes(), doctor.CheckEndpoint)
	for _, code := range doctorCodes {
		if !seen[string(DoctorCheck)+"/"+string(code)] {
			t.Errorf("missing doctor troubleshooting code %q", code)
		}
	}
	domainCodes := []domain.Code{
		domain.CodeInvalidArgument, domain.CodeNotFound, domain.CodeAlreadyExists, domain.CodePermissionDenied,
		domain.CodeAuthRequired, domain.CodeTransportInterrupted, domain.CodeTimeout, domain.CodeUnsupported,
		domain.CodeCapabilityLost, domain.CodeConflict, domain.CodeResourceExhausted, domain.CodeIntegrityFailed,
		domain.CodeCanceled, domain.CodeProtocolIncompatible, domain.CodeInternal,
	}
	for _, code := range domainCodes {
		if !seen[string(DomainError)+"/"+string(code)] {
			t.Errorf("missing domain troubleshooting code %q", code)
		}
	}
	if len(entries) != len(doctorCodes)+len(domainCodes) {
		t.Fatalf("entry count = %d, want %d", len(entries), len(doctorCodes)+len(domainCodes))
	}
}
