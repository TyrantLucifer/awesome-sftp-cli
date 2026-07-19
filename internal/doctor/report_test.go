package doctor

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
)

func TestRunProducesEveryRequiredCheckInFrozenOrder(t *testing.T) {
	probes := make(map[Code]Probe)
	for _, code := range RequiredCodes() {
		code := code
		probes[code] = func(context.Context) (Observation, error) {
			return Observation{Status: Pass, DetailCode: "available"}, nil
		}
	}
	report := Run(context.Background(), probes, false)
	if report.OutputVersion != OutputVersion || len(report.Results) != len(RequiredCodes()) {
		t.Fatalf("report = %#v", report)
	}
	for index, code := range RequiredCodes() {
		result := report.Results[index]
		if result.Code != code || result.Status != Pass || result.Severity != Info || result.DetailCode != "available" {
			t.Fatalf("result[%d] = %#v, want pass for %q", index, result, code)
		}
		if result.Remediation == "" || !strings.HasPrefix(result.Remediation, "troubleshooting/") {
			t.Fatalf("result[%d] remediation = %q", index, result.Remediation)
		}
	}
}

func TestRunBoundsFailuresAndNeverExportsRawCause(t *testing.T) {
	const secret = "stage6-secret-user@example.com/private/key"
	probes := map[Code]Probe{
		CheckConfig: func(context.Context) (Observation, error) {
			return Observation{}, errors.New(secret)
		},
		CheckRuntimeDirectory: func(context.Context) (Observation, error) {
			return Observation{Status: Fail, DetailCode: secret}, nil
		},
		CheckSocket: func(context.Context) (Observation, error) {
			return Observation{Status: Warn, DetailCode: "not_running"}, nil
		},
	}
	report := Run(context.Background(), probes, true)
	encoded, err := json.Marshal(report)
	if err != nil {
		t.Fatalf("json.Marshal(): %v", err)
	}
	if strings.Contains(string(encoded), secret) {
		t.Fatalf("report leaked raw cause: %s", encoded)
	}
	if len(report.Results) != len(RequiredCodes())+1 || report.Results[len(report.Results)-1].Code != CheckEndpoint {
		t.Fatalf("optional endpoint result missing: %#v", report.Results)
	}
	assertResult(t, report.Results[0], CheckConfig, Fail, Error, "probe_failed")
	assertResult(t, report.Results[1], CheckRuntimeDirectory, Fail, Error, "probe_failed")
	assertResult(t, report.Results[2], CheckSocket, Warn, Warning, "not_running")
	assertResult(t, report.Results[3], CheckDaemon, Skipped, Info, "probe_unavailable")
}

func assertResult(t *testing.T, got Result, code Code, status Status, severity Severity, detail string) {
	t.Helper()
	if got.Code != code || got.Status != status || got.Severity != severity || got.DetailCode != detail {
		t.Fatalf("result = %#v, want code=%q status=%q severity=%q detail=%q", got, code, status, severity, detail)
	}
}
