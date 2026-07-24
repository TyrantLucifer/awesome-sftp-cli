package docscheck

import "testing"

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
