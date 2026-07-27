# Getting Started

[简体中文](../zh-CN/user/getting-started.md)

Start with an existing SSH account and finish with a verified file transfer.
AMSFTP leaves authentication and host-key decisions to the system OpenSSH client.

## 1. Prepare an SSH alias

Add a concrete host entry to `~/.ssh/config`:

```sshconfig
Host work
    HostName dev.example.com
    User alice
    IdentityFile ~/.ssh/id_ed25519
```

The alias can use the OpenSSH features already configured on the system,
including an agent, ProxyJump, ProxyCommand, password or MFA prompts, and
Kerberos/GSSAPI.

Connect with the system client before opening AMSFTP:

```sh
/usr/bin/ssh work
```

Review any new or changed host key and complete interactive authentication in
this system client. AMSFTP does not silently accept host keys or copy credentials
into its own configuration.

## 2. Check the installation and endpoint

```sh
amsftp --version
amsftp config validate
amsftp daemon start
amsftp doctor --endpoint work
```

`doctor` performs read-only checks of the local installation, runtime paths,
daemon, OpenSSH policy, and selected endpoint. It does not change remote files. A
proxy configuration may be reported as not directly probed; this does not mean
authentication failed.

If the SSH alias uses Kerberos, check or renew the ticket with the usual system
tools before continuing.

## 3. Open two locations

Start with a local directory in each pane:

```sh
mkdir -p /tmp/amsftp-source /tmp/amsftp-destination
printf 'hello from AMSFTP\n' > /tmp/amsftp-source/hello.txt
amsftp /tmp/amsftp-source /tmp/amsftp-destination
```

Locations follow two forms:

```text
/absolute/local/path
SSH-alias:/absolute/remote/path
```

Relative paths are rejected. The remote part before `:` is an OpenSSH host alias,
not a hostname that AMSFTP stores separately.

Other startup forms are:

```sh
amsftp
amsftp /absolute/local/path
amsftp work:/absolute/remote/path /absolute/local/path
amsftp --workspace <name>
```

With no locations, the startup picker lists saved workspaces, `local`, and
concrete aliases found in the OpenSSH configuration. Wildcard `Host` patterns are
not listed as selectable servers.

## 4. Make a local test copy

In the left pane:

1. Move to `hello.txt` with `j` or `k`.
2. Press `y` to capture the selection for copying.
3. Press `Tab` to focus the destination pane.
4. Press `p`, review the destination, and confirm.
5. Press `J` to watch the Job until it is complete.

`y` does not read or upload the file. The transfer starts only when `p` creates
the Job. The destination becomes final only after its content is written,
verified, and committed.

Press `q` to leave the TUI. Background Jobs keep running. Reopen AMSFTP or use
`amsftp job list` to check them.

## 5. Try the remote endpoint

Open the local test directory beside a directory you can write remotely:

```sh
amsftp /tmp/amsftp-source work:/absolute/remote/path
```

Repeat the copy steps. If authentication requires interaction, AMSFTP uses the
OpenSSH flow instead of asking you to save a password. If the server disconnects,
the affected pane reconnects independently while the other pane remains usable.

For a remote-to-remote copy, open two SSH locations. Public builds relay data
through bounded memory in the local daemon. They do not copy credentials between
servers or store the complete file in the preview cache.

## 6. Save the workspace

Press `S`, enter a name, and confirm. A workspace remembers:

- the two endpoint aliases and paths;
- the active pane;
- sorting, filtering, and hidden-file choices;
- the cache policy for that workspace.

It does not contain passwords, private keys, expanded SSH configuration, agent
contents, or Kerberos tickets.

Reopen it later:

```sh
amsftp --workspace <name>
```

If a saved remote directory no longer exists, AMSFTP tries the nearest accessible
parent and reports the location it opened.

## What to read next

- [Everyday use](everyday-use.md) covers navigation, selection, workspaces, and
  drawers.
- [Transfers](transfers.md) covers moves, conflicts, Job controls, and recovery.
- [Preview, edit, and search](preview-edit-search.md) covers file inspection and
  local editing.
- [Troubleshooting](../help/troubleshooting.md) organizes fixes by symptom.
