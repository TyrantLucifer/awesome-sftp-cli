# AMSFTP

English | [简体中文](README.zh-CN.md)

AMSFTP is a Vim-first, two-pane file manager for local files and SFTP servers. Each
pane can point to a local directory or a host from `~/.ssh/config`, so browsing,
previewing, editing, searching, and transferring all happen in one terminal workspace.

> [!WARNING]
> AMSFTP is a public preview, not a 1.0 release. The macOS builds are not signed or
> notarized. Standard SFTP is the supported remote path; optional remote
> acceleration and direct server-to-server transfer are not available in public
> builds.

## What it does

- Browses local and remote directories side by side.
- Uses your system OpenSSH configuration, keys, agent, host-key policy,
  ProxyJump/ProxyCommand, and Kerberos/GSSAPI setup.
- Runs copy, move, rename, and delete operations as durable background Jobs.
- Verifies transferred content before publishing the destination.
- Previews text and images and supports editing or opening remote files locally.
- Searches by filename or content with clear limits on large searches.
- Provides `doctor`, structured diagnostics, and consent-based support bundles.

AMSFTP does not store passwords, private keys, agent contents, Kerberos tickets, or
authentication answers.

## Requirements

- macOS 15 or later, or Ubuntu 22.04/24.04; other Linux is best effort.
- The system `/usr/bin/ssh` and an SFTP subsystem on each remote server.
- A concrete host alias in `~/.ssh/config`.

## Install

With Homebrew:

```sh
brew install TyrantLucifer/tap/amsftp
```

Or install the latest standalone build into `$HOME/.local`:

```sh
curl --proto '=https' --tlsv1.2 -fsSL \
  https://github.com/TyrantLucifer/awesome-sftp-cli/releases/latest/download/install.sh | sh
```

The installer verifies the release checksum before replacing the binary. If your
home-directory layout does not pass its ownership and permission checks, it prints
the exact commands needed to prepare a private managed installation root.

See the [installation guide](docs/user/installation.md) for manual installation,
upgrades, shell completion, building from source, and uninstalling.

## First connection

Add a normal OpenSSH alias:

```sshconfig
Host work
    HostName dev.example.com
    User alice
    IdentityFile ~/.ssh/id_ed25519
```

Let OpenSSH handle the first host-key and authentication exchange, then check the
endpoint:

```sh
/usr/bin/ssh work
amsftp doctor --endpoint work
```

Open a local directory beside a remote directory:

```sh
amsftp /absolute/local/path work:/absolute/remote/path
```

Locations are either absolute local paths or
`<SSH-alias>:<absolute-remote-path>`. Run `amsftp` without locations to use the
startup picker, or `amsftp --workspace <name>` to reopen a saved workspace.

## Essential keys

| Key | Action |
| --- | --- |
| `h` / `j` / `k` / `l` or arrow keys | Parent / down / up / open |
| `Tab` | Switch pane |
| `c` | Change the active pane's endpoint |
| `/` | Find an entry in the current directory |
| `Space`, `v`, `V` | Select files |
| `y`, `d`, `p` | Copy, cut, paste |
| `r`, `D` | Rename, delete with confirmation |
| `K`, `J`, `L` | Preview, Jobs, Log |
| `e`, `o` | Edit or open a file locally |
| `f`, `g/` | Search filenames or file contents |
| `S` | Save the current workspace |
| `q` | Leave the TUI; background Jobs keep running |

The action bar shows the keys that apply to the current selection. Print the full
effective map with `amsftp config print-effective-keymap`.

Copy and cut only capture a selection. A paste shows its destination and required
choices before creating Jobs. New content stays private until it is verified and
committed, and a move keeps its source until the destination is complete.
Remote-to-remote copies use the local daemon as a bounded relay without copying
SSH credentials to either server.

## Documentation

- [Complete the first connection and transfer](docs/user/getting-started.md)
- [Browse all user guides](docs/README.md)
- [Troubleshoot a problem](docs/help/troubleshooting.md)
- [Understand the architecture](docs/architecture/overview.md)
- [`amsftp(1)` manual](docs/man/amsftp.1)

AMSFTP is licensed under the [Apache License 2.0](LICENSE).
