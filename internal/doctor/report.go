package doctor

import (
	"context"

	"github.com/TyrantLucifer/awesome-sftp-cli/internal/redaction"
)

const OutputVersion = 1

type Code string

const (
	CheckConfig           Code = "config"
	CheckRuntimeDirectory Code = "runtime_directory"
	CheckSocket           Code = "socket"
	CheckDaemon           Code = "daemon"
	CheckOpenSSH          Code = "openssh"
	CheckKnownHosts       Code = "known_hosts"
	CheckDatabase         Code = "database"
	CheckCache            Code = "cache"
	CheckHelper           Code = "helper"
	CheckDiskSpace        Code = "disk_space"
	CheckEndpoint         Code = "endpoint"
)

var requiredCodes = [...]Code{
	CheckConfig,
	CheckRuntimeDirectory,
	CheckSocket,
	CheckDaemon,
	CheckOpenSSH,
	CheckKnownHosts,
	CheckDatabase,
	CheckCache,
	CheckHelper,
	CheckDiskSpace,
}

type Status string

const (
	Pass    Status = "pass"
	Warn    Status = "warn"
	Fail    Status = "fail"
	Skipped Status = "skipped"
)

type Severity string

const (
	Info    Severity = "info"
	Warning Severity = "warning"
	Error   Severity = "error"
)

type Observation struct {
	Status     Status
	DetailCode string
}

type Probe func(context.Context) (Observation, error)

type Result struct {
	Code        Code     `json:"code"`
	Status      Status   `json:"status"`
	Severity    Severity `json:"severity"`
	DetailCode  string   `json:"detail_code"`
	Remediation string   `json:"remediation"`
}

type Report struct {
	OutputVersion int      `json:"output_version"`
	Results       []Result `json:"results"`
}

func RequiredCodes() []Code {
	return append([]Code(nil), requiredCodes[:]...)
}

func Run(ctx context.Context, probes map[Code]Probe, includeEndpoint bool) Report {
	codes := RequiredCodes()
	if includeEndpoint {
		codes = append(codes, CheckEndpoint)
	}
	results := make([]Result, 0, len(codes))
	for _, code := range codes {
		results = append(results, runOne(ctx, code, probes[code]))
	}
	return Report{OutputVersion: OutputVersion, Results: results}
}

func runOne(ctx context.Context, code Code, probe Probe) Result {
	if probe == nil {
		return result(code, Skipped, "probe_unavailable")
	}
	observation, err := probe(ctx)
	if err != nil || !validObservation(observation) {
		return result(code, Fail, "probe_failed")
	}
	return result(code, observation.Status, observation.DetailCode)
}

func validObservation(observation Observation) bool {
	switch observation.Status {
	case Pass, Warn, Fail, Skipped:
	default:
		return false
	}
	return redaction.SafeToken(observation.DetailCode)
}

func result(code Code, status Status, detail string) Result {
	return Result{
		Code:        code,
		Status:      status,
		Severity:    severity(status),
		DetailCode:  detail,
		Remediation: "troubleshooting/" + string(code),
	}
}

func severity(status Status) Severity {
	switch status {
	case Warn:
		return Warning
	case Fail:
		return Error
	default:
		return Info
	}
}
