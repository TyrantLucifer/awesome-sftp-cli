package auth

import (
	"strings"
	"testing"
)

func TestOpenSSHEnvironmentReplacesProtectedValuesAndPreservesAuthSources(t *testing.T) {
	base := []string{
		"PATH=/poisoned",
		"SSH_AUTH_SOCK=/private/agent.sock",
		"KRB5CCNAME=FILE:/private/ticket",
		"SSH_ASKPASS=/unsafe/askpass",
		"SSH_ASKPASS_REQUIRE=never",
		"DISPLAY=:9",
		EnvInternalRole + "=client",
		EnvAttemptToken + "=attacker-token",
	}
	token := Token("YWJjZGVmZ2hpamtsbW5vcHFyc3R1dnd4eXphYmNkZWY")
	environment, err := OpenSSHEnvironment(base, "/opt/amsftp/bin/amsftp", token)
	if err != nil {
		t.Fatal(err)
	}
	joined := "\n" + strings.Join(environment, "\n") + "\n"
	for _, preserved := range []string{"PATH=/poisoned", "SSH_AUTH_SOCK=/private/agent.sock", "KRB5CCNAME=FILE:/private/ticket"} {
		if !strings.Contains(joined, "\n"+preserved+"\n") {
			t.Errorf("environment does not preserve %q", preserved)
		}
	}
	// #nosec G101 -- expected environment key/value fixtures contain no credentials.
	for key, value := range map[string]string{
		"SSH_ASKPASS":         "/opt/amsftp/bin/amsftp",
		"SSH_ASKPASS_REQUIRE": "force",
		"DISPLAY":             "amsftp",
		EnvInternalRole:       string(InternalRoleAskpass),
		EnvAttemptToken:       string(token),
	} {
		needle := "\n" + key + "=" + value + "\n"
		if strings.Count(joined, needle) != 1 {
			t.Errorf("environment count for %q = %d, want 1", needle, strings.Count(joined, needle))
		}
	}
	for _, rejected := range []string{"SSH_ASKPASS=/unsafe/askpass", "SSH_ASKPASS_REQUIRE=never", "DISPLAY=:9", EnvInternalRole + "=client", EnvAttemptToken + "=attacker-token"} {
		if strings.Contains(joined, "\n"+rejected+"\n") {
			t.Errorf("environment retains protected value %q", rejected)
		}
	}
}

func TestOpenSSHEnvironmentRejectsUnsafeInputs(t *testing.T) {
	token := Token("YWJjZGVmZ2hpamtsbW5vcHFyc3R1dnd4eXphYmNkZWY")
	for _, executable := range []string{"", "amsftp", "/opt/amsftp\x00bad"} {
		if _, err := OpenSSHEnvironment(nil, executable, token); err == nil {
			t.Fatalf("OpenSSHEnvironment executable %q error = nil", executable)
		}
	}
	if _, err := OpenSSHEnvironment(nil, "/opt/amsftp", "bad-token"); err == nil {
		t.Fatal("OpenSSHEnvironment accepted invalid attempt token")
	}
}
