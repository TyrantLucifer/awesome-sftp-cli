package app

import (
	"fmt"
	"strings"
)

const PublicCLIContractVersion = 1

type cliCommandFact struct {
	name        string
	syntax      string
	description string
	children    []string
	internal    bool
}

var publicCLIContract = []cliCommandFact{
	{syntax: "amsftp [<location> [<location>]]", description: "Open the two-pane client with zero, one, or two locations."},
	{name: "--workspace", syntax: "amsftp --workspace <name>", description: "Open a saved workspace."},
	{name: "config", syntax: "amsftp config <validate|print-effective> [<path>]", description: "Validate configuration or print versioned redacted effective JSON.", children: []string{"validate", "print-effective"}},
	{name: "completion", syntax: "amsftp completion <bash|zsh|fish>", description: "Print a static shell completion script.", children: []string{"bash", "zsh", "fish"}},
	{syntax: "amsftp [client|daemon|askpass|helper] [arguments...]", description: "Run an explicit client or restricted internal role.", internal: true},
	{name: "--help", syntax: "amsftp --help", description: "Print command help."},
	{name: "--version", syntax: "amsftp --version", description: "Print version and build information."},
}

func Usage() string {
	var builder strings.Builder
	builder.WriteString("Usage:\n")
	for _, fact := range publicCLIContract {
		builder.WriteString("  ")
		builder.WriteString(fact.syntax)
		builder.WriteByte('\n')
	}
	return builder.String()
}

func RenderManPage() string {
	var builder strings.Builder
	builder.WriteString(".TH AMSFTP 1\n")
	builder.WriteString(".SH NAME\n")
	builder.WriteString("amsftp \\- Vim-first two-pane SFTP commander\n")
	builder.WriteString(".SH SYNOPSIS\n")
	for _, fact := range publicCLIContract {
		builder.WriteString(".TP\n\\fB")
		builder.WriteString(fact.syntax)
		builder.WriteString("\\fR\n")
		builder.WriteString(fact.description)
		builder.WriteByte('\n')
	}
	builder.WriteString(".SH DESCRIPTION\n")
	builder.WriteString("AMSFTP delegates SSH authentication and host policy to the validated system OpenSSH, keeps standard SFTP as the baseline, and requires explicit confirmation for destructive operations.\n")
	builder.WriteString(".SH EXIT STATUS\n")
	builder.WriteString("0 success; 1 internal; 2 usage; 3 configuration; 4 authentication; 5 network; 6 conflict; 7 partial completion; 8 user cancellation.\n")
	builder.WriteString(".SH FILES\n")
	builder.WriteString("See the AMSFTP configuration reference for platform paths, schema version, precedence, and redaction.\n")
	return builder.String()
}

func RenderCompletion(shell string) (string, error) {
	top := completionWords(publicCLIContract)
	config := strings.Join(childrenFor("config"), " ")
	completion := strings.Join(childrenFor("completion"), " ")
	switch shell {
	case "bash":
		return fmt.Sprintf(`_amsftp() {
  local current previous
  current="${COMP_WORDS[COMP_CWORD]}"
  previous="${COMP_WORDS[COMP_CWORD-1]}"
  case "$previous" in
    config) COMPREPLY=( $(compgen -W %q -- "$current") ) ;;
    completion) COMPREPLY=( $(compgen -W %q -- "$current") ) ;;
    *) COMPREPLY=( $(compgen -W %q -- "$current") ) ;;
  esac
}
complete -F _amsftp amsftp
`, config, completion, top), nil
	case "zsh":
		return fmt.Sprintf(`#compdef amsftp
_amsftp() {
  local -a commands config_commands completion_commands
  commands=(%s)
  config_commands=(%s)
  completion_commands=(%s)
  _arguments '1:command:($commands)' '2:subcommand:($config_commands $completion_commands)' '*:location or path:_files'
}
_amsftp
`, top, config, completion), nil
	case "fish":
		var builder strings.Builder
		for _, word := range strings.Fields(top) {
			fmt.Fprintf(&builder, "complete -c amsftp -n '__fish_use_subcommand' -a %q\n", word)
		}
		for _, word := range childrenFor("config") {
			fmt.Fprintf(&builder, "complete -c amsftp -n '__fish_seen_subcommand_from config' -a %q\n", word)
		}
		for _, word := range childrenFor("completion") {
			fmt.Fprintf(&builder, "complete -c amsftp -n '__fish_seen_subcommand_from completion' -a %q\n", word)
		}
		return builder.String(), nil
	default:
		return "", fmt.Errorf("unsupported completion shell %q; want bash, zsh, or fish", shell)
	}
}

func completionWords(facts []cliCommandFact) string {
	words := make([]string, 0, len(facts))
	for _, fact := range facts {
		if fact.name != "" && !fact.internal {
			words = append(words, fact.name)
		}
	}
	return strings.Join(words, " ")
}

func childrenFor(name string) []string {
	for _, fact := range publicCLIContract {
		if fact.name == name {
			return append([]string(nil), fact.children...)
		}
	}
	return nil
}
