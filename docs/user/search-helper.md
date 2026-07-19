# Search and Optional Helper

Stage 4 adds recursive search while keeping standard SFTP as the permanent baseline.

## Search keys

- `/` filters the current directory only.
- `f` opens recursive filename search rooted at the active pane. Results stream into the search drawer and can be opened, previewed, or copied through the existing operation intents.
- `g/` opens recursive content search. Without a Helper, AMSFTP first shows that this is a bounded, slower SFTP range-read scan; press Enter again to confirm. Esc cancels the modal or active search.

Search freezes its endpoint, session, pane generation, scope, options, and budgets. Navigating or reconnecting cannot splice late results into a new search. Limits on depth, entries, files, bytes, duration, results, snippets, and queued events are safety behavior, not tuning hints. A limited run ends as `partial_results` with its reason; cancellation retains results already shown and marks the run `canceled`.

Filename search does not follow directory symlinks. Content search skips binary data under the default policy and reports permission, encoding, long-line, oversized-file, changed-file, and budget boundaries explicitly. A result is a snapshot hint: opening or mutating it still goes through a fresh Provider/Planner check.

## Capability levels

- Level 0: standard SFTP search and all Stage 1–3 features. Always available when the SFTP endpoint is healthy.
- Level 1: an optional verified Helper can accelerate filename/content search and independently offer strong hash, disk stats, tail, watch, and same-host copy.
- Level 2 belongs to Stage 5 and is not claimed by Stage 4.

The status line refreshes the active SSH pane's capability snapshot at most once per second and shows `helper:L0 <reason>` or `helper:L1 <version> [capabilities]`. Stale Endpoint/session/generation refreshes are discarded. The Jobs drawer shows the frozen route (`local`, `sftp_relay`, or `helper_same_host`). Helper presence never implies every capability. A crash or malformed frame marks only the current enhanced action partial; a new search uses Level 0, and the SFTP Provider and unrelated Jobs remain available.

Tail/watch events are loss-possible, coalesced hints. Refresh/stat/list is still authoritative. Disk stats do not treat unknown quota as unlimited. Strong-hash output becomes invalid if the file changes while it is read.

## Install, disable, and remove boundary

The lifecycle requires two distinct approvals. Preliminary approval occurs before any remote probe and names the endpoint, declared target, shared-session-stable-home assumption, and possible SSH audit side effects. Final approval occurs only after a fresh numeric-UID/physical-home/OS/architecture/namespace/ancestor plan and displays the exact path, directories, mode, size, digest, key, floor, and high-water decision. Canceling preliminary approval performs zero probes; canceling final approval creates no application install-tree directory or content.

After approval, installation uses a content-addressed path, exact owner-only modes, an unpredictable exclusive temporary file, client readback verification, and a target-exists-fails publication. Directory creation requires a separately reviewed SFTP MKDIR primitive that carries `0700` at creation time; create-then-chmod is rejected, and production installation remains unavailable until that raw primitive and release custody exist. Every enable repeats current signature/revocation/floor policy, pre/post-probe absolute-utility checks, and fresh remote identity/attributes/hash checks. Disable/remove retain the monotonic high-water so an older or same-version-different artifact cannot be replayed. Exact removal also takes the Job Store admission lease and scans frozen Endpoint plus artifact identity; a queued, paused, running, or restartable Job prevents its exact Helper artifact from being removed.

Production Helper distribution is **CLOSED**. The shipped production verifier trusts no fixture key, and ordinary runtime/configuration cannot enable the repository's non-release test fixture. Consequently there is no supported production install command or production Helper download yet. `amsftp helper status <SSH-host>` is a read-only Stage 6 management surface that reports the negotiated Level 0/Level 1 state and the closed production gate; it does not mutate remote installation state. Lifecycle tests use explicit non-release test-only injection; they are not a production artifact or trust claim.

## Same-host copy

For one regular file copied within the same SSH Endpoint, Planner may freeze `helper_same_host` only when strong-hash and same-host-copy capabilities are independently available. The durable binding records that exact Endpoint and Helper protocol/version/target/artifact SHA; its size, mtime at Provider precision, available file ID, and any existing SHA-256 must agree with the frozen Provider FileRef. Job admission is held from before Helper preparation through durable creation, so removal cannot race a frozen-but-not-yet-persisted plan. The Helper stages exactly the normal Job `.part-<JobID>` path and never commits the final name. The durable Worker performs the existing checksum verification, conflict policy, rename/commit, checkpoint, and restart recovery. If eligibility is absent while planning, the route is SFTP relay. If a frozen Helper route fails during execution, that Job fails/retries visibly rather than mixing two transports into one claimed-complete snapshot.
