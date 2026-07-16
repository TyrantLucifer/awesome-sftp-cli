# Read-only Explorer Guide

This guide covers the Stage 1 `amsftp` browser. The current interface is deliberately read-only: it can list directories and preview bounded file content, but it has no copy, move, upload, delete, rename, or shell operation.

## Prerequisites

- macOS 15 or Ubuntu 22.04/24.04.
- The system `/usr/bin/ssh` and an OpenSSH Host alias for each remote endpoint.
- Any authentication already supported by that OpenSSH configuration: key files, an agent, password/MFA prompts, ProxyCommand/ProxyJump, or Kerberos/GSSAPI tickets.

AMSFTP does not import private keys, run `kinit`, copy tickets, or maintain a second SSH configuration. Test the alias with the system `ssh` command first when a connection fails.

## Start locations

```text
amsftp
amsftp /local/path
amsftp /left/path /right/path
amsftp host-alias:/remote/path /local/path
amsftp host-a:/path/a host-b:/path/b
amsftp --workspace workspace-name
```

A local Location is a canonical absolute path. A remote Location is an OpenSSH Host alias, a colon, and a canonical absolute remote path. Relative paths and aliases beginning with `-` are rejected.

With no arguments, the startup picker combines saved workspaces and selectable Host aliases discovered from the user's OpenSSH configuration. Concrete `Host` values from supported `Include` files are selectable; wildcard templates are not offered as concrete endpoints. A valid alias can still be typed manually.

## Keys

| Key | Action |
|---|---|
| `Tab` | Switch the active pane without changing the other pane. |
| `j`, `k` | Move down or up. A numeric prefix such as `12j` is supported; counts do not repeat unsafe actions. |
| `h`, `l` | Open the parent, or open the selected directory/file preview. |
| `g` | Enter a canonical absolute path for the active pane. |
| `c` | Switch only the active pane to `local` or an SSH Host alias. The old remote session is released after the replacement listing succeeds. |
| `/` | Filter the entries already received for the active directory. |
| `s`, `H`, `R` | Cycle sort, toggle hidden entries, or refresh. |
| `v`, `V`, `Space` | Maintain visual or discrete selection state; Stage 1 never turns it into a write operation. |
| `S` | Save the two-pane workspace under a validated name. |
| `Esc` | Cancel the innermost prompt or in-flight preview. |
| `q`, `Ctrl-C` | Exit the client and restore the terminal. |

The status line always includes `READ-ONLY`. A preview reads at most 64 KiB, marks truncation, sanitizes terminal control characters, and can be canceled without allowing an older result to replace a newer one.

## Workspaces

A workspace stores two endpoint references, paths, active pane, sort/filter/hidden preferences, and the ephemeral cache policy. Remote endpoints store only their OpenSSH Host aliases. Passwords, private keys, agent contents, Kerberos tickets, askpass answers, and expanded SSH configuration are never workspace fields.

Saving uses an owner-private temporary file, file sync, atomic replacement, and directory sync. If an existing workspace document is invalid, AMSFTP preserves its bytes instead of overwriting it. Loading a missing or damaged workspace returns to the startup picker with the error visible so another workspace or Host can be selected.

## Connection and daemon recovery

Each pane has an independent endpoint session. A failed or restarted SSH server changes only its pane to `disconnected`/reconnecting; the other pane remains usable. Reconnection uses bounded backoff, obtains a new capability snapshot, and commits the new endpoint only after the first listing page succeeds. If the saved directory disappeared or became inaccessible, the pane walks to the nearest accessible parent and reports that fallback.

If the local daemon stops, the client starts a replacement, obtains the new local endpoint identity, reconnects both panes, and discards results belonging to the old connection epochs. A failed endpoint switch keeps the previously committed endpoint and listing.

## Private files and diagnostics

The daemon uses an owner-only Unix socket (`0600`) below the platform runtime root. Workspaces and logs are below the platform state/log roots and are validated against symlink, owner, mode, and ACL expansion before use.

Persistent daemon logs are JSON, owner-only (`0600`), and bounded to a 4 MiB current file plus three backups. The persistent handler accepts only registered correlation fields such as component, event, endpoint ID, job ID, request ID, and stable error code. It replaces free-form messages and drops paths, raw errors, commands, environment values, and authentication material before encoding. RPC errors shown in the TUI include a copyable `request_id` and `error_code` summary.

## Troubleshooting

- **Host-key failure:** inspect the alias with system OpenSSH and the configured `known_hosts`; AMSFTP never disables checking or silently accepts a changed key.
- **Authentication failure:** verify the agent, key permissions, password/MFA policy, or Kerberos ticket outside AMSFTP. Authentication/configuration errors are not blindly retried.
- **Connection refused or interrupted:** restart the server and press `R`; the pane performs bounded reconnect and path validation.
- **Workspace cannot load:** leave the invalid file in place for diagnosis and choose another picker entry. Saving under the same name is intentionally refused until the invalid file is moved or repaired.
- **Terminal is too small:** resize to at least 20 columns by 5 rows. Resize events trigger a full layout sync.

Remote directory enumeration is packet-bounded through ADR-0011's immutable `github.com/pkg/sftp` cursor fork: AMSFTP can emit the first daemon page without waiting for all later `READDIR` responses. The maintained fork is an explicit compatibility boundary; upgrades must preserve its malicious-packet, cancellation, handle-release, dual-toolchain and real client/server tests.
