package docscheck

import "testing"

func TestUbuntu2404CapturesExactProFTPDVendorSFTPVersion(t *testing.T) {
	t.Parallel()
	workflow := readOpenSSHFloorContractFile(t, "../../.github/workflows/ci.yml")
	assertOpenSSHFloorOrder(t, workflow, []string{
		`auth-integration:`,
		`runs-on: ubuntu-24.04`,
		`proftpd-core \`,
		`proftpd-mod-crypto`,
		`- name: Capture current ProFTPD vendor SFTP version`,
		`proftpd_version="$(/usr/sbin/proftpd -v 2>&1)"`,
		`ProFTPD\ Version\ *)`,
		`dpkg-query -W -f='${Package}=${Version}\n' proftpd-core proftpd-mod-crypto`,
		`"${RUNNER_TEMP}/auth-integration/proftpd-current-version"`,
	})
}

func TestProFTPDVersionIsBoundIntoRealVendorSFTPHarness(t *testing.T) {
	t.Parallel()
	workflow := readOpenSSHFloorContractFile(t, "../../.github/workflows/ci.yml")
	assertOpenSSHFloorOrder(t, workflow, []string{
		`- name: Capture current ProFTPD vendor SFTP version`,
		`- name: Run real ProFTPD vendor SFTP matrix`,
		`AMSFTP_VENDOR_SFTP_EXPECT_VERSION_FILE="${RUNNER_TEMP}/auth-integration/proftpd-current-version"`,
		`AMSFTP_VENDOR_SFTP_ROOT="/tmp/amsftp-vendor-sftp-${GITHUB_RUN_ID}-${GITHUB_RUN_ATTEMPT}"`,
		`bash ./internal/integration/hosted-vendor-sftp.sh`,
	})
}

func TestHostedVendorSFTPRejectsVersionDriftBeforeMutation(t *testing.T) {
	t.Parallel()
	harness := readOpenSSHFloorContractFile(t, "../integration/hosted-vendor-sftp.sh")
	assertOpenSSHFloorOrder(t, harness, []string{
		`: "${AMSFTP_VENDOR_SFTP_EXPECT_VERSION_FILE:?AMSFTP_VENDOR_SFTP_EXPECT_VERSION_FILE is required}"`,
		`actual_version_file="${root}/proftpd-actual-version"`,
		`/usr/sbin/proftpd -v 2>&1`,
		`dpkg-query -W -f='${Package}=${Version}\n' proftpd-core proftpd-mod-crypto`,
		`cmp -s "${AMSFTP_VENDOR_SFTP_EXPECT_VERSION_FILE}" "${actual_version_file}"`,
		`current ProFTPD vendor SFTP version binding passed`,
		`if id "${vendor_user}"`,
	})
}

func TestHostedVendorSFTPRunsProviderAndPreviewBinary(t *testing.T) {
	t.Parallel()
	harness := readOpenSSHFloorContractFile(t, "../integration/hosted-vendor-sftp.sh")
	assertOpenSSHFloorOrder(t, harness, []string{
		`TestRealProFTPDVendorSFTPLevel0AndDurableTransfers`,
		`vendor SFTP provider browse and durable transfers passed`,
		`set stty_init "rows 30 columns 200"`,
		`spawn -noecho /bin/sh -c {exec "$AMSFTP_VENDOR_BINARY" "amsftp-proftpd:/" 2>"$AMSFTP_VENDOR_TUI_STDERR"}`,
		`vendor-sftp-marker.txt`,
		`if test "${expect_rc}" -ne 0; then`,
		`vendor SFTP preview-binary TUI browse passed`,
	})
}
