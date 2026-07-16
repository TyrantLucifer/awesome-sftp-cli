# Stage 2 Verification Record

- **Status**: Complete
- **Updated**: 2026-07-16
- **Repository root**: `/Users/bytedance/Downloads/projects/awesome-mac-sftp`
- **Branch**: `codex/stage2-durable-transfers`
- **Stage 1 merge baseline**: commit `b99fca2f729a8445b20935c69eda52cfa6dbbd28`, tree `1cf952ea743992c685f6bf05a75de43ebe7499a8`
- **Baseline Hosted run**: [29468930350](https://github.com/TyrantLucifer/awsome-sftp-cli/actions/runs/29468930350) — exact merge commit, successful
- **Current milestone**: M2.4 complete; Stage 2 exit evidence closed

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

**Status**: Complete

Admitted exact pins:

- `modernc.org/sqlite v1.53.0`: tag `v1.53.0`, commit `6b32d1ee965dfe59bf2e50baeb6f451b67d6a71e`, module sum `h1:20WG8N9q4ji/dEqGk4uiI0c6OPjSeLTNYGFCc3+7c1M=`, `go.mod` sum `h1:xoEpOIpGrgT48H5iiyt/YXPCZPEzlfmfFwtk8Lklw8s=`.
- Upstream-resolved `modernc.org/libc v1.73.4`: tag `v1.73.4`, commit `70624da7facfac5a8d2f9b70cba0288b68b5ad01`, module sum `h1:+ra4Ui8ngyt8HDcO1FTDPWlkAh6yOdaO2yAoh8MddQA=`, `go.mod` sum `h1:DXZ3eO8qMCNn2SnmTNCiC71nJ9Rcq3PsnpU6Vc4rWK8=`.

Both candidates are unretracted exact tags, require Go 1.25, and are BSD-3-Clause licensed. The SQLite tag embeds SQLite 3.53.2. The libc distribution also carries the upstream Go BSD and musl MIT notices. Release/tag metadata and the upstream SQLite `go.mod` were reviewed; the latter selects this exact libc version. No `replace`, `latest`, second SQLite driver, or loose modernc upgrade was introduced.

The selected additions are `github.com/dustin/go-humanize v1.0.1`, `github.com/google/pprof v0.0.0-20250317173921-a4b03ec1a45e`, `github.com/google/uuid v1.6.0`, `github.com/hashicorp/golang-lru/v2 v2.0.7`, `github.com/mattn/go-isatty v0.0.20`, `github.com/ncruces/go-strftime v1.0.0`, `github.com/remyoudompheng/bigfft v0.0.0-20230129092748-24d4a6f8daec`, `modernc.org/cc/v4 v4.28.4`, `modernc.org/ccgo/v4 v4.34.4`, `modernc.org/fileutil v1.4.0`, `modernc.org/gc/v2 v2.6.5`, `modernc.org/gc/v3 v3.1.3`, `modernc.org/goabi0 v0.2.0`, `modernc.org/libc v1.73.4`, `modernc.org/mathutil v1.7.1`, `modernc.org/memory v1.11.0`, `modernc.org/opt v0.2.0`, `modernc.org/sortutil v1.2.1`, `modernc.org/sqlite v1.53.0`, `modernc.org/strutil v1.2.1`, and `modernc.org/token v1.1.0`. The reviewed `go.sum` delta is 49 additive lines. License obligations are compatible with the project: MIT/BSD/Apache-2.0 notices must be retained; HashiCorp LRU's MPL-2.0 file-level source and notice obligations apply if distributed; modernc libc/memory third-party notices must be preserved.

The source-contract test freezes the narrow behavior used by the future backup path: `NewBackup` creates the destination through `newConn`, URI `_pragma` values are applied before `sqlite3_backup_init`, repeated pragmas execute in encoded order, multi-statement `Exec` consumes SQL tails, and the upstream module continues to select libc v1.73.4. The native smoke proves SQLite 3.53.2 open, rollback, commit, a non-empty WAL frame, `Step(-1)`/`Commit` online backup, destination `checkpoint_fullfsync=1`, `fullfsync=1`, `synchronous=FULL`, and the committed row in the backup. It runs only against a temporary generic intake database; production state opening remains absent.

Evidence before any schema or production database open path:

- [x] Exact tag/commit, license, changelog/tag metadata, retraction, complete selected module graph and reviewed `go.sum` diff.
- [x] No replace, `latest`, second SQLite driver or loose upgrade; libc remains the upstream resolution.
- [x] `govulncheck`: zero reachable and zero imported-package findings. The only required-module-only finding is pre-existing GO-2026-5932 in uncalled/unimported `golang.org/x/crypto/openpgp`; it is not introduced or made applicable by this intake.
- [x] Go 1.26.5 and exact Go 1.25.12 compile/test/tidy/verify gates.
- [x] darwin/linux × amd64/arm64 `CGO_ENABLED=0` package and product builds.
- [x] Native macOS APFS open/transaction/WAL/online-backup smoke on Darwin arm64 with 58 GiB free.
- [x] Native Linux open/transaction/WAL/online-backup smoke on exact commit `6959a5ea58aa4a9f6601a10bd91da7164ec891ad`: Ubuntu 22.04 and 24.04 native jobs passed in [Hosted run 29470643854](https://github.com/TyrantLucifer/awsome-sftp-cli/actions/runs/29470643854).
- [x] modernc v1.53.0 `NewBackup`, URI pragma ordering and narrow API source-contract check.

Local command ledger:

| Command | Result |
|---|---|
| `go test ./internal/state/sqlite` | PASS on Go 1.26.5, Darwin arm64/APFS |
| `GOTOOLCHAIN=go1.25.12 go test ./internal/state/sqlite` | PASS |
| `go test -race ./internal/state/sqlite` | PASS |
| four `CGO_ENABLED=0 GOOS={darwin,linux} GOARCH={arm64,amd64} go test -c ./internal/state/sqlite` commands | PASS |
| current and Go 1.25.12 root/tools `go mod tidy -diff` and `go mod verify` | PASS; all modules verified |
| `go list -m -retracted modernc.org/sqlite@v1.53.0 modernc.org/libc@v1.73.4` | PASS; neither version retracted |
| `go tool -modfile=tools/go.mod govulncheck -show=verbose ./...` | PASS; zero reachable/imported findings; one pre-existing required-module-only finding |
| `GOTOOLCHAIN=go1.25.12 make check` | PASS |
| `make lint` | PASS; zero issues |
| external-output `make ci` | PASS, including race, fuzz smoke, supply chain, actionlint and four product targets |

## Milestone ledger

### M2.1 — Persistent state-machine foundation

- **Status**: Complete
- **Goal**: ADR-0008 state store, Version 1 schema, Job/step state machine, transactional events and deterministic restart recovery.
- **Completion gate**: local current and exact Go 1.25.12 suites pass; both Linux native jobs for exact SHA `f83aa45de9b83f42d6f64944401ddde0e1e92d01` pass the full persistent-state suite on asserted ext4 and a private XFS mount, including a real XFS `ENOSPC` transaction rollback and clean restart.
- **Implemented foundation**: checksum-v1 golden and real-v1 digest; frozen original..target migration-set digest; strict attempt lifecycle with only `ready` auto-continuing; single-statement lexer/main-only admission; explicit `BEGIN IMMEDIATE` runner with history+attempt-prefix commit; exact runner/domain DDL; 24,495-byte whole-schema contract (`659edd23b5bc332b488a171c920815daffef6223ef2d3859215ba177c3d55e64`); APFS/ext4/XFS gate; raw identity; same-binary cross-process WAL/locking/full-sync probe; durable-intent/no-replace bootstrap; runtime validation; transactional Job/event store and conservative restart pause before bind. The probe launches the exact current executable with an empty environment and a five-second lifetime, passes the random database path and commands only over inherited descriptors 3/4, verifies a distinct child PID, reads a parent marker that remains in a physical WAL frame, observes bounded child writer exclusion, commits after release, reaps the child, full-syncs main/WAL/root, and treats startup/protocol/crash failure as fatal. The online backup path keeps modernc source use inside `sql.Conn.Raw`, snapshots live WAL, verifies the fixed destination durability pragmas before sanitize, removes only the matching attempt, installs the restore hold, performs quick/FK/history/whole-schema and immutable checks, hashes/full-syncs, publishes no-replace, syncs the directory, then atomically catalogs the backup and marks the source attempt `ready`. Exact partial temp and published-final restart paths are bounded; collisions durably mark `failed`. Backup validation is per-original-head rather than fixed to V1. Catalog time is monotonic across wall-clock rollback and overflow fails closed. The space gate uses checked `page_count × page_size + max(pending WAL budget) + 64 MiB` arithmetic and unprivileged filesystem availability. Retention validates the protected top two and candidate hashes/attrs/sidecar absence, blocks active attempts and restore holds, pins the root directory descriptor, marks one full catalog snapshot `deleting`, unlinks no-follow relative to the pinned directory, syncs that same directory, and only then removes the exact catalog row; present/missing crash continuations and latest/sole/corrupt protections are covered. Runtime write batches now freeze 4 MiB per-statement and 8 MiB per-transaction budgets, a global 8 MiB reservation ceiling, 64/256/264 MiB pressure boundaries, at most 256 operations, and 2-second readers. The guard measures the private no-follow WAL after each statement and boundary, preflights worst-case growth, runs PASSIVE after every bounded committed batch, and requires an all-zero idle TRUNCATE after restart recovery. Version 2+ migrations perform the same exact physical WAL measurement, reject an oversized uncommitted prefix before commit, preserve a committed prefix with a typed post-commit violation, and truncate successfully applied migration WAL. The integrated coordinator verifies exact history and whole-schema contracts at every committed prefix, freezes one original..target set, creates/reuses one backup, requires explicit resume for non-`ready` states, clears the completed attempt, reconciles retention, runs quick/FK/TRUNCATE, closes all connections, proves sidecar absence, validates the target immutably, and only then reopens the runtime pool. Fresh V1→V3 and interrupted-running explicit-resume tests cover the multi-pending and backup-inode reuse paths.
- **Crash evidence**: a real child process is terminated after bootstrap intent persistence, temporary creation, Version 1 commit, temporary full sync, no-replace publication, publication directory sync and intent removal; consecutive pre-publication deaths retain only one bounded claimed generation, and every restart converges to exactly one immutable Version 1 final. A separate abrupt runtime writer exit leaves a committed physical WAL that startup recovers, verifies and truncates while retaining the committed row. Migration child deaths after the statement, history row, attempt-head update and pre-commit budget all recover the unchanged Version 1 prefix; death immediately after `COMMIT` recovers schema/history/attempt at Version 2 together, after which both paths validate and clear the same frozen attempt.
- **Fail-closed daemon evidence**: corrupt project bytes, a valid database carrying a newer history head, and a private but non-owner-writable database are preserved byte-for-byte and with unchanged attributes while the daemon starts with an in-memory diagnostic sink, exposes the Stage 1 local endpoint, rejects persistent workspace state as unsupported, and creates neither a workspace store nor a persistent log.
- **Restart-state evidence**: `TestStateTransitions` exhaustively checks every state pair; the transactional store rejects illegal transitions and keeps event sequence monotonic; `TestDaemonRestartRecoversEveryNonterminalJobStateDeterministically` starts the real daemon over every persisted nonterminal state and proves only `running`/`verifying` plus active steps conservatively become `paused`, while stable draft/confirmation/queue/wait/control states remain byte-for-byte semantic equivalents. Recovery is idempotent and emits exactly one event per changed Job.
- **Native filesystem evidence**: both Ubuntu native legs for exact SHA `3a8ec31d6a7f7afdaf7f6aa1a44e546cfc2145f6` first asserted the trusted state fixture is ext4, then formatted and mounted a private 512 MiB XFS image and passed all state, migration, WAL, job-store, bootstrap, probe and process-death suites there. Full [Hosted run 29475833368](https://github.com/TyrantLucifer/awsome-sftp-cli/actions/runs/29475833368) succeeded. Both Linux native jobs for exact SHA `f83aa45de9b83f42d6f64944401ddde0e1e92d01` then repeated that matrix and passed `TestNativeXFSDiskFullRollsBackWithoutFalseCommit` after filling the XFS filesystem to a real `SQLITE_FULL`/`ENOSPC`; restart proved the failed row absent. See [Hosted run 29476167115](https://github.com/TyrantLucifer/awsome-sftp-cli/actions/runs/29476167115).
- **Last green command**: current `make check`, current M2.1 package matrix, exact Go 1.25.12 app/state/statefs packages, and the focused process-death race passed; `make ci` included full unit/contract/race, lint, fuzz, supply-chain, actionlint and four product builds. Exact predecessor SHA `1ec9097448d0ec40d32f0a87aeeb822e5651d381` passed all native, oldstable, quality, auth, build, reproducibility and comparison jobs in [Hosted run 29475259444](https://github.com/TyrantLucifer/awsome-sftp-cli/actions/runs/29475259444).

## M2.1 feature evidence

| Feature ID | Result | Evidence |
|---|---|---|
| DAEM-006 | PASS | Exact ADR-0008 state gates, single-writer/busy behavior, process-death recovery and fail-closed degradation pass locally; both Linux native jobs in run 29476167115 pass the complete matrix on ext4 and XFS plus real XFS ENOSPC rollback. |
| JOB-002 | PASS | Exhaustive state-pair, transactional transition/idempotency, monotonic event and deterministic real-daemon restart tests pass under current and exact Go 1.25.12; Hosted quality for the native candidate passed. |

### M2.2 — Single-file copy, conflict and commit

- **Status**: Complete
- **Gate**: satisfied by the M2.1 local and exact-SHA Hosted native evidence above.
- **Required MVP**: user-visible local and temporary-sshd single-file copy steps plus real execution evidence.
- **Current implementation checkpoint**: the shared `MutableProvider` contract now runs against Fake, LocalFS and protocol SFTP. LocalFS mutations use Go's rooted filesystem handle so parent symlinks cannot escape the configured root; final no-replace publication uses an atomic hard-link appearance followed by separately observable part cleanup. SFTP exposes `write` only when the current server session advertises both `fsync@openssh.com` and `hardlink@openssh.com`; weak servers retain read-only capability instead of receiving an unsafe fallback. Frozen `FileRef`/Intent/Plan tests cover source identity, capability revisions, local/SFTP relay routes, conflict policy and caller mutation after freeze. The complete immutable Plan, including its original requested name and endpoint descriptors, is persisted and cross-checked before daemon restart execution. The bounded worker writes only the same-directory Job part, persists SHA-256/checkpoint state, rereads for verification, rechecks the final at commit, and proves postconditions after an indeterminate rename response. A bounded daemon-owned manager owns client-independent contexts, reloads queued work, retains frozen endpoint leases before returning a queued Job, rehydrates exact descriptors after restart, and releases leases after execution. Initial and commit-time conflicts are durable rows; opening or resolving a conflict atomically changes Job state and emits the matching event. Overwrite, skip, auto-rename and Job-local apply-all resolutions resume the immutable plan, while pause, cancel, auth resume and retry-wait resume remain durable controls. `y` and `d` capture immutable source refs, `p` creates the Job against the current destination, and `J` opens a bounded Jobs view with state, phase, source/final, items, bytes, waiting reason, terminal summary and controls. M2.4 now executes cut as the safe move sequence or reports `completed_with_source_retained` with its exact source-retention reason.
- **Focused evidence**: `make check`, `make lint`, `go test ./internal/state/jobstore ./internal/transfer ./internal/daemon ./internal/tui ./internal/app`, and `go test -race ./internal/state/jobstore ./internal/transfer ./internal/daemon` pass. Exact SHA `e5b5cd287b1519b235d8444262cc83fdfa76ed51` passed both complete Hosted runs [29479576412](https://github.com/TyrantLucifer/awsome-sftp-cli/actions/runs/29479576412) and [29479579080](https://github.com/TyrantLucifer/awsome-sftp-cli/actions/runs/29479579080), closing the cross-platform checkpoint fingerprint repair. The user-visible TUI checkpoint `274b0ecd69cdc8a8117718997add18c4760c9080` then passed both complete Hosted runs [29480204995](https://github.com/TyrantLucifer/awsome-sftp-cli/actions/runs/29480204995) and [29480207927](https://github.com/TyrantLucifer/awsome-sftp-cli/actions/runs/29480207927). The guarded real-OpenSSH fixture performs both local→SFTP and SFTP→local worker copies and requires the real server's durable write capability.
- **Hosted completion gate**: exact SHA `811ce6b90364446612721ba7cb809a284d633521` passed both complete runs [29482708033](https://github.com/TyrantLucifer/awsome-sftp-cli/actions/runs/29482708033) and [29482709588](https://github.com/TyrantLucifer/awsome-sftp-cli/actions/runs/29482709588), including quality's real sshd PTY proof, auth/recovery, native, oldstable, race, reproducibility and provenance comparison.

The focused fault matrix now proves short reads and short writes within the frozen buffer budget, transport interruption with resume from the durable offset, permission and resource-exhaustion classification without final publication, abrupt manager close with part-fingerprint revalidation, and a rename that applies before its response is lost. The Jobs view exposes bounded recent error and recovery summaries. Provider error details carrying a secret canary are reduced to structured code/operation data before persistence; the terminal summary, event payloads and SQLite/WAL/SHM artifacts remain canary-free.

#### M2.2 user-visible MVP

The native PTY harness drives the product keys rather than calling the worker directly: it waits for a real pane listing, presses `y`, switches panes with Tab, presses `p`, opens `J`, waits for the durable Job to reach `completed`, exits the client, reattaches a new client, and proves the completed Job remains visible. It verifies exact destination bytes and rejects any retained `.part-<job>` after successful commit.

| Command | Result |
|---|---|
| `go build -trimpath -o /tmp/amsftp-stage2-build/amsftp ./cmd/amsftp` | PASS on macOS arm64/APFS |
| `python3 internal/integration/hosted-stage2-mvp.py /tmp/amsftp-stage2-build/amsftp` | PASS: local→local PTY copy, final bytes, no part residue, durable Jobs reattach |
| `go test ./internal/integration -run TestStage2LocalPTYCopyAndDurableJobsReattachMVP -count=1 -v` | PASS |
| `AMSFTP_REAL_SSHD=1 go test ./internal/integration -run TestStage2TemporarySSHDPTYUploadDownloadMVP -count=1 -v` | PASS: real temporary-sshd local→SFTP upload and SFTP→local download through the same PTY flow |

The quality workflow runs the local PTY proof in `make check` and explicitly runs both the original protocol fixture and the Stage 2 temporary-sshd PTY proof with `AMSFTP_REAL_SSHD=1`; exact M2.2 Hosted promotion is complete in runs 29482708033 and 29482709588.

The first Hosted MVP candidate `286528c` exposed two test-fixture defects: raw PTY byte matching could not reconstruct tcell cursor-addressed text on macOS, and the Linux test binary lived below an ancestor rejected by the production executable-integrity policy. The harness now reuses the Stage 1 VT observer and installs the same binary inside the workflow's private `0700` persistent root. Both local and temporary-sshd PTY tests pass after the repair. The same candidate's Stage 1 recovery job failed once waiting for daemon replacement; exact completion SHA `811ce6b90364446612721ba7cb809a284d633521` subsequently reran and passed the complete push and PR matrices.

### M2.3 — Directory copy and dual-remote relay

- **Status**: Complete
- **Gate**: satisfied by exact SHA `811ce6b90364446612721ba7cb809a284d633521` and both complete Hosted runs above.
- **Completion basis**: exact implementation SHA and both Hosted completion runs are recorded below.

**Implementation checkpoint**: directory `FileRef` capture freezes the root identity and a `64`-item queue, `256`-entry page and `128`-level recursion budget without enumerating the tree. Discovery streams through a bounded channel, validates every child remains a direct descendant of the listed directory, emits symlinks without following them, rejects depth exhaustion and same-endpoint destinations inside the source root, and cancels the producer on early consumer exit. The directory worker creates directories on demand and runs every regular file through the existing same-directory part→SHA-256 verify→commit worker. Its restart checkpoint records the owned root, current item, aggregate bytes/items and phase; restart revalidates already published children by content, cleans only the Job-owned incomplete part and continues. The same code path covers local, same-remote and remote A→B through Provider streams; no complete local relay file exists.

Resource and route evidence:

| Fixture | Result |
|---|---|
| `TestDiscoverDirectoryStreamsMillionEntriesWithinFrozenBudgets` | PASS: 1,000,000 generated entries; channel capacity 17; Provider page limit never exceeded 31; no tree materialization. |
| `TestWorkerHundredGiBSyntheticSourceStopsAtBoundedCheckpoint` | PASS: advertised 100 GiB source; first durable cancel checkpoint at 64 KiB; observed transfer buffer exactly 64 KiB. |
| `TestWorkerCopiesDirectoryTreeWithBoundedRelayAndNoSymlinkTraversal` | PASS: nested tree preserved, symlink visible to discovery but not copied/followed, 3-byte stream buffer and no successful part residue. |
| `TestManagerRestartResumesDirectoryFromOwnedRoot` | PASS: daemon-owned Job interrupted during a file read, restart recovers paused, root/part postconditions revalidate, resume completes. |
| `TestManagerRetriesOnlyFailedDirectoryItemsAfterPermissionRepair` | PASS: one denied item produces `retry_wait` with a bounded manifest while its successful sibling stays committed; after permission repair, resume revalidates the sibling and copies only the missing item. |
| `AMSFTP_REAL_SSHD=1 go test ./internal/integration -run TestRealOpenSSHSFTPHostAliasAndNonDefaultPort -count=10` | PASS: local↔SFTP, same remote directory copy and two-independent-sshd remote A→B directory relay; a 7-byte stream budget applies backpressure and no local content cache is created. |

The expanded real-sshd loop also exposed a close race where Go may report the session's own command cancellation as `context.Canceled`; a focused RED test now classifies only Close-owned cancellation as expected, and the complete real fixture passed ten consecutive runs.

Per-item results retain source, destination, status, bytes and stable error code for the first 256 entries, plus complete succeeded/skipped/failed/item counts and an explicit truncation count. This keeps event/checkpoint payloads bounded for million-entry Jobs while preserving actionable failures. Permission failures are isolated and enter durable `retry_wait`; transport/auth/conflict failures still stop at their dedicated state. Resume revalidates already committed children by content and retries the missing failed items without overwriting proven successes. Exact-candidate Hosted promotion completed in the two runs below.

**Hosted completion gate**: exact SHA `eb4f152f305812f30e7573a690e570e8ca41b96b` passed both complete runs [29484442378](https://github.com/TyrantLucifer/awsome-sftp-cli/actions/runs/29484442378) and [29484446997](https://github.com/TyrantLucifer/awsome-sftp-cli/actions/runs/29484446997), including real dual-sshd quality, auth/recovery, all native/oldstable legs, race, build and provenance comparison.

### M2.4 — Move, rename, delete and recovery closeout

- **Status**: Complete
- **Gate**: exact implementation SHA `54b0285d7278d58e67c35a280fa8b996a99a321d`, tree `3fe5af7767a61fd10c5608431ff81cf361634ce8`, passed complete [push run 29488697276](https://github.com/TyrantLucifer/awsome-sftp-cli/actions/runs/29488697276) on attempt 2 and [PR run 29488700235](https://github.com/TyrantLucifer/awsome-sftp-cli/actions/runs/29488700235).
- **Implementation candidate**: commits `cf10e2031ff4929b5b8bc6882aad473445841f7d` and `29592921b24039a568677e4974541d9656c8f952`, with deterministic Hosted fixture synchronization through `54b0285d7278d58e67c35a280fa8b996a99a321d`.
- **Closeout**: complete local current/oldstable gates, exact implementation Hosted green, documentation/feature reconciliation and the independent cold-start re-audit all pass. The evidence-only closeout commit must itself remain green before the PR is moved to Ready.

The Planner now freezes a source `write`/delete capability for every cut in addition to source read and destination write. Same-Endpoint moves select the zero-stream rename path only when that exact snapshot explicitly advertises `atomic_rename`; the executor requires `Atomic=true` and proves source absence plus the frozen destination fingerprint. Standard LocalFS/SFTP do not advertise this stronger operation and therefore use the generic copy→SHA-256 verify→commit→source identity/capability revalidation→conditional delete→absence proof sequence.

File and directory source changes, capability changes, incomplete per-item verification, symlink-containing directory trees and unproved deletes preserve the verified destination and finish as `completed_with_source_retained`. Directory moves reread every source and destination regular file through the frozen buffer before bounded bottom-up deletion. A daemon stop while the destination is committed and source `Remove` is blocked leaves both copies; restart pauses/reconciles the Job, and explicit resume revalidates before completing deletion. Unknown delete responses are accepted only after a not-found postcondition.

Explicit delete is a separate durable Plan/Job route. It rejects empty/root/unsupported/stale identities, requires source read plus write capability, and uses three persisted steps (`verify_source`, `delete`, `verify_absent`). A `trash` route is selected only when a frozen capability and optional Provider facet both exist; otherwise the Plan is `irreversible_delete`. Recursive deletion uses the same frozen page/depth budgets, validates every descendant and deletes a symlink without following it. Current LocalFS/SFTP advertise no reliable trash; the test Provider proves the capability-aware route.

The TUI now captures single, directory, visual/marked and counted multi-selection FileRefs. `d` remains cut only; cut paste shows a move confirmation, `D` shows frozen-scope and second irreversible confirmations, and `r` accepts exactly one source plus one plain name. Counted paste is capped at 1,024 Job intents. `.` repeats frozen identities, but cut/rename reopens move confirmation and delete restarts both confirmations. High-level capture/create-delete RPCs preserve the Planner→Job-only mutation boundary; raw Provider mutation remains unavailable over RPC.

Focused evidence:

| Fixture / command | Result |
|---|---|
| `TestManagerCutDeletesSourceOnlyAfterVerifiedCommit` | PASS: source disappears only after committed final SHA-256 proof. |
| `TestManagerMoveRetainsSourceWhenItChangesAfterCommit` | PASS: changed source yields `completed_with_source_retained`. |
| `TestManagerDirectoryMoveVerifiesEveryItemBeforeBoundedSourceDelete` | PASS: nested items are content-verified before bounded bottom-up deletion. |
| `TestManagerUsesFrozenAtomicRenameFastPathWithoutStreaming` | PASS: explicit capability, atomic/postcondition proof and zero read/write streams. |
| `TestManagerMoveProvesDeleteAfterResponseLoss` and `TestManagerExplicitDeleteProvesUnknownResponseByPostcondition` | PASS: unknown responses are reconciled by stat, not replayed blindly. |
| `TestManagerRestartBetweenCommitAndSourceDeleteRetainsThenFinishes` | PASS: source retained across stop, restart/resume completes after revalidation. |
| `TestManagerPrefersAdvertisedTrashWithoutIrreversibleConfirmation` | PASS: capability+facet selects trash; no-trash requires irreversible confirmation. |
| `TestManagerRecursiveDeleteIsBoundedAndNeverFollowsSymlink` and `TestManagerDeletesFrozenSymlinkWithoutFollowingTarget` | PASS: bounded tree removal and link-only delete. |
| TUI reducer/tcell suites | PASS: `d`/`D`, single/multi/directory, count, repeat, move/delete reconfirmation and rename input. |
| `go test ./internal/integration -run TestStage2LocalPTYCopyAndDurableJobsReattachMVP -count=1 -v` | PASS: native PTY copy, confirmed cut/paste move, rename, two-confirmation delete, final/source postconditions and durable Jobs reattach. |
| `make check` | PASS on the implementation worktree; includes full unit/contract/docs and the local PTY workflow. |

The PTY fixture initially selected the first sorted entry rather than the named entry after adding more operations; two test-only call-site corrections aligned each operation with the visible cursor. The third run passed without weakening product assertions. No M2.4 issue exceeded the three-attempt limit.

## Failure and repair ledger

| Issue | Attempt and evidence | Repair | Result |
|---|---|---|---|
| Test-first dependency intake | Focused test failed because `modernc.org/sqlite` was not yet required. | Added only the two exact pins and the registration seam. | Expected RED then GREEN. |
| Intake lint | First run reported an unwrapped EOF comparison and untrusted file/tool paths. | Used `errors.Is` and documented trusted module-cache reads. | Advanced to one remaining issue. |
| Intake lint | Second run reported deprecated `runtime.GOROOT`. | Resolved the active Go binary with `exec.LookPath`. | Third run passed with zero issues. |
| Tools module check | A root-level `-modfile=tools/go.mod` tidy attempt incorrectly tried to resolve repository-internal imports. | Used the Makefile's correct `go -C tools` form; no file was changed by the failed command. | Both toolchains' tools tidy/verify checks passed. |
| Schema contract query | First runtime manifest attempt used unquoted `notnull` in the table-valued PRAGMA query. | Quoted the metadata column while keeping table names parameterized. | Contract suite passed on the second attempt. |
| State foundation lint | First lint run found wrapped-error, trusted-path annotation and one Darwin conversion issue. | Preserved causes with `%w`, documented exact validated paths and removed the redundant conversion. | Second lint run passed with zero issues. |
| Immutable max-page validation | First existing-state reopen expected connection-local `max_page_count` in an immutable reader. | Kept raw 8 GiB file-size identity plus bootstrap/runtime max-page readback; immutable validation no longer claims a writer-connection setting. | Initial bootstrap and existing reopen passed on the second attempt. |
| Backup immutable max-page validation | The first immutable-backup pass made the same connection-local `max_page_count` assumption and observed SQLite's immutable-reader default `4294967294`. | Require the exact max only on the controlled writable verifier; immutable verification still proves header identity, complete history/schema, zero attempts, hold, quick/FK and sidecar absence. | Backup and tampered-history suites passed on the second attempt. |
| Runner v2 regression tests | The first post-attempt test audit found two v2 tests stopped at missing `AttemptID` instead of exercising duplicate-statement rollback and SQL-tail admission. | Supplied exact valid attempt IDs and asserted the intended failure boundary. | Both focused tests now exercise and pass their named behavior. |
| Retention missing verified candidate | The first crash-continuation implementation marked an already-missing `verified` candidate `deleting`, then mistook absence for a completed unlink. | Verify candidate existence, attrs, digest and sidecar absence before the durable deleting marker; only a row already marked deleting may resume from absence. | The focused RED now fails closed while preserving the verified catalog row. |
| Retention sole usable rollback | The first protected-set query considered only verified rows and did not validate the newest two before deleting an older valid backup. | Rank the top two across all catalog states, require both to be verified and valid, block top-two deleting anomalies/restore holds, and pin the root descriptor through unlink+sync. | Missing/corrupt/latest-deleting/sidecar fixtures preserve every usable backup and pass after the repair. |
| Retention lint | The first lint pass found one checked uint64→int64 fixture conversion and an unnecessary test conversion. | Bound/document the head conversions and remove the redundant conversion. | Second lint pass passed with zero issues. |
| Foundation module hygiene | First dual-toolchain `make check` stopped at `go mod tidy -diff` because new statefs code directly imports the existing `x/sys v0.47.0` pin. | Moved the unchanged pin from indirect to direct with `go mod tidy`. | Current and exact Go 1.25.12 `make check` passed on the second attempt. |
| Hosted persistent-test root | Superseded Hosted runs [29474663746](https://github.com/TyrantLucifer/awsome-sftp-cli/actions/runs/29474663746) and [29474661816](https://github.com/TyrantLucifer/awsome-sftp-cli/actions/runs/29474661816) reached the intended Linux trust check with new state fixtures beneath sticky `/tmp`; quality, Linux native and Linux oldstable jobs correctly rejected the writable ancestor even though the workflow had provisioned `/var/lib/amsftp-tests/<euid>`. | Reused the repository's existing `testkit.PersistentTempDir` for every new fixture that represents persistent state, preserving the production rejection while selecting the workflow's owner-private root on Linux. | Exact replacement SHA `1ec9097448d0ec40d32f0a87aeeb822e5651d381` passed the complete Hosted matrix in [run 29475259444](https://github.com/TyrantLucifer/awsome-sftp-cli/actions/runs/29475259444). |
| Native disk-full lint | The first lint pass on the environment-gated XFS filler test correctly treated cleanup paths derived from the fixture environment as tainted. | Documented the exact path proof at each removal: the environment root must first pass the production XFS validator and both removed paths are fixed direct children. | Second lint pass completed with zero issues; both Linux native jobs then passed real XFS ENOSPC execution in Hosted run 29476167115. |
| M2.1 Hosted auth timing | In run 29476167115, quality and both Linux native legs passed, but the unrelated auth job observed the host-key-changed RPC failure before its asynchronous diagnostic record became visible. All auth cases themselves had passed, and the expected record appeared in the emitted log immediately after the assertion. | No state-foundation code or auth assertion was weakened; record the timing failure and require the next candidate's complete Hosted matrix to rerun it. | M2.1's required exact-SHA native gate is green; the complete final implementation runs close the whole-matrix gate. |
| First M2.4 Hosted lint | Both push and PR runs for `29592921b24039a568677e4974541d9656c8f952` reached the intended quality gate and reported only two `gosec G304` findings on fixed child reads in private temporary test roots. All jobs preceding lint passed. | Added the repository-standard `#nosec G304` proof at the exact reads; no production path, assertion or fixture behavior changed. | Local `make lint` and both exact implementation Hosted runs pass with zero lint issues. |
| M2.4 PR auth observation | Push run 29487538801 for `c2d1413f531728b075b3fcaab2cb3d2526d2029b` was completely green. Its PR twin 29487541087 repeated the previously recorded host-key diagnostic timing failure: the failure-mode Expect script waited a fixed second, quit the client and unconditionally recorded bounded failure before the async endpoint request necessarily returned/logged. | Replaced incidental TUI-byte timing with a bounded poll for the causally relevant `rpc_request_failed` diagnostic record. No timeout, product behavior or required diagnostic was weakened. | Exact SHA `54b0285d7278d58e67c35a280fa8b996a99a321d` passed auth-integration in both complete Hosted runs. |
| M2.4 oldstable WAL-idle observation | The same PR twin saw `TestManagerNeverPersistsProviderErrorDetails` call `CheckpointIdle` while the Job worker goroutine had published terminal state but had not necessarily returned from all manager work. A 100-run exact-Go-1.25.12 focused loop did not reproduce. | Close/wait the Manager after inspecting durable terminal events and before asserting WAL idle. This establishes the test's stated precondition without retrying or weakening WAL guard behavior. | Exact Go 1.25.12 focused test passes 100/100; all exact implementation oldstable jobs pass. |
| Exact implementation push recovery observation | Push run 29488697276 attempt 1 passed every authentication case, including host-key-changed, then the pre-existing Stage 1 recovery fixture timed out waiting for a replacement daemon. The exact PR twin passed the same fixture and all other push jobs were green. | No code or assertion changed. Reran only the failed job on the same SHA to distinguish a transient process-replacement observation from a candidate defect. | Push run 29488697276 attempt 2 and PR run 29488700235 are complete and green on the same SHA. |
| Final local concurrent PTY observation | The first closeout `make ci` ran concurrently with the complete exact-oldstable gate and timed out before the local PTY copy published its first final path; the oldstable gate passed. | No file or assertion changed. Reran the exact PTY alone, then reran the complete current-toolchain gate without the competing full suite. | Focused PTY and isolated `make ci` pass; this was one evidence attempt, not a product repair. |
| First M2.2 lint pass | Race tests passed, while lint found two rooted LocalFS opens, two checked checkpoint offset conversions and three single-value helper parameters. | Replaced path-string mutation with `os.Root`, centralized the MaxInt64 conversion, and removed the redundant helper parameters without weakening errors or effects. | The next focused test and lint pass completed with zero issues. |
| M2.2 Hosted close-time part identity | Both push and PR runs for exact SHA `9e878a86a67765f440deb89039e59044a0ac6b45` failed the same restart test on Linux and macOS: the write checkpoint was captured before the pause path's deferred close, while those filesystems finalized part metadata at close and made the persisted fingerprint stale. | Added a deterministic provider fixture that changes the real part mtime on write-handle close, then changed pause/cancel to sync, close, restat, validate size and durably refresh the part fingerprint before returning. | The reproducer changed RED→GREEN; the original database/worker restart test passed 100 consecutive local runs, with the replacement Hosted result closed by the next ledger row. |
| M2.2 Hosted timestamp representation | Replacement SHA `5637d464154cc230500a035977e2e687f504f980` proved the close-order repair but exposed the remaining cross-environment cause: JSON reload produced UTC timestamp locations while provider fingerprints on UTC-configured Hosted runners carried `time.Local`; `reflect.DeepEqual` rejected equal instants, which also blocked manager plan reload. | Added an explicit fixed-zone alias RED, canonicalized fingerprint timestamps to UTC at Fake/LocalFS/SFTP and frozen-plan boundaries, and reran the restart test 100 times plus all transfer tests ten times under `TZ=UTC`. | Third/final repair SHA `e5b5cd287b1519b235d8444262cc83fdfa76ed51` passed both full Hosted runs 29479576412 and 29479579080. |

No issue exceeded three attempts.

## Independent cold-start audit

An independent read-only agent started only from `docs/README.md`. Its first pass correctly found that final local results were not yet bound to an immutable commit/tree, that historical M2.2/M2.3 paragraphs retained obsolete pending instructions, that `PROJECT_STATE.md` bundled multiple next actions, and that two cross-stage Level-0 rows plus the executable-role testing guide were stale. The documentation was reconciled without changing implementation claims.

The same agent then repeated the cold start from the documentation map and reported zero High, Medium or Low content findings. It recovered Stage 2/M2.4 lifecycle, all milestone states, V1 `init` head, migration checksum `281a5d34c0ebdd06de26fd1098fbf3efd7c8a7e283f5328ea218d1ca8dfb19f9`, 24,495-byte schema contract digest `659edd23b5bc332b488a171c920815daffef6223ef2d3859215ba177c3d55e64`, Provider capability differences, worker/queue/buffer/WAL ceilings, million-entry/100 GiB/two-sshd fixtures, exact validation ledger, deferred Stage 3–6 scope and one next action. It also confirmed staging empty, `git diff --check` clean, no apparent secret filename/content, and only the documented ignored `.idea/`, `.superpowers/`, `coverage/`, and `dist/` roots. The implementation candidate is now immutably bound to the exact SHA/tree and complete Hosted runs recorded above.

## Final gate ledger

All required Stage 2 gates are closed by the evidence below:

- [x] `make docs-check`
- [x] `make check`
- [x] `make lint`
- [x] `make supply-chain`
- [x] `make ci`
- [x] `go test -race ./...`
- [x] `GOTOOLCHAIN=go1.25.12 make check`
- [x] Stage 2 SQLite, migration, Provider mutation, Planner, Job, IPC/event, transfer, conflict, recovery, PTY and performance suites.
- [x] Four-target build and reproducibility/provenance comparison.
- [x] Native APFS/ext4/XFS, two-sshd, ProxyCommand, Kerberos, crash/fault, sparse-file/large-tree and secret checks; final candidate pollution manifest remains part of the last clean-tree audit.
- [x] Independent cold-start audit from `docs/README.md`; reconciliation re-audit reports zero High/Medium/Low findings.
- [x] Exact implementation SHA Hosted CI green; the evidence-only closeout commit is required to rerun green before the PR Ready transition.

Final closeout-tree commands on native macOS arm64/APFS with Go 1.26.5, using external `/tmp/amsftp-stage2-closeout-{build,coverage,coverage-oldstable}` outputs:

| Command | Result |
|---|---|
| `make docs-check` | PASS |
| `make check` | PASS; full unit/contract/docs, local PTY copy/move/rename/delete/reattach and module gates |
| `make lint` | PASS; zero issues after the test-root trust annotation repair |
| `make supply-chain` | PASS; zero reachable/imported vulnerabilities, one previously admitted required-module-only finding, actionlint clean |
| `go test -race ./...` | PASS |
| `GOTOOLCHAIN=go1.25.12 make check COVERAGE_DIR=/tmp/amsftp-stage2-closeout-coverage-oldstable` | PASS |
| `go test ./internal/integration -run '^TestStage2LocalPTYCopyAndDurableJobsReattachMVP$' -count=1 -v` | PASS after the concurrent-suite observation; unchanged native PTY copy/move/rename/delete/reattach fixture. |
| `make ci BUILD_DIR=/tmp/amsftp-stage2-closeout-build COVERAGE_DIR=/tmp/amsftp-stage2-closeout-coverage` | PASS in isolation; check, lint, race, four fuzz smokes, supply chain and darwin/linux × arm64/amd64 CGO-disabled builds |

## Stage 2 feature evidence

The Feature Matrix's Stage 2 rows are closed by the milestone-specific evidence above and the exact implementation candidate's complete local and Hosted gates.

| ID | Result | Evidence |
|---|---|---|
| AUTH-007 | PASS | Durable waiting-auth and client-independent resume evidence. |
| AUTH-010 | PASS | Recoverable authentication failure and frozen-plan resume evidence. |
| PANE-009 | PASS | Conservative symlink discovery, copy and delete evidence. |
| VIM-006 | PASS | Immutable copy reference capture evidence. |
| VIM-007 | PASS | Capture-only cut and confirmed move evidence. |
| VIM-008 | PASS | Immutable paste intent and bounded count evidence. |
| VIM-009 | PASS | Dedicated two-confirmation delete evidence. |
| VIM-010 | PASS | Bounded count-prefix behavior evidence. |
| VIM-011 | PASS | Repeat with fresh validation and reconfirmation evidence. |
| VIM-015 | PASS | Durable rename intent and PTY evidence. |
| DAEM-004 | PASS | Daemon-owned client-independent execution evidence. |
| DAEM-005 | PASS | Monotonic event cursor and replay evidence. |
| DAEM-006 | PASS | M2.1 single-writer state and native filesystem evidence. |
| DAEM-007 | PASS | Job, directory and commit-to-delete restart evidence. |
| JOB-001 | PASS | Transactional durable Plan, Job, step and event evidence. |
| JOB-002 | PASS | Exhaustive transactional state-machine evidence. |
| JOB-003 | PASS | Waiting, terminal and source-retained state evidence. |
| JOB-004 | PASS | Persisted immutable plan and descriptor evidence. |
| JOB-005 | PASS | Confirmation-free ordinary copy PTY evidence. |
| JOB-006 | PASS | Move, overwrite and delete confirmation evidence. |
| JOB-007 | PASS | Fixed queue, worker and endpoint lease evidence. |
| JOB-008 | PASS | Durable pause, resume and cancel evidence. |
| XFER-001 | PASS | Frozen Location clipboard and stale-source evidence. |
| XFER-002 | PASS | Local, same-remote and dual-remote route evidence. |
| XFER-003 | PASS | Two-sshd bounded local relay evidence. |
| XFER-004 | PASS | Frozen buffer, queue, page and depth budget evidence. |
| XFER-005 | PASS | Same-directory owned part and absent-final evidence. |
| XFER-006 | PASS | SHA-256 verify, conditional commit and reread evidence. |
| XFER-007 | PASS | Durable offset, source and part checkpoint evidence. |
| XFER-008 | PASS | Frozen stream-SHA-256 integrity evidence. |
| XFER-009 | PASS | Million-entry streaming directory evidence. |
| XFER-010 | PASS | On-demand directory and explicit metadata-policy evidence. |
| XFER-011 | PASS | Short-I/O progression and digest evidence. |
| XFER-012 | PASS | Runtime ENOSPC/EDQUOT fail-safe evidence. |
| XFER-013 | PASS | Honest durable item and byte progress evidence. |
| XFER-014 | PASS | Persisted route and capability explanation evidence. |
| XFER-015 | PASS | Selective failed-item retry evidence. |
| XFER-016 | PASS | Bounded per-item result manifest evidence. |
| MOVE-001 | PASS | Explicit atomic-rename capability and postcondition evidence. |
| MOVE-002 | PASS | Copy, verify, commit, revalidate, delete ordering evidence. |
| MOVE-003 | PASS | Insufficient-proof source-retention evidence. |
| MOVE-004 | PASS | Changed-source deletion refusal evidence. |
| MOVE-005 | PASS | Per-item move outcome and replay-safety evidence. |
| DEL-001 | PASS | Uppercase-delete versus lowercase-cut evidence. |
| DEL-002 | PASS | Frozen scope and cancellation evidence. |
| DEL-003 | PASS | Capability-gated reliable trash evidence. |
| DEL-004 | PASS | Irreversible SFTP warning evidence. |
| DEL-005 | PASS | Bounded recursive symlink-safe delete evidence. |
| DEL-006 | PASS | Durable audit and response-loss postcondition evidence. |
| CONF-001 | PASS | Planning and commit-time conflict race evidence. |
| CONF-002 | PASS | Ask, skip, overwrite, rename and cancel evidence. |
| CONF-003 | PASS | Job-scoped apply-all isolation evidence. |
| CONF-004 | PASS | Client-independent durable conflict evidence. |
| CONF-005 | PASS | Part-first verified overwrite evidence. |
| CONF-006 | PASS | Transactional conflict decision evidence. |
| RECV-001 | PASS | Disconnect checkpoint revalidation evidence. |
| RECV-002 | PASS | Process-death and commit-to-delete recovery evidence. |
| RECV-003 | PASS | Postcondition-first non-idempotent recovery evidence. |
| RECV-004 | PASS | Bounded retry-wait and explicit resume evidence. |
| RECV-005 | PASS | Cancel-time part and checkpoint retention evidence. |
| RECV-006 | PASS | Job/source/destination-bound part identity evidence. |
| RECV-007 | PASS | Permission, resource and short-I/O final safety evidence. |
| RECV-008 | PASS | Monotonic recovery audit evidence. |
| DIRECT-001 | PASS | Same-endpoint zero-stream atomic rename evidence. |
| SCALE-004 | PASS | Million-entry bounded work-queue evidence. |
| SEC-006 | PASS | Rooted normalized path and symlink boundary evidence. |
| SEC-007 | PASS | Job-bound part and final-race protection evidence. |
| SEC-013 | PASS | Explicit destructive-operation authorization evidence. |
| OBS-002 | PASS | Bounded Jobs phase and waiting-reason evidence. |
| OBS-003 | PASS | Honest known/unknown progress evidence. |
| OBS-004 | PASS | Structured stable-code event evidence. |
| OBS-008 | PASS | Verification, commit and source-retention evidence. |
| PLAT-007 | PASS | Cross-platform/provider semantic capability evidence. |
