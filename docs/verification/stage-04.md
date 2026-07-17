# Stage 4 Verification Record

- **Status**: In Progress — M4.1 Complete; M4.2/M4.3 implementation in progress
- **Updated**: 2026-07-17
- **Repository root**: `/Users/bytedance/Downloads/projects/awesome-mac-sftp`
- **Branch**: `codex/stage4-search-helper`
- **Stage 3 merge baseline**: commit `09821bdbcfc9693b309a1a39ee5121113c033254`, tree `c18e4cf8faf8eb70cc9964e242513b30ab0e79cc`
- **Baseline Hosted run**: [29517334761](https://github.com/TyrantLucifer/awsome-sftp-cli/actions/runs/29517334761) — exact merge commit, successful
- **Production Helper distribution**: **CLOSED**

Stage 4 delivers Level 0 bounded SFTP search before any optional Helper work. Helper lifecycle, protocol and capability work is authorized only through explicitly injected non-release `testdata` fixtures; the production verifier must reject fixture keys and no production Helper artifact, manifest, key or custody claim may be created in this stage.

## Initial safety checkpoint

| Check | Result |
|---|---|
| `git status --porcelain=v1 -b` | PASS: clean `main`, tracking `origin/main` before branch creation |
| `git rev-parse HEAD origin/main HEAD^{tree}` | PASS: commit `09821bdbcfc9693b309a1a39ee5121113c033254`, tree `c18e4cf8faf8eb70cc9964e242513b30ab0e79cc`, exact remote match |
| fixed branch existence audit | PASS: absent locally and remotely; created once without reset/rebase/overwrite |
| exact-main Hosted run | PASS: run `29517334761`, exact `headSha`, complete `success`, final compare green |
| baseline `make docs-check` | PASS |
| baseline `make check` | PASS |

## Milestone evidence

### M4.1 — SFTP search baseline

**Status: Complete.**

The first implementation action was the required failing standard-Provider filename-search contract. The current Level 0 slice now includes Provider-only bounded `f` traversal, range-bounded literal `g/`, typed terminal/partial reasons, per-session daemon cursors, raw-byte-safe IPC, exact identity rejection in the reducer, bounded drawers, cancel/drain/session-close ownership, preview/copy/open routing, and app snapshot binding. No Helper probe/install/exec surface is reachable from either route.

The synthetic million-node Provider generates only the requested 128-entry page. `TestMillionNodeFilenameFixtureStreamsFirstResultWithoutMaterializingTree` observed one List call, maximum resident page 128 and first result in about 65 µs on the development host. The isolated resource run added about 6 MiB peak RSS and recorded bounded goroutine and FD counts. `AMSFTP_REAL_SSHD=1` now covers Level 0 filename/content results over a real temporary OpenSSH SFTP session. Focused race tests pass.

### M4.2 — Helper install and handshake

**Status: In Progress.** Production distribution remains CLOSED; only explicit test-only fixture injection is permitted.

The current internal lifecycle freezes canonical Manifest v1 and 89-byte detached signature parsing, Ed25519/key-ID verification, empty production trust, current-policy floors/revocation/denylist, monotonic version/hash decisions, strict safe-home/target/path derivation, two consents, fresh-plan drift rejection, expected+1 artifact reads, exclusive unpredictable temp upload, pre-first-byte chmod/handle/path checks, client readback, no-replace publication, final verification and post-handshake high-water update. The fixture private key exists only in `_test.go`; the sole artifact is `internal/helper/testdata/nonrelease-helper-fixture.txt`.

The fresh OpenSSH process session uses the exact restricted command builder, forces GSS delegation and all ControlMaster settings off, requires the Helper preface at stdout byte zero, concurrently drains/redacts stderr with a 65,536-byte hard cap, and enables bounded heartbeat failure. Protected metadata/high-water persistence, a real SFTP install adapter and binding probe, runtime consent UI, every-exec freshness, disable/remove, and the hostile OpenSSH/native matrix remain pending.

### M4.3 — Helper search

**Status: In Progress.**

Protocol v1 now has strict envelopes and payloads, 1 MiB frames, depth/string/capability/concurrency bounds, independent capability negotiation, request-ID non-reuse, concurrent result/progress/error/complete streams, cancel, operation timeout and nonce heartbeat. The built-in scanner supplies bounded filename/content search without shell interpolation. Daemon routing uses Helper only after independent capability negotiation, preserves the exact Level 0 identity, emits partial results on Helper failure, and never mixes fallback results into the same request. A closed Helper causes the next request to use Level 0 while the Provider snapshot remains healthy.

### M4.4 — Enhanced capabilities and degradation closure

**Status: Not Started.**

## Command ledger

| Candidate | Command | Result |
|---|---|---|
| exact Stage 3 merge baseline | `make docs-check` | PASS |
| exact Stage 3 merge baseline | `make check` | PASS |
| M4.1 working candidate | `go test ./internal/app ./internal/tui ./internal/search ./internal/ipc ./internal/daemon -count=1` | PASS |
| M4.1 lifecycle | `go test ./internal/daemon -run '^TestProviderSession(StreamsBoundedLevel0(Content\|Filename)SearchPages\|RejectsDuplicateAndUnknownSearchCursors\|CancelDrainsToCanceledTerminal\|CloseCancelsAndDrainsFilenameSearch)$' -count=1 -v` | PASS |
| M4.1 scale | `go test ./internal/search -run '^TestMillionNode' -count=1 -v` | PASS; first result 182.292 µs, one List, max page 128 |
| M4.1 working candidate | `make docs-check && make check` | PASS; full unit/provider contract/docs/tidy/verify gate |
| M4.1 real SFTP | `AMSFTP_REAL_SSHD=1 go test ./internal/integration -run TestRealSSHDLevel0Search -count=1 -v` | PASS |
| M4.1 focused race | `go test -race ./internal/search ./internal/daemon -count=1` | PASS |
| M4.2/M4.3 focused packages | `go test ./internal/app ./internal/helper ./internal/search ./internal/daemon ./internal/tui ./internal/integration -count=1` | PASS |
| Helper/client race repetition | `go test -race ./internal/helper -run 'TestHelperClient(Heartbeat\|Handshake\|Protocol)' -count=10` | PASS |
| Helper route race | `go test -race ./internal/helper ./internal/daemon ./internal/search -count=1` | PASS |

## Pending final gates

All Stage 4 local/current-oldstable, native/Hosted, temporary-sshd, protocol, lifecycle, million-node resource, fault, security/pollution, independent review and cold-start gates remain pending. Feature Matrix rows remain Planned until implementation and focused evidence exist, and cannot become Verified before the exact final candidate passes every required gate.
