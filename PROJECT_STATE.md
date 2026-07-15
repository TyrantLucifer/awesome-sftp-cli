# Project State

- **Updated**: 2026-07-15
- **Lifecycle**: Stage 1 read-only explorer in progress
- **Active stage**: Stage 1 — Read-only Explorer
- **Product / command**: `AMSFTP` / `amsftp`
- **Repository name**: `awesome-mac-sftp`

## Current outcome

The approved Stage 0 baseline remains `d637474ac52ef2c5b9f78c9be663e52c6a9f441c`. M1.1 now has a locally green candidate: exact tcell v3.4.0 intake, private platform paths/ACL/lock/socket/peer-UID boundaries, a cancellable framed daemon client/server, LocalFS read-only RPC routes, and a windowed two-pane tcell UI with Vim navigation, filtering, selection and a 64 KiB preview. The real binary auto-start path and signal lifecycle are wired; repeated reconnect, single-instance convergence, cancellation, full local checks, both Go versions, four target builds, the 50k benchmark and a darwin/arm64 PTY smoke pass. The milestone is not Complete until the candidate is committed and Hosted native/oldstable gates are green.

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

Commit and push the locally green M1.1 candidate, run the Hosted matrix, and close any native platform failures. Only after M1.1 Hosted evidence is green may M1.2 begin with the exact `pkg/sftp v1.13.10` dependency intake and ADR-0001 system-OpenSSH transport.

## Required reading for the next session

1. [Documentation map](docs/README.md)
2. [Implementation plan](IMPLEMENTATION_PLAN.md), Stage 1
3. [Feature matrix](docs/product/feature-matrix.md), Stage 1 rows
4. [Stage 1 specification](docs/stages/01-read-only-explorer.md)
5. [Stage 0 verification](docs/verification/stage-00.md), as the completed foundation handoff
6. [Approved design](docs/superpowers/specs/2026-07-14-vim-first-sftp-commander-design.md)
7. ADRs referenced by Stage 1

## Validation record

The active command/result ledger is [Stage 1 verification](docs/verification/stage-01.md); [Stage 0 verification](docs/verification/stage-00.md) remains the completed foundation handoff. Initial Stage 1 safety checks passed on 2026-07-15: clean branch `codex/stage1-read-only-explorer`, HEAD `d637474ac52ef2c5b9f78c9be663e52c6a9f441c`, tree `83a515607f44f7edb85f8103962b6d9d1173c02d`, and matching `origin/codex/stage1-read-only-explorer`. No Stage 1 test gate has run yet.

Task 11 focused revalidation now also passes: the final cross-document decision review is clean; GNU Make 3.81 rejects late/continued execution flags, target-specific controls, internal-guard command-line overrides and `-e` environment overrides while preserving all 14 forced guards and 11 Go probes and accepting safe output-directory assignments. Provenance policy binds actual artifact hashes and target tuples to comparison evidence, requires `-buildvcs=false` for cross/repro builds, compares canonical shell content with exact semantic whitespace, and fixes nightly fuzz/concurrency workloads into the producer profile. IPC envelope/control JSON is strict UTF-8 on decode and encode; Code/Retry/Effect are canonical and retry delay is non-negative while raw error paths remain base64 diagnostic context. Focused package/race/vet/lint/docscheck checks and independent re-reviews passed with staging empty.

The accepted final pre-Hosted local closeout checkpoint is tree `5d598eea00fac2b5580bc04596d2bb2c435f4799` at `/private/var/folders/l7/7379px6d495gzqjf6df3953m0000gn/T/amsftp-stage0-final.b5i0FK`; its compact attestation and the earlier exact replay ledger are in Stage 0 verification. Hosted run [29394164471](https://github.com/TyrantLucifer/awsome-sftp-cli/actions/runs/29394164471) bound commit `1da725478aa772ebc408885427df23f3b9f4c53c` and tree `5880f05d52d618a9b128a37f6925467666fe7cc8`, but is permanently superseded. The fixed candidate's final local evidence is at `/var/folders/l7/7379px6d495gzqjf6df3953m0000gn/T/amsftp-hosted-fix-final.5yKPjp`; Go 1.26.5 `make ci` and exact Go 1.25.12 `make check` passed on tree `e70a8f0c5fc57817f6fa44dda31faaf4652b67c5`. Replacement Hosted run [29394698864](https://github.com/TyrantLucifer/awsome-sftp-cli/actions/runs/29394698864) bound that exact tree and passed 23/23 jobs. Its final comparison artifact is [provenance-compare 8334635589](https://github.com/TyrantLucifer/awsome-sftp-cli/actions/runs/29394698864/artifacts/8334635589), digest `sha256:5e0b1ce400c43c0156fa7c2ad1e4089e83c41708115de688a045f3678f337712`. Cross-compilation is not native evidence; the run's native legs provide the platform proof. The prior Task 9 clean-main result remains revoked; its correct-root replacement remains accepted only for that snapshot.

## Known constraints and deliberately deferred choices

- The product display name is `AMSFTP` and the public command is `amsftp`; `awesome-mac-sftp` remains only the repository name. The package/application ID is `io.github.tyrantlucifer.amsftp`.
- The Stage 0 module is `github.com/TyrantLucifer/awesome-mac-sftp`, with Go 1.25.0 language compatibility, Go 1.26.5 preferred, and exact Go 1.25.12 oldstable verification.
- Cross-host direct transfer is not assumed to work with Kerberos. It is an optional capability that must prove destination reachability and non-interactive credentials on the source host without forwarding or copying user credentials; otherwise the route is local relay.
- GUI opener behavior differs by platform. Stage 3 must implement platform adapters and validate lease/change detection on both macOS and Linux.
- Two user-owned IDE files, `.idea/.gitignore` and `.idea/misc.xml`, appeared concurrently during the Task 8 final review. Task 11 classified `.idea/` as local JetBrains/Java IDE metadata and excluded it through the repository root `.gitignore`; the files themselves were preserved and are not product-candidate content.
- M1.1 has local implementation evidence only. Remote browsing, SSH transport, authentication broker, workspace persistence, recovery, Kerberos and Stage 1 Hosted CI are not yet delivered or claimed.

## Working-tree policy

- Do not commit unless the user explicitly requests it.
- Preserve `.superpowers/` as disposable coordination output only; it is ignored by Git and cannot be the sole durable evidence source. The deleted Superpowers skill is not a project dependency; `docs/superpowers/` is only a historical path for the approved durable design document.
- Review tracked and untracked Stage 0 files together; a plain `git diff` omits most current implementation files.
- At the end of every work session, update this file even when work stops on a failure.
