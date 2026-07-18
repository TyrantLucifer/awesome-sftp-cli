# Stage 6 Security Finding Ledger

This ledger records concrete findings discovered during Stage 6 and their evidence-backed disposition. It is not a final security-review approval: unexecuted review areas are listed separately so absence of a finding row cannot be read as completed coverage.

| ID | Severity | Status | Boundary | Finding | Disposition | Evidence | Owner |
|---|---|---|---|---|---|---|---|
| M6-SEC-001 | high | fixed | persistent diagnostics and support export | Lexically valid, safe-shaped values could carry usernames, hostnames, private-key text, Askpass answers, Kerberos tickets, environment secrets, command arguments, or file content into persistent JSON or a support archive. | Persistent component/event/error values now require reviewed semantic registries; every exported structured field uses a domain allowlist/registry and unknown safe-shaped values become `<redacted>`. Expanded archives are scanned with an eight-class corpus. | `ba334b8d8968f5b09c91c0185996994b9a307ff4` plus focused race/vet and dual-Go full local gates | M6.3 diagnostics/support owner |

## Review coverage

`reviewed` means the current implementation and its cited negative evidence were inspected inside the stated boundary. `partial` means the current implementation has reviewed controls and executable evidence, but a production, platform, artifact, or final-review boundary remains open. Neither status means release approval.

| ID | Review domain | Status | Reviewed controls and executable evidence | Remaining boundary |
|---|---|---|---|---|
| REL007-CREDENTIAL-AUTH | credentials and authentication provenance | partial | Askpass writes only the broker answer and workspace persistence excludes secrets: `TestRunAskpassWritesOnlyBrokerAnswer`, `TestDocumentRoundTripIsStrictAndSecretFree`. Dual-workflow OpenSSH/Askpass and renewed Kerberos artifacts pass expanded support-bundle scans. | Real hardware security-key authentication, macOS Kerberos, and protected final artifacts remain open. |
| REL007-HOST-KEY | host-key verification | reviewed | Alias option/control injection is rejected and host-key/config failures never enter authentication retry: `TestValidateHostAliasRejectsOptionAndControlInjection`, `TestClassifySSHConnectErrorDoesNotRetryAuthHostKeyOrConfig`. | Final independent review remains open; no broader host-key support claim is inferred. |
| REL007-PATH-RACE | path and filesystem races | reviewed | Owner-private state preparation rejects symlink components and executable validation rejects writable/symlink targets: `TestPreparePrivateDirectoryRejectsSymlinkComponent`, `TestValidateExecutableRejectsWritableAndSymlinkFiles`. | Final independent review remains open. |
| REL007-DELETE-OVERWRITE | delete and overwrite safety | reviewed | Explicit deletion requires confirmation and rejects root; recursive deletion is bounded and does not follow symlinks: `TestManagerExplicitDeleteRequiresConfirmationAndRejectsRoot`, `TestManagerRecursiveDeleteIsBoundedAndNeverFollowsSymlink`. | Final independent review remains open. |
| REL007-HELPER | Helper trust and protocol | partial | Detached signatures reject non-canonical encodings and protocol violations fail only the Helper session: `TestDetachedSignatureRejectsNonCanonicalBase64`, `TestHelperClientProtocolViolationFailsOnlyHelperSession`. | Production trust, final bytes, signing/notarization, distribution, rollback, and recovery remain closed. |
| REL007-DIRECT-TRANSFER | direct-transfer isolation | partial | Production workers cannot execute the fixture-only Level 2 plan and the frozen control plane contains no credential delegation or command surface: `TestProductionWorkerCannotExecuteFixtureOnlyLevel2Plan`, `TestLevel2FrozenControlPlaneContainsNoCredentialDelegationOrCommandSurface`. | Production isolated-network identity/authentication, containment, artifact integrity, cancellation, negative routing, and relay fallback remain closed. |
| REL007-LOG-REDACTION | diagnostic and support-bundle redaction | partial | Persistent logs drop lexically valid unregistered secret values and support bundles drop the safe-shaped secret corpus: `TestPersistentHandlerDropsLexicallyValidButUnregisteredSecretValues`, `TestSupportBundleCompositionDropsSafeShapedSecretCorpus`. | Production package/Helper artifacts cannot be scanned until protected final bytes exist. |
| REL007-RECOVERY | migration, rollback, and recovery | partial | Failed migrations roll back atomically and failed attempts preserve before/after backup evidence: `TestRunnerRollsBackWholeMigrationOnStatementFailure`, `TestMarkAttemptFailedPreservesPreAndPostBackupRecoveryEvidence`. | Final public-channel withdrawal, production Helper recovery, and release-candidate rehearsal remain open. |

No unresolved high-severity finding remains inside the reviewed implementation scope. This is a scoped statement backed by the table above, not a final release-wide security approval.

## Open release boundaries

- Production Helper final-byte trust, signing/notarization credentials, offline-key procedure, real artifact secret scan, distribution, rollback, and recovery remain open. Production Helper remains **CLOSED**.
- Production Level 2 isolated-network identity/authentication, containment, integrity, cancellation, negative routing, and relay fallback remain open. Production Level 2 remains **CLOSED**.
- Real hardware security-key authentication, macOS Kerberos, vendor SFTP servers, Linux arm64 native smoke, and a physical 100 GiB execution remain outside current native evidence.
- Final independent security review remains open. Independent correctness review, final finding disposition, project LICENSE owner/legal choice, and protected public-channel evidence also remain open.

No open area above is claimed as verified, supported, or release-approved by this ledger. REL-007 remains `In Progress` until the final independent review and release-wide disposition are complete.
