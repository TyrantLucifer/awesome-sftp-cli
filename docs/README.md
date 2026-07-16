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
8. [Stage verification](verification/stage-04.md) records the active stage's exact evidence, candidate identity, and handoff gates; the Stage 1–3 ledgers remain immutable historical evidence.
9. [Project state](../PROJECT_STATE.md) is the short, current handoff for the next work session.

## Engineering gates

- [Local testing and quality gates](development/testing.md) documents the repeatable Make entrypoints, exact toolchains, and platform matrix.
- [Dependency and supply-chain policy](security/dependency-policy.md) defines dependency review, pinned tools, and immutable CI actions.
- [Read-only Explorer guide](user/read-only-explorer.md) documents the current Stage 1 launch, OpenSSH reuse, keys, workspaces, recovery, and diagnostics behavior.
- [Durable Transfers guide](user/durable-transfers.md) documents Stage 2 clipboard, move, rename, delete, conflict, Jobs, part, and recovery behavior.
- [Preview, Edit, Cache, and Shell guide](user/preview-edit-cache.md) documents Stage 3 `K/J/L`, bounded preview/image behavior, cache policies and safe cleanup, `e`/`o` conflict and recovery, structured external commands, `!`/`gs`, privacy, and current limitations.

## Required reading order for a new session

Read only as far as needed, in this order:

1. `PROJECT_STATE.md`
2. the active stage in `IMPLEMENTATION_PLAN.md`
3. `docs/product/feature-matrix.md`
4. the active `docs/stages/NN-*.md`
5. the latest completed or active `docs/verification/stage-NN.md` (currently Stage 3)
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

## Stage 3 cold-start capsule

Stage 3 is the Preview/Edit/Cache slice on `codex/stage3-preview-edit-cache`; its final local verdict belongs in [Stage 3 verification](verification/stage-03.md), whose delivery link points to PR #3's immutable exact candidate SHA/tree and final Hosted runs (a commit cannot contain its own SHA). Evidence never lives only in chat or a temporary directory. Stage 3 preserves Stage 1 browsing/auth/workspaces and routes every edit write-back through Stage 2's durable Plan/Job/part/verify/commit path.

- SQLite head is Version 3: V2 owns cache/lease/edit-session catalog; V3 adds reconstructible edit decision details. V3 checksum is `16ae664c033fb1fae7da937eae6c4b19c6b05430fa3499fa5f0da8daa58e1ab4`; head-3 contract digest is `a523d6c4aeebb386780f7283b63aacb175cf0420027114edac51a032425615a2`.
- Cache defaults are 2 GiB/4,096 managed objects globally, where objects are entries+materializations; the non-shared workspace share is 1 GiB. Live publication/handoff is serialized through admission, which may make room in at most four batches of 256 candidates before typed `resource_exhausted`. Startup lifecycle uses 256-item keyset pages for at most 64 batches (16,384 theoretical candidates, practically capped by 4,096 managed objects); it immediately reclaims only exact provably dead Preview handoffs and retains edit/open/upload references. Policies are `lru`, `ephemeral`, and `pinned_offline`; disconnected pinned reuse requires one exact revalidated historical fingerprint and is marked freshness `unknown`. Lease heartbeat/expiry/opener grace are 30 seconds/2 minutes/15 minutes.
- Preview reads 64 KiB per request and retains/renders at most 512 KiB; JSON is 256 KiB/depth 64; output is 10,000 lines/4,096 spans, with bounded full Provider object metadata. Active image probing is 200 ms/256 bytes; terminal encoding is 4 MiB payload/6 MiB output/1,000,000 pixels. Conflict diff is 32 KiB per side/512 lines/64 KiB output. Edit baseline/observation and Stage 2 pre-publish checks include full streamed content SHA-256, so same-stat byte changes conflict. `!` is single-flight, 32 KiB, `Esc`-cancelable, capped at 15 minutes, and uses separate 1 MiB stdout/stderr rings.
- Catalog/manifest inconsistency preserves bytes and fails closed. Existing manifests lack policy/dirty/reference/lease state, so Stage 3 does not claim automatic catalog reconstruction; unsafe cleanup and guessed rebuild are forbidden.
- User behavior and exact key sequences are in the [Stage 3 user guide](user/preview-edit-cache.md); architecture and security contracts are [ADR-0014](architecture/adr/0014-stage3-cache-preview-and-edit-session-contracts.md) and [ADR-0015](architecture/adr/0015-stage3-external-process-shell-and-tty-contracts.md).
- Stage 4–6 remain outside this slice: recursive search, Helper install/handshake/hash/watch/tail, cross-host direct/scale hardening, packaging/signing/notarization, and release readiness. Production Helper distribution remains **CLOSED** while real offline private-key custody/dual-control ceremony evidence is absent. The unique first Stage 4 implementation action is the RED Level 0 recursive filename-search contract over the standard SFTP Provider—bounded streaming pages, cancellation, generation isolation and explicit partial results—without invoking or distributing a Helper. Custody/recovery/revoke/rotation evidence remains a separate prerequisite before any production Helper artifact or remote install path.

Final acceptance must run and record `make docs-check`, `make check`, `make lint`, `make supply-chain`, `make ci`, `go test -race ./...`, and `GOTOOLCHAIN=go1.25.12 make check`, plus the native/platform and pollution gates listed in the Stage 3 verification ledger. A new session must not infer green status from this capsule; it must follow the verification ledger to PR #3's exact-SHA delivery results and reconcile them with `PROJECT_STATE.md`.
