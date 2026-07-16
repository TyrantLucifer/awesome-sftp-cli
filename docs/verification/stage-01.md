# Stage 1 Verification Record

- **Status**: Complete
- **Updated**: 2026-07-16
- **Repository root**: `/Users/bytedance/Downloads/projects/awesome-mac-sftp`
- **Branch**: `codex/stage1-read-only-explorer`
- **Stage 0 baseline commit/tree**: `d637474ac52ef2c5b9f78c9be663e52c6a9f441c` / `83a515607f44f7edb85f8103962b6d9d1173c02d`
- **Current milestone**: Stage 1 complete; Stage 2 not started
- **Current candidate**: final implementation commit `90cbfea81bd2d802bd3f7579a0b192c81ba3281b`, tree `53c7b1ac62e809b7046ea366701a21e6dc0bf757`; [Hosted run 29467496969](https://github.com/TyrantLucifer/awsome-sftp-cli/actions/runs/29467496969) passed 24/24 jobs

Stage 1 delivers the read-only explorer only. It does not deliver Stage 2 transfer or mutation operations, Stage 3 external editing/cache, Stage 4 helper/search, Stage 5 direct transfer/scale hardening, or Stage 6 release readiness.

## Initial safety checkpoint

Run from the repository root before any Stage 1 edit:

| Check | Result |
|---|---|
| `git status --short --branch` | PASS: clean `codex/stage1-read-only-explorer`, tracking `origin/codex/stage1-read-only-explorer` |
| `git rev-parse HEAD` | PASS: exact required baseline `d637474ac52ef2c5b9f78c9be663e52c6a9f441c` |
| `git rev-parse HEAD^{tree}` | PASS: `83a515607f44f7edb85f8103962b6d9d1173c02d` |
| `git remote -v` and `git branch -vv` | PASS: origin is `TyrantLucifer/awsome-sftp-cli`; local Stage 1 branch tracks the matching remote branch |
| codebase-memory index status | PASS: repository graph is ready at the Stage 0 baseline |

No user change was present or overwritten at initialization.

## M1.1 dependency intake — tcell v3.4.0

The root module now directly pins `github.com/gdamore/tcell/v3 v3.4.0`; `go.sum` records the downloaded module and runtime transitive graph. The upstream tag resolves to commit `c67165c6c22b6758eb43209aaee45303f5b08b5b`, declares `go 1.25.0`, and is not retracted.

Runtime transitive modules are `github.com/clipperhouse/displaywidth v0.11.0`, `github.com/clipperhouse/uax29/v2 v2.7.0`, `github.com/gdamore/encoding v1.0.1`, `github.com/lucasb-eyer/go-colorful v1.4.0`, `golang.org/x/sys v0.44.0`, `golang.org/x/term v0.43.0`, and `golang.org/x/text v0.37.0`. The complete MVS graph also names build-graph-only modules reported by `go mod why -m` as not needed by the main module. Every selected version reports no retraction. License review found Apache-2.0 for tcell/gdamore encoding, MIT for clipperhouse/go-colorful/goldmark, and BSD-3-Clause for the Go x modules; no incompatible runtime license was found.

| Command/check | Result |
|---|---|
| `GOTOOLCHAIN=go1.26.5 go list -m -json github.com/gdamore/tcell/v3@v3.4.0` | PASS: exact tag, commit, timestamp and Go 1.25.0 declaration recorded |
| `GOTOOLCHAIN=go1.26.5 go list -m -retracted ...` for the complete selected graph | PASS: no selected version is retracted |
| upstream `LICENSE`, `go.mod` and `CHANGESv3.md` plus each selected module license | PASS: Apache/MIT/BSD-compatible graph; tcell v3 event/cell API changes acknowledged |
| `GOTOOLCHAIN=go1.26.5 go mod tidy` and `go mod download all` | PASS: exact direct and indirect requirements plus checksums generated |
| Go 1.26.5 and Go 1.25.12 `go mod verify` | PASS: `all modules verified` under both toolchains |
| `GOTOOLCHAIN=go1.26.5 go tool -modfile=tools/go.mod govulncheck ./...` | PASS: `No vulnerabilities found.` |
| CI setup-go cache policy and focused `TestCISetupGoRequiresBothModuleLocks` | PASS: canonical cache input includes exact root `go.sum` and `tools/go.sum`; omitting either fails closed |
| `GOTOOLCHAIN=go1.26.5 go test -count=1 ./...` | PASS on the complete local M1.1 candidate |
| `GOTOOLCHAIN=go1.25.12 go test -count=1 ./...` | PASS on the same candidate |
| four `CGO_ENABLED=0` darwin/linux × amd64/arm64 product builds | PASS; artifacts written outside the repository |
| `GOTOOLCHAIN=go1.26.5 make check`, `make lint`, `make supply-chain` | PASS; lint reports 0 issues and govulncheck reports no vulnerabilities |

The local dependency intake sub-gate is closed. Hosted native and oldstable jobs remain required before the milestone itself becomes Complete.

## Milestone ledger

### M1.1 — Local read-only end to end

- **Status**: Complete
- **Goal**: exact tcell intake; ADR-0007 Paths/ACL/lock/peer UID; daemon/IPC lifecycle; LocalFS; local/local Vim-first windowed TUI and bounded preview.
- **Candidate commit**: `8e649f534b500e494ec2984a763e4491711df5fe`
- **Hosted run**: [29399674061](https://github.com/TyrantLucifer/awsome-sftp-cli/actions/runs/29399674061) — PASS for native, oldstable, quality, four builds, eight reproducibility producers, compare and provenance aggregation
- **Historical next action after M1.1**: exact `github.com/pkg/sftp v1.13.10` intake, then ADR-0001 validated `/usr/bin/ssh` stdio transport and SFTP Provider. ADR-0011 later superseded that initial pin as recorded under M1.4.

Required evidence:

- [x] Exact tcell pin, `go.sum`, module graph, license/changelog/retraction/vulnerability review.
- [x] Go 1.25.12 and 1.26.5 compatibility plus darwin/linux × amd64/arm64 `CGO_ENABLED=0` builds.
- [x] ADR-0007 config/state/cache/log/runtime and ancestor trust on macOS/Linux, including ACL profiles and sticky `/tmp` fallback.
- [x] Single-instance lock, stale socket, `0600` socket, no TCP listener, and bidirectional peer UID verification.
- [x] Daemon auto-start path, handshake/reconnect/cancel, bounded in-flight requests, single-instance convergence, five reconnect cycles, idle-connection shutdown and socket cleanup are covered locally.
- [x] LocalFS shared Provider contract and explicit read-only route boundary pass locally.
- [x] Local/local two-pane model, visible-window renderer, Vim navigation/filter/selection, terminal sanitization and 64 KiB preview pass locally.
- [x] 50,000-entry structural renderer benchmark and offline PTY browse/quit/SIGTERM smoke pass on darwin/arm64.

Hosted Linux native ACL/SO_PEERCRED/flock/socket execution and both macOS runner variants are green. Real Darwin allow/deny ACL kernel fixtures and hostile other-UID peers remain final Stage 1 hardening evidence and are not inferred from parser fixtures.

### M1.2 — Real SFTP endpoints

- **Status**: Complete
- **Goal**: exact pkg/sftp intake, ADR-0001 system OpenSSH transport, SFTP Provider, local/remote and remote/remote browsing.

Current candidate evidence:

- The M1.2 candidate directly pinned `github.com/pkg/sftp v1.13.10` (tag commit `939b20346433320aab08dfb0f175db0742304cf5`, `go 1.23.0`, BSD-3-Clause, not retracted). ADR-0011 later superseded this historical pin as recorded under M1.4. Runtime additions at M1.2 were `github.com/kr/fs v0.1.0` and `golang.org/x/crypto v0.41.0`, both BSD-3-Clause and not retracted.
- `govulncheck ./...` reports zero reachable vulnerabilities. It reports vulnerable symbols in required modules that the candidate does not call; the exact scan output is retained as a dependency-risk note rather than mislabeled as a clean module graph.
- `internal/transport/openssh` validates `/usr/bin/ssh` or a clean absolute override from root through final inode, rejects symlink/writable/special-bit/non-executable paths, compares the final inode immediately before start, uses the exact ADR-0001 argv, rejects option/control-byte host aliases, and bounds sanitized stderr to 64 KiB.
- `internal/provider/sftp` runs structured SFTP over the OpenSSH stdio pipes and passes the shared read-only Provider contract against a real in-process SFTP protocol server. Client RPC can add per-connection SSH endpoints, so local/remote and remote/remote pane combinations use the same daemon routes.
- The quality job provisions two temporary real `sshd` instances and runs `TestRealOpenSSHSFTPHostAliasAndNonDefaultPort` with the isolated runner account's ephemeral `ssh_config`. It proves two independently browsable endpoints, Host aliases, non-default ports, poisoned-PATH fake ssh 0-hit, ADR-0001 overrides against conflicting TTY/escape/session/forward/local/remote-command/stdin/background/tunnel settings, and disconnect isolation. Local runs intentionally skip that guarded test because modifying a developer's real SSH config is outside the safe local fixture boundary.
- Commit `28f8731604201763e48bf43c5a7f7e2a7014ca6c` passed local `make check`, `make lint`, `make docs-check`, `make supply-chain`, full race, exact Go 1.25.12 and four CGO-disabled target builds. Hosted run [29401801663](https://github.com/TyrantLucifer/awsome-sftp-cli/actions/runs/29401801663) passed quality including the strengthened real-sshd fixture, all native/oldstable/build/reproducibility jobs, compare and provenance aggregation.

Stage-level resolution: the original pkg/sftp v1.13.10 complete-slice boundary was closed by ADR-0011's immutable v1.13.11-based cursor fork, whose context-aware `ReadDirCursor` returns one protocol response at a time. Durable reconnect, degraded-state UI and location recovery were completed in M1.4. Root/current-euid replacement after final inode validation remains ADR-0001's declared same-user machine trust boundary.

### M1.3 — Authentication and complex SSH configuration

- **Status**: Complete
- **Goal**: askpass/Auth Broker plus ProxyCommand/ProxyJump, agent/key/password/MFA and real MIT Kerberos/GSSAPI evidence without secret persistence.

Current candidate evidence:

- `internal/auth` owns short-lived random attempt/challenge IDs, endpoint binding, a bounded prompt count, exact single ownership, attach/detach requeue, single-consumption answers, timeout and cancellation. IPC never returns an answer in a resolve response; the askpass role writes only the one claimed answer to stdout, and the TUI masks the input.
- OpenSSH receives a fresh `SSH_ASKPASS` environment pointing at the installed same binary and retains system authentication sources such as `SSH_AUTH_SOCK`, `KRB5_CONFIG` and `KRB5CCNAME`. The application never invokes `kinit`, parses a private key, or implements another SSH/Kerberos stack. Safe IPC connection errors preserve the public stage/classification while raw OpenSSH causes remain daemon-local.
- Local `GOTOOLCHAIN=go1.26.5 go test -count=1 -race ./internal/auth ./internal/app ./internal/ipc ./internal/daemon ./internal/transport/openssh ./internal/tui` and matching focused `go vet` pass at the M1.3 candidate.
- Hosted run [29408865534](https://github.com/TyrantLucifer/awsome-sftp-cli/actions/runs/29408865534), auth job [87330882913](https://github.com/TyrantLucifer/awsome-sftp-cli/actions/runs/29408865534/job/87330882913), is bound to commit `7f0ea00981cecd5799b3c17ee56eff204cfd5a90`. Its ordinary matrix passed real key, login agent, ProxyCommand, ProxyJump, password, one-attempt wrong password, user cancellation, key-passphrase plus password MFA, explicit first-use host confirmation, and changed-host-key fail-closed without rewriting the supplied known_hosts file. Test-owned config/runtime/log/workspace files were scanned for all injected plaintext secrets.
- The same job's isolated MIT realm passed GSSAPI-only SFTP with a valid ticket, bounded failures for missing and expired tickets, and recovery after an external `kinit` reacquired a ticket. The exact ADR-0001 argv retained GSSAPI authentication while disabling new credential delegation; the fixture scanned client-owned artifacts for the credential-cache path and ticket-byte copies, then destroyed the ccache and keytab.

## M1.3 feature evidence

| ID | Result | Evidence |
|---|---|---|
| AUTH-001 | PASS | Validated absolute system OpenSSH, exact ADR-0001 argv, real sshd and real MIT Kerberos use one stdio transport; no second SSH/Kerberos stack or application `kinit` exists. |
| AUTH-002 | PASS | Real Include/Match, Identity/auth/host-key/GSSAPI/proxy/agent configuration, conflicting fixed options, and ControlMaster new/reuse passed through system OpenSSH. |
| AUTH-003 | PASS | GSSAPI-only valid, missing, expired and externally renewed ticket cases passed in Hosted job 87330882913. |
| AUTH-004 | PASS | Real key, login-agent, password and key-passphrase plus password MFA passed through system OpenSSH in Hosted job 87330882913. |
| AUTH-005 | PASS | Real ProxyCommand and ProxyJump SFTP cases passed without application-side ssh_config parsing in Hosted job 87330882913. |
| AUTH-006 | PASS | Broker owner/race/detach/no-client/timeout/cancel/prompt-limit, same-binary askpass/TUI, focused race and real interactive cases passed. |
| AUTH-008 | PASS | Known host, explicit first-use confirmation and changed-key fail-closed with unchanged known_hosts passed in Hosted job 87330882913. |
| AUTH-009 | PASS | Plaintext secret markers, Kerberos cache-path/content-copy scans and credential destruction passed in Hosted job 87330882913. |

### M1.4 — Workspace and recovery

- **Status**: Complete
- **Goal**: CLI Locations, Host picker, workspace save/restore, disconnect/daemon/capability/location recovery, and macOS/Linux PTY evidence.

Current candidate evidence:

- CLI parsing accepts zero, one or two local/`host:path` Locations and the exclusive `--workspace <name>` form. Stable Endpoint references preserve local roots and OpenSSH Host aliases across save/reopen. Parser and startup tests reject ambiguous or excess operands without opening a mutation route.
- OpenSSH Host discovery expands `Include`, respects positive/negated `Host` patterns and offers only concrete selectable aliases. The startup picker combines those aliases with deterministic recent/corrupt workspace summaries, fuzzy filtering and explicit manual alias entry. Endpoint switching is pane-local and commits endpoint, Location and capability revision only after the first successful page; failed or stale async work leaves the previously committed pane intact.
- The versioned workspace document contains two pane Endpoint references, canonical paths, sorting/filter/layout state and an ephemeral cache policy, but no credential material. The owner-private store writes regular `0600` files atomically, preserves the prior document on interrupted replacement, surfaces corrupt files, and refuses to overwrite corruption silently.
- Listing/refresh transactions carry connection epochs and request generations. Stale pages are ignored, partial state is retained, refresh remaps cursor/marks by canonical Location, invalid directories recover toward the nearest valid parent without changing Endpoint, and capability replacement cannot leak state between panes. Preview requests are cancellable, preserve sanitized line structure and remain capped at 64 KiB.
- The TUI implements bounded numeric counts only for safe navigation and bounded repeat for the last repeatable navigation action. Normal/Visual selection, independent sort/hidden/refresh controls, direct path navigation, endpoint/workspace modals and read-only intent tests keep all file mutation operations unreachable in the Stage 1 UI/RPC/CLI surface.
- Client-visible daemon failures append only a safe correlation summary (`request_id`, canonical error code, Endpoint and retry/effect); owner-private bounded JSON logs retain the diagnostic cause without passwords, askpass answers, ticket material or raw file content.
- The Hosted authentication fixture already proves `Include`/`Match`, ControlMaster creation/reuse, bounded cancellation that returns control to the TUI, and changed-host-key refusal with the supplied known_hosts file byte-identical. The prior recovery fixture exposed that a reconnect creates a new Endpoint but its bare parent fallback dropped the Endpoint commit transaction, so the reducer correctly rejected the page. The new `paneRecovery` state machine retains the exact validation intent across fallback, tracks its generation, terminates on non-recoverable errors and completes only on the current fallback's terminal page. Deterministic tests cover each transition; the unchanged real two-sshd fixture remains the required Hosted proof.

Local validation before the Hosted-only recovery failure:

| Command/check | Result |
|---|---|
| `make docs-check` | PASS |
| `make ci` | PASS: contract/fmt/vet/unit/race, lint (0 issues), docs, module verification, four one-second fuzz smokes, supply-chain scan, actionlint and four CGO-disabled builds |
| `GOTOOLCHAIN=go1.25.12 make check` | PASS on the exact oldstable toolchain |
| `go tool -modfile=tools/go.mod govulncheck ./...` within `make ci` | PASS for reachable code: 0 reachable vulnerabilities; imported but uncalled module findings remain documented dependency risk |
| `bash -n internal/integration/hosted-stage1-auth.sh internal/integration/hosted-stage1-recovery.sh` | PASS after strict-shell fixture correction |

Hosted candidate [run 29416890911](https://github.com/TyrantLucifer/awsome-sftp-cli/actions/runs/29416890911) is bound to commit `4803d2789504d00566d51282b82c5d6fa5bf561a`. Quality, native Ubuntu 22.04/24.04, native macOS 15 ARM/Intel, all oldstable legs, four builds and reproducibility/compare passed. Auth job [87357175516](https://github.com/TyrantLucifer/awsome-sftp-cli/actions/runs/29416890911/job/87357175516) passed Include/Match, ControlMaster new/reuse, key, agent, ProxyCommand, ProxyJump, password, wrong-password, cancel, MFA, confirmation and changed-host-key cases before the recovery scenario failed. The job and run are FAILED, MIT Kerberos did not run in this exact candidate because it follows that step, and final compare was skipped.

Source-streaming resolution:

- [ADR-0011](../architecture/adr/0011-pkg-sftp-streaming-directory-cursor.md) replaces the v1.13.10 pin with an immutable narrow fork based on upstream v1.13.11. Root imports remain `github.com/pkg/sftp`; the exact replace is `github.com/TyrantLucifer/sftp v1.13.12-0.20260715132526-f947b886400b`, commit `f947b886400be01ed663564525f8bacf1be6c74e`, tree `32258bedd3d535d1da84d5ca9fc489533bce88c6`, module sum `h1:nTtLW2gYG18og3cJx3d8cxf1vEX4naYExzYQvdRtg6s=`.
- The fork exposes a context-aware `ReadDirCursor` that returns one `SSH_FXP_NAME` response at a time and safely rejects impossible counts/truncated names. Its real client/server fixture returns a 257-entry directory as `128/128/1/EOF`; current-toolchain, race and Go 1.25.12 suites pass.
- The Provider now stores only the remote cursor plus the current protocol batch remainder. A deterministic request-server test blocks the second `READDIR` and proves `Limit=1` returns the first page before requesting that batch. EOF, cancellation, error, conflict, discard and Provider close release the handle.
- Fork `go vet ./...` reports the same two `ReadFrom` signature and two test lock-copy warnings as pristine upstream v1.13.11; the fork adds none. This upstream baseline exception does not weaken the root module's vet/lint gates.
- PANE-004 is Verified: the packet-bounded cursor tests and complete exact-head local/Hosted gates pass.

Approved blocker-resolution local validation:

| Command/check | Result |
|---|---|
| fork `GOTOOLCHAIN=go1.26.5 go test ./... -count=1` and `go test -race ./... -count=1` | PASS at immutable fork commit `f947b886400be01ed663564525f8bacf1be6c74e` |
| fork `GOTOOLCHAIN=go1.25.12 go test ./... -count=1` | PASS on the project oldstable line |
| `GOTOOLCHAIN=go1.26.5 go test -count=1 ./internal/app ./internal/tui ./internal/provider/sftp` | PASS for recovery state, reducer transaction and streaming Provider tests |
| same focused packages with `-race`, plus exact Go 1.25.12 without race | PASS |
| Go 1.26.5 and 1.25.12 `go mod tidy -diff` / `go mod verify` | PASS; both report `all modules verified` and no tidy diff |
| `GOTOOLCHAIN=go1.26.5 make ci` | PASS: fmt/vet/unit/contracts/docs/module verification, lint 0 issues, full race, four fuzz smokes, govulncheck/actionlint and four CGO-disabled target builds |
| `GOTOOLCHAIN=go1.25.12 make check` | PASS on the complete same working-tree candidate |
| `git diff --check` | PASS |

`govulncheck` reports zero reachable and zero imported-package vulnerabilities, with one uncalled finding somewhere in the required module graph; this remains a dependency-risk note rather than a false claim that every required module is vulnerability-free. Exact-head Hosted/native, real recovery and reproducibility evidence passed in run 29467496969.

Platform final-evidence candidate:

- Darwin-native tests now create real kernel ACLs and prove deny-only ACLs do not expand authority, owner-private direct allow-read is rejected, an integrity ancestor granting mutation is rejected, and a kernel-inherited allow ACE is both observed in `filesec` data and rejected.
- Linux-native tests set real `system.posix_acl_access` and `system.posix_acl_default` xattrs and prove named effective access and inherited defaults fail closed.
- A subprocess fixture proves the instance lock is exclusive across processes and available only after release.
- A root-gated adversarial fixture creates a real client under another UID, deliberately widens the test directory/socket DAC modes, and proves the listener's peer-credential check independently rejects it. Native CI compiles one platform test binary, runs kernel ACL/lock tests unprivileged, then runs this peer test through `sudo` on Ubuntu 22.04/24.04 and macOS 15 ARM/Intel.
- Fresh local `go test -race -count=1 ./internal/platform`, exact `GOTOOLCHAIN=go1.25.12 go test -count=1 ./internal/platform`, `make docs-check`, actionlint and `git diff --check` pass. Exact-head Hosted run [29417470068](https://github.com/TyrantLucifer/awsome-sftp-cli/actions/runs/29417470068) then passed the kernel ACL, cross-process lock and root hostile-UID step on [Ubuntu 22.04](https://github.com/TyrantLucifer/awsome-sftp-cli/actions/runs/29417470068/job/87359131342), [Ubuntu 24.04](https://github.com/TyrantLucifer/awsome-sftp-cli/actions/runs/29417470068/job/87359131321), [macOS 15 ARM](https://github.com/TyrantLucifer/awsome-sftp-cli/actions/runs/29417470068/job/87359131251), and [macOS 15 Intel](https://github.com/TyrantLucifer/awsome-sftp-cli/actions/runs/29417470068/job/87359131365). DAEM-002 and SEC-001 are Verified, and the same platform matrix passed again in the final run.

## Stage 1 explorer feature evidence

These rows record independently satisfied Stage 1 features. Rows whose acceptance also includes later-stage behavior remain `In Progress` in the Feature Matrix and are intentionally absent here.

| ID | Result | Evidence |
|---|---|---|
| CONN-001 | PASS | LocalFS shared contract, local/local PTY and all four native platform legs pass. |
| CONN-002 | PASS | Exact OpenSSH stdio transport and structured SFTP contract pass with two real sshd endpoints and poisoned PATH 0-hit. |
| CONN-003 | PASS | Zero/one/two CLI Locations and the real local/local, local/remote and remote/remote PTY scenarios pass. |
| CONN-004 | PASS | Include/pattern Host discovery and deterministic fuzzy/manual picker tests and snapshots pass. |
| CONN-005 | PASS | Two isolated real sshd sessions browse independently; terminating one product-owned session leaves the other usable. |
| CONN-006 | PASS | Real sshd and daemon restart, bounded reconnect, stale-result rejection and nearest-parent recovery pass in run 29467496969. |
| CONN-007 | PASS | Capability generation replacement/withdrawal tests and the real reconnect transaction reject reuse of the old session snapshot. |
| CONN-008 | PASS | Domain, IPC, CLI and workspace tests preserve Endpoint identity, canonical absolute paths and raw bytes. |
| CONN-009 | PASS | Host alias, stage, safe request/code/endpoint/retry context and actionable auth/host-key/transport/subsystem messages are tested. |
| PANE-001 | PASS | Equal independent pane reducers/render snapshots and native PTY evidence pass. |
| PANE-002 | PASS | All local/remote combinations and two independent real sshd endpoints pass. |
| PANE-003 | PASS | Pane-local endpoint switching/recovery preserves the other pane and commits only after a successful first page. |
| PANE-004 | PASS | ADR-0011 returns a first page before a blocked second `READDIR`; slow/cancel/generation tests and complete gates pass. |
| PANE-005 | PASS | Visible-window/overscan tests and the 50,000-entry structural benchmark remain bounded. |
| PANE-006 | PASS | LocalFS/SFTP contract and renderer fixtures preserve available type, size, mtime, permission and link metadata. |
| PANE-007 | PASS | Per-pane sort/hidden/refresh and canonical cursor/mark remapping tests pass. |
| PANE-008 | PASS | Direct parent/child/root navigation and failed-location transaction tests pass without premature pane changes. |
| PANE-010 | PASS | Partial pages survive cancellation/error, stale completions are rejected and refresh recovery is rendered explicitly. |
| WORK-001 | PASS | Atomic owner-private workspace save and real two-remote PTY save/reopen pass. |
| WORK-002 | PASS | `--workspace` restores both remote panes in a fresh process and preserves missing/corrupt recovery behavior. |
| WORK-003 | PASS | Recent/corrupt workspace ordering, Host merge, fuzzy/manual selection and minimum-size picker snapshots pass. |
| WORK-004 | PASS | Strict schema and secret scans prove workspaces contain Endpoint aliases/UI policy but no credential material. |
| VIM-001 | PASS | Normal-mode startup, mode visibility and layered modal exit tests/snapshots pass. |
| VIM-002 | PASS | `h/j/k/l`, bounded safe navigation counts and safe repeat reducer/translation tests pass. |
| VIM-003 | PASS | `Tab` preserves both PaneState values and updates only focus in reducer/render tests. |
| VIM-004 | PASS | Visual selection growth/cancel and canonical refresh remapping tests pass. |
| VIM-005 | PASS | Discrete mark toggle/identity/remapping tests pass without mutation intents. |
| VIM-012 | PASS | Filter/path/endpoint/workspace/selection/preview Esc tests and real auth cancellation return control predictably. |
| DAEM-001 | PASS | Ten-process convergence, stale socket, repeated reconnect, cancellation and four-platform PTY exit/re-entry pass. |
| DAEM-002 | PASS | Real Darwin/Linux kernel ACLs, cross-process locking and hostile-other-UID peer rejection passed on all four native Hosted legs in run 29417470068. |
| PREV-001 | PASS | Bounded LocalFS/SFTP reads, 64 KiB cap, binary/split-UTF-8/multiline/stale/cancel tests pass. |
| SRCH-001 | PASS | Loaded and incoming page filtering, clear/cursor behavior and zero remote-command routes pass. |
| SEC-001 | PASS | The private Unix socket has no TCP route and rejects a real other-UID peer even when the adversarial fixture removes DAC protection. |
| SEC-002 | PASS | Broker lifetime plus Hosted secret/workspace/log/Kerberos artifact scans pass. |
| SEC-003 | PASS | Resolver, exact argv, poisoned PATH, real proxy/auth/GSSAPI and changed-key refusal evidence pass. |
| SEC-009 | PASS | Malicious filenames, stderr, picker problems, preview lines and invalid UTF-8 are terminal-sanitized. |
| PLAT-008 | PASS | Minimum layout plus Ubuntu/macOS Intel/ARM PTY resize, quit/re-entry and SIGTERM smokes pass. |

## Stage 1 exit evidence

Every checklist item in [Stage 1 specification](../stages/01-read-only-explorer.md#6-可验证退出标准) is closed. Feature Matrix rows are `Verified` where Stage 1 owns the full acceptance; cross-stage rows remain `In Progress` when transfer, editor/cache, helper/search or release acceptance intentionally belongs to later stages.

Mandatory final commands include:

```text
make docs-check
make check
make lint
make supply-chain
make ci
go test -race ./...
```

They must be supplemented by Stage 1 integration, PTY, sshd, Kerberos, Provider contract and performance tests; exact Go 1.25.12; four-target builds and metadata; macOS/Linux native and oldstable Hosted CI; reproducibility/provenance comparison; complete candidate-tree pollution checks; and an independent cold-start audit.

Final implementation candidate results:

| Command/check | Result |
|---|---|
| `GOTOOLCHAIN=go1.26.5 make ci` | PASS: docs/check/lint/supply-chain, full race, four fuzz smokes, actionlint and four CGO-disabled builds |
| `GOTOOLCHAIN=go1.25.12 make check` | PASS on the exact oldstable toolchain |
| `GOTOOLCHAIN=go1.26.5 go test -count=1 ./internal/integration` | PASS |
| `GOTOOLCHAIN=go1.25.12 go test -count=1 ./internal/integration` | PASS |
| `GOTOOLCHAIN=go1.26.5 go test -race -count=1 -run '^(TestVTObserverAccumulatesPatternsAcrossSynchronizedFrames\|TestHostedStage1RecoveryNormalizesSplitTerminalWrites\|TestHostedKerberosFailureKeepsTUIResponsive)$' ./internal/integration` | PASS |
| `bash -n internal/integration/hosted-kerberos.sh`; `git diff --check`; `git diff --cached --check` | PASS |
| `git status --short --untracked-files=all`; `git ls-files --others --exclude-standard`; candidate `git ls-tree -r --name-only HEAD` pollution review | PASS: no staged/non-ignored untracked product pollution; ignored local-only paths are recorded in `PROJECT_STATE.md` |
| Independent cold-start audit of commit `e0a8368fd92ff73dbae3f94ba652a7016acb90a5`, tree `2ba553689169b8feb76908000cbe07793fb89375`, starting only from `docs/README.md` | PASS: lifecycle, exact evidence, commands, delivered/deferred scope, dependency boundary, pollution inventory and the single Stage 2 first action were recovered without prior context; no content/completion blocker found |
| [Hosted run 29467496969](https://github.com/TyrantLucifer/awsome-sftp-cli/actions/runs/29467496969) | PASS 24/24 at commit `90cbfea81bd2d802bd3f7579a0b192c81ba3281b`, tree `53c7b1ac62e809b7046ea366701a21e6dc0bf757` |
| [auth job 87523638581](https://github.com/TyrantLucifer/awsome-sftp-cli/actions/runs/29467496969/job/87523638581) | PASS: real OpenSSH, recovery and MIT Kerberos/GSSAPI matrices |

## Failures, fixes and skipped gates

The first PTY smoke found that daemon context cancellation did not interrupt an idle framed read. `TestServeConnContextCancellationClosesIdleConnection` now reproduces that case, `ServeConn` closes the connection on cancellation, focused race tests pass, and the repeated PTY smoke exits both client and daemon cleanly. M1.2's first strengthened sshd run [29401311147](https://github.com/TyrantLucifer/awsome-sftp-cli/actions/runs/29401311147) exposed an unbounded fixture wait on forked sshd children; the second [29401550909](https://github.com/TyrantLucifer/awsome-sftp-cli/actions/runs/29401550909) proved listener termination was not a deterministic established-session disconnect. The final fixture closes the product-owned OpenSSH session, verifies typed interruption and second-endpoint isolation, and passed on [29401801663](https://github.com/TyrantLucifer/awsome-sftp-cli/actions/runs/29401801663).

M1.3's first three Kerberos Hosted attempts established separate failure boundaries: run `29407836699` found an IPv6-only keyscan mismatch, run `29408137670` found a non-default-port known_hosts alias mismatch, and run `29408333811` proved successful TGT/service-ticket issuance before sshd rejected the locked local account. After the required stop/reassessment, the isolated account was unlocked with an unused random local password while sshd kept password, keyboard-interactive and public-key authentication disabled. Run `29408646901` then passed the full GSSAPI matrix. The final host-key negative extension and unchanged Kerberos regression both passed on [29408865534](https://github.com/TyrantLucifer/awsome-sftp-cli/actions/runs/29408865534). Required Hosted environment evidence cannot be replaced by mocks, skips or weakened assertions.

M1.4 recovery used its three allowed evidence-driven Hosted attempts before the user authorized the explicit state-machine repair:

1. [Run 29416101770](https://github.com/TyrantLucifer/awsome-sftp-cli/actions/runs/29416101770), commit `a67ea98a10727f8cd2514d7bece8dc9303faf7a6`, fixed strict-shell local initialization and reached the PTY fixture, then timed out before the first local/remote markers. The fixture lacked failure-tail diagnostics.
2. [Run 29416557838](https://github.com/TyrantLucifer/awsome-sftp-cli/actions/runs/29416557838), commit `c3afef05a239f50de0bdf994fe06bb49a7d1d6f9`, unlocked test-only target accounts while keeping password authentication disabled and added PTY tail diagnostics. It passed startup/workspace scenarios, but stopping only the listener process group left an accepted sshd child alive; deleting the path therefore produced `not_found` without a transport reconnect.
3. [Run 29416890911](https://github.com/TyrantLucifer/awsome-sftp-cli/actions/runs/29416890911), commit `4803d2789504d00566d51282b82c5d6fa5bf561a`, terminated the complete `/proc` sshd descendant tree. The transcript proves `connection lost; reconnecting`, a new ready capability session, and then `not_found` for the removed directory. The application did not issue the next parent listing, so `hosted-stage1-recovery.py:322` timed out waiting for `reconnected at nearest accessible parent` and `a-recovered-marker.txt`.

Exact failed command: `sudo env AMSFTP_AUTH_BINARY=... AMSFTP_AUTH_ROOT=... bash ./internal/integration/hosted-auth.sh`, in auth job 87357175516. Root-cause tracing found that `PaneConnected(PreserveCommitted)` correctly emitted an intent containing `CommitEndpoint`, the new Endpoint and capability transaction, but the subsequent `not_found` branch created a bare parent `IntentList`; the reducer therefore correctly refused to commit a page for the new Endpoint. The approved `paneRecovery` state machine preserves the original intent through the parent fallback. RED→GREEN tests cover transaction preservation, current-generation completion, connection failure and terminal listing failure. No fixture assertion or product contract was weakened; the same real recovery scenario will run in the exact candidate.

The first approved-fix Hosted candidate `da4aa361c81ba93d14733819e21c3cba092b3590` ran as push [29420191827](https://github.com/TyrantLucifer/awsome-sftp-cli/actions/runs/29420191827) and PR [29420195012](https://github.com/TyrantLucifer/awsome-sftp-cli/actions/runs/29420195012). In auth job [87368385121](https://github.com/TyrantLucifer/awsome-sftp-cli/actions/runs/29420191827/job/87368385121), the PTY transcript visibly contains the recovered parent, `a-recovered-marker.txt` and the status `reconnected at nearest accessible parent`; all pre-recovery authentication cases passed. The job nevertheless timed out because `wait_for` searched the raw ANSI delta stream for a contiguous status string. tcell wrote `recon`, retained an unchanged `n` cell from the prior `loading` status, then wrote `ected at nearest accessible` and `parent` at explicit cursor positions, so the visible screen was correct while the raw byte substring could not exist. A deterministic harness self-test first disproved simple ANSI stripping, then drove a bounded 200×30 CSI cursor/erase screen replay and `wait_for_screen` assertion. The assertion still requires both the exact recovery status and recovered marker; no product behavior, timeout or fixture postcondition was weakened. This is the first Hosted attempt for the separately identified harness-observation issue.

The second observation candidate `44f2f138951ca8277c2b20350b7903f1e7d3203b` ran in [29421112752](https://github.com/TyrantLucifer/awsome-sftp-cli/actions/runs/29421112752): 22 independent jobs passed, and auth job [87371504535](https://github.com/TyrantLucifer/awsome-sftp-cli/actions/runs/29421112752/job/87371504535) again visibly showed the exact recovered marker/status before `wait_for_screen` timed out. The remaining harness mismatch was that its replay required both strings in one synchronized frame. The final deterministic self-test now models a split retained-cell status frame followed by a separate marker/cleanup frame, scopes observation to the post-refresh checkpoint, and accumulates each exact pattern only at completed tcell synchronized updates. This preserves the two original postconditions within the same recovery event without requiring an incidental single-frame paint order. Docker-based local reproduction was unavailable because the configured local Docker daemon was not running; no Hosted fixture dependency, timeout or assertion text was removed. This is attempt two of three for the harness-observation issue.

Later exact logs separated the product contract from two Kerberos harness defects. First, `log_user 0` combined with `log_file -noappend` produced a zero-byte terminal capture even though Expect had matched the live `failed` output; a minimum isolated Expect/VT experiment reproduced 0 bytes and proved `log_file -a -noappend` records and replays the same frame. [Run 29467304585](https://github.com/TyrantLucifer/awsome-sftp-cli/actions/runs/29467304585) then exposed the second defect: post-spawn `stty rows 30 columns 200` changed the controlling terminal rather than the spawned PTY. The application correctly saw 80×25, clipped the left header before `(failed)`, and rendered `connect auth-gssapi failed` in the status line, while the observer incorrectly replayed the stream as 200×30. Expect's documented pre-spawn `stty_init` contract and an isolated `stty size` proof established the fix without weakening the assertion.

Commit `90cbfea81bd2d802bd3f7579a0b192c81ba3281b` sets the spawned PTY to 200×30 before application startup while retaining structured `auth_required`, live failure paint, exact visible `(failed)`, responsive quit and secret-scan assertions. [Run 29467496969](https://github.com/TyrantLucifer/awsome-sftp-cli/actions/runs/29467496969) passed all 24 jobs, including [auth job 87523638581](https://github.com/TyrantLucifer/awsome-sftp-cli/actions/runs/29467496969/job/87523638581). The failure was therefore a CI fixture-observation configuration defect, not a product flow defect; leaving it unresolved would have blocked evidence and PR readiness but would not have broken ordinary browsing.
