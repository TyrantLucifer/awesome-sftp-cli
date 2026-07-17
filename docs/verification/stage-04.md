# Stage 4 Verification Record

- **Status**: In Progress — M4.1 Complete; M4.2–M4.4 focused implementation complete, final candidate gates pending
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

The fresh OpenSSH process session uses the exact restricted command builder, forces GSS delegation and all ControlMaster settings off, requires the Helper preface at stdout byte zero, concurrently drains/redacts stderr with an exact 65,536-byte accepted boundary, rejects byte 65,537, and kills its OpenSSH process group on explicit close, heartbeat failure, hard request deadline, or protocol failure.

Exact raw signed metadata is now durably staged before probe in a 0700/0600 content-addressed store. Its atomic index separates the enabled bit from persistent Endpoint/protocol/target version/hash high-water, fails closed on missing/corrupt/symlink metadata, caps records and metadata files at 4,096, and preserves high-water across disable/remove and restart. `PrepareEnable` reloads exact bytes and repeats current policy, fresh binding/target/namespace/ancestor/final attributes and full remote hash before validating exact protocol/version/independent capabilities.

The `pkg/sftp` adapter requires raw UID/mode attributes, exact 0600 handle operations, exclusive create, readback and exact removal. It rejects ordinary create-then-chmod `Mkdir` because that exposes an umask-dependent permission window, and requires a construction-time raw-MKDIR primitive whose Stage 4 fixture creates with exact `0700`; the production packet implementation remains intentionally absent while distribution is CLOSED. Testing proved a nominal SFTP v3 server may replace on rename, so publication deliberately requires OpenSSH `hardlink@openssh.com` target-exists-fails and refuses servers without it; it never uses replacement rename or delete-first. Utility attributes are checked before and after probe, the formal executable must match the full content-addressed grammar, and exact removal uses the Job Store's admission/removal lease to scan exact artifact IDs in non-terminal durable plans. [ADR-0016](../architecture/adr/0016-stage4-search-helper-runtime-contracts.md) records these fail-closed decisions. Final hostile OpenSSH/native/pollution gates remain pending.

### M4.3 — Helper search

**Status: In Progress.**

Protocol v1 now has the ADR-0010-frozen `amsftp-helper-wire-v1` byte-zero preface, strict envelopes and payloads, 1 MiB frames, depth/string/capability/concurrency bounds, independent capability negotiation, permanent request-ID non-reuse including rejected requests, concurrent result/progress/error/complete streams, cancel, operation timeout and mandatory nonce heartbeat. Server and client use payload-byte accounting, release a concurrency slot before exposing `complete`, and independently cap every request at ten minutes, 100,000 results and 64 MiB. The built-in scanner supplies bounded filename/content search without shell interpolation. Daemon routing uses Helper only after independent capability negotiation, preserves the exact Level 0 identity, reports canceled contexts (including an empty closed Helper stream) as canceled before Provider snapshot validation, emits partial results on Helper failure, and never mixes fallback results into the same request. A closed Helper causes the next request to use Level 0 while the Provider snapshot remains healthy.

The million-node Helper synthetic walk traverses one million generated entries without a retained tree, streams 100 results, reports its first result in about 0.29 ms and observed about 3.5 MiB peak allocation delta on the development host. Protocol seed fuzz tests exercise envelope, manifest, and signature parsers.

### M4.4 — Enhanced capabilities and degradation closure

**Status: In Progress.** Focused implementation is complete; final cross-platform and fault gates remain pending.

`strong_hash` returns SHA-256, file identity and compute time, and invalidates a mid-read change. `disk_stats` reports statfs total/available with quota explicitly unknown. Tail reports truncate/rotation and byte/time limits; watch is explicitly loss-possible, coalesced, and refresh-required.

Planner selects `helper_same_host` only for a regular-file copy within one SSH Endpoint after independent `strong_hash` and `same_host_copy` negotiation. The concrete Helper session is bound to that exact EndpointID, and Helper size/mtime at Provider precision/available file ID/existing SHA-256 must agree with the frozen Provider FileRef. The frozen Plan carries exact Endpoint, protocol, Helper version, OS/architecture/artifact SHA, capability version and source SHA-256/size/identity. Manager holds the shared Job Store admission lease from before Helper preparation through durable creation; exact removal takes the same coordinator and scans every non-terminal durable plan. Helper may create only the standard `.part-<JobID>` location. The existing Worker then verifies, applies conflict policy, commits, checkpoints and adopts an exact response-lost part after restart. Durable Manager, overwrite, cancel propagation, route visibility and fallback-to-relay-at-plan-time tests are green. The TUI refreshes each ready SSH pane's dynamic Helper status through a one-at-a-time one-second snapshot loop and rejects stale Endpoint/session/generation actions. The production Manager still receives no fixture backend while distribution is CLOSED.

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
| durable state/fresh enable/SFTP adapter | `go test ./internal/helper -run 'Test(StateStore\|PrepareEnable\|SFTPInstallRemote\|OpenSSHBindingProbe)' -count=1` | PASS |
| same-host Planner/Job path | `go test ./internal/transfer -run 'Test(PlannerSelectsSameHost\|WorkerStagesSameHost\|WorkerAdoptsExact\|SameHostRoute\|ManagerPersistsAndExecutesSameHost\|SameHostWorkerPropagates)' -count=1 -v` | PASS |
| Helper million fixture | `go test ./internal/helper -run TestMillionNodeHelper -count=1 -v` | PASS; first result 290.583 µs, peak allocation delta 3,457,368 bytes, 100 streamed results |
| current focused packages | `go test ./internal/helper ./internal/transfer ./internal/daemon ./internal/tui ./internal/app ./internal/search -count=1` | PASS |
| current focused vet | `go vet ./internal/helper ./internal/transfer ./internal/daemon ./internal/tui ./internal/app ./internal/search` | PASS |
| current focused race | `go test -race ./internal/helper ./internal/transfer ./internal/daemon ./internal/tui ./internal/search -count=1` | PASS |
| independent-review fixes | `go test ./... -count=1 -timeout=10m` | PASS |
| independent-review focused race | `go test -race ./internal/helper ./internal/transfer ./internal/daemon ./internal/tui ./internal/app ./internal/search -count=1 -timeout=10m` | PASS |
| Helper completion ordering stress | `go test ./internal/helper -run '^TestHelperClientMayStartNextRequestImmediatelyAfterCompleteAtConcurrencyOne$' -count=100 -timeout=60s` | PASS |
| real temporary-sshd Level 0 search | `AMSFTP_REAL_SSHD=1 go test ./internal/integration -run '^TestRealSSHDLevel0Search$' -count=1 -v -timeout=2m` | PASS: the named test ran and completed in 0.43 s |
| current docs/lint/diff | `make docs-check && git diff --check && make lint` | PASS; golangci-lint reports 0 issues |
| final independent-review fixes | removal/admission, process fatal hook, request-ID rejection reuse, unified output budget, empty cancel, FileID/hash negative tests | PASS; independent re-review reports no remaining blockers |
| removal/admission repetition | `go test ./internal/state/jobstore -run '^TestHelperRemovalLeaseRejectsPinnedArtifactAndExcludesNewJobAdmission$' -count=20` and Manager prepare→create test `-count=20` | PASS |
| protocol/process repetition | focused request-ID/output-budget/heartbeat/hard-deadline tests `-count=20` | PASS |
| final focused race | `go test -race ./internal/state/jobstore ./internal/transfer ./internal/helper ./internal/daemon -count=1 -timeout=7m` | PASS |
| Go 1.25.12 oldstable | `GOTOOLCHAIN=go1.25.12 BUILD_DIR=/tmp/... COVERAGE_DIR=/tmp/... make check` | PASS after responsive-heartbeat shutdown repetition `-count=100` |
| final cold-start/documentation review | Feature Matrix, production CLOSED, focused Helper/removal/transfer tests, docs check | PASS; no remaining blockers |
| final current gate | `BUILD_DIR=/tmp/... COVERAGE_DIR=/tmp/... make ci` | PASS; unit/contract/docs/tidy/verify/lint/full race/fuzz/vulnerability/workflow/four-target build |
| final reproducibility | two independent `-trimpath -buildvcs=false` builds for darwin/linux × arm64/amd64, compared byte-for-byte | PASS |
| final real SFTP search | `AMSFTP_REAL_SSHD=1 go test ./internal/integration -run '^TestRealSSHDLevel0Search$' -count=1 -v -timeout=2m` | PASS; named test ran in 0.53 s |
| final hostile process fixture | focused restricted argv/binding probe/process session/production-trust tests | PASS |
| final production pollution | production binary string scan plus tracked `dist/**`/`coverage/**` scan | PASS; no fixture key/artifact/child-mode marker or tracked generated output |
| implementation candidate `145b50ae871aa91f8acc0505d2b6b9bd19bae742` | Hosted push run [29557260133](https://github.com/TyrantLucifer/awsome-sftp-cli/actions/runs/29557260133) and PR run [29557261524](https://github.com/TyrantLucifer/awsome-sftp-cli/actions/runs/29557261524) | FAIL: Linux correctly rejected security-sensitive test fixtures rooted below world-writable `/tmp`; production path validation remained fail-closed |
| trusted-root fixture repair | affected Helper tests, focused race, and `make check` | PASS: executable and persistent-state fixtures now use the CI-provisioned `testkit.PersistentTempDir`; production code is unchanged |
| repair candidate `5de255740f9cb1a600648e5ab0181615468c66d2` | Hosted push run [29557617101](https://github.com/TyrantLucifer/awsome-sftp-cli/actions/runs/29557617101) and PR run [29557619380](https://github.com/TyrantLucifer/awsome-sftp-cli/actions/runs/29557619380) quality jobs | FAIL: Linux-only lint identified an unchecked signed `statfs` block-size conversion and a cross-platform test conversion; the repaired Linux native Helper tests passed |
| Linux lint repair | native Linux package analysis and focused disk-stats/SFTP adapter tests | PASS: non-positive block sizes fail closed before conversion, and the intentional platform-width test conversion is documented |

## Pending final gates

Independent security/correctness and cold-start/documentation re-reviews report no remaining blockers. Exact current/oldstable, real temporary-sshd, hostile process fixture, reproducibility and production-pollution gates are green. Native/Hosted exact-SHA jobs, including the repository's full OpenSSH/auth/provenance matrix, remain pending. Focused implementation rows are `Implemented`; none may become `Verified` before the exact final candidate passes its required gates.
