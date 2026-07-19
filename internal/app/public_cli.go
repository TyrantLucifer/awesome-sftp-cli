package app

import (
	"fmt"
	"strings"
)

const PublicCLIContractVersion = 1

type cliCommandFact struct {
	name           string
	syntax         string
	description    string
	children       []string
	arguments      []string
	childArguments map[string][]string
	internal       bool
}

var publicCLIContract = []cliCommandFact{
	{syntax: "amsftp [<location> [<location>]]", description: "Open the two-pane client with zero, one, or two locations."},
	{name: "--workspace", syntax: "amsftp --workspace <name>", description: "Open a saved workspace."},
	{name: "daemon", syntax: "amsftp daemon <start|status> [--format human|json] | amsftp daemon stop --confirm stop [--format human|json]", description: "Start, inspect, or explicitly stop the local daemon.", children: []string{"start", "status", "stop"}, childArguments: map[string][]string{"start": {"--format"}, "status": {"--format"}, "stop": {"--format", "--confirm"}}},
	{name: "job", syntax: "amsftp job <list|events|pause|resume|cancel> [arguments]", description: "Query or control durable Jobs through the local daemon; cancellation requires exact Job ID confirmation.", children: []string{"list", "events", "pause", "resume", "cancel"}, childArguments: map[string][]string{"list": {"--limit", "--format"}, "events": {"--after", "--limit", "--format"}, "pause": {"--format"}, "resume": {"--format"}, "cancel": {"--format", "--confirm"}}},
	{name: "helper", syntax: "amsftp helper <status|disable> <SSH-host> [--format human|json] | amsftp helper <install|upgrade|remove> <SSH-host> --accept-shared-session-stable-home [--format human|json]", description: "Inspect or disable Helper state, or request release-admitted install/upgrade/exact removal; lifecycle remains fail-closed until protected composition exists.", children: []string{"status", "install", "upgrade", "disable", "remove"}, childArguments: map[string][]string{"status": {"--format"}, "install": {"--accept-shared-session-stable-home", "--format"}, "upgrade": {"--accept-shared-session-stable-home", "--format"}, "disable": {"--format"}, "remove": {"--accept-shared-session-stable-home", "--format"}}},
	{name: "config", syntax: "amsftp config <validate|print-effective|print-effective-keymap|reset-keymap> [arguments]", description: "Validate configuration, print versioned effective output, or explicitly reset keymap overrides.", children: []string{"validate", "print-effective", "print-effective-keymap", "reset-keymap"}, childArguments: map[string][]string{"reset-keymap": {"--yes"}}},
	{name: "doctor", syntax: "amsftp doctor [--endpoint <SSH-host>] [--format human|json]", description: "Run bounded read-only local checks and optionally test one SSH endpoint without prompting for credentials.", arguments: []string{"--endpoint", "--format"}},
	{name: "support-bundle", syntax: "amsftp support-bundle preview [--format human|json] | amsftp support-bundle create --consent <sha256> --output <absolute-path> [--format human|json]", description: "Preview the exact reviewed diagnostic archive, then explicitly publish it to a private no-replace local file.", children: []string{"preview", "create"}, childArguments: map[string][]string{"preview": {"--format"}, "create": {"--consent", "--output", "--format"}}},
	{name: "completion", syntax: "amsftp completion <bash|zsh|fish>", description: "Print a shell completion script with saved-workspace completion.", children: []string{"bash", "zsh", "fish"}},
	{syntax: "amsftp [client|askpass|helper] [arguments...]", description: "Run an explicit client or restricted internal role.", internal: true},
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
	configReset := strings.Join(childArgumentsFor("config", "reset-keymap"), " ")
	daemon := strings.Join(childrenFor("daemon"), " ")
	daemonStart := strings.Join(childArgumentsFor("daemon", "start"), " ")
	daemonStatus := strings.Join(childArgumentsFor("daemon", "status"), " ")
	daemonStop := strings.Join(childArgumentsFor("daemon", "stop"), " ")
	job := strings.Join(childrenFor("job"), " ")
	jobList := strings.Join(childArgumentsFor("job", "list"), " ")
	jobEvents := strings.Join(childArgumentsFor("job", "events"), " ")
	jobPause := strings.Join(childArgumentsFor("job", "pause"), " ")
	jobResume := strings.Join(childArgumentsFor("job", "resume"), " ")
	jobCancel := strings.Join(childArgumentsFor("job", "cancel"), " ")
	helper := strings.Join(childrenFor("helper"), " ")
	helperStatus := strings.Join(childArgumentsFor("helper", "status"), " ")
	helperInstall := strings.Join(childArgumentsFor("helper", "install"), " ")
	helperUpgrade := strings.Join(childArgumentsFor("helper", "upgrade"), " ")
	helperDisable := strings.Join(childArgumentsFor("helper", "disable"), " ")
	helperRemove := strings.Join(childArgumentsFor("helper", "remove"), " ")
	doctor := strings.Join(argumentsFor("doctor"), " ")
	supportBundle := strings.Join(childrenFor("support-bundle"), " ")
	supportBundlePreview := strings.Join(childArgumentsFor("support-bundle", "preview"), " ")
	supportBundleCreate := strings.Join(childArgumentsFor("support-bundle", "create"), " ")
	completion := strings.Join(childrenFor("completion"), " ")
	switch shell {
	case "bash":
		return fmt.Sprintf(`_amsftp() {
  local current previous
  current="${COMP_WORDS[COMP_CWORD]}"
  previous="${COMP_WORDS[COMP_CWORD-1]}"
  if [[ "$previous" == "--workspace" ]]; then
    COMPREPLY=( $(compgen -W "$(command amsftp completion __workspaces 2>/dev/null)" -- "$current") )
    return
  fi
  case "$previous" in
    config) COMPREPLY=( $(compgen -W %q -- "$current") ) ;;
    daemon) COMPREPLY=( $(compgen -W %q -- "$current") ) ;;
    job) COMPREPLY=( $(compgen -W %q -- "$current") ) ;;
    helper) COMPREPLY=( $(compgen -W %q -- "$current") ) ;;
	doctor) COMPREPLY=( $(compgen -W %q -- "$current") ) ;;
	support-bundle) COMPREPLY=( $(compgen -W %q -- "$current") ) ;;
    completion) COMPREPLY=( $(compgen -W %q -- "$current") ) ;;
    reset-keymap) COMPREPLY=( $(compgen -W %q -- "$current") ) ;;
    start) COMPREPLY=( $(compgen -W %q -- "$current") ) ;;
    status) COMPREPLY=( $(compgen -W %q -- "$current") ) ;;
	install) COMPREPLY=( $(compgen -W %q -- "$current") ) ;;
	upgrade) COMPREPLY=( $(compgen -W %q -- "$current") ) ;;
	disable) COMPREPLY=( $(compgen -W %q -- "$current") ) ;;
	remove) COMPREPLY=( $(compgen -W %q -- "$current") ) ;;
    stop) COMPREPLY=( $(compgen -W %q -- "$current") ) ;;
    list) COMPREPLY=( $(compgen -W %q -- "$current") ) ;;
    events) COMPREPLY=( $(compgen -W %q -- "$current") ) ;;
    pause) COMPREPLY=( $(compgen -W %q -- "$current") ) ;;
    resume) COMPREPLY=( $(compgen -W %q -- "$current") ) ;;
	cancel) COMPREPLY=( $(compgen -W %q -- "$current") ) ;;
	preview) COMPREPLY=( $(compgen -W %q -- "$current") ) ;;
	create) COMPREPLY=( $(compgen -W %q -- "$current") ) ;;
    *) COMPREPLY=( $(compgen -W %q -- "$current") ) ;;
  esac
}
complete -F _amsftp amsftp
`, config, daemon, job, helper, doctor, supportBundle, completion, configReset, daemonStart, helperStatus, helperInstall, helperUpgrade, helperDisable, helperRemove, daemonStop, jobList, jobEvents, jobPause, jobResume, jobCancel, supportBundlePreview, supportBundleCreate, top), nil
	case "zsh":
		return fmt.Sprintf(`#compdef amsftp
_amsftp() {
  local -a commands config_commands daemon_commands job_commands helper_commands doctor_arguments support_bundle_commands completion_commands
  commands=(%s)
  config_commands=(%s)
  daemon_commands=(%s)
  job_commands=(%s)
  helper_commands=(%s)
	doctor_arguments=(%s)
	support_bundle_commands=(%s)
  completion_commands=(%s)
  if [[ "${words[CURRENT-1]}" == "--workspace" ]]; then
    local -a workspace_names
    workspace_names=("${(@f)$(command amsftp completion __workspaces 2>/dev/null)}")
    _describe 'workspace' workspace_names
    return
  fi
  _arguments '1:command:($commands)' '2:subcommand or argument:($config_commands $daemon_commands $job_commands $helper_commands $doctor_arguments $support_bundle_commands $completion_commands)' '3:argument:(%s %s %s %s %s %s %s %s %s %s %s %s %s %s %s %s)' '*:location or path:_files'
}
_amsftp
`, top, config, daemon, job, helper, doctor, supportBundle, completion, configReset, daemonStart, daemonStatus, daemonStop, jobList, jobEvents, jobPause, jobResume, jobCancel, helperStatus, helperInstall, helperUpgrade, helperDisable, helperRemove, supportBundlePreview, supportBundleCreate), nil
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
		for _, word := range childrenFor("daemon") {
			fmt.Fprintf(&builder, "complete -c amsftp -n '__fish_seen_subcommand_from daemon' -a %q\n", word)
		}
		for _, child := range childrenFor("daemon") {
			for _, word := range childArgumentsFor("daemon", child) {
				fmt.Fprintf(&builder, "complete -c amsftp -n '__fish_seen_subcommand_from %s' -a %q\n", child, word)
			}
		}
		for _, word := range childrenFor("job") {
			fmt.Fprintf(&builder, "complete -c amsftp -n '__fish_seen_subcommand_from job' -a %q\n", word)
		}
		for _, child := range childrenFor("job") {
			for _, word := range childArgumentsFor("job", child) {
				fmt.Fprintf(&builder, "complete -c amsftp -n '__fish_seen_subcommand_from %s' -a %q\n", child, word)
			}
		}
		for _, word := range childrenFor("helper") {
			fmt.Fprintf(&builder, "complete -c amsftp -n '__fish_seen_subcommand_from helper' -a %q\n", word)
		}
		for _, word := range argumentsFor("doctor") {
			fmt.Fprintf(&builder, "complete -c amsftp -n '__fish_seen_subcommand_from doctor' -a %q\n", word)
		}
		for _, word := range childrenFor("support-bundle") {
			fmt.Fprintf(&builder, "complete -c amsftp -n '__fish_seen_subcommand_from support-bundle' -a %q\n", word)
		}
		for _, child := range childrenFor("support-bundle") {
			for _, word := range childArgumentsFor("support-bundle", child) {
				fmt.Fprintf(&builder, "complete -c amsftp -n '__fish_seen_subcommand_from %s' -a %q\n", child, word)
			}
		}
		for _, child := range childrenFor("helper") {
			for _, word := range childArgumentsFor("helper", child) {
				fmt.Fprintf(&builder, "complete -c amsftp -n '__fish_seen_subcommand_from %s' -a %q\n", child, word)
			}
		}
		for _, word := range childArgumentsFor("config", "reset-keymap") {
			fmt.Fprintf(&builder, "complete -c amsftp -n '__fish_seen_subcommand_from reset-keymap' -a %q\n", word)
		}
		builder.WriteString("complete -c amsftp -n 'test (count (commandline -opc)) -gt 0; and test (commandline -opc)[-1] = --workspace' -a '(command amsftp completion __workspaces 2>/dev/null)'\n")
		return builder.String(), nil
	default:
		return "", fmt.Errorf("unsupported completion shell %q; want bash, zsh, or fish", shell)
	}
}

func argumentsFor(name string) []string {
	for _, fact := range publicCLIContract {
		if fact.name == name {
			return append([]string(nil), fact.arguments...)
		}
	}
	return nil
}

func childArgumentsFor(name, child string) []string {
	for _, fact := range publicCLIContract {
		if fact.name == name {
			return append([]string(nil), fact.childArguments[child]...)
		}
	}
	return nil
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
