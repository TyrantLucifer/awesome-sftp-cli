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
	for _, command := range []string{"--workspace", "daemon", "job", "config", "completion", "--help", "--version"} {
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
		for _, command := range []string{"daemon", "start", "status", "stop", "job", "list", "events", "pause", "resume", "cancel", "--limit", "--after", "--format", "--confirm", "config", "completion", "validate", "print-effective", "print-effective-keymap", "reset-keymap", "--yes"} {
			if !strings.Contains(completion, command) {
				t.Fatalf("%s completion does not contain %q:\n%s", shell, command, completion)
			}
		}
		for _, fact := range publicCLIContract {
			if fact.name != "" && !fact.internal && !strings.Contains(completion, fact.name) {
				t.Fatalf("%s completion drifted from command fact %q", shell, fact.name)
			}
			for _, child := range fact.children {
				if !strings.Contains(completion, child) {
					t.Fatalf("%s completion drifted from %s child %q", shell, fact.name, child)
				}
				for _, argument := range fact.childArguments[child] {
					if !strings.Contains(completion, argument) {
						t.Fatalf("%s completion drifted from %s %s argument %q", shell, fact.name, child, argument)
					}
				}
			}
		}
		if strings.Contains(completion, "%!") {
			t.Fatalf("%s completion contains a formatting artifact:\n%s", shell, completion)
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
