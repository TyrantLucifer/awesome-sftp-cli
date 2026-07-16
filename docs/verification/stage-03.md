# Stage 3 Verification Record

- **Status**: Complete — 40/40 Stage 3 matrix rows Verified
- **Updated**: 2026-07-16
- **Repository root**: `/Users/bytedance/Downloads/projects/awesome-mac-sftp`
- **Branch**: `codex/stage3-preview-edit-cache`
- **Stage 2 merge baseline**: commit `8a118d7069e4bf86e4f7e73d6fc41977cf1202f5`, tree `ee1ebdf11b61f1ac05fa0b2a4f23800ab9ba934a`
- **Baseline Hosted run**: [29490490339](https://github.com/TyrantLucifer/awsome-sftp-cli/actions/runs/29490490339) — exact merge commit, successful
- **Last immutable implementation checkpoint**: commit `0833f3fab39848da44c7d69e02f47535f3f60130`, tree `be29e207405d2b7e071229a9d871ffd6e52f80fa`
- **Final candidate commit/tree and exact-SHA Hosted runs**: recorded in the delivery summary of [PR #3](https://github.com/TyrantLucifer/awsome-sftp-cli/pull/3)
- **Delivery PR**: [#3 — feat: deliver the Stage 3 preview edit and cache](https://github.com/TyrantLucifer/awsome-sftp-cli/pull/3), Ready and intentionally unmerged

Stage 3 implements Preview/Jobs/Log drawers, bounded built-in and external preview, a daemon-owned managed cache, durable editor/opener recovery, explicit one-shot command and interactive shell surfaces, and terminal recovery. Stage 1 browsing/auth/workspace behavior and the Stage 2 Planner→Job→part→verify→commit mutation path remain authoritative. Cache, Preview, external-process, Provider and RPC paths do not gain a second write route.

This record keeps repository-verifiable design, local, native and checkpoint evidence durable while PR #3 binds the final immutable SHA/tree to the two exact-SHA Hosted runs. The closeout passed the complete current/oldstable local gate, candidate-tree secret/pollution audit, independent concurrency/recovery review and docs-only cold-start audit before the PR changed from Draft to Ready. No merge is part of this delivery.

## Initial safety checkpoint

| Check | Result |
|---|---|
| `git status --short --branch` | PASS: clean `main`, tracking `origin/main` before branch creation |
| `git rev-parse HEAD HEAD^{tree} origin/main` | PASS: `8a118d7069e4bf86e4f7e73d6fc41977cf1202f5`, tree `ee1ebdf11b61f1ac05fa0b2a4f23800ab9ba934a`, `origin/main` exact match |
| `gh run view 29490490339 --repo TyrantLucifer/awsome-sftp-cli ...` | PASS: `headSha` is the exact merge commit; run and all required jobs concluded `success` |
| baseline `make docs-check` | PASS on exact main |
| baseline `make check` | PASS on exact main |
| branch creation | PASS: `codex/stage3-preview-edit-cache` was created once from the verified baseline |

The first reviewable checkpoint, commit `1b3244b97c074ae8736ac59f0645a959415044fd`, passed both push run [29492926455](https://github.com/TyrantLucifer/awsome-sftp-cli/actions/runs/29492926455) and PR run [29492939398](https://github.com/TyrantLucifer/awsome-sftp-cli/actions/runs/29492939398). These are historical checkpoint evidence, not substitutes for final exact-SHA CI.

## Frozen persistence and resource contracts

### Schema and cache ownership

- Schema head is Version 3. Version 1 remains byte-immutable.
- Version 2, `stage3_cache`, owns cache blobs, entries, materializations, references, leases and edit-session catalog state. Its checksum remains `3e15e4350c117143015526452c9d5e517bed29940bbd8c17c7b5172e69c2d821` and its migration WAL budget is 64 MiB.
- Version 3, `edit_session_recovery`, adds reconstructible recovery/decision inputs without modifying Version 2. Its checksum is `16ae664c033fb1fae7da937eae6c4b19c6b05430fa3499fa5f0da8daa58e1ab4`, migration WAL budget is 16 MiB, and the whole head-3 schema contract digest is `a523d6c4aeebb386780f7283b63aacb175cf0420027114edac51a032425615a2`.
- The standard migration coordinator still provides immutable per-head contracts, original..target set digests, online backup, explicit resume, rollback, retention and WAL enforcement. `TestVersion3ChecksumBudgetAndWholeSchemaContractAreFrozen`, `TestVersion3LeavesVersion2ContractAndChecksumUnchanged`, migration crash tests and native SQLite/statefs tests cover this path.
- Cache content lives below the platform cache root in owner-only `amsftp/content-v1`. Directories are real `0700`; blobs, manifests and materializations are regular `0600` files. Publication is staged, verified, synced, no-replace and revalidated with no-follow/type/mode/owner/link-count checks.

### Frozen budgets

| Surface | Bound |
|---|---:|
| one Provider Preview read | 64 KiB |
| retained Preview window | 512 KiB |
| built-in render input/output | 512 KiB / 512 KiB |
| JSON input/depth | 256 KiB / 64 |
| rendered lines/style spans | 10,000 / 4,096 |
| image metadata pixel guard | 40,000,000 pixels |
| terminal image payload/output/pixels | 4 MiB / 6 MiB / 1,000,000 |
| terminal capability response | 256 bytes with a 200 ms application deadline |
| default cache quota | 2 GiB, 4,096 entries, 1 GiB per workspace |
| eviction candidates per pass | 256 |
| lease heartbeat/expiry/opener grace | 30 s / 2 min / 15 min |
| one-shot command input | 32 KiB UTF-8 single line |
| command stdout/stderr retention | 1 MiB per stream, continuously drained |
| command diagnostic | 32 KiB, single-line and redacted |
| external previewer diagnostic | 4 KiB, single-line and redacted |
| Log in-memory ring/page | 1,000 records / 256 records |
| rotating daemon log | 4 MiB per file by default |
| edit conflict view | 32 KiB per side, 512 lines, 64 KiB output |
| SQLite WAL controller | 4 MiB statement, 8 MiB transaction, 64 MiB soft, 256 MiB hard stop |

The 100 GiB sparse/synthetic preview fixtures assert exact 64 KiB range plans and reject external materialization before any file read when the known size exceeds its rule. Renderer allocation tests bind intermediate growth to output budgets rather than total source size. Final runtime-only `/usr/bin/time -l` measurements (test binaries compiled first into an external `/tmp` root) were: 100 GiB refusal 0.57 s / 11,304,960-byte max RSS / 4,276,800-byte footprint; complete Preview suite 6.37 s / 27,754,496-byte max RSS / 17,924,672-byte footprint; dual 1 MiB command rings 1.01 s / 18,612,224-byte max RSS / 11,731,568-byte footprint. No measurement grew with the declared 100 GiB source size.

## Stage 3 feature evidence

### M3.1 — Drawer and cache foundation

**Implementation status: complete.**

- `K`, `J` and `L` drive a pure `closed|preview|jobs|log` reducer with independent `pane|drawer` focus. Repeated keys open, switch, focus or close without changing pane Locations; `Esc` returns focus; lowercase navigation remains distinct. `TestDrawerReducerOpensFocusesSwitchesAndClosesWithoutChangingPane`, `TestDrawerReducerKeepsLowercaseNavigationSeparateAndRefreshesPreviewGeneration` and `TestTranslateTCellDistinguishesUppercaseDrawerKeys` cover the key contract.
- Normal `100x16`, narrow `32x7`, minimum-size and resize rendering retain two-pane context and a bounded bottom drawer. Workspace schema v2 strictly round-trips drawer tab/focus/rows and `lru|ephemeral|pinned_offline`, migrates v1 deterministically, and never persists Preview/Jobs/Log bodies.
- Jobs consumes the Stage 2 daemon snapshot. Log consumes a redacted 1,000-record daemon ring through validated, capped 256-record Job/Endpoint-filtered pages.
- Cache IDs bind Endpoint + canonical raw path + frozen fingerprint; complete SHA-256 blobs deduplicate across independent entries. Default quotas are deterministic and protect pinned, dirty, deleting, leased, shared, referenced and materialization-reachable state.
- The Version 2 catalog and content-v1 filesystem publish complete verified blobs, entry manifests and materializations in crash-safe order. Eviction first commits a durable deleting claim, deletes only the exact revalidated object and resumes idempotently after restart. Shared blobs survive removal of one reference.
- Live admission enforces global bytes, total catalog object count and per-workspace non-shared bytes before catalog publication. It can evict eligible clean LRU state, serializes concurrent spend, handles cross-workspace dedup correctly, bounds repeated zero-byte handoffs, removes short-read staging content, and returns typed `resource_exhausted` without mutating protected content when no safe plan exists.
- Leases bind owner, daemon instance and optional PID+birth identity. Heartbeat/release, opener grace, live-process preservation, reused-PID rejection, uncertain fail-closed classification and exact restart adoption are covered with an injected clock and native process classifiers.
- Startup lifecycle reclaims an active Preview handoff immediately when its exact process identity is dead; it does not wait for expiry. Cursor queries are capped at 256 leases, exact reference lookup at 2, and the default 64 batches bound one run to 16,384 candidates. Editor/opener/upload handoffs remain retained for explicit recovery even when their process disappeared.
- Startup reconciliation reports crash temps, corrupt manifests, filesystem/catalog inconsistencies and orphan materializations without deleting unknown bytes. Pending claimed evictions resume. Clear is explicit, policy-scoped, bounded and refuses any unreconciled filesystem.
- A corrupt catalog cannot be safely reconstructed solely from filesystem manifests because workspace policy, pin/dirty/reference/lease state is not recoverable from those manifests. The implemented safe behavior is therefore the objective's **diagnostic** branch: preserve all content, disable eviction/clear, report `ErrCacheNeedsAttention`, and require an operator decision. It intentionally does not invent safety-critical metadata or destructively “rebuild” unknown content.
- `pinned_offline` persists and protects verified content. On a typed transport interruption, the daemon serializes selection against online publication, resolves exactly one path/workspace-bound pinned entry, revalidates its filesystem/catalog identity, durably marks its catalog freshness `unknown`, and rechecks uniqueness during handoff. A gated concurrent-publication test proves a second fingerprint commits first and then forces ambiguity rather than materializing the earlier selection. Unpinned content is never substituted; reconnection publishes a newly observed identity.

Primary tests: `internal/cache`, `internal/cachefs`, `internal/cachemanager`, `internal/cacheprocess`, `internal/state/cachestore`, `internal/daemon/cache_router_test.go`, `internal/workspace/schema_v2_test.go`, `internal/tui/cache_control_test.go`, and `internal/tui/drawer_test.go`.

### M3.2 — Built-in, image and external preview

**Implementation status: complete.**

- Head, tail, absolute range and sliding continue use exact 64 KiB reads with a 512 KiB retained cap. Source identity includes Location, fingerprint, file size, request identity, endpoint generation and UI generation. Short/malformed reads and every stale identity mismatch are rejected.
- Built-in renderers provide terminal-safe text, dependency-free bounded code spans, formatted/raw JSON, metadata, hexadecimal binary and image metadata. Invalid UTF-8, NUL, ANSI/control bytes, malformed/deep/partial JSON, arithmetic overflow and malicious output expansion fail safely with explicit partial/warning state.
- Preview selection changes emit cancellation; late request/generation results cannot replace the visible selection. Preview failure remains drawer-local.
- Kitty, iTerm2 and Sixel encoders require an actively confirmed bounded reply. Environment hints only select which active probe to try. None/misprobe/oversize/corrupt/wrong-media cases emit no image bytes and continue through the external previewer or safe built-in metadata fallback.
- External previewers are ordered extension/MIME rules with structured command/args, maximum input, completeness, timeout and redacted diagnostic limits. The frozen executable and canonical materialization are revalidated before direct exec; stdout is discarded, stderr is bounded, and timeout/cancel terminate the process group.
- `TestOrchestrateHundredGiBSparseFileStopsBeforeReadOrMaterialize` proves the 100 GiB case is rejected from known metadata before reading or materializing. Planner tests separately prove range offsets near 100 GiB and `math.MaxUint64` without overflow.
- Real protocol evidence: official Kitty 0.47.4 DMG SHA-256 `b53b9b18a27d53ad44a25dd6776fde8c47487b4e103ac50d682af1ee8e7b77ed` was mounted read-only on macOS arm64. With `AMSFTP_REAL_KITTY` pointing to that binary, `TestStage3RealKittyImageProtocol` actively received the exact `i=31;OK` reply and emitted a 131-byte bounded image sequence for a 73-byte PNG. The test passed in 0.01 s (`internal/app` package 0.374 s). The committed test is opt-in and skips unless a real Kitty executable is supplied.
- Full object metadata Preview renders bounded, terminal-sanitized Endpoint, canonical path, kind, size, mtime, permissions, symlink target, fingerprint strength and available hash. File, directory and symlink metadata requests use Provider stat facts and perform zero content reads/materializations; hostile Provider fields are bounded before composition. Binary file metadata uses the same safe view.

Primary tests: `internal/preview/{planner,render,protocol}_test.go`, `internal/externalpreviewer/*_test.go`, `internal/app/{preview_orchestration,terminal_image_probe,terminal_image_native}_test.go`, and `internal/tui/preview_test.go`.

### M3.3 — Editor and default opener

**Implementation status: complete and verified.**

- `e`/`o` route only regular files. A complete verified cache materialization and lease are created before confirmation and handoff. The TUI displays the frozen executable/arguments, suspends through one idempotent handoff controller, then restores foreground process group, termios, alternate screen, cursor and resize handling before tcell resumes input.
- Editor precedence is explicit structured config → `VISUAL` → `EDITOR` → `nvim` → `vim` → `vi`. The bounded lexer accepts quote/backslash grouping but rejects expansion, substitution, glob, pipes, redirects, operators and control bytes. PATH is discovery-only; the canonical absolute regular executable and canonical absolute materialization are revalidated immediately before direct exec, with the file as the final independent argument.
- macOS defaults to fixed `/usr/bin/open`; Linux defaults to fixed `/usr/bin/xdg-open`. Custom openers use the same structured boundary. An opener retains the lease through heartbeat/grace and can only initiate change processing through the explicit “check changes” path; possible modification never authorizes automatic upload.
- Post-editor observation hashes content rather than trusting mtime. No local change creates no upload; remote-only change offers refresh; local-only change requires explicit upload; concurrent modify/delete/replace or unreliable stat enters conflict.
- The edit baseline uses the SHA-256 calculated while streaming Provider bytes into the cache. Remote re-observation obtains a bounded Provider-stream hash, so a same-size/same-second rewrite with an unchanged metadata fingerprint is still a conflict. The final protocol avoids a non-atomic SHA CAS: it no-replace moves the original to a deterministic job preservation path, then validates its full SHA/size within 2 GiB and 15 minutes, and publishes the part no-replace. Tests inject a same-fingerprint change before preservation (restored + `waiting_conflict`) and an old-inode write after strong verification (successful edited final plus preserved concurrent bytes).
- Conflict offers a bounded remote→local diff, save-as, skip/retain and explicit overwrite with no default. Remote content is read under the observed precondition and rechecked; local content is no-follow/private/hash revalidated. All decisions remain bound to the same durable edit session.
- Sync-back atomically creates/binds the Stage 2 Plan and Job. Completion atomically advances the baseline and releases the handoff; failure preserves the dirty materialization. Client/daemon restart reconstructs the exact handoff, decision inputs and conflict view, and still requires an explicit decision before queueing.
- Native local macOS evidence: real `/usr/bin/vim` direct-exec changed a private materialization; `/usr/bin/open -R` passed after confirming a LaunchServices session; local `/bin/sh` foreground PTY handoff returned to the two-pane TUI. Neovim was not installed in that local environment and was skipped there, not claimed as a local pass.
- The Linux quality gate installs missing `neovim`, `vim`, `xdg-utils` and `openssh-server`, then requires real Vim/Neovim materialization, `/usr/bin/xdg-open`, local/remote command and shell tests. Its final exact-SHA result is bound in PR #3's delivery summary.

Primary tests: `internal/edit/*_test.go`, `internal/externalprocess/*_test.go`, `internal/app/edit_workflow_test.go`, `internal/state/{editstore,jobstore}/*_test.go`, `internal/tui/edit_session_test.go`, and `internal/integration/stage3_native_test.go`.

### M3.4 — Command, shell and platform closeout

**Implementation status: complete and verified.**

- `!` is entered only from explicit Normal-mode input and shows Endpoint, canonical cwd, shell and transport before confirmation. Input must be one valid UTF-8, NUL/CR/LF-free line of at most 32 KiB.
- Local `!` sets `cmd.Dir` and passes `shell`, `-c`, user text as separate values. Remote `!` freezes ADR-0001 fresh `ssh -T` argv with forwarding, GSS delegation and ControlMaster disabled; a root-owned utility preflight and byte-safe POSIX-Q cwd bootstrap must produce stdout byte 0 before any user-command byte is written.
- Stdout and stderr drain concurrently into independent 1 MiB rings. Discard counts remain visible. Timeout/cancel kills the local process group; remote cancellation that may have detached effects is explicitly `remote effect unknown`. Every completion refreshes the frozen pane.
- Local `gs` starts the user's absolute shell in the pane cwd. Remote home uses fresh `ssh -tt` with no command after the host. Remote current-cwd requires a separate successful byte-safe probe; formal failure restores the terminal before explicitly offering a home retry.
- Interactive shell terminal bytes are neither captured nor logged. The handoff controller gives the child the foreground TTY, forwards SIGWINCH, then restores pgrp, termios, alternate screen and cursor once. Terminal image capability is actively re-probed after every editor/opener/shell handoff and before tcell resumes input.
- If spawn succeeds but foreground-pgrp transfer fails, the controller terminates and reaps that child before restoring the TUI. An abnormal stalled Terminate or Wait is bounded to 2 seconds: the TUI restores with an explicit diagnostic while the single buffered cleanup worker continues, and deterministic gates prove that worker finishes after the stall is released.
- Unit tests cover exact argv, conflicting SSH settings, quoted/raw cwd bytes, unsafe utilities, banners/marker EOF, marker-before-input, dual-stream flooding, process-group cancellation, home/current-cwd, `-tt`, explicit fallback and state-machine idempotence.
- TUI command state is single-flight. Confirmation names local `shell -c` or remote fresh `ssh -T`/cwd-marker/no-fallback semantics; `Esc` cancels the active run, the application applies a 15-minute user-flow deadline, and completion remains bounded and refreshes the frozen pane.
- Native local evidence: `TestStage3LocalShellForegroundPTY` entered `/bin/sh` at the exact pane cwd, wrote a marker through the foreground PTY, exited and observed `shell exited; pane refreshed` before clean TUI exit.
- The native harness drives both `!` and `gs`: it waits for the one-shot command input/confirmation surfaces, writes an exact-cwd marker, observes `command exit 0`, then enters the foreground shell, writes its exact-cwd marker and observes restored TUI state. The temporary-sshd variant runs the same sequence through real remote OpenSSH; the final exact-SHA Hosted result is bound in PR #3's delivery summary.
- No embedded terminal emulator, shell RPC, Provider command route or Helper execution path was added.
- ADR-0010 handoff is fail-closed. The repository contains a deterministic public-key-ID verifier, public RFC 8032 test-vector fixture, offline custody/recovery runbook, revoke-window proposal and public-only rotation tabletop. It contains no production key/private material. Because real offline stations, named custodians, recovery and production dual-key rotation are absent, Helper distribution is explicitly **CLOSED** and Stage 4 may begin only Level 0 work.

Primary tests: `internal/commandrun/*_test.go`, `internal/tui/command_shell_test.go`, `internal/app/{external_command,external_shell}_test.go`, `internal/terminalhandoff/*_test.go`, `internal/integration/stage3_native_test.go`, and `docs/security/verify-helper-public-keys.go`.

### Feature Matrix verification results

The milestone evidence above and the final gate ledger below close every Stage 3 row promoted to `Verified`.

| ID | Result | Evidence |
|---|---|---|
| WORK-005 | PASS | Workspace v2 round-trip, migration and explicit LRU/ephemeral/pinned-offline selection pass. |
| VIM-016 | PASS | Preview/Jobs/Log drawer reducer, translation and bounded layout snapshots pass. |
| VIM-017 | PASS | Regular-file-only editor/opener routing and complete handoff workflows pass. |
| VIM-019 | PASS | Normal-mode `!`/`gs` sequences, confirmation and local/remote routing pass. |
| PREV-002 | PASS | Bounded code spans and formatted/raw JSON with safe fallback pass. |
| PREV-003 | PASS | Bounded metadata for file/directory/symlink with zero content read/materialization passes. |
| PREV-004 | PASS | Fixed read/retained-output budgets, cancellation and 100 GiB early refusal pass. |
| PREV-005 | PASS | Exact bounded head/tail/range offsets, including near-100-GiB inputs, pass. |
| PREV-006 | PASS | Active proof-gated Kitty/iTerm2/Sixel detection and real Kitty evidence pass. |
| PREV-007 | PASS | Protocol→external→metadata fallback and lease release pass. |
| PREV-008 | PASS | Ordered structured previewer rules, direct exec, timeout and cancellation pass. |
| PREV-009 | PASS | Binary/invalid UTF-8/control-byte terminal-safe rendering and fuzz bounds pass. |
| PREV-010 | PASS | Cancellation plus full identity/generation mismatch rejection pass. |
| EDIT-001 | PASS | Terminal editor suspend/resume, exit matrix and real Vim path pass. |
| EDIT-002 | PASS | Vim-first precedence, restricted lexer, canonical direct-exec plan and revalidation pass. |
| EDIT-003 | PASS | Verified materialization, lease/heartbeat/adoption and offline handoff pass. |
| EDIT-004 | PASS | Content-based no-change/change classification without mtime trust passes. |
| EDIT-005 | PASS | Metadata plus Provider-stream hash concurrent-remote-change detection passes. |
| EDIT-006 | PASS | Bounded diff, save-as, retain and explicit overwrite durable decisions pass. |
| EDIT-007 | PASS | Stage 2 Job binding and no-replace original preservation/publication race matrix pass. |
| EDIT-008 | PASS | Fixed macOS/Linux opener plans, canonical argument and native harness pass. |
| EDIT-009 | PASS | Lease/grace plus explicit change check and no automatic upload pass. |
| EDIT-010 | PASS | TTY restore, image re-probe and single bounded stalled-cleanup gate pass. |
| CACHE-001 | PASS | Global/object/workspace live admission and protected LRU eviction pass. |
| CACHE-002 | PASS | Verified SHA-256 dedup, exact-location idempotency and shared reachability pass. |
| CACHE-003 | PASS | Ephemeral safe clear with shared-blob preservation passes. |
| CACHE-004 | PASS | Serialized exact pinned-offline selection, durable unknown freshness and ambiguity refusal pass. |
| CACHE-005 | PASS | Owner heartbeat, PID+birth classification, adoption and owner-specific reclaim pass. |
| CACHE-006 | PASS | Typed fingerprint identity and stale/unknown fail-closed decisions pass. |
| CACHE-007 | PASS | Bounded keyset reconciliation, dead-Preview reclaim and unknown-byte preservation pass. |
| CACHE-008 | PASS | Private no-follow cache filesystem and secret/body-free strict metadata pass. |
| CACHE-009 | PASS | Explainable quota diagnostics, safe clear and ENOSPC rollback pass. |
| SHELL-001 | PASS | Exact local/remote argv, marker-before-input, dual bounded drains and cancellation pass. |
| SHELL-002 | PASS | Exact local/remote PTY, cwd probe/fallback and terminal ownership pass. |
| SHELL-003 | PASS | Direct system process/TTY handoff with no embedded emulator passes. |
| SHELL-004 | PASS | Explicit-only shell reachability and byte-safe generated bootstrap pass. |
| SEC-008 | PASS | Structured external execution, explicit command surfaces and closed Helper route pass. |
| SEC-010 | PASS | Private cache/state modes, ACL/no-follow validation and native filesystem gates pass. |
| OBS-001 | PASS | Bounded drawer/log state with contextual filters and no persisted preview body passes. |
| PLAT-004 | PASS | Baseline terminal fallback, active image probes and synchronized post-handoff re-probe pass. |

## Native and Hosted evidence matrix

| Surface | macOS local/native | Linux/Hosted | Current conclusion |
|---|---|---|---|
| cache/state filesystem | APFS root validation, owner/mode/no-follow and native SQLite/WAL tests | ext4 and loop-mounted XFS state suites; XFS ENOSPC fixture is in CI | local and exact-SHA Hosted gates green |
| editor | real Vim direct exec passed; Neovim unavailable/skipped locally | Linux quality gate provisions Vim/Neovim and requires both subtests | native scope green; exact-SHA Hosted proof in PR #3 |
| opener | real `/usr/bin/open -R` passed with LaunchServices | isolated `/usr/bin/xdg-open` desktop handler in native test | native scope green; exact-SHA Hosted proof in PR #3 |
| shell/command | real local `/bin/sh` foreground PTY/cwd/resume passed | one harness drives real local/temporary-sshd `!` plus `gs`, exact cwd and TUI return | native scope green; exact-SHA Hosted proof in PR #3 |
| terminal images | real Kitty 0.47.4 active-probe/output passed | controlled Kitty/iTerm2/Sixel/none snapshots and fail-closed tests | Kitty real proof complete |
| auth/transport regression | inherited Stage 1 OpenSSH/ProxyCommand/Kerberos suites | Hosted auth-integration remains a required job | checkpoint and final exact-SHA Hosted gates green |

## Local verification ledger

The following commands have passed on the current Stage 3 line during implementation:

```text
go test ./internal/edit ./internal/app ./internal/tui ./internal/integration -count=1
go test -race ./internal/edit ./internal/app ./internal/tui ./internal/integration -count=1
go test ./internal/... -count=1
go test -race ./internal/app -count=1
make lint                         # 0 issues on local macOS
```

The real Kitty opt-in command also passed as described above. Focused cache, Preview, edit, command/shell, migration, native Vim/open/local-shell and repeated race tests passed throughout their RED→GREEN commits.

At code checkpoint `0833f3f`, the combined affected-package normal suite, the same affected surface under the race detector, and `make lint` were rerun and passed. Its exact push [29510260905](https://github.com/TyrantLucifer/awsome-sftp-cli/actions/runs/29510260905) and PR [29510265214](https://github.com/TyrantLucifer/awsome-sftp-cli/actions/runs/29510265214) workflows are both green, including native editor/opener/command/shell legs. The subsequent independent closeout review found and regression-tested seven concurrency/restore gaps before final promotion: atomic provider-boundary preservation, weak metadata identity, clear/admission serialization, immediate bounded dead-Preview reclaim, offline selection/publication ambiguity plus durable unknown freshness, bounded catalog list APIs, and stalled post-spawn cleanup.

Final local closeout on macOS arm64/APFS used only external `/tmp` build/coverage roots:

| Command | Result |
|---|---|
| `go test ./internal/... -count=1` | PASS |
| `go test -race ./... -count=1` | PASS |
| `BUILD_DIR=/tmp/... COVERAGE_DIR=/tmp/... make ci` | PASS: check, lint (0 issues), repository race, four fuzz smokes, supply chain/actionlint and darwin/linux × arm64/amd64 builds |
| `GOTOOLCHAIN=go1.25.12 BUILD_DIR=/tmp/... COVERAGE_DIR=/tmp/... make check` | PASS |
| root and `tools/` `go mod tidy -diff` / `go mod verify` | PASS under current and oldstable gates |
| `go test ./internal/integration -run '^TestStage3' -count=1 -v` | PASS: real Vim, macOS opener and local foreground PTY; local Neovim/temporary-sshd correctly skipped, covered by provisioned Hosted legs |
| `make docs-check` and `git diff --check` | PASS after final truth-chain promotion |
| isolated `FuzzWireBytes` after one loaded-suite deadline | PASS at about 350k executions; the subsequent complete `make ci` fuzz sequence passed all four smokes |

## Hosted history and final delivery

- The prior `6b036a0` push/PR failures identified Linux `gosec` fixture findings and a macOS empty PID-file observation race. Commit `ff60f4d` fixed both without weakening product assertions.
- Commit `03c9c77` added the real-Kitty gate and bounded Darwin TTY zero-read handling. Commits `725f8b9`, `2ae1aa9` and `0833f3f` closed process cleanup, live cache admission, object metadata, pinned-offline reopen, strong edit preconditions and native command/editor gaps.
- Exact checkpoint `0833f3fab39848da44c7d69e02f47535f3f60130`, tree `be29e207405d2b7e071229a9d871ffd6e52f80fa`, passed complete push [29510260905](https://github.com/TyrantLucifer/awsome-sftp-cli/actions/runs/29510260905) and PR [29510265214](https://github.com/TyrantLucifer/awsome-sftp-cli/actions/runs/29510265214) workflows. This proves the full native/current/oldstable/build/reproducibility/auth matrix through that checkpoint.
- The final evidence commit additionally contains the independent-review fixes listed above. PR #3 records its exact commit/tree and the successful push and PR workflow URLs; the historical `0833f3f` greens are checkpoint evidence only.

## Final evidence gates

1. [x] Complete current and exact Go 1.25.12 local gates, runtime resource measurements and independent High/Medium code review.
2. [x] Complete candidate secret/pollution inventory and exact committed-tree archive inventory; no product cache/build/coverage/binary or secret entered the candidate.
3. [x] Complete the independent cold-start audit from `docs/README.md` only with no unresolved High/Medium finding.
4. [x] Obtain green push and PR Hosted runs at the exact final SHA, including provisioned Vim/Neovim/xdg-open/sshd and real local/remote `!`/`gs`.
5. [x] Make PR #3 Ready without merging and prove the clean branch is synchronized with origin.

## Security, privacy and pollution evidence

- Cache filesystem tests reject symlinks, special/linked/public/wrong-owner/wrong-digest objects; a wide umask cannot widen explicit private modes.
- Catalog tests reject unknown enums/contracts and prove fingerprints remain typed metadata. Manifest tests reject unknown/secret fields and never serialize file bodies, credentials, preview text or command output.
- Preview and command diagnostics escape terminal controls and are byte-bounded; external previewer diagnostics redact configured values. Shell terminal bytes and command streams are not persisted by default.
- The existing real auth/Kerberos scans continue to prove injected passwords, MFA answers, key passphrases and ticket material do not enter config/state/cache/log/workspace artifacts.
- Repository builds, coverage and native fixtures use external temp roots. No cache database, blob, materialization, sidecar, coverage profile or product binary is intended to enter the candidate tree.
- `git ls-files --cached --others --exclude-standard` found only the intended Stage 3 source/document files. Pollution-name, binary-diff, secret-filename and high-confidence private-key/cloud-token content scans returned zero matches. Exact committed `git ls-tree`/archive inventory and the clean synchronized status are recorded in PR #3's delivery summary; ignored user `.idea/`, `.superpowers/`, `coverage/` and `dist/` remain outside the candidate.

## Exit checklist status

The eleven user-visible/architectural checks are complete in [the Stage 3 specification](../stages/03-preview-edit-cache.md#6-可验证退出标准). The explicitly CLOSED Helper distribution outcome is the permitted fail-closed result of the final item. The full local/current-oldstable ledger, exact-SHA push/PR Hosted runs, resource/security/pollution evidence, docs-only cold-start audit and clean Ready-but-unmerged PR all agree on the final delivery.

## Known limitations and Stage 4 boundary

- Automatic catalog reconstruction from filesystem manifests is intentionally unavailable because it would lose or invent policy, pin, dirty, reference and lease authority. Corruption enters non-destructive diagnostic preservation; only known claimed evictions and explicit safe cleanup proceed.
- Built-in syntax highlighting is deliberately dependency-free and bounded rather than a full editor grammar engine.
- Only PNG is emitted through terminal image protocols; other image types receive bounded metadata/external fallback. Kitty is the one real protocol proof; iTerm2 and Sixel remain controlled protocol fixtures.
- Neovim was unavailable in the local native environment; the Linux quality gate provisions and executes it, while the local result remains an explicit skip rather than a false native claim.
- GUI opener lifetime cannot prove an application has stopped using a file. The lease therefore uses heartbeat/grace and explicit “check changes”; it never auto-uploads.
- A successful sync-back deliberately retains the Job-reported hidden original-preservation sibling; AMSFTP does not auto-delete that safety copy.
- Helper download/upload/install/signing is not implemented. Production custody is CLOSED; Stage 4's only authorized first action is Level 0 SFTP search work unless real custody evidence later opens the Helper gate.
