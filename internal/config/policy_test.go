package config

import (
	"bytes"
	"encoding/json"
	"reflect"
	"strings"
	"testing"

	"github.com/TyrantLucifer/awesome-sftp-cli/internal/helper"
)

func TestResolutionPolicyFreezesSupportedAndUnsupportedLayers(t *testing.T) {
	want := ResolutionPolicy{
		Precedence:        []string{"cli_startup_selection", "workspace_state", "user_config", "built_in_defaults"},
		Unsupported:       []string{"system_config", "amsftp_environment_config"},
		EnvironmentRole:   "openssh_and_external_command_discovery_only",
		HotReloadPolicy:   "none_restart_required",
		JobSemanticPolicy: "frozen_at_plan_creation",
	}
	if got := DefaultResolutionPolicy(); !reflect.DeepEqual(got, want) {
		t.Fatalf("resolution policy = %#v, want %#v", got, want)
	}
}

func TestEffectiveOutputCarriesResolutionPolicy(t *testing.T) {
	var output bytes.Buffer
	if err := WriteRedactedEffective(&output, Default()); err != nil {
		t.Fatal(err)
	}
	var decoded struct {
		Policy ResolutionPolicy `json:"resolution_policy"`
	}
	if err := json.Unmarshal(output.Bytes(), &decoded); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(decoded.Policy, DefaultResolutionPolicy()) {
		t.Fatalf("effective policy = %#v", decoded.Policy)
	}
}

func TestHelperConfigurationDerivesProductionClosureAndCannotOpenIt(t *testing.T) {
	if Default().Helper.Enabled {
		t.Fatal("helper.enabled default must require explicit opt-in")
	}
	if helper.ProductionDistributionOpen {
		t.Fatal("production Helper distribution opened before release trust gates")
	}
	_, err := Decode(strings.NewReader(`{"schema_version":1,"helper":{"enabled":true}}`))
	if err == nil || !strings.Contains(err.Error(), "helper.enabled") || !strings.Contains(err.Error(), "production distribution is closed") {
		t.Fatalf("closed Helper enable error = %v", err)
	}
}
