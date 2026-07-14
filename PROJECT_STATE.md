# Project State

- **Updated**: 2026-07-14
- **Lifecycle**: Approved design; implementation has not started
- **Active stage**: Stage 0 — Foundation and Knowledge
- **Repository working title**: `awesome-mac-sftp`

## Current outcome

The product and architecture design have been approved. The repository currently contains durable planning documents only; there is no application source, build configuration, database, helper binary, release artifact, or completed feature.

## Product in one paragraph

Build a Vim-first, two-pane terminal file commander for macOS and Linux. Either pane can independently point at the local machine or any SSH/SFTP endpoint. Authentication and SSH configuration are delegated to the user's system OpenSSH so Kerberos/GSSAPI, `ProxyCommand`, agents, host aliases, and existing policy continue to work. A local daemon owns sessions, durable background jobs, cache, and workspaces. Standard SFTP is always the baseline; a versioned, explicitly installed remote helper unlocks faster search, hashing, watch/tail, same-host copy, and carefully preflighted direct transfers without becoming a privileged or resident service.

## Approved decisions that must not be rediscovered

- Implementation language: Go.
- Primary platforms: macOS and Linux terminals.
- UI: two symmetric panes; each can switch among local and arbitrary remote endpoints.
- Interaction: Vim-first Normal/Visual model, counts and dot-repeat; no initial macro or named-register system.
- SSH transport: spawn system `ssh` with the SFTP subsystem and connect a Go SFTP protocol client to its stdio.
- Authentication: system OpenSSH is the only source of truth; no Kerberos implementation and no secret/ticket storage in the application.
- Process model: TUI/CLI client plus an auto-started per-user local daemon over a permission-restricted Unix socket.
- Transfer model: persistent jobs, bounded-memory streaming, temporary destination plus atomic commit, source deletion only after verified move commit.
- Remote-to-remote routing: safe fast/direct paths when capability and policy preflight succeeds; bounded-memory local relay otherwise.
- Remote helper: optional, user-approved, versioned, unprivileged, invoked over SSH stdio, never a listener or persistent remote daemon.
- Cache: short-lived LRU by default; workspace-scoped ephemeral or explicitly pinned offline content.
- Scale target: directories with tens of thousands of entries, trees with millions of paths, and individual files in the hundreds of gigabytes.
- Delivery: seven vertical stages (0 through 6), each ending in an executable, independently verifiable capability slice.

Changing any item above requires an explicit ADR and corresponding updates to the design, feature matrix, active stage, and this file.

## Next action

Execute Stage 0 only after the written specification is reviewed. Start by invoking the implementation-planning workflow, then turn the Stage 0 specification into task-sized, test-first steps. Do not begin Stage 1 features during Stage 0.

## Required reading for the next session

1. [Documentation map](docs/README.md)
2. [Implementation plan](IMPLEMENTATION_PLAN.md), Stage 0
3. [Feature matrix](docs/product/feature-matrix.md), Stage 0 rows
4. [Stage 0 specification](docs/stages/00-foundation.md)
5. [Approved design](docs/superpowers/specs/2026-07-14-vim-first-sftp-commander-design.md)
6. ADRs referenced by Stage 0

## Validation record

Design-document self-review completed on 2026-07-14:

- relative Markdown target checker: passed for every durable `.md` file;
- feature-matrix schema check: 246 rows, 246 unique IDs, all stages in 0–6, all statuses `Planned`, and explicit expected evidence on every row;
- stage-contract check: all seven stage specs contain scope, dependencies, deliverables, ordered milestones, exit gates, tests, rollback, document updates, and handoff information; `IMPLEMENTATION_PLAN.md` contains exactly one Stage 0–6 entry and all are `Not Started`;
- unresolved-marker scan: passed across all durable documents;
- approved-decision coverage check: passed for OpenSSH/Kerberos, symmetric panes, daemon/IPC, durable jobs, transfer safety, helper levels, Vim actions, cache, direct fallback, and scale boundaries.

No build, formatter, linter, or code tests were run because Stage 0 has not created application source or tool configuration. The next evidence gate is user review of the written specification.

## Known constraints and deliberately deferred choices

- The public product and binary name will be finalized in Stage 0 before any release-facing artifact. Stable feature IDs and architecture do not depend on the name.
- Exact Go module path, dependency versions, storage paths, config syntax, and packaging identifiers are Stage 0 decisions and must be verified against then-current supported releases.
- Cross-host direct transfer is not assumed to work with Kerberos. It is an optional capability that must prove destination reachability and non-interactive credentials on the source host without forwarding or copying user credentials; otherwise the route is local relay.
- GUI opener behavior differs by platform. Stage 3 must implement platform adapters and validate lease/change detection on both macOS and Linux.

## Working-tree policy

- Do not commit unless the user explicitly requests it.
- Preserve `.superpowers/` only as disposable brainstorming output; it is ignored by Git.
- At the end of every work session, update this file even when work stops on a failure.
