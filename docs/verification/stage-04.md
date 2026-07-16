# Stage 4 Verification Record

- **Status**: In Progress — M4.1 Level 0 implementation green; native/resource closeout pending
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

**Status: In Progress.**

The first implementation action was the required failing standard-Provider filename-search contract. The current Level 0 slice now includes Provider-only bounded `f` traversal, range-bounded literal `g/`, typed terminal/partial reasons, per-session daemon cursors, raw-byte-safe IPC, exact identity rejection in the reducer, bounded drawers, cancel/drain/session-close ownership, preview/copy/open routing, and app snapshot binding. No Helper probe/install/exec surface is reachable from either route.

The synthetic million-node Provider generates only the requested 128-entry page. `TestMillionNodeFilenameFixtureStreamsFirstResultWithoutMaterializingTree` observed one List call, maximum resident page 128 and first result in under 1 ms on the development host; standalone peak RSS/goroutine/FD and temporary-sshd evidence remain required before M4.1 is Complete.

### M4.2 — Helper install and handshake

**Status: Not Started.** Production distribution remains CLOSED; only explicit test-only fixture injection is permitted.

### M4.3 — Helper search

**Status: Not Started.**

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

## Pending final gates

All Stage 4 local/current-oldstable, native/Hosted, temporary-sshd, protocol, lifecycle, million-node resource, fault, security/pollution, independent review and cold-start gates remain pending. Feature Matrix rows remain Planned until implementation and focused evidence exist, and cannot become Verified before the exact final candidate passes every required gate.
