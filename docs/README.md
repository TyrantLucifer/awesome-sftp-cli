# Documentation Map

This repository treats documentation as part of the product contract. A feature is not complete until implementation, tests, the feature matrix, verification evidence, and `PROJECT_STATE.md` agree.

## Source-of-truth chain

1. [Product vision](product/vision.md) defines who the product is for and what it must become.
2. [Approved design](superpowers/specs/2026-07-14-vim-first-sftp-commander-design.md) records the complete product and architecture decisions.
3. [Feature matrix](product/feature-matrix.md) assigns every promised capability a stable ID, delivery stage, status, and evidence requirement.
4. [Architecture overview](architecture/overview.md) and [ADRs](architecture/adr/) explain system boundaries and irreversible decisions.
5. [Implementation plan](../IMPLEMENTATION_PLAN.md) tracks stage-level execution.
6. [Stage specifications](stages/) define scope, tests, exit criteria, and handoff requirements for each stage.
7. [Testing strategy](testing/strategy.md) defines the validation ladder and required fixtures.
8. [Stage verification](verification/stage-06.md) records the active stage's exact evidence, candidate identity, and handoff gates; the Stage 0–5 ledgers remain immutable historical evidence.
9. [Project state](../PROJECT_STATE.md) is the short, current handoff for the next work session.

## Engineering gates

- [Local testing and quality gates](development/testing.md) documents the repeatable Make entrypoints, exact toolchains, and platform matrix.
- [Dependency and supply-chain policy](security/dependency-policy.md) defines dependency review, pinned tools, and immutable CI actions.
- [Read-only Explorer guide](user/read-only-explorer.md) documents the current Stage 1 launch, OpenSSH reuse, keys, workspaces, recovery, and diagnostics behavior.
- [Durable Transfers guide](user/durable-transfers.md) documents Stage 2 clipboard, move, rename, delete, conflict, Jobs, part, and recovery behavior.
- [Preview, Edit, Cache, and Shell guide](user/preview-edit-cache.md) documents Stage 3 `K/J/L`, bounded preview/image behavior, cache policies and safe cleanup, `e`/`o` conflict and recovery, structured external commands, `!`/`gs`, privacy, and current limitations.
- [Search and Optional Helper guide](user/search-helper.md) documents Stage 4 `f`/`g/`, budgets/partial results, Level 0/1 status, fixture-only consent/lifecycle, degradation, same-host copy, and the production distribution CLOSED boundary.
- [Configuration reference](user/configuration.md) documents the versioned strict JSON schema, current precedence, validation/effective-output commands, redaction, and stable public exit codes while M6.1 expands the remaining sections.
- [Vim-first keymap reference](user/keymap.md) documents the exact default action map, Normal/Visual remapping, reserved dangerous and sequence actions, count/repeat boundaries, and the 1.0 macro/named-register exclusion.
- [amsftp(1)](man/amsftp.1) is the committed man page checked against the same ordered command facts that render `--help` and bash/zsh/fish completions.

## Required reading order for a new session

Read only as far as needed, in this order:

1. `PROJECT_STATE.md`
2. the active stage in `IMPLEMENTATION_PLAN.md`
3. `docs/product/feature-matrix.md`
4. the active `docs/stages/NN-*.md`
5. the latest completed or active `docs/verification/stage-NN.md` (currently Stage 6)
6. ADRs and interfaces linked by that stage
7. the complete worktree status/manifest plus the last green validation commands recorded in the verification record and `PROJECT_STATE.md`

If these sources disagree, stop feature work and reconcile them in the same change. The approved design is authoritative for product intent; a newer accepted ADR is authoritative for a deliberately changed technical decision.

## Status vocabulary

- `Planned`: approved but implementation has not begun.
- `In Progress`: implementation exists but the feature's complete exit evidence is not yet green.
- `Implemented`: code and focused tests are complete; broader stage gates may remain.
- `Verified`: all feature and stage evidence is recorded and green.
- `Deferred`: explicitly removed from the current stage while remaining in the product vision, with a reason and target stage.
- `Removed`: deliberately removed from the product, with an ADR or decision record; its stable feature ID is never reused.

Do not use `Done` because it obscures whether verification and documentation were completed.

## End-of-stage handoff contract

Before marking a stage complete:

1. Run every required stage command and record exact results.
2. Update each affected feature-matrix row and link its evidence.
3. Record accepted architecture changes as ADRs; never silently rewrite old decisions.
4. Update `PROJECT_STATE.md` with the current revision, known limitations, last green commands, and the single next action.
5. Mark the stage `Complete` in `IMPLEMENTATION_PLAN.md` only after its exit criteria are green.
6. Open the next stage by checking its assumptions against the code and current environment.

The temporary `.superpowers/` visual workspace is intentionally ignored and cannot be the only source of implementation evidence. Durable decisions and final task verdicts must be copied into the approved design, active verification record, and linked documents.

## Stage 6 cold-start capsule

Stage 6 is active on fixed branch `codex/stage6-hardening-release` from the sole verified baseline `312bcccbcbd54246bbe5ff9babf4f14560449176`, tree `e0316c286ce11512cb0b92c917fa29b80f9e3305`. Exact-main Hosted run [29579514879](https://github.com/TyrantLucifer/awsome-sftp-cli/actions/runs/29579514879) passed 24/24 jobs and the untouched local baseline passed CI-equivalent `make ci`. The authoritative live ledger is [Stage 6 verification](verification/stage-06.md) and the strict M6.1→M6.4 work breakdown is the [Stage 6 execution plan](stages/06-hardening-release-plan.md).

- M6.1 implementation is underway. VIM-013, VIM-014, REL-001, REL-002, and REL-011 are `In Progress` after versioned-default, validated context-keymap, reserved-dangerous-action, config-command, redacted machine-output, stable exit-code, and help/man/completion parity contracts; the other 18 Stage 6-owned rows remain `Planned` and all 12 exit criteria remain open.
- Production Helper distribution and production Level 2 remain **CLOSED**. The repository has no real production offline signing key/custody ceremony or final Developer ID/notary evidence; none may be fabricated.
- Stage 5's same-process Level 2 data fixture does not satisfy the required Stage 6 process/network-isolated data plane. Its 100 GiB decomposition does not satisfy the complete physical 100 GiB LocalFS/SFTP release run.
- The fixed delivery PR remains Draft until the same exact RC passes complete local/native/Hosted, security, compatibility, clean-machine, signed-byte, final-review, rollback, and truth-chain gates. A commit cannot contain its own SHA, so final identity is bound by Git/PR/Hosted/tag/release/channel metadata after the last documentation commit.

A new session must not infer release readiness from this capsule. It must reconcile `PROJECT_STATE.md`, `IMPLEMENTATION_PLAN.md`, the feature matrix, Stage 6 specification, execution plan, and ledger, then resume the first incomplete milestone without bypassing protected release boundaries.
