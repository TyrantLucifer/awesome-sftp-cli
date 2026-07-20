// Package troubleshooting publishes the stable doctor and domain-code map.
package troubleshooting

import (
	"fmt"
	"strings"

	"github.com/TyrantLucifer/awesome-sftp-cli/internal/doctor"
	"github.com/TyrantLucifer/awesome-sftp-cli/internal/domain"
)

type Kind string

const (
	DoctorCheck Kind = "doctor"
	DomainError Kind = "error"
)

type Entry struct {
	Kind    Kind
	Code    string
	Meaning string
	Action  string
}

func Entries() []Entry {
	return []Entry{
		{DoctorCheck, string(doctor.CheckConfig), "Configuration could not be validated or is not usable.", "Run `amsftp config print-effective`; correct the reported configuration source without replacing unrelated settings."},
		{DoctorCheck, string(doctor.CheckRuntimeDirectory), "The per-user runtime directory is absent, unsafe, or inaccessible.", "Restore ownership and private permissions for the reported runtime directory, then rerun `amsftp doctor`."},
		{DoctorCheck, string(doctor.CheckSocket), "The daemon socket is absent, stale, unsafe, or unreachable.", "Check `amsftp daemon status`; start the daemon normally and do not delete an active socket by hand."},
		{DoctorCheck, string(doctor.CheckDaemon), "The daemon health probe did not return a valid healthy response.", "Run `amsftp daemon status`; restart only your own AMSFTP daemon if the status remains unhealthy."},
		{DoctorCheck, string(doctor.CheckOpenSSH), "The required system OpenSSH client is unavailable or unsuitable.", "Verify `/usr/bin/ssh` exists and is executable; AMSFTP does not substitute an unreviewed SSH binary."},
		{DoctorCheck, string(doctor.CheckKnownHosts), "The known-hosts trust store is absent, unsafe, or cannot be inspected.", "Repair the reported ownership or permissions; confirm host-key changes out of band instead of deleting trust data."},
		{DoctorCheck, string(doctor.CheckDatabase), "Persistent database integrity, format, ownership, or permissions failed validation.", "Stop your daemon, preserve a backup of the reported database, and inspect the stable detail code before any recovery action."},
		{DoctorCheck, string(doctor.CheckCache), "Cache metadata, content, ownership, or permissions failed validation.", "Preserve the reported cache state and inspect the detail code; do not rebuild or delete it automatically."},
		{DoctorCheck, string(doctor.CheckHelper), "Helper is absent, disabled, incompatible, or failed its bounded probe.", "Continue with Level 0/relay, or inspect Helper status and trust metadata; production Helper distribution remains closed."},
		{DoctorCheck, string(doctor.CheckDiskSpace), "Available space is below the safe operating threshold or could not be measured.", "Free space on the reported local filesystem, then rerun the check before starting large work."},
		{DoctorCheck, string(doctor.CheckEndpoint), "The optional endpoint probe could not validate connection readiness.", "Verify the endpoint reference with system `ssh`, then rerun doctor for that endpoint; preserve host-key prompts and authentication policy."},
		{DomainError, string(domain.CodeInvalidArgument), "An input violates a stable command or API contract.", "Correct the named argument using `--help`; do not retry unchanged input."},
		{DomainError, string(domain.CodeNotFound), "The requested endpoint, path, Job, or other object does not exist.", "Refresh the relevant view or identifier and verify the parent location before retrying."},
		{DomainError, string(domain.CodeAlreadyExists), "Creation would overwrite an existing object without an explicit conflict choice.", "Choose the intended conflict action or a different destination."},
		{DomainError, string(domain.CodePermissionDenied), "Local or remote authorization denied the operation.", "Verify ownership, mode, ACL, and remote account access; do not bypass the denied boundary."},
		{DomainError, string(domain.CodeAuthRequired), "Authentication is missing, expired, or needs user interaction.", "Authenticate through system OpenSSH/Askpass and retry after successful identity verification."},
		{DomainError, string(domain.CodeTransportInterrupted), "The SSH/SFTP transport ended before the operation completed.", "Reconnect, inspect network and server health, then resume only through the recorded Job state."},
		{DomainError, string(domain.CodeTimeout), "A bounded operation exceeded its deadline.", "Check network/server load and retry with backoff; inspect Job state before repeating a mutating operation."},
		{DomainError, string(domain.CodeUnsupported), "The requested feature or environment is outside the supported capability set.", "Use the documented fallback or a supported environment; do not force an unavailable capability."},
		{DomainError, string(domain.CodeCapabilityLost), "A capability changed after planning or connection setup.", "Refresh capabilities and let AMSFTP replan to a supported route."},
		{DomainError, string(domain.CodeConflict), "Current remote or local state conflicts with the recorded precondition.", "Refresh metadata and make an explicit keep/replace/rename/skip decision."},
		{DomainError, string(domain.CodeResourceExhausted), "A bounded resource such as disk, quota, memory, or concurrency is exhausted.", "Free or raise the named resource within policy, then resume from durable state."},
		{DomainError, string(domain.CodeIntegrityFailed), "Content, metadata, protocol, executable, or persistent-state integrity validation failed.", "Preserve the evidence and stop mutation of the affected object. If v0.1.0 reports `validate authentication helper` from a Homebrew install, stop the daemon with `amsftp daemon stop --confirm stop`, restart it once with `\"$(brew --prefix amsftp)/bin/amsftp\" daemon start`, and upgrade to the latest patch release; do not loosen modes, ACLs, Gatekeeper, or SSH policy."},
		{DomainError, string(domain.CodeCanceled), "The operation was canceled before normal completion.", "Inspect the durable Job/effect state before deciding whether to resume or start new work."},
		{DomainError, string(domain.CodeProtocolIncompatible), "Client, daemon, Helper, or stored format has no compatible protocol/version.", "Use matching released components or migrate through the documented supported path."},
		{DomainError, string(domain.CodeInternal), "A failure was safely mapped without exposing its raw cause.", "Run `amsftp doctor`, preview a support bundle, and report the stable code plus product version."},
	}
}

func Markdown() string {
	var output strings.Builder
	output.WriteString("# Troubleshooting Code Map\n\n")
	output.WriteString("AMSFTP exposes stable doctor and domain codes without persisting raw error causes. Match the exact `kind/code` below, perform only the bounded action, and rerun the original read-only check before mutation. For additional evidence, run `amsftp doctor --format json` and preview a support bundle before creating it. The ordered [operations runbook](../operations/runbook.md) covers escalation, Job recovery, safe fallback, and rollback.\n\n")
	output.WriteString("| Kind | Code | Meaning | Bounded action |\n")
	output.WriteString("|---|---|---|---|\n")
	for _, entry := range Entries() {
		fmt.Fprintf(&output, "| %s | `%s` | %s | %s |\n", entry.Kind, entry.Code, entry.Meaning, entry.Action)
	}
	output.WriteString("\nA support bundle is private, previewed, consent-bound output. Review the manifest and destination before creation; never attach authentication material, private keys, tickets, or unredacted command/file content.\n")
	return output.String()
}
