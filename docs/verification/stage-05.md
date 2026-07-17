# Stage 5 Verification Record

- **Status**: In Progress — M5.1 route unification
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
- M5.2 is closed until the M5.1 shared route contract, route regression, Plan persistence/restart, and Jobs/Log/TUI evidence gates pass.

## Milestone evidence

### M5.1 — Route unification and same-Endpoint fast paths

**Status: In Progress.** Baseline, code-boundary audit, durable plan, verification skeleton and accepted ADR are complete. The required first implementation action was a shared route contract which failed in all five cases because the frozen Plan had no `route_evidence`. The minimal implementation now freezes v1 evidence for atomic rename, same-Endpoint server/Helper candidates, bounded relay and production-closed Level 2; records selected/candidate stable reasons, strong integrity, part/final, downgrade, risk and progress semantics; rejects altered evidence before execution; re-freezes derived directory item Plans; round-trips through the durable Job store; and exposes the same evidence through JobView and the Jobs drawer. Existing Worker bytes/conflict/verify/commit/source-delete behavior is unchanged. Declared SFTP server-copy execution, the complete decision table and final restart/UI regressions remain pending.

### M5.2 — Level 2 preflight and direct transfer

**Status: Not Started.** Production distribution remains CLOSED.

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
| DIRECT-002 | PENDING | Declared SFTP server-copy route and fault contract not implemented. |
| DIRECT-004 | PENDING | Explicit direct policy and route disclosure not implemented. |
| DIRECT-005 | PENDING | Per-condition Level 2 preflight matrix not implemented. |
| DIRECT-006 | PENDING | No-forwarding/delegation/copy integration evidence not implemented. |
| DIRECT-007 | IN PROGRESS | Route evidence v1 is durable and tamper-checked before execution; full Level 2 preflight/expiry evidence remains pending. |
| DIRECT-008 | PENDING | Safe pre-commit downgrade matrix not implemented. |
| DIRECT-009 | PENDING | Direct-relay golden equivalence not implemented. |
| DIRECT-010 | IN PROGRESS | JobView and Jobs drawer show stable selected reason/integrity/downgrade, including `production_distribution_closed`; Log/runtime downgrade matrix remains pending. |
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

## Pending closeout gates

M5.1–M5.4, all 22 independent feature PASS cells, current/oldstable/native/platform/filesystem/auth/route/direct/fault/scale/resource/soak/reproducibility/pollution gates, two independent final reviews, exact final push and PR Hosted CI remain pending. The PR must remain Draft until M5.1 reviewable MVP exists and may become Ready only after every final gate is green; it must not be merged automatically.
