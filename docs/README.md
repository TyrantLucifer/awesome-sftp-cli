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
8. [Project state](../PROJECT_STATE.md) is the short, current handoff for the next work session.

## Required reading order for a new session

Read only as far as needed, in this order:

1. `PROJECT_STATE.md`
2. the active stage in `IMPLEMENTATION_PLAN.md`
3. `docs/product/feature-matrix.md`
4. the active `docs/stages/NN-*.md`
5. ADRs and interfaces linked by that stage
6. the repository diff plus the last green validation commands recorded in `PROJECT_STATE.md`

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

The temporary `.superpowers/` visual workspace is intentionally ignored. Durable decisions from it live in the approved design and linked documents.
