# Troubleshooting and Recovery

[简体中文](../zh-CN/help/troubleshooting.md)

AMSFTP uses conservative recovery: when it cannot prove whether a write or
delete finished, it preserves evidence and waits for your decision instead of
guessing.

## Start with read-only checks

Most issues can be diagnosed without modifying files or restarting processes:

```sh
amsftp --version
amsftp daemon status --format json
amsftp doctor --format json
amsftp doctor --endpoint work --format json   # if one SSH host is affected
```

`doctor` is strictly read-only—it never alters SSH trust, repairs databases, or
uploads data.

**Before changing anything after a crash or failed transfer:**

1. Do not start duplicate copy, move, delete, or edit jobs on the same files.
2. Record the error code, request ID, and Job ID.
3. **Never** manually delete the SQLite database, WAL files, control socket,
   `.part` temporary files, or workspace backups—they are the evidence AMSFTP
   uses to recover safely.

## Connection, SSH, and authentication

**Remote host won't open:** AMSFTP uses your system OpenSSH aliases. Test
outside AMSFTP first:

```sh
/usr/bin/ssh work
amsftp doctor --endpoint work
```

Check that the alias exists in `~/.ssh/config`, the remote path is absolute
(`work:/srv/project`), the server supports the SFTP subsystem, and your jump
host / VPN / agent / Kerberos ticket is working.

**Host-key warning:** Verify the changed fingerprint through another trusted
channel (such as your server admin or cloud console). Never delete `known_hosts`
or disable host-key checks just to silence a warning. Once `/usr/bin/ssh work`
connects under your normal policy, retry AMSFTP.

**Repeated auth prompts or `waiting_auth`:** Complete login or renew your SSH
agent / Kerberos ticket in `/usr/bin/ssh work`. If a background job needs an
interactive answer while no TUI client is attached, it pauses instead of storing
or guessing credentials—open the TUI to authenticate, then resume the job.

## Startup, permissions, and configuration

**AMSFTP refuses to start:**

```sh
amsftp config validate
amsftp config print-effective
```

- **Unknown config field:** Fix or remove the named key; strict JSON validation
  rejects typos intentionally.
- **Unsafe path, owner, permissions, ACL, or symlink:** Never `chmod 777` your
  home or data directory to work around the check. Re-run the official installer;
  if it asks for a custom private data directory, have an administrator create
  the exact directory it prints, then install again.
- **`permission_denied` on files:** Check local/remote file permissions. Do not
  run AMSFTP as `root`—that uses a different state directory and SSH identity.
- **`protocol_incompatible`:** Client, daemon, or stored state come from
  incompatible versions. Finish your upgrade or reinstall a single matching
  release.

## Interrupted jobs, conflicts, and edits

Inspect stuck or interrupted jobs:

```sh
amsftp job list --limit 50
amsftp job events <job-id> --after 0 --limit 100
```

| State / error | What to do |
| --- | --- |
| `paused` | Resume with `U` in the TUI or `amsftp job resume <job-id>`. |
| `waiting_auth` | Renew OpenSSH auth, then resume. |
| `waiting_conflict` | Refresh both panes, inspect both versions, and choose overwrite/skip/auto-rename (`w`/`x`/`a`). |
| Transport interrupted / timeout | Restore network/server, check events, then resume the existing job (do not start a second copy). |
| Resource exhausted | Free disk space, cache quota, or wait for active jobs, then resume. |
| Server features changed (`capability_lost`) | Reconnect so AMSFTP re-checks supported SFTP extensions and falls back to basic SFTP if needed. |

To cancel explicitly: `amsftp job cancel <job-id> --confirm <job-id>`.

**Uncertain move or partial file:** Never manually finish a move or delete the
source based only on timestamps. Resume the recorded job so AMSFTP can verify
that the source file has not changed, check partial file digests, and confirm
whether the destination was already renamed into place. If a move finished
copying but could not prove source deletion, both copies are kept
intentionally—inspect both before removing anything.

**Interrupted remote edit:** Press `E` in the TUI to open edit recovery, inspect
the local edited copy with `K`, and decide whether to upload, save as a new
name, or discard.

**Preview or search stops early:** This is intentional when a time, byte, depth,
or result cap is reached. Narrow the directory or query; a partial search result
does not mean no further matches exist.

## Daemon, workspace, cache, and upgrade recovery

**Daemon unavailable:** Check `amsftp daemon status`. If it is simply stopped,
run `amsftp daemon start` and `amsftp doctor`. If status reports an unsafe
socket or wrong owner, **stop**—do not delete the socket or kill unknown
processes. After a crash, the restarted daemon automatically inspects incomplete
jobs before continuing.

**Damaged workspace:**

1. Confirm other locations open normally without that workspace.
2. Stop the daemon (`amsftp daemon stop --confirm stop`).
3. Back up the workspace file (and any `<name>.schema-v1.backup` file) without
   editing them in place. Default locations:
   - macOS: `~/Library/Application Support/io.github.tyrantlucifer.amsftp/state/workspaces/`
   - Linux: `${XDG_STATE_HOME:-$HOME/.local/state}/amsftp/workspaces/`
4. Restart the daemon and save a new workspace from known-good locations.

**Cache issues:** Close any editor, opener, or preview using cached files first,
preserve any edited local copy, run `amsftp doctor`, and use `gc`/`gC` inside
AMSFTP instead of recursively deleting the cache folder.

**Failed or refused upgrade:**

- **Refused before replacing binary:** Usually an active job is running, another
  upgrade holds the lock, or permissions/checksum failed. Finish or pause jobs
  and retry `amsftp upgrade`.
- **Binary replaced, daemon failed to restart:** Check `amsftp --version`,
  `amsftp daemon status`, and `amsftp doctor`. Re-run Homebrew upgrade or the
  verified installer. Do not launch an older binary if a state migration may
  have run.
- **Database migration failed:** Stop all writes, preserve the entire private
  state directory, and keep any read-only `doctor` output. Do not copy live
  SQLite files or remove WAL files.

## Bug reports and support bundles

Include `amsftp --version`, the failing command/action, any error code and
request ID, and whether `/usr/bin/ssh <alias>` works.

To collect redacted local diagnostics in two steps:

```sh
amsftp support-bundle preview --format json
amsftp support-bundle create \
  --consent <sha256-from-preview> \
  --output /absolute/private/path/amsftp-support.tar.gz \
  --format json
```

The output file must not already exist and must live inside a directory
accessible only by your user (`0700`). If state changes after preview, the
SHA-256 confirmation hash expires and you must preview again. AMSFTP never
uploads bundles automatically. Review the archive before sharing—redaction omits
credentials and raw paths, but system metadata remains.
