# Project State

- **Updated**: 2026-07-16
- **Lifecycle**: Stage 3 preview, edit and cache Not Started
- **Active stage**: Stage 3 — Preview, Edit & Cache
- **Current milestone**: Stage 3 contract intake — managed cache and lease boundary
- **Product / command**: `AMSFTP` / `amsftp`
- **Repository name**: `awesome-mac-sftp`

## Current outcome

Stage 1 is complete at merge commit `b99fca2f729a8445b20935c69eda52cfa6dbbd28`, tree `1cf952ea743992c685f6bf05a75de43ebe7499a8`; exact-main [Hosted run 29468930350](https://github.com/TyrantLucifer/awsome-sftp-cli/actions/runs/29468930350) is green. Stage 2 is complete on `codex/stage2-durable-transfers`. M2.1's Version 1 state foundation covers the exact schema/contract, APFS/ext4/XFS gates, atomic bootstrap, migration/backup/retention/WAL budgets, transactional Job/events, deterministic restart recovery, process-death boundaries, and fail-closed Stage 1 browsing. Exact SHA `3a8ec31d6a7f7afdaf7f6aa1a44e546cfc2145f6` passed [Hosted run 29475833368](https://github.com/TyrantLucifer/awsome-sftp-cli/actions/runs/29475833368); both Linux native jobs for `f83aa45de9b83f42d6f64944401ddde0e1e92d01` passed ext4/XFS plus real XFS `ENOSPC` rollback in [run 29476167115](https://github.com/TyrantLucifer/awsome-sftp-cli/actions/runs/29476167115).

M2.2 is complete. It has shared Fake/LocalFS/SFTP mutation contracts, frozen single-file plans, a bounded SHA-256 part/verify/commit worker, real SQLite checkpoint resume, daemon-owned scheduling, pre-return endpoint leases, exact-descriptor restart rehydration, durable conflicts, controls, `y`/`d`/`p`, and a bounded polling `J` Jobs view. Short I/O, disconnect/resume, permission, resource exhaustion, commit-response loss, abrupt-handle recovery and secret-zero-persistence fixtures pass. Exact SHA `811ce6b90364446612721ba7cb809a284d633521` passed complete Hosted runs [29482708033](https://github.com/TyrantLucifer/awsome-sftp-cli/actions/runs/29482708033) and [29482709588](https://github.com/TyrantLucifer/awsome-sftp-cli/actions/runs/29482709588), including real local and bidirectional temporary-sshd PTY workflows plus Stage 1 auth/recovery.

M2.3 is complete. Frozen directory-root plans carry hard queue/page/depth budgets; the million-entry synthetic discovery, 100 GiB synthetic bounded checkpoint, nested copy, conservative symlink handling, daemon restart, same-remote and two-independent-sshd remote A→B relay tests pass. The default discovery budgets are 64 queued items, 256 entries per Provider page and depth 128; fresh/recovery buffer ceilings are 256/512 KiB. Bounded results and selective retry are durable. Exact SHA `eb4f152f305812f30e7573a690e570e8ca41b96b` passed complete Hosted runs [29484442378](https://github.com/TyrantLucifer/awsome-sftp-cli/actions/runs/29484442378) and [29484446997](https://github.com/TyrantLucifer/awsome-sftp-cli/actions/runs/29484446997).

M2.4 and Stage 2 are complete. Same-Endpoint rename is selected only by an explicit frozen `atomic_rename` capability and proved postconditions; all other moves use copy→verify→commit→source revalidation→conditional delete. Source change/capability loss/unproved directory verification/delete uncertainty ends as `completed_with_source_retained`. `D` has frozen-scope plus irreversible confirmation, `r` is a durable same-Endpoint move, reliable advertised trash is preferred, symlinks are never followed, and count/`.` cannot bypass move/delete confirmation. The native PTY drives copy, confirmed cut/paste move, rename, two-confirmation delete and Jobs reattach. Exact implementation SHA `54b0285d7278d58e67c35a280fa8b996a99a321d`, tree `3fe5af7767a61fd10c5608431ff81cf361634ce8`, passed complete [push run 29488697276](https://github.com/TyrantLucifer/awsome-sftp-cli/actions/runs/29488697276) on attempt 2 and [PR run 29488700235](https://github.com/TyrantLucifer/awsome-sftp-cli/actions/runs/29488700235). Stage 3 remains Not Started.

Stage 0 establishes and verifies foundation contracts and engineering gates only. It does not provide a usable TUI, daemon service, SSH/SFTP connection, SQLite persistence, transfer engine, or remote helper, and it is not production-ready. Production/release readiness is assessed only by the Stage 6 hardening and 1.0 release gates.

## Product in one paragraph

Build a Vim-first, two-pane terminal file commander for macOS and Linux. Either pane can independently point at the local machine or any SSH/SFTP endpoint. Authentication and SSH configuration are delegated to the user's system OpenSSH so Kerberos/GSSAPI, `ProxyCommand`, agents, host aliases, and existing policy continue to work. A local daemon owns sessions, durable background jobs, cache, and workspaces. Standard SFTP is always the baseline; a versioned, explicitly installed remote helper unlocks faster search, hashing, watch/tail, same-host copy, and carefully preflighted direct transfers without becoming a privileged or resident service.

## Approved decisions that must not be rediscovered

- Implementation language: Go.
- Primary platforms: macOS and Linux terminals.
- UI: two symmetric panes; each can switch among local and arbitrary remote endpoints.
- Interaction: Vim-first Normal/Visual model, counts and dot-repeat; no initial macro or named-register system.
- SSH transport: spawn ADR-0001's validated absolute OpenSSH binary (`/usr/bin/ssh` by default, never a PATH lookup) with the exact SFTP-subsystem argv and connect a Go SFTP protocol client to its stdio.
- Authentication: system OpenSSH is the only source of truth; no Kerberos implementation and no secret/ticket storage in the application.
- Process model: TUI/CLI client plus an auto-started per-user local daemon over a permission-restricted Unix socket.
- Transfer model: persistent jobs, bounded-memory streaming, temporary destination plus atomic commit, source deletion only after verified move commit.
- Remote-to-remote routing: safe fast/direct paths when capability and policy preflight succeeds; bounded-memory local relay otherwise.
- Remote helper: optional, user-approved, versioned and unprivileged; eligibility requires an explicit shared-session-stable-home policy plus ADR-0010 current-policy/binding/path checks, and it is invoked over SSH stdio without a listener or persistent remote daemon.
- Cache: short-lived LRU by default; workspace-scoped ephemeral or explicitly pinned offline content.
- Scale target: directories with tens of thousands of entries, trees with millions of paths, and individual files in the hundreds of gigabytes.
- Delivery: seven vertical stages (0 through 6), each ending in an executable, independently verifiable capability slice.

Changing any item above requires an explicit ADR and corresponding updates to the design, feature matrix, active stage, and this file.

## Next action

Freeze the Stage 3 managed cache/lease contract and write RED lease-lifecycle tests before external-edit wiring.

## Current risks

- Stage 3 must reuse Stage 2's Planner→Job→Worker→Verify→Commit path; preview, cache and external-edit flows must not introduce a second mutation path.
- APFS can be exercised locally, but ext4/XFS database semantics require native Linux Hosted fixtures; cross-builds are not acceptance evidence.
- GUI opener behavior and managed cache lease cleanup differ by platform; Stage 3 must validate both macOS and Linux adapters.
- Stage 2's persistent state and destructive operations are complete but remain fail-closed to the verified Stage 1 read-only surface whenever their state/capability gates cannot be proved.

## Required reading for the next session

1. [Documentation map](docs/README.md)
2. [Implementation plan](IMPLEMENTATION_PLAN.md), Stage 3
3. [Feature matrix](docs/product/feature-matrix.md), Stage 3 rows
4. [Stage 3 specification](docs/stages/03-preview-edit-cache.md)
5. [Stage 2 verification](docs/verification/stage-02.md), as the completed durable-transfer handoff
6. [Approved design](docs/superpowers/specs/2026-07-14-vim-first-sftp-commander-design.md)
7. ADRs referenced by Stage 3, beginning with the Stage 2 mutation and postcondition handoff

## Validation record

Stage 2 baseline checks on 2026-07-16: clean `main` exactly matched `origin/main` at `b99fca2f729a8445b20935c69eda52cfa6dbbd28` / tree `1cf952ea743992c685f6bf05a75de43ebe7499a8`; `git fetch --prune origin` succeeded; the fixed Stage 2 branch did not exist locally or remotely and was created from that baseline; exact-main Hosted run `29468930350` reports `success`. Stage 2 evidence is maintained in [Stage 2 verification](docs/verification/stage-02.md).

The completed command/result ledger is [Stage 1 verification](docs/verification/stage-01.md); [Stage 0 verification](docs/verification/stage-00.md) remains the foundation handoff. On the final implementation tree, `GOTOOLCHAIN=go1.26.5 make ci`, exact `GOTOOLCHAIN=go1.25.12 make check`, focused current/oldstable integration tests, focused race, shell syntax and pollution checks passed. This covers docs, unit/Provider contracts, full race, lint, four fuzz smokes, supply chain, actionlint and four target builds. Hosted run [29467496969](https://github.com/TyrantLucifer/awsome-sftp-cli/actions/runs/29467496969) then passed all 24 quality, auth, native, oldstable, build, reproducibility and comparison jobs.

After the two approvals, the recovery state-machine and ADR-0011 streaming-cursor candidate passed focused current/oldstable/race tests, both toolchains' `go mod tidy -diff` and `go mod verify`, current `make ci`, oldstable `make check`, docscheck and `git diff --check`. Current `govulncheck` finds zero reachable/imported-package vulnerabilities and one uncalled required-module finding. These local precursor results are superseded by the exact implementation-tree Hosted pass recorded above.

Task 11 focused revalidation now also passes: the final cross-document decision review is clean; GNU Make 3.81 rejects late/continued execution flags, target-specific controls, internal-guard command-line overrides and `-e` environment overrides while preserving all 14 forced guards and 11 Go probes and accepting safe output-directory assignments. Provenance policy binds actual artifact hashes and target tuples to comparison evidence, requires `-buildvcs=false` for cross/repro builds, compares canonical shell content with exact semantic whitespace, and fixes nightly fuzz/concurrency workloads into the producer profile. IPC envelope/control JSON is strict UTF-8 on decode and encode; Code/Retry/Effect are canonical and retry delay is non-negative while raw error paths remain base64 diagnostic context. Focused package/race/vet/lint/docscheck checks and independent re-reviews passed with staging empty.

The accepted final pre-Hosted local closeout checkpoint is tree `5d598eea00fac2b5580bc04596d2bb2c435f4799` at `/private/var/folders/l7/7379px6d495gzqjf6df3953m0000gn/T/amsftp-stage0-final.b5i0FK`; its compact attestation and the earlier exact replay ledger are in Stage 0 verification. Hosted run [29394164471](https://github.com/TyrantLucifer/awsome-sftp-cli/actions/runs/29394164471) bound commit `1da725478aa772ebc408885427df23f3b9f4c53c` and tree `5880f05d52d618a9b128a37f6925467666fe7cc8`, but is permanently superseded. The fixed candidate's final local evidence is at `/var/folders/l7/7379px6d495gzqjf6df3953m0000gn/T/amsftp-hosted-fix-final.5yKPjp`; Go 1.26.5 `make ci` and exact Go 1.25.12 `make check` passed on tree `e70a8f0c5fc57817f6fa44dda31faaf4652b67c5`. Replacement Hosted run [29394698864](https://github.com/TyrantLucifer/awsome-sftp-cli/actions/runs/29394698864) bound that exact tree and passed 23/23 jobs. Its final comparison artifact is [provenance-compare 8334635589](https://github.com/TyrantLucifer/awsome-sftp-cli/actions/runs/29394698864/artifacts/8334635589), digest `sha256:5e0b1ce400c43c0156fa7c2ad1e4089e83c41708115de688a045f3678f337712`. Cross-compilation is not native evidence; the run's native legs provide the platform proof. The prior Task 9 clean-main result remains revoked; its correct-root replacement remains accepted only for that snapshot.

## Known constraints and deliberately deferred choices

- The product display name is `AMSFTP` and the public command is `amsftp`; `awesome-mac-sftp` remains only the repository name. The package/application ID is `io.github.tyrantlucifer.amsftp`.
- The Stage 0 module is `github.com/TyrantLucifer/awesome-mac-sftp`, with Go 1.25.0 language compatibility, Go 1.26.5 preferred, and exact Go 1.25.12 oldstable verification.
- Cross-host direct transfer is not assumed to work with Kerberos. It is an optional capability that must prove destination reachability and non-interactive credentials on the source host without forwarding or copying user credentials; otherwise the route is local relay.
- GUI opener behavior differs by platform. Stage 3 must implement platform adapters and validate lease/change detection on both macOS and Linux.
- Seven user-owned JetBrains files are present under ignored `.idea/`: `.gitignore`, `awesome-mac-sftp.iml`, `go.imports.xml`, `misc.xml`, `modules.xml`, `vcs.xml` and `workspace.xml`. They are preserved local IDE metadata and are not product-candidate content. Ignored `.superpowers/`, `coverage/` and `dist/` are respectively disposable coordination output and reproducible validation artifacts; none are tracked.
- Final platform-security fixtures cover real Darwin deny/direct/inherited ACLs, Linux access/default ACL xattrs, cross-process locking and a root-gated hostile-other-UID socket peer. Local race, exact Go 1.25.12, docs and actionlint checks pass, and all four native Hosted jobs passed in run 29417470068; DAEM-002/SEC-001 are Verified.
- Stage 1 remains the fail-closed Explorer baseline. Stage 2 now adds copy, move, upload, rename, delete, durable Jobs and SQLite persistence but does not claim 1.0 production readiness; external editing/cache remains Stage 3, recursive search/helper Stage 4, direct transfer/scale hardening Stage 5, and release readiness Stage 6.

## Working-tree policy

- Do not commit unless the user explicitly requests it.
- Preserve `.superpowers/` as disposable coordination output only; it is ignored by Git and cannot be the sole durable evidence source. The deleted Superpowers skill is not a project dependency; `docs/superpowers/` is only a historical path for the approved durable design document.
- Review tracked and untracked Stage 0 files together; a plain `git diff` omits most current implementation files.
- At the end of every work session, update this file even when work stops on a failure.
