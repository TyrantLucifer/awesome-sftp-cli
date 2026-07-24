# Install and Maintain AMSFTP

[简体中文](https://github.com/TyrantLucifer/awesome-sftp-cli/blob/main/docs/zh-CN/user/installation.md)

AMSFTP supports macOS on Intel and Apple silicon, and Linux on AMD64 and ARM64.
Homebrew is the shortest installation path. The standalone installer is useful
when you prefer a user-owned prefix without a package manager.

> [!WARNING]
> The public macOS builds are not signed or notarized. Verify that downloads come
> from the project release page and do not disable system-wide security controls
> to run an unverified copy.

## Install with Homebrew

```sh
brew install TyrantLucifer/tap/amsftp
```

Verify the binary and start the user daemon:

```sh
amsftp --version
amsftp daemon start
amsftp daemon status
```

Homebrew installs the manual page and keeps the binary in its managed prefix.

## Install the standalone build

The official installer defaults to `$HOME/.local` and does not use `sudo`:

```sh
curl --proto '=https' --tlsv1.2 -fsSL \
  https://github.com/TyrantLucifer/awesome-sftp-cli/releases/latest/download/install.sh | sh
```

It downloads the matching archive, verifies its SHA-256 checksum, checks the
ownership and permissions of the installation and data paths, then replaces the
binary atomically. It also installs the man page and generates shell completion.

Make sure `$HOME/.local/bin` is on `PATH`:

```sh
export PATH="$HOME/.local/bin:$PATH"
```

Add that line to the startup file for your shell if necessary.

### If the installer asks for a managed root

AMSFTP refuses to place private state below a path with unsafe ownership,
permissions, ACLs, or symlinks. When the normal home-directory layout cannot be
trusted, the installer looks for a private root at
`/var/lib/amsftp-users/<your-uid>`.

If that directory does not exist, the installer stops before changing the target
and prints the exact one-time administrator commands. Ask an administrator to run
those commands, then run the installer again. Do not work around the check by
making your home directory or AMSFTP data world-writable.

You can also select a pre-created private root explicitly:

```sh
sh install.sh --root /absolute/private/path
```

Run `install.sh --help` for the current `--prefix`, `--root`, `--version`, and
daemon-start options.

## Install an archive manually

1. Open the project's [GitHub Releases](https://github.com/TyrantLucifer/awesome-sftp-cli/releases)
   page.
2. Download the archive for your operating system and CPU, plus
   `checksums.txt`, from the same release.
3. Compute the archive digest before extracting it:

   ```sh
   sha256sum <downloaded-archive>
   ```

   On macOS, use `shasum -a 256 <downloaded-archive>`. Require the result to
   exactly match the line for that filename in `checksums.txt`.
4. Extract into a new directory and install `amsftp` in a user-owned `PATH`
   directory, such as `$HOME/.local/bin`.
5. Install `share/man/man1/amsftp.1` in the matching user man directory.
6. Generate completion from the installed binary:

   ```sh
   amsftp completion bash
   amsftp completion zsh
   amsftp completion fish
   ```
7. Verify the installation:

   ```sh
   amsftp --version
   amsftp config validate
   amsftp doctor
   ```

Do not run AMSFTP as root, set it setuid, or install it in a directory that other
users can modify.

## Build from source

Source builds require Go 1.26.5:

```sh
git clone https://github.com/TyrantLucifer/awesome-sftp-cli.git
cd awesome-sftp-cli
GOTOOLCHAIN=local go build -trimpath -o ./amsftp ./cmd/amsftp
./amsftp --version
```

A development build is suitable for local testing but is not part of the
automatic release-upgrade channel.

## Upgrade

For both Homebrew and official standalone installations:

```sh
amsftp upgrade
```

AMSFTP checks whether an update exists before stopping anything. If a Job is
actively transferring data, the upgrade is refused so that the Job is not
silently interrupted. Finish or pause important work, then try again.

The command restarts the daemon only if it was running before the upgrade and
checks that the new binary and daemon versions agree. If it reports a partial
upgrade, do not delete the socket or state database. Instead run:

```sh
amsftp --version
amsftp daemon status
amsftp doctor
```

For recovery, Homebrew users can run:

```sh
brew update
brew upgrade TyrantLucifer/tap/amsftp
```

Standalone users can rerun the verified installer. It keeps the previous binary
as `amsftp.previous` when a real earlier installation exists.

## Roll back after a failed upgrade

If the new binary failed before it changed persistent state:

1. Stop the proven user daemon with
   `amsftp daemon stop --confirm stop`.
2. Restore the previous verified binary, man page, and matching completion files.
   A standalone installation normally keeps the earlier binary as
   `<prefix>/bin/amsftp.previous`.
3. Run `amsftp --version`, start the daemon, and check it with
   `amsftp daemon status` and `amsftp doctor`.
4. Open a representative workspace read-only before resuming transfers.

If a database migration started, the state format is newer, or you cannot prove
what changed, do not run an older binary against that state. Stop mutating files
through AMSFTP, preserve the complete private state directory and any backup, and
use the current binary's read-only `doctor` output to identify the safe recovery
path. Never delete the control socket or copy/replace a live SQLite database to
force a rollback.

## Uninstall

First stop only your own daemon:

```sh
amsftp daemon stop --confirm stop
amsftp daemon status
```

For Homebrew:

```sh
brew uninstall amsftp
```

For a standalone installation, remove only the installed `amsftp` binary, its
`amsftp.1` page, and the completion files you generated. User configuration,
workspaces, Job history, recovery records, logs, and cache are intentionally kept.

Removing that data is a separate destructive decision. Default locations are:

- macOS: `~/Library/Application Support/io.github.tyrantlucifer.amsftp`,
  `~/Library/Caches/io.github.tyrantlucifer.amsftp`, and
  `~/Library/Logs/io.github.tyrantlucifer.amsftp`.
- Linux: the `amsftp` children below your effective XDG config, state, cache, and
  runtime directories. Logs are below the AMSFTP state directory.
- Managed installation: the `config`, `state`, and `cache` directories below the
  exact managed root.

Back up anything you may need before deleting those paths. Never remove a broad
home, XDG, `/tmp`, or `/var/lib/amsftp-users` directory.

Next: [make your first connection](https://github.com/TyrantLucifer/awesome-sftp-cli/blob/main/docs/user/getting-started.md).
