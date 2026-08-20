# Install, Upgrade, and Remove AMSFTP

[简体中文](https://github.com/TyrantLucifer/awesome-sftp-cli/blob/main/docs/zh-CN/user/installation.md)

AMSFTP supports macOS on Intel and Apple silicon, plus Linux on AMD64 and ARM64.
Use Homebrew for a package-managed installation. Use the standalone installer
for a user-owned prefix without a package manager.

> [!WARNING]
> The public macOS builds are neither signed nor notarized. Download them only
> from the project release page. Do not disable system-wide security controls to
> run an unverified copy.

## Install with Homebrew

```sh
brew install TyrantLucifer/tap/amsftp
```

Then verify the binary and start the user daemon:

```sh
amsftp --version
amsftp daemon start
amsftp daemon status
```

Homebrew installs the manual page and keeps the binary in its managed prefix.

## Install the standalone build

The official installer uses `$HOME/.local` by default and does not call `sudo`:

```sh
curl --proto '=https' --tlsv1.2 -fsSL \
  https://github.com/TyrantLucifer/awesome-sftp-cli/releases/latest/download/install.sh | sh
```

The installer downloads the matching archive and verifies its SHA-256 checksum.
It then checks the ownership and permissions of the installation and data paths
before replacing the binary atomically. It also installs the man page and
generates shell completion.

Make sure `$HOME/.local/bin` is on `PATH`:

```sh
export PATH="$HOME/.local/bin:$PATH"
```

If necessary, add that line to the startup file for your shell.

### If the installer asks for a managed root

AMSFTP refuses to place private state under a path with unsafe ownership,
permissions, ACLs, or symlinks. If the normal home-directory layout fails these
checks, the installer looks for a private root at
`/var/lib/amsftp-users/<your-uid>`.

If that directory does not exist, the installer stops without changing the
target and prints the exact one-time administrator commands. Ask an administrator
to run those commands, then rerun the installer. Do not bypass the check by making
your home directory or AMSFTP data world-writable.

You can also select a pre-created private root explicitly:

```sh
sh install.sh --root /absolute/private/path
```

Run `install.sh --help` to list the current `--prefix`, `--root`, `--version`,
and daemon-start options.

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

Source builds require Go 1.26.6:

```sh
git clone https://github.com/TyrantLucifer/awesome-sftp-cli.git
cd awesome-sftp-cli
GOTOOLCHAIN=local go build -trimpath -o ./amsftp ./cmd/amsftp
./amsftp --version
```

Development builds are for local testing and are not part of the automatic
release-upgrade channel.

## Upgrade

Homebrew and official standalone installations both support:

```sh
amsftp upgrade
```

AMSFTP checks for an update before stopping anything. If a Job is transferring
data, AMSFTP refuses the upgrade instead of silently interrupting the Job. Finish
or pause important work, then try again.

The command restarts the daemon only if it was running before the upgrade. It
also checks that the new binary and daemon versions agree. If it reports a
partial upgrade, do not delete the socket or state database. Run:

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

Standalone users can rerun the verified installer. When a real earlier
installation exists, the installer keeps its binary as `amsftp.previous`.

## Roll back after a failed upgrade

If the new binary failed without changing persistent state:

1. Stop the proven user daemon with
   `amsftp daemon stop --confirm stop`.
2. Restore the previous verified binary, man page, and matching completion files.
   A standalone installation normally keeps the earlier binary as
   `<prefix>/bin/amsftp.previous`.
3. Run `amsftp --version`, start the daemon, and check it with
   `amsftp daemon status` and `amsftp doctor`.
4. Open a representative workspace read-only before resuming transfers.

If a database migration started, the state format is newer, or you cannot prove
what changed, do not run an older binary against that state. Stop changing files
through AMSFTP. Preserve the complete private state directory and any backup, then
use the current binary's read-only `doctor` output to identify a safe recovery
path. Never force a rollback by deleting the control socket or copying or
replacing a live SQLite database.

## Uninstall

First, stop only your own daemon:

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

Removing this retained data is a separate destructive decision. The default
locations are:

- macOS: `~/Library/Application Support/io.github.tyrantlucifer.amsftp`,
  `~/Library/Caches/io.github.tyrantlucifer.amsftp`, and
  `~/Library/Logs/io.github.tyrantlucifer.amsftp`.
- Linux: the `amsftp` children below your effective XDG config, state, cache, and
  runtime directories. Logs are below the AMSFTP state directory.
- Managed installation: the `config`, `state`, and `cache` directories below the
  exact managed root.

Back up anything you may need before deleting these paths. Never remove a broad
home, XDG, `/tmp`, or `/var/lib/amsftp-users` directory.

Next: [make your first connection](https://github.com/TyrantLucifer/awesome-sftp-cli/blob/main/docs/user/getting-started.md).
