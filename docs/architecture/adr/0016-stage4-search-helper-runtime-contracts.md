# ADR-0016: Stage 4 Search 与可选 Helper 运行时契约

- **Status**: Accepted
- **Date**: 2026-07-17
- **Decision owners**: Stage 4 implementation

## Context

Stage 4 must add recursive filename/content search and optional remote acceleration without weakening the standard SFTP baseline, durable mutation path, OpenSSH authentication boundary, or production Helper custody gate. Search results arrive asynchronously and may outlive a pane generation. Helper installation crosses signed-artifact, SFTP namespace, login-shell, remote process, and durable state boundaries. Same-host copy is a mutation optimization and therefore cannot become a second commit path.

SFTP v3 `SSH_FXP_RENAME` is also not a dependable no-replace primitive across servers: the in-process compatibility server used by the project replaces an existing destination. Pre-stat plus rename would retain a race. OpenSSH's `hardlink@openssh.com` extension has the required target-exists-fails property for an immutable regular Helper artifact.

## Decision

1. Level 0 filename and content search use only the existing Provider. Every request and event freezes EndpointID, Provider SessionID, capability generation, RequestID, scope, options, and hard budgets. A mismatch terminates that generation; late events never enter a newer drawer.
2. Filename and content search share bounded streaming semantics: fixed page/queue/depth/file/result/output/read/time limits, explicit `complete`/`partial_results`/`canceled`, and a concrete stop reason. Content search remains an explicitly confirmed slow SFTP range-read mode when no Helper is available.
3. Helper protocol v1 starts at stdout byte zero with the ADR-0010-frozen `amsftp-helper-wire-v1\n`, then uses 4-byte framed strict JSON. The hard limits are 1 MiB/frame, JSON depth 8, string 4,096 bytes, four concurrent requests, 100,000 results, 64 MiB output, and ten minutes/request. Request IDs cannot be reused. User paths and patterns appear only in framed stdin.
4. Helper is a non-root, non-listening, non-resident SSH stdio child. Binding and formal sessions use fresh validated absolute OpenSSH argv with GSS delegation and ControlMaster/Path/Persist disabled. Explicit cancellation, heartbeat failure, hard request timeout, and protocol failure cancel the formal session and kill the local OpenSSH process group; Helper failure never closes the independent SFTP Provider.
5. Exact raw manifest and detached-signature bytes are stored in an owner-private content-addressed file before probe. An atomic owner-private index retains per Endpoint/protocol/target high-water and a separate enabled bit. Disable and exact remove clear only the enabled bit; downgrade and same-version republish protection remain. Metadata and index records are capped at 4,096 each.
6. Every enable starts by reloading those exact bytes and repeats current signature/revoke/deny/floor policy, fresh binding/target/namespace/ancestor/final attributes, persistent high-water, and the complete remote hash. Exact protocol, Helper version, and each required capability are then bound to the framed handshake.
7. Helper artifact publication requires an exclusive random temporary file, exact pre-write `0600`, bounded write/readback hash, final `0700`, and a target-exists-fails operation. The implementation requires `hardlink@openssh.com`, links temp to the immutable final, then removes temp. If the extension is absent it fails closed; it never falls back to a potentially replacing rename or delete-first sequence.
8. Same-host copy is eligible only for a regular-file copy whose source and destination have the same SSH EndpointID and whose independently negotiated `strong_hash` and `same_host_copy` capabilities succeed. Planner freezes protocol/version/capability plus source digest/identity. Helper may create only the exact Stage 2 `.part-<JobID>` path. Worker alone verifies the part, applies conflict policy, commits/renames, checkpoints, and recovers after restart. A frozen Helper route does not silently switch to relay mid-request; a later newly planned request may choose Level 0.
9. `strong_hash`, `disk_stats`, `tail`, `watch`, and `same_host_copy` are independent capabilities. Quota remains unknown unless measured; watch events are coalesced/loss-possible hints requiring Provider refresh; hash changes invalidate the result.
10. Production Helper distribution remains **CLOSED**. `NewProductionVerifier` trusts no fixture key, the ordinary daemon creates no fixture install backend, and Stage 4 publishes no production Helper artifact, manifest, signature, or custody claim. Only same-package/test-only explicit fixture injection exercises the lifecycle.
11. Utility ownership/mode is checked through SFTP before and after every binding probe. The concrete `pkg/sftp` adapter refuses ordinary `Mkdir`, whose packet omits mode, and fails closed unless its construction site supplies a separately reviewed raw `SSH_FXP_MKDIR` primitive carrying exact `0700`; a post-create lstat is still mandatory. Stage 4 tests prove this adapter boundary with a raw-primitive fixture, not a production packet implementation; production distribution remains CLOSED.
12. A formal Helper executable path must match the complete content-addressed template, not merely be absolute/printable. Client and server account output in payload bytes and independently enforce the ten-minute, 100,000-result and 64-MiB limits. Every syntactically valid request ID becomes permanently seen before capability/concurrency rejection, and both sides release concurrency slots before exposing `complete`. Formal process sessions always enable a bounded heartbeat.
13. A same-host backend binds one exact EndpointID and persists the exact protocol/version/OS/architecture/artifact SHA in the frozen Plan. Planner accepts the binding only when Helper size, mtime at Provider precision, available file ID, and any pre-existing SHA-256 all match the frozen Provider FileRef. The Job Store serializes new Job admission with removal, scans every non-terminal durable `helper_same_host` plan, and rejects removal while an exact artifact is pinned.

## Consequences

- Level 0 search, browsing, transfer, Preview/Edit/Cache, and unrelated Jobs remain available when Helper is absent, disabled, incompatible, killed, or malformed.
- The no-replace install route is unavailable on servers without the OpenSSH hardlink extension; this is an explicit safety failure, not a transparent downgrade.
- Same-host copy can reduce client data relay but never bypasses the durable Job or existing conflict/commit postconditions.
- Stage 5 may add Level 2 direct routing, but cannot reinterpret Stage 4's Level 1 capability or persistence records without a new ADR/migration.

## Evidence

Stable implementation evidence is maintained by the [search contracts](../../../internal/search/content_contract_test.go), [Helper protocol tests](../../../internal/helper/protocol_test.go), and the [Search and Optional Helper guide](../../user/search-helper.md).
