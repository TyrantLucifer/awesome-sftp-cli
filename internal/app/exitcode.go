package app

import (
	"errors"
	"fmt"
)

type ExitCode int

const (
	ExitSuccess        ExitCode = 0
	ExitInternal       ExitCode = 1
	ExitUsage          ExitCode = 2
	ExitConfig         ExitCode = 3
	ExitAuthentication ExitCode = 4
	ExitNetwork        ExitCode = 5
	ExitConflict       ExitCode = 6
	ExitPartial        ExitCode = 7
	ExitCanceled       ExitCode = 8
)

type ExitError struct {
	code ExitCode
	err  error
}

func NewExitError(code ExitCode, err error) error {
	if err == nil {
		err = errors.New("operation failed")
	}
	if code <= ExitSuccess || code > ExitCanceled {
		code = ExitInternal
	}
	return &ExitError{code: code, err: err}
}

func (e *ExitError) Error() string {
	return e.err.Error()
}

func (e *ExitError) Unwrap() error {
	return e.err
}

func exitCode(err error) ExitCode {
	var typed *ExitError
	if errors.As(err, &typed) {
		return typed.code
	}
	return ExitInternal
}

func validateExitCode(code ExitCode) error {
	if code < ExitSuccess || code > ExitCanceled {
		return fmt.Errorf("exit code %d is outside the public contract", code)
	}
	return nil
}
