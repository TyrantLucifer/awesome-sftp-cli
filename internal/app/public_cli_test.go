package app

import (
	"bytes"
	"os"
	"strings"
	"testing"
)

func TestPublicHelpManAndCompletionsShareCommandFacts(t *testing.T) {
	help := Usage()
	man := RenderManPage()
	for _, command := range []string{"--workspace", "config", "completion", "--help", "--version"} {
		if !strings.Contains(help, command) {
			t.Fatalf("help does not contain %q:\n%s", command, help)
		}
		if !strings.Contains(man, command) {
			t.Fatalf("man page does not contain %q:\n%s", command, man)
		}
	}

	for _, shell := range []string{"bash", "zsh", "fish"} {
		completion, err := RenderCompletion(shell)
		if err != nil {
			t.Fatalf("RenderCompletion(%q): %v", shell, err)
		}
		for _, command := range []string{"config", "completion", "validate", "print-effective"} {
			if !strings.Contains(completion, command) {
				t.Fatalf("%s completion does not contain %q:\n%s", shell, command, completion)
			}
		}
		for _, forbidden := range []string{"/usr/bin/ssh", "ProxyCommand", "askpass", "helper serve"} {
			if strings.Contains(completion, forbidden) {
				t.Fatalf("%s completion contains forbidden runtime operation %q", shell, forbidden)
			}
		}
	}
}

func TestCommittedManPageMatchesCommandFacts(t *testing.T) {
	committed, err := os.ReadFile("../../docs/man/amsftp.1")
	if err != nil {
		t.Fatal(err)
	}
	if got, want := string(committed), RenderManPage(); got != want {
		t.Fatalf("docs/man/amsftp.1 drifted from public command facts")
	}
}

func TestRunCompletionPrintsStaticScriptAndClassifiesUnknownShell(t *testing.T) {
	var stdout bytes.Buffer
	if err := runCompletion(t.Context(), []string{"bash"}, &stdout, &bytes.Buffer{}); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(stdout.String(), "complete") {
		t.Fatalf("bash completion = %q", stdout.String())
	}

	err := runCompletion(t.Context(), []string{"powershell"}, &bytes.Buffer{}, &bytes.Buffer{})
	if err == nil || exitCode(err) != ExitUsage {
		t.Fatalf("unknown shell error = %v, exit = %d", err, exitCode(err))
	}
}
