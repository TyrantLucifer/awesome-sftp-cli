package compatibility

import (
	"os"
	"strings"
	"testing"
)

func TestEnvironmentMatrixFreezesEvidenceStatusesAndClaims(t *testing.T) {
	want := strings.TrimSpace(`
platform | macOS 15 arm64 | native-tested | supported | Hosted native install, daemon, Job, uninstall, APFS and current/oldstable gates
platform | macOS 15 amd64 | native-tested | supported | Hosted native install, daemon, Job, uninstall, APFS and current/oldstable gates
platform | Ubuntu 22.04 amd64 | native-tested | supported floor | Hosted /usr/bin/ssh reports OpenSSH_8.9p1; native doctor, full authentication/support scan, ext4/XFS and current/oldstable gates
platform | Ubuntu 24.04 amd64 | native-tested | supported | Hosted native install, current OpenSSH, ext4/XFS and current/oldstable gates
platform | Linux arm64 | build-only | release target pending native smoke | deterministic cross-build and reproducibility only
platform | Windows | unsupported | fail before runtime | outside the 1.0 product scope
openssh | 8.9p1 | native-tested | supported floor, capability results win over version parsing | Ubuntu 22.04 real sshd/SFTP gates
openssh | system current | native-tested | supported | Ubuntu 24.04 and macOS 15 real sshd/SFTP gates
openssh | Host Include Match ProxyCommand ProxyJump agent ControlMaster | native-tested | system configuration preserved within fixed safety overrides | real Hosted authentication matrix
openssh | hardware security key | untested | no product-specific block; no 1.0 compatibility claim yet | release matrix remains open
authentication | MIT Kerberos GSSAPI on Linux | native-tested | supported without credential delegation | real KDC, GSSAPI-only sshd, expiry and recovery matrix
authentication | Kerberos GSSAPI on macOS | untested | system OpenSSH path only; no native evidence yet | release matrix remains open
sftp | OpenSSH internal-sftp | native-tested | supported baseline | real sshd browse, search, transfer and recovery gates
sftp | other standards-compatible servers | best-effort | standard SFTP baseline; extensions are capability-gated | vendor matrix remains open
terminal | basic ANSI | native-tested | supported fallback | TUI native PTY and no-color/narrow fixtures
terminal | Kitty 0.47.4 image protocol | native-tested | proof-gated PNG output | real macOS arm64 active probe
terminal | iTerm2 image protocol | fixture-tested | proof-gated PNG output, otherwise fallback | controlled reply/output fixtures only
terminal | Sixel image protocol | fixture-tested | proof-gated PNG output, otherwise fallback | controlled reply/output fixtures only
terminal | unknown terminal | best-effort | safe ANSI/metadata fallback without image bytes | none/misprobe/corrupt/oversize fixtures
filesystem | APFS | native-tested | supported persistent state | macOS native state, ACL, WAL and lifecycle gates
filesystem | ext4 and XFS | native-tested | supported persistent state | Ubuntu native state, ACL, WAL and XFS ENOSPC gates
filesystem | NFS SMB CIFS network state roots | unsupported | persistent state fails closed | filesystem-type security policy and negative fixtures
helper | absent | native-tested | Level 0 browse and standard transfer remain available | native install/auth/recovery gates
helper | wire envelope v1 | fixture-tested | version/capability mismatch fails only Helper session | protocol, fuzz and tamper fixtures
helper | production distribution | untested | CLOSED until protected final-byte trust gates pass | no production credential or artifact evidence
direct-transfer | production Level 2 | untested | CLOSED; bounded relay remains default | real isolated network data-plane proof remains open`)
	if got := EnvironmentSnapshotText(); got != want {
		t.Fatalf("environment compatibility matrix changed:\n%s\nwant:\n%s", got, want)
	}

	seen := map[EvidenceStatus]bool{}
	for _, row := range EnvironmentMatrix() {
		seen[row.Status] = true
	}
	for _, required := range []EvidenceStatus{NativeTested, BuildOnly, FixtureTested, BestEffort, Unsupported, Untested} {
		if !seen[required] {
			t.Errorf("compatibility matrix does not distinguish %q", required)
		}
	}
}

func TestCommittedEnvironmentCompatibilityMatrixMatchesRegistry(t *testing.T) {
	raw, err := os.ReadFile("../../docs/product/environment-compatibility.md")
	if err != nil {
		t.Fatal(err)
	}
	if got, want := string(raw), EnvironmentMarkdown(); got != want {
		t.Fatal("committed environment compatibility matrix drifted from registry")
	}
}
