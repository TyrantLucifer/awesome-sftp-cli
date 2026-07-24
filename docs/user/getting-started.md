# Getting Started

[简体中文](../zh-CN/user/getting-started.md)

This guide takes you from an existing SSH account to a first verified file
transfer. AMSFTP deliberately leaves authentication and host-key decisions to the
system OpenSSH client.

## 1. Prepare an SSH alias

Add a concrete host entry to `~/.ssh/config`:

```sshconfig
Host work
    HostName dev.example.com
    User alice
    IdentityFile ~/.ssh/id_ed25519
```

Aliases can use the OpenSSH features you already rely on, including an agent,
ProxyJump, ProxyCommand, password or MFA prompts, and Kerberos/GSSAPI.

Connect with the system client before opening AMSFTP:

```sh
/usr/bin/ssh work
```

This is where you should review a new or changed host key and complete any
interactive authentication. AMSFTP will not silently accept a host key or copy
credentials into its own configuration.

## 2. Check the installation and endpoint

```sh
amsftp --version
amsftp config validate
amsftp daemon start
amsftp doctor --endpoint work
```

`doctor` is read-only. It checks the local installation, runtime paths, daemon,
OpenSSH policy, and the selected endpoint without changing remote files. A proxy
configuration may be reported as not directly probed; that is not the same as an
authentication failure.

If the SSH alias uses Kerberos, check or renew the ticket with your normal system
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

Other ways to start are:

```sh
amsftp
amsftp /absolute/local/path
amsftp work:/absolute/remote/path /absolute/local/path
amsftp --workspace <name>
```

With no locations, the startup picker shows saved workspaces, `local`, and
concrete aliases discovered from OpenSSH configuration. Wildcard `Host` patterns
are not offered as selectable servers.

## 4. Make a local test copy

In the left pane:

1. Move to `hello.txt` with `j` or `k`.
2. Press `y` to copy its reference.
3. Press `Tab` to focus the destination pane.
4. Press `p`, review the destination, and confirm.
5. Press `J` to watch the Job until it is complete.

`y` does not read or upload the file. The transfer starts only after `p` creates
the Job. The destination becomes final only after its content has been written,
verified, and committed.

Press `q` to leave the TUI. A background Job keeps running; reopen AMSFTP or use
`amsftp job list` to check it.

## 5. Try the remote endpoint

Open the local test directory beside a directory you can write remotely:

```sh
amsftp /tmp/amsftp-source work:/absolute/remote/path
```

Repeat the copy steps. If authentication needs interaction, AMSFTP uses the
OpenSSH flow rather than asking you to save a password. If the server disconnects,
the affected pane reconnects independently and the other pane remains usable.

For a remote-to-remote copy, open two SSH locations. Public builds relay the data
through bounded memory in your local daemon; they do not copy credentials between
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
parent and tells you where it landed.

## What to read next

- [Everyday use](everyday-use.md) explains navigation, selection, workspaces, and
  drawers.
- [Transfers](transfers.md) covers moves, conflicts, Job controls, and recovery.
- [Preview, edit, and search](preview-edit-search.md) covers file inspection and
  local editing.
- [Troubleshooting](../help/troubleshooting.md) starts from common symptoms.
