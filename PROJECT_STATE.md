# Project State

- **Updated**: 2026-07-15
- **Lifecycle**: Stage 0 foundation implementation in progress
- **Active stage**: Stage 0 — Foundation and Knowledge
- **Product / command**: `AMSFTP` / `amsftp`
- **Repository name**: `awesome-mac-sftp`

## Current outcome

The approved design now has a Go foundation implementation in the isolated `codex/stage0-foundation` worktree. Module/toolchain, role dispatch, domain/config/clock, framed IPC, Provider contracts, deterministic Fake base, docscheck, Make/tooling, supply-chain checks, and pinned CI/reproducibility workflows exist. Tasks 1–10 are implemented; their frozen code partitions passed independent review and the prior Bash 3.2 local acceptance block. Task 11 then repaired whole-tree SQLite, remote-shell, Helper-install, release, Make, provenance, nightly-workload and IPC truth-chain gaps. The first two post-remediation candidates (`d30a1f4…`, `6f33b11a…`) failed their local gates. The third candidate (`4b60326…`) completed its entire local gate without pollution, but its independent whole-tree review found one Medium command-line override of Make's internal guards, so that exact tree is also superseded. The guard fix and sole fix re-review pass with no remaining High, Medium or Low finding. The post-fix replacement candidate (`c91ea59…`) passed the entire Bash 3.2 local gate without pollution on Go 1.26.5 and 1.25.12, and an independent evidence audit passed. Cold-start audit 1 recovered every requested fact and exposed only a placeholder command ledger; after that ledger was made literal, cold-start audit 2 found one stale checkpoint sentence, whose minimal fix then passed recheck. Both independent audit cycles are complete. Final local closeout tree `5d598ee…` also passed its evidence-only gate without pollution. Hosted CI has not run, so Stage 0 remains In Progress and Stage 1 remains closed.

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

After the authorized local commit, stop at the separate authorization boundary for pushing and running exact-tree hosted CI across all eight native/oldstable legs, artifact handoff, reproducibility and provenance. Do not push, dispatch remote CI, mark Stage 0 Complete, or begin Stage 1 while that authorization and Stage 0 exit evidence are absent.

## Required reading for the next session

1. [Documentation map](docs/README.md)
2. [Implementation plan](IMPLEMENTATION_PLAN.md), Stage 0
3. [Feature matrix](docs/product/feature-matrix.md), Stage 0 rows
4. [Stage 0 specification](docs/stages/00-foundation.md)
5. [Stage 0 verification](docs/verification/stage-00.md)
6. [Approved design](docs/superpowers/specs/2026-07-14-vim-first-sftp-commander-design.md)
7. ADRs referenced by Stage 0

## Validation record

The current command/result ledger is [Stage 0 verification](docs/verification/stage-00.md). The latest controller-run checkpoint used macOS GNU Bash 3.2 and GNU Make 3.81 with external output paths containing spaces. Go 1.26.5 `make check`, `lint`, `supply-chain`, `build-all`, and `ci` passed, including full race, four fuzz targets, zero lint issues, no reported vulnerabilities, actionlint, four cross-builds with metadata assertions, and native darwin/arm64 help/version smoke. Exact Go 1.25.12 `make check` also passed. Complete candidate and ignored-output tree/tar snapshots were byte-identical before and after, and staging remained empty. All four immutable action refs were revalidated read-only against their official tags on 2026-07-15.

Task 11 focused revalidation now also passes: the final cross-document decision review is clean; GNU Make 3.81 rejects late/continued execution flags, target-specific controls, internal-guard command-line overrides and `-e` environment overrides while preserving all 14 forced guards and 11 Go probes and accepting safe output-directory assignments. Provenance policy binds actual artifact hashes and target tuples to comparison evidence, requires `-buildvcs=false` for cross/repro builds, compares canonical shell content with exact semantic whitespace, and fixes nightly fuzz/concurrency workloads into the producer profile. IPC envelope/control JSON is strict UTF-8 on decode and encode; Code/Retry/Effect are canonical and retry delay is non-negative while raw error paths remain base64 diagnostic context. Focused package/race/vet/lint/docscheck checks and independent re-reviews passed with staging empty.

The accepted final local closeout checkpoint is tree `5d598eea00fac2b5580bc04596d2bb2c435f4799` at `/private/var/folders/l7/7379px6d495gzqjf6df3953m0000gn/T/amsftp-stage0-final.b5i0FK`; its compact attestation and the earlier exact replay ledger are in Stage 0 verification. CI/nightly have not been dispatched. Missing hosted legs are exactly: `native-ubuntu-22.04` (Go 1.26.5), `oldstable-ubuntu-22.04` (Go 1.25.12), `native-ubuntu-24.04` (Go 1.26.5), `oldstable-ubuntu-24.04` (Go 1.25.12), `native-macos-15` (Go 1.26.5), `oldstable-macos-15` (Go 1.25.12), `native-macos-15-intel` (Go 1.26.5), and `oldstable-macos-15-intel` (Go 1.25.12). Workflow/job/artifact links, independent-cache reproducibility and provenance aggregation remain. Cross-compilation is not native evidence. The prior Task 9 clean-main result remains revoked; its correct-root replacement remains accepted only for that snapshot.

## Known constraints and deliberately deferred choices

- The product display name is `AMSFTP` and the public command is `amsftp`; `awesome-mac-sftp` remains only the repository name. The package/application ID is `io.github.tyrantlucifer.amsftp`.
- The Stage 0 module is `github.com/TyrantLucifer/awesome-mac-sftp`, with Go 1.25.0 language compatibility, Go 1.26.5 preferred, and exact Go 1.25.12 oldstable verification.
- Cross-host direct transfer is not assumed to work with Kerberos. It is an optional capability that must prove destination reachability and non-interactive credentials on the source host without forwarding or copying user credentials; otherwise the route is local relay.
- GUI opener behavior differs by platform. Stage 3 must implement platform adapters and validate lease/change detection on both macOS and Linux.
- Two user-owned IDE files, `.idea/.gitignore` and `.idea/misc.xml`, appeared concurrently during the Task 8 final review. Task 11 classified `.idea/` as local JetBrains/Java IDE metadata and excluded it through the repository root `.gitignore`; the files themselves were preserved and are not product-candidate content.

## Working-tree policy

- Do not commit unless the user explicitly requests it.
- Preserve `.superpowers/` as disposable coordination output only; it is ignored by Git and cannot be the sole durable evidence source. The deleted Superpowers skill is not a project dependency; `docs/superpowers/` is only a historical path for the approved durable design document.
- Review tracked and untracked Stage 0 files together; a plain `git diff` omits most current implementation files.
- At the end of every work session, update this file even when work stops on a failure.
