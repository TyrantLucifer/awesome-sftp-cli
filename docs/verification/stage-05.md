# Stage 5 Verification Record

- **Status**: In Progress — M5.1 complete; M5.2 Level 2 preflight/control active
- **Updated**: 2026-07-17
- **Repository root**: `/data00/home/tianchao.thatcher/projects/awsome-sftp-cli`
- **Branch**: `codex/stage5-direct-transfer-scale`
- **Sole exact-main baseline**: commit `06415e1e9fe5ffa93999f112b64aee0bd35e5c75`, tree `5fd662757dfc96e8cb8980c4d721151b0fb510c6`
- **Baseline Hosted run**: [29559563378](https://github.com/TyrantLucifer/awsome-sftp-cli/actions/runs/29559563378) — exact main commit, successful
- **Production Helper distribution / production Level 2**: **CLOSED**

Stage 5 adds only route/performance alternatives around the existing Planner→durable Job→Worker mutation path. Fast/direct routes may not create a second conflict, verification, commit, restart, cleanup or source-delete implementation. Test-only Level 2 requires an explicit non-release fixture constructor; ordinary runtime must record `production_distribution_closed` and safely relay.

## Initial safety checkpoint

| Check | Result |
|---|---|
| `git fetch --prune origin` and exact ref audit | PASS: local `main`, `origin/main`, and remote `refs/heads/main` all resolved to `06415e1e9fe5ffa93999f112b64aee0bd35e5c75`; tree `5fd662757dfc96e8cb8980c4d721151b0fb510c6` |
| fixed branch / PR existence audit | PASS: branch absent locally/remotely and PR absent; branch created once from the sole baseline |
| exact-main Hosted run | PASS: run `29559563378` completed `success` at the exact baseline SHA |
| earlier run `29559132294` classification | CI scheduling, not a code failure: superseded/canceled by a later main push; completed quality/oldstable/auth/build/macOS/Ubuntu 22 jobs were green and Ubuntu 24 was canceled during race execution |
| baseline `make docs-check` | PASS in CI-equivalent environment |
| baseline `make check` | PASS in CI-equivalent environment |

The development shell initially lacked the installed Go SDK on `PATH`; `/home/tianchao.thatcher` is a symlink rejected by owner-private ancestor validation; and interactive `umask 077` converted tests' deliberately unsafe 0755/0644 fixtures to safe 0700/0600. These were environment mismatches, not code defects. The final baseline used `/data00/home/tianchao.thatcher/sdk/go1.25.7/bin` on `PATH`, `umask 0022`, external `BUILD_DIR`/`COVERAGE_DIR`, and the workflow-equivalent root-owned `/var/lib/amsftp-tests/1001` fixture root. No production behavior or assertion was weakened.

## Frozen Stage 5 contracts

- [ADR-0017](../architecture/adr/0017-stage5-unified-routing-direct-transfer-and-resource-budgets.md) freezes route evidence v1, route/reason/integrity codes, Level 2 auth/control boundaries, downgrade rules and scheduler/resource ceilings.
- Existing `local`, `sftp_relay`, and `helper_same_host` Plans remain compatible. New candidates are `atomic_rename`, `sftp_server_copy`, and test-only `level2_direct`.
- Integrity policies are `baseline`, `strong`, and `require_strong`; metadata preservation evidence remains separate.
- `FreezeCopy`/mutation planning freezes the evidence before durable Job creation. The Worker cannot silently reroute and remains the sole verify/commit/source-delete owner.
- M5.1's shared route contract, route regression, Plan persistence/restart, and Jobs/Log/TUI evidence gates pass. M5.2 is open, while executable Level 2 remains fixture-only and production distribution remains CLOSED.

## Milestone evidence

### M5.1 — Route unification and same-Endpoint fast paths

**Status: Complete.** Baseline, code-boundary audit, durable plan, accepted ADR and exit gates are complete. The required first implementation action was a shared route contract which failed in all five cases because the frozen Plan had no `route_evidence`. The implementation freezes v1 evidence for atomic rename, same-Endpoint server/Helper candidates, bounded relay and production-closed Level 2; records selected/candidate stable reasons, strong integrity, part/final, downgrade, risk and progress semantics; rejects altered evidence and checkpoint route reasons before I/O; re-freezes derived directory item Plans; round-trips through the durable Job store; and exposes the same evidence through JobView, `job_created`/runtime Log events and the Jobs drawer.

Declared SFTP server-copy requires the same SSH Endpoint, a regular file, explicit versioned `server_copy` capability, an actual structural facet, a frozen 1 TiB hard ceiling and exact source size. Planning performs no write. Execution durably records the exact Job-owned part intent, never lets the facet publish final, independently hashes source and part, and reuses the existing Worker conflict/commit path. Capability-only/facet-only claims remain relay; altered bindings fail before execution; same-size corruption retains part and never publishes final; a lost successful response is adopted only after full verification. A failed server-copy may become relay only after an exact stat proves the frozen part absent, recording `server_copy_failed_part_absent`; unknown part state, context cancellation and deadlines never downgrade. Restart resumes the durable actual relay route without retrying server-copy. Server-copy and relay produce identical conflict outcomes for ask/overwrite/skip/auto-rename and identical public cancellation facts: canceled outcome, zero committed bytes, absent final and retained source. Phase-only server-copy propagates durable cancellation to the in-flight facet within the bounded control poll. CI-equivalent full check/lint and focused race gates pass.

### M5.2 — Level 2 preflight and direct transfer

**Status: In Progress.** A lower-level, typed direct protocol v1 now shares the Helper/Planner contract without creating the pre-existing Helper→transfer import cycle. It freezes request/Job/Endpoint/path correlation, trusted destination SSH alias, 1 TiB ceiling, strong source identity/hash, ten-minute hard deadline, nonce, 1 MiB frame, four-request concurrency, heartbeat, request-context cancellation, target-durable progress and staged-not-committed result semantics. Every one of protocol/capability/network/address/write/temp/space/quota/auth/host-key/user/workspace/data/hash independently returns fail/unknown and selects relay with zero direct staging. Malformed, stale, reordered, weak, untrusted or altered evidence fails closed.

Only same-package `_test.go` constructors can attach the unexported preflight/data facets. An all-pass fixture Plan stages directly between isolated source/target roots, persists target-durable acknowledgements, remotely strong-hashes part and final, and uses the shared Worker commit path while counting zero source/destination Provider content reads in the daemon. Expired evidence is freshly correlated and revalidated before target write; fail/unknown performs zero direct stage. In-flight cancel retains only the exact acknowledged part and never final/source-delete; a lost complete stage response restarts from the exact acknowledged checkpoint without retransmitting bytes; a failure proven before any target write durably records `level2_direct`→`sftp_relay`. Frozen evidence contains no command, argv, password, private-key, ticket, Agent/GSS, known-hosts, ControlMaster or ProxyCommand surface. Production constructors remain CLOSED and relay with `production_distribution_closed`. A real isolated dual-sshd fixture and the remaining fault/equivalence matrix are still pending before M5.2/M5.3 closeout.

### M5.3 — Downgrade, fault and semantic equivalence

**Status: Not Started.**

### M5.4 — Scale, resource budgets and fair scheduling

**Status: Not Started.**

## Stage 5 feature evidence

The 22 Stage 5 rows remain `Planned` until their implementations and focused tests exist. A row moves to `Verified` only with an independent PASS cell here and exact final-candidate evidence.

| Feature ID | Result | Evidence |
|---|---|---|
| CONN-010 | PENDING | Controlled connection reuse and idle recovery not implemented. |
| JOB-009 | PENDING | Job/global bandwidth policy and deterministic token bucket not implemented. |
| DIRECT-002 | IMPLEMENTED | Explicit capability + structural facet gate, immutable binding, exact part staging, independent strong verification, response-loss adoption, corruption isolation, safe absent-part fallback, restart, pre/in-flight cancellation and relay conflict/cancel equivalence pass the complete M5.1 local gate; exact-final Hosted evidence remains required for PASS. |
| DIRECT-004 | IMPLEMENTED | Frozen user/workspace/data/integrity policy gates selection; explicit disablement and production closure select relay with stable reasons. Exact-final Hosted evidence remains required. |
| DIRECT-005 | IMPLEMENTED | All 14 ordered required conditions independently fail/unknown to relay; malformed/stale evidence and expired execution evidence fail closed before direct write. Real dual-sshd closeout remains pending. |
| DIRECT-006 | IMPLEMENTED | Typed control evidence has no credential/command surface and target authority comes only from the frozen Endpoint SSH alias; isolated topology/native audit remains pending. |
| DIRECT-007 | IMPLEMENTED | Direct protocol/preflight/request/result/control limits, source identity/hash, part/final, target alias and expiry are durable and tamper-checked; expired evidence is revalidated. Exact-final evidence remains required. |
| DIRECT-008 | IN PROGRESS | Proven absent-part write-before failure durably downgrades direct→relay; full mid-part/commit uncertainty matrix remains pending. |
| DIRECT-009 | PENDING | Direct-relay golden equivalence not implemented. |
| DIRECT-010 | IMPLEMENTED | JobView, Jobs drawer and `job_created` Log show selected reason/integrity/downgrade/progress; runtime safe fallback durably shows planned→actual route and stable reason. Exact-final Hosted evidence remains required for PASS. |
| SCALE-001 | PENDING | 50k directory production-scale gate not implemented. |
| SCALE-002 | PENDING | Million-tree transfer/plan gate not implemented. |
| SCALE-003 | PENDING | 100GB sparse local/relay/direct gate not implemented. |
| SCALE-005 | PENDING | Unified FD/goroutine/process budget gate not implemented. |
| SCALE-006 | PENDING | Slow-I/O UI/cancel latency gate not implemented. |
| SCALE-007 | PENDING | Connection/bandwidth fairness gate not implemented. |
| SCALE-008 | PENDING | Deep-path non-recursive traversal gate not implemented. |
| SCALE-009 | PENDING | Fixed benchmark environment and first trend record not implemented. |
| SCALE-010 | PENDING | Low-disk relay/cache/part recovery gate not implemented. |
| SCALE-011 | PENDING | Bounded large event/log pagination and truncation gate not implemented. |
| SCALE-012 | PENDING | Low-capability scale-degradation evidence not implemented. |
| SEC-011 | PENDING | Direct authentication non-expansion topology gate not implemented. |

## Command ledger

| Candidate | Command | Result |
|---|---|---|
| exact main baseline | `make docs-check` | PASS in CI-equivalent environment |
| exact main baseline | `make check` | PASS in CI-equivalent environment |
| M5.1 required first RED | `go test ./internal/transfer -run '^TestPlannerFreezesUnifiedRouteEvidenceBeforeDurableJobCreation$' -count=1 -v` | FAIL as intended: all five cases reported `frozen plan has no route_evidence` |
| M5.1 route contract GREEN | same command after minimal implementation | PASS: atomic rename, server-copy-unavailable relay, `helper_same_host`, cross-Endpoint relay and production-closed Level 2 |
| M5.1 evidence tamper RED/GREEN | `go test ./internal/transfer -run '^TestValidateExecutionRejectsTamperedRouteEvidence$' -count=1 -v` | Initial FAIL for six accepted tamper cases; PASS after pre-execution validation |
| M5.1 directory regression | `go test ./internal/transfer -run '^TestWorkerCopiesDirectoryTreeWithBoundedRelayAndNoSymlinkTraversal$' -count=1 -v` | PASS after derived item Plans re-freeze their exact route evidence |
| M5.1 durable/UI evidence | focused durable same-host JobView and Jobs renderer tests | PASS |
| M5.1 focused packages | `go test ./internal/tui ./internal/transfer -count=1` | PASS |
| M5.1 full current gate | `make docs-check && make check` | PASS with external build/coverage directories and CI-equivalent fixture environment |
| M5.1 lint | `make lint` | PASS: golangci-lint reports 0 issues |
| M5.1 server-copy selection RED | `go test ./internal/transfer -run '^TestPlannerSelectsDeclaredServerCopyOnlyWithCapabilityAndFacet$' -count=1 -v` | Initial FAIL: selected `sftp_relay`; PASS after explicit capability/facet binding, with zero planning copy calls. |
| M5.1 server-copy execution RED | `go test ./internal/transfer -run '^TestWorkerStagesServerCopyPartThenVerifiesAndCommits$' -count=1 -v` | Initial FAIL: `unsupported route`; PASS after exact part staging, independent source/part SHA-256 and existing commit. |
| M5.1 server-copy security/fault group | focused capability/facet, version/revision/declaration drift, binding-tamper, response-loss, corrupt-part and pre-stage-cancel tests in `route_contract_test.go` | PASS; ordinary/unknown/inconsistent SFTP remains relay, no corrupt final publication, no pre-cancel write. |
| M5.1 recovery identity RED/GREEN | `go test ./internal/transfer -run '^TestWorkerRejectsCheckpointThatDoesNotMatchFrozenRouteIdentity$' -count=1 -v` | Initial FAIL: foreign checkpoint part was accepted and executed; PASS after shared pre-I/O source/part/final identity validation. |
| M5.1 server-copy transfer regression | `go test ./internal/transfer -count=1` | PASS. |
| M5.1 server-copy lint | `make lint` | PASS: golangci-lint reports 0 issues after replacing dynamic-path fixture I/O with Provider I/O. |
| M5.1 safe fallback RED/GREEN | focused `TestWorkerFallsBackToRelayOnlyAfterServerCopyProvesPartAbsent`, restart and unknown-part tests | Initial RED exposed no fallback contract; PASS after persisting server-copy→relay only when exact part is absent. Unknown/permission state does not downgrade and restart does not retry server-copy. |
| M5.1 conflict/cancel equivalence | `TestServerCopyAndRelayShareCommitConflictContract` and `TestServerCopyAndRelayShareCancellationContract` | PASS for ask/overwrite/skip/auto-rename plus canceled/zero-commit/final-absent/source-retained public state. |
| M5.1 in-flight cancellation RED/GREEN | `TestWorkerServerCopyPropagatesDurableCancelDuringStage` | Initial FAIL after 500 ms because server-copy did not observe durable control; PASS after sharing the bounded staged-copy control monitor. Context cancellation/deadline has a separate regression proving no relay downgrade. |
| M5.1 durable route identity | safe-downgrade Manager/JobView/Log test plus selected-reason tamper test | PASS: `job_created` carries selected route/reason/integrity/boundary/progress, runtime event carries planned/actual/reason, JobView survives restart, and forged checkpoint reason is rejected pre-I/O. |
| M5.1 complete current gate | `make docs-check && make check && make lint` | PASS in CI-equivalent environment; transfer coverage 70.5%, lint 0 issues. |
| M5.1 focused race | `go test -race ./internal/transfer ./internal/tui ./internal/state/jobstore -count=1` | PASS. |
| M5.2 protocol RED/GREEN | `go test ./internal/helper -run '^TestDirectPreflight' -count=1` | Initial compile RED for the typed v1 contract; PASS for strict round-trip, all 14 fail/unknown conditions, freshness, correlation, trust, limits and malformed evidence. |
| M5.2 planner RED/GREEN | `go test ./internal/transfer -run '^TestLevel2' -count=1` | Initial compile RED for policy/binding/backend; PASS for all-pass direct, 28 fail/unknown relay cases, production closure, policy disablement, durable reload and tamper rejection. |
| M5.2 fixture data plane | focused direct fixture, expiry, cancel, lost-response restart and absent-part downgrade tests | PASS: target-durable checkpoints, remote part/final hashes, zero daemon Provider content reads on direct, fresh expiry preflight, no commit/source delete on cancel, exact restart adoption and safe relay fallback. |
| M5.2 focused packages/race/lint | `go test ./internal/directprotocol ./internal/helper ./internal/transfer -count=1`; `go test -race` on the same packages; `make lint` | PASS; lint reports 0 issues. |

## Hosted CI instability classification

Commit `4aacfc8d8cb275b3b0abf70962418c9dd29d9510` predates the complete fallback slice but supplies an exact cross-run instability comparison. [Push run 29562430669](https://github.com/TyrantLucifer/awsome-sftp-cli/actions/runs/29562430669) failed only `native (macos-15-intel)` when the existing `TestDaemonRestartRecoversEveryNonterminalJobStateDeterministically` observed the control socket absent after its startup wait; the same SHA's PR macOS Intel native job passed. [PR run 29562433196](https://github.com/TyrantLucifer/awsome-sftp-cli/actions/runs/29562433196) failed only `oldstable (ubuntu-22.04)` when the existing Helper stderr-overflow process fixture unexpectedly completed below its observed drain boundary; the same SHA's push Ubuntu 22 oldstable job passed. All Stage 5 code tests, quality/auth/build/reproducibility and the opposite companion jobs were green. The failures are mutually non-reproducing existing timing fixtures, not Stage 5 route failures; no unrelated assertion or timeout was changed. The complete M5.1 candidate will receive fresh push/PR Hosted runs and neither old run is accepted as final evidence.

## Pending closeout gates

M5.2–M5.4, all 22 independent feature PASS cells, final current/oldstable/native/platform/filesystem/auth/route/direct/fault/scale/resource/soak/reproducibility/pollution gates, two independent final reviews, exact final push and PR Hosted CI remain pending. The PR remains Draft and may become Ready only after every final gate is green; it must not be merged automatically.
