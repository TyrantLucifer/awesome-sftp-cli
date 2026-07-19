package docscheck

import (
	"strings"
	"testing"
)

func TestSecurityReviewCoverageNamesEveryREL007Domain(t *testing.T) {
	t.Parallel()
	ledger := readOpenSSHFloorContractFile(t, "../../docs/security/finding-ledger.md")
	assertOpenSSHFloorOrder(t, ledger, []string{
		"## Review coverage",
		"| REL007-CREDENTIAL-AUTH | credentials and authentication provenance | partial |",
		"| REL007-HOST-KEY | host-key verification | reviewed |",
		"| REL007-PATH-RACE | path and filesystem races | reviewed |",
		"| REL007-DELETE-OVERWRITE | delete and overwrite safety | reviewed |",
		"| REL007-HELPER | Helper trust and protocol | partial |",
		"| REL007-DIRECT-TRANSFER | direct-transfer isolation | partial |",
		"| REL007-LOG-REDACTION | diagnostic and support-bundle redaction | partial |",
		"| REL007-RECOVERY | migration, rollback, and recovery | partial |",
	})
}

func TestSecurityReviewCoverageBindsReviewedDomainsToExecutableEvidence(t *testing.T) {
	t.Parallel()
	ledger := readOpenSSHFloorContractFile(t, "../../docs/security/finding-ledger.md")
	assertOpenSSHFloorOrder(t, ledger, []string{
		"`TestRunAskpassWritesOnlyBrokerAnswer`",
		"`TestDocumentRoundTripIsStrictAndSecretFree`",
		"`TestValidateHostAliasRejectsOptionAndControlInjection`",
		"`TestClassifySSHConnectErrorDoesNotRetryAuthHostKeyOrConfig`",
		"`TestPreparePrivateDirectoryRejectsSymlinkComponent`",
		"`TestValidateExecutableRejectsWritableAndSymlinkFiles`",
		"`TestManagerExplicitDeleteRequiresConfirmationAndRejectsRoot`",
		"`TestManagerRecursiveDeleteIsBoundedAndNeverFollowsSymlink`",
		"`TestDetachedSignatureRejectsNonCanonicalBase64`",
		"`TestHelperClientProtocolViolationFailsOnlyHelperSession`",
		"`TestProductionWorkerCannotExecuteFixtureOnlyLevel2Plan`",
		"`TestLevel2FrozenControlPlaneContainsNoCredentialDelegationOrCommandSurface`",
		"`TestPersistentHandlerDropsLexicallyValidButUnregisteredSecretValues`",
		"`TestSupportBundleCompositionDropsSafeShapedSecretCorpus`",
		"`TestRunnerRollsBackWholeMigrationOnStatementFailure`",
		"`TestMarkAttemptFailedPreservesPreAndPostBackupRecoveryEvidence`",
	})
}

func TestSecurityReviewCoverageKeepsIncompleteReleaseBoundariesOpen(t *testing.T) {
	t.Parallel()
	ledger := readOpenSSHFloorContractFile(t, "../../docs/security/finding-ledger.md")
	assertOpenSSHFloorOrder(t, ledger, []string{
		"No unresolved high-severity finding remains inside the reviewed implementation scope.",
		"Production Helper remains **CLOSED**.",
		"Production Level 2 remains **CLOSED**.",
		"Final independent security review remains open.",
	})

	featureMatrix := readOpenSSHFloorContractFile(t, "../../docs/product/feature-matrix.md")
	if !strings.Contains(featureMatrix, "| REL-007 | 最终安全审查 | In Progress |") {
		t.Fatal("REL-007 must remain In Progress while final review boundaries are open")
	}
	if strings.Contains(featureMatrix, "| REL-007 | 最终安全审查 | Verified |") {
		t.Fatal("REL-007 must not be Verified before final independent review")
	}
}
