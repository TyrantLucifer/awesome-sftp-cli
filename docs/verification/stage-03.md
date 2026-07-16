# Stage 3 Verification Record

- **Status**: In Progress
- **Updated**: 2026-07-16
- **Repository root**: `/Users/bytedance/Downloads/projects/awesome-mac-sftp`
- **Branch**: `codex/stage3-preview-edit-cache`
- **Stage 2 merge baseline**: commit `8a118d7069e4bf86e4f7e73d6fc41977cf1202f5`, tree `ee1ebdf11b61f1ac05fa0b2a4f23800ab9ba934a`
- **Baseline Hosted run**: [29490490339](https://github.com/TyrantLucifer/awsome-sftp-cli/actions/runs/29490490339) — exact merge commit, successful
- **Current milestone**: M3.1 cache domain/filesystem foundation in progress; bounded built-in Preview and external-process planning slices green
- **First pushed implementation**: commit `1b3244b97c074ae8736ac59f0645a959415044fd`, tree `cd57ca301c97017a65e9d33687cd615bb8236cec`
- **Draft PR**: [#3 — feat: deliver the Stage 3 preview edit and cache](https://github.com/TyrantLucifer/awsome-sftp-cli/pull/3)

Stage 3 delivers Preview/Jobs/Log drawers, bounded preview, managed cache/lease/edit sessions, editor/opener workflows, explicit `!`/`gs`, and terminal recovery. It must preserve the Stage 1 read-only/auth/workspace baseline and the Stage 2 Planner→Job→part→verify→commit mutation path. Cache, preview, external process, Provider and RPC paths do not gain a second write route.

## Initial safety checkpoint

| Check | Result |
|---|---|
| `git status --short --branch` | PASS: clean `main`, tracking `origin/main` before branch creation |
| `git rev-parse HEAD HEAD^{tree} origin/main` | PASS: `8a118d7069e4bf86e4f7e73d6fc41977cf1202f5`, tree `ee1ebdf11b61f1ac05fa0b2a4f23800ab9ba934a`, `origin/main` exact match |
| `git fetch --prune origin` | PASS: no local or remote `codex/stage3-preview-edit-cache` branch existed |
| `gh run view 29490490339 --repo TyrantLucifer/awsome-sftp-cli ...` | PASS: `headSha` is the exact merge commit; run and all required jobs concluded `success` |
| `make docs-check` | PASS on exact main |
| `make check` | PASS on exact main after granting access to the external Go build cache; unit, Provider contract, local Stage 2 PTY, docs and module gates passed |
| branch creation | PASS: created `codex/stage3-preview-edit-cache` once from the verified baseline |
| codebase-memory index | PASS: fresh fast index contains 1,983 nodes and 9,526 edges |

No user change was overwritten. Existing ignored `.idea/`, `.superpowers/`, `coverage/` and `dist/` content remains outside the candidate tree.

The first reviewable checkpoint was pushed with the branch exactly matching `origin/codex/stage3-preview-edit-cache`. Push run [29492926455](https://github.com/TyrantLucifer/awsome-sftp-cli/actions/runs/29492926455) and PR run [29492939398](https://github.com/TyrantLucifer/awsome-sftp-cli/actions/runs/29492939398) both completed successfully at exact SHA `1b3244b97c074ae8736ac59f0645a959415044fd`; they are checkpoint evidence only and do not replace the final exact-SHA Ready gate.

## Zero-gate contract and dependency intake

**Status**: In Progress

Current evidence:

- Version 1 is immutable at migration checksum `281a5d34c0ebdd06de26fd1098fbf3efd7c8a7e283f5328ea218d1ca8dfb19f9` and whole-schema digest `659edd23b5bc332b488a171c920815daffef6223ef2d3859215ba177c3d55e64`.
- Version 1 contains migration, Operation Plan, Job, step, checkpoint, conflict, result, event and request-dedup tables. It has no cache entry, blob reference, materialization, lease or edit-session tables.
- Durable cache/lease/edit-session state therefore requires an ADR-0008 forward Version 2 migration, a per-head `SchemaContract`, original..target migration-set digest coverage, backup/hold/rollback/WAL tests and native APFS/ext4/XFS evidence. Version 1 history will not be edited.
- Stage 3 will use the standard library and the already-admitted dependency graph unless a later feature proves a new dependency necessary through the full intake gate. No syntax-highlighting, MIME or image library is currently admitted.
- [ADR-0014](../architecture/adr/0014-stage3-cache-preview-and-edit-session-contracts.md) freezes cache layout/metadata, quota/LRU/lease/reconciliation, drawer/Preview identity/budgets, Version 2 ownership and edit-session→Job atomic binding.
- [ADR-0015](../architecture/adr/0015-stage3-external-process-shell-and-tty-contracts.md) freezes external executable config/lexer/direct exec, editor/opener change handling, local/remote `!`/`gs` separation and the TTY handoff state machine.
- No new runtime dependency is admitted by either contract.
- RED→GREEN Version 2 contract slice now exists without changing the default schema head: immutable `migration.Version2()` adds the eight typed Stage 3 ownership tables and seven LRU/reachability/recovery indexes with a 64 MiB migration WAL budget. Version 2 checksum is `3e15e4350c117143015526452c9d5e517bed29940bbd8c17c7b5172e69c2d821`; the 47,352-byte whole-schema contract digest is `eaf67a323a84b198864ffd9e9ef44566b1e2d9758df5235bb969e6ce7406a739`.
- `go test ./internal/state/migration -run '^TestVersion2' -count=1` first failed to compile on the absent Version 2 APIs, then passed after the immutable migration/contract implementation. `go test ./internal/state/migration -count=1` also passes.
- The production default is now exactly Version 1 plus Version 2 and both frozen contracts. A pristine V1 database upgrades through the existing backup/WAL/coordinator path; fresh attempts generate a CSPRNG 128-bit lower-hex attempt ID, while running/interrupted/failed attempts require explicit `daemon --resume-migration` and reuse the persisted attempt/backup. Reopen, no-second-backup, old-binary-on-V2, future-head-3, missing/changed-contract and explicit-resume failure tests pass.

## Milestone ledger

### M3.1 — Drawer and cache foundation

- **Status**: In Progress
- **Goal**: bounded Preview/Jobs/Log state plus owner-only content-addressed cache, references, quotas, LRU, leases and restart reconciliation.
- **Drawer checkpoint**: RED compilation first proved the absence of `DrawerState`, K/L keys and focus modes. The green reducer now freezes `closed|preview|jobs|log` plus `pane|drawer` focus; K/J/L open/switch/refocus/close without changing active pane/Locations, Esc returns to pane while retaining the tab, and lowercase navigation remains independent. Switching or moving a visible Preview emits cancel before a new bounded Preview intent. Jobs continues to read the Stage 2 daemon snapshot rather than copying durable authority.
- **Layout checkpoint**: the centered Jobs modal was replaced by a bounded bottom region. Normal `100x16` and narrow `32x7` snapshots retain both panes, status, tabs and waiting Job state; minimum-size behavior remains explicit. Workspace schema v2 strictly persists drawer tab/focus/rows and `lru|ephemeral|pinned_offline`, migrates v1 deterministically, restores only structural state, and never persists Preview/Jobs/Log bodies.
- **Log checkpoint**: daemon logging now fans out to the existing rotating redacted JSON sink and a 1,000-record in-memory ring. `diagnostic.list` caps replay pages at 256, validates optional Job/Endpoint filters, and the Log drawer polls/renders sanitized structured records without reading unbounded history or duplicating Job authority.
- **Cache-domain checkpoint**: typed content/entry/materialization/reference/lease IDs, golden Location+fingerprint entry identity, manual-clock heartbeat/release and PID+birth fail-closed classification, deduplicated global/workspace accounting, bounded deterministic LRU, and pinned/dirty/deleting/leased/shared/referenced/reachable protection pass unit, race, vet and lint checks. Filesystem publication/restart scanning remains in progress; SQLite catalog integration and native crash/ENOSPC evidence remain open.
- **Commands**: drawer/log/workspace/cache focused tests and package race tests pass; `go test ./internal/diagnostic ./internal/daemon ./internal/tui ./internal/app -count=1`, `go test ./internal/statefs ./internal/state/migration ./internal/app -count=1`, and statefs/migration race tests pass.
- **Last green command**: `go test -race ./internal/preview ./internal/tui -count=1`.
- **Next gate**: finish owner-only atomic cache filesystem publication/restart scanning, then bind the cache catalog to Version 2 transactionally.

### M3.2 — Built-in, image and external preview

- **Status**: In Progress

- Dependency-free built-in rendering now caps retained input/output at 512 KiB, JSON at 256 KiB/depth 64, output at 10,000 lines, terminal-sanitizes text, pretty-prints complete bounded JSON, emits bounded hexadecimal binary summaries, identifies PNG/JPEG/GIF dimensions without embedding payload, and labels partial byte ranges. The current Provider read remains the initial 64 KiB range; continue/head/tail, syntax color and terminal image protocols remain open.
- `go test ./internal/preview ./internal/tui ./internal/app -count=1` and `go test -race ./internal/preview ./internal/tui -count=1` pass.

### M3.3 — Editor and default opener

- **Status**: In Progress

- The isolated external-process planner implements the restricted no-expansion lexer, fail-closed editor precedence, canonical absolute PATH resolution plus identity revalidation, fixed macOS/Linux opener paths, scrubbed environment and direct-exec plans with the canonical materialization as the final separate argument. Cache materialization/lease, terminal suspend/resume, change matrix and Job sync-back remain open.
- External-process unit tests pass 20 consecutive runs plus race, vet and Linux cross-compile checks.

### M3.4 — Command, shell and platform closeout

- **Status**: Not Started

## Required final evidence

The authoritative exit checklist remains [Stage 3](../stages/03-preview-edit-cache.md#6-可验证退出标准). Each item stays open until implementation, focused tests, complete local gates, native platform evidence, exact-SHA Hosted CI and the final cold-start audit all agree.
