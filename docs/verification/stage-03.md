# Stage 3 Verification Record

- **Status**: In Progress
- **Updated**: 2026-07-16
- **Repository root**: `/Users/bytedance/Downloads/projects/awesome-mac-sftp`
- **Branch**: `codex/stage3-preview-edit-cache`
- **Stage 2 merge baseline**: commit `8a118d7069e4bf86e4f7e73d6fc41977cf1202f5`, tree `ee1ebdf11b61f1ac05fa0b2a4f23800ab9ba934a`
- **Baseline Hosted run**: [29490490339](https://github.com/TyrantLucifer/awsome-sftp-cli/actions/runs/29490490339) — exact merge commit, successful
- **Current milestone**: M3.1 drawer foundation green; cache domain/lease RED tests next

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
- The compiled default remains Version 1 until production CSPRNG attempt-ID and explicit-resume paths plus V1→V2 upgrade/crash tests are green; fresh daemon startup behavior is therefore unchanged by this contract slice.

## Milestone ledger

### M3.1 — Drawer and cache foundation

- **Status**: In Progress
- **Goal**: bounded Preview/Jobs/Log state plus owner-only content-addressed cache, references, quotas, LRU, leases and restart reconciliation.
- **Drawer checkpoint**: RED compilation first proved the absence of `DrawerState`, K/L keys and focus modes. The green reducer now freezes `closed|preview|jobs|log` plus `pane|drawer` focus; K/J/L open/switch/refocus/close without changing active pane/Locations, Esc returns to pane while retaining the tab, and lowercase navigation remains independent. Switching or moving a visible Preview emits cancel before a new bounded Preview intent. Jobs continues to read the Stage 2 daemon snapshot rather than copying durable authority.
- **Layout checkpoint**: the centered Jobs modal was replaced by a bounded bottom region. Normal `100x16` and narrow `32x7` snapshots retain both panes, status, tabs and waiting Job state; minimum-size behavior remains explicit. Log has a bounded empty state only—snapshot/replay/filter/redaction is not yet implemented and OBS-001 stays In Progress.
- **Commands**: `go test ./internal/tui -run '^TestDrawer|^TestTranslateTCellDistinguishes' -count=1` PASS after the expected RED; focused drawer/legacy renderer tests pass 20 consecutive runs; `go test ./internal/tui ./internal/app -count=1` PASS; `go test -race ./internal/tui ./internal/app -count=1` PASS; complete `make check` PASS.
- **Last green command**: `make check`.
- **Next gate**: add RED typed cache/quota/LRU/reference/lease tests with a manual clock, then implement the daemon-owned cache domain without activating Version 2 as the production default.

### M3.2 — Built-in, image and external preview

- **Status**: Not Started

### M3.3 — Editor and default opener

- **Status**: Not Started

### M3.4 — Command, shell and platform closeout

- **Status**: Not Started

## Required final evidence

The authoritative exit checklist remains [Stage 3](../stages/03-preview-edit-cache.md#6-可验证退出标准). Each item stays open until implementation, focused tests, complete local gates, native platform evidence, exact-SHA Hosted CI and the final cold-start audit all agree.
