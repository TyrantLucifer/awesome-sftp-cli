# Durable Transfers Guide

AMSFTP routes every copy, move, rename, and delete through the daemon-owned `Intent → frozen Plan → durable Job → bounded Worker → verify → commit` pipeline. The TUI never writes a LocalFS or SFTP path directly. If the SQLite state store cannot be opened safely, mutation RPCs are unavailable while the Explorer baseline remains usable for read-only diagnosis.

## Clipboard and selection

Use `Space` for discrete marks or `v`/`V` for a visual selection. With no selection, an operation uses the cursor entry. A numeric prefix selects a bounded run beginning at the cursor for `y`, `d`, and `D`.

| Key | Durable behavior |
|---|---|
| `y` | Capture immutable copy `FileRef` values for the selected files or directories. No content is read and no Job starts yet. |
| `d` | Capture the same frozen identities with cut semantics. It never deletes or renames a source by itself. |
| `p` | Freeze destination directory, names, endpoint capabilities, route, conflict policy, verification, and resource budgets, then create one durably admitted Job per clipboard item. If `y`/`d` is still freezing a multi-selection, the paste is queued against that capture generation instead of reusing an older clipboard. Concurrent Job persistence is serialized across the shared WAL boundary so one selected item cannot displace another. A cut paste shows a move confirmation first. |
| `r` | Capture exactly one file or directory, request a new plain entry name, and create a same-endpoint durable move. Multi-selection is rejected. |
| `D` | Capture the selected identities for explicit deletion. The first confirmation freezes the scope; a second confirmation authorizes irreversible deletion when reliable trash is unavailable. |
| `.` | Repeat only the last frozen high-level operation. Copy repeats directly; move/rename reopens confirmation; delete restarts both delete confirmations. A stale source identity is rejected by the Planner. |
| `J` | Open or close the bounded Jobs view. |

A counted paste such as `2p` repeats the already frozen clipboard batch, with a hard limit of 1,024 Job intents. Every resulting destination conflict is still handled by its Job; a count cannot convert `ask` into overwrite. Counts are cleared after one command and cannot leak into an unrelated action.

## Commit and move safety

Files stream into a unique same-directory `.<final>.part-<job-id>`. The worker persists source identity, part identity, verified offset, SHA-256 state, and phase. It rereads and verifies the part before publishing the final name, then verifies the committed final. A failed or canceled transfer never reports the part as the final file.

Directory discovery is streaming and bounded. The default Plan freezes 64 queued entries, 256 entries per Provider page, depth 128, and a 256 KiB transfer buffer. Symlinks are visible but directory copy does not follow or copy them.

The daemon applies one shared bandwidth/resource scheduler. A Plan may freeze a per-Job byte rate; global, both Endpoint, and Job token buckets are enforced at a fixed maximum 256 KiB quantum with bounded interactive service. Fast paths that cannot be rate-controlled are skipped when the policy requires exact control. Runtime configuration may tighten, but never expand, the documented hard ceilings (16 active Jobs, 8 per Endpoint, 512 queued Jobs, 32 connections, 4 per Endpoint, 32 SSH processes, 4 Helper processes, 512 FDs, 256 goroutines, and 64 MiB accounted memory).

A move uses an atomic rename only when the same endpoint explicitly advertises that exact capability and its postconditions can be proved. Otherwise it performs copy → verify → commit → revalidate the frozen source → delete source → prove absence. Source capability loss, source replacement, incomplete directory verification, or an unproved delete leaves the verified destination intact and completes as `completed_with_source_retained` with the reason in Jobs.

Remote A→remote B uses two independent OpenSSH/SFTP sessions and a daemon memory buffer. It does not stage the complete file in a local content cache.

Route evidence records why each fast/direct candidate was selected or rejected. Production Level 2 distribution remains closed, so ordinary builds show `production_distribution_closed` and use bounded relay; only non-release fixtures exercise the direct data plane. No route enables Agent forwarding, GSS credential delegation, secret copying, or relaxed host-key checking.

## Conflicts and controls

The baseline conflict policies are `ask`, `overwrite`, `skip`, and `auto-rename`. `ask` enters `waiting_conflict`. In `J`, use:

| Key | Jobs action |
|---|---|
| `j`, `k` | Select a Job. |
| `P`, `U`, `C` | Pause, resume, or cancel. Cancel preserves a matching resumable part/checkpoint by default. |
| `w`, `x`, `a` | Resolve the selected conflict as overwrite, skip, or auto-rename. |
| `W`, `X`, `A` | Apply the same decision only to matching conflicts in this Job. It is never a global default. |

The default Jobs list prioritizes user-facing operation, state, and progress. Byte counts automatically use `KiB`, `MiB`, `GiB`, and larger units, with a percentage when the total is known. During transfer, adjacent bounded refresh samples provide the displayed rate; the first sample shows `—/s`, while waiting, paused, and terminal Jobs omit an instantaneous rate. Selecting a Job expands its source and destination onto separate lines. Narrow terminals preserve both ends of each path with an explicit middle ellipsis so long paths do not displace state and progress.

Route, integrity, downgrade-boundary, and progress-semantics evidence remains available in durable Job/JSON diagnostic data but is not included in the default TUI summary. Values such as `bounded_relay_default`, `strong`, `before_target_write`, and `durable_bytes` are internal route evidence, not errors or states that require user action. The UI renders `waiting_auth` as `Waiting for authentication`; it remains distinct from network retry. Repair or renew the system OpenSSH credential source, reattach the TUI if an interactive prompt is needed, then resume the Job.

## Delete and trash behavior

`D` is the only explicit delete entry point; lowercase `d` remains cut. Root, empty, non-canonical, changed, or unsupported identities are rejected. Recursive deletion is page/depth bounded, checks every discovered descendant, and removes a symlink itself without following its target.

The Plan uses trash only when the frozen endpoint snapshot advertises `trash` and the Provider supplies the matching mutation facet. Current LocalFS and standard SFTP providers do not advertise reliable trash, so their delete plans are marked irreversible and require both confirmations. If a delete or trash response is lost, the daemon stats the frozen identity before deciding whether the operation completed; it does not blindly replay an unknown non-idempotent effect.

## Recovery and cleanup

Closing the TUI does not cancel a non-interactive Job. On daemon restart, every nonterminal Job is recovered conservatively, external source/part/final identities are revalidated, and unsafe work pauses instead of guessing. A kill between destination commit and source deletion retains the source; after restart and explicit resume, the daemon revalidates both sides before attempting the source step.

Successful commits normally remove their Job part. Pause and cancel preserve a matching part for safe resume. AMSFTP does not recursively delete unknown `.part`, backup, WAL, or database files, and it never stores passwords, private keys, askpass answers, agent contents, or Kerberos tickets in Jobs or backups.

## Minimal local walkthrough

```text
amsftp /absolute/source /absolute/destination
```

1. Select a file or directory in the left pane and press `y`.
2. Press `Tab`, then `p`; open `J` to observe the Job through `running`, `verifying`, and `completed`.
3. For a move, use `d`, `Tab`, `p`, review the frozen count and paths, then press `Enter`.
4. For rename, press `r`, type one new entry name, and press `Enter`.
5. For delete, press `D`, review the frozen selection, press `Enter`, review the irreversible warning, and press `Enter` again.

The same flow accepts `host-alias:/absolute/path` on either pane. Host aliases, authentication, ProxyCommand/ProxyJump, agents, host-key policy, and Kerberos/GSSAPI continue to come from system OpenSSH.

While the TUI is running, newly created nonterminal Jobs are polled with a bounded request even when the Jobs drawer is closed. When a Job first reaches `completed`, visible panes that show an affected parent directory are relisted once: copy refreshes the destination, delete refreshes the source parent, and move/rename refreshes both sides. `completed_with_source_retained` refreshes only the destination because the source was proved to remain in place. Failed or canceled Jobs do not infer a visible mutation; use `R` when an explicit authoritative relist is still needed.
