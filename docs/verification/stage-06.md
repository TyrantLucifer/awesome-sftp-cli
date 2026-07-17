# Stage 6 Verification Record

- **Status**: In Progress — M6.1 configuration contracts underway
- **Updated**: 2026-07-17
- **Repository root**: `/data00/home/tianchao.thatcher/projects/awsome-sftp-cli`
- **Branch**: `codex/stage6-hardening-release`
- **Delivery PR**: Draft [#6](https://github.com/TyrantLucifer/awsome-sftp-cli/pull/6), title `feat: ship AMSFTP 1.0.0`, base `main`; remains Draft until final gates
- **Sole exact-main baseline**: commit `312bcccbcbd54246bbe5ff9babf4f14560449176`, tree `e0316c286ce11512cb0b92c917fa29b80f9e3305`
- **Baseline Hosted run**: [29579514879](https://github.com/TyrantLucifer/awsome-sftp-cli/actions/runs/29579514879) — exact main commit, completed `success`, 24/24 jobs successful
- **Production Helper distribution / production Level 2**: **CLOSED**
- **Final release/tag/Homebrew state**: not created

This ledger records only observed evidence. Planned commands and acceptance ownership live in the [Stage 6 execution plan](../stages/06-hardening-release-plan.md); a planned gate is not evidence.

## Initial safety checkpoint

| Check | Result |
|---|---|
| exact baseline identity | PASS: local `main`, `origin/main`, and remote `refs/heads/main` all resolved to `312bcccbcbd54246bbe5ff9babf4f14560449176`; tree `e0316c286ce11512cb0b92c917fa29b80f9e3305` |
| fixed branch/tag audit | PASS: remote Stage 6 branch and `v1.0.0` tag were absent before branch creation; `codex/stage6-hardening-release` was created once from the sole baseline |
| exact-main Hosted run | PASS: run `29579514879` completed successfully at the exact baseline SHA; 24 of 24 jobs succeeded |
| baseline local CI | PASS: CI-equivalent `make ci` completed unit/contract/integration, docs, tidy/verify, lint, full race, four fuzz smokes, `govulncheck`, `actionlint`, and darwin/linux × arm64/amd64 builds |
| baseline worktree | PASS: index/worktree clean before branch creation and no product files changed before the baseline gate |
| production credentials | ABSENT by design: no production Helper offline key/custody ceremony or final Developer ID/notary evidence is available; no substitute material was generated |

The local baseline command used the installed SDK at `/data00/home/tianchao.thatcher/sdk/go1.25.7/bin`, `umask 0022`, root-owned persistent fixture root `/var/lib/amsftp-tests/1001`, and external build/coverage directories. It establishes that the frozen baseline is green in this environment; it is not the final required Go 1.26.5 or exact Go 1.25.12 release evidence.

## Canonical baseline command

```sh
env PATH=/data00/home/tianchao.thatcher/sdk/go1.25.7/bin:$PATH AMSFTP_TEST_PERSISTENT_ROOT=/var/lib/amsftp-tests/1001 BUILD_DIR=/tmp/amsftp-stage6-baseline/build COVERAGE_DIR=/tmp/amsftp-stage6-baseline/coverage sh -c 'umask 0022; make ci'
```

Result: **PASS** with exit status 0 on the untouched exact-main tree.

## CI failure classification contract

Every failed job is recorded with exact SHA/tree, workflow/run/job, platform image, failing command/test, retained logs/artifacts, same-SHA companion result, rerun result, and reproducibility. Classification rules:

- **Code**: reproducible locally or on another same-SHA leg; changed Stage 6 area is implicated; assertion/security/compatibility contract fails; or available evidence is ambiguous.
- **Environment/known fixture**: the same SHA passes a comparable companion, logs identify a runner/service/timing condition outside changed behavior, and a targeted no-code/no-assertion/no-timeout-change rerun passes.
- **Infrastructure**: setup, service, quota, network, or runner failure occurs before the product/test contract executes and is confirmed by same-SHA evidence.

Only a subsequent complete exact-candidate matrix can become final release evidence. Reruns do not erase the original failure or its classification.

### Initial Draft-PR CI classification

Plan-only commit `59c0d2003e41a6ec798fc696bcffcd4d72526622` produced failed push run [29581551106](https://github.com/TyrantLucifer/awsome-sftp-cli/actions/runs/29581551106) and PR run [29581560324](https://github.com/TyrantLucifer/awsome-sftp-cli/actions/runs/29581560324) on attempt 1. The failures were outside the documentation-only change:

| Failed leg | Observed failure | Same-SHA companion | No-change rerun |
|---|---|---|---|
| push auth-integration | Stage 1 replacement-daemon wait timed out after the OpenSSH auth cases passed | PR auth-integration passed | push attempt 2 passed |
| push oldstable Ubuntu 24.04 | existing remote-command cancel fixture reached start deadline before command bytes | PR oldstable Ubuntu 24.04 passed | push attempt 2 passed |
| push native Ubuntu 22.04 | existing Level 2 in-flight cancel result timing fixture | PR native Ubuntu 22.04 passed | push attempt 2 passed |
| PR oldstable macOS 15 Intel | existing Helper exact-stderr-cap reader observed zero bytes | push oldstable macOS 15 Intel passed | PR attempt 2 passed |

Both failed-only attempt 2 reruns completed `success` without code, assertion, timeout, or workflow changes. Classification: **environment/known timing fixtures, not introduced code**. Attempts 1 remain preserved and neither attempt is final release evidence.

### M6.1 public-interface CI classification

Public-interface commit `51b7cfc2b5c4c3ce9c6989bb482564d1b096f603` produced successful push run [29582457142](https://github.com/TyrantLucifer/awsome-sftp-cli/actions/runs/29582457142) and failed PR run [29582459680](https://github.com/TyrantLucifer/awsome-sftp-cli/actions/runs/29582459680) on attempt 1. The PR failures were confined to existing macOS timing fixtures:

| Failed leg | Observed failure | Same-SHA companion | No-change rerun |
|---|---|---|---|
| PR oldstable macOS 15 | existing Helper exact-stderr-cap reader observed zero bytes; the existing Stage 2 PTY fixture then observed a transient empty selection before delete confirmation | push oldstable macOS 15 job `87891201150` passed | pending |
| PR native macOS 15 | existing Helper heartbeat-termination test observed process termination before the client failure became visible | push native macOS 15 job `87891201232` passed | pending |

Classification before rerun: **environment/known timing fixtures, not introduced code**. The same SHA passed both directly comparable push jobs, Linux native/oldstable legs, quality, integration, reproducibility, and all changed app/config/docs packages; the failing tests do not exercise the new config/help/man/completion behavior. A failed-only rerun was requested without code, assertion, timeout, or workflow changes. Attempt 1 remains preserved and cannot serve as final release evidence.

## Milestone status

| Milestone | Status | Evidence |
|---|---|---|
| M6.1 configuration/keymap/public interfaces | In Progress | default overlay, versioned config CLI/output, help/man/completion, and validated Normal/Visual keymap RED/GREEN complete; remaining schema, precedence, effective keymap/export, version inventory, and compatibility contracts open |
| M6.2 migration/package/clean machine | Not Started | no implementation evidence |
| M6.3 security/compatibility/diagnostics | Not Started | no implementation evidence |
| M6.4 RC/1.0 | Not Started | no RC, release artifacts, tag, release, or channel evidence |

## Feature status

VIM-013, VIM-014, REL-001, REL-002, and REL-011 are `In Progress` after the versioned-default, validated context-keymap/reserved-action, config command, redacted machine output, stable exit-code, and help/man/completion parity contracts. The other 18 Stage 6-owned rows remain `Planned`: WORK-006, JOB-010, HELP-013, SEC-012, SEC-014, OBS-009, OBS-010, PLAT-003, PLAT-009, REL-003 through REL-010, and REL-012. Shared rows that remain `In Progress` are not advanced by this evidence.

## Exit criteria

All 12 Stage 6 exit criteria remain open. Their milestone ownership and required proof are mapped in the execution plan. No checkbox may be closed from design intent, cross-builds, synthetic substitutes, or credentials that do not exist.

## Protected release boundary

Production Helper distribution and production Level 2 stay **CLOSED**. Opening them requires final release bytes, real protected Developer ID/notary success where applicable, byte-identical accepted notarization input and final tar binary, a production Helper manifest bound only to those final bytes, real offline signature/custody evidence, four-platform manifest-to-tar identity, clean quarantine/Gatekeeper evidence, and all security/compatibility/release gates. Fixture keys, public CI, tabletop ceremonies, or locally generated credentials cannot satisfy this boundary.

## Command ledger

| Candidate | Command/evidence | Result |
|---|---|---|
| exact main | local/origin/remote ref and tree audit | PASS; all identities matched the frozen baseline |
| exact main | Hosted run `29579514879` | PASS; completed success, 24/24 jobs |
| exact main | CI-equivalent baseline `make ci` | PASS; exit 0, no code change required |
| M6.1 config defaults RED | `go test ./internal/config -run='^TestDecode(AppliesDocumentedDefaultsToOmittedFields\|RequiresExplicitSchemaVersion)$' -count=1 -v` | Intended FAIL: minimal explicit-version document produced `ipc.max_frame_bytes must be greater than zero`; missing version remained rejected |
| M6.1 config defaults GREEN | `go test ./internal/config -count=1` | PASS: omitted fields inherit the single documented `Default()` source; explicit schema version remains required; unknown/trailing/invalid explicit values remain rejected |
| M6.1 config integration gate | `go test ./internal/app ./internal/config -count=1`; CI-equivalent `make check` | PASS: application loading regression and complete unit/provider/docs/tidy/verify gate green; config coverage 88.6% |
| M6.1 config CLI/machine-output RED | `go test ./internal/app ./internal/config -run='(TestRunReturnsStableTypedExitCode\|TestWriteRedactedEffectiveConfig)' -count=1` | Intended compile FAIL: no `config` role, typed exit contract, redacted effective writer, or output-version contract existed |
| M6.1 config CLI/machine-output GREEN | `go test ./internal/app ./internal/config -count=1`; focused `-race` on both packages | PASS: `config validate`/`print-effective`, explicit private-file validation, output v1, argv redaction/non-mutation, exit 0–8 snapshot, dispatch and error-channel contracts green |
| M6.1 config CLI complete local gate | CI-equivalent `make check`; `make lint`; `make docs-check`; `git diff --check` | PASS after adding precise `#nosec G302` rationale for the two test-only owner-private 0700 directories; no product permission or lint rule was weakened |
| M6.1 help/man/completion RED | `go test ./internal/app -run='(TestPublicHelpManAndCompletionsShareCommandFacts\|TestRunCompletion\|TestCommittedManPage)' -count=1` | Intended compile FAIL: no shared public CLI facts, man renderer, completion renderer, or completion command existed |
| M6.1 help/man/completion GREEN | focused `go test` and `go test -race ./internal/app -count=1`; `make lint`; `make docs-check` | PASS: ordered facts drive help/man and bash/zsh/fish static completions; committed man parity and forbidden remote/auth operation scans green; lint 0 issues |
| M6.1 keymap registry RED | `go test ./internal/keymap ./internal/tui -run='(TestDefaultSnapshot\|TestNewSupportsContext\|TestNewRejects\|TestTranslateTCellEventWithKeymap)' -count=1` | Intended compile FAIL: no keymap registry, default snapshot, override validation, context lookup, or keymap-aware tcell translation existed |
| M6.1 config keymap RED | `go test ./internal/config -run='TestDecode(AcceptsContextKeymapRemap\|RejectsConflictingOrReservedKeymap)' -count=1` | Intended compile FAIL: schema had no keymap section |
| M6.1 keymap GREEN | `go test ./internal/config ./internal/keymap ./internal/tui ./internal/app -count=1`; `go test -race ./internal/keymap ./internal/tui -count=1`; `make lint`; `make docs-check` | PASS: exact Vim default snapshot, Normal/Visual remap isolation, conflict/unreachable/unknown/count/control/dangerous rejection, schema round-trip, app wiring, default tcell regressions, race, lint 0, and docs green |
| M6.1 keymap complete local gate | CI-equivalent `make check`; `make lint`; `make docs-check`; `git diff --check` | PASS: full unit/provider/integration/docs/tidy/verify gate green; keymap coverage 90.3%, config 87.0%, TUI 69.7%; lint 0 and clean diff check |
