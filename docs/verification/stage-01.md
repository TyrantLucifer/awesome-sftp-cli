# Stage 1 Verification Record

- **Status**: In Progress
- **Updated**: 2026-07-15
- **Repository root**: `/Users/bytedance/Downloads/projects/awesome-mac-sftp`
- **Branch**: `codex/stage1-read-only-explorer`
- **Stage 0 baseline commit/tree**: `d637474ac52ef2c5b9f78c9be663e52c6a9f441c` / `83a515607f44f7edb85f8103962b6d9d1173c02d`
- **Current milestone**: M1.4 — Workspace and recovery
- **Current candidate**: M1.3 complete at `7f0ea00981cecd5799b3c17ee56eff204cfd5a90`

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
- **Next action**: exact `github.com/pkg/sftp v1.13.10` intake, then ADR-0001 validated `/usr/bin/ssh` stdio transport and SFTP Provider.

Required evidence:

- [x] Exact tcell pin, `go.sum`, module graph, license/changelog/retraction/vulnerability review.
- [x] Go 1.25.12 and 1.26.5 compatibility plus darwin/linux × amd64/arm64 `CGO_ENABLED=0` builds.
- [ ] ADR-0007 config/state/cache/log/runtime and ancestor trust on macOS/Linux, including ACL profiles and sticky `/tmp` fallback.
- [ ] Single-instance lock, stale socket, `0600` socket, no TCP listener, and bidirectional peer UID verification.
- [x] Daemon auto-start path, handshake/reconnect/cancel, bounded in-flight requests, single-instance convergence, five reconnect cycles, idle-connection shutdown and socket cleanup are covered locally.
- [x] LocalFS shared Provider contract and explicit read-only route boundary pass locally.
- [x] Local/local two-pane model, visible-window renderer, Vim navigation/filter/selection, terminal sanitization and 64 KiB preview pass locally.
- [x] 50,000-entry structural renderer benchmark and offline PTY browse/quit/SIGTERM smoke pass on darwin/arm64.

Hosted Linux native ACL/SO_PEERCRED/flock/socket execution and both macOS runner variants are green. Real Darwin allow/deny ACL kernel fixtures and hostile other-UID peers remain final Stage 1 hardening evidence and are not inferred from parser fixtures.

### M1.2 — Real SFTP endpoints

- **Status**: Complete
- **Goal**: exact pkg/sftp intake, ADR-0001 system OpenSSH transport, SFTP Provider, local/remote and remote/remote browsing.

Current candidate evidence:

- Root module directly pins `github.com/pkg/sftp v1.13.10` (tag commit `939b20346433320aab08dfb0f175db0742304cf5`, `go 1.23.0`, BSD-3-Clause, not retracted). Runtime additions are `github.com/kr/fs v0.1.0` and `golang.org/x/crypto v0.41.0`, both BSD-3-Clause and not retracted.
- `govulncheck ./...` reports zero reachable vulnerabilities. It reports vulnerable symbols in required modules that the candidate does not call; the exact scan output is retained as a dependency-risk note rather than mislabeled as a clean module graph.
- `internal/transport/openssh` validates `/usr/bin/ssh` or a clean absolute override from root through final inode, rejects symlink/writable/special-bit/non-executable paths, compares the final inode immediately before start, uses the exact ADR-0001 argv, rejects option/control-byte host aliases, and bounds sanitized stderr to 64 KiB.
- `internal/provider/sftp` runs structured SFTP over the OpenSSH stdio pipes and passes the shared read-only Provider contract against a real in-process SFTP protocol server. Client RPC can add per-connection SSH endpoints, so local/remote and remote/remote pane combinations use the same daemon routes.
- The quality job provisions two temporary real `sshd` instances and runs `TestRealOpenSSHSFTPHostAliasAndNonDefaultPort` with the isolated runner account's ephemeral `ssh_config`. It proves two independently browsable endpoints, Host aliases, non-default ports, poisoned-PATH fake ssh 0-hit, ADR-0001 overrides against conflicting TTY/escape/session/forward/local/remote-command/stdin/background/tunnel settings, and disconnect isolation. Local runs intentionally skip that guarded test because modifying a developer's real SSH config is outside the safe local fixture boundary.
- Commit `28f8731604201763e48bf43c5a7f7e2a7014ca6c` passed local `make check`, `make lint`, `make docs-check`, `make supply-chain`, full race, exact Go 1.25.12 and four CGO-disabled target builds. Hosted run [29401801663](https://github.com/TyrantLucifer/awsome-sftp-cli/actions/runs/29401801663) passed quality including the strengthened real-sshd fixture, all native/oldstable/build/reproducibility jobs, compare and provenance aggregation.

Stage-level carry-forward: pkg/sftp v1.13.10 exposes `ReadDirContext` as a complete slice, so daemon/UI pages are bounded but the source listing is not yet truly streamed; the Stage 1 exit gate remains open until that limitation is resolved. Durable reconnect, degraded-state UI and location recovery are M1.4 work. Root/current-euid replacement after final inode validation remains ADR-0001's declared same-user machine trust boundary.

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
| AUTH-003 | PASS | GSSAPI-only valid, missing, expired and externally renewed ticket cases passed in Hosted job 87330882913. |
| AUTH-004 | PASS | Real key, login-agent, password and key-passphrase plus password MFA passed through system OpenSSH in Hosted job 87330882913. |
| AUTH-005 | PASS | Real ProxyCommand and ProxyJump SFTP cases passed without application-side ssh_config parsing in Hosted job 87330882913. |
| AUTH-006 | PASS | Broker owner/race/detach/no-client/timeout/cancel/prompt-limit, same-binary askpass/TUI, focused race and real interactive cases passed. |
| AUTH-008 | PASS | Known host, explicit first-use confirmation and changed-key fail-closed with unchanged known_hosts passed in Hosted job 87330882913. |
| AUTH-009 | PASS | Plaintext secret markers, Kerberos cache-path/content-copy scans and credential destruction passed in Hosted job 87330882913. |

### M1.4 — Workspace and recovery

- **Status**: In Progress
- **Goal**: CLI Locations, Host picker, workspace save/restore, disconnect/daemon/capability/location recovery, and macOS/Linux PTY evidence.

## Stage 1 exit evidence

The checklist in [Stage 1 specification](../stages/01-read-only-explorer.md#6-可验证退出标准) remains open until the final cross-milestone audit. Feature Matrix rows remain `In Progress` or `Planned` and may become `Verified` only when code, focused tests, required real-environment evidence, this ledger and `PROJECT_STATE.md` agree.

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

## Failures, fixes and skipped gates

The first PTY smoke found that daemon context cancellation did not interrupt an idle framed read. `TestServeConnContextCancellationClosesIdleConnection` now reproduces that case, `ServeConn` closes the connection on cancellation, focused race tests pass, and the repeated PTY smoke exits both client and daemon cleanly. M1.2's first strengthened sshd run [29401311147](https://github.com/TyrantLucifer/awsome-sftp-cli/actions/runs/29401311147) exposed an unbounded fixture wait on forked sshd children; the second [29401550909](https://github.com/TyrantLucifer/awsome-sftp-cli/actions/runs/29401550909) proved listener termination was not a deterministic established-session disconnect. The final fixture closes the product-owned OpenSSH session, verifies typed interruption and second-endpoint isolation, and passed on [29401801663](https://github.com/TyrantLucifer/awsome-sftp-cli/actions/runs/29401801663).

M1.3's first three Kerberos Hosted attempts established separate failure boundaries: run `29407836699` found an IPv6-only keyscan mismatch, run `29408137670` found a non-default-port known_hosts alias mismatch, and run `29408333811` proved successful TGT/service-ticket issuance before sshd rejected the locked local account. After the required stop/reassessment, the isolated account was unlocked with an unused random local password while sshd kept password, keyboard-interactive and public-key authentication disabled. Run `29408646901` then passed the full GSSAPI matrix. The final host-key negative extension and unchanged Kerberos regression both passed on [29408865534](https://github.com/TyrantLucifer/awsome-sftp-cli/actions/runs/29408865534). Required Hosted environment evidence cannot be replaced by mocks, skips or weakened assertions.
