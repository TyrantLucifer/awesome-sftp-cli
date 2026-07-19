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
	for _, command := range []string{"--workspace", "daemon", "job", "helper", "config", "doctor", "support-bundle", "completion", "--help", "--version"} {
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
		for _, command := range []string{"daemon", "start", "status", "stop", "job", "list", "events", "pause", "resume", "cancel", "--limit", "--after", "--format", "--confirm", "helper", "install", "upgrade", "disable", "remove", "--accept-shared-session-stable-home", "config", "doctor", "--endpoint", "support-bundle", "preview", "create", "--consent", "--output", "completion", "validate", "print-effective", "print-effective-keymap", "reset-keymap", "--yes"} {
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
			for _, argument := range fact.arguments {
				if !strings.Contains(completion, argument) {
					t.Fatalf("%s completion drifted from %s argument %q", shell, fact.name, argument)
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

func TestCompletionScriptsDynamicallyQueryHiddenSavedWorkspaces(t *testing.T) {
	shellCondition := map[string]string{
		"bash": `[[ "$previous" == "--workspace" ]]`,
		"zsh":  `[[ "${words[CURRENT-1]}" == "--workspace" ]]`,
		"fish": `test (count (commandline -opc)) -gt 0; and test (commandline -opc)[-1] = --workspace`,
	}
	for _, shell := range []string{"bash", "zsh", "fish"} {
		script, err := RenderCompletion(shell)
		if err != nil {
			t.Fatal(err)
		}
		for _, required := range []string{"--workspace", "completion __workspaces", "2>/dev/null"} {
			if !strings.Contains(script, required) {
				t.Fatalf("%s completion is missing %q:\n%s", shell, required, script)
			}
		}
		if !strings.Contains(script, shellCondition[shell]) {
			t.Fatalf("%s completion is missing guarded workspace position check %q:\n%s", shell, shellCondition[shell], script)
		}
	}
	for _, publicSurface := range []string{Usage(), RenderManPage()} {
		if strings.Contains(publicSurface, "__workspaces") {
			t.Fatalf("hidden completion query leaked into public surface:\n%s", publicSurface)
		}
	}
}
