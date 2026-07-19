package docscheck

import "testing"

func TestUbuntu2404CapturesExactMITKerberosVersion(t *testing.T) {
	t.Parallel()
	workflow := readOpenSSHFloorContractFile(t, "../../.github/workflows/ci.yml")
	assertOpenSSHFloorOrder(t, workflow, []string{
		`auth-integration:`,
		`runs-on: ubuntu-24.04`,
		`- name: Capture current MIT Kerberos version`,
		`kerberos_version="$(/usr/bin/klist -V 2>&1)"`,
		`Kerberos\ 5\ version\ *)`,
		`"${RUNNER_TEMP}/auth-integration/kerberos-current-version"`,
	})
}

func TestMITKerberosVersionIsBoundIntoRealAuthenticationHarness(t *testing.T) {
	t.Parallel()
	workflow := readOpenSSHFloorContractFile(t, "../../.github/workflows/ci.yml")
	assertOpenSSHFloorOrder(t, workflow, []string{
		`- name: Capture current MIT Kerberos version`,
		`- name: Run real MIT Kerberos/GSSAPI matrix`,
		`AMSFTP_KERBEROS_EXPECT_VERSION="$(cat "${RUNNER_TEMP}/auth-integration/kerberos-current-version")"`,
		`bash ./internal/integration/hosted-kerberos.sh`,
	})
}

func TestHostedKerberosRejectsVersionDriftBeforeMutation(t *testing.T) {
	t.Parallel()
	kerberos := readOpenSSHFloorContractFile(t, "../integration/hosted-kerberos.sh")
	assertOpenSSHFloorOrder(t, kerberos, []string{
		`: "${AMSFTP_KERBEROS_EXPECT_VERSION:?AMSFTP_KERBEROS_EXPECT_VERSION is required}"`,
		`actual_kerberos_version="$(/usr/bin/klist -V 2>&1)"`,
		`test "${actual_kerberos_version}" = "${AMSFTP_KERBEROS_EXPECT_VERSION}"`,
		`current MIT Kerberos version binding passed`,
		`if id "${client_user}"`,
	})
}
