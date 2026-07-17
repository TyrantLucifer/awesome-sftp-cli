package app

import "testing"

func TestPublicExitCodeContractIsStableAndComplete(t *testing.T) {
	want := []ExitCode{
		ExitSuccess,
		ExitInternal,
		ExitUsage,
		ExitConfig,
		ExitAuthentication,
		ExitNetwork,
		ExitConflict,
		ExitPartial,
		ExitCanceled,
	}
	for index, code := range want {
		if int(code) != index {
			t.Fatalf("exit code at index %d = %d", index, code)
		}
		if err := validateExitCode(code); err != nil {
			t.Fatalf("exit code %d is invalid: %v", code, err)
		}
	}
}
