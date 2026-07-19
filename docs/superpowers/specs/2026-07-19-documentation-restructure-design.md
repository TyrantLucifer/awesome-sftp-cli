# Documentation Restructure Design

**Date:** 2026-07-19  
**Status:** Approved  
**Scope:** Repository documentation, documentation validation, and contributor handoff

## Objective

Turn the repository documentation into a compact, current product surface that supports users, maintainers, and the next implementation cycle. The root README becomes the primary entry point, stable design and operating contracts remain under `docs/`, and transient Stage execution material is removed from the active tree.

## Audience and information architecture

The documentation has three primary audiences:

1. Users evaluating, installing, and operating the internal preview.
2. Maintainers changing the Go implementation without violating security or durability contracts.
3. Coding agents beginning the next iteration from a reliable repository-local handoff.

The durable structure is:

- `README.md`: product overview, internal-preview status, install and first-run path, core workflows, architecture summary, limitations, development quick start, and documentation map.
- `AGENTS.md`: repository-specific engineering instructions, code map, invariants, validation ladder, documentation rules, and next-iteration handoff.
- `docs/README.md`: concise documentation hub grouped by users, design, operations, security, release, and development.
- `docs/architecture/`: current architecture and immutable ADR history.
- `docs/user/`: detailed task-oriented usage and reference material.
- `docs/product/`: product intent, implemented capability status, compatibility, and next-stage roadmap.
- `docs/development/`: contributor toolchain, testing, and concise current project status.
- `docs/operations/`, `docs/security/`, `docs/release/`, and `docs/man/`: stable operational and distribution contracts.

## Root README design

The README leads with the current outcome rather than the historical build sequence. It must contain:

- A one-paragraph description of AMSFTP and the Vim-first dual-pane model.
- A visible warning that the available release is an unsigned, owner-only internal preview.
- A current capability boundary: Level 0 SFTP is available; production Helper and Level 2 remain closed.
- Requirements, installation, configuration, first-run commands, location syntax, and the default keymap.
- Safe copy/move/delete, durable Job, preview/edit/cache, search, daemon, doctor, and support-bundle workflows.
- A compact architecture diagram and explanation of the TUI/CLI, daemon, providers, SQLite state, and system OpenSSH boundary.
- Known limitations, troubleshooting links, build/test commands, and a curated documentation map.

Detailed procedures stay in focused documents. The README summarizes and links instead of duplicating every CLI field or release gate.

## Stable design and usage documentation

`docs/architecture/overview.md` is rewritten to clearly distinguish current implementation from future or release-gated capabilities. ADRs remain because they explain durable decisions and security boundaries.

Existing user guides remain task-specific. Their status language and cross-links are updated so they describe the merged internal preview rather than an active Stage. The documentation hub provides the canonical reading paths for first use, daily operation, troubleshooting, design review, and contribution.

A concise current-status document records the internal release, merged baseline, supported surface, known limits, and next action. A roadmap document replaces the completed Stage implementation plan and contains only future iteration themes and acceptance expectations.

## Aggressive cleanup policy

The following material is deleted after durable facts are migrated:

- Root `STAGE5_GOAL.md`, `IMPLEMENTATION_PLAN.md`, and `PROJECT_STATE.md`.
- `docs/stages/`, including the Stage 6 execution plan.
- `docs/verification/` command and candidate ledgers.
- `docs/superpowers/` historical design and implementation plans, including this approved design after it has served as the committed decision record.

Git history remains the archive. The active documentation tree does not retain generated coordination artifacts, chronological command transcripts, stale commit identities, or plans for completed stages.

The feature matrix is retained only if it can serve as a concise current capability index. Historical Stage and Verification links are removed or replaced with stable implementation/test/document references.

## AGENTS.md contract

`AGENTS.md` must let a new coding agent start safely without reading historical ledgers. It defines:

- Product and release status.
- Authoritative reading order and documentation ownership.
- Package boundaries and the codebase-memory discovery preference.
- OpenSSH, credential, IPC, provider, transfer, Helper, and Level 2 invariants.
- Proportional validation commands, with focused tests preferred during iteration and release gates reserved for release work.
- Git/worktree hygiene, generated-artifact handling, and documentation synchronization.
- The next-iteration starting point and the rule that completed historical stages are discovered through Git history, not live documents.

## Documentation validation

`internal/docscheck` is migrated away from the retired Stage truth chain. The new checks require the durable entry points, validate repository-local Markdown links, and preserve checks for the capability matrix, workflows, release materials, and security contracts where still applicable.

Validation for this change is intentionally focused:

1. Documentation-check unit tests.
2. The repository documentation check entry point.
3. Markdown link validation through the updated checker.
4. `git diff --check`.

No unrelated full runtime, native platform, or release matrix is required for a documentation-only reorganization unless the documentation checker changes expose a broader failure.

## Success criteria

- A new user can install and complete a safe first run from the root README.
- A maintainer can understand the implemented architecture, invariants, and relevant validation commands without reading Stage logs.
- A new coding agent can enter the next iteration using `AGENTS.md`, current status, and roadmap.
- No active document claims PR #6 is open, Stage 6 is still the working branch, or the internal preview is a public 1.0 release.
- Removed documents leave no broken repository-local links or hard-coded docs-check requirements.
