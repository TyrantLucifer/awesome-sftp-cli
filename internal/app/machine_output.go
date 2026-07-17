package app

import (
	"encoding/json"
	"io"

	"github.com/TyrantLucifer/awesome-mac-sftp/internal/daemon"
	"github.com/TyrantLucifer/awesome-mac-sftp/internal/domain"
)

type machineError struct {
	err      error
	envelope cliErrorEnvelope
}

type cliErrorEnvelope struct {
	OutputVersion int                `json:"output_version"`
	Error         cliErrorDescriptor `json:"error"`
}

type cliErrorDescriptor struct {
	ExitCode  int                 `json:"exit_code"`
	Class     string              `json:"class"`
	Message   string              `json:"message"`
	RequestID domain.RequestID    `json:"request_id"`
	ErrorCode domain.Code         `json:"error_code"`
	Retry     domain.RetryKind    `json:"retry"`
	Effect    domain.EffectStatus `json:"effect"`
}

func (err *machineError) Error() string { return err.err.Error() }

func (err *machineError) Unwrap() error { return err.err }

func (err *machineError) RenderCLIError(writer io.Writer) error {
	encoder := json.NewEncoder(writer)
	encoder.SetEscapeHTML(false)
	return encoder.Encode(err.envelope)
}

func machineCommandError(args []string, err error) error {
	if err == nil || !machineJSONRequested(args) {
		return err
	}
	code := exitCode(err)
	summary := daemon.DiagnosticSummary(err)
	return &machineError{
		err: err,
		envelope: cliErrorEnvelope{
			OutputVersion: PublicCLIContractVersion,
			Error: cliErrorDescriptor{
				ExitCode:  int(code),
				Class:     exitClass(code),
				Message:   err.Error(),
				RequestID: summary.RequestID,
				ErrorCode: summary.ErrorCode,
				Retry:     summary.Retry,
				Effect:    summary.Effect,
			},
		},
	}
}

func machineJSONRequested(args []string) bool {
	for index := 0; index+1 < len(args); index++ {
		if args[index] == "--format" && args[index+1] == "json" {
			return true
		}
	}
	return false
}

func exitClass(code ExitCode) string {
	switch code {
	case ExitSuccess:
		return "success"
	case ExitInternal:
		return "internal"
	case ExitUsage:
		return "usage"
	case ExitConfig:
		return "configuration"
	case ExitAuthentication:
		return "authentication"
	case ExitNetwork:
		return "network"
	case ExitConflict:
		return "conflict"
	case ExitPartial:
		return "partial_completion"
	case ExitCanceled:
		return "canceled"
	default:
		return "internal"
	}
}
