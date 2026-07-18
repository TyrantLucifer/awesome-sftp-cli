package docscheck

import (
	"os"
	"os/exec"
	"strings"
	"testing"
)

func TestHostedAuthenticationMatricesScanCreatedSupportBundles(t *testing.T) {
	t.Parallel()
	for _, path := range []string{
		"../integration/support-bundle-secret-scan.sh",
		"../integration/hosted-auth.sh",
		"../integration/hosted-kerberos.sh",
	} {
		// #nosec G204 -- every path comes from the fixed repository-owned list above.
		if output, err := exec.Command("bash", "-n", path).CombinedOutput(); err != nil {
			t.Fatalf("bash -n %s: %v\n%s", path, err, output)
		}
	}

	helper := readAuthArtifactContractFile(t, "../integration/support-bundle-secret-scan.sh")
	for _, required := range []string{
		"run_support_bundle_secret_scan()",
		"support-bundle preview --format json",
		"support-bundle create --consent",
		"tar -xzf",
		"grep -aFl -f",
		"cmp -s",
		"sha256sum",
		"base64 -w 0",
		"mode=600",
	} {
		if !strings.Contains(helper, required) {
			t.Errorf("support-bundle authentication scanner missing %q", required)
		}
	}

	auth := readAuthArtifactContractFile(t, "../integration/hosted-auth.sh")
	assertAuthArtifactScanOrder(t, auth, "run_case confirm confirm", "run_support_bundle_secret_scan")
	for _, required := range []string{
		"support-bundle-secret-scan.sh",
		`printf '%s\n' "${password}" "${mfa_password}" "${key_passphrase}"`,
		`"${client_home}/.ssh/client_key"`,
		`"${client_home}/.ssh/mfa_key"`,
	} {
		if !strings.Contains(auth, required) {
			t.Errorf("hosted OpenSSH authentication scan missing %q", required)
		}
	}

	kerberos := readAuthArtifactContractFile(t, "../integration/hosted-kerberos.sh")
	assertAuthArtifactScanOrder(t, kerberos, "run_case externally-renewed yes", "run_support_bundle_secret_scan")
	for _, required := range []string{
		"support-bundle-secret-scan.sh",
		`printf '%s\n' "${master_password}" "${client_principal}" "${client_ccache}" "${client_keytab}"`,
		`"${client_ccache}"`,
		`"${client_keytab}"`,
		`"${host_keytab}"`,
	} {
		if !strings.Contains(kerberos, required) {
			t.Errorf("hosted Kerberos scan missing %q", required)
		}
	}
}

func readAuthArtifactContractFile(t *testing.T, path string) string {
	t.Helper()
	// #nosec G304 -- callers pass only fixed repository-owned contract paths.
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(raw)
}

func assertAuthArtifactScanOrder(t *testing.T, script, completedMatrix, scan string) {
	t.Helper()
	matrixIndex := strings.LastIndex(script, completedMatrix)
	scanIndex := strings.LastIndex(script, scan)
	if matrixIndex < 0 || scanIndex <= matrixIndex {
		t.Errorf("artifact scan must run after %q: matrix=%d scan=%d", completedMatrix, matrixIndex, scanIndex)
	}
}
