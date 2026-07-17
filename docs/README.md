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
8. [Stage verification](verification/stage-05.md) records the active stage's exact evidence, candidate identity, and handoff gates; the Stage 0–4 ledgers remain immutable historical evidence.
9. [Project state](../PROJECT_STATE.md) is the short, current handoff for the next work session.

## Engineering gates

- [Local testing and quality gates](development/testing.md) documents the repeatable Make entrypoints, exact toolchains, and platform matrix.
- [Dependency and supply-chain policy](security/dependency-policy.md) defines dependency review, pinned tools, and immutable CI actions.
- [Read-only Explorer guide](user/read-only-explorer.md) documents the current Stage 1 launch, OpenSSH reuse, keys, workspaces, recovery, and diagnostics behavior.
- [Durable Transfers guide](user/durable-transfers.md) documents Stage 2 clipboard, move, rename, delete, conflict, Jobs, part, and recovery behavior.
- [Preview, Edit, Cache, and Shell guide](user/preview-edit-cache.md) documents Stage 3 `K/J/L`, bounded preview/image behavior, cache policies and safe cleanup, `e`/`o` conflict and recovery, structured external commands, `!`/`gs`, privacy, and current limitations.
- [Search and Optional Helper guide](user/search-helper.md) documents Stage 4 `f`/`g/`, budgets/partial results, Level 0/1 status, fixture-only consent/lifecycle, degradation, same-host copy, and the production distribution CLOSED boundary.

## Required reading order for a new session

Read only as far as needed, in this order:

1. `PROJECT_STATE.md`
2. the active stage in `IMPLEMENTATION_PLAN.md`
3. `docs/product/feature-matrix.md`
4. the active `docs/stages/NN-*.md`
5. the latest completed or active `docs/verification/stage-NN.md` (currently Stage 5)
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

## Stage 5 cold-start capsule

Stage 5 is active on fixed branch `codex/stage5-direct-transfer-scale` from the sole verified baseline `06415e1e9fe5ffa93999f112b64aee0bd35e5c75`. Its authoritative live ledger is [Stage 5 verification](verification/stage-05.md); Draft PR [#5](https://github.com/TyrantLucifer/awsome-sftp-cli/pull/5) stays unmerged until the exact final push and PR matrices are green. A commit cannot contain its own SHA, so the final identity is bound by Git/PR/Hosted metadata after the last documentation commit, never inferred from a predecessor checkpoint.

- M5.1–M5.4 are locally implemented: unified frozen route evidence, declared server copy, fixture-only Level 2 direct, safe downgrade/fault equivalence, 50k/million/100 GiB scale contracts, bounded BFS, shared bandwidth/resource scheduling, Endpoint lease reuse, bounded events/logs and reproducible trends.
- Production Helper distribution and production Level 2 remain **CLOSED**. Ordinary runtime has no fixture backend/config switch and records `production_distribution_closed`; no Agent forwarding, Kerberos delegation, key/ticket/known-host copying or relaxed host-key policy is introduced.
- ADR-0017 explicitly decomposes Stage 5's 100 GiB evidence: actual/synthetic 100 GiB executions prove 64-bit and fixed-resource/cancel boundaries, while the same Worker/Journal/Scheduler lifecycle proves pause/checksum/restart resume/rate/SHA-256/commit on sparse-shaped multi-quantum content. A complete physical 100 GiB LocalFS/SFTP run remains a Stage 6 nightly/release gate and is not claimed by the fast gate.
- Stage 0–4 evidence remains immutable history. Stage 6 packaging, signing, notarization and release readiness have not started, so Stage 5 completion is not a 1.0 release claim.

A new session must not infer final green status from this capsule. It must reconcile `PROJECT_STATE.md`, `IMPLEMENTATION_PLAN.md`, the feature matrix and the Stage 5 ledger, then require the listed CI-equivalent current/oldstable/race/scale/native/reproducibility/pollution gates, two fresh independent reviews, exact final push/PR Hosted success and a Ready-but-unmerged PR.
