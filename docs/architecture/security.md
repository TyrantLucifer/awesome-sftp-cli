# Security

[简体中文](../zh-CN/architecture/security.md)

AMSFTP moves data between filesystems, starts OpenSSH, and keeps resumable local
state. Its security model is therefore built around clear ownership: OpenSSH
owns SSH trust and authentication, the local daemon owns durable operations,
and the user owns every destructive or externally executed action.

This page describes the guarantees the design aims to provide, the assumptions
those guarantees rely on, and the limits users should understand. The component
relationships are explained in [Architecture](overview.md).

## Trust boundaries

| Boundary | What AMSFTP trusts | What it continues to validate |
| --- | --- | --- |
| Local operating-system account | an authenticated user deliberately running the client | environment values, configuration, terminal input, filesystem ownership and permissions |
| Client and daemon | a peer only after same-user and protocol checks | frame size, request identity, version, rate, deadline, and message structure |
| System OpenSSH | the validated `/usr/bin/ssh` and its established policy | executable path, structured arguments, process lifetime, and bounded output |
| Remote SSH/SFTP server | the session identity only after OpenSSH accepts its host and authentication policy | every name, metadata value, content byte, capability claim, error, and disconnect |
| Editor, opener, and shell | only the program or command the user explicitly configured or entered | argument structure and the data brought back into AMSFTP |
| Installed release | bytes obtained from a documented channel and verified as that channel specifies | installation path ownership, permissions, symlinks, and runtime compatibility |

Authentication proves which remote account answered; it does not make the
remote filesystem's output safe to display, store, or interpret without
validation.

## SSH configuration and credentials

AMSFTP does not implement its own SSH stack. The SFTP provider launches the
validated system OpenSSH client with fixed arguments and communicates with its
SFTP subsystem over standard input and output. A host alias is passed as data,
not through a shell.

OpenSSH remains responsible for:

- host-key verification and `known_hosts`;
- `~/.ssh/config`, `Include`, and `Match`;
- private keys, security keys, and SSH Agent;
- Kerberos/GSSAPI;
- interactive authentication;
- `ProxyJump` and `ProxyCommand`.

AMSFTP does not copy private keys, Agent contents, Kerberos tickets, passwords,
or authentication answers into its configuration, database, workspace files,
Jobs, logs, cache metadata, or support bundles. An interactive answer is
available only to the active, validated authentication exchange. If no trusted
client can answer, the operation waits.

AMSFTP does not enable Agent forwarding, credential delegation, or relaxed
host-key checking to improve compatibility or performance. If your SSH alias
enables those features, that remains an explicit part of your OpenSSH policy.

## Local daemon and private state

The daemon is a per-user process. It uses an owner-private Unix socket rather
than a network listener and validates the peer user before accepting requests.
The protocol handshake rejects incompatible clients before dispatching an
operation.

Configuration, state, cache, logs, sockets, and upgrade locks live under
private roots. Before using sensitive paths, AMSFTP checks the expected owner,
mode, ACL where supported, and symbolic-link conditions. An unsafe root fails
closed instead of being silently repaired with broader permissions.

These checks protect one user's AMSFTP state from accidental sharing and common
path-substitution attacks. They do not sandbox the daemon from the same
operating-system account. A process already able to act fully as that user—or
as root—can generally read that user's files and influence their sessions.

## Remote data is untrusted input

Remote filenames, metadata, file contents, server extensions, error text, and
timing are treated as untrusted even on an authenticated connection.

AMSFTP:

- keeps raw path bytes separate from display text;
- escapes terminal control content before rendering;
- uses structured process arguments instead of building shell command lines
  from filenames;
- validates paths relative to the intended provider boundary;
- places limits on frames, names, listings, previews, searches, and process
  output;
- accepts optional behavior only after it is proven by the current session's
  capabilities.

A malicious remote server can still return false file contents or metadata and
can deny service. SSH protects the transport and server identity according to
OpenSSH policy; it cannot make a compromised server truthful.

## Safe changes and recovery

The client cannot directly mutate a provider. It sends an operation intent to
the daemon, which freezes the source, destination, route, conflict policy, and
required confirmations into a persistent Job.

For streamed copies, the destination is written under a Job-specific temporary
name. AMSFTP records progress at durable boundaries, verifies the result, and
only then publishes the final name. An incomplete transfer is not exposed as
the intended final file.

Move adds one more rule: the source is deleted only after the destination has
been verified and committed. If source deletion is uncertain, retaining an
extra copy is safer than risking loss.

Overwrite, recursive deletion, conflict resolution, and other destructive
choices require an explicit action. Retries cross only steps known to be
idempotent, or first inspect whether the earlier effect already occurred.
Network interruption or daemon restart does not justify blindly repeating a
rename, commit, or delete.

These guarantees reduce accidental loss; they are not a backup. Server-side
changes, disk failure, compromised accounts, and a user explicitly confirming
the wrong action can still destroy data.

## Cache, editors, openers, and shells

Remote preview and edit content is materialized in a private cache with quotas
and leases. A leased item cannot be evicted while AMSFTP knows an editor,
opener, or preview is using it. Before uploading an edit, AMSFTP compares the
starting remote identity with the current remote object and reports concurrent
changes as a conflict.

An editor or opener runs with the user's operating-system permissions and can
read the file it is given. An interactive local or remote shell is an explicit
escape from the structured file-operation interface. Commands typed there
have the authority of the corresponding local or SSH account; AMSFTP cannot
make an unsafe command safe.

Only configure programs you trust. Avoid commands that pass filenames through
another shell unless you have reviewed their quoting and behavior.

## Diagnostics and support bundles

Human and JSON errors expose stable, bounded fields rather than raw underlying
causes. Persistent logs use an allowlist of fields and omit raw paths, command
arguments, environment values, file contents, and authentication material.

`doctor` is a read-only diagnostic command. It does not repair a database,
change SSH trust, install remote software, or upload results.

A support bundle is generated in two steps:

1. preview the exact file list, sensitivity labels, and consent digest;
2. create a new owner-private archive using that matching digest.

AMSFTP has no automatic upload command. Redaction reduces exposure but does
not make a bundle public information: it can contain versions, capability
results, pseudonymous identifiers, and other system metadata. Review it before
sharing.

## Resource and denial-of-service limits

Directory entries, event history, logs, search results, preview bytes, cache
usage, transfer buffers, connections, queues, and concurrent workers all have
bounds.

When a limit is reached, AMSFTP applies backpressure, waits, returns a partial
result, or reports a resource error. This protects the local session from
unbounded growth. It cannot prevent a remote server from being slow,
disconnecting repeatedly, or consuming the bandwidth of work the user
explicitly requested.

## Optional remote enhancements

Standard SFTP is the supported baseline. Current public builds do not enable
the remote Helper or direct remote-to-remote transfer. Remote-to-remote copies
use the bounded local relay unless a separately supported safe route is
available.

Do not install test artifacts, inject trust keys, delegate credentials, or
weaken host-key policy to force a disabled path. Failure of an optional
enhancement must remain isolated from the standard SFTP route.

## User responsibilities

AMSFTP relies on the user to:

- protect the local account, terminal session, SSH configuration, Agent, and
  Kerberos ticket cache;
- verify unexpected host-key changes out of band;
- install releases only from documented channels and perform the stated
  checksum or package-manager verification;
- review source, destination, conflict, overwrite, move, and delete
  confirmations;
- keep independent backups of important data;
- configure only trusted editors, openers, and shell commands;
- keep private state and support bundles private;
- apply security updates to AMSFTP, OpenSSH, the operating system, and remote
  servers.

Checksums prove that downloaded bytes match the published checksum. They do not
by themselves provide code signing, notarization, or assurance that the
publisher's account was uncompromised. Follow the current
[installation guidance](../user/installation.md) for the guarantees available
on each channel.

## Limits of the model

AMSFTP does not claim to protect against:

- a compromised local user account, root user, kernel, terminal, OpenSSH
  executable, or configured external program;
- a compromised remote account or server returning malicious or false data;
- data loss outside the scope of a recorded AMSFTP Job;
- a user intentionally overriding OpenSSH policy or confirming a destructive
  action;
- unlimited availability in the presence of disk exhaustion, network failure,
  or a hostile server;
- native Windows operation, a GUI sandbox, a mounted remote filesystem, or a
  general-purpose secrets vault.

On macOS, consult the installation page and release notes for the current
signing and notarization status before changing Gatekeeper settings. Never
disable operating-system security controls for an artifact whose origin and
checksum you have not independently verified.
