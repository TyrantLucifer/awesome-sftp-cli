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
		`trusted_parent=/var/lib/amsftp-tests`,
		`trusted_user_root="${trusted_parent}/$(id -u)"`,
		`trusted_client_root="${trusted_user_root}/vendor-sftp-${root_suffix}"`,
		`sudo install -d -o root -g root -m 0755 "${trusted_parent}"`,
		`sudo install -d -o "$(id -u)" -g "$(id -g)" -m 0700 "${trusted_user_root}"`,
		`install -m 0700 "${source_binary}" "${trusted_binary_root}/amsftp"`,
		`AMSFTP_VENDOR_BINARY="${trusted_binary_root}/amsftp"`,
		`TestRealProFTPDVendorSFTPLevel0AndDurableTransfers`,
		`vendor SFTP provider browse and durable transfers passed`,
		`export XDG_STATE_HOME="${trusted_client_root}/xdg-state"`,
		`export AMSFTP_VENDOR_DAEMON_LOG="${trusted_client_root}/xdg-state/amsftp/log/daemon.jsonl"`,
		`"${AMSFTP_VENDOR_BINARY}" daemon start --format json | grep -F '"running":true'`,
		`/usr/bin/env -i \`,
		`AMSFTP_VENDOR_TUI_LOCATION=amsftp-proftpd:/`,
		`AMSFTP_VENDOR_TUI_LOCAL=/tmp`,
		`set stty_init "rows 30 columns 200"`,
		`spawn -noecho /bin/sh -c {exec "$AMSFTP_VENDOR_BINARY" "$AMSFTP_VENDOR_TUI_LOCATION" "$AMSFTP_VENDOR_TUI_LOCAL" 2>"$AMSFTP_VENDOR_TUI_STDERR"}`,
		`vendor-sftp-marker.txt`,
		`if test "${expect_rc}" -ne 0; then`,
		`vendor TUI expect exit: %s`,
		`"${AMSFTP_VENDOR_DAEMON_LOG}"`,
		`sudo sed -n '1,240p' "${diagnostic}" >&2 || true`,
		`vendor SFTP preview-binary TUI browse passed`,
	})
}
