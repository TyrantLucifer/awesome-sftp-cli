package app

import (
	"bytes"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/TyrantLucifer/awesome-sftp-cli/internal/domain"
)

func TestInstallPreflightUsesManagedRootAndVersionedJSON(t *testing.T) {
	var gotPath string
	var gotManaged bool
	var stdout bytes.Buffer
	err := runInstallCommandWithPreflight(
		[]string{"preflight", "--root", "/safe/root", "--format", "json"},
		&stdout,
		func(path string, managed bool) error {
			gotPath, gotManaged = path, managed
			return nil
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	if gotPath != "/safe/root" || !gotManaged {
		t.Fatalf("preflight target = (%q, %t)", gotPath, gotManaged)
	}
	var output installPreflightOutput
	if err := json.Unmarshal(stdout.Bytes(), &output); err != nil {
		t.Fatal(err)
	}
	if output.OutputVersion != PublicCLIContractVersion || !output.Safe {
		t.Fatalf("preflight output = %#v", output)
	}
}

func TestInstallPreflightFailureIsStableConfigurationIntegrityError(t *testing.T) {
	var stdout bytes.Buffer
	err := runInstallCommandWithPreflight(
		[]string{"preflight", "--prefix", "/sensitive/home/.local", "--format", "json"},
		&stdout,
		func(string, bool) error { return errors.New("ancestor /sensitive is owned by another uid") },
	)
	if err == nil {
		t.Fatal("preflight error = nil")
	}
	var rendered bytes.Buffer
	var machine *machineError
	if !errors.As(err, &machine) {
		t.Fatalf("error type = %T", err)
	}
	if err := machine.RenderCLIError(&rendered); err != nil {
		t.Fatal(err)
	}
	var envelope cliErrorEnvelope
	if err := json.Unmarshal(rendered.Bytes(), &envelope); err != nil {
		t.Fatal(err)
	}
	if envelope.Error.Class != "configuration" || envelope.Error.ErrorCode != domain.CodeIntegrityFailed {
		t.Fatalf("machine error = %#v", envelope)
	}
	if strings.Contains(rendered.String(), "/sensitive") {
		t.Fatalf("machine error leaked preflight cause: %q", rendered.String())
	}
}

func TestInstallPreflightRejectsAmbiguousArguments(t *testing.T) {
	for _, args := range [][]string{
		{"preflight"},
		{"preflight", "--root", "/safe", "--prefix", "/other"},
		{"preflight", "--root", "/safe", "--format", "json", "--format", "human"},
		{"unknown", "--root", "/safe"},
	} {
		if _, err := parseInstallPreflight(args); err == nil {
			t.Fatalf("parseInstallPreflight(%q) error = nil", args)
		}
	}
}
