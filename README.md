# AMSFTP

English | [简体中文](README.zh-CN.md)

AMSFTP is a Vim-style, two-pane terminal file manager for local directories and
SFTP servers. Open any combination of local folders and `~/.ssh/config` hosts
side by side to browse, preview, edit, search, and transfer files without
leaving the terminal.

> [!WARNING]
> **Public preview (pre-1.0):** macOS builds are currently unsigned and
> unnotarized. Transfers run over standard SFTP; optional remote helpers and
> direct server-to-server transfers are not enabled in public builds.

## Why AMSFTP

- **Uses your existing OpenSSH setup:** Reads `~/.ssh/config` (including
  `ProxyJump`, `ProxyCommand`, SSH agent, host-key checks, and Kerberos/GSSAPI).
  AMSFTP never stores passwords, private keys, or tickets.
- **Transfers that survive disconnects:** Copy, move, rename, and delete run as
  resumable background jobs in a per-user local daemon. Closing the TUI does not
  interrupt active transfers.
- **Safe writes and moves:** Files are written to a temporary name first and
  renamed into place only after completion. Moves never delete the source until
  the destination is verified. Optional SHA-256 verification and `fsync` are
  available when you need stricter guarantees.
- **Remote-to-remote copies (via local relay):** Copy between two SSH servers
  through your local machine in small memory chunks—no full local copy on disk
  and no SSH keys copied to either server.
- **Built for daily terminal work:** On-demand text and image previews (reads
  small chunks, not whole files), safe remote editing with conflict detection,
  filename and content search with built-in time and size limits, and read-only
  diagnostics (`amsftp doctor`).

## Requirements

- macOS 15+ (Apple Silicon / Intel) or Ubuntu 22.04 / 24.04 (other Linux
  distributions are best effort)
- System `/usr/bin/ssh` and an SFTP subsystem on each remote server
- A named host entry in `~/.ssh/config` (not a wildcard like `Host *`)

## Install

**Homebrew:**

```sh
brew install TyrantLucifer/tap/amsftp
```

**Standalone script (installs into `$HOME/.local`, no `sudo`):**

```sh
curl --proto '=https' --tlsv1.2 -fsSL \
  https://github.com/TyrantLucifer/awesome-sftp-cli/releases/latest/download/install.sh | sh
```

The installer verifies the release SHA-256 checksum before replacing the binary.
See the [installation guide](docs/user/installation.md) for manual downloads,
upgrades, shell completions, building from source, and uninstalling.

## Quick start

1. Make sure you have a normal host alias in `~/.ssh/config`:

   ```sshconfig
   Host work
       HostName dev.example.com
       User alice
       IdentityFile ~/.ssh/id_ed25519
   ```

2. Complete the first host-key check and login with OpenSSH, then run `doctor`:

   ```sh
   /usr/bin/ssh work
   amsftp doctor --endpoint work
   ```

3. Open a local directory next to a remote directory:

   ```sh
   amsftp /absolute/local/path work:/absolute/remote/path
   ```

Paths must be absolute (`/local/path` or `<ssh-alias>:/remote/path`). Run
`amsftp` with no arguments to open the startup picker, or
`amsftp --workspace <name>` to reopen a saved layout.

## Essential keys

| Key | Action |
| --- | --- |
| `h` / `j` / `k` / `l` or arrows | Parent / down / up / open or preview |
| `gg` / `G` | Jump to first / last loaded item |
| `Tab` / `c` | Switch pane / switch host (`local` or SSH alias) |
| `/` / `f` / `g/` | Jump in directory / search filenames / search file contents |
| `Space`, `v`, `V` | Mark or visually select items |
| `y` / `d` / `p` | Copy / cut / paste (paste confirms before starting) |
| `r` / `D` | Rename / delete (with confirmation) |
| `K` / `J` / `L` | Toggle Preview / Jobs / Log drawer |
| `e` / `o` | Edit in terminal editor / open in default app |
| `S` | Save current two-pane workspace |
| `q` | Quit TUI (background jobs keep running) |

The bottom action bar always shows valid keys for the current selection. Run
`amsftp config print-effective-keymap` to see your active keybindings.

## Documentation

- [Documentation home](docs/README.md)
- [Install, upgrade, and uninstall](docs/user/installation.md)
- [User guide: browsing, transfers, editing, and search](docs/user/guide.md)
- [Configuration, keys, and CLI reference](docs/user/reference.md)
- [Troubleshooting and recovery](docs/help/troubleshooting.md)
- [Technical design and architecture](docs/architecture/design.md)
- [`amsftp(1)` manual page](docs/man/amsftp.1)

Licensed under the [Apache License 2.0](LICENSE).
