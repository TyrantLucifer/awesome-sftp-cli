# Stage 6 Execution Plan

- **Status**: In Progress — M6.1 complete; M6.2 independent migration/package/native/preview-channel evidence active; M6.3 shared redaction, doctor, support-bundle, and extension-free SFTP v3 compatibility evidence active
- **Updated**: 2026-07-19
- **Sole baseline**: commit `312bcccbcbd54246bbe5ff9babf4f14560449176`, tree `e0316c286ce11512cb0b92c917fa29b80f9e3305`
- **Fixed branch**: `codex/stage6-hardening-release`
- **Delivery PR**: Draft PR [#6](https://github.com/TyrantLucifer/awsome-sftp-cli/pull/6), title `feat: ship AMSFTP 1.0.0`, base `main`
- **Authoritative scope**: [Stage 6 specification](06-hardening-release.md), [ADR-0009](../architecture/adr/0009-supported-platform-ci-and-packaging-baseline.md), [ADR-0010](../architecture/adr/0010-helper-artifact-trust-and-distribution.md), [ADR-0017](../architecture/adr/0017-stage5-unified-routing-direct-transfer-and-resource-budgets.md), and the frozen feature rows below

This is an execution plan, not release evidence. A row advances from `Planned` only after its first failing contract is recorded, and reaches `Verified` only after its focused, milestone, exact-candidate, platform, documentation, and Hosted gates are green. Milestone ownership remains M6.1 → M6.2 → M6.3 → M6.4; when an earlier milestone's remaining gates require owner/external release authority or final protected bytes, later independent non-release work may proceed without falsely closing the earlier milestone.

## Non-negotiable execution rules

1. Use test-first delivery for every behavior change: add the smallest contract that fails for the missing behavior, record the failure, implement the minimum safe behavior, then run focused race/negative tests and the milestone gate.
2. Preserve the Planner → durable Job → Worker mutation boundary, Level 0 SFTP fallback, system OpenSSH authority, owner-private local state, explicit destructive confirmation, and fail-closed version/trust behavior.
3. Do not add production Helper or Level 2 credentials, fixture trust, test backends, generic remote command surfaces, Agent forwarding, ticket/key copying, relaxed host-key policy, or release-only secrets to the repository.
4. Production Helper and production Level 2 remain **CLOSED** until final release bytes exist, Developer ID/notarization gates are accepted, final binary identity is frozen, the Helper manifest is signed offline under real custody, and every manifest-to-tar binding check passes.
5. A CI failure is a code failure unless exact-SHA companion evidence, logs, platform identity, and a no-change rerun prove an infrastructure or known-fixture cause. Never weaken assertions, security defaults, or timeouts to obtain green status.
6. A full physical 100 GiB LocalFS/SFTP run and a process/network-isolated Level 2 data plane are release gates. Stage 5 synthetic/sparse decomposition and same-process data fixtures are not substitutes.
7. No release tag, non-Draft transition, merge, GitHub Release, Homebrew update, production manifest, or support claim is allowed before every M6.4 gate and all 12 exit criteria are green on the same exact candidate.

## Evidence discipline

For each RED/GREEN cycle, append to [Stage 6 verification](../verification/stage-06.md): exact candidate/tree, literal command, toolchain/environment, intended RED reason or GREEN result, and affected feature/exit IDs. For platform or Hosted failures also record run/job URL, runner image, log excerpt location, same-SHA companion status, rerun result, and classification. A commit cannot contain its own SHA; final identity is bound after the last documentation commit by Git, PR, Actions, tag, release, and channel metadata.

## M6.1 — Configuration, keymap, and public-interface freeze

**Features owned**: VIM-013, VIM-014, REL-001, REL-002, REL-011; begins WORK-006 and REL-003 compatibility inventory.

### RED contracts

1. Add configuration contract tests under `internal/config` and application-loading tests under `internal/app` for schema version, complete defaults, system/user/workspace/environment/CLI precedence, unknown/deprecated fields, typed field/file diagnostics, redacted effective output, and safe-only reload. Job-semantic settings must remain frozen in existing Plans.
2. Add keymap contract tests under `internal/tui` for a documented default snapshot, context-local remapping, duplicate/unreachable/reserved/dangerous binding rejection, export/reset, count/dot/Visual boundaries, and explicit absence of macros/named registers.
3. Add CLI contract tests under `internal/app` and `cmd/amsftp` for stable commands, launch semantics, exit-code classes, stdout/stderr separation, versioned JSON, destructive confirmation policy, `--help`, completion generation, and man-page parity.
4. Freeze an inventory test for configuration, workspace, IPC, database, cache, Helper manifest/protocol, daemon, and CLI versions before changing any compatibility boundary.

### GREEN implementation slices

1. Expand `internal/config` into the single versioned schema/default/validation/redaction source and make `internal/app` merge documented layers deterministically. Expose validate and redacted effective-config commands without secret expansion or remote activity.
2. Introduce a typed keymap registry consumed by the reducer rather than a second action path. Keep the existing Vim-first defaults byte-for-byte represented by a golden contract; remapping changes dispatch only, not confirmation or mutation semantics.
3. Introduce a typed CLI command/output/error contract. Generate help, completions, and `docs/man/amsftp.1` from the same command facts or reject drift in `docs-check`.
4. Publish configuration, keymap, CLI/machine-output, and compatibility references. Advance owned feature rows only after focused tests, race where applicable, docs-check, and current/oldstable gates pass.

### M6.1 gate

- All user-visible defaults and versioned public interfaces are enumerated; unknown ownership is zero.
- Focused config/app/TUI/IPC/buildinfo tests, focused race, `make docs-check`, `make check`, `make lint`, current Go `make ci`, and exact Go 1.25.12 `make check` pass.
- VIM-013, VIM-014, REL-001, REL-002, and the M6.1 portion of REL-011 have direct evidence; migration formats have immutable historical fixture inventory before M6.2 code changes.

## M6.2 — Migration, packaging, and clean machines

**Features owned**: WORK-006, JOB-010, HELP-013, PLAT-003, REL-003, REL-004, REL-005, REL-012; packaging portion of SEC-014.

### RED contracts

1. Check in immutable real historical fixtures for each supported workspace/config/database/cache/Helper source format. Add migration tests for no/single/multiple pending attempts, WAL-safe backup identity, space admission, crash points, explicit resume, read-only diagnosis, verified restore hold, retention, newer-schema refusal, and rollback safety.
2. Add Job-history retention tests proving audit/recovery references prevent cleanup. Add Helper parallel-install/handshake/switch/rollback/remove tests proving active old Jobs remain valid and Level 0 never regresses.
3. Add release-pipeline tests for exact four archive names, deterministic contents, version metadata, LICENSE/NOTICE/install/uninstall material, checksums, SPDX SBOM, provenance/attestation inputs, and daemon service IDs.
4. Add negative macOS ordering tests: pre-sign or pre-`Accepted` bytes cannot enter a production manifest; final manifest/tar/Accepted-ZIP identity must match. Add Linux final-unsigned-byte binding tests.
5. Add clean-machine install, first-run, daemon lifecycle, SSH smoke, supported upgrade, failed-upgrade recovery, uninstall, Homebrew formula, quarantine, codesign, Gatekeeper, and version-smoke harnesses.

### GREEN implementation slices

1. Implement recoverable, version-gated migrations through existing workspace/state/cache/Helper ownership boundaries. Never raw-copy a live SQLite/WAL set, overwrite an unverified destination, silently clear damage, or let an older binary write a newer format.
2. Implement bounded Job retention and Helper lifecycle cleanup with exact live-reference checks and explicit uninstall.
3. Add release scripts/workflows that build immutable darwin/linux × arm64/amd64 archives, emit checksums/SBOM/provenance inputs, and enforce ADR-0009 identifiers and byte-binding order. PR builds exercise public deterministic paths only; protected credentials are unavailable there by design.
4. Build clean-machine automation for supported macOS 15 and Linux matrices. Record native execution separately from cross-build evidence.

**Current checkpoint**: deterministic packaging/dependency/NOTICE closure, installed lifecycle, public Helper surface, release source, owner composition, four-platform native lifecycle, preview-archive picker, and pinned-cache recovery have exact-SHA evidence while production trust remains empty. Homebrew preview SHA `ed7dcd46dab669b929a483767a97890776caf4b9` passes exact current/oldstable local gates and Hosted macOS ARM/Intel local-tap install 0.9→upgrade 1.0→version→formula test→forced all-version uninstall→untap. This is CI-only loopback pre-publication equivalence with a harness-only BSD-3-Clause placeholder, not the owner-selected project LICENSE, a published formula, final protected bytes, or release evidence. Independent work may proceed to M6.3; real public-channel execution, LICENSE selection, signing/notary, protected production Helper and Level 2 remain open.

**Latest increment**: implementation SHA `ed7dcd46dab669b929a483767a97890776caf4b9`, tree `be1ccc50a0986add61890f799bb6fbb2c4d0a76d`, passed exact current Go 1.26.5 `make ci` and exact oldstable Go 1.25.12 `make check`. Push [29643786050](https://github.com/TyrantLucifer/awsome-sftp-cli/actions/runs/29643786050) completed the new Homebrew preview lifecycle on Hosted macOS ARM/Intel; PR [29643787252](https://github.com/TyrantLucifer/awsome-sftp-cli/actions/runs/29643787252) completed it on ARM, while Intel hit an existing heartbeat fixture before the step and its same-SHA push companion passed. Residual exact-stderr-cap/readiness/heartbeat failures have same-SHA successful companions and are classified as known timing instability. No rerun or weakening occurred. This closes only the CI-only local-tap preview harness, not the project LICENSE, real public channel, or final release trust.

### M6.2 gate

- Every supported source version upgrades from immutable fixtures; every interruption either resumes explicitly or preserves a verified restore point and read-only diagnostics.
- Four platform archives are deterministic and structurally valid. Public packaging and clean-machine gates pass without developer-machine residue.
- Protected Developer ID/notary/offline-signing results may remain pending only as an explicitly recorded external release blocker; no production claim or production Helper/Level 2 opening follows from public fixtures.

## M6.3 — Security, compatibility, and diagnostics

**Features owned**: SEC-012, SEC-014, OBS-009, OBS-010, PLAT-009, REL-006, REL-007; completes REL-011 compatibility behavior.

### RED contracts

1. Convert the Stage 6 threat list into negative tests and a finding ledger covering hostile filenames/Unicode/control sequences, symlinks/path traversal, TOCTOU, socket peers/replay/frames, Askpass provenance, environment inheritance, external process boundaries, Helper downgrade/tamper, Level 2 identity/auth boundaries, state/backup permissions, and resource exhaustion.
2. Add redaction goldens seeded with unique secrets in usernames, hosts, paths, environment variables, command arguments, Askpass answers, Kerberos material, file contents, Job errors, logs, databases, cache diagnostics, and Helper/protocol errors. Scan both normal logs and exported bundles.
3. Add `doctor` result contracts for configuration, runtime directories, socket/daemon, validated absolute OpenSSH, known-host behavior, database/migration state, cache, Helper compatibility, disk space, and optional endpoint reachability. Each check has a stable code, severity, safe detail, and remediation link.
4. Add support-bundle contracts for deterministic local-only layout, preview/consent, sensitivity labels, bounded logs/events, redacted configuration shape, corruption-tolerant health summaries, no automatic upload, and cleanup.
5. Execute and record native compatibility cases for supported OS/architectures, OpenSSH 8.9p1 and current, Host/ProxyJump/ProxyCommand/Agent/hardware key/Kerberos, SFTP servers/extensions/filesystems, terminals, editors/openers/shells, and client/daemon/Helper version combinations.

### GREEN implementation slices

1. Fix security findings at their owning boundary and add regression tests; accepted non-high risks require an explicit owner, rationale, and user-visible mitigation. Any unresolved high risk blocks release.
2. Implement one structured redaction policy shared by diagnostics, logs, doctor, and support bundles. Keep raw causes daemon-local where already required; exports contain only bounded safe summaries.
3. Implement read-only `doctor` and local support-bundle commands. Neither command performs destructive repair, remote mutation, credential prompting floods, or automatic network upload.
4. Publish the threat model, finding ledger, compatibility matrix, privacy/support-bundle contract, and troubleshooting code map.

**Current checkpoint**: foundation, doctor, support-bundle, semantic hardening, compatibility/troubleshooting, threat model, and initial finding ledger are delivered. Real-auth correction SHA `fd5ebe499331f5697cdc5eb1e238d12e939ee53a` has dual-workflow support-bundle scan evidence after complete OpenSSH/Askpass and externally renewed Kerberos/GSSAPI matrices. OpenSSH-floor/current SHAs `e7733ec23ec1903dbdc41a926db20fe6defcccc7`/`a7f3ddd8a99df973518140de7eae45e295ae6ea2` and Kerberos-binding SHA `14e5ec4e63c70fb93ed93d15adbe7fdf7cce5d5e` pass exact current/oldstable local gates. Dual-workflow jobs record exact `OpenSSH_8.9p1 Ubuntu-3ubuntu0.16`, `OpenSSH_9.6p1 Ubuntu-3ubuntu13.18`, and `Kerberos 5 version 1.20.1`; each captured value is bound into the real harness and rechecked before mutation. REL-007 SHA `cc6559e41572aa21a7fc249fe0086fe4df50fe27` covers all eight required security/fault domains with executable negative evidence, dual-Go local gates, and successful dual-workflow quality/auth jobs; its residual Hosted failures are exact-companion-covered Helper reader timing and external Homebrew API availability. PLAT-009 is `Verified`; REL-006 and REL-007 are `In Progress`. Production-Helper artifact scans and final independent review remain open.

### M6.3 gate

- No unresolved high-risk finding; all other findings have disposition and evidence.
- Doctor/support-bundle secret scans pass, bundle contents are previewable and bounded, and major failure classes are diagnosable without content or credentials.
- Compatibility claims distinguish native-tested, build-only, best-effort, unsupported, and untested states; PLAT-009 records 8.9p1 as a tested floor rather than a string-based startup rejection, and REL-006 records exact MIT Kerberos 5 1.20.1 evidence without broadening untested combinations.

## M6.4 — Release candidate and 1.0

**Features owned**: REL-008, REL-009, REL-010 and final verification of every Stage 6/shared row; closes REL-004/005/007/012/SEC-014.

**Current checkpoint**: REL-009 implementation SHA `19e8ab73d5f90a51815634b9b36112f86711f372`, tree `ceb8e749ac11c873b703d3bee3f3168cb9f5fce2`, has complete local and classified dual-workflow evidence for its linked user/operations lifecycle; independent exact-RC new-user execution remains open. REL-008 SHA `bad687f67789960bac426bd9414cfbcc5b859b49`, tree `bfa026b52e2a55738f023623093347c5ee78d21e`, has five executable contracts and a strict [release-candidate gate record](../release/RC-GATES.md) that binds 17 required local/native/protected gates, candidate push/PR, and post-merge main to exact identities while rejecting synthetic physical/Level 2 evidence and partial Hosted success. Exact dual-Go local gates pass; PR `29657551397` passed 24/24, while push `29657550405` passed quality/auth and has same-SHA companion coverage for two existing Helper timing failures. REL-010 SHA `181f8a8fbf083c5dadf450373981693e82ecdf74`, tree `a05b87fd21a90b84970f835331edfb5148b5b6b5`, adds an explicit final-only audit that freezes the exact 23 Stage 6 rows and 12 exit criteria; exact dual-Go local gates and push `29658450113` plus PR `29658451365` at 24/24 pass, while its truthful current output remains 21 nonterminal rows plus 12 unchecked exits. This is preparation, not final evidence: no RC record exists, no RC is frozen, and no release/channel action is authorized.

**Timing-stability checkpoint**: SHA `60255dc8dc5e4732ae0fe55ac11ca860390bc81c` adds Helper terminal joining, deterministic tail polling, and an initial Stage 2 barrier; SHA `573aeefcaa488d73a7b5186a46ef03f87c1d8416` restores the resident stderr child; SHA `60ff9080812e0e47f720d102671116a6557b9921` guarantees exact boundary writes. Loaded-selection SHA `2f4ed536ca2553e1bd266ed66fd275b664a00690`, tree `7961527cf2273dc55a88f8409b6af989c80c97b8`, routes all four selection-dependent actions through one barrier and passes exact dual-Go local gates, but its push/PR exposed that fixed `READ-ONLY | sort:name` excludes loaded remote capability rows. Final-screen correction SHA `98a3315070f12b97fe43fa1a79dcdfc4e7428701`, tree `6c2e1dda9cb1768d98498458dcbe25adfe684aea`, requires the selected `> filename` and rejects `READ-ONLY | loading`; deterministic local/remote capability screens, the complete local PTY, exact dual-Go local gates, push `29662965862`, and PR `29662967034` all pass, with each Hosted run at 24/24. A retained temporary-sshd stress attempt failed only after its isolated transport stopped accepting sessions and was not rerun. This is pre-RC hardening; it does not create an RC record, close a release gate, or open production Helper/Level 2.

### RC freeze and final gates

1. Freeze one exact RC SHA/tree. Only release-blocking defects may change it; every change creates a new RC and reruns all affected plus global gates.
2. Run current Go 1.26.5 `make ci`, exact Go 1.25.12 `make check`, full race/fuzz/fault/native SSH/Kerberos, scale/benchmark/reproducibility, clean-machine, migration, compatibility, security, secret/pollution, and documentation gates on the exact RC.
3. Run the true process/network-isolated Level 2 data plane with two independent remote processes and network boundary; prove daemon-side content bytes remain absent, credentials are not forwarded/copied, strict host identity remains enabled, durable target checkpoints/restart/cancel/commit work, and safe relay downgrade still obeys frozen policy.
4. Run the complete physical 100 GiB LocalFS/SFTP transfer on the production Worker/Journal/Scheduler, including pause, checksum state, daemon restart resume, configured rate, final SHA-256, commit, cancel/part retention, bounded RSS/FD/goroutine/process counts, and no early final.
5. Run the declared soak duration and record workload, resources, reconnects, Jobs, log growth, and zero race/deadlock/leak acceptance thresholds.
6. Obtain two independent read-only final reviews against the same exact RC: security/correctness and cold-start/truth-chain. Reviewers may not rely on prior conversational context.
7. With protected release credentials, sign darwin binaries with Developer ID Application/hardened runtime/timestamp, obtain notarization `Accepted` for byte-identical ZIPs, freeze the same binaries, offline-sign final Helper manifests under real custody, assemble final tarballs, and cross-check manifest/checksum/SBOM/attestation identities.
8. On clean macOS 15 Intel/ARM machines, apply quarantine to the actual final tarballs, extract, run strict codesign and Gatekeeper assessment, and version-smoke the binaries. Install all four archives through supported native environments; test daemon upgrade/socket cleanup/uninstall and Homebrew from immutable release URLs/hashes.
9. Reconcile every feature-matrix row to `Verified`, `Deferred`, or `Removed` with evidence/decision. Run a new-user cold-start path and a release-withdrawal/rollback rehearsal.

### Delivery sequence after all gates are green

1. Push the exact final candidate and require successful push and PR Hosted matrices. Classify any failures with the evidence rule above.
2. Record final SHA/tree/run IDs externally and in the closeout handoff, mark the Draft PR ready, and merge only that reviewed exact candidate.
3. Require exact-main CI success for the merge SHA, then create immutable tag `v1.0.0` on that SHA.
4. Publish the GitHub Release with the four archives, `checksums.txt`, `sbom.spdx.json`, provenance/attestation, release notes, install/upgrade/uninstall/rollback material, and signed Helper manifests.
5. Update the `amsftp` Homebrew formula to immutable release URLs and hashes; run clean install/upgrade smoke from the channel.
6. Record tag/release/channel identities, support matrix, known issues, rollback owner, and post-release smoke in the verification ledger and truth chain before declaring Stage 6 Complete.

## Feature accountability matrix

| Feature | Milestone | First decisive evidence | Final evidence |
|---|---|---|---|
| WORK-006 | M6.1 inventory, M6.2 delivery | two immutable workspace fixtures, failed migration non-overwrite | clean upgrade/restore/rollback matrix |
| VIM-013 | M6.1 | default snapshot, remap round-trip, conflict/unreachable rejection | TUI regression, docs/help parity |
| VIM-014 | M6.1 | reserved-key and help/schema absence tests | cold-start/keymap audit |
| JOB-010 | M6.2 | retention/reference cleanup contracts | migration, recovery, bounded history gates |
| HELP-013 | M6.2 | parallel version switch/rollback/active-reference tests | final artifact upgrade/uninstall plus Level 0 regression |
| SEC-012 | M6.3 | unique-secret log/export goldens | final RC pollution/redaction audit |
| SEC-014 | M6.2–M6.4 | public dependency/license/SBOM/repro contracts | final signed-byte/provenance/attestation review |
| OBS-009 | M6.3 | bundle schema/preview/redaction/corruption tests | final RC user exercise and secret scan |
| OBS-010 | M6.3 | healthy and multi-fault stable-code snapshots | clean-machine and compatibility runs |
| PLAT-003 | M6.2–M6.4 | deterministic four-target build/version smoke | native final-archive install/start evidence |
| PLAT-009 | M6.3 | 8.9p1/current capability matrix | final documented support/diagnostic behavior |
| REL-001 | M6.1 | full schema/default/precedence/error snapshots | cold-start configuration exercise |
| REL-002 | M6.1 | generated help/man/completion parity | install-channel smoke |
| REL-003 | M6.2 | pending-attempt/WAL/backup/restore crash matrix | final upgrade and rollback rehearsal |
| REL-004 | M6.2–M6.4 | deterministic archive/public packaging negatives | signed/notarized/quarantined final release evidence |
| REL-005 | M6.2–M6.4 | clean first run and supported-version upgrade | channel install/upgrade/uninstall smoke |
| REL-006 | M6.3–M6.4 | exact OpenSSH/Kerberos version-bound native matrix | remaining macOS Kerberos/vendor SFTP/Linux arm64/production Helper runs, exact-RC links and published limits |
| REL-007 | M6.3–M6.4 | threat/finding ledger and negative tests | two final reviews, zero unresolved high risk |
| REL-008 | M6.4 | exact-RC complete local/native matrices | exact push/PR/main Hosted success |
| REL-009 | M6.1–M6.4 | executable docs examples and links | independent new-user path |
| REL-010 | M6.4 | zero Planned/In Progress release audit | final matrix and truth-chain reconciliation |
| REL-011 | M6.1–M6.3 | version inventory and incompatible-pair tests | final compatibility/upgrade order evidence |
| REL-012 | M6.2–M6.4 | newer-state read-only and binary rollback tests | withdrawal rehearsal and published procedure |

## Exit-criterion accountability

| Exit | Owning milestone | Required proof |
|---|---|---|
| E1 public compatibility boundaries | M6.1/M6.3 | config/keymap/CLI/exit/IPC/machine-output documents and tests |
| E2 help/man/completion/config parity | M6.1 | generated or drift-rejected artifacts plus docs-check |
| E3 all supported migrations recover | M6.2 | immutable fixtures, interruption matrix, verified backup/restore |
| E4 platform archives and clean installs | M6.2/M6.4 | four exact artifacts, native clean-machine and channel evidence |
| E5 daemon upgrade/version safety | M6.2/M6.3 | old/new client, socket residue, incompatible-version tests |
| E6 security disposition | M6.3/M6.4 | threat/finding ledger, zero unresolved high risk, final review |
| E7 compatibility matrix | M6.3 | native OpenSSH/Kerberos/SFTP/terminal/Helper records |
| E8 safe doctor/support bundle | M6.3 | fault localization and unique-secret exclusion scans |
| E9 final scale/fault/race/fuzz/soak | M6.4 | exact-RC 50k/1M/physical-100GiB/isolated-Level2/full gates |
| E10 truth-chain consistency | M6.4 | matrix, every Verification, plan, state, tag/release alignment |
| E11 independent new-user path | M6.4 | installation through recovery from formal docs only |
| E12 withdrawal/rollback rehearsal | M6.4 | tested procedure and explicit irreversible-migration boundaries |

## Known protected-environment blocker

The repository currently has no real production Helper offline private key, named offline custodians, completed custody/recovery/rotation ceremony, or accepted Developer ID/notarization evidence for final bytes. These materials must not be invented or committed. Work independent of them continues through public packaging, negative ordering tests, migration, diagnostics, compatibility, security, scale, isolation, and RC preparation. If the same missing external authority remains after all independent work is green, the ledger will identify the exact unmet credentials/ceremony/gates and Stage 6 will remain incomplete.
