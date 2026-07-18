package compatibility

import (
	"fmt"
	"strings"
)

type EvidenceStatus string

const (
	NativeTested  EvidenceStatus = "native-tested"
	BuildOnly     EvidenceStatus = "build-only"
	FixtureTested EvidenceStatus = "fixture-tested"
	BestEffort    EvidenceStatus = "best-effort"
	Unsupported   EvidenceStatus = "unsupported"
	Untested      EvidenceStatus = "untested"
)

type EnvironmentCase struct {
	Area     string
	Case     string
	Status   EvidenceStatus
	Behavior string
	Evidence string
}

func EnvironmentMatrix() []EnvironmentCase {
	return []EnvironmentCase{
		{Area: "platform", Case: "macOS 15 arm64", Status: NativeTested, Behavior: "supported", Evidence: "Hosted native install, daemon, Job, uninstall, APFS and current/oldstable gates"},
		{Area: "platform", Case: "macOS 15 amd64", Status: NativeTested, Behavior: "supported", Evidence: "Hosted native install, daemon, Job, uninstall, APFS and current/oldstable gates"},
		{Area: "platform", Case: "Ubuntu 22.04 amd64", Status: NativeTested, Behavior: "supported floor", Evidence: "Hosted native install, OpenSSH 8.9p1, ext4/XFS and current/oldstable gates"},
		{Area: "platform", Case: "Ubuntu 24.04 amd64", Status: NativeTested, Behavior: "supported", Evidence: "Hosted native install, current OpenSSH, ext4/XFS and current/oldstable gates"},
		{Area: "platform", Case: "Linux arm64", Status: BuildOnly, Behavior: "release target pending native smoke", Evidence: "deterministic cross-build and reproducibility only"},
		{Area: "platform", Case: "Windows", Status: Unsupported, Behavior: "fail before runtime", Evidence: "outside the 1.0 product scope"},
		{Area: "openssh", Case: "8.9p1", Status: NativeTested, Behavior: "supported floor, capability results win over version parsing", Evidence: "Ubuntu 22.04 real sshd/SFTP gates"},
		{Area: "openssh", Case: "system current", Status: NativeTested, Behavior: "supported", Evidence: "Ubuntu 24.04 and macOS 15 real sshd/SFTP gates"},
		{Area: "openssh", Case: "Host Include Match ProxyCommand ProxyJump agent ControlMaster", Status: NativeTested, Behavior: "system configuration preserved within fixed safety overrides", Evidence: "real Hosted authentication matrix"},
		{Area: "openssh", Case: "hardware security key", Status: Untested, Behavior: "no product-specific block; no 1.0 compatibility claim yet", Evidence: "release matrix remains open"},
		{Area: "authentication", Case: "MIT Kerberos GSSAPI on Linux", Status: NativeTested, Behavior: "supported without credential delegation", Evidence: "real KDC, GSSAPI-only sshd, expiry and recovery matrix"},
		{Area: "authentication", Case: "Kerberos GSSAPI on macOS", Status: Untested, Behavior: "system OpenSSH path only; no native evidence yet", Evidence: "release matrix remains open"},
		{Area: "sftp", Case: "OpenSSH internal-sftp", Status: NativeTested, Behavior: "supported baseline", Evidence: "real sshd browse, search, transfer and recovery gates"},
		{Area: "sftp", Case: "other standards-compatible servers", Status: BestEffort, Behavior: "standard SFTP baseline; extensions are capability-gated", Evidence: "vendor matrix remains open"},
		{Area: "terminal", Case: "basic ANSI", Status: NativeTested, Behavior: "supported fallback", Evidence: "TUI native PTY and no-color/narrow fixtures"},
		{Area: "terminal", Case: "Kitty 0.47.4 image protocol", Status: NativeTested, Behavior: "proof-gated PNG output", Evidence: "real macOS arm64 active probe"},
		{Area: "terminal", Case: "iTerm2 image protocol", Status: FixtureTested, Behavior: "proof-gated PNG output, otherwise fallback", Evidence: "controlled reply/output fixtures only"},
		{Area: "terminal", Case: "Sixel image protocol", Status: FixtureTested, Behavior: "proof-gated PNG output, otherwise fallback", Evidence: "controlled reply/output fixtures only"},
		{Area: "terminal", Case: "unknown terminal", Status: BestEffort, Behavior: "safe ANSI/metadata fallback without image bytes", Evidence: "none/misprobe/corrupt/oversize fixtures"},
		{Area: "filesystem", Case: "APFS", Status: NativeTested, Behavior: "supported persistent state", Evidence: "macOS native state, ACL, WAL and lifecycle gates"},
		{Area: "filesystem", Case: "ext4 and XFS", Status: NativeTested, Behavior: "supported persistent state", Evidence: "Ubuntu native state, ACL, WAL and XFS ENOSPC gates"},
		{Area: "filesystem", Case: "NFS SMB CIFS network state roots", Status: Unsupported, Behavior: "persistent state fails closed", Evidence: "filesystem-type security policy and negative fixtures"},
		{Area: "helper", Case: "absent", Status: NativeTested, Behavior: "Level 0 browse and standard transfer remain available", Evidence: "native install/auth/recovery gates"},
		{Area: "helper", Case: "wire envelope v1", Status: FixtureTested, Behavior: "version/capability mismatch fails only Helper session", Evidence: "protocol, fuzz and tamper fixtures"},
		{Area: "helper", Case: "production distribution", Status: Untested, Behavior: "CLOSED until protected final-byte trust gates pass", Evidence: "no production credential or artifact evidence"},
		{Area: "direct-transfer", Case: "production Level 2", Status: Untested, Behavior: "CLOSED; bounded relay remains default", Evidence: "real isolated network data-plane proof remains open"},
	}
}

func EnvironmentSnapshotText() string {
	lines := make([]string, 0, len(EnvironmentMatrix()))
	for _, row := range EnvironmentMatrix() {
		lines = append(lines, fmt.Sprintf("%s | %s | %s | %s | %s", row.Area, row.Case, row.Status, row.Behavior, row.Evidence))
	}
	return strings.Join(lines, "\n")
}

func EnvironmentMarkdown() string {
	var output strings.Builder
	output.WriteString("# Environment Compatibility Matrix\n\n")
	output.WriteString("This matrix separates observed evidence from product behavior. `native-tested` means the case ran on the named native environment; `build-only` means only cross-build/reproducibility evidence exists; `fixture-tested` means controlled protocol fixtures exist; `best-effort` has a safe fallback but no complete native claim; `unsupported` fails closed or is outside 1.0 scope; and `untested` carries no 1.0 compatibility claim.\n\n")
	output.WriteString("| Area | Environment or capability | Evidence status | Product behavior | Evidence boundary |\n")
	output.WriteString("|---|---|---|---|---|\n")
	for _, row := range EnvironmentMatrix() {
		fmt.Fprintf(&output, "| %s | %s | %s | %s | %s |\n", row.Area, row.Case, row.Status, row.Behavior, row.Evidence)
	}
	output.WriteString("\nProduction Helper distribution and production Level 2 remain **CLOSED**. Their `untested` rows are explicit release boundaries, not evidence that the rest of the independently tested product is blocked.\n")
	return output.String()
}
