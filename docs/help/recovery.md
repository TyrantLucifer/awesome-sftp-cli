# Recovery

[简体中文](../zh-CN/help/recovery.md)

AMSFTP recovery is deliberately conservative. When it cannot prove whether a
write completed, it preserves the evidence and asks for a decision instead of
repeating a destructive step.

Use this guide after a daemon crash, interrupted transfer, damaged workspace,
or failed upgrade. For ordinary connection and configuration problems, start
with [Troubleshooting](troubleshooting.md).

## Before changing anything

1. Stop starting new copies, moves, deletes, or edits that touch the same
   files.
2. Record the installed version, daemon status, and read-only diagnosis:

   ```sh
   amsftp --version
   amsftp daemon status --format json
   amsftp doctor --format json
   ```

3. Keep the reported Job ID, stable error code, and request ID.
4. Do not delete or rename the database, WAL files, control socket, cache,
   transfer parts, workspace backups, or final destination.

Those files may be the only proof AMSFTP can use to decide whether continuing
is safe.

## Recover the daemon

Check its state first:

```sh
amsftp daemon status
```

If the daemon is simply stopped, start it:

```sh
amsftp daemon start
```

Then run:

```sh
amsftp doctor
```

If the status says the socket is present but unsafe or belongs to another
user, stop. Do not remove it or change its permissions. AMSFTP uses a private
Unix socket and verifies the peer user; bypassing that check could send file
operations to the wrong process.

After a crash, a newly started daemon reads durable Job state and checks what
exists on both sides of every incomplete operation. It does not assume that a
request failed merely because the old connection disappeared.

If the daemon repeatedly exits, preserve the current state and preview a
support bundle. Do not try to repair the SQLite database with unrelated tools.

## Recover a Job

List the durable Jobs:

```sh
amsftp job list --limit 50
```

Inspect the affected Job:

```sh
amsftp job events <job-id> --after 0 --limit 100
```

Resolve the condition named by the most recent event:

- restore network access or the remote server;
- complete authentication through system OpenSSH;
- free local or remote disk space;
- choose a conflict result;
- wait for another operation that owns the same resource.

Then resume the existing Job:

```sh
amsftp job resume <job-id>
```

Use pause when you want to keep the Job and its resumable progress:

```sh
amsftp job pause <job-id>
```

Cancellation is explicit and requires the exact ID:

```sh
amsftp job cancel <job-id> --confirm <job-id>
```

Cancellation does not promise that every temporary byte disappears. AMSFTP may
retain a matching part that is useful for safe recovery, and it will not
recursively delete unknown files.

### If the result is uncertain

Do not manually finish the move, overwrite the destination, or remove the
source. Resume the recorded Job and let AMSFTP check:

- the source identity recorded when the Job was planned;
- the durable offset and temporary destination;
- whether the final destination was already committed;
- whether a move source is still present.

A move never deletes its source before the destination has been verified and
committed. If the destination completed but source deletion could not be
proved, AMSFTP keeps the source and reports that outcome rather than guessing.

If both source and destination are present and the Job cannot proceed, preserve
both. Include the stable status in a bug report; do not choose one copy based
only on timestamps.

## Recover a workspace

A saved workspace contains pane locations and view preferences, not
credentials. If one workspace cannot be opened:

1. Confirm that other locations can still be opened without that workspace.
2. Stop the daemon before copying workspace state:

   ```sh
   amsftp daemon stop --confirm stop
   ```

3. Preserve the original workspace document in a private backup location. Do
   not edit it in place.
4. If a file named `<name>.schema-v1.backup` exists, preserve it too. It is the
   original created during a successful format migration and must not be
   overwritten.
5. Start the daemon and create a new workspace name from known-good locations.

The usual workspace roots are:

- macOS:
  `~/Library/Application Support/io.github.tyrantlucifer.amsftp/state/workspaces/`
- Linux:
  `${XDG_STATE_HOME:-$HOME/.local/state}/amsftp/workspaces/`

An installation that uses a managed root can store state elsewhere. Use the
path reported by AMSFTP rather than assuming the default.

Workspace files are owner-private and use a strict schema. If the original and
the migration backup disagree, or the error mentions a backup conflict,
preserve both and stop. Do not replace either file with a hand-edited version
under the same name.

## Recover an upgrade

First determine how far the upgrade progressed:

```sh
amsftp --version
amsftp daemon status --format json
amsftp doctor --format json
```

### The upgrade was refused before replacement

AMSFTP makes no package change when:

- active Jobs would be interrupted;
- another upgrade holds the upgrade lock;
- installation or state paths fail their ownership and permission checks;
- the release or installer cannot be verified.

Resolve only the reported condition and retry. Do not loosen directory
permissions, replace the control socket, or disable system security policy.

### The package changed but the daemon did not restart

Run the new executable's `--version`, then check daemon status and `doctor`.
Re-run the verified installer or package-manager upgrade if the executable is
missing or inconsistent.

Do not immediately start a saved older executable. Persistent state may already
use a newer format. A binary downgrade is safe only when the release
documentation explicitly says that the older version can use that state, or
when a documented restore procedure also restores the matching state.

### Persistent-state migration failed

Stop mutations and preserve the entire owner-private state directory. Do not
copy a live SQLite main file without its associated recovery procedure, remove
WAL files, edit migration records, or create a fresh database over the old one.

AMSFTP may leave state read-only after an interrupted or failed migration. Use
the current executable and the stable diagnosis to identify the recorded
backup or recovery hold. If there is no documented recovery command for that
exact release, preserve the state and report the issue rather than improvising
a restore.

See [Installation and maintenance](../user/installation.md) for verified
installation channels and normal upgrade or uninstall instructions.

## Recover preview, edit, or cache state

If a preview or opener is still using an item, or an editor contains unsaved
work, close it normally before clearing cache entries. Active items are
protected by leases, and a failed edit can leave the only changed local copy in
the cache.

When `doctor` reports a cache integrity or permission problem:

1. stop new previews and edits;
2. preserve any edited local copy;
3. close AMSFTP and the associated editor or opener;
4. run `doctor` again;
5. use AMSFTP's own cache action only after the active work is safe.

Do not recursively delete the cache directory to repair a single entry.

## Create a reviewed support bundle

Preview the exact contents first:

```sh
amsftp support-bundle preview --format json
```

The preview returns a consent digest. After reviewing the file list and
sensitivity labels, create a new archive in an existing owner-private
directory:

```sh
amsftp support-bundle create \
  --consent <sha256-from-preview> \
  --output /absolute/private/path/amsftp-support.tar.gz \
  --format json
```

If the diagnostic state changes after preview, the digest no longer matches;
preview again instead of reusing old consent. AMSFTP never uploads the bundle.
Review the archive before sharing it, even though sensitive fields are
redacted and publication uses a private, no-replace file.
