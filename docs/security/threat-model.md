# AMSFTP 1.0 Threat Model

This model records the security boundaries exercised by the 1.0 implementation and the scope that is still open. It does not convert fixture or build evidence into a production compatibility claim.

## Assets

- Authentication material: private keys, agent handles, Askpass answers, Kerberos tickets, host-key decisions, and credentials inherited by system OpenSSH.
- User data: local and remote names, paths, metadata, file contents, previews, command arguments, and transfer results.
- Persistent state: configuration, workspace documents, SQLite Job state and WAL, cache metadata/content, logs, diagnostics, and support bundles.
- Trust state: known-hosts data, Helper release manifests, public keys, installed Helper metadata, monotonic version state, and capability results.
- Release bytes and evidence: source provenance, deterministic artifacts, checksums, signatures/notarization inputs, package metadata, and exact-SHA gate results.

## Trust boundaries

| Boundary | Trusted inputs | Untrusted or separately validated inputs | Required behavior |
|---|---|---|---|
| Local user process | Explicit CLI/TUI intent and same-user IPC peer after validation | environment, terminal replies, filenames, configuration bytes, filesystem metadata, daemon frames | bound size/time, validate provenance and structure, redact before persistence |
| System OpenSSH | `/usr/bin/ssh` execution and its established authentication semantics | remote banners, prompts, stderr/stdout, connection timing, configuration expansion | preserve system configuration inside fixed safety overrides; never parse credentials from output |
| Remote SSH/SFTP server | authenticated session identity after host-key policy succeeds | names, metadata, file content, extensions, errors, disconnects, capability claims | treat all remote bytes as hostile; capability-gate behavior; retain durable effect state |
| Local daemon | same-user authenticated framed protocol at a private runtime socket | malformed, replayed, oversized, truncated, slow, or version-incompatible messages | reject before dispatch; enforce request identity, replay, size, rate, and deadline bounds |
| Remote Helper | no implicit trust from installation or same account | release bytes, manifest, metadata, envelope, capability claims, downgrade state, output | production distribution stays closed until final-byte trust gates pass; isolate Helper failure from Level 0/relay |
| Release infrastructure | immutable reviewed source and protected release policy | runner state, fetched dependencies, credentials, package channels, generated artifacts | pin inputs, bind signatures/checksums to final bytes, preserve exact-SHA evidence, fail closed on drift |

## Threats and controls

| Threat | Security consequence | Implemented control and evidence boundary |
|---|---|---|
| Hostile filenames, paths, terminal controls, and remote metadata | UI spoofing, argument injection, path escape, log/support leakage | structured arguments, display escaping, root-relative path validation, bounded rendering, semantic diagnostic/export registries |
| Symlink, ownership, mode, mount-type, and time-of-check/time-of-use attacks | state substitution or access outside private roots | descriptor-relative validation, no-follow/open-and-recheck patterns, same-owner/private-mode requirements, network-state-root rejection |
| Malformed, replayed, slow, or oversized IPC | unauthorized operations, denial of service, request confusion | peer identity, versioned envelopes, request IDs, replay cache, frame/queue/deadline/rate bounds, fail-before-dispatch tests |
| Askpass or authentication provenance confusion | credential theft or unintended prompt approval | fixed launcher/provenance validation, inherited system OpenSSH semantics, no answer persistence, corpus scans over logs and support archives |
| Environment and external-process injection | attacker-controlled executable, flags, or secret propagation | fixed executable policy, allowlisted environment, structured argv, bounded output, semantic persistence policy |
| Helper tamper, downgrade, partial install, or capability spoofing | remote-code execution or unsafe fast-path selection | signed-manifest design, digest/metadata checks, atomic parallel install state, monotonic high-water, protocol/capability validation; production distribution remains closed |
| Level 2 peer or route identity confusion | direct data-plane exposure or transfer to the wrong peer | identity-bound preflight, capability checks, bounded relay fallback; production Level 2 remains closed pending isolated-network proof |
| Corrupt configuration, database, cache, or daemon response | unsafe recovery, data loss, or secret disclosure | read-only doctor/support gather, stable-code summaries, byte-preservation tests, no automatic destructive repair |
| Disk, memory, output, queue, or concurrency exhaustion | denial of service or partial mutation | deterministic byte/item/time/concurrency quotas, durable Job effects, ENOSPC and overload coverage, backpressure |
| Support export containing safe-shaped secrets | credential or user-content disclosure despite lexical validation | reviewed field registries/allowlists, redaction placeholders, preview plus exact consent, private atomic publication, expanded-archive eight-class corpus tests |
| Dependency or release-pipeline compromise | shipped bytes differ from reviewed source | dependency/license/NOTICE gates, locked workflows, deterministic builds and archives, exact-SHA Hosted evidence; final public-channel signing remains open |

## Residual and open scope

- Production Helper distribution is **CLOSED**: protected signing/notarization credentials, offline key ceremony, final-byte verification, and actual production artifact scanning have not run.
- Production Level 2 is **CLOSED**: the real isolated-network identity, authentication, containment, integrity, cancellation, and fallback matrix remains open; bounded relay stays the default.
- Real hardware security-key authentication, macOS Kerberos, non-OpenSSH SFTP vendor behavior, and a physical 100 GiB run do not yet have native evidence.
- Actual authentication-artifact and production Helper artifact secret scans remain open. Current safe-shaped corpus coverage is a code-boundary proof, not a statement about bytes that do not yet exist.
- Independent correctness/security review, release finding disposition, project LICENSE owner/legal choice, and final protected public-channel evidence remain release work.

## Verification ownership

| Owner | Verification responsibility |
|---|---|
| `internal/platform`, `internal/statefs`, `internal/cachefs` | local identity, private roots, stable-open validation, permissions, filesystem policy, retention |
| `internal/ssh`, `internal/askpass`, authentication integrations | system OpenSSH execution, prompt provenance, agent/Kerberos/host-key boundaries |
| `internal/ipc`, `internal/daemon` | framed protocol, peer/request identity, replay, version, rate, queue, size, and deadline enforcement |
| `internal/helper`, `internal/transfer` | Helper trust lifecycle, envelope/capability validation, downgrade resistance, route planning, relay fallback, durable effects |
| `internal/diagnostic`, `internal/supportbundle`, `internal/doctor` | semantic persistence policy, redaction, bounded read-only probes, preview/consent/private publication |
| release workflow and maintainers | dependency/provenance policy, deterministic artifacts, protected credentials, final-byte signatures, exact-SHA Hosted evidence, review and ledger closure |
