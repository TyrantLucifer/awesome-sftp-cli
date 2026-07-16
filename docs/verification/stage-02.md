# Stage 2 Verification Record

- **Status**: In Progress
- **Updated**: 2026-07-16
- **Repository root**: `/Users/bytedance/Downloads/projects/awesome-mac-sftp`
- **Branch**: `codex/stage2-durable-transfers`
- **Stage 1 merge baseline**: commit `b99fca2f729a8445b20935c69eda52cfa6dbbd28`, tree `1cf952ea743992c685f6bf05a75de43ebe7499a8`
- **Baseline Hosted run**: [29468930350](https://github.com/TyrantLucifer/awsome-sftp-cli/actions/runs/29468930350) — exact merge commit, successful
- **Current milestone**: M2.1 dependency intake; no schema or production database-open path yet

Stage 2 delivers durable transfers only. It must preserve Stage 1 local/SFTP browsing, authentication, workspace recovery and read-only diagnostics. If persistent state cannot be opened safely, all mutation routes remain disabled.

## Initial safety checkpoint

| Check | Result |
|---|---|
| `git status --short --branch` | PASS: clean `main`, tracking `origin/main` before branch creation |
| `git rev-parse HEAD` | PASS: `b99fca2f729a8445b20935c69eda52cfa6dbbd28` |
| `git rev-parse HEAD^{tree}` | PASS: `1cf952ea743992c685f6bf05a75de43ebe7499a8` |
| `git fetch --prune origin` | PASS: local `main` remains synchronized with `origin/main` |
| `gh run view 29468930350 --repo TyrantLucifer/awsome-sftp-cli ...` | PASS: `headSha` is the exact merge commit and all jobs concluded successfully |
| local/remote Stage 2 branch lookup | PASS: branch absent before creation; created once from the verified baseline |
| codebase-memory index | PASS: project index ready; no worktree changes detected at baseline |

No user change was overwritten. Existing ignored IDE and validation artifacts remain outside the candidate tree.

## Zero-gate dependency intake

**Status**: In Progress

Required exact pins:

- `modernc.org/sqlite v1.53.0`
- upstream-resolved `modernc.org/libc v1.73.4`

Evidence to record before any schema or production database open path:

- [ ] Exact tag/commit, license, changelog, retraction, complete selected module graph and reviewed `go.sum` diff.
- [ ] No replace, `latest`, second SQLite driver or loose upgrade; libc remains the upstream resolution.
- [ ] `govulncheck` reachable/imported/required-module findings and applicability disposition.
- [ ] Go 1.26.5 and exact Go 1.25.12 compile/test/tidy/verify gates.
- [ ] darwin/linux × amd64/arm64 `CGO_ENABLED=0` builds.
- [ ] Native macOS and Linux open/transaction/WAL/online-backup smoke.
- [ ] modernc v1.53.0 `NewBackup`, URI pragma ordering and narrow API source-contract check.

## Milestone ledger

### M2.1 — Persistent state-machine foundation

- **Status**: In Progress
- **Goal**: ADR-0008 state store, Version 1 schema, Job/step state machine, transactional events and deterministic restart recovery.
- **Current action**: close the exact dependency intake gate.
- **Last green command**: Stage 1 baseline Hosted run 29468930350; Stage 2 commands not yet recorded.

### M2.2 — Single-file copy, conflict and commit

- **Status**: Not Started
- **Gate**: M2.1 complete locally and in Hosted native evidence.
- **Required MVP**: user-visible local and temporary-sshd single-file copy steps plus real execution evidence.

### M2.3 — Directory copy and dual-remote relay

- **Status**: Not Started
- **Gate**: M2.2 complete with final-name safety and recovery evidence.

### M2.4 — Move, rename, delete and recovery closeout

- **Status**: Not Started
- **Gate**: M2.3 complete with bounded directory/relay resource evidence.

## Failure and repair ledger

No Stage 2 implementation failure has been recorded yet. Each issue is limited to three evidence-driven attempts as required by the repository instructions.

## Final gate ledger

The following remain open until exact-candidate evidence is recorded:

- [ ] `make docs-check`
- [ ] `make check`
- [ ] `make lint`
- [ ] `make supply-chain`
- [ ] `make ci`
- [ ] `go test -race ./...`
- [ ] `GOTOOLCHAIN=go1.25.12 make check`
- [ ] Stage 2 SQLite, migration, Provider mutation, Planner, Job, IPC/event, transfer, conflict, recovery, PTY and performance suites.
- [ ] Four-target build and reproducibility/provenance comparison.
- [ ] Native APFS/ext4/XFS, two-sshd, ProxyCommand, Kerberos, crash/fault, sparse-file/large-tree and secret/pollution evidence.
- [ ] Independent cold-start audit from `docs/README.md`.
- [ ] Exact final SHA Hosted CI green and PR Ready for review.
