# Transfers and Jobs

[简体中文](../zh-CN/user/transfers.md)

AMSFTP runs every copy, move, rename, and delete as a durable background Job. The
file panes collect your choices and confirmations; the daemon records and performs
the work. Closing the TUI does not cancel these Jobs.

## Copy files

1. Select one or more items with `Space`, `v`, or `V`. With no selection, the
   cursor item is used.
2. Press `y`. This captures the file references but does not start reading data.
3. Focus the destination pane with `Tab`.
4. Press `p`.
5. Review the source, destination, item count, and any conflict choice, then
   confirm.
6. Open `J` to follow the new Jobs.

AMSFTP first writes each destination to private temporary content. It verifies and
commits that content before the final name becomes visible. A failed transfer is
never reported as a complete final file.

Directory copies are walked incrementally. Symlinks are shown as symlinks; AMSFTP
does not follow them while recursively copying a directory.

## Move files

Use the same flow with `d` instead of `y`:

1. Select the source.
2. Press `d`.
3. Move to the destination pane and press `p`.
4. Review the move confirmation and start the Jobs.

Lowercase `d` only marks the selection as a cut. It does not remove anything by
itself.

When a safe same-endpoint rename is available, AMSFTP can use it. Otherwise, a move
copies, verifies, and commits the destination before rechecking and deleting the
original. If the original changed or its deletion cannot be proved, the Job keeps
the verified destination and finishes with the source retained. Review both copies
before taking further action.

## Rename one item

Place the cursor on one file or directory, press `r`, enter a plain new entry name,
and confirm. Rename does not accept a multi-selection.

The operation still appears as a Job so its result and any conflict remain visible.

## Delete files

Uppercase `D` is the only delete entry point. It is separate from lowercase `d`.

1. Select the intended files.
2. Press `D` and review the frozen selection.
3. Confirm the scope.
4. If the endpoint has no reliable trash support, review the irreversible warning
   and confirm again.

The current local and standard SFTP paths do not advertise a reliable trash
facility, so deletion normally requires both confirmations. AMSFTP rejects root,
empty, changed, or otherwise unsafe targets. Recursive deletion does not follow a
symlink to its target.

## Resolve destination conflicts

The available choices are:

- **Overwrite**: replace the destination only after its recorded state is checked.
- **Skip**: leave this source and destination unchanged.
- **Auto-rename**: create a non-conflicting destination name.
- **Ask**: pause the Job until you choose overwrite, skip, or auto-rename.

In the Jobs drawer, use:

| Key | Action |
| --- | --- |
| `w` / `x` / `a` | Overwrite / skip / auto-rename this conflict |
| `W` / `X` / `A` | Apply that choice to matching conflicts in this Job |

An apply-all choice is limited to the selected Job. It does not become a global
default.

## Follow and control Jobs

Press `J` to open the Jobs drawer. Use `j` and `k` to select a Job.

| Key | When available |
| --- | --- |
| `P` | Pause queued or active work |
| `U` | Resume paused work or retry a recoverable wait |
| `C` | Cancel non-terminal work |

The header shows only valid actions for the current state. Completed, failed, and
canceled Jobs remain selectable but do not pretend that an action is available.

The same controls are available from the command line:

```sh
amsftp job list
amsftp job events job_aaaaaaaaaaaaaaaaaaaaaaaaaa --after 0 --limit 50
amsftp job pause job_aaaaaaaaaaaaaaaaaaaaaaaaaa
amsftp job resume job_aaaaaaaaaaaaaaaaaaaaaaaaaa
amsftp job cancel job_aaaaaaaaaaaaaaaaaaaaaaaaaa \
  --confirm job_aaaaaaaaaaaaaaaaaaaaaaaaaa
```

Cancellation requires the exact Job ID, which prevents a pasted or mistyped
command from canceling unrelated work. Add `--format json` when a script needs
stable structured output.

## Understand common states

- **Queued / running / verifying**: work is waiting, transferring, or checking the
  result.
- **Waiting for authentication**: renew or complete authentication through
  OpenSSH, then resume.
- **Waiting for conflict**: choose overwrite, skip, or auto-rename.
- **Paused**: durable progress is retained; use `U` when ready.
- **Retry wait**: a safely retryable transport failure is waiting before another
  attempt.
- **Completed with source retained**: the destination is verified, but the move's
  source was intentionally kept because safe deletion was not proved.
- **Failed / canceled**: inspect the Job and refresh both locations before deciding
  whether to start new work.

Transfer speed uses a short moving average and is shown only while data is moving.
For a directory Job, transferred bytes include the current file before that file
finishes verification, while the completed item count advances only after the
file is committed. Byte totals may be unknown for part of a directory operation.

Without a bandwidth limit, standard SFTP uses a bounded 4 MiB durable transfer
chunk and allows up to 64 concurrent 32 KiB write requests inside that chunk.
This keeps a full protocol window active on higher-latency links instead of
waiting after every small group of packets. Bandwidth-controlled Jobs retain a
256 KiB scheduling quantum. At the end of each durable chunk, AMSFTP synchronizes
the temporary destination, checks its size, and records a checkpoint before it
advances recoverable progress.

## Recovery after interruption

The daemon records safe progress as data is written. After a restart, it checks the
source, partial destination, and final destination again before continuing. If the
state is uncertain, the Job pauses or fails visibly instead of guessing.

Pause normally keeps matching partial data for resume. Cancellation or failure may
also leave Job-owned partial data or an edit safety copy when removing it would
lose recovery evidence. Do not manually delete `.part`, backup, SQLite, or WAL
files while diagnosing a Job.

If an interruption happens after a moved destination is committed but before the
source is deleted, the source stays in place. Resume only after reviewing both
sides.

See [Recovery](../help/recovery.md) when the daemon or stored Job state is unhealthy.

## Remote-to-remote transfers

A copy between two SSH endpoints uses two ordinary OpenSSH/SFTP sessions and a
bounded memory relay in the local daemon. It does not:

- copy a key, ticket, or agent to either server;
- enable agent forwarding or GSS credential delegation;
- relax host-key policy;
- store the complete transfer in the preview/edit cache.

Production server-side acceleration and direct server-to-server transfer are not
available in public builds. Standard SFTP remains the supported path.
