package helper

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/TyrantLucifer/awesome-mac-sftp/internal/domain"
)

func TestDirectPreflightProtocolIsBoundedCorrelatedAndComplete(t *testing.T) {
	now := time.Unix(1_800_000_000, 0)
	request := validDirectPreflightRequest()
	raw, err := json.Marshal(request)
	if err != nil {
		t.Fatal(err)
	}
	decoded, err := DecodeDirectPreflightRequest(raw, now)
	if err != nil || !reflect.DeepEqual(decoded, request) {
		t.Fatalf("decoded request = (%+v, %v), want %+v", decoded, err, request)
	}
	if decoded.Control != (DirectControlLimits{
		MaxFrameBytes: MaxHelperFrameBytes, MaxConcurrent: MaxHelperConcurrent,
		HeartbeatIntervalMS: 5_000, HeartbeatTimeoutMS: 15_000,
		CancelSemantics: "request_context", ProgressSemantics: "target_durable_bytes", ResultSemantics: "staged_not_committed",
	}) {
		t.Fatalf("direct control limits = %#v", decoded.Control)
	}
	result := validDirectPreflightResult(request, now)
	resultRaw, err := json.Marshal(result)
	if err != nil {
		t.Fatal(err)
	}
	decodedResult, err := DecodeDirectPreflightResult(resultRaw)
	if err != nil || !reflect.DeepEqual(decodedResult, result) {
		t.Fatalf("decoded result = (%+v, %v), want %+v", decodedResult, err, result)
	}
	passed, first, err := EvaluateDirectPreflight(request, result, now)
	if err != nil || !passed || first != (DirectPreflightCheck{}) {
		t.Fatalf("evaluation = (%t, %+v, %v), want pass", passed, first, err)
	}
}

func TestDirectPreflightEvaluationReportsFirstRequiredFailureOrUnknown(t *testing.T) {
	now := time.Unix(1_800_000_000, 0)
	request := validDirectPreflightRequest()
	for index, name := range DirectPreflightCheckOrder() {
		for _, status := range []DirectPreflightStatus{DirectPreflightFail, DirectPreflightUnknown} {
			t.Run(string(name)+"/"+string(status), func(t *testing.T) {
				result := validDirectPreflightResult(request, now)
				result.Checks[index].Status = status
				result.Checks[index].Reason = string(name) + "_" + string(status)
				passed, first, err := EvaluateDirectPreflight(request, result, now)
				if err != nil || passed || first != result.Checks[index] {
					t.Fatalf("evaluation = (%t, %+v, %v), want first %+v", passed, first, err, result.Checks[index])
				}
			})
		}
	}
}

func TestDirectPreflightRejectsUntrustedOrMalformedEvidence(t *testing.T) {
	now := time.Unix(1_800_000_000, 0)
	request := validDirectPreflightRequest()
	validResult := validDirectPreflightResult(request, now)
	tests := []struct {
		name   string
		mutate func(*DirectPreflightRequest, *DirectPreflightResult)
	}{
		{name: "host alias begins with option", mutate: func(request *DirectPreflightRequest, _ *DirectPreflightResult) {
			request.TargetHostAlias = "-oProxyCommand=bad"
		}},
		{name: "host alias contains whitespace", mutate: func(request *DirectPreflightRequest, _ *DirectPreflightResult) {
			request.TargetHostAlias = "target bad"
		}},
		{name: "part not job owned", mutate: func(request *DirectPreflightRequest, _ *DirectPreflightResult) {
			request.PartPath = "/target/.foreign.part"
		}},
		{name: "deadline exceeds hard limit", mutate: func(request *DirectPreflightRequest, _ *DirectPreflightResult) {
			request.DeadlineUnix = now.Add(MaxHelperRequestDuration + time.Second).Unix()
		}},
		{name: "frame limit changed", mutate: func(request *DirectPreflightRequest, _ *DirectPreflightResult) {
			request.Control.MaxFrameBytes++
		}},
		{name: "concurrency limit changed", mutate: func(request *DirectPreflightRequest, _ *DirectPreflightResult) {
			request.Control.MaxConcurrent++
		}},
		{name: "heartbeat contract changed", mutate: func(request *DirectPreflightRequest, _ *DirectPreflightResult) {
			request.Control.HeartbeatTimeoutMS = request.Control.HeartbeatIntervalMS
		}},
		{name: "cancel semantics changed", mutate: func(request *DirectPreflightRequest, _ *DirectPreflightResult) {
			request.Control.CancelSemantics = "best_effort"
		}},
		{name: "wrong request correlation", mutate: func(_ *DirectPreflightRequest, result *DirectPreflightResult) {
			result.RequestID = "req_bbbbbbbbbbbbbbbbbbbbbbbbbb"
		}},
		{name: "wrong job correlation", mutate: func(_ *DirectPreflightRequest, result *DirectPreflightResult) {
			result.JobID = "job_bbbbbbbbbbbbbbbbbbbbbbbbbb"
		}},
		{name: "source fingerprint drift", mutate: func(_ *DirectPreflightRequest, result *DirectPreflightResult) {
			result.SourceFingerprint.Size = uint64Pointer(999)
		}},
		{name: "missing required check", mutate: func(_ *DirectPreflightRequest, result *DirectPreflightResult) {
			result.Checks = result.Checks[:len(result.Checks)-1]
		}},
		{name: "reordered checks", mutate: func(_ *DirectPreflightRequest, result *DirectPreflightResult) {
			result.Checks[0], result.Checks[1] = result.Checks[1], result.Checks[0]
		}},
		{name: "duplicate checks", mutate: func(_ *DirectPreflightRequest, result *DirectPreflightResult) {
			result.Checks[1].Name = result.Checks[0].Name
		}},
		{name: "expired result", mutate: func(_ *DirectPreflightRequest, result *DirectPreflightResult) {
			result.ExpiresAtUnix = now.Add(-time.Second).Unix()
		}},
		{name: "digest is not sha256", mutate: func(_ *DirectPreflightRequest, result *DirectPreflightResult) {
			result.SourceSHA256 = strings.Repeat("g", 64)
		}},
		{name: "non stable reason", mutate: func(_ *DirectPreflightRequest, result *DirectPreflightResult) {
			result.Checks[0].Reason = "raw network: secret"
		}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			candidateRequest := request
			candidateRequest.SourceFingerprint = cloneDirectFingerprint(request.SourceFingerprint)
			candidateResult := validResult
			candidateResult.SourceFingerprint = cloneDirectFingerprint(validResult.SourceFingerprint)
			candidateResult.Checks = append([]DirectPreflightCheck(nil), validResult.Checks...)
			test.mutate(&candidateRequest, &candidateResult)
			if _, _, err := EvaluateDirectPreflight(candidateRequest, candidateResult, now); err == nil {
				t.Fatal("malformed or untrusted direct preflight evidence was accepted")
			}
		})
	}
	if _, err := DecodeDirectPreflightRequest([]byte(`{"version":1,"unknown":true}`), now); err == nil {
		t.Fatal("unknown direct preflight request field was accepted")
	}
}

func validDirectPreflightRequest() DirectPreflightRequest {
	size := uint64(27)
	versionID := "source-version-v1"
	return DirectPreflightRequest{
		Version: DirectProtocolVersion, RequestID: "req_aaaaaaaaaaaaaaaaaaaaaaaaaa", JobID: "job_aaaaaaaaaaaaaaaaaaaaaaaaaa",
		SourceEndpointID: "ep_aaaaaaaaaaaaaaaaaaaaaaaaaa", SourcePath: "/source/file",
		DestinationEndpointID: "ep_bbbbbbbbbbbbbbbbbbbbbbbbbb", PartPath: "/target/.file.part-job_aaaaaaaaaaaaaaaaaaaaaaaaaa",
		FinalPath: "/target/file", TargetHostAlias: "trusted-target", ExpectedSize: size,
		SourceFingerprint: domain.Fingerprint{Size: &size, VersionID: &versionID},
		IntegrityPolicy:   "require_strong", DeadlineUnix: 1_800_000_300, Nonce: strings.Repeat("a", 32),
		Control: DirectControlLimits{
			MaxFrameBytes: MaxHelperFrameBytes, MaxConcurrent: MaxHelperConcurrent,
			HeartbeatIntervalMS: 5_000, HeartbeatTimeoutMS: 15_000,
			CancelSemantics: "request_context", ProgressSemantics: "target_durable_bytes", ResultSemantics: "staged_not_committed",
		},
	}
}

func validDirectPreflightResult(request DirectPreflightRequest, now time.Time) DirectPreflightResult {
	checks := make([]DirectPreflightCheck, 0, len(DirectPreflightCheckOrder()))
	for _, name := range DirectPreflightCheckOrder() {
		checks = append(checks, DirectPreflightCheck{Name: name, Status: DirectPreflightPass, Reason: "passed"})
	}
	return DirectPreflightResult{
		Version: DirectProtocolVersion, RequestID: request.RequestID, JobID: request.JobID,
		CheckedAtUnix: now.Unix(), ExpiresAtUnix: now.Add(time.Minute).Unix(), Checks: checks,
		SourceFingerprint: cloneDirectFingerprint(request.SourceFingerprint), SourceSize: request.ExpectedSize,
		SourceSHA256: strings.Repeat("a", 64),
	}
}

func cloneDirectFingerprint(value domain.Fingerprint) domain.Fingerprint {
	cloned := value
	if value.Size != nil {
		size := *value.Size
		cloned.Size = &size
	}
	if value.VersionID != nil {
		versionID := *value.VersionID
		cloned.VersionID = &versionID
	}
	return cloned
}

func uint64Pointer(value uint64) *uint64 { return &value }
