# Troubleshooting

[简体中文](../zh-CN/help/troubleshooting.md)

Most AMSFTP problems can be narrowed down without changing files or restarting
anything. Start with these checks:

```sh
amsftp --version
amsftp daemon status --format json
amsftp doctor --format json
```

If the problem concerns one SSH host, add an endpoint check:

```sh
amsftp doctor --endpoint work --format json
```

`doctor` is read-only. It does not repair state, migrate data, change SSH trust,
or upload a report. The endpoint check also avoids interactive authentication,
so a successful manual SSH login can still be necessary.

## AMSFTP refuses to start

First validate the configuration:

```sh
amsftp config validate
amsftp config print-effective
```

If the message mentions an untrusted installation path, runtime directory,
owner, permissions, an ACL, or a symbolic link, do not work around it by making
the directory world-writable. Re-run the verified installer. If it asks for a
managed root, have an administrator create the exact private path printed by
the installer, then run the installer again.

An unknown configuration field is rejected intentionally. Correct or remove
the named field rather than replacing the entire configuration. See
[Configuration](../user/configuration.md) for supported settings and
precedence.

## A remote location will not open

AMSFTP uses the host aliases understood by the system OpenSSH client. Test the
same alias outside AMSFTP:

```sh
/usr/bin/ssh work
```

Then check the endpoint:

```sh
amsftp doctor --endpoint work
```

Common causes are:

- the alias is missing from `~/.ssh/config`, or expands differently than
  expected;
- the remote path is not absolute (`work:/srv/project` is valid);
- the remote account cannot use the SFTP subsystem;
- a jump host, VPN, DNS record, agent, or Kerberos ticket is unavailable;
- the server closed the connection or denied access.

Fix the OpenSSH connection first. AMSFTP deliberately does not maintain a
second SSH configuration or credential store.

## OpenSSH reports a host-key problem

Stop and verify the new fingerprint through a trusted, independent channel.
Do not delete `known_hosts`, disable host-key checking, or accept a changed key
only to make the warning disappear.

Once `/usr/bin/ssh work` accepts the host according to your normal policy,
retry AMSFTP.

## Authentication is missing or keeps prompting

Complete any first-time or renewed authentication in a normal SSH session:

```sh
/usr/bin/ssh work
```

Check that the expected key is available to your agent, or that the relevant
Kerberos ticket is current. If an AMSFTP Job needs an interactive answer while
no trusted client is present, it waits instead of storing or guessing the
answer. Open the TUI again, authenticate, and resume the Job if necessary.

AMSFTP never writes passwords, private keys, agent contents, Kerberos tickets,
or prompt answers to its configuration, database, Jobs, logs, or support
bundles.

## The daemon is unavailable or the TUI keeps reconnecting

Inspect the daemon before trying to replace it:

```sh
amsftp daemon status
amsftp doctor
```

If it is stopped, start it normally:

```sh
amsftp daemon start
```

Do not delete the control socket by hand and do not kill an unknown process
that appears to own it. AMSFTP validates the socket owner and the peer user
before using it. During an upgrade, other clients may wait briefly for the old
daemon to release ownership and for the new one to start.

If the client and daemon report incompatible versions, finish the upgrade or
reinstall one current release so both come from the same installation.

## A Job is not making progress

List Jobs and inspect the recent events for the affected ID:

```sh
amsftp job list --limit 50
amsftp job events <job-id> --after 0 --limit 50
```

The state usually points to the next action:

| State or message | What to do |
| --- | --- |
| `paused` | Resume when you are ready with `amsftp job resume <job-id>`. |
| authentication required | Restore the OpenSSH authentication session, then resume. |
| conflict | Refresh both locations and make the requested keep, replace, rename, or skip choice. |
| transport interrupted or timeout | Restore the network or server, inspect the events, then resume the existing Job. |
| resource exhausted | Free the named disk, quota, or concurrency resource before resuming. |
| capability lost or unsupported | Reconnect so AMSFTP can use the standard SFTP route or another supported path. |

Do not start a second copy of the same transfer merely because the first one
stopped. A Job records its safe progress and checks the source, temporary
destination, and final destination before continuing.

For uncertain or destructive results, follow
[Recovery](recovery.md#recover-a-job) before touching temporary files.

## A copy or edit reports a conflict

A conflict means the source or destination no longer matches what AMSFTP saw
when it planned the operation. This can happen when another process edits,
renames, replaces, or deletes the same file.

Refresh both panes, inspect both versions, and choose explicitly. AMSFTP does
not silently overwrite a concurrently changed file. For a remote edit, keep
the local edited copy until you have decided whether to upload it under a new
name, replace the remote version, or discard it.

## A file is missing or permission is denied

Refresh the parent directory and confirm that you are looking at the intended
local or SSH endpoint. A `not_found` result can also mean that another process
moved the item after it was listed.

For `permission_denied`, check the local owner, mode, and ACL, or the remote
account's access. Do not run AMSFTP as root to bypass the boundary; doing so
would create a different user state and a different SSH identity.

## Preview or search stops early

Directory loading, preview, and search have limits on time, bytes, results,
and concurrency. When a limit is reached, AMSFTP shows a partial result instead
of consuming unbounded memory or reading an entire remote tree.

Narrow the directory or search expression and try again. Large remote content
searches over standard SFTP can be slow because every examined byte crosses
the SSH connection. A partial result is not evidence that no later match
exists.

## The local editor or opener fails

Check the configured command and try it on a local file first. AMSFTP passes
structured arguments to the configured program; it does not interpret a file
name as a shell command.

When an editor returns, AMSFTP compares the cached starting version, your local
changes, and the current remote version. If the remote file changed too, the
upload waits for an explicit conflict decision.

## Upgrade does not complete

An upgrade is refused while a Job is actively changing data. Let those Jobs
finish or pause them. Cancel only if you actually intend to cancel the work,
then retry:

```sh
amsftp upgrade
```

Only one upgrade can run at a time. If package replacement completed but
verification failed, record:

```sh
amsftp --version
amsftp daemon status --format json
amsftp doctor --format json
```

Do not delete the database or run an older binary against newer persistent
state. Continue with [Upgrade recovery](recovery.md#recover-an-upgrade).

## An internal or protocol error appears

`protocol_incompatible` normally means that the client, daemon, or stored state
comes from a version that cannot safely communicate with the others. Use one
current installation and follow the documented upgrade path.

`internal` means AMSFTP hid a lower-level cause that was not safe to expose.
Run `doctor`, record the product version and stable error code, and prepare a
reviewed support bundle if the problem persists.

## Prepare information for a bug report

Include:

- `amsftp --version`;
- the command or TUI action that failed;
- the stable error code and request ID, if shown;
- whether `/usr/bin/ssh <alias>` succeeds;
- the Job ID only if sharing it is appropriate.

You can preview the diagnostic bundle without creating it:

```sh
amsftp support-bundle preview --format json
```

Review the listed files and sensitivity labels before creating or sharing a
bundle. A support bundle is local and private by default, but it can still
contain system metadata. Never attach private keys, tickets, passwords,
unreviewed logs, file contents, or raw command lines to a public report.
