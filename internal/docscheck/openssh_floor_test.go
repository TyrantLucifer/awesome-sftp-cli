package docscheck

import (
	"io/fs"
	"os"
	"strings"
	"testing"
)

func TestUbuntu2204RunsRealOpenSSH89FloorAndRecordsExactVersion(t *testing.T) {
	t.Parallel()
	workflow := readOpenSSHFloorContractFile(t, "../../.github/workflows/ci.yml")
	assertOpenSSHFloorOrder(t, workflow, []string{
		`- name: Run OpenSSH 8.9p1 compatibility floor`,
		`if: matrix.os == 'ubuntu-22.04'`,
		`sudo apt-get update`,
		`expect netcat-openbsd openssh-server`,
		`floor_version="$(/usr/bin/ssh -V 2>&1)"`,
		`OpenSSH_8.9p1\ *)`,
		`"${RUNNER_TEMP}/native/openssh-floor-version"`,
		`AMSFTP_AUTH_BINARY="${RUNNER_TEMP}/native/bin/amsftp"`,
		`AMSFTP_AUTH_EXPECT_OPENSSH_VERSION="${floor_version}"`,
		`bash ./internal/integration/hosted-auth.sh`,
	})
}

func TestHostedAuthenticationMatrixRunsDoctorAgainstSystemOpenSSH(t *testing.T) {
	t.Parallel()
	auth := readOpenSSHFloorContractFile(t, "../integration/hosted-auth.sh")
	assertOpenSSHFloorOrder(t, auth, []string{
		`write_config "Include ${include_directory}/*.conf`,
		`"${installed}" doctor --format json --endpoint auth-include-match`,
		`result["code"] == "openssh" and result["status"] == "pass"`,
		`result["code"] == "endpoint" and result["status"] == "pass"`,
		`authentication doctor OpenSSH and endpoint checks passed`,
		`preflight_sftp_transport auth-include-match`,
	})
}

func TestEveryNativePlatformRunsDoctorDuringHealthyInstalledLifecycle(t *testing.T) {
	t.Parallel()
	workflow := readOpenSSHFloorContractFile(t, "../../.github/workflows/ci.yml")
	assertOpenSSHFloorOrder(t, workflow, []string{
		`- name: Exercise native clean install and uninstall lifecycle`,
		`daemon start --format json`,
		`doctor --format json`,
		`expected_codes = [`,
		`"config", "runtime_directory", "socket", "daemon", "openssh",`,
		`"known_hosts", "database", "cache", "helper", "disk_space",`,
		`"known_hosts": ("skipped", "info", "endpoint_not_requested")`,
		`"database": ("warn", "warning", "database_active")`,
		`assert report["output_version"] == 1`,
		`assert [result["code"] for result in results] == expected_codes`,
		`assert all(result["detail_code"] != "probe_failed" for result in results)`,
		`"remediation": "troubleshooting/" + result["code"]`,
		`native installed doctor checks passed`,
		`daemon stop --confirm stop --format json`,
	})
}

func TestUbuntu2404CapturesExactCurrentOpenSSHVersion(t *testing.T) {
	t.Parallel()
	workflow := readOpenSSHFloorContractFile(t, "../../.github/workflows/ci.yml")
	assertOpenSSHFloorOrder(t, workflow, []string{
		`auth-integration:`,
		`runs-on: ubuntu-24.04`,
		`- name: Capture current OpenSSH version`,
		`current_version="$(/usr/bin/ssh -V 2>&1)"`,
		`OpenSSH_*)`,
		`"${RUNNER_TEMP}/auth-integration/openssh-current-version"`,
	})
}

func TestCurrentOpenSSHVersionIsBoundIntoRealAuthenticationHarness(t *testing.T) {
	t.Parallel()
	workflow := readOpenSSHFloorContractFile(t, "../../.github/workflows/ci.yml")
	assertOpenSSHFloorOrder(t, workflow, []string{
		`- name: Capture current OpenSSH version`,
		`- name: Run real OpenSSH authentication matrix`,
		`AMSFTP_AUTH_EXPECT_OPENSSH_VERSION="$(cat "${RUNNER_TEMP}/auth-integration/openssh-current-version")"`,
		`bash ./internal/integration/hosted-auth.sh`,
	})
}

func TestHostedAuthenticationRejectsCurrentOpenSSHVersionDriftBeforeMutation(t *testing.T) {
	t.Parallel()
	auth := readOpenSSHFloorContractFile(t, "../integration/hosted-auth.sh")
	assertOpenSSHFloorOrder(t, auth, []string{
		`: "${AMSFTP_AUTH_EXPECT_OPENSSH_VERSION:?AMSFTP_AUTH_EXPECT_OPENSSH_VERSION is required}"`,
		`actual_openssh_version="$(/usr/bin/ssh -V 2>&1)"`,
		`test "${actual_openssh_version}" = "${AMSFTP_AUTH_EXPECT_OPENSSH_VERSION}"`,
		`current OpenSSH version binding passed`,
		`for user_name in "${client_user}" "${target_user}" "${mfa_user}"`,
	})
}

func TestOpenSSH89FloorIsEvidenceNotRuntimeVersionRejection(t *testing.T) {
	t.Parallel()
	for _, root := range []string{"../app", "../transport/openssh"} {
		filesystem := os.DirFS(root)
		err := fs.WalkDir(filesystem, ".", func(path string, entry fs.DirEntry, walkErr error) error {
			if walkErr != nil {
				return walkErr
			}
			if entry.IsDir() || !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
				return nil
			}
			raw, err := fs.ReadFile(filesystem, path)
			if err != nil {
				return err
			}
			if strings.Contains(string(raw), "OpenSSH_8.9p1") {
				t.Errorf("runtime source %s/%s hard-codes the tested OpenSSH floor", root, path)
			}
			return nil
		})
		if err != nil {
			t.Fatal(err)
		}
	}
}

func readOpenSSHFloorContractFile(t *testing.T, path string) string {
	t.Helper()
	// #nosec G304 -- callers pass only fixed repository-owned contract paths.
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(raw)
}

func assertOpenSSHFloorOrder(t *testing.T, content string, ordered []string) {
	t.Helper()
	cursor := 0
	for _, fragment := range ordered {
		relative := strings.Index(content[cursor:], fragment)
		if relative < 0 {
			t.Fatalf("OpenSSH floor contract missing ordered fragment %q", fragment)
		}
		cursor += relative + len(fragment)
	}
}
