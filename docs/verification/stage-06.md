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

## Milestone status

| Milestone | Status | Evidence |
|---|---|---|
| M6.1 configuration/keymap/public interfaces | In Progress | first config default-overlay RED/GREEN complete; remaining schema, precedence, redaction, keymap, CLI, help/man/completion, and compatibility contracts open |
| M6.2 migration/package/clean machine | Not Started | no implementation evidence |
| M6.3 security/compatibility/diagnostics | Not Started | no implementation evidence |
| M6.4 RC/1.0 | Not Started | no RC, release artifacts, tag, release, or channel evidence |

## Feature status

REL-001 is `In Progress` after the first versioned-default decode contract. The other 22 Stage 6-owned rows remain `Planned`: WORK-006, VIM-013, VIM-014, JOB-010, HELP-013, SEC-012, SEC-014, OBS-009, OBS-010, PLAT-003, PLAT-009, and REL-002 through REL-012. Shared rows that remain `In Progress` are not advanced by this evidence.

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
